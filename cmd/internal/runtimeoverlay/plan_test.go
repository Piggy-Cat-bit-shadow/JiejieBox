package runtimeoverlay

import (
	"os"
	"strings"
	"testing"
)

func TestResolveModes(t *testing.T) {
	bindTarget := "ios/arm64,macos/arm64"
	cases := []struct {
		mode    Mode
		enabled bool
		want    Params
	}{
		{
			mode:    ModeBase,
			enabled: false,
			want:    upstreamParams(),
		},
		{
			mode:    ModeSmall,
			enabled: true,
			want:    Params{SpanSetBlockEntries: 32, SpanSetInitSpineCap: 16, StackCacheSize: 16 * 1024, FreeExitedStackBacklog: 8},
		},
		{
			mode:    ModeCGO,
			enabled: true,
			want:    Params{SpanSetBlockEntries: 512, SpanSetInitSpineCap: 256, StackCacheSize: 32 * 1024, DropExtraM: true},
		},
		{
			mode:    ModeBoth,
			enabled: true,
			want:    Params{SpanSetBlockEntries: 32, SpanSetInitSpineCap: 16, StackCacheSize: 16 * 1024, FreeExitedStackBacklog: 8, DropExtraM: true},
		},
	}
	for _, testCase := range cases {
		plan, err := Resolve(testCase.mode, bindTarget)
		if err != nil {
			t.Fatalf("%s: %v", testCase.mode, err)
		}
		if plan.Enabled() != testCase.enabled {
			t.Fatalf("%s: enabled=%v, want %v", testCase.mode, plan.Enabled(), testCase.enabled)
		}
		if plan.Params != testCase.want {
			t.Fatalf("%s: params %+v, want %+v", testCase.mode, plan.Params, testCase.want)
		}
		if plan.Mode != testCase.mode {
			t.Fatalf("%s: mode resolved to %s", testCase.mode, plan.Mode)
		}
	}
}

// A macOS-only framework must keep the toolchain's runtime, and must be honest about it: the
// plan says "base", so nothing is prepared and nothing can leak.
func TestResolveDowngradesATargetWithoutIOSFamilySlices(t *testing.T) {
	for _, bindTarget := range []string{"macos/arm64", "macos/arm64,macos/amd64", ""} {
		plan, err := Resolve(ModeBoth, bindTarget)
		if err != nil {
			t.Fatalf("%q: %v", bindTarget, err)
		}
		if plan.Enabled() || plan.Mode != ModeBase {
			t.Fatalf("%q resolved to %s with %+v; a target with no iOS-family slice must compile the official runtime",
				bindTarget, plan.Mode, plan.Params)
		}
	}
	for _, bindTarget := range []string{"ios", "ios/arm64", "ios,iossimulator,macos", "tvos/arm64", "tvossimulator/arm64"} {
		plan, err := Resolve(ModeSmall, bindTarget)
		if err != nil {
			t.Fatalf("%q: %v", bindTarget, err)
		}
		if !plan.Enabled() {
			t.Fatalf("%q has an iOS-family slice but resolved to mode %s", bindTarget, plan.Mode)
		}
	}
}

func TestParseModeRejectsUnknownModes(t *testing.T) {
	for _, text := range []string{"", "production", "SMALL ", "both,small"} {
		if _, err := ParseMode(text); err == nil {
			if strings.TrimSpace(strings.ToLower(text)) == "small" {
				continue
			}
			t.Fatalf("mode %q was accepted", text)
		}
	}
	if mode, err := ParseMode(" SMALL "); err != nil || mode != ModeSmall {
		t.Fatalf("a padded upper-case mode must parse to %s: %s, %v", ModeSmall, mode, err)
	}
}

// The marker has to name every value the plan sets, or a slice compiled with a different plan
// could satisfy the check by accident.
func TestMarkerNamesEveryPlannedValue(t *testing.T) {
	plan, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	marker := plan.Marker()
	for _, fragment := range []string{
		MarkerPrefix,
		"mode=both",
		"spanSetBlockEntries=32",
		"spanSetInitSpineCap=16",
		"stackCacheSize=16384",
		"freeExitedStack=0",
		"freeStackBacklog=8",
		"dropExtraM=1",
	} {
		if !strings.Contains(marker, fragment) {
			t.Fatalf("marker %q does not name %q", marker, fragment)
		}
	}
	other, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if other.Marker() == marker {
		t.Fatal("two different plans produced the same marker; the verification could not tell them apart")
	}
	if missing := other.Marker(); strings.Contains(missing, "dropExtraM=1") {
		t.Fatalf("mode %s must not claim group B: %s", ModeSmall, missing)
	}
}

func TestSliceIsIOSFamily(t *testing.T) {
	ios := []string{"ios-arm64", "ios-arm64_x86_64-simulator", "tvos-arm64", "tvos-arm64_x86_64-simulator"}
	notIOS := []string{"macos-arm64", "macos-arm64_x86_64", "maccatalyst-arm64", "xros-arm64"}
	for _, slice := range ios {
		if !SliceIsIOSFamily(slice) {
			t.Fatalf("%s must be an iOS-family slice", slice)
		}
	}
	for _, slice := range notIOS {
		if SliceIsIOSFamily(slice) {
			t.Fatalf("%s must NOT be treated as an iOS-family slice: the marker check would fail to notice a leak", slice)
		}
	}
}

