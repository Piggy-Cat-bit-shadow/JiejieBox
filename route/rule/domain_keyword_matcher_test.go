package rule

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The compiled large-set matcher must be indistinguishable from the keyword loop. These tests are
// differential: the same keywords and host go through both paths, including empty keywords, raw
// uppercase keywords, non-ASCII and invalid UTF-8 bytes, so a semantic drift fails on the value
// rather than on a benchmark.

func naiveDomainKeywordMatch(keywords []string, host string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, keyword := range keywords {
		if strings.Contains(host, keyword) {
			return true
		}
	}
	return false
}

func keywordMetadata(host string) *adapter.InboundContext {
	return &adapter.InboundContext{
		Domain:      host,
		Destination: M.Socksaddr{Fqdn: host},
	}
}

func randomKeyword(rng *rand.Rand, alphabet []byte) string {
	length := rng.Intn(7)
	keyword := make([]byte, length)
	for index := range keyword {
		keyword[index] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(keyword)
}

func TestDomainKeywordMatcherEqualsLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(20261108))
	alphabet := []byte("abcxyz.-0123ABCXYZ_/\u00e9\u4e2d")
	sizes := []int{0, 1, 2, 10, 63, 64, 65, 200}

	for _, size := range sizes {
		keywords := make([]string, size)
		for index := range keywords {
			keywords[index] = randomKeyword(rng, alphabet)
		}
		item := NewDomainKeywordItem(keywords)
		if size >= domainKeywordCompileThreshold {
			require.NotNil(t, item.matcher, "a %d-keyword set must take the compiled path", size)
		} else {
			require.Nil(t, item.matcher, "a %d-keyword set must stay on the loop", size)
		}
		for hostIndex := 0; hostIndex < 40; hostIndex++ {
			host := randomKeyword(rng, alphabet)
			want := naiveDomainKeywordMatch(keywords, host)
			require.Equal(t, want, item.Match(keywordMetadata(host)),
				"size=%d host=%q keywords=%q", size, host, keywords)
		}
	}
}

func TestDomainKeywordMatcherEmptyKeywordMeansAnyNonEmptyHost(t *testing.T) {
	keywords := make([]string, domainKeywordCompileThreshold)
	keywords[7] = ""
	item := NewDomainKeywordItem(keywords)
	require.NotNil(t, item.matcher)
	require.True(t, item.Match(keywordMetadata("x")))
	require.False(t, item.Match(keywordMetadata("")))
}

func TestDomainKeywordMatcherKeywordCaseIsNotNormalised(t *testing.T) {
	keywords := make([]string, domainKeywordCompileThreshold)
	keywords[0] = "UPPER"
	keywords[1] = "\u00c9X"
	for index := 2; index < len(keywords); index++ {
		keywords[index] = "filler-" + itoa(index)
	}
	item := NewDomainKeywordItem(keywords)
	require.NotNil(t, item.matcher)
	require.False(t, item.Match(keywordMetadata("upper")),
		"keywords are byte-exact in the loop; the matcher must not silently lowercase them")
	require.False(t, item.Match(keywordMetadata("\u00e9x")))
}

func TestDomainKeywordMatcherFallsBackPastItsNodeBudget(t *testing.T) {
	keywords := make([]string, domainKeywordCompileThreshold)
	// 64 keywords with distinct first code points; together they exceed the 65,536-node budget while
	// staying under the byte cap.
	for index := range keywords {
		keywords[index] = string(rune(0x100+index)) + strings.Repeat("x", 1999)
	}
	item := NewDomainKeywordItem(keywords)
	require.Nil(t, item.matcher, "a set past the node budget must fall back to the loop")
	require.True(t, item.Match(keywordMetadata(keywords[0])),
		"the fallback must keep matching, not drop the rule")
	require.False(t, item.Match(keywordMetadata(strings.Repeat("z", 2001))))
}

func TestDomainKeywordMatcherUsesDestinationFqdnFallback(t *testing.T) {
	keywords := make([]string, domainKeywordCompileThreshold)
	keywords[0] = "needle"
	item := NewDomainKeywordItem(keywords)
	metadata := &adapter.InboundContext{Destination: M.Socksaddr{Fqdn: "host.needle.example"}}
	require.True(t, item.Match(metadata))
}

func BenchmarkDomainKeywordMiss(b *testing.B) {
	keywords := make([]string, 1024)
	for index := range keywords {
		keywords[index] = "keyword-" + strings.Repeat("x", index%17) + "-" + string(rune('a'+index%26)) + "-" + itoa(index)
	}
	compiled := NewDomainKeywordItem(keywords)
	looped := &DomainKeywordItem{keywords: keywords}
	metadata := keywordMetadata("this-host-matches-nothing.example")

	b.Run("compiled", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = compiled.Match(metadata)
		}
	})
	b.Run("loop", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = looped.Match(metadata)
		}
	})
}

func BenchmarkDomainKeywordLateHit(b *testing.B) {
	keywords := make([]string, 1024)
	for index := range keywords {
		keywords[index] = "keyword-" + strings.Repeat("x", index%17) + "-" + string(rune('a'+index%26)) + "-" + itoa(index)
	}
	compiled := NewDomainKeywordItem(keywords)
	looped := &DomainKeywordItem{keywords: keywords}
	metadata := keywordMetadata("host-with-" + keywords[len(keywords)-1] + ".example")

	b.Run("compiled", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = compiled.Match(metadata)
		}
	})
	b.Run("loop", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = looped.Match(metadata)
		}
	})
}

// itoa avoids pulling a formatting dependency into the hot test fixture.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [8]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
