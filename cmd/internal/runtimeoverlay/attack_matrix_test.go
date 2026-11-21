package runtimeoverlay

// P02 / P03 / P04, Runtime side: the overlay's edits and the linked startup policy are two build
// inputs that land in the SAME runtime, and neither one's own test covers the other.
//
// The probe is not a new one: the test compiles cmd/internal/build_shared/testdata/startupprobe,
// the exact program the GM-2 startup-policy evidence used, so the numbers here and the numbers in
// the startup-policy report cannot drift apart. It is built four ways from ONE overlay and ONE
// module:
//
//	overlay values ON / OFF  x  startup policy inherit / memprofilerate=off,adaptivestackstart=off
//
// and the assertions are about the CROSS terms:
//
//	P03  the overlay must not change the sampling rate or make an enabled profiler empty, and a
//	     disabled profiler must be visibly empty on the same workload rather than "no leaks";
//	P02  group A3 (exit stacks) and adaptivestackstart both change what a REUSED g gets, so the
//	     matrix tells whether the overlay already reclaims what the policy claims, and A3's effect
//	     must survive the policy being off;
//	P04  at GOMAXPROCS=1/2/4 the same churn traffic must finish with every goroutine gone: a
//	     stack-freeing or m-release bug shows up as goroutines that never retire.
//
// Measurements are repeated and reported as min/median/spread; no performance PASS is claimed from
// a single sample, and timing is logged rather than asserted except for the completion invariant.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/cmd/internal/build_shared"
)

const startupProbeSource = "../build_shared/testdata/startupprobe"

type probePhase struct {
	StackBytes    uint64  `json:"stack_bytes"`
	StartingSize  uint64  `json:"starting_size_bytes"`
	Goroutines    uint64  `json:"goroutines"`
	NumGoroutines int     `json:"num_goroutines"`
	Created       uint64  `json:"goroutines_created"`
	CPUSeconds    float64 `json:"cpu_seconds_used"`
	WallMicros    int64   `json:"wall_micros"`
}

type rateProbeReport struct {
	Schema                       string `json:"schema"`
	Mode                         string `json:"mode"`
	GOOS                         string `json:"goos"`
	GOARCH                       string `json:"goarch"`
	GoVersion                    string `json:"go_version"`
	PID                          int    `json:"pid"`
	GOMAXPROCS                   int    `json:"gomaxprocs"`
	MemProfileRate               int64  `json:"mem_profile_rate"`
	HeapProfileSamplesAtMain     int64  `json:"heap_profile_samples_at_main"`
	BucketsAtMain                uint64 `json:"buckets_at_main"`
	BucketsAfterAlloc            uint64 `json:"buckets_after_alloc"`
	HeapProfileSamplesAfterAlloc int64  `json:"heap_profile_samples_after_alloc"`
}

type stackProbeReport struct {
	Schema         string       `json:"schema"`
	Mode           string       `json:"mode"`
	GOOS           string       `json:"goos"`
	GOARCH         string       `json:"goarch"`
	GoVersion      string       `json:"go_version"`
	GOMAXPROCS     int          `json:"gomaxprocs"`
	MemProfileRate int64        `json:"mem_profile_rate"`
	Baseline       probePhase   `json:"baseline"`
	AfterPrimeGC   *probePhase  `json:"after_prime_gc"`
	BeforeWave     probePhase   `json:"before_wave"`
	WaveLive       *probePhase  `json:"wave_live"`
	ChurnReadings  []probePhase `json:"churn_readings"`
	AfterWave      *probePhase  `json:"after_wave"`
	AfterGC        probePhase   `json:"after_gc"`
}

