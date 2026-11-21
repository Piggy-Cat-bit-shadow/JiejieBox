package runtimeoverlay

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Toolchain is the Go installation the overlay will be compiled with, as the INNER go process
// sees it.
//
// It is probed rather than assumed. `which go` on the outer shell answers a different question:
// the toolchain a build actually runs is whatever GOTOOLCHAIN resolves to for this module, and
// a downloaded toolchain lives under GOMODCACHE, where the go command refuses to overlay
// anything at all. Probing the same environment gomobile will inherit is what makes this a
// fact rather than a guess.
type Toolchain struct {
	GOROOT     string
	GOMODCACHE string
	GOVersion  string
}

// Probe reads the toolchain facts from the go binary that will run the build.
func Probe(goBinary, dir string, env []string) (Toolchain, error) {
	command := exec.Command(goBinary, "env", "-json", "GOROOT", "GOMODCACHE", "GOVERSION")
	command.Dir = dir
	command.Env = env
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			return Toolchain{}, fmt.Errorf("runtime overlay: go env failed: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return Toolchain{}, fmt.Errorf("runtime overlay: go env failed: %w", err)
	}
	var values struct {
		GOROOT     string
		GOMODCACHE string
		GOVERSION  string
	}
	if err := json.Unmarshal(output, &values); err != nil {
		return Toolchain{}, fmt.Errorf("runtime overlay: parse go env: %w", err)
	}
	toolchain := Toolchain{
		GOROOT:     strings.TrimSpace(values.GOROOT),
		GOMODCACHE: strings.TrimSpace(values.GOMODCACHE),
		GOVersion:  strings.TrimSpace(values.GOVERSION),
	}
	for name, value := range map[string]string{
		"GOROOT":     toolchain.GOROOT,
		"GOMODCACHE": toolchain.GOMODCACHE,
		"GOVERSION":  toolchain.GOVersion,
	} {
		if value == "" {
			return Toolchain{}, fmt.Errorf("runtime overlay: go env gave no %s, so the overlay cannot be aimed at a runtime", name)
		}
	}
	return toolchain, nil
}

// reviewedToolchain pins the exact runtime sources this overlay was read against.
//
// # Why a digest and not a version check alone
//
// The edits are literal text replacements in files the Go project owns. A patch release can
// move one line of `proc.go` without changing anything this repository controls, and the
// failure mode of that is not a compile error - it is an edit that no longer replaces anything
// (caught by the anchor count) or, worse, an edit whose anchor still matches while the code
// around it means something else. `go1.27.4` and `go1.27.5` are the same version string to a
// `>=` comparison and different runtimes to this package.
//
// So the pin is the digest of the four (and, with group B, five) files, the version string is
// the index into them, and an un-reviewed toolchain fails the build with instructions instead
// of compiling something nobody read.
type reviewedToolchain struct {
	// files maps the GOROOT-relative path to the SHA256 of the reviewed bytes.
	files map[string]string
}

// reviewedToolchains is the complete set of toolchains this overlay has been read against.
//
// Adding an entry is a deliberate act: read the new toolchain's version of every file, confirm
// each anchor still means what its edit assumes, then record the new digests. Anything not in
// this map is refused, including a patch bump of the same minor.
var reviewedToolchains = map[string]reviewedToolchain{
	"go1.27.4": {files: map[string]string{
		sourceMSpanSet:            "85faad45efe16dc88c72253e6279f58e2cb161d948534dda5c47672243ad367a",
		sourceMalloc:              "69e2dc64c34699e7fb8dcaae3eafd396834f42755ef3ad0b1e287ae43198bcea",
		sourceProc:                "40d15ce858cf058cff92606001a29ee1689db88c121a2f0c283163a0d64a7375",
		sourceASMArm64:            "47f9313d4f956b4159dcf8b292bf340a12adb6b58ee9a94360fe948045e2c5db",
		"src/runtime/asm_amd64.s": "71da1bf81ac9a5972ab4ddaccb0f2bab4c9272a888991a34721e520513f458bd",
	}},
}

// ReviewedVersions lists the toolchain versions the overlay supports, sorted.
func ReviewedVersions() []string {
	versions := make([]string, 0, len(reviewedToolchains))
	for version := range reviewedToolchains {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}

// Check proves the toolchain is one the overlay may be applied to, and that its runtime source
// is byte-for-byte the reviewed one.
func (t Toolchain) Check() error {
	pin, reviewed := reviewedToolchains[t.GOVersion]
	if !reviewed {
		return fmt.Errorf(
			"runtime overlay: this build would compile Go %s, and the overlay has only been read against %s. "+
				"The runtime source is what the overlay replaces, so a version it has not been read against cannot be compiled with it: "+
				"read %s/src/runtime/{mspanset.go,malloc.go,proc.go,asm_arm64.s} in the new toolchain, confirm each anchor still means the same thing, "+
				"then add the version and its file digests to reviewedToolchains in cmd/internal/runtimeoverlay/toolchain.go",
			t.GOVersion, strings.Join(ReviewedVersions(), ", "), t.GOROOT)
	}
	if t.GOMODCACHE != "" {
		modCache := filepath.Clean(t.GOMODCACHE)
		if t.GOROOT == modCache || strings.HasPrefix(filepath.Clean(t.GOROOT), modCache+string(os.PathSeparator)) {
			return fmt.Errorf(
				"runtime overlay: GOROOT %s is inside GOMODCACHE %s, which is how a toolchain downloaded by GOTOOLCHAIN=auto looks. "+
					"go build refuses to overlay files beneath the module cache, so the iOS slices could not be built at all. "+
					"Install %s as a real toolchain (go install golang.org/dl/%s@latest && %s download) instead of letting this build download it",
				t.GOROOT, t.GOMODCACHE, t.GOVersion, t.GOVersion, t.GOVersion)
		}
	}
	for _, source := range sortedKeys(pin.files) {
		expected := pin.files[source]
		path := filepath.Join(t.GOROOT, filepath.FromSlash(source))
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("runtime overlay: read the toolchain's %s: %w", source, err)
		}
		actual, err := digestOfBytes(data)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf(
				"runtime overlay: %s in the toolchain at %s hashes to %s, not the reviewed %s for %s. "+
					"Either this GOROOT has been patched, or the release is not the one this overlay was read against; "+
					"neither may be compiled silently, so the build stops here",
				source, t.GOROOT, actual, expected, t.GOVersion)
		}
	}
	return nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
