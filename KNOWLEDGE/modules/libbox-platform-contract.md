# libbox 与平台契约（libbox and the platform contract）

## Apple 来源与单运行器差异（Apple source and single-runner delta (updated 2026-10-31)）

在当前源码目标中，Apple 的 gitlink 和 ABI 检查常量都指向
`044e6d5447eb1285c950bd12cee10effab641f5a`；Android 对应
`c7837107dc0126fb704fdf08cf203e10b30c8a22`。在父仓库 `0067b03` 上，本地运行的
`check-libbox-abi.sh --pin-only` 检查通过，但它只比较固定版本，不是完整 ABI 扫描，也没有
扫描 Swift 调用点。

当时的两个本地客户端检出都早于父仓库记录的 gitlink，且未被重置。Windows 的符号链接检出
方式以及未初始化的 Desktop 子模块也限制了本地检查。源码选择、dispatch SHA、单 runner 模式
和发布冻结要求见 `ci-release.md` 的 2026-10-29 小节；下文 v0.1.31 的观察只适用于该版本。

## 基线后 TUN 重置交接（Post-baseline TUN reset handoff (testing, 2026-10-28)）

**VERIFIED_SOURCE，审阅版本 `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`。**
`protocol/tun/inbound.go` 使用 `InterfaceUpdated`、`stackDidStart`、`consumePendingReset`
和 `resetNetwork` 协调网络更新与协议栈启动。

协议栈启动前收到更新时，代码会设置 pending 标志；启动过程在保存协议栈后再次检查，并通过
原子操作取走待处理标志，避免启动与更新交错时漏掉重置。多次启动前更新可以合并；启动后，
每次被接受的更新分别触发重置，启动期间积累的待处理重置也会单独执行。已关闭的 inbound
不会重置。回归用例见 `protocol/tun/reset_before_start_test.go`；测试文件存在不等于已运行。

启动后的行为包含 `1ff0af39110`，该提交晚于 `v0.1.30`，但已进入后来的 v0.1.31 标签。
这一提交关系不能证明真实设备上的网络切换行为。

### IPv6 入口地址族决策（IPv6 entry presentment）

`route/network_family_entry.go` 在 box 级别只发布一次入口地址族决策，TUN 打开接口之前读取该决策（`protocol/tun/entry_ipv6.go`）。网络变化可以改变后续拨号时的 capability，但不会改写已打开的 TUN 地址；要改变入口决策，需要重启 box。

前置检查旨在确认所有已配置的 TUN 都能执行 IPv6 抑制。如果最终归约仍拒绝，inbound 会保留 IPv6 并记录不一致，不会回滚共享决策。`route/network.go` 中较旧的注释提到后续纠正写入，但应以当前 store 与 inbound 实现为准。本结论是针对审阅版本的 `VERIFIED_SOURCE`，没有经过真机网络切换验证。

### TUN DNS 劫持与快速路径顺序（TUN DNS hijack and fast-path ordering）

`protocol/tun/inbound.go` 配置显式或推导出的 DNS 劫持地址，并单独跟踪 53 端口劫持模式。
`JudgeFlow` 先检查 DNS 劫持，再看直连绕过或路由排除；
UDP 与 TCP 得到的动作并不相同，所以请保留确切的分支。
`dns_hijack_configuration_test.go` 与 `direct_fast_path_dns_test.go` 覆盖了配置与
顺序。本地 TUN 包测试套件在 Windows 上通过，但它不是真机泄漏测试。

### 命令服务器关闭（Command-server shutdown）

`experimental/libbox/command_server.go` 现在会在关掉 `StartedService` 的订阅者与管理器之前，先从
`CommandServer.Close` 调用 `CloseService`。因此，即使某个嵌入方在关闭服务器前没
单独发起 close-service RPC，它仍然会请求 Box 关闭。该方法
丢掉了 `CloseService` 的错误，所以它的空返回值并不是 Box 清理成功的证据。
参见带 `p2ios` 标签的 `TestCommandServerCloseClosesTheBox`；这里不暗示任何设备级结果。

### 平台默认接口监视器顺序（Platform default-interface monitor ordering）

在审阅版本中，如果管理器支持 `RefreshInterfaces`，`experimental/libbox/monitor.go` 会先
刷新接口列表，再发布默认接口和 pause 状态变化。默认接口没有变化时，它会在接口可用的情况下
调用 `UpdateNetworkEnvironment`，以便观察网关等环境变化；不支持拆分接口的旧实现则调用
`UpdateInterfaces`。

刷新可能耗时。为避免刷新期间监视器已关闭、刷新完成后仍写入默认接口，代码会在刷新后持有
默认接口锁并再次检查 closed 标志。`monitor_ordering_test.go` 和
`monitor_close_fence_test.go` 用测试替身验证这些边界，并不证明真实 Android 或 Apple 设备
上的网络切换。

