# 核心生命周期与运行时协调（Core lifecycle and runtime coordination）

## v0.1.9 之后的网络切换所有权（Network transition ownership after v0.1.9）

**VERIFIED_SOURCE**，测试目标 `011f3fdcd99`：`adapter/network.go` 区分重置 generation、
切换稳定性，以及一次原子的 `NetworkTransitionSnapshotter` 观测。`route/network.go` 在
`transitionAccess` 下实现这一对机制：只凭一个新发布的所有权 epoch，不足以证明
一次切换已经落定，因为 DNS 与传输层重置可能还挂着。按流（per-flow）的工作不能把在这个窗口里
捕获的结果当成稳定网络结果。重置 generation 在重置开始时推进，不在重置完成时推进；这几个相关计数器
各有各的角色。改这条边界时，请检查 `NetworkTransitionSnapshot`、`beginTransition` 与
`ResetNetwork`。相关测试是 `common/dialer/transition_stability_test.go` 与
`common/dialer/reset_epoch_ownership_test.go`。这不是设备恢复测量。

`route/conn_halfclose.go` 观测半关闭（half-closed）流，只在对端可证明已消失时释放；它有意不把
健康的静默当成失败。从不发生半关闭的流不在这个观测器的范围内，所以它不是能检测活跃 TCP 流
静默停滞的通用探测器。设计空闲超时之前，请看看历史完整审计的 BUG-007 和当前源码；
从这个观测器推不出任何设备级别的可用性结论。

观测器只在一个前提下启动：半关闭干净地发生过，底层连接接受 keepalive 配置，**并且**之后的一次读取
能给出终局裁决。`terminalReadObservable` 接受用户态的 `tun.GoConn` 与显式测试标记；普通内核
`net.TCPConn` 即使能武装 keepalive 也会被跳过。EOF 和本地读取超时不会释放一条流；终局错误会
一次性结算这个观测器，并调用共享的幂等拆除（teardown）。正常拆除或关闭会把挂起的观测结算为已停止。
生产设置要求 20 s 空闲、三次 10 s keepalive 探测，以及从 15 s 到最长 60 s 的观测器轮询；这些是
配置与调度参数，不是实测的设备恢复保证。本次校准中，聚焦的对端消失（peer-gone）、存活对端
（live-peer）、内核跳过（kernel-skip）、关闭（shutdown）与拆除竞态（teardown-race）单元测试
在 Windows 上以 Go 1.26.10 通过。

`route/network.go` 在派发（dispatch）之前认领一个切换令牌，并合并重复通知；更新一次所有权会取代
一个挂起的令牌。它会重置路由器的 DNS 状态，而这件事跟网络暂停期间接口监听器是否被推迟无关。
`route/conn_reclaim.go` 区分两件事：排空（drain）一次切换，与回收一条已确认死亡的路径或关闭。
排空策略保留近期或不可观测的活动，并清扫旧代次的流；死路径/关闭策略则关闭全部。接口更新路径
在分类恢复时还会考虑发生变化的接口；不要用统一的 `CloseAll` 取代它，也不要假设每一次短暂间隙
都是死路径。参见 `route/handover_recovery_classification_test.go` 与
`android_handover_generation_test.go`。

`cb71cf80d72` 上新增的入口与生命周期边界：

* `common/listener/NetworkWithAddressFamily` 给 IPv4 与 IPv6 的**通配**（wildcard）绑定加上显式的
  `4` 或 `6` 后缀，具体地址或无效地址则原样通过。TCP 与 UDP 监听器工厂共享已配置的监听选项，
  Mieru SDK 工厂路径也在其内；但不要推断每个第三方监听器都用这些辅助函数。本地 listener 包的
  测试套件在 Windows 上通过，仅限 Linux 的通配冲突测试没有跑。
