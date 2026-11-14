# 09 定向对抗 Debug 报告（第二阶段）

> 对象：第一阶段分支 `work/resource-governance-closure-2026-11-14`
> Debug 起点：`ce95f50ea`（第一阶段最终 HEAD）
> 基线：`testing` @ `fa6150073`
> 本轮性质：独立、敌意、证据驱动的 Debug；只修与第一阶段直接相关的缺陷。
> **最终判断：`DEBUG_CLOSED`**（真机项按 `NEEDS_DEVICE` 标注，不并入闭环）。

## 0. 结论摘要

第一阶段的三项改造经调用链、账本、生命周期、平台隔离、变异测试与性能测量验证**确实生效**，但 Debug 发现并修复了 **3 个真实缺陷**（2 个 P2、1 个 P1）、**1 次自查回归**（D3 过宽版本，被全量套件抓住）与 **2 个测试缺陷**：

| 编号 | 缺陷 | 级别 | 状态 |
|---|---|---|---|
| D1 | `Guard.Stats()` 在转移交错时把旧 level 与新 plan 拼成自相矛盾的快照（"critical, limit 32"） | P2 | `FIXED` `6d887ee49` |
| D2 | `Permit.Wait()` 把持有时间算进等待时间（load 证据恒为 ~0） | P2 | `FIXED` `f52b8102a` |
| D3 | 本地准入拒绝被当成路径失败：删除 URLTest 节点测量 + ERROR 刷屏 | P1 | `FIXED` `c92ae7f34` |
| D4 | D3 的第一版修复过宽（把拨号超时也降级），被全量 route 套件抓住 | 自查回归 | 已收窄并补边界测试（并入 `c92ae7f34`） |
| D5 | 队列上界没有测试能抓住"上界失效"（变异后全绿） | 测试缺口 | 已补 `queue_bound_test.go` |
| D6 | Debug 自写测试的两处错误期望 | 测试缺陷 | 已修正 |

6 个变异全部被测试抓住；压力→准入→创建的真实链路有端到端证据；性能变化可解释。详见 `10_BUG_LEDGER.md`。

## 1. Git 与范围

```text
fresh clone : C:\Deepseek\rg-debug-2026-11-14（从 GitHub 重新 clone）
branch      : work/resource-governance-closure-2026-11-14（第一阶段同一分支）
clone HEAD  : ce95f50ea（= origin/work/...，已核对）
base        : fa6150073（testing 未动）
```

第一阶段生产改动只有 4 个文件：`route/conn.go`、`service/oomkiller/{timer,recorder}.go`、`transport/v2rayquic/client.go`；其余为测试与证据文档。Debug 逐文件沿调用链检查，未扩展到无关模块。

## 2. 新 Gate 是否真的到达昂贵创建（§2.1）

### 2.1 route 非 connected UDP setup

```text
route.NewPacketConnection (route/conn.go:820-873)
  trafficclass.WithClass(ctx, metadata.TrafficClass)   ← 一次 context value，listen 之前
  releaseDial := m.enterDialGate(ctx, class)           ← 切换窗口内的软限速，listen 之前
  setupCtx, finishSetup := m.dialSetups.begin(ctx)     ← 代次 context，listen 之前
  dialer.ListenSerialNetworkPacket / ListenPacketWithDestination / this.ListenPacket
  finishSetup(); releaseDial()                         ← 建立或失败都归还
```

- **顺序**：三个 gate 都在 `ListenPacket` 之前；`finishSetup` 在返回后立即调用，成功/失败同一路径。
- **昂贵分配**：真实 socket / 握手在 `ListenPacket` 内部，位于 setupCtx 之下。网络切换取消 setup 的测试证明：取消时拨号立即返回 `context.Canceled`，不产生已建立连接（`TestNetworkTransitionCancelsAnInFlightPacketSetup`）。
- **既有限制（非本轮引入）**：TUN NAT entry、首包引用和 NAT goroutine 在 route 之前创建。这不因 route 层修复而消失，`03`/`04` 已记录，属 `DEFERRED` 的依赖仓库问题。
- **变异反证**：M1 把 `setupCtx` 换回 `ctx` → 测试立即失败。

### 2.2 v2rayquic 共享 QUIC 创建

```text
DialContext → offer()
  ├─ 池中活跃连接且代次未变 → 直接返回（不进入创建）
  └─ 无连接/陈旧 → newOfferContext → pending slot → go completeOffer
       completeOffer → createConnection
         ├─ admission == nil → dialConn(offerCtx)
         └─ AdmitCreation(offerCtx, dialConn) → offerNew: UDP dial + QUIC handshake
```

