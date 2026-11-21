package build_shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// compatibilityEntries are the TLS/behaviour defaults from flags.go that every policy must keep.
// They are restated here rather than read from the constant so that deleting one from flags.go
// fails this test instead of silently shrinking what every shipped ClientHello carries.
var compatibilityEntries = []string{
	"multipathtcp=0",
	"tlssha1=1",
	"tlssecpmlkem=0",
	"tracebacklabels=0",
	"x509sslcertoverrideplatform=0",
}

// TestStartupPolicyKeepsTheCompatibilityDefaults is the merge property: -X runtime.godebugDefault=
// REPLACES the toolchain default, so a policy that forgot the compatibility list would change the
// shipped TLS fingerprint on every policy build. It also rejects a duplicate key, because a policy
// that restates a compatibility setting would silently win or lose depending on order.
func TestStartupPolicyKeepsTheCompatibilityDefaults(t *testing.T) {
	policies := []string{
		"",
		"memprofilerate=inherit,adaptivestackstart=inherit",
		"memprofilerate=off",
		"adaptivestackstart=off",
		"memprofilerate=off,adaptivestackstart=off",
		"memprofilerate=1048576,adaptivestackstart=on",
	}
	for _, text := range policies {
		policy, err := ParseStartupPolicy(text)
		require.NoError(t, err, text)
		composed := policy.GODEBUGDefaults()
		require.True(t, strings.HasPrefix(composed, godebugDefaults),
			"policy %q: %q must start with the shipped compatibility defaults %q", text, composed, godebugDefaults)
		for _, entry := range compatibilityEntries {
			require.Contains(t, composed, entry, "policy %q dropped a compatibility default", text)
		}
		seen := make(map[string]bool)
		for _, field := range strings.Split(composed, ",") {
			key, _, found := strings.Cut(field, "=")
			require.True(t, found, "policy %q produced a malformed field %q", text, field)
			require.False(t, seen[key], "policy %q produced %s twice in %q", text, key, composed)
			seen[key] = true
		}
		for _, base := range []string{"multipathtcp", "tlssha1", "tlssecpmlkem", "tracebacklabels", "x509sslcertoverrideplatform"} {
			require.True(t, seen[base], "policy %q lost %s", text, base)
		}
	}
}

func TestParseStartupPolicy(t *testing.T) {
	type want struct {
		description string
		entries     []string
	}
	valid := []struct {
		text string
		want want
	}{
		{"", want{"memprofilerate=inherit,adaptivestackstart=inherit", nil}},
		{"memprofilerate=inherit", want{"memprofilerate=inherit,adaptivestackstart=inherit", nil}},
		{"memprofilerate=off", want{"memprofilerate=0,adaptivestackstart=inherit", []string{"memprofilerate=0"}}},
		{"memprofilerate=0", want{"memprofilerate=0,adaptivestackstart=inherit", []string{"memprofilerate=0"}}},
		{"memprofilerate=524288", want{"memprofilerate=524288,adaptivestackstart=inherit", []string{"memprofilerate=524288"}}},
		{"adaptivestackstart=off", want{"memprofilerate=inherit,adaptivestackstart=0", []string{"adaptivestackstart=0"}}},
		{"adaptivestackstart=0", want{"memprofilerate=inherit,adaptivestackstart=0", []string{"adaptivestackstart=0"}}},
		{"adaptivestackstart=on", want{"memprofilerate=inherit,adaptivestackstart=1", []string{"adaptivestackstart=1"}}},
		{" MemProfileRate = OFF , AdaptiveStackStart = 1 ", want{"memprofilerate=0,adaptivestackstart=1", []string{"memprofilerate=0", "adaptivestackstart=1"}}},
		{"memprofilerate=off,", want{"memprofilerate=0,adaptivestackstart=inherit", []string{"memprofilerate=0"}}},
		// Canonical order is memprofilerate then adaptivestackstart, whatever the input order.
		{"adaptivestackstart=0,memprofilerate=1048576", want{"memprofilerate=1048576,adaptivestackstart=0", []string{"memprofilerate=1048576", "adaptivestackstart=0"}}},
	}
	for _, testCase := range valid {
		policy, err := ParseStartupPolicy(testCase.text)
		require.NoError(t, err, testCase.text)
		require.Equal(t, testCase.want.description, policy.String(), testCase.text)
		require.Equal(t, testCase.want.entries, policy.Entries(), testCase.text)
	}

	invalid := []struct{ text, because string }{
		{"memprofilerate", "no equals sign"},
		{"memprofilerate=-1", "negative rate"},
		{"memprofilerate=on", "on is not a rate"},
		{"memprofilerate=512k", "not a decimal rate"},
		{"adaptivestackstart=2", "not a boolean"},
		{"adaptivestackstart=yes", "not a boolean"},
		{"memprofilerate=off,memprofilerate=0", "duplicate key"},
		{"tlssha1=0", "compatibility defaults are not a policy input"},
		{"tlssecpmlkem=1", "compatibility defaults are not a policy input"},
		{"unknown=1", "unknown key"},
	}
	for _, testCase := range invalid {
		_, err := ParseStartupPolicy(testCase.text)
		require.Error(t, err, "%q must be rejected (%s)", testCase.text, testCase.because)
	}
}

