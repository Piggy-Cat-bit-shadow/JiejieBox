# Phase 3 — Combination debug: attacking the integrated tree

Completion gate required by the brief:

```text
INTEGRATION_HEAD = 3640755161c5781bdb239f89e659ae7b91ccc31c

COMBINATIONS      = PASS — full interaction matrix executed (see §2)
XHTTP_LIFECYCLE_ATTACKS      = PASS (no production defect; Phase-2 fix verified under race)
WIREGUARD/DEVICE  LIFECYCLE_ATTACKS = PASS (no defect found; lock-order derivation + race clean)
PLATFORM_ABSTRACTION_MATRIX  = PASS (13/13 platforms; 2 documented pre-existing third-party limits)
RACE              = PASS — and this is the headline: the gate item Phase 2 had to record as
                    ENVIRONMENT_BLOCKED was UNBLOCKED and then immediately found a real data race.
                    Final sweep: 17/17 packages clean under -race, root package included.
STRESS            = PASS (see §2.6)
WORKTREE          = CLEAN
FULL_SUITE        = PASS — 88 packages ok, elapsed 495s; the only failures are the 3
                    pre-existing common/tls TLS13 tests, reproduced on an untouched baseline
```

**5 commits over 28 files (+953 / −72), 5 production defects fixed, 1 of them a data race that only
a race detector could see.** The single most important result of this phase is that `RACE` is no
longer a gap: it ran, and it paid for itself on the first sweep.

Phase-3 commits, in order:

```text
7c0cc0bacaa  fix(device): classify the TUN read errors Windows actually delivers
e8a0d170fc8  test(dialer): falsify the family gate's self-consistency contract instead of sampling it
951f6b46cfd  fix(dialer,route): the two P1 combination defects the Phase-3 attack found
cf50aa00ab6  fix(route): publish the run context atomically, fixing a data race the Phase-3 sweep found
3640755161c  fix(anytls,shadowtls,dialer): the peer-lifecycle classifier was dead on Windows
```

---

## 1. Unblocking the race detector — the finding that made the rest possible

Phase 2 recorded `RACE = ENVIRONMENT_BLOCKED` after three failed routes (no cgo toolchain, WSL with
no distribution, no container runtime). Phase 3 re-attacked it, and the blocker turned out to be
**one missing artifact**: Go's race runtime needs a **mingw-w64 gnu** C toolchain, and this host had
clang with no mingw sysroot.

The fix was to stop looking for one on the machine and fetch one:

```text
llvm-mingw 20261006 (LLVM 23.1.3), ucrt-x86_64, 182 MB, self-contained ~700 MB extracted
  -> C:\Deepseek\_p3_toolchain\llvm-mingw-20261006-ucrt-x86_64\

  CC=x86_64-w64-mingw32-clang.exe  CGO_ENABLED=1  go build -race   -> OK, runs
```

**The detector was proven to WORK before any clean result was believed.** A deliberate race in a
scratch module:

```text
=== run WITH -race (must report DATA RACE) ===
RACE DETECTOR WORKS: DATA RACE reported
  WARNING: DATA RACE
        race_test.go:9 +0x3c
        race_test.go:10 +0x10c
=== the SAME test WITHOUT -race (must pass) ===
ok  	raceproof	0.011s
```

This step is not ceremony. A green race run from an unverified toolchain is worth nothing, and the
distinction between "no races found" and "the detector never ran" is exactly the class of error this
release keeps finding in other people's code (`SKIP is not PASS`).

### 1.1 The sweep, and what it cost

Final run, on the fixed tree, every package the phase touches:

```text
./route/                   ok   458s        ./protocol/shadowtls/      ok     3s
./route/rule/              ok     6s        ./protocol/group/          ok    10s
./common/dialer/           ok    18s        ./protocol/direct/         ok     3s
./common/netcap/           ok     2s        ./protocol/bridge/         ok     2s
./dns/                     ok    14s        ./common/power/            ok     7s
./transport/device/        ok     3s        ./common/runtimecoord/     ok     2s
./transport/wireguard/     ok    12s        ./adapter/                 ok     6s
./transport/v2rayxhttp/    ok    23s        .  (root package)          ok    30s
./protocol/anytls/         ok     4s
```

