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
