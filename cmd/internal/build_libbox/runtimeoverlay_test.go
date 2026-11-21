package main

import (
	"os"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/cmd/internal/runtimeoverlay"
)

// The Apple artifacts must never be compiled with the overlay switched off by accident, and the
// default is where that would happen. This asserts the property rather than the literal so
// changing the default to a different optimised mode is still fine.
func TestTheAppleBuildDoesNotDefaultToTheToolchainRuntime(t *testing.T) {
	if runtimeoverlay.DefaultMode == runtimeoverlay.ModeBase {
		t.Fatal("the Apple Libbox default mode is base, so no small-process optimisation would ship")
	}
	plan, err := runtimeoverlay.Resolve(runtimeoverlay.DefaultMode, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Enabled() {
		t.Fatalf("the default mode %q resolves to nothing for the shipped target", runtimeoverlay.DefaultMode)
	}
}

// The overlay mode is a builder-private flag with an environment override. This pins the
// precedence, because the alternative - a flag that silently wins over the environment or an empty
// value that means "off" - is how a local A/B measurement ends up recording a mode it did not
// compile.
func TestRuntimeOverlayModeFromEnvironment(t *testing.T) {
	savedFlag := runtimeOverlayMode
	defer func() { runtimeOverlayMode = savedFlag }()

	t.Setenv("JJ_RUNTIME_OVERLAY", "")
	runtimeOverlayMode = ""
	if mode, err := runtimeOverlayModeFromEnvironment(); err != nil || mode != runtimeoverlay.DefaultMode {
		t.Fatalf("no flag and no environment must resolve to the default: %s, %v", mode, err)
	}

	t.Setenv("JJ_RUNTIME_OVERLAY", "both")
	if mode, err := runtimeOverlayModeFromEnvironment(); err != nil || mode != runtimeoverlay.ModeBoth {
		t.Fatalf("the environment must select a mode: %s, %v", mode, err)
	}

	runtimeOverlayMode = "base"
	if mode, err := runtimeOverlayModeFromEnvironment(); err != nil || mode != runtimeoverlay.ModeBase {
		t.Fatalf("the flag must beat the environment: %s, %v", mode, err)
	}

	runtimeOverlayMode = "experimental"
	if _, err := runtimeOverlayModeFromEnvironment(); err == nil {
		t.Fatal("an unknown mode must fail the build rather than fall back to the default")
	}
}

// An Apple artifact always records the runtime overlay mode, including when it is base: "no
// overlay" is a fact about those bytes, and a missing key would be indistinguishable from an
// artifact built before this existed. A non-Apple build records nothing, because the overlay is
// an Apple concern and a line about it in the Android provenance would be a lie.
func TestProvenanceRecordsTheOverlayOnlyForApple(t *testing.T) {
	savedTarget := target
	savedOverlay := appleRuntimeOverlay
	savedMode := resolvedRuntimeOverlayMode
	defer func() {
		target = savedTarget
		appleRuntimeOverlay = savedOverlay
		resolvedRuntimeOverlayMode = savedMode
	}()

	appleRuntimeOverlay = nil
	resolvedRuntimeOverlayMode = runtimeoverlay.ModeBase
	target = "apple"
	lines := runtimeOverlayProvenanceLines()
	if len(lines) != 1 || lines[0] != "runtime.overlay=base" {
		t.Fatalf("an Apple build with no overlay must record mode base: %v", lines)
	}

	target = "android"
	if lines := runtimeOverlayProvenanceLines(); lines != nil {
		t.Fatalf("a non-Apple build must record nothing about the Apple overlay: %v", lines)
	}
}

// The verify step in .github/actions/apple-libbox-build reads the expected plan out of this file,
// so the keys it writes are a contract between two programs.
func TestOverlayProvenanceIsParseableByTheVerifier(t *testing.T) {
	plan, err := runtimeoverlay.Resolve(runtimeoverlay.ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		"commit=bc943086c440dbfadc538ad5c3846923d0850395",
		"version=0.1.55",
		"tags.apple=with_quic",
		"runtime.overlay=" + string(plan.Mode),
		"runtime.overlay.go=go1.27.4",
		"runtime.overlay.marker=" + plan.Marker(),
		"runtime.overlay.sha256.src_runtime_proc.go=deadbeef",
	}, "\n")
	recorded, err := runtimeoverlay.ParseProvenanceOverlay(content)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Mode != plan.Mode || recorded.ExpectedMarker() != plan.Marker() {
		t.Fatalf("round trip lost the plan: %+v", recorded)
	}
	if _, err := os.Stat("../../runtimeoverlay/plan.go"); err != nil {
		t.Logf("packages are not siblings on this checkout: %v", err)
	}
}
