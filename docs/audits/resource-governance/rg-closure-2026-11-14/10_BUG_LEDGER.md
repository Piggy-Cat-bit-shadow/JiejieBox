# 10 Debug 缺陷台账（Bug ledger）

> 判定口径：`PRE_EXISTING_EXPOSED` = 既有缺陷被第一阶段路径暴露；
> `SELF_INFLICTED_IN_DEBUG` = Debug 轮修复过程中引入、被测试抓住并已收窄；
> `TEST_GAP` = 生产代码正确但测试无法抓住绕过；`VERIFIED_ONLY` = 本轮验证通过、未改代码；
> `DEFERRED` = 有证据但不在本轮闭环范围；`ENVIRONMENT` = 环境问题。

| ID | 级别 | 类型 | 问题 | 证据 | 处理 |
|---|---|---|---|---|---|
| **D1** | P2 | `PRE_EXISTING_EXPOSED` | `Guard.Stats()` 在两次转移交错时把旧 observation 的 level 与新 plan 拼在同一个快照里（"critical, limit 32"）。第一阶段的 OOM 报告正是读这个快照 | RED：`guard_stats_snapshot_test.go` → `level critical, limit 32 (want 8)`；确定性交错（阻塞 shed hook + fake clock，无 sleep） | `FIXED` `6d887ee49`：在 `g.access` 下同时读取 applied level 与 gate plan |
| **D2** | P2 | `PRE_EXISTING_EXPOSED` | `Permit.Wait()` 返回 `time.Since(acquired)`，把持有时间算进等待时间；load-definition 证据因此恒为 ~0 | RED：`permit_wait_test.go` → 持有 30 ms 后报 `30.49ms` | `FIXED` `f52b8102a`：grant 时记录 `granted`，Wait 返回 `granted-acquired` |
| **D3** | P1 | `PRE_EXISTING_EXPOSED`（第一阶段扩大暴露面） | 本地准入拒绝（`ErrQueueFull`/`ErrWaitTimeout`/`ErrRateLimited`）被当作真实拨号失败：删除 URLTest 节点健康测量，并以 ERROR 级日志刷屏。第一阶段把 `transport.type=quic` 创建接入同一 gate，使该错误首次能到达这两个处理器 | RED：`admission_rejection_health_test.go` 两条本地拒绝路径都删除了 `leaf-a` 的测量；对照测试证明真实失败仍会删除 | `FIXED` `c92ae7f34`：`recordSetupFailure` 统一判定；**仅三种 gate 结果**不删测量、降为 Debug |
| **D4** | P1 | `SELF_INFLICTED_IN_DEBUG` | D3 的第一版修复用 `resourceguard.IsLocalRejection`，把 `context.DeadlineExceeded` / `context.Canceled` 也当成"本地拒绝"：拨号超时被降为 Debug 并排除在健康账本之外，而超时是真正的路径失败 | 全量 `go test ./route` 抓到 `TestConnectionNXDomainLoggingIsDebug/timeout` 与 `/NXDOMAIN_mixed_with_a_real_failure` 失败（`raw/72_debug_route_full_summary.txt`） | `FIXED`（并入 `c92ae7f34`）：收窄为 `admissionBackpressure`（只认三个 gate 错误），新增 `TestDialTimeoutStillClearsTheNodeMeasurement` 钉住边界 |
| **D5** | P2 | `TEST_GAP` | 队列上界没有任何测试能抓住"上界失效"：把 `pending >= plan.queue` 改成恒假后，原有全部测试仍通过（多等的 5 s 看起来只是慢） | mutation M4：`TestQueueBoundRefusesBeyondItsLimit` 失败 | `FIXED (test)`：新增 `queue_bound_test.go`（上界两侧 + 取消离队） |
| **D6** | — | `TEST_GAP`（本轮自写测试的错误） | Debug 阶段新写的第一版 v2rayquic 生命周期测试有两处错误期望：用 `Queue=0` 的 guard 期待"排队后关闭"，以及期待成功创建后 permit 仍被持有（实际设计是创建返回即释放） | 运行输出：`the creation never reached the gate` / `expected 1, actual 2` | `FIXED (test)`：改为 `Queue=4` 的可排队 guard；成功创建同样释放 permit |
| **D7** | — | `VERIFIED_ONLY` | 陈旧 poll 覆盖新 observation | mutation M5：移除 `observation <= lastAppliedObservation` 后 `TestStalePollCannotPublishAnOlderObservation` 失败 | 现有实现正确，新增确定性测试 |
| **D8** | — | `VERIFIED_ONLY` | 取消中的 waiter 不归还队列位置 | mutation M6：`ctx.Done()` 分支不调用 `withdraw` 后 `TestQueuedCancellationLeavesNoWaiter` 失败 | 现有实现正确，新增测试 |
| **D9** | — | `VERIFIED_ONLY` | 复用被错误计费 / 共享创建重复计费 | 依赖 SDK 自身契约测试：`sing-anytls` `TestAdmitCreationIsAskedOnlyForANewSession`、`sing-quic` hysteria2/tuic `TestOneSharedCreationChargesOneAdmission` 全部 PASS；本仓 AnyTLS `TestReuseDoesNotWaitForACreationPermit`、v2rayquic `TestEstablishedSessionIsReusedWithoutASecondPermit` | 无代码改动 |
| **D10** | — | `VERIFIED_ONLY` | 压力到准入的端到端传递 | 新增 `TestCriticalPressureTightensTheRealGate`：Critical 后第 9 个创建在 100 ms 预算内被拒，`Inflight==8` | 无代码改动 |
| **D11** | — | `DEFERRED` | reclaim 执行器在销毁动作中发布 Critical（Darwin 压力通知/危机路径），可能短暂覆盖更新的测量 | 新增 `TestCriticalPublicationFromReclaimIsCorrectedByTheNextMeasuredPoll`：下一次 poll 恢复 Warning，边界为一个采样周期 | 有意行为（销毁期间背压），有界；不改代码 |
| **D12** | — | `DEFERRED` | `Level()` 返回观察值，而非 gate 已发布的 plan（build gate 可短暂使用旧 level） | `common/resourceguard/guard.go` `Level()`；build gate 只影响自身 2 个 build 槽 | 报告一致性问题，下一轮统一 |
| **D13** | — | `DEFERRED` | 第一阶段开放问题仍然开放：同键 session coalescing、XHTTP/MASQUE H3 准入、UDP NAT 首包字节账本、DNS 非 hijack 并发 | 第一阶段 `08_OPEN_QUESTIONS.md` Q1–Q7 | 证据和要求未变，本轮不扩散 |
| **D14** | — | `ENVIRONMENT` | `TestMieruOutboundCloseTerminatesSessions` 在 Windows 上找不到同时空闲的 TCP/UDP 端口（200 次尝试后 `bind: access permissions`） | `raw/70_debug_regression.txt`；重试记录 `raw/71_mieru_retry.txt`（单测与整包均通过） | 与本次改动无关，记录环境现象 |

## 验证结论

- **3 个生产缺陷**（D1–D3）已修复，均有 RED → GREEN 与独立提交；D4 是 D3 修复过程中被全量套件抓住的过宽版本，已收窄并补边界测试。
- **2 个测试缺口/错误**（D5、D6）已修复；变异 M4 证明新的队列测试具备反证能力。
- 其余为有证据的验证项（D7–D10）、明确的研究边界（D11–D13）或环境现象（D14）。
