package parser

import (
	"encoding/base64"
	"math/rand"
	"strings"
	"testing"
)

// This is the old decoder's permissive contract, including spaces, interior
// padding and malformed input being returned unchanged. Optimizing detection
// must not change which subscription or share link the caller receives.
func legacyDecodeBase64(content string) string {
	s := strings.ReplaceAll(content, " ", "-")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "=", "")
	decoded, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return content
	}
	return string(decoded)
}

func TestBase64DetectionPreservesTheExistingSubscriptionContract(t *testing.T) {
	cases := []string{"", "Zg==", "Z=g=", "Zm9v\r\nYmFy", "++//", "  //", "a", "{}", "proxies:\n", "vless://node", "\x00\xff"}
	random := rand.New(rand.NewSource(21))
	alphabet := "abcXYZ019-_/+= \r\n\t:{}\x00\xff"
	for range 2000 {
		input := make([]byte, random.Intn(80))
		for i := range input {
			input[i] = alphabet[random.Intn(len(alphabet))]
		}
		cases = append(cases, string(input))
	}
	for _, input := range cases {
		got, err := DecodeBase64URLSafe(input)
		if err != nil || got != legacyDecodeBase64(input) {
			t.Fatalf("input %q: got %q, %v; want %q", input, got, err, legacyDecodeBase64(input))
		}
	}
}

func FuzzBase64SubscriptionCompatibility(f *testing.F) {
	for _, seed := range []string{"Zg==", "Z=g=", "Zm9v\r\nYmFy", "++//", "{}", "vless://node", strings.Repeat("a", 128) + ":"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<20 {
			t.Skip("bound document allocations during fuzzing")
		}
		got, err := DecodeBase64URLSafe(input)
		if err != nil || got != legacyDecodeBase64(input) {
			t.Fatalf("decode changed input %q", input)
		}
	})
}

var decodedDocumentSink string

func BenchmarkDecodeProviderDocument(b *testing.B) {
	plain := "{\n" + strings.Repeat("  \"node\": \"https://host/path?a=b+c\",\n", 30000) + "}"
	for _, fixture := range []struct{ name, input string }{
		{"plain-json", plain},
		{"base64-url", base64.RawURLEncoding.EncodeToString([]byte(plain))},
		{"base64-standard", base64.StdEncoding.EncodeToString([]byte(plain))},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.input)))
			for range b.N {
				decodedDocumentSink, _ = DecodeBase64URLSafe(fixture.input)
			}
		})
	}
}
