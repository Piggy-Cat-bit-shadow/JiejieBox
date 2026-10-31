# 功耗与移动端生命周期（Power and mobile lifecycle）

## v0.1.9 之后的渐进式内存释放（Progressive memory release after v0.1.9）

**VERIFIED_SOURCE，审阅版本 `011f3fdcd99`。** `adapter/network.go` 区分三种内存处理：
可选的 `TrimCaches` 清理读路径本来就会拒绝的条目；`TrimMemory` 复用池，不重置网络，也不
影响活跃流量；`ReleaseMemory` 则执行更激进的重置与释放。实现见 `route/network.go`，不能用
完整网络重置替代普通的渐进式清理。

`box_lifecycle.go` 同样区分 resume 和 `woke()`：后者在释放暂停状态前先发布一次复用边界。
这些是源码观察，不是设备测试结果。

`common/power/governor.go` 会先复制观察者 slice，再在不持 governor 锁的情况下调用回调。
如果通知期间有新观察者注册，它会重新通知，最多循环八次。这样既避免并发修改 slice 的底层
数组，也不需要在调用外部代码时持锁。回归测试见 `governor_observer_race_test.go`；本次
Windows 校准没有启用 `-race`，因此不能算一次竞态检测。

**范围：** `common/power/governor.go`、`common/runtimecoord/platform_events.go`、`box_lifecycle.go`、`experimental/libbox/platform_events.go`、`service/oomkiller`、`internal/memmetrics`，以及 `route/reference.go` 中的复用策略。

**来源：** `common/power` 是一个 **fork 创建的包**（7 个文件、约 2,579 行新增，首个提交 `aac340808`）；`internal/memmetrics` 也是 fork 创建的。Apple 设备轴、Android trim 映射与复用分带策略都是 fork 的工作。

**改以下内容时加载本模块：** 屏幕状态、锁屏状态、休眠/恢复、设备唤醒、内存裁剪、`ReuseSuspect`、暂停/保活策略，或者任何在设备可能空闲时运行的定时器。

---

## 1. 边界：客户端报告事实，核心决定策略（The boundary: the client reports facts, the core decides policy）

客户端只报告平台提供的原始状态；这些状态如何影响内存、暂停和连接复用，由核心代码决定。
下表列出各部分的归属：

| 关注点 | 归属 | 锚点 |
|---|---|---|
| 平台*报告*什么 | 平台，经由 `experimental/libbox/platform_events.go`——一个**薄转发器**：`MemoryTrim(level int32)` `:234`、`SetAppForeground(bool)` `:243`、`SetScreenOn(bool)` `:261`、`ReportDeviceWake()` `:269`、`Close()` `:277` | `:92` 构建 `boxPlatformTarget`；`:194 NewPlatformEvents` 转发进 `runtimecoord.PlatformEvents` |
| 一个 level *意味着*什么 | **核心**——`TrimLevel` 常量与 `TrimLevel.Action()` | `common/runtimecoord/platform_events.go:152-220` |
| 合并窗口 | **核心**——`trimCoalesceWindow = time.Second`（`:223`）、`releaseCoalesceWindow = adapter.RecoveryWindow`（`:231`） | 如所引 |
| 设备状态机 | **核心**——`common/power.Governor` | `governor.go:53 State`, `:93 Allow`, `:109 ReuseAction`, `:141 ReuseBoundary`, `:168 Policy`, `:222 ReuseFreshness.Classify`, `:239 WakeStagger`, `:264 DefaultPolicy`, `:472 DevicePaused`, `:491 DeviceWake`, `:526 SleepStarted`, `:564 Resumed`, `:660 NetworkPaused`, `:672 NetworkWake`, `:708 ObserveTraffic`, `:917 ReuseEpoch`, `:939 AddReuseObserver`, `:950 WaitActive`, `:987 WaitProviderRefresh` |

对平台原始数值的分类在核心中完成。客户端上报操作系统给出的数值，不直接向核心发送
“应该采取哪种内存清理策略”的结论；否则同一策略就需要在多个客户端中重复实现。

---

## 2. level、edge 和 snapshot：需要区分的三类接口（Level, edge and snapshot）

