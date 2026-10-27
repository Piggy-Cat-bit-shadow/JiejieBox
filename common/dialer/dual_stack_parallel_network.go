package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var _ ParallelNetworkDialer = (*resolveParallelNetworkDialer)(nil)

// Candidate discovery for the NETWORK-scheduling entry points.
//
// # The split, stated once
//
//	Candidate discovery   the resolve dialer's. It knows the resolver, the query policy, the
//	                      sniffed domain, the local family capability and the family strategy.
//	Candidate scheduling  the scheduler's. Which candidate, when to launch, how to cancel losers,
//	                      family health.
//
// This file is the discovery side. Scheduling stays in `DialSerialNetwork`, `DialParallelNetwork` and
// the `candidateScheduler`, which receive a candidate list and know nothing about sniffing, FakeIP or
// DNS. Keeping the scheduler ignorant of business metadata is what prevents a second scheduler from
// growing here.
//
// # The defect this closes
//
// This package has two ways in for one destination: `DialContext`, and `DialParallelNetwork` /
// `ListenSerialNetworkPacket` with the candidate addresses the caller holds. Domain recovery lived
// only on the first.
//
// `route.ConnectionManager.NewConnection` takes the SECOND whenever the destination is already an
// address:
//
//	if len(metadata.DestinationAddresses) > 0 || metadata.Destination.IsIP() {
//	    remoteConn, err = dialer.DialSerialNetwork(...)
//	} else {
//	    remoteConn, err = this.DialContext(...)
//	}
//
// which is every TUN flow from an application that has already resolved the name - the common case,
// and the one the user report came from. `DialSerialNetwork` consults `ParallelNetworkDialer`, then
// `ParallelInterfaceDialer`. The resolve dialer implemented only the second, so a literal took
// `DialParallelInterface` once per address, and that method's literal arm hands the address to the real
// dialer unchanged. Recovery - `dialLiteralWithRecovery`, the sniffed-domain lookup, the family
// fallback - was never reached, while `recoverCandidates` sat one call away with the sniffed domain
// already in the context metadata.
//
// The observable result is in the production log: a WeChat flow to `[2409:...]:80` with a sniffed
// `szminorshort.weixin.qq.com` was refused by the global-IPv6 egress gate on a machine whose IPv4 path
// was healthy, and the words `dns: lookup domain szminorshort.weixin.qq.com` never appeared. The
// component was correct; the production path did not reach it.
//
// # Discovery may not become a precondition
//
// The literal is the endpoint the application chose, and the rule for it is that recovery is a FALLBACK:
// it must not defer the original attempt by even one syscall of DNS work. So `DialParallelNetwork` does
// not look the name up and then schedule. It schedules the literal immediately and feeds the recovered
// candidates in as LATE candidates, which is the mechanism this scheduler already has for exactly this
// problem - the same one the hostname path uses for a family that answers late.
//
// That is why the first draft of this fix was wrong, and the regression test caught it rather than a
// reviewer: it ran discovery to completion before scheduling, so a resolver that never answered held a
// connection whose own address was perfectly dialable. A caller that cannot be delayed is not served by
// a discovery-then-schedule sequence, however clean the ownership looks on paper.

