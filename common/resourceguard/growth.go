package resourceguard

import (
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/bufio"
)

// The relay copy-growth budget.
//
// # What it bounds
//
// route/conn.go copies each direction of a proxied stream with
// bufio.CopyWithIncreateBuffer, and sing grows the read buffer once the direction
// has copied IncreaseBufferAfter bytes: from buf.BufferSize to the MTU-sized (or
// 65535-byte) buffer. The threshold is chosen per flow at copy start
// (connectionIncreaseBufferAfter: 1 for a writer that opted in for geometry
// reasons, otherwise bufio.DefaultIncreaseBufferAfter), and NOTHING bounds how
// many flows are grown at the same time. Every grown flow retains roughly one
// larger read buffer for as long as it is copying, so N concurrent bulk streams
// cost N enlarged buffers: a desktop with a few hundred streams, or a phone
// pushing a sync and a backup at once, pays it without any policy seeing the sum.
//
// GrowthBudget is that missing sum. It hands out one permit per copy DIRECTION
// (the two directions of a connection are independent in the copy engine, and
// each has its own buffer, so they are counted separately) and returns a
// threshold for that direction:
//
//	permit granted    the direction grows exactly as it did before
//	                  (bufio.DefaultIncreaseBufferAfter, or 1 for an opted-in
//	                  writer)
//	permit refused    the direction does not grow at all (threshold 0, which
//	                  sing reads as "never grow")
//
// A refused direction still copies correctly; it just keeps the small buffer.
// That is the reason this may deny growth at all: it is a memory choice, not a
// correctness one.
//
// # Why the permit is held for the whole copy
//
// sing's latch is one-shot and inside the copy loop: once the threshold is
// crossed the direction uses the larger buffer until it ends, and the route layer
// has no callback to ask again. A "lease" that expired would therefore free the
// count while the memory stayed grown, which is a bound that lies. The permit is
// released when the direction's copy returns (the route wiring releases it in
// connectionCopy's defer), so permits live exactly as long as the enlarged buffers
// do.
//
// # Fairness: the interactive reserve
//
// A long bulk stream holds its permit until it ends, so without a reserve a burst
// of bulk flows would take every permit and later, latency-sensitive flows would
// keep the small buffer even though their whole transfer is a few hundred KB. The
// reserve is a small number of permits beyond the ordinary cap that only
// high-priority classes (interactive, realtime) may use. It is the same shape as
// Guard's interactive reserve, for the same reason, and it is bounded in the same
// way: a reserve is a priority, not a second unlimited pool.
//
// Short flows are otherwise unaffected: a transfer that ends before the threshold
// never grows a buffer, so the cap neither helps nor costs it. A refused flow's
// permit is refused at the start of its copy - there is no queue and no wait,
// because delaying a relay's data path to decide about a buffer would trade a
// measurable latency cost for an unmeasurable memory gain.
//
// # Pressure
//
// The capacity is derived from the process pressure level, read through the same
// function the Guard uses (on iOS, the oomkiller service's published state), so
// the two policies cannot disagree about the level. The derivation is monotone and
// clamped to the level above, exactly like Config.planFor: higher pressure never
// admits more. Existing permits are NOT revoked when pressure rises, because the
// buffer is already grown and revoking the count would only make the bound
// untrue; a rising level stops NEW growth, and the already-grown directions shed
// when their copies end.
//
// # Wiring (owned by the network window)
//
// This type is written and tested here; route/conn.go is the network window's file,
// so the production wiring is theirs. The intended wiring, one call per direction,
// is:
//
//	// before starting connectionCopy for a direction:
//	threshold, permit := m.growth.Admit(metadata.TrafficClass, base == earlyConnectionBufferIncreaseAfter)
//	if permit != nil {
//		defer permit.Release()
//	}
//	// connectionCopy then uses threshold instead of base
//
// where base is the existing connectionIncreaseBufferAfter call for that
// direction. A nil *GrowthBudget keeps today's thresholds (see Admit), so a Box
// without a budget behaves exactly as before.
type GrowthBudget struct {
	config   GrowthConfig
	pressure func() Level

	access sync.Mutex
	// used counts granted ordinary permits; reservedUsed counts the part of used
	// that came from the interactive reserve.
	used         int
	reservedUsed int
	peak         int
	granted      uint64
	released     uint64
	refused      uint64
	geometry     uint64
	// doubleReleases counts a permit released twice. It is a defect counter, not a
	// policy state, and must stay zero.
	doubleReleases uint64
}

