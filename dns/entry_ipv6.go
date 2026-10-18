package dns

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"

	mDNS "github.com/miekg/dns"
)

// This file is the DNS half of the entry IPv6 decision.
//
// # Why the gate is here, and not in the TUN
//
// The application-facing DNS question is asked in exactly one place that every transport shares:
// `prepareExchange`, the entry of both `Exchange` and `ExchangeAsync`. Putting the refusal there -
// and only there - means:
//
//   - it precedes rule matching, the Hosts transport, the Fake-IP store, every upstream resolver,
//     the shared cache and the reverse mapping, so a suppressed query creates no mapping, writes no
//     cache entry and dials nothing;
//   - it is reached by the UDP hijack and the TCP hijack alike, because both hand their query to the
//     same router entry;
//   - it cannot drift between the synchronous and the asynchronous interface, which are two
//     wrappers over the same preparation.
//
// `Lookup` is deliberately NOT gated. It is the resolver a dialer uses for a destination of its own
// choosing, and that destination may be reached through a remote proxy: this machine's lack of
// public IPv6 says nothing about whether a proxy can carry it. The same reason keeps the gate out
// of `dns.Router.Exchange` as a whole - the router is also the resolver of the proxy's own domain,
// of internal services and of the DNS transports themselves, and none of those is an application
// being told what this machine can do.

// applicationEntrySuppressesIPv6 reports whether this query is asked by a local application through
// an entry that has committed an IPv4-only presentment.
//
// # Why the source test is `InboundType == tun`
//
// The TUN is the only entry that speaks for the whole machine: every application's DNS goes through
// it without the application choosing anything, and the same decision governs the interface address
// it receives. The other callers of the hijack path are not that:
//
//   - a WireGuard, MASQUE, OpenVPN or Tailscale endpoint forwards DNS for a REMOTE peer, whose
//     connectivity is that peer's, not this machine's;
//   - a `hijack_dns` rule action on a proxy inbound is reached because the application deliberately
//     sent DNS into a proxy, and the address it gets back is one that proxy can carry. Suppressing
//     it would break exactly the "remote egress can still reach IPv6" case this round is required
//     not to break;
//   - an internal exchange (ECH, resolver discovery, a DNS transport) carries no inbound metadata at
//     all.
//
// So the test is a positive identification of the local entry, not a negative one: anything this
// file does not recognise keeps its previous behaviour.
func (r *Router) applicationEntrySuppressesIPv6(ctx context.Context) bool {
	if r.entryIPv6Presentment == nil {
		return false
	}
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil || metadata.InboundType != C.TypeTun {
		return false
	}
	return r.entryIPv6Presentment.IPv6EntryPresentment() == netcap.SuppressIPv6
}

// appIPv4OnlyNoData is the protocol-correct answer for an AAAA question on a machine that presents
// IPv4 only: NOERROR with no answer records.
//
// # Why NODATA and not NXDOMAIN
//
// The name may well exist and may well have an IPv4 address; the question is about a record type
// this machine has decided not to present. NXDOMAIN would say the name does not exist, poison the
// application's negative cache for the name rather than the type, and break the A lookups that are
// supposed to keep working.
//
// # What it deliberately does not carry
//
// No authority section: an SOA is the negative-cache TTL mechanism of a real zone, and inventing one
// here would claim authority this process does not have over the name. No OPT record is added
// either - the response is not truncated, and the request's own EDNS0 shape is not echoed because
// nothing here negotiated with an upstream. The header fields mirror the router's other local
// early answers so a client cannot tell them apart from a resolver's empty response.
func appIPv4OnlyNoData(message *mDNS.Msg) *mDNS.Msg {
	return &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{
			Id:                 message.Id,
			Response:           true,
			RecursionDesired:   message.RecursionDesired,
			RecursionAvailable: true,
			Rcode:              mDNS.RcodeSuccess,
		},
		Question: message.Question,
	}
}