* `transport/device/system.go` 用 `tun.ReadRetry` 重试已分类的瞬态读取失败，遇到已关闭和
  未分类错误就停。Windows 专有的新增部分显式识别原始值 10035、8 与 995。注释里虽然提到原始
  Winsock 10004、10008 与 10055，当前谓词并没有显式包含它们；在 Windows Go 1.26.10 上，
  这几个值也不匹配固定版本的 `tun.IsRecoverableReadError` 所测试的 POSIX 常量。这些取值的
  原始错误会被谓词判成致命；真实 TUN 在给定路径上会不会投递其中之一，尚未验证。`transport/device`
  的单元测试在本地通过；它们的 Windows 用例没有测那三个取值。
* SSH 的 `InterfaceUpdated` 在 `clientAccess` 下把当前客户端与连接分离，再在锁外关闭分离出来的连接。
  这样可以避免一次未加锁的更新关掉新装入的会话；但它不确立无缝的在线流（live-stream）恢复。
  SSH 包测试在 Windows 上本地通过。
* `route/flow_tracker.go` 用一个互斥锁串行化 `AttachFlow` 与 `CloseFlow` 回调，数据包计数器却留在
  锁外。串行化能防止重叠；但它本身不证明两个回调并发开始时谁会胜出。
  聚焦的串行化与计数器测试在 Windows 上本地通过。
* Linux 网络命名空间（network-namespace）启动靠自己的管道让持有者保持存活，并向 scope 注册清理。
  `ownsUserNamespace` 依据 euid 与命名空间所有者检查来挑选被记录下来的 `nsenter` 命令；它本身
  并不是进入命名空间的操作。本次 Windows 校准没有测 Linux 命名空间执行。

协议所有权不可互换：AnyTLS 和 Snell 把各自的客户端注册到 scope；VLESS 在存在时会注册它的传输与
多路复用拨号器。它们的 outbound 还实现接口更新与空闲连接钩子（`protocol/anytls/outbound.go`、
`protocol/snell/outbound.go`、`protocol/vless/outbound.go`）。接口更新或空闲裁剪应当调用所有者
预期的钩子；这不是关闭全部活跃连接的许可。这些是评审目标上的源码级契约；在本主机未加构建标签的
运行里，Snell 包报告没有任何测试文件。

`Box.Close`（`box.go`）用 `closeOnce` 串行化重复调用，并记录最终错误。它把清理过程中的 panic
恢复成一个错误，让后续的 Close 无法在一次失败的拆除之后报告成功。这个 panic 会继续展开，越过
剩余的清理：恢复并**不**证明拆除的其余部分已经执行。状态视图在 governor 与 scope 拆除之前就已
分离。这些在评审目标上是 `VERIFIED_SOURCE`；改边界之前请检查 `box_close_test.go` 与确切的
目标。

ShadowTLS 入站有两个不同的所有权边界：`protocol/shadowtls/pre_auth_timeout.go` 给第一条完整的
TLS 记录设定时限，随后把该截止时间清掉；`handshake_target_cleanup.go` 为一次握手跟踪目标连接，
并在 SDK 返回之后关闭遗留连接。不要把预认证（pre-auth）截止时间套到随后的长期中继上，也不要
假设框架持有一个从未注册过的 SDK 连接。这在 `011f3fdcd99` 上是 `VERIFIED_SOURCE`，并不是与
每一台 fallback 服务器互通的声明。

**范围（Scope）：** `adapter.Scope`、`Box` 的启动/关闭顺序、`common/runtimecoord`、
`route.ReferenceManager`，以及 `common/power` 与它们的交互。

**来源（Provenance）：** `common/runtimecoord` 与 `common/power` 是 **fork 创建的包**（在
`e4b7e692` 处不存在）。`adapter/lifecycle.go`、`box.go`、`box_lifecycle.go` 与 `route/reference.go`
被大幅修改。触及这些路径的 fork 提交有 **85** 个（`adapter` 77 个，`common/runtimecoord` 8 个）。

