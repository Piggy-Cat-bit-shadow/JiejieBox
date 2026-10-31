# 架构收敛后的深度排查与审计（第二阶段补充）

- **终点 HEAD**：`dbfcca7deb1`（本补充新增 1 个提交，累计 18 个）
- **工作区**：干净；`testing` 领先 `origin/testing` 18 个提交，未推送
- 本文件接续 `POST_REFACTOR_AUDIT_2026-10-17.md`；**原报告中标注为"未覆盖 / 待办"的条目，本节全部给出终局**。

---

## 一、原报告待办的处理结果

原报告列出六项尚未覆盖的并发时序和一项测试侧影子模型问题，本轮处理结果如下：

| 条目 | 终局 | 证据 |
|---|---|---|
| #11 Wi-Fi 回调 × 电源 resume **结果**（非仅互斥） | **已写** | `route/reset_outcome_race_test.go`：既接受 2 次也接受 3 次 reset（合并是合法决策），但断言环境指纹等于一次干净发布、网络最终 settled、DNS 重置数 == epoch 推进数 |
| #2 真实 socket 跨 transition commit | **已写**（上一批） | `common/dialer/transition_during_dial_test.go`：epoch 不动，仅靠"开始状态"拒绝；mutation 已验证 |
| #14 FakeIP 退役区间 + 进程重启 | **原报告判断错误——早已覆盖** | `e2e/zz_fip_placeholder_escape_test.go` 的 `TestFIPAddressFromADeadProcessDoesNotEscape` 已在本环境**实跑通过**：第二代 Box 用**全新 registry、无 ledger**，只靠缓存文件里的退役记录拒绝，日志行即为拒绝原因；对照组 `198.24.0.200` 正常放行 |
| #6 unregister 与回调体并发 | **由现有结构保证，未新增测试** | 回调体与 release 共用 `routeAddressSetAccess`，两者互斥。进一步验证并发读写需要 race detector，本轮受环境限制未执行（`BLOCKED_BY_ENVIRONMENT`） |
| #15 reload 发生在未提交 transition 中 | **不是缺陷** | reload 关闭旧 Box 并新建：新 manager 的 epoch 从 0 起，`EstablishInitialNetworkEnvironment` 首次发布指纹时会**claim 一次 transition**，因此启动期本身是 unsettled 的；旧代未提交的状态随旧 manager 一起消失。`TestTheDevicePauseSurvivesAServiceReload` 已覆盖 reload-while-paused 的一半 |
| #4 recovery 跨 generation | **已决策并解释** | 代码里没有拒绝路径是设计：`admittedRecoveredCandidates` 在 ownership 过期时 fail-open（过期判决不能拒绝可拨地址），跨代连接由 hand-over 拒绝。安全边界由 #2 的真实 socket 测试覆盖；没有增加锁定具体实现方式的测试，因为将 capture 传入 recovery 也是合法的实现方案 |
| 测试侧影子模型（hit report / benchmarks） | **已修** | `productionDirectOutbound` 现在通过 `direct.NewOutbound` 构造；三个直接使用结构体字面量的调用点也已改用构造器。更改后，旧字面量会触发 nil panic，因此得以定位这些调用点。命中率仍为 3/6 个场景、85% 权重：该测试拓扑没有 ambient 策略，fixture 机器也没有相关判决，说明此前结果相同并不能证明影子模型正确 |

**结论：本轮在该环境中可处理的问题已完成。** 剩余事项包括已经落实到代码或测试中的设计取舍（见原报告 F1/F3），以及受环境限制而未完成的验证（见第四节）。

---

## 二、失败处理策略（任务要求的清单）

`fail-closed` = 不确定时拒绝；`fail-open` = 不确定时放行。**每个不作判断并选择放行的分支，都需要说明放行为何安全。**

