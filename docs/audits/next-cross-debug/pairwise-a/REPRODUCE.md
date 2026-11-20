# Reproduce — window A (protocol pools and resource lifecycle)

Exact commands, in the order they were run. Windows PowerShell; adjust paths for another host. Each
block says what it proves and what red looks like.

## 0. Environment

```powershell
# The toolchain pinned by go.mod's `toolchain` line.
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH

# -race needs cgo on Windows; the bundled mingw64 gcc is what the earlier rounds used.
$env:PATH = 'C:\Deepseek\.tools\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'

# The repository is checked out in module mode.
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

Tags: `with_trusttunnel,with_quic` for the TrustTunnel/AnyTLS packages; the repository build uses
`release/DEFAULT_BUILD_TAGS_OTHERS`.

## 1. `P03` — TrustTunnel borrow window

### 1.1 Green (current HEAD)

```powershell
go test -count=1 -tags with_trusttunnel -run 'TestFreshSessionHandedToACallerSurvivesATrim|TestReusedSessionHandedToACallerSurvivesATrim|TestHealthProbeHandedASessionSurvivesATrim|TestFailedConnectReturnsItsReservedSlot' ./transport/trusttunnel/
```

Expected: `ok ... 0.09s`. The four tests are, in order: the fresh-session attack, the reused-session
attack, the health-probe attack, and the leak control for a returned slot.

### 1.2 Red (the parent commit is the mutation)

```powershell
git stash push transport/trusttunnel/client.go transport/trusttunnel/client_borrow_window_test.go
git checkout f11def4b1^ -- transport/trusttunnel/client.go
# re-create the two test hooks the new test file needs (they are nil-checked and behaviour-free):
# add `var testHookSessionHandedOff func(*clientSession)` plus the three call sites at the end of
# getSession's three hand-off paths, then:
go test -count=1 -tags with_trusttunnel -run 'TestFreshSessionHandedToACallerSurvivesATrim|TestReusedSessionHandedToACallerSurvivesATrim|TestHealthProbeHandedASessionSurvivesATrim' ./transport/trusttunnel/
```

Red output from the round:

```
TestFreshSessionHandedToACallerSurvivesATrim/h2:
    a trim closed the carrier the demand had just been handed
    trusttunnel: CONNECT hold.test:1: use of closed network connection
TestReusedSessionHandedToACallerSurvivesATrim/h2:
    Not equal: expected: 1  actual: 2
    the demand rebuilt a session a trim should not have closed
TestHealthProbeHandedASessionSurvivesATrim/h2:
    a trim closed the carrier under the health probe it had just been handed
    trusttunnel: health check: use of closed network connection
