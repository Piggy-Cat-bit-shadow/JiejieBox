package box_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"runtime"
	"runtime/metrics"
	"strconv"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/json"
)

// What one connection that is being HELD costs the process.
//
// # Why this is a test and not an audit line
//
// `outbound_cold_cost_test.go` pins the half of the census that answers "what does a node that is
// configured but never dialled cost" (nothing, for the light kinds). The other half is the one that
// decides whether a tunnel that holds forty connections fits: a held flow still keeps goroutines
// parked in a read, and the goroutine stacks plus the live heap behind them are the per-flow
// increment that a footprint budget is spent on.
//
// The measurement is the process's, not the estimator's: live heap and heap stack bytes from
// runtime/metrics after three forced collections, and the goroutine count, all taken with the
// connections established and idle, then again after they are closed. It is the same shape Clash
// uses in `held_connection_budget_test.go` - delta per connection, not a total - because that is
// what makes the number comparable across processes and machines.
//
// # What this deliberately does not measure
//
// TUN ingress, and therefore `phys_footprint`. A Network Extension's budget is spent on its
// footprint, and a host test cannot see it; the SOCKS inbound here carries the same relay and the
// same two copies, so the per-flow figure is the relay's and not the tunnel device's.
//
// # The budgets
//
// They are derived from THIS measurement, not copied from another project: the harness prints the
// delta and the assertions allow headroom over what the tree does today, so a change that
// doubles per-flow cost fails while ordinary noise does not.

const (
	heldConnectionCount = 64
	// heldConnectionStackBudget is per connection, in bytes of goroutine stack. Two goroutines
	// parked in a read are the floor; a frame that grows because the relay is nested inside the
	// connection's preparation code is what would blow it.
	heldConnectionStackBudget = 32 << 10
	// heldConnectionBookkeeping is what a held flow may cost on top of its one read buffer: the
	// connection objects, the route snapshot, the SOCKS request, the cached reader's header, the
	// timer and the two parked goroutines' bookkeeping. It is measured, not assumed - see the
	// budget derivation below.
	heldConnectionBookkeeping = 12 << 10
	// heldConnectionGoroutineBudget is per connection. Two copies per held flow is the design; a
	// third would mean a per-flow goroutine that is not the relay.
	heldConnectionGoroutineBudget = 3.0
)

// heldConnectionHeapBudget is the per-flow live-heap budget, expressed in the build's own buffer
// geometry rather than as an absolute number.
//
// # Why the geometry and not a constant
//
// A held flow's live heap is dominated by ONE `buf.BufferSize` read buffer: the direction that has
// data pending allocates its cached reader, and the other direction's writer allocates lazily on
// first write, so a flow that is held and quiet keeps one buffer and not two. `buf.BufferSize` is
// 32 KiB in the default build and 16 KiB under `with_low_memory` (which is what iOS and tvOS
// ship), so the same invariant - one buffer plus bookkeeping - is 44 KiB in one build and 28 KiB
// in the other. Writing the budget as a constant would either be slack enough to hide a doubled
// buffer on the mobile geometry, or fail on the desktop one for no reason.
//
// This is also why the number is not Clash's 24 KiB: that is a different relay with a different
// buffer strategy, measured on a different build. The figure here is what THIS tree does, pinned
// so it cannot silently double.
func heldConnectionHeapBudget() int64 {
	return int64(buf.BufferSize) + heldConnectionBookkeeping
}

type heldConnectionSample struct {
	heap       uint64
	stacks     uint64
	goroutines int
}

