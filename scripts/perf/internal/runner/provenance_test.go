package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBinaryProvenanceDetectsReplacement is the pre/post check: a binary
// replaced while a run is in progress must not look like the same binary.
func TestBinaryProvenanceDetectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sing-box")
	if err := os.WriteFile(path, []byte("first build"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, changed, err := binaryProvenance("", path)
	if err != nil || before == "" {
		t.Fatalf("initial hash: hash=%q err=%v", before, err)
	}
	if changed != nil {
		t.Fatalf("missing before-hash must be unknown, got changed=%v", *changed)
	}
	after, changed, err := binaryProvenance(before, path)
	if err != nil || changed == nil || *changed || after != before {
		t.Fatalf("unchanged file: changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(path, []byte("second build"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, changed, err = binaryProvenance(before, path)
	if err != nil || changed == nil || !*changed || after == before {
		t.Fatalf("replaced file not detected: changed=%v after=%q err=%v", changed, after, err)
	}
}
