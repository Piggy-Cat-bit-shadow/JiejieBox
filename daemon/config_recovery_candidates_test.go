package daemon

// C06 - the two candidate shapes the recovery acceptance uses, pinned at the entry.
//
// The distinction this file exists for: a candidate that cannot be PARSED is refused before the
// running Box is touched, so it is a refusal (`failed`, `retained`, the running revision stays) and
// must not produce a recovery event. A candidate that parses and then fails while the Box is BUILT
// is the failure recovery exists for: the running Box is gone at that point, and the stored
// last-good document is what comes back.
//
// The acceptance prompt originally used the duplicate-tag document as its "C". That document is a
// parse rejection, so it exercises the first shape, not recovery. It is kept here - as its own
// case, with its own assertions - and the recovery case uses the undeclared-rule-set document.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// duplicateInboundTagConfig decodes as JSON and fails the effective-option validation: two inbounds
// share a tag. The entry refuses it before touching the running Box.
const duplicateInboundTagConfig = `{
  "log": {"level": "error"},
  "inbounds": [
    {"type": "http", "tag": "dup", "listen": "127.0.0.1", "listen_port": 1},
    {"type": "http", "tag": "dup", "listen": "127.0.0.1", "listen_port": 2}
  ],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`

func TestAParseRejectionAfterAnUpdateIsNotARecovery(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.NoError(t, service.StartOrReloadService(context.Background(), ruleConfig, nil))
	running := service.Instance()
	require.NotNil(t, running)

	require.Error(t, service.StartOrReloadService(context.Background(), duplicateInboundTagConfig, nil))
	require.Same(t, running, service.Instance(), "a rejected candidate must not replace the running Box")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.True(t, report.Retained, "the update that ran before the attempt is still the running one")
	require.Equal(t, configRevisionOf(ruleConfig), report.ActiveRevision)
	require.Equal(t, configRevisionOf(duplicateInboundTagConfig), report.Plan.TargetRevision)
	require.NotEmpty(t, report.Reason, "the refusal keeps the validation message")
	require.Nil(t, service.LastConfigRecovery(), "a refused candidate is not a recovery")
	require.Equal(t, ruleConfig, storedActive(t, directory), "the store still holds the update that ran")
}

func TestABuildFailureAfterAParseableCandidateRecoversToTheStoredDocument(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	// The recovery candidate must pass the preview stage; that is what makes it different from the
	// duplicate-tag document above.
	plan, err := service.PlanServiceChange(buildFailingConfig, nil)
	require.NoError(t, err, "the recovery candidate must be previewable")
	require.Equal(t, configRevisionOf(buildFailingConfig), plan.TargetRevision)

	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil),
		"with recovery enabled the daemon comes back on the stored document")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision)
	require.Equal(t, configRevisionOf(buildFailingConfig), report.Plan.TargetRevision)
	// `retained` is true here because the stored document IS the one that was running: the Box was
	// rebuilt and the same configuration came back. The other side of the distinction - recovery to
	// a saved document that was NOT the running one, which is `retained: false` - needs an update
	// whose persistence failed first; TestARecoveryServesTheOldRoutingRuleAndProviderNodes drives
	// exactly that sequence with business traffic.
	require.True(t, report.Retained, "the recovered document is the one that was running")

	info := service.LastConfigRecovery()
	require.NotNil(t, info)
	require.Equal(t, configRevisionOf(buildFailingConfig), info.FailedRevision)
	require.Equal(t, configRevisionOf(minimalConfig), info.ActiveRevision)
	require.Equal(t, minimalConfig, storedActive(t, directory), "recovery does not replace the stored document")
}

// TestTheRecoverySurvivesAProcessRestart is the in-process form of the acceptance's last step:
// the daemon is terminated, a NEW service object is opened on the same directory, and the same
// failing candidate is applied again. Recovery must read the transaction from disk, not from the
// previous object's memory, and the candidate marker must not survive.
func TestTheRecoverySurvivesAProcessRestart(t *testing.T) {
	directory := t.TempDir()
	first := recoveryService(t, directory, true)
	require.NoError(t, first.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.NoError(t, first.CloseService())
	first.Close()

	// A fresh object on the same directory: nothing of the previous instance is in memory.
	second := recoveryService(t, directory, true)
	require.NoError(t, second.StartOrReloadService(context.Background(), buildFailingConfig, nil),
		"the restarted instance recovers from the transaction on disk")

	report := second.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision)
	require.Equal(t, configRevisionOf(buildFailingConfig), report.Plan.TargetRevision)
	require.NotNil(t, second.Instance(), "a Box must be running after the replay recovered")

	info := second.LastConfigRecovery()
	require.NotNil(t, info)
	require.Equal(t, configRevisionOf(buildFailingConfig), info.FailedRevision)
	require.Equal(t, configRevisionOf(minimalConfig), info.ActiveRevision)
	require.Equal(t, minimalConfig, storedActive(t, directory), "the replay does not replace the stored document")
	_, statErr := os.Stat(filepath.Join(directory, "commit.candidate"))
	require.True(t, os.IsNotExist(statErr), "the candidate marker must not survive a completed start")
}
