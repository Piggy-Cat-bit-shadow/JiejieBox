package resourceguard

// The growth budget's copy-engine seam, exercised through sing's real copy engine with
// the production GrowthBoundary wiring: the decision is made at the actual growth
// boundary, a quiet direction never asks, a refused direction keeps the small buffer and
// retries at a bounded milestone, and the permit's lifetime is exactly the enlarged
// buffer's lifetime.
//
// These complement growth_relay_test.go (which owns the permit lifecycle) and
// growth_test.go (which owns the policy arithmetic): here the seam and the engine are
// run together, which is the only way to see "quiet holds nothing" and "a refused
// direction can recover later" as behaviour rather than as comments.

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"

	"github.com/stretchr/testify/require"
)

// boundaryCopy is one direction copied through the growth-boundary seam.
type boundaryCopy struct {
	done   chan struct{}
	copied int64
	err    error
}

func startBoundaryCopy(budget *GrowthBudget, class trafficclass.Class, threshold int64, geometry bool, source io.Reader, destination io.Writer) *boundaryCopy {
	direction := &boundaryCopy{done: make(chan struct{})}
	var seam bufio.IncreaseBufferPermit
	if budget != nil {
		seam = budget.GrowthBoundary(class, geometry)
	}
	go func() {
		defer close(direction.done)
		direction.copied, direction.err = bufio.CopyWithIncreateBufferPermit(destination, source, threshold, bufio.DefaultBatchSize, seam)
	}()
	return direction
}

func (d *boundaryCopy) wait(t *testing.T) {
	t.Helper()
	select {
	case <-d.done:
	case <-time.After(testWait):
		t.Fatal("the copy direction never returned")
	}
}

// pauseWriter blocks the copy after it has accepted pauseAt bytes, which is how a test
// keeps a direction that HAS grown alive while it inspects the budget.
type pauseWriter struct {
	access  sync.Mutex
	written int
	pauseAt int
	resume  chan struct{}
	paused  bool
}

func (w *pauseWriter) Write(payload []byte) (int, error) {
	w.access.Lock()
	shouldPause := !w.paused && w.written >= w.pauseAt && w.resume != nil
	if shouldPause {
		w.paused = true
		resume := w.resume
		w.access.Unlock()
		<-resume
		w.access.Lock()
		w.resume = nil
	}
	w.written += len(payload)
	w.access.Unlock()
	return len(payload), nil
}

func (w *pauseWriter) release() {
	w.access.Lock()
	defer w.access.Unlock()
	if w.resume != nil {
		close(w.resume)
		w.resume = nil
	}
}

// pauseReader stops feeding the copy after it has produced pauseAt bytes. The refusal
// recovery test needs the refused direction to WAIT between the first refused decision
// and the milestone at which it is allowed to ask again.
type pauseReader struct {
	access  sync.Mutex
	total   int
	copied  int
	pauseAt int
	resume  chan struct{}
	paused  bool
	reads   []int
}

func newPauseReader(total int, pauseAt int) *pauseReader {
	return &pauseReader{total: total, pauseAt: pauseAt, resume: make(chan struct{})}
}

func (r *pauseReader) Read(payload []byte) (int, error) {
	r.access.Lock()
	if !r.paused && r.pauseAt > 0 && r.copied >= r.pauseAt && r.resume != nil {
		r.paused = true
		resume := r.resume
		r.access.Unlock()
		<-resume
		r.access.Lock()
		r.resume = nil
	}
	defer r.access.Unlock()
	if r.copied >= r.total {
		return 0, io.EOF
	}
	n := min(len(payload), testChunk, r.total-r.copied)
	for index := 0; index < n; index++ {
		payload[index] = 'g'
	}
	r.copied += n
	r.reads = append(r.reads, len(payload))
	return n, nil
}

func (r *pauseReader) release() {
	r.access.Lock()
	defer r.access.Unlock()
	if r.resume != nil {
		close(r.resume)
		r.resume = nil
	}
}

func (r *pauseReader) pausedNow() bool {
	r.access.Lock()
	defer r.access.Unlock()
	return r.paused
}

func (r *pauseReader) grownReads() int {
	r.access.Lock()
	defer r.access.Unlock()
	grown := 0
	for _, size := range r.reads {
		if size > buf.BufferSize {
			grown++
		}
	}
	return grown
}