| 词汇 | Level API | Edge API | 快照/协调 API |
|---|---|---|---|
| `common/power.Governor`（状态机） | `DevicePaused` `:472`, `NetworkPaused` `:660` | `DeviceWake` `:491`, `SleepStarted` `:526`, `Resumed` `:564`, `NetworkWake` `:672` | `State` `:53`, `Policy` `:168`, `ReuseBoundary` `:141` |
| `pause.Manager`（sing 的 level+edges） | 它的 level getter | 它的 edge 回调 | — |
| `box_lifecycle.go`（Apple 映射） | `screenState(on)` `:173-179`, `lockState(locked)` `:193-199` | `slept()` `:94-101`（先 edge 再 level）、`resumed()` `:141`（**不发布任何东西**）、`woke()` `:156-163`（先边界再 level） | `reconcileScreenState` / `reconcileLockState` `:228-252`——**仅 level** |
| `common/runtimecoord/platform_events.go`（Android/前台） | 下面的 trim 阶梯 | 屏幕/前台 edge | 事件快照 |

### 为什么 `reconcileScreenState` / `reconcileLockState` 必须只处理 level（Why `reconcileScreenState` / `reconcileLockState` must be level-only）

读取当前状态不等于发生了一次状态变化。如果每次读取都发布 edge，轮询就会被误认为新的
睡眠或唤醒事件。这里要保持的不变量是：

> **Apple 的快照协调只处理 level。它不得伪造 sleep/wake edge，也不得仅凭一次屏幕读取就释放一次暂停。**

这两条约束分别来自提交 `645d96b4` 和 `60681f27`：读取当前屏幕状态只协调 level，不开启新的休眠时段；resume 本身也不会结束休眠时段，因此不能消耗该时段的复用边界。

### 为什么显示屏亮起**不算**解锁（Why a display turning **on** is not an unlock）

在 `box_lifecycle.go` 中，display-on 进入 `resumed()`，目前不会发布事件；解锁则进入 `woke()`，并释放暂停状态。两者不能混为一谈，否则会重新引入此次拆分所修复的缺陷。

### `resumed()` 不发布事件（`resumed()` deliberately publishes nothing）

```go
func (l lifecycle) resumed() {}
```

实现位于 `box_lifecycle.go:141`，实测经过记录在 `:109-137`。在一次 **28 m 24 s** 的休眠中，13 次扩展 resume 产生了 **13 个锚点和一个 28.29 s 的边界**，导致测量结果失真。正确的顺序是：由 sleep edge 启动测量；设备通过 `woke()` 恢复可用时，在释放 level 之前发布复用边界。单独收到扩展 resume 时，两者都不应发生。

**锚点和每个休眠时段的边界闩锁（latch）共用同一个字段。** 后续曾尝试用 `IsDevicePaused()` 控制 sleep edge，但在约十分钟的锁屏测试中没有产生任何边界，结果比原缺陷更差。这项修改随后被回滚（`e20875359` → `e661258a6`；两者均标为 `ARCHIVE-ONLY`）。详情见 `docs/fork/p1c-iphone-device-closure.md:289-291`。

### Apple 睡眠边界的历史设备证据（Historical device evidence for the Apple sleep boundary）

设备：**“Jiejie”——iPhone 17 Pro Max（`iPhone18,2`），iOS 26.4.2（23E261），实体机，有线连接。**

| 构建 | 时段 | 回调 | 已发布的边界 | 测量 |
|---|---|---|---|---|
| 修复前 | 一次 28 m 24 s 锁屏 | 26 次 `ne-sleep`/`ne-wake`（13 个循环），0 次屏幕事件 | **12** | **28.29 s**（错误的时长） |
| 修复后 `60681f277` | 一次 13 m 13 s 锁屏 | 20 次回调（10 个循环） | **1** | `reuse: epoch 1, sleep 13m13.492448s`——**精确到秒** |
| **被拒绝** `e20875359` | 锁屏约 10 分钟 | — | **0** | 严格劣于它要修复的缺陷；已回滚 |

iPad 与 macOS 为 **`NOT_RUN` / `SIGNING_OUT_OF_SCOPE`**。这只是一台设备、一个 OS 构建、一个产品、一个记录在案的修订版。它针对的是这个配置与所测的休眠时段，不是对所有当前构建、其他设备或其他生命周期路径的证明。

---

## 3. Android 的 trim 阶梯（The Android trim ladder）

`common/runtimecoord/platform_events.go:152-220`。

