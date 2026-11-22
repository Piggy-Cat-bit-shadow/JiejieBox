package parser

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func linkMemoryDocument(nodes int, padding int) string {
	builder := &strings.Builder{}
	builder.Grow(nodes*90 + padding)
	for index := 0; index < nodes; index++ {
		if index > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("trojan://study@198.51.100.")
		builder.WriteString(strconv.Itoa(7 + index%200))
		builder.WriteString(":443?security=tls&sni=cold.example#node-")
		fmt.Fprintf(builder, "%05d", index)
	}
	if padding > 0 {
		builder.WriteString("\n")
		builder.WriteString(strings.Repeat("P", padding))
	}
	return builder.String()
}

// TestParseRawSubscriptionReleasesDocumentPadding pins G02's link-list ownership boundary. A
// share-link line is a substring of the whole subscription; the link parsers keep substrings of
// their input in option fields, so without copying the line the parsed nodes pin the document
// allocation (including an ignored padding tail). The 4 MiB padding must be collectable after the
// parser drops its own input reference.
func TestParseRawSubscriptionReleasesDocumentPadding(t *testing.T) {
	const padding = 4 << 20
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	document := linkMemoryDocument(100, padding)
	require.Greater(t, len(document), padding)

	outbounds, err := ParseSubscription(context.Background(), document, nil, "held")
	require.NoError(t, err)
	require.NotEmpty(t, outbounds)
	document = ""

	// Two collections make the padding unreachable in the same cycle as the input string; the
	// parsed option values are kept alive across the read so their (small) allocations count.
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	runtime.KeepAlive(outbounds)
	if stats.HeapAlloc > before.HeapAlloc+2<<20 {
		t.Fatalf("parsed link list retained the document: heap=%d baseline=%d padding=%d, want < baseline+2 MiB", stats.HeapAlloc, before.HeapAlloc, padding)
	}
}