**改以下内容时加载本模块：** 启动/关闭顺序、拆除、网络 generation、重建租约、空闲资源退役、内存裁剪，
或者任何会响应网络切换的东西。

---

## 1. `adapter.Scope` —— 所有权原语（the ownership primitive）

`adapter/lifecycle.go` 负责注册到某个 scope 的清理逻辑，以及由 `Scope.Start` 创建的子 scope。
这不意味着进程里的每个资源、每次关闭都属于这个 scope；请按实际的所有者与注册路径来。

| 元素 | 锚点 | 规则 |
|---|---|---|
| 状态阶梯 | `lifecycle.go:70-80` | `scopeOpen` → `scopeClosing` → `scopeClosed` |
| `Scope.Add(cleanup)` | `lifecycle.go:112-138` | **如果 scope 已经在关闭，清理会在*调用者*的 goroutine 上同步执行。** 注册绝不会被静默丢掉。 |
| `Scope.Start(name, component, stage)` | `lifecycle.go:147-183` | 以**组件**（component）为键创建子 scope，把子对象的 `Close` 注册成清理，在**锁外**运行 `component.Start`，然后重新读取子上下文并返回 `E.Cause(ctxErr, …)`，让中途被取消的启动**报告失败而不是成功**。 |
| `Scope.Close` | `lifecycle.go:218-283`，排空在 `:251` | 首个调用者接管队列，按**反注册顺序**（`slices.Backward`）排空；后来的调用者通过 `closeDone` **汇入**；结果会滤掉 `E.IsClosed`/`E.IsCanceled`，但仍记录下来。**在同一个 Scope 上不可重入**（`:214-217`）。 |
| 阶段（Stages） | `lifecycle.go:21-35` | `StartStateInitialize → StartStateStart → StartStatePostStart → StartStateStarted` |

### 完成边界 —— 要准确表述（The completion boundary — state it correctly）

`Scope.Close()` 返回，意味着**恰好**是下面这些，仅此而已：

* 正常返回时，队列里已有的每一项清理都已执行，`closeErr` 也已是最终值；
* 它**不**等待一个正在运行、还没走到 `Add()` 的 `component.Start()`——`Start` 的时长没有任何上界，
  一等就会让 `Close` 变成无界的；
* 在这个时点之后注册的清理，会在 `Add()` 内部、于注册方的 goroutine 上同步执行——泄漏避免了，但这笔
  迟到的资源**不**在 `Close` 报告的范围内；
* 这类迟到清理产生的错误由 `Add()` 记日志，不会被归并到任何地方；`closeErr` 已是最终值，
  所以后续的 `Close` 也不会把它收进去。

由 `adapter/lifecycle_close_boundary_test.go` 固定（`TestScopeCloseDoesNotWaitForAnInFlightStart`、
`TestScopeCloseDoesNotFoldInALateCleanupError`、`TestScopeCloseJoinsAnAlreadyStartedDrain`、
`TestScopeCloseDoesNotWaitForAStartThatNeverRegistered`）。

清理里发生 panic，会在 `scopeClosed` 与 `closeDone` 发布之前打断排空；于是随后直接调用这个 scope 的
`Close`，可能无限等下去。`Box.Close` 会捕获这个 panic，并为重复的 Box 调用保留一个错误，
但不会把被打断的排空恢复回来。请避免同 scope 的重入清理，那同样会死锁。嵌套子 scope 的清理是另一种
所有权关系。

---

## 2. `Box` 的启动与关闭顺序（Box start and close ordering）

### 启动（Start）（`box.go`、`preStart` 与 `start`）

在 `011f3fdcd99` 上，入口点分别是第 1045 行的 `PreStart`、第 1055 行的 `Start`、第 1090 行的
`preStart`、第 1160 行的 `start`、第 1240 行的 `Close` 与第 1264 行的 `close`。目标一变，这些
函数就要重新定位；旧的 v0.1.9 行号只是历史锚点。