// GrowthConfig is the growth policy at LevelNormal. Other levels are derived from it
// by growthPlanFor, so a change here moves every level together.
type GrowthConfig struct {
	// Flows is how many copy directions may hold an enlarged buffer at LevelNormal.
	// It is the only number an operator would tune, and it is expressed in flows
	// because that is what the policy can see: one buffer per direction. Zero (or
	// less) disables ordinary growth entirely; a nil *GrowthBudget, not a zero
	// config, is how "keep the old behaviour" is spelled.
	Flows int
	// InteractiveReserve is how many permits beyond Flows high-priority classes may
	// use, so a bulk burst cannot keep an interactive flow on the small buffer.
	InteractiveReserve int
}

// DefaultGrowthConfig is the policy until the exclusive performance window says
// otherwise.
//
// 256 concurrent grown directions is deliberately far above a steady state (a
// phone or desktop proxies a handful of bulk streams at once) and below the burst
// that motivated this work (hundreds of flows from one sync). At ~64 KiB per grown
// direction the cap is ~16 MiB, which is the number this policy is actually
// about; it is a bound on top of the per-flow behaviour, not a replacement for it.
//
// These are POLICY defaults, not measurements. Choosing them by feel is exactly
// what the stage-03 record refused to do for the relay path, which is why the
// production wiring is gated on the exclusive benchmark window recording grown
// flows, throughput and tail latency at the same completed bytes.
func DefaultGrowthConfig() GrowthConfig {
	return GrowthConfig{
		Flows:              256,
		InteractiveReserve: 16,
	}
}

// growthPlan is the derived capacity for one pressure level.
type growthPlan struct {
	flows   int
	reserve int
}

// growthPlanFor derives the capacity for one level.
//
// The derivation mirrors Config.planFor: half at Armed, none at Critical, with
// every derived value clamped to the level above so the function cannot admit
// more under higher pressure. Critical is a hard zero rather than a quarter
// because the point of that level is that the process cannot afford another grown
// buffer at all.
func (c GrowthConfig) growthPlanFor(level Level) growthPlan {
	normal := growthPlan{
		flows:   max(c.Flows, 0),
		reserve: max(min(c.InteractiveReserve, max(c.Flows, 0)), 0),
	}
	if normal.flows == 0 {
		normal.reserve = 0
	}
	switch level {
	case LevelCritical:
		return growthPlan{}
	case LevelArmed:
		return growthPlan{
			flows:   min(max(normal.flows/2, 1), normal.flows),
			reserve: min(max(normal.reserve/2, 0), normal.reserve),
		}
	default:
		return normal
	}
}

// NewGrowthBudget builds a growth policy. pressure may be nil, which means the
// process is always at LevelNormal; in production pass the Guard's Level method
// (or the same source it reads) so the two policies share one pressure signal.
func NewGrowthBudget(config GrowthConfig, pressure func() Level) *GrowthBudget {
	return &GrowthBudget{
		config:   config,
		pressure: pressure,
	}
}

// Copy growth thresholds, mirroring what sing's copy loop understands.
const (
	// CopyGrowthNever is the threshold that means "do not enlarge the buffer":
	// sing's condition is `IncreaseBufferAfter > 0 && n >= IncreaseBufferAfter`.
	CopyGrowthNever int64 = 0
	// CopyGrowthImmediate grows after the first transfer instead of after
	// bufio.DefaultIncreaseBufferAfter. It is what a writer with a geometry reason
	// asks for through adapter.CopyBufferGrowthTuner.
	CopyGrowthImmediate int64 = 1
	// CopyGrowthDefault is sing's threshold for an ordinary connection.
	CopyGrowthDefault int64 = bufio.DefaultIncreaseBufferAfter
)

