# iOS Go runtime overlay: what it changes, why, and what is not proven

This is an engineering record for `cmd/internal/runtimeoverlay`. It exists so the next reader can
answer three questions without re-deriving them: which runtime invariants this tree modifies, what
the measurement says, and what has NOT been measured.

Everything here is about the **Go runtime that the iOS-family libbox slices are compiled with**. No
application code, no protocol, no buffer geometry, no GC policy and no OOM/threshold configuration
is changed by this work.

## 1. The real build path

The overlay is not a separate experimental build. It rides in the existing Apple Libbox path:

```
go.mod                     go 1.26.0 / toolchain go1.27.4        (the toolchain the overlay is pinned to)
  └─ cmd/internal/build_libbox      -target apple -platform ios/arm64,macos/arm64
       │  prepareRuntimeOverlay()   resolves mode -> cmd/internal/runtimeoverlay
       │                            writes patched GOROOT files + overlay.json in a temp dir,
       │                            writes a `go` shim in that dir, prepends it to PATH
       └─ gomobile bind        cmd/gomobile/{build,bind_iosapp}.go resolve `go` through PATH
            │                   shim injects -overlay into build|install|list|test|vet|run
            └─ go build -buildmode=c-archive   GOOS=ios  GOARCH=arm64   (runtime compiled from the patched source)
                 │                             GOOS=darwin GOARCH=arm64 (runtime compiled from the `!ios` file)
                 └─ Libbox.xcframework/{ios-arm64,macos-arm64}/Libbox.framework
                      │  build_libbox verifies the marker in every slice, then writes libbox.provenance
                      └─ .github/actions/apple-libbox-build
                           │  go run ./cmd/internal/verify_runtime_overlay   (same bytes, before packing)
                           └─ scripts/ci/apple-libbox-artifact.sh pack       (manifest + BUILD-INFO)
                                └─ client-apple.yml libbox job -> artifact -> GUI jobs -> PacketTunnel
```

Two properties matter and are enforced:

* **The proof is on the shipped bytes.** The verifier runs against the installed
  `Libbox.xcframework` the pack step is about to archive, and takes the expected plan from
  `libbox.provenance`, not from a flag.
* **The macOS slice is not collateral.** The same gomobile invocation produces it, so the overlay
  supplies both build constraints: `ios` gets the plan's values, `!ios` restates the toolchain's own
  values and carries no marker at all. A macOS slice that acquires a marker fails the build.

## 2. Upstream design, the invariant it touches, and what this fork does

The starting point is Clash/Hako's `cmd/build_libbox` runtime overlay. It was read in full, not
skimmed, and re-derived against Go 1.27.4 because two of its anchors sit next to code that has
changed since Go 1.26 (the adaptive starting stack size, in particular).

| Upstream idea | Runtime invariant it touches | Expected gain | Risk | What this fork does |
|---|---|---|---|---|
| `spanSetBlockEntries` 512 -> 32 | `mspan` bookkeeping blocks are `persistentalloc`'d off-heap and never returned; each of the 4 spanSets per mcentral pays one block per span-set generation | ~1.2 MiB of `GCSys`/`metadata/other` on a few-MiB heap | more spine growth, more block allocs under a large heap | **Adopted** at the same 32/16, measured below |
| `_StackCacheSize` 32 KiB -> 16 KiB | per-P per-order stack cache block also sizes the span `stackpoolalloc` cuts stacks from | freed stack pages are returned sooner after a burst | more `stackcacherefill`/`release` traffic; on iOS 16 KiB is exactly one page | **Adopted** at 16 KiB |
| Exited goroutine returns its starting stack | upstream parks the starting stack on the P's `gFree`; Go 1.27 also *recomputes* `startingStackSize` every GC, so "the starting size" is no longer a constant | ~1.8 MiB of stack residency after a burst | the next goroutine in that slot allocates a stack instead of finding one | **Adopted with a different policy** - see §3.3; it is the single largest win and it has a real cost |
| `needAndBindM` + `asm_arm64.s` pair | a non-Go thread that calls into Go gets an m bound to its pthread until the thread exits | ~36 KiB per thread that has ever called in (g0 + signal stack) | two extra syscalls per call (`minit`/`unminit` sigaltstack) | **Implemented, verified on a host, NOT enabled by default** - see §3.4 |
| marker symbol + `nm` check | - | - | `nm` stops answering once the release link uses `-s -w` | **Replaced** with a string marker reachable from `schedinit`, checked by byte search |

