package runtimeoverlay

import (
	"strings"
	"testing"
)

// The product decision this pins: the default enables B for the iOS family and leaves macOS on the
// toolchain's runtime. It is one decision, so it is one test - a change to any of the three parts
// (the default mode, which constraint selects the values, or the arch gate) breaks it.
func TestTheProductDefaultEnablesGroupBForIOSOnly(t *testing.T) {
	plan, err := Resolve(DefaultMode, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Enabled() {
		t.Fatalf("the default mode %q prepares nothing for the shipped target", DefaultMode)
	}
	if !plan.groupB() {
		t.Fatalf("the default mode %q no longer enables group B for iOS", DefaultMode)
	}
	marker := plan.Marker()
	if !strings.Contains(marker, "dropExtraM=1") {
		t.Fatalf("the default plan's marker does not claim group B: %s", marker)
	}
	if expression := plan.dropExtraMExpression(); !strings.Contains(expression, "goarch.IsArm64") {
		t.Fatalf("group B must stay arm64-only, resolved to %q", expression)
	}

	// The iOS slice compiles this, and only this, file.
	onFile := plan.onFileSource()
	if !strings.Contains(onFile, "//go:build ios") {
		t.Fatal("the values file must still be selected by the ios build constraint")
	}
	if !strings.Contains(onFile, quote(marker)) {
		t.Fatal("the ios values file does not carry the default plan's marker")
	}
	// The macOS slice compiles the other one: upstream's constants, no drop, and no marker for the
	// leak check to find. That is what makes "iOS only" true of a framework that carries both
	// slices, and it is the half a reviewer should be able to check without a device.
	offFile := plan.offFileSource()
	if !strings.Contains(offFile, "//go:build !ios") {
		t.Fatal("the upstream file must still be selected by the negation of that constraint")
	}
	if !strings.Contains(offFile, "var jjOverlayDropExtraM = false") {
		t.Fatal("the macOS slice would drop the extra m; group B is supposed to be iOS-only")
	}
	if strings.Contains(offFile, MarkerPrefix) {
		t.Fatal("the macOS slice carries a marker, so the leak check would fail on a correct build")
	}

	// And a macOS-only build prepares nothing at all, so its framework keeps the official runtime
	// rather than an inert overlay.
	macOSOnly, err := Resolve(DefaultMode, "macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if macOSOnly.Enabled() {
		t.Fatalf("a macOS-only build resolved to %s; it must keep the toolchain's runtime", macOSOnly.Mode)
	}
	if macOSOnly.Params.DropExtraM {
		t.Fatal("a macOS-only build acquired group B")
	}
}

// The builder's own resolution has to agree with the plan, because that is what the Apple pipeline
// gate and the provenance record read.
func TestTheBuilderPrintsTheSameDefaultForTheProductTarget(t *testing.T) {
	plan, err := Resolve(DefaultMode, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if string(plan.Mode) != string(DefaultMode) {
		t.Fatalf("resolved %s for the shipped target but the default is %s", plan.Mode, DefaultMode)
	}
	// The default is one of the modes the flag accepts, which is what -print-runtime-overlay echoes.
	if _, err := ParseMode(string(DefaultMode)); err != nil {
		t.Fatal(err)
	}
}
