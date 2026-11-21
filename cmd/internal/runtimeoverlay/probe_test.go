package runtimeoverlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The probe is the behavioural half of the proof. A compiled overlay with a marker proves the
// edits reached the compiler; it does not prove they changed the runtime, and the four values
// could plausibly be wrong for this toolchain in a way that compiles.
//
// It runs ONE overlay twice: the same JSON, once with the probe tag (the overlay's values) and
// once without it (the toolchain's values). The two binaries are otherwise identical, so every
// difference is attributable to the overlay and not to a second source tree.
func TestOverlayMovesTheRuntimeClassesItTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("the probe compiles two binaries with a patched runtime; -short skips it")
	}
	goBinary := goBinary(t)
	plan, overlay, _ := buildOverlayForTest(t, ModeBoth)

	probeDir := filepath.Join(t.TempDir(), "probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("testdata", "probe", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	module := "module runtimeoverlayprobe\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(probeDir, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(overlayValues bool) map[string]int64 {
		args := []string{"run", "-overlay=" + overlay.JSONPath}
		if overlayValues {
			args = append(args, "-tags", plan.onConstraint())
		}
		args = append(args, ".")
		// Two processes per binary, minimum across them. The probe already takes the best of
		// several rounds inside one process; the remaining noise is between-process (GC timing,
		// processor placement), and a single sample of the same binary was measured 1.5x apart.
		best := map[string]int64{}
		for round := 0; round < 2; round++ {
			command := exec.Command(goBinary, args...)
			command.Dir = probeDir
			command.Env = append(os.Environ(),
				"CGO_ENABLED=0",
				"GOFLAGS=",
				"GOTOOLCHAIN=local",
				"PROBE_GOMAXPROCS=4",
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("probe with the overlay's values=%v: %v\n%s", overlayValues, err, output)
			}
			for key, value := range parseProbeOutput(t, string(output)) {
				if current, seen := best[key]; !seen || value < current {
					best[key] = value
				}
			}
		}
		return best
	}

	upstream := run(false)
	overlaid := run(true)

	for _, key := range []string{
		"/memory/classes/heap/stacks:bytes",
		"/memory/classes/metadata/other:bytes",
	} {
		if _, ok := upstream[key]; !ok {
			t.Fatalf("the probe did not report %s", key)
		}
	}
	t.Logf("stack residency   : %d bytes upstream -> %d bytes overlaid", upstream["/memory/classes/heap/stacks:bytes"], overlaid["/memory/classes/heap/stacks:bytes"])
	t.Logf("span-set metadata : %d bytes upstream -> %d bytes overlaid", upstream["/memory/classes/metadata/other:bytes"], overlaid["/memory/classes/metadata/other:bytes"])
	t.Logf("mspan metadata    : %d bytes upstream -> %d bytes overlaid", upstream["/memory/classes/metadata/mspan/inuse:bytes"], overlaid["/memory/classes/metadata/mspan/inuse:bytes"])
	t.Logf("heap in use       : %d bytes upstream -> %d bytes overlaid", upstream["/memory/classes/heap/inuse:bytes"], overlaid["/memory/classes/heap/inuse:bytes"])
	t.Logf("goroutine churn   : %d ns/goroutine upstream -> %d ns/goroutine overlaid", upstream["churnPerGoroutineNs"], overlaid["churnPerGoroutineNs"])
	t.Logf("goroutines        : %d upstream -> %d overlaid", upstream["goroutines"], overlaid["goroutines"])

	stacksUpstream := upstream["/memory/classes/heap/stacks:bytes"]
	stacksOverlaid := overlaid["/memory/classes/heap/stacks:bytes"]
	if stacksOverlaid >= stacksUpstream {
		t.Errorf("stack residency did not fall: %d -> %d bytes. A3 releases an exited goroutine's starting stack and A2 "+
			"halves the per-P cache block, so a burst-then-idle probe must end with less stack resident",
			stacksUpstream, stacksOverlaid)
	}

	metadataUpstream := upstream["/memory/classes/metadata/other:bytes"]
	metadataOverlaid := overlaid["/memory/classes/metadata/other:bytes"]
	if metadataOverlaid*10 > metadataUpstream*7 {
		t.Errorf("span-set metadata is %d bytes overlaid against %d upstream, more than the 70%% this change is supposed "+
			"to leave. /memory/classes/metadata/other is inWorkBufs plus gcMiscSys, and the span-set blocks are "+
			"persistentalloc'd into gcMiscSys, so this is the class the block size moves",
			metadataOverlaid, metadataUpstream)
	}

	// The cost side: releasing stacks means allocating them again, so the churn rate is where a
	// regression would show. Measured on this host the shipped plan costs roughly +210..+400 ns
	// per goroutine create-and-exit - a real and reported price, not a free lunch.
	//
	// The gate is an ABSOLUTE budget, not a ratio, and that is deliberate. The unmodified runtime
	// measures 190..330 ns per goroutine between runs of the SAME binary, so a ratio threshold
	// close to the measured 2-3x would fail on noise; the quantity a regression would move is the
	// ADDED cost, which is bounded here at 3 us - an order of magnitude above the accepted price
	// and far below anything that matters at a tunnel's connection rate.
	delta := int64(overlaid["churnPerGoroutineNs"]) - int64(upstream["churnPerGoroutineNs"])
	ratio := 0.0
	if upstream["churnPerGoroutineNs"] > 0 {
		ratio = float64(overlaid["churnPerGoroutineNs"]) / float64(upstream["churnPerGoroutineNs"])
	}
	// Logged as a ratio too, because "2.4x" is what the evidence table reports and a reader
	// comparing the two should see the same shape.
	t.Logf("goroutine cost as a ratio: %.2fx (%.2f us of the %.2f us budget)", ratio, float64(delta)/1000, 3.0)
	if delta > 3000 {
		t.Errorf("goroutine creation and exit costs %d ns more per goroutine with the overlay (%d vs %d ns, %.2fx); "+
			"the accepted price of A3 is a few hundred nanoseconds, so this is a product regression even if residency fell",
			delta, overlaid["churnPerGoroutineNs"], upstream["churnPerGoroutineNs"], ratio)
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		// The shipped slice's own architecture. Nothing here asserts phys_footprint: that needs a
		// Network Extension on a device.
		t.Log("the probe ran on the shipped slice's architecture; device phys_footprint is still unmeasured")
	}
}

func parseProbeOutput(t *testing.T, output string) map[string]int64 {
	t.Helper()
	values := make(map[string]int64)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		values[key] = parsed
	}
	return values
}
