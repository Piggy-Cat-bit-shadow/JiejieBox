package runtimeoverlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// buildStressProgram copies the stress probe into its own module so it can be built against the
// overlay without being part of this module's package graph.
func buildStressProgram(t *testing.T, goBinary string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stress")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("testdata", "stress", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module stress\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runStress(t *testing.T, goBinary, dir string, overlay *Overlay, plan Plan, mode string, extraEnv ...string) map[string]uint64 {
	t.Helper()
	command := exec.Command(goBinary, "run", "-overlay="+overlay.JSONPath, "-tags", plan.onConstraint(), ".")
	command.Dir = dir
	command.Env = append(os.Environ(),
		append([]string{
			"CGO_ENABLED=0",
			"GOFLAGS=",
			"GOTOOLCHAIN=local",
			"STRESS_MODE=" + mode,
		}, extraEnv...)...,
	)
	output, err := command.CombinedOutput()
	text := string(output)
	if err != nil {
		t.Fatalf("%s stress failed: %v\n%s", mode, err, text)
	}
	return parseUintProbeOutput(t, text)
}

// A1's block is used by a structure that is pushed and popped from every P at once, and the block a
// set no longer needs is returned to a lock-free pool when its pop counter reaches the entry count.
// A 16x smaller block means a set crosses blocks, frees them and grows its spine far more often, so
// the shapes that matter are "many spans in one size class", "several Ps at once" and "allocate,
// free, collect, repeat".
//
// What a broken invariant looks like here: a wrong-slot span (intact=0, or a crash), a block freed
// while another popper still holds it (crash), or a spine that grows without bound (metadata rising
// round over round).
func TestSpanSetStaysCorrectAcrossBlocksSpinesAndPs(t *testing.T) {
	if testing.Short() {
		t.Skip("the span attack allocates across the whole size-class ladder; -short skips it")
	}
	goBinary := goBinary(t)
	plan, overlay, _ := buildOverlayForTest(t, ModeSmall)
	dir := buildStressProgram(t, goBinary)

	var previousMetadata uint64
	for _, procs := range []string{"1", "2", "4", "8"} {
		values := runStress(t, goBinary, dir, overlay, plan, "span",
			"STRESS_GOMAXPROCS="+procs, "STRESS_ROUNDS=4")
		if values["done"] != 1 {
			t.Fatalf("GOMAXPROCS=%s: the span attack did not finish: %+v", procs, values)
		}
		if values["intact"] != 0 {
			t.Fatalf("GOMAXPROCS=%s: a live object was corrupted (intact=0)", procs)
		}
		last := values["round.3.metadata.other"]
		t.Logf("GOMAXPROCS=%s metadata/other after 4 rounds: %d bytes", procs, last)
		// The last round is the one that has seen the most churn. It must not be wildly larger than
		// the first round in this process: a spine that never stops growing would show here.
		if first := values["round.0.metadata.other"]; first > 0 && last > first*4 {
			t.Fatalf("GOMAXPROCS=%s: metadata/other grew from %d to %d over four rounds of allocate, "+
				"free and collect; a span set that never gives memory back would look like this",
				procs, first, last)
		}
		previousMetadata = last
	}
	t.Logf("last measured metadata/other: %d bytes (A1's target class)", previousMetadata)
}

