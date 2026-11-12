package oomkiller

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/buildgate"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/urltest"
	boxConstant "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
)

func RegisterService(registry *boxService.Registry) {
	boxService.Register[option.OOMKillerServiceOptions](registry, boxConstant.TypeOOMKiller, NewService)
}

type Service struct {
	boxService.Adapter
	ctx           context.Context
	logger        log.ContextLogger
	network       adapter.NetworkManager
	connections   adapter.ConnectionManager
	recorder      *Recorder
	timerConfig   timerConfig
	adaptiveTimer *adaptiveTimer
	pressure      atomic.Uint32
	// guard is the admission policy for expensive creations, or nil when this build has no
	// process budget to protect. See NewService for the only condition that installs one.
	guard *resourceguard.Guard
	// reclaim observes what the memory-reclaim executor actually did. It owns no policy and cannot
	// start a pass; it is the bounded record the report reads. See reclaim_observation.go.
	reclaim *reclaimObserver
}

func NewService(ctx context.Context, logger log.ContextLogger, tag string, options option.OOMKillerServiceOptions) (adapter.Service, error) {
	memoryLimit, mode := resolvePolicyMode(ctx, options)
	config, err := buildTimerConfig(options, memoryLimit, mode, options.KillerDisabled)
	if err != nil {
		return nil, err
	}
	s := &Service{
		Adapter:     boxService.NewAdapter(boxConstant.TypeOOMKiller, tag),
		ctx:         ctx,
		logger:      logger,
		network:     service.FromContext[adapter.NetworkManager](ctx),
		connections: service.FromContext[adapter.ConnectionManager](ctx),
		recorder:    service.FromContext[*Recorder](ctx),
		timerConfig: config,
		reclaim:     newReclaimObserver(),
	}
	service.MustRegister[*Service](ctx, s)
	// The reclaim record is contributed under its own top-level key, next to the client's document
	// and next to the admission guard's. Registration is unconditional on a recorder existing, for
	// the same reason the report itself is: a build without a recorder has nowhere to write, and one
	// with a recorder gets the same additive key whether or not this process has a strict budget.
	if s.recorder != nil {
		s.recorder.SetExtraMetadata("memoryReclaim", s.reclaimMetadata)
	}
	// The residency inventory is contributed the same way and for the same reason: an additive
	// top-level key produced at write time from the owner that can actually answer. The producer is
	// looked up lazily, because the reference manager registers itself while starting - after this
	// service is constructed - and a build with no started box simply reports no key.
	if s.recorder != nil {
		s.recorder.SetExtraMetadata("residency", s.residencyMetadata)
	}
	// The object-identity ledger is the second half of the same question, contributed as its own key.
	// It is DELIBERATELY not folded into "residency": that key's contract says its numbers are
	// owner-reach and not resource counts, and mixing real object counts into it would restore the
	// exact misreading the contract exists to prevent. A reader that wants "how much is resident"
	// reads this key; a reader that wants "who can be asked" reads the other.
	if s.recorder != nil {
		s.recorder.SetExtraMetadata("objectResidency", s.objectResidencyMetadata)
	}
	// And the build reservation, which is the third ledger the audit asks to be readable from one
	// entry: creation admission ("resourceGuard"), parked builds (this key's queue/wait/waiters) and
	// residency ("objectResidency"). The gate is looked up lazily because the Box installs it on the
	// construction context before this service exists; a build with no gate writes no key.
	if s.recorder != nil {
		s.recorder.SetExtraMetadata("buildGate", s.buildGateMetadata)
	}
	// The queue ledger is the measurement side of the same question (audit Q08/Q15): how many rounds
	// and probes are running, how many are WAITING, and what the round queue's bound is. Without it a
	// reader can see the build reservation and the resident objects but not the one queue whose growth
	// the audit measured. It is contributed as its own key for the same reason as the others.
	if s.recorder != nil {
		s.recorder.SetExtraMetadata("queueLedger", s.queueMetadata)
	}
	// The admission guard is installed ONLY for a Network Extension with a process budget.
	//
	// That condition, and not "a memory limit was configured", is what makes this safe to
	// ship: a desktop or Android process has no 50 MiB ceiling to police, and imposing
	// admission there would trade throughput for a limit the platform does not enforce. The
	// guard is registered in the context so protocol code reads the POLICY OWNER's decision
	// instead of deciding for itself whether it is running inside a Network Extension.
	if mode == policyModeNetworkExtension {
		s.guard = resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), s.guardLevel)
		service.MustRegister[*resourceguard.Guard](ctx, s.guard)
		if s.recorder != nil {
			s.recorder.SetExtraMetadata("resourceGuard", s.guardMetadata)
		}
	}
	return s, nil
}