// startupProbeModule copies the shared probe into a module of its own, exactly as the build_shared
// test does, so this test compiles the same source those numbers came from.
func startupProbeModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(startupProbeSource)
	if err != nil {
		t.Fatalf("the shared startup probe is not where this test expects it: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(startupProbeSource, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module startupmatrixprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type matrixCell struct {
	name        string
	overlay     bool
	samplingOff bool
	policy      build_shared.StartupPolicy
	binary      string
}

func (c matrixCell) overlayValues() string {
	if c.overlay {
		return "on"
	}
	return "off"
}

// buildMatrixCells builds the four combinations from one overlay JSON and one probe module.
func buildMatrixCells(t *testing.T, moduleDir string, overlay *Overlay, probeTag string) []matrixCell {
	t.Helper()
	goBinary := goBinary(t)
	inherit := build_shared.StartupPolicyInherit()
	off, err := build_shared.ParseStartupPolicy("memprofilerate=off,adaptivestackstart=off")
	if err != nil {
		t.Fatal(err)
	}
	cells := []matrixCell{
		{name: "overlay=on,sampling=inherit", overlay: true, policy: inherit},
		{name: "overlay=on,sampling=off", overlay: true, samplingOff: true, policy: off},
		{name: "overlay=off,sampling=inherit", overlay: false, policy: inherit},
		{name: "overlay=off,sampling=off", overlay: false, samplingOff: true, policy: off},
	}
	binaryName := "probe"
	if runtime.GOOS == "windows" {
		binaryName = "probe.exe"
	}
	for index := range cells {
		binary := filepath.Join(t.TempDir(), binaryName)
		arguments := []string{
			"build",
			"-trimpath",
			"-buildvcs=false",
			"-overlay=" + overlay.JSONPath,
			"-ldflags", build_shared.LinkerFlagsWithStartupPolicy("v0.0.0-r", false, cells[index].policy),
			"-o", binary,
		}
		if cells[index].overlay {
			arguments = append(arguments, "-tags", probeTag)
		}
		arguments = append(arguments, ".")
		command := exec.Command(goBinary, arguments...)
		command.Dir = moduleDir
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: building the probe: %v\n%s", cells[index].name, err, output)
		}
		cells[index].binary = binary
	}
	return cells
}

// runProbe executes one cell with a deadline and clears the environment variables that would
// override the linked startup default. GODEBUG is the important one: it WINS over the linked value
// at startup, so a developer machine with GODEBUG set would turn the measurement into a measurement
// of the environment.
func runProbe(t *testing.T, cell matrixCell, gomaxprocs int, arguments ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cell.binary, arguments...)
	command.Env = append(os.Environ(),
		"GODEBUG=",
		"GOMAXPROCS=",
		"PROBE_GOMAXPROCS="+fmt.Sprint(gomaxprocs),
	)
	started := time.Now()
	output, err := command.CombinedOutput()
	elapsed := time.Since(started)
	if ctx.Err() != nil {
		t.Fatalf("%s %v: killed after the 180s deadline; this is a hang, not a slow host", cell.name, arguments)
	}
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", cell.name, arguments, err, output)
	}
	t.Logf("METRIC cell=%s args=%q pid=%d wall_ms=%d", cell.name, strings.Join(arguments, " "), command.Process.Pid, elapsed.Milliseconds())
	return output
}

func decodeRate(t *testing.T, cell matrixCell, output []byte) rateProbeReport {
	t.Helper()
	var report rateProbeReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("%s: the probe did not emit one JSON object: %v\n%s", cell.name, err, output)
	}
	if report.Schema != "gm2-startupprobe/1" || report.Mode != "rate" {
		t.Fatalf("%s: unexpected report header: %+v", cell.name, report)
	}
	return report
}

func decodeStack(t *testing.T, cell matrixCell, output []byte) stackProbeReport {
	t.Helper()
	var report stackProbeReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("%s: the probe did not emit one JSON object: %v\n%s", cell.name, err, output)
	}
	if report.Schema != "gm2-startupprobe/1" || report.Mode != "stack" {
		t.Fatalf("%s: unexpected report header: %+v", cell.name, report)
	}
	return report
}

