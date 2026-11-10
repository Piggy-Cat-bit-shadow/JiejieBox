package dialer

import (
	"context"
	"net/netip"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

// Dual-stack recovery for direct dials to a literal address.
//
// # Why this exists
//
// Fixing the resolve action is not enough, because a real TUN deployment usually has no
// `route action: resolve` configured at all. The application has already resolved the name
// and connects to an address, so the dialer receives a literal IP - and with a broken path
// for that address's family there is nothing to fall back to. The connection fails or times
// out while the other family was available the whole time.
//
// Sniffing recovers the domain the client actually asked for. This turns that domain back
// into candidate addresses so the scheduler has something to race.
//
// # Only the configured resolver, never the system one
//
// Recovery goes through the same adapter.DNSRouter as every other lookup, so the configured
// DNS rules, transports, caching and singleflight all apply. The system resolver is never
// consulted: on a proxy that would leak the user's DNS queries outside the tunnel, which is
// a far worse outcome than a slow connection.
//
// # No recursion
//
// A DNS exchange may itself travel through a direct outbound. If that outbound then ran
// recovery, resolving a name would require resolving a name, and a DNS server's own
// connection could recurse without bound. Recovery is therefore refused whenever the context
// already carries the marker, and the marker is set for the duration of the recovery lookup.

// recoveryDisabledContextKey marks a context in which address recovery must not run.
type recoveryDisabledContextKey struct{}

// withoutAddressRecovery returns a context in which recovery will not run.
//
// It is applied around the recovery lookup itself, so a DNS exchange performed to recover
// candidates cannot trigger another recovery.
func withoutAddressRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, recoveryDisabledContextKey{}, true)
}

// addressRecoveryDisabled reports whether recovery is suppressed for this context.
func addressRecoveryDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(recoveryDisabledContextKey{}).(bool)
	return disabled
}

// recoveryStream is a running recovery lookup: the candidate batches it publishes, and the means to
// stop and join the producer that owns them.
//
// # Why a batch is the unit, and why it is not a slice of addresses
//
// A batch is one family's published answer. The consumer needs that unit because "this family
// answered" and "this address is NEW" are different facts: a family whose only address is the
// literal the caller is already dialling contributes nothing to the race, and it must not be
// mistaken for a candidate that can carry the flow - nor may a repeated address close the source,
// because the other family may still be in flight. The producer therefore publishes a batch ONLY
// when it carries at least one candidate that has not been offered yet, so an arriving batch always
// means real progress.
type recoveryStream struct {
	batches <-chan []dualStackCandidate
	// stop cancels the lookup and waits for the OUTER producer. The LookupFamilies call itself runs
	// one frame below and is deliberately not joined - see streamRecoveredCandidates.
	stop func()
}

// ready reports whether this stream can produce anything at all.
func (s recoveryStream) ready() bool {
	return s.batches != nil
}

// recoveryDomain returns the sniffed domain recovery may use, and whether recovery applies at all.
//
// Every condition is a refusal, so an unusual situation degrades to the previous single-candidate
// dial rather than to a surprising one.
func (d *resolveDialer) recoveryDomain(ctx context.Context, destination M.Socksaddr) (string, bool) {
	// Only for literal destinations. A domain destination is already resolved by the normal path,
	// and re-resolving it here would duplicate that work.
	if !destination.IsIP() {
		return "", false
	}
	if addressRecoveryDisabled(ctx) {
		return "", false
	}
	if d.router == nil {
		return "", false
	}
	// The sniffed domain, validated. It is attacker-influenced - derived from bytes the peer chose -
	// and it is about to be given to a resolver.
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		return "", false
	}
	domain := validRecoveryDomain(metadata.Domain)
	if domain == "" {
		return "", false
	}
	return domain, true
}

