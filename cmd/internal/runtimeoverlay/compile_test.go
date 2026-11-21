package runtimeoverlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildOverlayForTest prepares an overlay whose added files are retagged so a HOST binary can
// select them, and whose assembly half is mirrored onto the host architecture.
//
// This is the only way group B's semantics can be exercised on a machine that is not an Apple
// device: the product overlay is arm64-only by design, so a test that insisted on the product
// tags could only ever compile-check it. Everything it turns on is confined to the returned
// plan, and TestProductPlansNeverSetTheProbeTag asserts the product path cannot reach any of it.
func buildOverlayForTest(t *testing.T, mode Mode) (Plan, *Overlay, Toolchain) {
	t.Helper()
	toolchain := hostToolchain(t)
	plan, err := Resolve(mode, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	plan.probeTag = "jj_runtime_overlay_probe"
	plan.forceDropExtraMAllArches = true
	if plan.groupB() {
		hostASM := "src/runtime/asm_" + runtime.GOARCH + ".s"
		if hostASM != sourceASMArm64 {
			plan.extraEditTargets = []string{hostASM}
		}
	}
	overlay, err := Generate(GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan, Toolchain: toolchain})
	if err != nil {
		t.Fatalf("%s: %v", mode, err)
	}
	return plan, overlay, toolchain
}

// The overlay must compile for the slices this repository ships and for the ones its source
// default would produce. A compile failure here is the cheap version of a failed release: the
// assembly half of group B is the part that cannot be checked by reading it.
func TestOverlayCompilesForEveryAppleSlice(t *testing.T) {
	goBinary := goBinary(t)
	type target struct{ goos, goarch, tags string }
	targets := []target{
		{"ios", "arm64", ""},
		{"darwin", "arm64", ""},
		{"darwin", "arm64", "ios,tvos"}, // tvOS: GOOS=darwin with the ios tag
		{runtime.GOOS, runtime.GOARCH, ""},
	}
	if os.Getenv("RTOVL_FULL_SLICE_MATRIX") != "" {
		targets = append(targets,
			target{"ios", "amd64", ""},
			target{"darwin", "amd64", ""},
		)
	}
	for _, mode := range []Mode{ModeSmall, ModeBoth} {
		plan, overlay, _ := buildOverlayForTest(t, mode)
		for _, slice := range targets {
			args := []string{"build", "-overlay=" + overlay.JSONPath}
			if plan.offConstraint() != "!ios" || slice.goos == "ios" {
				// The probe retags the added files, so the tag must be selected explicitly for
				// every target that should compile the overlay's values.
				args = append(args, "-tags", plan.onConstraint())
			}
			args = append(args, "runtime")
			command := exec.Command(goBinary, args...)
			command.Dir = repoRoot(t)
			command.Env = append(os.Environ(),
				"GOOS="+slice.goos, "GOARCH="+slice.goarch, "CGO_ENABLED=0", "GOFLAGS=")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("mode %s, %s/%s tags=%q with the overlay: %v\n%s",
					mode, slice.goos, slice.goarch, slice.tags, err, output)
			}
		}
	}
}

// The same matrix, with the overlay's own `ios` constraint instead of the probe tag: this is
// what the product build compiles, so a mistake that only shows up under the real constraint
// (an identifier that does not exist, an import the off file needs) is caught here.
func TestProductOverlayCompilesForTheShippedSlices(t *testing.T) {
	goBinary := goBinary(t)
	toolchain := hostToolchain(t)
	plan, err := Resolve(ModeBoth, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := Generate(GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan, Toolchain: toolchain})
	if err != nil {
		t.Fatal(err)
	}
	for _, slice := range []struct{ goos, goarch, tags string }{
		{"ios", "arm64", ""},
		{"darwin", "arm64", ""},
	} {
		args := []string{"build", "-overlay=" + overlay.JSONPath}
		if slice.tags != "" {
			args = append(args, "-tags", slice.tags)
		}
		args = append(args, "runtime")
		command := exec.Command(goBinary, args...)
		command.Dir = repoRoot(t)
		command.Env = append(os.Environ(),
			"GOOS="+slice.goos, "GOARCH="+slice.goarch, "CGO_ENABLED=0", "GOFLAGS=")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s/%s tags=%q with the product overlay: %v\n%s", slice.goos, slice.goarch, slice.tags, err, output)
		}
	}
}

// The bug this catches: an edit that produces a file the compiler accepts but whose identifier
// is only referenced from the other file. `build runtime` for a non-iOS target compiles the
// `!ios` file, so an `ios`-only typo would pass every check above except this one.
//
// The marker trio is deliberately in the ios-only list: the `!ios` file carries no marker string
// at all, which is what makes "a macOS slice must not contain it" a checkable asymmetry.
func TestOnAndOffFilesAreSymmetrical(t *testing.T) {
	plan, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	on := plan.onFileSource()
	off := plan.offFileSource()
	shared := []string{
		"jjOverlaySpanSetBlockEntries",
		"jjOverlaySpanSetInitSpineCap",
		"jjOverlayStackCacheSize",
		"jjOverlayFreeExitedStack",
		"jjOverlayFreeExitedStackBacklog",
		"jjOverlayDropExtraM",
		"jjOverlayMarker",
	}
	for _, identifier := range shared {
		if !strings.Contains(on, identifier) {
			t.Fatalf("the ios file does not define %s", identifier)
		}
		if !strings.Contains(off, identifier) {
			t.Fatalf("the !ios file does not define %s, so a shared edit would not compile for macOS", identifier)
		}
	}
	for _, identifier := range []string{"jjOverlayMarkerValue", "jjOverlayMarkerText"} {
		if !strings.Contains(on, identifier) {
			t.Fatalf("the ios file does not define %s", identifier)
		}
		if strings.Contains(off, identifier) {
			t.Fatalf("the !ios file defines %s; the marker must exist only where the overlay applies", identifier)
		}
	}
	if strings.Contains(off, "import") {
		t.Fatal("the !ios file must not import anything: it restates constants and defines a no-op marker")
	}
}

// A generated file must satisfy the same gofmt rules the rest of the tree does, so a diff of a
// patched GOROOT is readable and a future `gofmt -w` distinction is not the first sign that the
// generator drifted. The files are read back from a real Generate, so this checks what is
// written rather than what the composer intended to write.
func TestGeneratedSourcesAreGofmtClean(t *testing.T) {
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skip("gofmt is not on PATH")
	}
	toolchain := hostToolchain(t)
	for _, mode := range []Mode{ModeSmall, ModeBoth} {
		plan, err := Resolve(mode, "ios/arm64")
		if err != nil {
			t.Fatal(err)
		}
		overlay, err := Generate(GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan, Toolchain: toolchain})
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range overlay.Files {
			if file.Role != "add" {
				continue
			}
			output, err := exec.Command(gofmt, "-l", file.Path).CombinedOutput()
			if err != nil {
				t.Fatalf("gofmt %s: %v\n%s", file.Path, err, output)
			}
			if strings.TrimSpace(string(output)) != "" {
				data, readErr := os.ReadFile(file.Path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				formatted, formatErr := exec.Command(gofmt, file.Path).Output()
				if formatErr != nil {
					t.Fatal(formatErr)
				}
				t.Fatalf("%s (%s) is not gofmt-clean:\nwritten:\n%s\nwant:\n%s", filepath.Base(file.Path), mode, data, formatted)
			}
		}
	}
}