// TestOverlayAndSamplingMatrix is P03. The oracle is the same workload under the same binary
// source, with only the rate changed: an enabled rate must sample and pay the profiler table, a
// disabled rate must produce no samples at all on that same allocation, and the overlay's runtime
// edits must change neither. An empty profile from the disabled cell is then attributable to the
// rate, not to "the process allocated nothing".
func TestOverlayAndSamplingMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and runs four probe binaries; -short skips it")
	}
	plan, overlay, _ := buildOverlayForTest(t, ModeBoth)
	moduleDir := startupProbeModule(t)
	cells := buildMatrixCells(t, moduleDir, overlay, plan.onConstraint())

	reports := map[string]rateProbeReport{}
	for _, cell := range cells {
		reports[cell.name] = decodeRate(t, cell, runProbe(t, cell, 2, "-mode", "rate", "-allocmib", "64"))
	}

	for _, cell := range cells {
		report := reports[cell.name]
		t.Logf("METRIC rate cell=%s mem_profile_rate=%d samples_after_alloc=%d buckets_after_alloc=%d",
			cell.name, report.MemProfileRate, report.HeapProfileSamplesAfterAlloc, report.BucketsAfterAlloc)
		wantRate := int64(build_shared.RuntimeMemProfileRateDefault)
		if cell.samplingOff {
			wantRate = 0
		}
		if report.MemProfileRate != wantRate {
			t.Fatalf("%s: the running process reports MemProfileRate=%d, want %d; the linked policy did not become the startup value",
				cell.name, report.MemProfileRate, wantRate)
		}
		if wantRate == 0 {
			if report.HeapProfileSamplesAfterAlloc != 0 {
				t.Fatalf("%s: sampling is off but %d samples were taken", cell.name, report.HeapProfileSamplesAfterAlloc)
			}
			if report.BucketsAfterAlloc >= 100_000 {
				t.Fatalf("%s: sampling off still allocated the profiler table (%d bytes in profiling/buckets)",
					cell.name, report.BucketsAfterAlloc)
			}
			continue
		}
		if report.HeapProfileSamplesAfterAlloc <= 0 {
			t.Fatalf("%s: sampling is on but 64 MiB produced no samples; the overlay broke the consumer path",
				cell.name)
		}
		if report.BucketsAfterAlloc < 1_400_000 {
			t.Fatalf("%s: sampling is on but the profiler's 1439992-byte table was never allocated (%d bytes)",
				cell.name, report.BucketsAfterAlloc)
		}
	}
	// The pairwise claim: the overlay does not disturb either half of the policy. Comparing the
	// overlay-on and overlay-off cells in the same policy is what makes this about the overlay
	// rather than about the host.
	for _, policyName := range []string{"inherit", "off"} {
		on := reports["overlay=on,sampling="+policyName]
		off := reports["overlay=off,sampling="+policyName]
		if on.MemProfileRate != off.MemProfileRate {
			t.Fatalf("sampling=%s: the overlay changed the sampling rate (%d on, %d off)", policyName, on.MemProfileRate, off.MemProfileRate)
		}
		if (on.HeapProfileSamplesAfterAlloc > 0) != (off.HeapProfileSamplesAfterAlloc > 0) {
			t.Fatalf("sampling=%s: the overlay changed whether the profiler produced samples (%d on, %d off)",
				policyName, on.HeapProfileSamplesAfterAlloc, off.HeapProfileSamplesAfterAlloc)
		}
	}

	// The no-consumer confound, with the overlay ON: the linker sees nothing keeping
	// runtime.memProfileInternal reachable, forces MemProfileRate to 0 AFTER the linked default is
	// parsed, and the process reports 0 samples with -1 from the consumer counter. The artifact
	// still contains the linked string, which is exactly why the string alone is not evidence.
	noprofile := matrixCell{name: "overlay=on,noprofile", overlay: true, policy: build_shared.StartupPolicyInherit()}
	{
		goBinary := goBinary(t)
		binaryName := "probe"
		if runtime.GOOS == "windows" {
			binaryName = "probe.exe"
		}
		binary := filepath.Join(t.TempDir(), binaryName)
		command := exec.Command(goBinary, "build", "-trimpath", "-buildvcs=false",
			"-overlay="+overlay.JSONPath,
			"-ldflags", build_shared.LinkerFlagsWithStartupPolicy("v0.0.0-r", false, noprofile.policy),
			"-tags", plan.onConstraint()+",noprofile",
			"-o", binary, ".")
		command.Dir = moduleDir
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOTOOLCHAIN=local")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("building the no-consumer probe: %v\n%s", err, output)
		}
		noprofile.binary = binary
	}
	noprofileReport := decodeRate(t, noprofile, runProbe(t, noprofile, 2, "-mode", "rate", "-allocmib", "64"))
	t.Logf("METRIC rate cell=%s mem_profile_rate=%d samples_at_main=%d", noprofile.name, noprofileReport.MemProfileRate, noprofileReport.HeapProfileSamplesAtMain)
	if noprofileReport.MemProfileRate != 0 {
		t.Fatalf("the no-consumer build reports MemProfileRate=%d: the linker's disableMemoryProfiling flag did not win",
			noprofileReport.MemProfileRate)
	}
	if noprofileReport.HeapProfileSamplesAtMain != -1 {
		t.Fatalf("the no-consumer build must report -1 samples at main, got %d; 0 would be indistinguishable from a measurement",
			noprofileReport.HeapProfileSamplesAtMain)
	}
}