- **准入在构造之前**：`AdmitCreation` 只包住构造调用；拒绝时构造器一次都不执行（`TestSaturatedAdmissionRefusesTheSharedCreation`，变异 M3 反证）。
- **复用不计费**：池命中路径不经过 `createConnection`；`TestEstablishedSessionIsReusedWithoutASecondPermit` 断言 `Admitted=1`、第二次 demand 不增加。
- **取消/关闭 exactly-once**：`TestCloseDuringAdmissionReleasesTheCreationWaiter`（排队中被 Close → `net.ErrClosed`、waiters=0、未执行构造）、`TestFailedCreationReleasesThePermit`、`TestAStaleCreationDoesNotChargeTheNextOne`。
- **ctx 语义**：`newOfferContext` 从触发 demand 复制值（class、`HeldPermit`），从 client 复制生命周期；`TestTheSharedCreationSeesTheTriggeringFlowClass`、`TestAnAlreadyAdmittedDialIsNotChargedTwice` 钉住。

### 2.3 OOMKiller → Guard → 创建

```text
poll → readSample (memory.Total = phys_footprint via task_info)
  → nextState (footprintUnavailable → armed/hold)
  → pressure.Store(MemoryPressure)
  → Service.guardLevel() (与 TUN 共用同一个 atomic)
  → guard.observe → apply → gate.setPlan
  → 协议 AdmitCreation → gate.Acquire
```

- 映射完整：`TestGuardLevelMapsEveryPublishedPressure`（含未知值→Normal）。
- **真实改变行为**：`TestCriticalPressureTightensTheRealGate`：Critical 后第 9 个创建在 100 ms 预算内被拒，`Inflight==8`、`MaxInflight==8`。
- **恢复**：`common/resourceguard/guard_cycles_test.go`、`attack_test.go`（demote dwell、压力回落后再施压）、`growth_relay_test.go` 的恢复用例。

## 3. 复用与创建边界（§2.4）

| 路径 | 证据 | 结论 |
|---|---|---|
| AnyTLS 复用/创建顺序 | 依赖 SDK `TestAdmitCreationIsAskedOnlyForANewSession`；本仓 `TestReuseDoesNotWaitForACreationPermit`、`TestCreationAdmissionSeamDoesNotAskOnReuse`（真实本地 AnyTLS server） | 复用不计费 |
| AnyTLS 嵌套/链式 | `TestNestedCreationDoesNotAskTheGateAgain`、`common/tls/marker_scope_test.go` | 一次逻辑拨号一份许可 |
| AnyTLS 失败归还 | `TestFailedCreationReleasesThePermit` | exactly-once |
| Hysteria2 / TUIC | 依赖 SDK `TestOneSharedCreationChargesOneAdmission`、`TestAdmissionRefusalDoesNotDial`（两个协议都 PASS） | 每条物理 QUIC 连接一次 |
| v2rayquic | 本轮 `admission_test.go` + `admission_lifecycle_test.go` | 创建一次，复用零次 |
| 共享 TLS | `common/tls/handshake_admission_test.go`、`handshake_class_test.go` | 物理 TCP+TLS 一次 |
| Mieru | `protocol/mieru/dialer.go:85-122` 在 `newUnderlay` 之前准入；本地 e2e 通过 | socket 创建前 |

**没有因为"Dial 被调用一次"推断一次握手**：AnyTLS 用真实本地 server 的 accept 计数（`accepted.Load()==1`），QUIC 用构造器调用计数，guards 用 gate 计数。

## 4. 账本守恒与有界性（§2.2/2.3）

### 4.1 守恒量

| 不变量 | 证据 | 结果 |
|---|---|---|
| `requested == admitted + refused + timedOut + canceled` | `TestAttackMixedOutcomesKeepTheAccountingExact`（burst 0/1/31/32/33/200/500/1000）；本轮 `TestAdmitCreation*` | 成立 |
| `admitted - released == inflight` | 同上；关闭后 `inflight==0` | 成立 |
| waiter 结束即离队 | `TestQueuedCancellationLeavesNoWaiter`；变异 M6（取消不 withdraw → 失败） | 成立 |
| permit 双释放不发生 | `TestAdmitCreationDoubleReleaseIsInert` | 成立 |
| 构造 panic 仍归还 permit | `TestAdmitCreationReleasesThePermitWhenTheCreatePanics` | 成立 |
| 拒绝不调用构造 | `TestAdmitCreationRefusalDoesNotRunTheConstructor`；变异 M3 | 成立 |

### 4.2 队列有界

