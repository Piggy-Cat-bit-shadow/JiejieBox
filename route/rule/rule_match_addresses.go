package rule

import (
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
)

// destinationMatchSource reports the address evidence a destination-IP condition should evaluate.
//
// Exactly one of the two results is non-empty:
//
//	exact       the destination IS this address (a literal target)
//	candidates  the flow offers these addresses as destination evidence
//
// # Why one function
//
// The three sources are not interchangeable, so their order is a semantic decision and every
// destination-IP condition has to make it identically. Before `match_only` existed each condition
// carried its own copy of the same two-step order; adding a third source in three places is how
// those copies start to disagree.
//
// # The precedence
//
//  1. A literal destination. It is the address the client actually connected to, and a resolution
//     performed for matching is not allowed to displace it: the same precedence already applies to
//     the dial candidate list, because a resolve action on a literal destination never changed what
//     ip_cidr or ip_is_private matched. For a sniffed domain on a literal destination that means
//     the SAME conservative answer in both modes - the address really in use wins over a CDN answer
//     the local resolver happened to return - rather than `match_only` quietly inventing a second
//     meaning for the same rule.
//  2. MatchAddresses, what `resolve` with `match_only: true` published. It is the newest statement
//     about this target made for the purpose of matching, so it is consulted before the candidate
//     list. A later resolve that publishes dial candidates clears it (see
//     route/route.go publishResolvedAddresses), so "newest wins" holds in both directions.
//  3. DestinationAddresses, the dial candidate list. The fallback, kept so a configuration without
//     `match_only` behaves exactly as it did.
//
// Returning the forms separately keeps the literal path free of allocation and keeps the candidate
// path identical to the one it replaces (`common.Any` over the list), which is what these
// conditions did before the third source existed.
func destinationMatchSource(metadata *adapter.InboundContext) (exact netip.Addr, candidates []netip.Addr) {
	if metadata.Destination.IsIP() {
		return metadata.Destination.Addr, nil
	}
	if len(metadata.MatchAddresses) > 0 {
		return netip.Addr{}, metadata.MatchAddresses
	}
	if len(metadata.DestinationAddresses) > 0 {
		return netip.Addr{}, metadata.DestinationAddresses
	}
	return netip.Addr{}, nil
}
