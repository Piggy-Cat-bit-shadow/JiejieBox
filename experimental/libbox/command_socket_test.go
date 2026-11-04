package libbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The line these tests exist for, reported from a device on 2026-11-04:
//
//	start command server error: listen command server: socket path is 110 bytes, the kernel
//	allows 103: /private/var/mobile/Containers/Data/PluginKitPlugin/<UUID>/tmp/jjb-13de0c06.sock
//
// The first fix moved the socket to os.TempDir() on the assumption that it is short. On a
// re-signed iOS build the Packet Tunnel's temporary directory is inside its PluginKit container
// and is 92 bytes before a name is appended, so the fallback itself was 110 bytes.
const (
	reportedContainerRoot = "/private/var/mobile/Containers/Data/PluginKitPlugin/5B6182FE-3CD7-4330-A11D-6336B908C07A"
	reportedTempDir       = reportedContainerRoot + "/tmp"
	reportedBasePath      = reportedContainerRoot + "/Library/Application Support/JiejieBox-Shared"
)

func TestTheReportedPathsReproduceTheFailure(t *testing.T) {
	t.Parallel()
	// The inputs, not a copy of the screenshot's output: these lengths are what the device
	// reported, and the old implementation's fallback is what produced 110.
	oldFallback := filepath.Join(reportedTempDir, "jjb-13de0c06.sock")
	if len(oldFallback) != 110 {
		t.Fatalf("the reported fallback is %d bytes, expected 110: %s", len(oldFallback), oldFallback)
	}
	if len(reportedBasePath+"/command.sock") <= maxUnixSocketPath {
		t.Fatal("the reported base path is short enough to bind; it is not the failing case")
	}
}

func TestTheReportedFailureResolvesToABindablePath(t *testing.T) {
	t.Parallel()
	resolved, err := resolveCommandSocketPath(reportedBasePath, reportedTempDir)
	if err != nil {
		t.Fatalf("the reported layout has no bindable path: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolved path is %d bytes: %s", len(resolved), resolved)
	}
	// The name must be one this package generates. Which directory wins depends on the host's
	// separator rules (Windows treats these POSIX strings differently), so the container-root
	// expectation lives in the Darwin test, where the kernel limit is the one being satisfied.
	if !isGeneratedSocketName(filepath.Base(resolved)) {
		t.Fatalf("resolved name is not a generated one: %s", filepath.Base(resolved))
	}
}

func TestABindableBasePathIsUsedVerbatim(t *testing.T) {
	t.Parallel()
	base := "/var/mobile/Containers/Data/Application/ABC/Library/Shared"
	resolved, err := resolveCommandSocketPath(base, "/tmp")
	if err != nil {
		t.Fatalf("a short base path failed: %v", err)
	}
	want := filepath.Join(base, commandSocketName)
	if resolved != want {
		t.Fatalf("a short base path was changed: got %s, want %s", resolved, want)
	}
}

func TestTheByteBoundaryIsExact(t *testing.T) {
	t.Parallel()
	// 103 fits; 104 does not. The boundary is the kernel's, not a guess: Darwin's sun_path[104]
	// holds 103 bytes plus the terminating NUL.
	base := "/" + strings.Repeat("a", maxUnixSocketPath-len("/"+commandSocketName)-1)
	resolved, err := resolveCommandSocketPath(base, reportedTempDir)
	if err != nil {
		t.Fatalf("a base path at the limit failed: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("at the limit, resolved %d bytes", len(resolved))
	}
	over, err := resolveCommandSocketPath(base+"a", reportedTempDir)
	if err != nil {
		t.Fatalf("one byte over produced no path: %v", err)
	}
	if !pathFits(over) {
		t.Fatalf("one byte over resolved to %d bytes", len(over))
	}
	// "Verbatim" would be <over>/command.sock, which is 104 bytes. Using the same DIRECTORY with a
	// generated short name is the correct answer, so the invariant is about the path, not the dir.
	if over == filepath.Join(base+"a", commandSocketName) {
		t.Fatal("a base path over the limit was used verbatim")
	}
}

func TestMultibyteDirectoriesAreMeasuredInBytes(t *testing.T) {
	t.Parallel()
	// The kernel counts bytes, not runes. A path of three-byte characters must be measured the
	// same way, or the budget is wrong by a factor of three.
	base := "/" + strings.Repeat("文", 30) // 90 bytes plus the separator
	resolved, err := resolveCommandSocketPath(base, reportedTempDir)
	if err != nil {
		t.Fatalf("multibyte base failed: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("multibyte path resolved to %d bytes", len(resolved))
	}
}

func TestAnImpossibleLayoutFailsWithSizesNotPaths(t *testing.T) {
	t.Parallel()
	// Deep enough that no candidate leaves room for even a minimum name: the failure must be a
	// returned error that reports sizes, never an EINVAL from bind and never the private path.
	deep := "/" + strings.Repeat("x", 120)
	_, err := resolveCommandSocketPath(deep+"/a/b", deep+"/a/b/tmp")
	if err == nil {
		t.Fatal("an impossible layout resolved successfully")
	}
	message := err.Error()
	if strings.Contains(message, "xxxx") {
		t.Fatalf("the error discloses the container path: %s", message)
	}
	if !strings.Contains(message, "103") {
		t.Fatalf("the error does not name the limit: %s", message)
	}
}

func TestTheFallbackNameIsStableAndDistinct(t *testing.T) {
	t.Parallel()
	first, err := resolveCommandSocketPath(reportedBasePath, reportedTempDir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second, err := resolveCommandSocketPath(reportedBasePath, reportedTempDir)
	if err != nil || second != first {
		t.Fatalf("the rule is not deterministic: %s vs %s (%v)", first, second, err)
	}
	other, err := resolveCommandSocketPath(reportedBasePath+"-other", reportedTempDir)
	if err != nil {
		t.Fatalf("resolve other: %v", err)
	}
	if other == first {
		t.Fatal("two different base paths collapsed onto one socket")
	}
}

func TestOnlyGeneratedNamesAreEverRemoved(t *testing.T) {
	t.Parallel()
	// Cleanup may remove a stale socket only when this package could have created the name.
	for name, want := range map[string]bool{
		"command.sock":   false,
		"h13de0c06.sock": true,
		"h1234.sock":     true,
		"h12.sock":       false,
		"hzzzz.sock":     false,
		"socket":         false,
		"h1234":          false,
	} {
		if got := isGeneratedSocketName(name); got != want {
			t.Fatalf("isGeneratedSocketName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTheDirectoryDefaultIsUsedWhenNoTempIsGiven(t *testing.T) {
	t.Parallel()
	resolved, err := resolveCommandSocketPath(reportedBasePath, "")
	if err != nil {
		t.Fatalf("the default temp directory produced no path: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolved path is %d bytes", len(resolved))
	}
	if dir := filepath.Dir(resolved); dir != filepath.Clean(os.TempDir()) && dir != reportedContainerRoot {
		t.Fatalf("resolved into an unexpected directory: %s", dir)
	}
}
