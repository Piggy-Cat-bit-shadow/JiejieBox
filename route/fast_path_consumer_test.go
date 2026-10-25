package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The implicit Direct Fast Path chain is computed for exactly one caller: the entry whose transport
// applies a Bypass verdict - sing-tun's nfqueue consumer, which is what auto_redirect uses. Every
// other entry (the go, gVisor and system TUN stacks) installs the verdict as an accept entry and
// cannot act on it, so for them the authorisation scan, the keyword match and the twenty refusal
// reasons in canFastBypass could not change where the bytes go. See
// adapter.InboundContext.ConsumesBypassVerdict.
//
// These tests pin both halves on any host, without a platform constant and without an iOS binary:
// without the capability the gate is not consulted at all - not consulted and discarded, which the
// counting outbound makes observable - and with it the behaviour is what it was before this round.

func TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer(t *testing.T) {
	router, outbound := eligibleRouter(t)
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	metadata.ConsumesBypassVerdict = false

	result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")

	require.NotEqual(t, adapter.PreMatchBypass, result.Action,
		"a caller that cannot apply a Bypass verdict must not be given one")
	require.Zero(t, outbound.calls.Load(),
		"and the eligibility chain must not be consulted at all - not consulted and discarded")
}

func TestTheImplicitFastPathIsComputedForABypassConsumer(t *testing.T) {
	// The control for the test above: the same flow, with the capability set, still takes the fast
	// path. Without it, "not computed without a consumer" would also pass if the mechanism had been
	// deleted outright.
	router, outbound := eligibleRouter(t)
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")

	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"the auto-redirect entry must still receive the verdict it applies")
	require.EqualValues(t, 1, outbound.calls.Load())
}

// The capability gates the IMPLICIT decision only. An explicit `action: bypass` rule is produced by
// the rule loop without consulting canFastBypass, so it keeps its meaning for a caller that cannot
// apply the verdict just as much as for one that can.
func TestAnExplicitBypassRuleIsIndependentOfABypassConsumer(t *testing.T) {
	router, _, destination := optionsFixture(t)
	router.rules = []adapter.Rule{&fixedRule{action: &R.RuleActionBypass{}}}

	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	metadata.ConsumesBypassVerdict = false
	result := router.PreMatch(metadata, nil)
	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"an explicit bypass action is an operator instruction; the consumer capability may not remove it")
}
