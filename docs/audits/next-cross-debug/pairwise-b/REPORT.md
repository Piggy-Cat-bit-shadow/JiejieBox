# Two-by-two cross debug — window B — DNS, Selector, routing and the real data plane

Second round of the pairwise window that owns `dns/`, `common/dialer/`, `common/urltest/`,
`protocol/group/`, `route/`, `protocol/tun/`, `protocol/redirect/` and the capture/route
implementations behind them.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch
- Starting HEAD: `72c11f522` (the state the resource-lifecycle window left; its reports and fixes are
  not modified here). Commits added: `016f6c729`, `48db6678c`, plus these three report files
- Toolchain: Go **1.27.4**; `-race` with the bundled mingw64 gcc; no dependency pin changed by this
  window (both fixes are in-tree), so `release/source-lock.json` is untouched
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

Two production defects were reproduced and fixed, and both are in the same seam: **the network
generation engine and the protocol owners that must act on it**. The first is a permanent outage: the
Hysteria2 single-client outbound gave the generation gate its own listener method as the
network-change action, and that method routes back through the gate - so the first real transition
re-entered a non-reentrant transition lock, the listener hung holding it, the reset never ran, and
every later demand on that outbound blocked on the same mutex. The second is the same seam seen from
the other side: the AnyTLS pool was never migrated to the gate at all, so a demand arriving in the
publish-to-listener window was multiplexed onto a session built for the previous network, with no
keepalive to notice. Both were found by taking the "control plane says A, data plane does B" question
literally: who owns the epoch, who owns the resource, and which code path actually runs the action.
The rest of the matrix is recorded honestly: one previously recorded finding (R-B1) is **already
fixed at HEAD** and is corrected here rather than re-claimed, and the DNS/URLTest rows are
`NOT_COVERED` as new attacks with the executed suite evidence that does exist, not as passes.

## 1. What was actually executed

| Command | Result |
| --- | --- |
| `go test -count=1 ./protocol/hysteria2/` | `ok` 0.98 s |
| `go test -race -count=1 ./protocol/hysteria2/` | `ok` 2.32 s |
| `go test -count=1 ./protocol/anytls/` | `ok` 7.41 s |
| `go test -race -count=1 ./protocol/anytls/` | `ok` 8.58 s |
| `go test -count=1 ./dns/... ./common/urltest/ ./protocol/group/ ./common/dialer/` | all `ok`: dns 8.66 s, dns/transport 6.13 s, fakeip 0.24 s, dns/transport/group 0.96 s, hosts 0.12 s, quic 0.20 s, dhcp 0.12 s, urltest 37.04 s, group 7.16 s, dialer 22.01 s |
| `go test -count=1 ./protocol/tun/ ./protocol/redirect/` | tun `ok` 4.56 s; redirect **no test files** on this platform (Linux/Darwin build tags) |
| `go test -count=1 ./route/` | `ok` 459.45 s |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | exit 0 (run in the previous window at a tree that differs by these two fixes; both packages are in the general profile) |

Platform note: Windows amd64. No Linux netns/nftables/TProxy, no iOS/macOS Network Extension, no
device sleep/wake. Nothing here is a claim about those; the rows that need them are `NOT_OBSERVABLE`.

## 2. FIXED — `B-P14a`: the Hysteria2 gate action re-enters the gate (permanent outage)

**Where.** `protocol/hysteria2/outbound.go`: the single-client construction path passed
`created.InterfaceUpdated` as `runtimecoord.NewGenerationGate`'s reset action. `InterfaceUpdated`
itself calls `gate.NetworkChanged`. `common/runtimecoord/generation_gate.go`'s `applyTransition`
takes `resetAccess` and holds it **across** the action (deliberately: a demand that starts during the
transition must wait rather than borrow a half-reset resource).

**Trigger.** One physical network change: the coordinator publishes epoch N+1 and the listener phase
calls `InterfaceUpdated`. `applyTransition` sees `currentEpoch > resetEpoch`, sets the ledger and
calls the action - which is `InterfaceUpdated` again - which calls `NetworkChanged` →
`applyTransition` → `resetAccess.Lock()` on the same goroutine. A `sync.Mutex` is not reentrant, so
the goroutine blocks forever. The outer call never returns, so **the reset never runs**, and the
mutex is never released: `DialContext`/`ListenPacket` call `BeforeDial` first, which locks the same
mutex, so **every later Hysteria2 dial blocks forever** - the outbound is dead until the process
restarts, with the transition goroutine leaked. The network manager's listener phase has a deadline
and deliberately "leaves a listener to its own budget", so no other component notices.