### 2.1 Why the marker is a string and not a symbol

`build_shared.LinkerFlags` passes `-s -w -buildid=` for a release build, so the symbol table is gone
from the artifact that ships. A check that reads symbols would pass locally and fail - or silently
pass - in the release. The marker is therefore a plain string constant reached through a
`//go:noinline` function from `schedinit`:

```go
const jjOverlayMarkerText = "jiejiebox-ios-runtime-overlay/1 mode=… spanSetBlockEntries=…"
//go:noinline
func jjOverlayMarkerValue() string { return jjOverlayMarkerText }
//go:noinline
func jjOverlayMarker() { if jjOverlayMarkerValue() == "" { throw("…") } }
```

Two details are deliberately ugly:

* The string is handed out by a **function call**. The runtime package's own variable initialisers
  have not run when `schedinit` calls this, so a package-level variable is still zero there - an
  early version of this file threw `marker was compiled empty` on every launch because of exactly
  that.
* The comparison is against a **noinline call's result**, not against the constant. A constant
  compared with itself folds away, and a marker a dead-code pass may delete is not evidence.

The marker names every value the plan set, so a slice compiled with a different plan cannot satisfy
the check, and the verifier reports "an overlay marker, but not this one" separately from "no marker
at all" - the first is a stale plan, the second is an injection that never happened.

## 3. What each item does, in Go 1.27.4 terms

Anchors are literal strings in the toolchain's own files; each must occur **exactly once** and the
file must hash to the pinned value. `TestAnchorsAreUniqueInTheReviewedRuntime` re-checks the
anchors, `TestReviewedToolchainMatchesTheRuntimeUnderTest` re-checks the pin, and
`TestUpstreamParamsMatchTheToolchain` re-reads the upstream numbers so the `!ios` restatement cannot
drift.

### 3.1 A1 - span-set metadata (`src/runtime/mspanset.go`)

`spanSetBlock` is `[512]atomic.Pointer[mspan]` plus a header: 4 KiB per block. There are four
spanSets per `mcentral` (`partial[2]`, `full[2]`), one `mcentral` per (size class, span class), and
a block is allocated the first time a set is used. A tunnel that touches a few dozen size classes
therefore pays tens of blocks of 4 KiB for a few thousand spans. 32 entries make a block 256 bytes;
the spine grows more often, and a large heap would grow it more, which is the accepted trade.

Blocks and spines are `persistentalloc`'d into `gcMiscSys`, so the class that moves is
`/memory/classes/metadata/other:bytes` (`inWorkBufs + gcMiscSys`). That class is not the overlay's
exclusive property - work buffers are in it too - so the assertion is on the ratio, not on absolute
attribution.

### 3.2 A2 - per-P stack cache (`src/runtime/malloc.go`, `src/runtime/stack.go`)

`_StackCacheSize` is the block size `stackpoolalloc` cuts stacks from and the cap of a P's
per-order cache (`refill` until `size < _StackCacheSize/2`, `release` above `_StackCacheSize`). At
16 KiB on iOS it is exactly one 16 KiB page, `stackinit`'s multiple-of-page-size check still holds,
and a 16 KiB stack (order 3) stops fitting the small-stack path and takes a page of its own - which
is upstream's documented intent for large stacks. macOS keeps 32 KiB because it compiles the `!ios`
file.

### 3.3 A3 - exited goroutine stacks (`src/runtime/proc.go`, `gfput`)

Upstream:

```go
if stksize != uintptr(startingStackSize) {
    // non-standard stack size - free it.
```

The overlay's condition adds two terms, driven by constants the `!ios` file zeroes:

```go
if stksize != uintptr(startingStackSize) || jjOverlayFreeExitedStack ||
    (jjOverlayFreeExitedStackBacklog > 0 && pp.gFree.size >= jjOverlayFreeExitedStackBacklog) {
```

