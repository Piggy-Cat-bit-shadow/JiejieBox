package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderHeaderSplitPreservesTheExactDocument(t *testing.T) {
	for _, content := range []string{"", "single", "\n", "header\n", "header\r\nbody\r\n", "header\nfirst\n\nlast\n"} {
		lines := strings.Split(content, "\n")
		first, rest := getFirstLine(content)
		if first != lines[0] || rest != strings.Join(lines[1:], "\n") {
			t.Fatalf("split changed %q: %q / %q", content, first, rest)
		}
	}
}

var providerHeaderSink, providerBodySink string

func BenchmarkProviderFirstLine(b *testing.B) {
	content := "# upload=1; download=2; total=3; expire=4;\n" + strings.Repeat("vless://node@example.com:443#node\n", 30000)
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for range b.N {
		providerHeaderSink, providerBodySink = getFirstLine(content)
	}
}

// Exercise a real download, format detection, decode and publication. The
// component benchmarks alone do not show the cost of parsing and node updates.
func BenchmarkProviderDocumentRefresh(b *testing.B) {
	document := "# upload=1; download=2; total=3; expire=4;\n" + buildConcurrentDocument(1<<20)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()
	provider, _ := newRemoteHarness(b, server.URL)
	provider.httpClient = server.Client()
	b.Cleanup(func() { _ = provider.Close() })
	b.ReportAllocs()
	b.SetBytes(int64(len(document)))
	b.ResetTimer()
	for range b.N {
		if err := provider.fetch(context.Background(), true); err != nil {
			b.Fatal(err)
		}
	}
}