// DialParallelNetwork completes the parallel surface begun by DialParallelInterface.
//
// Without it the NETWORK entry point fell through to per-address interface dialling, which is the second
// half of the same defect: `DialSerialNetwork` asks for this interface first, and a dialer that does not
// implement it never receives the candidate list as a list.
//
// # Two shapes, chosen by whether recovery has anything to do
//
// No sniffed domain - or no resolver, or the recursion marker is set - and the call is a plain
// scheduling request, forwarded with no goroutine, no channel and no timer. A literal therefore pays
// nothing for a capability it is not using, which is what keeps its cost identical to before.
//
// A sniffed domain, and the literal is scheduled immediately while the recovered candidates join as late
// candidates. See the file header for why the lookup may not run first.
func (d *resolveParallelNetworkDialer) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	err := d.initialize()
	if err != nil {
		return nil, err
	}
	// ONE admission policy for this whole flow, captured before any decision is made - the same rule the
	// single-destination path applies. Every branch below is handed it and never reads the network again,
	// so this flow cannot plan against one reading and schedule against another.
	admission := d.captureAdmissionPolicy()
	// The strict family strategy is applied to the LIST, before it is planned. `planCandidates` is built
	// for a preference (`prefer_ipv4` / `prefer_ipv6` / AsIS) and has no strict mode, and the scheduler
	// never re-checks the policy - it filters the local family capability and nothing else. So an address
	// of an excluded family must not enter the plan at all, and the candidate list is exactly where
	// "only means only" is enforceable.
	candidates := withoutFamilyExcluded(destinationAddresses, admission.strategy)
	strategyExcludedEverything := len(candidates) == 0 && len(destinationAddresses) > 0
	recoveryApplies := d.recoveryPossible(ctx) && isBareLiteralList(destination, destinationAddresses)

	if strategyExcludedEverything && recoveryApplies {
		// The policy excluded every candidate the caller supplied, and there IS a name to recover from.
		// Recovery therefore runs to completion here, and doing so does not violate "discovery is a
		// fallback, not a precondition": there is no immediate attempt for it to delay, because the
		// policy has already removed the one candidate that existed. Waiting is the only way this flow
		// can connect at all, and refusing immediately would kill a flow the operator's own policy still
		// admits through the other family.
		//
		// This is a correction to an earlier version of the error reporting below, which returned the
		// strategy exclusion as soon as the filtered list was empty. That produced a good message and
		// skipped the recovery which could have carried the flow: with `ipv6_only` and an IPv4 literal
		// whose sniffed name has an AAAA, the userspace path refused a connection it could have made.
		recovered := d.mergeRecovered(destination, d.recoverCandidates(ctx, destination, admission), admission)
		if len(recovered) > 0 {
			return d.schedule(ctx, network, destination, destination.Addr, recovered, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, nil, admission)
		}
	}
	if strategyExcludedEverything {
		// Either there was nothing to recover from, or recovery found nothing this policy admits. The
		// list is empty because the POLICY emptied it, which is a different fact from "there was nothing
		// to try", and it is the fact an operator has to be told: the single-destination path says
		// "destination <addr> excluded by strategy ipv4_only", and this path and
		// `raceCandidatesExcluding` used to flatten the same mistake into "no dial candidates" - a
		// symptom that names neither the policy nor the destination.
		return nil, E.New("destination ", destination, " excluded by strategy ", strategyName(admission.strategy),
			": every supplied candidate belongs to the family this policy forbids")
	}
	if !recoveryApplies {
		return d.schedule(ctx, network, destination, destination.Addr, candidates, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, nil, admission)
	}
	feed := d.feedRecoveryAsLateCandidates(ctx, destination, admission)
	// The feed is joined on the way out, whatever the outcome: a winner must not leave a resolver query
	// in flight, and a recovered address must not be left parked on a channel the scheduler has stopped
	// reading. Doing it here rather than relying on the caller's context being cancelled turns the
	// guarantee into a fact this function keeps - see feedRecoveryAsLateCandidates for why it is owed.
	defer feed.stop()
	return d.schedule(ctx, network, destination, destination.Addr, candidates, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, feed.candidates, admission)
}

