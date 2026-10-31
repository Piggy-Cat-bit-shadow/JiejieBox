# 功耗与移动端生命周期（Power and mobile lifecycle）

## v0.1.9 之后的渐进式内存释放（Progressive memory release after v0.1.9）

**VERIFIED_SOURCE**，测试目标 `011f3fdcd99`：`adapter/network.go` 区分可选的 `TrimCaches`（只清理读路径会拒绝的条目）、`TrimMemory`（复用池，但不重置网络、不触碰活跃流量）以及激进的 `ReleaseMemory`（重置并释放）。它们的实现在 `route/network.go`。不要用一次完整的网络重置替代一次普通的渐进裁剪。`box_lifecycle.go` 仍然把 resume 与 `woke()` 区分开：后者在释放被暂停的 level 之前发布一次复用边界。这些源码观察不是设备测试。

`common/power/governor.go` 在无锁情况下调用回调之前先拷贝观察者 slice，然后当观察者在一次遍历期间注册时重复通知遍历；该循环被限制为八次。这既防止了 slice 底层数组的竞态，也在一次性复用边界处处理了注册，同时不在外部回调周围持有调速器锁。`governor_observer_race_test.go` 是回归锚点；本次 Windows 刷新在没有 `-race` 的情况下运行了该包，并不是一次竞态检测器运行。

**范围：** `common/power/governor.go`、`common/runtimecoord/platform_events.go`、`box_lifecycle.go`、`experimental/libbox/platform_events.go`、`service/oomkiller`、`internal/memmetrics`，以及 `route/reference.go` 中的复用策略。

**来源：** `common/power` 是一个 **fork 创建的包**（7 个文件、约 2,579 行新增，首个提交 `aac340808`）；`internal/memmetrics` 也是 fork 创建的。Apple 设备轴、Android trim 映射以及复用分带策略都是 fork 的工作。

**在改动以下内容时加载本模块：** 屏幕状态、锁屏状态、休眠/恢复、设备唤醒、内存裁剪、`ReuseSuspect`、暂停/保活策略，或任何在设备可能空闲时运行的定时器。

---

## 1. 边界：客户端报告事实，核心决定策略（The boundary: the client reports facts, the core decides policy）

这是真实存在的，而且它由分类逻辑所在的位置来强制，而不是靠约定。

| 关注点 | 归属 | 锚点 |
|---|---|---|
| 平台*报告*什么 | 平台，经由 `experimental/libbox/platform_events.go`——一个**薄转发器**：`MemoryTrim(level int32)` `:234`、`SetAppForeground(bool)` `:243`、`SetScreenOn(bool)` `:261`、`ReportDeviceWake()` `:269`、`Close()` `:277` | `:92` 构建 `boxPlatformTarget`；`:194 NewPlatformEvents` 路由进 `runtimecoord.PlatformEvents` |
| 一个 level *意味着*什么 | **核心**——`TrimLevel` 常量与 `TrimLevel.Action()` | `common/runtimecoord/platform_events.go:152-220` |
| 合并窗口 | **核心**——`trimCoalesceWindow = time.Second`（`:223`）、`releaseCoalesceWindow = adapter.RecoveryWindow`（`:231`） | 如所引 |
| 设备状态机 | **核心**——`common/power.Governor` | `governor.go:53 State`, `:93 Allow`, `:109 ReuseAction`, `:141 ReuseBoundary`, `:168 Policy`, `:222 ReuseFreshness.Classify`, `:239 WakeStagger`, `:264 DefaultPolicy`, `:472 DevicePaused`, `:491 DeviceWake`, `:526 SleepStarted`, `:564 Resumed`, `:660 NetworkPaused`, `:672 NetworkWake`, `:708 ObserveTraffic`, `:917 ReuseEpoch`, `:939 AddReuseObserver`, `:950 WaitActive`, `:987 WaitProviderRefresh` |

**对原始平台数值的分类住在核心里，而不是客户端里。** 如果客户端发送“你应该裁剪”，策略就搬进了三个客户端代码库；而实际上客户端发送操作系统给它的数值，由核心来决定。

---

## 2. level、edge、snapshot——四种不得合并的词汇（Level, edge, snapshot — four vocabularies that must not be merged）

