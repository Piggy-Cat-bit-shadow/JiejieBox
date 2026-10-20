package physicalpath

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The report's severity split is data, not message text: a caller that decides whether to refuse the
// start must not have to look at the wording of a sentence. These cases pin the split at the source.
func TestReportSeparatesFatalFindingsFromCapabilityWarnings(t *testing.T) {
	capability := Failure{Root: "sel", Leaf: "tcp-only", Kind: FailureCapability, Reason: "does not carry udp"}
	structure := Failure{Root: "sel", Leaf: "ghost", Kind: FailureStructure, Reason: "no such member"}
	promise := Failure{Root: "proxy", Leaf: "proxy", Kind: FailurePromise, Reason: "cannot own the destination"}

	t.Run("only capability findings are warnings", func(t *testing.T) {
		report := Report{Failures: []Failure{capability, structure, promise}}
		require.Equal(t, []Failure{capability}, report.Warnings())
		require.Equal(t, []Failure{structure, promise}, report.Fatal())
	})

	t.Run("Err is nil when every finding is a capability one", func(t *testing.T) {
		report := Report{Failures: []Failure{capability}}
		require.NoError(t, report.Err(),
			"a capability finding must not be renderable as a start error")
		warningErr := report.WarningErr()
		require.Error(t, warningErr)
		require.Contains(t, warningErr.Error(), "does not carry udp")
	})

	t.Run("WarningErr is nil when every finding is fatal", func(t *testing.T) {
		report := Report{Failures: []Failure{structure}}
		require.NoError(t, report.WarningErr())
		fatalErr := report.Err()
		require.Error(t, fatalErr)
		require.Contains(t, fatalErr.Error(), "no such member")
		require.NotContains(t, fatalErr.Error(), "does not carry udp")
	})

	t.Run("an empty report renders neither", func(t *testing.T) {
		report := Report{}
		require.NoError(t, report.Err())
		require.NoError(t, report.WarningErr())
		require.Empty(t, report.Fatal())
		require.Empty(t, report.Warnings())
	})
}

// TestCapabilityFailuresAreTaggedAtTheirSource is the other half: the split above is only useful if
// the sites that report a network mismatch actually tag it. A site left at the zero value would be
// silently fatal, which is the behaviour this round exists to remove.
func TestCapabilityFailuresAreTaggedAtTheirSource(t *testing.T) {
	source, err := readSource("dryrun.go")
	require.NoError(t, err)
	// The two sentences a network mismatch produces, plus the group-level "no member carries".
	for _, sentence := range []string{
		"this outbound does not carry ",
		"this outbound reports no network it can carry",
		"no reachable member carries ",
	} {
		index := strings.Index(source, sentence)
		require.GreaterOrEqual(t, index, 0, "the sentence must still exist: %s", sentence)
		window := source[maxInt(0, index-400):index]
		require.Contains(t, window, "FailureCapability",
			"the failure that renders %q must be tagged as a capability finding", sentence)
	}
	// And the fatal sentences must not be.
	for _, sentence := range []string{
		"the group declares this member but no outbound",
		"this tag is claimed by BOTH an outbound and an endpoint",
	} {
		index := strings.Index(source, sentence)
		require.GreaterOrEqual(t, index, 0)
		window := source[maxInt(0, index-400):index]
		require.NotContains(t, window, "FailureCapability")
	}
}

func readSource(name string) (string, error) {
	data, err := os.ReadFile(name)
	return string(data), err
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