// ListenSerialNetworkPacket is the packet counterpart, with the same hole: the caller's else-chain sends
// an unconnected packet flow with pre-resolved candidates through this method, whose
// ParallelInterfaceDialer fallback also skipped discovery.
//
// # Why this one MAY wait for the lookup when the stream path may not
//
// The packet path has no scheduler and no race: `ListenSerialNetworkPacket` is a serial walk whose only
// fallback is the next candidate in the list, because a UDP socket creation is not a verified outcome
// and racing two packet sockets would declare a winner that never sent anything. There is accordingly no
// "dial the literal now and join later" shape available - a packet connection cannot be swapped after it
// is handed out. So the lookup runs up front, bounded by the caller's context, and the recovered
// candidates are ordered BEHIND the literal the application chose.
func (d *resolveParallelNetworkDialer) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	err := d.initialize()
	if err != nil {
		return nil, netip.Addr{}, err
	}
	admission := d.captureAdmissionPolicy()
	candidates := withoutFamilyExcluded(destinationAddresses, admission.strategy)
	strategyExcludedEverything := len(candidates) == 0 && len(destinationAddresses) > 0
	if d.recoveryPossible(ctx) && isBareLiteralList(destination, destinationAddresses) {
		if recovered := d.recoverCandidates(ctx, destination, admission); len(recovered) > 0 {
			candidates = withoutFamilyExcluded(d.mergeRecovered(destination, recovered, admission), admission.strategy)
		}
	}
	if len(candidates) > 0 {
		// The packet path has no scheduler and no race: `ListenSerialNetworkPacket` walks the list
		// and the FIRST socket that can be created wins outright. Ordering IS the decision here.
		// Leaving the caller's order untouched made prefer_ipv4 depend on the resolver's A/AAAA
		// order (and on the application's own list) rather than on the effective strategy. Apply the
		// SAME planner the stream path uses: the original literal leads its family, the effective
		// preference interleaves the rest, and AsIS without an original preserves the caller's
		// leading family.
		plannableOriginal := netip.Addr{}
		if admission.admits(destination.Addr) {
			plannableOriginal = destination.Addr
		}
		planStrategy := admission.strategy
		if planStrategy == C.DomainStrategyAsIS && !plannableOriginal.IsValid() {
			planStrategy = domainStrategyFor(candidates[0].Is6())
		}
		candidates = planCandidates(candidates, plannableOriginal, planStrategy).addresses()
	}
	if len(candidates) == 0 && strategyExcludedEverything {
		// The policy emptied the list, and recovery found nothing it admits. This MUST NOT reach the
		// helper below with an empty list, because the helper reads an empty list as "the caller had no
		// candidates" and puts `destination.Addr` back:
		//
		//	if len(destinationAddresses) == 0 { destinationAddresses = []netip.Addr{destination.Addr} }
		//
		// That convenience is right for a caller with nothing and is a policy violation for a caller whose
		// list is empty BECAUSE THE POLICY EMPTIED IT: measured, with `ipv4_only` and a UDP flow to an IPv6
		// literal, the excluded literal was re-synthesised and the socket was created for the family the
		// operator forbade. The re-synthesis happens below this wrapper, so it cannot be prevented from
		// there - the only place that knows the difference is here, and reporting the exclusion is how it
		// stays known.
		return nil, netip.Addr{}, E.New("destination ", destination, " excluded by strategy ", strategyName(admission.strategy),
			": every supplied candidate belongs to the family this policy forbids")
	}
	return ListenSerialNetworkPacket(ctx, d.attemptDialer(), destination, candidates, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
}

