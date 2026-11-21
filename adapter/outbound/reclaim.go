package outbound

// Reclamation of deferred expansions: WHO releases an expanded node, WHEN, and under WHAT budget.
//
// # The problem this file exists for
//
// A deferred node costs ~0.7 KiB while it is only a recipe and ~3.6 KiB once it is expanded (MEASURED
// on this tree, see the report). That is the whole point of deferring - until something dials every
// node. A full URLTest round over a 1000-node subscription expands all 1000, and without eviction the
// prototype is then 41% MORE expensive than the eager construction it replaced. Deferring without
// reclaiming is not a memory optimisation, it is a leak with a delayed invoice.
//
// # Who
//
// The MANAGER, because it already owns the one namespace of published objects and it is the only
// component that can see all of them (Outbounds). The individual node never decides anything: it
// answers a read-only observation and it accepts an Evict. That split is deliberate - a policy spread
// over N objects is N chances to disagree with itself, and a node cannot see its siblings.
//
// # When
//
// Three triggers, all of them EXISTING signals. No new sampler, no new timer, no ticker:
//
//  1. The existing idle walk. route/reference.go's retireIdleResources already walks every outbound
//     for DEEP_IDLE, the memory pass and the resume boundary and asks for CloseIdleConnections; a
//     deferred node reports that walk to its owner (LazyOutbound.NotifyIdleWalk), and the owner may
//     sweep. Debounced by MinInterval because the walk visits every object.
//  2. Demand. An expansion that pushes the live count past the budget asks for one sweep. That is
//     what bounds a full probe round WHILE it runs instead of after the fact, and it needs no clock.
//  3. Explicitly, ReclaimDeferredExpansions, for a product that knows a round just finished.
//
// # What budget
//
// Two upper bounds, both enforced by one sweep: a COUNT (MaxExpanded) and an ESTIMATED BYTE total
// (MaxBytes). The count bounds the number of round trips a later idle walk can make; the byte total
// bounds what a heavier protocol may keep per node. Both shrink under pressure - with the SAME
// resourceguard level the admission gate and the build gate already read, not a second measurement.
//
// # What is never evicted
//
// An expansion that anything is using: a manager hand-off lease, a dial in flight, or an open
// connection. Evict makes that decision under the object's own lock (see LazyOutbound.Evict), so the
// policy cannot get it wrong; the sweep can only choose among nodes whose observation says Idle.
//
// Eviction is always LRU: the node that has gone unused longest is the one least likely to be dialled
// next, and a node that is being used is excluded before ordering matters.

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
)

// ReclaimCause names why one sweep ran. It is reported, never interpreted: a policy change must not
// have to reverse-engineer which caller it is serving.
type ReclaimCause uint8

const (
	// ReclaimCauseIdleWalk: the existing idle walk reached a deferred node.
	ReclaimCauseIdleWalk ReclaimCause = iota
	// ReclaimCauseBudget: an expansion pushed the live count past the budget.
	ReclaimCauseBudget
	// ReclaimCauseExplicit: a caller asked for a sweep.
	ReclaimCauseExplicit
)

func (c ReclaimCause) String() string {
	switch c {
	case ReclaimCauseIdleWalk:
		return "idle-walk"
	case ReclaimCauseBudget:
		return "budget"
	case ReclaimCauseExplicit:
		return "explicit"
	default:
		return "unknown"
	}
}

// DeferredExpansion is the OPTIONAL capability a deferred object exposes to the reclamation policy.
//
// It is the smallest surface that can be swept: a read-only observation and the release verb the
// object already has. Anything else - which node to pick, how many, under what pressure - belongs to
// the owner, so a protocol implements two methods and gets the whole policy.
type DeferredExpansion interface {
	adapter.Outbound
	// ExpansionObservation reports the current residency. It must never build, dial or block on
	// another goroutine: a sweep runs it for every published object, and a management query may run
	// it too.
	ExpansionObservation() ExpansionObservation
	// Evict releases the expansion if nothing is using it, and reports whether it did.
	Evict() bool
}