| 常量 | 值 | `Action()` |
|---|---|---|
| `TrimRunningModerate` | 5 | `TrimIgnore` |
| `TrimRunningLow` | 10 | `TrimProgressive` |
| `TrimRunningCritical` | 15 | **`TrimRelease`** |
| `TrimUIHidden` | 20 | `TrimIgnore` |
| `TrimBackground` | 40 | `TrimProgressive` |
| `TrimModerate` | 60 | `TrimProgressive` |
| `TrimComplete` | 80 | `TrimProgressive` |
| 其他任何值 | — | **`TrimIgnore`** |

`TrimIgnore` 这个默认值自带理由：*"a trim issued for a level whose meaning we do not know is a cost paid for a guess."*（中文：为一个我们不知道含义的 level 发起裁剪，等于为一次猜测付出代价。）**只有 `TrimRunningCritical` 才会走到重置这一步。**

**`TrimUIHidden` 明确不是内存信号**（`:171-178`）。UI 被隐藏说明不了任何内存压力，把它当成压力，会让用户每切换一次应用就裁剪一次。

---

## 4. 内存策略：三种动作与禁止的行为（Memory policy — three distinct actions, and the one that is forbidden）

| 动作 | 它可否重置网络？ | 它的用途 |
|---|---|---|
| **Trim**（`TrimIdleResources`, `TrimProgressive`） | **否** | 只做空闲回收 |
| **Release**（`ReleaseMemory`, `TrimRelease`） | **是** | 真正的内存紧急状态 |
| **复用边界**（`RetireSuspect`） | 不重置，但是一个更强的复用决策 | 一个休眠/复用边界，**不是裁剪的替代品** |

这条不变量是硬性的：

> **内存裁剪只处理空闲：它不得重置网络、拨号、唤醒被挂起的工作，或终止活跃的流。** `ReuseSuspect` 是一个更强的休眠/复用边界动作，而不是裁剪的替代品。

`route/reference.go:154-197` 的 `onReuseBoundary` 遵守它：它**不**重置网络、不终止流、不触碰按需隧道、不拨号。

上述约束适用于 **Trim 裁剪路径**，不能推广到所有内存回收。`service/oomkiller/timer.go` 在危机回收时会调用 `ReleaseMemory`，后者按设计可以重置网络；`route/conn_reclaim.go:18` 记录了这一意图。

是否实际启用 OOM killer 需要检查主机设置与服务选项：守护进程有条件地添加该服务，内部 `KillerDisabled` 标志还会选择仅报告的危机行为。不能仅凭选项的 `json:"-"` 标签判断服务状态。维护时应保持 Trim 和 Release 的区别，避免让普通裁剪重置网络，或无意改变 Release 的重置行为。

### v0.1.9 之后的 OOM 回收协调（OOM reclaim coordination after v0.1.9）

在 `cb71cf80d72`，`service/oomkiller/timer.go` 通过回收执行器串行处理空闲回收、平台压力和危机请求：

- 危机请求优先于队列中的压力和空闲工作，但不会中断正在执行的动作；若危机已排队，空闲遍历会在层边界让出。
- 空闲回收依次尝试过期缓存和空闲池；只有收益不足且运行时条件或处于 Armed 状态的压力条件允许时，才强制 GC。收益较低的空闲工作会进入冷却，危机请求不受该冷却限制。
- Darwin 压力通知会刷新缓存并强制 GC，不会重置网络。危机回收在破坏性重置前会重新核对采样值与已启用阈值；仅报告模式跳过网络重置，但仍执行报告与释放路径。

`stop` 会拒绝新工作、丢弃排队请求，等待已接受的轮询和正在执行的动作结束，再等待执行器退出。Reload 不继承待处理的压力请求；继承的 Triggered 状态会变为 Armed，由下一次采样重新判断危机是否存在。Triggered 在 Armed 对应的阈值处退出，不使用更低的空闲恢复阈值。

本次校准中，`go test ./service/oomkiller -count=1` 在 Windows、Go 1.26.10 下通过；该结果不证明 Darwin 回调或设备行为，也不代表运行了竞态检测。

### 四个池（The four pools）

`runtimecoord` 的边界遍历必须到达**全部四个**：出站、端点、DNS 传输，以及 HTTP 客户端管理器。第四个曾经连续四轮评审都不可达，因为遍历断言的是捆绑的 `IdleConnectionKeeper`，而不是 `IdleConnectionReleaser`——见 `core-lifecycle-runtime.md` §4 与 `buffer-ownership.md` B22。

