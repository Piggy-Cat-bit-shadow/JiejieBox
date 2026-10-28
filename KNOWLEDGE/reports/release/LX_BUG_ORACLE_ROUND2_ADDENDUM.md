# LX Bug Oracle — Round 2 Addendum

> Historical audit at the SHAs below, not a current defect list. In particular, the
> `FakeIPSaveMetadata` unconditional-success findings in §E and §G were subsequently fixed: at
> `011f3fdcd99`, `experimental/cachefile/fakeip.go` returns `c.flush()` and
> `TestFakeIPSaveMetadataReportsAFailedFlush` exercises the failure. This source/test observation
> does not establish that every other finding in this archive has been resolved.

Companion to the Round-1 A–G report. Same branch, same worktree.

```text
BASE_BRANCH = testing
BASE_SHA    = c32c210b0dbb1a23fb9b19ef731d164cd8745d34
WORK_BRANCH = audit/lx-bug-oracle-core-hardening
FINAL_SHA   = dfb93cb81a6194f002a4a356657e59767c592783
COMMITS     = 9   (all local; PUSHED = NO; nothing merged, no tag, no version change)
TREE        = CLEAN
```

Round 1 closed 6 bugs. Round 2 closed 2 more and **corrected one of its own audit
findings by measurement**. Diff vs base: 18 files, +2185 / −48.

---

## B (continued). Confirmed bugs fixed in round 2

### B7. A malformed persisted FakeIP cursor destroyed the entire cache file — `5ab370cea27`

| | |
|---|---|
| **Historical oracle** | `474aa5b5` (corrupt cache crashed the client) + `080f1c56` ("adding a recover is not the same as being safe") |
| **Current trigger** | A `fakeip_metadata` value shorter than its own length prefix. Measured minimal input: the three bytes `00 01 00`. |
| **Root cause** | `adapter/fakeip_metadata.go` `UnmarshalBinary` declares an `error` return and then calls `common.Must(binary.Read(...))` — which **panics** — on the one failure it is most likely to meet. |
| **Blast radius** | The panic escapes the caller's bbolt transaction, and `experimental/cachefile/cache.go`'s `view`/`batch`/`update` answer a panic inside a transaction by calling `resetDB` → `filemanager.Remove(c.path)`: the **whole file** goes, taking the DNS cache, rule-set cache, persisted selector choices and Clash mode with it. |
| **Second-order harm** | `FakeIPMetadata()` then returns nil, the store takes the "no metadata" branch and resets its cursor **without recording a retired generation**, so addresses still live for one domain are re-issued to another — the exact hazard `experimental/cachefile/fakeip.go:23-33` records as already fixed. |
| **Regression test** | `experimental/cachefile/fakeip_metadata_corruption_test.go` — 6 malformed shapes over a real file that also holds an **unrelated bucket**, plus a blast-radius test that also asserts the file still exists on disk, plus a positive control separating "empty store" from "malformed value" |
| **Pre-fix falsification** | **PASS→FAIL verified**: 3 of 6 shapes fail with *"a malformed FakeIP metadata value destroyed an UNRELATED cache bucket"* |
| **Fix** | Return the `binary.Read` error, wrapped. The overrunning-length case was already handled by the `io.ReadFull` below it. |
| **Isomorphic sweep** | `adapter/fakeip_metadata.go` is the only `common.Must` on this path; the rest of the `recover()` chain was audited (see E). |

### B8. Power governor: observer slice race **and** a permanently lost reuse boundary — `dfb93cb81a6`

| | |
|---|---|
| **Historical oracle** | `b5d5338e` family (async notification delivering into a component that is being changed underneath it) |
| **Current trigger** | An observer registered while a transition is being delivered. Realistic: `route/reference.go` calls `AddReuseObserver` from its scope start and `box.go` calls `AddObserver` during construction — both can run while a platform power callback delivers. |
| **Root cause (two, on the same lines)** | `flushNotifications` (1) copied the slice **header** under the lock and walked the backing array **after** unlocking, so a concurrent `AddObserver` append writes the memory the walk reads — the defect the fork already documented and fixed for the urltest update hooks (`common/urltest/hook_race_test.go`: *"the slice header and its backing array were therefore read and written concurrently"*, fixed by copying under the lock at `urltest.go:303-305`); and (2) cleared `pendingNotify`/`pendingReuse` **before** the walk, so an observer registering during it is told nothing and has no debt left to be paid. For a **reuse boundary** that is terminal: `ReuseEpoch` has already moved and the boundary is never republished. |
| **Regression test** | `common/power/governor_observer_race_test.go` — a 400-transition × 3-registrar race test, and a deterministic lost-notification test |
| **Pre-fix falsification** | **PASS→FAIL verified** for the functional half: *"an observer registered during a notification was never notified: the debt was cleared before the walk"*. The `-race` half did **not** fire in this window and is claimed as structural, not measured. |
| **Fix** | `slices.Clone` under the lock; a pass that observes a registration land re-arms the debt and delivers again; bounded at 8 passes so an observer that registers another on every delivery cannot loop on a platform callback thread. |
| **Why safe** | The repeat is conditional on an actual registration (list **length** under the lock), so an ordinary transition still delivers exactly once — a duplicated edge would be a duplicated retire-and-redial. |
| **Isomorphic sweep** | `common/urltest/urltest.go:303-305` already copies. `experimental/clashmode/manager.go:238` has the same slice-header snapshot; **not changed** — its elements are channel-backed `observable.Subscriber`s with non-blocking `Emit` and it is the control plane, not the device axis. Recorded as a follow-up. |