**范围：** `experimental/libbox/*`、`daemon/*`、`include/*`（就它们定义面向客户端的表面而言）、
`cmd/internal/{mobilebuildtags,applebuildtags,appletags}`、`release/apple-client-refs.env`，以及
Apple/Android 产物固定值（pin）。

**本模块主要记录契约中核心侧的部分。** 客户端是独立的 Git
子模块，能不能用取决于检出状态。2026-10-31 的校准中，父级
固定值是 Apple `044e6d5447eb` 与 Android `c7837107dc01`；本地客户端工作树没有重置，可能
停在别的修订（观测到 Apple `aeccc754`、Android `9d15717a`）。Desktop 未初始化。
本地 Windows Git 对 Apple 设了 `core.symlinks=false`，因此链接条目物化成文本文件，
不会作为真实链接被跟随。下文关于客户端源码的发现，都限定在这些修订与这次
检出上。

**在修改以下内容时加载本模块：** 任何导出的 libbox 符号、`SetupOptions`、CommandServer API、
ABI 版本、平台事实（platform-facts）API，或 Apple/Android 固定值。

---

## 1. 不存在 `libbox.go`（There is no `libbox.go`）

gomobile 接口分布在 `experimental/libbox/` 的 110 个 `.go` 文件中，其中 90 个不是测试文件。`experimental/libbox/internal/**` 下另有 18 个文件，涉及 `oomprofile`、`runtimeinfo`（包括 `linkname.go` / `goroutine_badlinkname.go`）和 `procfs`。这些文件是内部构建输入，不属于导出的 ABI；统计 gomobile 构建范围时仍需将其计入。

### 顶层入口（Top-level entry points）

| 符号 | 锚点 |
|---|---|
| `Setup(*SetupOptions) error` | `setup.go:162` |
| `ReloadSetupOptions` | `setup.go:90` |
| `SetLocale` | `setup.go:171` |
| `Version()`, `GoVersion()` | `setup.go:178`, `:182` |
| `FormatBytes` / `FormatMemoryBytes` / `FormatDuration` / `FormatBitrate` / `FormatNATMapping` / `FormatNATFiltering` / `FormatFQDN` / `ProxyDisplayType` | `setup.go:186-252` |
| `CheckConfig(string) error` | `config.go:51` |
| `GenerateConfigSchema()`, `FormatConfig`, `HasTunInbound` | `config.go:259`, `:267`, `:282` |
| `AvailablePort(int32)`, `RandomHex(int32)` | `service.go:447`, `:467` |
| `ReadAndroidVPNType` | `build_info.go:32`, 仅 Android |

### `SetupOptions` —— 平台握手（`SetupOptions` — the platform handshake）

`setup.go:49-66`。**十六个字段**，全是字符串或整数，所以这个调用在构造上就是 ABI 稳定的：

`BasePath`、`WorkingPath`、`TempPath`、`FixAndroidStack`、`CommandServerListenPort`、
`CommandServerSecret`、`LogMaxLines`、`Debug`、`CrashReportSource`、`AppVersion`、
`AppMarketingVersion`、`OomKillerEnabled`、`OomKillerDisabled`、`OomMemoryLimit`、
`PowerReportEnabled`、`PlatformMetadata`。

**这个握手有三点客户端必须知道：**

* **`CommandServerListenPort` 是 `int32`，被收窄为 `uint16` 且没有范围检查**（`setup.go:80`）。
  大于 65535 的值会静默回绕。
* **`CommandServerSecret` 为空表示不做认证。**
* **不是有效 JSON 的 `PlatformMetadata` 会被静默丢弃为 `nil`**（`setup.go:95-99`）。
* **`ReloadSetupOptions`（`:90`）只覆盖 16 个字段中的 5 个。** 启动之后请用它 —— 绝不要再次调用
  `Setup`。

声明出来的表面很大：`gomobile_surface_test.go:348 TestGomobileMethodResultSurface` 数出
**452 个绑定声明，其中 23 个带指针结果** —— 这是一笔已登记的债务，因为 gomobile 打包结果帧里的
Go 指针正是 `bulkBarrierPreWrite` 崩溃（§8）。

---

## 2. 策略边界：核心决策，平台上报（The policy boundary: the core decides, the platform reports）

**这一点成立，而且由分类逻辑所在的位置来强制。**

