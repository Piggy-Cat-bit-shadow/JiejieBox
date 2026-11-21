package varch_test

import (
	"os"
	"path/filepath"
	"testing"
)

func evidenceFile(t *testing.T, parts ...string) string {
	t.Helper()
	all := append([]string{evidenceRoot(t)}, parts...)
	return filepath.Join(all...)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}

// evidenceRoot locates the round's evidence directory. It lives here rather than in one of the
// document tests so every file in this package can use it, including the profiles where the
// document tests are excluded.
func evidenceRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("V_EVIDENCE")
	if root == "" {
		root = `C:\Deepseek\JiejieBox-node-architecture-2026-11-22`
	}
	// The fixtures and ledgers are artefacts of the round, checked in with the evidence package, not
	// part of the module. A runner that does not carry them must SKIP (and say so), not FAIL: a test
	// that fails for a missing artefact trains people to ignore the colour. `V_EVIDENCE` points at
	// the directory when it lives somewhere else.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("evidence directory %s is not present on this host; this test needs the round's artefacts", root)
	}
	return root
}