`preStart()`：
1. `logFactory.Start()`
2. 先 `scope.Add(runtimeCoordinator.Close)`，再 `scope.Add(logFactory.Close)`——
   **先注册 ⇒ 最后关闭**
3. `HistoryStorage.Close`——只有这个存储由 `Box` 创建时才有这一步，清理归创建者所有
4. `applyDebugOptions`、调试 HTTP 服务器，然后初始化内部服务
5. `startComponents(Initialize)` —— **httpClient → network → dns-transport → dns-router →
   connection → router → outbound → endpoint → certificate-provider → inbound → service**
6. `startComponents(Start)` —— **outbound → dns-transport → network → connection → httpClient →
   router → dns-router**

随后 `start()` 启动内部服务、NTP 与 **endpoint → cert-provider → inbound → service**；它在
`start()` 里的组件列表及其对应的内部服务阶段上运行 `PostStart` 与 `Started`，然后调用
`s.network.EstablishInitialNetworkEnvironment()`。HTTP 客户端服务不在这两个组件列表里；
不要推断每个组件都会走完每个阶段。

> **在网络管理器观测到自己的网络之前，Box 都不算“已就绪”。** `Start` 会等
> 这次调用**同步**执行完才返回。措辞要精确：只有当管理器已同步观测到自己的网络、
> **或已确立没有任何东西可观测**之后，`Start` 才返回——这次调用受 `s.network != nil`
> 保护，在切换不适用或没有任何变化（已经观测过，或没有默认接口）时是空操作。将来若有
> 改动把它提前，第一条被路由的流就会跟它本该看到的网络状态发生竞态。

有两处顺序看着随意，其实不是：

* **Initialize 与 Start 的顺序不同。** `httpClient` *最先*初始化、*第五*个启动；`outbound`
  *第七*个初始化、*最先*启动。校验要依赖图，运行时则要传输层。
* **`router.Initialize` 只做*编译*。** 规则的 `Start()` 发生在 **PostStart**（`route/router.go:252-261`）——
  那是规则可能打开规则集文件、或启动进程读取器的第一个时点。所以每一项启动期校验，都在任何规则
  被启动之前就跑完了。

### 关闭（Close）（`box.go`、`Close` 与 `close`）

`closeOnce.Do` → `statusView.Disconnect()` → 注销暂停回调 → **关闭电源 governor** →
`scope.Close()`。

> governor 在 scope **之前**关闭，因为 `ReferenceManager` 会读它。把这两者的顺序倒过来，就会关掉
> 一个观测者已经不在了的资源。

---

## 3. `common/runtimecoord` —— 一个代次、基于身份的租约（one generation, identity-based leases）

`common/runtimecoord/coordinator.go`（631 行）与 `platform_events.go`（507 行）。

### 对象（The objects）

