package daemon

// C04 — the mode domain's plan and commit, checked at the boundaries a plan exists for:
// nothing to do, a live switch, a change that needs the owner rebuilt, a target that removes
// the owner, and a plan computed against another configuration revision.

import (
	"testing"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// modeDocument builds a minimal document carrying a clash_api owner.
func modeDocument(controller string, secret string, mode string) *option.Options {
	return &option.Options{
		Experimental: &option.ExperimentalOptions{
			ClashAPI: &option.ClashAPIOptions{
				ExternalController: controller,
				Secret:             secret,
				DefaultMode:        mode,
			},
		},
	}
}

// recordingOwner records the modes it was asked to switch to.
type recordingOwner struct {
	modes []string
}

func (o *recordingOwner) SetMode(mode string) { o.modes = append(o.modes, mode) }

func TestPlanModeIsUnchangedWhenTheModeIsTheSame(t *testing.T) {
	plan := PlanModeDomain(modeDocument("127.0.0.1:9090", "s", "rule"), "", modeDocument("127.0.0.1:9090", "s", "rule"), "")
	require.True(t, plan.Supported)
	require.False(t, plan.RestartRequired)
	require.Empty(t, plan.Changes)
	require.NotEmpty(t, plan.CurrentRevision)
	require.Equal(t, plan.CurrentRevision, plan.TargetRevision)

	owner := &recordingOwner{}
	report := applyModeDomain(owner, plan)
	require.Equal(t, DomainStatusUnchanged, report.Status)
	require.Empty(t, owner.modes, "an unchanged plan must not touch the running owner")
}

func TestPlanModeSwitchesTheModeLive(t *testing.T) {
	plan := PlanModeDomain(modeDocument("127.0.0.1:9090", "s", "rule"), "", modeDocument("127.0.0.1:9090", "s", "direct"), "")
	require.True(t, plan.Supported)
	require.False(t, plan.RestartRequired)
	require.Equal(t, []DomainChange{{Kind: "mode", From: "rule", To: "direct"}}, plan.Changes)

	owner := &recordingOwner{}
	report := applyModeDomain(owner, plan)
	require.Equal(t, DomainStatusCommitted, report.Status)
	require.Equal(t, []string{"direct"}, owner.modes)
	require.Equal(t, []DomainChange{{Kind: "mode", From: "rule", To: "direct"}}, report.Applied)
	require.Equal(t, plan.TargetRevision, report.Revision)
}

func TestPlanModeRequiresARestartWhenTheOwnerChanges(t *testing.T) {
	plan := PlanModeDomain(modeDocument("127.0.0.1:9090", "old-secret", "rule"), "", modeDocument("127.0.0.1:9091", "new-secret", "direct"), "")
	require.True(t, plan.Supported)
	require.True(t, plan.RestartRequired, "a controller or secret change rebuilds the owner")

	owner := &recordingOwner{}
	report := applyModeDomain(owner, plan)
	require.Equal(t, DomainStatusRestartRequired, report.Status)
	require.True(t, report.RestartRequired)
	require.Empty(t, owner.modes, "a restart-required plan must not half-apply the mode")
}

func TestPlanModeIsUnsupportedWithoutAnOwner(t *testing.T) {
	plan := PlanModeDomain(&option.Options{}, "", modeDocument("127.0.0.1:9090", "s", "direct"), "")
	require.False(t, plan.Supported)
	require.NotEmpty(t, plan.UnsupportedReason)

	owner := &recordingOwner{}
	report := applyModeDomain(owner, plan)
	require.Equal(t, DomainStatusUnsupported, report.Status)
	require.Empty(t, owner.modes)
}

func TestPlanModeIsUnsupportedWhenTheTargetRemovesTheOwner(t *testing.T) {
	plan := PlanModeDomain(modeDocument("127.0.0.1:9090", "s", "rule"), "", &option.Options{}, "")
	require.False(t, plan.Supported)
	require.Contains(t, plan.UnsupportedReason, "restart")
}

func TestAStalePlanIsRefusedBeforeItTouchesTheOwner(t *testing.T) {
	plan := PlanModeDomain(modeDocument("127.0.0.1:9090", "s", "rule"), "revision-a", modeDocument("127.0.0.1:9090", "s", "direct"), "revision-b")
	service := &StartedService{serviceStatus: &ServiceStatus{}}
	report, err := service.ApplyModePlan(plan, "revision-other")
	require.NoError(t, err)
	require.Equal(t, DomainStatusStalePlan, report.Status, "a plan for another revision must not be applied")
}

func TestANonModeChangeIsNeverAppliedByTheModeOwner(t *testing.T) {
	plan := DomainPlan{
		Domain:          "mode",
		Supported:       true,
		TargetRevision:  "revision-b",
		CurrentRevision: "revision-a",
		Changes:         []DomainChange{{Kind: "clash_api"}},
	}
	owner := &recordingOwner{}
	report := applyModeDomain(owner, plan)
	require.Equal(t, DomainStatusRestartRequired, report.Status)
	require.Empty(t, owner.modes)
}
