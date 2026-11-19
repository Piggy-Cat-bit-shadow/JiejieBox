# PAIRWISE A — Findings

Three defects were confirmed by source and by execution, and fixed in this round. Four more were
confirmed **by reading only** and are recorded with their exact locations and the test that would
settle them; they are not fixed here, and each says why. Nothing in this file is marked fixed
without a regression that fails when the fix is removed.

Ordering is by risk to the running system, not by how easy the fix was.

---

## F-1〔已确认，已修复〕The naive outbound's only reclaim hook terminates live tunnels

**Pair:** A01 (ResourceGuard shed contract) × A02 (physical connection lifetime).

**Trigger.** Any transition of the admission guard into `LevelCritical` while a Naive outbound is
started. On iOS that is a system memory-pressure notification: `service/oomkiller/timer.go`
`runPressureReclaim` calls `releaseMemory`, which stores `tun.MemoryPressureCritical`
(`timer.go:1171`), and `Service.guardLevel` maps that to `resourceguard.LevelCritical`
(`service.go:120-129`).

**What happened.** `protocol/naive/outbound.go` (before this change) did:

```go
// Idle connections are the only reclaim action this outbound can offer a process under
// pressure: a busy CONNECT cannot be closed without cutting the user's flow, and the
// engine's pool is idle-only by definition.
if guard := resourceguard.FromContext(h.ctx); guard != nil {
    guard.RegisterShed(h.client.CloseAllConnections)
}
```

The comment described the intent correctly and the code implemented the opposite: the pinned engine
has **no** idle-only close. `Engine.CloseAllConnections` is the whole surface
(`cronet-go/engine_cgo.go:208`, `engine_purego.go:201`), and the dependency's own integration test
pins what it does to a live tunnel — it writes and reads through an established CONNECT, calls
`CloseAllConnections`, then requires the connection to die:

```go
client.Engine().CloseAllConnections()
require.Eventually(t, func() bool {
    _, writeErr := conn.Write([]byte("probe after close-all"))
    return writeErr != nil
}, 5*time.Second, 20*time.Millisecond, "connection should auto-close after CloseAllConnections()")
```
— `cronet-go/test/integration_test.go:913-918` (`TestCloseAllConnectionsThenClientClose`).

**Why the existing judgement did not catch it.** Three independent reasons, and each one is the kind
this round exists to find:

1. `resourceguard.RegisterShed`'s contract is prose ("must not close a connection that is serving a
   flow", `guard.go:266-269`). The naive registration satisfied the *type* of the contract and
   violated its meaning. No test asserted the contract against a protocol hook.
2. The naive outbound's own comment was treated as evidence. It says "the engine's pool is
   idle-only by definition"; that premise is false at this pin, and the falsifying fact lives in the
   dependency repository, not in this one.
3. The unit tests that exist for the shed path (`common/resourceguard/guard_test.go`,
   `reclaim_bound_test.go`, `guard_cycles_test.go`) all use `func(){}` hooks, so they prove the
   gate's transition logic and never touch a protocol's action.

**Why it is worse than a redundant trim.** The comment in `runPressureReclaim` says it is
deliberately *not* a network reset, "because the dispatch notification is system-wide and says
nothing about whether this process's paths broke". The guard's hook then closed the very paths that
pass had decided to keep. Every closed flow redialled, and each redial is a creation the gate admits
at a quarter of the normal limit while the process is still under pressure — a creation storm on the
recovery path.

**Fix.** `protocol/naive/outbound.go`: the registration is replaced by `registerNaiveShedHook`, which
contributes no hook, with the dependency evidence and the two callers that are allowed to terminate
flows (`InterfaceUpdated` for a network change, `Close` for shutdown) named in the comment. The
function exists as a seam so the absence is testable rather than a deleted line.

**Regression.** `protocol/naive/shed_contract_test.go`
(`TestNaiveShedHookNeverCutsALiveTunnel`): builds the guard exactly as `Start` does, forces the
transition into `Critical`, and requires that the engine's close primitive was never called.
**Mutation control:** re-adding `guard.RegisterShed(client.CloseAllConnections)` inside the helper
turns it red with `expected 0, actual 1`.

