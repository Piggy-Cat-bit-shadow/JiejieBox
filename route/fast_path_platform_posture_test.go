package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The implicit Direct Fast Path is compiled out where the TUN engine cannot act on its verdict
// (see implicitDirectFastPathIsExecutable). Two properties have to hold together, and they are
// tested together because either one alone would be a plausible-looking mistake:
//
//   - the guard is exactly the iOS platform check, so the mechanism is not disabled everywhere and
//     is not widened to C.IsDarwin, which includes iOS and would silently drop macOS;
//   - on a build where the guard is live the mechanism still fires, and where the guard is
//     compiled out it must not be consulted at all - not consulted and ignored.
//
// The second half is asserted through the same call the production path makes, so on an iOS test
// target it becomes the executable proof that the eligibility gate is gone rather than neutralised.
// On every other target the same assertion is the proof that the subtraction did not leak into
// platforms that can honour the verdict.
//
// The remaining iOS evidence is compile-time and cannot be produced by a unit test: the iOS build
// of (*Router).preMatchFlow contains no call to resolveFastPathRequest or canFastBypass.
// scripts/ci/verify-ios-fast-path-guard.sh compiles the package for GOOS=ios and fails if either
// call comes back, which is what keeps this contract enforced instead of merely documented.
func TestTheImplicitFastPathGuardIsExactlyTheIOSPlatformCheck(t *testing.T) {
	require.Equal(t, !C.IsIos, implicitDirectFastPathIsExecutable,
		"the guard must stay !C.IsIos: C.IsDarwin includes iOS, so using it would also disable "+
			"the mechanism on macOS, where the verdict is still honoured")
	if !C.IsIos {
		require.True(t, implicitDirectFastPathIsExecutable,
			"this test binary is not an iOS build, so the implicit Direct Fast Path must stay live")
	}
}

// TestTheImplicitFastPathFiresExactlyWhereTheGuardAllowsIt drives the eligibility decision through
// preMatchFlow - the production entry point - and asserts the platform-appropriate outcome.
func TestTheImplicitFastPathFiresExactlyWhereTheGuardAllowsIt(t *testing.T) {
	router, outbound := eligibleRouter(t)
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")

	if implicitDirectFastPathIsExecutable {
		require.Equal(t, adapter.PreMatchBypass, result.Action,
			"where the verdict can be honoured, a plain direct TUN flow must still take the fast path")
		require.EqualValues(t, 1, outbound.calls.Load(),
			"the eligibility gate must still consult the outbound on this platform")
		return
	}

	// The compiled-out case. A transparent chain in front of direct is the strongest shape the
	// removed authorisation could have produced - it is authorised by the intrinsic grant alone -
	// so if anything could still reach the gate, this flow would.
	require.NotEqual(t, adapter.PreMatchBypass, result.Action,
		"a platform that cannot honour the verdict must not produce one")
	require.Zero(t, outbound.calls.Load(),
		"the eligibility gate must not be consulted at all where its verdict is discarded")
}

// TestTheExplicitBypassRuleSurvivesThePlatformGuard pins the boundary of the subtraction: an
// operator's `action: bypass` rule is produced by the rule loop without consulting the implicit
// decision, and must keep its meaning on every platform, iOS included.
func TestTheExplicitBypassRuleSurvivesThePlatformGuard(t *testing.T) {
	router, _, destination := optionsFixture(t)
	router.rules = []adapter.Rule{&fixedRule{action: &R.RuleActionBypass{}}}

	result := router.PreMatch(fastBypassMetadata(N.NetworkTCP, destination), nil)
	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"an explicit bypass action is an operator instruction; the platform guard may not touch it")
}
