package dialer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

// Dual-stack candidate scheduling.
//
// # The problem this solves
//
// Candidates used to be attempted one at a time, in order. Against a blackholed address
// that means waiting the entire connect timeout before trying the next one, even when the
// next address is healthy - and when the two addresses are in different families, the
// healthy family is never attempted until the broken one has fully timed out.
//
// The scheduler starts the first candidate immediately and adds one more every
// fallbackDelay. A candidate that fails FAST advances the schedule immediately, because
// there is no reason to wait out a delay once the answer is already known to be no.
//
// # What "success" means here
//
// Exactly what the executor says it means. This scheduler is handed a function that
// performs one attempt and reports whether it worked. For TCP that is a completed
// connection. For QUIC it is a completed handshake - a UDP socket being created, or
// DialEarly returning, is NOT success, and no caller of this code may treat it as such.
//
// # Winner ownership
//
// Exactly one attempt wins. Every other attempt is closed before this function returns,
// including attempts that succeed late: an attempt that reports a connection after the
// winner has been chosen would otherwise be recorded nowhere and leak its socket. That
// specific bug was fixed once in the MASQUE racer; this is the same discipline.

// dialAttemptFunc performs one attempt against one address.
//
// Returning a non-nil conn AFTER an error is not allowed; callers must close anything they
// create before returning an error.
type dialAttemptFunc func(ctx context.Context, address netip.Addr) (net.Conn, error)

// pendingAttempt records an attempt that has started and not yet reported.
//
// # Why the scheduler needs this at all
//
// A family that silently blackholes never reports an error: the attempt is still blocked when
// the other family wins, and it unwinds with context.Canceled - which must NOT be recorded as a
// family failure, because a cancelled loser says nothing about the network. So without some
// record of what was still outstanding, the scheduler had no way to notice that a family had
// demonstrably failed to answer, and the next connection paid the fallback delay again.
//
// The record is deliberately tiny and lives in the scheduler: the candidate count is small, a
// slice is cheaper than a map, and nothing here outlives one race.
type pendingAttempt struct {
	address   netip.Addr
	family    addressFamily
	startedAt time.Time
	// completed is written by the attempt goroutine and read by the winner path, so it must be
	// synchronized. A plain bool here is a data race: the winner decides which families stalled
	// while attempts are still finishing on their own goroutines.
	completed atomic.Bool
}

// dialAttemptResult is one attempt's outcome.
type dialAttemptResult struct {
	conn    net.Conn
	err     error
	address netip.Addr
}

// candidateScheduler races candidates for a single connection.
type candidateScheduler struct {
	// fallbackDelay is how long to wait before starting the next candidate. Zero uses the
	// package default.
	fallbackDelay time.Duration
	// health, when set, may promote the first candidate of a recently-failed family so a
	// broken family costs no delay on the next connection.
	health *familyHealth
	// networkEnvironment scopes the health state.
	networkEnvironment uint64
	// localFamily is what the machine could dial when this connection began. A family the machine
	// has no addressing for is dropped from the plan and refused from the late stream, so the race
	// never spends an attempt - or a fallback interval - on an address that cannot be reached.
	//
	// It restricts ONLY a family that is provably undialable; see localFamilyPolicy. The zero value
	// restricts nothing, which is what a scheduler built without a capability (every test that does
	// not opt in, and any manager that cannot report one) gets.
	localFamily localFamilyPolicy
}

// dial runs the race and returns the winning connection.
//
// Ownership on return:
//   - exactly one conn is returned, or none on error
//   - every losing conn, including a late success, has been Closed before returning
//   - every started goroutine has exited before returning
func (s *candidateScheduler) dial(ctx context.Context, plan candidatePlan, attempt dialAttemptFunc) (net.Conn, netip.Addr, error) {
	return s.dialWithLateCandidates(ctx, plan, nil, attempt)
}

