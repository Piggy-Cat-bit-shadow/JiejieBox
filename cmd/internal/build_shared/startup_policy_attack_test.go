package build_shared

// Adversarial coverage for the startup-policy vocabulary. The shipping tests in
// startup_policy_test.go prove the shipped strings and the round trip; this file attacks the
// PARSER from the outside, because the parser is the only thing between an operator's typo (or a
// stale CI variable) and a binary whose provenance claims a runtime default it does not have.
//
// The oracles are deliberately independent of the parser's own code:
//
//   - an accepted policy must be expressible as `key=value` fields the RUNTIME's parser accepts
//     (memprofilerate through strconv.Atoi, adaptivestackstart through strconv.ParseInt(.., 10, 32),
//     which is what runtime/runtime1.go of the pinned toolchain does), so "accepted" means "the
//     runtime will honour it", not merely "our parser liked it";
//   - a rejected policy must return the ZERO policy, so a caller that ignores the error cannot
//     link half of the input;
//   - no accepted input may smuggle in a field the vocabulary does not own. The composed value is
//     a link flag: a comma or a newline that survived parsing would be a second GODEBUG setting.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStartupPolicyAcceptsOnlyValuesTheRuntimeWillHonour pins the accepted vocabulary to the
// runtime's own parser. The pinned runtime reads `memprofilerate` with strconv.Atoi (int) and
// every other startup variable with strconv.ParseInt(value, 10, 32); it has no notion of "on" or
// "off". A policy that wrote the operator's word through to the link string would be accepted by
// the builder and silently ignored by the runtime.
func TestStartupPolicyAcceptsOnlyValuesTheRuntimeWillHonour(t *testing.T) {
	accepted := []string{
		"", "inherit",
		"memprofilerate=off,adaptivestackstart=off",
		"memprofilerate=0,adaptivestackstart=0",
		"memprofilerate=1,adaptivestackstart=on",
		"memprofilerate=1048576,adaptivestackstart=1",
		"memprofilerate=9223372036854775807", // int64 max: Atoi accepts it on every 64-bit target
		" MemProfileRate = +1048576 , AdaptiveStackStart = ON ",
		"memprofilerate=000000001048576",
		"memprofilerate=-0", // negative zero is zero; the runtime reads it as 0
		",memprofilerate=off,",
		"memprofilerate=off,adaptivestackstart=off,", // trailing separator: empty field skipped
	}
	for _, text := range accepted {
		policy, err := ParseStartupPolicy(text)
		require.NoError(t, err, "accepted spelling %q", text)
		for _, entry := range policy.Entries() {
			key, value, found := strings.Cut(entry, "=")
			require.True(t, found, "%q produced a field with no '=': %q", text, entry)
			switch key {
			case MemProfileRateSetting:
				rate, atoiErr := strconv.Atoi(value)
				require.NoError(t, atoiErr, "%q: the runtime's strconv.Atoi cannot read %q", text, value)
				require.GreaterOrEqual(t, rate, 0, "%q: the runtime would read a negative rate", text)
				require.Equal(t, strconv.Itoa(rate), value, "%q: %q is not the canonical decimal form", text, value)
			case AdaptiveStackStartSetting:
				// runtime1.go parses every non-memprofilerate startup variable with
				// ParseInt(value, 10, 32); "on"/"off" would be dropped on the floor.
				parsed, parseErr := strconv.ParseInt(value, 10, 32)
				require.NoError(t, parseErr, "%q: the runtime's ParseInt(.., 10, 32) cannot read %q", text, value)
				require.Contains(t, []int64{0, 1}, parsed, "%q: the runtime understands only 0 and 1, got %d", text, parsed)
			default:
				t.Fatalf("%q produced an unknown key %q", text, key)
			}
		}
		// The canonical spelling is idempotent: parsing String() yields the same policy. A
		// provenance line is read back through this path by a human and by tests.
		reparsed, err := ParseStartupPolicy(policy.String())
		require.NoError(t, err, "%q: canonical form %q does not parse", text, policy.String())
		require.Equal(t, policy, reparsed, "%q: canonical form is not idempotent", text)
	}
}