- **数量**：`TestQueueBoundRefusesBeyondItsLimit`：limit 1 / queue 2 / 10 callers → 恰好 2 个排队、8 个立即 `ErrQueueFull`；变异 M4（上界恒假）失败。
- **字节**：gate 的 waiter 只持有一个 waiter 结构 + channel + timer；真实创建尚未开始，因此**没有 buffer 字节**需要记账。字节预算在别处：NAT 队列（依赖仓库 256 KiB/会话、8 MiB/NAT）、copy growth（256 方向 + 预留）。
- **等待者 goroutine**：不额外创建——调用者停在自己的 channel 上（变异/扫描见 `gate.go` 注释与 `load_definition_test.go` 的 192 caller 压测）。
- **公平性**：FIFO + 老化（`StarvationAfter`）已有测试。

## 5. 生命周期与确定性交错（§3.2）

| 交错 | 测试 | 结果 |
|---|---|---|
| A 观察 Armed 暂停 → B 应用 Critical → A 恢复 | `TestAStaleObservationCannotReopenTheGate`（直接驱动 observe/apply） | 旧状态不能覆盖新 plan |
| 旧 Critical 晚到 | `TestAStaleCriticalObservationCannotShedAgain` | 不重复 shed、不重新限流 |
| 指标读取与转移交错 | **本轮新增** `TestAttackStatsSnapshotCannotPairAStaleLevelWithANewerPlan`；阻塞 shed hook + fake clock，无 sleep | 修复前 RED（critical/limit 32），修复后一致 |
| 陈旧 poll 覆盖新 observation | **本轮新增** `TestStalePollCannotPublishAnOlderObservation`（sampleFunc 作为暂停点） | 旧 poll 被丢弃；变异 M5 反证 |
| 并发指标 + 准入 | `TestConcurrentSnapshotsKeepLevelAndLimitInStep`、`TestShedIsNotRunByAConcurrentSnapshotAfterTheFirst` | 每个快照自洽；shed 恰好一次 |
| 关闭/取消 | `TestCloseDuringAdmissionReleasesTheCreationWaiter`、`TestCloseCancelsInFlightDialsAndPreventsResurrection` | 不复活、不泄漏 |
| 网络切换 | `TestNetworkTransitionCancelsAnInFlightPacketSetup`、`TestNetworkTransitionSparesADialStartedAfterIt` | 按代次取消 |
| 回收期 Critical 发布 | `TestCriticalPublicationFromReclaimIsCorrectedByTheNextMeasuredPoll` | 一个采样周期内被测量纠正 |

**没有用 sleep 制造因果**：所有交错由 channel/barrier/fake clock 驱动；仅有两处用真实短睡眠让"时间流逝"本身可观测（Permit.Wait 的 30 ms、AnyTLS idle 列表的 20 ms），断言留有 3 个数量级余量。

## 6. OOMKiller 与 ResourceGuard 的单一权威（§3.1）

- 压力事实只有 `Service.pressure` 一个 atomic；guard 通过 `guardLevel()` 读取，TUN 读同一个值。`TestCriticalPressureTightensTheRealGate` 证明它改变真实准入。
- `observe/apply` 的 order 由 transition 版本号保证（既有测试 + 本轮 poll 交错测试）。
- **发现并修复**：`Stats()` 曾经能把两次转移拼进一个快照（D1）。
- shed 只在进入 Critical 的边沿执行（`TestAttackShedIsEdgeTriggeredFromTheMetricsPath`、并发版）；shed hooks 只关闭空闲 session（`protocol/anytls/outbound.go:116`、`naive:323`、`trusttunnel:153`），不关闭有流的 session（`soak_cycles_test.go`）。
- **有意保留的行为**：reclaim 执行器在销毁动作中发布 Critical（Darwin 压力通知/危机路径），下一次测量 poll 纠正；测试证明边界为一个采样周期，而非永久降级。
- **已知不一致（DEFERRED）**：`Level()` 返回观察值；build gate 可能短暂使用旧 level（只影响自身 2 个 build 槽）。记录在台账 D12。

## 7. iOS 预算与平台隔离（§4）