// TestBoundaryQuietDirectionsHoldNothing pins the core repair at the policy boundary:
// two quiet directions (real copies that have not produced enough bytes to grow) ask
// nothing, a later bulk direction grows while they are still open, and a second quiet
// direction started while the bulk permit is held does not affect occupancy.
func TestBoundaryQuietDirectionsHoldNothing(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 2}, nil)
	quietGate := make(chan struct{})
	quietOne := startBoundaryCopy(budget, trafficclass.ClassDefault, CopyGrowthDefault, false,
		newScriptedReader(8*testChunk, quietGate, nil), &countWriter{})
	quietTwo := startBoundaryCopy(budget, trafficclass.ClassDefault, CopyGrowthDefault, false,
		newScriptedReader(8*testChunk, quietGate, nil), &countWriter{})

	// Neither direction can reach the threshold: their whole payload is smaller than it.
	require.Zero(t, budget.Stats().InUse, "a quiet direction must hold no growth permit")
	require.Zero(t, budget.Stats().Granted)

	bulkWriter := &pauseWriter{pauseAt: 600 * 1000, resume: make(chan struct{})}
	bulk := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		newScriptedReader(2<<20, nil, nil), bulkWriter)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 1 }, testWait, testPoll,
		"the bulk direction was not granted growth while the quiet directions were open")
	require.Equal(t, uint64(1), budget.Stats().Granted)

	// A third quiet direction opened while the permit is held changes nothing.
	quietGateThree := make(chan struct{})
	quietThree := startBoundaryCopy(budget, trafficclass.ClassDefault, CopyGrowthDefault, false,
		newScriptedReader(8*testChunk, quietGateThree, nil), &countWriter{})
	require.Equal(t, 1, budget.Stats().InUse)

	// The quiet directions end: they never held anything, so occupancy is unchanged.
	close(quietGate)
	close(quietGateThree)
	quietOne.wait(t)
	quietTwo.wait(t)
	quietThree.wait(t)
	require.Equal(t, 1, budget.Stats().InUse)

	bulkWriter.release()
	bulk.wait(t)
	require.NoError(t, bulk.err)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll)
	final := budget.Stats()
	require.Zero(t, final.DoubleReleases)
	require.Equal(t, final.Granted-final.Released, uint64(final.InUse))
	require.Equal(t, uint64(1), final.Granted, "only the bulk direction ever grew")
}

// TestBoundaryPressureTightensAndRecovers pins the pressure levels at the new boundary:
// four grown directions occupy the Normal cap, Armed halves the cap and refuses the next
// growth even while capacity remains above the halved cap, a freed permit admits one
// direction again, and Critical refuses even when nothing is held.
func TestBoundaryPressureTightensAndRecovers(t *testing.T) {
	t.Parallel()

	var level atomic.Uint32
	level.Store(uint32(LevelNormal))
	budget := NewGrowthBudget(GrowthConfig{Flows: 4}, func() Level { return Level(level.Load()) })

	held := make([]*pauseWriter, 0, 4)
	for index := 0; index < 4; index++ {
		writer := &pauseWriter{pauseAt: 600 * 1000, resume: make(chan struct{})}
		held = append(held, writer)
		startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
			newScriptedReader(2<<20, nil, nil), writer)
	}
	require.Eventually(t, func() bool { return budget.Stats().InUse == 4 }, testWait, testPoll,
		"four bulk directions must each hold one permit at Normal")

	level.Store(uint32(LevelArmed))
	require.Eventually(t, func() bool { return budget.Stats().Flows == 2 }, testWait, testPoll)

	refusedReader := newScriptedReader(2<<20, nil, nil)
	refusedGrantedBefore := budget.Stats().Granted
	refused := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		refusedReader, &countWriter{})
	refused.wait(t)
	require.Zero(t, refusedReader.grownReads(), "Armed must refuse a new direction while four permits are held")
	require.Equal(t, refusedGrantedBefore, budget.Stats().Granted)

	// Free two permits: two remain in use, exactly the Armed total, so the next growth is
	// still refused.
	held[0].release()
	held[1].release()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 2 }, testWait, testPoll)
	stillRefusedReader := newScriptedReader(2<<20, nil, nil)
	stillRefused := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		stillRefusedReader, &countWriter{})
	stillRefused.wait(t)
	require.Zero(t, stillRefusedReader.grownReads(),
		"a direction at the Armed total must still be refused at its growth boundary")

	// Free one more: below the Armed total, so a new direction may grow again.
	held[2].release()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 1 }, testWait, testPoll)
	admittedReader := newScriptedReader(2<<20, nil, nil)
	admitted := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		admittedReader, &countWriter{})
	admitted.wait(t)
	require.NotZero(t, admittedReader.grownReads(), "a freed permit must admit a new direction at Armed")
	require.Eventually(t, func() bool { return budget.Stats().InUse == 1 }, testWait, testPoll)

	// Critical refuses ordinary growth even with the pool empty.
	held[3].release()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll)
	level.Store(uint32(LevelCritical))
	criticalReader := newScriptedReader(2<<20, nil, nil)
	critical := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		criticalReader, &countWriter{})
	critical.wait(t)
	require.Zero(t, criticalReader.grownReads(), "Critical must refuse ordinary growth")
	require.Zero(t, budget.Stats().InUse)
	require.Zero(t, budget.Stats().DoubleReleases)
}

