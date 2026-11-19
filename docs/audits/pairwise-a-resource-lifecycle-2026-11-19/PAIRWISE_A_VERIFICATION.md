# PAIRWISE A — Verification

Everything here is reproducible from the repository root with the toolchain named below. Raw command
output is in `raw/` next to this file.

## 1. Environment

| Field | Value |
| --- | --- |
| Repository / remote | `Piggy-Cat-bit-shadow/JiejieBox` (`origin`) |
| Branch | `testing` (the development/integration branch; `origin/HEAD -> origin/testing`) |
| HEAD at start and at the end of the code work | `7654f1a779fa671a7075a347f64a4b23cbed434a` |
| `origin/testing` at the start | same SHA (working tree clean, no local-only commits) |
| OS | Windows 10.0.19044 (amd64) |
| Go | `go1.27.4 windows/amd64` (the `toolchain` directive of `go.mod`; `go 1.25.5` is the language minimum) |
| cgo | enabled with `CC=C:\Deepseek\.tools\mingw64\bin\gcc.exe` (gcc 16.2.0) for `-race`; the default `CGO_ENABLED` in this environment is also 1 |
| Tags used | default (no tags) everywhere except `protocol/naive`, which needs `with_naive_outbound,with_purego`; `with_purego` is required by the Cronet pin on Windows at this revision |

`raw/environment.txt` records the same.

## 2. Commands and results (final tree)

```
go build ./...                                                   # OK (no output)
go test ./common/resourceguard ./common/buildgate ./common/tls \
        ./common/trafficclass ./common/httpclient \
        ./protocol/anytls ./protocol/trusttunnel ./protocol/hysteria2 \
        ./protocol/tuic ./protocol/mieru ./service/oomkiller \
        ./transport/v2rayxhttp ./transport/v2rayquic ./route       # see §3, §5
go test -v ./common/resourceguard ./protocol/hysteria2 ./protocol/tuic ./service/oomkiller
                                                                  # 231 PASS, 1 SKIP, 0 FAIL
go test -v -tags "with_naive_outbound,with_purego" ./protocol/naive
                                                                  # 211 PASS, 5 SKIP, 0 FAIL
go test -v ./protocol/anytls ./protocol/trusttunnel ./common/tls ./common/httpclient ./common/buildgate
                                                                  # 184 PASS, 0 SKIP, 0 FAIL
go test -count=1 ./transport/v2rayxhttp                            # ok, 21.9s (was FAIL at HEAD)
go test -race ./common/resourceguard ./protocol/hysteria2 ./protocol/tuic ./service/oomkiller
                                                                  # OK, 2.165s / 2.446s / 2.271s / 1.970s
go test -race -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./transport/v2rayxhttp
                                                                  # ok, 23.9s — the CI shape this package now runs under
go test ./transport/v2rayxhttp -run 'TestIdlePooledConnectionIsReusedWithoutANewDial|TestTransportRedialIsInvisibleToXMUX' -count=1
                                                                  # 3 consecutive runs ok (1.52s each) — the corrected tests are stable
go vet -tags "with_naive_outbound,with_purego" ./protocol/naive     # OK
gofmt -l common/resourceguard protocol/naive protocol/hysteria2 protocol/tuic transport/v2rayxhttp   # no output
```

Evidence files: `raw/tests_changed_packages.txt`, `raw/tests_naive.txt`, `raw/tests_neighbours.txt`,
`raw/tests_route_mieru.txt`, `raw/environment.txt`.

### Tests added in this round (all pass, all mutation-checked)

| Test | Package | Pins |
| --- | --- | --- |
| `TestNaiveShedHookNeverCutsALiveTunnel` | `protocol/naive` | F-1: a shed hook must not terminate a live tunnel |
| `TestNaiveDoesNotClaimAnIdleOnlyReclaimAction` | `protocol/naive` | F-1 limitation: naive offers no idle-only action, and must not claim one |
| `TestAPlanRaiseHandsRecoveredCapacityToTheQueue` | `common/resourceguard` | F-2: the recovery edge reaches queued callers |
| `TestAPlanShrinkDoesNotDisturbTheQueueOrTheInFlightWork` | `common/resourceguard` | F-2 guard-rail: a tighten does not revoke or refuse |
| `TestAShutdownCarrierNeverCreatesAClientAgain` (hy2, tuic) | `protocol/hysteria2`, `protocol/tuic` | F-3: no client after teardown |
| `TestANetworkChangeLeavesTheCarrierReusable` (hy2, tuic) | same | F-3 guard-rail: a network change must stay reusable |
| `TestPublishedCriticalPressureStopsARealExpensiveCreation` | `service/oomkiller` | A01: published pressure → gate → a real TLS creation is refused before the dial, and recovers |
| `settledAccepts` + the two corrected pool tests | `transport/v2rayxhttp` | F-4: existing connections are reused, and a re-dial is detected only when it happens |

## 3. Per-package results

| Package | Result |
| --- | --- |
| `common/resourceguard` | ok |
| `common/buildgate` | ok |
| `common/tls` | ok |
| `common/trafficclass` | ok |
| `common/httpclient` | ok |
| `protocol/anytls` | ok |
| `protocol/trusttunnel` | ok |
| `protocol/hysteria2` | ok |
| `protocol/tuic` | ok |
| `protocol/naive` (`with_naive_outbound,with_purego`) | ok (5 SKIP are the native-library-presence skips of that package, unchanged by this round) |
| `protocol/mieru` | ok |
| `service/oomkiller` | ok |
| `route` | ok |
| `transport/v2rayxhttp` | ok (21.9 s; 23.9 s under `-race` with the Darwin tag profile). Was **FAIL** at HEAD — see §5 |
| `transport/v2rayquic` | no test files |