---

## 5. 唤醒后的复用分带（Post-wake reuse bands）

`common/power/governor.go` 根据休眠时长给出三种复用判定（`governor.go:112-125`）。`DefaultPolicy` 的两个阈值分别为 `SuspectAfter: 5 * time.Second` 和 `RetireAfter: 15 * time.Second`（`governor.go:289-290`）：

| 休眠时长 | 判定 | 行为 |
|---|---|---|
| `< 5 s` | `ReuseKeep` | 保留连接池；**epoch 不推进，也不通知观察者**。 |
| `>= 5 s` | `ReuseSuspect` | 推进 epoch 并发布判定，但按策略保留连接池，避免短暂休眠后不必要的连接重建。 |
| `>= 15 s` | `ReuseRetire` | 推进 epoch 并退役**空闲**可复用资源，后续有需求时再建立新连接。**活跃流不受影响，退役本身也不拨号。** |

**未知的休眠时长会取严格判定**（`TestTheBridgeUsesThePolicyBandsAndNothingElse`）。

`adapter.ReuseSuspect` 用于处理状态可疑但仍有活跃流的资源：设备休眠后，连接即使仍显示打开，也未必仍然可用。它的契约是停止向该资源分配新工作、保留现有工作，并在最后一条流结束后清理资源。

keeper 只能关闭没有活跃用户流量的资源，不能拨号。`naive` 有意不实现 keeper：`sing-mux` 没有安全的“禁止新流”状态，Cronet 和 Naive 只提供破坏性关闭。因此退役遍历使用 `IdleConnectionReleaser`，不要求完整的 keeper 接口。

由 `common/power.TestReuseKeepsEveryResourceForAShortSleep`、`common/power.TestReuseBandsAreThePolicy`、`box.TestTheBridgeUsesThePolicyBandsAndNothingElse`、`route.TestTheSuspectBandDrainsNothing` 固定。

**后台工作不会唤醒空闲资源**——唤醒它的是需求（`docs/fork/runtime-lifecycle-phase1.5.md:30,64`）。一个会唤醒被挂起资源的后台定时器就是缺陷，哪怕这个定时器干的活有用。

---

## 6. 定时器普查——哪些定时器可以推迟，哪些不能（The timer census — which timers may be deferred and which must not）

`docs/fork/power-timer-audit.md`。哪个方向出错都是缺陷：推迟一个看门狗会掩盖泄漏；不推迟一次轮询会烧掉无线时间。

| 定时器 | 节奏 | 判定 |
|---|---|---|
| `service/oomkiller` 轮询 | 自适应：默认最小 100 ms，最大 10 s | 这是必要的看门狗，并非无效唤醒。`intervalForState` 在稳定状态下逐步拉长，在压力下回到最小值；选项与平台策略会影响有效边界。 |
| `internal/memmetrics` 采样 | 1 ms | **仅基准** |
| `service/ssmapi/server.go` | 1 min | 可推迟——**但第一次遍历刻意不推迟** |
| `route/rule/rule_set_updater.go` | — | 可推迟——**第一次遍历也不推迟** |
| `service/api/dashboard.go` | 24 h（默认） | 可推迟——**第一次遍历也不推迟**，而且它是等待而不是跳过：`WaitProviderRefresh` 让循环停驻，于是下载落在唤醒时，而不是再等一整个间隔 |
| `dns_cache` | 1 h | 必须**不**被门控 |
| `route` macOS 挂起看门狗 | 5 s | **一个看门狗，不是投机。** 它比较系统时钟（wall clock）与单调时钟（后者在休眠期间停走），一旦二者分歧就调用 `SystemResumed`。它存在，是因为无头 Mac 拿不到 NetworkExtension 的 sleep/wake 回调；NetworkExtension 路径改用 `CommandServer.Pause`/`Wake`。它必须**不**被休眠状态门控。 |

表中三个可推迟的周期任务都会照常执行第一次遍历，避免初始状态因省电策略而延迟建立。

另有两个后台工作路径需要单独说明，其触发方式和处理策略与上表不同：

