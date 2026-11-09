package rule

// C06 — the keyword matcher's compile limits are part of its contract, not an optimisation detail.
//
// A rule set below the compile threshold keeps the keyword loop; one inside the limits gets the
// frozen Aho-Corasick index; one past a limit must fall back to the loop instead of growing an
// unbounded index. The constructor says that with nil. These cases pin each boundary, because a
// silent change here would either pay index memory for a handful of keywords or build an index the
// caps exist to refuse.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func numberedKeywords(count int) []string {
	patterns := make([]string, count)
	for index := range patterns {
		patterns[index] = fmt.Sprintf("keyword-%05d", index)
	}
	return patterns
}

func TestKeywordMatcherCompilesOnlyInsideItsLimits(t *testing.T) {
	oversized := make([]string, domainKeywordCompileThreshold)
	for index := range oversized {
		oversized[index] = strings.Repeat("x", 20000)
	}
	cases := []struct {
		name     string
		patterns []string
		compiled bool
	}{
		{"below the threshold stays on the loop", numberedKeywords(domainKeywordCompileThreshold - 1), false},
		{"at the threshold compiles", numberedKeywords(domainKeywordCompileThreshold), true},
		{"at the pattern maximum compiles", numberedKeywords(domainKeywordMaxPatterns), true},
		{"past the pattern maximum falls back", numberedKeywords(domainKeywordMaxPatterns + 1), false},
		{"past the byte maximum falls back", oversized, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			matcher := newDomainKeywordMatcher(testCase.patterns)
			if testCase.compiled {
				require.NotNil(t, matcher, "a set inside the limits must be compiled")
				require.NotEmpty(t, matcher.nodes)
				return
			}
			require.Nil(t, matcher, "a set outside the limits must say so, so the caller keeps the loop")
		})
	}
}