// streamRecoveredCandidates starts the recovery lookup and publishes each family's admissible,
// genuinely-new candidates as they arrive.
//
// # The defect this shape removes
//
// Recovery used to end at the FIRST family that passed admission, and ending the loop cancelled the
// other family's query. Two consequences, both observed in the field:
//
//   - a first family that is silently blackholed - the ordinary case, because "passes admission"
//     says the address may be dialled, not that anything answers - ended the lookup and left the
//     flow with no fallback at all;
//   - a first family that answered with the SAME blackholed literal the caller was already dialling
//     contributed no new candidate after deduplication, and still ended the lookup. One duplicate
//     address closed the candidate source.
//
// So the producer no longer decides when the lookup is over. It publishes everything the flow may
// dial as it arrives, drops duplicates without treating them as an answer, and lets the race decide:
// the lookup ends when the resolver finishes, when the winner is known (which cancels this context),
// or when the caller's budget ends.
//
// # Freezing the policy once
//
// `admission` is the caller's single capture for the whole flow - the same object the plan was built
// under - so a family that answers seconds later is judged by the policy, the capability and the
// strategy the flow started with, not by a re-read of a value that may have moved.
//
// # Lifetime
//
// The channel is the producer's output, not its lifetime: the caller MUST call stop(). stop cancels
// the lookup and waits for the outer producer, which leaves on cancellation even when the resolver
// below it does not - that boundary is stated and tested by
// TestResolverThatIgnoresCancellationStatesTheBoundary.
func (d *resolveDialer) streamRecoveredCandidates(ctx context.Context, destination M.Socksaddr, admission admissionPolicy) recoveryStream {
	domain, applies := d.recoveryDomain(ctx, destination)
	if !applies {
		return recoveryStream{stop: func() {}}
	}

	// The lookup is logged at debug: it is an internal recovery step, not something the user asked
	// for, and it must not add noise to the connection log. `withoutAddressRecovery` marks the
	// context so a DNS exchange performed to recover cannot itself trigger recovery.
	streamCtx, cancelStream := context.WithCancel(withoutAddressRecovery(log.ContextWithOverrideLevel(ctx, log.LevelDebug)))

	outer := make(chan []dualStackCandidate, 2)
	inner := make(chan []dualStackCandidate, 2)

	// seen is the identity set for this lookup: the application's own literal, which has its own
	// attempt and its own owner, plus every address already published. It is touched only by the
	// lookup goroutine.
	seen := make(map[netip.Addr]struct{}, 4)
	seen[unmapAddress(destination.Addr)] = struct{}{}

	var producers sync.WaitGroup
	producers.Add(1)
	go func() {
		// The OUTER producer owns the caller-facing channel and is the only goroutine stop waits
		// for. It leaves on cancellation even if the lookup is still parked inside the resolver.
		defer producers.Done()
		defer close(outer)
		for {
			select {
			case batch, open := <-inner:
				if !open {
					return
				}
				select {
				case outer <- batch:
				case <-streamCtx.Done():
					return
				}
			case <-streamCtx.Done():
				return
			}
		}
	}()

	go func() {
		// The INNER lookup. Deliberately not joined: waiting for an implementation that ignores
		// cancellation would hand a successful connection's lifetime to a resolver that has not
		// answered. A cooperative resolver exits as soon as `streamCtx` is cancelled, which is what
		// releases its send.
		defer close(inner)
		deliver := func(addresses []netip.Addr) {
			// "Usable" has to mean ADMISSIBLE, and it has to be decided here rather than one layer
			// up: the family that passes admission is a candidate for the race, while filtering it
			// afterwards would let a refused family consume the whole answer and leave the admitted
			// one unread.
			admitted := d.admittedRecoveredCandidates(addresses, admission)
			batch := make([]dualStackCandidate, 0, len(admitted))
			for _, address := range admitted {
				canonical := unmapAddress(address)
				if _, duplicate := seen[canonical]; duplicate {
					continue
				}
				seen[canonical] = struct{}{}
				batch = append(batch, dualStackCandidate{
					address: canonical,
					family:  classifyAddress(canonical),
				})
			}
			if len(batch) == 0 {
				// Nothing NEW. The source stays open: the other family may still answer, and a
				// duplicate address must never be the reason a usable answer is abandoned.
				return
			}
			select {
			case inner <- batch:
			case <-streamCtx.Done():
			}
		}

		if dualStackRouter, supportsStreaming := d.router.(adapter.DNSDualStackRouter); supportsStreaming {
			_ = dualStackRouter.LookupFamilies(streamCtx, domain, d.queryOptions, func(result adapter.DNSFamilyResult) {
				if len(result.Addresses) == 0 {
					return
				}
				deliver(result.Addresses)
			})
			return
		}
		addresses, err := d.router.Lookup(streamCtx, domain, d.queryOptions)
		if err != nil {
			// Recovery is a best-effort improvement. A failure must not turn a connection that
			// would otherwise have been attempted into an error the user never asked for.
			return
		}
		deliver(addresses)
	}()

	return recoveryStream{
		batches: outer,
		stop: func() {
			cancelStream()
			producers.Wait()
		},
	}
}

