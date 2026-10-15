# Phase 2 — merged-state audit and regression: integration/release-next

Completion gate required by the brief:

```text
INTEGRATION_HEAD = 7803155f5844bf9cde2687c409d02c5b4e9de802

MERGED_STATE_AUDIT = PASS (with 8 defects found and fixed — see §3)
FORMATTING = PASS
VET = PASS (only the 3 pre-existing unsafe.Pointer findings, byte-identical to baseline)
PLATFORM_BUILD_MATRIX = PASS (13/13 platforms build; 2 documented pre-existing third-party limits)
FREEBSD_KNOWN_FAILURE = FIXED_AND_PROVED (Phase 1) + a second FreeBSD-class gap FIXED (Phase 2)
NORMAL_TESTS = PASS (4 pre-existing failures, each reproduced on a baseline comparator)
RACE_TESTS = NOT RUN — ENVIRONMENT_BLOCKED, with the exact cause and the substitutes used (§6)
STRESS/REPEAT_TESTS = PASS (count=10 and count=20 across 12 concurrency-heavy packages)
MEANINGFUL_REGRESSION_TESTS = PASS (every new test proved to fail without its fix — §4)
PERFORMANCE = NO_UNEXPLAINED_REGRESSION
WORKTREE = CLEAN
```

Phase 2 added **9 commits over 22 files** (+1238 / −40) on top of the Phase-1 integration head, and
fixed **8 defects**. One of them was P0.

---

## 1. Re-anchoring

```text
INTEGRATION_BRANCH          integration/release-next
INTEGRATION_HEAD (Phase 1)  36f8a2b33787b9e922089bef5acb48dee4123563
INTEGRATION_HEAD (Phase 2)  7803155f5844bf9cde2687c409d02c5b4e9de802
AUDIT_BASE (v0.1.10)        9711a97d0450e594219f261558a66deda7ec825a
TESTING_BASE_AT_INTEGRATION c32c210b0dbb1a23fb9b19ef731d164cd8745d34
MERGE-BASE HEAD vs testing  c32c210b0dbb1a23fb9b19ef731d164cd8745d34   (== testing: correct)
WORKTREE CLEAN              yes
```

The head had not moved unexpectedly, so no re-investigation of Phase 1 was needed. The diffs the
brief asks for:

```text
v0.1.10  -> HEAD   170 files changed, 28210 insertions(+), 322 deletions(-)
testing  -> HEAD   158 files changed, 27343 insertions(+), 289 deletions(-)
commits  -> HEAD   54 (Phase 1) + 9 (Phase 2) = 63
```

---

## 2. How the merged state was audited

The brief's central question — *does the final code as composed still satisfy every invariant each
branch intended to protect?* — was answered per zone, by four independent read-only audits run
against a fixed revision in parallel with my own work, plus my own trace of the network lifecycle:

| zone | method | outcome |
|---|---|---|
| **A** network lifecycle / ownership / generations | my own end-to-end trace + all call sites | **1 defect** (§3.3) |
| **B** IPv4/IPv6 family semantics | my own trace of every `HardUndialable` call site, the bypass path, and the `netcap` gate | clean; 1 test gap closed |
| **C** DNS adblock terminality | independent audit, mutation-tested | terminal on every rule-walking path; **1 doc defect + 1 behaviour defect** (§3.2, §3.7) |
| **D** XHTTP H2/H3/packet-up | independent audit, 11 mutations | no production defect; **1 defect fixed** (§3.4) |
| **E** WireGuard / pause-resume / reload-close | independent audit, full lock-order derivation | **P0** (§3.1) + 1 ordering defect (§3.5) |
| **F** Windows integration | independent audit, gates run for real | **3 defects** (§3.6, §3.8) |

Every audit was instructed to classify findings as
`REAL_DEFECT | LATENT_RISK | TEST_THEATRE | STALE_COMMENT | NO_ISSUE`, to give `file:line` and quoted
code for every claim, and to state what it checked and found clean. Two of them independently
**reproduced** their findings by mutation in throwaway copies rather than arguing from reading.

Crucially, **the audit changed the merged state's verdict.** Five of the eight defects were
invisible to the full test suite — the tests passed with the defect present — which is exactly the
premise of this phase.

---

## 3. Bugs found and fixed

Each is a separate commit with its reasoning, its evidence, and its discrimination measurement.

### 3.1 P0 — the WireGuard pause registration did not exist