**This fork does not ship Clash's unconditional free.** Two policies were measured (§4.3): freeing
every exited stack, and freeing once the P already parks eight. They reach the same steady state
(0.15 MiB against upstream's 2.4 MiB), but the bounded form states what it bounds - eight goroutines
per P, at most 16 KiB of parked stacks - instead of relying on the stack cache to drain. The cost is
the same either way, and it is real: **every goroutine creation in the releasing path pays a
`systemstack` + `stackalloc`.**

Go 1.27 makes this edit *more* subtle than it looks, and the pin is what absorbs that:
`startingStackSize` is no longer `const fixedStack` - it is a `var` recomputed by
`gcComputeStartingStackSize` every GC. The overlay's added term is insensitive to that (it is an
`||`, not a replacement of the comparison), which is why the same anchor still means what
`gfput` says it means; a future toolchain that restructures `gfput` fails the hash check instead of
being silently patched.

### 3.4 B - the cgo extra `m` (`src/runtime/proc.go`, `src/runtime/asm_arm64.s`)

The two halves must move together, which is why one plan field drives both edits:

```
proc.go   needAndBindM:   if !jjOverlayDropExtraM && _cgo_pthread_key_created != nil && *(*uintptr)(...) != 0 { cgoBindM() }
asm_arm64 cgocallback:    MOVBU runtime·jjOverlayDropExtraM(SB), R6 ; CBNZ R6, dropm   (before upstream's check)
```

`jjOverlayDropExtraM` is a **variable**, not a constant, because assembly loads its address, and its
value is `goarch.IsArm64 == 1`. That is the arm64-only invariant: only `asm_arm64.s` is edited, so
an iOS **amd64 simulator** slice must take upstream's path in *both* halves. Suppressing `cgoBindM`
without the assembly half would leak an m; dropping without suppressing the binding would let the
pthread destructor and `dropm` both return the same m.

**B is not enabled by default.** Two reasons, both measured:

1. Its own cost is 1.7 us per Swift->Go call against 0.19 us (see §4.4) - a 9x per-call price. On a
   control-plane call rate that is nothing, but the brief for this work names "every Swift/Go API
   call becomes slower" as a cost a ~1 MiB saving does not justify without evidence that the real
   call rate is low. No such evidence exists for this client yet.
2. Its arm64 half has never executed on an Apple device. The host verification in §4.4 runs the
   *mirrored* amd64 epilogue; that proves the state machine and the TLS/destructor invariants, not
   the arm64 instruction sequence on a device.

Enabling it is one flag: `-runtime-overlay=both` (or `JJ_RUNTIME_OVERLAY=both`). The verification,
marker, provenance and macOS-leak gates are identical for both modes, so the same pipeline proves it
once a device run exists. The evidence a device run must produce is listed in §7.

## 4. Evidence

All numbers below are from this host, with the commands given. They are **not** `phys_footprint`:
that needs a Network Extension on a device and is `NEEDS_DEVICE` (see §7).

Environment: Windows 11 / amd64, Go 1.27.4 (the pinned toolchain), 12 logical cores; the group B
probe ran under WSL2 Ubuntu 24.04 / linux-amd64 with the same Go 1.27.4.

### 4.1 How to reproduce

```sh
# the whole package's suite, including the real overlay compiles and the behavioural probe
go test ./cmd/internal/runtimeoverlay/ -count=1 -v

# one item at a time (the probe prints both sides)
go test ./cmd/internal/runtimeoverlay/ -count=1 -run TestOverlayMovesTheRuntimeClassesItTargets -v
go test ./cmd/internal/runtimeoverlay/ -count=1 -run TestGroupBReleasesTheExtraMOfAThreadThatStopsCallingGo -v   # linux/darwin only

# which mode would the product build compile?
go run ./cmd/internal/build_libbox -print-runtime-overlay ios/arm64,macos/arm64

# attributing one item on a local build
JJ_RUNTIME_OVERLAY=base  ./scripts/ci/build-apple-libbox.sh ios
JJ_RUNTIME_OVERLAY=small ./scripts/ci/build-apple-libbox.sh ios
JJ_RUNTIME_OVERLAY=both  ./scripts/ci/build-apple-libbox.sh ios
```

### 4.2 Group A, per item (`testdata/probe`: 256-goroutine bursts x8, 64 objects in every step of
the size-class ladder, `GOMAXPROCS=4`, measured after two GCs)

