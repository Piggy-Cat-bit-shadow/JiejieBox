package runtimeoverlay

import (
	"strings"
	"testing"
)

// Two values in Params compile cleanly and then kill the process on a target this host cannot run.
// They were both reachable from the parameter space during the measurement round, so the refusal is
// tested as a refusal and not as a comment.
func TestParamsRejectValuesThatCompileAndThenThrow(t *testing.T) {
	cases := []struct {
		name   string
		params Params
		want   string
	}{
		{
			// Measured as the best stack number on an amd64 host with 4 KiB pages. On Apple arm64
			// the page size is 16 KiB and stackinit throws before main.
			name:   "8 KiB stack cache",
			params: Params{SpanSetBlockEntries: 512, SpanSetInitSpineCap: 256, StackCacheSize: 8 * 1024},
			want:   "multiple of 16384",
		},
		{
			// A multiple of the page size but not a power of two: stackpoolalloc's
			// `i += elemsize` loop would put the last stack past the end of its span.
			name:   "48 KiB stack cache",
			params: Params{SpanSetBlockEntries: 512, SpanSetInitSpineCap: 256, StackCacheSize: 48 * 1024},
			want:   "power of two",
		},
		{
			name:   "zero stack cache",
			params: Params{SpanSetBlockEntries: 512, SpanSetInitSpineCap: 256},
			want:   "StackCacheSize",
		},
		{
			name:   "span set block of 100",
			params: Params{SpanSetBlockEntries: 100, SpanSetInitSpineCap: 16, StackCacheSize: 16 * 1024},
			want:   "power of two",
		},
		{
			name:   "span set block of 1",
			params: Params{SpanSetBlockEntries: 1, SpanSetInitSpineCap: 16, StackCacheSize: 16 * 1024},
			want:   "between 2 and 4096",
		},
		{
			name:   "spine capacity of zero",
			params: Params{SpanSetBlockEntries: 32, StackCacheSize: 16 * 1024},
			want:   "SpanSetInitSpineCap",
		},
		{
			name:   "negative backlog",
			params: Params{SpanSetBlockEntries: 32, SpanSetInitSpineCap: 16, StackCacheSize: 16 * 1024, FreeExitedStackBacklog: -1},
			want:   "negative backlog",
		},
	}
	for _, testCase := range cases {
		err := testCase.params.Validate()
		if err == nil {
			t.Fatalf("%s: accepted; it would compile and then fail on the device", testCase.name)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%s: the refusal does not explain itself (%q): %v", testCase.name, testCase.want, err)
		}
	}
}

// Every mode the builder can produce must pass the same validation, or the product build would be
// the one case the check does not cover.
func TestEveryProductModePassesValidation(t *testing.T) {
	for _, mode := range []Mode{ModeBase, ModeSmall, ModeCGO, ModeBoth} {
		plan, err := Resolve(mode, "ios/arm64,macos/arm64")
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if err := plan.Params.Validate(); err != nil {
			t.Fatalf("%s: the shipped parameters do not validate: %v", mode, err)
		}
	}
}

// Generate is a second entry point into the same values (a caller can construct a Plan), so the
// refusal has to hold there too rather than only in Resolve.
func TestGenerateRefusesAPlanWhoseStackCacheWouldThrow(t *testing.T) {
	plan, err := Resolve(ModeSmall, "ios/arm64")
	if err != nil {
		t.Fatal(err)
	}
	plan.Params.StackCacheSize = 8 * 1024
	_, err = Generate(GenerationRequest{Root: repoRoot(t), Dir: t.TempDir(), Plan: plan, Toolchain: Toolchain{GOROOT: "/nonexistent", GOVersion: "go1.99"}})
	if err == nil {
		t.Fatal("an 8 KiB cache compiles and crashes every iOS launch; Generate must refuse it before it looks at the toolchain")
	}
	if !strings.Contains(err.Error(), "16384") {
		t.Fatalf("the refusal must name the page size: %v", err)
	}
}
