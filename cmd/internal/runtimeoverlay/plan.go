package runtimeoverlay

import (
	"fmt"
	"sort"
	"strings"
)

// Mode is one of the four local build modes. They exist to attribute an effect to a group and
// are NOT four shipped artifacts: the shipping build uses exactly one of them, chosen by the
// builder's reviewed default. See DefaultMode.
type Mode string

const (
	// ModeBase compiles the toolchain's runtime untouched. It is the same code and the same Go
	// as every other mode, so a BASE artifact is the native baseline an A/B measurement needs.
	ModeBase Mode = "base"
	// ModeSmall enables group A only: the small-process runtime (spans, stacks, exited stacks).
	ModeSmall Mode = "small"
	// ModeCGO enables group B only: the cgo extra-M lifetime change.
	ModeCGO Mode = "cgo"
	// ModeBoth enables A and B.
	ModeBoth Mode = "both"
)

// DefaultMode is what the product build compiles when nothing overrides it.
//
// It is A without B, and that is a decision rather than an oversight. Group A's effect is
// measurable on any host that can run the runtime it patches, and it was measured; group B
// changes how a foreign thread's m is released, and the only machine that can execute its
// arm64 half is an Apple device this repository's build machines are not. Shipping B by
// default without that evidence is exactly the trade the construction brief forbids, so B
// stays one flag away (-runtime-overlay=both, or JJ_RUNTIME_OVERLAY=both) until a device run
// exists. See docs/internal/ios-runtime-overlay.md for the evidence table and the remaining gap.
const DefaultMode = ModeSmall

// ParseMode validates a mode string from a flag or the environment.
func ParseMode(text string) (Mode, error) {
	mode := Mode(strings.TrimSpace(strings.ToLower(text)))
	for _, known := range []Mode{ModeBase, ModeSmall, ModeCGO, ModeBoth} {
		if mode == known {
			return mode, nil
		}
	}
	return "", fmt.Errorf("unknown runtime overlay mode %q: want one of %s", text, ModeList())
}

// ModeList is the accepted mode vocabulary, for flags and error messages.
func ModeList() string {
	names := make([]string, 0, 4)
	for _, mode := range []Mode{ModeBase, ModeSmall, ModeCGO, ModeBoth} {
		names = append(names, string(mode))
	}
	return strings.Join(names, "|")
}

// Params are the runtime values an overlay plan compiles. They are DATA rather than edited
// source so that changing a value cannot invalidate an anchor, and so a measurement can vary
// one of them without touching the generator.
type Params struct {
	// SpanSetBlockEntries is the number of span pointers in one spanSetBlock.
	SpanSetBlockEntries int
	// SpanSetInitSpineCap is the initial capacity of a spanSet spine, in pointers.
	SpanSetInitSpineCap int
	// StackCacheSize is the per-P, per-order stack cache block size, in bytes.
	StackCacheSize int
	// FreeExitedStack makes EVERY exited goroutine return its starting stack. It is the shape
	// upstream uses only for non-standard stacks, applied unconditionally.
	FreeExitedStack bool
	// FreeExitedStackBacklog makes an exited goroutine return its starting stack once its P
	// already parks this many goroutines. 0 disables it. It exists because returning the stack
	// unconditionally costs a stack allocation on the next goroutine that reuses the g, and the
	// measurement in docs/internal/ios-runtime-overlay.md is what decides which of the two ships.
	FreeExitedStackBacklog int
	// DropExtraM makes a cgo callback hand its m back after every call into Go.
	DropExtraM bool
}

// The two runtime constants every plan value has to respect, neither of which the Go compiler
// checks.
const (
	// appleArm64PageSize is physPageSize on every Apple arm64 target: iPhone/iPad, the simulator on
	// Apple silicon, and tvOS.
	appleArm64PageSize = 16 * 1024
	// upstreamFixedStack is fixedStack on the 64-bit targets this repository ships; it is the size
	// of order 0 and therefore the grain stackpoolalloc slices a cache block into.
	upstreamFixedStack = 2 * 1024
)