// TestStartupPolicyRejectsMalformedVocabulary is the refusal matrix. Each entry names the reason,
// and the assertion checks both halves of "fail closed": an error that explains itself, and the
// zero policy rather than a partially applied one.
func TestStartupPolicyRejectsMalformedVocabulary(t *testing.T) {
	rejected := []struct{ text, because string }{
		// Numeric forms the runtime would read differently, or not at all.
		{"memprofilerate=9223372036854775808", "int64 overflow"},
		{"memprofilerate=-9223372036854775808", "negative rate"},
		{"memprofilerate=-1", "negative rate"},
		{"memprofilerate=abc", "not a number"},
		{"memprofilerate=", "empty value"},
		{"memprofilerate=1e6", "scientific notation is not Atoi"},
		{"memprofilerate=0x40000", "hex is not Atoi"},
		{"memprofilerate=1_048_576", "digit separators are not Atoi"},
		{"memprofilerate=５１２", "fullwidth digits are not ASCII"},
		{"memprofilerate=52 4288", "an embedded space is not a number"},
		{"memprofilerate=off=extra", "a second '=' belongs to the value"},
		{"memprofilerate", "no '='"},
		{"=off", "empty key"},
		{"memprofilerate=off,adaptivestackstart", "one malformed field poisons the document"},
		{"memprofilerate=off,adaptivestackstart=off,unknown=1", "unknown key"},
		{"memprofilerate=off;adaptivestackstart=off", "';' is not a separator, so the value is not a rate"},
		{"memprofilerate=off\nadaptivestackstart=off", "a newline is not a separator"},
		{"godebug=memprofilerate=off", "a wrapped key is not a setting"},
		{"memprofilerate=off,memprofilerate=inherit", "duplicate key"},
		{"MEMPROFILERATE=off,memprofilerate=off", "duplicate key regardless of case"},
		{"memprofilerate=off, memprofilerate = 0", "duplicate key regardless of spacing"},
		{"adaptivestackstart=2", "not a boolean the runtime defines"},
		{"adaptivestackstart=-1", "not a boolean the runtime defines"},
		{"adaptivestackstart=1.0", "not an integer"},
		{"adaptivestackstart=true", "not an integer"},
		{"adaptivestackstart=on,adaptivestackstart=off", "duplicate key"},
		{"inherit,inherit", "the shorthand is the whole document, not a field"},
		{"inherit,memprofilerate=off", "the shorthand is the whole document, not a field"},
		{"tlssecpmlkem=0", "the compatibility defaults are not a policy input"},
	}
	for _, testCase := range rejected {
		policy, err := ParseStartupPolicy(testCase.text)
		require.Error(t, err, "%q must be rejected (%s)", testCase.text, testCase.because)
		require.NotEmpty(t, err.Error(), "%q: the refusal must explain itself", testCase.text)
		require.True(t, policy.IsZero(), "%q: a rejected input must not return a partially applied policy: %+v", testCase.text, policy)
	}
}

// TestStartupPolicyNeverInjectsALinkFlagField is the security-shaped half: an operator-controlled
// string reaches `-X runtime.godebugDefault=<composed>`. Every accepted composition must be fields
// of the form key=value only, with no separator or whitespace inside a field, so no input can add a
// second setting (or a shell word) by smuggling a comma or a newline through a value.
func TestStartupPolicyNeverInjectsALinkFlagField(t *testing.T) {
	inputs := []string{
		"", "inherit", "memprofilerate=off", "adaptivestackstart=off",
		"memprofilerate=0,adaptivestackstart=0",
		"memprofilerate=18446744073709551616", // overflow: rejected, but the property is asserted anyway
		"memprofilerate=off,memprofilerate=0", // duplicate: rejected
		"memprofilerate=off,foo=bar",          // unknown: rejected
		"memprofilerate=off\nGODEBUG=1",       // newline: rejected
		"memprofilerate=off,-tags=evil",       // lookalike flag: rejected
	}
	for _, input := range inputs {
		policy, err := ParseStartupPolicy(input)
		if err != nil {
			continue
		}
		composed := policy.GODEBUGDefaults()
		require.True(t, strings.HasPrefix(composed, godebugDefaults), "%q: compatibility list lost", input)
		for _, field := range strings.Split(composed, ",") {
			require.NotEmpty(t, field, "%q: an empty field reached the link flag", input)
			require.NotContains(t, field, " ", "%q: a field contains a space: %q", input, field)
			require.NotContains(t, field, "\n", "%q: a field contains a newline: %q", input, field)
			require.NotContains(t, field, "\t", "%q: a field contains a tab: %q", input, field)
			key, _, found := strings.Cut(field, "=")
			require.True(t, found, "%q: a field has no '=': %q", input, field)
			require.True(t, compatibilityKey(key) || key == MemProfileRateSetting || key == AdaptiveStackStartSetting,
				"%q: an unexpected key %q reached the link flag", input, key)
		}
	}
}

func compatibilityKey(key string) bool {
	for _, entry := range compatibilityEntries {
		if strings.HasPrefix(entry, key+"=") {
			return true
		}
	}
	return false
}

// TestStartupPolicyVeryLongInputIsBounded feeds a megabyte of adversarial text. It must be refused
// (the vocabulary is small), and it must be refused by the parser rather than by a panic or an
// allocation that takes the build down: a CI variable can be arbitrarily long.
func TestStartupPolicyVeryLongInputIsBounded(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("a", 1<<20),
		"memprofilerate=" + strings.Repeat("9", 1<<20),
		strings.Repeat("memprofilerate=off,", 1<<16),
	} {
		policy, err := ParseStartupPolicy(text)
		require.Error(t, err, "a %d-byte input must be refused", len(text))
		require.True(t, policy.IsZero())
	}
	policy, err := ParseStartupPolicy(strings.Repeat(" ", 1<<20))
	require.NoError(t, err, "whitespace-only is the inherit policy, not an error")
	require.True(t, policy.IsZero())
}