| 关注点 | 归属方 | 锚点 |
|---|---|---|
| 上报一次平台观测 | **平台**，经由 `experimental/libbox/platform_events.go` —— 一个**薄转发器（thin forwarder）**：`MemoryTrim(level int32)` `:251`、`SetAppForeground(bool)` `:260`、`SetScreenOn(bool)` `:278`、`ReportDeviceWake()` `:286`、`Close()` `:294` | `:220` 构建 `boxPlatformTarget`；`:211 NewPlatformEvents` 路由进 `runtimecoord.PlatformEvents` |
| 判定某个级别**意味着什么** | **核心** —— `TrimLevel` 常量与 `TrimLevel.Action()` | `common/runtimecoord/platform_events.go:152-220` |
| 合并窗口 | **核心** —— `trimCoalesceWindow = time.Second`（`:223`）、`releaseCoalesceWindow = adapter.RecoveryWindow`（`:231`） | 如上所引 |
| 设备状态机 | **核心** —— `common/power.Governor` | `power-mobile-lifecycle.md` |

**如果让客户端发送“你应该 trim”，策略就会搬进三个客户端代码库。** 所以反过来：客户端发送
操作系统给出的数字，由核心分类。这就是契约。

### Android trim 阶梯（由核心拥有）（The Android trim ladder (core-owned)）

`common/runtimecoord/platform_events.go:152-179`；动作映射为 `TrimAction`，位于 `:184-191`
（`TrimIgnore` = 0、`TrimProgressive` = 1、`TrimRelease` = 2）。

| 常量 | 数值 | 动作 |
|---|---|---|
| `TrimLevelUnspecified` | 0 | **`TrimIgnore`** —— *"the zero value: no trim was reported. It is **not** Android's level 0."*（`:155-156`）（中文：零值：未上报任何 trim。它**不是** Android 的级别 0。） |
| `TrimRunningModerate` | 5 | **`TrimIgnore`** —— 建议性（`:158-159`, `:212-213`） |
| `TrimRunningLow` | 10 | `TrimProgressive`（`:160-161`） |
| `TrimRunningCritical` | 15 | **`TrimRelease`** —— *"the documented meaning is that a currently-running process will be killed if memory is not released. That is the only Android fact that justifies the reset-shaped pass"*（`:162-164`, `:148-151`）（中文：其文档化含义是，如果不释放内存，当前正在运行的进程将被杀死。这是唯一能证明“重置形态的一趟处理”合理的 Android 事实。） |
| `TrimUIHidden` | 20 | **`TrimIgnore`**（`:165-170`） |
| `TrimBackground` | 40 | `TrimProgressive`（`:171-172`） |
| `TrimModerate` | 60 | `TrimProgressive`（`:173-174`） |
| `TrimComplete` | 80 | `TrimProgressive` —— *"A reset here would shed the transports of a process that is already going away"*（`:175-178`）（中文：在这里重置会丢弃一个已在退场的进程的传输。） |
| 其他任何值 | — | **`TrimIgnore`**（`:211-215` 处的 `default:`） |

**Progressive = 10、40、60、80。Release = 15。Ignore = 0、5、20 以及所有未知值。**

这些数字是 Android 自己的 `ComponentCallbacks2` 取值原样带过来的（`:131-136`）——
核心对它们做映射，而不重新编号。由
`experimental/libbox/platform_events_test.go:344-352` 固定。

**为什么未知级别被忽略**（`:193-201`，引文）：*"Trimming is not free: it drops a DNS or HTTP
pool the next request then has to rebuild, so a trim issued for a level whose meaning we do not know is
a cost paid for a guess. Ignoring it is the safe direction — the consequence is only that a genuinely
tight process keeps its pools until the next level we do understand, while the alternative would make
every future platform level a silent behavioural change."*（中文：trim 并非无代价：它会丢弃一个 DNS 或
HTTP 池，下一个请求必须重建，因此对一个含义未知的级别施加 trim 是为一次猜测付出的成本。忽略它是更安全的
方向 —— 后果仅仅是一个内存确实紧张的进程会保留其连接池，直到我们确实理解的下一个级别；而另一种做法会让每一个
未来的平台级别都变成一次静默的行为变更。）

**为什么 `TrimUIHidden` 不是内存信号**（`:165-170`）：*"This is a LIFECYCLE fact, not a memory
reading, and it is deliberately NOT mapped to a trim… the trim callback is not a reliable delivery
channel for it."*（中文：这是一个生命周期事实，不是一次内存读数，且有意地不映射到 trim……trim 回调并不是
它的可靠投递通道。）在客户端侧于 `experimental/libbox/platform_events.go:246-250` 重复：
*"Do not map `TRIM_MEMORY_UI_HIDDEN` to a lifecycle fact here."*（中文：不要在这里把它
映射为生命周期事实。）

**合并：** `trimCoalesceWindow = time.Second`（`:223`）、`releaseCoalesceWindow =
adapter.RecoveryWindow = **90 s**`（`:231`、`adapter/runtime.go:153`）。一个**不同的**级别会立即生效
—— *"an escalation is a new fact"*（`:222`）（中文：升级是一个新事实）。trim 回调必须在**主线程之外**
上报（`experimental/libbox/platform_events.go:239-244`）。

### 三个结构性证明：决策者是核心而非平台（Three structural proofs that the core, not the platform, decides）

