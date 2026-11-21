package runtimeoverlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The probes are the evidence for two shipped claims - "the runtime classes this overlay targets
// actually move" and "group B releases the m of a thread that stops calling in" - so they have to be
// race-free before any number they print means anything.
//
// They were not. `probe` wrote one plain byte from every burst goroutine and `bprobe` appended to
// one slice from every pthread, which `go run -race` reports as 2 and 6 data races. The numbers were
// reproducible and looked stable, which is exactly why a race in a probe is dangerous: nothing in the
// measurement tells you it is measuring a data race's worth of scheduling.
//
// This runs both of them under the detector, so the next edit to either probe fails here rather than
// in a reviewer's shell.
func TestTheProbesAreRaceFree(t *testing.T) {
	if testing.Short() {
		t.Skip("building two programs with the race detector is not a -short test")
	}
	goBinary := goBinary(t)
	// -race requires cgo. A host without a C toolchain cannot answer this question, and saying
	// "skipped" is the honest answer rather than "passed".
	cgoEnabled, err := exec.Command(goBinary, "env", "CGO_ENABLED").Output()
	if err != nil {
		t.Fatalf("read CGO_ENABLED: %v", err)
	}
	if strings.TrimSpace(string(cgoEnabled)) != "1" {
		t.Skip("the race detector needs a cgo toolchain, and this host reports CGO_ENABLED=" + strings.TrimSpace(string(cgoEnabled)))
	}

	for _, program := range []struct {
		name       string
		source     string
		env        []string
		wantOutput []string
	}{
		{
			name:       "probe",
			source:     filepath.Join("testdata", "probe", "main.go"),
			wantOutput: []string{"/memory/classes/heap/stacks:bytes=", "goroutines="},
		},
		{
			name:       "bprobe",
			source:     filepath.Join("testdata", "bprobe", "main.go"),
			env:        []string{"BPROBE_THREADS=8", "BPROBE_MIXED=1"},
			wantOutput: []string{"parked.errors=0", "mixed.errors=0", "done=1"},
		},
	} {
		t.Run(program.name, func(t *testing.T) {
			dir := t.TempDir()
			source, err := os.ReadFile(program.source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), source, 0o644); err != nil {
				t.Fatal(err)
			}
			module := "module raceprobe\n\ngo 1.26\n"
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0o644); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(goBinary, "run", "-race", ".")
			command.Dir = dir
			command.Env = append(os.Environ(), append([]string{"CGO_ENABLED=1", "GOFLAGS=", "GOTOOLCHAIN=local"}, program.env...)...)
			output, err := command.CombinedOutput()
			text := string(output)
			if err != nil {
				t.Fatalf("the %s probe failed under the race detector: %v\n%s", program.name, err, text)
			}
			if strings.Contains(text, "WARNING: DATA RACE") || strings.Contains(text, "race detected during execution") {
				t.Fatalf("the %s probe has a data race:\n%s", program.name, text)
			}
			for _, want := range program.wantOutput {
				if !strings.Contains(text, want) {
					t.Fatalf("the %s probe did not report %q, so it did not get far enough to have been "+
						"checked:\n%s", program.name, want, text)
				}
			}
		})
	}
}
