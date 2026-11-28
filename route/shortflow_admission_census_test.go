package route

// The goroutine and stack census for the two short-flow paths this line owns.
//
// # Why a census and not an opinion
//
// M22's premise is the upstream report of 224 concurrent DNS UDP flows turning into 672 waiting
// goroutines. This tree's answer to that was not "make it asynchronous": the packet dispatcher
// already handed the query to a goroutine, and the defect re-appeared in the opposite direction as
// one goroutine per packet. What exists in the shipping code is a bounded number of parked
// goroutines (dnsHijackConcurrency) plus a small control-plane reservation in the connection
// admission (controlPlaneAdmissionReservation). Both are counts, and a count is only a memory bound
// if it is multiplied by a measured per-unit cost. This file measures that cost on this host:
//
//	DNS   one parked hijacked query -> goroutines, goroutine stacks, attribution by stack frame
//	UDP   one relayed UDP session   -> goroutines, goroutine stacks, attribution by stack frame
//
// # Why the attribution is by stack frame rather than a count delta
//
// runtime.NumGoroutine is process-global: it moves with every other test in the binary, with the
// runtime's own workers and with timers. A delta is therefore evidence of nothing unless it is
// large. The census instead walks the goroutine profile (debug=1, one block per goroutine) and
// counts the blocks whose stack contains the function that owns the work under test. That number is
// exact: it cannot be inflated by an unrelated goroutine, and it cannot be deflated by one exiting.
//
// # What these tests do not measure
//
// The buffers behind a parked read belong to the owners that allocated them (the UDP socket's kernel
// buffers, the DNS transport's buffers); only the goroutine stacks are attributed here. TUN ingress,
// a real Network Extension and `phys_footprint` are not reachable from a host test, so no footprint
// claim is made.

import (
	"context"
	"net"
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// dnsHijackOwnerFrame is the stack frame of the goroutine HijackDNSPacket starts. It names the
// closure, so a rename of the enclosing method cannot silently turn the census into zero.
const dnsHijackOwnerFrame = "route.(*Router).HijackDNSPacket.func1"

// udpRelayOwnerFrame is the stack frame of the two copy goroutines one relayed UDP session starts.
const udpRelayOwnerFrame = "route.(*ConnectionManager).packetConnectionCopy"

// goroutinesWithFrame counts the goroutines whose stack contains marker.
//
// runtime.Stack(text=true) prints one block per goroutine ("goroutine N [state]:" plus its frames,
// including the `created by` line), so a frame that belongs to exactly one goroutine per unit of
// work counts that goroutine exactly once. The goroutine PROFILE is deliberately not used here: its
// text form aggregates identical stacks into "N @ 0x..." records, which would make the count a
// property of the aggregation rather than of the goroutines.
func goroutinesWithFrame(t *testing.T, marker string) int {
	t.Helper()
	size := 4 << 20
	for {
		buffer := make([]byte, size)
		written := runtime.Stack(buffer, true)
		if written < len(buffer) {
			return strings.Count(string(buffer[:written]), marker)
		}
		size *= 2
		if size > 256<<20 {
			t.Fatalf("the goroutine dump did not fit in memory while counting %q", marker)
		}
	}
}

// stackBytes reads the runtime's own account of goroutine stack memory.
//
// It is read after two collections so an exited goroutine's stack has been returned, which is what
// makes the difference between two reads the cost of the goroutines that are still parked.
func stackBytes() uint64 {
	runtime.GC()
	runtime.GC()
	samples := []metrics.Sample{{Name: "/memory/classes/heap/stacks:bytes"}}
	metrics.Read(samples)
	return samples[0].Value.Uint64()
}

// TestHijackedDNSQueryGoroutineCensus measures the cost of the DNS hijack bound at four loads.
//
// The loads straddle the bound on purpose: 8 and 64 are below it, 256 is it, and 1024 is four times
// it. The last one is the upstream failure shape (a flood while the resolver is not answering): the
// census must show the same number of parked goroutines as 256, not four times as many.
func TestHijackedDNSQueryGoroutineCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("the census parks hundreds of goroutines and is not a -short test")
	}

	query := new(mDNS.Msg)
	query.SetQuestion("census.example.", mDNS.TypeA)
	payload, err := query.Pack()
	require.NoError(t, err)
	metadata := adapter.InboundContext{
		Inbound:     "tun-in",
		InboundType: C.TypeTun,
		Network:     N.NetworkUDP,
		Source:      M.ParseSocksaddr("10.0.0.2:41000"),
		Destination: M.ParseSocksaddr("1.1.1.1:53"),
	}

	t.Logf("%-8s %-10s %-12s %-14s %-10s %-12s", "load", "parked", "goroutines", "stack bytes", "B/query", "process delta")
	for _, load := range []int{8, 64, dnsHijackConcurrency, dnsHijackConcurrency * 4} {
		want := min(load, dnsHijackConcurrency)
		stalled := newBlockingExchangeRouter()
		router := &Router{logger: log.NewNOPFactory().Logger(), dns: stalled}

		beforeGoroutines := goroutinesWithFrame(t, dnsHijackOwnerFrame)
		beforeStacks := stackBytes()
		beforeProcess := runtime.NumGoroutine()
		for index := 0; index < load; index++ {
			router.HijackDNSPacket(context.Background(), payload, discardingPacketWriter{}, metadata)
		}
		require.Eventuallyf(t, func() bool {
			return router.dnsHijackInFlight.Load() == int64(want)
		}, 5*time.Second, 2*time.Millisecond,
			"%d of %d queries reached the DNS router", want, load)

		parked := goroutinesWithFrame(t, dnsHijackOwnerFrame)
		afterStacks := stackBytes()
		// The process-global count is recorded, not asserted: other tests' goroutines and the
		// runtime's own workers move it, which is exactly why the attribution above is by frame.
		processDelta := runtime.NumGoroutine() - beforeProcess
		stalled.releaseStalled()
		require.Eventuallyf(t, func() bool {
			return router.dnsHijackInFlight.Load() == 0
		}, 10*time.Second, 5*time.Millisecond, "queries stayed parked after the release")

		// Exact, because the profile names the closure. A drop below it would mean a query
		// reached the router without its own goroutine; a rise means the bound leaked.
		require.Equal(t, want, parked-beforeGoroutines,
			"load %d: the hijack must park exactly one goroutine per admitted query", load)
		stackGrowth := int64(afterStacks) - int64(beforeStacks)
		perQuery := stackGrowth / int64(max(want, 1))
		t.Logf("%-8d %-10d %-12d %-14d %-10d %-12d", load, want, want, stackGrowth, perQuery, processDelta)
		// A parked query holds its own stack inside the DNS transport's call chain. The Windows
		// starting stack is 8 KiB, so anything beyond 32 KiB per query means the goroutine is
		// parked deep inside a dial rather than at the dispatcher.
		require.Lessf(t, perQuery, int64(32<<10),
			"load %d: a parked hijacked query holds %d B of goroutine stack", load, perQuery)
	}
}