不是注释 —— 是产物：

1. **Android 的 sink 在物理上回不了话。** 每个 `PlatformEvents` 方法都不返回任何内容
   （`experimental/libbox/platform_events.go:234-282`），文件头（`:11-17`）给出了原因：
   `string`/`[]byte` 结果会把 Go 指针放进打包的 cgo 结果帧。**让平台来做分类的设计，
   就需要返回值。** 由
   `platform_events_test.go:106 TestPlatformEventsAreScalarAtTheBoundary` 固定，并由
   `gomobile_surface_test.go:348` 穷举检验。
2. **`writes == stats.Pauses + stats.Wakes`** —— `platform_events_action_ownership_test.go:28`、
   `:108-110`：目标**按每个*已发布的动作*恰好写入一次**设备级镜像，绝不
   按每个事实写入；而一个**延迟的** wake 既不会到达管理器，也不会到达镜像（`:74-80`）。
3. **sink 不能修饰一次测量** —— `platform_events_test.go:66-99` 解析源码并
   **禁止**它导入 `adapter` 或调用 `ContextWithProbeOrigin` / `MeasurementContext` /
   `WithValue` / `WithCancel` / `WithTimeout`。

### 边界不成立之处 —— 范围限制（Where the boundary does NOT hold — the scope limit）

`PlatformInterface`（`experimental/libbox/platform.go:5-35`）还带着几项**判断**：它们由平台写，
核心直接消费，不再重新推导——`isExpensive` / `isConstrained`（`:123`）、
`NetworkInterface.Type` / `Metered`（`:141`、`:145`），以及能力声明
（`UnderNetworkExtension`、`IncludeAllNetworks`、`UsePlatformShell`、
`UsePlatformBridge :31`、`UsePlatformAutoRedirect :33`）。

在受审目标上，`UseProcFS` 已不是 `PlatformInterface` 的方法。包装器把
所有权查找委托给 `PlatformInterface.FindConnectionOwner`；Android 另外暴露了一个独立的 libbox
`FindConnectionOwner` 实现，由 procfs 支持（`connection_owner_android.go`）。不要以为
删掉旧的包装器分支就等于删掉了 Android 助手，也不要以为每个客户端都会调用它。

> **准确的说法：生命周期与内存分类，以及它们换来的动作，都归核心；
> 平台拥有网络成本/类型分类和它自己的能力。** 把“平台只上报事实”当成 `PlatformInterface` 的
> 全局不变量，是错的。

平台的两项判断要**经核对（reconciled）**，不是直接信任：
`handover_coalescing_test.go:234`（一个计量来源必须与 `UpdateDefaultInterface` 一致）与
`:276`（仅计量变化不得声称一个网络 generation）。

**核心侧还承认*客户端侧*存在双驱动，这里引用并标记为 `UNVERIFIED`：**
`experimental/libbox/platform_events.go:272-277` 记下 Android 也会从
`ACTION_DEVICE_IDLE_MODE_CHANGED` 驱动 `Pause`/`Wake`，并说明混用两者*"would let a Doze wake lift a pause the
screen fact still asserts, with neither path knowing."*（中文：会让一次 Doze wake 解除屏幕事实仍然主张的
pause，而两条路径都不知情。）在 Android 客户端修订 `9d15717a85c5` 上，
`BoxService.kt` 接收 `ACTION_DEVICE_IDLE_MODE_CHANGED`，把 `isDeviceIdleMode` 入队，再在消费者里调用
`commandServer.pause()` 或 `.wake()`。这确认了那条具体的客户端源码
路径；它本身并不确立所报告的设备级竞态，也不覆盖所有客户端配置。

---

## 3. 级别、边沿与快照 —— 四套词汇（Level, edge and snapshot — four vocabularies）

| 词汇 | 级别 | 边沿 | 快照 / 核对 |
|---|---|---|---|
| `common/power.Governor` | `DevicePaused` `:471`、`NetworkPaused` `:659` | `DeviceWake` `:490`、`SleepStarted` `:525`、`Resumed` `:563`、`NetworkWake` `:671` | `State` `:53`、`Policy` `:168`、`ReuseBoundary` `:141` |
| `pause.Manager` | 其级别 getter | 其边沿回调 | — |
| `box_lifecycle.go`（Apple 映射） | `screenState(on)` `:173-179`、`lockState(locked)` `:193-199` | `slept()` `:94-101`、`resumed()` `:141`、`woke()` `:156-163` | `reconcileScreenState` / `reconcileLockState` `:228-252` —— **仅级别** |
| `runtimecoord/platform_events.go` | trim 阶梯 | 屏幕/前台边沿 | 事件快照 |

**为什么核对 API 只能处理级别：** 读一次当前状态不是一次转换。如果一次重读
就发布一个边沿，那么每次轮询都会像一次新转换。由
`box_lifecycle_ordering_test.go` 以及 `645d96b4` / `60681f27` 固定。

