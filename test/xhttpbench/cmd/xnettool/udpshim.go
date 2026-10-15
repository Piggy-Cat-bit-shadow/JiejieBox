package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The UDP counting relay.
//
// QUIC traffic cannot be counted with the Windows per-process IO counters (they
// count file-handle I/O, not Winsock sends), and a packet capture needs
// privileges this host does not have. A relay in front of the reference server
// measures what the question actually asks — datagrams and wire bytes per
// megabyte, per direction — with both clients behind the identical path.
//
// It is a NAT, not a single-socket forwarder: the client may open a second QUIC
// connection from a NEW source port (the HTTP transport redials underneath its
// pool), and a forwarder that pinned one upstream socket to the first address it
// saw would deliver that connection's replies to the old port. Measured the hard
// way: the first version of this relay did exactly that, and the streams stalled
// with "no recent network activity" while the counters looked healthy.
//
// Datagrams are forwarded verbatim — no re-framing, no coalescing — and the
// counters are per direction. CPU measured through the relay is NOT comparable
// with a direct run: one extra hop copies every datagram through userspace. The
// ratios this reports (datagrams per MB, wire bytes per MB) are unaffected.
type udpShimStats struct {
	upPackets   atomic.Int64
	upBytes     atomic.Int64
	downPackets atomic.Int64
	downBytes   atomic.Int64
	errors      atomic.Int64
}

type udpRelay struct {
	forward *net.UDPAddr
	stats   *udpShimStats

	access  sync.Mutex
	clients map[string]*udpClient
}

type udpClient struct {
	address  *net.UDPAddr
	upstream *net.UDPConn
}

func newUDPRelay(forward *net.UDPAddr, stats *udpShimStats) *udpRelay {
	return &udpRelay{forward: forward, stats: stats, clients: make(map[string]*udpClient)}
}

// upstreamFor returns the per-client upstream socket, dialing a new one the first
// time a client address is seen, and starting its return path.
func (relay *udpRelay) upstreamFor(clientAddress *net.UDPAddr, listener *net.UDPConn) (*net.UDPConn, error) {
	key := clientAddress.String()
	relay.access.Lock()
	if existing, ok := relay.clients[key]; ok {
		relay.access.Unlock()
		return existing.upstream, nil
	}
	relay.access.Unlock()

	upstream, err := net.DialUDP("udp", nil, relay.forward)
	if err != nil {
		return nil, err
	}
	entry := &udpClient{address: clientAddress, upstream: upstream}
	relay.access.Lock()
	if existing, ok := relay.clients[key]; ok {
		relay.access.Unlock()
		_ = upstream.Close()
		return existing.upstream, nil
	}
	relay.clients[key] = entry
	relay.access.Unlock()
	fmt.Fprintf(os.Stderr, "udpshim: client %s -> %s\n", key, upstream.LocalAddr())

	go func() {
		buffer := make([]byte, 65535)
		for {
			read, readErr := upstream.Read(buffer)
			if readErr != nil {
				return
			}
			relay.stats.downPackets.Add(1)
			relay.stats.downBytes.Add(int64(read))
			if _, writeErr := listener.WriteToUDP(buffer[:read], entry.address); writeErr != nil {
				relay.stats.errors.Add(1)
			}
		}
	}()
	return upstream, nil
}

func runUDPShim(args []string) error {
	flags := flag.NewFlagSet("udpshim", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:9600", "listen address (the client points here)")
	forward := flags.String("forward", "127.0.0.1:9601", "address to forward to (the reference server)")
	report := flags.String("report", "", "write a JSON counters snapshot here on SIGINT")
	reportEvery := flags.Int("report-every-ms", 0, "also rewrite the report this often, so a caller can bracket a measured phase (0 = only on SIGINT)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	listenAddress, err := net.ResolveUDPAddr("udp", *listen)
	if err != nil {
		return err
	}
	listener, err := net.ListenUDP("udp", listenAddress)
	if err != nil {
		return err
	}
	forwardAddress, err := net.ResolveUDPAddr("udp", *forward)
	if err != nil {
		return err
	}
	stats := &udpShimStats{}
	relay := newUDPRelay(forwardAddress, stats)
	fmt.Fprintf(os.Stderr, "udpshim: %s -> %s\n", listener.LocalAddr(), forwardAddress)

	go func() {
		buffer := make([]byte, 65535)
		for {
			read, from, readErr := listener.ReadFromUDP(buffer)
			if readErr != nil {
				if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			upstream, upstreamErr := relay.upstreamFor(from, listener)
			if upstreamErr != nil {
				stats.errors.Add(1)
				continue
			}
			stats.upPackets.Add(1)
			stats.upBytes.Add(int64(read))
			if _, writeErr := upstream.Write(buffer[:read]); writeErr != nil {
				stats.errors.Add(1)
			}
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// A snapshot before and after a load phase is how a caller separates the
	// measured traffic from the handshake and warmup that preceded it. A killed
	// process writes nothing on Windows — a kill there is not a signal — so the
	// periodic snapshot is what makes the counters readable after the fact.
	if *report != "" && *reportEvery > 0 {
		go func() {
			ticker := time.NewTicker(time.Duration(*reportEvery) * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				_ = writeUDPShimReport(*report, listener, forwardAddress, relay, stats)
			}
		}()
	}

	<-stop
	if *report != "" {
		return writeUDPShimReport(*report, listener, forwardAddress, relay, stats)
	}
	return nil
}

func writeUDPShimReport(path string, listener *net.UDPConn, forward *net.UDPAddr, relay *udpRelay, stats *udpShimStats) error {
	relay.access.Lock()
	clients := len(relay.clients)
	relay.access.Unlock()
	return writeJSONFile(path, map[string]any{
		"listen":           listener.LocalAddr().String(),
		"forward":          forward.String(),
		"clients":          clients,
		"client_to_server": map[string]any{"datagrams": stats.upPackets.Load(), "bytes": stats.upBytes.Load()},
		"server_to_client": map[string]any{"datagrams": stats.downPackets.Load(), "bytes": stats.downBytes.Load()},
		"relay_errors":     stats.errors.Load(),
		"measured_at":      time.Now().Format(time.RFC3339Nano),
	})
}
