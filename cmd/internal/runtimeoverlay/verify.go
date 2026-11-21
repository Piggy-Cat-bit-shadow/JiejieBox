package runtimeoverlay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SliceCheck is what one xcframework slice was found to contain.
type SliceCheck struct {
	// Slice is the slice directory name, e.g. "ios-arm64" or "macos-arm64".
	Slice string
	// IOSFamily reports whether the overlay is expected in this slice.
	IOSFamily bool
	// Binary is the framework binary that was scanned.
	Binary string
	// Wanted is the exact marker this build should carry, empty for ModeBase.
	Wanted string
	// WantedPresent reports whether the exact wanted marker occurs in the binary.
	WantedPresent bool
	// AnyMarker reports whether ANY overlay marker occurs, wanted or not. It is what separates
	// "the overlay never reached this slice" from "a different plan reached it".
	AnyMarker bool
}

// Verdict is the per-slice pass/fail with a message a build log can print.
func (c SliceCheck) Verdict() error {
	switch {
	case c.Wanted == "":
		if c.AnyMarker {
			return fmt.Errorf(
				"slice %s carries a runtime overlay marker although this build compiled mode base: "+
					"the overlay's ios build constraint leaked into a slice that must keep the official runtime",
				c.Slice)
		}
		return nil
	case !c.IOSFamily:
		if c.AnyMarker {
			return fmt.Errorf(
				"slice %s carries a runtime overlay marker although it is not an iOS-family slice: "+
					"the overlay's ios build constraint leaked into it, and that slice must keep the official runtime",
				c.Slice)
		}
		return nil
	case !c.AnyMarker:
		return fmt.Errorf(
			"slice %s was built past the runtime overlay: it carries no %s marker, so its Go runtime is the unmodified one. "+
				"The go shim was not on PATH for this target, or the overlay was dropped between prepare and build",
			c.Slice, MarkerPrefix)
	case !c.WantedPresent:
		return fmt.Errorf(
			"slice %s carries an overlay marker that is not this build's plan: expected %s",
			c.Slice, c.Wanted)
	default:
		return nil
	}
}

// SliceBinary locates the framework binary inside one xcframework slice directory.
//
// The layout is the toolchain's choice, not this repository's: iOS slices are flat
// (Libbox.framework/Libbox) and macOS slices are versioned
// (Libbox.framework/Versions/A/Libbox). Both are looked for by name rather than guessed.
func SliceBinary(sliceDir string) (string, error) {
	frameworks, err := filepath.Glob(filepath.Join(sliceDir, "*.framework"))
	if err != nil {
		return "", err
	}
	if len(frameworks) != 1 {
		return "", fmt.Errorf("runtime overlay: %s has %d *.framework directories, want exactly one", sliceDir, len(frameworks))
	}
	framework := frameworks[0]
	name := strings.TrimSuffix(filepath.Base(framework), ".framework")
	candidates := []string{
		filepath.Join(framework, name),
		filepath.Join(framework, "Versions", "A", name),
	}
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate)
		if statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("runtime overlay: no framework binary named %s under %s (looked at %s)",
		name, framework, strings.Join(candidates, ", "))
}

// VerifyXCframework checks every slice of one xcframework against the plan's marker.
//
// The expected set is derived from the slice directories that are actually there, not from the
// gomobile target that asked for them: a slice this repository did not intend to build must
// fail the check rather than escape it, which is what an unexpected name (maccatalyst, a new
// simulator variant) would otherwise do.
func VerifyXCframework(xcframeworkPath, wanted string) ([]SliceCheck, error) {
	entries, err := os.ReadDir(xcframeworkPath)
	if err != nil {
		return nil, fmt.Errorf("runtime overlay: read %s: %w", xcframeworkPath, err)
	}
	sliceNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// EVERY directory is a slice, including one whose name does not look like one. Filtering
		// by name shape was the earlier form and it had a hole: an unexpected directory (a new
		// platform, a renamed slice, a stray build product) was silently skipped, and a slice that
		// is never inspected is a slice whose leaked marker cannot fail the build. Every directory
		// is therefore inspected, and one that is not a framework fails loudly in SliceBinary.
		sliceNames = append(sliceNames, entry.Name())
	}
	sort.Strings(sliceNames)
	if len(sliceNames) == 0 {
		return nil, fmt.Errorf("runtime overlay: %s has no slice directories", xcframeworkPath)
	}
	checks := make([]SliceCheck, 0, len(sliceNames))
	for _, slice := range sliceNames {
		binary, err := SliceBinary(filepath.Join(xcframeworkPath, slice))
		if err != nil {
			return nil, err
		}
		anyMarker, wantedPresent, err := scanBinaryMarkers(binary, wanted)
		if err != nil {
			return nil, err
		}
		checks = append(checks, SliceCheck{
			Slice:         slice,
			IOSFamily:     SliceIsIOSFamily(slice),
			Binary:        binary,
			Wanted:        wanted,
			WantedPresent: wantedPresent,
			AnyMarker:     anyMarker,
		})
	}
	return checks, nil
}

