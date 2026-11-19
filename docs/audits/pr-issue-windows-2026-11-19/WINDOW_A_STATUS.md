# Window A（核心控制面：TUN / DNS / 生命周期）——逐组执行状态

This page answers window A's eight first-round attack groups one by one: what was actually executed,
what it proved, and what could not be observed here. The file's appendix (80 numbered seeds) was read
at group level only; no per-seed id is claimed.

Base: `testing` @ `d7f4cc327` (plus this round's commits).

## Group 1 — DNS 交叉阻塞（一个卡住的 DNS 不得拖住无关 TCP；队列不得无限等待）

**Executed.** The DNS path has no process-wide lock: a stuck transport holds its own goroutine, and the
`resourceguard` queue is bounded with a per-level wait (`PAIRWISE_A_MATRIX` rows for `dns/dialer` +
window A's gate tests). `go test ./dns/... ./common/dialer` green (8.7 s + 6.1 s + 22.0 s), and
`-race ./common/dialer` green (23.3 s).

**Verdict `NO_DEFECT`.** The isolation is structural (per-transport state, no shared mutex on the
resolve path), which is why there is no test named "a stuck DNS does not block TCP": there is no single
place that could block everything.

## Group 2 — DNS stream 隔离（并发请求乱序/短读/EOF；响应绝不串线）

**Attacked this round; `NO_DEFECT`, with the mechanism established rather than assumed.**

The obvious harness — two concurrent `Exchange` calls, server answers in reverse, assert each caller
got its own answer — **deadlocks in this repository**, and the deadlock is the finding, not a flake:

- `dispatch` (`dns/transport/multiplexer.go`) states the serial path is unconditional: *"one connection
  at a time, held across sequential queries by the serial pool. Only intra-connection pipelining is
  given up."*
- `ConnPoolOrdered` holds **at most one connection out at a time**, so two callers cannot both be
  mid-query on the same socket.

A single-connection harness therefore hangs: the second query's bytes never appear because the second
caller is queued behind the first. When both callers are allowed their own socket, the correlation is
trivial (one outstanding query per socket), so "两个响应串线" is impossible by construction - a stronger
guarantee than matching message IDs. The first version of the harness was deleted rather than committed
red; this paragraph is its evidence.

Residual coverage: `dns/transport/multiplexer_test.go` covers sequential reuse on one kept-alive
connection, a read error on a reused connection, timeout invalidation, and "a slow query does not poison
the pool". The `recvLoop`'s ID-keyed completion exists for the shared-connection path.

## Group 3 — FakeIP 冷启动（timer 连续更新、kill/restart、断写入；映射与元数据一致）

**Executed, four defects fixed in this effort:**

| Defect | Fix | Test |
| --- | --- | --- |
| a `/31` or `/32` range issued an address outside itself | `issuableAddress` | `store_range_test.go` |
| a placeholder from a retired generation bypassed the router via the route set | `fakeIPPlaceholder` | `fakeip_retired_guard_test.go` |
| the durable retirement record had no reader without a fakeip server | cache-file fallback | `fakeip_durable_retired_test.go` |
| a reload re-issued a held address | cursor resumes past the ledger | `store_reload_test.go` |

**Verdict `CONFIRMED → FIXED`.**

## Group 4 — UDP 生命周期（reject + sniff + Close + 网络切换交错；PacketConn 与 flow map 最终可回收）

**Executed in part.** The fake-IP NAT wrapper now has its first contract tests (triple B, TB-6): upload
reported as the domain and then as the resolved address, download delivered from the fake address, the
app-driven direct exception. The route-set guard covers the "reject + sniff" interleaving.

**`PARTIAL`.** The sing-tun ownership half (a NAT entry's dial is not cancelled when the entry is
evicted) is cross-repo and recorded; the in-repo entry point (`NewPacketConnection` →
`this.ListenPacket` with no admission) is identified and deliberately not gated during the freeze
because it would refuse plain UDP at Critical — see `PAIRWISE_C_RELEASE_READINESS.md` §5.

## Group 5 — URLTest / Selector Race（热测速、热更新、热切组并发）

**Executed: one defect confirmed and fixed.** A provider that replaced or removed a node left the
*predecessor's* URL-test measurement in the storage, so the group's ordering and the dashboard used
evidence about an object that no longer exists; a reused tag inherited it. Fixed in `adapter/provider`
(commit `29289af38`), with `health_history_ownership_test.go` and two mutation controls that each fail
their own half. `common/urltest` under the release profile: 45.4 s green.

## Group 6 — Dual-stack dial（A/AAAA 延迟、IPv6-only、频繁取消）

**Executed.** `common/dialer`'s ~45-file suite, 22.0 s, plus `-race` 23.3 s, green. The winner/loser
ownership is the suite's own subject matter; no new attack was built.

**Verdict `NO_DEFECT` (existing coverage), `-race` included.**

## Group 7 — TUN 恢复与回滚（VM/namespace 中断、nftables 路由差分）

**`NOT_OBSERVABLE`.** Windows host: no network namespace, no nftables, no privilege. Nothing here is
claimed about `auto_redirect` or host-rule preservation.

## Group 8 — 资源回落（大量短连接 → 静置 → GC → 重复）

**Executed.** Window A's resource-governance round: the naive pressure hook that cut live tunnels, the
admission gate's recovered-capacity edge, the hy2/tuic shut-down state, and the oomkiller→gate→real
creation test with its mutation control. `service/oomkiller` + `common/resourceguard` under the release
profile: 0.9 s + 1.2 s green; `-race` on both, green.

## Summary

| Group | Verdict |
| --- | --- |
| 1 DNS cross-blocking | `NO_DEFECT` (structural) |
| 2 DNS stream isolation | `NO_DEFECT` (structural; the naive harness deadlocks, recorded) |
| 3 fake-IP cold start | **4 fixed** |
| 4 UDP lifecycle | `PARTIAL` (wrapper tested; cross-repo half recorded; in-repo gate deliberately not applied) |
| 5 URLTest/Selector race | **1 fixed** |
| 6 dual-stack | `NO_DEFECT` (suite + race) |
| 7 TUN recovery / nftables | `NOT_OBSERVABLE` |
| 8 resource return | **4 fixed** (window A round) |

No new defect was found in groups 1, 2 or 6 this round; the value of running them was the negative
evidence above, especially group 2's mechanism, which had been asserted in prose and is now stated with
the reason a naive test cannot pass.
