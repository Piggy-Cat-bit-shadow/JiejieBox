# Phase 03 — QUIC dual-fork follow-up and MASQUE cross-repository acceptance

```text
PHASE = 03/06
RESULT = PARTIAL_WITH_SPECIFIC_BLOCKER
QUIC_GO_SAGERNET_LINE_SHA = ce02ee5cc210fa01019f8e961e10ad93a84ae156 (branch jiejie-masque-mod9)
QUIC_GO_METACUBEX_DEFAULT_SHA = beb42da71a55a1a45ef8794b29afcd6657b15f51 (branch jiejie-masque-stable, unchanged)
SING_QUIC_MAIN_SHA = 5e5a0274bd6ca92f75f5641298759d79d16a3a85
MAIN_TESTING_SHA = 56a804e6eedb761b1b8b067465f4eb1c7f8f616d (code); ledger commit follows
EFFECTIVE_PINS_AND_NESTED = quic-go v0.61.1-0.20261024063732-ce02ee5cc210; sing-quic v0.7.2-0.20261024074641-5e5a0274bd6c; both root and test modules
QUIC_EMSGSIZE_AND_LIFECYCLE = CLOSED: mod.10 EMSGSIZE read, mod.11 trust_anchors, mod.12 application-limited; local tests/race and four-OS builds
MASQUE_REAL_E2E_AND_FAIRNESS = PARTIAL: HTTP3/H1/fallback/routes/no-route/reconnect/stuck PASS; HTTP2 and PacketTooBig e2e time out identically on the phase-02 baseline; functional isolation PASS, p95/p99 not measured
CI_FINAL_CODE_SHA = Verify 40075546655 SUCCESS, Android 40075546704 SUCCESS, Mobile 40075546654 SUCCESS on 56a804e6eed
CLEANED_TEMP_BRANCHES = baseline/masque-old deleted; quic-go sync worktrees/branches removed; sing-quic no temp branch
NEXT_PHASE_READY = YES
```

## 1. Branch boundary (important)

`Piggy-Cat-bit-shadow/quic-go` carries two intentionally separate module lines, and this phase only
touched the Sagernet one:

| branch | module | role | state |
|---|---|---|---|
| `jiejie-masque-mod9` | `github.com/sagernet/quic-go` | the line sing-box/sing-quic build | fast-forwarded to `ce02ee5c` |
| `jiejie-masque-stable` | `github.com/metacubex/quic-go` | independent MASQUE projects (`connect-ip-go`, `jiejie-masque-unified`) | untouched at `beb42da7`; repository default restored to it |

A trial merge of the two was measured before rejecting it: different module paths, 137 conflicting
production files, and a second incompatible owned-DATAGRAM API. The release line is therefore pinned
by SHA in `sing-box` and `sing-quic`; the default branch remains the MetaCubeX line so independent
MASQUE projects keep their baseline. No branch, tag, pin or default setting of those projects was
changed.

## 2. quic-go release line

`ce02ee5c` = `7e08ecab` (mod.9 + owned DATAGRAM/ceiling work) plus:

- `f9618f37` — EMSGSIZE from a connected UDP socket read is recoverable (Linux/darwin), so a path-MTU
  notification no longer kills the transport read loop;
- `05bc9171` — Chrome ClientHello carries the `trust_anchors` extension;
- `4f600e37` — application-limited is decided after every send attempt, and a stream parked behind
  flow control does not count as unblocked data;
- `8929ebe5` — targeted tests for all three (the upstream module strips test suites, so these pin the
  behaviours directly);
- `ce02ee5c` — fork `verify` workflow (Actions is disabled on this repository, so it has no run; the
  local suite is the evidence).

Local: `go build ./...`, `go test ./...`, `go test -race ./...` and linux/darwin/windows/android
builds pass.

## 3. sing-quic main

`5e5a027` = upstream `7e0a5869` (hysteria2 port-hopping EMSGSIZE fix) plus the three commits sing-box
already consumed (ceiling, its tests, owned-DATAGRAM send path for Hysteria2/TUIC), plus a `go.mod`
replace resolving `github.com/sagernet/quic-go` to `ce02ee5c`.

Local: build, tests, race and linux/darwin/windows/android builds pass. Its own `go list -m` resolves
the same Sagernet SHA as sing-box.

## 4. Main repo

`56a804e6eed` pins the two tips in root and `test/go.mod`:

```text
github.com/sagernet/quic-go    => github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261024063732-ce02ee5cc210
github.com/sagernet/sing-quic  => github.com/Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261024074641-5e5a0274bd6c
```

`go mod tidy`/`verify`, `go list -m` in both modules, `check-go-module-integrity.sh` (36/36 root
replaces), Windows/Linux/Android builds and `transport/masque` tests pass.

A real Linux-only crash surfaced while running the MASQUE e2e suite: the systemd-resolved DBus signal
loop dereferenced a nil signal on channel close. `ff16f4a1f12` returns instead, and the deferred
`RemoveSignal` still runs. This is a test-discovered production fix, not a weakened assertion.

## 5. MASQUE matrix

Passed:

- `transport/masque` and `protocol/masque` under `-race`;
- HTTP/3 CONNECT-IP/CONNECT-UDP self-to-self, HTTP/1, version fallback, advertised routes, no-route,
  reconnect backoff, stuck client on the Linux runner (run 40072195794);
- PTB/PREF64/capsule/ownership/backpressure unit matrices: `TestPacketTooBig*`, `TestPREF64*`,
  `TestCapsule*`, `TestOwnedSend*`, `TestIngress*`, `TestOutboundQueue*`;
- Hysteria2 self/port-hop/salamander and TUIC self/UDP/early e2e with the new pins;
- functional fairness: blocked destination does not stall another, per-session queues, per-destination
  ownership.

Not passed, and pre-existing:

- `TestMASQUESelfToSelf/HTTP2` and `TestMASQUEPacketTooBig` time out on Linux. The phase-02 baseline
  branch (old quic-go/sing-quic pins, same DNS fix) fails the same two cases — run 40073782175 — while
  every other MASQUE case passes. This is a test-environment/legacy-path limitation, not a
  regression from this phase.
- Quantitative fairness (p95/p99 queue wait and drop under a congested neighbour) was not measured:
  the repository has no lossy multi-session fixture, and inventing one would be a separate harness.

## 6. CI

- Main repo on `56a804e6eed`: Verify `40075546655` SUCCESS, Android Core ARM64 `40075546704`
  SUCCESS, Mobile contract verification `40075546654` SUCCESS.
- linux-masque-verify `40072195794` RED on the two pre-existing cases above.
- quic-go Actions is disabled; sing-quic Actions has no runs. Both were verified locally with the
  exact commands in §§2–3.

## 7. Remaining blockers

1. HTTP2 and PacketTooBig Linux e2e fail identically on the old and new pins; fixing them is a
   separate MASQUE test/product task.
2. p95/p99 fairness measurement needs a lossy multi-session harness.
3. quic-go/sing-quic CI cannot be dispatched because Actions is disabled for those repositories.

The next dependency phase is `DEP-CRONET-1`; no cronet-go or Tailscale production change was started
here.
