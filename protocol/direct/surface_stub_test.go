package direct

import (
	"net/netip"

	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
)

// Test doubles for the two OPTIONAL capabilities of an outbound's dial surface.
//
// # Why these exist as separate types rather than closures injected into fields
//
// The outbound used to hold `familyStrategy func() ...` and `localFamily func(netip.Addr) bool`, captured
// at construction off the chain by type assertion. A test could therefore set the answer it wanted without
// ever producing an object that has the capability - so the fixture could express a state the production
// graph cannot reach, and the assertion "the constructor wires this" was worth nothing.
//
// The surface holds the INTERFACES the chain implements, so a test that wants a verdict has to supply an
// object that answers the interface. That is more ceremony and it is the point: the same goes for the
// production constructor.
//
// The embedded interface is nil on purpose. These doubles are used by tests about the bypass DECISION,
// which never dials; a call to an unimplemented method panicking is the correct behaviour for a double that
// was asked to do something the test did not set up.
type stubResolveDialer struct {
	dialer.ResolveDialer
	strategy func() C.DomainStrategy
}

func (d stubResolveDialer) EffectiveFamilyStrategy() C.DomainStrategy {
	if d.strategy == nil {
		return C.DomainStrategyAsIS
	}
	return d.strategy()
}

type stubLocalFamilyDialer struct {
	dialer.LocalFamilyDialer
	blocked func(netip.Addr) bool
}

func (d stubLocalFamilyDialer) LocalFamilyBlocked(address netip.Addr) bool {
	if d.blocked == nil {
		return false
	}
	return d.blocked(address)
}