// TestBoundaryInteractiveReserveBeatsBulk pins the reserve at the new boundary: with the
// ordinary cap full of bulk growth, an interactive direction may still grow from the
// reserve while another bulk direction is refused.
func TestBoundaryInteractiveReserveBeatsBulk(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 2, InteractiveReserve: 1}, nil)
	held := make([]*pauseWriter, 0, 2)
	for index := 0; index < 2; index++ {
		writer := &pauseWriter{pauseAt: 600 * 1000, resume: make(chan struct{})}
		held = append(held, writer)
		startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
			newScriptedReader(2<<20, nil, nil), writer)
	}
	require.Eventually(t, func() bool { return budget.Stats().InUse == 2 }, testWait, testPoll)

	bulkReader := newScriptedReader(2<<20, nil, nil)
	bulk := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		bulkReader, &countWriter{})
	bulk.wait(t)
	require.Zero(t, bulkReader.grownReads(), "the ordinary cap is full of bulk growth")

	interactiveWriter := &pauseWriter{pauseAt: 600 * 1000, resume: make(chan struct{})}
	interactiveReader := newScriptedReader(2<<20, nil, nil)
	interactive := startBoundaryCopy(budget, trafficclass.ClassInteractive, CopyGrowthDefault, false,
		interactiveReader, interactiveWriter)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 3 }, testWait, testPoll,
		"an interactive direction must grow from the reserve")
	require.NotZero(t, interactiveReader.grownReads())
	require.Equal(t, 1, budget.Stats().ReservedInUse)
	require.Equal(t, uint64(3), budget.Stats().Granted)
	interactiveWriter.release()
	interactive.wait(t)

	for _, writer := range held {
		writer.release()
	}
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll)
	require.Zero(t, budget.Stats().DoubleReleases)
}

// TestBoundaryRefusalRecoversWhenAPermitFrees is the recovery the seam exists for: a
// direction refused at 512000 bytes waits (still copying on the small buffer), the
// permit holder ends, and the refused direction grows at the next bounded milestone.
func TestBoundaryRefusalRecoversWhenAPermitFrees(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 1}, nil)

	holderWriter := &pauseWriter{pauseAt: 600 * 1000, resume: make(chan struct{})}
	holder := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		newScriptedReader(2<<20, nil, nil), holderWriter)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 1 }, testWait, testPoll)

	refusedReader := newPauseReader(2<<20, 600*1000)
	refused := startBoundaryCopy(budget, trafficclass.ClassBulk, CopyGrowthDefault, false,
		refusedReader, &countWriter{})
	require.Eventually(t, refusedReader.pausedNow, testWait, testPoll,
		"the refused direction never reached its pause point")
	require.Zero(t, refusedReader.grownReads(), "the refused direction must keep the small buffer")
	require.Equal(t, 1, budget.Stats().InUse)
	require.GreaterOrEqual(t, budget.Stats().Refused, uint64(1))

	holderWriter.release()
	holder.wait(t)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll,
		"the holder's permit did not return when its copy ended")

	refusedReader.release()
	refused.wait(t)
	require.NoError(t, refused.err)
	require.NotZero(t, refusedReader.grownReads(),
		"the refused direction must grow at its retry milestone after a permit frees")
	require.Equal(t, uint64(2), budget.Stats().Granted)
	require.Equal(t, 0, budget.Stats().InUse)
	require.Zero(t, budget.Stats().DoubleReleases)
}

// TestBoundaryGeometryIsNeverRefusedByTheOrdinaryCap pins the exemption through the real
// engine: with the ordinary cap zero, a geometry-required direction still grows, is
// counted for diagnosis, and takes no permit.
func TestBoundaryGeometryIsNeverRefusedByTheOrdinaryCap(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 0}, nil)
	geometryReader := newScriptedReader(4*256*1024, nil, nil)
	geometry := startBoundaryCopy(budget, trafficclass.ClassDefault, CopyGrowthImmediate, true,
		geometryReader, &countWriter{})
	geometry.wait(t)
	require.NoError(t, geometry.err)
	require.NotZero(t, geometryReader.grownReads(), "geometry-required growth must not be refused")
	stats := budget.Stats()
	require.Equal(t, uint64(1), stats.GeometryNeeded)
	require.Zero(t, stats.InUse)
	require.Zero(t, stats.Granted)
	require.Zero(t, stats.Refused)
}
