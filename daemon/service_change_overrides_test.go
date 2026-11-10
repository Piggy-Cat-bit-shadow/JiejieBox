package daemon

// D — the effective configuration includes the run-only overrides, so a preview has to be computed
// with them.
//
// `parseEffectiveOptions` merges the platform's TUN patch into the document before anything is
// compared. A preview that parsed the candidate WITHOUT that patch compared two documents that
// differ by an input it never saw: the body is byte-identical to the running one, and the plan says
// the service must restart. The fix is for the plan's default input to be the overrides the running
// document was actually built from, while an explicit override (an embedder previewing a settings
// change) still wins.
//
// The plan also has to say what it CANNOT see. The platform installs its own network settings when
// it opens the TUN - included and excluded routes, the APNs exclusion, the proxy - and no document
// comparison observes them. Without that statement "already runs this document" reads as "nothing
// would change", which is false for an APNs switch.

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// tunProfile is a document with the one TUN inbound the overrides patch.
const tunProfile = `{
  "log": {"level": "error"},
  "inbounds": [{"type": "tun", "tag": "tun-in", "auto_route": true}]
}`

// planningContext is the service context plus the TUN inbound type.
//
// The daemon test harness registers no protocol inbounds - its profiles carry none - and the tun
// type is registered through an explicit call rather than an import side effect. The CONSTRUCTOR is
// never called here: a plan decodes the document and compares it, it does not build a Box, and a
// Box would need a platform to open a TUN.
func planningContext(t *testing.T) context.Context {
	t.Helper()
	ctx := libboxContext(t)
	registry, isRegistry := service.FromContext[adapter.InboundRegistry](ctx).(*inbound.Registry)
	require.True(t, isRegistry)
	inbound.Register[option.TunInboundOptions](registry, "tun",
		func(context.Context, adapter.Router, log.ContextLogger, string, option.TunInboundOptions) (adapter.Inbound, error) {
			return nil, E.New("the planning tests never build a TUN inbound")
		})
	return ctx
}

// serviceRunningWithOverrides installs a running document built with these overrides, without
// building a real Box: the planner only reads the document, the revision and the override record.
func serviceRunningWithOverrides(t *testing.T, profile string, overrides *OverrideOptions, revision string) *StartedService {
	t.Helper()
	service := NewStartedService(ServiceOptions{Context: planningContext(t)})
	t.Cleanup(service.Close)
	document, err := service.parseEffectiveOptions(context.Background(), profile, overrides)
	require.NoError(t, err)
	instance := &Instance{options: document, revision: revision}
	if overrides != nil {
		instance.overrideOptions = *overrides
		instance.hasOverrides = true
	}
	service.serviceAccess.Lock()
	service.instance = instance
	service.serviceStatus.Status = ServiceStatus_STARTED
	service.serviceAccess.Unlock()
	return service
}

// TestAPreviewUsesTheOverridesTheRunningDocumentWasBuiltWith is the defect: the same body, the
// same effective document, no restart.
func TestAPreviewUsesTheOverridesTheRunningDocumentWasBuiltWith(t *testing.T) {
	loopback := OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}}
	service := serviceRunningWithOverrides(t, tunProfile, &loopback, configRevisionOf(tunProfile))

	plan, err := service.PlanServiceChange(tunProfile, nil)
	require.NoError(t, err)
	require.True(t, plan.Unchanged,
		"the same body with the same overrides is the running document, not a change")
	require.False(t, plan.RestartRequired)
	require.Contains(t, plan.OverridesConsidered, "loopback_address",
		"the plan says which override inputs it was computed with")
	require.Contains(t, plan.UncoveredInputs, "platform network settings",
		"the platform's own settings are outside every document comparison")
}

// TestAnExplicitOverrideBeatsTheRunningOne keeps the other direction: an embedder previewing a
// settings change passes its own overrides, and the answer is about THOSE.
func TestAnExplicitOverrideBeatsTheRunningOne(t *testing.T) {
	loopback := OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}}
	service := serviceRunningWithOverrides(t, tunProfile, &loopback, configRevisionOf(tunProfile))

	// The platform's loopback switch is now off: the document it would build differs from the
	// running one by that address, and only a rebuild reinstalls the TUN without it.
	plan, err := service.PlanServiceChange(tunProfile, &OverrideOptions{})
	require.NoError(t, err)
	require.False(t, plan.Unchanged)
	require.True(t, plan.ChangedDocument)
	require.True(t, plan.RestartRequired, "the effective document really changed")
	require.Equal(t, []string{"empty_override_set"}, plan.OverridesConsidered,
		"an empty override set is an input of its own: it clears the document fields the overrides own")
}

// TestAPreviewWithTheSameExplicitOverrideIsUnchanged proves the comparison is about the effective
// document rather than about whether the caller passed an argument at all.
func TestAPreviewWithTheSameExplicitOverrideIsUnchanged(t *testing.T) {
	loopback := OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}}
	service := serviceRunningWithOverrides(t, tunProfile, &loopback, configRevisionOf(tunProfile))

	sameOverride := OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}}
	plan, err := service.PlanServiceChange(tunProfile, &sameOverride)
	require.NoError(t, err)
	require.True(t, plan.Unchanged)
	require.Contains(t, plan.OverridesConsidered, "loopback_address")
}

// TestAProfileWithoutOverridesStillPlans shows the default is not invented: a Box built without
// overrides plans the candidate without them, exactly as before.
func TestAProfileWithoutOverridesStillPlans(t *testing.T) {
	service := serviceRunningWithOverrides(t, minimalConfig, nil, configRevisionOf(minimalConfig))

	plan, err := service.PlanServiceChange(minimalConfig, nil)
	require.NoError(t, err)
	require.True(t, plan.Unchanged)
	require.Empty(t, plan.OverridesConsidered)
	require.Contains(t, plan.UncoveredInputs, "platform network settings")
}

// TestAnEmptyOverrideSetIsItsOwnInput pins the difference the plan must be able to state: an override
// SET with every field zero clears the document fields the overrides own (`auto_redirect`), so it is
// not the same input as passing no overrides at all - and a plan that reported neither would hide
// which of the two produced it.
func TestAnEmptyOverrideSetIsItsOwnInput(t *testing.T) {
	loopback := OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}}
	service := serviceRunningWithOverrides(t, tunProfile, &loopback, configRevisionOf(tunProfile))

	plan, err := service.PlanServiceChange(tunProfile, &OverrideOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"empty_override_set"}, plan.OverridesConsidered)
	require.True(t, plan.RestartRequired, "the effective document really changed: the loopback patch is gone")
}

// TestAServiceThatIsNotStartedDoesNotLendItsOverrides closes the window between "an instance exists"
// and "a document runs": during STARTING/STOPPING there is nothing to inherit from, and labelling a
// first-start plan with a Box that is not serving anything would be a claim about the wrong object.
func TestAServiceThatIsNotStartedDoesNotLendItsOverrides(t *testing.T) {
	loopback := OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}}
	service := serviceRunningWithOverrides(t, tunProfile, &loopback, configRevisionOf(tunProfile))
	service.serviceAccess.Lock()
	service.serviceStatus.Status = ServiceStatus_STARTING
	service.serviceAccess.Unlock()

	plan, err := service.PlanServiceChange(tunProfile, nil)
	require.NoError(t, err)
	require.False(t, plan.Started, "there is no running document to compare against")
	require.Empty(t, plan.OverridesConsidered, "an instance that is not serving lends no overrides")
}
