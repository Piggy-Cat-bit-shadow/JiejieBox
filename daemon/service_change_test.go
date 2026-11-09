package daemon

// C04 - the real configuration entry: refuse, apply live, restart, and say which one happened.
//
// These tests drive `StartOrReloadService`, the entry every embedder reaches (the libbox command
// server, the daemon binary, the Apple/Android tunnels). Nothing here is a stand-in for it.
//
// The three failures a caller has to be able to tell apart:
//
//	the candidate is refused     the running Box is NOT touched, the caller gets the error
//	the change is live           the Box and its connections stay, the mode is switched
//	the change needs a restart   a new Box is built, and the report says a restart was required

import (
	"context"
	"testing"

	// The daemon package does not import `include`, so the Clash API constructor is not linked
	// into the test binary by the product build graph. The mode tests below need a real Box with a
	// real clash_api owner, and importing the package registers its constructor in an init.
	_ "github.com/sagernet/sing-box/experimental/clashapi"

	"github.com/stretchr/testify/require"
)

// modeConfig is a document whose clash_api names a mode that the route rules also produce, so a
// switch between "Global" and "Direct" is a live change and a real one.
func modeConfig(defaultMode string) string {
	return `{
  "log": {"level": "error"},
  "route": {"rules": [
    {"clash_mode": "Global", "action": "reject"},
    {"clash_mode": "Direct", "action": "reject"}
  ]},
  "experimental": {"clash_api": {"default_mode": "` + defaultMode + `"}}
}`
}

// ruleConfig is minimalConfig plus one routing rule: the same shape, a different rule list.
const ruleConfig = `{
  "log": {"level": "error"},
  "route": {"rules": [{"domain": ["reload.example"], "action": "reject"}]}
}`

func startedForChange(t *testing.T, profile string) *StartedService {
	t.Helper()
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	return service
}

func TestTheEntryAppliesAModeOnlyChangeWithoutRebuilding(t *testing.T) {
	service := startedForChange(t, modeConfig("Global"))
	before := service.Instance()
	require.NotNil(t, before)
	require.Equal(t, "Global", before.clashMode.Mode())

	require.NoError(t, service.StartOrReloadService(context.Background(), modeConfig("Direct"), nil))

	after := service.Instance()
	require.Same(t, before, after, "a mode-only change must not replace the Box: that is what a live owner is for")
	require.Equal(t, "Direct", after.clashMode.Mode(), "the running Box must actually be in the new mode")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusAppliedLive, report.Status)
	require.False(t, report.RestartRequired)
	require.True(t, report.Plan.LiveOnly)
	require.Equal(t, configRevisionOf(modeConfig("Global")), report.Plan.CurrentRevision)
	require.Equal(t, configRevisionOf(modeConfig("Direct")), report.Plan.TargetRevision)
}

func TestTheEntryRebuildsWhenTheRoutingRulesChange(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	before := service.Instance()

	require.NoError(t, service.StartOrReloadService(context.Background(), ruleConfig, nil))

	after := service.Instance()
	require.NotSame(t, before, after, "a routing-rule change is owned by the Box, so it needs a new one")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRestarted, report.Status)
	require.True(t, report.RestartRequired)
	require.True(t, report.Plan.ChangedDocument)
	require.NotEmpty(t, report.Plan.Rule.Changes, "the plan must name the rule change that required the restart")
	require.Equal(t, report.Plan.TargetRevision, after.revision,
		"the running revision must identify the document the Box was built from")
}