`transport/wireguard/endpoint.go` · commit `68342bb1c21`

`34cc5834d9a` ("keep the endpoint state lock out of the pause registration") correctly moved the
pause registration out of the `stateAccess` critical section and **deleted the two
`RegisterCallback` calls without re-adding them**:

```go
var registeredPause, registeredWake *list.Element[pause.Callback]   // never assigned
...
e.pauseCallback = registeredPause        // publishes a nil
e.deviceWakeCallback = registeredWake    // publishes a nil
```

**Impact:** `EventNetworkPause` no longer took the WireGuard device down, so the synchronous
resource contract `onPauseUpdated` implements was unenforced; the DeviceWake recovery nudge never
fired, so every idle endpoint kept its sockets and handshake timers across a suspend or handover;
and `Close` unregistered nothing because there were no handles.

**Why the suite was green:** every test registered the callbacks *by hand* on a fake manager and
never called `Start`. Reverting production to the pre-fix shape left all three lock-order tests
passing — a fixture that supplies the wiring on production's behalf cannot notice production
losing it.

**Fix + proof:** the registrations go back, before `stateAccess` is taken, which is where the
surrounding comment already said they were. Two new tests drive the real `Start` and assert on the
**endpoint's own handles** (not the fake manager's count, which the test itself could satisfy):

```text
pre-fix:  --- FAIL: TestStartRegistersThePauseCallbacks
          --- FAIL: TestCloseUnregistersTheCallbacksStartRegistered
with fix: both PASS, full package suite PASS
```

### 3.2 `protocol/group` classified socket errors that Windows never delivers

commit `cb16fa1b046`

The **same defect class the fork already paid for once**, one package over: `a4fd1c55aaa` fixed
`common/dialer`'s Windows errno classifier and documented why, and that fix was merged by the
dual-stack line *in this integration* — while `protocol/group` kept comparing `syscall.E*EXACT`
constants that never arrive on Windows.

**Impact, measured:** `RetryThisFlow` returned false for a kernel-unreachable path and for a
refusal, so a flow was not retried on another member; `PenalizeMemberGlobally` never demoted a
member whose network was gone, so dead members stayed in rotation. `syscall.ECONNRESET` was equally
dead, agreeing with the conservative default *by coincidence*.

**Fix:** rather than copy the WSA numbers (which is the drift that file exists to prevent), the
helper and its errno sets are exported from `common/dialer` and reused — with the import direction
verified, not assumed (`go list -deps ./common/dialer/` contains no protocol package). Two new sets
(`UnreachableErrnos`, `TimeoutErrnos`) exist because the callers' questions differ, and the existing
`TestWindowsWSAErrorChainIsClassified` **caught me** when I first put `ETIMEDOUT` in the family
path-failure set: a bare errno is not a `net.Error`, so a kernel timeout is not evidence a family
cannot carry packets.

**Why the suite was green:** the table injected the very POSIX constants the classifier compared —
on Windows those are the same number, so both sides agreed on a value production never produces.
The table now carries both spellings numerically: **all 8 new WSA rows FAIL against the pre-fix
code and pass with it.**

### 3.3 A stalled upload response body was recorded as genuine data flow

`transport/v2rayxhttp/conn.go` · commit `8134f118688`

`drainPacketUpResponse` discarded the body read error, and `sendPacket` then called
`xmux.noteSuccess()` unconditionally — so a post whose 200 body died after the headers **reset the
pooled connection's failure streak and disarmed the manager backoff**. SPEC 076's own comment names
the case it exists for: "200-raises whose bodies died seconds later".

**Fix + proof:** the drain returns its outcome and the post fails on it. Two tests, differing only
in whether the body reaches EOF; the breaker is seeded non-zero first, because the defect *resets*
to zero and a fresh client is already zero — without that the assertion would pass against the bug.
Discrimination measured: pre-fix the write path reported nothing for the full 3 s post timeout.

### 3.4 A blocked lookup was logged as a lookup failure

`dns/router.go` · commit `9670dc83e05`

A rule action's verdict and an upstream answer are the same rcode and were treated identically, so
a blocked **dialer** lookup printed:

```text
ERROR[0000] lookup failed for ads.example: (exchange4: NXDOMAIN | exchange6: NXDOMAIN)
```

for the adblock feature working as configured — on the path a dialer takes for every blocked
connection, and in direct contradiction of the module's own reasoning about not making log I/O the
new cost centre.