// TestExitStackAndAdaptiveStartMatrix is P02: A3 (free an exited goroutine's stack) and
// adaptivestackstart both decide what a REUSED g gets, so their savings can overlap. The matrix
// measures both overlay states under both policies and asserts the four mechanism-level claims
// rather than a headline number.
func TestExitStackAndAdaptiveStartMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and runs four probe binaries; -short skips it")
	}
	plan, overlay, _ := buildOverlayForTest(t, ModeBoth)
	moduleDir := startupProbeModule(t)
	cells := buildMatrixCells(t, moduleDir, overlay, plan.onConstraint())

	reports := map[string]stackProbeReport{}
	for _, cell := range cells {
		reports[cell.name] = decodeStack(t, cell, runProbe(t, cell, 2,
			"-mode", "stack", "-prime", "32", "-wave", "64", "-depth", "24"))
	}
	for _, cell := range cells {
		report := reports[cell.name]
		if report.AfterPrimeGC == nil || report.WaveLive == nil || report.AfterWave == nil {
			t.Fatalf("%s: the probe did not report every phase: %+v", cell.name, report)
		}
		t.Logf("METRIC stack cell=%s starting_baseline=%d starting_after_prime=%d stacks_before_wave=%d stacks_wave_live=%d stacks_after_wave=%d stacks_after_gc=%d",
			cell.name, report.Baseline.StartingSize, report.AfterPrimeGC.StartingSize, report.BeforeWave.StackBytes,
			report.WaveLive.StackBytes, report.AfterWave.StackBytes, report.AfterGC.StackBytes)
	}

	for _, policyName := range []string{"inherit", "off"} {
		for _, overlayState := range []string{"on", "off"} {
			cellName := "overlay=" + overlayState + ",sampling=" + policyName
			report := reports[cellName]
			adaptive := policyName == "inherit"
			baseline := report.Baseline.StartingSize
			afterPrime := report.AfterPrimeGC.StartingSize
			if adaptive {
				if afterPrime <= baseline {
					t.Fatalf("%s: adaptivestackstart=1 did not move the starting size after a deep-stack GC (%d -> %d)",
						cellName, baseline, afterPrime)
				}
			} else {
				if afterPrime != baseline {
					t.Fatalf("%s: adaptivestackstart=0 did not freeze the starting size across the training GC (%d -> %d)",
						cellName, baseline, afterPrime)
				}
			}
		}
	}

	// A3's effect has to exist on its own: the overlay's A3 releases exited stacks, so at the same
	// policy the overlay-on artifact must hold fewer stack bytes after the prime phase and after
	// the wave than the overlay-off artifact built from the same source.
	for _, policyName := range []string{"inherit", "off"} {
		on := reports["overlay=on,sampling="+policyName]
		off := reports["overlay=off,sampling="+policyName]
		if on.BeforeWave.StackBytes >= off.BeforeWave.StackBytes {
			t.Fatalf("sampling=%s: A3 did not reclaim exited stacks (%d bytes with the overlay, %d without)",
				policyName, on.BeforeWave.StackBytes, off.BeforeWave.StackBytes)
		}
		if on.AfterGC.StackBytes >= off.AfterGC.StackBytes {
			t.Fatalf("sampling=%s: after a final GC the overlay holds %d stack bytes against %d without it; A3's saving did not settle",
				policyName, on.AfterGC.StackBytes, off.AfterGC.StackBytes)
		}
	}

	// And the adaptive saving must still be visible where it is defined: a reused g allocates
	// startingStackSize at gfget, so the live wave is larger with the default policy than with
	// adaptivestackstart=0 - in BOTH overlay states, because A3 frees stacks without removing the g
	// from the free list.
	for _, overlayState := range []string{"on", "off"} {
		adaptive := reports["overlay="+overlayState+",sampling=inherit"]
		frozen := reports["overlay="+overlayState+",sampling=off"]
		if adaptive.WaveLive.StackBytes <= frozen.WaveLive.StackBytes {
			t.Fatalf("overlay=%s: the live wave is not larger with adaptivestackstart=1 (%d) than with 0 (%d); the setting is not reaching the reused-g path",
				overlayState, adaptive.WaveLive.StackBytes, frozen.WaveLive.StackBytes)
		}
	}

	// The non-additivity question, measured rather than asserted as a headline: how much does the
	// adaptive setting add on top of A3? If A3 already reclaims the exited stacks, the same setting
	// should buy less in the overlay-on artifact than in the overlay-off one.
	for _, phaseName := range []string{"before_wave", "after_gc"} {
		value := func(report stackProbeReport) uint64 {
			if phaseName == "before_wave" {
				return report.BeforeWave.StackBytes
			}
			return report.AfterGC.StackBytes
		}
		adaptiveOnOverlay := value(reports["overlay=on,sampling=inherit"]) - value(reports["overlay=on,sampling=off"])
		adaptiveOffOverlay := value(reports["overlay=off,sampling=inherit"]) - value(reports["overlay=off,sampling=off"])
		t.Logf("METRIC adaptive_saving phase=%s overlay=on=%d overlay=off=%d", phaseName, adaptiveOnOverlay, adaptiveOffOverlay)
	}
}