func TestStartupPolicyStringRoundTrips(t *testing.T) {
	for _, text := range []string{
		"memprofilerate=inherit,adaptivestackstart=inherit",
		"memprofilerate=0,adaptivestackstart=0",
		"memprofilerate=4194304,adaptivestackstart=1",
	} {
		first, err := ParseStartupPolicy(text)
		require.NoError(t, err)
		second, err := ParseStartupPolicy(first.String())
		require.NoError(t, err)
		require.Equal(t, first, second, text)
	}
}

// TestStartupPolicyLinkStrings pins the exact bytes of the shipped link flag, and that the
// inherit policy is byte-identical to the flag every non-Apple build already links.
func TestStartupPolicyLinkStrings(t *testing.T) {
	inherit := StartupPolicyInherit()
	require.True(t, inherit.IsZero())
	require.Equal(t, godebugDefaults, inherit.GODEBUGDefaults())
	require.Equal(t, "memprofilerate=inherit,adaptivestackstart=inherit", inherit.String())
	for _, debug := range []bool{false, true} {
		require.Equal(t, LinkerFlags("v1.2.3", debug), LinkerFlagsWithStartupPolicy("v1.2.3", debug, inherit),
			"an inherit policy must not change a single byte of the build flags (debug=%v)", debug)
	}

	memoryFirst, err := ParseStartupPolicy("memprofilerate=off,adaptivestackstart=off")
	require.NoError(t, err)
	const wantGODEBUG = "multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0,memprofilerate=0,adaptivestackstart=0"
	require.Equal(t, wantGODEBUG, memoryFirst.GODEBUGDefaults())

	releaseFlags := LinkerFlagsWithStartupPolicy("v1.2.3", false, memoryFirst)
	require.Contains(t, releaseFlags, "-X runtime.godebugDefault="+wantGODEBUG)
	require.Contains(t, releaseFlags, "-X github.com/sagernet/sing-box/constant.Version=v1.2.3")
	require.Contains(t, releaseFlags, "-checklinkname=0")
	require.Contains(t, releaseFlags, "-s -w -buildid=")

	debugFlags := LinkerFlagsWithStartupPolicy("v1.2.3", true, memoryFirst)
	require.Contains(t, debugFlags, "-X runtime.godebugDefault="+wantGODEBUG)
	require.NotContains(t, debugFlags, "-s -w", "a debug build must keep its symbol table")

	// The base list has to be the one release/LDFLAGS carries: this package and the release
	// recipe both ship the same wire, and flags_test.go asserts that for LinkerFlags.
	releaseLDFLAGS, err := os.ReadFile(filepath.Join("..", "..", "..", "release", "LDFLAGS"))
	require.NoError(t, err)
	require.Equal(t, godebugDefaultFrom(t, string(releaseLDFLAGS)), godebugDefaults)
}

// TestLinkedGODEBUGDefaultsArePresent pins the byte-check primitive to an exact full-string match:
// a prefix, a shorter compatibility-only list or a different policy must not pass.
func TestLinkedGODEBUGDefaultsArePresent(t *testing.T) {
	offPolicy, err := ParseStartupPolicy("memprofilerate=off,adaptivestackstart=off")
	require.NoError(t, err)
	inherited := StartupPolicyInherit()

	policyArtifact := []byte("prefix\x00" + offPolicy.GODEBUGDefaults() + "\x00suffix")
	require.True(t, offPolicy.LinkedGODEBUGDefaultsArePresent(policyArtifact))
	require.False(t, inherited.LinkedGODEBUGDefaultsArePresent(policyArtifact),
		"the inherit value is a PREFIX of every policy build's value; an inherit check must not pass on a policy artifact")

	coarse, err := ParseStartupPolicy("memprofilerate=4194304")
	require.NoError(t, err)
	require.False(t, coarse.LinkedGODEBUGDefaultsArePresent(policyArtifact),
		"a different rate must not pass the exact-match check")

	inheritArtifact := []byte("prefix\x00" + inherited.GODEBUGDefaults() + "\x00suffix")
	require.True(t, inherited.LinkedGODEBUGDefaultsArePresent(inheritArtifact))
	require.False(t, offPolicy.LinkedGODEBUGDefaultsArePresent(inheritArtifact),
		"the compatibility list alone must not satisfy a policy that asks for more")

	// A match that continues directly into another comma-separated setting is a different value,
	// even when it runs to the end of the buffer.
	require.False(t, inherited.LinkedGODEBUGDefaultsArePresent([]byte(inherited.GODEBUGDefaults()+",")))
}