| Build | `/memory/classes/heap/stacks` | `/memory/classes/metadata/other` | goroutine create+exit |
|---|---:|---:|---:|
| toolchain runtime | 1 998 848 | 2 938 960 | 214 ns |
| + A1 only | ~2 000 000 | 1 691 600 | ~unchanged |
| + A2 only | ~2 030 000 | ~2 941 000 | ~unchanged |
| + A3 (backlog 8) only | 196 608 | ~2 970 000 | 449 ns |
| **shipped `small`** (A1+A2+A3) | **147 456** | **1 691 600** | **559 ns** |

- The stack residency falls by **1.77 MiB** (93%).
- `metadata/other` falls by **1.19 MiB** (42%); `/memory/classes/metadata/mspan/inuse` moves slightly
  *up* (+16 KiB), which is expected: the blocks A1 shrinks are persistent allocations, not `mspan`s.
- The goroutine create+exit rate costs roughly **+210..350 ns** per goroutine. This is the price of
  A3 and it is reported, not hidden. With a tunnel's connection rate it is a fraction of a percent
  of CPU, but it is a real regression on a pure churn microbenchmark and the plan says "8 parked".
- `heap/inuse` was 0 in this probe (everything it allocates is retained by the harness, so the class
  reports the retained slices through goal rather than in-use); it is not used as a signal.

### 4.3 The A3 policy trade-off (same harness, A1+A2 held constant)

| A3 policy | stack residency | create+exit |
|---|---:|---:|
| off (upstream) | 2.4 MiB | 1.00x |
| backlog 64 | 2.36 MiB | 1.07x |
| backlog 32 | 0.82 MiB | 1.86x |
| **backlog 8 (shipped)** | 0.16 MiB | 2.13x |
| always (Clash) | 0.15 MiB | 2.31x |

The cliff between 32 and 64 is upstream's `gFree.size >= 64` batch transfer: a P's local list hovers
at 32..64, so a threshold at 64 never fires. 8 was chosen because it is on the flat part of the
residency curve and does not trade the clamp for a bigger per-goroutine cost.

### 4.4 Group B (`testdata/bprobe`: 48 pthreads, each calls one exported Go function once and then
parks; linux-amd64; `GOMAXPROCS=2`)

| | stack growth while 48 threads park | stack residency after they exit | callback cost, 8 concurrent callers |
|---|---:|---:|---:|
| toolchain runtime | +1 802 240 B | 2 129 920 B | 185 ns/call |
| group B (mode `cgo`) | +65 536 B | 393 216 B | **1 702 ns/call** |

- 48 parked threads cost the unmodified runtime **+1.72 MiB** and group B **+0.06 MiB**: the m is
  handed back after the call, so what stays is bounded by the calls in flight, not by the threads
  that ever called. That is ~36 KiB per thread, which matches one g0 (8 KiB) plus one signal stack
  (32 KiB).
- Every callback returned the value the C thread expected, in both builds, including 12 000
  concurrent calls from 8 threads: no TLS corruption, no double free, no crash.
- The cost is 9x per call (1.5 us), which is the price the analysis predicted (`minit`/`unminit`
  sigaltstack syscalls). §3.4 is why this does not ship enabled.

### 4.5 Negative gates that were actually exercised

| Gate | How it was exercised | Result |
|---|---|---|
| overlay not injected | `TestRequireVerifiedXCframeworkRejectsASliceBuiltPastTheOverlay`, and the verifier CLI against a framework whose marker was stripped | fails, names the slice |
| macOS slice acquired the marker | `TestRequireVerifiedXCframeworkRejectsAMacOSSliceThatPickedUpTheMarker` | fails, names the slice |
| wrong plan in the slice | `TestScanDistinguishesAnotherPlanFromNoOverlay` | fails, "an overlay marker that is not this build's plan" |
| unreviewed Go version | `TestToolchainCheckRefusesAnUnreviewedVersion` | fails with the re-review instructions |
| patched GOROOT | `TestToolchainCheckRefusesAPatchedGOROOT` | fails, names the file |
| toolchain under GOMODCACHE | `TestToolchainCheckRefusesAToolchainInsideTheModuleCache` | fails with the install instruction |
| anchor moved | `TestApplyEditsRefusesMissingAndDuplicatedAnchors` | fails, refuses to choose between two occurrences |
| GOROOT already has a file the overlay adds | `TestGenerateRefusesToReplaceAFileItMeansToAdd` | fails |
| an Apple build would compile mode base | the `build-apple-libbox.sh` gate + `-print-runtime-overlay` | fails the build |
| the shim was never used | `build_libbox`'s post-build check on the shim's injection log | fails the build |