| 检查 | 结果 | 证据 |
|---|---|---|
| footprint 来源 | `TASK_VM_INFO.phys_footprint`（bytes），不是 RSS | `sing/common/memory/memory_darwin.go`（依赖 pin） |
| Go heap/stack/sys 分列 | timeline 各自字段 | `recorder.go` timelineRow |
| GOMEMLIMIT 与进程上限区分 | `RuntimeMemoryLimit = armed = 40 MiB`，仅约束 Go runtime | `timer.go:1235-1238`、`experimental/libbox/setup.go:136-137` |
| 采样失败 | 本轮验证：0 → 不可测量 → ARMED，不发布 Normal | `TestUnmeasurableFootprint*`、变异 M2 |
| 平台隔离 | `policyModeNetworkExtension` 需要 `IsIos && UnderNetworkExtension`；host 构建即使显式配置 memory_limit 也不会选中 50 MiB 策略 | **本轮新增** `TestResolvePolicyModeCannotSelectTheNetworkExtensionBudgetOffIOS`（default / memory_limit / override 三种配置） |
| 不可测量规则的范围 | NE 与显式 memory_limit 收紧为 ARMED；available 模式行为不变 | **本轮新增** `TestUnavailableFootprintRuleIsScopedToFootprintModes` |
| 真机 footprint / Jetsam | 无设备 | `NEEDS_DEVICE` |

## 8. 变异测试（§6.2）

| 变异 | 目标测试 | 结果 |
|---|---|---|
| M1 非 connected UDP 传 `ctx` 而非 `setupCtx` | `TestNetworkTransitionCancelsAnInFlightPacketSetup` | **FAIL（被抓住）** |
| M2 删除 `footprintUnavailable` 分支 | `TestUnmeasurableFootprint*` | **FAIL（3 个用例）** |
| M3 v2rayquic 绕过 admission | `TestSaturatedAdmissionRefusesTheSharedCreation` | **FAIL** |
| M4 gate 队列上界恒假 | `TestQueueBoundRefusesBeyondItsLimit` | **FAIL** |
| M5 删除 poll 陈旧检查 | `TestStalePollCannotPublishAnOlderObservation` | **FAIL** |
| M6 取消不移除 waiter | `TestQueuedCancellationLeavesNoWaiter` | **FAIL** |

完整输出：`raw/50_mutation_log.txt`。变异已全部用 `git checkout` 撤销，最终工作树不含破坏性改动（见 §12）。

## 8.1 全量套件抓住的一次自查回归（D4）

D3 的第一版修复把 `resourceguard.IsLocalRejection` 当作"本地拒绝"的判据，而该函数同时覆盖
`context.DeadlineExceeded` / `context.Canceled`。全量 `go test ./route` 立即失败：

```text
--- FAIL: TestConnectionNXDomainLoggingIsDebug/timeout
--- FAIL: TestConnectionNXDomainLoggingIsDebug/NXDOMAIN_mixed_with_a_real_failure
```

即：拨号超时被降级为 Debug 并从健康账本中排除。修复收窄为 `admissionBackpressure`（只认
`ErrQueueFull` / `ErrWaitTimeout` / `ErrRateLimited`），并新增 `TestDialTimeoutStillClearsTheNodeMeasurement`
把边界钉住。证据：`raw/72_debug_route_full_caught_regression.txt`；修复后的全量运行：
`raw/74_debug_route_full_final.txt`（1186 PASS / 0 FAIL / 446.6 s）。

## 9. 性能 A/B（§7）

环境：同一主机、同一 Go、`CGO_ENABLED=0`、`-benchtime=300ms -count=5 -benchmem`；基准文件同时复制到 `fa6150073` 干净 worktree 运行。

| 基准 | base | 分支 | 差异 |
|---|---|---|---|
| route 非 connected UDP setup（失败拨号，per flow setup） | 880.8 ns/op，1144 B/op，10 allocs/op | 1908 ns/op，1440 B/op，16 allocs/op | +1.03 µs，+296 B，+6 allocs |
| v2rayquic 共享创建 NoGuard（per creation） | 1592 ns/op，320 B/op，5 allocs/op | 2080 ns/op，512 B/op，10 allocs/op | +488 ns，+192 B，+5 allocs |
| v2rayquic 共享创建 WithGuard | 1615 ns/op，320 B/op，5 allocs/op（hook 不存在） | 2983 ns/op，672 B/op，14 allocs/op | +1.37 µs，+352 B，+9 allocs（其中 admission ≈ +0.90 µs / +4 allocs） |

**解释**

- 三条路径都是**每次 flow setup / 每次共享创建一次**，不在数据面、不在每包、不在每 stream。
- route 的 +6 allocs 来自：class context value（1）、dial gate（0–1）、setup context + AfterFunc（2–3）、错误路径绝对值相同但 context 生命周期更长。
- v2rayquic 的 +5 allocs 来自 `WithoutCancel` + `WithCancelCause` + `AfterFunc` 的注册；admission 再加 gate 的 permit/waiter 分配。
- 与真实网络成本相比：一次 UDP setup 通常 10 µs–ms 量级，+1 µs 不改变量级；QUIC 握手是 ms 量级。
- **没有可测量的稳态回归路径**（copy/batch/splice 未被修改），但没有真机吞吐数据，故 iOS 性能仍为 `NEEDS_DEVICE`。