// RequireVerifiedXCframework runs VerifyXCframework and fails when any slice disagrees, or when
// no slice would have been expected to carry the overlay.
func RequireVerifiedXCframework(xcframeworkPath, wanted string) ([]SliceCheck, error) {
	checks, err := VerifyXCframework(xcframeworkPath, wanted)
	if err != nil {
		return nil, err
	}
	var failures []error
	expected := 0
	for _, check := range checks {
		if check.IOSFamily && check.Wanted != "" {
			expected++
		}
		if verdictErr := check.Verdict(); verdictErr != nil {
			failures = append(failures, verdictErr)
		}
	}
	if wanted != "" && expected == 0 {
		failures = append(failures, fmt.Errorf(
			"runtime overlay: %s has no iOS-family slice, so the overlay had nothing to compile into while mode was requested. "+
				"The build target and the artifact disagree", xcframeworkPath))
	}
	if len(failures) > 0 {
		return checks, errors.Join(failures...)
	}
	return checks, nil
}

// scanBinaryMarkers reads the binary once and answers two questions: does the exact wanted
// marker occur, and does any overlay marker occur at all.
//
// A byte search is used rather than `nm` because release libbox links with `-s -w`: the symbol
// table is exactly what a shipping build drops, and a marker only a symbol table could show
// would be unverifiable in the artifact that matters. The string lives in __TEXT/__rodata,
// which survives stripping and does not depend on LTO keeping a particular function.
//
// The match is exact on purpose. Everything between two markers in rodata is arbitrary bytes of
// neighbouring strings, so an extraction that ran to the next delimiter would report a marker
// plus whatever followed it, and a comparison on that would be either wrong or vacuous.
func scanBinaryMarkers(path, wanted string) (anyMarker, wantedPresent bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return false, false, fmt.Errorf("runtime overlay: open %s: %w", path, err)
	}
	defer file.Close()
	const (
		chunkSize = 1 << 20
		// overlap is the tail kept from one chunk and re-read at the head of the next, so a
		// marker split across a chunk boundary is still seen whole. It is longer than any
		// marker this package can generate by a wide margin.
		overlap = 1024
	)
	prefix := []byte(MarkerPrefix)
	wantedBytes := []byte(wanted)
	chunk := make([]byte, chunkSize)
	window := make([]byte, 0, chunkSize+overlap)
	for {
		read, readErr := file.Read(chunk)
		if read > 0 {
			window = append(window, chunk[:read]...)
			if !anyMarker && bytes.Contains(window, prefix) {
				anyMarker = true
			}
			if len(wantedBytes) > 0 && !wantedPresent && bytes.Contains(window, wantedBytes) {
				wantedPresent = true
			}
			if anyMarker && (wantedPresent || len(wantedBytes) == 0) {
				return anyMarker, wantedPresent, nil
			}
			if len(window) > overlap {
				window = append(window[:0], window[len(window)-overlap:]...)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return anyMarker, wantedPresent, nil
			}
			return false, false, fmt.Errorf("runtime overlay: read %s: %w", path, readErr)
		}
	}
}

// ProvenanceOverlay is what libbox.provenance records about the overlay.
type ProvenanceOverlay struct {
	Mode      Mode
	Marker    string
	GoVersion string
}

