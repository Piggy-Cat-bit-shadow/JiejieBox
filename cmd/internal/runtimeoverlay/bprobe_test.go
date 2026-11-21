package runtimeoverlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Group B cannot be measured with -short or on a platform without pthreads: the whole effect is
// that a NON-Go thread's m is or is not kept after the call, and a Go test cannot be a foreign
// thread. On Windows `_cgo_pthread_key_created` is never created, so upstream already drops the m
// on every call and the two builds would be identical by construction - which is why this test
// refuses to draw a conclusion there instead of reporting a vacuous pass.
func TestGroupBReleasesTheExtraMOfAThreadThatStopsCallingGo(t *testing.T) {
	if testing.Short() {
		t.Skip("the group B probe builds and runs a cgo program; -short skips it")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("group B's measurement needs a pthread platform; on this one upstream already drops the m")
	}
	goBinary := goBinary(t)
	plan, overlay, _ := buildOverlayForTest(t, ModeCGO)

	probeDir := filepath.Join(t.TempDir(), "bprobe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("testdata", "bprobe", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	module := "module runtimeoverlaybprobe\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(probeDir, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	const threads = 48
	run := func(overlayValues bool) map[string]uint64 {
		args := []string{"run", "-overlay=" + overlay.JSONPath}
		if overlayValues {
			args = append(args, "-tags", plan.onConstraint())
		}
		args = append(args, ".")
		command := exec.Command(goBinary, args...)
		command.Dir = probeDir
		command.Env = append(os.Environ(),
			"CGO_ENABLED=1",
			"GOFLAGS=",
			"GOTOOLCHAIN=local",
			"BPROBE_THREADS="+itoa(threads),
			"BPROBE_GOMAXPROCS=2",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("group B probe with the overlay's values=%v: %v\n%s", overlayValues, err, output)
		}
		return parseUintProbeOutput(t, string(output))
	}

	upstream := run(false)
	overlaid := run(true)

	// Correctness first, on both builds: a wrong answer or a crash means the m lifecycle is broken
	// and no memory number matters.
	for name, values := range map[string]map[string]uint64{"upstream": upstream, "overlaid": overlaid} {
		if values["parked.errors"] != 0 || values["after.errors"] != 0 || values["churn.errors"] != 0 {
			t.Fatalf("%s: the callbacks disagreed with the C threads (parked=%d after=%d churn=%d); "+
				"a released m must never change what a call returns",
				name, values["parked.errors"], values["after.errors"], values["churn.errors"])
		}
	}

	upstreamGrowth := int64(upstream["parked.stacks"]) - int64(upstream["baseline.stacks"])
	overlaidGrowth := int64(overlaid["parked.stacks"]) - int64(overlaid["baseline.stacks"])
	t.Logf("stack growth while %d threads sat parked after one call into Go:", threads)
	t.Logf("  upstream runtime: %+d bytes", upstreamGrowth)
	t.Logf("  overlaid runtime: %+d bytes", overlaidGrowth)
	t.Logf("heap growth while parked: upstream %+d, overlaid %+d",
		int64(upstream["parked.heap"])-int64(upstream["baseline.heap"]),
		int64(overlaid["parked.heap"])-int64(overlaid["baseline.heap"]))
	t.Logf("stack residency after the threads exited: upstream %d, overlaid %d",
		upstream["after.stacks"], overlaid["after.stacks"])
	t.Logf("callback cost under 8 concurrent callers: upstream %d ns/call, overlaid %d ns/call",
		upstream["churn.nsPerCall"], overlaid["churn.nsPerCall"])

	// The claim: with upstream's binding, each parked thread that has called into Go holds an m
	// with its g0 and signal stacks, so residency grows with the NUMBER OF THREADS. With group B,
	// the m went back to the extra list after the call, so residency is bounded by the calls that
	// were in flight at once - one, here.
	if overlaidGrowth >= upstreamGrowth/2 {
		t.Fatalf("group B did not release the parked threads' ms: upstream grew %d bytes of stack, overlaid %d. "+
			"Expected the overlaid build to grow far less, because no m stays bound to a thread that has "+
			"already returned to C", upstreamGrowth, overlaidGrowth)
	}
	if upstreamGrowth < int64(threads)*8192 {
		t.Fatalf("the control case did not reproduce upstream's per-thread cost: %d threads grew only %d bytes "+
			"of stack, which is less than one g0 per thread. The probe is not exercising the path this test "+
			"claims to measure", threads, upstreamGrowth)
	}
}

// parseUintProbeOutput reads key=value lines of unsigned integers.
func parseUintProbeOutput(t *testing.T, output string) map[string]uint64 {
	t.Helper()
	values := make(map[string]uint64)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		var parsed uint64
		for _, char := range value {
			if char < '0' || char > '9' {
				parsed = 0
				break
			}
			parsed = parsed*10 + uint64(char-'0')
		}
		values[key] = parsed
	}
	return values
}
