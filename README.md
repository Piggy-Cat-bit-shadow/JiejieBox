# JiejieBox

基于 [sing-box](https://github.com/SagerNet/sing-box) 持续开发的跨平台代理内核，扩展 XHTTP、Mieru、VLESS 加密与 Native Naive，优化 DNS、双栈拨号、链式代理、网络恢复及资源管理，并提供配套桌面端和移动端集成。

[下载](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [更新日志](CHANGELOG.md) · [Android 客户端](https://github.com/Piggy-Cat-bit-shadow/satelite-one) · [配置文档](docs/configuration) · [问题反馈](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/issues)

## 客户端与命令行内核

| 平台 | 下载与使用 |
| --- | --- |
| **Windows x64** | 桌面安装包、独立命令行内核 |
| **Android arm64** | [satelite-one 客户端](https://github.com/Piggy-Cat-bit-shadow/satelite-one)；本仓附件为开发集成用 AAR |
| **Linux amd64** | 命令行内核 · [服务端部署](docs/JIEJIE-SERVER.md) |
| **macOS arm64** | 命令行内核 · [运行说明](docs/JIEJIE-MACOS-CLIENT.md) |
| **iOS / iPadOS arm64** | 未签名 IPA，支持自行签名安装 · [安装指南](docs/IOS-SELFSIGN-AND-LOOPBACK.md) |
| **Apple 开发集成** | Libbox |

附件与校验和见 [Releases](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases)。**TestFlight / Apple GUI 构建**：Windows 发起 Actions，Mac 获取同次 Libbox 后完成签名、归档与上传；支持 iOS、macOS 或同时构建，见 [构建步骤与签名配置](docs/APPLE-BETA-RELEASE.md)。

## 配置与使用

[常规配置](docs/configuration) · [本项目扩展配置与示例](docs/FORK-CONFIGURATION.md) · [Naive](docs/naive.md) · [MASQUE](docs/masque.md) · [DNS 广告拦截](KNOWLEDGE/modules/dns-adblock-action.md) · [本地回环](#ios-本地回环)

常规配置参考已纳入的 sing-box **v1.15.0-alpha.13**；XHTTP、Mieru、负载均衡、上传调度等扩展按本项目文档配置。协议可用性取决于平台与[构建档位](docs/BUILD-PROFILES.md)。当前 Naive 引擎为 **Chromium 154.0.8037.49 + NaiveProxy patch5 相关修复**，Mieru SDK 为 **v3.39.0**。

下文结合具体实现与历史对照介绍功能；性能数字保留测试场景，完整方法与原始记录见 [性能专页](docs/PERFORMANCE-COMPARISONS.md)。

## 协议与网络能力

### XHTTP

- 支持 `packet-up`、`stream-up`、`stream-one`、`auto` 四种客户端模式，覆盖 HTTP/1.1、HTTP/2 和 HTTP/3。
- 支持 X-Padding、上传分块与 XMUX 连接复用，传递拨号取消和 deadline。
- packet-up 使用有界队列与单一 flusher 聚合写入，保留首包低延迟、顺序、背压及尾部数据完整性。
- 连接池接入网络切换、休眠恢复和空闲清理，处理上传拒绝、H3 故障与关闭边界。

VLESS/XHTTP 的旧连接池退役后停止承接新流，已有流排空后释放；单个逻辑流重置不会被直接当作整条共享连接损坏。

历史 TLS/H2 受控测试，服务端为 Xray v26.9.30：

| 指标 | 早期实现 | 优化后 JiejieBox | Xray 参考客户端 |
| --- | ---: | ---: | ---: |
| packet-up 单流上传 | 2.12 MB/s | **26.8 MB/s** | 22.9 MB/s |
| 建连 p99 | 31.7 ms | **约 4.3 ms** | 约 4.35 ms |

该组上传约为早期实现的 12.6 倍；另一轮 RTT 扫描中，100 ms 下双方为 7.88 / 7.80 MB/s，200 ms 下为 3.92 / 4.04 MB/s。完整条件、后续混合负载与实现说明见 [性能对照](docs/PERFORMANCE-COMPARISONS.md#2-xhttp与-xray-的实现速度对比)和 [XHTTP 模块](KNOWLEDGE/modules/xhttp-client-transport.md)。

### Mieru

- 基于官方 SDK 提供客户端和服务端，支持 TCP、UDP、端口范围及协议参数。
- DNS、双栈候选、接口绑定、路由标记和 socket 创建接入共享内核，保留 SDK 的协议与会话机制。
- 可选启用 TCP/UDP 传输回退，包含冷却、退避、按需恢复探测和网络代次隔离；传输偏好变化只影响新会话。
- 统一监听器、在途连接与 SDK Runtime 的清理责任，处理部分启动失败和切网后的迟到结果。
- 通过类型化失败信息区分目标拒绝、本地错误、调用方取消与代理通路故障，供代理组决定重试和健康状态。

TCP/UDP 回退分别维护会话，切换偏好只影响新连接；切网重建隔离旧拨号结果，目标拒绝、用户取消与节点故障分别处理。

历史实现对照：

| 场景 | 对官方同版本客户端 | 对 Mihomo 产品 |
| --- | ---: | ---: |
| 低复用 TCP 下载 | +10.2% | 1.07× |
| 低复用 TCP 上传 | −6.1% | 0.95× |
| UDP underlay 上传 | +16.0% | 1.27× |

官方对照使用 SDK v3.38.1，Mihomo v1.19.32 使用 SDK v3.38.0。1200 字节 UoT 帧微基准中，适配层写入为 118.8 ns/op，官方包装路径为 666.1 ns/op；分配量为 64 / 1280 B/op，读取分配次数为 0 / 2。当前 SDK 已升级至 v3.39.0，历史矩阵尚未按新 SDK 全量重测。完整结果见 [Mieru 性能对照](docs/PERFORMANCE-COMPARISONS.md#3-mieru官方-sdk官方客户端与-mihomo-对照)与 [集成说明](KNOWLEDGE/modules/mieru.md)。

### REALITY 与 VLESS

- REALITY 支持混合密钥交换、可配置 `key_share`，以及实际作用于握手的 TCP 分片和 TLS record 分片。
- 提供 VLESS 客户端应用层后量子加密，处理握手取消、帧边界与重密钥。
- 对不支持的指纹与 ECH 组合明确报错，校验 Vision 依赖的接口契约。

连接包装保留 Vision 专用写入、缓冲区交接和半关闭能力，并检查依赖内部布局兼容性，防止依赖升级破坏传输路径。

XHTTP 和 VLESS 加密扩展以客户端为主，不包含对应服务端实现。参见 [配置示例](docs/FORK-CONFIGURATION.md)及 [传输模块](KNOWLEDGE/modules/xhttp-client-transport.md)。

### Native Naive 与 HTTP 服务端

- 原生 Naive 入站支持认证 CONNECT、padding、UoT v1/v2、伪装响应和认证前资源限制。
- 对 UoT 非固定目标会话实施逐数据报目标检查，保留认证身份与路由上下文。
- HTTP/2、HTTP/3 提供并发流、流控窗口与请求阶段限制，处理非法 CONNECT、短写和关闭边界。
- 客户端使用自定义 Cronet fork，保留 host dialer、可配置窗口及可选 H3→H2 回退。
- Chromium 154.0.8037.49 已吸收 NaiveProxy patch5 相关证书封锁、QUIC 窗口与自动调节修复，并修复 Linux DNS 桥的匿名 socketpair 回复。

Cronet 的连接创建接入 Go 侧解析与双栈拨号，完成 CONNECT 后才交付连接。Linux DNS 桥修复后，自定义拨号测试由约 274 秒超时恢复为约 0.79 秒通过；这是故障修复的用例结果。

能力由实际构建标签决定：Linux 默认服务端档包含 Naive 入站，未启用 Cronet 出站；Windows 使用 `with_purego` 加载原生库。参见 [Naive 说明](docs/naive.md)、[HTTP 传输模块](KNOWLEDGE/modules/naive-http-transport.md)和 [Cronet 修复记录](KNOWLEDGE/reports/release/cronet-patch5-linux-dns-2026-11-05.md)。

### MASQUE / CONNECT-IP

- 完善 CONNECT、CONNECT-UDP 与 CONNECT-IP 数据面，处理地址分配、路由通告、DNS_ASSIGN 和 PREF64。
- 区分连接代理服务器的 Bootstrap DNS 与隧道内部解析，支持已有 H3 连接上的 DoH 及解析状态快照。
- 在 QUIC 握手阶段竞争候选，回收落败连接；H3 建连保留独立尝试窗口，为 UDP 黑洞下的 H2 回退预留时间。
- 优化 DATAGRAM 所有权与批处理，限制 Capsule 长度和条目数量；按已知下层容量处理包大小与 ICMP Packet Too Big。

CONNECT-IP 同步远端地址、路由与隧道内 DNS；H3 故障判断区分 QUIC 建连失败与 CONNECT 响应缓慢，避免应用层慢响应导致后续连接误选传输。

详见 [MASQUE 使用说明](docs/masque.md)和 [CONNECT-IP 模块](KNOWLEDGE/modules/masque-connectip.md)。

### AnyTLS、TLS 与传统代理

- AnyTLS 将 session 复用、开流、空闲退役与网络变化接入共享生命周期；帧头使用缓冲区预留空间，session 级写入保持协议顺序。
- 共享 TLS dialer 统一传递握手取消、错误和流量类别；在 NE 模式与 AnyTLS 共用创建许可，使 Trojan、VLESS/VMess 及相关 HTTP/DNS 传输使用一致的准入策略。
- ShadowTLS 限制认证前读取，清理失败的握手目标连接，并保留包装后所需的复制与关闭能力。
- SOCKS/HTTP CONNECT 保留目标解析归属、首包缓存和半关闭；SOCKS5 可选 TCP 预连接池有界运行，UDP ASSOCIATE 与 UoT 使用各自路径。
- Shadowsocks、Snell 等既有数据面结合 MTU、加密帧和缓冲区容量优化复制；协议需要保留的读写隔离按实际所有权处理。

公共 TLS 的创建治理见 [资源准入模块](KNOWLEDGE/modules/resource-admission.md)，协议包装和复制约束见 [缓冲区所有权](KNOWLEDGE/modules/buffer-ownership.md)。

### Hysteria2、TUIC 与网络端点

- Hysteria2/TUIC 通过 sing-quic 与 quic-go fork 衔接数据报所有权、UDP socket、会话复用和网络变化，减少载荷复制及接收队列对大缓冲区的长期引用。
- 嵌套代理时，将已知下层容量传到 QUIC 包大小约束；Hysteria2 的硬上限在 ChromeParrot 指纹覆盖后应用，TUIC 同样区分初始包偏好与持续约束。
- WireGuard 协调暂停、重新绑定与关闭，设备发布时继承已经生效的暂停状态；避免在睡眠或停止后残留底层 bind。
- MASQUE、WireGuard、Tailscale、OpenVPN 和 OpenConnect 等 endpoint 按自身能力与 HostPort/TUN 协作，使端点本地流量使用相应地址族和路由上下文。

隧道包大小、重建时机和平台接口分别按协议处理。详见 [MTU 与缓冲区几何](KNOWLEDGE/modules/mtu-and-geometry.md)、[endpoint 数据面](KNOWLEDGE/modules/masque-connectip.md)和 [生命周期](KNOWLEDGE/modules/core-lifecycle-runtime.md)。

## 网络链路优化

### 入口与首包处理

- **TUN 收包**：DNS 地址与端口劫持先于路由地址集判断，避免 DNS 查询提前绕出；当前 FakeIP 范围内的地址交回路由器恢复域名，保留后续规则判断。
- **系统代理 / Mixed**：复用同一个读取器识别 HTTP 与 SOCKS，握手缓存的首包直接交给后续连接；保留用户认证和原目标，减少重复读取与包装。

详见 [入口与数据路径](KNOWLEDGE/modules/data-path-optimization.md#入口嗅探与系统代理路径)。

### 流量嗅探与协议识别（sniff）

支持 HTTP、TLS、QUIC、DNS、SSH、RDP、BitTorrent、STUN、WireGuard、DTLS、NTP 等流量识别，并将可获取的域名、协议及客户端信息交给路由规则。针对首包和分片解析，优化内容包括：

- **TLS 提前结束**：提取完整 ClientHello 后立即结束嗅探，移除原先继续执行服务端握手、生成 ECDHE / ML-KEM 密钥再失败的无效工作；保留 SNI 与客户端识别结果。
- **减少重复构造**：以可重置游标读取缓存首包和新数据，替代每个嗅探器、每轮分片都重建 reader 链；错误聚合延后到本次尝试结束，数据包成功路径避免构造无用聚合对象。
- **HTTP / TLS 前置检查**：有界检查提前排除不匹配输入，保留“需要更多数据”的分片语义；已读内容继续重放给后续转发。
- **QUIC 分片与内存边界**：工作副本只覆盖当前 QUIC 包，避免复制同一数据报中的无关尾部；补充读取只运行 QUIC ClientHello 解析，最多 64 轮，放弃或出错时释放所持缓冲区。可信 FakeIP 域名满足条件时提前结束等待。
- **识别与路由一致性**：WireGuard 握手先于易碰撞的 UTP 检查；TLS / QUIC 的 SNI 规范化后再作为域名使用，避免 IP 字面量进入域名规则。嗅探结果保留为路由元数据，目标覆写遵循显式配置。

| 历史对照场景 | 优化结果 |
| --- | --- |
| TLS 流嗅探 | 耗时 **−96.2%**，分配字节 **−32.6%**，分配次数 **−35.3%**；移除每流约 127 µs、13 KiB 的无效握手工作 |
| 分片 TLS ClientHello，8 次读取 | 耗时 **−91.9%**，分配次数 **−64.5%** |
| DNS-over-stream 输入经过 HTTP 嗅探 | 分配字节 **4,889 → 205 B/op**，分配次数 **9 → 3** |

TLS 数据来自 2026-10-09 的九次交错采样，时间数据受共享主机负载影响；HTTP 为专门的前置检查 A/B。分片 TLS 与 QUIC 分片是不同测试，以上比例仅对应表中场景。原始提交、方法和边界见 [嗅探性能对照](docs/PERFORMANCE-COMPARISONS.md#嗅探首包解析与分片处理)与 [sniff 模块](KNOWLEDGE/modules/sniff.md)。

### DNS 与 FakeIP

- **多上游分组**：`stable`、`fastest`、`parallel` 支持不同选择策略，协调冷启动竞选和失败恢复，控制请求放大。
- **查询与缓存协调**：相同查询并发去重，过期缓存的后台刷新按键合并；缓存提交检查网络与策略代次。
- **NXDOMAIN 负缓存**：满足单问题、有效 SOA、非零负 TTL 等条件时，复用同一域名不存在的结论，减少 A/AAAA/HTTPS/SVCB 重复请求；重建请求 ID、问题和剩余 TTL。
- **本地广告拦截**：DNS `adblock` 动作按规则直接返回带 SOA 的 NXDOMAIN，不访问上游；与上游负缓存分别管理。
- **FakeIP 所有权**：记录签发、退役与持久化状态，避免旧虚拟地址被当作真实目标；异步劫持传递已解析消息，避免读取复用后的收包缓冲区。
- **异步建连**：DNS 冷连接建立移出调用方收包路径，打包前复制消息，避免并发修改共享对象。

配置和边界见 [NXDOMAIN](docs/DNS-NXDOMAIN.md)、[adblock](KNOWLEDGE/modules/dns-adblock-action.md)与 [DNS 模块](KNOWLEDGE/modules/dns-demand.md)。

### 规则匹配、自动测速与负载均衡

- **规则匹配**：使用紧凑域名 / IP 匹配结构；更新时先构建并校验新规则，再发布切换，失败保留旧代规则。大规则集的加载优化见下方低内存设计。
- **路径预检**：启动时检查出站引用、跨类型循环和能力要求，尽早发现无效代理链；检查过程不发起网络访问，也不推进组的轮询位置。
- `urltest` 先预热再测后续请求延迟，支持 `expected_status` 表达式，如 `204`、`200-299`、`200/204/301-399`。
- `loadbalance` 支持 `round_robin`、`consistent_hashing` 和 `sticky_sessions`，按 TCP 连接或 UDP 会话选择成员。
- 相同测试条件可共享健康记录，手动测速显示与自动选路状态分别管理。

详见 [负载均衡与故障重试](docs/FORK-CONFIGURATION.md#负载均衡与故障重试)、[规则加载与内存测量](docs/PERFORMANCE-COMPARISONS.md#1-代表性的优化前后)及 [路径校验](KNOWLEDGE/modules/path-selection-validation.md)。

### 双栈拨号与链式代理

- A/AAAA 增量结果可直接进入连接竞争；候选按地址族交错启动，快速失败时推进其他候选，严格单地址族策略同样约束字面地址。
- selector、URLTest 和 loadbalance 传递完整候选列表与网络策略，避免代理组包装丢失双栈能力。
- 区分本机出口能力与远端代理的目标可达性；网络切换后的旧查询、落败连接和迟到结果按代次处理。
- 代理服务器与最终目标的 DNS 分别归属；支持的 SOCKS/HTTP 出站可显式设置 `destination_dns_ownership`，保持默认远端解析语义。
- 显式开启组故障切换后，嵌套代理组共享逐流重试预算，按错误来源决定是否尝试备选节点。

例如 `exit.detour = entry` 表示流量先经过 `entry`，再到达 `exit`。配置、解析归属与失败边界见 [扩展配置](docs/FORK-CONFIGURATION.md)、[路径选择](KNOWLEDGE/modules/path-selection-validation.md)和 [双栈审计](KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md)。

### 传输、上传调度与快速路径

- **实际 socket 策略**：将接口绑定、路由标记、namespace、超时和取消传到协议 SDK、TLS 与原生拨号边界；回收候选竞争中落败或取消后才建立的连接。
- **协议传输**：XHTTP 聚合小写入并以有界队列维持背压；AnyTLS、Naive padding 等按帧容量利用预留头部空间；MASQUE、Hysteria2、TUIC 在支持的路径交接数据报所有权，减少中间载荷复制。各协议细节见上方对应章节。
- **上传拥塞控制**：显式配置 `traffic_scheduler.upload_rate` 后持续整形受管理的用户态上行流，并结合 `traffic_class` 保护交互请求，减少批量上传先占满共享发送队列的影响。未配置速率时走惰性路径；内核 splice 和原生直连旁路不进入该调度器。
- **Direct Offload**：Linux `auto_redirect` / nfqueue 可将符合条件的直连流交还系统转发，省去后续用户态代理开销；逐流核对 FakeIP、目标改写、统计和 socket 语义，条件不符则保留原路径。普通 TUN 和系统代理分别使用自身数据路径。
- **透明组与复制路径**：selector/URLTest 的透明选择可保留底层快速路径资格，显式 `fast_path: false` 关闭该资格；连接建立后的内核辅助复制另按平台和连接能力选择，建立失败回退用户态复制。

在 4 条普通流争用约 2 MB/s FIFO 的历史实验中，按容量 85% 持续整形，交互消息 p99 **146 → 9 ms**、平均积压 **257 → 15 KiB**，持续上传后首条交互消息 p50 **127 → 2 ms**。配置速率按实际填写值执行，85% 是该次实验条件。见 [调度对照](docs/PERFORMANCE-COMPARISONS.md#1-代表性的优化前后)。

配置见 [上传调度与 Fast Path](docs/FORK-CONFIGURATION.md)，实现范围见 [数据路径优化](KNOWLEDGE/modules/data-path-optimization.md)和 [Fast Path 设计](docs/FAST-PATH-AND-MEMORY.md)。

### 零拷贝与缓冲区交接

- **TCP 内核辅助复制**：连接能力与平台允许时使用 splice，减少数据经 Go 缓冲区中转；建立管道失败立即回退普通复制。TLS 路径保留控制消息与尾部数据处理，接管成功和实际执行 splice 分别观察。
- **QUIC / HTTP/3 数据报**：MASQUE、Hysteria2、TUIC 的适用发送路径将已有缓冲区交给下层，省去再次复制载荷；成功后由接收方释放，失败时由原持有方处理。CONNECT-IP 批量按下层容量分块，避免大批次整体退回逐包发送。
- **Naive 首包与 padding**：缓存首包直接交接，padding 利用预留空间就地构帧；异步 Cronet 回调完成前保持缓冲区有效，防止复用后读到错误数据。
- **保留必要复制**：加密、QUIC 嗅探的独立工作副本和不支持交接的接口仍使用相应复制路径，具体节省发生在支持该能力的边界。

| 实际优化位置 | 原路径 | 优化路径 | 历史测试条件 |
| --- | ---: | ---: | --- |
| CONNECT-UDP 包装 | 409–428 ns，1,345 B，2 次分配 | **83–85 ns，64 B，1 次分配** | 1,200 B 载荷，少一次 payload copy |
| CONNECT-UDP 真实连接发送 | 10.6 µs/包 | **9.5 µs/包** | quic-go，1,000 B，5,000 次发送 |
| Naive 64 B 缓存首包 | 612–637 ns，1 次分配 | **282–290 ns，0 次分配** | 2026-10-20，Go 层内存 sink |
| Naive 16 KiB 缓存首包 | 868–943 ns，1 次分配 | **271–278 ns，0 次分配** | 同组 buffer / copying 对照 |

包装边界的收益与实际传输分别测量；表中零分配仅对应指定路径。来源见 [CONNECT-UDP 对照](docs/PERFORMANCE-COMPARISONS.md#connect-udp包装与真实连接)、[Naive 分层审计](KNOWLEDGE/reports/core/NAIVEPROXY_PERFORMANCE_READONLY_AUDIT_2026-10-20.md)和 [缓冲区交接约束](KNOWLEDGE/modules/buffer-ownership.md)。

### 回包、复制与连接关闭

- **TUN 回写**：常见单包写回复用调用方切片，减少临时切片与对象。dispatcher + portNAT 单包微基准由 **236–238 ns、72 B、2 次分配** 降至 **约 156 ns、0 B、0 次分配**；批量数据报按下层容量分块提交，仅对尚未交接的部分回退和释放。
- **包大小控制**：嵌套隧道按已知下层容量扣除封装开销，向支持硬上限的 QUIC 路径传递约束；CONNECT-IP 包过大时按条件返回 ICMP Packet Too Big，避免静默丢包。
- **TCP 与 TLS 尾部**：先交付缓存首包和有效尾部数据，再报告后续错误；链式包装保留半关闭与复制能力，连接接管后沿原有跟踪、释放链清理资源，避免提前关闭或重复释放。

详见 [缓冲区所有权](KNOWLEDGE/modules/buffer-ownership.md)、[MTU 与包大小](KNOWLEDGE/modules/mtu-and-geometry.md)及 [链路实现说明](docs/ARCHITECTURE-AND-NETWORK-PATH.md)。

### 网络切换与休眠恢复

- **隔离旧结果**：DNS 查询、缓存提交、拨号和隧道重建携带网络代次，避免旧网络的迟到结果覆盖当前状态；重建租约合并同一窗口内的重复恢复。
- **按状态处理复用**：短暂休眠保留可用池；需要退役时，支持排空的协议池停止承接新流，已有流按协议约定结束，空闲资源随后释放。网络变化与普通空闲清理使用不同动作。
- **缓解集中重连**：路径退役后的短窗口对普通与批量拨号实施有等待预算的软限制，交互 / 实时流优先；槽位释放通过事件通知等待者，减少轮询唤醒。
- **启动与关闭交接**：合并 TUN 启动前的待处理网络更新，在启动后复查；关闭及重载时取消旧任务、结清资源所有权，防止迟到回调继续操作新实例。

详见 [运行时与生命周期](KNOWLEDGE/modules/core-lifecycle-runtime.md)和 [拨号调速器](KNOWLEDGE/modules/data-path-optimization.md#part-c--拨号调速器the-dial-governor)。

## 低内存与低功耗设计

### 移动端缓冲区档位

Android 与 iOS/tvOS 的 Libbox 构建启用 `with_low_memory`，macOS 保留默认档位。各平台从共享构建定义读取低内存标签，Apple 混合目标构建将该标签单独传给移动端。

| 构建档位 | 流缓冲区 | 包缓冲区 |
| --- | ---: | ---: |
| 默认 | 32 KiB | 16 KiB |
| `with_low_memory` | 16 KiB | 8 KiB |

较小档位降低并发流的工作集，同时改变单次读取容量和加密构帧边界。协议按实际头尾预留空间计算载荷，确保缓冲区可归还池中，避免边界溢出或逐包重新分配；这是编译期选择，不能通过运行配置切换。

历史 Apple M1 主机、进程内缓冲区基准，每流持有两个缓冲区，经 GC 观察活跃 heap：

| 并发流数 | 默认档位总驻留 | 低内存档位总驻留 |
| --- | ---: | ---: |
| 32 | 2,052 KiB | **1,028 KiB** |
| 128 | 8,208 KiB | **4,112 KiB** |
| 512 | 32.06 MiB | **16.07 MiB** |

每流约 **64.1 → 32.1 KiB**，512 流时该工作集减少约 **16 MiB**。代价也有记录：特定 Shadowsocks MTU 上传基准耗时增加 4.1%–19.4%、分配次数增加 71%–80%；普通 socket 的 TCP/UDP 对照未测出明确差异。完整方法及适用复制路径见 [低内存构建实测](docs/PERFORMANCE-COMPARISONS.md#低内存构建并发流与规则加载)。

### 减少分配与长期驻留

- **大规则集**：受限扩容减少一次性大数组和反复增长；无 `cache_file` 的远程二进制 SRS 直接解析下载流，省去整份下载副本，并保留截断、解压预算和发布前完整性检查。iOS 还使用规则集 mmap 路径，其他平台按自身实现处理。
- **QUIC 接收内存**：乱序小帧在底层数组容量明显大于有效数据时收缩复制，尽早归还大池缓冲；QUIC / HTTP/3 数据报出队时清空原槽位，避免队列已空却继续引用已交付载荷。
- **在途数据**：嗅探续读、XHTTP 上传队列、Capsule 解析和批量数据报均按各自上界处理；首包与数据报只在生命周期允许时借用或转移，失败、取消和关闭时释放仍由本层持有的部分。
- **按需运行**：轻量出站避免仅因配置存在就建立完整网络会话，复用连接按需创建；Mieru 未使用前不因接口通知重建 SDK Runtime。重型 endpoint 的启动成本按协议分别处理。

规则加载的历史前后对照：

| 场景与指标 | 优化前 | 优化后 |
| --- | ---: | ---: |
| 百万域名加载累计分配 | 42.5 MiB | **20.6 MiB** |
| 同组百万域名稳态存活内存 | 8.3 MiB | **7.4 MiB** |
| 同组百万域名加载 GC 次数 | 10 | **5** |
| 10 万域名加载累计分配 | 4.1 MiB | **2.9 MiB** |
| 无缓存远程 SRS 加载累计分配 | 21,229,120 B | **约 125 KB** |

规则扩容对照为 Windows / Ryzen 4800H 的同样例测试；10 万域名的稳态存活内存同时由 882.4 增至 986.3 KiB，保留扩容余量有成本。SRS 使用“有效规则附加 4 MiB 非压缩尾部”负载，优化后包含完整性修复，数字描述累计分配。来源见 [规则加载数据](docs/PERFORMANCE-COMPARISONS.md#低内存构建并发流与规则加载)。

### 分层回收与平台预算

- **先清缓存，再清空闲池**：依次尝试过期条目、空闲连接资源，收益足够即停止；不足时才根据压力和 runtime 可回收页面决定强制 GC / 归还页面。低收益整理进入冷却，减少反复 GC。
- **共用回收执行器**：空闲整理、系统压力与危机回收串行协调，紧急请求优先；真正重置网络前重新检查当前压力，避免已过期的危机请求打断恢复后的连接。
- **普通整理保留业务**：`TrimMemory` 清理空闲项，不重置网络或主动拨号；严重压力下的 `ReleaseMemory` 可执行更强释放与网络重置。回收遍历覆盖出站、endpoint、DNS 传输和 HTTP 客户端池。
- **平台分别治理**：Apple 同时观察进程 footprint 与 Go runtime 软限制，计入原生侧内存的差异；Android 按系统 trim 等级处理，界面隐藏本身不触发内存回收。关闭与重载等待已接受的回收结束，丢弃旧实例待执行请求。

**GC 策略对照**：在 40 MiB `GOMEMLIMIT` 下，将 `GOGC` 从 50 调整为 100。SS2022 内存压力基准每组 10 次交替运行、每次使用新进程，中位数如下：

| 指标 | GOGC 50 | GOGC 100 | 变化 |
| --- | ---: | ---: | ---: |
| GC 次数 | 29 | 13.5 | **−53.4%** |
| GC 累计暂停 | 861,564 ns | 415,604 ns | **−51.8%** |
| 基准吞吐 | 5,706.39 MB/s | 5,776.48 MB/s | **+1.23%** |

同组 Go runtime 管理内存的连续采样峰值中位数为 37.5 → 38.9 MiB，体现减少 GC 与保留更多内存的取舍。这是进程内压力基准，不能当作代理网络吞吐、iOS footprint 或耗电改善比例；[原始样本与完整对照](docs/PERFORMANCE-COMPARISONS.md#受限内存下的-gc-策略)保留了复算入口。

### iOS 高成本创建准入

Network Extension 模式使用 OOMKiller 已有压力状态，限制 AnyTLS TCP/UoT 与共享 TLS 握手的创建并发、等待队列和等待时长，缓解突发建连阶段的内存压力。压力升高时逐级收紧额度：

| 默认压力状态 | 普通创建并发 | 等待队列 | 最大等待 |
| --- | ---: | ---: | ---: |
| Normal | 32 | 256 | 5 s |
| Armed | 16 | 128 | 2.5 s |
| Critical | 8 | 32 | 100 ms |

交互流量另有 4 个预留额度；流量类别沿拨号上下文传递，TLS、AnyTLS 与多跳 detour 共用已持许可，避免嵌套调用重复排队。许可在创建返回后释放，不占用整个连接生命周期；压力更新带版本校验，诊断记录排队、拒绝、取消和在途计数。

该准入默认不作用于非 NE 平台，也尚未为所有 QUIC、Cronet、Mieru 创建路径提供统一预算；实际 iOS footprint 与 Jetsam 改善仍需对应设备验证。

### 后台任务与设备唤醒

- 暂停或深度空闲时抑制非必要 DNS 乐观刷新；规则集与 Dashboard 周期更新等待适当工作窗口，首次必要初始化照常执行。
- 屏幕亮起、解锁、扩展 resume 与真实睡眠边界分别处理，避免重复退役连接或错误计算休眠时长。
- 内存看门狗按压力自适应采样；必要的恢复观察继续运行，非必要后台任务不主动唤醒按需隧道或重新拨号。

设计与适用范围见 [低内存缓冲区](KNOWLEDGE/modules/mtu-and-geometry.md#5-低内存几何-profilethe-low-memory-geometry-profile)、[分层内存管理](docs/FAST-PATH-AND-MEMORY.md#内存管理的分层设计)、[创建准入](KNOWLEDGE/modules/resource-admission.md)和 [移动端电源策略](KNOWLEDGE/modules/power-mobile-lifecycle.md)。

## iOS 本地回环

**开启路径：更多 → 连接行为 → 配置覆盖 → 本地签名回环。** iPad 在“配置覆盖”中查找同名开关。默认关闭，开启并重载后向本次 TUN 注入 `10.7.0.1`，用于本机回环连接。

**SideStore 续签（真机反馈）**。纯 IPv4 的 TUN 配置下，开启本地签名回环即可续签。IPv4/IPv6 双栈下，保持回环开启，再到 **SideStore → Developer Options → Accept IPv6 Config** 打开该选项，即可保留 TUN IPv6 并完成续签。此前遇到的 `DeviceEndpointNotInitialized` 在该设备上也通过这一设置解决。[续签步骤与排障](docs/IOS-SELFSIGN-AND-LOOPBACK.md#当前兼容性与续签方式)

另有真机反馈，WLOC 的开发者模式虚拟定位可正常使用。

上述结果仅对应已反馈的设备与使用场景。默认 `go` 栈支持 TCP、UDP、ICMPv4/v6 反射；其他显式栈尚无完整同等实现。配置与排障见 [本地回环说明](docs/IOS-SELFSIGN-AND-LOOPBACK.md#打开本地签名回环)。

## 安全与部署

### TCP 监听默认安全

Linux 普通入站在未显式启用 `tcp_multi_path` 时使用普通 TCP；Clash API、V2Ray API、Debug HTTP、libbox pprof 等管理入口同样显式关闭默认 MPTCP，减少 Go 默认设置导致的意外暴露。

这一措施缓解 CVE-2026-98432 已公开利用路径的攻击面。底层缺陷位于 Linux IPv4 TCP，系统仍需安装内核安全补丁；`tcp_multi_path: true` 保留主动启用能力。详见 [MPTCP 安全说明](docs/SECURITY-MPTCP.md)。

### 服务端与桌面客户端

- Naive、HTTP 和 MASQUE 的认证、伪装响应及隧道建立分别校验；提供连接数、请求头、并发流和窗口等资源限制。
- UoT 非固定目标会话逐包执行目标访问检查；具体准入由规则顺序与配置决定。
- Windows 守护进程校验调用进程、安装位置、角色和签名者，按安装身份隔离服务状态、应用数据与卸载清理。
- 配套客户端区分订阅正文与用量信息，支持 `Subscription-Userinfo`，维护 Dashboard 集成。

部署与本地服务边界见 [服务端说明](docs/JIEJIE-SERVER.md)和 [Windows 守护进程模块](KNOWLEDGE/modules/windows-daemon-security.md)。

## 依赖与验证

项目维护 sing、sing-tun、sing-quic、quic-go、uTLS、cronet-go 等依赖 fork，将协议改动延伸到 socket、TUN、QUIC 和原生引擎。构建固定 Go 模块、客户端及原生库来源，测试覆盖真实载荷、协议互通、网络切换、取消关闭、缓冲区交接与故障注入。

[关联仓库](FORK_REPOSITORIES.md) · [依赖整合](docs/fork/UPSTREAM-DEPENDENCY-INTEGRATION.md) · [测试范围与验收记录](KNOWLEDGE/ACCEPTANCE.md)

## 文档

- **配置与使用**：[配置参考](docs/configuration) · [扩展配置示例](docs/FORK-CONFIGURATION.md) · [NXDOMAIN](docs/DNS-NXDOMAIN.md) · [本地回环](docs/IOS-SELFSIGN-AND-LOOPBACK.md#打开本地签名回环)
- **构建与发布**：[构建档位](docs/BUILD-PROFILES.md) · [Apple TestFlight](docs/APPLE-BETA-RELEASE.md) · [更新日志](CHANGELOG.md)
- **性能与设计**：[链路实现说明](docs/ARCHITECTURE-AND-NETWORK-PATH.md) · [性能对照](docs/PERFORMANCE-COMPARISONS.md) · [性能参数](KNOWLEDGE/PERFORMANCE-FACTORS.md) · [Fast Path 与内存管理](docs/FAST-PATH-AND-MEMORY.md) · [工程架构评估](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)
- **开发维护**：[Fork 差异](docs/FORK-DIFF.md) · [知识库](KNOWLEDGE/README.md) · [任务索引](KNOWLEDGE/INDEX.md) · [维护指引](KNOWLEDGE/REFRESH.md)

开发及 AI 助手从 [AGENTS.md](AGENTS.md) 进入，先确认知识库基线与 Git 增量，再按任务查阅模块。

- [Release 构建指引](docs/RELEASE-WINDOWS.md)：在 Windows 发起内核正式版构建、发布和验收。
- [TF 构建指引](docs/TESTFLIGHT-MAC.md)：内核正式版发布并验收后，在 Mac 构建并上传 TestFlight。

## 致谢与许可

感谢 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 及相关上游项目和贡献者。本项目采用 [GNU GPL v3.0 或更新版本](LICENSE)。
