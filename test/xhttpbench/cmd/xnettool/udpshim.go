package main

import (
	"flag"
	"fmt"
	"math/rand"
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
	// Fault counters, so a run can prove the impairment it asked for happened
	// instead of assuming it did.
	dropped    atomic.Int64
	delayed    atomic.Int64
	blackholed atomic.Int64
}

// udpFaults is the impairment applied to the relayed path. Everything here is
// expressed as what it does to datagrams, because that is what a test can then
// assert about the client: a QUIC connection whose path stops carrying datagrams
// for a while is a dead connection from the client's point of view, and that is
// the failure this host can produce without a packet filter.
type udpFaults struct {
	dropProb       float64       // probability a datagram is discarded
	delay          time.Duration // held before forwarding (adds one-way latency)
	dropAfter      time.Duration // after this since the first datagram, drop everything
	blackholeEvery time.Duration // period of a recurring outage
	blackholeFor   time.Duration // length of each outage

	started  time.Time
	access   sync.Mutex
	started_ bool
}

func (f *udpFaults) startedAt() time.Time {
	f.access.Lock()
	defer f.access.Unlock()
	if !f.started_ {
		f.started = time.Now()
		f.started_ = true
	}
	return f.started
}

// block reports whether a datagram should be dropped right now, and counts why.
func (f *udpFaults) block(stats *udpShimStats) bool {
	now := time.Now()
	if f.dropAfter > 0 {
		if now.Sub(f.startedAt()) > f.dropAfter {
			stats.blackholed.Add(1)
			return true
		}
	}
	if f.blackholeEvery > 0 && f.blackholeFor > 0 {
		phase := now.Sub(f.startedAt()) % f.blackholeEvery
		if phase < f.blackholeFor {
			stats.blackholed.Add(1)
			return true
		}
	}
	if f.dropProb > 0 && rand.Float64() < f.dropProb {
		stats.dropped.Add(1)
		return true
	}
	return false
}

// udpFaults holds no per-datagram sleep: delay is a queue of release times (see
// udpRelay.delayed), because sleeping in the forwarding goroutine would make the
// path stop-and-wait and cap throughput at (datagram size)/delay.
type udpRelay struct {
	forward *net.UDPAddr
	stats   *udpShimStats
	faults  *udpFaults

	access  sync.Mutex
	clients map[string]*udpClient

	// delayed is the queue of datagrams waiting out their delay. A per-datagram
	// sleep in the forwarding goroutine would be stop-and-wait — throughput would
	// collapse to (datagram size)/delay, and a "high latency" scenario would really
	// be measuring the shim's serialization. The release times keep the path
	// pipelined: each datagram leaves `delay` after it arrived, so bandwidth is
	// preserved and only the first byte pays the latency.
	delayed chan udpPending
}

type udpPending struct {
	data    []byte
	release time.Time
	client  *udpClient
	upward  bool
}

type udpClient struct {
	address  *net.UDPAddr
	upstream *net.UDPConn
}

func newUDPRelay(forward *net.UDPAddr, stats *udpShimStats, faults *udpFaults) *udpRelay {
	relay := &udpRelay{forward: forward, stats: stats, faults: faults, clients: make(map[string]*udpClient)}
	if faults.delay > 0 {
		relay.delayed = make(chan udpPending, 8192)
	}
	return relay
}

// releaseOutOfBand writes a delayed datagram when its release time arrives.
func (relay *udpRelay) releaseOutOfBand(listener *net.UDPConn) {
	for pending := range relay.delayed {
		if wait := time.Until(pending.release); wait > 0 {
			time.Sleep(wait)
		}
		if pending.upward {
			if _, err := pending.client.upstream.Write(pending.data); err != nil {
				relay.stats.errors.Add(1)
			}
			continue
		}
		if _, err := listener.WriteToUDP(pending.data, pending.client.address); err != nil {
			relay.stats.errors.Add(1)
		}
	}
}