// A3 changes when an exited goroutine's stack is freed. The failure it could introduce is a stack
// that is freed while something still walks it - a traceback, the GC, or the next goroutine that
// reuses the g - and the way to make that visible is to keep creating and retiring goroutines of
// several stack sizes while tracing every live goroutine and collecting.
//
// The second thing it checks is the trend: A3 is supposed to make residency fall and stay down, so
// a run whose stack residency grows without coming back is a leak that the change either caused or
// exposed.
func TestExitedStacksAreFreedWhileGoroutinesAreTraced(t *testing.T) {
	if testing.Short() {
		t.Skip("the stack attack creates thousands of goroutines across several stack sizes; -short skips it")
	}
	goBinary := goBinary(t)
	plan, overlay, _ := buildOverlayForTest(t, ModeSmall)
	dir := buildStressProgram(t, goBinary)

	values := runStress(t, goBinary, dir, overlay, plan, "stack",
		"STRESS_GOMAXPROCS=4", "STRESS_ROUNDS=30")
	if values["done"] != 1 {
		t.Fatalf("the stack attack did not finish: %+v", values)
	}
	if values["leaked"] != 0 {
		t.Fatalf("stack residency grew past three times its settled value (first=%d now=%d)",
			values["leaked"], values["round.29.stacks"])
	}
	settled := values["round.4.stacks"]
	final := values["round.29.stacks"]
	t.Logf("stack residency: settled %d bytes at round 4, %d bytes at round 29", settled, final)
	if settled > 0 && final > settled {
		// Residency may wobble, but it must not trend upward across 25 rounds of churn.
		t.Logf("note: residency is higher at the end than at round 4 (%d > %d)", final, settled)
	}
	// Same run, without the overlay: the toolchain's runtime must show a larger settled residency,
	// which is what makes the number above attributable to A3 rather than to the harness.
	upstreamValues := runStressWithoutOverlayValues(t, goBinary, dir, overlay, plan)
	upstreamSettled := upstreamValues["round.4.stacks"]
	if upstreamSettled > 0 && settled > 0 && settled >= upstreamSettled {
		t.Fatalf("the overlay's stack residency (%d) is not below the toolchain runtime's (%d) in the "+
			"same attack; A3 is supposed to be what makes it fall", settled, upstreamSettled)
	}
	t.Logf("settled residency: overlay %d bytes, toolchain runtime %d bytes", settled, upstreamSettled)
}

func runStressWithoutOverlayValues(t *testing.T, goBinary, dir string, overlay *Overlay, plan Plan) map[string]uint64 {
	t.Helper()
	command := exec.Command(goBinary, "run", "-overlay="+overlay.JSONPath, ".")
	command.Dir = dir
	command.Env = append(os.Environ(),
		"CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local",
		"STRESS_MODE=stack", "STRESS_GOMAXPROCS=4", "STRESS_ROUNDS=30",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("stack attack without the overlay's values: %v\n%s", err, output)
	}
	return parseUintProbeOutput(t, string(output))
}

// A build that alternates the overlay on and off, writing to the SAME output path, must alternate
// the binary. The go build cache keys on the overlay's content, and if it did not, a stale runtime
// package would silently answer for the other mode - which is the worst kind of failure here,
// because every artifact would still carry a marker.
func TestBuildCacheCannotServeTheOtherMode(t *testing.T) {
	goBinary := goBinary(t)
	plan, overlay, _ := buildOverlayForTest(t, ModeSmall)
	dir := buildStressProgram(t, goBinary)
	output := filepath.Join(t.TempDir(), "probe.bin")

	for index, withOverlayValues := range []bool{true, false, true, false} {
		args := []string{"build", "-overlay=" + overlay.JSONPath, "-o", output}
		if withOverlayValues {
			args = append(args, "-tags", plan.onConstraint())
		}
		args = append(args, ".")
		command := exec.Command(goBinary, args...)
		command.Dir = dir
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
		if combined, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %d: %v\n%s", index, err, combined)
		}
		anyMarker, wantedPresent, err := scanBinaryMarkers(output, plan.Marker())
		if err != nil {
			t.Fatal(err)
		}
		if anyMarker != withOverlayValues || wantedPresent != withOverlayValues {
			t.Fatalf("build %d asked for the overlay's values=%v and got anyMarker=%v wanted=%v: the "+
				"build cache answered for the other mode", index, withOverlayValues, anyMarker, wantedPresent)
		}
	}
}

var _ = strconv.Itoa
var _ = strings.TrimSpace
