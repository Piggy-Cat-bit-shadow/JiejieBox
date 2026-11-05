发布日期：2026-11-05。比较基线：[v0.1.37 → v0.1.39](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/compare/v0.1.37...v0.1.39)。发布源码：`fc7e400243d46a451b2dffbe68cb5a3147c66289`。

**这是一轮横跨共享内核、依赖 fork、Apple 客户端、资源治理和发布链的集中更新。** 对用户而言，最直接的变化是：正式附件加入 iOS 自签 IPA，配套 iOS/iPadOS 客户端提供本地签名回环；内存紧张时开始在昂贵握手创建前控制并发；Naive/Cronet 吸收上游 patch5 相关安全及 QUIC 修复，并修复 Linux DNS 桥导致的自定义拨号超时。

这一轮并不止于几个版本号的替换。与上一正式版 v0.1.37 相比，Git 区间包含 **157 个提交、108 个文件、10,574 行新增与 421 行删除**，涉及实现、边界测试、故障复现、平台修复、源码锁定和产物核验。数字描述实际变更规模，不把合并、测试或文档提交算作独立功能，更不把上游继承工作全部算作原创。

## 一、iOS 自签 IPA 正式进入发布附件

v0.1.39 发布 `jiejie-ios-selfsign-unsigned-0.1.39.ipa`，让有自己签名方式的用户可以直接获取完整客户端包，不必先搭建 TestFlight 上传链。包本身未签名，需要自己的证书、描述文件及工具完成签名。

围绕自签，项目处理了从“安装得上”到“能启动、隧道可接管、状态可观察”的整条链路：

- **启动不再盲目信任 App Group。** 共享容器不可用时提供退化路径及诊断，避免强制解包直接闪退。
- **实际授权与构建声明分开。** 重签后不只使用编译进 plist 的旧组名，还发现签名配置中可用的 App Group，并用系统容器访问确认；减少主应用与扩展分别落入私有目录、隧道工作但日志统计不可达的问题。
- **适应重签后的扩展身份。** 根据包内实际的 Packet Tunnel 扩展发现 provider，读取注入的团队身份，而不是假定打包时与安装后标识永远相同。
- **“已连接却未路由”变得可诊断。** 对 TUN 未完成网络设置的情况给出明确错误，避免只依赖 VPN 图标判断成功。
- **版本与来源可追溯。** 主应用、扩展、核心提交、Apple 客户端提交和 Libbox 来源由 manifest 记录，检查的不只是文件名上的版本。

这些修复覆盖多轮真实故障反馈、源码核对、Darwin bind 与构建验证。它们提高重签兼容性，但不会自动授予证书没有的 Network Extension 权限，也不宣称所有签名服务、设备和系统版本均已通过真机验收。

操作方法及排障见 [iOS 自签与回环指南](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/IOS-SELFSIGN-AND-LOOPBACK.md)。

## 二、本地签名回环：把常用需求做成可直接开启的功能

配套 iPhone / iPad 客户端新增 **本地签名回环（Local Signing Loopback）**，面向 SideStore、LiveContainer 等本机连接需求。

**打开方式：更多 → 连接行为 → 配置覆盖 → 本地签名回环。** iPad 在配置覆盖设置中查找同名开关。默认关闭；打开后服务重载，把 `10.7.0.1` 加入本次运行的 TUN，并为同一地址安装相应 iOS 主机路由。

这项功能同时打通了：客户端偏好保存 → 启动选项 → libbox 覆盖参数 → TUN 地址合并 → 栈的数据面反射 → NE 主机路由。只增加一个开关或只加一条路由，都不足以形成可用功能。

- 运行时覆盖保留用户已有地址和顺序，规范化 IPv4-mapped / zone 表达并去重。
- 不改写订阅与原始配置；关闭后下一次运行不再注入地址。
- 默认 `go` 栈支持 TCP、UDP、ICMPv4/v6 的本地反射。
- 路由注入限定 iOS，macOS 等平台不因新增开关获得无关行为改变。
- 显式选择不具备对应反射实现的栈时，核心给出警告，不让开关静默表现为有效。

**适用范围：完整反射当前覆盖默认 `go` 栈；特定签名工具的刷新与设备路由仍需在自己的环境验证。** 详情见 [使用指南](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/IOS-SELFSIGN-AND-LOOPBACK.md) 和 [专项审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/LOCAL_SIGNING_LOOPBACK_DEBUG_REPORT.md)。

## 三、资源治理：从事后回收扩展到高成本创建前准入

本轮针对 iOS Network Extension 的小内存预算，引入 `common/resourceguard`。已有 OOMKiller 继续负责内存压力与分级回收，新的准入闸门控制昂贵连接创建，避免突发流量同时启动过多握手。

### 共享入口，而非逐协议复制限流

AnyTLS 的 TCP 与 UoT 拨号，以及共享 TLS dialer 的握手入口接入同一进程级策略。后者覆盖实际通过该 dialer 建连的 TLS 出站、HTTP 传输和相关 DNS 传输；并非给每个 QUIC stream 或每条已有连接都套一个全局队列。