// Admit decides whether one copy direction may use an enlarged buffer and returns
// the threshold to hand to bufio.CopyWithIncreateBuffer, plus the permit to release
// when the direction ends.
//
// geometryRequired marks a direction whose writer (or outbound) asked for early
// growth because its framing needs the larger buffer, through
// adapter.CopyBufferGrowthTuner or adapter.ConnectionCopyTuner. That is a property
// of the writer, not a policy choice, so such a direction is always allowed and
// keeps CopyGrowthImmediate; it is counted for diagnosis but not charged against
// the shared cap. Refusing it would change a protocol's padding behaviour to save
// memory its owner already accounted for.
//
// A nil *GrowthBudget keeps the pre-existing thresholds and returns no permit, so
// a Box without a budget behaves exactly as before it existed.
func (b *GrowthBudget) Admit(class trafficclass.Class, geometryRequired bool) (int64, *GrowthPermit) {
	if geometryRequired {
		if b != nil {
			b.access.Lock()
			b.geometry++
			b.access.Unlock()
		}
		return CopyGrowthImmediate, nil
	}
	if b == nil {
		return CopyGrowthDefault, nil
	}
	level := LevelNormal
	if b.pressure != nil {
		level = b.pressure()
	}
	plan := b.config.growthPlanFor(level)
	highPriority := class.IsHighPriority()

	b.access.Lock()
	defer b.access.Unlock()
	reserved := false
	// A downgrade never grants above the CURRENT level's total. Permits that were
	// handed out under a looser level stay in use and drain as their copies end, but
	// while InUse is at or above flows+reserve every new direction is refused, including
	// a high-priority one - otherwise arming the guard would still add occupancy on top
	// of a cap it just lowered. Checking only reservedUsed missed exactly that case:
	// Flows=4/InteractiveReserve=2 at Normal, four permits out, Armed is 2+1, and an
	// interactive direction took the fifth.
	if b.used >= plan.flows+plan.reserve {
		b.refused++
		return CopyGrowthNever, nil
	}
	if b.used >= plan.flows {
		// The ordinary cap is reached. A high-priority flow may still draw on the
		// reserve up to the current total; anything else keeps the small buffer.
		if !highPriority || b.reservedUsed >= plan.reserve {
			b.refused++
			return CopyGrowthNever, nil
		}
		reserved = true
		b.reservedUsed++
	}
	b.used++
	b.granted++
	if b.used > b.peak {
		b.peak = b.used
	}
	return CopyGrowthDefault, &GrowthPermit{budget: b, reserved: reserved}
}

// release returns one permit to the pool.
func (b *GrowthBudget) release(reserved bool) {
	b.access.Lock()
	b.used--
	if reserved {
		b.reservedUsed--
	}
	if b.used < 0 {
		// Defensive: an over-release is a bookkeeping defect, not a policy state.
		b.used = 0
		b.reservedUsed = 0
	}
	b.released++
	b.access.Unlock()
}

// noteDoubleRelease counts a permit released twice. The pool is not touched: the
// second release is the defect, and applying it would let one direction hold a
// slot it no longer owns.
func (b *GrowthBudget) noteDoubleRelease() {
	b.access.Lock()
	b.doubleReleases++
	b.access.Unlock()
}

// GrowthPermit is one direction's claim on an enlarged copy buffer. Release it when
// that direction's copy returns; Release is idempotent and nil-safe.
type GrowthPermit struct {
	budget   *GrowthBudget
	reserved bool
	released atomic.Bool
}

// Release returns the permit to its budget. Calling it more than once is counted
// as a defect and has no other effect.
func (p *GrowthPermit) Release() {
	if p == nil || p.budget == nil {
		return
	}
	if !p.released.CompareAndSwap(false, true) {
		p.budget.noteDoubleRelease()
		return
	}
	p.budget.release(p.reserved)
}

// GrowthStats is a consistent snapshot of the growth policy.
//
// The identities a reader can rely on:
//
//	InUse            == Granted - Released, and never above the current cap
//	ReservedInUse    <= min(Reserve, InUse)
//	DoubleReleases   == 0
type GrowthStats struct {
	Level          Level
	Flows          int
	Reserve        int
	InUse          int
	ReservedInUse  int
	Peak           int
	Granted        uint64
	Released       uint64
	Refused        uint64
	GeometryNeeded uint64
	// DoubleReleases is a defect counter and must be zero.
	DoubleReleases uint64
}

// Stats snapshots the policy at the current pressure level.
func (b *GrowthBudget) Stats() GrowthStats {
	if b == nil {
		return GrowthStats{}
	}
	level := LevelNormal
	if b.pressure != nil {
		level = b.pressure()
	}
	plan := b.config.growthPlanFor(level)
	b.access.Lock()
	defer b.access.Unlock()
	return GrowthStats{
		Level:          level,
		Flows:          plan.flows,
		Reserve:        plan.reserve,
		InUse:          b.used,
		ReservedInUse:  b.reservedUsed,
		Peak:           b.peak,
		Granted:        b.granted,
		Released:       b.released,
		Refused:        b.refused,
		GeometryNeeded: b.geometry,
		DoubleReleases: b.doubleReleases,
	}
}
