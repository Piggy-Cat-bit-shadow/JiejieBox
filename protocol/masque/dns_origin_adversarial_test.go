package masque

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Adversarial cases for the same-connection DoH origin gate.
//
// The gate is what lets a DNS query ride the tunnel's authenticated authority. Loosening it leaks a
// query to a host the connection was never authenticated for, so these cases are mostly about
// staying STRICT: the case fix must not turn a different host, a different port or a different
// address family into a match.

func TestSameOriginCaseAndShapeMatrix(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		first     string
		second    string
		wantMatch bool
		reason    string
	}{
		{"capital A at the start", "Masque.example", "masque.example", true, "one host, two spellings"},
		{"capital A in the middle", "mAsque.example", "masque.example", true, "the fold must cover A inside the name"},
		{"capital A at the end", "masque.examplA", "masque.exampla", true, "the fold must cover A at the end"},
		{"A against a on each side", "masque.example", "MASQUE.EXAMPLE", true, "symmetric, unlike before the fix"},
		{"whole run uppercase", "ABCDEF.EXAMPLE", "abcdef.example", true, "every ASCII letter folds"},
		{"missing letter", "masque.example", "masqu.example", false, "length differs"},
		{"subdomain is not the parent", "a.example.com", "example.com", false, "a suffix is not the same origin"},
		{"parent is not the subdomain", "example.com", "a.example.com", false, "and not in the other direction either"},
		{"explicit default port", "masque.example:443", "masque.example", true, "the default port is the same origin"},
		{"non-default port", "masque.example:8443", "masque.example", false, "a port makes it a different origin"},
		{"two non-default ports differ", "masque.example:8443", "masque.example:9443", false, "ports must compare"},
		{"trailing root dot", "masque.example.", "masque.example", true, "the wire FQDN and the authority agree"},
		{"root dot on both", "masque.example.", "masque.example.", true, "and when both carry it"},
		{"IPv6 hex case", "[2001:DB8::1]:443", "[2001:db8::1]:443", true, "hex letters are ASCII letters too"},
		{"IPv6 capital A in hex", "[2001:db8::A]:443", "[2001:db8::a]:443", true, "A is the letter the old fold missed"},
		{"different IPv6", "[2001:db8::1]:443", "[2001:db8::2]:443", false, "different address"},
		{"punycode case", "xn--bcher-kva.example", "XN--BCHER-KVA.EXAMPLE", true, "punycode is ASCII"},
		{"different punycode", "xn--bcher-kva.example", "xn--bcher-kvb.example", false, "different label"},
		{"empty authorities", "", "", true, "two empty origins are equal"},
		{"empty against a dot", "", ".", false, "a root dot is not the empty name"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, testCase.wantMatch, sameOrigin(testCase.first, testCase.second), testCase.reason)
			require.Equal(t, testCase.wantMatch, sameOrigin(testCase.second, testCase.first),
				"the comparison must stay symmetric: %s", testCase.reason)
		})
	}
}

// TestSameOriginNormalizationStillResolvesUnicodeCase documents what the gate does with a non-ASCII
// host: normalizeHostName lowercases first, so the unicode case is already folded before the ASCII
// comparison runs. The ASCII-only comparison is about NOT folding unicode in the comparison itself.
func TestSameOriginNormalizationStillResolvesUnicodeCase(t *testing.T) {
	t.Parallel()

	require.True(t, sameOrigin("BÜCHER.example", "bücher.example"),
		"normalizeHostName lowercases with strings.ToLower, so the unicode case is settled there")
	require.False(t, sameOrigin("bücher.example", "bucher.example"),
		"and a real difference is still a difference")
}

// TestSameConnectionDoHStillRequiresEverything proves the gate was not loosened by the fold fix:
// same host is necessary and NOT sufficient. The existing capability tests cover the full matrix;
// this one states the load-bearing half directly.
func TestSameConnectionDoHStillRequiresEverything(t *testing.T) {
	t.Parallel()

	tunnelOrigin := h3Capability("masque.example.org")
	require.True(t, tunnelOrigin.sameH3OriginAvailable("MASQUE.Example.Org."),
		"the authenticated origin itself, in any spelling")
	require.False(t, tunnelOrigin.sameH3OriginAvailable("masque.example.org.evil"),
		"a suffix attack is not the same origin")
	require.False(t, tunnelOrigin.sameH3OriginAvailable("masque.example"),
		"a shorter name is not the same origin")
	require.False(t, tunnelOrigin.sameH3OriginAvailable("masque.example.org:8443"),
		"a different port is not the same origin")
}