// TestLowGOMAXPROCSChurnCompletes is the Runtime half of P04: at GOMAXPROCS=1, 2 and 4 the same
// create/run/exit traffic plus explicit GCs must retire every goroutine, on the shipping plan.
// The judging point is starvation/owner-bug shaped: a stack that is freed while still referenced,
// or a goroutine that cannot be scheduled because the runtime is stuck, leaves goroutines behind or
// hangs the process. Timing is logged for the N/G composition work, not asserted.
func TestLowGOMAXPROCSChurnCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and runs a probe binary; -short skips it")
	}
	plan, overlay, _ := buildOverlayForTest(t, ModeBoth)
	moduleDir := startupProbeModule(t)
	cells := buildMatrixCells(t, moduleDir, overlay, plan.onConstraint())
	cell := cells[0]
	if !cell.overlay {
		t.Fatal("the first cell must be the overlay-on one")
	}
	for _, procs := range []int{1, 2, 4} {
		var walls []int64
		for rep := 0; rep < 3; rep++ {
			report := decodeStack(t, cell, runProbe(t, cell, procs,
				"-mode", "stack", "-prime", "0", "-wave", "64", "-churn-waves", "8"))
			if report.GOMAXPROCS != procs {
				t.Fatalf("P=%d: the probe reports GOMAXPROCS=%d", procs, report.GOMAXPROCS)
			}
			if len(report.ChurnReadings) != 8 {
				t.Fatalf("P=%d: %d churn waves reported, want 8", procs, len(report.ChurnReadings))
			}
			previousCreated := report.BeforeWave.Created
			for waveIndex, reading := range report.ChurnReadings {
				// The probe reports wall_micros, but a warm wave can legitimately complete in
				// under one microsecond on this host, so wall time is NOT the progress oracle.
				// The goroutine-created counter is: each wave must have created exactly its 64
				// goroutines and retired all of them.
				if delta := reading.Created - previousCreated; delta != 64 {
					t.Fatalf("P=%d wave %d: %d goroutines created, want 64; the wave did not run to completion",
						procs, waveIndex, delta)
				}
				previousCreated = reading.Created
				if reading.NumGoroutines > 16 {
					t.Fatalf("P=%d wave %d: %d goroutines are still alive after the wave exited; a goroutine could not retire",
						procs, waveIndex, reading.NumGoroutines)
				}
			}
			if report.AfterGC.NumGoroutines > 16 {
				t.Fatalf("P=%d: %d goroutines survived the final GC", procs, report.AfterGC.NumGoroutines)
			}
			walls = append(walls, report.AfterGC.WallMicros)
		}
		minimum, maximum := walls[0], walls[0]
		var total int64
		for _, wall := range walls {
			total += wall
			if wall < minimum {
				minimum = wall
			}
			if wall > maximum {
				maximum = wall
			}
		}
		t.Logf("METRIC churn P=%d final_gc_us=%v min_us=%d max_us=%d mean_us=%d spread_pct=%.1f",
			procs, walls, minimum, maximum, total/int64(len(walls)),
			100*float64(maximum-minimum)/float64(maximum))
	}

	// The repeated-traffic half of A3: 32 create/run/exit waves, and the stack bytes must settle
	// lower with the overlay than without it. A one-wave test can pass by luck of the GC's timing;
	// a steady state cannot.
	settled := map[string]uint64{}
	for _, candidate := range cells {
		if candidate.policy.String() != "memprofilerate=inherit,adaptivestackstart=inherit" {
			continue
		}
		report := decodeStack(t, candidate, runProbe(t, candidate, 2,
			"-mode", "stack", "-prime", "0", "-wave", "64", "-churn-waves", "32"))
		if len(report.ChurnReadings) != 32 {
			t.Fatalf("%s: %d churn waves reported, want 32", candidate.name, len(report.ChurnReadings))
		}
		settled[candidate.name] = report.ChurnReadings[len(report.ChurnReadings)-1].StackBytes
	}
	on := settled["overlay=on,sampling=inherit"]
	off := settled["overlay=off,sampling=inherit"]
	t.Logf("METRIC churn_settled overlay=on=%d overlay=off=%d", on, off)
	if on >= off {
		t.Fatalf("after 32 create/exit waves the overlay holds %d stack bytes against %d without it; A3's steady state did not settle below upstream",
			on, off)
	}
}
