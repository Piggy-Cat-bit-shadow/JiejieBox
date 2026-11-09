package resourceguard

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"

	"github.com/stretchr/testify/require"
)

// The relay's two copy directions, run through sing's real copy engine with the permits
// wired the way route/conn.go wires them (one Admit per direction, released in the
// direction's defer). The unit tests in growth_test.go check the policy as numbers and
// growth_copy_test.go checks the engine's threshold semantics; these run the lifecycle the
// fault matrix asks for:
//
//   - a direction holds its permit for exactly as long as its copy runs, and gives it back
//     when the copy returns - not when the gate that fed it is closed;
//   - half-close (one read side ending) frees that direction's permit while the other
//     direction keeps copying and holding its own;
//   - a cancelled direction returns its permit on the copy's error path, exactly once;
//   - after pressure drops and the old copies end, the pool admits again;
//   - a geometry-required direction grows without a permit at all.
//
// What is NOT covered here: route/conn.go's ConnectionManager.Start (the network window
// owns that file) and a real network peer. This is the copy engine plus the permit
// lifecycle, which is the part this budget owns.

const (
	testWait  = 5 * time.Second
	testPoll  = time.Millisecond
	testChunk = 4096
)

// scriptedReader feeds one copy direction: it emits testChunk bytes per read until the
// total is reached, then EOF (or err). A non-nil gate blocks the first read, which is how
// a test keeps a direction "in flight".
type scriptedReader struct {
	access    sync.Mutex
	remaining int
	gate      chan struct{}
	err       error
	reads     []int
}

func newScriptedReader(total int, gate chan struct{}, readErr error) *scriptedReader {
	return &scriptedReader{remaining: total, gate: gate, err: readErr}
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	r.access.Lock()
	gate := r.gate
	r.gate = nil
	r.access.Unlock()
	if gate != nil {
		<-gate
	}
	r.access.Lock()
	defer r.access.Unlock()
	if r.remaining <= 0 {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	if n > testChunk {
		n = testChunk
	}
	for index := 0; index < n; index++ {
		p[index] = 'r'
	}
	r.remaining -= n
	r.reads = append(r.reads, len(p))
	return n, nil
}

func (r *scriptedReader) grownReads() int {
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

// relayDirection is one copy direction wired the way route/conn.go wires it.
type relayDirection struct {
	reader    *scriptedReader
	writer    *countWriter
	gate      chan struct{}
	gateOnce  sync.Once
	threshold int64
	permit    *GrowthPermit
	done      chan struct{}
	copied    int64
	err       error
}

// startRelayDirection performs the Admit call and starts the copy. A non-nil gate holds
// the direction's first read until finish() (or the test) releases it.
func startRelayDirection(budget *GrowthBudget, class trafficclass.Class, geometry bool, total int, gate chan struct{}, readErr error) *relayDirection {
	direction := &relayDirection{
		reader: newScriptedReader(total, gate, readErr),
		writer: &countWriter{},
		gate:   gate,
		done:   make(chan struct{}),
	}
	// This is the production shape: decide once per direction, release in the defer.
	direction.threshold, direction.permit = budget.Admit(class, geometry)
	go func() {
		defer close(direction.done)
		if direction.permit != nil {
			defer direction.permit.Release()
		}
		copied, err := bufio.CopyWithIncreateBuffer(direction.writer, direction.reader, direction.threshold, bufio.DefaultBatchSize)
		direction.copied = copied
		direction.err = err
	}()
	return direction
}

// finish opens the direction's gate exactly once, so its reader can drain and the copy can
// return. It is idempotent because a test may finish a direction that never consumed it.
func (d *relayDirection) finish() {
	if d.gate == nil {
		return
	}
	d.gateOnce.Do(func() { close(d.gate) })
}

func (d *relayDirection) wait(t *testing.T) {
	t.Helper()
	select {
	case <-d.done:
	case <-time.After(testWait):
		t.Fatal("the copy direction never returned")
	}
}

// TestRelayDirectionsHoldTheirPermitsForTheCopy pins the holding rule: both directions of
// a relay are admitted, a third is refused but still copies, and a permit comes back when
// ITS copy ends - not when some external gate closes.
func TestRelayDirectionsHoldTheirPermitsForTheCopy(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 2}, nil)
	first := startRelayDirection(budget, trafficclass.ClassDefault, false, 8*testChunk, make(chan struct{}), nil)
	second := startRelayDirection(budget, trafficclass.ClassDefault, false, 8*testChunk, make(chan struct{}), nil)
	defer func() { first.finish(); second.finish() }()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 2 }, testWait, testPoll)

	// A third direction is refused, and that is a memory choice: it still copies every byte.
	third := startRelayDirection(budget, trafficclass.ClassDefault, false, 1024, nil, nil)
	third.wait(t)
	require.NoError(t, third.err)
	require.Equal(t, int64(1024), third.copied)
	require.Nil(t, third.permit)
	require.Equal(t, CopyGrowthNever, third.threshold)
	require.Equal(t, 2, budget.Stats().InUse, "a refused direction must not have taken a permit")

	// Half-close: the first direction's read side ends. Its permit returns when its copy
	// returns, and the second direction keeps copying and holding its own.
	first.finish()
	first.wait(t)
	require.NoError(t, first.err)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 1 }, testWait, testPoll)
	stats := budget.Stats()
	require.Equal(t, uint64(2), stats.Granted, "only the two admitted directions were granted")
	require.Equal(t, uint64(1), stats.Released)
	require.Equal(t, 1, stats.InUse)
	require.Equal(t, CopyGrowthDefault, second.threshold)
	select {
	case <-second.done:
		t.Fatal("the other direction ended when the first half-closed")
	default:
	}

	// With one permit free, a new direction is admitted while the second still copies.
	fourth := startRelayDirection(budget, trafficclass.ClassDefault, false, 1024, nil, nil)
	fourth.wait(t)
	require.NotNil(t, fourth.permit)
	require.Equal(t, int64(1024), fourth.copied)

	second.finish()
	second.wait(t)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll)
	final := budget.Stats()
	require.Zero(t, final.DoubleReleases)
	require.Equal(t, final.Granted, final.Released)
}

