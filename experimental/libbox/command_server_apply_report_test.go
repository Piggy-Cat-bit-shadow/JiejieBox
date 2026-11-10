package libbox

// The embedder-facing apply receipt, through the real CommandServer.
//
// `ApplyServiceChangeReport` exists because a gomobile bridge cannot return a document and an error
// at once: the generated method throws on a non-nil error and the document is lost. So the failure
// must be IN the document, and the two entry points must describe the same attempt - which is what
// these tests check, plus the guarantee that the older error-only method still behaves the same.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// newApplyReportServer builds a CommandServer with its own paths, the way `LibboxSetup` gives the
// real one a container. The platform interface is nil and unused on this path: the configuration
// below declares no tun inbound, so nothing asks the platform to open one.
func newApplyReportServer(t *testing.T) *CommandServer {
	t.Helper()
	previousWorking, previousTemp, previousBase := sWorkingPath, sTempPath, sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sBasePath = previousWorking, previousTemp, previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})

	server, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(server.Close)
	return server
}

type applyReportOutcome struct {
	Version int    `json:"version"`
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Status  string `json:"status"`
	Report  *struct {
		Status         string `json:"status"`
		ActiveRevision string `json:"activeRevision"`
		Retained       bool   `json:"retained"`
	} `json:"report"`
}

func decodeApplyOutcome(t *testing.T, box *StringBox) applyReportOutcome {
	t.Helper()
	require.NotNil(t, box, "the receipt path must always answer with a document")
	var outcome applyReportOutcome
	require.NoError(t, json.Unmarshal([]byte(box.Value), &outcome), box.Value)
	return outcome
}

func TestTheApplyReportIsReturnedByTheCallThatProducedIt(t *testing.T) {
	server := newApplyReportServer(t)

	outcome := decodeApplyOutcome(t, server.ApplyServiceChangeReport(`{"log":{"level":"error"}}`, &OverrideOptions{}))
	require.Equal(t, 1, outcome.Version)
	require.True(t, outcome.OK)
	require.Empty(t, outcome.Error)
	require.Equal(t, "started", outcome.Status)
	require.NotNil(t, outcome.Report)
	require.NotEmpty(t, outcome.Report.ActiveRevision, "the receipt names the document that runs")
}

// TestAFailedApplyStillAnswersWithADocument is the reason the receipt is not an error: the bridge
// would drop it. The failure is reported in the document, and the older error-only method keeps
// returning the error.
func TestAFailedApplyStillAnswersWithADocument(t *testing.T) {
	server := newApplyReportServer(t)
	_, err := server.applyServiceChange(`{"log":{"level":"error"}}`, &OverrideOptions{})
	require.NoError(t, err)

	outcome := decodeApplyOutcome(t, server.ApplyServiceChangeReport(`{"log":`, &OverrideOptions{}))
	require.Equal(t, 1, outcome.Version)
	require.False(t, outcome.OK)
	require.NotEmpty(t, outcome.Error, "the refusal is described in the document")
	require.Equal(t, "failed", outcome.Status)
	require.NotNil(t, outcome.Report)
	require.True(t, outcome.Report.Retained, "the previous document is still running")

	// The error-only contract of the older method is unchanged: same attempt, same failure, and a
	// caller that only handles errors is not forced to read the document.
	require.Error(t, server.StartOrReloadService(`{"log":`, &OverrideOptions{}))
}
