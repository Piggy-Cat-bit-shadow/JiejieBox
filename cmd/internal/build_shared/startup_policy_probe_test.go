package build_shared

// The integration half of the startup-policy evidence: build a real child binary with the REAL
// composed linker flags, then read the value the new process actually runs with. The unit tests in
// startup_policy_test.go can prove what the flag says; only a fresh process can prove what the
// runtime did with it, because:
//
//   - runtime.MemProfileRate is read by the first sampled allocation, and reparsedebugvars later
//     ignores memprofilerate entirely, so no in-process setter can reproduce the startup value;
//   - cmd/link sets runtime.disableMemoryProfiling when nothing keeps runtime.memProfileInternal
//     reachable, and proc.go then forces MemProfileRate to 0 AFTER parsing the linked default. A
//     build whose artifact contains "memprofilerate=524288" can still run at 0. That is why the
//     no-consumer case below is part of the test: it is the confound, not an accident.
//
// The child program is testdata/startupprobe, the same source the GM-2 evidence run uses, so the
// numbers in the report and the assertions here cannot drift apart.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const startupProbeSchema = "gm2-startupprobe/1"

type startupProbePhase struct {
	StackBytes    uint64 `json:"stack_bytes"`
	StartingSize  uint64 `json:"starting_size_bytes"`
	Goroutines    uint64 `json:"goroutines"`
	NumGoroutines int    `json:"num_goroutines"`
}

type startupProbeRateReport struct {
	Schema                       string `json:"schema"`
	MemProfileRate               int64  `json:"mem_profile_rate"`
	HeapProfileSamplesAtMain     int64  `json:"heap_profile_samples_at_main"`
	BucketsAtMain                uint64 `json:"buckets_at_main"`
	BucketsAfterAlloc            uint64 `json:"buckets_after_alloc"`
	HeapProfileSamplesAfterAlloc int64  `json:"heap_profile_samples_after_alloc"`
}

type startupProbeStackReport struct {
	Schema         string             `json:"schema"`
	MemProfileRate int64              `json:"mem_profile_rate"`
	Baseline       startupProbePhase  `json:"baseline"`
	AfterPrimeGC   *startupProbePhase `json:"after_prime_gc"`
	BeforeWave     startupProbePhase  `json:"before_wave"`
	WaveLive       *startupProbePhase `json:"wave_live"`
}

func startupProbeGo(t *testing.T) string {
	t.Helper()
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	binary := filepath.Join(runtime.GOROOT(), "bin", name)
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() {
		t.Skipf("the go toolchain that built this test is not at %s; the subprocess half cannot run", binary)
	}
	return binary
}

// buildStartupProbe compiles testdata/startupprobe in a module of its own with the composed link
// flags, and returns the binary path and its bytes.
func buildStartupProbe(t *testing.T, policy StartupPolicy, tags string) (string, []byte) {
	t.Helper()
	goBinary := startupProbeGo(t)

	probeDir := t.TempDir()
	entries, err := os.ReadDir(filepath.Join("testdata", "startupprobe"))
	require.NoError(t, err, "the probe source must ship with this test")
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join("testdata", "startupprobe", entry.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(probeDir, entry.Name()), content, 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(probeDir, "go.mod"), []byte("module startupprobe\n\ngo 1.26\n"), 0o644))

	binaryName := "probe"
	if runtime.GOOS == "windows" {
		binaryName = "probe.exe"
	}
	binary := filepath.Join(probeDir, binaryName)
	arguments := []string{
		"build",
		"-o", binary,
		"-trimpath",
		"-buildvcs=false",
		"-ldflags", LinkerFlagsWithStartupPolicy("v0.0.0-gm2", false, policy),
	}
	if tags != "" {
		arguments = append(arguments, "-tags", tags)
	}
	arguments = append(arguments, ".")
	command := exec.Command(goBinary, arguments...)
	command.Dir = probeDir
	command.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "build with policy %q and tags %q failed:\n%s", policy, tags, output)

	artifact, err := os.ReadFile(binary)
	require.NoError(t, err)
	return binary, artifact
}