// DeferredReclaimPolicy is the budget and the cadence of the sweep.
type DeferredReclaimPolicy struct {
	// MaxExpanded is the hard cap on simultaneously expanded deferred nodes. Zero disables the cap
	// (not the sweep); a negative value disables reclamation entirely (see
	// Manager.DisableDeferredReclaim).
	MaxExpanded int
	// MaxBytes is the estimated total the expansions may hold. The estimate per node comes from its
	// own observation, or from AssumedExpansionBytes when the protocol does not estimate.
	MaxBytes int64
	// AssumedExpansionBytes is used when an observation reports no estimate. It is the MEASURED cost
	// of an expanded node of the only protocol this tree currently admits, and it is an assumption
	// for any other one - a protocol whose expansion is much heavier must report its own.
	AssumedExpansionBytes int64
	// MaxEvictionsPerSweep bounds one sweep's teardown work. Each eviction runs the protocol's
	// Start-registered cleanups inline, so an unbounded sweep would be an unbounded pause. MEASURED:
	// 5.2 us per eviction on this tree (936 evictions of a 1000-node probe in 4.9 ms), so 256 puts a
	// hard ceiling of ~1.4 ms on one sweep while still letting a fully expanded 1000-node library
	// converge in four sweeps instead of thirty. The demand trigger is asynchronous, so this ceiling
	// is CPU on one goroutine, never a stall in a dial.
	MaxEvictionsPerSweep int
	// MinInterval debounces the walk-triggered sweep: the existing idle walk asks every outbound, so
	// without it one walk over a 1000-node namespace would ask for 1000 sweeps. The demand trigger is
	// not debounced by time - it is bounded by MaxEvictionsPerSweep and by single-flight instead.
	MinInterval time.Duration
}

// DefaultDeferredReclaimPolicy is the budget the manager installs together with the deferred seam.
//
// # Where the numbers come from
//
// MEASURED on this tree (10 000 never-dialled Trojan nodes, real Manager): a never-dialled deferred
// node holds 698 B and an expanded one holds 3 603 B, so one expansion costs about 2.9 KiB. The
// per-node estimate below is that expansion cost rounded up (3 KiB); the never-dialled floor is not
// reclaimable and is therefore not in the budget.
//
//   - MaxExpanded = 64: a session's real working set is a handful of nodes, so 64 is far above the
//     steady state and still bounds a full probe round of an arbitrarily large subscription to
//     64 x 3 KiB ~= 192 KiB, against 2.9 MiB for the same round unreclaimed (1000 nodes) and 29 MiB
//     for 10 000. Above 64 the extra memory buys nothing a rebuild (~3 us) would not restore.
//   - MaxBytes = 256 KiB: it does not bind for Trojan (64 x 3 KiB), and it is what bounds a protocol
//     whose expansion is heavier - a node with a pooled transport is 5-10x that size, and this cap
//     then decides the count instead of the count deciding the bytes.
//   - MaxEvictionsPerSweep = 256: MEASURED at 5.2 us per eviction (a 1000-node probe's 936
//     evictions took 4.9 ms), so one sweep costs at most ~1.4 ms of one goroutine - the demand trigger
//     runs off the dial path and the walk trigger runs on the timer's goroutine. Four sweeps converge
//     a library that a probe expanded completely.
//   - MinInterval = 30 s: the existing idle policy's own cooldown between idle passes
//     (service/oomkiller defaultIdleCooldown). Matching it means a deferred node cannot be swept by
//     the walk more often than the walk itself is allowed to run.
func DefaultDeferredReclaimPolicy() DeferredReclaimPolicy {
	return DeferredReclaimPolicy{
		MaxExpanded:           64,
		MaxBytes:              256 * 1024,
		AssumedExpansionBytes: 3 * 1024,
		MaxEvictionsPerSweep:  256,
		MinInterval:           30 * time.Second,
	}
}

