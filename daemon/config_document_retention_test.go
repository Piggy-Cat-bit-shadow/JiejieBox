package daemon

// The profile document must not outlive the parse.
//
// option.Options.UnmarshalJSONContext stores the document it decoded in RawMessage, for the CLI's
// config-merge path. The daemon never merges, so before this fix a service kept a copy of the
// profile sized by the file - not by what the file declares - for its whole lifetime: a config
// with 5 MiB of trailing whitespace left 5,266,072 B of live heap after a successful apply, and
// the heap profile put 5,128.23 kB of it in the stripJSONComments buffer that RawMessage aliases.
//
// Trailing whitespace is the instrument: it is valid JSON, the decoder ignores it, and it changes
// nothing about the parsed tree. Any heap that scales with it is a copy of the document.

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/sagernet/sing/common/json"
	"github.com/stretchr/testify/require"
)

const retentionTestDocument = `{"log":{"level":"error"}}`

func retentionHeapAfterGC() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// TestParseConfigDoesNotRetainTheDocument is the direct assertion. It fails the moment the
// RawMessage clear is removed.
//
// The capacity is what matters, not the length. option.Options.UnmarshalJSONContext is handed a
// sub-slice of the buffer stripJSONComments copied the document into, so RawMessage is a view
// whose len() is the parsed value's span and whose cap() is the whole profiled document. A
// 25-byte view with a 4 MiB cap keeps all 4 MiB alive; that is why the retention scales with the
// file and not with what the file declares.
func TestParseConfigDoesNotRetainTheDocument(t *testing.T) {
	const padding = 4 << 20
	document := retentionTestDocument + strings.Repeat(" ", padding)

	options, err := parseConfig(context.Background(), document)
	require.NoError(t, err)
	require.NotNil(t, options.Log, "the parsed tree must survive")
	require.Equal(t, "error", options.Log.Level)

	if len(options.RawMessage) != 0 || cap(options.RawMessage) != 0 {
		t.Fatalf("parseConfig kept a view (len %d, cap %d) into the %d-byte document; "+
			"the backing array is pinned for the life of the options",
			len(options.RawMessage), cap(options.RawMessage), len(document))
	}
	runtime.KeepAlive(options)
}

// TestParseConfigRetentionDoesNotScaleWithTheDocument is the magnitude assertion. It measures the
// live heap around the parse and requires that the retention is a fraction of the padding. The
// threshold is deliberately loose - it separates "the document is held" (>= padding) from
// "nothing meaningful is held" (a few KiB of parsed tree and test noise), not a precise count.
func TestParseConfigRetentionDoesNotScaleWithTheDocument(t *testing.T) {
	const padding = 4 << 20
	document := retentionTestDocument + strings.Repeat(" ", padding)

	// Warm the allocator so the first parse's growth is not counted as retention.
	warm, err := parseConfig(context.Background(), retentionTestDocument)
	require.NoError(t, err)
	runtime.KeepAlive(warm)

	baseline := retentionHeapAfterGC()
	options, err := parseConfig(context.Background(), document)
	require.NoError(t, err)
	after := retentionHeapAfterGC()

	if after > baseline {
		retained := after - baseline
		t.Logf("live heap %d B -> %d B, retained %d B for a %d-byte document (%d B padding)",
			baseline, after, retained, len(document), padding)
		if retained >= padding/2 {
			t.Fatalf("parseConfig retained %d B of a %d-byte document; the document is still held",
				retained, len(document))
		}
	} else {
		t.Logf("live heap %d B -> %d B (no growth)", baseline, after)
	}
	runtime.KeepAlive(options)
}

// TestParseConfigMarshalsTheSameWithAndWithoutTheDocument guards the semantic half: RawMessage is
// `json:"-"`, so clearing it must not change what the options marshal to. If this ever fails, the
// clear is not a pure retention change.
func TestParseConfigMarshalsTheSameWithAndWithoutTheDocument(t *testing.T) {
	ctx := context.Background()

	plain, err := parseConfig(ctx, retentionTestDocument)
	require.NoError(t, err)
	padded, err := parseConfig(ctx, retentionTestDocument+strings.Repeat(" ", 64<<10))
	require.NoError(t, err)

	plainJSON, err := json.MarshalContext(ctx, plain)
	require.NoError(t, err)
	paddedJSON, err := json.MarshalContext(ctx, padded)
	require.NoError(t, err)

	require.Equal(t, string(plainJSON), string(paddedJSON),
		"padding a document must not change the marshalled options")
	require.NotContains(t, string(plainJSON), `"RawMessage"`,
		"RawMessage must never be marshalled; it is json:\"-\"")
}

// TestParseConfigRejectsInvalidDocuments keeps the error path honest: the clear must not have
// turned a decode failure into a silently empty options value.
func TestParseConfigRejectsInvalidDocuments(t *testing.T) {
	_, err := parseConfig(context.Background(), `{"log":`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode config")
}