func TestHasIOSFamilyPlatform(t *testing.T) {
	for _, target := range []string{"ios", "ios/arm64", "tvos/arm64", "iossimulator/amd64", "ios,iossimulator,tvos,tvossimulator,macos"} {
		if !HasIOSFamilyPlatform(target) {
			t.Fatalf("%q must be recognised as producing an iOS-family slice", target)
		}
	}
	for _, target := range []string{"", "macos/arm64", "android/arm64", "macos"} {
		if HasIOSFamilyPlatform(target) {
			t.Fatalf("%q must not be recognised as producing an iOS-family slice", target)
		}
	}
}

// Group B's assembly half only exists for arm64, so the added file must resolve the flag with a
// goarch term. Without it, an iOS amd64 simulator slice would suppress cgoBindM (a Go-side edit
// that applies to every iOS slice) and never reach the assembly that drops the m - every call
// would leak its m.
func TestDropExtraMIsArm64OnlyUnlessTheProbeOverridesIt(t *testing.T) {
	plan, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if expression := plan.dropExtraMExpression(); !strings.Contains(expression, "goarch.IsArm64") {
		t.Fatalf("group B resolves to %q; it must be arm64-only", expression)
	}
	probe := plan
	probe.forceDropExtraMAllArches = true
	if expression := probe.dropExtraMExpression(); expression != "true" {
		t.Fatalf("the probe override must force the flag: %q", expression)
	}
	small, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if expression := small.dropExtraMExpression(); expression != "false" {
		t.Fatalf("a mode without group B must not reference it: %q", expression)
	}
}

// The product path must never reach the test-only knobs: a probe tag in a shipping build would
// compile the overlay under a constraint nothing selects, which looks exactly like an overlay
// that was never applied.
func TestProductPlansNeverSetTheProbeTag(t *testing.T) {
	for _, mode := range []Mode{ModeBase, ModeSmall, ModeCGO, ModeBoth} {
		for _, target := range []string{"ios/arm64", "ios,macos", "macos/arm64"} {
			plan, err := Resolve(mode, target)
			if err != nil {
				t.Fatalf("%s %s: %v", mode, target, err)
			}
			if plan.probeTag != "" || plan.forceDropExtraMAllArches || len(plan.extraEditTargets) != 0 {
				t.Fatalf("%s %s: a product plan carried a test-only knob", mode, target)
			}
			if plan.onConstraint() != "ios" || plan.offConstraint() != "!ios" {
				t.Fatalf("%s %s: constraints %q/%q, want ios/!ios", mode, target, plan.onConstraint(), plan.offConstraint())
			}
		}
	}
}

func TestCheckGoFlagsHasNoOverlay(t *testing.T) {
	if err := CheckGoFlagsHasNoOverlay([]string{"PATH=/bin", "GOFLAGS=-mod=mod"}); err != nil {
		t.Fatalf("a GOFLAGS without an overlay must pass: %v", err)
	}
	err := CheckGoFlagsHasNoOverlay([]string{"GOFLAGS=-overlay=/tmp/x.json"})
	if err == nil {
		t.Fatal("a second overlay in GOFLAGS would win or lose silently and must be refused")
	}
	if !strings.Contains(err.Error(), "GOFLAGS") {
		t.Fatalf("the failure must name the variable: %v", err)
	}
}

func TestWithShimPathPutsTheShimFirst(t *testing.T) {
	env := []string{"HOME=/root", "PATH=/usr/bin:/bin"}
	out := WithShimPath(env, "/tmp/shim/bin")
	if len(out) != 2 {
		t.Fatalf("the PATH entry must be replaced in place, not appended: %v", out)
	}
	if !strings.HasPrefix(out[1], "PATH=/tmp/shim/bin") {
		t.Fatalf("the shim must lead PATH so it is found before the real toolchain: %v", out[1])
	}
	if EnvValue(out, "PATH") != "/tmp/shim/bin"+string(os.PathListSeparator)+"/usr/bin:/bin" {
		t.Fatalf("PATH became %q", EnvValue(out, "PATH"))
	}
	withoutPath := WithShimPath([]string{"HOME=/root"}, "/tmp/shim/bin")
	if EnvValue(withoutPath, "PATH") != "/tmp/shim/bin" {
		t.Fatalf("a missing PATH must be created: %v", withoutPath)
	}
}

func TestDefaultModeIsOneOfTheReviewedModes(t *testing.T) {
	switch DefaultMode {
	case ModeSmall, ModeBoth:
	default:
		t.Fatalf("DefaultMode is %q; a product build may only default to a mode that ships optimisations or to both", DefaultMode)
	}
	if DefaultMode == ModeBase {
		t.Fatal("unreachable")
	}
}