// schedule runs the one scheduler, so every path in this file orders and cancels candidates identically.
//
// `lateCandidates` is nil for the common case, which is the scheduler's "static plan, no late arrivals"
// mode and costs it nothing.
func (d *resolveParallelNetworkDialer) schedule(ctx context.Context, network string, destination M.Socksaddr, original netip.Addr, candidates []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration, lateCandidates <-chan dualStackCandidate, admission admissionPolicy) (net.Conn, error) {
	// `original` is the literal the application selected, and it is passed to the planner so the
	// application's own endpoint leads its family - the same rule the single-destination path applies.
	// Leaving it out was a real defect in the first version of this function: with a healthy literal AND
	// a resolvable name, the recovered address could be dialled first, which is exactly the "recovery
	// overrules the application's choice" failure the promoteOriginal rule exists to prevent. The
	// regression test for it is TestNetworkEntryPointPreservesTheOriginalWithinItsFamily.
	//
	// It is admitted to the planner ONLY if the effective policy admits it. `promoteOriginal` re-inserts
	// the address it is given, and it does not consult the strategy - it cannot, it is a planner rule -
	// so handing it an address the policy excluded puts that address straight back into the plan. That
	// was the second defect this function shipped with, caught by
	// TestNetworkEntryPointReportsAStrategyExclusionAsOne: under ipv4_only the IPv6 literal was filtered
	// out of the list and then promoted back in, and the flow connected on the family the operator had
	// forbidden.
	plannableOriginal := netip.Addr{}
	if admission.admits(original) {
		plannableOriginal = original
	}

	// The planner takes a PREFERENCE and has no strict mode, so a strict policy is expressed by
	// filtering the list - which `withoutFamilyExcluded` has already done by the time this is called.
	// The preference itself must come from the EFFECTIVE DOMAIN STRATEGY, not from the first
	// candidate: a caller's list (TUN metadata, a cached answer, resolver order) can legitimately
	// lead with the family the operator did not prefer. Deriving "prefer IPv6" from `candidates[0]`
	// made `prefer_ipv4` race IPv6 first whenever the list did, which is a policy inversion rather
	// than an ordering detail.
	//
	// AsIS is the one strategy that has no preference of its own: it follows the application's
	// original family when one is present (the planner already implements that from the original
	// argument), and otherwise follows the list's leading family so a caller that supplied an
	// ordered answer keeps it.
	planStrategy := admission.strategy
	if planStrategy == C.DomainStrategyAsIS && !plannableOriginal.IsValid() {
		planStrategy = domainStrategyFor(len(candidates) > 0 && candidates[0].Is6())
	}
	plan := planCandidates(candidates, plannableOriginal, planStrategy)
	scheduler := d.schedulerFor(fallbackDelay, admission)
	// The same admission rule the plan was built under, handed to the scheduler so a LATE candidate - which
	// never passes through planCandidates - cannot reintroduce a family the policy excluded. The predicate
	// is captured once, so the guard applies the policy the plan was built with rather than re-reading it
	// mid-race.
	conn, _, err := scheduler.dialWithLateCandidatesAdmitting(ctx, plan, lateCandidates, admission.lateAdmission(), func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
		// Each attempt keeps the full configuration the caller supplied: the network strategy, the
		// interface selection and the fallback interface selection. Racing chooses WHICH address to try,
		// never how to try it.
		return d.attemptDialer().DialParallelInterface(attemptCtx, network, M.SocksaddrFrom(address, destination.Port), strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	})
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// domainStrategyFor maps the leading candidate's family to the planner's preference, for internal use.
//
// The planner takes a PREFERENCE and has no strict mode, so a strict policy is expressed by filtering the
// list - which `withoutFamilyExcluded` has already done by the time this is called - and this value then
// only decides the interleaving of what survived.
func domainStrategyFor(preferIPv6 bool) C.DomainStrategy {
	if preferIPv6 {
		return C.DomainStrategyPreferIPv6
	}
	return C.DomainStrategyPreferIPv4
}

// recoveryFeed is a running recovery lookup: the candidates it publishes, and the means to stop it.
//
// # Why this is a type rather than two return values
//
// The lookup's LIFETIME is part of its contract. `stop` cancels the feed and waits for the producer, so
// after it returns the goroutine has genuinely exited - and that has to be observable, not merely
// intended. `done` is what makes it observable: a closed channel is ambiguous (a buffered channel can be
// read without ever having been closed, and a close is not ordered with the goroutine leaving its frame),
// while a dedicated `done` is closed by the producer as its last act.
type recoveryFeed struct {
	// candidates is what the scheduler reads. Closed when the producer finishes, which is how the
	// scheduler learns no more are coming - the same contract the hostname path uses for a late family.
	candidates <-chan dualStackCandidate
	// done is closed by the producer as its final act, after its last send was delivered or abandoned.
	done <-chan struct{}
	// stop cancels the feed and WAITS for the producer.
	stop func()
}

// ready reports whether the scheduler can read from this feed at all.
func (f recoveryFeed) ready() bool {
	return f.candidates != nil
}

// feedRecoveryAsLateCandidates runs the recovery lookup in the background and publishes what it finds as
// candidates the race may still join.
//
// # Ownership: the caller must stop and join this producer
//
// The candidate channel is the producer's OUTPUT, not its lifetime. A caller that returns as soon as the
// scheduler does - which is every caller, because the scheduler returns on its winner - leaves the
// producer parked on a channel nobody will read again. The buffer holds one candidate, so the FIRST
// recovered address can be delivered, but a second one blocks in the send's select until the context is
// cancelled.
//
// That made the previous comment here ("a connection the literal established cancels it") false as
// written: nothing in this function cancelled anything on the literal's success, and correctness rested
// on an out-of-file convention - that every caller passes a context it cancels promptly after the dial
// returns. `route.ConnectionManager` does (`finishSetup`, one statement later), so this was never a
// production leak; it was a documented guarantee nobody was keeping, which is how a future caller holding
// a flow-lifetime context inherits a parked goroutine per connection.
//
// So the producer now owns its own context, derived from the caller's, and the caller is handed the means
// to prove it is gone. This mirrors `resolveDialer.raceCandidates` (resolve.go), which does exactly this
// for the hostname path and says why: a connection lives for minutes, and a producer must stop the moment
// the winner is known rather than when the connection eventually closes.
func (d *resolveParallelNetworkDialer) feedRecoveryAsLateCandidates(ctx context.Context, destination M.Socksaddr, admission admissionPolicy) recoveryFeed {
	// Narrower than the connection's lifetime on purpose. Cancelling the feed cannot affect the
	// connection that won - the producer owns no socket - and it is what releases a send parked on a
	// channel the scheduler has stopped reading.
	feedCtx, cancelFeed := context.WithCancel(ctx)
	lateCandidates := make(chan dualStackCandidate, 1)
	done := make(chan struct{})
	var producers sync.WaitGroup
	producers.Add(1)
	go func() {
		defer producers.Done()
		defer close(done)
		defer close(lateCandidates)
		recovered := d.recoverCandidates(feedCtx, destination, admission)
		if len(recovered) == 0 {
			return
		}
		for _, address := range d.mergeRecovered(destination, recovered, admission) {
			if address == destination.Addr {
				// The literal is already in the plan. Feeding it again would be a second attempt at the
				// endpoint the caller asked for, and the scheduler would only deduplicate it.
				continue
			}
			select {
			case lateCandidates <- dualStackCandidate{address: unmapAddress(address), family: classifyAddress(address)}:
			case <-feedCtx.Done():
				return
			}
		}
	}()
	return recoveryFeed{
		candidates: lateCandidates,
		done:       done,
		// stop cancels the feed and WAITS for the producer, so a caller that invokes it has proof the
		// goroutine has exited rather than an expectation that it will.
		stop: func() {
			cancelFeed()
			producers.Wait()
		},
	}
}

// mergeRecovered combines the destination literal with the recovered addresses.
//
// The application's own address is filtered by the same two rules the single-destination path applies to
// it - the strict family strategy and the local capability - so merging cannot reintroduce an address
// that path would have refused, and `MergeOriginalDestination` then places it first WITHIN its family,
// which is the rule everywhere else. Recovery is a fallback; it must not overrule the endpoint the
// application selected.
func (d *resolveParallelNetworkDialer) mergeRecovered(destination M.Socksaddr, recovered []netip.Addr, admission admissionPolicy) []netip.Addr {
	// The application's own address goes to the planner only when the effective policy admits it AND the
	// machine can dial it. `promoteOriginal` re-inserts whatever it is handed without consulting the
	// strategy - it is a planner rule and cannot - so an excluded address passed in here comes straight
	// back into the plan. Two regression tests pin this: TestNetworkEntryPointReportsAStrategyExclusionAsOne
	// (an ipv4_only flow must not connect on an IPv6 literal) and the local-family half, where a literal
	// this machine has no path for must not be promoted back in front of the recovered answer.
	original := netip.Addr{}
	if admission.admits(destination.Addr) {
		original = destination.Addr
	}
	return MergeOriginalDestination(original, recovered, admission.strategy)
}

// recoveryPossible reports whether a recovery lookup has anything to work with at all: an initialized
// resolver, a context allowed to use it, and a sniffed domain that passes validation.
//
// It exists so `DialParallelNetwork` can decide whether to start the race with a late-candidate channel
// WITHOUT doing any resolver work, which is what keeps a literal with no sniffed domain on exactly its
// previous path rather than paying for machinery that could only ever produce nothing.
func (d *resolveParallelNetworkDialer) recoveryPossible(ctx context.Context) bool {
	if d.router == nil || addressRecoveryDisabled(ctx) {
		return false
	}
	if err := d.initialize(); err != nil {
		return false
	}
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		return false
	}
	return validRecoveryDomain(metadata.Domain) != ""
}