// guardLevel maps the pressure this service already publishes onto the admission levels.
//
// It reads the SAME atomic the TUN stack consumes through MemoryPressure, so admission, the
// slab pools, and the reclaim path cannot hold three different opinions about how much
// pressure the process is under. The mapping is deliberately total: an unknown value is
// treated as normal rather than as pressure, because refusing traffic on an unrecognised
// state would turn a wiring mistake into an outage.
func (s *Service) guardLevel() resourceguard.Level {
	switch tun.MemoryPressure(s.pressure.Load()) {
	case tun.MemoryPressureCritical:
		return resourceguard.LevelCritical
	case tun.MemoryPressureWarning:
		return resourceguard.LevelArmed
	default:
		return resourceguard.LevelNormal
	}
}

// GuardStats reports the admission counters, or the zero value when this build has no guard.
//
// It exists so a pressure report can say not only "the process was at 47 MiB" but "the
// admission policy refused N creations and the peak in flight was M": the first is a symptom,
// the second is what the system did about it.
func (s *Service) GuardStats() resourceguard.GuardStats {
	if s.guard == nil {
		return resourceguard.GuardStats{}
	}
	return s.guard.Stats()
}

// guardMetadata renders the guard's counters for the OOM report's metadata document.
//
// # Why it is a map and not a struct
//
// The report schema belongs to the client, and this package must not extend it. A key the client
// does not know is ignored by every existing reader, which is what makes the addition safe; a
// struct with tags would be a second schema to keep in step with the first.
//
// It returns nil when there is no guard, so a platform without a NetworkExtension budget writes
// exactly the document it wrote before.
func (s *Service) guardMetadata() any {
	if s.guard == nil {
		return nil
	}
	stats := s.guard.Stats()
	gate := stats.Gate
	return map[string]any{
		"level":        stats.Level.String(),
		"transitions":  stats.Transitions,
		"limit":        gate.Limit,
		"queueLimit":   gate.QueueLimit,
		"waitBudgetMs": gate.WaitBudget.Milliseconds(),
		"inflight":     gate.Inflight,
		"waiters":      gate.Waiters,
		"maxInflight":  gate.MaxInflight,
		"requested":    gate.Requested,
		"admitted":     gate.Admitted,
		"queued":       gate.Queued,
		"refused":      gate.Refused,
		"timedOut":     gate.TimedOut,
		"canceled":     gate.Canceled,
		"released":     gate.Released,
		// The fast-fail policy is reported separately from the capacity counters: "the limit
		// refused 3" and "starting 12 more would have been wasted" are different events, and a
		// reader must not have to infer one from the other.
		"rateDelayMs":      stats.Rate.Delay.Milliseconds(),
		"rateWindows":      stats.Rate.Windows,
		"rateDelayed":      stats.Rate.Delayed,
		"rateRefused":      stats.Rate.Refused,
		"rateFastFailures": stats.Rate.FastFailures,
		"rateRecovered":    stats.Rate.Recovered,
	}
}