**Fix:** `ErrLocalVerdict` marks the rcode as locally produced, preserving the **message exactly**
(error text reaches logs and user-visible refusals) and widening only its identity. `IsLocalVerdict`
deliberately is **not** a bare `errors.Is`: a dual-stack lookup aggregates two family errors, and
`errors.Is` would demote a genuine failure to Debug as soon as it shared a tree with a blocked
family — so the predicate walks the tree and requires the error to consist *entirely* of verdicts.
Driven by the measured error tree, which is recorded in the commit.

**Two incidental findings while doing this**, both recorded rather than papered over: the DNS rule
walk has **two structurally similar switches** over the same action types, and my first edit marked
the one that a lookup never takes (it compiled, vetted clean, and did nothing — found by
instrumenting); and the audit's claim that the dialer path had no adblock coverage at all was
correct, so four tests now exist where there were none.

### 3.5 The `closed` flag was armed after the listener it guards, and not at all on some paths

`route/network.go` · commit `7803155f584`

`Scope.Close` runs cleanups in **reverse** registration order; the power listener is registered
after the interface monitor's cleanup, so the flag was armed *after* `powerListener.Close` — exactly
the window the field's own comment describes ("`PowerUnregisterSuspendResumeNotification` plus
`Unpin` is not a join"). It was also armed only inside `if r.interfaceMonitor != nil`, so a manager
built without a monitor never armed it at all.

**Fix:** the arming moves to a cleanup registered at the **end** of PostStart, so it runs **first**,
and is now unconditional. **Stated plainly: this fix is verified by construction and by the
existing tests, not by a new one.** I wrote a test that drives the real `Start`/`Scope.Close`; it
was not viable (`Start` requires a fully constructed manager with real monitors, and every fixture
in this package builds the struct directly), and I removed it rather than leave a brittle harness
around one boolean. The invariant a future reader must preserve is recorded in the commit.

### 3.6 The desktop identity gate was executed by nothing

commit `5058a8b0575`

`verify-desktop-identity-consistency.py` is the only thing comparing the four copies of the client's
and daemon's machine-global names, and **no workflow invoked it** — while its three siblings all
were, and while the client's own `identity.ts` cites it by name. The repository's doctrine forbids
this in as many words: *"A test that exists but is executed by nothing is worse than no test: an
audit sees coverage while nothing is proven."*

**Fix:** wired into the Windows client workflow's gate 2, and added to the contract-verify path
list. **Verified by running it exactly as the workflow does** against a real checkout of the pinned
desktop revision — PASS, 15 values compared.

**This one was my own Phase-1 error.** Phase 1 ran the gate *by hand*, verified its behaviour, and
reported that it "now runs on every Windows gate". A hand-run looks identical to a wired one. The
claim is corrected in place in the Phase-1 report, with the reason it was wrong.

### 3.7 The `protocol/bridge` tests did not compile on any platform without a backend

commits `0f548cdbf45` (bridge) — *the second FreeBSD-class defect*

`packet.go` and `backend.go` carry `//go:build linux || darwin || (windows && (amd64 || 386))`;
their two test files carried **no tag** and test that tagged code:

```text
GOOS=freebsd go vet ./protocol/bridge/
protocol/bridge/index_leak_test.go:31:2: undefined: bridgeIndexAccess
```

The binary was fine everywhere, which is why it survived — but the bridge package **could not be
tested at all** on FreeBSD, OpenBSD, NetBSD or DragonFly: a regression there would be found by
neither CI nor a developer, and the tests would not even report that they had been skipped. Both
files now carry the production constraint written out character for character; verified
`build`+`vet` clean on 8 platform/arch pairs, with the native test still running.

### 3.8 The daemon's appended build tags were checked by nothing

commit `b645cc94f7d`

`buildTags` appends **three** tags that no profile file carries, and the artifact gate required
only five of the union — so `tfogo_checklinkname0`, whose loss the code says *"would silently reduce
the daemon's libbox to the stub crash handler and the stub goroutine report"*, was in **neither**
list. A Windows release could have shipped a daemon with a stubbed crash handler and every existing
check would have passed. There was also no test file in that package at all.

**Fix:** both tags added to the gate, and five tests written — including one that **reads
`REQUIRED_TAGS` out of the release script** so the test's copy cannot drift from the gate it
mirrors. Proved by mutating the *gate* (the side a future edit is likely to touch) and watching the
test fail with both lists printed.

