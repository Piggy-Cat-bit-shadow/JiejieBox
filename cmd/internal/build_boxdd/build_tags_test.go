package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The daemon's tag set is a release artifact contract with two independent halves, and until this
// file NOTHING joined them:
//
//	release/DEFAULT_BUILD_TAGS_*        the profile files (the bulk of the tags)
//	buildTags in this package            three tags APPENDED on top, none of which appears in any
//	                                     profile file by design
//	verify-desktop-daemon-provenance.py  the artifact gate that asserts what the shipped binary
//	                                     carries
//
// The gate could therefore only check the half it listed. Everything below closes that.

// requiredByTheArtifactGate is the tag list scripts/ci/verify-desktop-daemon-provenance.py
// requires the shipped Windows daemon to carry.
//
// It is restated here rather than imported, because that file is a release script and not a Go
// package. A restated list that nothing compares would just be a second opinion, so
// TestTheRestatedGateListMatchesTheGate reads the script and fails when the two disagree - which
// means this copy cannot drift from the gate it mirrors, and editing one of them alone is a test
// failure rather than a silent divergence.
var requiredByTheArtifactGate = []string{
	"with_quic",
	"with_naive_outbound",
	"with_utls",
	"with_clash_api",
	"with_external_windivert",
	"with_external_usbip_drivers",
	"tfogo_checklinkname0",
}

// appendedByBuildTags are the tags no profile file carries, and the reason each is appended here
// rather than written into a profile.
var appendedByBuildTags = []string{
	// Travels with the -checklinkname=0 flag, which this recipe always passes. It must NOT go into
	// a profile file: those are also read by callers that link with Go's default -checklinkname
	// policy, and with the tag set experimental/libbox pulls runtime.fwdSig, which the linker
	// refuses without the flag.
	"tfogo_checklinkname0",
	// Windows only. The daemon's driver profile is a production decision, and both of these are
	// appended together.
	"with_external_windivert",
	"with_external_usbip_drivers",
}

// repoRoot walks up to the directory holding go.mod, because buildTags reads the profile files by
// path relative to the module root. Without this the test would depend on the working directory
// cmd/go happens to use, and a test that cannot find its inputs must fail rather than skip.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root above the test directory")
		}
		dir = parent
	}
}

// chdirToRepoRoot points the process at the module root for the duration of the test, so
// buildTags' relative profile paths resolve.
func chdirToRepoRoot(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if restoreErr := os.Chdir(previous); restoreErr != nil {
			t.Fatalf("restore working directory: %v", restoreErr)
		}
	})
}

// TestTheRestatedGateListMatchesTheGate makes the restated list a JOIN rather than a second
// opinion.
//
// Without it, editing the gate's REQUIRED_TAGS and leaving this copy alone would make the test
// above assert against a list nobody enforces - the exact "two things to keep in step" shape this
// repository has paid for repeatedly. The script is read and parsed rather than imported because it
// is a release script, not a Go package, so the test does the smallest thing that can notice a
// disagreement.
func TestTheRestatedGateListMatchesTheGate(t *testing.T) {
	root := repoRoot(t)

	const gatePath = "scripts/ci/verify-desktop-daemon-provenance.py"
	data, err := os.ReadFile(filepath.Join(root, gatePath))
	if err != nil {
		t.Fatalf("read %s: %v", gatePath, err)
	}

	// The list is a plain Python literal: `REQUIRED_TAGS = [` ... `]`, one quoted tag per line.
	source := string(data)
	start := strings.Index(source, "REQUIRED_TAGS = [")
	if start < 0 {
		t.Fatalf("%s no longer defines REQUIRED_TAGS; this test would pass vacuously and the tag "+
			"join is gone", gatePath)
	}
	end := strings.Index(source[start:], "]")
	if end < 0 {
		t.Fatalf("%s has an unterminated REQUIRED_TAGS list", gatePath)
	}
	body := source[start+len("REQUIRED_TAGS = [") : start+end]

	var fromGate []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if (strings.HasPrefix(line, `"`) && strings.HasSuffix(line, `",`)) ||
			(strings.HasPrefix(line, `"`) && strings.HasSuffix(line, `"`)) {
			fromGate = append(fromGate, strings.Trim(line, `",`))
		}
	}
	if len(fromGate) == 0 {
		t.Fatalf("parsed no tags out of REQUIRED_TAGS in %s; the parser and the file disagree, "+
			"and a silently empty list would make every assertion vacuous", gatePath)
	}

	slices.Sort(fromGate)
	restated := slices.Clone(requiredByTheArtifactGate)
	slices.Sort(restated)
	if !slices.Equal(fromGate, restated) {
		t.Errorf("this test's requiredByTheArtifactGate and %s's REQUIRED_TAGS disagree.\n"+
			"  gate:     %v\n  restated: %v\n"+
			"Update both together: the restated list is what the daemon's tags are checked "+
			"against here, and the gate's list is what the shipped artifact is checked against.",
			gatePath, fromGate, restated)
	}
}

