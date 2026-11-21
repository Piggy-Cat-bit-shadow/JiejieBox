package runtimeoverlay

// Bad-artifact fixtures for the slice verifier. The shipping tests check the four verdicts and the
// chunk-boundary scan; this file attacks the claim the verifier actually makes, in the situations a
// build system can produce but a unit test does not:
//
//   - the marker is present somewhere NEAR the binary (the framework's Info.plist), not in it;
//   - the marker is a near miss: one field removed, one field added, a prefix only;
//   - the artifact was stripped, packed and unpacked, then a byte inside the marker was damaged;
//   - the artifact and the provenance record disagree;
//   - a slice directory holds two frameworks, because the verifier must not guess which is libbox.
//
// Every fixture is built without Xcode: the host builds a REAL stripped Go binary carrying the
// overlay's marker, so the scan is exercised on the same class of file the release produces. Apple
// slice execution stays BLOCKED_BY_ENVIRONMENT; see the report.

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeRealStrippedArtifact builds a host binary with the overlay's values and the release link
// flags, and returns its path and bytes. It is the real-artifact half of the verifier fixtures:
// the marker has to survive `-s -w -buildid=`, which is what the release uses.
func writeRealStrippedArtifact(t *testing.T, plan Plan, overlay *Overlay) (string, []byte) {
	t.Helper()
	goBinary := goBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifierprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "probe")
	command := exec.Command(goBinary, "build", "-overlay="+overlay.JSONPath,
		"-ldflags", "-s -w -buildid=", "-tags", plan.onConstraint(), "-o", binary, ".")
	command.Dir = dir
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("building the stripped artifact: %v\n%s", err, output)
	}
	artifact, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return binary, artifact
}

