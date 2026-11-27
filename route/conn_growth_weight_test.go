package route

// Interaction tests for the C0 copy-growth wiring.
//
// These are adversarial: they are not about whether the budget counts what it is
// TOLD (that is common/resourceguard's own unit test), but whether the number the
// route wiring tells it is the number of buffers the copy engine actually holds,
// when the writer is the scheduler gate or a protocol wrapper that only becomes
// replaceable after its first write.

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/common/trafficsched"
	"github.com/sagernet/sing-box/log"

	"github.com/sagernet/sing/common/buf"
	B "github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// countingVectorConn is a destination the copy engine can vectorise. It wraps the real
// socket so the data still flows, and counts WriteVectorised calls so a test can tell
// the vectorised loop from the plain grown one.
type countingVectorConn struct {
	net.Conn
	calls atomic.Int32
}

func (c *countingVectorConn) WriteVectorised(buffers []*buf.Buffer) error {
	c.calls.Add(1)
	defer buf.ReleaseMulti(buffers)
	for _, buffer := range buffers {
		if _, err := c.Conn.Write(buffer.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// flipReplaceableConn models the protocol wrappers this tree actually has
// (common/tlsfragment.Conn, common/tlsspoof.Conn, transport/v2rayhttp.HTTPConn):
// WriterReplaceable is FALSE until the first write, then true. Unlike tlsfragment it
// can be built without a live TLS stream, and it exposes the vectorised writer so the
// test can prove the engine entered the vectorised loop.
type flipReplaceableConn struct {
	net.Conn
	flipped  atomic.Bool
	upstream N.VectorisedWriter
}

func (c *flipReplaceableConn) Write(p []byte) (int, error) {
	c.flipped.Store(true)
	return c.Conn.Write(p)
}

func (c *flipReplaceableConn) WriterReplaceable() bool { return c.flipped.Load() }

func (c *flipReplaceableConn) UpstreamWriter() any { return c.upstream }

func newRelayGeneration(t *testing.T, flows int) (*ConnectionManager, *resourceguard.GrowthBudget, chan net.Conn) {
	t.Helper()
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	budget := resourceguard.NewGrowthBudget(resourceguard.GrowthConfig{Flows: flows}, nil)
	manager.growth = budget
	pending := make(chan net.Conn, 16)
	t.Cleanup(func() {
		_ = manager.Close()
	})
	return manager, budget, pending
}

// transferAtoB writes total bytes into a relay and drains the far side. It returns as
// soon as the bytes are written; the relay is still open, which is what lets the test
// observe the permit while the enlarged buffers are held.
func transferAtoB(t *testing.T, app net.Conn, peer net.Conn, total int64) {
	t.Helper()
	go func() {
		_, _ = io.Copy(io.Discard, peer)
	}()
	payload := make([]byte, 32<<10)
	var written int64
	for written < total {
		n, err := app.Write(payload)
		if err != nil {
			t.Fatalf("write %d/%d: %v", written, total, err)
		}
		written += int64(n)
	}
}

func waitInUse(t *testing.T, budget *resourceguard.GrowthBudget, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return budget.Stats().InUse == want
	}, 5*time.Second, time.Millisecond, "growth budget InUse never became %d; stats: %+v", want, budget.Stats())
}

// TestGrowthBudgetGenerationIsNotReused is the route-level half of the reload question:
// the old generation's permit returns to zero when its connections end, and the next
// generation starts a NEW counter rather than inheriting the old one.
func TestGrowthBudgetGenerationIsNotReused(t *testing.T) {
	oldManager, oldBudget, oldPending := newRelayGeneration(t, 256)
	appA, inboundA := tcpPair(t)
	peerA, outboundA := tcpPair(t)
	oldPending <- outboundA
	oldManager.NewConnection(context.Background(), &growthProbeDialer{pending: oldPending}, inboundA,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("generation-old.example", 443)}, nil)
	transferAtoB(t, appA, peerA, 2<<20)
	waitInUse(t, oldBudget, 8)

	oldManager.CloseAll()
	_ = appA.Close()
	_ = peerA.Close()
	waitInUse(t, oldBudget, 0)

	// The "next generation": a different manager and a different budget object. No code
	// path in production reuses a ConnectionManager across Boxes (box.go builds one per
	// Box); this pins the property the reload relies on.
	newManager, newBudget, newPending := newRelayGeneration(t, 256)
	require.NotSame(t, oldBudget, newBudget)
	require.Zero(t, newBudget.Stats().InUse, "a new generation must not inherit the old generation's InUse")

	appB, inboundB := tcpPair(t)
	peerB, outboundB := tcpPair(t)
	newPending <- outboundB
	newManager.NewConnection(context.Background(), &growthProbeDialer{pending: newPending}, inboundB,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("generation-new.example", 443)}, nil)
	transferAtoB(t, appB, peerB, 2<<20)
	waitInUse(t, newBudget, 8)
	require.Zero(t, oldBudget.Stats().InUse, "the closed generation must stay at zero when the new one grows")

	newManager.CloseAll()
	_ = appB.Close()
	_ = peerB.Close()
	waitInUse(t, newBudget, 0)
}