| 对象 | 锚点 | 含义 |
|---|---|---|
| `Coordinator` | `coordinator.go:36-46` | `parent`、`access`、`epoch`（从 0 开始，表示“尚未重置”）、`changed chan struct{}`、`closed`、`registrated` |
| `Advance(epoch)` | `:80-104` | **单调**：`epoch <= c.epoch` 是静默空操作（`:85`）。关闭并*替换*变更通道，快照注册项，再在**锁外**通知（`:98-103`）。 |
| `GenerationChanged()` | `:142-149` | 在同一把锁下返回 `(epoch, channel)`，因此调用者不会在读编号与等通道之间错过一次切换。 |
| `Register(label)` | `:156-185` | 在已经推进过的代次上取得的注册，会记录当前 epoch **并**记录 `observed = true`（`:170-173`）。 |
| `Registration` | `:191-223` | 持有 `epoch`、`observed`、`lease *RebindLease`、`nextLease`、`lastRebind`、`rebindCount`、`window`。 |
| `RebindLease` | `:230-238` | `id`、`generation`、`reason`、`ctx`、`cancel`、`expired`。 |
| `Expired()` | `:282-289` | 以下任一成立即为真：已过期、**注册已关闭，或这个租约不再是当前所有者**。 |
| `Complete()` | `:295-310` | 对被取代的租约是**空操作**，但它**总是会取消**。 |
| `observeEpoch` | `:355-381` | 推进 epoch，清除 `observed`，取代并取消在途租约，并**清除 `lastRebind`**，好让新网络立刻得到一次重试（`:375`）。 |
| `ScheduleRebind`/`BeginRebind` | `:409-465` | 合并窗口默认是 `adapter.RecoveryWindow`（`:437-440`），在**授予（grant）**时武装，不在完成时武装（`:444-446`）。 |
| `Stale()` | `:499-518` | 以下任一成立即为真：已关闭、`!observed`，或协调器 epoch 大于本 epoch。 |
| `Acknowledge()` | `:526-536` | 唯一能把“epoch 已推进”变为“该资源是当前的”的机制。 |
| 探测来源（Probe origin） | `:583-631` | `ProbeForeground` 与 `ProbeAutomatic`，以及 `MeasurementContext`——决定一次测量能否**唤醒被挂起的资源**。 |

### 措辞不得夸大的那条不变量（The invariant that the wording must not overstate）

> **“一次只做一次逻辑恢复”是一种租约*身份*，不是一个布尔值。**

`Registration.lease` 之所以是 `*RebindLease`，正是为了让一个*被取代的*租约无法完成或释放一个
更新的租约。将来若有人用 `bool` 取代它，就会重新引入这个指针本来要防的那个缺陷。理由写在
`coordinator.go:202-215`。

### `TrimMemory` 与 `ReleaseMemory` 不同（TrimMemory and ReleaseMemory are different）

`TrimMemory` **不**推进网络 generation。`ReleaseMemory` **会**。协调器的 `Trim` 路径（`5f56f02c`、
`09964183`、`93823c3d`）与释放/重置路径，在设计上就是两个彼此分离的入口点。

---

## 4. `route.ReferenceManager` —— 退役清单（the retirement inventory）

`route/reference.go`（`ReferenceManager` 在 `:21-42`，于 `box.go:662` 构建）。

退役清单并不是唯一的主机/本地流表面。`adapter/network.go:37-38` 增加了
`RegisterHostPort`/`HostPort`，`route/network.go` 则从 TUN 路由端口服务端点本地流
（`93b2e4f5eec`）；流日志器搬到了 `adapter/flow_logger.go`，现在由路由流与主机流共享
（`ab5fcb59abd`；`route/flow_tracker.go` 已被删除）。端点本地的**契约**归
`masque-connectip.md`；本模块负责网络管理器注册与退役这一侧。

### 它按顺序做了什么（What it does, in order）

`Start` 只在 `StartStateStarted` 阶段运行（`:98-101`）：创建 `observable.Subscriber`（`:102`），
挂接 `urltest.HistoryStorage` 与 `clashmode.Manager`（`:104-111`），播种 `devicePaused`
（`:112-114`），运行 `update()`，启动 `m.loop()`（`:115-116`），注册暂停回调（`:117-133`），
注册 `powerGovernor.AddReuseObserver(m.onReuseBoundary)`（`:142-144`），**最后**设置
`m.closed.Store(true)`，让它成为**第一个**运行的清理（`:145-150`）。

引用闭包（`update()`，`:211-361`）：

* 静态种子：NTP detour（`:49-51`）、每个 outbound/endpoint 的 `domain_resolver`（`:52-57`）；
* DNS 规则引用（`collectDNSRuleReferences`，`route/reference_rule.go:140-168`）；如果**没有任何
  规则是终结的（terminal）**，就加入管理器的默认传输（`:228-232`）；