// TestAnExplicitReloadOfAnIdenticalProfileStillRebuilds pins the boundary between "the document
// needs no rebuild" and "the caller asked for a reload".
//
// A reload is also how a caller picks up an edited local rule set, certificate or local provider,
// and this entry cannot see those files. So the plan reports `unchanged` - the document itself
// needs nothing - while the entry still honours the request it was given. What the plan buys here
// is the answer, not a skipped reload.
func TestAnExplicitReloadOfAnIdenticalProfileStillRebuilds(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	before := service.Instance()

	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	require.NotSame(t, before, service.Instance(), "an explicit reload still replaces the Box")
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRestarted, report.Status)
	require.True(t, report.Plan.Unchanged, "the document itself is unchanged")
	require.False(t, report.Plan.RestartRequired, "and it needs no restart for its own content")
}

func TestTheEntryRefusesAnUnparseableCandidateAndKeepsTheRunningBox(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	before := service.Instance()

	err := service.StartOrReloadService(context.Background(), `{not json`, nil)
	require.Error(t, err)

	require.Same(t, before, service.Instance(), "a candidate that cannot parse must never cost the running Box")
	require.NotNil(t, service.runningInstance(), "the service is still STARTED")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.True(t, report.Retained, "the previous configuration is still the running one")
	require.NotEmpty(t, report.Reason)
}

func TestTheEntryRefusesAStaticallyInvalidCandidateAndKeepsTheRunningBox(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	before := service.Instance()

	// The inbound type is not registered in this harness, so the document fails while parsing.
	err := service.StartOrReloadService(context.Background(),
		`{"log":{"level":"error"},"inbounds":[{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}]}`, nil)
	require.Error(t, err)

	require.Same(t, before, service.Instance(), "a static validation failure must not close the running Box")
	require.NotNil(t, service.runningInstance(), "the service is still STARTED")
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.True(t, report.Retained)
}

func TestPlanServiceChangeAnswersWithoutTouchingTheBox(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	before := service.Instance()
	// Starting IS an apply attempt, so one report already exists; planning must not add or replace
	// one.
	startReport := service.LastServiceChange()
	require.NotNil(t, startReport)

	unchanged, err := service.PlanServiceChange(minimalConfig, nil)
	require.NoError(t, err)
	require.True(t, unchanged.Started)
	require.True(t, unchanged.Unchanged)
	require.False(t, unchanged.RestartRequired)

	changed, err := service.PlanServiceChange(ruleConfig, nil)
	require.NoError(t, err)
	require.False(t, changed.Unchanged)
	require.True(t, changed.RestartRequired)
	require.NotEmpty(t, changed.Rule.Changes)
	require.Equal(t, configRevisionOf(minimalConfig), changed.CurrentRevision)

	_, err = service.PlanServiceChange(`{not json`, nil)
	require.Error(t, err, "a plan for an unparseable candidate is an error, not a guess")

	require.Same(t, before, service.Instance(), "planning is a read: it must not replace or close the running Box")
	require.NotNil(t, service.runningInstance(), "the service is still STARTED")
	require.Same(t, startReport, service.LastServiceChange(), "planning is not an apply attempt and must not report one")
}

func TestASuccessfulStartIsReportedAsAStartNotAReload(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusStarted, report.Status)
	require.False(t, report.Plan.Started, "there was no running Box to compare against")
}

// TestALiveChangeThatCannotBeAppliedFallsBackToARestart pins the honest failure: when the live
// owner is gone, the entry must not report success for a change the running Box did not absorb.
func TestALiveChangeThatCannotBeAppliedFallsBackToARestart(t *testing.T) {
	service := startedForChange(t, modeConfig("Global"))
	before := service.Instance()
	// The owner is what `PlanModeDomain` relies on; take it away from the running instance so the
	// live path has nothing to commit through.
	before.clashMode = nil

	require.NoError(t, service.StartOrReloadService(context.Background(), modeConfig("Direct"), nil))

	after := service.Instance()
	require.NotSame(t, before, after, "without a live owner the change needs a new Box")
	require.Equal(t, "Direct", after.clashMode.Mode(), "and the new Box must run the requested mode")
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRestarted, report.Status)
	require.True(t, report.RestartRequired)
	require.Contains(t, report.Reason, "live path was not available")
}