**为什么显示器点亮不是解锁：** `box_lifecycle.go:23-36`、`:173-199` —— 显示器点亮
只发布一个恢复*边沿*；只有**解锁**才释放该级别。

---

## 4. `CommandServer` / `CommandClient` —— 客户端使用的词汇（`CommandServer` / `CommandClient` — the vocabularies a client speaks）

| 侧 | 锚点 |
|---|---|
| `CommandServerHandler` | `command_server.go:54` |
| `NewCommandServer(handler, platformInterface)` | `:80` |
| gRPC 认证拦截器 | `:129`, `:147` |
| `Start` / `Close` | `:165`, `:210` |
| `StartOrReloadService(configContent, *OverrideOptions)` / `CloseService` | `:232`, `:251` |

**生命周期与控制（平台→核心表面）：**

| 调用 | 锚点 | 类别 |
|---|---|---|
| `Pause()` | `:422` | 边沿 |
| `Wake()` / `WakeNow()` | `:502`, `:555` | 边沿 |
| `RecordScreenState(on bool)` | `:583` | 边沿 |
| `RecordLockState(locked bool)` | `:612` | 边沿 |
| `ReconcileScreenState` / `ReconcileLockState` | `:643`, `:663` | **快照（级别）** |
| `ResetNetwork` | `:678` | 动作 |
| `UpdateWIFIState` | `:686` | 事实 |
| `rememberDeviceLevel` / `restoreDeviceLevel` | `:310`, `:368` | 内部级别记账 |
| `NeedWIFIState` / `NeedFindProcess` | `:263`, `:271` | 能力查询 |

`platformHandler` 实现 `ServiceStop` / `ServiceReload` / `SystemProxyStatus` /
`SetSystemProxyEnabled` / `TriggerNativeCrash` / `WriteDebugMessage` / `ConnectSSHAgent`
（`:694-727`）。

**客户端侧：** `CommandClient`（`command_client.go:25`）、`NewCommandClient`（`:92`）、
`NewStandaloneCommandClient`（`:83`）、`SetXPCDialer`（`:79`）、`Connect`（`:260`）、
`ConnectWithFD`（`:264`）、`Disconnect`（`:343`）；流 `handleLogStream`（`:435`）、
`handleStatusStream`（`:467`）、`handleGroupStream`（`:488`）、`handleClashModeStream`（`:505`）、
`handleConnectionsStream`（`:547`）、`handleOutboundsStream`（`:569`）；动作 `SelectOutbound`
（`:586`）、`URLTest`（`:599`）、`SetClashMode`（`:611`）、`CloseConnection`（`:623`）、
`CloseConnections`（`:635`）、`GetSystemProxyStatus`（`:675`）、`TriggerGoCrash`（`:697`）、
`TriggerNativeCrash`（`:709`）、`TriggerOOMReport`（`:721`）、`GetStartedAt`（`:750`）、
`StartNetworkQualityTest`（`:783`）、`StartSTUNTest`（`:859`），外加针对
Tailscale（`:976-1180`）、USBIP（`:989`、`:1298`）、OpenConnect（`:1002`）、OpenVPN（`:1058`）、
Taildrop（`:1354`、`:1367`、`:1441`、`:1543`、`:1556`）的按端点订阅。

**`daemon`：** `StartedService`（`started_service.go:48`）、`ServiceOptions`（`:89`）、
`NewStartedService`（`:105`）、`StartOrReloadService`（`:324`）、`CloseService`（`:422`）；
`Instance`（`instance.go:26`）、`attachInstance`（`:155`）；`ManagedService`（`managed_service.go:17`）；
`NewServer`（`server.go:16`）；`NewAttachedService`（`attached_service.go:11`）。

---

## 5. ABI 版本管理（ABI versioning）

| 元素 | 锚点 |
|---|---|
| 常量 | **`daemon/started_service.go:39` — `const APIVersion = 4`** |
| 提供方 | `StartedService.GetVersion`（`:208-213`、`:211` 的 `ApiVersion: APIVersion`） |
| 线缆字段 | `daemon/started_service.pb.go:297`（`ApiVersion int32`）、`GetApiVersion()` `:339` |
| 消费方 | `experimental/libbox/command_client.go:760 GetAPIVersion()` → `version.ApiVersion`（`:766`） |

`daemon/started_service.pb.go`、`managed_service.pb.go` 及其 `_grpc.pb.go` 是**已入库的
生成代码**；`cmd/internal/protogen` 负责重新生成（`Makefile:57 proto`）。**`protogen` 会去执行一个
`Makefile` 从未安装的 `protoc`**，所以 `make proto` 需要 `PATH` 上另有一个 `protoc`。

### ABI 表面随构建标签集合变化（The ABI surface varies with the build tag set）