* 路由规则引用（`collectRuleReferences`，`route/reference_rule.go:105-138`）；
* 按需（on-demand）endpoint 被单独挑出，排除在 outbound 队列之外（`:240-248`）；
* 对 inbound、service、HTTP 客户端管理器、DNS 传输与 outbound **传递地**遍历
  `adapter.Referrer`（`:249-302`）；
* 最后是 `IdleConnectionKeeper` 的决策（`:306-358`）与未被引用传输的重置（`:333-358`）。

### 花掉四轮评审的那条规则（The rule that cost four review passes）

`retireIdleResources()`（`:440-494`）覆盖 outbound、endpoint、DNS 传输**以及 HTTP 客户端
管理器**。断言的类型是 **`adapter.IdleConnectionReleaser`**，*不是*随附的
`IdleConnectionKeeper`。`:479-484` 处的注释记下了原因：断言随附接口时，运行时在**每一次**
遍历中都静默失败，导致 HTTP 客户端连接池在四轮评审里都不可达。修复提交是 `93823c3d` 与 `8735b1d`。

> **往退役遍历里新增连接池时，请实现并断言 `IdleConnectionReleaser`——并不要求完整的
> `IdleConnectionKeeper`，它缺失也不是缺陷。**

这是有意为之，不是缺口：某些传输没有安全的“不再产生新流”状态，因此在本地没法被复用排空，
只能提供破坏性关闭。

### `TrimIdleResources` vs `CloseIdleConnections` vs `RetireSuspect`

| 入口点 | 锚点 | 它可以做什么 |
|---|---|---|
| `TrimIdleResources()` | `:412-414` | 目前与 `CloseIdleConnections()` 的实现体完全相同；**区别只在调用方**（`:394-411`） |
| `CloseIdleConnections()` | `:416-418` | 相同的实现体 |
| `retireSuspectResources()` | `:512-537` | **先**调 `adapter.ReuseSuspect.RetireSuspect()`（`:496-511`），再调 `CloseIdleConnections`（`:547-556`） |
| `onReuseBoundary` | `:154-197` | **不重置网络、不终止流、不触碰按需隧道、不拨号。** |

`route/reusable_owner_inventory_test.go` 断言具名所有者（包括 XHTTP）仍然具备该遍历所期望的能力。
它没有枚举并禁止每一种可能新增的 `ReuseSuspect` 实现；新所有者仍然需要过一遍所有权与退役评审。

---

## 5. 证据、来源与注意事项（Evidence, provenance, cautions）