- `dns.client.backgroundRefreshDNS`——由一次缓存 HIT 调度，不由时钟。当调速器处于 `QUIESCENT`/`DEEP_IDLE` 或已关闭时直接拒绝，在 `WAKING` 带里经 `Allow().NetworkProbe` 放行。跳过它只损失新鲜度。
- `transport/wireguard/recovery.go`——会轮询，但只在会话陈旧时；而且它**不**被门控：`runtimecoord` 已经把 rebind 限制到每个窗口一次，休眠期间取消它，只会用一次白费的 rebind 换来一条没恢复的隧道。已接受，不推迟。

`HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` 记录了这两个门控背后的测试与变异。

---

## 7. `ReuseSuspect`（复用可疑判定）

| 属性 | 值 |
|---|---|
| 顺序 | `RetireSuspect()` **先**，然后 `CloseIdleConnections`（`route/reference.go:496-511`, `:547-556`） |
| 池清单 | `route/reusable_owner_inventory_test.go` 检查具名所有者与能力；但它不禁止其他实现 |
| 触发 | 一个**休眠/复用边界**，经由 `powerGovernor.AddReuseObserver(m.onReuseBoundary)`（`:142-144`） |
| 不得 | 重置网络、终止流、触碰按需隧道，或拨号 |

---

## 8. 证据（Evidence）

| 保证 | 等级 | 复检 |
|---|---|---|
| trim 阶梯的取值与动作；未知 ⇒ 忽略；`TrimUIHidden` 不是内存压力 | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./common/runtimecoord/...` |
| 调速器状态机与分带 | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./common/power/...` |
| 复用判定在 level 被释放**之前**发布 | `VERIFIED_UNIT`，且测试证明该分带确实被走到过 | `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestTheReuseVerdict .` |
| sleep edge 在 level 被暂停**之前**记录 | `VERIFIED_UNIT`，带一个空泛性防护 | `-run TestTheSleepEdge .` |
| 100 次 sleep/wake 循环、100 次网络转换、Close 之后无复活 | `VERIFIED_UNIT`（无 `-race`） | `-run 'TestOneHundred\|TestNoResurrectionAfterClose' .` |
| Apple 设备轴 | 对一次 iPhone 17 Pro Max / iOS 26.4.2 运行是 **`VERIFIED_DEVICE`**；iPad、macOS 与任何其他 OS 是 **`UNVERIFIED`** | 上面的设备记录 |
| macOS 休眠/唤醒修复 | 对决策为 `VERIFIED_SOURCE` + `VERIFIED_UNIT`（系统时钟（wall clock）对单调时钟的检测器、**以时段为键**的 `SystemResumed`、**感知时长**的重置：30 s 以下为 medium，达到或超过为 dead-path）；**在实体 Mac 上为 `UNVERIFIED`** | `go test ./common/suspendwatch -count=1` 与 `go test ./route -run TestSystemResumed -count=1`（9 个测试，包括第一次之后紧接着第二次休眠、短休眠的 medium 分支与长休眠的 `ReclaimDeadPath` 分支）；设备侧那一行是 `system resumed after …` 日志之后跟一次重置 |
| 真实 TUN、真实 NetworkExtension、网络转换 | **本次 Windows 刷新中为 `UNVERIFIED`**；设备与构建相关的证据必须单独检查 | 需要适当的平台与设备 |
| 无竞态 | **`BLOCKED_BY_ENVIRONMENT`** | 需要 cgo |

顺序测试在暂停回调与复用观察者之间共享 `orderingSequence`，断言 `[]string{"level-paused","reuse-verdict","level-released"}`；同时通过 `require.NotEqual(… ReuseKeep …)` 和 `require.Equal(uint64(1), governor.ReuseEpoch())` 确认相关分支确实执行。sleep 测试则用 `probingAxis` 在 level 调用内部观察顺序，再用 `require.True(t, axis.probed, …)` 确认探针被调用。维护类似测试时，应同时验证顺序和被测路径确实执行。

---

## 9. 来源（Provenance）

