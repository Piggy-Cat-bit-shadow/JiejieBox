package runtimeoverlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerdict(t *testing.T) {
	const wanted = "jiejiebox-ios-runtime-overlay/1 mode=small"
	cases := []struct {
		name    string
		check   SliceCheck
		wantErr bool
		naming  string
	}{
		{
			name:  "overlay slice with the wanted marker",
			check: SliceCheck{Slice: "ios-arm64", IOSFamily: true, Wanted: wanted, AnyMarker: true, WantedPresent: true},
		},
		{
			name:    "overlay slice with no marker at all",
			check:   SliceCheck{Slice: "ios-arm64", IOSFamily: true, Wanted: wanted},
			wantErr: true,
			naming:  "built past the runtime overlay",
		},
		{
			name:    "overlay slice with another plan's marker",
			check:   SliceCheck{Slice: "ios-arm64", IOSFamily: true, Wanted: wanted, AnyMarker: true},
			wantErr: true,
			naming:  wanted,
		},
		{
			name:  "macOS slice without a marker",
			check: SliceCheck{Slice: "macos-arm64", Wanted: wanted},
		},
		{
			name:    "macOS slice with a marker",
			check:   SliceCheck{Slice: "macos-arm64", Wanted: wanted, AnyMarker: true, WantedPresent: true},
			wantErr: true,
			naming:  "leaked",
		},
		{
			name:    "an unexpected slice that picked the iOS file up",
			check:   SliceCheck{Slice: "maccatalyst-arm64", Wanted: wanted, AnyMarker: true, WantedPresent: true},
			wantErr: true,
			naming:  "leaked",
		},
		{
			name:  "mode base expects no marker anywhere",
			check: SliceCheck{Slice: "ios-arm64", IOSFamily: true},
		},
		{
			name:    "mode base with a marker present",
			check:   SliceCheck{Slice: "ios-arm64", IOSFamily: true, AnyMarker: true, WantedPresent: true},
			wantErr: true,
			naming:  "mode base",
		},
	}
	for _, testCase := range cases {
		err := testCase.check.Verdict()
		if testCase.wantErr {
			if err == nil {
				t.Fatalf("%s: expected a failure", testCase.name)
			}
			if testCase.naming != "" && !strings.Contains(err.Error(), testCase.naming) {
				t.Fatalf("%s: the failure must contain %q: %v", testCase.name, testCase.naming, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
	}
}

// A record that disagrees with itself must be refused before any artifact is looked at. The
// verifier used to check the artifact against the marker while printing the mode, so `both` beside
// a `small` marker verified the small plan and reported the both plan.
func TestProvenanceRefusesARecordThatDisagreesWithItself(t *testing.T) {
	small, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	both, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	contradictions := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "mode both beside a small marker",
			content: "runtime.overlay=both\nruntime.overlay.marker=" + small.Marker() + "\n",
			want:    "different plan",
		},
		{
			name:    "mode small beside a both marker",
			content: "runtime.overlay=small\nruntime.overlay.marker=" + both.Marker() + "\n",
			want:    "different plan",
		},
		{
			name:    "mode base beside a marker",
			content: "runtime.overlay=base\nruntime.overlay.marker=" + small.Marker() + "\n",
			want:    "mode base",
		},
		{
			name:    "the marker is not a marker",
			content: "runtime.overlay=small\nruntime.overlay.marker=something else entirely\n",
			want:    "does not start with",
		},
		{
			name:    "two modes recorded",
			content: "runtime.overlay=small\nruntime.overlay=both\nruntime.overlay.marker=" + both.Marker() + "\n",
			want:    "records runtime.overlay 2 times",
		},
		{
			name:    "two markers recorded",
			content: "runtime.overlay=small\nruntime.overlay.marker=" + small.Marker() + "\nruntime.overlay.marker=" + both.Marker() + "\n",
			want:    "records runtime.overlay.marker 2 times",
		},
	}
	for _, testCase := range contradictions {
		_, err := ParseProvenanceOverlay(testCase.content)
		if err == nil {
			t.Fatalf("%s: accepted; the artifact would be verified against one plan and reported as another", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s: the refusal does not explain itself (%q): %v", testCase.name, testCase.want, err)
		}
	}
	// And the honest records still parse, in every mode the builder can produce.
	for _, plan := range []Plan{{Mode: ModeBase}, small, both} {
		content := "runtime.overlay=" + string(plan.Mode) + "\n"
		if plan.Enabled() {
			content += "runtime.overlay.marker=" + plan.Marker() + "\n"
		}
		parsed, err := ParseProvenanceOverlay(content)
		if err != nil {
			t.Fatalf("mode %s: %v", plan.Mode, err)
		}
		if parsed.Mode != plan.Mode {
			t.Fatalf("mode %s parsed as %s", plan.Mode, parsed.Mode)
		}
	}
}

func TestParseProvenanceOverlay(t *testing.T) {
	provenance := strings.Join([]string{
		"commit=bc943086c440dbfadc538ad5c3846923d0850395",
		"version=0.1.55",
		"runtime.overlay=both",
		"runtime.overlay.go=go1.27.4",
		"runtime.overlay.marker=jiejiebox-ios-runtime-overlay/1 mode=both",
		"tags.apple=with_quic,with_low_memory",
	}, "\r\n")
	parsed, err := ParseProvenanceOverlay(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Mode != ModeBoth {
		t.Fatalf("mode %s", parsed.Mode)
	}
	if parsed.ExpectedMarker() != "jiejiebox-ios-runtime-overlay/1 mode=both" {
		t.Fatalf("marker %q", parsed.ExpectedMarker())
	}
	if parsed.GoVersion != "go1.27.4" {
		t.Fatalf("go version %q", parsed.GoVersion)
	}

	base, err := ParseProvenanceOverlay("runtime.overlay=base\n")
	if err != nil {
		t.Fatal(err)
	}
	if base.ExpectedMarker() != "" {
		t.Fatal("mode base must expect no marker")
	}

	if _, err := ParseProvenanceOverlay("commit=abc\nversion=1\n"); err == nil {
		t.Fatal("a provenance file with no runtime.overlay key must be refused: there would be nothing to verify against")
	}
	if _, err := ParseProvenanceOverlay("runtime.overlay=small\n"); err == nil {
		t.Fatal("a non-base mode without a marker must be refused: the expected marker would be empty and the check vacuous")
	}
	if _, err := ParseProvenanceOverlay("runtime.overlay=nonsense\n"); err == nil {
		t.Fatal("an unknown mode must be refused")
	}
}

// writeFakeFramework builds the smallest directory tree SliceBinary and VerifyXCframework
// accept, so the verifier can be driven without an Xcode build.
func writeFakeFramework(t *testing.T, root string, slices map[string]string) {
	t.Helper()
	for slice, payload := range slices {
		dir := filepath.Join(root, slice)
		framework := filepath.Join(dir, "Libbox.framework")
		if err := os.MkdirAll(framework, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(framework, "Libbox"), []byte(payload), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerifyXCframeworkFindsAndAttributesMarkers(t *testing.T) {
	root := t.TempDir()
	plan, err := Resolve(ModeSmall, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	writeFakeFramework(t, root, map[string]string{
		"ios-arm64":   "\x00\x01binary\x00" + plan.Marker() + "\x00tail",
		"macos-arm64": "\x00\x01binary\x00libbox",
	})
	checks, err := RequireVerifiedXCframework(root, plan.Marker())
	if err != nil {
		t.Fatalf("a correctly built framework must verify: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("%d slices checked", len(checks))
	}
}

func TestRequireVerifiedXCframeworkRejectsASliceBuiltPastTheOverlay(t *testing.T) {
	root := t.TempDir()
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	writeFakeFramework(t, root, map[string]string{
		"ios-arm64":   "binary with no overlay",
		"macos-arm64": "binary with no overlay",
	})
	if _, err := RequireVerifiedXCframework(root, plan.Marker()); err == nil {
		t.Fatal("an iOS slice with no marker must fail the build")
	}
}

func TestRequireVerifiedXCframeworkRejectsAMacOSSliceThatPickedUpTheMarker(t *testing.T) {
	root := t.TempDir()
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	writeFakeFramework(t, root, map[string]string{
		"ios-arm64":   plan.Marker(),
		"macos-arm64": "x" + plan.Marker() + "y",
	})
	if _, err := RequireVerifiedXCframework(root, plan.Marker()); err == nil {
		t.Fatal("a macOS slice carrying the marker means the build constraint leaked and must fail")
	} else if !strings.Contains(err.Error(), "macos-arm64") {
		t.Fatalf("the failure must name the slice: %v", err)
	}
}

func TestRequireVerifiedXCframeworkRejectsAnArtifactWithNoIOSSlice(t *testing.T) {
	root := t.TempDir()
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	writeFakeFramework(t, root, map[string]string{"macos-arm64": "no marker"})
	if _, err := RequireVerifiedXCframework(root, plan.Marker()); err == nil {
		t.Fatal("an overlay mode with no iOS-family slice had nothing to compile into and must fail")
	}
}

// The scan keeps a tail across chunk boundaries; a marker that straddles one must still be
// found whole, which is the difference between a verifier and a verifier that works on small
// test files only.
func TestScanFindsAMarkerAcrossAChunkBoundary(t *testing.T) {
	plan, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	marker := plan.Marker()
	root := t.TempDir()
	payload := make([]byte, 1<<20+4096)
	for index := range payload {
		payload[index] = 'x'
	}
	copy(payload[1<<20-len(marker)/2:], marker)
	writeFakeFramework(t, root, map[string]string{"ios-arm64": string(payload)})
	checks, err := VerifyXCframework(root, marker)
	if err != nil {
		t.Fatal(err)
	}
	if !checks[0].AnyMarker || !checks[0].WantedPresent {
		t.Fatalf("the marker straddling a chunk boundary was not found whole: %+v", checks[0])
	}
}

// A binary that contains a DIFFERENT plan's marker must not satisfy the check, and must be
// distinguishable from a binary with no marker at all.
func TestScanDistinguishesAnotherPlanFromNoOverlay(t *testing.T) {
	both, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	small, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFakeFramework(t, root, map[string]string{
		"ios-arm64":   "prefix" + both.Marker() + "suffix",
		"macos-arm64": "no overlay here",
	})
	checks, err := VerifyXCframework(root, small.Marker())
	if err != nil {
		t.Fatal(err)
	}
	if !checks[0].AnyMarker || checks[0].WantedPresent {
		t.Fatalf("a slice carrying mode both must report any-marker and not wanted: %+v", checks[0])
	}
	if err := checks[0].Verdict(); err == nil {
		t.Fatal("a slice carrying another plan's marker must fail")
	}
	if checks[1].AnyMarker {
		t.Fatalf("the macOS slice must report no marker: %+v", checks[1])
	}
}

// A directory whose name does not look like a slice is still inspected. The earlier form filtered
// by name shape, so an unexpected directory - a new platform, a renamed slice - was skipped
// silently, and a slice nobody inspects is a slice whose leaked marker cannot fail the build.
func TestAnUnexpectedSliceDirectoryCannotEscapeTheCheck(t *testing.T) {
	root := t.TempDir()
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	writeFakeFramework(t, root, map[string]string{
		"ios-arm64": plan.Marker(),
		"weirdname": "x" + plan.Marker() + "y",
	})
	if _, err := RequireVerifiedXCframework(root, plan.Marker()); err == nil {
		t.Fatal("a directory that is not a slice carried the marker and was not inspected")
	} else if !strings.Contains(err.Error(), "weirdname") {
		t.Fatalf("the failure must name the unexpected directory: %v", err)
	}
	// And a directory that is not a framework at all fails loudly rather than being skipped.
	if _, err := VerifyXCframework(root, plan.Marker()); err != nil {
		t.Fatalf("both directories are frameworks here: %v", err)
	}
	notAFramework := t.TempDir()
	if err := os.MkdirAll(filepath.Join(notAFramework, "ios-arm64"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyXCframework(notAFramework, plan.Marker()); err == nil {
		t.Fatal("a slice directory with no framework inside must fail, not be skipped")
	}
}

func TestSliceBinaryUnderstandsBothFrameworkLayouts(t *testing.T) {
	root := t.TempDir()
	flat := filepath.Join(root, "ios-arm64", "Libbox.framework", "Libbox")
	if err := os.MkdirAll(filepath.Dir(flat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flat, []byte("flat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if binary, err := SliceBinary(filepath.Join(root, "ios-arm64")); err != nil || binary != flat {
		t.Fatalf("flat layout: %q, %v", binary, err)
	}
	versioned := filepath.Join(root, "macos-arm64", "Libbox.framework", "Versions", "A", "Libbox")
	if err := os.MkdirAll(filepath.Dir(versioned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("versioned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if binary, err := SliceBinary(filepath.Join(root, "macos-arm64")); err != nil || binary != versioned {
		t.Fatalf("versioned layout: %q, %v", binary, err)
	}
	if _, err := SliceBinary(filepath.Join(root, "ios-arm64", "Libbox.framework")); err == nil {
		t.Fatal("a directory that is not a slice must be refused")
	}
}