### 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| 关闭后注册的 scope 清理在调用者的 goroutine 上运行 | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./adapter/...` |
| `Close` 不等待在途的 `Start` | `VERIFIED_UNIT` | `adapter/lifecycle_close_boundary_test.go` |
| Epoch 单调性、租约取代、`Stale`/`Acknowledge` | `VERIFIED_UNIT` | `go test -count=1 ./common/runtimecoord/...` |
| 退役遍历到达全部四个连接池 | `VERIFIED_UNIT` | `route/reference_coverage_stress_test.go`, `route/reusable_owner_inventory_test.go` |
| 顺序固定（复用裁决先于级别释放；睡眠边沿先于级别暂停） | `VERIFIED_UNIT` | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run 'TestTheReuseVerdict\|TestTheSleepEdge' .` |
| 100 次睡眠/唤醒循环、100 次网络切换、Close 之后无复活 | `VERIFIED_UNIT`（无 `-race`） | `-run 'TestOneHundred\|TestNoResurrectionAfterClose' .` |
| 无竞态（race-freedom） | **`BLOCKED_BY_ENVIRONMENT`**——`-race` 需要 cgo，本主机没有 C 编译器 | Linux/macOS 或 Windows+gcc |
| 以上任何一项背后的设备行为 | **`UNVERIFIED`**——无 TUN、无真实 NetworkExtension | `docs/RELEASE-CERTIFICATE.md` §1 |

### 来源（Provenance）

| 提交 | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `8c7b230d` | LIFECYCLE | OWN | 新增资源生命周期协调器 |
| `5f56f02c` | MEMORY | OWN | 在内存偏高但尚未危急时裁剪空闲资源 |
| `09964183` | FIX | OWN | 阻止内存裁剪构建第二个传输 |
| `93823c3d` | MEMORY | OWN | 退役唤醒后不可达的连接池；能排空的就排空 |
| `8735b1d` | MEMORY | OWN | 让退役遍历能到达 HTTP 客户端连接池（此前从未到达） |
| `e4327dca` | LIFECYCLE | OWN | 给 Apple 设备轴一个真实的发布者与一次真实的释放 |
| `ae197b92` | FIX | OWN | 给被取代的接口监视器加栅栏；在切换锁之外发布 |
| `0ff90482` | FIX | OWN | 把设备恢复边沿用在边沿锁存上，不用在屏幕级别上 |
| `9dd91d51`, `482e4d3a` | HARDENING | OWN | scope 所有权加固 |
| `7e13f461` | FIX | **OWN, not upstream** | 清除**其所有者注册的**进程级接口 getter。提交正文说明，fork 自己的重构把注册移进 `newSystemBinding` 后**把它丢了**；这是修 fork 引入的泄漏，不是上游吸收。 |
| `27d8bb59`, `12ee55eef` | REPOSITORY-HYGIENE | OWN | 只为清掉 GitHub 上过时的“落后于 upstream”状态而做的祖先记录。**两者都是无树变更的合并**（`12ee55eef` 记录 `PRE_SYNC_TREE == post-merge tree`）。 |
| `813ddfa98`, `03de88adb`, `0d91c4547`, `16c898a82` | — | UPSTREAM-INHERITED | fork 携带的上游对象（见 `BASELINE.md` §provenance） |

### AI 注意事项（AI cautions）

1. **不要“修正”`Scope.Add` 同步执行迟到清理的行为。** 把它改成异步，正是资源能在 `Close` 无错
   返回之后继续活着的原因。
2. **不要让 `Scope.Close` 等待在途的 `Start`。** 它按构造就是无界的；两个需要它的组件
   （`protocol/masque/server.go`、`protocol/tun/inbound.go`）改用非阻塞交接（`startupGate`、
   `acquiredStillOwned`）。请照这个形状写。
3. **不要注册一个会关闭自己所在 scope 的清理。** 排空会等它自己；嵌套子 scope 是另一种所有权
   关系。
4. **不要用布尔值替代 `Registration.lease *RebindLease`。** 这个指针的存在，就是为了让被取代的租约
   无法释放更新的租约。
5. **不要把“epoch 已推进”当成“资源已重建”**——`Stale()` 与 `Acknowledge()` 是两种不同的状态。
6. **在只实现了 `IdleConnectionReleaser` 的地方不要断言 `IdleConnectionKeeper`。** 正是这个错误，
   静默地让整个退役遍历失效了。
7. **不要在 `Start` 中把 `EstablishInitialNetworkEnvironment()` 提前。**
8. **不要重排关闭顺序**（状态视图 → 暂停回调 → governor → scope）。
9. **不要让 `TrimMemory` 推进网络 generation**，也不要让 `ReleaseMemory` 停止推进。
10. **新的 `ReuseSuspect` 所有者要对照退役遍历与生命周期契约评审。**
11. **不要把 `27d8bb59`/`12ee55eef` 当作代码变更。** 两者都是只涉及祖先关系的合并；把它们当功能
    来读，正好颠倒它们的目的。
12. **不要为上述任何内容引用 `verify-evidence/`。** 那些日志指向 `cfc77dc5`，比发布基线落后
    101 个提交，按它自己 `README.md` 的规则已经过期。参见 `ci-release.md`。
