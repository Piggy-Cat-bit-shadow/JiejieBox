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
	if root := os.Getenv("V_EVIDENCE"); root != "" {
		return root
	}
	return `C:\Deepseek\JiejieBox-node-architecture-2026-11-22`
}