// TestVerifierScansTheBinaryNotTheSidecar is the "the marker is there, surely" trap: an
// Info.plist next to the binary contains the exact marker while the binary does not. A verifier
// that grepped the slice directory instead of the framework binary would pass this artifact.
func TestVerifierScansTheBinaryNotTheSidecar(t *testing.T) {
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFakeFramework(t, root, map[string]string{
		"ios-arm64":   "\x00\x01binary without the overlay\x00",
		"macos-arm64": "\x00\x01binary without the overlay\x00",
	})
	framework := filepath.Join(root, "ios-arm64", "Libbox.framework")
	if err := os.WriteFile(filepath.Join(framework, "Info.plist"), []byte("<plist>"+plan.Marker()+"</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireVerifiedXCframework(root, plan.Marker()); err == nil {
		t.Fatal("the marker in Info.plist satisfied the artifact check; the verifier is not scanning the binary")
	} else if !strings.Contains(err.Error(), "no jiejiebox-ios-runtime-overlay") {
		t.Fatalf("the failure must say the marker is absent from the slice: %v", err)
	}
}

// TestVerifierRejectsNearMissMarkers covers the "patch was half-applied and the marker was
// hand-edited" shapes. Every one is a different plan's string, which the verifier must distinguish
// from "no overlay at all" because the two mean different things to whoever reads the log.
func TestVerifierRejectsNearMissMarkers(t *testing.T) {
	plan, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	marker := plan.Marker()
	mutations := []struct{ name, value string }{
		{"a field removed", strings.Replace(marker, " spanSetInitSpineCap=16", "", 1)},
		{"a value edited", strings.Replace(marker, "stackCacheSize=16384", "stackCacheSize=32768", 1)},
		{"only the prefix", MarkerPrefix},
		{"only the version", MarkerPrefix + "1"},
		{"a truncated tail", marker[:len(marker)-7]},
		{"a NUL inside", strings.Replace(marker, "mode=both", "mode=bo\x00th", 1)},
	}
	for _, mutation := range mutations {
		root := t.TempDir()
		writeFakeFramework(t, root, map[string]string{
			"ios-arm64":   "prefix" + mutation.value + "suffix",
			"macos-arm64": "plain",
		})
		checks, err := VerifyXCframework(root, marker)
		if err != nil {
			t.Fatalf("%s: %v", mutation.name, err)
		}
		if !checks[0].AnyMarker {
			t.Fatalf("%s: an overlay-prefixed string was not recognised as a marker at all", mutation.name)
		}
		if checks[0].WantedPresent {
			t.Fatalf("%s: a mutated marker matched the wanted one", mutation.name)
		}
		if verdictErr := checks[0].Verdict(); verdictErr == nil {
			t.Fatalf("%s: a slice with a near-miss marker passed", mutation.name)
		}
	}
}

// TestVerifierFindsAMarkerAtTheFileEdges covers the two offsets the overlap window does not get a
// second chance at: the very first bytes, and a marker that ends exactly at EOF.
func TestVerifierFindsAMarkerAtTheFileEdges(t *testing.T) {
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	marker := plan.Marker()
	for name, payload := range map[string]string{
		"at offset zero": marker + "tail",
		"ending at EOF":  "head" + marker,
		"exactly":        marker,
	} {
		root := t.TempDir()
		writeFakeFramework(t, root, map[string]string{
			"ios-arm64":   payload,
			"macos-arm64": "plain",
		})
		checks, err := RequireVerifiedXCframework(root, marker)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !checks[0].WantedPresent {
			t.Fatalf("%s: the marker was not found", name)
		}
	}
}

// TestVerifierRejectsASliceWithTwoFrameworks: the verifier must not pick one of two framework
// directories. Picking would mean the marker check could pass on a framework the build did not
// produce while the real one was unverified.
func TestVerifierRejectsASliceWithTwoFrameworks(t *testing.T) {
	root := t.TempDir()
	slice := filepath.Join(root, "ios-arm64")
	for _, name := range []string{"Libbox", "Other"} {
		framework := filepath.Join(slice, name+".framework")
		if err := os.MkdirAll(framework, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(framework, name), []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := VerifyXCframework(root, "anything"); err == nil {
		t.Fatal("a slice with two frameworks was accepted; the verifier guessed which one is libbox")
	}
}

// TestRepackedArtifactStillVerifiesAndOneBrokenByteFails is the strip/repack/install property at
// the host level. The real artifact is zipped and unzipped - a different inode, directory entry
// order and path - and must still verify. Then exactly one byte inside the marker is changed, which
// must fail: this is what separates "the bytes contain the plan" from "a marker exists".
func TestRepackedArtifactStillVerifiesAndOneBrokenByteFails(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and archives a real binary; -short skips it")
	}
	plan, overlay, _ := buildOverlayForTest(t, ModeSmall)
	_, artifact := writeRealStrippedArtifact(t, plan, overlay)

	packed := t.TempDir()
	writeFakeFramework(t, packed, map[string]string{"ios-arm64": string(artifact), "macos-arm64": "plain macos binary"})
	if _, err := RequireVerifiedXCframework(packed, plan.Marker()); err != nil {
		t.Fatalf("the freshly built artifact does not verify: %v", err)
	}

	archivePath := filepath.Join(t.TempDir(), "Libbox.xcframework.zip")
	zipTree(t, packed, archivePath)
	unpacked := t.TempDir()
	unzipTree(t, archivePath, unpacked)
	if _, err := RequireVerifiedXCframework(unpacked, plan.Marker()); err != nil {
		t.Fatalf("the repacked artifact does not verify: %v", err)
	}

	binary := filepath.Join(unpacked, "ios-arm64", "Libbox.framework", "Libbox")
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	index := bytes.Index(data, []byte(plan.Marker()))
	if index < 0 {
		t.Fatal("the marker vanished across the archive round trip")
	}
	damaged := append([]byte{}, data...)
	// Change one byte in the middle of the marker's `mode=` field: the plan name no longer matches.
	damaged[index+len("jiejiebox-ios-runtime-overlay/1 mode=")] ^= 0x01
	if err := os.WriteFile(binary, damaged, 0o755); err != nil {
		t.Fatal(err)
	}
	checks, err := VerifyXCframework(unpacked, plan.Marker())
	if err != nil {
		t.Fatalf("reading the damaged artifact: %v", err)
	}
	if checks[0].WantedPresent {
		t.Fatal("a damaged marker still matched; the check is not comparing the whole plan")
	}
	if _, err := RequireVerifiedXCframework(unpacked, plan.Marker()); err == nil {
		t.Fatal("an artifact whose marker byte was damaged passed the verification")
	}
}

// TestProvenanceDrivesTheArtifactDecision is P20's end-to-end path: the artifact says which plan it
// claims, the verifier derives the expected marker from that record, and a mismatch fails. A
// verifier handed the mode by its caller could only agree with the caller.
func TestProvenanceDrivesTheArtifactDecision(t *testing.T) {
	both, err := Resolve(ModeBoth, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	small, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}

	// Honest record, honest artifact: passes.
	root := t.TempDir()
	writeFakeFramework(t, root, map[string]string{"ios-arm64": both.Marker(), "macos-arm64": "plain"})
	recorded, err := ParseProvenanceOverlay("runtime.overlay=both\nruntime.overlay.marker=" + both.Marker() + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequireVerifiedXCframework(root, recorded.ExpectedMarker()); err != nil {
		t.Fatalf("an honest artifact did not verify: %v", err)
	}

	// The record survived from a small build while the artifact was built with both: the marker in
	// the record is not the marker in the bytes, and the artifact must fail even though BOTH strings
	// are valid markers.
	root = t.TempDir()
	writeFakeFramework(t, root, map[string]string{"ios-arm64": both.Marker(), "macos-arm64": "plain"})
	recorded, err = ParseProvenanceOverlay("runtime.overlay=small\nruntime.overlay.marker=" + small.Marker() + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequireVerifiedXCframework(root, recorded.ExpectedMarker()); err == nil {
		t.Fatal("an artifact whose bytes say mode both verified against a provenance record that says small")
	}

	// A base record with a marker-bearing artifact is the leak the base case must catch.
	baseRecord, err := ParseProvenanceOverlay("runtime.overlay=base\n")
	if err != nil {
		t.Fatal(err)
	}
	if baseRecord.ExpectedMarker() != "" {
		t.Fatalf("a base record expected a marker: %q", baseRecord.ExpectedMarker())
	}
	if _, err := RequireVerifiedXCframework(root, baseRecord.ExpectedMarker()); err == nil {
		t.Fatal("an artifact carrying a marker verified against a base record; the overlay leaked into a slice that must keep the official runtime")
	}
}

func zipTree(t *testing.T, source, destination string) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	err = filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if relative == "." {
				return nil
			}
			_, err = writer.Create(relative + "/")
			return err
		}
		entry, err := writer.Create(relative)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func unzipTree(t *testing.T, source, destination string) {
	t.Helper()
	reader, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