// ParseProvenanceOverlay reads the runtime.overlay* keys out of a libbox.provenance file.
//
// The verifier takes the expected mode from the artifact's own record rather than from a flag:
// a verifier told the mode by its caller can only confirm that the caller and the builder
// agree, which is not the claim. The claim is that the bytes match what the build says it did.
func ParseProvenanceOverlay(content string) (ProvenanceOverlay, error) {
	var parsed ProvenanceOverlay
	modes := make([]string, 0, 2)
	markers := make([]string, 0, 2)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "runtime.overlay="):
			modes = append(modes, strings.TrimPrefix(line, "runtime.overlay="))
		case strings.HasPrefix(line, "runtime.overlay.marker="):
			markers = append(markers, strings.TrimPrefix(line, "runtime.overlay.marker="))
		case strings.HasPrefix(line, "runtime.overlay.go="):
			parsed.GoVersion = strings.TrimPrefix(line, "runtime.overlay.go=")
		}
	}
	if len(modes) == 0 {
		return ProvenanceOverlay{}, fmt.Errorf(
			"runtime overlay: this provenance file records no runtime.overlay= key, so it does not say which overlay mode built the artifact. " +
				"A framework cannot be verified against a plan nobody wrote down")
	}
	// Duplicates are refused for the same reason CheckGoFlagsHasNoOverlay refuses duplicated
	// GOFLAGS: everyone who reads the first entry silently ignores the rest, so a second one is
	// either dead text or a record written twice by two different builds.
	if len(modes) > 1 {
		return ProvenanceOverlay{}, fmt.Errorf(
			"runtime overlay: this provenance file records runtime.overlay %d times (%s). Only one plan compiles an artifact, and a "+
				"reader that takes the first entry would verify against a mode the build may not have used",
			len(modes), strings.Join(modes, ", "))
	}
	if len(markers) > 1 {
		return ProvenanceOverlay{}, fmt.Errorf(
			"runtime overlay: this provenance file records runtime.overlay.marker %d times; exactly one plan may compile an artifact",
			len(markers))
	}
	parsedMode, err := ParseMode(modes[0])
	if err != nil {
		return ProvenanceOverlay{}, err
	}
	parsed.Mode = parsedMode
	if len(markers) == 1 {
		parsed.Marker = markers[0]
	}
	if parsedMode == ModeBase {
		if parsed.Marker != "" {
			return ProvenanceOverlay{}, fmt.Errorf(
				"runtime overlay: provenance records mode base but also a marker (%s). Mode base compiles no overlay, so a marker "+
					"here means the record and the build disagree", parsed.Marker)
		}
		return parsed, nil
	}
	if parsed.Marker == "" {
		return ProvenanceOverlay{}, fmt.Errorf(
			"runtime overlay: provenance records mode %s but no runtime.overlay.marker=, so there is no expected marker to check", modes[0])
	}
	// The decisive consistency check: the marker names the mode it was generated for, and both
	// fields are written by the same build step from the same plan.
	//
	// This used to be unchecked, so a record saying `runtime.overlay=both` beside a marker saying
	// `mode=small` was accepted and the verifier then checked the artifact against the MARKER while
	// printing the MODE: the wrong plan verified, the right one reported, and nothing failed.
	if !strings.HasPrefix(parsed.Marker, MarkerPrefix) {
		return ProvenanceOverlay{}, fmt.Errorf(
			"runtime overlay: the recorded marker does not start with %q: %s", MarkerPrefix, parsed.Marker)
	}
	markerMode, hasMode := markerModeField(parsed.Marker)
	if !hasMode || markerMode != parsedMode {
		return ProvenanceOverlay{}, fmt.Errorf(
			"runtime overlay: provenance records mode %s but its marker describes a different plan (%s). The two fields come from "+
				"one build step, so a disagreement means the record was edited or the artifact was recorded twice; neither may be verified",
			parsedMode, parsed.Marker)
	}
	return parsed, nil
}

// markerModeField reads the `mode=` field out of a marker string.
//
// It is a field scan rather than a substring test so a marker that ends at the mode - which the
// unit tests use, and which an older marker shape could carry - is read the same way as one that
// continues with parameters.
func markerModeField(marker string) (Mode, bool) {
	rest, ok := strings.CutPrefix(marker, MarkerPrefix)
	if !ok {
		return "", false
	}
	for _, field := range strings.Fields(rest) {
		if value, ok := strings.CutPrefix(field, "mode="); ok {
			return Mode(value), true
		}
	}
	return "", false
}

// ExpectedMarker is the marker the recorded provenance says the slices should carry.
func (p ProvenanceOverlay) ExpectedMarker() string {
	if p.Mode == ModeBase {
		return ""
	}
	return p.Marker
}