// runStartupProbe runs one fresh process with the environment that must NOT override the linked
// default. GODEBUG is cleared explicitly: it wins over the linked default at startup, and a
// developer machine or CI image with GODEBUG set would otherwise turn this test into a measurement
// of the environment.
func runStartupProbe(t *testing.T, binary string, arguments ...string) []byte {
	return runStartupProbeWithEnv(t, binary, nil, arguments...)
}

func runStartupProbeWithEnv(t *testing.T, binary string, extraEnv []string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Env = append(os.Environ(),
		"GODEBUG=",
		"PROBE_GOMAXPROCS=1",
		"GOMAXPROCS=",
	)
	command.Env = append(command.Env, extraEnv...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "probe %q failed:\n%s", strings.Join(arguments, " "), output)
	return output
}

func decodeRateReport(t *testing.T, output []byte) startupProbeRateReport {
	t.Helper()
	var report startupProbeRateReport
	require.NoError(t, json.Unmarshal(output, &report), "probe output was not one JSON object: %s", output)
	require.Equal(t, startupProbeSchema, report.Schema)
	return report
}

func decodeStackReport(t *testing.T, output []byte) startupProbeStackReport {
	t.Helper()
	var report startupProbeStackReport
	require.NoError(t, json.Unmarshal(output, &report), "probe output was not one JSON object: %s", output)
	require.Equal(t, startupProbeSchema, report.Schema)
	return report
}