// pressurized returns the policy as it applies at one pressure level. The levels are resourceguard's,
// i.e. the same ones the admission gate and the build reservation tighten with: at Armed the budget
// halves, at Critical it drops to an eighth, always keeping a floor of two so the node a flow is
// about to use (and its detour) is not released on every pressure transition.
//
// # Why the floor is clamped to the configured budget
//
// A floor is a LOWER bound on how far pressure may shrink a budget; it is not permission to grow one.
// Written without the clamp, a budget of one expansion became TWO under Critical pressure (max(1/8,2)),
// and a byte budget below two expansions' worth was raised the same way - i.e. a pressure event made
// the process hold more than the operator configured, which is the exact opposite of what the level is
// for. The floor therefore takes the configured value as its ceiling, and the byte floor is computed
// only when it cannot overflow (AssumedExpansionBytes is an int64 an embedder supplies).
func (p DeferredReclaimPolicy) pressurized(level resourceguard.Level) DeferredReclaimPolicy {
	if level == resourceguard.LevelNormal {
		return p
	}
	divisor := 2
	if level == resourceguard.LevelCritical {
		divisor = 8
	}
	if p.MaxExpanded > 0 {
		floor := 2
		if p.MaxExpanded < floor {
			floor = p.MaxExpanded
		}
		p.MaxExpanded = max(p.MaxExpanded/divisor, floor)
	}
	if p.MaxBytes > 0 {
		// Two expansions' worth, without multiplying past MaxInt64 and never above what was configured.
		floor := p.MaxBytes
		if p.AssumedExpansionBytes > 0 && p.AssumedExpansionBytes <= p.MaxBytes/2 {
			floor = p.AssumedExpansionBytes * 2
		}
		p.MaxBytes = max(p.MaxBytes/int64(divisor), floor)
	}
	return p
}

// ReclaimResult is one sweep's outcome. It is a report, not a promise: Expanded is what the sweep
// observed before acting, and Evicted is how many the object layer actually released.
type ReclaimResult struct {
	Cause          ReclaimCause
	Level          resourceguard.Level
	Expanded       int
	Target         int
	EstimatedBytes int64
	BudgetBytes    int64
	Evicted        int
	ReleasedBytes  int64
	// InUse is how many expanded nodes the sweep would not touch because something was using them
	// (a lease, a dial, or an open connection).
	InUse int
	// DeferredByBudget is how many candidates were left for a later sweep because this one hit
	// MaxEvictionsPerSweep.
	DeferredByBudget int
}

// deferredReclaimState is the manager's own half of the policy: the settings, the single-flight flag
// that keeps one sweep in the air, and the debounce clock. It is a separate struct so the Manager's
// hot fields are not padded by policy state.
type deferredReclaimState struct {
	access    sync.Mutex
	enabled   bool
	policy    DeferredReclaimPolicy
	pressure  func(context.Context) resourceguard.Level
	lastSweep time.Time
	sweeping  bool
	// pending is the number of expansions the objects have reported and not yet reported releasing.
	// It is a TRIGGER only - the sweep counts the published objects itself - but it is kept exact by
	// construction (one increment per publish, one decrement per release), so the demand trigger fires
	// as soon as the live count passes the budget AND falls silent as soon as it drops back under it.
	pending atomic.Int64
}

// SetDeferredReclaimPolicy replaces the budget. It does not sweep by itself: a policy change takes
// effect on the next trigger, which keeps "changing a setting" from doing teardown work inline.
func (m *Manager) SetDeferredReclaimPolicy(policy DeferredReclaimPolicy) {
	if m == nil {
		return
	}
	state := &m.deferredReclaim
	state.access.Lock()
	state.policy = policy
	state.enabled = policy.MaxExpanded >= 0
	state.access.Unlock()
}

// DisableDeferredReclaim stops all sweeps. The deferred seam keeps working: nodes are still built on
// demand, they are just never released automatically. It exists for an embedder that wants the object
// layer without a policy, and for tests that must observe a set of expansions stay put.
func (m *Manager) DisableDeferredReclaim() {
	if m == nil {
		return
	}
	state := &m.deferredReclaim
	state.access.Lock()
	state.enabled = false
	state.access.Unlock()
}