// TestRelayCancelledDirectionReleasesOnTheCopyReturn pins the error path: a cancelled
// direction returns its permit when the copy returns, exactly once, and the other
// direction is unaffected.
func TestRelayCancelledDirectionReleasesOnTheCopyReturn(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 2}, nil)
	survivorGate := make(chan struct{})
	survivor := startRelayDirection(budget, trafficclass.ClassDefault, false, 8*testChunk, survivorGate, nil)
	defer survivor.finish()
	cancelled := startRelayDirection(budget, trafficclass.ClassDefault, false, 1024, make(chan struct{}), errors.New("relay cancelled"))
	require.Eventually(t, func() bool { return budget.Stats().InUse == 2 }, testWait, testPoll)

	cancelled.finish()
	cancelled.wait(t)
	require.Error(t, cancelled.err)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 1 }, testWait, testPoll)
	select {
	case <-survivor.done:
		t.Fatal("the surviving direction ended when the other was cancelled")
	default:
	}

	// The cancelled direction's permit is gone, so a new one is admitted immediately.
	replacement := startRelayDirection(budget, trafficclass.ClassDefault, false, 1024, nil, nil)
	replacement.wait(t)
	require.NotNil(t, replacement.permit)

	survivor.finish()
	survivor.wait(t)
	require.NoError(t, survivor.err)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll)
	require.Zero(t, budget.Stats().DoubleReleases)
}