// dialWithLateCandidates runs the race and accepts further candidates while it is in progress.
//
// # Why late candidates matter
//
// A and AAAA resolve independently, and one can answer far later than the other. Waiting for
// both before starting means either a connection that stalls on the slow family, or a candidate
// list that never contains the family that would have won. Letting a late family join a race
// already under way is what makes the slow answer useful.
//
// # What stays the same
//
// Exactly one winner; one fallback-timer owner; bounded launch (one new candidate per fallback
// interval, or immediately after a hard failure); synchronous loser cleanup; no re-ordering or
// restarting of a candidate that has already been launched.
//
// lateCandidates may be nil, in which case this is the previous static behaviour exactly. A
// closed channel means resolution has finished, so the scheduler knows no more are coming and
// can stop waiting for them.
func (s *candidateScheduler) dialWithLateCandidates(ctx context.Context, plan candidatePlan, lateCandidates <-chan dualStackCandidate, attempt dialAttemptFunc) (net.Conn, netip.Addr, error) {
	// Drop the candidates the local machine cannot dial, and report whether anything was dropped so
	// the empty-plan cases below can tell "no candidate was ever offered" from "every candidate was
	// refused locally".
	//
	// The filter runs here rather than in the planner because a late candidate never passes through
	// the planner at all: it is appended to the launch queue straight from the resolver's stream, so
	// a filter installed in planCandidates alone would be bypassed by whichever family answered
	// second - which is the case that matters, because the slow answer is the one that arrives after
	// the race has begun.
	launchPlan, refused := s.filterLocalFamily(plan)
	plan = launchPlan
	candidates := plan.candidates
	switch {
	case len(candidates) == 0 && lateCandidates == nil:
		if !refused.refused() {
			return nil, netip.Addr{}, E.New("no dial candidates")
		}
		// Every candidate belonged to a family the machine cannot dial. Returning here rather than
		// waiting is the whole point: the alternative is the caller's connect timeout spent on
		// addresses this machine has no way to reach.
		return nil, netip.Addr{}, localFamilyRefusalError(refused, s.localFamily)
	case len(candidates) == 0:
		// Nothing to start with. A late stream was supplied, so wait for it rather than
		// failing: the fast family may simply not have answered yet. A refused candidate is
		// dropped from that stream too, so a stream that carries only refused families still
		// terminates - the scheduler concludes when resolution finishes.
		//
		// `refused` is deliberately still live here. The wait exists for a family that may yet
		// answer, but the plan did not start empty - it was EMPTIED - and when the stream closes
		// without producing anything, the reason the plan was empty is the only explanation left.
		// Concluding "no dial candidates" there would discard the one fact the reader needs.
	case len(candidates) == 1 && lateCandidates == nil:
		// The hot path, and the common case for a literal IP with no recovery.
		//
		// No goroutine, no timer, no channel, no race state. A "dual-stack" scheduler that
		// costs a goroutine and a timer for a single address would be a regression on the
		// most frequent path in the process.
		//
		// Family health is still updated, cheaply and without any of that machinery. Leaving
		// it out would mean the single-candidate path - the one most connections take - could
		// neither record a failure nor clear a penalty, so a family would never recover from
		// a single-stack connection.
		conn, err := attempt(ctx, candidates[0].address)
		if err != nil {
			// An attempt should not hand back a connection alongside an error - that is its contract,
			// stated above - but nothing enforces it, and the only way to honour the contract from this
			// side is to close what we were given. Discarding it would leak a socket on the one path
			// where nothing else holds a reference to it, and the cost of being careful is one nil check.
			if conn != nil {
				_ = conn.Close()
			}
			if s.health != nil {
				s.health.recordFailure(s.networkEnvironment, candidates[0].family, err)
			}
			return nil, netip.Addr{}, E.Cause(err, "dial ", candidates[0].address)
		}
		if s.health != nil {
			s.health.recordSuccess(s.networkEnvironment, candidates[0].family)
		}
		return conn, candidates[0].address, nil
	}

	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()

	// Unbuffered: an attempt cannot deposit a result that nobody will read. With a buffered
	// channel a late success would be written, never collected, and its socket leaked.
	results := make(chan dialAttemptResult)

	var (
		workerGroup sync.WaitGroup
		started     int
	)
	// attempts tracks every started candidate so a winner can tell which families were still
	// outstanding when it won.
	var attempts []*pendingAttempt

	startAttempt := func(candidate dualStackCandidate) {
		started++
		workerGroup.Add(1)
		record := &pendingAttempt{
			address:   candidate.address,
			family:    candidate.family,
			startedAt: time.Now(),
		}
		attempts = append(attempts, record)
		go func() {
			defer workerGroup.Done()
			conn, err := attempt(raceCtx, candidate.address)
			record.completed.Store(true)

			// Health is updated ONLY while the parent operation is still running.
			//
			// When the parent context ends, every attempt is cancelled at once and returns a
			// context error. Recording that as a family verdict would blame the network for the
			// caller's own deadline and would poison the next connection for the full window.
			// The clock is checked here rather than the error string, because the same error
			// value is produced by a candidate's own dial timeout - which IS a valid path
			// signal - and only the parent's state distinguishes them.
			parentAlive := ctx.Err() == nil

			// Record a PATH failure here, where the attempt completes, rather than only on
			// the winner path.
			//
			// An attempt that loses the race is exactly the case that carries the useful
			// signal: the address that blackholed is usually the LOSING one, and the winner
			// is the family that already works. Recording only on success meant nothing was
			// ever learned from the situation the mechanism exists for, so the fast-fallback
			// path was inert in production.
			//
			// The classification is conservative: caller cancellation and closed connections
			// - which is how a loser is torn down - record nothing.
			if err != nil && s.health != nil && parentAlive {
				s.health.recordFailure(s.networkEnvironment, record.family, err)
			}

			select {
			case results <- dialAttemptResult{conn: conn, err: err, address: candidate.address}:
			case <-raceCtx.Done():
				// The race is over. Close anything this attempt created rather than
				// leaving it for a reader that no longer exists.
				if conn != nil {
					conn.Close()
				}
			}
		}()
	}

	// Close every loser and wait for every worker before returning. Losing connections are
	// collected here rather than closed inline so a late success is covered too.
	var (
		losersAccess sync.Mutex
		loserConns   []net.Conn
	)
	collectLoser := func(conn net.Conn) {
		if conn == nil {
			return
		}
		losersAccess.Lock()
		loserConns = append(loserConns, conn)
		losersAccess.Unlock()
	}
	closeLosers := func() {
		losersAccess.Lock()
		conns := loserConns
		loserConns = nil
		losersAccess.Unlock()
		for _, conn := range conns {
			conn.Close()
		}
	}

	defer func() {
		cancelRace()
		workerGroup.Wait()
		closeLosers()
	}()

	// Launch state is a growable queue rather than a fixed slice, so a candidate arriving
	// while the race is in progress can be appended without disturbing anything already
	// launched. Already-started candidates are never re-ordered or restarted.
	launch := append([]dualStackCandidate(nil), candidates...)
	// seen deduplicates across the initial set and every late arrival, including the original
	// literal candidate when one was supplied.
	seen := make(map[netip.Addr]struct{}, len(candidates)+2)
	for _, candidate := range candidates {
		seen[candidate.address] = struct{}{}
	}

	// Decide the launch order. With recent family failure recorded, the first candidate of
	// each family starts together so a broken family costs no delay at all.
	nextIndex := 0
	if len(launch) > 0 {
		startAttempt(launch[0])
		nextIndex = 1
		if len(launch) > 1 && s.health != nil {
			if s.health.fallbackImmediately(s.networkEnvironment, launch[0].family, launch[1].family) {
				startAttempt(launch[1])
				nextIndex = 2
			}
		}
	}

	// resolutionFinished tracks whether the late-candidate stream has closed. Until it does,
	// the race cannot conclude that the candidate set is complete.
	resolutionFinished := lateCandidates == nil

	var (
		timer     *time.Timer
		timerChan <-chan time.Time
		lastErr   error
		failures  []error
		successes []dialAttemptResult
	)
	resetTimer := func() {
		if timer == nil {
			timer = time.NewTimer(s.fallbackDelayOrDefault())
		} else {
			// Drain before Reset. Resetting a timer that already fired and was not drained
			// leaves a stale value that would advance the schedule spuriously.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.fallbackDelayOrDefault())
		}
		timerChan = timer.C
	}
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timerChan = nil
		}
	}
	defer stopTimer()

	armTimerIfPending := func() {
		if nextIndex < len(launch) {
			resetTimer()
		} else {
			stopTimer()
		}
	}
	if len(launch) > 0 {
		armTimerIfPending()
	}

	for {
		select {
		case <-ctx.Done():
			return nil, netip.Addr{}, ctx.Err()

		case candidate, stillOpen := <-lateCandidates:
			if !stillOpen {
				// Resolution finished: no more candidates can arrive, ever.
				lateCandidates = nil
				resolutionFinished = true

				// Nothing was ever launched and nothing more can arrive. Returning here is the
				// whole point: the previous version required `started > 0` to conclude, so a
				// provider that closed its stream without producing a single candidate left the
				// race waiting for the PARENT TIMEOUT even though it already knew the answer.
				//
				// That is not a theoretical case - a name where both A and AAAA return NODATA
				// produces exactly this, and the caller would sit for the whole connect timeout
				// before being told there was no address.
				if started == 0 {
					// The plan was empty and the stream produced nothing. If the plan was empty
					// because the machine refused its candidates, that refusal is the answer - the
					// stream having nothing to add does not change why nothing was attempted.
					if refused.refused() {
						return nil, netip.Addr{}, localFamilyRefusalError(refused, s.localFamily)
					}
					return nil, netip.Addr{}, E.New("no dial candidates")
				}
				if nextIndex >= len(launch) && started == len(failures) {
					if len(failures) == 0 {
						return nil, netip.Addr{}, E.Cause(lastErr, "dial failed")
					}
					return nil, netip.Addr{}, E.Errors(failures...)
				}
				continue
			}
			if _, duplicate := seen[candidate.address]; duplicate {
				// Already known, whether from the initial plan, an earlier late arrival, or
				// the original literal destination. Deduplication is by canonical address, so
				// an IPv4-mapped form and its IPv4 form count as one.
				continue
			}
			seen[candidate.address] = struct{}{}

			// A late candidate is subject to the same local-family filter as a planned one.
			//
			// This is the half of the filter that a check inside planCandidates cannot reach: the
			// late stream never passes through the planner, so filtering only there would let the
			// second family to answer walk an undialable candidate straight into the race. The
			// address is marked as seen first, so a stream that repeats it costs nothing.
			if s.localFamily.hardUndialable(candidate.address) {
				continue
			}

			// A late candidate joins the queue. If nothing is currently pending it starts at
			// once, so a family that answers after the race began is not made to wait for a
			// fallback interval that was scheduled before it existed.
			//
			// "Nothing pending" is the whole condition, and it is why nextIndex is advanced to
			// len(launch) here: the candidate that starts has consumed its slot, so the NEXT
			// arrival sees something pending and waits for the timer instead of starting too.
			//
			// Starting every arrival immediately is what let a preferred family lose the race to
			// the family it was preferred over. The feeder emits the preferred family first, but
			// when both arrived within microseconds each was started on its own goroutine and the
			// dial order became whichever goroutine reached the dialer first - so the preference
			// held by the feeder was discarded here. The cadence is what makes the stream order
			// authoritative.
			launch = append(launch, candidate)

			// Start at once when nothing is queued ahead of this candidate AND no cadence is
			// currently armed.
			//
			// The second half is what keeps a burst ordered. Without it the test below is true
			// for every arrival in a burst - after the first start nextIndex equals len(launch),
			// and the queue then grows by one per arrival - so each arrival started immediately
			// on its own goroutine and the dial order became a goroutine race. A pending timer IS
			// the cadence, so while one is armed the arrival queues instead.
			if nextIndex >= len(launch)-1 && timerChan == nil {
				// Nothing was pending, so this candidate starts at once rather than waiting out
				// a fallback interval that was scheduled before it existed.
				startAttempt(candidate)
				nextIndex = len(launch)

				// The cadence STILL applies to whatever follows in this burst.
				//
				// Each late candidate was started the instant it arrived, so two candidates
				// arriving back to back both started immediately - each on its own goroutine -
				// and the dial ORDER became whichever goroutine reached the dialer first. That
				// discarded the ordering the feeder had just established: it holds the
				// non-preferred family and emits the preferred one first, precisely so the
				// preferred family leads.
				//
				// Arming the timer here (rather than only when the queue is non-empty) makes the
				// arrival order authoritative: the first candidate of a burst starts now, the next
				// waits for the interval, exactly as if both had been known at the start.
				//
				// This does not defer a genuinely late arrival: by the time the timer fires the
				// queue is drained, so the next arrival finds it fired and starts immediately.
				resetTimer()
				continue
			}

			// Something is already pending, so a timer is already counting down toward it.
			//
			// The timer is NOT re-armed here. Re-arming on every arrival restarts the countdown
			// from that arrival, so a family stream that keeps producing addresses postpones the
			// pending launch for as long as the stream continues - the schedule is measured from
			// the last arrival instead of from the last launch. With every dial blocking, so that
			// no failure can advance the schedule either, a steady trickle of late candidates
			// starved the pending launch entirely.
			//
			// Leaving the timer alone keeps the cadence anchored to the last launch, which is what
			// the delay is defined to be. A newly arrived candidate joins the queue behind the
			// pending one and is reached on schedule.
			continue

		case result := <-results:
			if result.err == nil && result.conn != nil {
				successes = append(successes, result)
				if len(successes) == 1 {
					// The winner. Cancel the race and stop launching; pending attempts are
					// closed by the deferred cleanup.
					//
					// A family that just completed a connection has demonstrated the path
					// works, so any penalty it carries is cleared here rather than by a
					// caller remembering to do it. Without this a family penalised once
					// would stay penalised for the full window even after it recovered.
					winnerFamily := classifyAddress(result.address)
					if s.health != nil && ctx.Err() == nil {
						s.health.recordSuccess(s.networkEnvironment, winnerFamily)

						// A family that was still outstanding when another family connected has
						// demonstrably failed to answer. Without this the scheduler learned
						// nothing from the most common bad-family case: a silently blackholed
						// IPv6 attempt loses the race, unwinds with context.Canceled, and
						// context.Canceled must never be recorded as a failure. The next
						// connection then paid the fallback delay all over again.
						//
						// The verdict is SOFT: it does not disable the family, reorder it
						// permanently, or blacklist it. It only lets the next connection start
						// both families' first candidates together, which is a bounded,
						// expiring nudge - preference is not lock-in.
						s.recordStalledOppositeFamily(attempts, winnerFamily, result.address)
					}
					cancelRace()
					stopTimer()
					return result.conn, result.address, nil
				}
				// Unreachable while the first success returns immediately, but kept so a
				// future change that collects successes cannot silently drop one.
				collectLoser(result.conn)
				continue
			}

			lastErr = result.err
			failures = append(failures, E.Cause(result.err, result.address.String()))

			// Same defence as the single-candidate path: an attempt that returns a connection together
			// with an error has violated its contract, and the scheduler is the last owner of that
			// socket. Closing it is what keeps the contract from becoming a leak.
			if result.conn != nil {
				_ = result.conn.Close()
			}

			// A fast failure frees its slot immediately: waiting out the delay would only
			// postpone an answer already known to be no.
			if nextIndex < len(launch) {
				startAttempt(launch[nextIndex])
				nextIndex++
				if nextIndex < len(launch) {
					resetTimer()
				} else {
					stopTimer()
				}
				continue
			}
			if started == len(failures) && nextIndex >= len(launch) && resolutionFinished {
				// Everything that will ever be attempted has failed.
				if len(failures) == 0 {
					return nil, netip.Addr{}, E.Cause(lastErr, "dial failed")
				}
				return nil, netip.Addr{}, E.Errors(failures...)
			}

		case <-timerChan:
			if nextIndex < len(launch) {
				startAttempt(launch[nextIndex])
				nextIndex++
				if nextIndex < len(launch) {
					resetTimer()
				} else {
					stopTimer()
				}
			} else {
				stopTimer()
			}
		}
	}
}

