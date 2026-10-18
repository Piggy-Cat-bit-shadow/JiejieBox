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
	// HostsPublishesIPv6 reports whether a hosts server could answer AAAA. It is true for any hosts
	// server, because the transport always loads a file - the configured paths, or the SYSTEM hosts
	// file when `path` is empty, and it loads the latter even when `predefined` entries are also set
	// - and deciding whether that file carries an AAAA record would mean reading and scanning it
	// here. See hostsServerMayAnswerAAAA.
	HostsPublishesIPv6 bool
	// DNSCanAnswerAAAA reports whether this configuration has any DNS server of its own. The DNS
	// strategy is deliberately not consulted: `ipv4_only` narrows the address families a LOOKUP
	// asks for, and an application that asks for AAAA itself is still answered.
	DNSCanAnswerAAAA bool
}

// Required reports whether any application-facing surface of this configuration can present IPv6.
//
// False is the fast path: the entries keep their configured behaviour, no network observation is
// made for this feature, no decision is taken and nothing is committed. Besides the empty
// configuration, it is the answer for a box with no TUN inbound at all: the application-facing DNS
// gate is keyed on that inbound as the query source, so such a box has no entry this process
// controls. See ResolveIPv6EntryDemand.
func (d IPv6EntryDemand) Required() bool {
	// The two DNS-side flags are subsets of DNSCanAnswerAAAA today - a Fake-IP or hosts server is a
	// DNS server - so this disjunction is really "the TUN presents IPv6, or there is a resolver".
	// They are kept as separate inputs because they name WHICH surface, which is what a later
	// per-surface refinement would key on ("this resolver is Fake-IP only", "this hosts server is
	// file-only"), and because the tests assert each one independently.
	return d.TUNPresentsIPv6 || d.FakeIPAllocatesIPv6 || d.HostsPublishesIPv6 || d.DNSCanAnswerAAAA
}

// ResolveIPv6EntryDemand summarises the assembled configuration.
//
// It walks the inbound list and the DNS server list once, at construction, and allocates nothing for
// a configuration that has neither. The work is proportional to the number of inbounds and DNS
// servers - tens, not the rule count, the node count or the hosts table.
//
// # Why a configuration without a TUN has no demand at all
//
// The two consumers this summary exists for are the TUN's effective options and the
// application-facing DNS answer, and the second one is keyed on the query's SOURCE being that TUN
// (see dns/entry_ipv6.go). A proxy-only box therefore has no entry this process can suppress: a
// `hijack_dns` query from a socks inbound is deliberately not gated, so committing a decision for it
// would make the box pay for an observation that nothing consumes. So the DNS-side surfaces are only
// counted once a TUN inbound exists, and a configuration with no TUN takes the fast path.
func ResolveIPv6EntryDemand(options Options) IPv6EntryDemand {
	var (
		demand IPv6EntryDemand
		hasTun bool
	)
	for _, inbound := range options.Inbounds {
		if inbound.Type != C.TypeTun {
			continue
		}
		tunOptions, isTun := inbound.Options.(*TunInboundOptions)
		if !isTun || tunOptions == nil {
			continue
		}
		hasTun = true
		if tunInboundOptionsPresentIPv6(tunOptions) {
			demand.TUNPresentsIPv6 = true
		}
	}
	if !hasTun {
		return IPv6EntryDemand{}
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

// TunReducibleToIPv4Only reports whether a TUN with these addresses can be presented as IPv4 only
// without breaking it.
//
// # It is the ONE safety rule, shared by the two layers that need it
//
// The box-level pre-check (AllTunInboundsReducibleToIPv4Only) decides whether ANY tun may be
// reduced; the reduction itself (protocol/tun/effective_ipv6.go) applies it. A second copy of this
// rule is exactly how a split state appears: the pre-check says "safe", the reduction refuses for a
// reason the pre-check did not model, and one interface is stripped while the shared decision is
// rolled back - or, worse, the reverse. Both callers therefore call this function.
//
//	no IPv4 address           an interface must keep at least one address
//	DNS list with no IPv4     the hijack resolver list would have no server left
//
// A TUN with no address at all is not reducible either: there is no interface to open.
func TunReducibleToIPv4Only(inet4Addresses []netip.Prefix, dnsAddresses []netip.Addr) bool {
	if len(inet4Addresses) == 0 {
		return false
	}
	if len(dnsAddresses) == 0 {
		return true
	}
	for _, address := range dnsAddresses {
		if address.Is4() {
			return true
		}
	}
	return false
}

// AllTunInboundsReducibleToIPv4Only reports whether EVERY TUN inbound in this configuration can be
// reduced to IPv4 only.
//
// # Why the box needs this before it decides anything
//
// The entry decision is shared by the whole box (one TUN address space, one DNS answer, one Fake-IP
// store), so it can only be "suppress" if every interface it describes can actually be reduced.
// Deciding per TUN and rolling the shared value back afterwards is order-dependent and leaves a
// window in which one interface is stripped and the DNS answer still presents IPv6 (or the reverse).
// The pre-check is a pure function of the typed configuration - no network, no side effects - and and
// is evaluated once in box.New.
//
// It returns true for a configuration with no TUN at all, which is the vacuously-correct answer: the
// callers only suppress when the demand summary says a TUN exists.
func AllTunInboundsReducibleToIPv4Only(inbounds []Inbound) bool {
	for _, inbound := range inbounds {
		if inbound.Type != C.TypeTun {
			continue
		}
		tunOptions, isTun := inbound.Options.(*TunInboundOptions)
		if !isTun || tunOptions == nil {
			// A tun inbound whose options cannot be read is not evidence that it can be reduced.
			// Returning false keeps the box on "present", which cannot strip anything.
			return false
		}
		inet4Addresses := make([]netip.Prefix, 0, len(tunOptions.Address))
		for _, prefix := range tunOptions.Address {
			if prefix.Addr().Is4() {
				inet4Addresses = append(inet4Addresses, prefix)
			}
		}
		if !TunReducibleToIPv4Only(inet4Addresses, tunOptions.DNSAddress) {
			return false
		}
	}
	return true
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
// It is always true, and that is the finding rather than an oversight: the transport loads at least
// one FILE, and when `path` is empty it loads the SYSTEM hosts file even if `predefined` entries are
// also configured (dns/transport/hosts.NewTransport). Deciding otherwise would mean reading and
// scanning that file at configuration time - the per-configuration cost this round is required not
// to introduce. The unified DNS entry still decides at query time, so a hosts file with no IPv6
// content costs one comparison per AAAA query and nothing at startup.
//
// The function exists rather than a constant so the summary continues to name the SURFACE: when a
// future hosts transport can prove the answer from compiled metadata, it says so here without
// touching a caller.
func hostsServerMayAnswerAAAA(_ *HostsDNSServerOptions) bool {
	return true
}

func anyIPv6Prefix(prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		address := prefix.Addr()
		if !address.IsValid() {
			continue
		}
		// Is6, NOT Unmap().Is6(), and the difference is load-bearing: it is the SAME predicate the
		// TUN splits these lists with (protocol/tun/inbound.go filters Is4 and Is6), so a
		// four-in-six prefix is a demand here because it becomes an Inet6Address there. Using the
		// un-mapped form would classify it as IPv4 and miss a TUN that really does install IPv6.
		if address.Is6() {
			return true
		}
	}
	return false
}

func anyIPv6Addr(addresses []netip.Addr) bool {
	for _, address := range addresses {
		if address.IsValid() && address.Is6() {
			return true
		}
	}
	return false
}