`include/registry.go` 对每项能力都是标签极性的（见 `build-pruning-release.md` §2），所以**哪些
libbox 符号存在，取决于标签集合**。这也是为什么有三个没有
`main.go` 的 `cmd/internal` 包 —— `applebuildtags`、`mobilebuildtags` —— 外加 `appletags`，后者是它们之上的
打印器。

**Apple 几何标签**只由一条命令选出：`go run ./cmd/internal/appletags
-mobile-only`（`appletags/main.go:33`），低内存 CI 门禁在它返回空时**直接失败，不回退**
（`verify.yml:380-389`）。文档注释说明了它为什么存在：低内存门禁
与内存基准此前读 `DEFAULT_BUILD_TAGS_OTHERS`，而它*"does not match what
libbox builds"*（中文：与 libbox 构建的内容不匹配），于是那些检查*"were testing a configuration no Apple binary ships"*
（中文：在测试一个没有任何 Apple 二进制会发布的配置）。

**仅移动端（mobile-only）**标签绝不能进共享集合：`-tags` 对每个目标都生效，所以放进去的
移动端专用标签会在混合构建里到达 macOS。`applebuildtags/tags.go:63` 与
`mobilebuildtags/tags.go:42` 都这么说，而该标签通过 gomobile 的 `-tags-not-macos` 传递。

---

## 6. Apple 固定版本 —— 及其缺口（The Apple pin — and the gap）

### 每次构建一个已解析来源，以及独立的 gitlink 检查（One resolved source per build, and a separate gitlink check）

在 `cb71cf80d72` 处，`release/apple-client-refs.env` 默认取 Apple 客户端仓库 `main` 的顶端；
`APPLE_CLIENT_REF` 或 `APPLE_CLIENT_SHA` 可以覆盖它。`client-apple.yml` 在其 `resolve-apple-source` 任务里解析出一个
确切 SHA，再把结果传给下游任务。这是构建侧的选择；父级的 `clients/apple` gitlink 只是本地检出默认值，
不是默认的构建权威。`assert-shared-source` 要求 iOS 与 macOS 一致，会报告 gitlink 不匹配，
并且只在 `APPLE_CLIENT_REQUIRE_GITLINK=1` 时把这个不匹配当成致命错误。每个任务各自
重新解析移动分支，就会破坏这个单一 SHA 边界。

gitlink 对另一项检查仍然有意义：`verify.yml` 运行 `check-libbox-abi.sh --pin-only`，
把两个客户端 gitlink 与脚本里的 `VERIFIED_*_SHA` 常量比较。完整检查声明在
`client-apple.yml` 中，既含调用点扫描，也含 gitlink 检查。gitlink 与常量匹配**并不证明**
独立解析出的 Apple 来源是同一个提交；请检查解析出的构建 SHA 和实际运行，尤其当分支已经移动时。
本主机只跑了 pin-only 检查，没有跑完整的客户端 ABI 扫描或设备构建。

在固定的客户端修订上本地直扫 `probe-accessors.py`，返回 309 个 Kotlin 文件与
423 个 Swift/Objective-C 文件，报告零违规、各三个接口方法。Windows 上的 Apple 检出把受跟踪的
符号链接物化成文本；`SubscriptionQuotaCaption`
的 `SubscriptionInfo.swift` 链接指向一个缺失目标。所以，名义上的 Swift 扫描即使退出码为零，
也绝不能提升为完整的 Swift 调用点扫描。Android 的结果是一次静态扫描，不是
编译过的客户端、不是 ABI 兼容性证明，也不是设备测试。

v0.1.31 的 [Apple 工作流运行](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40606786576)
解析出 `3aedd7417488`，ABI 步骤报告 **PARTIAL**：绑定检查与 gitlink 检查
通过，Swift 访问器探针却因为引用的源文件缺失而跳过。该步骤
成功退出，并不是客户端调用点扫描通过的证据。签名预检在未签名模式下也被跳过；
这不是一次物理设备验证。

源码选择检查器已经接进 Apple 工作流。独立的
`verify-release-acceptance.sh` 检查器没有被发布路径调用；实际的发布门禁、源码选择历史
与各自不同的证据，见 `ci-release.md` §5。`load_refs` 在读 source 引用文件之前，
会先检查该文件里的各平台声明；把调用方环境当成
该文件的声明，此前曾破坏发布辅助脚本（`ece0fb9d2`）。

### Apple 侧确实已接线的部分（What IS wired for Apple）

打包/安装有效性（`apple-libbox-artifact.sh validate`/`install`、`client-apple.yml:483-486`、
`:845-848`）、签名环境（`check-apple-signing-environment.sh` `:335`、`:502`、`:803`）、
品牌化、macOS 命名与源码选择。**签名断言除非
`APPLE_SIGNING_MODE=development` 否则跳过**，而 `runtime_validation: NOT EXECUTED` 会被无条件写入
（`:588`）。