func (s *candidateScheduler) fallbackDelayOrDefault() time.Duration {
	if s.fallbackDelay > 0 {
		return s.fallbackDelay
	}
	return N.DefaultFallbackDelay
}

// localFamilyRefusal is a refusal the local machine issued, carrying BOTH the family and the address
// whose attempt it refused.
//
// # Why the address has to travel with the family
//
// The verdict has two terms. The family existence check answers "does this machine carry an address of
// that family at all", and the egress check answers "is there a public route for it". A global IPv6
// destination can be refused by either, and the two have different remedies, so a value that keeps only
// the family cannot produce the sentence the reader needs - it collapses the more specific verdict into
// the more general one.
//
// That collapse was the defect: `hardUnavailableError(family)` maps ipv6 to "no interface on this
// machine carries an ipv6 address" unconditionally, so on the reported machine - IPv6 present, no global
// route - the scheduler told the operator an address was missing while the packet path, holding the same
// capability, correctly blamed the route.
//
// The address is carried rather than a pre-rendered error so the refusal is still decided in ONE place,
// `localFamilyPolicy.hardUndialableError`, from ONE policy capture. Nothing here re-derives the verdict.
type localFamilyRefusal struct {
	family  addressFamily
	address netip.Addr
}

// refused reports whether anything was actually refused, which is what decides whether a refusal may be
// reported at all.
//
// A plan that was empty to begin with - a resolver that answered with nothing - must keep saying "no
// dial candidates": blaming the machine for a name that has no address would be a fabricated verdict.
func (r localFamilyRefusal) refused() bool {
	return r.family != familyInvalid || r.address.IsValid()
}