**Recorded limitation, pinned by a second test.** naive now implements no idle-only action at all,
so `adapter.IdleConnectionReleaser` (the trim walk, `route/reference.go:658-677`) cannot reach its
engine pool. `TestNaiveDoesNotClaimAnIdleOnlyReclaimAction` asserts the outbound does *not* satisfy
that interface, with a message telling the next implementer to wire the narrower primitive, test a
live tunnel across a trim, and retire the pin. A silent capability claim would be worse than the
gap: a trim that believes it reached a pool it never touched.

---

## F-2〔已确认，已修复〕A raised admission plan never wakes the callers already queued

**Pair:** A01 (OOMKiller demote → guard plan) × A06 (queue backpressure).

**Trigger.** The recovery edge: pressure falls, the guard publishes the wider plan through
`Gate.setPlan`, and callers are still parked in the queue from the crisis.

**What happened.** `setPlan` replaced the plan and returned. A waiter is handed a slot only by
`grantLocked`, which runs from `release()` or `withdraw()` (`gate.go:305-364`). A gate that has just
been widened has no release to run — that is what "the crisis is over" means. So the state was: the
reported limit is 32, in-flight is below it, three callers are queued and will each fail on the
budget they captured at `Critical`. The counters all look healthy while the recovery the OOMKiller
just decided never reaches the callers.

**Why the existing judgement did not catch it.** `setPlan`'s comment described the shrink case
correctly and completely ("a shrink takes effect for NEW acquisitions only"), and shrink *is* the
interesting half for ownership. The raise was covered by no sentence and no test: the existing pins
(`gate_reserve_test.go`, `queue_bound_test.go`, `permit_wait_test.go`) all drive grants through
release or withdraw.

**Fix.** `common/resourceguard/gate.go`: `grantLocked` reports whether it handed a slot over, a new
`grantAllLocked` loops it, and `setPlan` calls it after storing the plan, under the same lock. A
shrink passes through the same call and grants nothing, so the documented shrink behaviour is
unchanged (it is now pinned, see F-2b).

**Regression.** `common/resourceguard/gate_recovery_edge_test.go`
(`TestAPlanRaiseHandsRecoveredCapacityToTheQueue`): one holder, three queued callers, then
`setPlan(limit 4)`; all three must be admitted **without the holder releasing anything**.
**Mutation control:** deleting the single `g.grantAllLocked()` line turns it red with
`limit=4 inflight=1 waiters=3` — the exact sentence the test emits.

## F-2b〔已确认，已修复 — same change〕The other direction is pinned, not changed

`TestAPlanShrinkDoesNotDisturbTheQueueOrTheInFlightWork`: after `setPlan(limit 1)` with four permits
held and one caller queued, no permit is revoked, no waiter is refused, and nobody is admitted. This
is the guard-rail on F-2: the grant that makes recovery work must not turn a tighten into a
revocation.

---

## F-3〔已确认，已修复〕A QUIC carrier with no closed state installs a client after teardown

**Pair:** A02 (pool close) × Box lifecycle (`Scope` teardown).

**Trigger.** A dial that arrives **after** the outbound's Scope cleanup has run. Box teardown reaches
this shape: the Scope drains components in reverse start order and the connection manager closes
last, so a relay accepted just before its inbound listener closed can still call `DialContext` after
the outbound cleanup ran.

**What happened.** `protocol/hysteria2/family.go` and `protocol/tuic/family.go` had one teardown
entry point, `CloseWithError`, serving both a network change (after which the carrier must be
reusable) and shutdown (after which it must not). Its protection was the `generation` counter, which
covers a race that was **already running**: `race()` records the generation before creating
(`family.go:208`) and discards — and retires — a result whose generation moved
(`family.go:244-249`). A demand that starts *after* the last `CloseWithError` reads the current
generation, finds no client, creates one and installs it. Nothing bumps the generation again, so no
later `CloseWithError` can reach that client, and the fresh QUIC client keeps its socket and its
keepalive goroutines (`MaxIdle 30s`, `KeepAlive 10s`) for the life of the process.