// Validate refuses plan values that compile but violate a runtime invariant.
//
// # Why this exists at all
//
// Every value in Params is emitted as a Go constant, so any integer compiles. Two of them are read
// by code that THROWS at startup or, worse, writes past a span, and both were reachable from the
// parameter space during the measurement round:
//
//   - `_StackCacheSize` must be a multiple of the *target's* physPageSize, or `stackinit`
//     (src/runtime/stack.go) throws "cache size must be a multiple of page size" before main runs.
//     An 8 KiB block is a perfectly reasonable-looking tuning value, compiles, and runs correctly
//     on an amd64 host with 4 KiB pages - and crashes every iOS launch, where the page size is
//     16 KiB. It was measured as the best-looking stack number in the host probe, which is exactly
//     how a host measurement becomes a device launch crash.
//   - `stackpoolalloc` cuts one `_StackCacheSize` page run into `fixedStack<<order` stacks with
//     `for i := uintptr(0); i < _StackCacheSize; i += s.elemsize`. A block size that is not a
//     multiple of every reachable element size leaves the last stack straddling the end of its
//     span, which is heap corruption rather than a clean throw. A power of two that is a multiple
//     of the largest cached stack size is the condition that keeps the loop in bounds, so that is
//     what is required.
func (p Params) Validate() error {
	if p.SpanSetBlockEntries < 2 || p.SpanSetBlockEntries > 4096 ||
		p.SpanSetBlockEntries&(p.SpanSetBlockEntries-1) != 0 {
		return fmt.Errorf(
			"runtime overlay: SpanSetBlockEntries is %d; it must be a power of two between 2 and 4096. "+
				"A spanSet indexes its blocks with cursor/spanSetBlockEntries and cursor%%spanSetBlockEntries, and the "+
				"block-free path compares a counter against it, so a size that is not a power of two makes the two "+
				"halves of that arithmetic disagree", p.SpanSetBlockEntries)
	}
	if p.SpanSetInitSpineCap < 1 || p.SpanSetInitSpineCap > 4096 ||
		p.SpanSetInitSpineCap&(p.SpanSetInitSpineCap-1) != 0 {
		return fmt.Errorf(
			"runtime overlay: SpanSetInitSpineCap is %d; it must be a power of two between 1 and 4096, because the "+
				"spine grows by doubling from it", p.SpanSetInitSpineCap)
	}
	if p.StackCacheSize <= 0 || p.StackCacheSize&(p.StackCacheSize-1) != 0 {
		return fmt.Errorf(
			"runtime overlay: StackCacheSize is %d; it must be a power of two. stackpoolalloc slices one "+
				"_StackCacheSize run of pages into fixedStack<<order stacks with `for i := 0; i < _StackCacheSize; "+
				"i += elemsize` (src/runtime/stack.go), so a block that is not a multiple of every reachable element "+
				"size leaves the last stack straddling the end of its span", p.StackCacheSize)
	}
	if p.StackCacheSize%appleArm64PageSize != 0 {
		return fmt.Errorf(
			"runtime overlay: StackCacheSize is %d, which is not a multiple of %d - physPageSize on Apple arm64. "+
				"stackinit (src/runtime/stack.go) throws \"cache size must be a multiple of page size\" before main, so "+
				"the build would produce an app that crashes on every launch. This is not hypothetical: a value this "+
				"size measured better than 16384 on an amd64 host, whose pages are 4 KiB", p.StackCacheSize, appleArm64PageSize)
	}
	if p.FreeExitedStackBacklog < 0 {
		return fmt.Errorf(
			"runtime overlay: FreeExitedStackBacklog is %d; a negative backlog would be inert while the marker "+
				"claimed it was set, and an inert change that reports itself as active is worse than no change",
			p.FreeExitedStackBacklog)
	}
	return nil
}

// smallParams is group A at the values measured for this fork. See the evidence table in
// docs/internal/ios-runtime-overlay.md; the numbers are the reviewed ones, not a guess.
//
// The stack policy is a BACKLOG rather than Clash's unconditional free, and the measurements in
// that document are the reason: freeing every exited stack and freeing once the P already parks
// eight reach the same steady-state residency (0.15-0.16 MiB against upstream's 2.4 MiB in the
// burst-then-idle probe), while the bounded form states what it bounds - eight goroutines per P,
// at most 16 KiB of parked stacks - instead of relying on the stack cache to drain.
func smallParams() Params {
	return Params{
		SpanSetBlockEntries:    32,
		SpanSetInitSpineCap:    16,
		StackCacheSize:         16 * 1024,
		FreeExitedStackBacklog: 8,
	}
}

