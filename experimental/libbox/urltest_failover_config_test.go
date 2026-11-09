package libbox

// The configuration mapping for the stable/failover policy: an ABSENT block keeps the historical
// probing behaviour, a PRESENT block enables the policy with the values it names.
//
// This is the try-merge slice for the configuration window: the schema field and its mapping are
// verified here on a real Box, and the generated schema/docs artifacts stay with that window.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func failoverConfigTestServer(t *testing.T, child string) string {
	t.Helper()
	probe := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(probe.Close)
	return fmt.Sprintf(`{"log":{"level":"error"},"outbounds":[
		{"type":"direct","tag":"direct"},
		{"type":"direct","tag":"node-a"},
		{"type":"urltest","tag":"g1","outbounds":["node-a"],"url":%q,"interval":"10m"%s}
	],"route":{"final":"direct"}}`, probe.URL+"/generate_204", child)
}

func failoverGroupOutbound(t *testing.T, server *CommandServer) recoverySnapshotter {
	t.Helper()
	instance := server.Instance()
	require.NotNil(t, instance)
	outbound, loaded := instance.Box().Outbound().Outbound("g1")
	require.True(t, loaded)
	snapshotter, isSnapshotter := outbound.(recoverySnapshotter)
	require.True(t, isSnapshotter)
	return snapshotter
}

func TestTheAbsentFailoverBlockKeepsTheHistoricalProbing(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, failoverConfigTestServer(t, ""))
	groupOutbound := failoverGroupOutbound(t, server)

	require.True(t, groupOutbound.HealthSnapshot().Disabled,
		"a configuration that does not name the block keeps probing every member every round")
}

func TestTheFailoverBlockEnablesThePolicy(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, failoverConfigTestServer(t,
		`,"failover":{"widen_after":1,"settle_after":2,"recover_after":1,"tolerance":1}`))
	groupOutbound := failoverGroupOutbound(t, server)

	// A manual round seeds the selection and the policy's stable slot from it. The group may already
	// be running its own first round; a queued request reports "in progress", so retry briefly.
	outbound, loaded := server.Instance().Box().Outbound().Outbound("g1")
	require.True(t, loaded)
	runner, isRunner := outbound.(recoveryRoundRunner)
	require.True(t, isRunner)
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := runner.URLTest(context.Background())
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "already in progress") || time.Now().After(deadline) {
			require.NoError(t, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	snapshot := groupOutbound.HealthSnapshot()
	require.False(t, snapshot.Disabled, "the named block must enable the policy")
	require.NotEmpty(t, snapshot.Stable, "the policy adopts the committed selection as its stable member")
	require.False(t, snapshot.Widened)
	require.Equal(t, []string{"node-a"}, snapshot.NextProbes,
		"a healthy stable member means the next periodic round probes only it")
}
