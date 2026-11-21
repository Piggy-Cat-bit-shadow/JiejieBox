package runtimeoverlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Prepare is what the builder actually calls, so the orchestration it performs needs a test of its
// own: resolve, probe, generate, write the shim, rewrite PATH, and refuse the cases that cannot be
// attributed to one plan.
func TestPrepareWritesOverlayAndShim(t *testing.T) {
	goBinary := goBinary(t)
	prepared, err := Prepare(PrepareRequest{
		Root:       repoRoot(t),
		Mode:       ModeSmall,
		BindTarget: "ios/arm64,macos/arm64",
		GoBinary:   goBinary,
		Env:        os.Environ(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if prepared.Overlay == nil {
		t.Fatal("mode small prepared nothing")
	}
	if prepared.Skipped != "" {
		t.Fatalf("an iOS-family target was reported as skipped: %s", prepared.Skipped)
	}
	if prepared.Toolchain.GOVersion == "" || prepared.Toolchain.GOROOT == "" {
		t.Fatalf("the toolchain was not recorded: %+v", prepared.Toolchain)
	}
	if prepared.ShimDir() == "" {
		t.Fatal("no shim directory was recorded, so the caller cannot put it on PATH")
	}
	shim := filepath.Join(prepared.ShimDir(), "go")
	if info, err := os.Stat(shim); err != nil || info.IsDir() {
		t.Fatalf("the shim is not a file: %v", err)
	}
	pathEntry := EnvValue(prepared.Env, "PATH")
	if !strings.HasPrefix(pathEntry, prepared.ShimDir()+string(os.PathListSeparator)) {
		t.Fatalf("the shim is not first on PATH: %q", pathEntry)
	}
	// Nothing has run yet, so the shim has injected nothing. A build that stops here must be able
	// to tell that apart from a build that used it.
	if count, err := prepared.InjectionCount(); err != nil || count != 0 {
		t.Fatalf("a freshly prepared overlay reported %d injections (%v)", count, err)
	}

	data, err := os.ReadFile(prepared.JSONPath)
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatalf("overlay.json does not parse: %v", err)
	}
	wantKeys := map[string]bool{
		filepath.Join(prepared.Toolchain.GOROOT, filepath.FromSlash(sourceMSpanSet)): false,
		filepath.Join(prepared.Toolchain.GOROOT, filepath.FromSlash(sourceMalloc)):   false,
		filepath.Join(prepared.Toolchain.GOROOT, filepath.FromSlash(sourceProc)):     false,
		filepath.Join(prepared.Toolchain.GOROOT, "src", "runtime", addedOnFile):      false,
		filepath.Join(prepared.Toolchain.GOROOT, "src", "runtime", addedOffFile):     false,
	}
	if len(overlay.Replace) != len(wantKeys) {
		t.Fatalf("overlay.json replaces %d files, want %d: %v", len(overlay.Replace), len(wantKeys), overlay.Replace)
	}
	for key := range overlay.Replace {
		if _, ok := wantKeys[key]; !ok {
			t.Fatalf("overlay.json replaces an unexpected path: %s", key)
		}
		wantKeys[key] = true
	}
	for key, seen := range wantKeys {
		if !seen {
			t.Fatalf("overlay.json does not replace %s", key)
		}
	}
	// Every replacement must live in the overlay's own directory: a path into the checkout would
	// mean a generated file escaped into the tree, where `go test ./...` would enumerate it.
	for key, value := range overlay.Replace {
		if !strings.HasPrefix(value, prepared.Dir) {
			t.Fatalf("%s is replaced by %s, which is outside the overlay directory %s", key, value, prepared.Dir)
		}
	}
}

func TestPrepareReturnsNothingForModeBaseAndForAMacOSOnlyTarget(t *testing.T) {
	goBinary := goBinary(t)
	base, err := Prepare(PrepareRequest{Root: repoRoot(t), Mode: ModeBase, BindTarget: "ios/arm64", GoBinary: goBinary, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if base.Overlay != nil {
		base.Close()
		t.Fatal("mode base prepared an overlay; it must compile the toolchain's runtime")
	}
	if base.Skipped != "" {
		t.Fatalf("mode base was asked for and reported as skipped: %s", base.Skipped)
	}

	macos, err := Prepare(PrepareRequest{Root: repoRoot(t), Mode: ModeBoth, BindTarget: "macos/arm64", GoBinary: goBinary, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if macos.Overlay != nil {
		macos.Close()
		t.Fatal("a macOS-only target prepared an overlay; that slice must keep the official runtime")
	}
	if macos.Skipped == "" {
		t.Fatal("a macOS-only target must say why nothing was prepared, so the log is not silent")
	}
	if !strings.Contains(macos.Skipped, "iOS-family") {
		t.Fatalf("the explanation does not name the reason: %s", macos.Skipped)
	}
}

func TestPrepareRefusesAConflictingGoFlagsOverlay(t *testing.T) {
	goBinary := goBinary(t)
	// The inherited entry is REPLACED rather than appended to: execve keeps both, the go command
	// reads the first, and on a host that exports an empty GOFLAGS an appended one is dead weight.
	// The earlier form of this test appended, so it proved nothing on that host - which is exactly
	// how the duplicate-entry hole was found.
	env := withoutEnv(os.Environ(), "GOFLAGS")
	env = append(env, "GOFLAGS=-overlay=/tmp/somebody-elses.json")
	_, err := Prepare(PrepareRequest{Root: repoRoot(t), Mode: ModeSmall, BindTarget: "ios/arm64", GoBinary: goBinary, Env: env})
	if err == nil {
		t.Fatal("a GOFLAGS that already carries an overlay must be refused: two overlays cannot both be the one that compiled the artifact")
	}
	if !strings.Contains(err.Error(), "GOFLAGS") {
		t.Fatalf("the refusal must name the cause: %v", err)
	}
}

// withoutEnv drops every entry for one name, so a test can set a variable rather than stack a second
// assignment that the child would ignore.
func withoutEnv(env []string, name string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// The shim is the injection point, so its shape is a contract: it must add the overlay to the go
// subcommands that compile and to nothing else, and it must quote the paths it embeds.
func TestShimSourceShape(t *testing.T) {
	dir := t.TempDir()
	binDir, err := WriteGoShim(dir, "/usr/local/go/bin/go", "/tmp/overlay dir/overlay.json", filepath.Join(dir, "injections.log"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(binDir, "go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "#!/bin/sh") {
		t.Fatal("the shim must be a POSIX shell script: gomobile runs it on macOS")
	}
	for _, subcommand := range GoShimSubcommands() {
		if !strings.Contains(text, subcommand) {
			t.Fatalf("the shim does not inject into %s", subcommand)
		}
	}
	for _, subcommand := range []string{"env", "version", "mod"} {
		if strings.Contains(text, "|"+subcommand) || strings.Contains(text, subcommand+"|") {
			t.Fatalf("the shim must not intercept `go %s`: gomobile uses it to discover the environment", subcommand)
		}
	}
	if !strings.Contains(text, `'-overlay=$overlay'`) && !strings.Contains(text, `"-overlay=$overlay"`) {
		if !strings.Contains(text, `-overlay=$overlay`) {
			t.Fatal("the shim does not pass -overlay to the real go")
		}
	}
	// The overlay path contains a space on purpose: the shim must not rely on word splitting.
	if !strings.Contains(text, `'/tmp/overlay dir/overlay.json'`) {
		t.Fatalf("the overlay path is not shell-quoted:\n%s", text)
	}
}