| 词汇 | Level API | Edge API | 快照/协调 API |
|---|---|---|---|
| `common/power.Governor`（状态机） | `DevicePaused` `:472`, `NetworkPaused` `:660` | `DeviceWake` `:491`, `SleepStarted` `:526`, `Resumed` `:564`, `NetworkWake` `:672` | `State` `:53`, `Policy` `:168`, `ReuseBoundary` `:141` |
| `pause.Manager`（sing 的 level+edges） | 它的 level getter | 它的 edge 回调 | — |
| `box_lifecycle.go`（Apple 映射） | `screenState(on)` `:173-179`, `lockState(locked)` `:193-199` | `slept()` `:94-101`（先 edge 再 level）、`resumed()` `:141`（**不发布任何东西**）、`woke()` `:156-163`（先边界再 level） | `reconcileScreenState` / `reconcileLockState` `:228-252`——**仅 level** |
| `common/runtimecoord/platform_events.go`（Android/前台） | 下面的 trim 阶梯 | 屏幕/前台 edge | 事件快照 |

### 为什么 `reconcileScreenState` / `reconcileLockState` 必须只处理 level（Why `reconcileScreenState` / `reconcileLockState` must be level-only）

**当前状态读取不是 edge。** 如果一次重读发布了一个 edge，那么每次轮询都会看起来像一次新的转换。被固定的陈述就是这条不变量：

> **Apple 的快照协调只处理 level。它不得伪造 sleep/wake edge，也不得仅凭一次屏幕读取就释放一次暂停。**

`645d96b4` 是那个提交：*"a current-state screen read reconciles the level, it does not start a sleep."*（中文：一次当前状态的屏幕读取只协调 level，它不启动一次休眠。）`60681f27` 是它的一对：*"a resume does not end a sleep episode, so it must not spend its boundary."*（中文：一次 resume 不会结束一个休眠时段，因此它不得消耗掉自己的边界。）

### 为什么显示屏亮起**不算**解锁（Why a display turning **on** is not an unlock）

`box_lifecycle.go` 把 display-on 路由到 `resumed()`，而它目前不发布任何东西；解锁则调用 `woke()` 并释放 level。把 display-on 当作解锁，会重新造出这次拆分所要修掉的那个 bug。

### `resumed()` 刻意不发布任何东西（`resumed()` deliberately publishes nothing）

```go
func (l lifecycle) resumed() {}
```

`box_lifecycle.go:141`。测量到的完整经过在 `:109-137`：一次 **28 m 24 s** 的休眠、伴随 13 次扩展 resume，产出了 **13 个锚点和一个 28.29 s 的边界**——测量被 resume 本身毁掉了。sleep edge 启动测量；复用边界在设备经 `woke()` 变为可用时发布，且在 level 被释放之前。单纯一次扩展 resume 两件事都不做。

**锚点与每时段边界闩锁（latch）是同一个字段。** 这就是那个结构性原因：后来一次尝试用 `IsDevicePaused()` 来门控 sleep edge，在十分钟锁屏期间产出**零**个边界（严格劣于它所修的那个 bug），并被回滚（`e20875359` → `e661258a6`，两者都作为 `ARCHIVE-ONLY` 保留）。`docs/fork/p1c-iphone-device-closure.md:289-291` 陈述了这一点。

### Apple 睡眠边界的历史设备证据（Historical device evidence for the Apple sleep boundary）

设备：**“Jiejie”——iPhone 17 Pro Max（`iPhone18,2`），iOS 26.4.2（23E261），实体机，有线连接。**

| 构建 | 时段 | 回调 | 已发布的边界 | 测量 |
|---|---|---|---|---|
| 修复前 | 一次 28 m 24 s 锁屏 | 26 次 `ne-sleep`/`ne-wake`（13 个循环），0 次屏幕事件 | **12** | **28.29 s**（错误的时长） |
| 修复后 `60681f277` | 一次 13 m 13 s 锁屏 | 20 次回调（10 个循环） | **1** | `reuse: epoch 1, sleep 13m13.492448s`——**精确到秒** |
| **被拒绝** `e20875359` | 锁屏约 10 分钟 | — | **0** | 严格劣于那个 bug；已回滚 |

iPad 与 macOS 为 **`NOT_RUN` / `SIGNING_OUT_OF_SCOPE`**。这是一台设备、一个 OS 构建、一个产品以及所记录的修订版。它是针对该配置与所测休眠时段的设备证据，而不是对所有当前构建、其他设备或其他生命周期路径的证明。

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