// reclaimMetadata renders the reclaim executor's bounded record for the OOM report's metadata
// document.
//
// # Why this is separate from the events stream
//
// events.jsonl records the events that CHANGE what the report reader must explain: start, stop,
// state transitions, resets and snapshots. A reclaim pass can run many times without changing any of
// those, so writing each one there would grow the report with rows nobody asked for. The metadata
// key answers the other question - "what has the executor been doing lately, and did it pay" - with
// a fixed-size recent ring plus totals, and it is only as fresh as the document's rewrite cadence.
//
// It returns nil when there is no observer, so a platform without a reclaim executor writes exactly
// the document it wrote before.
func (s *Service) reclaimMetadata() any {
	if s.reclaim == nil {
		return nil
	}
	return s.reclaim.metadata(time.Now())
}

// residencyMetadata renders the retirement walk's owner inventory for the OOM report's metadata
// document.
//
// # What the numbers mean
//
// Per owner kind (outbound, endpoint, DNS transport, HTTP client manager) it reports two counts: how
// many owners the walk can reach, and how many of those expose the release capability. Both are
// OWNER-REACH counts. They are not connection, stream, session or byte counts, and they are not
// termination counts - see adapter.ResidencyOwnerCount, which is the type that carries this contract.
//
// "measure": "owner-reach" is part of the document rather than a comment so a reader that finds the
// key without this file cannot mistake the numbers for a resource ledger. The per-pool
// active/idle/draining ledger is the pool's own and is not inferred here.
//
// It returns nil when no started reference manager has registered itself, so a build without a box -
// or one that has not started yet - writes exactly the document it wrote before.
func (s *Service) residencyMetadata() any {
	if s == nil || s.ctx == nil {
		return nil
	}
	reporter := service.FromContext[adapter.ResidencyReporter](s.ctx)
	if reporter == nil {
		return nil
	}
	snapshot := reporter.ResidencySnapshot()
	return map[string]any{
		"measure":       "owner-reach",
		"outbounds":     residencyOwnerCount(snapshot.Outbounds),
		"endpoints":     residencyOwnerCount(snapshot.Endpoints),
		"dnsTransports": residencyOwnerCount(snapshot.DNSTransports),
		"httpClients":   residencyOwnerCount(snapshot.HTTPClients),
	}
}

// residencyOwnerCount renders one kind's counts. The field names repeat the semantics on purpose: a
// reader of the document alone must not read "reachable" as a number of live resources.
func residencyOwnerCount(count adapter.ResidencyOwnerCount) map[string]any {
	return map[string]any{
		"reachableOwners":  count.Reachable,
		"releasableOwners": count.Releasable,
	}
}

// objectResidencyMetadata renders the object-identity ledger for the OOM report's metadata
// document (audit Q15).
//
// # What the numbers are, and what they are not
//
// Per owner kind: how many owners the walk reaches, how many of them could answer with a ledger,
// how many could not (Unknown), how many answered with an estimate (Estimated), and the summed
// Creating/Active/Idle/Draining states plus the cumulative Terminated release count. The four states
// are exclusive per object, so Live = Creating+Active+Idle+Draining cannot double count; an owner is
// visited once by the shared walk, so summing across owners cannot either.
//
// "measure": "object-identity" makes the difference from the "residency" key readable from the
// document alone. Coverage is reported rather than hidden: a kind whose owners cannot answer reads
// {reportingOwners: 0, unknownOwners: N}, never a precise zero.
//
// It returns nil when no started reference manager has registered itself, or when the registered one
// only implements owner-reach - in both cases the document is exactly what it was before this key.
func (s *Service) objectResidencyMetadata() any {
	if s == nil || s.ctx == nil {
		return nil
	}
	ownerReporter := service.FromContext[adapter.ResidencyReporter](s.ctx)
	if ownerReporter == nil {
		return nil
	}
	reporter, isReporter := ownerReporter.(adapter.ObjectResidencyReporter)
	if !isReporter {
		return nil
	}
	snapshot := reporter.ObjectResidencySnapshot()
	return map[string]any{
		"measure":       "object-identity",
		"outbounds":     objectResidencyCount(snapshot.Outbounds),
		"endpoints":     objectResidencyCount(snapshot.Endpoints),
		"dnsTransports": objectResidencyCount(snapshot.DNSTransports),
		"httpClients":   objectResidencyCount(snapshot.HTTPClients),
		"terminated":    "cumulative completed releases per kind, not part of liveObjects",
		"unknown":       "an owner that cannot answer is counted here, never as zero objects",
	}
}