---

## 7. Android 平台（Android）

`android-core-arm64.yml` 构建 AAR，并在 CI 里断言：来源 `commit=` == `GITHUB_SHA`
（`:275-279`）；`jni/arm64-v8a/libbox.so` 存在、**> 1 MB**，`file` 报告 aarch64（`:292-307`）；
`go version -m` 显示没有 gVisor（读不出来就软失败成一条 NOTE，`:308-321`）；NDK 与 gomobile
固定值**推导得到，绝不复制**（`:167-171`、`:209-242`）；解析出的标签被断言
（`:127-138`）。它由对 `testing` 的 `push` 触发，**还带路径过滤** —— 所以一个新的顶层目录
可能悄悄让 Android ABI 验证失效。

`StringBox` 迁移正是 Android 子模块指向**fork** 而不是上游的原因：
`.gitmodules` 说明 ABI 迁移改了生成的 Java 签名，所以客户端调用
点必须跟着核心一起动，而且*"the upstream repository is not writable from here"*（中文：上游仓库从这里
不可写）。没有它，一次干净的检出会拿旧 ABI 去调重建后的 libbox 并编译失败。
构建通过 `cmd/internal/mobilebuildtags` 传入 `-checklinkname=0`（见
`build-pruning-release.md` §2）。

## 8. 历史对比中的两处接口变更（Two interface changes in the historical comparison）

| 接口 | 在 `e4b7e692` | 在 `a267e99` | 提交 | 原因 |
|---|---|---|---|---|
| `DeprecatedNote.Message()` / `MessageWithLink()` | `string` | `*StringBox` | `eef6bafff` | 一个裸 `string` 会把 Go 指针放进 gomobile 打包结果帧 → `fatal error: bulkBarrierPreWrite: unaligned arguments`，不可恢复。记录在 `docs/fork/libbox-abi-contract.tsv`。 |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | 服务器域名这一事实必须到达 adapter |

**不要假设上游的 libbox 能即插即用。** 这张表对后续
修订并不穷尽：`UseProcFS` 后来从 `PlatformInterface` 里移除，同时保留了一个独立的 Android
助手。部分出站能力是可选接口（`ConnectionCopyTuner`、
`CopyBufferGrowthTuner`、`BypassableOutbound`）；这并不确立每个 API 的兼容性。

---

## 9. 核心保证 / 客户端义务（Core guarantees / client obligations）

```text
Core guarantees
  - The core classifies platform levels and owns all lifecycle policy. A client reports the
    number its OS gave it; the core decides what it means.
  - A scope-owned resource is torn down by the Scope; a cleanup registered after close runs on
    the registering goroutine.
  - `APIVersion` (currently 4) is served on GetVersion and must be checked by the client.
  - Trim (progressive) never resets the network, dials, wakes suspended work or kills active
    flows. Release (critical) does reset it.
  - A reuse verdict is published before the device level is released.

Client must provide
  - SetupOptions: BasePath, WorkingPath, TempPath, CommandServerListenPort,
    CommandServerSecret, AppVersion, and the platform flags it actually supports.
  - Platform facts, at the granularity the OS gives them: memory trim level, screen on/off,
    lock state, device wake, network change.
  - A pause/resume edge when it observes one — never a level read presented as an edge.

Client must NOT reinterpret
  - A raw trim level's meaning: do not send "please trim"; send the level.
  - A display-on as an unlock.
  - A snapshot read as an edge (`ReconcileScreenState` is not `RecordScreenState`).
  - `libbox.DeprecatedNote.Message()` as a `string`: it returns `*StringBox` so gomobile's
    packed result frame stays aligned.
  - The Apple gitlink as the source used by every build: the workflow resolves and records
    its Apple source separately; the gitlink is also checked against the ABI contract constant.
  - The build-tag set as fixed: which libbox symbols exist depends on it.

Version / ABI coordination
  - `APIVersion = 4` (daemon/started_service.go:39), served by StartedService.GetVersion and
    read by command_client.go:760. A client that does not check it can call a surface that
    does not exist.
  - The Apple geometry tag is read from ONE source: `go run ./cmd/internal/appletags
    -mobile-only`. It must never be hardcoded.
  - The tag-polar registry means the ABI surface is a function of the tag set; a client built
    against one profile is not interchangeable with another.

External repositories
  - clients/apple   Piggy-Cat-bit-shadow/sing-box-for-apple   (build source is resolved separately)
  - clients/android Piggy-Cat-bit-shadow/sing-box-for-android (fork-located because the ABI
                    migration moves the generated Java signatures)
  - clients/desktop Piggy-Cat-bit-shadow/sing-box-for-desktop       (the fork's)
  Checkout status varies; see the scope note and record the actual submodule revision.
```

---