`TrimIgnore` 这一默认值自带理由：*"a trim issued for a level whose meaning we do not know is a cost paid for a guess."*（中文：为一个我们不知道其含义的 level 发起的裁剪，是为一次猜测付出的成本。）**只有 `TrimRunningCritical` 才配得到重置形态的那一趟。**

**`TrimUIHidden` 明确不是内存信号**（`:171-178`）。UI 被隐藏对内存压力什么也说明不了，而把它当作压力，会让用户每次切换应用都裁剪一次。

---

## 4. 内存策略——三种不同动作，以及那个被禁止的动作（Memory policy — three distinct actions, and the one that is forbidden）

| 动作 | 它可否重置网络？ | 它的用途 |
|---|---|---|
| **Trim**（`TrimIdleResources`, `TrimProgressive`） | **否** | 仅空闲回收 |
| **Release**（`ReleaseMemory`, `TrimRelease`） | **是** | 真实的内存紧急状态 |
| **复用边界**（`RetireSuspect`） | 不重置，但是一个更强的复用决策 | 一个休眠/复用边界，**不是裁剪的替代品** |

这条不变量，而且它是硬性的：

> **内存裁剪只处理空闲：它不得重置网络、拨号、唤醒被挂起的工作，或终止活跃的流。** `ReuseSuspect` 是一个更强的休眠/复用边界动作，而不是裁剪的替代品。

`route/reference.go:154-197` 的 `onReuseBoundary` 遵守它：它**不**重置网络、不终止流、不触碰按需隧道、不拨号。

**以及那个诚实的纠正：** 这条不变量关乎**裁剪**路径，而不是每一条路径。`service/oomkiller/timer.go` 的危机回收调用 `ReleaseMemory`，而它**确实**会重置网络——而且它*本应*如此。`route/conn_reclaim.go:18` 在代码中记录了意图：`ReleaseMemory (OOM killer) -> ResetNetwork + idle … aggressive, and deliberate`。killer 本身并不能仅凭该选项的 `json:"-"` 标签就确立：守护进程是有条件地添加该服务的，而其内部的 `KillerDisabled` 标志会选择仅报告的危机行为。在假定它已启用或已禁用之前，先检查实际的主机设置与服务选项。所以：

**Trim ≠ Release。让裁剪路径重置网络的未来改动就是那个 bug；让 Release 路径停止重置网络的未来改动同样是一个 bug。**

### v0.1.9 之后的 OOM 回收协调（OOM reclaim coordination after v0.1.9）

在 `cb71cf80d72` 处，`service/oomkiller/timer.go` 通过一个回收执行器串行化空闲、平台压力与危机请求。危机优先于待处理的压力与空闲工作，但不会中断一个已经在进行的动作；如果排队了危机，一次空闲遍历会在其层边界处让出。空闲回收先尝试过期缓存，然后尝试空闲池，只有当先前收益不足且运行时或已武装压力条件允许时，才强制 GC。低收益的空闲工作会冷却；危机不会被这个空闲冷却阻塞。Darwin 的压力通知会刷新缓存并强制 GC，但不重置网络。危机在破坏性重置之前，会把当前采样与已武装阈值重新核对；仅报告模式跳过网络重置，但仍执行其报告/释放路径。

`stop` 拒绝新工作、丢弃仍在排队的请求、等待已接受的轮询与已在运行的动作，并 join 执行器。Reload 不携带待处理的压力意图；被携带的 Triggered 状态会变成 Armed，以便下一次采样决定新的危机是否仍然存在。Triggered 在已武装边界处退出，而不是在更低的空闲恢复阈值处退出。本次校准中 `go test ./service/oomkiller -count=1` 在 Windows 上以 Go 1.26.10 通过；那次运行没有确立 Darwin 平台相关的回调/设备行为，也没有确立竞态检测器执行。

### 四个池（The four pools）

`runtimecoord` 的边界遍历必须到达**全部四个**：出站、端点、DNS 传输，以及 HTTP 客户端管理器。第四个曾在四轮评审中都不可达，因为该遍历断言的是捆绑的 `IdleConnectionKeeper` 而不是 `IdleConnectionReleaser`——见 `core-lifecycle-runtime.md` §4 与 `buffer-ownership.md` B22。

---

## 5. 唤醒后的复用分带（Post-wake reuse bands）

该策略属于 `common/power/governor.go`，判定结果恰好是三个值之一——`ReuseKeep`、`ReuseSuspect`、`ReuseRetire`（`governor.go:112-125`）——由 `DefaultPolicy` 的 `SuspectAfter: 5 * time.Second` 与 `RetireAfter: 15 * time.Second` 推导而来（`governor.go:289-290`）：