// error is the refusal reason, from the policy that produced it.
//
// The destination-aware accessor is used when an address is available, so the gate and the explanation
// are the same decision. The family-only fallback remains for a caller that genuinely has no address,
// which is the case an empty candidate list produces.
func (r localFamilyRefusal) error(policy localFamilyPolicy) error {
	if r.address.IsValid() {
		if refusal := policy.hardUndialableError(r.address); refusal != nil {
			return refusal
		}
		// The address is no longer refused: the policy this read belongs to is not the one the
		// decision was taken on (a network transition moved the capture), and the honest answer is
		// the general one rather than a fact about a machine state that has changed.
		return E.New("no dial candidates")
	}
	if r.family == familyInvalid {
		return E.New("no dial candidates")
	}
	return hardUnavailableError(r.family)
}

// filterLocalFamily drops the planned candidates whose family the machine cannot dial.
//
// It returns the plan UNCHANGED - the same slice header, so the hot path allocates nothing and the
// caller's data is not touched - when nothing is refused, which is every connection on a machine that
// has addressing for both families. Only when something is refused does it build a filtered slice.
//
// # Why the filtered slice is a copy rather than an in-place compaction
//
// planCandidates hands its result to whoever asked, and the direct outbound's resolved-address path
// passes a slice it built from metadata. Compacting in place would rewrite the caller's own slice as
// a side effect of dialling - a mutation nobody asked for, on data whose lifetime the scheduler does
// not own. The cost of not doing it is one allocation on exactly the connections where a family is
// provably absent, which is the case the feature exists to make cheap.
//
// The refused destination is returned rather than a boolean so that the caller can report WHICH
// destination was undialable AND WHY, from the capability that refused it. That is the difference
// between an actionable message and "no dial candidates", and it is the message a user sees for the
// case this whole round exists for: a dual-stack name on a machine with no IPv6 route, where the AAAA
// half of the answer must not cost a connect timeout.
func (s *candidateScheduler) filterLocalFamily(plan candidatePlan) (candidatePlan, localFamilyRefusal) {
	if plan.capabilityRestrictsNothing(s.localFamily) {
		return plan, localFamilyRefusal{family: familyInvalid}
	}
	return s.dropUndialableCandidates(plan)
}