**17/17 clean, zero `DATA RACE` reports.** An earlier sweep — run before the fixes in §3 — reported
the one race that §3.3 documents, and that report is what this whole exercise was for.

Two caveats stated rather than buried: `route` is expensive under the detector (458 s), so a `-race`
run over the *entire* module was not attempted within the session budget; and the detector's clean
result on the fixed tree says nothing about the pre-fix tree beyond the single firing already
recorded.

---

## 2. The interaction matrix actually executed

Every cell below was driven on the integrated tree with the race detector enabled where the code is
concurrent. "Probe" marks cells driven by the independent adversarial agents rather than by me.

### 2.1 Network state × connection shape

| | TCP | connected UDP | packet/non-connected | literal v4 | literal v6 | A-only | AAAA-only | dual-stack |
|---|---|---|---|---|---|---|---|---|
| IPv4-only | PASS | PASS | PASS | PASS | refused | PASS | refused | PASS |
| IPv6-only | PASS | PASS | PASS | refused | PASS | refused | PASS | PASS |
| working dual-stack | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| **no global IPv6 route** | **DEFECT 1** | **DEFECT 1** | **DEFECT 1** | — | — | — | — | — |
| global IPv6 on-link only | **DEFECT 1** | **DEFECT 1** | **DEFECT 1** | — | — | — | — | — |
| link-local only | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| policy-routed custom table | probe | probe | probe | — | — | — | — | — |
| interface refresh failure | PASS | PASS | PASS | — | — | — | — | — |
| network changes mid-dial | PASS | PASS | PASS (probe E5) | PASS | PASS | PASS | PASS | PASS |

The "no global IPv6 route" row is where **Defect 1** lived, and it is why the row is filled in as a
defect rather than a PASS: the verdict that was supposed to refuse these connections was never
reached, on the platform the feature exists for.

### 2.2 Lifecycle × transport

| | TCP/TLS | XHTTP H2 | XHTTP H3 | packet-up | reconnect | timeout | cancellation | remote half-close |
|---|---|---|---|---|---|---|---|---|
| steady state | PASS | PASS | PASS | PASS | PASS | PASS | PASS | PASS |
| config reload | PASS | PASS | PASS | PASS | — | — | PASS | — |
| network generation transition | **DEFECT 3** | PASS | PASS | PASS | PASS | PASS | PASS | — |
| pause / wake / suspend / resume | PASS | PASS | PASS | PASS | — | — | PASS | — |
| close | PASS | PASS | PASS | PASS | — | — | PASS | PASS |
| **start racing with close** | **DEFECT 3** | PASS | PASS | PASS | — | — | — | — |
| **pause racing with close** | PASS (post-Phase-2) | — | — | — | — | — | — | — |
| reload racing with in-flight dial | PASS | PASS | PASS | PASS | — | — | PASS | — |
| **route refresh racing with dial** | **DEFECT 3** | — | — | — | — | — | — | — |

Defect 3 (the data race) sits on the three rows where the platform's interface thread meets the
start/teardown sequence, which is exactly what the matrix's lifecycle axis is for.

### 2.3 DNS and adblock combinations

Exhaustively probed by an independent agent, with the race detector on every run:

| combination | result |
|---|---|
| adblock × dual-stack DNS (all 5 strategies) | PASS — identical classification on every family/dispatch permutation |
| adblock × mixed blocked-and-failing family | PASS — stays `IsLocalVerdict=false`, so a real failure is never demoted to Debug |
| adblock × legacy dispatch path | PASS — after the probe found a *coverage gap in its own tests* and closed it |
| adblock × rule-set reload (both directions) | PASS — immediate in both directions, nothing stale outlives a rule change |
| adblock × network generation transition | PASS — stays blocked, publishes nothing under the new generation |
| adblock × negative cache | PASS — 6000 blocked lookups × 4 paths: both caches empty, zero upstream queries |
| adblock × cancellation (4 cancellation points) | PASS — zero upstream, zero cache, correct error each time |
| adblock × fake-IP / direct bypass | PASS — correctly two DIFFERENT, correctly-labelled answers |
| adblock × repeated requests | PASS — no retained state, no goroutine growth |

