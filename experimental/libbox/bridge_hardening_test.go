package libbox

// Binding-surface hardening for the diagnostic/event bridge:
//   - a typed nil *CommandClient reaches the platform as a nil pointer; every client call must
//     return an error instead of dereferencing;
//   - the connection-test report crosses the bridge as one string and its stage detail contains a
//     rule description, which is unbounded for a rule-set or a large domain list. The encoded report
//     is capped and marked as truncated;
//   - the cap must not corrupt encoding: Go strings can contain invalid UTF-8 (read from a config
//     file), and the JSON handed to the platform must still be valid UTF-8.

import (
	stdjson "encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestANilCommandClientReportsErrorsInsteadOfPanicking(t *testing.T) {
	var client *CommandClient
	require.Error(t, client.URLTest("group"))
	require.NoError(t, client.Disconnect())

	// The same value through the interface the binding layer holds.
	var bridged interface {
		URLTest(groupTag string) error
	} = client
	require.Error(t, bridged.URLTest("group"))
}

func TestAnOversizedReportIsCappedAndMarkedTruncated(t *testing.T) {
	huge := strings.Repeat("a", 100_000)
	report := connectionTestReport{
		Root:    "direct",
		Network: "tcp",
		Target:  "http://127.0.0.1:443/" + huge + "\xff",
		Outcome: connectionTestOutcomeOK,
		Routing: connectionTestRoutingRule,
		Stages: []connectionTestStage{
			{ID: connectionTestStagePath, Status: connectionTestStageOK, Detail: huge + "\xff"},
			{ID: connectionTestStageDial, Status: connectionTestStageOK, Detail: "connected"},
		},
	}
	encoded := marshalConnectionTestReport(report)
	require.LessOrEqual(t, len(encoded), connectionTestMaxReportBytes)
	require.True(t, utf8.ValidString(encoded), "the bridge must never receive invalid UTF-8")

	var decoded connectionTestReport
	require.NoError(t, stdjson.Unmarshal([]byte(encoded), &decoded))
	require.True(t, decoded.Truncated, "a shortened report must say so")
	require.Len(t, decoded.Stages, 2, "the stage structure is kept")
	require.Contains(t, decoded.Stages[0].Detail, "(truncated)")
	require.LessOrEqual(t, len(decoded.Stages[0].Detail), connectionTestMaxFieldBytes+len("…(truncated)"))
}

func TestAManyEventReportDropsTheNarrativeInsteadOfExceedingTheCeiling(t *testing.T) {
	line := strings.Repeat("b", 100_000)
	report := connectionTestReport{
		Root:    "direct",
		Network: "tcp",
		Target:  "http://127.0.0.1:443/",
		Outcome: connectionTestOutcomeOK,
		Routing: connectionTestRoutingRule,
		Stages:  []connectionTestStage{{ID: connectionTestStagePath, Status: connectionTestStageOK}},
		Events:  make([]string, 200),
	}
	for index := range report.Events {
		report.Events[index] = line
	}
	encoded := marshalConnectionTestReport(report)
	require.LessOrEqual(t, len(encoded), connectionTestMaxReportBytes)

	var decoded connectionTestReport
	require.NoError(t, stdjson.Unmarshal([]byte(encoded), &decoded))
	require.True(t, decoded.Truncated)
	require.Empty(t, decoded.Events, "the narrative is what the ceiling drops first")
	require.NotEmpty(t, decoded.Stages, "the verdict stages survive")
}

// A normal report must not be marked or changed by the bounds.
func TestASmallReportIsNotMarkedTruncated(t *testing.T) {
	report := connectionTestReport{
		Root:    "direct",
		Network: "tcp",
		Target:  "http://127.0.0.1:443/",
		Outcome: connectionTestOutcomeOK,
		Routing: connectionTestRoutingRoot,
		Stages:  []connectionTestStage{{ID: connectionTestStagePath, Status: connectionTestStageOK, Detail: "direct"}},
	}
	encoded := marshalConnectionTestReport(report)
	var decoded connectionTestReport
	require.NoError(t, stdjson.Unmarshal([]byte(encoded), &decoded))
	require.False(t, decoded.Truncated)
	require.Equal(t, "direct", decoded.Stages[0].Detail)
}