**Why the existing judgement did not catch it.** The lifecycle tests pin the *network-change* meaning
of `CloseWithError` (`family_lifecycle_test.go`: "The network changed: the old clients are dropped
and the next demand installs a new generation") and they pass `net.ErrClosed` for it. Nothing in
either package closes the carrier and then dials, so "the carrier can always create again" was an
untested assumption on the shutdown path rather than a decision.

**Fix.** Both carriers get a `closed` flag, set under `access` at the start of `CloseWithError` when —
and only when — the caller passes `os.ErrClosed`, which is the value the outbound's Scope already
passes (`protocol/hysteria2/outbound.go:452`, `protocol/tuic/outbound.go:340`). The flag is checked
at `DialConn`/`ListenPacket` entry and again at the creation site (`clientLocked`), and it is never
cleared. A network-change error leaves the carrier reusable, which is the existing contract.

Note on the discriminator: `net.ErrClosed` and `os.ErrClosed` are **not** the same error and neither
`errors.Is` direction matches (verified on this toolchain). The network-change tests already pass
`net.ErrClosed`, so the shutdown gate is exactly the one caller that means shutdown.

**Regression.** `protocol/hysteria2/family_teardown_test.go` and
`protocol/tuic/family_teardown_test.go`:
- `TestAShutdownCarrierNeverCreatesAClientAgain` — after `CloseWithError(os.ErrClosed)`,
  `DialConn` and `ListenPacket` return `os.ErrClosed` **and the creation callback was never called**.
  The counter is the assertion; the error is asserted separately so a group policy cannot read a
  teardown as a node failure.
- `TestANetworkChangeLeavesTheCarrierReusable` — after `CloseWithError(<network change>)` the next
  demand does create a client. This is the guard-rail: setting the flag on *every* `CloseWithError`
  would silently disable the outbound after the first network change.

**Mutation control:** disabling the `os.ErrClosed` branch in `hysteria2/family.go` turns
`TestAShutdownCarrierNeverCreatesAClientAgain` red (`in chain: "hysteria2: no per-family client could
be created"`, i.e. the creation path was entered).

---

# Recorded, not fixed

Each of these is confirmed by reading with a file:line, and each is deliberately left for the next
round with the test that would settle it. None was dismissed as "probably fine".

## R-1〔P2〕trusttunnel: the session a caller is handed is not reserved

`transport/trusttunnel/client.go`: `getSession` returns a session without incrementing its stream
count (`:1179`, `:1214-1217`); `streams.Add(1)` happens later inside `roundTrip` (`:965`). Between
the two, `CloseIdleConnections` (`:868-878`) and `RetireSuspect` → `drainIfUnused` (`:883-890`,
`:692-706`) both see `streams == 0` and close the session the caller already owns. `openStream`'s
retry (`:934-943`) then re-sends the CONNECT, so the server can see two CONNECTs for one user
request. Exactly-once close survives (`closeOnce`), so this is duplicate work and a broken
"streams==0 means unused" assumption, not a leak.
*Test that settles it:* take `getSession`, call `CloseIdleConnections`, assert the session was not
closed; then run `roundTrip` and show the CONNECT landed on a closed carrier. Existing
`client_inflight_test.go` arms the trim only after `streams` is already 1.

## R-2〔P2〕anytls: no `ReuseSuspect`, so the post-sleep boundary degrades to idle-only

`protocol/anytls/outbound.go:29-33,165-171` exposes only `SetKeepIdleConnections` /
`CloseIdleConnections`; the reuse boundary in `route/reference.go:915-924` asks for
`adapter.ReuseSuspect` first and this owner does not implement it. A session that predates a
sleep/wake boundary and still carries one silent stream is skipped by the idle action (`sing-anytls
client.go:412-434` skips `hasStreams()`), and `releaseSession` puts a session with live streams back
on the idle list (`:395-401`), so a post-boundary demand can multiplex onto a pre-sleep carrier.
Both hy2/tuic have the same shape (they forward the releaser only). This is a behavioural gap on the
power/network axis (B window's territory), recorded here because the *mechanism* is A02's.
*Test that settles it:* `net.Pipe` `DialOut`, open two streams, close one, call
`CloseIdleConnections`, dial again, assert `DialOut` count went to 2 (today 1).

## R-3〔P2〕mieru: a real pool owner outside both walks

`protocol/mieru/outbound.go:36-39` implements neither `IdleConnectionReleaser` nor
`SetKeepIdleConnections`, so neither `TrimIdleResources` nor the eligibility pass can reach the
SDK's long-lived Mux underlays (`mieru/pkg/protocol/mux.go:770-813`). Bounded: the SDK reaps idle
underlays on its own ~5 s tick, so this is a delay in release, not a leak.
*Test that settles it:* `require.Implements((*adapter.IdleConnectionReleaser)(nil), outbound)` — the
assertion fails today, which is the point.

## R-4〔P1〕sing-tun: nothing admits a UDP flow before its per-flow objects exist

No `AcquireFor` / `AdmitCreation` exists anywhere on the TUN UDP path (`protocol/tun`,
`route/route.go:395-561`, `route/conn.go:794-960`); `route/conn.go:203` reads only `guard.Level` for
the copy-growth budget. The NAT entry and its handler goroutine are created first
(`sing-tun/udp_nat.go:394-459`), then the route dials and starts copies (`route/conn.go:836`,
`:875-879`, `:958-959`). The guard's seams are session/handshake seams that a `direct` outbound or an
already-established hy2 client never crosses. The 4096-entry table (iOS) is therefore the only bound
on the real per-flow cost.
*Test that settles it:* real `route.ConnectionManager` + a `PacketDialer` counting concurrent
`ListenPacket` calls with 1000 distinct destinations; assert the peak stays at the gate's limit.
`sing-tun`'s 200/2000-flow test does not cover this: its handler only reads and returns one shared
fake writer, so no dial, socket or copy goroutine exists.

## R-5〔P1〕sing-tun: evicting an entry does not cancel the creation it launched

`udp_nat.go:459` starts the handler on the *stack* context and never tracks it; `closeFromCache` /
`close` (`:955-992`) cancel nothing. At capacity the table always creates then evicts, so the entry
count is bounded while in-flight dials are not.
*Test that settles it:* handler blocked on a gate channel, cap 256, 2000 arrivals; assert live
handlers never exceed 256 (today 2000).

## R-6〔P2〕sing-tun: a download-only flow dies one `UDPTimeout` after its last upload

Lifetime refresh happens only in `getOrCreate` (`udp_nat.go:394` via `GetAndRefreshOrAdd`), reached
from `NewPacket`/`NewPacketBatch` (`:351`, `:617`). `WritePacket` (`:792-803`), the batch writer
(`:821-828`) and the spliced download path never refresh it. One datagram at t0 with replies for
longer than `UDPTimeout` (5 m) is evicted at t0+5 m and the flow ends.
*Test that settles it:* `UDPNat` with `Timeout=200ms`, no `NewPacket` but 10 `WritePacket` calls;
assert the entry survives at 500 ms.

## R-7〔P2〕sing-tun: the NAT-wide 8 MiB queued-byte budget multiplies by NAT instances

`stack_go.go:181-190` creates one `UDPNat` per queue for `address_and_port_dependent` mapping and
divides only `MaxSize`; `MaxBytes` is never propagated from `StackOptions` (`:73-79`), so each
instance takes the 8 MiB default (`udp_nat.go:169-173`). 8 queues → 64 MiB of permitted queued
datagrams against a documented 8 MiB aggregate.

---

## Not defects (checked with evidence)

- **Guard shed hooks as a second reclaim executor.** They do run on a dial goroutine while the
  OOMKiller's pass runs under `releaseAccess`, so the two are concurrent. For anytls and trusttunnel
  the action is idle-only and takes the client's own lock (`sing-anytls client.go:412-434`;
  `transport/trusttunnel/client.go:868-878`), and it is the same call the walk makes, so concurrent
  execution is bounded duplicate work, not corruption. The one hook that could not honour the
  contract was naive's (F-1).
- **xhttp pool borrow/release.** `acquireUsage` under the client lock plus `discardRetired` closes the
  borrow window (`xmux.go:145-163`, `client.go:332-346`, `:725-734`); release is exactly once
  (`xmux.go:266-289`); `Close`/`RetireSuspect` detach before teardown (`:554-569`, `:613-638`). No
  lock inversion found. (This reading is contradicted by two pre-existing red tests — see
  `PAIRWISE_A_VERIFICATION.md` §5 — so treat it as "not proven by reading alone".)
- **AnyTLS pool mechanics.** `takeIdleSession` removes the session under `c.access` before handing it
  out, so a concurrent trim cannot close a borrowed session (`sing-anytls client.go:354-367`);
  creation waiters are bounded by the creation context (`:260-287`).
- **MASQUE endpoint has no `CloseIdleConnections`.** Intentional and already pinned
  (`route/reusable_owner_inventory_test.go:62,72`): its only action cancels sessions and resets
  connections, which is not idle-only.
- **NAT entry double release / entry-after-close / cleanup goroutine / heap index.** All four ruled
  out with the `closeOnce` path and the state re-check (`udp_nat.go:947-957`, `:387`, `:443-447`;
  `udp_nat_cleanup.go:64-122`).