原始输出：`raw/60_bench_branch.txt`、`raw/61_bench_base.txt`。

## 10. 测试自审（§6.3）

- 本轮自写测试先有两处错误期望被运行结果暴露（D6），已修正——这正是"测试本身也要被 Debug"的证据。D4 则证明全量套件仍然必要：一个"只改日志等级"的过宽修复会放过拨号超时，是被既有测试抓到的。
- 所有压测/等待都有 watchdog（`Eventually` 带 2 s 上限、`select` + `time.After`），没有无限等待。
- `-race` 覆盖新增用例；新用例 `-count=5` 重复执行（`raw/73_interface_and_repetition.txt`）。
- 变异测试证明用例真的经过生产代码，而不是只测 mock。

## 11. 发现的问题与提交

```text
6d887ee49 fix(resourceguard): report the level the gate is publishing, not the stale observation
f52b8102a fix(resourceguard): measure admission wait, not holding time
c92ae7f34 fix(route): keep local admission refusals out of node health
          （含收窄后的 admissionBackpressure 与 TestDialTimeoutStillClearsTheNodeMeasurement；
           第一版过宽修复被全量 route 套件抓住，见台账 D4）
26eaf64a1 test: adversarial coverage for the round-1 admission seams
```

（随后提交：队列上界测试、平台隔离测试、基准与证据文档；最终 SHA 见 §13。）

## 12. 未验证边界

| 项 | 状态 | 缺少什么 |
|---|---|---|
| iOS NE 真机 footprint 曲线 / Jetsam / 采样失败时的 ARMED 行为 | `NEEDS_DEVICE` | iPhone + 签名 NE + 诊断导出 |
| Apple 交叉构建与 ABI | `BLOCKED_BY_ENVIRONMENT` | Apple SDK / 交叉 cgo |
| 真实 TUN 下 200–2000 UDP 流压力与峰值/回落 | `BLOCKED_BY_ENVIRONMENT` | Windows 主机无 TUN；纯 Go 注入不能替代 |
| iOS 真机吞吐/延迟 | `NEEDS_DEVICE` | 同上 |
| 同键 session coalescing、XHTTP/MASQUE H3 准入、UDP NAT 首包字节账本、DNS 非 hijack 并发 | `DEFERRED` | 见第一阶段 `08_OPEN_QUESTIONS.md` Q1–Q7 |

## 13. §9 完成标准逐项核对

- [x] 新 Gate 的真实调用路径已追踪到昂贵创建函数（§2）
- [x] 证明 admission 在关键分配之前（§2、拒绝不执行构造；NAT 前置成本如实标注）
- [x] 复用命中不会错误占用新建许可（§3）
- [x] 等待者、队列、字节预算有界（§4）
- [x] 取消、超时、失败、Close、Network Change 均 exactly-once 释放（§5、§4）
- [x] OOMKiller 与 ResourceGuard 状态/执行权一致（§6；D1 已修）
- [x] 压力状态真实改变 gate plan 和创建行为（§6）
- [x] 恢复状态真实恢复正常准入（§6，既有 recovery 测试）
- [x] 活跃连接没有被错误回收（§6，shed 仅空闲 session）
- [x] iOS 平台预算没有泄漏到其他平台（§7）
- [x] XHTTP/Mieru 等交界面没有退化（`raw/73_interface_and_repetition.txt`；第一阶段未改这些生产文件）
- [x] race / repeated / fuzz / integration / build 实际执行（§9、§12；route 全量含 fuzz seeds，AnyTLS/Mieru 本地 e2e）
- [x] 至少一项 mutation 证明测试会抓到绕过（§8，六项）
- [x] 正常路径性能无无法解释的回归（§9，差异已解释；设备吞吐仍 NEEDS_DEVICE）
- [x] 真机 Footprint/Jetsam 项准确标 `NEEDS_DEVICE`（§12）
- [x] 新发现有 Bug Ledger 和证据（`10_BUG_LEDGER.md`）
- [x] 最终代码、测试和报告提交并 Push 到同一 feature branch，本地与远端 HEAD 一致，工作树干净（§14）

## 14. 最终状态

最终 SHA、push 与远端核对记录见 `raw/80_debug_push.txt`。Debug 轮提交：`6d887ee49`、`f52b8102a`、`c92ae7f34`、`26eaf64a1`、`5914597b4`、`a6b7d5135`（随后为证据文档提交）。