| 休眠时长 | 判定 | 行为 |
|---|---|---|
| `< 5 s` | `ReuseKeep` | 什么也不做。**epoch 不前进，也没有观察者运行**——一个人马上就要再次使用的连接仍然留在池中。 |
| `>= 5 s` | `ReuseSuspect` | epoch 前进，判定被**发布**，但按策略**保留**池。中间那一带是一个*被发布的状态，而不是一个动作*：把一次短休眠通常都能挺过去的池翻搅一遍，只会白付一次握手而换不来任何正确性收益。 |
| `>= 15 s` | `ReuseRetire` | epoch 前进，**空闲**的可复用资源被退役，因此下一次需求会拨向一条已知是新的路径。**活跃流不受影响，也没有任何东西拨号。** |

**未知的休眠时长会取严格判定**（`TestTheBridgeUsesThePolicyBandsAndNothingElse`）。

`adapter.ReuseSuspect` 之所以存在，是因为**一条打开的流并不是存活的证明**——它可以整段休眠期间都保持打开而静默。它的契约是“不在你现在持有的东西上开新工作，保留已经在其上的工作，当最后一条流离开时把它拆掉”。

一个 keeper *"may only close a resource with no active user traffic, and can never dial"*（中文：只能关闭没有活跃用户流量的资源，且永远不能拨号）。`naive` 刻意**不是** keeper；`sing-mux` 没有安全的“无新流”状态；Cronet 与 Naive 只提供破坏性关闭。这就是为什么断言的是 `IdleConnectionReleaser`，而不是一个完整的 keeper。

由 `common/power.TestReuseKeepsEveryResourceForAShortSleep`、`common/power.TestReuseBandsAreThePolicy`、`box.TestTheBridgeUsesThePolicyBandsAndNothingElse`、`route.TestTheSuspectBandDrainsNothing` 固定。

**后台工作不会唤醒空闲资源**——唤醒它的是需求（`docs/fork/runtime-lifecycle-phase1.5.md:30,64`）。一个唤醒被挂起资源的后台定时器是缺陷，即使该定时器的工作有用。

---

## 6. 定时器普查——哪些定时器可以推迟，哪些不能（The timer census — which timers may be deferred and which must not）

`docs/fork/power-timer-audit.md`。任何方向搞错都是缺陷：推迟一个看门狗会掩盖泄漏；不推迟一次轮询会烧掉无线时间。

| 定时器 | 节奏 | 判定 |
|---|---|---|
| `service/oomkiller` 轮询 | 自适应：默认最小 100 ms，最大 10 s | 一个看门狗，不是投机性唤醒。`intervalForState` 在稳定条件下逐步拉长，在压力下回到最小值；选项与平台策略会影响有效边界。 |
| `internal/memmetrics` 采样 | 1 ms | **仅基准** |
| `service/ssmapi/server.go` | 1 min | 可推迟——**但第一次遍历刻意不推迟** |
| `route/rule/rule_set_updater.go` | — | 可推迟——**第一次遍历也不推迟** |
| `service/api/dashboard.go` | 24 h（默认） | 可推迟——**第一次遍历也不推迟**，而且它是等待而不是跳过：`WaitProviderRefresh` 让循环停驻，因此下载落在唤醒时，而不是再等一整个间隔 |
| `dns_cache` | 1 h | 必须**不**被门控 |
| `route` macOS 挂起看门狗 | 5 s | **一个看门狗，不是投机。** 它比较墙上时钟与单调时钟（后者在休眠期间停止），并在二者分歧时调用 `SystemResumed`。它存在是因为无头 Mac 没有 NetworkExtension 的 sleep/wake 回调；NetworkExtension 路径改用 `CommandServer.Pause`/`Wake`。它必须**不**被休眠状态门控。 |

**有三个周期定时器可推迟，而对这三个来说，第一次遍历无论如何都会运行。**“第一次遍历不推迟”这条规则，正是让推迟不会延迟初始正确性的那一条。

本领域有两个 worker **不是定时器**，它们由状态而不是由等待来门控，因此不属于上面的表：