正常态默认并发额度 32、队列 256、等待预算 5 秒，交互流量有 4 个额外预留额度；进入 Armed / Critical 后逐级收紧。guard 复用现有压力来源，不增加第二个 footprint 采样器、常驻 goroutine 或定时器。

### 让流量类别真正影响创建机会

`traffic_class` 从路由层沿拨号 Context 传递至 AnyTLS 与 TLS 准入入口。交互与实时流量可以使用预留额度，普通和批量流量保持各自边界；加入等待老化与取消处理，避免只做名义上的优先级。

### 修复多跳与并发中才出现的错误

- **一条逻辑拨号只领取一次许可。** TLS → detour → TLS、AnyTLS → TLS 和再次进入 AnyTLS 的路径都传播已持许可标记，避免嵌套拨号排队等待自己持有的资源。
- **陈旧状态不能覆盖新状态。** 压力观察与计划发布带版本校验，旧 Armed 不会重新放开已经 Critical 的闸门，旧 Critical 也不会在压力下降后重复触发回收。
- **保持回收责任明确。** 准入层轻量关闭空闲会话，破坏性的重回收仍由已有 OOMKiller 执行器协调。
- **账本与诊断可检查。** OOM 元数据增加在途、排队、获准、拒绝及取消等计数，保留既有诊断字段。
- **异常路径有回归。** 覆盖取消风暴、慢握手、重复压力周期、交互预留、不同并发规模及关闭后的资源归还；依赖侧补充 UDP NAT 200/2000 流压力测试。

默认生产 guard 仅安装在 NE 模式，其他平台不因此无故被套用移动端配额。该机制约束创建阶段，不是进程内存绝不超预算或 iOS 永不 Jetsam 的保证。AnyTLS 在确认空闲会话复用前可能先经过准入，仍作为已知边界保留。

设计、修复演进及复查入口见 [资源准入模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/resource-admission.md)。

## 四、NaiveProxy / Cronet：保留自定义能力，补齐上游修复

**Chromium 仍为 154.0.8037.49。** 本轮吸收 NaiveProxy v154.0.8037.49-5 中与 Cronet 集成相关的修复，不把版本号相同误当作补丁集相同，也没有强行迁移到 Chromium 157。

- **证书封锁更新。** 证书 blocklist 补齐上游新增 8 个 SPKI 哈希；源码列表与目标上游版本一致。
- **QUIC 接收窗口与调节修复。** 上限调整为流级 64 MiB、连接级 128 MiB，吸收 `EnsureWindowAtLeast` 和自动调节相关修复；保留既有可配置窗口与 `std::max` 边界语义。
- **CONNECT 行为按实际调用链判断。** 对上游强制隧道补丁做适用性核对，保留本项目的 HTTP CONNECT / 自定义拨号路径；没有为追求提交一致而覆盖 Socket Pool 自定义实现。
- **五目标原生库重建。** linux/amd64、windows/amd64、android/arm64、darwin/arm64、ios/arm64 以同一原生源码提交构建，与 Go 包装修复成套发布。
- **31 个模块路径成套接入。** root、29 个 lib 模块和 all 聚合模块保留 fork replace，依赖记录、子测试模块及 provenance 同步核对。

### Linux 自定义拨号超时：先复现，再修复，再消费

Linux 默认 DNS 桥使用匿名 AF_UNIX/SOCK_DGRAM socketpair，收到请求时没有可供 `WriteTo` 使用的对端地址。旧回复包装实际调用 `WriteTo(nil)`，写入失败后 DNS 回答丢失。后续集成表现为代理端口被拨向公网地址，第一次写入约 274 秒超时。

修复按**是否有对端地址**选择路径：有地址继续绑定 `WriteTo`，无地址由已连接端点 `Write` 回复；失败计入计数器，保持每查询不刷日志。没有通过强改 IP、延长 timeout 或跳过测试掩盖问题。

旧代码的真实 Linux socketpair 测试先失败，修复后同场景恢复；`TestNaiveCustomDialer` 约 **0.79 秒通过**，目标为预期 `127.0.0.1:10443`。Linux purego 与 cgo 同场景均通过，Windows 相关全量套件 34 项通过。这里是修复故障，不是把所有连接宣称提速数百倍；完整 DNS fallback 内部链路没有单独 NetLog 闭环。

复现、原生重建、发布与消费证据见 [Cronet 专项记录](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/release/cronet-patch5-linux-dns-2026-11-05.md)。

## 五、DNS、设备变化与 endpoint 路径继续收敛