// dropUndialableCandidates is the slow half of filterLocalFamily: it is reached only when the
// capability can refuse something.
//
// It is a separate function so that the check above stays small enough to inline into the scheduler,
// which is what keeps the ordinary dial - and every dial made by a manager that cannot report a
// capability - off this code path entirely rather than merely paying little for it. The split is not
// a micro-optimisation for its own sake: this runs once per connection, and it is the one place in
// the dial path that was allowed to become more expensive in this round.
//
// Every decision here goes through s.localFamily.excluded, which consults network ownership before it
// answers - so a scheduler whose capture has been superseded drops nothing, and the successor
// generation's candidates reach the race.
func (s *candidateScheduler) dropUndialableCandidates(plan candidatePlan) (candidatePlan, localFamilyRefusal) {
	planned := len(plan.candidates)
	if planned == 0 {
		return plan, localFamilyRefusal{family: familyInvalid}
	}
	// The first refused candidate is located before anything is copied. Everything before it is kept
	// verbatim, which makes the copy start at the first removal instead of at the front.
	firstRefused := -1
	var refused localFamilyRefusal
	for index, candidate := range plan.candidates {
		if s.localFamily.hardUndialable(candidate.address) {
			firstRefused = index
			// The destination travels with the family so the message can name the machine's ACTUAL
			// verdict. A value carrying only the family cannot express the egress term, which is how
			// a machine with IPv6 addressing came to be reported as having none.
			refused = localFamilyRefusal{family: candidate.family, address: candidate.address}
			break
		}
	}
	if firstRefused < 0 {
		return plan, localFamilyRefusal{family: familyInvalid}
	}
	kept := make([]dualStackCandidate, firstRefused, planned)
	copy(kept, plan.candidates[:firstRefused])
	for _, candidate := range plan.candidates[firstRefused+1:] {
		if s.localFamily.hardUndialable(candidate.address) {
			continue
		}
		kept = append(kept, candidate)
	}
	plan.candidates = kept
	return plan, refused
}