// objectResidencyCount renders one kind's object ledger.
//
// The per-unit records are the Q15 fix made readable: a kind can hold owners that count different
// objects (a stream, a session, a device, a pool generation), and a reader must not add their Active
// counts together. MixedUnits says when the flat totals below span more than one unit, and Units
// carries the split, so a document that only shows the flat numbers cannot silently conflate them.
func objectResidencyCount(count adapter.ResidencyObjectCounts) map[string]any {
	units := make([]map[string]any, 0, len(count.Units))
	for _, unit := range count.Units {
		units = append(units, map[string]any{
			"unit":               string(unit.Unit),
			"owners":             unit.Owners,
			"exact":              unit.Exact,
			"generation":         unit.Generation,
			"generations":        unit.Generations,
			"retiredGenerations": unit.RetiredGenerations,
			"creating":           unit.Creating,
			"active":             unit.Active,
			"idle":               unit.Idle,
			"draining":           unit.Draining,
			"liveObjects":        unit.Creating + unit.Active + unit.Idle + unit.Draining,
			"terminated":         unit.Terminated,
		})
	}
	return map[string]any{
		"owners":             count.Owners,
		"reportingOwners":    count.Reporting,
		"unknownOwners":      count.Unknown,
		"estimatedOwners":    count.Estimated,
		"mixedUnits":         count.MixedUnits,
		"units":              units,
		"generations":        count.Generations,
		"retiredGenerations": count.RetiredGenerations,
		"creating":           count.Creating,
		"active":             count.Active,
		"idle":               count.Idle,
		"draining":           count.Draining,
		"liveObjects":        count.Creating + count.Active + count.Idle + count.Draining,
		"terminated":         count.Terminated,
		"mixedUnitsRule":     "when mixedUnits is true the flat totals add unlike objects; read units instead",
	}
}

// queueMetadata renders the measurement coordinator's queue ledger for the OOM report (audit Q08/Q15).
//
// # Why it is a separate key
//
// The audit asks for three readable accounts - creation, build and queue. Creation is the admission
// guard's, the build reservation has its own key, and this is the queue: how many rounds and probes
// are in flight, how many are waiting, and what the round queue's bound is. The round queue is the one
// the audit measured growing with the number of rounds (2 waiting entries for 4 rounds, 30 for 32), so
// the document states the bound next to the two counts that can be compared with it: roundsQueued
// (inside the bound) and roundsOverflow (demand past it, waiting without a queue entry).
//
// It returns nil when the context carries no coordinator (a unit test, an embedder, a process that
// never built a box), so such a document is exactly what it was before.
func (s *Service) queueMetadata() any {
	if s == nil || s.ctx == nil {
		return nil
	}
	coordinator := urltest.CoordinatorFromContext(s.ctx)
	if coordinator == nil {
		return nil
	}
	return map[string]any{
		"measure":             "queue",
		"measurementLimit":    coordinator.Limit(),
		"measurementsInUse":   coordinator.InFlight(),
		"measurementsQueued":  coordinator.Queued(),
		"roundLimit":          coordinator.RoundLimit(),
		"roundsInUse":         coordinator.RoundsInFlight(),
		"roundsQueued":        coordinator.RoundsQueued(),
		"roundQueueBound":     coordinator.RoundWaiterLimit(),
		"roundsOverflow":      coordinator.RoundsOverflow(),
		"roundsOverflowTotal": coordinator.RoundsOverflowTotal(),
		"roundQueueRule": "roundsQueued never exceeds roundQueueBound; the demand past it waits " +
			"without a queue entry (roundsOverflow) and is never refused, so every round still " +
			"measures every member",
	}
}