## 5. What is deliberately NOT part of this

* `service/oomkiller` and the ResourceGuard thresholds, GC parameters (`GOGC`, `GOMEMLIMIT`), and
  the buffer geometry of `with_low_memory` are untouched.
* No protocol, DNS, TUN or UI behaviour is changed.
* No new workflow file, no new release channel, no second build path: the product artifact comes
  from the same `build-apple-libbox.sh both` the release already used.
* Android, Windows, Linux and macOS builds compile the toolchain's runtime. The overlay is only
  prepared for a gomobile target that contains an iOS-family platform.

## 6. Upgrading the Go toolchain

The overlay fails closed on any toolchain that is not in `reviewedToolchains`
(`cmd/internal/runtimeoverlay/toolchain.go`). To move to a new patch or minor:

1. Read the new toolchain's `src/runtime/{mspanset.go,malloc.go,proc.go,asm_arm64.s}` and confirm
   each anchor still means what §3 says it means. `TestAnchorsAreUniqueInTheReviewedRuntime` will
   tell you if one no longer occurs once.
2. Re-read the two group B halves together - `needAndBindM` and the `cgocallback` epilogue - because
   a change to either without the other is the failure this pair exists to prevent.
3. Add the version and the five file digests to `reviewedToolchains`.
4. Re-run `go test ./cmd/internal/runtimeoverlay/ -count=1 -v` and update §4's numbers if the
   toolchain changed them.

A patch bump is NOT automatically compatible: `go1.27.5` is the same version to a `>=` comparison
and a different runtime to this package.

## 7. NOT PROVEN - the device gap

Everything below is `NEEDS_DEVICE` and must not be read as a pass:

* **`phys_footprint`.** The metric that matters for the Network Extension jetsam budget was never
  measured, because it requires a running `NEPacketTunnelProvider`. What is measured is Go heap
  classes on a host. The two are not interchangeable, and no MiB figure in this document is a
  `phys_footprint` claim.
* **The arm64 assembly half of group B.** The host probe mirrors it onto amd64. Group B is off by
  default, so nothing ships with an unverified arm64 edit - but enabling it requires the run below.
* **The real Swift -> Go call rate.** It decides whether group B's 1.5 us per call is noise or not.
  Measuring it needs an instrumented build on a device.

The minimum device run that would close the group B gap, on the same commit, ideally as one
alternating sequence on one device:

1. Build `JJ_RUNTIME_OVERLAY=small` and `JJ_RUNTIME_OVERLAY=both`; keep both IPAs.
2. For each: cold start, load the user's real configuration, idle for 300 s, then measure
   `phys_footprint` (`task_vm_info.phys_footprint`), `os_proc_available_memory`, goroutine count and
   the process's thread count.
3. Drive the control plane the UI drives (open the dashboard, scroll the connection list, switch a
   profile, start/stop the tunnel 10x) and record the per-call latency distribution for a bound
   Swift->Go call under each build.
4. Leave the tunnel connected for 30 minutes with 40 active connections, then repeat (2).
5. Interleave BASE/SMALL/BOTH at least three times each; a single screenshot is not evidence.

For group A the same run answers the second open question: whether a 1.77 MiB stack-class saving on
the host becomes a `phys_footprint` saving on the device, and whether the extra stack allocation per
goroutine shows up at a realistic connection rate.

## 8. Rolling back

* One flag: `JJ_RUNTIME_OVERLAY=base` (or `-runtime-overlay=base`) produces the toolchain's runtime
  with no source edit, no shim and a provenance record that says `runtime.overlay=base`.
* One revert: `git revert <overlay commit>` removes the subsystem; nothing else depends on it. The
  build then compiles the toolchain's runtime, exactly as before this work.
* No GOROOT is modified at any point, so there is no installation to restore and no state a failed
  build leaves behind: every patched file lives in a temporary directory that is removed when the
  build ends.