| 提交 | 类型 | 它做了什么 |
|---|---|---|
| `aac340808` | LIFECYCLE | 休眠权威 |
| `c62342717`, `540d7cb94`, `4a95e0aeb`, `e966daea2`, `a804916ef`, `d93b68560` | FIX | `DEEP_IDLE` 处的池释放；不可达的 `DEEP_IDLE` 分支；一个陈旧截止时间 |
| `6ebb5799a` | FIX | 陈旧截止时间 |
| `89b4b2c25` | FIX | `DEEP_IDLE` 曾不可达 |
| `0ff90482` | FIX | 让设备 resume edge 使用 **edge 闩锁**，不影响屏幕 level |
| `e4327dca` | LIFECYCLE | 接通 Apple 设备状态的事件发布，并在解锁时解除暂停 |
| `4c7d96c0` | FIX | 将 Apple 设备轴纳入**发布分支**，而非仅保留在单独分支上 |
| `645d96b4` | FIX | 一次当前状态的屏幕读取只协调 level，不启动一次休眠 |
| `60681f27` | FIX | 一次 resume 不会结束一个休眠时段，因此不得消耗掉自己的边界 |
| `e20875359` → `e661258a6` | FEATURE → **REVERT** | `IsDevicePaused()` 的 sleep-edge 门控，基于设备证据回滚。两份存档都保留。 |
| `1331e8567`, `1f85dbc94` | FIX | 基于 generation 的 rebind 预算；`TrimMemory` **不**推进网络 generation，而 `ReleaseMemory` 会 |
| `5f56f02c`, `09964183`, `93823c3d` | MEMORY | 裁剪路径；四个池全部可达 |
| `3c6dd135b`, `f8b0af5f6`, `48dd4469e`, `10f9e17d2`, `eac9cc83d`, `b5c0009d1`, `95159084c`, `666470cbe`, `b5c39a11e`, `c2d93396e`, `da14572a7`, `1965bbf35` | TEST | 泄漏普查与测试完整性相关提交——**其中多项修复了无法失败的测试** |
| `12aa6db064f`, `dbf8de8b2a5` | FEATURE | 权威的前两个**消费者**：乐观 DNS 刷新（按状态拒绝）与 dashboard 下载（停驻在 `WaitProviderRefresh` 上）。测试与五个变异在 `reports/core/HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` |
| `ff897d35772` | FIX | 在构造期而不是在 `Start` 里解析 DNS 调速器：让后台 goroutine 读到的字段变成不可变，而不是给它排定顺序 |

历史普查行里的 `3c6dd135b` 在当前父仓库中解析不出来；在把那个前缀当作已验证的提交锚点之前，请先查原始报告或其他仓库。

全部为 **OWN**。上游提交系列 `Use screen state to end device pause on iOS`（本区间内出现 ×11）属于 `UPSTREAM-INHERITED`/孤儿——见 `BASELINE.md` §provenance。

---

## 10. AI 告诫（AI cautions）

1. **不要把 level 与 edge 合并。** 读一次当前状态不是一次转换；`resumed()` 刻意不发布任何东西。
2. **不要把 display-on 当作解锁。**
3. **不要靠门控一个暂停状态来“修复”休眠边界。** 试过了，在真实设备上测得零个边界，随后被回滚——锚点与闩锁是同一个字段。
4. **不要让裁剪路径重置网络、拨号、唤醒被挂起的工作或杀掉活跃的流。**
5. **不要让 `ReleaseMemory` 停止重置网络。**
6. **不要把 `TrimUIHidden` 当作内存压力。**
7. **不要把未知的 trim level 默认成渐进动作。** 未知的必须忽略。
8. **不要在客户端里分类 level。** 映射归核心。
9. **不要没查过普查就添加周期定时器**；如果它是看门狗，就绝不能被休眠状态门控。
10. **如果一个定时器可推迟，就保持第一次遍历不推迟。**
11. **不要在只实现了 `IdleConnectionReleaser` 的地方断言 `IdleConnectionKeeper`。**
12. **新的 `ReuseSuspect` 所有者，要对照退役遍历与生命周期契约审一遍。**
13. **不要让后台工作唤醒空闲资源。** 唤醒它的是需求。
14. **顺序测试还要确认被测路径确实执行。** 否则相关回调没有运行时，顺序断言也可能通过。
15. **不要把那一次 iPhone 运行泛化到 iPad、macOS 或其他 OS 构建。** 记录里只有一台设备。
16. **不要为上述任何内容引用 `verify-evidence/`**——见 `ci-release.md` §3。
17. **不要把 suspendwatch 的基线推迟到第一个 tick。** `Watcher.Start` 会在创建 ticker 之前同步设置检测器初值。如果第一次读数等到第一个 tick，主机在启动后立即休眠时，基线可能落在唤醒之后，导致漏报这次休眠。回归测试为 `TestTheWatcherPrimesAtStartBeforeTheFirstTick`。
