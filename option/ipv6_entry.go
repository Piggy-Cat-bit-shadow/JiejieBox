package option

import (
	"net/netip"

	C "github.com/sagernet/sing-box/constant"
)

// This file is the CONFIGURATION half of the entry IPv6 decision.
//
// # What it decides, and what it deliberately does not
//
// It answers one question: does this configuration have any application-facing surface that can
// present IPv6 at all? That answer decides whether the new adaptive entry system does ANY work - an
// interface enumeration and a route-table read at startup - or takes a fast path that touches
// nothing. It is a demand summary, not a policy: it never says whether IPv6 is available, never
// looks at the network, and never overrides what the user configured. The runtime decision (and its
// network evidence) lives in common/netcap and route.
//
// # Why typed options and not a text search
//
// A configuration that never mentions "ipv6" still presents IPv6: a TUN whose address list is
// written with a unique-local prefix, a Fake-IP server that only sets inet6_range, a hosts table
// with AAAA entries, a DNS server that answers AAAA because nobody set a strategy. Conversely a
// configuration can mention an IPv6 address that no application ever sees (a proxy server's own
// address). So the check reads the assembled typed options, which is what the entries are actually
// built from.
//
// # Why it is allowed to say yes too often
//
// A false positive costs one observation at startup and enables a decision that is still subject to
// the runtime evidence; a false negative means the entries never considered the question at all. The
// asymmetry is why the hosts file case below is "configured" rather than "contains an AAAA record":
// proving the latter would mean reading and scanning a file at configuration time, which is exactly
// the per-configuration cost this round is required not to add.
type IPv6EntryDemand struct {
	// TUNPresentsIPv6 reports whether a TUN inbound in this configuration could declare IPv6 to
	// applications: an IPv6 address, an IPv6 route or exclude route, an IPv6 DNS address, an IPv6
	// loopback address, or a rule-set that contributes IPv6 routes at start.
	TUNPresentsIPv6 bool
	// FakeIPAllocatesIPv6 reports whether a Fake-IP server is configured with an IPv6 range, so an
	// AAAA query can be answered with a synthetic IPv6 address.
	FakeIPAllocatesIPv6 bool
	// HostsPublishesIPv6 reports whether a hosts server could answer AAAA: a predefined entry
	// carrying an IPv6 address, or a hosts FILE, whose contents are not read here.
	HostsPublishesIPv6 bool
	// DNSCanAnswerAAAA reports whether this configuration has any DNS server of its own. The DNS
	// strategy is deliberately not consulted: `ipv4_only` narrows the address families a LOOKUP
	// asks for, and an application that asks for AAAA itself is still answered.
	DNSCanAnswerAAAA bool
}

// Required reports whether any application-facing surface of this configuration can present IPv6.
//
// False is the fast path: the entries keep their configured behaviour, no network observation is
// made for this feature, no decision is taken and nothing is committed. It is the answer for a
// configuration with no TUN, no DNS server of its own and therefore no application-facing surface
// this process controls.
func (d IPv6EntryDemand) Required() bool {
	return d.TUNPresentsIPv6 || d.FakeIPAllocatesIPv6 || d.HostsPublishesIPv6 || d.DNSCanAnswerAAAA
}

// ResolveIPv6EntryDemand summarises the assembled configuration.
//
// It walks the inbound list and the DNS server list once, at construction, and allocates nothing for
// a configuration that has neither. The work is proportional to the number of inbounds and DNS
// servers - tens, not the rule count, the node count or the hosts table.
func ResolveIPv6EntryDemand(options Options) IPv6EntryDemand {
	var demand IPv6EntryDemand
	for _, inbound := range options.Inbounds {
		if inbound.Type != C.TypeTun {
			continue
		}
		tunOptions, isTun := inbound.Options.(*TunInboundOptions)
		if !isTun || tunOptions == nil {
			continue
		}
		if tunInboundOptionsPresentIPv6(tunOptions) {
			demand.TUNPresentsIPv6 = true
		}
	}
	if options.DNS == nil {
		return demand
	}
	demand.DNSCanAnswerAAAA = len(options.DNS.Servers) > 0
	for _, server := range options.DNS.Servers {
		switch server.Type {
		case C.DNSTypeFakeIP:
			fakeIPOptions, isFakeIP := server.Options.(*FakeIPDNSServerOptions)
			if !isFakeIP || fakeIPOptions == nil {
				continue
			}
			if fakeIPOptions.Inet6Range != nil && fakeIPOptions.Inet6Range.Build(netip.Prefix{}).IsValid() {
				demand.FakeIPAllocatesIPv6 = true
			}
		case C.DNSTypeHosts:
			hostsOptions, isHosts := server.Options.(*HostsDNSServerOptions)
			if !isHosts || hostsOptions == nil {
				continue
			}
			if hostsServerMayAnswerAAAA(hostsOptions) {
				demand.HostsPublishesIPv6 = true
			}
		}
	}
	return demand
}

// tunInboundOptionsPresentIPv6 reports whether these TUN options could carry an IPv6 fact to the
// platform, the stack or an application.
//
// `auto_route` alone is not one: every stack in this tree derives the automatic IPv6 route range
// from the address list, so a TUN with no IPv6 address installs no IPv6 route however auto_route is
// set. The route-set entries ARE one, because the sets are appended to the effective route list at
// start and their contents are not known here.
func tunInboundOptionsPresentIPv6(options *TunInboundOptions) bool {
	if len(options.RouteAddressSet) > 0 || len(options.RouteExcludeAddressSet) > 0 {
		return true
	}
	if anyIPv6Prefix(options.Address) ||
		anyIPv6Prefix(options.RouteAddress) ||
		anyIPv6Prefix(options.RouteExcludeAddress) {
		return true
	}
	return anyIPv6Addr(options.DNSAddress) || anyIPv6Addr(options.LoopbackAddress)
}

// hostsServerMayAnswerAAAA reports whether a hosts server could answer an AAAA question.
//
// A configured FILE is reported as "may", because deciding otherwise would mean reading and scanning
// it here - the expensive per-configuration work this round is required not to introduce. The
// unified DNS entry still decides at query time whether the answer is presented, so a hosts file
// with no IPv6 content costs one comparison per AAAA query and nothing at startup.
func hostsServerMayAnswerAAAA(options *HostsDNSServerOptions) bool {
	if len(options.Path) > 0 {
		return true
	}
	if options.Predefined == nil {
		return false
	}
	for _, entry := range options.Predefined.Entries() {
		if anyIPv6Addr(entry.Value) {
			return true
		}
	}
	return false
}

func anyIPv6Prefix(prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		address := prefix.Addr()
		if address.IsValid() && address.Unmap().Is6() {
			return true
		}
	}
	return false
}

func anyIPv6Addr(addresses []netip.Addr) bool {
	for _, address := range addresses {
		if address.IsValid() && address.Unmap().Is6() {
			return true
		}
	}
	return false
}
