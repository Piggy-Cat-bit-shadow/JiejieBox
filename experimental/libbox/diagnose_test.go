package libbox

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/common/physicalpath"

	"github.com/stretchr/testify/require"
)

func TestPathExplanationReportsHopsControlsAndUnknowns(t *testing.T) {
	status := physicalpath.PathStatus{
		Root:        "proxy",
		Network:     "tcp",
		Entry:       "g",
		Exit:        "leaf",
		ControlPath: []string{"g", "leaf"},
		Controls: []physicalpath.ControlNode{
			{Tag: "g", Decision: "leaf", Committed: true},
		},
		Hops: []physicalpath.HopStatus{
			{Position: 0, Tag: "leaf", Type: "vless", Readiness: physicalpath.ReadinessReady, ClaimedState: physicalpath.LifecycleStateReady},
			{Position: 1, Tag: "unknown-hop", Readiness: physicalpath.ReadinessUnknown, Reason: "no state reporter"},
		},
		Unknowns: []physicalpath.Unknown{
			{Node: "unknown-hop", Position: 1, Reason: "no state reporter"},
		},
	}
	explanation := newPathExplanation(status)
	require.False(t, explanation.Ready, "a path with an unknown is not ready")
	require.Len(t, explanation.Hops, 2)
	require.Equal(t, "unknown", explanation.Hops[1].Readiness)
	require.Len(t, explanation.Unknowns, 1)
	require.Len(t, explanation.Controls, 1)
	require.True(t, explanation.Controls[0].Committed)

	encoded, err := json.Marshal(explanation)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, "proxy", decoded["root"])
	require.Equal(t, "tcp", decoded["network"])
	require.Equal(t, false, decoded["ready"])
	require.Contains(t, decoded, "unknowns")
	require.Contains(t, decoded, "hops")
}

func TestExplainPathRequiresARunningService(t *testing.T) {
	server := &CommandServer{}
	_, err := server.ExplainPath("proxy", "tcp")
	require.Error(t, err)
}