// forward hands a datagram to its destination, either immediately or through the
// delay queue.
func (relay *udpRelay) relayOrDelay(listener *net.UDPConn, pending udpPending) {
	if relay.delayed == nil {
		relay.writeNow(listener, pending)
		return
	}
	pending.release = time.Now().Add(relay.faults.delay)
	pending.data = append([]byte(nil), pending.data...)
	relay.delayed <- pending
}

func (relay *udpRelay) writeNow(listener *net.UDPConn, pending udpPending) {
	if pending.upward {
		if _, err := pending.client.upstream.Write(pending.data); err != nil {
			relay.stats.errors.Add(1)
		}
		return
	}
	if _, err := listener.WriteToUDP(pending.data, pending.client.address); err != nil {
		relay.stats.errors.Add(1)
	}
}

// upstreamFor returns the per-client upstream socket, dialing a new one the first
// time a client address is seen, and starting its return path.
func (relay *udpRelay) upstreamFor(clientAddress *net.UDPAddr, listener *net.UDPConn) (*udpClient, error) {
	key := clientAddress.String()
	relay.access.Lock()
	if existing, ok := relay.clients[key]; ok {
		relay.access.Unlock()
		return existing, nil
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
		return existing, nil
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
			if relay.faults.block(relay.stats) {
				continue
			}
			relay.stats.downPackets.Add(1)
			relay.stats.downBytes.Add(int64(read))
			relay.relayOrDelay(listener, udpPending{data: buffer[:read], client: entry})
		}
	}()
	return entry, nil
}

func runUDPShim(args []string) error {
	flags := flag.NewFlagSet("udpshim", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:9600", "listen address (the client points here)")
	forward := flags.String("forward", "127.0.0.1:9601", "address to forward to (the reference server)")
	report := flags.String("report", "", "write a JSON counters snapshot here on SIGINT")
	reportEvery := flags.Int("report-every-ms", 0, "also rewrite the report this often, so a caller can bracket a measured phase (0 = only on SIGINT)")
	dropProb := flags.Float64("drop-prob", 0, "probability that a datagram is discarded")
	delayMs := flags.Int("delay-ms", 0, "hold each datagram this long before forwarding it (adds one-way latency)")
	dropAfterMs := flags.Int("drop-after-ms", 0, "after this long, stop forwarding anything (a permanent path outage)")
	blackholeEveryMs := flags.Int("blackhole-every-ms", 0, "period of a recurring path outage")
	blackholeForMs := flags.Int("blackhole-for-ms", 0, "length of each recurring outage")
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
	faults := &udpFaults{
		dropProb:       *dropProb,
		delay:          time.Duration(*delayMs) * time.Millisecond,
		dropAfter:      time.Duration(*dropAfterMs) * time.Millisecond,
		blackholeEvery: time.Duration(*blackholeEveryMs) * time.Millisecond,
		blackholeFor:   time.Duration(*blackholeForMs) * time.Millisecond,
	}
	relay := newUDPRelay(forwardAddress, stats, faults)
	fmt.Fprintf(os.Stderr, "udpshim: %s -> %s drop=%.3f delay=%dms drop-after=%dms blackhole=%dms/%dms\n",
		listener.LocalAddr(), forwardAddress, faults.dropProb, *delayMs, *dropAfterMs, *blackholeEveryMs, *blackholeForMs)

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
			entry, upstreamErr := relay.upstreamFor(from, listener)
			if upstreamErr != nil {
				stats.errors.Add(1)
				continue
			}
			if relay.faults.block(stats) {
				continue
			}
			stats.upPackets.Add(1)
			stats.upBytes.Add(int64(read))
			relay.relayOrDelay(listener, udpPending{data: buffer[:read], client: entry, upward: true})
		}
	}()

	// The delay queue needs a writer of its own; without one the receive loop
	// would be the thing that sleeps, which is the stop-and-wait shape this
	// avoids.
	if relay.delayed != nil {
		go relay.releaseOutOfBand(listener)
	}

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
		"faults": map[string]any{
			"dropped":    stats.dropped.Load(),
			"blackholed": stats.blackholed.Load(),
		},
		"relay_errors": stats.errors.Load(),
		"measured_at":  time.Now().Format(time.RFC3339Nano),
	})
}
