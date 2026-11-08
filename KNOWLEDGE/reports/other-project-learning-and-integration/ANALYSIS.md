# ANALYSIS —— 现状调用图、锁序与生命周期

本文件补齐 01 的“三类资源及各关键 owner 的调用图、等待/锁图、创建→发布→退役→关闭路径”。**只描述已读源码的事实**；没有重新审计的部分明确标注，不用知识库结论冒充当前源码。基线：`25f5c9a`（v0.1.44 后 6 个文档提交），01 代码提交 `503a18b`。

## 1. 创建许可（Creation permits）

### 调用图

```text
route.ConnectionManager.NewConnection / NewPacketConnection   (route/conn.go)
  ctx = trafficclass.WithClass(ctx, metadata.TrafficClass)     ← 类别在此绑定一次
  m.enterDialGate(...)                                          ← 拨号调速器（transition window）
        │
        ▼
outbound.DialContext（协议各自实现）
  ├─ AnyTLS:  Outbound.dialAdmitted (protocol/anytls/outbound.go)
  │             → dialWithAdmission (protocol/anytls/admission.go)
  │                 ├─ HasHeldPermit(ctx)? → 直接 dial
  │                 ├─ class = Interactive if trafficclass says so
  │                 ├─ Guard.Acquire(ctx, class)
  │                 └─ dial(resourceguard.WithHeldPermit(ctx)) → Release
  │
  └─ TLS 系:  defaultDialer.DialTLSContext (common/tls/client.go)
                → handshakeWithAdmission
                    ├─ HasHeldPermit(ctx)? → 直接 dial
                    ├─ Guard.Acquire(ctx, class)
                    └─ dial(WithHeldPermit(ctx)) → Release
```

`Guard.Acquire`（`common/resourceguard/guard.go`）先 `observe()` 推进 level/滞回、`apply()` 发布 plan，再进
`Gate.Acquire`（`gate.go`）：`ctx.Err()` → 计数 → 有空位立即发许可 → 队列未满则挂到自己的 channel 等待 →
`ctx.Done()`/计时器到时则 `withdraw`。**每个等待者不创建 goroutine**，只持有一个 waiter struct + channel。

### 等待/锁图

```text
Guard.access ──(apply: 同一把锁内)──▶ Gate.access   (setPlan)
                                        │
Gate.access: inflight/waiters/counters  │ grantLocked → waiter.channel
                                        ▼
                                    caller chan (无锁等待)
```

- 锁序固定为 `Guard.access → Gate.access`；`Gate` 从不回调 `Guard`。
- 等待者的等待发生在**锁外**（channel select），撤销/授予在锁内完成；被授予后放弃的槽会通过 `withdraw → grantLocked` 交还，不泄漏。
- 许可在 `dial` 返回后 `Release`（幂等、nil-safe）；**不持有到会话结束**，因此不改变连接数语义。
- 失败路径：`ctx` 已死 → `canceled`；队列满 → `ErrQueueFull`；等待超时 → `ErrWaitTimeout`；三者与 `admitted/released` 分开计数。

### 计费边界

- 一个逻辑拨号最多一份许可：`HeldPermit` 标记沿 `context` 传给真正的内层拨号（detour、链式代理、共享 TLS）。
- 许可只覆盖“创建阶段并发”，不是常驻内存；`Plan` 的 `limit/queue/wait` 随 level 单调收紧（`planFor` 有 floor/clamp）。
- 未覆盖：QUIC/0-RTT、Cronet 引擎与原生新连接、Mieru `newUnderlay`（见 `CONTRACTS.md` §4，02 负责）。

## 2. 回收执行器（Reclaim executor）

### 调用图