---

## C (continued). Corpus classification added in round 2

| Candidate | Status | Current evidence | Action |
|---|---|---|---|
| `474aa5b5` corrupt cache file crashes at startup | **BUG_CONFIRMED_FIXED** | the file-level shapes were already handled (9 damage shapes measured CLEAN); the **value-level** shape was not | **B7** |
| `080f1c56` the `recover()` itself is wrong | **BUG_CONFIRMED_FIXED** (the actionable half) | the chain is over-broad by design and stays that way; the change removes the panic that was feeding it pathological input | **B7**, E |
| `886422d2` FakeIP reset / missing bucket | ALREADY_COVERED | `FakeIPReset` guards all three deletions with `tx.Bucket(name) == nil → continue`; empty and partial stores both return nil | none |
| `bea01fe8` HTTP proxy early-response panic | **NOT VERIFIABLE HERE** | lives in `sing/common/badhttp`-adjacent code in the dependency, not in this repo | reported |
| `6a7f11fc` truncated JSON comment scanner | **NOT VERIFIABLE HERE** | lives in `sing/common/json/comment.go` | reported |
| `bd602b6d` websocket handshake panic | ALREADY_COVERED | `transport/v2raywebsocket/client.go:84-89` error-checks and returns `websocketConn, nil` | none |
| `ee808c46` package manager unavailable panic | **NEEDS_RUNTIME_PROOF** | `protocol/tun/inbound.go:595` passes `t.networkManager.PackageManager()` with no guard; `route/network.go:294-313` leaves it nil when `Start()` fails (warn only). Accidentally safe on the libbox path, because a **typed-nil** `(*platformInterfaceStub)(nil)` makes `platformInterface == nil` false. Android-only. | reported |
| `288411b0` interface monitor unavailable | **NEEDS_RUNTIME_PROOF** | 6 sibling sites guarded; `common/dialer/default_parallel_interface.go:233` was missed. Currently closed because the only platform interface that sets `UsePlatformNetworkInterfaces()` also sets `UsePlatformDefaultInterfaceMonitor()`. | reported |
| `d9149321` nil `RemoteAddr()` | ALREADY_COVERED | every consumer routes through `M.AddrFromNet`/`AddrPortFromNet`/`SocksaddrFromNet`, which nil-check in the pinned `sing` | none |
| `65b17425` security dependency audit | **DEPENDENCY_COVERED / not reachable** | grpc **1.79.1** (CVE-2026-33186 fixed); go-jose **4.1.3**, **indirect** — `go mod why` shows the only path is cloudflare-tunnel OIDC token **verification** (JWS, not JWE), and the repo never imports it (`grep 'go-jose\|jose\.\|jwt\.'` → 0 matches). Bump to 4.1.4 recommended, not reachable. | reported |

---

## D (continued). New discoveries in round 2

1. **A decoder that panics feeds a recovery chain that deletes user data.** The chain is not wrong to reset a corrupt store; the defect is that a *data* problem was raised as a *programming* problem one layer down. The same shape should be checked anywhere a `Must`/`panic` sits on a path fed by a persisted or network value whose caller has an error return. (B7)
2. **Observer/notification primitives are the fork's most consistent blind spot.** Three siblings, three different states: `interrupt.Group` is correct (collect-then-close), `urltest` is fixed and has a test that names the exact defect, `power.Governor` was still walking an unlocked slice **and** losing notifications, `clashmode.Manager` still snapshots a slice header. A single "copy under the lock, deliver outside, repeat if the list grew" rule would cover all four. (B8)
3. **An over-broad `recover()` converts a bug into data loss.** The three cachefile recoveries catch everything and answer with `remove(path)`. A nil-map write in *any* closure that runs inside a bbolt transaction is therefore reported as "database corrupted" and costs the user their DNS cache, rule-set cache and selections. Not fixed (narrowing it risks re-raising real corruption), but it is why B7's impact was so large, and it is the general form of `080f1c56`.