// attemptDialer is the dialer one ATTEMPT goes through.
//
// # Why it is named rather than just `d.dialer`
//
// The two roles are different objects in the production graph and the difference is load-bearing.
// `route.ConnectionManager` hands `DialSerialNetwork` the OUTBOUND, and the outbound's
// `DialParallelNetwork` schedules onto the resolve WRAPPER the outbound holds. So a call arriving here
// has `d.dialer == the outbound`, and scheduling the race onto it would call the outbound's
// `DialParallelNetwork` again - unbounded recursion. Production avoids that because the outbound's method
// builds its own scheduler rather than re-entering the wrapper; this file gets the same property by
// naming the per-attempt dialer explicitly.
//
// A chain built the other way round - the wrapper directly over the leaf, which is what
// `NewResolveDialer` produces and what most fixtures use - sets both to the leaf, so the distinction
// costs nothing.
func (d *resolveParallelNetworkDialer) attemptDialer() ParallelInterfaceDialer {
	if d.attempts != nil {
		return d.attempts
	}
	return d.dialer
}

// schedulerFor binds the race to this chain's long-lived family health, so a verdict a literal learns is
// the verdict a hostname already learned.
//
// Discovery makes this reachable for flows that previously never consulted an owner at all:
// `default_parallel_network.go` looks for one through the resolve wrappers, and the network entry point
// now goes through that lookup instead of past it.
func (d *resolveParallelNetworkDialer) schedulerFor(fallbackDelay time.Duration, admission admissionPolicy) *candidateScheduler {
	if owner, isOwner := d.attemptDialer().(familyHealthOwner); isOwner {
		return owner.newDualStackScheduler(d.fallbackDelayOrDefaultFor(fallbackDelay), admission)
	}
	return &candidateScheduler{fallbackDelay: d.fallbackDelayOrDefaultFor(fallbackDelay)}
}