// cgoParams is group B alone. The stack and span values are upstream's, so a CGO artifact
// isolates the m-lifetime change from the small-process one.
func cgoParams() Params {
	params := upstreamParams()
	params.DropExtraM = true
	return params
}

// upstreamParams are the toolchain's own values, restated so that a mode which switches a
// group off compiles the same code with the group inert instead of with a different code path.
// TestUpstreamParamsMatchTheToolchain asserts these against GOROOT, because a restatement that
// drifts is a silent behaviour change on the slices that are supposed to be untouched.
func upstreamParams() Params {
	return Params{
		SpanSetBlockEntries: 512,
		SpanSetInitSpineCap: 256,
		StackCacheSize:      32 * 1024,
		DropExtraM:          false,
	}
}

// Plan is one resolved overlay: which mode, the exact values, and what it will be compiled
// into.
type Plan struct {
	Mode   Mode
	Params Params
	// BindTarget is the gomobile -target value the overlay rides into. It decides whether any
	// iOS-family slice will exist at all; a macOS-only build gets no overlay.
	BindTarget string

	// probeTag is a test-only override of the `ios` build constraint. The host probe retags both
	// added files so a linux/amd64 test binary can select the overlay's arm64-only semantics and
	// measure them. Empty in every product build, and asserted empty by
	// TestProductPlansNeverSetTheProbeTag.
	probeTag string
	// forceDropExtraMAllArches is a test-only relaxation of the goarch guard described in doc.go.
	// Product plans never set it.
	forceDropExtraMAllArches bool
	// extraEditTargets are additional assembly files the host probe patches with group B's
	// insertion (asm_amd64.s on the probe host). Product plans never set it.
	extraEditTargets []string
}

// Resolve validates a mode for a gomobile target and returns the plan it implies.
//
// A target without an iOS-family platform resolves to ModeBase: the overlay only exists for
// the iOS-family runtime, a macOS-only framework must keep the official one, and silently
// applying the edits to it is the leak this function's Enabled() check prevents.
func Resolve(mode Mode, bindTarget string) (Plan, error) {
	parsed, err := ParseMode(string(mode))
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Mode: parsed, BindTarget: bindTarget}
	switch parsed {
	case ModeBase:
		plan.Params = upstreamParams()
	case ModeSmall:
		plan.Params = smallParams()
	case ModeCGO:
		plan.Params = cgoParams()
	case ModeBoth:
		params := smallParams()
		params.DropExtraM = true
		plan.Params = params
	}
	if err := plan.Params.Validate(); err != nil {
		return Plan{}, err
	}
	if !HasIOSFamilyPlatform(bindTarget) {
		// Not an error: `-platform macos/arm64` is a legitimate local build, and the honest
		// answer for it is the official runtime. What must not happen is an iOS-family build
		// silently skipping the overlay, which verify.go fails instead.
		plan.Mode = ModeBase
		plan.Params = upstreamParams()
	}
	return plan, nil
}

// Enabled reports whether this plan compiles anything at all. A disabled plan must produce no
// shim, no overlay JSON and no marker.
func (p Plan) Enabled() bool {
	return p.Mode != ModeBase
}

// groupA reports whether the small-process values differ from the toolchain's.
func (p Plan) groupA() bool {
	return p.Params.SpanSetBlockEntries != upstreamParams().SpanSetBlockEntries ||
		p.Params.SpanSetInitSpineCap != upstreamParams().SpanSetInitSpineCap ||
		p.Params.StackCacheSize != upstreamParams().StackCacheSize ||
		p.Params.FreeExitedStack || p.Params.FreeExitedStackBacklog > 0
}

// groupB reports whether the cgo extra-M change is requested.
func (p Plan) groupB() bool {
	return p.Params.DropExtraM
}

// onConstraint is the build constraint of the file that carries the plan's values.
func (p Plan) onConstraint() string {
	if p.probeTag != "" {
		return p.probeTag
	}
	return "ios"
}

