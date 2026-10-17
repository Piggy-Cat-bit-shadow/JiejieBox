package dialer

import (
	"context"
	"net/netip"
	"strings"

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

// recoverCandidates resolves the sniffed domain into candidates for a literal destination.
//
// It returns nil when recovery does not apply, which leaves the caller's behaviour exactly as
// it was. Every condition is a refusal, so an unusual situation degrades to the previous
// single-candidate dial rather than to a surprising one.
func (d *resolveDialer) recoverCandidates(ctx context.Context, destination M.Socksaddr) []netip.Addr {
	// Only for literal destinations. A domain destination is already resolved by the normal
	// path, and re-resolving it here would duplicate that work.
	if !destination.IsIP() {
		return nil
	}
	if addressRecoveryDisabled(ctx) {
		return nil
	}
	if d.router == nil {
		return nil
	}

	// The sniffed domain, validated. It is attacker-influenced - derived from bytes the peer
	// chose - and it is about to be given to a resolver.
	// Read the context metadata without copying it: recovery only reads the sniffed domain.
	domain := ""
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		domain = validRecoveryDomain(metadata.Domain)
	}
	if domain == "" {
		return nil
	}

	// The lookup is logged at debug: it is an internal recovery step, not something the user
	// asked for, and it must not add noise to the connection log.
	// Cancellation is honoured by the router lookup below. The caller abandons this work the
	// moment the original address connects, so a lookup that ignored cancellation would keep
	// running DNS for a connection that is already established.
	recoveryCtx := withoutAddressRecovery(log.ContextWithOverrideLevel(ctx, log.LevelDebug))

	// Recovery streams when the router can, so a family that has answered is usable immediately.
	//
	// The complete Lookup waits for BOTH families, which is right for a routing decision that
	// needs the whole address set and wrong here: recovery exists to provide a fallback as soon as
	// one is available. With a resolver answering A in 10ms and AAAA in 3s, waiting for the
	// complete set made the recovered IPv4 unusable for three seconds - turning a working fallback
	// into a stall.
	//
	// The first USABLE family is returned as soon as it arrives - usable, not merely non-empty.
	//
	// # Why the filter has to be part of the choice, not a step after it
	//
	// This took the first published family and filtered it afterwards, which is only equivalent when that
	// family turns out to be dialable. When it is not, the filter emptied the list and this function
	// returned nothing, while the other family's answer was still in flight and `cancelStream` killed it.
	//
	// That is the reported machine's ordinary case: IPv6 addressing present and no global IPv6 egress
	// means an AAAA answer is refused by the egress term, A and AAAA are independent queries, and either
	// can answer first. On the runs where AAAA won, recovery produced nothing and a literal IPv6 flow was
	// refused with no IPv4 attempt - the symptom the recovery path exists to remove, occurring only under
	// a resolver ordering.
	//
	// The existing fixture had this recorded and routed around: `newRecoveryHarness` delays the AAAA half
	// "so the A answer is always published first", explaining that otherwise "the IPv6 answer would
	// sometimes arrive first, be refused by the filter, and end the lookup". So the code was measuring its
	// own workaround. The loop below is what makes the fixture's delay unnecessary.
	//
	// The streaming property is preserved: a family that is usable still ends the loop on its own
	// publication, so a fast dialable answer is not held waiting for a slow one.
	var recovered []netip.Addr
	if dualStackRouter, supportsStreaming := d.router.(adapter.DNSDualStackRouter); supportsStreaming {
		collected := make(chan []netip.Addr, 1)
		streamCtx, cancelStream := context.WithCancel(recoveryCtx)
		defer cancelStream()

		go func() {
			_ = dualStackRouter.LookupFamilies(streamCtx, domain, d.queryOptions,
				func(result adapter.DNSFamilyResult) {
					if len(result.Addresses) == 0 {
						return
					}
					select {
					case collected <- result.Addresses:
					case <-streamCtx.Done():
					}
				})
			// The stream ended. A nil value is the terminator, and it cannot be mistaken for an
			// answer: a published family is always non-empty (the callback drops empty ones).
			select {
			case collected <- nil:
			case <-streamCtx.Done():
			}
		}()

		for recovered == nil {
			select {
			case family := <-collected:
				if family == nil {
					// Resolution finished without a family this machine can dial.
					return nil
				}
				// "Usable" has to mean ADMISSIBLE, not merely dialable, and it has to be decided here
				// because this is what ends the loop.
				//
				// The capability filter alone was not enough: the strict family strategy was applied
				// one layer up, in `mergeRecovered`, so under `ipv6_only` an A answer would end the
				// lookup, be dropped by the strategy on the way out, and the AAAA that arrived later
				// would never be read. Deciding admission here is what makes the first family to answer
				// and be ADMITTED the one that ends the lookup.
				if usable := d.admittedRecoveredCandidates(family); len(usable) > 0 {
					recovered = usable
				}
			case <-recoveryCtx.Done():
				return nil
			}
		}
	} else {
		addresses, err := d.router.Lookup(recoveryCtx, domain, d.queryOptions)
		if err != nil {
			// Recovery is a best-effort improvement. A failure must not turn a connection that
			// would otherwise have been attempted into an error the user never asked for.
			return nil
		}
		recovered = d.admittedRecoveredCandidates(addresses)
	}

	if len(recovered) == 0 {
		return nil
	}
	return recovered
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
func (d *resolveDialer) admittedRecoveredCandidates(addresses []netip.Addr) []netip.Addr {
	return withoutUndialable(withoutFamilyExcluded(addresses, d.effectiveFamilyStrategy()), d.dialerLocalFamilyPolicy())
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
func (d *resolveDialer) withoutUndialableCandidates(addresses []netip.Addr) []netip.Addr {
	return withoutUndialable(addresses, d.dialerLocalFamilyPolicy())
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
// It goes through the familyHealthOwner when the underlying dialer has one, so that a resolve
// dialer - which is a wrapper, not an owner - asks the same object the scheduler will ask. A chain
// that leads nowhere returns the zero policy, which restricts nothing: an implementation that
// cannot report the capability keeps its previous behaviour exactly.
func (d *resolveDialer) dialerLocalFamilyPolicy() localFamilyPolicy {
	if owner, isOwner := d.dialer.(localFamilyOwner); isOwner {
		return owner.localFamilyPolicy()
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
