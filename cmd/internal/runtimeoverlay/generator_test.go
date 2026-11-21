package runtimeoverlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// goBinary is the go command the tests drive. It is resolved through PATH, which is how every
// build in this repository works; a host without one skips rather than fails, because the
// overlay itself needs no toolchain to be constructed.
func goBinary(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH; the toolchain-dependent half of this package cannot run")
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root above the test directory")
		}
		dir = parent
	}
}

// hostToolchain probes the toolchain the tests run under, which is the one go.mod pins.
func hostToolchain(t *testing.T) Toolchain {
	t.Helper()
	toolchain, err := Probe(goBinary(t), repoRoot(t), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	return toolchain
}

// The `!ios` file restates the toolchain's own values so that the shared edits are inert on
// macOS. That restatement is a second source of truth, and a second source of truth drifts:
// these assertions read the values out of the live GOROOT so a drift is a failure here rather
// than a behaviour change on the slice that is supposed to be untouched.
func TestUpstreamParamsMatchTheToolchain(t *testing.T) {
	toolchain := hostToolchain(t)
	spanSet := readToolchainFile(t, toolchain, sourceMSpanSet)
	blockEntries := regexp.MustCompile(`(?m)^\tspanSetBlockEntries = (\d+)`).FindStringSubmatch(spanSet)
	spineCap := regexp.MustCompile(`(?m)^\tspanSetInitSpineCap = (\d+)`).FindStringSubmatch(spanSet)
	if blockEntries == nil || spineCap == nil {
		t.Fatalf("%s no longer declares spanSetBlockEntries/spanSetInitSpineCap in the form this test reads", sourceMSpanSet)
	}
	malloc := readToolchainFile(t, toolchain, sourceMalloc)
	stackCache := regexp.MustCompile(`(?m)^\t_StackCacheSize = (\d+) \* 1024`).FindStringSubmatch(malloc)
	if stackCache == nil {
		t.Fatalf("%s no longer declares _StackCacheSize as N * 1024", sourceMalloc)
	}
	upstream := upstreamParams()
	if value := mustAtoi(t, blockEntries[1]); value != upstream.SpanSetBlockEntries {
		t.Fatalf("upstream spanSetBlockEntries is %d but upstreamParams says %d", value, upstream.SpanSetBlockEntries)
	}
	if value := mustAtoi(t, spineCap[1]); value != upstream.SpanSetInitSpineCap {
		t.Fatalf("upstream spanSetInitSpineCap is %d but upstreamParams says %d", value, upstream.SpanSetInitSpineCap)
	}
	if value := mustAtoi(t, stackCache[1]) * 1024; value != upstream.StackCacheSize {
		t.Fatalf("upstream _StackCacheSize is %d but upstreamParams says %d", value, upstream.StackCacheSize)
	}
	if upstream.FreeExitedStack || upstream.DropExtraM {
		t.Fatal("upstreamParams must describe the toolchain, and the toolchain does neither of the two optional changes")
	}
}

// The pin is what makes an un-reviewed toolchain fail closed. This asserts it against the
// runtime the tests are actually compiled with, so a toolchain upgrade that nobody re-reviewed
// fails here instead of in a shipping build - and fails with the re-review instructions.
func TestReviewedToolchainMatchesTheRuntimeUnderTest(t *testing.T) {
	toolchain := hostToolchain(t)
	if err := toolchain.Check(); err != nil {
		t.Fatalf("%v", err)
	}
	reviewed, ok := reviewedToolchains[toolchain.GOVersion]
	if !ok {
		t.Fatalf("%s is not in reviewedToolchains", toolchain.GOVersion)
	}
	if len(reviewed.files) != 5 {
		t.Fatalf("the pin for %s covers %d files; it must cover every file an edit can touch", toolchain.GOVersion, len(reviewed.files))
	}
}

func TestToolchainCheckRefusesAnUnreviewedVersion(t *testing.T) {
	err := Toolchain{GOROOT: "/nonexistent", GOVersion: "go1.99.0"}.Check()
	if err == nil {
		t.Fatal("an un-reviewed toolchain version must fail closed")
	}
	for _, fragment := range []string{"go1.99.0", "reviewedToolchains", strings.Join(ReviewedVersions(), ", ")} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("the refusal must say %q so the next reader knows what to do: %v", fragment, err)
		}
	}
}

func TestToolchainCheckRefusesAToolchainInsideTheModuleCache(t *testing.T) {
	err := Toolchain{
		GOROOT:     filepath.Join("/home/user/go/pkg/mod", "golang.org", "toolchain@v0.0.1-go1.27.4.darwin-arm64"),
		GOMODCACHE: "/home/user/go/pkg/mod",
		GOVersion:  "go1.27.4",
	}.Check()
	if err == nil {
		t.Fatal("a GOROOT under GOMODCACHE cannot be overlaid and must fail closed")
	}
	if !strings.Contains(err.Error(), "GOMODCACHE") {
		t.Fatalf("the refusal must name the cause: %v", err)
	}
}

