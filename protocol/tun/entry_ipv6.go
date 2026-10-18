package tun

import (
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common"
)

// This file is the inbound's half of the entry IPv6 decision: how it consumes the decision the
// network manager took at start.
//
// The decision itself is netcap's (ProjectIPv6Presentment), the observation and publication are the
// network manager's (route/network_family_entry.go), and the option surgery is effective_ipv6.go.
// What is here is only the application: the inbound reads the committed value, reduces the options
// while the interface does not exist yet, and every consumer of those options - the stack, the
// platform client, the auto-redirect rules and the route-set prefixes appended later in Start -
// then sees one consistent interface.

// applyEntryIPv6Presentment applies the committed entry decision to this interface's options.
//
// # Why the inbound only READS the decision
//
// The decision is shared by the whole box - one TUN address space, one DNS answer, one Fake-IP store
// - so it is taken once, in the network manager, where the box also proved that every configured TUN
// can honour it (option.AllTunInboundsReducibleToIPv4Only). An earlier design let each TUN correct
// the shared value when its own reduction was refused; that was order-dependent and produced exactly
// the split it was meant to prevent: the first interface stripped, the second rolling the value back
// to "present" for everyone.
//
// # The refusal that should not happen
//
// `applyIPv4OnlyPresentment` keeps its guard, because the effective options are the last word on what
// this interface will be. With the box pre-check in place a refusal here means the two predicates
// disagreed - a defect, not a configuration. The direction taken is the safe one: the interface keeps
// its configured IPv6 (there is nothing else to do at this point) and the shared decision is NOT
// rolled back, so the DNS answer stays conservative rather than presenting an IPv6 this interface
// was just stripped of. It is logged at Error so the disagreement cannot pass silently.
func (t *Inbound) applyEntryIPv6Presentment() {
	reader, isReader := t.networkManager.(adapter.IPv6EntryPresentmentReader)
	if !isReader {
		// A manager that cannot report the decision keeps the previous behaviour exactly: the
		// options are untouched.
		return
	}
	if reader.IPv6EntryPresentment() != netcap.SuppressIPv6 {
		return
	}
	if applyIPv4OnlyPresentment(&t.tunOptions) {
		t.entryIPv4Only = true
		// The DNS hijack addresses are derived from the addresses, so one of them is now an address
		// this interface does not have. They are only used to recognise DNS traffic, and leaving a
		// dead IPv6 entry there would be a claim about an address the interface no longer carries.
		t.dnsHijackAddress = common.Filter(t.dnsHijackAddress, netip.Addr.Is4)
		t.logger.Info("ipv6 entry presentment: this interface is opened ipv4-only ",
			"(ipv6 addressing, routes and the ipv6 dns hijack address are removed from the effective ",
			"options; the decision holds until the box is restarted)")
		return
	}
	t.logger.Error("ipv6 entry presentment: the box committed an ipv4-only presentment but this ",
		"interface cannot be reduced (no ipv4 address, or a hijack dns address list with no ipv4 ",
		"entry); keeping the configured ipv6 on this interface and leaving the shared decision ",
		"unchanged. The pre-check and the reduction disagree, which is a defect")
}