// TestLinkedMemProfileRateIsTheRuntimeValue is the three-layer chain for memprofilerate:
// the composed link string, its presence in the artifact bytes, and the value the process reads.
func TestLinkedMemProfileRateIsTheRuntimeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and runs child processes; -short skips it")
	}
	inherit := StartupPolicyInherit()
	off, err := ParseStartupPolicy("memprofilerate=off")
	require.NoError(t, err)
	coarse, err := ParseStartupPolicy("memprofilerate=4194304")
	require.NoError(t, err)

	// Layer 1+2: inherit is the shipped default, and the exact full string is in the artifact.
	inheritBinary, inheritArtifact := buildStartupProbe(t, inherit, "")
	require.True(t, inherit.LinkedGODEBUGDefaultsArePresent(inheritArtifact),
		"the composed -X value %q must be present in the built binary's bytes", inherit.GODEBUGDefaults())

	// Layer 3a: at process entry, before this program's own allocation, runtime's default rate is
	// what the process runs with.
	atMain := decodeRateReport(t, runStartupProbe(t, inheritBinary, "-mode", "rate", "-allocmib", "0"))
	require.Equal(t, int64(RuntimeMemProfileRateDefault), atMain.MemProfileRate,
		"an inherit policy must leave runtime's 512 KiB default in place")

	// Layer 3b: 64 MiB across four distinct call sites allocates the 179999-entry profiler hash
	// table (179999*8 = 1439992 bytes) and produces samples a diagnostic consumer can read.
	inheritAfter := decodeRateReport(t, runStartupProbe(t, inheritBinary, "-mode", "rate", "-allocmib", "64"))
	require.Equal(t, int64(RuntimeMemProfileRateDefault), inheritAfter.MemProfileRate)
	require.Greater(t, inheritAfter.HeapProfileSamplesAfterAlloc, int64(0),
		"a heap-profile consumer must see samples when the rate is 512 KiB")
	require.GreaterOrEqual(t, inheritAfter.BucketsAfterAlloc, uint64(1_400_000),
		"after 64 MiB the profiler hash table (1439992 bytes) must be allocated; got %d", inheritAfter.BucketsAfterAlloc)

	// Layer 3c: the table can already be there at process entry. A libbox allocates MiB during
	// package initialization, and the first sample allocates the table. 16 MiB of init-time
	// allocation makes that deterministic instead of a Poisson coin flip.
	inheritWarm := decodeRateReport(t, runStartupProbeWithEnv(t, inheritBinary,
		[]string{"PROBE_WARMUP_MIB=16"}, "-mode", "rate", "-allocmib", "0"))
	require.GreaterOrEqual(t, inheritWarm.BucketsAtMain, uint64(1_400_000),
		"a process that allocated 16 MiB before main must already carry the profiler hash table; %d says it did not", inheritWarm.BucketsAtMain)

	// The candidate: off. Both the artifact and the process must say 0, and 64 MiB must not grow
	// the profiling bucket class at all.
	offBinary, offArtifact := buildStartupProbe(t, off, "")
	require.True(t, off.LinkedGODEBUGDefaultsArePresent(offArtifact))
	offAtMain := decodeRateReport(t, runStartupProbe(t, offBinary, "-mode", "rate", "-allocmib", "0"))
	require.Equal(t, int64(0), offAtMain.MemProfileRate)
	offAfter := decodeRateReport(t, runStartupProbe(t, offBinary, "-mode", "rate", "-allocmib", "64"))
	require.Equal(t, int64(0), offAfter.MemProfileRate)
	require.Equal(t, int64(0), offAfter.HeapProfileSamplesAfterAlloc,
		"with sampling off the heap profile has no historical samples: this is the diagnostic cost")
	require.GreaterOrEqual(t, offAfter.BucketsAfterAlloc, offAfter.BucketsAtMain)
	require.LessOrEqual(t, offAfter.BucketsAfterAlloc-offAfter.BucketsAtMain, uint64(64*1024),
		"sampling off must not allocate 1.37 MiB of profiler buckets during allocation")
	require.Less(t, offAfter.BucketsAfterAlloc, uint64(100_000),
		"sampling off leaves only the pointer-mask baseline (a few KiB) in /memory/classes/profiling/buckets:bytes")
	require.Greater(t, inheritAfter.BucketsAfterAlloc, offAfter.BucketsAfterAlloc+1_000_000,
		"the linked rate, not the workload, must explain the ~1.37 MiB bucket difference")

	// The same 16 MiB init-time allocation under the off policy must NOT leave the table behind.
	offWarm := decodeRateReport(t, runStartupProbeWithEnv(t, offBinary,
		[]string{"PROBE_WARMUP_MIB=16"}, "-mode", "rate", "-allocmib", "0"))
	require.Less(t, offWarm.BucketsAtMain, uint64(100_000),
		"with sampling off even a 16 MiB pre-main allocation must not allocate the profiler hash table")

	// A coarse rate is a real third option: it still pays the one-time table, but samples ~8x less
	// often than the default.
	coarseBinary, coarseArtifact := buildStartupProbe(t, coarse, "")
	require.True(t, coarse.LinkedGODEBUGDefaultsArePresent(coarseArtifact))
	coarseAfter := decodeRateReport(t, runStartupProbe(t, coarseBinary, "-mode", "rate", "-allocmib", "64"))
	require.Equal(t, int64(4*1024*1024), coarseAfter.MemProfileRate)
	require.Greater(t, coarseAfter.HeapProfileSamplesAfterAlloc, int64(0),
		"a coarse rate must keep the consumer working, just with fewer samples")
	require.GreaterOrEqual(t, coarseAfter.BucketsAfterAlloc, uint64(1_400_000),
		"the first sample allocates the whole table whatever the rate is")
}

