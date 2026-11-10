# 03｜网络生命周期与资源所有权说明

本文件描述 `integration/0.1.45-final` 在阶段 03 之后的连接生命周期状态机，以及每个资源的创建者、持有者、释放者。它对应阶段报告 §5 的设计决策，也是后续阶段（DNS/H3 回退、NE/客户端）判断"谁该关闭什么"的依据。

所有表述以当前树为准（提交见 `INTEGRATION-LOG.md` 的 Stage 03 记录）。

---

## 1. 参与对象

| 对象 | 代码位置 | 职责 |
|---|---|---|
| `MemoryTun` / `OutboundQueue` | sing-tun `v0.0.0-20261110141903-57e9d17fa852` | 设备级字节预算、队列、waiter 记录、队列关闭 |
| `ConnectionManager` | `route/conn.go`、`route/conn_reclaim.go` | 准入账本、被跟踪连接、generation、物理关闭调度 |
| `admissionTicket` | `route/conn_reclaim.go` | 一次已授权但尚未移交的拨号槽位 |
| `disposalState` | `route/conn_reclaim.go` | 物理关闭的所有权账本（queue/pending/condemned/syncing/admitted） |
| `trafficsched.Scheduler` | `route/connection_flow.go` 等 | 每条流的调度许可；连接结束时释放 |
| `resourceguard.GrowthBudget` | `route/conn.go`（候选基线已有） | 每个方向的 copy 增长许可；copy goroutine 结束时释放 |
| xHTTP `sharedUplinkBudget` | `transport/v2rayxhttp/uplink_budget.go`（候选基线已有） | 多流共享的上行字节池；Box 级 provider 可统一绑定 |
| `NetworkManager` | `route/network.go`、`route/network_environment.go` | 网络切换的 generation、DNS 固定、监听器调度 |
| outbound `Reset/ResetContext` | `protocol/{vless,vmess,trojan}`、`transport/v2rayquic`、`transport/v2rayxhttp` | 网络切换时重置传输，而不是最终关闭 |

---

## 2. 连接状态机

```
                    dial 开始
                       │
              AcquireConnection(ctx)          ← 准入（与 manager 生命周期原子）
                       │  失败：ctx 取消 / net.ErrClosed
                 ADMITTED（ticket, admitted++）
                   │            │
        TrackConn/TrackPacketConn        Release（拨号失败/取消）
                   │            │
             TRACKED/ACTIVE   RELEASED（admitted--）
                   │
       ┌── 传输错误 / 对端关闭 / 取消 ──┐
       │                               │
   RETIRED(reason)                （由 copy 循环自然结束）
       │
   ReclaimNetworkTransition / pause / memory / CloseAll / Close
       │
   stageDisposals（持 m.access 与 d.mu）
       ├── 预算内 → STAGED（pending++，进入 queue）
       └── 预算外 → CONDEMNED（仍归 manager 所有，不入队、不可准入）
       │
   disposalWorker（唯一物理关闭者）
       STAGED → CLOSING → CLOSED（pending--）
       CONDEMNED ──(pending 有空位时 promote)──→ STAGED
       │
   CloseAll 同步路径：
       取 tracked 批 + condemned 批 → SYNCING(count++) → 内联 Close → count--
```

管理器状态：

```
RUNNING ──Close()──▶ SHUTDOWN（closed=true, disposal.shutdown=true, 拨号闸门关闭）
                          │
                  CloseAll 关闭 tracked/condemned（SYNCING 记账）
                          │
                  waitForDisposals：pending=0 且 syncing=0 且 admitted=0
                          │
                       CLOSED
```

关键点：

- **ADMITTED 也是账本的一部分**：`Close` 会等 `admitted` 归零，而 ticket 的 `Track*` 在 shutдown 后返回 `net.ErrClosed`，并把 socket 放进 disposal 队列，因此"授权后关闭"不会泄漏 socket。
- **移除与移交是同一个临界区**：`stageDisposals` 在 `m.access` 下调用，`CloseAll` 取同一把锁；因此"从 tracked 集合移除"与"进入 pending/queue"不可分割，`Close` 不会在窗口里看到空集。
- **过渡体不等待物理 close**：网络切换只做 drain（按 reason 判定）、DNS 固定、提交；物理关闭由单个 worker 完成。backlog 满时新对象进入 condemned，而不是让过渡体阻塞。
- **同步关闭单独记账**：`CloseAll` 内联关闭的对象进入 `syncing`，`Close` 同时等 `pending` 与 `syncing`，不会在"有人还在 socket 的 Close 里"时返回。

---

## 3. 网络切换的顺序

```
平台事件（NetworkManager.Reset）
  │
  ├─ 认领本次 transition 的 generation token
  ├─ 过渡体（持 resetRunAccess）：
  │     ├─ 按 reason 回收旧 generation 的连接（admission 半同步；物理关闭交给 disposer）
  │     ├─ 为"本次已认领的 generation"固定 DNS
  │     └─ 提交 transition（settle token）
  ├─ 通知监听器：每个 owner 一个 goroutine，共享一个 deadline
  │     └─ vless/vmess/trojan → ResetContext（把 deadline 传下去）
  │         └─ v2rayquic：候选迁移受 deadline 约束；被取消 = 放弃候选（socket 关闭，连接留在原路径）
  │         └─ v2rayxhttp：Reset → RetireSuspect（关闭 idle，drain 在用，开新 generation）
  └─ 新 generation 的拨号不再被"network changed while dialling"拒绝
```

顺序不变量：