- `dns.client.backgroundRefreshDNS`——由一次缓存 HIT 调度，而不是由时钟。当调速器处于 `QUIESCENT`/`DEEP_IDLE` 或已关闭时被直接拒绝，并在 `WAKING` 带中经 `Allow().NetworkProbe` 放行。跳过它只损失新鲜度。
- `transport/wireguard/recovery.go`——会轮询，但只在一个会话陈旧时；而且它**不**被门控：`runtimecoord` 已经把 rebind 限制为每个窗口一次，在休眠期间取消它会用一次浪费的 rebind 换来一条未恢复的隧道。已接受，不推迟。

`HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` 载有两个门控背后的测试与变异。

---

## 7. `ReuseSuspect`（复用可疑判定）

| 属性 | 值 |
|---|---|
| 顺序 | `RetireSuspect()` **先**，然后 `CloseIdleConnections`（`route/reference.go:496-511`, `:547-556`） |
| 池清单 | `route/reusable_owner_inventory_test.go` 检查具名所有者与能力；它并不禁止其他实现 |
| 触发 | 一个**休眠/复用边界**，经由 `powerGovernor.AddReuseObserver(m.onReuseBoundary)`（`:142-144`） |
| 不得 | 重置网络、终止流、触碰按需隧道，或拨号 |

---

## 8. 证据（Evidence）

| 保证 | 等级 | 复检 |
|---|---|---|
| trim 阶梯的取值与动作；未知 ⇒ 忽略；`TrimUIHidden` 不是内存压力 | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./common/runtimecoord/...` |
| 调速器状态机与分带 | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./common/power/...` |
| 复用判定在 level 被释放**之前**发布 | `VERIFIED_UNIT`，且测试证明该分带确实被演练过 | `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestTheReuseVerdict .` |
| sleep edge 在 level 被暂停**之前**记录 | `VERIFIED_UNIT`，带一个空泛性防护 | `-run TestTheSleepEdge .` |
| 100 次 sleep/wake 循环、100 次网络转换、Close 之后无复活 | `VERIFIED_UNIT`（无 `-race`） | `-run 'TestOneHundred\|TestNoResurrectionAfterClose' .` |
| Apple 设备轴 | 对一次 iPhone 17 Pro Max / iOS 26.4.2 运行为 **`VERIFIED_DEVICE`**；对 iPad、macOS 及任何其他 OS 为 **`UNVERIFIED`** | 上面的设备记录 |
| macOS 休眠/唤醒修复 | 对决策为 `VERIFIED_SOURCE` + `VERIFIED_UNIT`（墙上时钟对单调时钟的检测器、**以时段为键**的 `SystemResumed`、**感知时长**的重置：30 s 以下为 medium，达到或超过为 dead-path）；**在实体 Mac 上为 `UNVERIFIED`** | `go test ./common/suspendwatch -count=1` 与 `go test ./route -run TestSystemResumed -count=1`（9 个测试，包括第一次之后立刻第二次休眠、短休眠的 medium 分支与长休眠的 `ReclaimDeadPath` 分支）；设备侧那一行是 `system resumed after …` 日志之后跟一次重置 |
| 真实 TUN、真实 NetworkExtension、网络转换 | **本次 Windows 刷新中为 `UNVERIFIED`**；设备与构建相关的证据必须单独检查 | 需要适当的平台与设备 |
| 无竞态 | **`BLOCKED_BY_ENVIRONMENT`** | 需要 cgo |

那个顺序测试是这组里设计最强的文件：它在暂停回调与复用观察者之间共享一个 `orderingSequence` 记录器，并断言 `[]string{"level-paused","reuse-verdict","level-released"}`，然后用 `require.NotEqual(… ReuseKeep …)` 与 `require.Equal(uint64(1), governor.ReuseEpoch())` **防止该断言变成空泛的**。sleep 测试通过一个 `probingAxis` 从 level 调用**内部**观察顺序，并用 `require.True(t, axis.probed, …)` 防止空泛。**照抄这个模式**——一个不证明代码路径确实被走到的顺序测试毫无价值。

---

## 9. 来源（Provenance）