// TestStartupPolicyRejectedInputLeavesTheReviewedDefaultAlone is the integration property the
// environment path depends on: a bad JJ_APPLE_STARTUP_POLICY must surface as an error, never as a
// silent fallback to the reviewed default (which would make a broken build look configured) and
// never as a partial policy.
func TestStartupPolicyRejectedInputLeavesTheReviewedDefaultAlone(t *testing.T) {
	for _, text := range []string{
		"memprofilerate=abc",
		"memprofilerate=off,adaptivestackstart=2",
		"unknown=1",
		"memprofilerate=off,memprofilerate=off",
	} {
		t.Setenv(StartupPolicyEnvironmentVariable, text)
		policy, err := StartupPolicyFromEnvironment()
		require.Error(t, err, "environment value %q must be refused", text)
		require.True(t, policy.IsZero(), "environment value %q must not fall back to the default", text)
		require.Contains(t, err.Error(), StartupPolicyEnvironmentVariable,
			"the error must name the variable so the operator knows what to unset")
	}
}

// TestStartupPolicyByteCheckCannotBeFooledByAPolicyArtifact is the verifier half: the inherit
// value is a prefix of every policy value, and the exact-match rule is what keeps an inherit
// artifact from verifying a policy build. It also pins the "second occurrence at EOF" branch of the
// scan, which a single-occurrence test cannot reach.
func TestStartupPolicyByteCheckCannotBeFooledByAPolicyArtifact(t *testing.T) {
	inherit := StartupPolicyInherit()
	off, err := ParseStartupPolicy("memprofilerate=off,adaptivestackstart=off")
	require.NoError(t, err)

	// The policy value occurs twice: once extended by another setting (must not match), once at
	// EOF (must match). A scanner that stopped at the first occurrence would report false.
	twice := []byte(off.GODEBUGDefaults() + ",x=1\x00" + off.GODEBUGDefaults())
	require.True(t, off.LinkedGODEBUGDefaultsArePresent(twice))

	require.True(t, inherit.LinkedGODEBUGDefaultsArePresent([]byte(inherit.GODEBUGDefaults())),
		"the exact value at EOF must match")
	require.False(t, inherit.LinkedGODEBUGDefaultsArePresent([]byte(inherit.GODEBUGDefaults()+",memprofilerate=0")),
		"a policy build must not satisfy the inherit check")
	require.False(t, off.LinkedGODEBUGDefaultsArePresent([]byte(inherit.GODEBUGDefaults())),
		"the inherited compatibility list must not satisfy a policy that asks for more")
}

// FuzzParseStartupPolicy holds the same invariants over arbitrary input: no panic; accepted input
// round-trips through String(); accepted input's fields are exactly the vocabulary; rejected input
// returns the zero policy. Run with:
//
//	go test ./cmd/internal/build_shared -run '^$' -fuzz '^FuzzParseStartupPolicy$' -fuzztime=60s
func FuzzParseStartupPolicy(f *testing.F) {
	for _, seed := range []string{
		"", "inherit", "memprofilerate=off,adaptivestackstart=off",
		"memprofilerate=1048576,adaptivestackstart=on",
		"memprofilerate=abc", "adaptivestackstart=2", "unknown=1",
		"memprofilerate=off,memprofilerate=off", "inherit,inherit",
		" MemProfileRate = OFF ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		policy, err := ParseStartupPolicy(text)
		if err != nil {
			if !policy.IsZero() {
				t.Fatalf("rejected input %q returned a partial policy %+v", text, policy)
			}
			return
		}
		reparsed, err := ParseStartupPolicy(policy.String())
		if err != nil {
			t.Fatalf("accepted input %q has a canonical form that does not parse: %v (%s)", text, err, policy.String())
		}
		if reparsed != policy {
			t.Fatalf("accepted input %q does not round-trip: %+v != %+v", text, reparsed, policy)
		}
		for _, entry := range policy.Entries() {
			key, value, found := strings.Cut(entry, "=")
			if !found {
				t.Fatalf("accepted input %q produced malformed entry %q", text, entry)
			}
			switch key {
			case MemProfileRateSetting:
				if _, err := strconv.Atoi(value); err != nil {
					t.Fatalf("accepted input %q produced a rate the runtime cannot read: %q", text, value)
				}
			case AdaptiveStackStartSetting:
				parsed, err := strconv.ParseInt(value, 10, 32)
				if err != nil || (parsed != 0 && parsed != 1) {
					t.Fatalf("accepted input %q produced a boolean the runtime cannot read: %q", text, value)
				}
			default:
				t.Fatalf("accepted input %q produced an unknown key %q", text, key)
			}
		}
		if composed := policy.GODEBUGDefaults(); !strings.HasPrefix(composed, godebugDefaults) {
			t.Fatalf("accepted input %q dropped the compatibility defaults: %q", text, composed)
		}
	})
}