**Why it survived every test.** `gate_routing_test.go` exercises the gate with a *counting* reset
function, and `outbound_carrier_recovery_test.go` builds a real outbound with **no coordinator**
(`generation == nil`), so the production wiring - gated method as action - was never executed.

**Fix.** The gate action is now `created.resetForNetworkChange` (the plain close that this outbound's
dual-client path and the other six gate owners already pass), and the constructor carries the rule:
the gate holds its transition lock across the action, so the action must not come back through the
gate.

**Red → green → mutation.**

| Stage | Evidence |
| --- | --- |
| red | `the listener phase (InterfaceUpdated) did not return within 2s: it is blocked on the generation gate's transition lock`; the stack is `applyTransition → NetworkChanged → InterfaceUpdated → applyTransition` |
| green | `TestTheListenerTransitionIsNotReentrant` and `TestTheSecondNotificationIsStillDeduplicated` pass; the reset ran exactly once (`client.generation == 1`), a following `BeforeDial` returns, a duplicate notification does not reset twice, and a newer epoch does |
| mutation control | restoring `created.InterfaceUpdated` turns both tests red again (2 s each) |

**Collateral.** None: the action is the same `CloseWithError("network changed")` the listener already
ran; only who runs it and how often changes.

## 3. FIXED — `B-P14b`: the AnyTLS pool was never migrated to the gate

**Where.** `protocol/anytls/outbound.go` had a listener (`InterfaceUpdated → h.client.Reset()`) and
a dial path, but no `GenerationGate` - the pool was outside the mechanism the other owners use.

**Trigger and impact.** A demand that arrives between the epoch advance and the listener phase is
handed the session built for the previous network by `sing-anytls`'s idle list. TLS over TCP carries
no session keepalive, so nothing refuses that session: the flow is written into a dead path and only
its next read fails. This is the same defect the gate exists for, on a multiplexed pool - the exact
"old generation lent to a new demand" shape.

**Fix (both halves, mirroring the seven existing owners).**

- `DialContext`/`ListenPacket` call `BeforeDial` at the hand-out and `AfterDial` only on success, so
  the first demand inside the window runs the pool's reset before the client can lend the old
  session;
- `InterfaceUpdated` routes through `NetworkChanged`, so the late listener does not run the same
  destruction again and cut the session that demand just built;
- `resetForNetworkChange` is the action (with a nil-client guard: this outbound's client is created
  in `Start`), and the gate's registration is released from the outbound's scope.

**Red → green → mutation.** The tests build the outbound through `NewOutbound` with a coordinator in
the constructor context and count sessions at the real pinned AnyTLS service by client source port:

| Stage | Evidence |
| --- | --- |
| red (dial half) | `the demand inside the publish-to-listener window reused the session built on the previous network` - one source port for two flows |
| red (listener half) | `the late listener notification cut the session the in-window demand built` |
| green | the in-window demand opens a second session (two source ports) and the pre-change session refuses a write (the transition really ran); the late listener leaves the new session carrying bytes |
| mutation controls | bypassing the gate in the dial half turns the first test red; bypassing it in the listener half turns the second red |

**Collateral.** `resetForNetworkChange` runs the same `Client.Reset()` the listener ran; the only
difference is that it runs once per generation and can run earlier (at the hand-out). A build with no
coordinator keeps the previous behaviour byte for byte (`generation == nil` → direct call).

## 4. Re-checked and corrected

