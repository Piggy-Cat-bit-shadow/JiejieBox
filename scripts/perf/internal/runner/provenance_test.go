package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBinaryStateDetectsReplacement is the pre/post check: a binary replaced
// while a run is in progress must not look like the same binary.
func TestBinaryStateDetectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sing-box")
	if err := os.WriteFile(path, []byte("first build"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, changed, err := binaryState("", path)
	if err != nil || changed || before == "" {
		t.Fatalf("initial hash: hash=%q changed=%v err=%v", before, changed, err)
	}
	after, changed, err := binaryState(before, path)
	if err != nil || changed || after != before {
		t.Fatalf("unchanged file: changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(path, []byte("second build"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, changed, err = binaryState(before, path)
	if err != nil || !changed || after == before {
		t.Fatalf("replaced file not detected: changed=%v after=%q err=%v", changed, after, err)
	}
}