// buildGateMetadata renders the build reservation for the OOM report's metadata document (Q20).
//
// It reports the budget the reservation is currently running under - level, limit, queue, wait and
// the countable overlap bound - next to the counters that say what it did (admitted, deferred,
// inflight, waiters, maxInflight). ByteBoundKnown is carried rather than assumed: the audit's rule
// is that an estimate must not be presented as a bound, so the document states which part is exact.
//
// It returns nil when the Box installed no gate, so an embedder's document is unchanged.
func (s *Service) buildGateMetadata() any {
	if s == nil || s.ctx == nil {
		return nil
	}
	gate := buildgate.FromContext(s.ctx)
	if gate == nil {
		return nil
	}
	stats := gate.Stats()
	budget := gate.Budget()
	return map[string]any{
		"measure": "build-count",
		"level":   budget.Level.String(),
		// The admission limit and the overlap are separate numbers on purpose (Q20): a permit is
		// never revoked, so builds admitted before pressure rose are still running. "limit" is what
		// the gate admits from now on; "maxOverlapping" is the bound that is still true now;
		// "peakOverlapping" is the largest overlap ever observed.
		"limit":            budget.Limit,
		"inflightBuilds":   budget.Inflight,
		"maxOverlapping":   budget.MaxOverlappingBuilds,
		"peakOverlapping":  budget.PeakOverlapping,
		"queueLimit":       budget.Queue,
		"waitBudgetMs":     budget.Wait.Milliseconds(),
		"oldDocumentBound": budget.MaxOldDocuments,
		"byteBoundKnown":   budget.ByteBoundKnown,
		"budgetNote":       budget.Note,
		"inflight":         stats.Gate.Inflight,
		"waiters":          stats.Gate.Waiters,
		"maxInflight":      stats.Gate.MaxInflight,
		"requested":        stats.Gate.Requested,
		"admitted":         stats.Gate.Admitted,
		"queued":           stats.Gate.Queued,
		"refused":          stats.Gate.Refused,
		"timedOut":         stats.Gate.TimedOut,
		"canceled":         stats.Gate.Canceled,
		"released":         stats.Gate.Released,
		"admittedBuilds":   stats.AdmittedBuilds,
		"deferredBuilds":   stats.Deferred,
		"succeededBuilds":  stats.Succeeded,
		"failedBuilds":     stats.Failed,
		"abandonedBuilds":  stats.Abandoned,
		"replacingBuilds":  stats.BuildsWithPreviousVersion,
		"firstBuilds":      stats.BuildsWithoutPreviousVersion,
		"transitions":      stats.Transitions,
		"outcomes":         "admittedBuilds = succeeded + failed + abandoned + inflight; every outcome returns the slot",
	}
}

func MemoryPressure(ctx context.Context) func() tun.MemoryPressure {
	oomKiller := service.FromContext[*Service](ctx)
	if oomKiller == nil {
		return nil
	}
	return oomKiller.MemoryPressure
}

func (s *Service) MemoryPressure() tun.MemoryPressure {
	return tun.MemoryPressure(s.pressure.Load())
}

func (s *Service) startTimer() error {
	if !s.timerConfig.policyMode.hasTimerMode() {
		return E.New("memory pressure monitoring is not available on this platform without memory_limit")
	}
	s.adaptiveTimer = newAdaptiveTimer(s.logger, s.network, s.connections, service.FromContext[adapter.CacheFile](s.ctx), s.recorder, &s.pressure, s.timerConfig)
	// The observer is attached before the timer is started: start() may schedule a poll immediately,
	// and a pass that ran before the field was set would be missing from the report.
	s.adaptiveTimer.reclaim = s.reclaim
	var carriedState *timerState
	if s.recorder != nil {
		carriedState = s.recorder.instanceStarted(s.timerConfig, s.adaptiveTimer.limitThresholds)
	}
	s.adaptiveTimer.start(carriedState)
	return nil
}

func (s *Service) stopTimer() {
	var state timerState
	if s.adaptiveTimer != nil {
		state = s.adaptiveTimer.stop()
	}
	s.pressure.Store(uint32(tun.MemoryPressureNone))
	if s.recorder != nil {
		s.recorder.instanceStopped(state)
	}
}