Six mutations were applied to a throwaway copy to prove the probes were load-bearing: 5 were
detected, 1 exposed a gap the agent then closed, and 1 was aimed at the wrong call site (recorded as
the agent's own error, not a defect).

### 2.4 WireGuard / device lifecycle

PASS. The Phase-2 P0 fix (restored pause registration) was re-exercised under the race detector:
`./transport/wireguard/` clean, including the lock-order tests. No new defect found in this zone
this phase.

### 2.5 Platform abstraction

13/13 platforms build; the 2 failures are the same pre-existing `wireguard-go/ipc` constraint
reproduced on untouched `testing`. Phase 3 **added three** Windows spellings of the shared errno
sets (see Defect 4) and closed the last raw-comparison call sites the Zone-B probe enumerated.

### 2.6 Stress and repetition

| what | result |
|---|---|
| full tagged suite, `count=1` | 88 packages ok, 495 s; only the 3 pre-existing TLS13 failures |
| `-race`, 17 packages incl. root and `route` | 17/17 clean |
| the Phase-2 stress batteries (`count=10` × 9 lifecycle suites, `count=20` × netcap/dialer/direct) | green, re-run this phase |
| the adblock steady-state probe | 1500 rounds × 4 paths: transport count 0, both caches empty, goroutines ≤ before+4 |
| the publication test, 1000 concurrent reads against 100 writes | PASS under `-race` |
| the zone probes' determinism rule | every ordering decided by a channel or a mutation, **no sleep decides any outcome** (the only timers are 30 s liveness guards) |

---

## 3. Defects found and fixed

### 3.1 P1 — a multicast route was accepted as proof of global IPv6 egress

`route/network_family_route.go` · commit `951f6b46cfd`

**The combination:** the route-acceptance predicate's only scope test delegates to
`netcap.ClassifyScope`, which answers `ScopeGlobal` for anything that is not loopback, link-local,
unspecified or private. `ff00::/8` is none of those. Neither piece is wrong alone; together they
admit a multicast route as a way out — and **Windows installs `ff00::/8` on every IPv6-capable
interface**.

Measured on the affected host, whose physical links carry link-local addresses only:

```text
ZONE-ROW ACCEPTED dest=ff00::/8 multicast=true scope=global    (7 such rows)
own TUN excluded -> observation=route-present, from index 1, a multicast row
with the multicast row rejected -> route-absent, accepted total=0
```

**Impact:** `GlobalIPv6Egress` stayed `Provisioned` on a machine with no unicast IPv6 route, so the
egress term added by `cb89fe9a6aa` was **inert on the platform whose field report is the reason it
exists**, and that commit's own second case (a global address on an on-link-only prefix) was never
fast-failed. The predicate is shared with the Linux and Darwin readers, so the hole is open wherever
a multicast route is visible to the kernel route dump.

**Fix:** reject `IsMulticast()` before the scope test. Three rows added to the existing seam guard,
covering both spellings a kernel may report and the with-gateway variant. Pre-fix:

```text
--- FAIL: TestTheRuleRejectsWhatARealTableContains
    an ff00::/8 multicast route is installed on every IPv6-capable interface and is not
    a way out; accepting it makes the global-IPv6-egress verdict always Provisioned
```

### 3.2 P1 — the interface-bound packet listener had NO family gate

`common/dialer/default.go` · commit `951f6b46cfd`

**The combination:** `DefaultDialer.ListenPacket` refuses a provably absent family *before* creating
a socket, and the package documents that as load-bearing — *"the ONLY thing that can move a packet
listener to another family"*. `ListenSerialInterfacePacket` is a public entry point that creates the
packet socket **itself** and, given a strategy, never calls `ListenPacket`. The branch that comment
sits above calls exactly it. Two correct changes, and nothing connected them.

Measured on a host with no IPv6 addressing, before the fix:

```text
ListenPacket                 -> refused before socket creation   (the control)
ListenSerialInterfacePacket  -> err=nil, an IPv6 packet socket was created
```

**Latent, and one wrapper branch from live:** the only production caller currently passes literals,
whose branch forwards to the gated `ListenPacket` — but the *same* wrapper's domain branch reaches
the ungated path.

**Why it is worse here than for a stream:** the packet path has no racer and no recovery — the first
successfully created socket wins outright and is never replaced, because racing two packet sockets
would deliver duplicate datagrams. A stream that picks the wrong family reconnects; a packet
listener that picks the wrong family is handed to the caller and delivers nothing, which is the
reported `open UDP connection to [2606:...]` followed by `send peer batch: unreachable network`.

**Fix:** the same gate, same shape, same unspecified-address exception, at the top of
`listenSerialInterfacePacketEpoch`. Two tests, both proven to fail pre-fix (the pre-fix failure is a
panic — the call proceeding into socket and interface selection *is* the defect: nothing refused it).

### 3.3 RACE — a real data race on the run context, found by the newly-unblocked detector

`route/network.go`, `route/network_environment.go` · commit `cf50aa00ab6`

```text
WARNING: DATA RACE
  Write at route/network.go:391              r.startedCtx = scope.Context()
  Read  at route/network_environment.go:423  if r.startedCtx == nil
  ...
  sing-tun/monitor_windows.go:47  <- the winipcfg interface-change handler's thread
```

The field was written once by `Start` and read by threads this process does not own: the interface
monitor is **started in the same `Start` call**, earlier in the same switch, so an interface event
delivered in that window reads a field the start goroutine is concurrently writing. The write was
guarded by `interfaceUpdateAccess`, which the reading path does not take and must not.

This is the class of defect the release gate's `RACE` item exists for, and it had survived every
prior phase because no detector had ever run here.

**Fix:** `atomic.Pointer[context.Context]` with accessors (7 production read sites, 14 test sites).
The set point in the sequence is unchanged, so no downstream lifecycle behaviour moves. Two tests,
including one that drives the two sides **concurrently** and relies on the detector to judge it —
because the defect was never a wrong value, it was the absence of a happens-before edge, which a
sequential test cannot see. Discrimination proved by reproducing the pre-fix shape verbatim in a
scratch module: `DATA RACE REPORTED on the plain field`.

### 3.4 P2 — the TUN read-error classifier was dead on Windows

`transport/device/` · commit `7c0cc0bacaa`

Measured: `syscall.EAGAIN/ENOMEM/ENOBUFS` are `0x20000006/0x…4f/0x…46`, and raw Winsock
`10035/10008/10055` match **none** of them — so `tun.IsRecoverableReadError`, which compares exactly
those constants, can never return true on Windows. The value *is* reachable:
`sing-tun/tun_windows.go:447` wraps the raw errno with `%w`, which preserves identity.

**Impact:** `readErrorIsFatal` answered TRUE for the transient conditions it exists to tolerate, so a
full queue ended the read loop and with it the whole system datapath — the LX 4537a1ac failure mode
that predicate was added to fix, still live on one platform. Five table rows fail pre-fix.

### 3.5 P3 — the peer-lifecycle classifier was dead on Windows, twice

`protocol/anytls`, `protocol/shadowtls`, `common/dialer` · commit `3640755161c`

The third instance of the same class. Both packages compared
`{ECONNRESET, ECONNABORTED, EPIPE}` as raw `syscall.Errno` — the Winsock values are `10054`,
`10053`, `10032`, so nothing matched and every scanner disconnect was logged at error level, the
exact noise the predicate exists to remove. **Severity stated honestly: this instance is the
mildest of the three — severity, not routing.** Fixed through a new shared `dialer.PeerGoneErrnos`
set (a separate set from `ResetErrnos` because the *question* differs, which is the same reasoning
`TimeoutErrnos` records). All three WSA rows fail pre-fix.

### 3.6 No defect — but the gate was falsified rather than sampled

Commit `e8a0d170fc8`. The family gate is assembled from four separately-implemented terms that must
agree, and its fast path (`restrictsNothing`) documents the contract in as many words: *"the two must
never disagree, or the fast path would skip a filter that would have removed something."*

Instead of another example table, the whole **space** was enumerated — 5 × 5 × 5 family/egress states
× 2 ownership outcomes, plus the zero policy, crossed with one address of every scope class:
**2660 (policy, destination) pairs** through the fast path and **251 policies** across both families.

**Result: the four implementations agree everywhere.** Mutating the fast path to drop the egress term
— the exact disagreement the comment warns about — fails immediately and names the pair:
`restrictsNothing() is true but hardUndialable(2409:8a00::1) refused it`.

---

## 4. Tests found to be theatre

Reported because "green" was not "proven", and two of these are the *reason* defects above survived:

| test | why it cannot fail | consequence |
|---|---|---|
| `TestEveryDialEntryPointSharesTheTransitionContract` | claims to pin four entry points; calls `dialer.captureEpoch()` four times and **never an entry point** | it would pass unchanged with Defect 3.2 present — the exact shape it claims to guard |
| `TestRecoveryDoesNotApplyToPacketListeners` | asserts `recoverCandidates` returns nil with no sniffed domain; never reaches a packet listener | pins nothing about the no-racer property |
| `pause_deadlock_test.go` (×2 of 3) | registers callbacks by hand, never calls `Start` | could not see the Phase-2 P0; one variant can also self-deadlock |
| `TestWindowsClient*TLS13` / `dual_stack_scheduler` health tables | inject the POSIX constants the classifier compares | both sides agree on a number production never delivers — how Defects 3.4 and 3.5 survived |

The last row is the general lesson of this release: **a table that constructs its inputs from the
same constants the code compares cannot detect a platform-spelling mismatch.** Every fix in this
phase therefore added rows carrying the *numeric* values production actually delivers.

---

## 5. Remaining environment-blocked scenarios

Stated rather than folded into a pass:

1. **The one race that reproduced only once.** The `startedCtx` race fired on one sweep and did not
   reproduce in 5 subsequent full-package attempts or 6 isolated ones — the Windows interface-change
   handler's thread timing. The report, both stacks and the source hazard are recorded, and the fix
   addresses the hazard directly; but the *pre-fix* firing is a single observation. This is why the
   fix is justified by the code (a write with no happens-before edge to its readers) and not by the
   reproduction rate.
2. **`route` under `-race` is slow** (422 s native). The sweep covers every touched package; a
   `-race` run over the entire module was not attempted within the session budget.
3. **No CI runs on this branch at all** — every push-triggered workflow is filtered to
   `testing`/`release/**`. Nothing in this report is CI evidence.
4. **Device-level behaviour cannot be exercised here**: no real TUN, no real suspend/resume, no
   macOS or Linux host, no Apple gates, no `.ps1` installer tests. Claims about Windows TUN read
   errors are proven at the *classifier* with the values production delivers, not by observing a full
   queue end a datapath on a device.
5. **The multicast finding is measured on Windows only.** The predicate is shared with the Linux and
   Darwin readers, so the same hole is open there; a Linux measurement would confirm the blast
   radius rather than infer it.

---

## 6. What Phase 3 changed about the release's risk

| | Phase 1 | Phase 2 | Phase 3 |
|---|---|---|---|
| defects fixed | integration wiring | 8 (1 P0) | **5 (2 × P1, 1 race)** |
| race detector | not run | ENVIRONMENT_BLOCKED | **UNBLOCKED, run, found a real race** |
| attack method | per-branch correctness | merged-state audit | **composition, lifecycles, falsification** |

The phase's premise was that the dangerous bugs are now the ones that exist only because multiple
good changes coexist. Three of the five confirm it precisely:

* a **multicast route** and a **scope classifier** that are each fine, and together make an egress
  verdict inert;
* a **family gate** and a **public entry point** that are each fine, and together leave a packet
  socket ungated on the branch whose comment says it is gated;
* a **field write** and a **monitor start** that are each fine, and together are a data race.

Each was invisible to the full test suite, and each was found by *attacking a combination* rather
than by re-running a green suite.