// recoverCandidates resolves the sniffed domain into candidates for a literal destination.
//
// It returns nil when recovery does not apply, which leaves the caller's behaviour exactly as it
// was, and the FIRST family whose answer this flow may dial - usable meaning ADMITTED, not merely
// non-empty - then stops the lookup, so a fast dialable answer is never held waiting for a slow one.
//
// The producer it reads from publishes additional families for as long as it runs; a caller that
// wants them while the race is still alive reads the STREAM instead (see streamRecoveredCandidates).
// This shape remains for the callers that make a single decision from the first answer: the packet
// path, whose serial walk has no race to join, and the strategy-exclusion branch that has no
// immediate attempt to delay.
func (d *resolveDialer) recoverCandidates(ctx context.Context, destination M.Socksaddr, admission admissionPolicy) []netip.Addr {
	stream := d.streamRecoveredCandidates(ctx, destination, admission)
	defer stream.stop()
	if !stream.ready() {
		return nil
	}
	select {
	case batch, open := <-stream.batches:
		if !open {
			// Resolution finished without a family this flow can dial.
			return nil
		}
		return candidateAddresses(batch)
	case <-ctx.Done():
		return nil
	}
}

// candidateAddresses returns one published batch's addresses, in publication order.
func candidateAddresses(batch []dualStackCandidate) []netip.Addr {
	addresses := make([]netip.Addr, len(batch))
	for index, candidate := range batch {
		addresses[index] = candidate.address
	}
	return addresses
}

// admittedRecoveredCandidates drops the recovered addresses this flow may not dial, applying both terms of
// the decision in one place: the local family capability (a property of the machine) and the strict family
// strategy (a decision of the operator).
//
// # Why both terms belong here, and not one here and one later
//
// The two filters used to sit at different layers - capability in this function, strategy in
// `mergeRecovered` - and that split is what let a forbidden family end the lookup: this function stopped at
// the first capability-usable answer, the strategy dropped it afterwards, and the family the policy actually
// admitted was never read even though it was already in flight.
//
// Applying both here makes the caller's later filtering idempotent instead of load-bearing. It does not
// change which addresses are ultimately dialled; it changes which family gets to END the lookup.
func (d *resolveDialer) admittedRecoveredCandidates(addresses []netip.Addr, admission admissionPolicy) []netip.Addr {
	admitted, _, _ := admission.admitCandidates(addresses)
	return admitted
}

// withoutUndialableCandidates drops recovered addresses whose family this machine cannot dial.
//
// # Why the scheduler's own filter is not enough here
//
// The scheduler refuses those candidates anyway, and it is the authoritative place because it also
// covers late arrivals. Doing it here as well is not redundancy for its own sake: recovery runs while
// the caller is waiting, and its result decides whether the caller races recovery at all or keeps
// waiting for the original attempt. A list that LOOKS non-empty but contains only addresses this
// machine cannot reach would send the caller into a race that cannot produce a connection.
//
// It cannot empty the list into losing a fallback: it only removes addresses of a family that is
// provably undialable here, and those were never candidates in any sense that could have connected.
// A local-scope destination is exempt for the same reason it is exempt on the dial path.
//
// A chain that cannot report the capability returns the list unchanged, which is the required
// degradation: an implementation without the capability keeps its previous behaviour exactly.
func (d *resolveDialer) withoutUndialableCandidates(addresses []netip.Addr, admission admissionPolicy) []netip.Addr {
	return withoutUndialable(addresses, admission.localFamily)
}