// TestRelayPressureRecoveryAdmitsAfterTheLevelDrops pins the recovery half of the P2 fix:
// a downgrade refuses growth above the current total, only the high-priority reserve is
// left for interactive directions, already-grown directions drain, and after the level
// returns to Normal the pool admits again.
func TestRelayPressureRecoveryAdmitsAfterTheLevelDrops(t *testing.T) {
	t.Parallel()

	var levelAccess sync.Mutex
	level := LevelNormal
	setLevel := func(next Level) { levelAccess.Lock(); level = next; levelAccess.Unlock() }
	pressure := func() Level { levelAccess.Lock(); defer levelAccess.Unlock(); return level }
	budget := NewGrowthBudget(GrowthConfig{Flows: 4, InteractiveReserve: 2}, pressure)

	first := startRelayDirection(budget, trafficclass.ClassDefault, false, 8*testChunk, make(chan struct{}), nil)
	second := startRelayDirection(budget, trafficclass.ClassDefault, false, 8*testChunk, make(chan struct{}), nil)
	defer func() { first.finish(); second.finish() }()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 2 }, testWait, testPoll)

	// Armed halves the ordinary cap to 2 and the reserve to 1. With two ordinary permits in
	// use, the ordinary cap is reached, so only a high-priority direction can take the
	// reserve - and the total (2+1) is the hard ceiling.
	setLevel(LevelArmed)
	interactive := startRelayDirection(budget, trafficclass.ClassInteractive, false, 8*testChunk, make(chan struct{}), nil)
	defer interactive.finish()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 3 && budget.Stats().ReservedInUse == 1 },
		testWait, testPoll, "Armed 2+1 must admit one interactive direction via the reserve")

	fourth := startRelayDirection(budget, trafficclass.ClassDefault, false, 1024, nil, nil)
	fourth.wait(t)
	require.Nil(t, fourth.permit, "Armed 2+1 with three in use must refuse a fourth ordinary direction")
	require.Equal(t, CopyGrowthNever, fourth.threshold)
	second5 := startRelayDirection(budget, trafficclass.ClassInteractive, false, 1024, nil, nil)
	second5.wait(t)
	require.Nil(t, second5.permit, "the Armed reserve is one permit, not two")

	// The interactive direction ends: the reserve returns and the next interactive
	// direction fits the Armed total again.
	interactive.finish()
	interactive.wait(t)
	require.Eventually(t, func() bool {
		stats := budget.Stats()
		return stats.InUse == 2 && stats.ReservedInUse == 0
	}, testWait, testPoll)
	replacement := startRelayDirection(budget, trafficclass.ClassInteractive, false, 1024, nil, nil)
	replacement.wait(t)
	require.NotNil(t, replacement.permit, "the freed reserve must admit an interactive direction")

	// Critical refuses everything new, including a high-priority direction.
	setLevel(LevelCritical)
	refused := startRelayDirection(budget, trafficclass.ClassInteractive, false, 1024, nil, nil)
	refused.wait(t)
	require.Nil(t, refused.permit)
	require.Equal(t, CopyGrowthNever, refused.threshold)

	// The old copies end, and with the level back at Normal admission resumes.
	first.finish()
	second.finish()
	first.wait(t)
	second.wait(t)
	setLevel(LevelNormal)
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 }, testWait, testPoll)
	recovered := startRelayDirection(budget, trafficclass.ClassDefault, false, 1024, nil, nil)
	recovered.wait(t)
	require.NotNil(t, recovered.permit)
	require.Equal(t, CopyGrowthDefault, recovered.threshold)
}

// TestRelayGeometryDirectionGrowsWithoutAPermit pins the exemption: a direction whose
// writer needs the larger buffer grows from its first read, is counted for diagnosis, and
// takes no permit from the pool.
func TestRelayGeometryDirectionGrowsWithoutAPermit(t *testing.T) {
	t.Parallel()

	budget := NewGrowthBudget(GrowthConfig{Flows: 1}, nil)
	geometry := startRelayDirection(budget, trafficclass.ClassDefault, true, 4*buf.BufferSize, nil, nil)
	geometry.wait(t)
	require.NoError(t, geometry.err)
	require.Equal(t, int64(4*buf.BufferSize), geometry.copied)
	require.Nil(t, geometry.permit)
	require.Equal(t, CopyGrowthImmediate, geometry.threshold)
	require.Greater(t, geometry.reader.grownReads(), 0, "the geometry direction must actually grow")
	require.Equal(t, uint64(1), budget.Stats().GeometryNeeded)
	require.Zero(t, budget.Stats().InUse, "a geometry direction must not charge the shared cap")
}