// fallbackDelayOrDefaultFor resolves the fallback interval for THIS call: the caller's value when it
// asked for one, otherwise the dialer's configured value, otherwise the connection default.
//
// It is the order the single-destination path applies, kept here rather than re-derived, so a configured
// fallback delay is not silently replaced by the package default on the network path.
func (d *resolveParallelNetworkDialer) fallbackDelayOrDefaultFor(fallbackDelay time.Duration) time.Duration {
	if fallbackDelay > 0 {
		return fallbackDelay
	}
	if d.fallbackDelay > 0 {
		return d.fallbackDelay
	}
	return N.DefaultFallbackDelay
}

// isBareLiteralList reports whether this call is a literal destination with no candidate list of its own.
//
// # Why the length alone is not the question
//
// `DialSerialNetwork` fills an empty list with the literal before scheduling:
//
//	if len(destinationAddresses) == 0 {
//	    if !destination.IsIP() { panic("invalid usage") }
//	    destinationAddresses = []netip.Addr{destination.Addr}
//	}
//
// so by the time a dialer is called, the caller's nil and the scheduler's synthesized single candidate
// are the SAME slice value, and a dialer can no longer tell them apart. The two shapes are still
// distinguishable in the direction that matters: a caller that genuinely resolved a name supplies either
// more than one address or addresses that differ from the destination, while a caller that supplied
// exactly the destination has said nothing the literal has not already said - so adding discovery to it
// cannot displace an endpoint the caller chose.
func isBareLiteralList(destination M.Socksaddr, destinationAddresses []netip.Addr) bool {
	if !destination.IsIP() {
		return false
	}
	switch len(destinationAddresses) {
	case 0:
		return true
	case 1:
		return destinationAddresses[0] == destination.Addr
	default:
		return false
	}
}