- **DNS 冷连接不阻塞调用者的收包路径。** 连接建立移到异步调度，避免 TCP/TLS 建连等待占住 TUN packet processing；补充取消与后台任务退出测试。
- **打包消息前深复制。** 多条 DNS 传输不再通过浅复制共享可变消息，降低并发打包影响调用方对象的风险。
- **设备地址改变时中断旧 UDP socket。** 地址与 interrupt group 同步切换，避免新地址配上旧套接字。
- **补齐 OpenVPN / OpenConnect 的 endpoint 本地流量裁决。** 与其他 endpoint 的 HostPort / TUN 处理方式对齐，保留普通配置的默认行为。
- **选择性吸收上游 TUN 与 OpenConnect 修复。** 包括严格路由与内核 IPv6 支持、Windows RAS 默认路由、CSD/F5/GPST 等兼容性变更；保留 fork 的路由端口和其他增强。
- **内存诊断处理 runtime 发布滞后。** 不再将短暂的指标发布差异错误理解成计数器不可读。

上游分支有历史重写，本轮按补丁语义和当前源码核对，不用“落后多少个 SHA”直接判断未吸收工作量。Android auto-redirect 的完整客户端联动仍有边界，不把只存在上游的能力写成这版已发布功能。

## 六、Apple 构建与发布：让产物证明自己来自哪里

- 将同提交 Libbox 作为共享生产物，在可复用时供 iOS、macOS 与自签消费者使用，避免各入口无条件重复编译。
- 复用前校验父 SHA、版本与库哈希；没有合格产物时的构建路径明确记录来源。
- 同一流程固定 Apple 客户端来源，修复 gitlink 与工作流常量漂移，避免编译另一个分支后只在记录里写当前 pin。
- 无签名包也注入实际产品版本，修复包名与 App/扩展内部版本不一致的问题。
- 正式 Release 增加自签 IPA 附件契约，Windows 安装包继续是必需产物。
- `source-lock` 比较实际 pseudo-version，不只比较末尾提交短 SHA；扁平化上传列表按统一附件契约判断，避免维护第二份过期文件清单。
- 从多个临时构建与验收分支收敛回产品主线，保留依赖 fork 与可回退的发布版本。

## 七、升级后继续拥有的协议与安全能力

这版承接此前的 XHTTP 四模式、Mieru TCP/UDP、VLESS 客户端加密、REALITY 握手扩展、Native Naive、MASQUE，以及双栈候选、代理链、DNS 缓存和上传调度。它们是本项目持续演进的成果，**并非全部首次出现在 v0.1.39**。

历史数据能看见这些工程的实际效果：XHTTP packet-up 受控上传从 2.12 到 26.8 MB/s，同场 Xray 参考为 22.9 MB/s；Mieru 低复用 TCP 下载在官方同版本对照中 +10.2%，但对应上传 −6.1%；百万域名加载累计分配从 42.5 到 20.6 MiB。完整条件、负面结果和实现对照见 [性能专页](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/PERFORMANCE-COMPARISONS.md)。这些不是本版全矩阵重测成绩。

**默认避免意外启用 MPTCP** 也是继续保留的安全特性：普通入站与 API／调试监听显式选择普通 TCP，减少 Go 默认设置带来的 MPTCP 暴露。该能力自 v0.1.37 已交付，用于缓解 CVE-2026-98432 已公开利用路径的攻击面；系统仍需更新内核，显式 `tcp_multi_path: true` 仍为主动启用。参见 [安全说明及一手来源](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/SECURITY-MPTCP.md)。

## 八、下载选择、兼容性与验收范围

v0.1.39 正式附件共 **8 项**：

| 使用方式 | 选择的附件 |
| --- | --- |
| Windows x64 桌面客户端 | `Jiejiebox-v0.1.39-windows-x64.exe` |
| Windows / Linux / macOS 命令行 | 对应 `windows-amd64`、`linux-amd64`、`macos-arm64` 核心压缩包 |
| Android 客户端开发集成 | `android-arm64` 压缩包中的 AAR；不是 APK |
| Apple 客户端开发集成 | `jiejie-libbox-apple-0.1.39.tar.gz` |
| iOS / iPadOS 自签安装 | `jiejie-ios-selfsign-unsigned-0.1.39.ipa` |
| 文件完整性核对 | `SHA256SUMS` |

macOS GUI 和 DMG 构建链存在，但 **DMG 不在本次正式附件内**。安装器的 CI 测试签名也不等于受操作系统普遍信任的商业签名，下载与安装仍按系统提示处理。

Naive 窗口、QUIC 与 UDP-over-TCP 既有配置语义保留；新增能力按对应配置或开关启用。Linux 默认服务端档不含 Cronet 出站，包含 Naive 入站；不要把 Linux 依赖测试通过等同于默认产品档已启用该客户端功能。Apple darwin_amd64 与两个模拟器原生分片本轮未重建，provenance 保留其实际字节；29 个模块可解析不表示 29 个架构都重建。

最终标签提交的执行证据包括：[Verify](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42007622263)、[发布契约](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42007622392)、[Apple](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42007676300)、[自签包](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42007682596)、[Windows 安装包](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42014374567)、[Windows runtime audit](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42015405748) 和 [正式发布](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42017850417)。这些运行均为对应 SHA 的 CI 记录，不能外推为任意真机环境已通过。

[下载本版](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.39) · [完整版本历史](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [首页与功能导航](https://github.com/Piggy-Cat-bit-shadow/JiejieBox)