```text
adaptiveTimer.poll (service/oomkiller/timer.go)
  ├─ readSample → nextState → publish pressure (atomic) → recorder.sample
  ├─ 危机条件满足 → submitCrisisLocked ─┐
  ├─ 空闲机会 → submitIdleLocked ───────┤
  └─ Darwin 压力回调 → submitPressureLocked ─┐
                                             ▼
                              pendingReclaim（单槽，按紧急度替换）
                                             │ reclaimWake
                                             ▼
                              reclaimLoop（唯一破坏性执行者）
                                 ├─ runIdleReclaim → releaseAccess → L1 cache → L2 pools → L3 GC
                                 ├─ runPressureReclaim → releaseAccess → releaseMemory（cache flush + GC）
                                 └─ runCrisisReclaim → releaseAccess → ReleaseMemory（网络重置）+ releaseMemory
                                             │
                                             ▼
                              reclaimObserver.record（本轮新增，无调度能力）
                                             │ metadata.json（report 时）
                                             ▼
                              Service.reclaimMetadata → Recorder.SetExtraMetadata("memoryReclaim")
```

### 等待/锁图

```text
releaseAccess ──(先)──▶ access          # 代码注释固定：order is releaseAccess -> access
       │                    │
       │                    ├─ 状态机/队列/冷却字段
       │                    └─ crisisQueued()：L1/L2 边界检查是否让出
       ▼
   破坏性动作（cache/pools/GC/reset）——期间不取 access
```

- `stop()` 持 `access` 清 `timer`/`reclaimStopped`，再 `Wait` `pressureCallbacks`；每个 Add 都在 `access` 内且在 active 检查之后，因此不会漏等。
- 执行器在锁外运行破坏性动作；`finishReclaimLocked` 再取 `access` 收尾。`pending` 可被更紧急请求替换，替换复用同一个 WaitGroup token（不出现瞬时零）。
- crisis 在破坏性重置前重新读样本，若已低于 armed 线则跳过重置（报告仍记录）。
- 回收动作期间**不等待预算/网络/worker**：cache/pools 是本地遍历，GC 是运行时调用；没有任何“释放所需锁内等网络”的路径。

### 计费边界

- 三种动作：idle（可失败、有低收益冷却）、pressure（Darwin 通知，不重置网络）、crisis（重置网络 + 释放，report-only 时跳过重置）。
- 冷却只抑制 idle；crisis/pressure 不受它限制。
- 收益是 footprint 差值（信用值），cache 条目数是唯一真实对象计数；没有“真实终结数”（归 04）。

## 3. 平台内存口径（Platform memory accounting）

```text
memory.Total()/memory.AvailableAvailable()/memory.Available()   (sing/common/memory)
        │  readMemorySample(policyMode)
        ▼
memorySample{usage, available, availableKnown}
        ├─ computeLimitThresholds → trigger/armed/resume（policyMode memory_limit / network_extension）
        ├─ availableThresholds（policyMode available）
        ├─ nextPressureState（滞回）→ publish tun.MemoryPressure（原子）
        └─ Recorder.sample → timelineRow
              ├─ memoryBytes = footprint（含 native）
              ├─ availableBytes + availableKnown（未知 ≠ 0）
              ├─ goRuntimeManagedBytes = total - heap/released；goMemoryLimitBytes；GOGCPercent
              └─ goroutines / connections / gcCycles
```

- `ReportStatus.ObservedBudget` 只在“同一瞬间同时有 usage 与 available”时计算；`BudgetObserved` 说明它是否有效，**不是**系统保证。
- GOMEMLIMIT 只约束 Go 托管内存；`memory.Total()` 是进程 footprint（含 native）。二者分列，报告不合并。
- 单一压力源：`Service.pressure` 是原子；TUN 的 `MemoryPressure(ctx)`、guard level、回收状态机读同一个值。01 未新增采样器。

## 4. 生命周期：创建 → 发布 → 退役 → 关闭