## 4. Mutation (negative) controls

Each fix was re-verified by disabling the mechanism, observing a red test, and restoring it. The
mutations were never committed; the tree after each control was confirmed byte-identical through
`git diff`.

| Control | Mutation | Result |
| --- | --- | --- |
| MC-1 | `Service.guardLevel`: `MemoryPressureCritical → LevelNormal` | `TestPublishedCriticalPressureStopsARealExpensiveCreation` FAILED (`in chain: "the expensive creation was started"`) |
| MC-2 | remove `g.grantAllLocked()` from `Gate.setPlan` | `TestAPlanRaiseHandsRecoveredCapacityToTheQueue` FAILED (`limit=4 inflight=1 waiters=3`) |
| MC-3 | re-add `guard.RegisterShed(client.CloseAllConnections)` | `TestNaiveShedHookNeverCutsALiveTunnel` FAILED (`expected 0, actual 1`) |
| MC-4 | `hysteria2/family.go`: never set `closed` on `os.ErrClosed` | `TestAShutdownCarrierNeverCreatesAClientAgain` FAILED (`in chain: "hysteria2: no per-family client could be created"`) |
| MC-5 | kill the pooled connection before the idle period (`server.listener.killAll()`) | `TestIdlePooledConnectionIsReusedWithoutANewDial` FAILED (`saw 4 TCP connections across an idle period, want the 2 the first session left open`) — so the corrected delta assertion still detects a re-dial |

## 5. Two tests that were red at HEAD, and are now green for the right reason

`transport/v2rayxhttp`:

```
--- FAIL: TestIdlePooledConnectionIsReusedWithoutANewDial
    http2_test.go:145: server saw 2 TCP connections across an idle period, want 1 (the pooled connection was re-dialed)
--- FAIL: TestTransportRedialIsInvisibleToXMUX
    packetup_uplink_debug_test.go:914: server saw 2 TCP connections for the first session, want 1
```

Verified on a clean tree first (with only the new untracked test files present), so they were not
caused by this round; the failing output is kept in `raw/preexisting_xhttp_failure.txt`. They were
then diagnosed rather than adjusted: the cause is a cold HTTP/2
transport giving the un-ordered downlink GET and first upload POST a connection each (2 back-to-back,
1 with a ≥5 ms gap, reproduced in plain Go on the pinned `x/net`), so the assertions were measuring a
property Go does not guarantee. They now assert the **delta** — existing connections are reused, and a
new one appears only after the old one is dead — with the measurement recorded in `settledAccepts`,
and MC-5 proves the delta still catches a re-dial. Full write-up: `PAIRWISE_A_FINDINGS.md` F-4.

The underlying cost is real and is **not** hidden by that change: a first packet-up session on a cold
pool can cost two TCP connections (two handshakes) because SPEC 061 deliberately does not order the
two halves. Removing it needs either an ordered start or a connection-level wait, both of which are
transport design changes; recorded here rather than done at the end of a debug round.

## 6. Coverage limits — stated, not glossed

1. **No iOS device, no Network Extension, no Jetsam/footprint measurement.** A04 is `未覆盖`. No
   device number appears anywhere in this round's documents. The Go/Linux path cannot substitute:
   Go heap, runtime Sys, RSS, Apple `phys_footprint`, and Cronet/native allocations are different
   quantities, and closing a connection does not immediately shrink any of them.
2. **No real UDP NAT pressure test was executed.** R-4…R-7 come from reading `sing-tun` at
   `c0855e6`, `protocol/tun` and `route/conn.go`; the deterministic tests in the findings are
   designed but not written, because R-4/R-5 need a change in the `sing-tun` fork plus a re-pin, and
   a cross-repository fix was not going to be made on reading alone.
3. **`-race` covered four packages**, not the whole tree: `route` and `protocol/mieru` were run
   without `-race` (their full suites take ~7.5 min and ~1.5 min respectively here), and the Windows
   build cannot run the Darwin-only parts at all.
4. **No performance comparison was produced.** Nothing in this round changes a data-path hot path
   (the naive change *removes* a hook registration; the gate changes only run on a plan change), so
   there is no before/after number to report — and none is claimed.
5. **CI was not executed.** The new tests are plain `go test`s, and the CI mounts were chosen from
   what the repository already runs: `verify.yml`'s push-gate race step now includes
   `./transport/v2rayxhttp/...` (local reproduction of that exact invocation: `ok 23.9s`), and
   `resource-guard.yml` now races `service/oomkiller`, `protocol/hysteria2` and `protocol/tuic` with
   matching path filters. Workflow YAML was parsed after editing and the run lines asserted from the
   parsed document. What is **not** established is the result of those jobs on GitHub: no run was
   triggered from this environment, and a workflow edit is not evidence that a job ran.
6. **Child repositories were read, not modified.** `sing-tun` (`c0855e6`), `sing-anytls`
   (`8282e00`), `cronet-go` (`d3ba128`) were inspected for the evidence quoted above; no pin was
   changed.
