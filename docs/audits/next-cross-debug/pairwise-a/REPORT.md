# Two-by-two cross debug — window A — protocol pools and resource lifecycle

Second round of the pairwise window that owns `TrustTunnel / AnyTLS / Naive / XHTTP / v2rayhttp /
H2-H3 / QUIC` pools and the reclaim side that closes them (`ResourceGuard`, `OOMKiller`, the
reference manager's trim and reuse-boundary walks).

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`** (no new branch was created)
- Starting HEAD: `17b6fb2e0` (`testing` == `origin/testing`, clean tree); commits added:
  `f11def4b1` (TrustTunnel), `c867befd0` + `8426642c9` (AnyTLS + pin), `9e00fb390` (Guard),\n  `418044508` (P11), and the audit reports themselves
- Fork touched: `Piggy-Cat-bit-shadow/sing-anytls` `main` → `da880d8` (two commits: `693a1fe`,
  `da880d8`), consumed through the `replace` pin and `release/source-lock.json`
- Toolchain: Go **1.27.4** (the `toolchain` line), `-race` with the bundled mingw64 gcc,
  tags `with_trusttunnel,with_quic` for the pools and `release/DEFAULT_BUILD_TAGS_OTHERS` for the
  repository build
- Labels: `FIXED`, `NO_DEFECT`, `SOURCE_ONLY`, `NOT_COVERED`, `NOT_OBSERVABLE`, `POLICY_DECISION`

## 0. Result in one paragraph

Two production defects were reproduced, fixed and pinned; both are the same class of failure —
**a lifecycle action decided from a counter that did not yet include the work the pool had already
promised** — and both were invisible to `-race` because they are ordering bugs, not data races.
The first is in `transport/trusttunnel`: `getSession` handed a session to a caller and only
`roundTrip` counted the CONNECT, so a trim (`CloseIdleConnections`), a reuse boundary
(`RetireSuspect` → `drainIfUnused`) or the idle watcher could close a carrier that already had an
owner. On a fresh session that failed the demand outright; on a reused one it paid a second session
creation; the health probe lost its carrier the same way. The second is in `protocol/anytls`: the
outbound forwarded only the trim, so on a sleep/wake boundary the reference manager fell back to
`CloseIdleConnections`, which skips a session that still carries a stream — the flow written onto
that pre-sleep session next died on the SDK's `streamOpenTimeout` (3 s, armed from the second
stream on - exactly this case). The fix required the pool's own state, so it was made in the
`sing-anytls` fork (`Client.RetireSuspect`), pinned in both Go modules, and reached from the
outbound through `adapter.ReuseSuspect`. Three more rows were attacked and ruled out with
executed evidence (`P09`, `P11`, `P12`); two are recorded as policy or unobservable here with
the reason (`P05`, `P02`).

## 1. What was actually executed

| Command | Result |
| --- | --- |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | **exit 0**, at the committed HEAD `8426642c9` |
| `go test -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/ ./protocol/trusttunnel/ ./protocol/anytls/` | `ok` 52.976 s / 0.236 s / 7.196 s |
| `go test -count=1 ./route/` (whole package) | `ok` 459.936 s |
| `go test -race -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/ ./protocol/anytls/` | `ok` 55.689 s / 8.328 s |
| `go test -race -count=2 ./common/resourceguard/` | `ok` 3.192 s |
| `python scripts/ci/release-freeze.py verify --lock release/source-lock.json` | `PASS: every pin in the tree is the pin the lock records` |
| `python scripts/ci/check-pin-runs.py` | **FAIL by design**: `no recorded run carries the current pin set` (see §5) |
| `go test -tags with_trusttunnel -run 'TestClosingAStalledStream|TestServerEndedStream|TestTrimCannotTouch' ./transport/trusttunnel/` | `ok` 0.11 s on h2 and 0.13 s with `with_quic` (h2+h3); 2 mutation controls red |
| `go test -race -count=1 -tags with_trusttunnel,with_quic ./transport/trusttunnel/ ./protocol/anytls/ ./protocol/trusttunnel/` (final HEAD) | `ok` 55.897 s / 8.893 s / 1.754 s |
| fork `test` workflow at `da880d8` | run `44673034831`, success (23 s) |
| fork `lint` workflow at `da880d8` | 13 errors, **identical count to the pre-change run** `43884383720`; 0 mention the new file |

Platform note: everything above ran on Windows amd64 (this host). Nothing here is a claim about
Linux netns, nftables, iOS Network Extension or native Cronet, which are `NOT_OBSERVABLE` in this
environment.

## 2. FIXED — `P03`: the TrustTunnel session a caller is handed is not reserved

**Where.** `transport/trusttunnel/client.go`. Before the fix: `getSession` returned a session while
`session.streams` was still 0 (`getSession` at the old `:1179`), and `roundTrip` incremented it later
(old `:965`). After the fix: the slot is reserved under the client lock inside `getSession`
(`client.go:1263` onwards), ownership is explicit through `streamReservation` (`client.go:939`), and
`openStreamAttempt` (`client.go:972`) is the single place that consumes or returns it.

**Trigger and impact (all three reproduced at the product entry).**

1. *Fresh session.* A demand creates the session, `getSession` returns it, and a trim lands before
   the CONNECT: `CloseIdleConnections` sees `streams == 0`, detaches and closes the carrier.
   `roundTrip` then sends the CONNECT into a closed connection. Because the session is *fresh*,
   `openStream` deliberately does not retry it on another carrier, so **the user's request fails**:
   `trusttunnel: CONNECT hold.test:1: use of closed network connection`.
2. *Reused session.* Same window on an existing session: the trim closes a carrier that is in use,
   the CONNECT fails, and the demand pays a **second session creation** (a second TCP/TLS session to
   the server). The server sees a CONNECT attempt for a flow that then has to be re-dialled.
3. *Health probe.* `Check()` sends `CONNECT _check` without ever creating a stream, so it depended on
   the same missing reservation. A trim in that window failed the probe, and the probe's failure
   path retires the carrier as "unusable" — a false verdict caused by the trim, not by the network.

The shared boundary is the `streams` counter: it is the only evidence the trim, the reuse boundary,
the idle watcher and the residency ledger have that a session is in use. Reserving *after* the
hand-off makes that evidence lag the promise by one step — a `compare-then-act` gap
(`P3`) that also lets one event (a demand taking the session) be seen by three different observers
(`P8`) as "nothing is using this".

**Fix.** One rule, enforced by ownership rather than by ordering: `getSession` reserves exactly one
stream slot before it drops the client lock, `roundTrip` consumes that slot (it becomes the stream,
or it is returned on every failure path), and every path that does not end in a stream releases it
(`Check` included). A retiring session whose last slot comes back closes in
`releaseSessionReservation`. The trim, the drain and the idle watcher now read a counter that already
contains every promise the pool has made, and they still cannot cut a stream that is carrying
traffic.

**Red → green → mutation.**

| Stage | Evidence |
| --- | --- |
| red (parent commit) | `TestFreshSessionHandedToACallerSurvivesATrim`: `a trim closed the carrier the demand had just been handed` + `use of closed network connection`. `TestReusedSessionHandedToACallerSurvivesATrim`: `expected 1, actual 2` (rebuilt the session). `TestHealthProbeHandedASessionSurvivesATrim`: `a trim closed the carrier under the health probe` |
| green | same three tests pass; full package green with and without `-race`, with and without `with_quic` |
| mutation control | the parent commit *is* the mutation (the tests were written first and failed against it) |
| leak control | `TestFailedConnectReturnsItsReservedSlot`: a failed CONNECT must return its slot, or the session stays "active" forever and no trim can ever reclaim it. Passes, and fails if the release is removed |

**Six-stage check.**

1. *Loaded*: `transport/trusttunnel` is in the default build (`with_trusttunnel` in
   `release/DEFAULT_BUILD_TAGS_OTHERS`, and the repository build in §1 compiles it).
2. *Reached*: `DialContext`/`ListenPacket` → `openStream` → `getSession`; `Check` → `getSession`. The
   closer is reached by `route/reference.go`'s `retireIdleResources` / `retireSuspectTarget`, by
   `Guard.runShed`, and by the client's own `idleWatcher`.
3. *Decided*: the decision is `streams == 0` under `c.access`; it now includes the caller's slot.
4. *Acted*: the trim either detaches and closes (nobody owns the session) or returns without
   touching it (someone does); `drainIfUnused` only closes when the last slot is returned.
5. *Observed*: before the fix the server-side witness is a failed CONNECT / a second session; after
   the fix the first session carries the CONNECT and no second creation happens.
6. *Settled*: `streamConn.Close` returns the slot, `drainIfUnused` closes a retiring session when the
   count reaches zero, and the leak control proves a failed attempt does not strand the slot.

**Collateral.** A `Check()` now holds a slot for the duration of the probe, so a trim cannot close
the carrier under a probe that is in flight (bounded by `healthCheckTimeout`, 7 s). That is the
intended contract of the trim, not a new stall: the probe is traffic. Everything else keeps its
meaning — `Reset`/`Close` still abort in-flight work, and a fresh session's CONNECT is still not
retried on another carrier.

**Commit.** `f11def4b1`.

## 3. FIXED — `P04`: the AnyTLS pool cannot express the reuse boundary, so a pre-sleep session keeps taking flows

**Where.** `protocol/anytls/outbound.go` (the outbound only had `CloseIdleConnections` and
`InterfaceUpdated`), `route/reference.go` (`retireSuspectTarget` asks for `adapter.ReuseSuspect`
first and falls back to the trim), and the `sing-anytls` fork's `client.go` (the session pool that
owns the state).

**Trigger and impact (reproduced end to end against the real service).** The SDK lists a session for
reuse as soon as *any* of its streams finishes (`sing-anytls client.go:369-403`), so a session that
still carries a live stream is on the idle list — that is the normal shape after a burst, which the
coalescing path now creates on purpose. `CloseIdleConnections` skips such a session because
`hasStreams()` is true (`client.go:412-434`). After a sleep/wake boundary the old session is
therefore still the pool's preferred carrier: the next flow is multiplexed onto it, its `cmdSYN` is
written into a TCP path that died during the sleep, and the flow fails ~3 s later when
`streamOpenTimeout` (`sing-anytls anytls.go:43`, armed from the second stream on, which is
exactly this case) closes the session. The user-visible result is a
failed connection after a wait, at exactly the moment (wake) where a fresh session would have worked.

**Why this is not a trim bug.** Closing the session would cut the stream it still carries — the trim
is right to refuse. Not closing it leaves an unverified carrier in the pool, because an open stream
can be silent across a sleep. The only correct action is the third one the adapter documents: refuse
new work, keep what is running (`adapter/outbound.go:352-384`).

**Fix (fork first, then pin, then consumer).**

- `sing-anytls` `Client.RetireSuspect` (`client.go:476`): every held session is marked retired; a
  session on the idle list is removed from it; one with no stream is closed and dropped from the
  client's set; one still carrying a stream keeps it and closes itself when its last stream finishes.
  `releaseSession` (`client.go:369`) refuses to re-list a retired session and owns that close
  (`client.go:385`). A session a dial is already opening a stream on is only marked, so the boundary
  cannot cut a demand that raced it.
- JiejieBox `protocol/anytls.Outbound.RetireSuspect` (`outbound.go:189`) forwards it, and
  `outbound.go:33` asserts `adapter.ReuseSuspect` so the walk asks the pool instead of falling back.
  `InterfaceUpdated` keeps its own, larger meaning (a full reset that cuts flows) — the two are not
  interchangeable, and the code says why.
- `route/reusable_owner_inventory_test.go` now pins both halves for this owner, next to the XHTTP
  entry that documented the capability.

**Red → green → mutation.**

| Stage | Evidence |
| --- | --- |
| fork red (mutation) | with the `RetireSuspect` body removed: `the retired session is still listed for reuse`; `an empty session was drained instead of closed` |
| fork green | `TestRetireSuspectDrainsASessionWithALiveStream` (two coalesced flows on one session, one finishes, the next flow must create a new session and the surviving flow must keep carrying bytes), `TestRetireSuspectClosesASessionNobodyUses`, `TestRetireSuspectOnAnEmptyPoolIsInert`; full fork suite green, `-race -count=3` green |
| product-entry red (mutation) | with the outbound's `RetireSuspect` delegating to `CloseIdleConnections`: `the flow after the reuse boundary was multiplexed onto the session that predates it` (the server counts client source ports: one in the red case, two after the fix) |
| product-entry green | `TestTheReuseBoundaryDrainsASessionWithALiveFlow`: the post-boundary flow opens a second session (2 distinct source ports) and the flow that was already running still echoes |
| type-level pin | `route` inventory: `protocol/anytls.Outbound` implements both `IdleConnectionReleaser` and `ReuseSuspect` |

**Six-stage check.**

1. *Loaded*: `protocol/anytls` is registered unconditionally by `include/registry.go`
   (`anytls.RegisterOutbound`), and the effective dependency is proven by
   `go list -m -json github.com/sagernet/sing-anytls` → replaced version
   `v0.0.0-20261120041358-da880d8d402e`, `Dir` under the module cache (a fetched revision, not a
   local `replace ../fork`). The nested `test/` module carries the same pin.
2. *Reached*: `route/reference.go`'s boundary case (`power.ReuseRetire`) → `retireSuspectResources` →
   `retireSuspectTarget` → `Outbound.RetireSuspect` → `Client.RetireSuspect`; the end-to-end test
   drives the outbound method directly, which is what the walk calls.
3. *Decided*: the boundary decision is "this session predates the boundary"; the state it needs
   (idle list membership, live stream set) lives in the pool, which is why the decision had to move
   there rather than be approximated at the outbound.
4. *Acted*: the pool removes the session from the idle list and closes it at its last stream; a
   session with no stream is closed immediately.
5. *Observed*: the server counts a second client source port for the post-boundary flow (a new
   session), and the flow on the old session still completes an echo round trip (not cut).
6. *Settled*: the drained session closes when its last stream ends, and the empty-pool/empty-session
   tests pin the two idempotent edges.

**Collateral.** `RetireSuspect` never dials and never cuts a busy stream; a healthy process that
never crosses a boundary pays nothing (the method is only called by the boundary walk). The
`SetKeepIdleConnections`/eligibility axis is unchanged.

**Cross-repo pin chain.** fork `693a1fe` (capability + tests) → `da880d8` (test lint) → `go.mod` and
`test/go.mod` `replace`, both `go.sum`s, `release/source-lock.json`, `release/pin-runs.json`
(observed tip). `release-freeze.py verify` passes. **`check-pin-runs.py` fails by design**: no CI run
was recorded against the new lock yet (see §5).

**Commits.** `c867befd0` (pin), `8426642c9` (outbound + tests + inventory).

## 4. Attacked and ruled out

### 4.1 `P09` — a late creation must not publish into a dead pool: `NO_DEFECT`

`transport/trusttunnel` already closes this window, and the closing is the reason the new reservation
work could be kept local. `publishOffer` decides *under* `c.access` whether the offer was discarded
(`completeOffer` → `publishOffer`, old `:1365`): `Close`/`Reset` mark the pending offer discarded
under the same lock before cancelling the dial, and `publishOffer` closes a late success exactly once
instead of installing it. `rejectPublishedSession` re-checks the generation and the verdict after a
waiter wakes, so a session published for a generation `Reset` has since left is retired rather than
handed out. Re-ran, all pass (0.3 s):

`TestClientCloseDiscardsTheInFlightDial`, `TestClientResetDiscardsTheInFlightDial`,
`TestClientWaiterCancelKeepsTheSharedDial`,
`TestPendingWaiterRejectsSessionFromPreviousGeneration`,
`TestOldGenerationFailureCannotCordonCurrentGeneration`,
`TestFallbackLateHTTP3SuccessDoesNotClearTheNewGenerationsVerdict`,
`TestFallbackOldFailureAfterResetCannotCordonTheNewNetwork`.

No new interleaving was found in which a detached owner installs a session, a late dial re-creates a
transport, or a stale generation is stamped on a published session. The new reservation does not
weaken this: it happens before the same lock is dropped, and a discarded offer never reserves.

### 4.2 `P12` — `Guard.Stats()` is a reclaimer, not a reader: `NO_DEFECT` for the contract, executed

`Stats()` observes and applies by design (`common/resourceguard/guard.go:430-468`), which makes every
diagnostic path a potential reentrant caller of the reclaimer (the OOM report, the guard-metadata map,
a protocol logging its state from inside a shed hook). The two failure modes that would follow —
deadlock and repeated reclaim — were executed rather than argued, in
`common/resourceguard/guard_reentrant_shed_test.go`:

- a shed hook calls `guard.Stats()` and `guard.Level()` from inside the reclaim: no deadlock within
  5 s, the reentrant snapshot sees the transition's own level;
- eight further `Stats()` calls at the same level do not run the hooks again (one reclaim per
  transition);
- a later escalation reclaims exactly once more, and the demotion in between reclaims nothing;
- recovery driven by `Stats()` alone completes the demotion, so a process measured only through
  reports still recovers (the earlier "recovered capacity never woke the queue" fix was at the queue;
  this is the report entry).

The read-only change is still rejected for the reason the code states: a snapshot read without
observing can report "level critical, limit 32", which is the self-contradicting report the shared
snapshot exists to avoid. This row is a contract pin, not a production change
(`9e00fb390`).

### 4.3 `P05` — Naive H3 fallback probe budget vs caller deadline: `POLICY_DECISION`

The code and its tests were read and the strategy is deliberate, not accidentally missing:
`h3ProbeContext` splits the caller's remaining budget in half (`protocol/naive/h3_fallback.go`), a
probe that expires on that budget is *ambiguous* and retries the flow over H2 without arming the
30 s window, while a Chromium connection timeout or a proxy answer arms/discards it
(`h3_fallback_test.go`: `TestH3FallbackProbeExpiryIsAmbiguous`,
`TestH3FallbackProxyAnswerOutranksProbeExpiry`, `TestH3FallbackCallerCancelDuringProbeDoesNotRetry`).
With a 200 ms caller deadline the H3 leg gets ~100 ms and the H2 retry the rest, so a "three short
deadline CONNECTs" experiment observes three H3 probes — which is the documented answer for an
ambiguous expiry, not a defect. Changing it (for example, arming the window on ambiguous expiry)
would be a **policy change** with a cost on H3-only deployments, so it is not made here. The native
Cronet paths (`DialEarly`, `DialEarlyWithQUIC`) are `NOT_OBSERVABLE` on this host; the fallback
decision itself is in-tree and testable without Cronet.

### 4.4 `P11` - a stream's end must reach the pool's books: `NO_DEFECT`

The stream is the pool's Active/Idle evidence, so its lifetime has to end in `streams` no matter how
it ends. Three shapes were executed against the real service, for BOTH carriers (`h2` and `h3` under
`with_quic`), in `transport/trusttunnel/stream_lifecycle_test.go`:

- **A stalled tunnel the caller never reads, then closes.** Close returns within 5 s, the server
  releases its side (`handler.holding` back to 0), the carrier is the same one (`dials == 1`) and it
  still carries a sibling flow, and the slot comes back: `streams` reaches 0 and the trim can then
  close the session.
- **The target answers and closes (half-close).** The body reaches `io.EOF`; a write after that EOF
  resolves (accepting it or reporting the reset) instead of blocking the caller, and the session
  still serves the next CONNECT without a re-dial.
- **The trim while a stream is live.** `CloseIdleConnections` leaves the session alive and the stream
  keeps echoing across it; the SAME call closes the session once that stream ends.

Mutation controls, both executed and both red: removing the slot return from `streamConn.Close` makes
the first and third fail (`a closed stream left its slot reserved, so the session can never be
trimmed`); making `CloseIdleConnections` ignore the stream count makes the third fail (`the trim
closed a session that was carrying a stream`). The 1000-stream limit case is already pinned by
`testStreamLimit`, which now also exercises the new reservation (one session, one slot freed and
reused) and stays green.
## 5. Not covered, not observable, and the gates that stay red

| Item | Label | Why |
| --- | --- | --- |
| `P02` QUIC PMTU probe × path migration | `NOT_COVERED` | requires simultaneous PMTU-blackhole injection and NAT rebinding; the blackhole and the send-queue tests exist separately (`q35_udp_*`, `congestion_test.go`) but the cross-injection was not built |
| native Cronet / iOS Network Extension / Linux netns | `NOT_OBSERVABLE` | not available on this host; no claim is made about them |
| `sing-tun` UDP NAT residuals (R-4..R-7 of the previous round) | out of this window | they belong to the TUN/DNS window; nothing here changes their status |

**Gates.** The repository build, the listed package suites, `-race`, `release-freeze.py verify` and
the fork's `test` workflow are green (§1). Two gates are **not** green and are left that way on
purpose rather than papered over:

1. `scripts/ci/check-pin-runs.py` → `FAIL: no recorded run carries the current pin set`. A pin moved
   and no CI run has been recorded against the new lock, so the sources the lock names have not been
   built by CI. `release/pin-runs.json` records the new tip and the note states this. Closing it
   requires a workflow run at a commit whose `release/source-lock.json` carries `da880d8` (for the
   kernel: `Android Core ARM64` / `linux-kernel-verify`), recorded in a follow-up commit. It cannot be
   produced from this environment without pushing the branch and waiting for the runner fleet.
2. The fork's `lint` job is red on `main` **before and after** this change with the *same 13 errors*
   (`coalescing_test.go`, `admit_creation_test.go`); the new test file contributes none. The first
   push of the change did add three `paralleltest` findings in the new file, which the follow-up
   commit `da880d8` fixes — that is why the pinned revision is the follow-up and not `693a1fe`.
3. **The commit set is on `origin/testing` and its push gate is green.** Pushing was blocked for a
   while (GitHub HTTPS and SSH both reset), and when the path recovered the ten commits went up as
   `17b6fb2e0..0e2726686`. CI at `0e2726686` ran the fleet: **`linux-kernel-verify`
   (`44678300273`, 2m50s) succeeded** - it builds with `with_trusttunnel`/`with_quic` and runs
   `transport/trusttunnel` on a Linux kernel, the platform half this Windows host cannot cover - and
   `Release contract verification` (`44678300270`), `iOS resource guard` (`44678300297`) and
   `Maintenance ledger` (`44678300224`) also succeeded. That kernel run is what closes the pin gate:
   it was recorded in `release/pin-runs.json`'s `parent_runs` (`b724a1e80`), so
   `check-pin-runs.py` now reports `PASS: every pin in the lock is carried by an executed run at the
   same pin set (44678300273)`, and `verify.yml` at `b724a1e80` is **success** (run `44678967288`).
   The first push's own `Verify` run was cancelled by that follow-up push (concurrency
   cancel-in-progress), which is why the green one is the follow-up.

## 6. Residual risks in the two fixes (stated, not hidden)

- **TrustTunnel `Check()` now holds a slot for up to 7 s.** A trim that arrives during a health probe
  waits for the probe instead of closing the carrier. This is the contract, but it means an aggressive
  memory walk can be delayed by one probe interval per client.
- **AnyTLS: a creation in flight when the boundary fires is neither retired nor cancelled.** It
  publishes a session after the boundary. That session is not obviously wrong (its connect and
  handshake completed *after* the boundary, which is itself the proof the retired sessions lack), but
  the boundary does not make the pool discard it. A future `RetireSuspect` that also retracts a
  pending creation would need the SDK's `pendingCreation` to grow a discard path, the same one
  TrustTunnel already has.
- **The TrustTunnel reservation is per-session, not per-CONNECT.** `Check` covers the probe but the
  window between `streams.Add(1)` and the CONNECT reaching the wire is still *inside* the session, so
  a `Reset`/`Close` (which intentionally cut in-flight work) can still terminate a CONNECT that has
  been promised but not sent. That is the documented behaviour of those two verbs, not the trim's.

## 7. Reproduce

`REPRODUCE.md` in this directory has the exact commands (red/green/mutation, the fork tests, the
pin-chain checks and the full-suite runs). `MATRIX.md` is the row-by-row table with owners, shared
objects, timelines, product paths and conclusions.
