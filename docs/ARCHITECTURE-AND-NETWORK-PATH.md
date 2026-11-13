# 架构与网络链路优化

本文从协议与共享核心的协作关系说明 JiejieBox 的改造。旧版机制参照 [2026-11-05 增量校准](../KNOWLEDGE/CALIBRATION-2026-11-05.md)，0.1.45 范围结合当前源码与[协议模块](../KNOWLEDGE/modules/protocols-0145.md)、[规则与策略组模块](../KNOWLEDGE/modules/rules-and-groups-0145.md)阅读；性能数据均指向对应报告，不以本页代替测试记录。

[返回首页](../README.md) · [架构边界](../KNOWLEDGE/ARCHITECTURE.md) · [性能对照](PERFORMANCE-COMPARISONS.md)

## 1. 共享核心如何组织协议

一条代理流涉及目标识别、解析、规则、组选、拨号、握手、数据传输和资源释放。不同协议在这些阶段需要的公共条件相近，但会话和传输语义不同。项目通过共享接口传递条件，由协议在实际使用位置执行。

| 层次 | 公共职责 | 协议适配责任 |
| --- | --- | --- |
| 目标与解析 | DNS 查询策略、FakeIP、地址族能力、候选、缓存与网络归属 | 明确代理服务器和最终目标分别由谁解析；MASQUE 保留 Bootstrap/Inner DNS 区分 |
| 路由与路径 | 规则匹配、代理组、detour、依赖排序及只读能力检查 | 声明 TCP/UDP 等能力，传递完整候选和每流选项，返回可靠的失败信息 |
| socket 与握手 | 接口绑定、mark、namespace、取消、超时、TLS 和 NE 准入 | 将上下文传到 SDK、原生回调或具体连接创建点，不在包装层丢失约束 |
| 数据传输 | 缓冲区几何、可选调度、统计、半关闭和复制能力 | 维护协议自己的帧边界、加密状态、逻辑流顺序和所有权交接 |
| 生命周期 | Scope、网络 generation、恢复租约、空闲退役、电源与内存事件 | 以能力接口暴露清理、停止接新流或重建，保留本协议会话语义 |

`common/httpclient.Manager` 管理可共享的 HTTP 客户端和传输资源；`route.ReferenceManager` 发现被引用资源并按能力执行维护；`common/runtimecoord` 管理网络代次和恢复租约。三者的作用范围分别是 HTTP 资源、跨组件引用与网络状态协调，不合并为同一个连接池。

启动期的 `PhysicalPath` / `ControlPath` 校验区分物理跳点和组选控制，不通过真实解析或拨号证明配置。运行时再依据选中成员的实际能力执行，未知能力保留未知状态。

源码入口：[ReferenceManager](../route/reference.go)、[RuntimeCoordinator](../common/runtimecoord/coordinator.go)、[HTTP Manager](../common/httpclient/manager.go)、[路径选择与校验](../KNOWLEDGE/modules/path-selection-validation.md)。

## 2. 协议如何接入公共机制

### TrustTunnel：H2/H3 外层与逻辑流

TrustTunnel 入站的 `network` 控制 H2/TCP 与 H3/UDP 监听，出站的 `quic` 选择 H3，而其 `network` 控制隧道内 TCP/UDP 能力。认证、CONNECT 与 UDP association 由协议层处理；共享 session 的复用与闲置退役、网络代次切换和连接关闭必须与 socket/TLS 归属一致。H3 依赖 QUIC 的数据报、拥塞控制及资源准入，H2 走 TCP/TLS；两者共享路由语义，但不把外层协议选择误作业务网络限制。

入口：[入站配置](configuration/inbound/trusttunnel.zh.md)、[出站配置](configuration/outbound/trusttunnel.zh.md)、[实现](../protocol/trusttunnel)。

### AmneziaWG：WireGuard 设备上的混淆层

AWG 字段附着在 `wireguard` endpoint 根层，沿用其 peer、路由、按需恢复与设备所有权。握手前 junk、魔数、CPS/声明式伪装、头保护、填充和随机尾部改变报文形态；配置与服务端不一致会在真实互通中暴露。`with_awg` 决定运行能力，缺失时显式拒绝 AWG 配置，避免静默退化为普通 WG。并发发送空间的等待/唤醒需在协议和依赖 wireguard-go 中共同核对。

