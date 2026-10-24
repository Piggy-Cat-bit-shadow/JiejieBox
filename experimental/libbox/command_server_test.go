package libbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An unwritable base path must fail at once. The old retry loop slept for 30 seconds on
// EROFS/ENOTDIR-style errors, which is indistinguishable from a hang to a daemon supervisor and
// to the user watching the app start.
func TestCommandServerListenFailsImmediately(t *testing.T) {
	previousBase := sBasePath
	previousPort := sCommandServerListenPort
	t.Cleanup(func() {
		sBasePath = previousBase
		sCommandServerListenPort = previousPort
	})

	baseFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(baseFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sBasePath = baseFile
	sCommandServerListenPort = 0

	server := &CommandServer{}
	started := time.Now()
	err := server.Start()
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("listen on a file path unexpectedly succeeded")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("listen failure took %v; an unwritable base path must fail immediately", elapsed)
	}
}