// capabilityRestrictsNothing reports whether every candidate of this plan is dialable, without
// looking at the plan.
//
// It answers from the capability and its ownership alone, which is what makes it constant-time and
// inlinable: a policy that cannot refuse anything cannot refuse any candidate, so there is nothing to
// scan. It delegates the ownership half to the policy rather than re-deriving it, so the fast path and
// the slow path cannot disagree about whether a verdict is still current - a disagreement there would
// let the fast path skip a filter that would have removed something.
func (p candidatePlan) capabilityRestrictsNothing(policy localFamilyPolicy) bool {
	return policy.restrictsNothing()
}

// localFamilyRefusalError reports why no candidate could be attempted.
//
// A refusal that came from the local family capability is reported as itself, because "no dial
// candidates" would hide the cause: the resolver answered, and the machine dropped the answer. The
// reason is taken from the SAME policy the filter used, so the refusal and its explanation describe one
// machine state rather than two reads of a value that may have moved between them.
func localFamilyRefusalError(refused localFamilyRefusal, policy localFamilyPolicy) error {
	return refused.error(policy)
}

// familyHealth remembers which address family recently failed to establish a connection, so
// the next connection does not pay the fallback delay again to rediscover it.
//
// # Scope
//
// Keyed by network environment. A Wi-Fi network with broken IPv6 says nothing about the
// cellular network, and carrying a verdict across that boundary would penalise a working
// path. The environment changing clears the state, which is also what makes this safe to
// keep in memory only: there is nothing to persist because it is only valid for the network
// it was observed on.
//
// # What counts as a failure
//
// Only path failures. A TLS or authentication error means the network delivered packets and
// the peer answered - the family is working, and treating that as a family failure would
// penalise a healthy path because one server misbehaved. When a failure cannot be
// classified confidently, no state is recorded.
//
// # Window
//
// C.TCPTimeout. Long enough to cover a connection attempt, short enough that a recovered
// family is retried promptly rather than being written off.
type familyHealth struct {
	access sync.Mutex
	state  map[uint64]familyHealthState
}