| 位置 | 极性 | 理由（也是它的守护测试） |
|---|---|---|
| `SocketSemantics` 零值 / 未分类字段 | **closed** | 未分类的 profile 不能视为与普通 connect 等价；`TestUnclassifiedOptionBlocksEverything`、`TestAnUnclassifiedFieldRefusesEvenWhenItIsZero` |
| `NetworkPolicyBlockers(nil)` | **open** | 没有 manager 时不存在会被遗漏的 ambient 策略；`TestNoAmbientPolicyStillAllowsTheBypass` |
| `NetworkPolicyBlockers`：`auto_detect_interface` | **open（有意）** | 与平台在单网卡下的选路一致；拒绝会关掉默认配置的 fast path；`TestAutomaticBindingIsDeliberatelyNotABlocker` |
| bypass：链上无 resolve wrapper | **open** | 此时 userspace 也不应用策略，两侧一致；`TestABypassWithoutAnEffectivePolicyStillWorks` |
| bypass：无 `LocalFamily` 能力 | **open** | 能力缺失 ≠ 可拨；"无意见"是设计降级；`TestBypassWithoutTheCapabilityIsUnchanged` |
| bypass：目的地址非法 / 带 zone | **closed** | 平台路径按地址索引，无法表达此类地址；`TestLiteralDestinationIsNotResolution` 一族 |
| bypass：tracker / power report 活动 | **closed** | 不能静默丢失统计信息；`TestARunningPowerReportRefusesTheFastPath` |
| `localFamilyPolicy`：ownership 过期或 unsettled | **open** | 过期判决不能否认候选；由 hand-over 兜底；`TestOldVerdictCannotDenyNewGenerationScheduler` |
| `localFamilyPolicy`：能力缺失 | **open** | 同 `restrictsNothing`；`TestDialerWithoutACapabilityIsUnchanged` |
| `ownership`：无 source（manager 什么也不报） | **open** | 区分不成立，检查不适用；`TestCaptureEpochWithNoSourceAtAllStillAbsolves` |
| hand-over（`stillCurrentEpoch`） | **closed** | 跨代连接属于已离开的网络；`TestCase1ResetBeforeTheDialReturns`、`TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits` |
| hand-over 拒绝后连接仍 tracked | **保守** | 丢弃会留下无人可关的 socket；`TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits`（末段） |
| `DialSerialNetwork`：无并行面 → 串行 fallback | **open** | 唯一"合法缺失"的调用者是 detour，其目的地是对端业务，逐连接网络选项本不适用；`TestAPlainLeafStillDialsThroughTheFailoverGroup` |
| DNS `networkTransitionStable`：无 source | **open** | 区分不成立；否则所有查询都不缓存；`TestTheSettledStateIsReadFromTheSnapshotWhenTheGetterIsAbsent` 的末段 |
| DNS 缓存：捕获到的 environment 未知(0) 且当前非 0 | **closed** | 无法归属的答案不入缓存；`finishCacheKey` 一族测试 |
| DNS：策略纪元守卫（check-then-store） | **closed** | 跨纪元答案不能落到新纪元；`policy_cache_commit_window_test.go` |
| `resolveDialer.ListenPacket`：literal 被有效策略排除 | **closed** | 与 stream 一致；`TestAStrictFamilyPolicyIsAppliedOnEveryPath` |
| `NextLocalDialSurface`：缺规划面 | **closed（构造错误）** | 不降级到窄 helper；`TestASurfaceWithoutThePlanningCapabilityIsAnError` |

---

## 三、runtimecoord 边界（prompt §三.6）

- **本轮未触碰**：`git diff 396f2ae9826..HEAD -- common/runtimecoord/` 为空；`transport/wireguard` 同样为空。
- **它拥有的状态**：`epoch` / `changed` / `registrated`（`Coordinator`），`Registration` 的 `epoch`、`observed`、`lease`、`nextLease`、`lastRebind`、`rebindCount`、`window`，以及 `PlatformEvents` 的 `paused`、`wakePending`、`screenOn/appForeground`、`lastTrimLevel/lastTrimAt/lastReleaseAt`。
- **它不拥有**：WireGuard/MASQUE session、DNS pool、QUIC 状态、任何协议重试/backoff 状态机（其公开面只有 `Epoch`/`Advance`/`Close`/`GenerationChanged`/`Register`）。
- **判断**：属于 prompt 允许的集合（generation 发布、注册、stale、取消、恢复合并与窗口）；`lastTrim*` 是内存压力的合并窗口，同一族而非新职责。**无需回收。**

---

## 四、环境阻塞（不是 PASS）

| 项 | 为什么阻塞 | 它本应证明什么 |
|---|---|---|
| `-race` | 本机无 C 工具链（`gcc`/`cl` 缺失，`CGO_ENABLED=1` 时 `runtime/cgo` 构建失败） | 并发读写观测三元组、lease、DNS pin 不撕裂；替代证据是结构性收敛（单次 capture + 单次短锁）与 `route` 包既有的时序/死锁测试 |
| 真机 / 跨平台行为 | 需要带已发布 nftables 规则的 Linux 内核（redirect 下 ambient bind 与平台连接是否等价）、多网卡宿主、Android `protect` 设备 | 决定 `auto_detect_interface` 是否应当成为 ambient blocker；决定 `netns` 是否真的在 redirect 路径上生效 |
| 完整 CI | 树内无 workflow-run 记录 | 跨平台 PASS 的权威证据 |

**本轮所有结论的证据等级仍是 `SOURCE + LOCAL TEST`**，没有一条达到 `VERIFIED_CI` 或设备级。

---

## 五、最终仓库状态

| 项 | 值 |
|---|---|
| 分支 | `testing` |
| HEAD | `dbfcca7deb1` |
| 本阶段提交 | 7 个：单次所有权读取 · 拒绝穿过 detour · 路径并排覆盖 · 编译期 pin + ambient 生产测试 · 真实 dial 跨 commit · 窄化 hop 协议 + 补第三处查找 · 竞速 reset 结果 + 真实 outbound 的命中率 |
| 累计（相对 `396f2ae9826`） | 18 个提交，54 个文件，+3300 左右 |
| 工作区 | 干净 |
| 测试 | `go build ./...` PASS；`common/dialer` / `protocol/direct` / `route`（423s）/ `dns` / `adapter` / `transport/wireguard` / `common/httpclient` / root / `protocol/group` / `experimental/libbox` 全 PASS；`e2e` 的 FakeIP 两条重启行实跑 PASS |
| mutation 验证 | 本阶段新增 11 条，累计 18 条，每条都在还原前确认失败 |
| 推送 | **未推送**（ahead 18） |
