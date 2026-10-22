# Adversarial audit of the half-close release

This is the second round's record: the first round's fix (`5034c5ee0` .. `55555b8a53b`) attacked on
purpose. Every claim below is a test that can be re-run; where a claim cannot be made here, it says
so.

**Baseline this round started from:** `testing` at `55555b8a53b6297df8eeff30bd0526dde504eb19`, equal
to `origin/testing`, clean tree, no commits from anyone else.

**Production code changed this round: none.** The adversarial work added tests, disproved one
suspicion about the fix, and pinned two contracts that were previously only reasoned about. That is
the honest result: the round found no new defect in the release path, and it found several places
where the evidence was thinner than the claim.

## 1. One-page summary

The report was "an application's connections stay Active for two hours; Close All removes them". The
first round proved the client renders `closedAt == nil` and the daemon reconciles against the core's
active table, so those rows were real held flows, and it released the one state that had no owner: a
flow whose upload direction had half-closed cleanly and whose peer then disappeared. The mechanism
is a short keepalive plus a backed-off read, releasing only on a terminal error.

This round asked three questions.

1. **Is the mechanism actually armed on the connections the report is about?** They carry sniffed
   protocol labels, and sniffing inserts a `bufio.CachedConn` between the tracker and the socket. The
   capability walk looked like it would stop there, because `CachedConn` embeds `net.Conn` instead of
   advertising replaceability. **Disproved:** `CachedConn` implements `Upstream()`, the walk reaches
   the stack's socket, and a regression test now pins the real chain
   (`TestKeepAliveWalkReachesTheSocketThroughTheSniffCache`).
2. **Does the release reach the transport, or only the table?** A table-only "fix" would look green
   and leak the socket. **Measured:** after a release, the route manager's count returns to zero, the
   peer observes its side close, `onClose` runs exactly once, and no copy goroutine survives
   (`TestTUNHalfClosedPeerGoneClosesTheTransport`, `TestTUNHalfCloseCyclesConverge`).
3. **Can the mechanism kill something legitimate?** **Measured:** a live peer's kernel answers the
   probes, so a slow response arriving 2s after the half-close - far beyond the observation's
   keepalive idle - is delivered, and the flow ends normally
   (`TestTUNHalfClosedSlowResponseIsNotKilled`). A connection that has not half-closed is never
   observed at all (`TestTUNHealthyIdleIsNeverObserved`).

Two independent problems were found in **the client's event contract** as gaps of evidence rather
than defects: no test pinned what a duplicate, late, out-of-order or unknown-id event may do to a row.
Eight tests now do (`experimental/libbox/connection_events_test.go`). One of them changed an
assumption: the five-minute close-history window is decided by the event's `ClosedAt`, not by
arrival, so a stale close is dropped in the same batch that inserts it - which is the reason that
window can never be the explanation for an *active* row.

## 2. Before / after, per layer

The "after" is measured in-process; the OS column on a phone is `UNVERIFIED` and cannot be produced
by this environment.

| Layer | Before the first round's fix | After (measured) | Evidence |
| --- | --- | --- | --- |
| TUN flow | half-closed flow stays until close-all | released within ~1s of the peer's socket answering with RST / the stack's probe countdown expiring | `TestTUNHalfClosedPeerGoneClosesTheTransport` |
| route manager | count stays 1 | `Count()` returns to 0 | same test, `waitRoute(0, ...)` |
| traffic table | entry stays, UI shows Active | entry gone, `Observed`/`Released` counted | same test + `HalfCloseDiagnostics` |
| peer socket | never sees a close | peer observes EOF (the difference between a deleted row and a closed socket) | `target.sawPeerClose()` |
| copy goroutines | one parked forever | zero after release; 8 cycles leave none | `TestTUNHalfCloseCyclesConverge` |
| goroutines | one per parked flow accumulates | 6 at rest, 6 after 8 cycles (3 identical runs) | same test, `runtime.NumGoroutine` |
| client map | untested against out-of-order/duplicate/unknown events | CLOSED wins for every input; UPDATE cannot create or reopen; Reset drops pre-reset ids; NEW is the only reopen | `experimental/libbox/connection_events_test.go` (8 tests) |
| OS socket state on the phone | `UNVERIFIED` | `UNVERIFIED` - needs the device, and the shutdown line is the field observation for it | - |

## 3. Attack matrix scoreboard

`PASS` means a test in this tree asserts the outcome; `CHARACTERISED` means the behaviour is pinned as
a boundary, not as a fix; `NOT_COVERED` says why.