type familyHealthState struct {
	failedFamily addressFamily
	failedAt     time.Time
}

func newFamilyHealth() *familyHealth {
	return &familyHealth{state: make(map[uint64]familyHealthState)}
}

// fallbackImmediately reports whether both families' first candidates should start together.
func (h *familyHealth) fallbackImmediately(environment uint64, preferred addressFamily, other addressFamily) bool {
	if preferred == other || preferred == familyInvalid || other == familyInvalid {
		return false
	}
	h.access.Lock()
	defer h.access.Unlock()
	entry, known := h.state[environment]
	if !known {
		return false
	}
	if time.Since(entry.failedAt) > tcpTimeoutWindow {
		delete(h.state, environment)
		return false
	}
	// Only promote when the family that recently failed is the one that would otherwise
	// have been preferred, and the other family is the alternative being offered.
	return entry.failedFamily == preferred
}

// recordStalledOppositeFamily records a SOFT verdict for a family that was still unresolved
// when another family established a connection.
//
// # What qualifies
//
// Three conditions, all required:
//
//   - the pending attempt is in a DIFFERENT family from the winner. Two addresses in one family
//     are ordinary racing, not evidence that the family is degraded, so a slow same-family
//     candidate proves nothing about the family as a whole.
//   - it started BEFORE the winner completed, so it had its chance.
//   - it has been pending for at least the effective fallback delay. A candidate that started a
//     millisecond ago has not stalled; it simply has not finished. This is what keeps a fast
//     healthy race - where both families start within a millisecond and one wins in five - from
//     manufacturing a penalty out of nothing.
//
// # What this is not
//
// It is not a path failure. A path failure means the kernel reported the network unreachable; a
// stall means the opposite family connected first while this one stayed silent. The two are
// recorded through different entry points and mean different things, and fabricating a
// DeadlineExceeded error to reuse the failure path would have conflated them.
func (s *candidateScheduler) recordStalledOppositeFamily(attempts []*pendingAttempt, winnerFamily addressFamily, winnerAddress netip.Addr) {
	now := time.Now()
	minimumPending := s.fallbackDelayOrDefault()

	var stalled addressFamily
	for _, attempt := range attempts {
		if attempt.completed.Load() {
			continue
		}
		if attempt.address == winnerAddress {
			continue
		}
		if attempt.family == winnerFamily || attempt.family == familyInvalid {
			continue
		}
		if now.Sub(attempt.startedAt) < minimumPending {
			continue
		}
		stalled = attempt.family
		break
	}

	if stalled != familyInvalid {
		s.health.recordStalledFamily(s.networkEnvironment, stalled)
	}
}

// recordStalledFamily records a SOFT verdict: another family established a connection while this
// one remained unresolved.
//
// # How this differs from recordFailure
//
// recordFailure means the kernel reported the path as broken - unreachable network, unreachable
// host, address unavailable. recordStalledFamily means something weaker and more common: this
// family did not answer in time, while the other family demonstrably could. The distinction is
// kept because they are genuinely different observations, and collapsing them would mean
// inventing an error the network never produced.
//
// # What it does NOT do
//
// It does not disable the family, order it permanently last, or blacklist it. The only effect is
// the same one a hard verdict has: the NEXT connection starts both families' first candidates
// together instead of making the suspect family wait out a fallback delay. Every other candidate
// still follows the normal schedule.
//
// It expires on the same C.TCPTimeout window and clears on the first successful connection in
// that family, so a transient stall costs one connection's worth of caution rather than
// changing the user's configuration.
func (h *familyHealth) recordStalledFamily(environment uint64, family addressFamily) {
	if family == familyInvalid {
		return
	}
	h.access.Lock()
	defer h.access.Unlock()
	h.state[environment] = familyHealthState{
		failedFamily: family,
		failedAt:     time.Now(),
	}
}

