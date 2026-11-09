package daemon

// C04 review A2 — a restart has to name its reason in the caller's vocabulary.
//
// "RequiresRestart: true" tells a caller that a reload is needed. It does not tell them WHY, so they
// cannot decide whether to postpone the edit, warn the user, or look for an owner that could have
// applied it. The plan now names the document sections that differ outside the domains with a live
// owner, which is the reason in the terms the caller sees in their configuration file.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestThePlanNamesTheSectionsThatForceARestart(t *testing.T) {
	service := startedForChange(t, minimalConfig)

	// The log level changed: no domain owns it, so the reason is the log section.
	withLogLevel, err := service.PlanServiceChange(`{"log":{"level":"debug"}}`, nil)
	require.NoError(t, err)
	require.True(t, withLogLevel.RestartRequired)
	require.Contains(t, withLogLevel.ChangedSections, "log")
	require.NotContains(t, withLogLevel.ChangedSections, "route")

	// A routing rule changed: named both as a domain change and as the document section.
	changed, err := service.PlanServiceChange(ruleConfig, nil)
	require.NoError(t, err)
	require.True(t, changed.RestartRequired)
	require.Contains(t, changed.ChangedSections, "route")
	require.NotEmpty(t, changed.Rule.Changes)

	// A mode-only change is live and has no restart reason to report.
	liveService := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(liveService.Close)
	require.NoError(t, liveService.StartOrReloadService(context.Background(), modeConfig("Global"), nil))
	live, err := liveService.PlanServiceChange(modeConfig("Direct"), nil)
	require.NoError(t, err)
	require.True(t, live.LiveOnly)
	require.False(t, live.RestartRequired)
	require.Empty(t, live.ChangedSections, "a change with a live owner has no restart reason")
}

func TestAnUnchangedPlanNamesNoSection(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	unchanged, err := service.PlanServiceChange(minimalConfig, nil)
	require.NoError(t, err)
	require.True(t, unchanged.Unchanged)
	require.Empty(t, unchanged.ChangedSections)
}