## 10. 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| 导出的表面与 `SetupOptions` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./experimental/libbox/...`（实测：ok，20.3 s） |
| 核心拥有 trim 分类与合并 | VERIFIED_SOURCE | `common/runtimecoord/platform_events.go:152-231` |
| trim 阶梯完整且未知级别被忽略 | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` |
| `APIVersion = 4` 由提供方暴露并由调用方使用 | VERIFIED_SOURCE | `git grep -n APIVersion` |
| Apple 客户端对 iOS 与 macOS 是同一来源 | 源码与本地 fixture 检查；在本次校准中，该选择脚本在 MSYS2 下用随附的 Python 通过了 72 项检查 | `bash scripts/ci/check-apple-source-selection.sh`；检查实际工作流运行 |
| 客户端 gitlink 与 ABI 契约常量匹配 | 本地 pin-only 脚本通过；Verify 声明同一门禁。这并不证明解析出的 Apple 构建 SHA 等于该 gitlink | `./scripts/ci/check-libbox-abi.sh --pin-only`；检查 Apple 工作流解析出的源码 |
| refs 文件不能静默恢复双来源固定 | VERIFIED_SOURCE + VERIFIED_UNIT | `sed -n '88,105p' scripts/ci/apple-client-source.sh`；断言它 grep 的是**文件**而非环境 —— `ece0fb9d2` |
| 固定修订处的 ABI 调用点兼容性 | Android 静态探针本地通过；Swift 名义扫描因符号链接物化/缺失目标而不完整，且确切目标的 CI ABI 结果为 PARTIAL | 对每个客户端运行 `probe-accessors.py`；完整门禁需要 `gobind` 与有效检出 |
| Android AAR 来源与架构 | **UNVERIFIED（仅声明）** | `android-core-arm64.yml` |
| 任一平台上的设备行为 | **UNVERIFIED** —— 无设备、无 SDK、无 Xcode | — |

---

## 11. AI 注意事项（AI cautions）

1. **不要去找 `libbox.go`。** 这个表面是 110 个 `.go` 文件（90 个非测试）。
2. **不要把级别分类移进客户端。** 核心拥有 trim 阶梯、合并
   窗口以及边沿/级别区分。
3. **不要把未知的 trim 级别默认为渐进动作。** 未知即忽略。
4. **不要把 `RecordScreenState` 与 `ReconcileScreenState` 合并。** 一个是边沿，一个是级别，
   合并它们会重置生命周期锚点。
5. **不要把显示器点亮当作解锁。**
6. **不要把 `DeprecatedNote.Message()` 改回 `string`。** 它返回 `*StringBox`，好让
   gomobile 打包结果帧保持对齐；另一种选择是不可恢复的致命错误。
7. **不要认为上游的 libbox 在这里可以即插即用替换。**
8. **不要硬编码 Apple 几何标签。** 从 `cmd/internal/appletags -mobile-only` 读取它。
9. **不要把仅移动端标签放进共享 Apple 集合。** 在混合构建中 `-tags` 会到达 macOS。
10. **不要把 Apple 来源选择与 gitlink ABI 检查混淆。** 工作流每次构建冻结
    一次解析出的来源；独立的 gitlink/常量检查并不选择该来源。
11. **不要声称 Apple ABI 在固定修订处已验证。** 它并没有。
12. **不要把 ABI 门禁说成未接线。** Verify 声明了它的 pin-only 一半，而 Apple 工作流
    声明了完整检查；在声称覆盖前请检查实际运行与解析出的来源。
13. **不要假设 `make proto` 可用。** `protogen` 需要一个 `Makefile` 从未安装的 `protoc`。
14. **把客户端侧主张限定到已检出的客户端 SHA 与实际检查过的文件。** 仅凭
    父项目 gitlink 并不证明客户端构建或设备行为。
15. **不要忘记 `include/*` 零测试文件。** 注册表与墓志铭（tombstones）是
    无守卫的；那里的改动只能被一次构建捕获。
16. **如果你移动了 Apple 固定值，常量必须随之移动。** `clients/apple` 的 gitlink 是
    记录的检出，而 `scripts/ci/check-libbox-abi.sh` 中的 `VERIFIED_APPLE_SHA` 是交叉校验。
    `verify.yml` 现在运行 `--pin-only` 并在两者分歧时失败；`client-apple.yml` 运行完整
    门禁。该门禁告诉你它们不一致，而不告诉你意图是哪个修订：请重新推导
    依据，而不是继承上一份，并对新修订运行第 4 层
    （`probe-accessors.py`，或在 macOS 主机上运行 `check-libbox-abi.sh --client-root <checkout>`）。
    v0.1.28 的移动就是现成例子：`6e559a4` -> `3aedd741` 改变了配额标题、配置
    标题与路径监视器，没有 ABI 调用点，而常量不得不被重新推导以与之匹配。