`R-B1` of the previous round states that only `protocol/trusttunnel` routed the listener half through
the gate, and that six other owners took the listener call directly, so a destructive transition ran
twice per generation. **That is fixed at HEAD** and is not re-claimed here: all seven owners
(trusttunnel, vless, vmess, trojan, hysteria2's two paths, tuic, masque) call
`generation.NetworkChanged(ctx)` from `InterfaceUpdated`, and `NetworkChanged` has no other call site
in the tree. The regression tests for the "one action per generation" property exist per owner
(`gate_routing_test.go` in each package) - and the hysteria2 pair in this window is the one that
finally drives the *real* action rather than a counting stub.

The gate-action inventory was also re-derived mechanically: `NetworkChanged` is called only from
`InterfaceUpdated` methods, and the only owner that passed a gated method as the gate's action was
Hysteria2's single-client path. That is how the deadlock was found and why it cannot be a second
instance elsewhere today.

## 5. Not covered, and why

| Row | Label | Why |
| --- | --- | --- |
| `P01` URLTest × Selector | `NOT_COVERED` | the packages are dense (25 `urltest` test files, 30 `group` files, all green above), but no new attack was built this round. The row needs a per-node pausable probe server and an assertion on the socket the chosen node reached; the recipe is in `MATRIX.md` |
| `P07` DNS cache × IPv4/IPv6 racing | `NOT_COVERED` | the DNS epoch/cache half is pinned in-process (`source_binding_test.go` etc.) but the cached answer was not pushed through `common/dialer`'s candidate planner this round |
| `P08` rule-set update × DNS cache | `NOT_COVERED` | needs a hot rule-set provider swap racing a cached resolution; not built |
| `P15` DNS hijack × local forwarding | `NOT_COVERED` at the capture layer | the verdict layer is already pinned by `direct_fast_path_dns_test.go`, `judge_flow_truth_table_test.go`, `l0_authoritative_vs_reverse_mapping_test.go`; the capture half needs a platform resolver this host does not have |
| `P10` TProxy UDP, `P13` auto_redirect | `NOT_OBSERVABLE` | no netns, nftables or TProxy driver on Windows, and no privilege to install rules |
| `R-B3` silent H3→H2→HTTP/1.1 demotion, `R-B4` naive fallback window | still open | `R-B3` is an observability gap in `common/httpclient`; `R-B4` was classified `POLICY_DECISION` by the resource-lifecycle window and is unchanged here |
| `R-B8` fake-IP cursor after a reload without `store_fakeip` | still open | recorded by the previous round; not attacked here (it needs a config reload, not a code path this window owns) |

## 6. Residual risk this window found but did not close

**The gate rollout is 8 of the ~15 protocol owners that hold dial-path sessions.** The inventory
(this window's count over `protocol/`):

- gated: `anytls`, `hysteria2`, `masque`, `trojan`, `trusttunnel`, `tuic`, `vless`, `vmess`;
- listener present, **no gate**: `http`, `hysteria` (v1), `mieru`, `naive`, `openconnect`, `openvpn`,
  `snell`, `ssh` (plus `shadowsocks`/`direct`/`group`/`tun`/`redirect`/`wireguard`/`tailscale`,
  whose resets are state refresh or a NAT purge rather than a session pool).

Each of those seven is the same bounded window this window closed for AnyTLS, and the recipe is now
mechanical: a `generation` field, `NewGenerationGate(..., resetForNetworkChange)` in the constructor,
`BeforeDial`/`AfterDial` around the dial wrappers, `InterfaceUpdated` through `NetworkChanged`, and
the registration released from the scope. Closing them needs one test per protocol (the AnyTLS one in
this window is the template: count sessions at a real server across a synthetic epoch advance). It was
not done here because doing eight protocol dial paths in one pass without device coverage of their
reconnect behaviour is the kind of change that should not be made at the end of a session.

**The transition lock is held across the action by design.** Any future action that waits on
something a dial holds - or that re-enters the gate, as Hysteria2's did - blocks the owner forever
rather than for one transition. The hysteria2 test pins the re-entry case; a "the action must not
block on a dial" review rule belongs in `generation_gate.go`'s comment (it is now in the constructor
comment where the mistake was made).

## 7. Gates and CI

The two fixes are in-tree, so no pin moved and `release/source-lock.json` is untouched. Behaviour,
package regression and `-race` results are in §1, and the tagged whole-repository build at this HEAD
exits 0.

The branch is on `origin/testing` (`17b6fb2e0..0e2726686`) and its push gate is green: `Verify` at
`b724a1e80` is **success** (run `44678967288`), `linux-kernel-verify` at `0e2726686` is **success**
(run `44678300273`; it runs the TrustTunnel half on a Linux kernel), and
`Release contract verification` (run `44678300270`) also succeeded. The only gate that was still red
after the first push - `check-pin-runs.py`, because the AnyTLS pin moved in the resource-lifecycle
window without a run at its lock - was closed by recording that kernel run in
`release/pin-runs.json` (`b724a1e80`); `check-pin-runs.py` then reports
`PASS: every pin in the lock is carried by an executed run at the same pin set (44678300273)`.

