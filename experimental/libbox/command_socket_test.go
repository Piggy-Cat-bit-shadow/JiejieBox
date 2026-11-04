package libbox

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The three device reports, as inputs.
//
// # Why the tests are shaped this way
//
// Two earlier rounds of tests passed while the tunnel failed, because they fed the resolver a
// short directory of their own choosing. On a device the extension is a Packet Tunnel with no App
// Group: its base path is `<PluginKit container>/Library/Application Support/JiejieBox-Shared`
// (133 bytes with this UUID), the tempPath it passes is `<shared>/Library/Caches` (148), and the
// only short directory it owns is TMPDIR, `<container>/tmp` (92). A test that does not model
// those three together is not testing the product.
const (
	deviceUUID    = "5B6182FE-3CD7-4330-A11D-6336B908C07A"
	devicePlugin  = "/private/var/mobile/Containers/Data/PluginKitPlugin/" + deviceUUID
	deviceShared  = devicePlugin + "/Library/Application Support/JiejieBox-Shared"
	deviceCaches  = deviceShared + "/Library/Caches"
	deviceTempDir = devicePlugin + "/tmp"
)

func TestTheDeviceLayoutResolvesInTheContainerTemporaryDirectory(t *testing.T) {
	t.Parallel()
	resolved, err := resolveCommandSocketPathIn(deviceShared, deviceCaches, deviceTempDir)
	if err != nil {
		t.Fatalf("the device layout has no bindable path: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("resolved %d bytes: %s", len(resolved), resolved)
	}
	if dir := filepath.Dir(resolved); dir != filepath.Clean(deviceTempDir) {
		t.Fatalf("resolved into %q, want the container temporary directory", dir)
	}
	if !isGeneratedSocketName(filepath.Base(resolved)) {
		t.Fatalf("resolved name is not generated: %s", filepath.Base(resolved))
	}
	// 92-byte directory, separator, and a nine-byte name: the ten bytes the directory leaves.
	if len(resolved) != 102 {
		t.Fatalf("resolved %d bytes, expected 102", len(resolved))
	}
}

// The same layout with the temp path the extension actually passes and the temp path it does not:
// the answer must not depend on which of the two the caller happens to hand over.
func TestTheAnswerDoesNotDependOnThePlatformTempArgument(t *testing.T) {
	t.Parallel()
	for _, tempArgument := range []string{deviceCaches, deviceTempDir, "", devicePlugin} {
		resolved, err := resolveCommandSocketPathIn(deviceShared, tempArgument, deviceTempDir)
		if err != nil {
			t.Fatalf("temp argument %q: %v", tempArgument, err)
		}
		if !pathFits(resolved) {
			t.Fatalf("temp argument %q resolved %d bytes", tempArgument, len(resolved))
		}
	}
}

func TestTheSystemTemporaryDirectoryIsAlwaysACandidate(t *testing.T) {
	t.Parallel()
	// This is the defect that survived two rounds: os.TempDir() was consulted only when the
	// caller passed no temp path, and the extension always passes one.
	candidates := commandSocketCandidates(deviceShared, deviceCaches, deviceTempDir)
	found := false
	for _, dir := range candidates {
		if dir == filepath.Clean(deviceTempDir) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the system temporary directory is not a candidate: %v", candidates)
	}
}

func TestAShortBasePathKeepsTheDocumentedName(t *testing.T) {
	t.Parallel()
	base := "/var/mobile/Containers/Data/Application/ABC/Library/Shared"
	resolved, err := resolveCommandSocketPathIn(base, "", "/tmp")
	if err != nil {
		t.Fatalf("a short base path failed: %v", err)
	}
	if want := filepath.Join(base, commandSocketName); resolved != want {
		t.Fatalf("a short base path was changed: got %s, want %s", resolved, want)
	}
}

func TestTheResultNeverExceedsTheLimit(t *testing.T) {
	t.Parallel()
	// Depths from tiny to hopeless, all three inputs moving together.
	uuid := strings.Repeat("a", 36)
	for _, rootLen := range []int{4, 40, 70, 92, 100, 110, 130, 160} {
		root := "/" + strings.Repeat("r", rootLen-1)
		base := root + "/Library/Application Support/JiejieBox-Shared"
		temp := base + "/Library/Caches"
		sysTemp := root + "/" + uuid + "/tmp"
		resolved, err := resolveCommandSocketPathIn(base, temp, sysTemp)
		if err != nil {
			continue // a layout that cannot fit must say so, not emit a long path
		}
		if !pathFits(resolved) {
			t.Fatalf("root %d resolved %d bytes", rootLen, len(resolved))
		}
	}
}

func TestABindableBasePathAtTheLimitIsUsedVerbatim(t *testing.T) {
	t.Parallel()
	// base + "/command.sock" is exactly 103: the documented name must still be chosen.
	base := "/" + strings.Repeat("a", maxUnixSocketPath-len("/"+commandSocketName)-1)
	resolved, err := resolveCommandSocketPathIn(base, "", deviceTempDir)
	if err != nil {
		t.Fatalf("a base path at the limit failed: %v", err)
	}
	if want := filepath.Join(base, commandSocketName); resolved != want {
		t.Fatalf("at the limit: got %s, want %s (%d bytes)", resolved, want, len(want))
	}
	// One byte more and the documented name no longer fits; the answer must be a shorter path,
	// never the 104-byte one.
	over, err := resolveCommandSocketPathIn(base+"a", "", deviceTempDir)
	if err != nil {
		t.Fatalf("one byte over produced no path: %v", err)
	}
	if !pathFits(over) {
		t.Fatalf("one byte over resolved %d bytes", len(over))
	}
	if over == filepath.Join(base+"a", commandSocketName) {
		t.Fatal("the over-limit path was returned verbatim")
	}
}

func TestMultibyteDirectoriesAreMeasuredInBytes(t *testing.T) {
	t.Parallel()
	base := "/" + strings.Repeat("文", 30) // 90 bytes
	resolved, err := resolveCommandSocketPathIn(base, "", deviceTempDir)
	if err != nil {
		t.Fatalf("multibyte base failed: %v", err)
	}
	if !pathFits(resolved) {
		t.Fatalf("multibyte path resolved to %d bytes", len(resolved))
	}
}

func TestAnImpossibleLayoutReportsTheTrueShortestBudget(t *testing.T) {
	t.Parallel()
	// Every candidate is over the limit. The message must report the budget of the SHORTEST
	// candidate, not of the last one in the list - the earlier implementation reported whichever
	// came last, so a helpful directory could be hidden by an unhelpful one.
	deep := "/" + strings.Repeat("x", 110)
	_, err := resolveCommandSocketPathIn(deep+"/base", deep+"/base/caches", deep+"/tmp")
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
	if !strings.Contains(message, "0 bytes for a name") {
		t.Fatalf("the error does not report a clamped budget: %s", message)
	}
}

func TestOneViableCandidateInTheMiddleIsFound(t *testing.T) {
	t.Parallel()
	// Base path and platform temp are hopeless; the system temp fits. The resolver must not stop
	// at the first two.
	deep := "/" + strings.Repeat("x", 120)
	resolved, err := resolveCommandSocketPathIn(deep+"/base", deep+"/base/caches", "/tmp")
	if err != nil {
		t.Fatalf("the viable candidate was not found: %v", err)
	}
	if !strings.HasPrefix(filepath.ToSlash(resolved), "/tmp/") {
		t.Fatalf("resolved %s, want a path under /tmp", resolved)
	}
}

func TestTheFallbackNameIsStableAndDistinct(t *testing.T) {
	t.Parallel()
	first, err := resolveCommandSocketPathIn(deviceShared, deviceCaches, deviceTempDir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second, err := resolveCommandSocketPathIn(deviceShared, deviceCaches, deviceTempDir)
	if err != nil || second != first {
		t.Fatalf("not deterministic: %s vs %s (%v)", first, second, err)
	}
	other, err := resolveCommandSocketPathIn(deviceShared+"-other", deviceCaches, deviceTempDir)
	if err != nil {
		t.Fatalf("resolve other: %v", err)
	}
	if other == first {
		t.Fatal("two different base paths collapsed onto one socket")
	}
}

func TestOnlyGeneratedNamesAreEverRemoved(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"command.sock": false,
		"h13de0c06":    true,
		"h1a2b":        true,
		"h123":         false,
		"h123456789":   false,
		"hzzzz":        false,
		"socket":       false,
		"h":            false,
	} {
		if got := isGeneratedSocketName(name); got != want {
			t.Fatalf("isGeneratedSocketName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTheGeneratedNameFitsTheBudgetItWasGiven(t *testing.T) {
	t.Parallel()
	for budget := 0; budget <= 20; budget++ {
		name, ok := hashedSocketName("/some/base/command.sock", budget)
		if !ok {
			continue
		}
		if len(name) > budget {
			t.Fatalf("budget %d produced a %d-byte name: %s", budget, len(name), name)
		}
		if !isGeneratedSocketName(name) {
			t.Fatalf("budget %d produced a name the cleanup rule would not recognise: %s", budget, name)
		}
	}
}

func TestTheDiagnosticWithholdsTheContainer(t *testing.T) {
	t.Parallel()
	got := socketPathDiagnostics(deviceTempDir + "/h91d35256")
	if strings.Contains(got, "PluginKitPlugin") {
		t.Fatalf("the diagnostic discloses the container: %s", got)
	}
	if !strings.Contains(got, "h91d35256") || !strings.Contains(got, fmt.Sprintf("%d-byte", len(deviceTempDir))) {
		t.Fatalf("the diagnostic lost the useful parts: %s", got)
	}
}