// TestLinkerDisabledProfilingOverridesTheLinkedDefault is the confound as a test. Without a heap
// profile consumer the linker sets runtime.disableMemoryProfiling, proc.go forces
// MemProfileRate to 0 after the default is parsed, and the artifact still contains the string.
// Both halves are asserted: presence in bytes AND 0 in the running process.
func TestLinkerDisabledProfilingOverridesTheLinkedDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and runs child processes; -short skips it")
	}
	inherit := StartupPolicyInherit()
	binary, artifact := buildStartupProbe(t, inherit, "noprofile")
	require.True(t, inherit.LinkedGODEBUGDefaultsArePresent(artifact),
		"the linked default is in the bytes even when the linker disables profiling")
	report := decodeRateReport(t, runStartupProbe(t, binary, "-mode", "rate", "-allocmib", "64"))
	require.Equal(t, int64(0), report.MemProfileRate,
		"with no reachable heap-profile consumer the linker forces MemProfileRate to 0, which is why the policy test above imports one")
	require.Equal(t, int64(-1), report.HeapProfileSamplesAtMain,
		"the no-consumer build must report -1, not 0 samples")
}

// TestLinkedAdaptiveStackStartIsTheRuntimeValue separates the two goroutine paths:
//
//	cold g  - newproc1 calls malg(stackMin) whatever the setting is, so the two policies must agree;
//	reused g - gfget calls stackalloc(startingStackSize), which is exactly what the setting changes.
//
// The assertion on AfterPrimeGC is the mechanism itself: with adaptivestackstart=0 the GC must NOT
// move the starting size, and without it a GC that scanned deep stacks must move it.
func TestLinkedAdaptiveStackStartIsTheRuntimeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("this test builds and runs child processes; -short skips it")
	}
	inherit := StartupPolicyInherit()
	off, err := ParseStartupPolicy("adaptivestackstart=off")
	require.NoError(t, err)

	inheritBinary, _ := buildStartupProbe(t, inherit, "")
	offBinary, offArtifact := buildStartupProbe(t, off, "")
	require.True(t, off.LinkedGODEBUGDefaultsArePresent(offArtifact))
	require.Contains(t, string(offArtifact), "adaptivestackstart=0")

	coldInherit := decodeStackReport(t, runStartupProbe(t, inheritBinary, "-mode", "stack", "-prime", "0", "-wave", "64"))
	coldOff := decodeStackReport(t, runStartupProbe(t, offBinary, "-mode", "stack", "-prime", "0", "-wave", "64"))
	require.NotNil(t, coldInherit.WaveLive)
	require.NotNil(t, coldOff.WaveLive)
	require.InDelta(t, float64(coldInherit.WaveLive.StackBytes), float64(coldOff.WaveLive.StackBytes), 64*1024,
		"cold goroutines get malg(stackMin) under both policies; the residual is stack-cache block quantization (up to one 32 KiB block), not this setting")

	primedInherit := decodeStackReport(t, runStartupProbe(t, inheritBinary, "-mode", "stack", "-prime", "32", "-wave", "64"))
	primedOff := decodeStackReport(t, runStartupProbe(t, offBinary, "-mode", "stack", "-prime", "32", "-wave", "64"))
	require.NotNil(t, primedInherit.AfterPrimeGC)
	require.NotNil(t, primedOff.AfterPrimeGC)
	require.NotNil(t, primedInherit.WaveLive)
	require.NotNil(t, primedOff.WaveLive)
	require.Equal(t, primedOff.Baseline.StartingSize, primedOff.AfterPrimeGC.StartingSize,
		"adaptivestackstart=0 must freeze the starting size across the training GC")
	require.Greater(t, primedInherit.AfterPrimeGC.StartingSize, primedInherit.Baseline.StartingSize,
		"with the default, a GC that scanned deep stacks must move the reused-g starting size")
	require.Greater(t, primedInherit.AfterPrimeGC.StartingSize, primedOff.AfterPrimeGC.StartingSize)
	require.Greater(t, primedInherit.WaveLive.StackBytes, primedOff.WaveLive.StackBytes,
		"reused goroutines allocate the (larger) starting size under the default policy; this is the memory cost of leaving adaptive stack start on")
	require.Equal(t, primedInherit.MemProfileRate, int64(RuntimeMemProfileRateDefault),
		"the adaptive-stack policy must not disturb the sampling rate the other setting controls")
	require.Equal(t, primedOff.MemProfileRate, int64(RuntimeMemProfileRateDefault))
}