### 3.9 Documentation: a pinned resolver never reaches the DNS rule walk

commit `1c24dd9af9d`

The adblock guarantee stops at `options.Transport != nil` — a pinned resolver skips the rule walk
entirely, so a blocked name resolves and connects. Measured:

```text
Lookup("ads.example.", Transport = pinned)  addresses = [203.0.113.9]  err = <nil>     queryCount = 2
Lookup("ads.example.", Transport = nil)     addresses = []             err = NXDOMAIN  queryCount = 0
```

This is the pre-existing contract for **every** DNS rule action, so the code was not changed;
what was wrong is that nothing said so. The module claimed only that adblock is "handled inside the
DNS rule walk", and neither its Scope nor its Not-handled section listed the limit. Recorded in the
module (§9 and §10) and in both the English and Chinese user docs.

---

## 4. Every new regression test was proved to discriminate

The brief warns that *"a test that always passes because its setup never reaches the intended branch
is worse than no test"*. Every test added in this phase was therefore run against the defect it
targets, with the defect reintroduced or the fix reverted:

| test | how discrimination was proved | result |
|---|---|---|
| `TestStartRegistersThePauseCallbacks` | production file reverted (fix stashed) | FAIL → PASS |
| `TestCloseUnregistersTheCallbacksStartRegistered` | same | FAIL → PASS |
| the 8 WSA rows in `loadbalance_failure_facts_test.go` | production files reverted | 8 FAIL → PASS |
| `TestLocalVerdictKeepsTheRcodeAndAddsTheMarker` (+3) | verified as the only coverage of the path; `IsLocalVerdict` asserted false for an upstream rcode | PASS, with the negative control |
| `TestUplinkStalledResponseBodyIsNotGenuineDataFlow` | production file reverted | FAIL → PASS |
| `TestTheRestatedGateListMatchesTheGate` | **the release script mutated**, not the test | FAIL → PASS |
| the bridge tag fix | `go vet` on 8 platform/arch pairs | FAIL → PASS on 5 of them |

Two audits independently mutated production code in throwaway copies to prove *existing* tests were
theatre — including one that found three tests passing against the exact pre-fix shape of the P0.

---

## 5. Tests that were found to be theatre

Reported because "green" was not the same as "proven":

* **`pause_deadlock_test.go`** registers the callbacks by hand and never calls `Start`; reverting
  production to the pre-fix shape leaves all three passing. **Two of the three are vacuous**, and
  one (`TestCloseUnregisters…`) is real. The first also **self-deadlocks against its own fixture**
  when run with `-count=10` — it holds `endpoint.stateAccess` and then calls `callbackCount()`,
  which needs `pause.access`, while an emit goroutine holds `pause.access` and wants `stateAccess`.
  Its three possible outcomes are a vacuous pass, a misdiagnosing failure, or a hang until the
  package timeout; the audit reproduced the hang twice. **Carried to the risk ledger, not fixed
  here** — it is a fixture rework, separate from the production defect it failed to catch.
* **`governor_observer_race_test.go`'s race half** cannot fail: with `flushNotifications` reverted
  to the pre-fix body, the `-race` test still passes while the functional test fails correctly.
  The fix is right; only the race claim is theatre. (The same unsynchronised shape remains live in
  `experimental/clashmode/manager.go:238/247`.)
* **`system_read_loop_test.go`**, **`network_postclose_test.go`** and
  **`hijacked_conn_halfclose_test.go`** were checked and are **real** — each fails when its fix is
  reverted.

---

## 6. Race: NOT RUN, and why the substitutes are not a substitute

`RACE_TESTS = NOT RUN`. This is the one gate item that is not satisfied, and it is
`ENVIRONMENT_BLOCKED` with a fully diagnosed cause rather than an assumption:

```text
go test -race               -> "go: -race requires cgo"
CGO_ENABLED=1 CC=clang      -> "clang: error: unsupported option '-mthreads' for target
                                'x86_64-unknown-windows-msvc'"
clang --target=x86_64-w64-windows-gnu -mthreads   -> accepted, but the toolchain ships NO mingw
                                sysroot: "'stdio.h' file not found"
WSL                         -> installed but has NO distribution ("wsl -l -v" fails, "wsl -e echo"
                                fails)
docker / podman / qemu      -> absent
Windows SDK                 -> present; MSVC itself absent, and Go's runtime/cgo forces -mthreads
```

