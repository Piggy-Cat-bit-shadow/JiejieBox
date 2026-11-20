# Reproduce — window B (generation gate × protocol owners)

Exact commands, in the order they were run. Windows PowerShell. Each block says what it proves and
what red looks like.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH            # the go.mod toolchain
$env:PATH = 'C:\Deepseek\.tools\mingw64\bin;' + $env:PATH; $env:CGO_ENABLED = '1'   # for -race
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. `B-P14a` — the Hysteria2 gate action must not re-enter the gate

### 1.1 Green (current HEAD)

```powershell
go test -count=1 -run 'TestTheListenerTransitionIsNotReentrant|TestTheSecondNotificationIsStillDeduplicated' -v -timeout 90s ./protocol/hysteria2/
# --- PASS × 2, ok 0.10 s
go test -count=1 ./protocol/hysteria2/                 # ok 0.98 s
go test -race -count=1 ./protocol/hysteria2/           # ok 2.32 s
```

### 1.2 Red (the parent commit, or a one-line mutation)

The red state is the tree before `016f6c729`; the same effect is produced by restoring the old gate
action in `protocol/hysteria2/outbound.go`:

```go
	created.generation = runtimecoord.NewGenerationGate(
		service.FromContext[*runtimecoord.Coordinator](ctx),
		"hysteria2/"+options.ServerOptions.Build().String(),
		created.InterfaceUpdated, // <- the defect
	)
```

```powershell
go test -count=1 -run 'TestTheListenerTransitionIsNotReentrant|TestTheSecondNotificationIsStillDeduplicated' -timeout 90s ./protocol/hysteria2/
```

Red output from the round (2 s per call, then the test binary is left with one blocked goroutine
holding the gate; that is the point of the failure):

```
--- FAIL: TestTheListenerTransitionIsNotReentrant (2.00s)
    the listener phase (InterfaceUpdated) did not return within 2s: it is blocked on the generation gate's transition lock
--- FAIL: TestTheSecondNotificationIsStillDeduplicated (2.00s)
    ... same message ...
```

A `go test -timeout 60s` run of the same tree ends with the goroutine dump whose stack is the proof
of the recursion:

```
applyTransition  generation_gate.go:147   (resetAccess.Lock)
NetworkChanged   generation_gate.go:138
InterfaceUpdated hysteria2/outbound.go:437
applyTransition  generation_gate.go:154   (g.reset(ctx))
NetworkChanged   generation_gate.go:138
InterfaceUpdated hysteria2/outbound.go:437
```

## 2. `B-P14b` — AnyTLS: the pool must be gated

### 2.1 Green

```powershell
go test -count=1 -run 'TestADemandInThePublishToListenerWindowDoesNotReuseTheOldSession|TestTheLateListenerDoesNotCutTheSessionTheDemandBuilt' -v -timeout 120s ./protocol/anytls/
# --- PASS × 2, ok 0.24 s
go test -count=1 ./protocol/anytls/                    # ok 7.41 s
go test -race -count=1 ./protocol/anytls/              # ok 8.58 s
```

The tests build the outbound through `NewOutbound` with a coordinator in the constructor context
(`service.ContextWith[*runtimecoord.Coordinator]`), drive a synthetic epoch advance
(`coordinator.Advance(1)`), and count sessions at the real pinned AnyTLS service by client source
port (`carrierHandler.sessions()`). No fake transport is involved.

### 2.2 Red (dial half)

Remove the `BeforeDial`/`AfterDial` pair from `Outbound.DialContext` (leaving `h.dialContext` only):

```powershell
go test -count=1 -run 'TestADemandInThePublishToListenerWindowDoesNotReuseTheOldSession' -timeout 120s ./protocol/anytls/
# --- FAIL ... the demand inside the publish-to-listener window reused the session built on the previous network
```

### 2.3 Red (listener half)

Replace `Outbound.InterfaceUpdated`'s body with `h.client.Reset()`:

```powershell
go test -count=1 -run 'TestTheLateListenerDoesNotCutTheSessionTheDemandBuilt' -timeout 120s ./protocol/anytls/
# --- FAIL ... the late listener notification cut the session the in-window demand built
```

Restore the file before committing (both mutations are one edit each; keep a copy, do not rely on
`git checkout` if other changes are uncommitted).

## 3. `R-B1` re-check (previous round's finding)

```powershell
git grep -n 'NetworkChanged(' -- protocol/
# one call per InterfaceUpdated method: hysteria2 (2), masque, trojan, trusttunnel, tuic, vless, vmess
```

All seven owners route the listener half through the gate at HEAD, so R-B1 is fixed and is not
re-claimed. The per-owner regression is each package's `gate_routing_test.go`.

## 4. Window-B suites (executed evidence for the matrix rows)

```powershell
go test -count=1 -timeout 1800s ./dns/... ./common/urltest/ ./protocol/group/ ./common/dialer/
# ok: dns 8.66 s, dns/transport 6.13 s, fakeip 0.24 s, dns/transport/group 0.96 s, hosts 0.12 s,
#     quic 0.20 s, dhcp 0.12 s, common/urltest 37.04 s, protocol/group 7.16 s, common/dialer 22.01 s

go test -count=1 -timeout 600s ./protocol/tun/ ./protocol/redirect/
# tun ok 4.56 s; redirect has no test files on Windows (Linux/Darwin build tags)

go test -count=1 -timeout 1800s ./route/
# ok 459.45 s
```

These are suite re-runs, not new attacks: `P01`, `P07`, `P08` and the capture half of `P15` remain
`NOT_COVERED` as new attacks (see `MATRIX.md` for the witnesses they would need).

## 5. What cannot be reproduced here

- Linux netns / nftables / TProxy / `auto_redirect` (`P10`, `P13`): no capability on this host.
- iOS/macOS Network Extension, a real device sleep/wake, or a Wi-Fi→cellular handover: no device.
- A kernel CI run at these commits: the branch could not be pushed (GitHub HTTPS and SSH both reset
  for the duration of the session).