// udpCensusDialer is the smallest N.Dialer that can carry a packet connection: it returns a real
// unconnected UDP socket, so both relay directions park in a real read instead of a stub.
type udpCensusDialer struct {
	conn net.PacketConn
}

func (d *udpCensusDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (d *udpCensusDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return d.conn, nil
}

// TestRelayedUDPSessionGoroutineCensus measures what one relayed UDP session costs and proves the
// number is two: the upload copy and the download copy, nothing else.
//
// # Why a session with no traffic
//
// A short UDP flow - a DNS query, a STUN binding, one QUIC handshake - exists for milliseconds and
// moves a handful of datagrams. Its cost is the two goroutines that are created before any byte
// moves, so the census holds the sessions open with nothing flowing: that is the state a burst of
// them leaves behind.
func TestRelayedUDPSessionGoroutineCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("the census opens hundreds of real sockets and is not a -short test")
	}

	// A destination port with no protocol mapping, so packetTimeout installs no per-session
	// canceler: the census must count the relay's own goroutines, not a timer's.
	const destinationPort = 40000
	require.Zero(t, C.PortProtocols[destinationPort],
		"the census destination must not carry a protocol timeout")

	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("udp-census"))
	require.NoError(t, manager.Start(adapter.StartStateInitialize, scope))
	t.Cleanup(func() {
		_ = manager.Close()
		_ = scope.Close()
	})

	destination := M.ParseSocksaddr(net.JoinHostPort("127.0.0.1", "40000"))
	metadata := adapter.InboundContext{
		Inbound:     "tun-in",
		InboundType: C.TypeTun,
		Network:     N.NetworkUDP,
		Source:      M.ParseSocksaddr("10.0.0.2:41000"),
		Destination: destination,
	}

	t.Logf("%-8s %-12s %-12s %-14s %-10s %-14s", "sessions", "goroutines", "per session", "stack bytes", "B/session", "process delta")
	for _, load := range []int{8, 64, 256, 1024} {
		baseline := goroutinesWithFrame(t, udpRelayOwnerFrame)
		beforeStacks := stackBytes()
		beforeProcess := runtime.NumGoroutine()
		clients := make([]net.PacketConn, 0, load)
		for index := 0; index < load; index++ {
			clientSocket, err := net.ListenPacket("udp4", "127.0.0.1:0")
			require.NoError(t, err)
			client := bufioPacketConn(clientSocket)
			remoteSocket, err := net.ListenPacket("udp4", "127.0.0.1:0")
			require.NoError(t, err)
			manager.NewPacketConnection(context.Background(),
				&udpCensusDialer{conn: remoteSocket}, client, metadata, nil)
			clients = append(clients, clientSocket, remoteSocket)
		}
		require.Eventuallyf(t, func() bool {
			return goroutinesWithFrame(t, udpRelayOwnerFrame)-baseline == 2*load
		}, 10*time.Second, 2*time.Millisecond,
			"%d sessions did not settle at %d relay goroutines", load, 2*load)

		relayGoroutines := goroutinesWithFrame(t, udpRelayOwnerFrame) - baseline
		afterStacks := stackBytes()
		processDelta := runtime.NumGoroutine() - beforeProcess
		stackGrowth := int64(afterStacks) - int64(beforeStacks)
		perSession := stackGrowth / int64(load)
		t.Logf("%-8d %-12d %-12d %-14d %-10d %-14d", load, relayGoroutines, relayGoroutines/load, stackGrowth, perSession, processDelta)
		require.Equal(t, 2, relayGoroutines/load,
			"load %d: a relayed UDP session must cost its two copy goroutines and nothing else", load)
		require.Lessf(t, perSession, int64(64<<10),
			"load %d: a relayed UDP session holds %d B of goroutine stack", load, perSession)

		for _, socket := range clients {
			_ = socket.Close()
		}
		require.Eventuallyf(t, func() bool {
			return goroutinesWithFrame(t, udpRelayOwnerFrame) == baseline
		}, 10*time.Second, 5*time.Millisecond,
			"sessions did not release their relay goroutines after close (load %d)", load)
	}
}

// bufioPacketConn is the production adapter from a net.PacketConn to the packet interface the relay
// consumes, so the census exercises the same wrapper shape a real flow arrives through.
func bufioPacketConn(conn net.PacketConn) N.NetPacketConn {
	return bufio.NewPacketConn(conn)
}