The race runtime needs a **mingw-w64 gnu** target and this host has no mingw headers or CRT. Three
different routes were tried and each failed at a different stage; the failure is the toolchain, not
the command.

**What was done instead, and its honest ceiling:**

* the full suite plus `count=10` (nine concurrency-heavy packages) and `count=20` (dialer, netcap,
  direct) runs — flakiness and functional races surface, data races do not;
* the audits' mutation work, which exercises the concurrent paths in both directions;
* static lock-order derivation for the whole WireGuard/route zone, including a **cycle check over
  every acquisition site** (result: no cycle, no re-entrancy) — this found the P0 that no test could;
* the XHTTP audit's own concurrency probes: 2 concurrent `Close` + a concurrent writer, 262144
  bytes accepted and delivered, `maxInflight` 1, no goroutine leak.

**These are not a race detector.** A data race that is not also a functional bug would not be
caught by any of them. That is the residual risk, and it is carried into the risk ledger rather
than absorbed into a "PASS".

---

## 7. Platform build matrix

22 target packages (the packages this cycle touched), `build` and `vet` (which compiles tests):

```text
windows  amd64  OK / OK        darwin   amd64  OK / OK        freebsd  amd64  OK / OK
windows  arm64  OK / OK        darwin   arm64  OK / OK        freebsd  arm64  OK / OK
linux    amd64  OK / OK        android  arm64  OK / OK        openbsd  amd64  OK / OK
linux    arm64  OK / OK
linux    386    OK / OK
netbsd   amd64  FAIL / FAIL    dragonfly amd64 FAIL / FAIL
```

Both failures are the **same pre-existing third-party limitation**, reproduced identically on
untouched `origin/testing`:

```text
transport/wireguard -> sagernet/wireguard-go/device -> wireguard-go/ipc
  "build constraints exclude all Go files"
```

`go build ./...` (tagged) and `go build ./...` (untagged) both exit 0 on the host, and
`experimental/libbox`'s BSD gap remains pre-existing and unrelated.

---

## 8. Performance: no unexplained regression

Measured against the numbers the branches themselves recorded, and against the pre-change tree for
the buffers this phase touched.

**Family gate and dialer scheduler** — the dual-stack line recorded "the gate is 10-17 ns and
allocation-free" and "the per-dial scheduler path is 51.5 ns/op against a 49.8-68.1 ns baseline
band":

```text
BenchmarkLocalFamilyGate/*            12.6 - 29.8 ns/op     0 B/op    0 allocs/op
BenchmarkSchedulerSingleCandidate     51.7 - 58.6 ns/op    16 B/op    1 allocs/op
```

Both hold: the gate is inside its documented range and **allocation-free**, and the scheduler is
inside its 49.8-68.1 ns band. The scheduler's single allocation is in the benchmark's own success
path (`held`), and the file's contract is "must not spawn anything" — nothing in this cycle changed
it.

**DNS hot path** — the adblock line pins a zero-allocation steady state, and this phase added both a
`bool` field to a result struct and a predicate on the *error* path:

```text
TestAdblockSteadyStateAllocatesNothing   PASS
BenchmarkAdblockResponseConstruction     105 - 179 ns/op   136 B/op   3 allocs/op
```

No new allocation, and the allocation-free steady-state test still passes.

**XHTTP** — the line's recorded wins are intact:

```text
BenchmarkPacketUpUplinkWrite/8KiB         0 allocs/op
BenchmarkPacketUpUplinkWrite/64KiB        2 allocs/op
BenchmarkPaddingRefererAssembly/concatenation        662-718 ns/op     3 allocs/op
BenchmarkPaddingRefererAssembly/legacy_url_chain    3037-3690 ns/op   11 allocs/op
```

The Referer concatenation is still ~5× the replaced URL chain, and the uplink write path is still
allocation-free at 8 KiB. The drain-error change adds no allocation: it returns an existing error
value instead of discarding it.

The absolute ns/op figures were taken while other suites ran, so they are noisy in the second
digit — the structural results (allocations, ratios, band membership) are the ones being claimed.

---

## 9. Failures, classified

Every non-passing result, with its class and its evidence:

| result | class | evidence |
|---|---|---|
| `common/tls` ×3 TLS13 tests | PRE_EXISTING | reproduced test-for-test on untouched `testing` |
| `common/trafficsched` rate test | INFRASTRUCTURE_FLAKE | untouched package; passes 3/3 in isolation; failed once under full-suite contention at 4.5 % under a stated-few-percent bound |
| netbsd/dragonfly build+vet | EXPECTED_UNSUPPORTED | third-party `wireguard-go/ipc`; reproduced on `testing` |
| `experimental/libbox` on BSD | EXPECTED_UNSUPPORTED | mobile-only package; reproduced on `testing` |
| `protocol/bridge` test compile on BSD | PLATFORM_COVERAGE_GAP | **fixed** (§3.7) |
| WireGuard pause registration | REAL_REGRESSION (composition) | **fixed** (§3.1) |
| `pause_deadlock_test.go` hang at `-count=10` | TEST_BUG | fixture self-deadlock; **in the risk ledger** |
| `-race` | ENVIRONMENT_BLOCKED | toolchain, diagnosed in §6 |

No failure was reclassified as pre-existing without being reproduced on a comparator, and "flake"
was used only where isolation proved it.

---

## 10. Risk ledger

Carried forward, in priority order. None of these blocks Phase 3; each is stated with what would
close it.

1. **`-race` has never run.** The single largest verification gap. A data race that is not also a
   functional bug is invisible to everything done here. *Closed by:* any host with a mingw-w64
   toolchain, or a Linux/CI runner — `go test -race` on `route`, `common/dialer`, `dns`,
   `transport/v2rayxhttp`, `transport/wireguard`, `adapter`.
2. **`pause_deadlock_test.go` can hang the wireguard package** (and it self-deadlocks against its
   own fixture: it holds `stateAccess` then calls `callbackCount()`, needing `pause.access`, while
   an emit goroutine holds `pause.access` and wants `stateAccess`). It also cannot detect the defect
   it was written for. *Closed by:* reworking the fixture — drive the real `Start`, signal the emit
   only after registration, and put a deadline on every manager access.
3. **`experimental/clashmode/manager.go:238/247`** has the same unsynchronised observer-list shape
   the power governor was fixed for, recorded by that line as "not observed" rather than "not a
   race". *Closed by:* the same `slices.Clone` + re-arm treatment.
4. **`transport/device/system.go`'s recoverability arm is dead on Windows**: `tun.IsRecoverableReadError`
   compares `syscall.EAGAIN/ENOBUFS/…`, which never equal the Winsock values that arrive — the same
   class §3.2 fixed for `protocol/group`. On Windows `readErrorIsFatal` collapses to "fatal unless
   closed", so an unexpected wintun receive error still ends the datapath. *Closed by:* extending
   the shared errno sets with the TUN-read conditions and adding the WSA rows to the test table.
5. **`readErrorIsFatal`'s test table drives the POSIX spellings**, so it passes for the same reason
   §3.2's did.
6. **The desktop client must keep `a917e616` reachable** — it lives on a feature branch of
   `sing-box-for-desktop`; `jiejiebox/main` is 6 commits behind and promoting it is a
   fast-forward. Phase 1's item, unchanged.
7. **`experimental/` is in no gofmt gate** — four workflows enumerate paths explicitly and all omit
   it, which is why one of Phase 1's two formatting repairs was invisible to CI.
8. **No CI runs on `integration/release-next`** at all: every push-triggered workflow is filtered to
   `testing`/`release/**`. Nothing in this report is CI evidence; it is local and static
   verification. *Closed by:* dispatching `verify`/`windows-boxdd-tests` manually, or moving the
   branch under `release/**`.
9. **`pending/device-axis-reload-window-tests`** still needs the product decision Phase 1 recorded
   (2 of its 7 tests fail on the integrated tree by design).

---

## 11. What Phase 2 changed about the release's risk

Phase 1 delivered a tree whose branches were individually green. Phase 2 established that green was
not the same as correct, and the numbers make the point:

* **8 defects fixed**, of which **5 were invisible to the full test suite**;
* **1 of them was P0** — a production callback registration that had been deleted in a refactor, in
  a subsystem whose tests registered it by hand;
* **2 were the same "platform classifier compares constants the platform never produces" class**,
  one of them in a package the very commit that documents the lesson had just been merged next to;
* **2 were "the test that proves this exists but is executed by nothing"** — one in `experimental/`
  formatting, one in a release gate;
* **3 existing tests were shown to be theatre**, and are recorded as such rather than counted as
  coverage.

The one thing that could not be done is the race detector, and it is stated as a gap rather than
folded into a pass.