| Attack | Result | What asserts it |
| --- | --- | --- |
| 1. client FIN, server EOF | PASS | `TestTrafficTrackerReleasesOnCleanFIN`, `TestTUNDeviceFinReleasesTrackedFlow` |
| 2. server FIN first, client later finishes | PASS | `TestTUNServerFinFirstThenDeviceFinReleases` |
| 3. client write-FIN only, server answers late | PASS (not killed) | `TestTUNHalfClosedSlowResponseIsNotKilled` |
| 4. server half-close, client keeps uploading | PASS (data conserved) | `TestTUNServerFinFirstThenDeviceFinReleases` (`still-uploading` after the server's CloseWrite) |
| 5. RST / ECONNRESET | PASS | `TestTUNDeviceResetReleasesTrackedFlow`, `TestTUNServerFinFirstThenDeviceResetReleases`, `TestTrafficTrackerReleasesOnReset` |
| 6. both directions silent (blackhole) | CHARACTERISED | `route/active_stall_repro_test.go`, `TestTrafficTrackerHalfClosedKernelPeerIsNotObservable` |
| 7. one side done, the other never answers | CHARACTERISED + release pair | `TestTeardown_OneSidedCompletionNeverRunsOnClose` (unchanged; comment extended), `TestHalfClosedPeerGoneIsReleased` |
| 8. healthy idle long connection | PASS (never observed) | `TestTUNHealthyIdleIsNeverObserved`, `TestHalfClosedLivePeerIsNotReleased` |
| 9. healthy backpressure | PASS (unchanged) | `TestBackpressure_HealthyBlockedWriterIsIdenticalToStalled` in the full run |
| TUN userspace vs splice-eligible | PASS both | `TestTUNDeviceFinReleasesTrackedFlow/{userspace-copy,splice-eligible}`, `TestTUNTrafficCloseAllClosesTheSocket/{...}` |
| Mixed inbound | positive controls PASS; peer-gone shape CHARACTERISED | `e2e/connection_lifecycle_test.go` (4 PASS + 1 characterisation) |
| CloseAll decomposition: traffic manager alone | PASS (socket really closes) | `TestTUNTrafficCloseAllClosesTheSocket` |
| CloseAll decomposition: route manager alone | PASS (traffic released too) | `TestTUNRouteCloseAllReleasesTrafficToo` |
| CloseAll decomposition: one tracker | PASS (both tables + onClose once) | `TestTUNSingleConnectionCloseReleasesEverything` |
| network drain / pause / wake | PASS as the existing gates | `route` full suite (`conn_reclaim_test.go`, `network_transition_reclaim_reason_test.go`, `box_power_test.go`); untouched by this work |
| client event order / replay / reset | PASS (unit level) | `experimental/libbox/connection_events_test.go` |
| concurrency | PASS | full `route` under `-race`; new tests `-race -count=3`; `GOMAXPROCS=1` run |
| proxy outbounds (not direct) | PASS for the mechanism | `TestTUNHalfClosedReleaseDoesNotDependOnTheOutboundType`: an outbound connection that hides every optional capability still releases, closes the peer visible socket and arms the observation - it is armed on the inbound side, so the outbound type cannot disable it. The proxy protocol's own wire behaviour is not claimed |
| daemon snapshot / reconcile / event translation | PASS | `daemon/connection_events_test.go` (6 tests) |
| iOS device / system TUN stack | NOT_VERIFIED_ON_DEVICE | see §8 |

## 3a. The daemon layer, driven with the real manager

The UI's rows come from three functions of `StartedService`, and they had no test: the initial
message, the per-second reconcile, and the per-event translation. Six tests now drive them against a
real `trafficcontrol.Manager` (no Box, no gRPC stream, because they are pure functions of the manager
and the snapshot map):

- the initial message carries active connections open and the closed history closed, and only the
  active ones enter the reconcile snapshot (`TestInitialConnectionStateCarriesActiveAndClosed`);
- an id that went away between two passes is announced closed once, with its `ClosedAt` and its row,
  and it is forgotten (`TestReconcileEmitsClosedForAGoneConnection`);
- a connection created between two passes is announced rather than appearing silently
  (`TestReconcileEmitsNewForAnUnannouncedConnection`);
- a late streaming CLOSED after the reconcile already closed and forgot the id still translates to a
  CLOSED event, never to a NEW one - including for an id it never announced
  (`TestStreamingClosedAfterReconcileNeverResurrects`);
- the closed event carries the final totals, the creation time and the chain, which is P4 of the
  acceptance list (`TestClosedEventCarriesTheFinalTotals`);
- and the client's summed deltas equal the manager's totals exactly, so "the counters are conserved
  across the reconcile" is measured rather than assumed
  (`TestReconcileKeepsTheTrafficDeltaAccounting`).

## 4. New findings register

| # | Finding | Evidence | Status |
| --- | --- | --- | --- |
| 1 | The capability walk was suspected of stopping at the sniff cache, which would have made the whole mechanism inert for the labelled connections in the report | `TestKeepAliveWalkReachesTheSocketThroughTheSniffCache` fails if that tree is broken; it passes | **DISPROVEN** - `CachedConn` implements `Upstream()`; test kept as a regression |
| 2 | The client's map had no pinned contract for duplicate / late / unknown / post-close events, and the 5-minute window's actual rule was only described in comments | 8 tests now assert CLOSED-wins, no-reactivation-by-update, Reset truth, NEW-reopens, timestamp-based eviction | **GAP CLOSED (tests)** - no code defect found |
| 3 | "Close All only deleted the statistics row" was a standing suspicion about the user's own workaround | `TestTUNTrafficCloseAllClosesTheSocket`: closing the tracker closes the socket; the peer observes it, in both copy and splice shapes | **DISPROVEN** |
| 4 | The first round's release test asserted the traffic table only | now also asserts route count, peer-visible close, `onClose` once, and zero surviving copy goroutines | **EVIDENCE GAP CLOSED** |
| 5 | The first round had no false-positive test at the *integration* level (only at the observer level) | `TestTUNHalfClosedSlowResponseIsNotKilled` drives a real 2s late response through the stack with the observation armed | **EVIDENCE GAP CLOSED** |
| 6 | `go.sum` gained 175 module-graph lines from a `go doc` invocation during the audit | reverted; no dependency was added or changed | **PROCESS NOTE** |
| 7 | The daemon's three connection-list functions had no test at all, although the phone's rows are built from exactly them | `daemon/connection_events_test.go` (6 tests, above) | **GAP CLOSED (tests)** |
| 8 | The observation could conceivably have depended on the outbound's capabilities | `TestTUNHalfClosedReleaseDoesNotDependOnTheOutboundType` with a capability-less outbound wrapper | **DISPROVEN** - it is armed on the inbound side |

## 5. Implementation notes

No production code changed. Why not:

- the release path's ownership is unchanged from the first round: one `flowTeardown`, one `sync.Once`,
  the observer as a third participant;
- making the manager's `CloseAllConnections` clear a flow tracker whose handle never calls back was
  considered and **not** done: every `tun.FlowHandle` in the pinned stack does reach the tracker
  (`forwardFlow.CloseFlow` → report; `GoPacketConn.CloseFlow` → closes the connection, whose copy
  loop leaves; gVisor ICMP → report), and `attachUDPSession` stores the conn before attaching the
  tracker, so the one window that looked reachable is closed by construction. Changing it would have
  been a speculative edit with no failing test behind it;
- the characterisation test `TestTeardown_OneSidedCompletionNeverRunsOnClose` kept its assertion and
  gained a paragraph explaining that the new mechanism releases only a peer that has *disappeared*,
  which is the case its premise excludes.

## 6. Regression and performance

- Full `./route` package: PASS without race (434s) and PASS under `-race` (443s), on the final tree
  including every test in this record.
- `./protocol/tun`, `./e2e`, `./common/trafficcontrol`, `./daemon` (6 new tests), `./experimental/libbox`
  (8 new tests), `./experimental/clashapi` and the root package: PASS.
- New tests: `-race -count=3` PASS; `GOMAXPROCS=1` PASS; `-count=5`/`-count=10` PASS on the e2e and
  TUN gates.
- Resources over 8 release cycles: route count 0, traffic table 0, copy goroutines 0, goroutines
  6 → 6 across three runs. No per-cycle growth.
- Cost of the observation is unchanged by this round (no production change): one timer per
  *half-closed* flow, one read per interval while it lasts, and no probe at all for a connection that
  never half-closes (`TestTUNHealthyIdleIsNeverObserved` asserts `Observed == 0`).
- `-race` requires cgo on this host; runs used `CC=C:\msys64\mingw64\bin\gcc.exe`,
  `CGO_ENABLED=1`.

## 7. Git and CI

- Round-1 fix: `5034c5ee0` (mechanism + tests), `38dad31dbe3` (race fix, diagnostics, record),
  `55555b8a53b` (gate-list correction). `Verify` passed on `38dad31db` and on `55555b8a53b`
  (runs 39699826372 and 39701753874); the intermediate `5034c5ee0` failed only in "race on the fork's
  own data paths", which is the defect the second commit fixed.
- This round's commit adds only tests, one comment block, and this record. Its SHA and CI run are in
  the final report; no tag, no release, no force-push, no submodule change, no Apple client change.

## 8. What still needs a device

Only these, and only because they need the phone:

1. Start the tunnel, use the applications, close them, leave the phone idle for a minute or two, stop
   the tunnel. The log line `half-closed flows: N observed, released N (peer reset N, keepalive
   timeout N), stopped without a verdict N` is the field evidence that the mechanism fires on iOS
   (the code path is the same userspace stack these tests drive). No log-level change is needed: an
   unset `log.level` resolves to the most verbose level (`log/log.go`), so the line is emitted by
   default; a profile that sets `warn` or higher suppresses it.
2. If a row still lingers while the application is running in the background, that is the suspended
   case: the OS still owns the socket, and the observation is designed not to touch it. A screenshot
   of the same id's `closedAt` after the application is force-quit would close that question on the
   device side.
3. A kernel-socket inbound (mixed) whose peer vanished after its FIN is the one shape measured as
   unobservable on this platform; nothing should be promised for it, and nothing is.
