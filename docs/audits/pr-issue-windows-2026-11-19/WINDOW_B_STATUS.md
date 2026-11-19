# Window B（协议传输：QUIC / H3 / XHTTP / Naive）——逐组执行状态

Same rule as `WINDOW_A_STATUS.md`: what was executed, what it proved, and what could not be observed.
The file's 48-seed appendix was read at group level only.

Base: `testing` @ `11695cbb4` plus this round's commit.

## Group 1 — H3 小流公平性（大下载流并行小 GET/CONNECT）

`transport/trusttunnel` 52.8 s, `transport/v2rayxhttp` 21.9 s, `common/httpclient` 2.9 s —
all green under the shipped Windows profile.

**`PARTIAL / NOT_COVERED`, corrected after the audit.** The earlier wording said "NO_DEFECT by
coverage", which claimed more than the evidence: a passing suite is not a fairness measurement. The
property this row names — a small GET or CONNECT is not starved while a bulk download shares the same
H3 connection — needs a rate-limited path and per-stream latency attribution, and no fixture for that
was built. What the suites do establish is that the three transports function and their pooling/fallback
paths are exercised; they say nothing about P95/P99 of a small stream under a large one, its timeout
rate, the queue bound, or recovery when the pressure is withdrawn. Those four numbers are what a future
round should produce.

## Group 2 — QUIC 发送队列（full→writable 临界事件，对端不发新包）

**Executed this round: covered by a userspace backpressure harness, `NO_DEFECT`.**

The fixture's server now holds its stream readers before their first read
(`migrationServer.readsBlocked`, nil by default so no other test changes behaviour). The test writes
16 KB chunks until a write deadline fires — on this build the send queue filled after **2,097,152 bytes**,
the peer's advertised window — then releases the reader and requires that a **fresh stream** on the same
connection carries data and that the server saw one handshake (no reconnect).

Two deliberate choices make it robust rather than window-size-dependent:

- **The fill size is not asserted.** It is the peer's advertised window, not a contract of this
  transport; asserting it would turn every upstream window change into a false failure. The test records
  it in its log and asserts only that *some* write blocked and that the connection recovered.
- **Recovery uses a new stream.** A QUIC stream whose write deadline fired is cancelled and cannot be
  resumed — that is protocol semantics, not a transport defect, and the comment says so.

**Vacuous-pass guard:** if the window never fills inside the ten-second budget the test reports `Skip`
with the byte count instead of passing, so it cannot claim "recovered from a full queue" without one.
The first version of this test also learned something worth writing down: opening a stream does not by
itself drive the handshake, so the handshake needs a first write before the server has anything to
accept — without it the test waited two seconds for a stream the server never saw.

## Group 3 — PMTU 黑洞（MTU 1280/1290、不可达 ICMP/EMSGSIZE）

**Executed this round: covered by a userspace black-hole path, `NO_DEFECT`.**

`client_pmtu_blackhole_test.go` wraps the client's UDP socket in a `net.Conn` that accepts datagrams up
to 1280 and **silently drops** everything larger, reporting success to the sender exactly as a path with
a smaller MTU and no ICMP does. No netem, no namespace, no privilege. The workload is 256 KB of channel
records, which is what makes the discovery actually probe: the run reports **5 dropped datagrams** and
the transfer completes, then a second stream still works. Larger datagrams being dropped mid-connection
is therefore survivable, and the connection falls back to a size the path carries.

Two controls:

- **The path really swallows traffic:** `TestAPathThatDropsEverythingFailsForTheCaller` makes every
  datagram vanish and requires the dial to fail when the caller's two-second deadline expires. It also
  records the ownership contract: the transport adds no timeout of its own (`offerNew` dials under a
  context derived from the client's lifetime), so a caller that never gives up waits - the earlier
  version of this control used `context.Background()` and hung past ten seconds, which is not a defect.
- **The scenario is reachable only through the production switch:** inverting the client's
  `DisablePathMTUDiscovery: !C.IsLinux && !C.IsWindows` line (so discovery is off on Windows) makes the
  run report **0 dropped** datagrams and the test fails on its own guard: *"the simulated path dropped
  nothing: the workload never grew a datagram past 1280, so this test did not exercise a black hole at
  all"*. That guard exists so the test can never pass vacuously. Reverted; the package is green (11.6 s).

## Group 4 — 连接迁移（多次 NAT rebinding、接口切换）

**`NO_DEFECT` by coverage.** `client_migration_test.go` (20 KB), `path_migration_test.go` (17 KB),
`ns_dualpath_test.go` (7 KB) and the migration suite cover candidate validation, sabotage, the
"unchanged" outcome and the retired-socket accounting; the whole package is green (9.9 s). This round
also changed the migration path's acknowledgement (triple C's C-T5) and these tests are its positive
control.

## Group 5 — 取消与流终止（仅目标流失败、不污染整条 QUIC 连接）

**Executed this round: a new regression test, no defect found.**

`client_stream_isolation_test.go` opens two streams on one connection, closes the second with a
**partial record** on it (the "caller gave up mid-frame" shape), then requires that the sibling still
sends records the server accepts, that the pooled connection object is unchanged, and that the pool
neither retires it nor dials a replacement (client socket count and server handshake count both stay 1).

**Mutation control:** making `releaseStream` detach the session on any stream close
(`drainAfterStreams.Store(true)`) turns it red at the sibling's next record — "record 1 must be accepted
by the migrated stream" — so the test observes the mechanism it claims to. Reverted.

The mechanism itself is structural and worth writing down: `releaseStream` closes the connection only
when `streams == 0`, so a cancel can only end the session when it was the last flow on it.

## Group 6 — Mieru UDP

**`NOT_COVERED` at the current pin.** Window A's R-3 records why: the SDK exposes no idle releaser and
the client cannot be stopped and restarted, so the interesting interleavings cannot be driven from this
repository.

## Group 7 — XHTTP / H2（SETTINGS frame size × 并发 streams × 慢读）

Window B's **B-1 fix** lives here: Go 1.27's `defaultMaxReadFrameSize` moved the TrustTunnel preface
away from Chrome's, and the pin now keeps it at 16384 with a test that fails when the toolchain default
is used. `transport/v2rayxhttp` (21.9 s) green, including the delta-assertion repairs. A dedicated
slow-read × concurrent-streams harness was **not** built.

## Group 8 — Naive / Reality（半握手、切网、强制断链）

Window A's naive fix (a pressure hook that terminated live tunnels is gone) and window C's gate routing
(`InterfaceUpdated` now goes through the generation gate, which resets the H3 fallback window) cover the
lifecycle half. `protocol/naive` green under the release profile (1.1 s). The remaining policy item —
a fallback window that can stay unarmed on short deadlines — is recorded as R-B4.

## Summary

| Group | Verdict |
| --- | --- |
| 1 H3 small-flow fairness | `PARTIAL / NOT_COVERED` (corrected after the audit: a passing suite is not a fairness measurement) |
| 2 QUIC send queue | **new test** (2 MB filled, reader released, fresh stream writes) with a vacuous-pass guard, `NO_DEFECT` |
| 3 PMTU black hole | **new pair of tests** (userspace 1280 path, 5 drops, transfer survives) + two controls, `NO_DEFECT` |
| 4 connection migration | `NO_DEFECT` (suite coverage; also C-T5's positive control) |
| 5 cancel / stream termination | **new regression test**, `NO_DEFECT`, mutation control red |
| 6 Mieru UDP | `NOT_COVERED` (SDK limitation at the pin) |
| 7 XHTTP/H2 | **B-1 fixed** earlier; no slow-read harness |
| 8 Naive/Reality | **fixed** (A + C); R-B4 recorded |

No new defect was found in this window. Six of its eight groups now have executed evidence; the two that
do not are named with the reason (the Mieru SDK at the pin, and the xhttp slow-read harness that was not
built). The round's products are three new test files — send-queue recovery, PMTU black hole, and stream
isolation — each with a guard or control that would notice if the mechanism it depends on changed.