1. **DNS 固定发生在提交之前、旧 generation 的物理关闭之外**。旧 socket 的 `Close` 可以慢于整个过渡体。
2. **监听器是"反应"而不是"前置条件"**：它们并发执行且受统一 deadline 约束；一个慢 outbound 不再把 N 个 outbound 的总和加在过渡体上。
3. **过渡体只认领自己的 generation**：被后续事件取代的候选迁移会让位，不会把新 generation 的 DNS 固定绑到旧事件上。

---

## 4. 资源所有权表

| 资源 | 创建者 | 持有者 | 释放者 | 不变量 |
|---|---|---|---|---|
| OS socket（TCP/UDP） | dialer | ADMITTED 时在拨号方；TRACKED 后归 `ConnectionManager` | `disposalWorker`（异步）或 `CloseAll`（同步，记 `syncing`） | 每个 socket 恰好 close 一次；`pending`/`syncing` 覆盖所有已移交未关闭对象 |
| 准入槽位 | `AcquireConnection`（写 `admitted`） | ticket | `Track*` 成功（转成 tracked）或 `Release`；shutdown 时被撤销 | `Close` 等 `admitted=0`；撤销时 socket 进 disposal 队列 |
| 控制平面准入（DNS） | `WithControlPlaneAdmission` | 预留池（4） | 与普通槽位相同 | 预留池独立、有界；不是 shutdown 绕过 |
| 流调度许可 | `ConnectionManager` 每条流 | copy 循环 | 流结束时 | 关闭连接不足以唤醒被 permit 阻塞的 copy，所以 `Close` 最后关闭 scheduler |
| copy 增长许可 | `GrowthBudget.Admit`（候选基线） | 每个方向 | copy goroutine `Release` | 预算只能收紧、不能放宽阈值 |
| 上行字节池（xHTTP packet-up） | `newSharedUplinkBudget` | `Client` 或 Box provider 绑定后的共享账本 | 每条流归还 | 池的总和不超过配置推导值；绑定失败退回 per-client 池（宁可多绑，不可少绑） |
| 设备字节预算（sing-tun） | `MemoryTun.writePackets` | inbound ring / outbound queue | 读取、出队、detach、Close、迁移 | 每帧预留恰好归还一次；waiter 记录属于唯一队列，成功/取消/关闭/迁移时清除 |
| DNS 固定 | `NetworkManager` transition body | 本次 generation | 下一个 generation 变更 | 固定发生在提交前；不在旧 socket 的 Close 之后 |
| FD | OS | socket | 同 socket close | Linux 有 FD 计数测试；Windows 上真实 FD 测试标记受限 |

---

## 5. 加锁顺序

| 场景 | 顺序 | 说明 |
|---|---|---|
| 回收/移交 | `m.access` → `disposal.mu` | `stageDisposals`、`rejectAdmissionLocked` 都按此顺序 |
| 过渡体 | `resetRunAccess` →（listener 阶段不持该锁） | listener 在提交后并发执行 |
| sing-tun 释放 | `inboundAccess` → 释放后通知队列 | `releaseQueuedBytesLocked` 在 inbound 锁内；`notifyOutboundByteWaiters` 在解锁后 |
| 队列操作 | `queue.access` → `queue.waitAccess` | `drain`/`enqueue` 持 access 调 `minWaitNeed`/`noteWait` |

---

## 6. 由测试钉住的不变量（对应报告 §7 的 28 个 route 测试）

- 准入：`TestAdmissionSlotsAreAllocatedNotJustObserved`、`TestACanceledAdmissionWaiterDoesNotLeakASlot`、`TestAdmissionTicketCannotOutliveTheManager`、`TestAdmissionDoesNotOutliveTheManager`、`TestFinalCloseWaitsForAnOutstandingAdmission`、`TestEveryDialEntryPointRefusesAClosedManager`。
- 关闭/移交：`TestShutdownWaitsForAConnectionInTheDisposalHandoff`、`TestABatchStagedAfterShutdownIsStillClosedByClose`、`TestFinalCloseWaitsForASynchronousCloseAll`、`TestCloseAllClosesTheCondemnedSetSynchronously`。
- 预算/背压：`TestTheBudgetThresholdIsWhereNewConnectionsStop`、`TestTheDisposalBacklogStopsAdmissionAndRecovers`、`TestABatchLargerThanTheDisposalBudgetStillSettlesTheGeneration`、`TestConsecutiveOverBudgetTransitionsKeepSettling`、`TestConsecutiveReclaimsKeepOnePhysicalCloseInFlight`。
- 过渡语义：`TestAProductionNetworkEventSettlesTheNewGenerationBeforeOldCleanup`、`TestAnOrdinaryNetworkChangeDoesNotQueueBehindASlowOldPacketClose`、`TestADeadPathResetDoesNotQueueBehindASlowOldPacketClose`、`TestAConsecutiveChangeIsNotBlockedByTheFirstClose`。
- 控制平面：`TestControlPlaneReservationIsBounded`、`TestColdProductionDNSStillResolvesWhenTheDisposalBacklogIsFull`、`TestTheDNSReservationIsBoundedAndRecovers`。
- 优先级/拨号入口：`TestPriorityTicketsAreReleasedByTrackingAndDoubleReleaseIsIdempotent`、`TestOnlyTheActualSlotIsHandedToRealDials`、`TestTheDialerCannotAdmitAfterShutdownMidHandoff`、`TestEveryDialEntryPointRejectsAShutdownHandoff`、`TestATicketConsumedAfterATransitionBelongsToTheNewGeneration`、`TestAReentrantCloseCallbackDoesNotDeadlock`。