```

The simpler, hook-free mutation is to move `session.streams.Add(1)` back from `getSession` into
`roundTrip`: the three tests above fail exactly the same way, and the leak control still passes
because the failure and the release happen in the same call.

### 1.3 Package and race

```powershell
go test -count=1 -tags with_trusttunnel ./transport/trusttunnel/                      # 33.3 s
go test -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/ ./protocol/trusttunnel/   # 52.98 s / 0.24 s
go test -race -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/     # 55.69 s
```

## 2. `P04` — AnyTLS reuse boundary

### 2.1 Fork (`C:\Deepseek\sing-anytls`)

```powershell
cd C:\Deepseek\sing-anytls
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;C:\Deepseek\.tools\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
go test -count=1 -race -count=3 ./...      # ok, ~1.1 s
git log --oneline -2                       # da880d8, 693a1fe
```

Mutation control (makes the first two tests red):

```powershell
# in client.go, put `if true { return }` at the top of Client.RetireSuspect, then:
go test -count=1 -run 'TestRetireSuspect' ./...
# -> FAIL TestRetireSuspectDrainsASessionWithALiveStream: the retired session is still listed for reuse
# -> FAIL TestRetireSuspectClosesASessionNobodyUses: an empty session was drained instead of closed
# revert the line before committing
```

### 2.2 Product entry (`protocol/anytls`, real pinned service on loopback TLS)

```powershell
cd C:\Deepseek\JiejieBox
go test -count=1 -run 'TestTheReuseBoundaryDrainsASessionWithALiveFlow|TestTheRemoteBoundaryIsReachable' -v ./protocol/anytls/
# --- PASS: TestTheReuseBoundaryDrainsASessionWithALiveFlow (0.08s)
# --- PASS: TestTheRemoteBoundaryIsReachable (0.00s)
```

The first test: two concurrent flows share one creation (a gated listener holds the first TLS
handshake so the coalescing path is deterministic), one flow finishes, `Outbound.RetireSuspect()` is
applied, and the next flow must reach the server on a **second** client source port while the
surviving flow still echoes.

Mutation control (the pre-fix behaviour):

```powershell
# in protocol/anytls/outbound.go, replace `h.client.RetireSuspect()` with `h.client.CloseIdleConnections()`
go test -count=1 -run 'TestTheReuseBoundaryDrainsASessionWithALiveFlow' ./protocol/anytls/
# -> FAIL: the flow after the reuse boundary was multiplexed onto the session that predates it
```

### 2.3 Type-level inventory (the walk asks this owner)

```powershell
go test -count=1 -run 'TestTheReusableOwnersTheWalkIsDocumentedToReachKeepTheirCapabilities|TestTheWalkReachesTheRealHTTPClientManager' ./route/
# ok 0.21 s
```

### 2.4 Pin chain

```powershell
go list -m -json github.com/sagernet/sing-anytls    # Replace.Version == v0.0.0-20261120041358-da880d8d402e
cd test; go list -m -f '{{.Path}} {{.Version}} => {{.Replace.Version}}' github.com/sagernet/sing-anytls
# github.com/sagernet/sing-anytls v0.0.0-20260928104022-580984e4d8cb => v0.0.0-20261120041358-da880d8d402e
go vet ./...                                        # exit 0 (the nested module compiles with the new pin)
cd ..
python scripts/ci/release-freeze.py verify --lock release/source-lock.json
# PASS: every pin in the tree is the pin the lock records
python scripts/ci/check-pin-runs.py
# FAIL: no recorded run carries the current pin set   <- expected until a CI run at this lock is recorded
```

## 3. `P09` — late creation must not publish into a dead pool

```powershell
go test -count=1 -tags with_trusttunnel,with_quic -run 'TestClientCloseDiscardsTheInFlightDial|TestClientResetDiscardsTheInFlightDial|TestPendingWaiterRejectsSessionFromPreviousGeneration|TestClientWaiterCancelKeepsTheSharedDial|TestOldGenerationFailureCannotCordonCurrentGeneration|TestFallbackLateHTTP3SuccessDoesNotClearTheNewGenerationsVerdict|TestFallbackOldFailureAfterResetCannotCordonTheNewNetwork' -v ./transport/trusttunnel/
# 7 tests, all PASS, 0.32 s
```

## 3b. `P11` — a stream's end must reach the pool's books

```powershell
go test -count=1 -tags with_trusttunnel -run 'TestClosingAStalledStreamKeepsTheSessionAndItsSibling|TestServerEndedStreamLeavesTheSessionUsable|TestTrimCannotTouchALiveStream' -v ./transport/trusttunnel/
# --- PASS × 3, ok 0.11 s
```

The three shapes: a stalled tunnel the caller closes; a target that answers and closes (half-close,
then a write after EOF); and a live stream across `CloseIdleConnections`. Each asserts the server
released its side, the carrier was not re-dialled, the sibling kept working, and `streams` returned
to 0.

Mutation controls (both red in the round):

```powershell
# 1) in transport/trusttunnel/client.go, delete `c.session.streams.Add(-1)` from streamConn.Close:
#    FAIL TestClosingAStalledStream...  "a closed stream left its slot reserved, so the session can never be trimmed"
#    FAIL TestTrimCannotTouchALiveStream  "the closed stream left its slot reserved"
# 2) make CloseIdleConnections ignore the count (`if session == nil {`):
#    FAIL TestTrimCannotTouchALiveStream  "the trim closed a session that was carrying a stream"
```
## 4. `P12` — `Stats()` as a reentrant reclaimer

```powershell
go test -count=1 -run 'TestShedHookCanCallStatsWithoutDeadlockOrReclaimingTwice|TestRepeatedStatsDuringRecoveryDoesNotStallTheQueue' -v ./common/resourceguard/
go test -race -count=2 ./common/resourceguard/        # ok 3.19 s
```

Mutation flavour that the first test catches: make `runShed` hold `shedAccess` while invoking the
hooks (the hook's `Stats()` then blocks and the 5 s failure message fires), or remove the
`appliedVersion` guard in `apply` (the eight repeat reads re-run the hooks and the hook counter
exceeds one).

## 5. Whole-tree evidence at the committed code HEAD (`8426642c9`)

```powershell
$env:GOFLAGS = '-mod=mod'
$tags = (Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
go build -tags $tags ./...                                              # exit 0
go test -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/ ./protocol/trusttunnel/ ./protocol/anytls/
go test -count=1 ./route/                                               # ok 459.9 s
go test -race -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/ ./protocol/anytls/
```

`9e00fb390`, `418044508` and the documentation commits add only test/report files after that code HEAD; they do not change a build input. The `P11` suite (418044508) was run separately: `ok 0.11 s`.

Recorded results: build exit 0; `trusttunnel` 52.98 s; `protocol/trusttunnel` 0.24 s;
`protocol/anytls` 7.20 s; `route` 459.94 s; `-race` 55.69 s and 8.33 s.

## 6. What cannot be reproduced here

- iOS Network Extension footprint, Linux netns/nftables, Android OOM reports, native Cronet
  (`DialEarly`/`DialEarlyWithQUIC`): **not observable** on this host. No claim about them is made.
- The release gate that needs a CI run at the new pin set (`check-pin-runs.py`) is closed, but not
  from this host: it took the branch push and a runner job. `linux-kernel-verify` 44678300273
  succeeded at `0e2726686`, was recorded in `release/pin-runs.json` (`b724a1e80`), and the gate now
  passes. Re-check it with `python scripts/ci/check-pin-runs.py`.