// SetDeferredReclaimPressure installs the pressure source. It must be the process's ONE pressure
// fact - resourceguard.Guard.Level, which service/oomkiller feeds from the timer that already samples
// the footprint - and not a second measurement: a policy that samples for itself can disagree with
// the reclaim path about what state the process is in.
//
// A nil source restores the default, which reads resourceguard.FromContext from the context the
// trigger carried (the deferred node's construction context, i.e. the Box context the guard was
// registered on).
func (m *Manager) SetDeferredReclaimPressure(pressure func(context.Context) resourceguard.Level) {
	if m == nil {
		return
	}
	state := &m.deferredReclaim
	state.access.Lock()
	state.pressure = pressure
	state.access.Unlock()
}

// defaultDeferredReclaimPressure reads the guard the pressure policy owner registered. No guard means
// no pressure policy, which is resourceguard.LevelNormal - the same mapping the build gate uses.
func defaultDeferredReclaimPressure(ctx context.Context) resourceguard.Level {
	if ctx == nil {
		return resourceguard.LevelNormal
	}
	guard := resourceguard.FromContext(ctx)
	if guard == nil {
		return resourceguard.LevelNormal
	}
	return guard.Level()
}

// DeferredReclaimEnabled reports whether sweeps are armed. It is a read-only query: it builds
// nothing and takes only the policy lock.
func (m *Manager) DeferredReclaimEnabled() bool {
	if m == nil {
		return false
	}
	state := &m.deferredReclaim
	state.access.Lock()
	enabled := state.enabled
	state.access.Unlock()
	return enabled
}

// DeferredExpansionCount reports how many published nodes are expanded right now. It is a pure
// query over the published namespace: it takes no build, no dial and no eviction, and a never-built
// node is not counted.
func (m *Manager) DeferredExpansionCount() int {
	if m == nil {
		return 0
	}
	expanded := 0
	for _, object := range m.Outbounds() {
		deferred, isDeferred := object.(DeferredExpansion)
		if !isDeferred {
			continue
		}
		if deferred.ExpansionObservation().Expanded {
			expanded++
		}
	}
	return expanded
}

// NoteExpansionPublished is called by a deferred object after it publishes an expansion. It is on the
// dial path, so it does one atomic add and (only when the trigger fires) one goroutine hand-off.
//
// The count is incremented BEFORE the enabled check, because NoteExpansionReleased decrements
// unconditionally: a policy that is disarmed and re-armed must not inherit a count that is missing the
// publishes made while it was disarmed. That drift made the demand trigger fall silent exactly when a
// process was already over budget - only the 30-second idle walk would have noticed. Keeping the
// increment unconditional costs one atomic add and makes the pair exact by construction.
func (m *Manager) NoteExpansionPublished(ctx context.Context) {
	if m == nil {
		return
	}
	state := &m.deferredReclaim
	pending := state.pending.Add(1)
	state.access.Lock()
	enabled := state.enabled
	policy := state.policy
	state.access.Unlock()
	if !enabled {
		return
	}
	if policy.MaxExpanded <= 0 || int(pending) <= policy.MaxExpanded {
		return
	}
	m.RequestDeferredReclaim(ctx, ReclaimCauseBudget)
}

// NoteExpansionReleased is called by a deferred object after it released an expansion, so the demand
// trigger does not drift upward as nodes are evicted.
func (m *Manager) NoteExpansionReleased() {
	if m == nil {
		return
	}
	m.deferredReclaim.pending.Add(-1)
}

// RequestDeferredReclaim asks for one sweep without waiting for it. Single-flight: a request that
// arrives while a sweep is running is dropped, because the running sweep is already reading the same
// namespace and its result is newer than any decision this request could make.
func (m *Manager) RequestDeferredReclaim(ctx context.Context, cause ReclaimCause) {
	if m == nil {
		return
	}
	state := &m.deferredReclaim
	state.access.Lock()
	if !state.enabled || state.sweeping {
		state.access.Unlock()
		return
	}
	if cause == ReclaimCauseIdleWalk && !state.lastSweep.IsZero() && time.Since(state.lastSweep) < state.policy.MinInterval {
		state.access.Unlock()
		return
	}
	state.sweeping = true
	state.access.Unlock()
	go func() {
		defer func() {
			state.access.Lock()
			state.sweeping = false
			state.lastSweep = time.Now()
			state.access.Unlock()
		}()
		m.ReclaimDeferredExpansions(ctx, cause)
	}()
}