// recordFailure notes a path failure for the family an address belongs to.
func (h *familyHealth) recordFailure(environment uint64, family addressFamily, err error) {
	if family == familyInvalid {
		return
	}
	if !isFamilyPathFailure(err) {
		return
	}
	h.access.Lock()
	defer h.access.Unlock()
	h.state[environment] = familyHealthState{
		failedFamily: family,
		failedAt:     time.Now(),
	}
}

// recordSuccess clears a family's penalty once it is shown to work again.
func (h *familyHealth) recordSuccess(environment uint64, family addressFamily) {
	if family == familyInvalid {
		return
	}
	h.access.Lock()
	defer h.access.Unlock()
	if entry, known := h.state[environment]; known && entry.failedFamily == family {
		delete(h.state, environment)
	}
}

// invalidateEnvironment drops state for one environment, used when the network changes.
func (h *familyHealth) invalidateEnvironment(environment uint64) {
	h.access.Lock()
	defer h.access.Unlock()
	delete(h.state, environment)
}

// invalidateAll drops every verdict, for a full reset.
func (h *familyHealth) invalidateAll() {
	h.access.Lock()
	defer h.access.Unlock()
	h.state = make(map[uint64]familyHealthState)
}

// tcpTimeoutWindow is the lifetime of a family verdict. It is deliberately C.TCPTimeout
// rather than a new constant, so the window matches the time a connection attempt may
// legitimately take.
const tcpTimeoutWindow = 15 * time.Second

// isFamilyPathFailure reports whether an error means the network PATH for this family is
// broken, as opposed to the far end rejecting the connection.
//
// # Conservative by construction
//
// An unclassifiable error returns false, so no health state is recorded. The asymmetry is
// deliberate: failing to record a genuine path failure costs one extra fallback delay on the
// next connection, while recording a false one penalises a working family and hides a real
// protocol problem behind a network verdict.
func isFamilyPathFailure(err error) bool {
	if err == nil {
		return false
	}
	// Explicit path errors from the kernel.
	//
	// Matched through PathFailureErrnos rather than against the syscall constants directly, because on
	// Windows the two are different numbers: the standard library surfaces the raw Winsock errno, so
	// syscall.ENETUNREACH (0x20000043) never equals the 10051 that arrives. See
	// windows_socket_errors.go - the checks below were dead on the platform whose reported failure is
	// exactly this condition.
	if ErroredWith(err, PathFailureErrnos...) {
		return true
	}
	// Cancellation is never a family verdict. A cancelled context means the CALLER stopped
	// waiting - a losing attempt being cleaned up, a user aborting, or the whole operation's
	// deadline expiring and cancelling every attempt at once. Recording that as "this family
	// is broken" would penalise a healthy family for fifteen seconds because a different
	// candidate happened to win, or because the user navigated away.
	if errors.Is(err, context.Canceled) {
		return false
	}
	// net.ErrClosed is the same class: it comes from caller cancellation, a network switch,
	// or this package closing a loser. None of those says anything about the path.
	if errors.Is(err, net.ErrClosed) {
		return false
	}
	// io.EOF during connect means the peer closed before answering. On a datagram-oriented
	// path that is ambiguous, and on a stream it is indistinguishable from a server that
	// accepts and immediately hangs up - which is a server problem, not a broken family. The
	// conservative reading is to record nothing.
	if errors.Is(err, io.EOF) {
		return false
	}
	// A refusal is the peer actively answering: the path works.
	//
	// Checked in BOTH spellings for the same reason as the group above, and this is the more dangerous
	// half: a missed refusal means a healthy family is penalised because one server was down.
	if ErroredWith(err, PeerAnsweredErrnos...) {
		return false
	}
	// A timeout means nothing answered on that path. This is the blackhole case the whole
	// mechanism exists for.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// A bare deadline exceeded reaches here only when the attempt itself timed out rather
	// than the parent operation: a parent deadline arrives as context.Canceled-derived
	// cancellation of the attempt, and is excluded above.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}