// withoutUndialable drops the addresses a policy refuses, and returns the input untouched when the
// policy refuses nothing.
//
// It is a free function rather than a method because the serial branch of DialContext needs the same
// filter and must not go through the dialer to get it: the filter is a pure function of a list and a
// policy, and keeping it that way is what makes "the same rule applies on every local dial path"
// checkable instead of merely intended.
//
// # It does NOT modify the caller's slice, and that is a contract rather than a nicety
//
// The addresses reaching this function come from a resolver - DNSRouter.Lookup, or a family result
// published on a stream - and the interface says nothing about who owns the backing array. An
// implementation is free to return a cache-backed slice, and several do exactly that from a cache
// hit: the same array is handed to every caller for the lifetime of the entry. Compacting it in place
// would rewrite the resolver's own cache as a side effect of one connection's filtering, which
// corrupts answers that later callers receive and would be attributed to the resolver.
//
// So the two cases are deliberately different:
//
//	nothing refused -> the SAME slice, no copy, no allocation, no mutation
//	anything refused -> a NEW slice; the caller's array is not written at all
//
// Copying only when something is removed is what keeps the ordinary dual-stack connection - where the
// policy refuses nothing - on exactly the path it took before this round, which is why this is not
// implemented as "clone, then filter".
func withoutUndialable(addresses []netip.Addr, policy localFamilyPolicy) []netip.Addr {
	if len(addresses) == 0 {
		return addresses
	}
	// The first refusal is located before anything is copied, so the common case is one scan and no
	// allocation, and the copy is exactly as long as it needs to be.
	firstRefused := -1
	for index, address := range addresses {
		if policy.hardUndialable(address) {
			firstRefused = index
			break
		}
	}
	if firstRefused < 0 {
		return addresses
	}
	filtered := make([]netip.Addr, firstRefused, len(addresses))
	copy(filtered, addresses[:firstRefused])
	for _, address := range addresses[firstRefused+1:] {
		if policy.hardUndialable(address) {
			continue
		}
		filtered = append(filtered, address)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// refusedCandidateIn reports which candidate a policy refuses among these addresses.
//
// It exists so a caller that is about to filter can still NAME the reason afterwards: the filtered list
// is empty in exactly the case where the explanation matters most, and an empty list cannot say which
// destination produced it - nor, on a machine that has one address family and no public route for it,
// WHICH of the two facts did the refusing. The family alone cannot express that, so the address travels
// with it; see localFamilyRefusal.
//
// It reads the list without modifying it, so it and withoutUndialable can be called in either order;
// the serial branch calls it first only because that reads better.
func refusedCandidateIn(addresses []netip.Addr, policy localFamilyPolicy) localFamilyRefusal {
	for _, address := range addresses {
		if policy.hardUndialable(address) {
			return localFamilyRefusal{family: classifyAddress(address), address: address}
		}
	}
	return localFamilyRefusal{family: familyInvalid}
}

// withoutFamilyExcluded drops the addresses a strict single-family strategy excludes, and returns the
// input untouched when the strategy excludes nothing.
//
// # Why this is separate from planCandidates
//
// `planCandidates` expresses a PREFERENCE - prefer_ipv4, prefer_ipv6, AsIS - and orders both families
// accordingly; it has no strict mode. A candidate list that must contain one family only therefore has
// to be filtered before it is planned, and the planner's ordering then applies within the surviving
// family.
//
// # Why it is separate from withoutUndialable
//
// The two filters answer different questions and must not be merged. `withoutUndialable` asks "can
// this machine dial it" - a fact about the device. This asks "did the operator's policy allow it" - a
// decision about the configuration. A caller that reported one reason for the other would send an
// operator to look at the wrong thing, which is the same argument `dialLiteralWithRecovery` makes when
// it reports a strategy exclusion differently from a local family refusal.
//
// Like `withoutUndialable`, it does NOT modify the caller's slice, and that is a contract rather than
// a nicety: the addresses reaching it can be a resolver's cache-backed array, and compacting it in
// place would rewrite the resolver's own answer as a side effect of one connection's filtering.
func withoutFamilyExcluded(addresses []netip.Addr, strategy C.DomainStrategy) []netip.Addr {
	if len(addresses) == 0 {
		return addresses
	}
	if strategy != C.DomainStrategyIPv4Only && strategy != C.DomainStrategyIPv6Only {
		return addresses
	}
	firstExcluded := -1
	for index, address := range addresses {
		if !AddressAllowedByStrategy(address, strategy) {
			firstExcluded = index
			break
		}
	}
	if firstExcluded < 0 {
		return addresses
	}
	filtered := make([]netip.Addr, firstExcluded, len(addresses))
	copy(filtered, addresses[:firstExcluded])
	for _, address := range addresses[firstExcluded+1:] {
		if !AddressAllowedByStrategy(address, strategy) {
			continue
		}
		filtered = append(filtered, address)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// dialerLocalFamilyPolicy reads this dialer's view of which families the machine can dial.
//
// It WALKS the chain to the owner, so that a resolve dialer - which is a wrapper, not an owner - asks the
// same object the scheduler will ask, however many hops separate them. A chain that leads nowhere returns
// the zero policy, which restricts nothing: an implementation that cannot report the capability keeps its
// previous behaviour exactly.
//
// # Why it walks rather than asserts once
//
// `localFamilyOwner` is unexported and satisfied by exactly one production type, `*DefaultDialer`. An
// outbound in another package therefore cannot implement it - Go's unexported-method rule forbids it - and
// a `resolveDialer` built over an `adapter.Outbound` used to fall through to the zero policy here, with
// `findFamilyHealthOwner`'s matching assertion doing the same. Two such chains exist, both in
// `daemon/started_service.go` (the network-quality and STUN test dialers), and the cost was that the
// wrapper lost the SHARED family health and its recovery candidate filtering became a no-op - silently,
// because a zero policy reads as "this machine can dial anything".
//
// What made it fixable was the chain itself: the outbound is a HOP whose dialer is the resolved surface,
// and it now reports that through the Upstream protocol - the same protocol `common.Cast` walks, and the
// one the resolve wrappers already implement. So the walk reaches the real owner through it. Nothing is
// re-exposed across packages and no public API was added; the capability was always in the chain, the
// chain just had no way to say so.
//
// What is still bounded: the leaf's own `DefaultDialer.DialContext` applies the real per-socket gate
// regardless, so even a chain that genuinely cannot answer never dials a family this machine lacks - the
// difference is that a candidate the leaf would refuse is offered first and costs a failed attempt
// instead of being dropped in advance.
func (d *resolveDialer) dialerLocalFamilyPolicy() localFamilyPolicy {
	current := d.dialer
	for depth := 0; depth < chainWalkDepth && current != nil; depth++ {
		if owner, isOwner := current.(localFamilyOwner); isOwner {
			return owner.localFamilyPolicy()
		}
		next, ok := unwrapDialer(current)
		if !ok {
			break
		}
		current = next
	}
	return localFamilyPolicy{}
}

// LocalFamilyBlocked implements LocalFamilyDialer for the resolving wrapper.
//
// # Why the wrapper answers it rather than forwarding only from the leaf
//
// The wrapper is what the direct outbound holds: NewResolveDialer wraps the real dialer, so the
// object the bypass path can see is this one. Answering by forwarding keeps a single source for the
// verdict - the same object the scheduler will consult - instead of leaving the bypass to read the
// network manager itself and form a second opinion about the same instant.
//
// It is deliberately NOT a method on the ResolveDialer interface. That interface is a contract every
// implementation and test double would have to satisfy, and a dialer that cannot report the
// capability must keep working unchanged; the type assertion is the capability check.
func (d *resolveDialer) LocalFamilyBlocked(address netip.Addr) bool {
	return d.dialerLocalFamilyPolicy().hardUndialable(address)
}

// validRecoveryDomain returns the sniffed domain to recover through, or empty when it is not
// usable.
//
// A sniffed domain is attacker-influenced: it is parsed out of bytes the remote peer chose,
// and it is about to be handed to a resolver. Empty, over-long, control-character, and
// structurally implausible values are refused rather than resolved.
func validRecoveryDomain(domain string) string {
	switch {
	case domain == "":
		return ""
	case len(domain) > 253:
		return ""
	case strings.ContainsAny(domain, "\x00 \t\r\n/"):
		return ""
	}
	if _, err := netip.ParseAddr(domain); err == nil {
		// A sniffed "domain" that is really an address literal must not be resolved.
		return ""
	}
	trimmed := strings.TrimSuffix(domain, ".")
	if trimmed == "" || !strings.Contains(trimmed, ".") {
		return ""
	}
	for _, label := range strings.Split(trimmed, ".") {
		if label == "" || len(label) > 63 {
			return ""
		}
		for _, char := range label {
			isAlnum := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
			if !isAlnum && char != '-' && char != '_' {
				return ""
			}
		}
	}
	return trimmed
}