// TestGrowthWeightThroughTheSchedulerGate is the positive control for Q2: a gate in
// front of a vectorisable writer is charged the vectorised price (8), and the engine
// really does enter the vectorised loop through the gate (WriteVectorised observed).
func TestGrowthWeightThroughTheSchedulerGate(t *testing.T) {
	manager, budget, _ := newRelayGeneration(t, 256)
	app, source := tcpPair(t)
	peer, rawDestination := tcpPair(t)
	sink := &countingVectorConn{Conn: rawDestination}
	gate := trafficsched.NewGate(sink, nil)

	require.Equal(t, B.DefaultBatchSize, growthBuffersForDirection(source, gate),
		"the gate must not hide the upstream's vectorised writer from the weight probe")

	done := new(atomic.Bool)
	go manager.connectionCopy(context.Background(), source, rawDestination, true, resourceguard.CopyGrowthDefault,
		done, nil, gate, nil, newFlowTeardown(context.Background(), nil, source, rawDestination),
		growthPlan{class: trafficclass.ClassBulk})

	transferAtoB(t, app, peer, 2<<20)
	require.Eventually(t, func() bool { return sink.calls.Load() > 0 }, 5*time.Second, time.Millisecond,
		"the engine did not enter the vectorised loop through the gate")
	waitInUse(t, budget, 8)
	_ = app.Close()
	_ = peer.Close()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, 5*time.Second, time.Millisecond)
}

// TestGrowthWeightCoversPostStartReplaceableWriter is the failing invariant: the
// weight is resolved ONCE before the copy starts. A writer that is not replaceable
// then but becomes replaceable on its first write (tlsfragment.Conn's actual shape)
// is charged 1 unit, while the copy engine - asking CreateVectorisedWriter at the
// growth boundary, after the write - vectorises and holds DefaultBatchSize buffers.
func TestGrowthWeightCoversPostStartReplaceableWriter(t *testing.T) {
	manager, budget, _ := newRelayGeneration(t, 256)
	app, source := tcpPair(t)
	peer, rawDestination := tcpPair(t)
	sink := &countingVectorConn{Conn: rawDestination}
	flip := &flipReplaceableConn{Conn: sink, upstream: sink}
	gate := trafficsched.NewGate(flip, nil)

	require.Equal(t, 1, growthBuffersForDirection(source, gate),
		"before the first write the wrapper hides the vectorised writer, which is the production shape")

	done := new(atomic.Bool)
	go manager.connectionCopy(context.Background(), source, rawDestination, true, resourceguard.CopyGrowthDefault,
		done, nil, gate, nil, newFlowTeardown(context.Background(), nil, source, rawDestination),
		growthPlan{class: trafficclass.ClassBulk})

	transferAtoB(t, app, peer, 2<<20)
	require.Eventually(t, func() bool { return sink.calls.Load() > 0 }, 5*time.Second, time.Millisecond,
		"the engine did not enter the vectorised loop after the writer became replaceable")
	require.Eventually(t, func() bool { return budget.Stats().InUse > 0 }, 5*time.Second, time.Millisecond,
		"the growth boundary was never charged")
	require.Equal(t, B.DefaultBatchSize, budget.Stats().InUse,
		"the engine holds DefaultBatchSize grown buffers but the budget was charged once the probe ran at copy start")
	_ = app.Close()
	_ = peer.Close()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, 5*time.Second, time.Millisecond)
}

// TestGrowthWeightWithProductionTLSFragmentWrapper is the reachable form of the same
// defect: route/conn.go wraps the outbound in common/tlsfragment.Conn whenever the
// inbound asks for TLS fragmentation. That wrapper's WriterReplaceable is false until
// the first packet is written, so the weight probe (at copy start) reads 1; by the
// time the growth boundary is reached the wrapper is replaceable and the engine
// vectorises through it. This uses the real tf.Conn and the real NewConnection wiring;
// only the outbound socket is a counting double so the vectorised call is observable.
func TestGrowthWeightWithProductionTLSFragmentWrapper(t *testing.T) {
	manager, budget, pending := newRelayGeneration(t, 256)
	app, inbound := tcpPair(t)
	peer, raw := tcpPair(t)
	sink := &countingVectorConn{Conn: raw}
	pending <- sink
	manager.NewConnection(context.Background(), &growthProbeDialer{pending: pending}, inbound,
		adapter.InboundContext{
			Destination: M.ParseSocksaddrHostPort("tls-fragment.example", 443),
			TLSFragment: true,
		}, nil)

	transferAtoB(t, app, peer, 2<<20)
	require.Eventually(t, func() bool { return sink.calls.Load() > 0 }, 5*time.Second, time.Millisecond,
		"the engine did not vectorise through the tlsfragment wrapper after its first write")
	require.Eventually(t, func() bool { return budget.Stats().InUse > 0 }, 5*time.Second, time.Millisecond,
		"the growth boundary was never charged")
	require.Equal(t, B.DefaultBatchSize, budget.Stats().InUse,
		"tlsfragment.Conn made the engine vectorise, but the probe at copy start charged one buffer")
	_ = app.Close()
	_ = peer.Close()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, 5*time.Second, time.Millisecond)
}