入口：[AWG 配置字段](configuration/endpoint/wireguard.zh.md#amneziawg-扩展)、[协议性能记录](final-audit/PERFORMANCE.md)、[WG/AWG 并发修复](final-audit/closure/WG-AWG-B7.md)。

### XHTTP：逻辑流、HTTP 连接与 XMUX

VLESS 的传输调用到达 XHTTP 后，XMUX 管理复用配额、连接轮换和 pooled transport，packet-up flusher 管理上传聚合和背压。逻辑流重置、POST 被拒、共享 HTTP 连接损坏属于不同范围的事件，错误应在对应范围内结束或传播。

生命周期调用由 VLESS 转发至 XHTTP，再由 XMUX 执行：`CloseIdleConnections` 清理不承载业务的连接；`RetireSuspect` 停止旧池承接新流，空闲项关闭、活跃项排空。网络变化按协议路径执行更强退役。普通 trim 不应成为重新拨号的触发器。

历史上传重构将零散 Write 汇入有界队列，由单个 flusher 顺序聚合，并为最初两次写入保留低延迟。容量、响应消费、取消及失败后的尾部数据共同决定实现是否正确，不能只修改 POST 间隔。

入口：[XHTTP client](../transport/v2rayxhttp/client.go)、[XMUX](../transport/v2rayxhttp/xmux.go)、[模块与配置](../KNOWLEDGE/modules/xhttp-client-transport.md)。

### Mieru：外部 SDK 与内核拨号、路由和恢复

协议握手与多路复用由官方 SDK 提供，listener/dialer 桥接将真实 socket 交给 JiejieBox 的配置策略创建。SDK 的预解析要求和主内核的实际解析职责需要同时满足，不能在集成时提前固定一个 IP 而丢失候选竞争。

适配层在自己拥有的 socket 边界保留类型化错误，再生成 `FailureFacts`。这使代理组能够区分目标拒绝、明确代理连接失败、本地接口问题和用户取消；含义不明的 EOF 不转成确定的节点故障，第二次模糊失败也不能覆盖第一次已有的可靠事实。

主备 TCP/UDP 分别持有 SDK Runtime、配置与会话，选择器只管理偏好、冷却和按需探测。网络变化时先构建可用的新运行时再交接代次，旧结果不能翻转新网络偏好；单纯传输偏好变化不迁移已有会话。

入口：[Mieru 模块](../KNOWLEDGE/modules/mieru.md)、[故障分类与回退](../protocol/mieru/fallback.go)、[集成与历史测量](fork/mieru-integration.md)。

### Naive：原生引擎与 Go 核心

Cronet host dialer 与 DNS 桥把原生连接创建接回 Go 侧服务器解析和网络策略；原生 Engine 的池化、回调和缓冲区仍受自己的 ABI 与生命周期约束。Linux socketpair 回复修复说明，公共 DNS 策略还依赖跨语言边界真正能够交付回答，配置有 resolver 并不自动代表路径闭合。

出站等待 CONNECT 结果，认证失败与可归因的网络错误进入上层失败判断。H3→H2 重试与共享 HTTP 管理使用不同的故障状态，保留尝试次序和预算。服务端则将认证身份、UoT 目的语义、padding 与路由检查衔接；原地构帧必须满足前后预留容量。

入口：[Naive 出站](../protocol/naive/outbound.go)、[模块](../KNOWLEDGE/modules/naive-http-transport.md)、[Cronet 修复与证据](../KNOWLEDGE/reports/release/cronet-patch5-linux-dns-2026-11-05.md)。

### MASQUE：外层连接、隧道状态与数据报

Bootstrap DNS 解析并建立代理端连接，Inner DNS 处理隧道内目标；DNS_ASSIGN/PREF64 和地址路由通告更新 endpoint 状态。H3 连接、CONNECT-IP 请求与内层 IP 数据面分别拥有超时和错误语义，已完成 QUIC 握手后的慢响应不直接证明 H3 线路不可用。

CONNECT-UDP 与 CONNECT-IP 在支持的边界使用 owned datagram，发送成功后由下游持有缓冲区；批量部分成功、能力拒绝、过大包和 capsule 回退分别结算尚未转移的部分。内层 MTU、外层 QUIC 包约束和实际链路 MTU 分别传递，过大 IP 包按条件生成 PTB。

入口：[MASQUE 模块](../KNOWLEDGE/modules/masque-connectip.md)、[缓冲区所有权](../KNOWLEDGE/modules/buffer-ownership.md)、[H3 慢响应修复](fork/mieru-masque-final-acceptance.md)。

### TLS、AnyTLS、Vision 与其他数据面

- **TLS / AnyTLS**：共享 TLS 入口消费流量类别与创建许可，AnyTLS 入口传播同一许可，避免多跳调用重复排队。AnyTLS 的物理 session、逻辑流及帧写序列仍由协议管理。
- **REALITY / Vision**：握手配置克隆保留 Verifier、密钥份额与分片设置；Vision 专用数据路径保留所需连接能力并检查依赖布局，通用包装不能静默替代其契约。
- **Hysteria2 / TUIC**：sing-quic 与 quic-go 承担数据报、队列和包大小约束。下层容量已知时传入独立硬上限；ChromeParrot 覆盖初始偏好后仍应用约束，不能用初始大小代表整个会话的包上限。
- **SOCKS / HTTP CONNECT**：保留服务器和目标解析归属、认证与缓存首包，预连接限定于支持的 TCP CONNECT 形态，数据包关联继续使用自己的生命周期。
- **ShadowTLS / Shadowsocks / Snell**：认证前超时、握手目标清理、加密帧容量和复制路径分别处理；必要复制根据所有权保留。
- **WireGuard 与 endpoint**：平台暂停事实到达实际 bind，网络重建和关闭结清设备资源；endpoint 本地流量经 HostPort/TUN 按模式与地址族裁决。

相关契约：[资源准入](../KNOWLEDGE/modules/resource-admission.md)、[TLS 引擎与依赖](../KNOWLEDGE/modules/build-pruning-release.md)、[MTU](../KNOWLEDGE/modules/mtu-and-geometry.md)、[生命周期](../KNOWLEDGE/modules/core-lifecycle-runtime.md)。

## 3. 从入口到回包的优化

### 入口、首包与解析

普通 TUN、Linux auto_redirect 与系统代理有不同入口。TUN 的 DNS 劫持需要先到达 DNS 路由器，FakeIP 需要恢复域名归属，流量预判才能保留后续完整选路的条件；Mixed 则以同一读取器处理 HTTP/SOCKS 识别、认证和缓存首包。

嗅探按需读取并保留重放数据：HTTP/TLS 尽早排除无关输入，TLS 只提取 ClientHello，QUIC 分片限制读取轮数和暂存范围。跨异步边界传递解析后的 DNS 消息，跨分片边界保留独立工作数据，避免引用已被收包循环复用的内存。

DNS 协调相同查询、缓存、后台刷新与地址族增量结果。每条流捕获自己的有效策略；A/AAAA 先完成的候选进入连接竞争，迟到候选仍遵守原网络归属。共享缓存保留可供不同消费者过滤的结果，不让一个调用者的单族选择污染其他调用者。

### 组选、拨号与多层隧道

规则集使用紧凑 matcher、受限扩容和流式下载解析。物理路径证明、组选预览和正式选择分别执行，成员候选列表贯穿组边界。detour 的服务器解析与最终目标解析区分归属，取消、超时和重试预算沿同一条流传递。

`resolve.match_only` 只为后续规则发布解析结果，不改写最终目标的拨号语义；策略组的 `hidden` 仅影响客户端展示。URLTest 显式配置 `failover` 后，在稳定状态缩窄探测范围、退化时扩展并确认恢复；loadbalance 的同名布尔字段则控制拨号失败后的备用成员重试，两个字段不共享含义。配置与组行为详见[扩展配置](FORK-CONFIGURATION.md#规则匹配与策略组显示探测)。

`common/dialer` 将接口、mark、namespace 与候选策略落实到 socket。快失败推进其他候选；落败或在取消后完成的连接负责清理。协议返回阶段化失败事实，决定是否回退或惩罚成员。外层隧道容量已知时再约束内层 QUIC；未知容量保持未知，不以猜测替代 PMTU。

### 稳态传输、回包与关闭

上传聚合、缓冲区帧头预留和 owned datagram 分别减少固定调用成本和载荷复制；它们仍保留帧顺序、背压与错误传播。上传调度在配置速率后持续整形，避免批量流量先占满共享队列再试图提高交互优先级。

TUN 回包复用单写回切片，QUIC 接收槽位及时清空，限制小载荷对大缓冲区的保留。TCP 按能力执行缓存首包交接或 splice，建立失败回到普通复制。TLS 收到尾部应用数据与控制错误时先交付有效数据，随后报告错误；接管路径使用适用的存活机制沿原清理链退出。

## 4. 快速路径与恢复的执行范围

Direct Offload 先证明当前流的路由和 socket 语义可重现，再由具备消费能力的入口执行。Linux auto_redirect/nfqueue 可以将适用流交还系统路由；普通 TUN 与系统代理没有因此获得同一条隐式旁路。连接建立后的 splice 是另一层 I/O 能力判断。

构造阶段对 DialerOptions 分类，逐流再结合地址族、FakeIP、目标改写、跟踪和显式组策略判断；新字段未分类时保持保守。这样既减少逐包配置检查，也避免接口、namespace 或观测要求在优化中丢失。

恢复同样按原因分层：网络变化推进代次并隔离旧结果；唤醒后可疑池停止接新流；普通 trim 清空闲项；严重压力再交给协调执行器执行更强回收。旧租约不能释放新租约，普通内存整理不推进网络代次，压力采样也不重复创造休眠事件。

## 5. 资料与验证范围

- [整体工程评估](../KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)：架构演变、协议协作、性能与历史审计。
- [七个数据面对象的横向审计](../KNOWLEDGE/reports/core/SIX_PROTOCOL_UNIFICATION_EFFICIENCY_AUDIT_2026-10-20.md)：公共接口成本、可复用机制和未采纳方案。
- [双栈跨组件收尾](../KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_CLOSURE_2026-10-20.md)：地址族、DNS、候选、代理组与网络交接。
- [真实代理链调试](fork/traffic-path-chain-debug-report.md)：配置解码、目标归属、实际对端与载荷交付。
- [Fast Path 与内存管理](FAST-PATH-AND-MEMORY.md)：旁路执行范围、分层回收与状态机。
- [性能对照](PERFORMANCE-COMPARISONS.md)：测量日期、载荷、参考实现与正负结果。

上述报告保留各自历史版本。接口接入、单元测试、对端互通和设备行为分别构成证据，不能由某一项通过推导所有协议在全部平台获得相同收益。
