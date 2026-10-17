package dialer

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// The ambient half of the bypass decision, which had no test at all until now.
//
// # Why it matters as much as the options
//
// `NetworkPolicyBlockers` asks whether the NETWORK MANAGER would make the userspace socket do something a
// platform connect does not: a default bind, a routing mark, or interface selection. A direct outbound
// with no dial options of its own still inherits those, so a profile that only read the outbound's
// configuration would report "equivalent to a plain connect" for a socket that is bound to an interface.
//
// It is the second model of an ambient policy, so it needs the same treatment the option table gets: every
// condition that refuses is enumerated, and the one that deliberately does NOT refuse is enumerated too -
// an unstated exclusion is indistinguishable from a forgotten one.

// ambientPolicyManager is a manager that reports a configuration without being one.
type ambientPolicyManager struct {
	adapter.NetworkManager
	options    adapter.NetworkOptions
	autoDetect bool
}

func (m *ambientPolicyManager) DefaultOptions() adapter.NetworkOptions { return m.options }
func (m *ambientPolicyManager) AutoDetectInterface() bool              { return m.autoDetect }

// TestEveryAmbientPolicyConditionRefuses is the enumeration.
func TestEveryAmbientPolicyConditionRefuses(t *testing.T) {
	strategy := C.NetworkStrategyDefault
	for _, testCase := range []struct {
		name    string
		options adapter.NetworkOptions
	}{
		{name: "bind interface", options: adapter.NetworkOptions{BindInterface: "en0"}},
		{name: "routing mark", options: adapter.NetworkOptions{RoutingMark: 1}},
		{name: "network strategy", options: adapter.NetworkOptions{NetworkStrategy: &strategy}},
		{name: "network type", options: adapter.NetworkOptions{NetworkType: []C.InterfaceType{C.InterfaceTypeWIFI}}},
		{name: "fallback network type", options: adapter.NetworkOptions{FallbackNetworkType: []C.InterfaceType{C.InterfaceTypeCellular}}},
		{name: "fallback delay", options: adapter.NetworkOptions{FallbackDelay: time.Second}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := &ambientPolicyManager{options: testCase.options}
			require.Equal(t, BlockerGlobalNetworkPolicy, NetworkPolicyBlockers(manager),
				"%s changes the socket the userspace path would use, and a platform connect performs none "+
					"of it - so the fast path must refuse", testCase.name)
		})
	}
}

// TestNoAmbientPolicyStillAllowsTheBypass is the control: a manager with nothing configured must not
// refuse, or every flow would be refused for a policy nobody set.
func TestNoAmbientPolicyStillAllowsTheBypass(t *testing.T) {
	require.Equal(t, BlockerNone, NetworkPolicyBlockers(nil),
		"a dialer built without a manager has no ambient policy to lose")
	require.Equal(t, BlockerNone, NetworkPolicyBlockers(&ambientPolicyManager{}),
		"and neither does a manager with nothing configured")
}

// TestAutomaticBindingIsDeliberatelyNotABlocker pins the documented exclusion.
//
// `auto_detect_interface` installs an auto-detect bind and an auto-redirect mark wrapper on every
// userspace socket without appearing in NetworkOptions. Refusing on their presence would disable the fast
// path for the default configuration of a TUN deployment; they differ from a platform connect only where
// the route table would pick a non-default interface, which is a topology this test cannot model.
//
// The assertion is the decision. If someone later decides the other way - because they measured the
// redirect path on a multi-homed host - this test fails and the comment above NetworkPolicyBlockers
// explains what has to be re-argued.
func TestAutomaticBindingIsDeliberatelyNotABlocker(t *testing.T) {
	manager := &ambientPolicyManager{autoDetect: true}
	require.True(t, manager.AutoDetectInterface(), "the fixture must be the automatic shape")
	require.Equal(t, BlockerNone, NetworkPolicyBlockers(manager),
		"automatic binding is not part of the CONFIGURED ambient policy this function judges")

	// And an explicit one, on the same manager, still refuses - the two are different facts.
	manager.options.BindInterface = "en0"
	require.Equal(t, BlockerGlobalNetworkPolicy, NetworkPolicyBlockers(manager),
		"an explicit bind is exactly what the function exists for")
}