// offConstraint is the negation of onConstraint, so exactly one of the two added files is
// compiled for any target.
func (p Plan) offConstraint() string {
	return "!" + p.onConstraint()
}

// dropExtraMExpression is the Go expression the added file assigns to jjOverlayDropExtraM.
//
// The goarch term is the arm64-only invariant from doc.go: without it, an iOS amd64 simulator
// slice (which asm_arm64.s never reaches) would suppress cgoBindM and never drop the m.
func (p Plan) dropExtraMExpression() string {
	if !p.groupB() {
		return "false"
	}
	if p.forceDropExtraMAllArches {
		return "true"
	}
	return "goarch.IsArm64 == 1"
}

// Marker is the exact string compiled into every overlay-selected slice. It names the mode and
// every value the overlay set, so a slice carrying it cannot be a slice built with a different
// plan, and the negative check (a macOS slice must not carry it) has something asymmetric to
// look for.
func (p Plan) Marker() string {
	flag := func(value bool) string {
		if value {
			return "1"
		}
		return "0"
	}
	params := p.Params
	return fmt.Sprintf(
		"jiejiebox-ios-runtime-overlay/1 mode=%s spanSetBlockEntries=%d spanSetInitSpineCap=%d stackCacheSize=%d freeExitedStack=%s freeStackBacklog=%d dropExtraM=%s",
		p.Mode,
		params.SpanSetBlockEntries,
		params.SpanSetInitSpineCap,
		params.StackCacheSize,
		flag(params.FreeExitedStack),
		params.FreeExitedStackBacklog,
		flag(params.DropExtraM),
	)
}

// MarkerPrefix is the invariant part of Marker, for diagnostics that need to say "an overlay
// marker is here but it is not the one this build expected".
const MarkerPrefix = "jiejiebox-ios-runtime-overlay/"

// iOSFamilyPlatforms are the gomobile platforms whose slices compile the `ios` build
// constraint. tvOS is built with GOOS=darwin plus the ios,tvos tags, so it is in this set even
// though its GOOS is not "ios".
var iOSFamilyPlatforms = []string{"ios", "iossimulator", "tvos", "tvossimulator"}

// HasIOSFamilyPlatform reports whether a gomobile -target value can produce an iOS-family
// slice.
func HasIOSFamilyPlatform(bindTarget string) bool {
	found := false
	for _, platform := range SplitTargetPlatforms(bindTarget) {
		for _, family := range iOSFamilyPlatforms {
			if platform == family {
				found = true
			}
		}
	}
	return found
}

// SplitTargetPlatforms returns the platform part of every comma-separated gomobile target,
// lowercased, with any /arch suffix removed.
func SplitTargetPlatforms(bindTarget string) []string {
	platforms := make([]string, 0, 4)
	for _, item := range strings.Split(bindTarget, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		platform := strings.SplitN(item, "/", 2)[0]
		platforms = append(platforms, strings.ToLower(strings.TrimSpace(platform)))
	}
	sort.Strings(platforms)
	return platforms
}

// SliceIsIOSFamily reports whether an xcframework slice directory name compiles the `ios` build
// constraint, which is what decides whether the overlay is expected in it.
//
// The slice name is chosen by the toolchain (ios-arm64, ios-arm64_x86_64-simulator, tvos-arm64,
// maccatalyst-arm64, macos-arm64, ...), so this asks about the prefix the toolchain emits rather
// than about a list of slices this repository happens to build: an unexpected name must fail the
// marker check, not escape it.
//
// maccatalyst is in this set because gomobile tags it `ios,macos,maccatalyst`
// (cmd/gomobile/env.go:107), so a Catalyst slice that shares a build with an iOS slice DOES compile
// the overlay's values and does carry the marker. Leaving it out would not make Catalyst safer; it
// would make a Catalyst build fail with a "leaked constraint" message that is not true.
// HasIOSFamilyPlatform deliberately does NOT include it: a Catalyst-only build compiles the
// toolchain's runtime, because that target has never been measured.
func SliceIsIOSFamily(slice string) bool {
	lower := strings.ToLower(slice)
	for _, family := range []string{"ios-", "ios_", "tvos-", "tvos_", "maccatalyst-"} {
		if strings.HasPrefix(lower, family) {
			return true
		}
	}
	return false
}
