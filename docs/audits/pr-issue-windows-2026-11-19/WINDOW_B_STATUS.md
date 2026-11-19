# Window B（协议传输：QUIC / H3 / XHTTP / Naive）——逐组执行状态

Same rule as `WINDOW_A_STATUS.md`: what was executed, what it proved, and what could not be observed.
The file's 48-seed appendix was read at group level only.

Base: `testing` @ `11695cbb4` plus this round's commit.

## Group 1 — H3 小流公平性（大下载流并行小 GET/CONNECT）

`transport/trusttunnel` 52.8 s, `transport/v2rayxhttp` 21.9 s, `common/httpclient` 2.9 s —
all green under the shipped Windows profile. **`NO_DEFECT` by coverage**; no new fairness harness was
built (one would need a rate-limited path and per-stream latency attribution).

## Group 2 — QUIC 发送队列（full→writable 临界事件，对端不发新包）

**`NOT_COVERED`.** Driving a `quic.Config` send queue to `full` requires controlling the peer's
receive window; the in-package fixtures have a real endpoint but no window control. The risk this group
names (a stalled send blocking an unrelated stream) is structurally limited by quic-go's per-stream
flow control, which is why it was not prioritised in a freeze.

## Group 3 — PMTU 黑洞（MTU 1280/1290、不可达 ICMP/EMSGSIZE）

**`NOT_OBSERVABLE`.** `NewClient` enables path-MTU discovery on Linux/Windows
(`DisablePathMTUDiscovery: !C.IsLinux && !C.IsWindows`), and a black hole needs a path that drops
oversized datagrams; no netem/namespace here. Nothing is claimed.

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
| 1 H3 small-flow fairness | `NO_DEFECT` (suite coverage) |
| 2 QUIC send queue | `NOT_COVERED` (needs window control) |
| 3 PMTU black hole | `NOT_OBSERVABLE` (needs netem) |
| 4 connection migration | `NO_DEFECT` (suite coverage; also C-T5's positive control) |
| 5 cancel / stream termination | **new regression test**, `NO_DEFECT`, mutation control red |
| 6 Mieru UDP | `NOT_COVERED` (SDK limitation at the pin) |
| 7 XHTTP/H2 | **B-1 fixed** earlier; no slow-read harness |
| 8 Naive/Reality | **fixed** (A + C); R-B4 recorded |

No new defect was found in this window. The value of the round is group 5's regression, which pins the
multiplexing property that every other protocol in this window depends on, and the explicit statement
that groups 2 and 3 were not attacked rather than passed.
