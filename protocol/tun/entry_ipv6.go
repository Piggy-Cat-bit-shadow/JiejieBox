package tun

import (
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common"
)

// This file is the inbound's half of the entry IPv6 decision: how it consumes the decision the
// network manager took at start, and the one case in which it has to correct it.
//
// The decision itself is netcap's (ProjectIPv6Presentment), the observation and publication are the
// network manager's (route/network_family_entry.go), and the option surgery is effective_ipv6.go.
// What is here is only the application: the inbound reads the committed value, reduces the options
// while the interface does not exist yet, and every consumer of those options - the stack, the
// platform client, the auto-redirect rules and the route-set prefixes appended later in Start - then
// sees one consistent interface.

// applyEntryIPv6Presentment applies the committed entry decision to this interface's options.
//
// # Why the inbound reads the decision instead of taking it
//
// The decision also governs the application-facing DNS answer, and that answer must be consistent
// with an interface that may not present IPv6 at all - a configuration whose TUN is IPv4-only can
// still hand applications a synthetic AAAA through Fake-IP. Taking the decision per entry would let
// the TUN and the DNS router disagree, and it would double the observation at every start. The
// manager takes it once; every entry consumes it.
//
// # What it does when the decision is "present", or absent
//
// Nothing. The options are exactly the configured ones, no field is touched and no log line is
// emitted, which is the behaviour of every build before this round.
//
// # The correction
//
// `applyIPv4OnlyPresentment` can refuse: an IPv6-only interface cannot be reduced without deleting
// its only address, and a hijack DNS address list with no IPv4 entry cannot be reduced without
// leaving applications without a resolver. In that case the configured IPv6 stays, and the recorded
// decision is corrected to "present" - a DNS entry suppressing AAAA beside an interface that still
// advertises IPv6 is the split this pipeline exists to prevent. The correction is the second and
// last write; the manager's commit runs first, from its own Start state, which the box's start
// sequence guarantees happens before any inbound starts.
func (t *Inbound) applyEntryIPv6Presentment() {
	presenter, isPresenter := t.networkManager.(adapter.IPv6EntryPresenter)
	if !isPresenter {
		// A manager that cannot report the decision keeps the previous behaviour exactly: the
		// options are untouched and nothing is recorded.
		return
	}
	if presenter.IPv6EntryPresentment() != netcap.SuppressIPv6 {
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
	// The reduction was refused, so this interface keeps presenting IPv6. Say so, or the DNS entry
	// would suppress AAAA for a family the interface is still advertising.
	t.logger.Warn("ipv6 entry presentment: keeping the configured ipv6 options because they cannot ",
		"be reduced to a working ipv4-only interface (an ipv6-only interface, or a hijack dns ",
		"address list with no ipv4 entry); applications will be presented with ipv6 as configured")
	presenter.CommitIPv6EntryPresentment(netcap.PresentIPv6)
}
