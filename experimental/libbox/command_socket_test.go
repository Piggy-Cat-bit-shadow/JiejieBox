package libbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The line this pins is the one that killed a re-signed tunnel on a device:
//
//	listen unix /var/mobile/Containers/Data/PluginKitPlugin/<UUID>/Library/Application
//	Support/JiejieBox-Shared/command.sock: bind: invalid argument
//
// Darwin's sockaddr_un.sun_path is 104 bytes, so a path of 138 fails in bind(2) with EINVAL, which
// reads like a malformed address rather than an over-long name.

const deepContainerBase = "/var/mobile/Containers/Data/PluginKitPlugin/BCBBA237-DE2B-4A84-B1CA-2C8401EBF60B/Library/Application Support/JiejieBox-Shared"

func TestTheReportedContainerPathIsTooLong(t *testing.T) {
	t.Parallel()
	// The failure is a property of the path, not of the device: if this ever stops being true the
	// fallback below is no longer what keeps the tunnel alive, and the test should say so.
	if got := len(filepath.Join(deepContainerBase, commandSocketName)); got <= maxUnixSocketPath {
		t.Fatalf("the reported container path is %d bytes; it is supposed to exceed %d", got, maxUnixSocketPath)
	}
}

func TestALongBasePathResolvesToAShortSocket(t *testing.T) {
	t.Parallel()
	resolved := resolveCommandSocketPath(deepContainerBase)
	if commandSocketPathTooLong(resolved) {
		t.Fatalf("resolved path is still %d bytes: %s", len(resolved), resolved)
	}
	if !strings.HasSuffix(resolved, ".sock") {
		t.Fatalf("resolved path is not a socket name: %s", resolved)
	}
	// It must not be a truncation of the deep path: a truncated name would point at a directory
	// that does not exist.
	if strings.Contains(resolved, "PluginKitPlugin") {
		t.Fatalf("the deep container leaked into the fallback: %s", resolved)
	}
}

func TestAShortBasePathIsUsedVerbatim(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	want := filepath.Join(base, commandSocketName)
	if got := resolveCommandSocketPath(base); got != want {
		t.Fatalf("a short base path was changed: got %s, want %s", got, want)
	}
}

func TestTheFallbackIsStableForOneBasePath(t *testing.T) {
	t.Parallel()
	// The server and the client run this rule independently and must agree without talking.
	first := resolveCommandSocketPath(deepContainerBase)
	second := resolveCommandSocketPath(deepContainerBase)
	if first != second {
		t.Fatalf("the rule is not deterministic: %s vs %s", first, second)
	}
	other := resolveCommandSocketPath(deepContainerBase + "-other")
	if other == first {
		t.Fatal("two different base paths collapsed onto one socket")
	}
}

func TestTheFallbackLivesInTheProcessTempDirectory(t *testing.T) {
	t.Parallel()
	resolved := resolveCommandSocketPath(deepContainerBase)
	if filepath.Dir(resolved) != filepath.Clean(os.TempDir()) {
		t.Fatalf("the fallback is not in the process temp directory: %s", resolved)
	}
}
