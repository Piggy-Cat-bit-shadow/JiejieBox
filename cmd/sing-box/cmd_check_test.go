package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/naive"
	"github.com/sagernet/sing/common/json"
)

// TestCheckPrintsConstructionWarnings is the check-side half of the visibility fix.
//
// The configuration below is valid, so check must exit successfully. It also carries a
// construction-time warning: the naive inbound serves UDP and leaves
// quic_congestion_control unset, so construction selects the loss-based CUBIC sender.
//
// Three properties are asserted together, because each one alone was already true before
// the fix and proved nothing:
//
//   - the warning is printed at all (the log factory is flushed before Close);
//   - it is printed despite the configured level being "error" (check reviews warnings);
//   - it is printed on stderr, not into the service log file the configuration names.
func TestCheckPrintsConstructionWarnings(t *testing.T) {
	if !naive.HTTP3ListenerAvailable {
		t.Skip("naive HTTP/3 is not compiled into this binary; run with -tags with_quic")
	}
	preRun(mainCommand, nil)

	logPath := filepath.Join(t.TempDir(), "service.log")
	configContent := fmt.Sprintf(`{
  "log": {"level": "error", "output": %q},
  "inbounds": [{
    "type": "naive",
    "tag": "naive-in",
    "network": "udp",
    "users": [{"username": "user", "password": "password"}],
    "tls": {"enabled": true, "insecure": true}
  }],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`, logPath)
	options, err := json.UnmarshalExtendedContext[option.Options](globalCtx, []byte(configContent))
	if err != nil {
		t.Fatalf("the fixture must decode; a parse failure would make the assertion meaningless: %v", err)
	}

	stderrFile, err := os.CreateTemp(t.TempDir(), "check-stderr-")
	if err != nil {
		t.Fatalf("create captured stderr: %v", err)
	}
	originalStderr := os.Stderr
	os.Stderr = stderrFile
	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = stderrFile.Close()
	})

	err = checkOptions(options)
	if err != nil {
		t.Fatalf("check refused a valid configuration: %v", err)
	}

	if _, err = stderrFile.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind captured stderr: %v", err)
	}
	output, err := io.ReadAll(stderrFile)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}

	if !strings.Contains(string(output), "quic_congestion_control is unset") {
		t.Fatalf("check did not print the construction-time warning; stderr:\n%s", output)
	}
	if !strings.Contains(string(output), "WARN") {
		t.Fatalf("the warning must be emitted at WARN; stderr:\n%s", output)
	}
	if _, err = os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("check must report on stderr rather than create the configured log file; stat err: %v", err)
	}
}