// TestWindowsDaemonCarriesTheTagsTheArtifactGateRequires is the join between the two halves.
//
// The failure it prevents is concrete and was found by the Phase-2 audit: the gate required five
// tags, while buildTags appends three more. tfogo_checklinkname0 was in neither list, and this
// package's own comment says what losing it costs - "dropping it here would silently reduce the
// daemon's libbox to the stub crash handler and the stub goroutine report". A release could have
// shipped a Windows daemon with a stubbed crash handler and nothing would have noticed.
func TestWindowsDaemonCarriesTheTagsTheArtifactGateRequires(t *testing.T) {
	chdirToRepoRoot(t)

	tags, err := buildTags("windows", "amd64", false)
	if err != nil {
		t.Fatalf("buildTags(windows, amd64): %v", err)
	}
	if len(tags) == 0 {
		t.Fatal("the windows daemon resolves to no tags; every assertion below would be vacuous")
	}
	for _, required := range requiredByTheArtifactGate {
		if !slices.Contains(tags, required) {
			t.Errorf("the artifact gate requires %q but the windows daemon does not carry it.\n"+
				"Either buildTags stopped appending it, or the gate's REQUIRED_TAGS lists a tag "+
				"no daemon gets - both make the gate meaningless for that tag.\nresolved: %v",
				required, tags)
		}
	}
}

// TestAppendedTagsAreNotInAnyProfileFile pins the reason the append exists.
//
// The three tags are appended rather than written into a profile because the profile files are read
// by callers that must NOT have them - `go build -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./...`
// links with Go's default -checklinkname policy and cannot carry tfogo_checklinkname0 at all. A
// well-meaning "cleanup" that moved the tag into release/DEFAULT_BUILD_TAGS_WINDOWS would break
// that caller, and this test is what makes the move fail loudly instead.
func TestAppendedTagsAreNotInAnyProfileFile(t *testing.T) {
	root := repoRoot(t)

	matches, err := filepath.Glob(filepath.Join(root, "release", "DEFAULT_BUILD_TAGS*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no release/DEFAULT_BUILD_TAGS* files found; this test would pass vacuously")
	}
	for _, path := range matches {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		// The files are comma-separated single lines.
		profiled := strings.Split(strings.TrimSpace(string(data)), ",")
		for _, appended := range appendedByBuildTags {
			if slices.Contains(profiled, appended) {
				t.Errorf("%s names %q, which buildTags appends at the call site because the tag "+
					"travels with a linker flag that profile-file callers do not pass. Keep it out "+
					"of the profile.", filepath.Base(path), appended)
			}
		}
	}
}

// TestOnlyWindowsGetsTheDriverTags keeps the platform condition from being lost.
//
// The driver tags are appended under `if operatingSystem == "windows"`. Dropping that condition
// would put with_external_windivert on every platform's daemon, which is not merely wasteful: the
// tags select platform-specific implementations, so a non-Windows build reaching them is a build
// error waiting for the first release that exercises that path.
func TestOnlyWindowsGetsTheDriverTags(t *testing.T) {
	chdirToRepoRoot(t)

	linuxTags, err := buildTags("linux", "amd64", true)
	if err != nil {
		t.Fatalf("buildTags(linux, amd64): %v", err)
	}
	for _, windowsOnly := range []string{"with_external_windivert", "with_external_usbip_drivers"} {
		if slices.Contains(linuxTags, windowsOnly) {
			t.Errorf("linux resolved %q, which is appended only for windows", windowsOnly)
		}
	}
	// tfogo_checklinkname0 is NOT platform-conditional: every daemon this recipe builds links
	// libbox with the flag, so every platform must carry the tag.
	if !slices.Contains(linuxTags, "tfogo_checklinkname0") {
		t.Error("the linux daemon must carry tfogo_checklinkname0: the tag travels with the " +
			"-checklinkname=0 flag this recipe always passes, on every platform")
	}
}

// TestWindows386UsesTheOthersProfile pins the one profile-selection branch that is easy to get
// wrong. There is no windows/386 profile file, so 386 falls back to DEFAULT_BUILD_TAGS_OTHERS; a
// change that sent it to DEFAULT_BUILD_TAGS_WINDOWS would fail to build rather than silently ship,
// but the assertion makes the intent explicit.
func TestWindows386UsesTheOthersProfile(t *testing.T) {
	chdirToRepoRoot(t)

	others, err := os.ReadFile("release/DEFAULT_BUILD_TAGS_OTHERS")
	if err != nil {
		t.Fatal(err)
	}
	tags, err := buildTags("windows", "386", false)
	if err != nil {
		t.Fatalf("buildTags(windows, 386): %v", err)
	}
	for _, profiled := range strings.Split(strings.TrimSpace(string(others)), ",") {
		if profiled != "" && !slices.Contains(tags, profiled) {
			t.Errorf("windows/386 is missing %q from DEFAULT_BUILD_TAGS_OTHERS", profiled)
		}
	}
}
