package runtimeoverlay

// Adversarial coverage for the overlay GENERATOR: the failure paths that decide whether a
// half-formed overlay can reach a compiler.
//
// The shipping tests already prove the happy path and the two anchor refusals (missing, twice).
// What is attacked here is the window BETWEEN them: a written-but-unusable overlay directory, a
// GOROOT under a path the shell and the go command have to quote, and the invariant that makes
// "half a patch" unreachable in the first place - overlay.json is written last, so a consumer
// either sees a complete plan or no plan.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateNeverPublishesAPartiallyWrittenOverlay forces a write failure in the middle of the
// patched-file sequence and pins the consumer contract: overlay.json is the only entry point a
// build reads, and it must not exist when any file behind it is missing.
//
// This is the "patch only applied half" attack at the layer where it is reachable. A mode that
// applied the span-set edit and then failed on proc.go would compile a runtime whose values are
// half upstream and half plan; nothing downstream can detect that from a marker, because the
// marker edit is in proc.go too. The defence is that the failure happens before overlay.json is
// written, so there is nothing for a build to consume.
func TestGenerateNeverPublishesAPartiallyWrittenOverlay(t *testing.T) {
	toolchain := hostToolchain(t)
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Sorted edit order is asm (group B only), malloc, mspanset, proc. ModeSmall has no asm edit,
	// so malloc and mspanset are written first and proc.go is where the write fails. A directory
	// occupying the destination name makes os.WriteFile fail on every platform without relying on
	// permissions or a read-only filesystem.
	blocked := filepath.Join(dir, "runtime", strings.ReplaceAll(sourceProc, "/", "_"))
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	overlay, err := Generate(GenerationRequest{
		Root:      repoRoot(t),
		Dir:       dir,
		Plan:      plan,
		Toolchain: toolchain,
	})
	if err == nil {
		t.Fatal("a write failure in the middle of the patched files must fail the generation")
	}
	if overlay != nil {
		t.Fatalf("a failed generation returned an overlay: %+v", overlay)
	}
	if !strings.Contains(err.Error(), filepath.Base(blocked)) {
		t.Fatalf("the failure must name the file it could not write: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "overlay.json")); !os.IsNotExist(statErr) {
		t.Fatalf("overlay.json exists after a failed generation (%v); a build would consume a half-applied patch", statErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("%s was written before generation completed", entry.Name())
		}
	}
}

// TestGenerateRefusesEitherAddedFileNameInTheGOROOT covers the second added file. The shipping
// test covers jj_ios_runtime_overlay.go only; a leftover of the OFF file is the same collision -
// the overlay would replace a file it means to add - and must fail with that file's name.
func TestGenerateRefusesEitherAddedFileNameInTheGOROOT(t *testing.T) {
	toolchain := hostToolchain(t)
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{addedOnFile, addedOffFile} {
		goroot := t.TempDir()
		copyReviewedFiles(t, toolchain, goroot)
		if err := os.WriteFile(filepath.Join(goroot, "src", "runtime", name), []byte("package runtime\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		_, err := Generate(GenerationRequest{
			Root:      repoRoot(t),
			Dir:       dir,
			Plan:      plan,
			Toolchain: Toolchain{GOROOT: goroot, GOVersion: toolchain.GOVersion},
		})
		if err == nil {
			t.Fatalf("a GOROOT that already has %s must fail the build", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("the refusal must name %s: %v", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "overlay.json")); !os.IsNotExist(statErr) {
			t.Fatalf("%s collision still wrote overlay.json", name)
		}
	}
}

// TestGenerateRefusesAToolchainWithAMissingReviewedFile is the missing half of the patched-GOROOT
// test: Check must fail on a file it cannot read, not panic and not fall through to the anchors.
func TestGenerateRefusesAToolchainWithAMissingReviewedFile(t *testing.T) {
	toolchain := hostToolchain(t)
	goroot := t.TempDir()
	copyReviewedFiles(t, toolchain, goroot)
	if err := os.Remove(filepath.Join(goroot, filepath.FromSlash("src/runtime/asm_amd64.s"))); err != nil {
		t.Fatal(err)
	}
	err := Toolchain{GOROOT: goroot, GOVersion: toolchain.GOVersion}.Check()
	if err == nil {
		t.Fatal("a toolchain missing a reviewed file must fail closed")
	}
	if !strings.Contains(err.Error(), "asm_amd64.s") {
		t.Fatalf("the refusal must name the missing file: %v", err)
	}
}

// TestGeneratedReplacementsDoNotContainEachOthersAnchors is the invariant that makes sequential
// edits on ONE file safe. applyEdits applies edits in slice order and re-searches the text each
// time; if one replacement contained another edit's anchor, the second edit would match text the
// generator itself inserted, and the resulting file would still be syntactically valid. That is
// the quiet version of "patched the wrong occurrence".
func TestGeneratedReplacementsDoNotContainEachOthersAnchors(t *testing.T) {
	for _, mode := range []Mode{ModeSmall, ModeCGO, ModeBoth} {
		plan, err := Resolve(mode, "ios/arm64")
		if err != nil {
			t.Fatal(err)
		}
		edits := editsFor(plan)
		// Two edits on the SAME source may not see each other's output. An insertion-style edit
		// that re-emits its own anchor (the marker call, the cgocallback comment) is fine only
		// while no other edit on that file searches for the same literal; the assertion below
		// covers that, including the same-anchor case.
		for left := range edits {
			for right := range edits {
				if left == right || edits[left].source != edits[right].source {
					continue
				}
				if strings.Contains(edits[left].replacement, edits[right].anchor) {
					t.Fatalf("%s: the %s replacement contains the %s anchor, so edit order changes what is patched",
						mode, edits[left].group, edits[right].group)
				}
			}
		}
	}
}

// applyEdits is sequential by contract, and the real edit set is order-insensitive only because of
// the invariant above. This pins the engine's own behaviour so a future "apply all anchors against
// the original text" refactor is a deliberate change: a second edit DOES see the first edit's
// output.
func TestApplyEditsAppliesSequentially(t *testing.T) {
	first := edit{source: "proc.go", anchor: "A\n", replacement: "B\nA\n", group: "first"}
	second := edit{source: "proc.go", anchor: "A\n", replacement: "C\n", group: "second"}
	// The first edit turns one "A\n" into "B\nA\n"; the second then sees exactly one "A\n" and
	// replaces it. A refactor that applied every edit against the ORIGINAL text would leave "A\n"
	// in place, and the assertion below would catch it.
	got, err := applyEdits("proc.go", "A\n", []edit{first, second}, "go1.27.4")
	if err != nil {
		t.Fatal(err)
	}
	if got != "B\nC\n" {
		t.Fatalf("sequential application produced %q", got)
	}

	// The count check is on the INPUT, not on the output: a replacement that re-emits its anchor
	// is accepted and duplicates it. That is not attacker-reachable (the replacements are literals
	// in this package), and TestGeneratedReplacementsDoNotContainEachOthersAnchors is the check
	// that keeps the real edit set from being in that shape. Pinned here so the engine's contract
	// is documented rather than assumed to be stronger than it is.
	duplicating := edit{source: "proc.go", anchor: "A\n", replacement: "A\nA\n", group: "duplicates"}
	got, err = applyEdits("proc.go", "A\n", []edit{duplicating}, "go1.27.4")
	if err != nil || got != "A\nA\n" {
		t.Fatalf("the engine validates the input count, not the output: got %q, %v", got, err)
	}
}

// TestGenerateFromAGOROOTPathWithSpacesAndUnicode is the source-path half of the path attack: a
// GOROOT whose directory name contains spaces, non-ASCII letters and an apostrophe. Generate must
// read through it, the Replace keys must be those paths (not re-quoted or re-encoded), and every
// generated file must exist.
func TestGenerateFromAGOROOTPathWithSpacesAndUnicode(t *testing.T) {
	toolchain := hostToolchain(t)
	weird := filepath.Join(t.TempDir(), "gö 空 格's root")
	if err := os.MkdirAll(weird, 0o755); err != nil {
		t.Fatal(err)
	}
	copyReviewedFiles(t, toolchain, weird)

	plan, err := Resolve(ModeBoth, "ios/arm64,macos/arm64")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	overlay, err := Generate(GenerationRequest{
		Root:      repoRoot(t),
		Dir:       dir,
		Plan:      plan,
		Toolchain: Toolchain{GOROOT: weird, GOVersion: toolchain.GOVersion},
	})
	if err != nil {
		t.Fatalf("a GOROOT path with spaces/Unicode/apostrophe must generate: %v", err)
	}
	payload, err := os.ReadFile(overlay.JSONPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct{ Replace map[string]string }
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("overlay.json is not valid JSON: %v\n%s", err, payload)
	}
	if len(document.Replace) == 0 {
		t.Fatal("overlay.json replaced nothing")
	}
	for source, replacement := range document.Replace {
		if !strings.HasPrefix(source, weird+string(os.PathSeparator)) {
			t.Fatalf("Replace key %q is not under the GOROOT it was generated from", source)
		}
		if _, statErr := os.Stat(replacement); statErr != nil {
			t.Fatalf("Replace value %q does not exist: %v", replacement, statErr)
		}
	}
	for _, file := range overlay.Files {
		if _, statErr := os.Stat(file.Path); statErr != nil {
			t.Fatalf("generated file %q does not exist: %v", file.Path, statErr)
		}
	}
}

// TestOverlayBuildsFromADirectoryWithSpacesAndUnicode is the destination-path half: the scratch
// directory (and therefore every replacement path and overlay.json itself) contains spaces, a
// non-ASCII letter and an apostrophe. The go command must accept the overlay and the compiled
// runtime archive must carry the plan's marker, which is what proves the patched files - not just
// the JSON - were read.
func TestOverlayBuildsFromADirectoryWithSpacesAndUnicode(t *testing.T) {
	if testing.Short() {
		t.Skip("this test invokes the go command; -short skips it")
	}
	goBinary := goBinary(t)
	plan, _, _ := buildOverlayForTest(t, ModeBoth)

	weirdDir := filepath.Join(t.TempDir(), "overlay 空 格's dir")
	if err := os.MkdirAll(weirdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toolchain := hostToolchain(t)
	weirdOverlay, err := Generate(GenerationRequest{
		Root:      repoRoot(t),
		Dir:       weirdDir,
		Plan:      plan,
		Toolchain: toolchain,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(weirdOverlay.JSONPath, "空") {
		t.Fatalf("the scratch path was not preserved: %q", weirdOverlay.JSONPath)
	}
	archive := filepath.Join(t.TempDir(), "runtime 空.a")
	command := exec.Command(goBinary, "build", "-overlay="+weirdOverlay.JSONPath, "-tags", plan.onConstraint(), "-o", archive, "runtime")
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, buildErr := command.CombinedOutput(); buildErr != nil {
		t.Fatalf("the go command rejected an overlay whose scratch path has spaces/Unicode: %v\n%s", buildErr, output)
	}
	compiled, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(compiled, []byte(plan.Marker())) {
		t.Fatal("the compiled runtime archive does not carry the marker; the added file was not compiled")
	}
	// The same archive built without the probe tag must NOT carry it: the off file is selected and
	// restates upstream. That asymmetry is what the macOS negative check relies on, and it is
	// asserted here on a real compiled artifact rather than on generated source.
	upstreamArchive := filepath.Join(t.TempDir(), "upstream.a")
	command = exec.Command(goBinary, "build", "-overlay="+weirdOverlay.JSONPath, "-o", upstreamArchive, "runtime")
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, buildErr := command.CombinedOutput(); buildErr != nil {
		t.Fatalf("the upstream half of the overlay did not build: %v\n%s", buildErr, output)
	}
	upstream, err := os.ReadFile(upstreamArchive)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(upstream, []byte(MarkerPrefix)) {
		t.Fatal("a runtime built without the overlay tag carries an overlay marker; the two added files are not selecting cleanly")
	}
}