---

## E (continued). Architecture findings

- **No panic containment in the datapath.** There is no `recover()` anywhere in `protocol/**`, `transport/**` or `route/**`; the 12 production recoveries are three in `experimental/cachefile`, two gRPC interceptors in `daemon/server_recover.go`, four in `protocol/group`, and three in `common/physicalpath`. Every one of them is correctly scoped for its purpose. Consequence: any established panic reachability in the datapath is an immediate whole-process kill.
- **`common/dialer/default_parallel_network.go:17,111,138` still contains `panic("invalid usage")`** on a dial path, enforced only by its callers' guards. Latent; a fourth caller kills the daemon.
- **The `pause.Manager` primitive still invokes callbacks while holding its own lock** (round-1 B1 fixed this fork's only violating consumer). See §G.
- **`experimental/cachefile.FakeIPSaveMetadata` always returns nil** (`fakeip.go:58-66`) while its caller `dns/transport/fakeip/store.go:231-234` branches on the error. The branch is unreachable, so a failed write silently loses the reservation. Reported, not fixed — the fix is in the pending/flush path and needs its own falsification.

## F (continued). Tests

| Added in round 2 | Result |
|---|---|
| `experimental/cachefile/corrupt_file_robustness_test.go` (9 damage shapes + recovery usability) | **PASS** |
| `experimental/cachefile/fakeip_metadata_corruption_test.go` (6 value shapes + blast radius + control) | **PASS** (pre-fix FAIL) |
| `experimental/cachefile/empty_key_sweep_test.go` (measured precondition + latent guard + sweep control) | **PASS** |
| `common/power/governor_observer_race_test.go` (2 tests) | **PASS** (pre-fix FAIL) |
| `go test -race ./experimental/cachefile/ ./adapter/ ./dns/transport/fakeip/ ./common/power/ ./route/ .` | **PASS** |
| `go build ./...` + darwin/linux cross-compile | **PASS** |

## G (continued). Unresolved high-value findings

1. **`sing-tun` IPv6-only accept-loop self-heal is still missing from the pin** — unchanged from round 1, still the highest-value item, still `PENDING_UNMERGED_PRIOR_WORK` (`d092c64`, with its production-path regression test already written).
2. **The `sing` pause primitive still invokes callbacks under its own lock** — unchanged.
3. **`experimental/clashmode/manager.go:238`** snapshots a slice header and walks it unlocked — same shape as B8, lower risk (non-blocking channel sends). Not fixed.
4. **`FakeIPSaveMetadata` can never report failure** — the reservation is silently lost on a failed flush.
5. **`dns/transport/fakeip/store.go:119-122`** validates only the two RANGES when restoring a persisted cursor; a hand-edited value with matching ranges and an invalid cursor is accepted, makes every query for that family fail, and is written back on `Close` so it survives restarts. Reachable from an edited file; needs its own repro before changing.
6. **`FakeIPLoadDomain` returns on-disk addresses with no range/family check** — untrusted disk state directly controls a DNS answer.
7. **TLS/QUIC SNI bare-literal case** — fix is in, the bare-literal input could not be reproduced in-process (round 1, B6).
8. **`https_transport.go` permanent DoH→HTTP/1.1 latch** — upstream-identical; reported, not fixed.
9. **`URLTest.performUpdateCheck` holds `updateAccess` across `Interrupt()`** — invariant violation, no cycle established.
10. **Four neighbour-monitor loops** `continue` on any non-timeout error with no backoff — family-B shape, permanence unproven.

## Corrections to this round's own audit

- **A reported startup panic is NOT reachable, and that was measured, not argued.** The on-disk audit reported that the startup bucket sweep's `name[0]` (`experimental/cachefile/cache.go:240`) indexes a zero-length key and kills the process. Tested directly: **bbolt refuses to create a zero-length top-level bucket name** (`"bucket name required"`), so `tx.ForEach` can never hand the sweep an empty key. The first version of my own probe reported the opposite, because it treated `tx.Bucket(k)` returning `(nil, nil)` as acceptance — a false positive in the probe, not in the product. The guard is recorded as latent with the measurement pinned (`empty_key_sweep_test.go`), so a future bbolt that changes its mind is noticed.
- **The `-race` detector did not fire for the governor defect.** The functional half (a permanently lost reuse boundary) is the one that was falsified; the unsynchronised slice read is reported as a structural finding. Both were fixed together because they are the same two lines.