func TestToolchainCheckRefusesAPatchedGOROOT(t *testing.T) {
	toolchain := hostToolchain(t)
	if toolchain.GOVersion != "go1.27.4" {
		t.Skip("the fixture below rewrites a go1.27.4 file")
	}
	dir := t.TempDir()
	copyReviewedFiles(t, toolchain, dir)
	proc := filepath.Join(dir, filepath.FromSlash(sourceProc))
	data, err := os.ReadFile(proc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proc, append(data, []byte("\n// a local patch\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Toolchain{GOROOT: dir, GOVersion: toolchain.GOVersion}.Check()
	if err == nil {
		t.Fatal("a GOROOT whose runtime source does not hash to the reviewed bytes must fail closed")
	}
	if !strings.Contains(err.Error(), "proc.go") {
		t.Fatalf("the refusal must name the file: %v", err)
	}
}

// copyReviewedFiles builds a GOROOT-shaped directory holding only the pinned files, so the
// refusal paths can be exercised without touching a real toolchain.
func copyReviewedFiles(t *testing.T, toolchain Toolchain, dir string) {
	t.Helper()
	reviewed := reviewedToolchains[toolchain.GOVersion]
	for _, source := range sortedKeys(reviewed.files) {
		target := filepath.Join(dir, filepath.FromSlash(source))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(toolchain.GOROOT, filepath.FromSlash(source)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readToolchainFile(t *testing.T, toolchain Toolchain, source string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(toolchain.GOROOT, filepath.FromSlash(source)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustAtoi(t *testing.T, text string) int {
	t.Helper()
	value, err := strconv.Atoi(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// The edits must be visible in the generated files and absent from them afterwards: an anchor
// that survives means the replacement did not happen, which is the failure mode a count check
// alone would not catch if the anchor and the replacement overlapped.
func TestGeneratedOverlayReplacesEveryAnchor(t *testing.T) {
	toolchain := hostToolchain(t)
	for _, mode := range []Mode{ModeSmall, ModeCGO, ModeBoth} {
		plan, err := Resolve(mode, "ios/arm64,macos/arm64")
		if err != nil {
			t.Fatal(err)
		}
		request := GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan, Toolchain: toolchain}
		overlay, err := Generate(request)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		edits := editsFor(plan)
		generated := map[string]string{}
		for _, file := range overlay.Files {
			if file.Role != "replace" {
				continue
			}
			data, err := os.ReadFile(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			generated[file.Source] = string(data)
		}
		wantReplaced := 3
		if plan.groupB() {
			wantReplaced = 4
		}
		if len(generated) != wantReplaced {
			t.Fatalf("%s: %d replaced file(s), want %d", mode, len(generated), wantReplaced)
		}
		for _, edit := range edits {
			text, ok := generated[edit.source]
			if !ok {
				t.Fatalf("%s: %s was not replaced", mode, edit.source)
			}
			if count := strings.Count(text, edit.replacement); count != 1 {
				t.Fatalf("%s: the %s replacement occurs %d times in %s", mode, edit.group, count, edit.source)
			}
			// An insertion-style edit (the marker call) legitimately leaves its anchor in place,
			// because the replacement starts with it. Every other anchor must be gone.
			if !strings.Contains(edit.replacement, edit.anchor) && strings.Contains(text, edit.anchor) {
				t.Fatalf("%s: the %s anchor survived in %s", mode, edit.group, edit.source)
			}
		}
		// Every edited line is annotated, so a patched runtime is attributable even when the
		// overlay directory is long gone.
		for _, source := range []string{sourceMSpanSet, sourceMalloc, sourceProc} {
			if !strings.Contains(generated[source], siteNote) {
				t.Fatalf("%s: %s carries no site note", mode, source)
			}
		}
		if plan.groupB() && !strings.Contains(generated[sourceASMArm64], "MOVBU\truntime·jjOverlayDropExtraM(SB), R6") {
			t.Fatalf("%s: the assembly half of group B is missing", mode)
		}
		if !plan.groupB() && len(generated[sourceASMArm64]) != 0 {
			t.Fatalf("%s: group B is off but %s was replaced", mode, sourceASMArm64)
		}
	}
}

func TestGeneratedAddedFilesCarryTheRightMarker(t *testing.T) {
	toolchain := hostToolchain(t)
	plan, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := Generate(GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan, Toolchain: toolchain})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range overlay.Files {
		data, err := os.ReadFile(file.Path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(file.Path)] = string(data)
		if file.Source != "" {
			files[file.Source] = string(data)
		}
	}
	onFile, ok := files[addedOnFile]
	if !ok {
		t.Fatalf("%s was not generated", addedOnFile)
	}
	offFile, ok := files[addedOffFile]
	if !ok {
		t.Fatalf("%s was not generated", addedOffFile)
	}
	if !strings.Contains(onFile, `//go:build ios`) {
		t.Fatal("the value file must be selected by the ios build constraint")
	}
	if !strings.Contains(offFile, `//go:build !ios`) {
		t.Fatal("the upstream file must be selected by the negation of that constraint")
	}
	if !strings.Contains(onFile, quote(overlay.Marker)) {
		t.Fatalf("the value file does not carry the plan's marker:\n%s", onFile)
	}
	if strings.Contains(offFile, MarkerPrefix) {
		t.Fatalf("the upstream file carries an overlay marker, so a macOS slice would fail the leak check:\n%s", offFile)
	}
	if !strings.Contains(onFile, "func jjOverlayMarker() {") || !strings.Contains(onFile, "func jjOverlayMarkerValue() string {") {
		t.Fatal("the marker must be reachable from schedinit and must not be a plain constant")
	}
	if !strings.Contains(files[sourceProc], "jjOverlayMarker()") {
		t.Fatal("schedinit must call the marker; a marker nothing references is not evidence")
	}
	if !strings.Contains(onFile, "import \"internal/goarch\"") {
		t.Fatal("the group B value must resolve the arm64 condition, which needs internal/goarch")
	}
	// The upstream file must restate the toolchain's values, not the plan's. The patterns allow
	// gofmt's column alignment, because a generated file is required to be gofmt-clean.
	for _, expected := range []string{
		`jjOverlaySpanSetBlockEntries\s+= 512`,
		`jjOverlaySpanSetInitSpineCap\s+= 256`,
		`jjOverlayStackCacheSize\s+= 32768`,
		`jjOverlayFreeExitedStack\s+= false`,
		`jjOverlayFreeExitedStackBacklog\s+= 0`,
		`var jjOverlayDropExtraM\s+= false`,
	} {
		if !regexp.MustCompile(expected).MatchString(offFile) {
			t.Fatalf("the upstream file does not carry %q:\n%s", expected, offFile)
		}
	}
}

func TestGenerateRefusesToReplaceAFileItMeansToAdd(t *testing.T) {
	toolchain := hostToolchain(t)
	dir := t.TempDir()
	copyReviewedFiles(t, toolchain, dir)
	if err := os.WriteFile(filepath.Join(dir, "src", "runtime", addedOnFile), []byte("package runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Generate(GenerationRequest{
		Root:      repoRoot(t),
		Dir:       t.TempDir(),
		Plan:      plan,
		Toolchain: Toolchain{GOROOT: dir, GOVersion: toolchain.GOVersion},
	})
	if err == nil {
		t.Fatal("a GOROOT that already has a file the overlay adds must fail the build")
	}
	if !strings.Contains(err.Error(), addedOnFile) {
		t.Fatalf("the refusal must name the file: %v", err)
	}
}

func TestGenerateRefusesAnInertPlan(t *testing.T) {
	plan, err := Resolve(ModeBase, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan}); err == nil {
		t.Fatal("mode base compiles nothing and Generate must refuse it rather than write an empty overlay")
	}
}

func TestApplyEditsRefusesMissingAndDuplicatedAnchors(t *testing.T) {
	edits := []edit{{source: "proc.go", anchor: "keep the stack\n", replacement: "free the stack\n", group: "test"}}
	if got, err := applyEdits("proc.go", "a\nkeep the stack\nb\n", edits, "go1.27.4"); err != nil || got != "a\nfree the stack\nb\n" {
		t.Fatalf("an anchor present once must be replaced: %q, %v", got, err)
	}
	if _, err := applyEdits("proc.go", "a\nb\n", edits, "go1.27.4"); err == nil {
		t.Fatal("an anchor the toolchain no longer has must fail the build, not vanish")
	}
	if _, err := applyEdits("proc.go", "keep the stack\nkeep the stack\n", edits, "go1.27.4"); err == nil {
		t.Fatal("an anchor the toolchain has twice must fail the build, not pick one")
	}
}

// Every anchor this package relies on must exist exactly once in the reviewed runtime. This is
// the check that would have caught a hand-edited anchor that no longer matches, independent of
// the digests: the digests say "this is the reviewed source", this says "the reviewed source
// still has the text the edits assume".
func TestAnchorsAreUniqueInTheReviewedRuntime(t *testing.T) {
	toolchain := hostToolchain(t)
	plan, err := Resolve(ModeBoth, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, current := range editsFor(plan) {
		text := readToolchainFile(t, toolchain, current.source)
		if count := strings.Count(text, current.anchor); count != 1 {
			t.Fatalf("%s: the %s anchor occurs %d times", current.source, current.group, count)
		}
		seen[current.source]++
	}
	if seen[sourceASMArm64] != 1 {
		t.Fatalf("group B must edit %s exactly once: %d", sourceASMArm64, seen[sourceASMArm64])
	}
}