| 提交 | 类型 | 它做了什么 |
|---|---|---|
| `aac340808` | LIFECYCLE | 休眠权威 |
| `c62342717`, `540d7cb94`, `4a95e0aeb`, `e966daea2`, `a804916ef`, `d93b68560` | FIX | `DEEP_IDLE` 处的池释放；不可达的 `DEEP_IDLE` 分支；一个陈旧截止时间 |
| `6ebb5799a` | FIX | 陈旧截止时间 |
| `89b4b2c25` | FIX | `DEEP_IDLE` 曾不可达 |
| `0ff90482` | FIX | 把设备 resume edge 花在一个 **edge 闩锁**上，而不是花在屏幕 level 上 |
| `e4327dca` | LIFECYCLE | 给 Apple 设备轴一个真实的发布者与一次真实的释放 |
| `4c7d96c0` | FIX | 在**释放线**上承载 Apple 设备轴，而不只是在一个分支上 |
| `645d96b4` | FIX | 一次当前状态的屏幕读取只协调 level，它不启动一次休眠 |
| `60681f27` | FIX | 一次 resume 不会结束一个休眠时段，因此它不得消耗掉自己的边界 |
| `e20875359` → `e661258a6` | FEATURE → **REVERT** | `IsDevicePaused()` 的 sleep-edge 门控，基于设备证据回滚。两份存档都保留。 |
| `1331e8567`, `1f85dbc94` | FIX | 基于代次的 rebind 预算；`TrimMemory` **不**推进网络代次，而 `ReleaseMemory` 会 |
| `5f56f02c`, `09964183`, `93823c3d` | MEMORY | 裁剪路径；四个池全部可达 |
| `3c6dd135b`, `f8b0af5f6`, `48dd4469e`, `10f9e17d2`, `eac9cc83d`, `b5c0009d1`, `95159084c`, `666470cbe`, `b5c39a11e`, `c2d93396e`, `da14572a7`, `1965bbf35` | TEST | 泄漏普查/测试完整性家族——**其中多个修复的是根本无法失败的测试** |
| `12aa6db064f`, `dbf8de8b2a5` | FEATURE | 权威的前两个**消费者**：乐观 DNS 刷新（按状态拒绝）与 dashboard 下载（停驻在 `WaitProviderRefresh` 上）。测试与五个变异在 `reports/core/HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` |
| `ff897d35772` | FIX | 在构造期而不是在 `Start` 中解析 DNS 调速器：让后台 goroutine 读取的字段变为不可变，而不是给它定序 |

历史普查行中的 `3c6dd135b` 在当前父仓库中无法解析；在把那个前缀当作已验证的提交锚点之前，请检查原始报告或其他仓库。

全部为 **OWN**。上游家族 `Use screen state to end device pause on iOS`（本区间内出现 ×11）是 `UPSTREAM-INHERITED`/孤儿——见 `BASELINE.md` §provenance。

---

## 10. AI 告诫（AI cautions）

1. **不要合并 level 与 edge。** 一次当前状态读取不是一次转换；`resumed()` 刻意不发布任何东西。
2. **不要把 display-on 当作解锁。**
3. **不要通过门控一个暂停状态来“修复”休眠边界。** 那被尝试过，在真实设备上测得零个边界，并被回滚——锚点与闩锁是同一个字段。
4. **不要让裁剪路径重置网络、拨号、唤醒被挂起的工作或杀掉活跃的流。**
5. **不要让 `ReleaseMemory` 停止重置网络。**
6. **不要把 `TrimUIHidden` 当作内存压力。**
7. **不要把未知的 trim level 默认成渐进动作。** 未知必须被忽略。
8. **不要在客户端里分类 level。** 映射归核心所有。
9. **不要在没有查过普查的情况下添加周期定时器**；如果它是一个看门狗，它就必须不被休眠状态门控。
10. **如果一个定时器可推迟，就保持第一次遍历不推迟。**
11. **不要在只实现了 `IdleConnectionReleaser` 的地方断言 `IdleConnectionKeeper`。**
12. **对照退役遍历与生命周期契约审查新的 `ReuseSuspect` 所有者。**
13. **不要让后台工作唤醒空闲资源。** 唤醒它的是需求。
14. **不要写没有空泛性防护的顺序测试。** 本 fork 自己最好的那个测试存在，正是因为一个顺序断言可以在路径从未运行的情况下通过。
15. **不要把那一次 iPhone 运行泛化到 iPad、macOS 或其他 OS 构建。** 记录只是一台设备。
16. **不要为上述任何内容引用 `verify-evidence/`**——见 `ci-release.md` §3。
17. **不要把 suspendwatch 的基线移到第一个 tick。** `Watcher.Start` 在 ticker 存在之前就同步地给检测器做了初值。如果第一次读数发生在第一个 tick 上，那么一个在 Start 之后立刻挂起的主机会在唤醒时带有一个落在休眠错误一侧的基线，而那次挂起将永远不会被报告。该回归是 `TestTheWatcherPrimesAtStartBeforeTheFirstTick`。