// ReclaimDeferredExpansions runs one sweep synchronously and reports what it did.
//
// It is safe to call from any goroutine, including while traffic flows: every eviction decision is
// made by the object under its own lock, and a node in use is refused there. It never builds, never
// dials and never publishes.
func (m *Manager) ReclaimDeferredExpansions(ctx context.Context, cause ReclaimCause) ReclaimResult {
	result := ReclaimResult{Cause: cause}
	if m == nil {
		return result
	}
	state := &m.deferredReclaim
	state.access.Lock()
	enabled := state.enabled
	policy := state.policy
	pressure := state.pressure
	state.access.Unlock()
	if !enabled {
		return result
	}
	if pressure != nil {
		result.Level = pressure(ctx)
	} else {
		result.Level = defaultDeferredReclaimPressure(ctx)
	}
	policy = policy.pressurized(result.Level)
	result.Target = policy.MaxExpanded
	result.BudgetBytes = policy.MaxBytes

	// The victim list is built from ONE snapshot of the published namespace: an object removed or
	// replaced during the sweep is simply no longer there when its turn comes, and Evict refuses.
	type candidate struct {
		object    DeferredExpansion
		estimated int64
		idleFor   time.Duration
		sequence  int
	}
	var candidates []candidate
	estimatedTotal := int64(0)
	for sequence, object := range m.Outbounds() {
		deferred, isDeferred := object.(DeferredExpansion)
		if !isDeferred {
			continue
		}
		observation := deferred.ExpansionObservation()
		if !observation.Expanded {
			continue
		}
		result.Expanded++
		estimated := observation.EstimatedBytes
		if estimated <= 0 {
			estimated = policy.AssumedExpansionBytes
		}
		estimatedTotal += estimated
		if !observation.Idle() {
			result.InUse++
			continue
		}
		candidates = append(candidates, candidate{
			object:    deferred,
			estimated: estimated,
			idleFor:   observation.IdleFor,
			sequence:  sequence,
		})
	}
	result.EstimatedBytes = estimatedTotal
	// Least recently used first; the sequence is the published order and only breaks ties, so a
	// sweep over a corpus measured in the same instant is still deterministic.
	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].idleFor != candidates[j].idleFor {
			return candidates[i].idleFor > candidates[j].idleFor
		}
		return candidates[i].sequence < candidates[j].sequence
	})
	remaining := result.Expanded
	remainingBytes := estimatedTotal
	evictions := 0
	for _, candidate := range candidates {
		overCount := policy.MaxExpanded > 0 && remaining > policy.MaxExpanded
		overBytes := policy.MaxBytes > 0 && remainingBytes > policy.MaxBytes
		if !overCount && !overBytes {
			break
		}
		if evictions >= policy.MaxEvictionsPerSweep && policy.MaxEvictionsPerSweep > 0 {
			result.DeferredByBudget++
			continue
		}
		if !candidate.object.Evict() {
			// A hand-off began between the observation and the call. It is in use now, so it is not
			// a candidate; the budget is simply not met by this sweep.
			result.InUse++
			continue
		}
		evictions++
		result.Evicted++
		result.ReleasedBytes += candidate.estimated
		remaining--
		remainingBytes -= candidate.estimated
	}
	// The expansion count is NOT corrected here. An earlier version stored "expanded after this
	// sweep" back into it, which silently discarded every publish that happened DURING the sweep -
	// and a library that finished a probe just above the budget then had no further publish to
	// trigger the sweep that would fix it. `pending` is exact as long as every publish and every
	// release reports (LazyOutbound does, on the ready path of Close and on a successful Evict), and
	// its failure mode if one is ever missed is an extra sweep that finds nothing - not a stale count
	// nobody re-checks.
	return result
}