| 资源 | 创建 | 发布/可见 | 退役 | 关闭/终结 | 01 观测 |
|---|---|---|---|---|---|
| 创建许可 | `Gate.Acquire` | 计数 `admitted/inflight` | 队列超时/取消/队列满 | `Permit.Release`（幂等） | `resourceGuard` 全量计数 |
| 过期 cache 条目 | 查询写入 | 存在于 cache | `TrimCaches` 删除过期项 | 立即从 cache 移除 | 真实条目数（`cacheEntriesRemoved`） |
| 空闲连接池 | 拨号建立 | 池内复用 | `TrimMemory → ReferenceManager.retireIdleResources` | keeper 的 `CloseIdleConnections` | **只有 footprint**；keeper 数不等于终结数 |
| 网络代次/租约 | `runtimecoord` epoch | `Stale/Acknowledge` | 新 epoch 发布后按需退役 | 租约身份换出后旧租约失效 | 01 未新增；见 `core-lifecycle-runtime.md` |
| Scope 资源 | `scope.Add` | 启动顺序内可见 | — | `Scope.Close` 逆序执行清理 | 01 未新增；见 `core-lifecycle-runtime.md` |
| 编译/构建产物 | Provider 下载/解析/构建 | 规则集/产物发布 | 旧版本删除/替换 | mmap/租约释放 | **无观测**（归 06） |

**关键不变量（已在源码/模块核实）**：从候选列表移除 ≠ 最后引用释放；关闭顺序由注册逆序与 owner 决定；资源可以由非注册者关闭，不能仅凭缺少 `scope.Add` 判定泄漏。01 不改变这些生命周期，只记录许可与回收动作。

## 5. 作用域

| 作用域 | 事实/对象 | owner | 观测 |
|---|---|---|---|
| 进程 | footprint、available、压力 level | `service/oomkiller.Service` | `Recorder` timeline/metadata、`pressure` 原子 |
| Box | 启动/关闭顺序、Scope、网络代次 | `box.go`、`adapter.Scope`、`common/runtimecoord` | 无统一计数（04 扩展） |
| 会话/连接 | TCP/UDP 连接、池成员 | `route.ConnectionManager`、各 outbound | `connections` 计数、回收 walk |
| Flow | 单条代理流 | `route/conn.go` | 08 结构化事件（后续） |
| 构建 | 规则集/Provider 产物 | 06 owner | 无（06 接） |

## 6. 现有开关、默认与可复用接点

| 项 | 位置 | 默认/行为 |
|---|---|---|
| 准入 guard | `NewService`：`policyMode == policyModeNetworkExtension` | 仅 iOS NE 安装；其他平台 nil-safe 全放行 |
| 准入参数 | `DefaultNetworkExtensionConfig` | Normal 32/256/5s + 预留 4；Armed 16/128/2.5s；Critical 8/32/100ms |
| 回收执行器 | `adaptiveTimer` | idle 1s 最小间隔、30s 低收益冷却、512KiB 收益门槛 |
| 报告键 | `SetExtraMetadata` | `resourceGuard`（已有）、`memoryReclaim`（01 新增） |
| 回收开关 | `option.OOMKillerServiceOptions` / `KillerDisabled` | report-only 时 crisis 只报告不重置网络 |
| 时钟/测试接缝 | `Guard.useClock`、`adaptiveTimer.now`、`sampleFunc/readUsage/trim*/freeOSMemory/releaseNetwork` | 生产 nil → 真实实现 |

## 7. 已知未审计区域（明确交代，不冒充已验证）

- `common/runtimecoord` 内部锁序/租约实现：01 只按 `ARCHITECTURE.md` §2 与模块文档使用，**没有**重新逐行审计。
- `box.go` 的 PreStart/Start/Close 具体阶段：按 `core-lifecycle-runtime.md` 使用，01 未重审。
- 各 keeper 的 `CloseIdleConnections` 实际关闭数量与阻塞行为：接口无返回值，01 不做推断（R01/R07 负责）。
- Provider/规则构建的瞬时占用：属 06（F03/F04）。