// sampleHeldConnections reads the process's live heap and stack bytes after three collections.
//
// Three rather than two because the relay's own bookkeeping (a connection object, a
// bufio.Reader/Writer pair, the route's metadata) becomes garbage when a connection is opened and
// must not be counted as live; the existing cold-cost census uses two and this one holds more
// churn.
func sampleHeldConnections() heldConnectionSample {
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	samples := []metrics.Sample{
		{Name: "/gc/heap/live:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
	}
	metrics.Read(samples)
	return heldConnectionSample{
		heap:       samples[0].Value.Uint64(),
		stacks:     samples[1].Value.Uint64(),
		goroutines: runtime.NumGoroutine(),
	}
}

func TestAHeldConnectionStaysWithinItsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("holding sixty-four connections through the real relay is not a -short test")
	}
	destination, accepted, closedDestination := holdDestination(t)
	socksAddress := startSOCKSBox(t)

	before := sampleHeldConnections()
	held := make([]net.Conn, 0, heldConnectionCount)
	for index := 0; index < heldConnectionCount; index++ {
		conn, err := dialThroughSOCKS(socksAddress, destination)
		if err != nil {
			t.Fatalf("connection %d: %v", index, err)
		}
		held = append(held, conn)
	}
	t.Cleanup(func() {
		for _, conn := range held {
			_ = conn.Close()
		}
	})
	waitForAccepted(t, accepted, heldConnectionCount)
	// Let the relays settle into their parked reads: the measurement is taken with nothing moving.
	time.Sleep(time.Second)

	after := sampleHeldConnections()
	stacks := (int64(after.stacks) - int64(before.stacks)) / heldConnectionCount
	heap := (int64(after.heap) - int64(before.heap)) / heldConnectionCount
	goroutines := float64(after.goroutines-before.goroutines) / heldConnectionCount
	t.Logf("a held connection keeps %d B of stacks (budget %d), %d B of live heap (budget %d: one %d B buffer + %d), and %.2f goroutines",
		stacks, heldConnectionStackBudget, heap, heldConnectionHeapBudget(), buf.BufferSize, heldConnectionBookkeeping, goroutines)
	if goroutines < 1.5 {
		t.Fatalf("%.2f goroutines per connection at the sample: the flows were not all held, so the "+
			"per-flow figure below is not per flow", goroutines)
	}
	if goroutines > heldConnectionGoroutineBudget {
		t.Errorf("%.2f goroutines per held connection, over %.0f: a per-flow goroutine that is not "+
			"one of the two copies is being kept for the connection's life",
			goroutines, heldConnectionGoroutineBudget)
	}
	if stacks > heldConnectionStackBudget {
		t.Errorf("a held connection keeps %d B of goroutine stacks, over its %d B budget: a goroutine "+
			"that parks for the connection's life is parked deeper than the relay needs",
			stacks, heldConnectionStackBudget)
	}
	if heap > heldConnectionHeapBudget() {
		t.Errorf("a held connection keeps %d B of live heap, over its %d B budget (one %d B buffer + %d): a second cached buffer is the usual way this doubles", heap, heldConnectionHeapBudget(), buf.BufferSize, heldConnectionBookkeeping)
	}

	// The release half: the budget is only meaningful if closing returns to it. A held flow that
	// leaves a goroutine or a buffer behind would show up as a residual after every connection is
	// gone, and that is what a long NE session would accumulate.
	for _, conn := range held {
		_ = conn.Close()
	}
	held = held[:0]
	closedDestination()
	// The relay releases on its own schedule (a read returns, a copy finishes), so this waits for
	// the process to come back rather than asserting an instant.
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		residual := sampleHeldConnections()
		if residual.goroutines <= before.goroutines {
			t.Logf("after closing every connection: %d B of stacks and %d B of live heap above the "+
				"baseline, goroutines back to %d",
				int64(residual.stacks)-int64(before.stacks), int64(residual.heap)-int64(before.heap),
				residual.goroutines)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines are still running after every connection was closed: a held flow "+
				"left something behind (baseline %d, now %d)",
				residual.goroutines-before.goroutines, before.goroutines, residual.goroutines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// holdDestination is a listener that accepts and then says nothing, so both relay directions park
// in a read. It reports every accept so the test can wait for all the flows to be established.
func holdDestination(t *testing.T) (net.Addr, func() int, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var connections []net.Conn
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			mutex.Lock()
			connections = append(connections, conn)
			mutex.Unlock()
		}
	}()
	count := func() int {
		mutex.Lock()
		defer mutex.Unlock()
		return len(connections)
	}
	closeAll := func() {
		_ = listener.Close()
		mutex.Lock()
		defer mutex.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		connections = nil
	}
	t.Cleanup(closeAll)
	return listener.Addr(), count, closeAll
}

func waitForAccepted(t *testing.T, accepted func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for accepted() < want {
		if time.Now().After(deadline) {
			t.Fatalf("the destination holds %d of %d connections", accepted(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startSOCKSBox assembles a real Box with a SOCKS inbound and a direct outbound, which is the
// smallest configuration that holds a flow through the production relay.
func startSOCKSBox(t *testing.T) string {
	t.Helper()
	port := reservePort(t)
	config := `{
  "log": {"disabled": true},
  "inbounds": [{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": ` + strconv.Itoa(port) + `}],
  "outbounds": [{"type": "direct", "tag": "out"}]
}`
	ctx := include.Context(context.Background())
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = instance.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// dialThroughSOCKS performs the smallest SOCKS5 handshake that yields an established stream: no
// auth, CONNECT to the destination's address. After it returns, the connection is a plain byte
// stream and the box is holding it.
func dialThroughSOCKS(proxyAddress string, destination net.Addr) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp4", proxyAddress, 10*time.Second)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	greeting := make([]byte, 2)
	if _, err = io.ReadFull(conn, greeting); err != nil {
		return fail(err)
	}
	if greeting[0] != 5 || greeting[1] != 0 {
		return fail(fmt.Errorf("proxy answered %v", greeting))
	}
	tcpAddress, ok := destination.(*net.TCPAddr)
	if !ok {
		return fail(fmt.Errorf("destination is %T", destination))
	}
	ipv4 := tcpAddress.IP.To4()
	if ipv4 == nil {
		return fail(fmt.Errorf("destination %v is not IPv4", tcpAddress))
	}
	request := []byte{5, 1, 0, 1}
	request = append(request, ipv4...)
	request = binary.BigEndian.AppendUint16(request, uint16(tcpAddress.Port))
	if _, err = conn.Write(request); err != nil {
		return fail(err)
	}
	reply := make([]byte, 10)
	if _, err = io.ReadFull(conn, reply); err != nil {
		return fail(err)
	}
	if reply[1] != 0 {
		return fail(fmt.Errorf("proxy refused the connect: code %d", reply[1]))
	}
	return conn, nil
}
