# JiejieBox

基于 [sing-box](https://github.com/SagerNet/sing-box) 持续开发的跨平台代理内核，扩展出站 Provider、XHTTP、Mieru、TrustTunnel、AmneziaWG、VLESS 加密与 Native Naive，在共享的路径模型、运行时协调与生命周期框架上，完善 DNS、双栈拨号、链式代理、内存控制和配置恢复，并提供配套桌面端和移动端集成。

[下载](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [更新日志](CHANGELOG.md) · [Android 客户端](https://github.com/Piggy-Cat-bit-shadow/satelite-one) · [配置文档](docs/configuration) · [问题反馈](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/issues)

## AI 与维护者阅读指引

读到本页后，请主动进入[知识库入口](KNOWLEDGE/README.md)，不要只凭 README 的功能概览推断实现。先读 [AGENTS.md](AGENTS.md) 与[知识库基线](KNOWLEDGE/BASELINE.md)，再读[项目总览](KNOWLEDGE/PROJECT.md)、[架构](KNOWLEDGE/ARCHITECTURE.md)和[主题索引](KNOWLEDGE/INDEX.md)。按当前问题进入[模块索引](KNOWLEDGE/modules/README.md)及相关模块，核对[跨模块约束](KNOWLEDGE/INVARIANTS.md)和[验收范围](KNOWLEDGE/ACCEPTANCE.md)。

知识库中的部分章节保留历史基线：落到具体行为或配置字段时，继续核对当前分支的源码、构建标签、`release/source-lock.json`、依赖仓库 gitlink 与基线后的 Git 增量；发现文档与实现不一致，按[知识库更新指引](KNOWLEDGE/REFRESH.md)修订相关入口和引用页。本次协议、规则、策略组与内存实现可从 [0.1.45 协议模块](KNOWLEDGE/modules/protocols-0145.md)、[规则与策略组模块](KNOWLEDGE/modules/rules-and-groups-0145.md)开始。

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

[常规配置](docs/configuration) · [TrustTunnel 入站](docs/configuration/inbound/trusttunnel.zh.md) / [出站](docs/configuration/outbound/trusttunnel.zh.md) · [AmneziaWG endpoint](docs/configuration/endpoint/wireguard.zh.md#amneziawg-扩展) · [出站 Provider](docs/configuration/provider/index.zh.md) · [本项目扩展配置与示例](docs/FORK-CONFIGURATION.md) · [Naive](docs/naive.md) · [MASQUE](docs/masque.md) · [DNS 广告拦截](KNOWLEDGE/modules/dns-adblock-action.md) · [本地回环](#ios-本地回环)

常规配置参考已纳入的 sing-box **v1.15.0-alpha.13**；XHTTP、Mieru、负载均衡、上传调度等扩展按本项目文档配置。协议可用性取决于平台与[构建档位](docs/BUILD-PROFILES.md)。当前 Naive 引擎为 **Chromium 154.0.8037.49 + NaiveProxy patch5 相关修复**，Mieru 基础 SDK 为 **v3.39.0**，实际消费 fork `v3.39.1-0.20261112135526-b5934f7136b4`。

下文结合具体实现与历史对照介绍功能；性能数字保留测试场景，完整方法与原始记录见 [性能专页](docs/PERFORMANCE-COMPARISONS.md)。

## 协议与网络能力

### TrustTunnel 与 AmneziaWG

- **TrustTunnel**：原生客户端与服务端，支持 H2/H3、TCP/UDP、用户名/密码认证、健康检查与空闲会话管理；共享 session 中的 stream 与 UDP association 分别管理。
- **协议接入**：TrustTunnel 使用内核 DNS、Dialer、TLS、路由和流量类别；UDP 关联以槽位、队列和字节预算控制占用，读期限与活动记录接到实际转发路径。
- **AmneziaWG 3.x / 3.1**：在 `wireguard` endpoint 上提供 junk、消息前缀、范围魔数、CPS/声明式伪装、头保护、padding、随机尾部及相应握手参数，支持主动连接与 Linux 监听应答。
- **设备复用**：普通 WireGuard 与 AWG 共用现有设备、peer、路由和按需恢复体系；修复并发流的等待/唤醒，保持设备关闭与重新绑定的所有权。

TrustTunnel 使用 `type: "trusttunnel"`；AWG 扩展字段位于 `type: "wireguard"` 的 endpoint 根层。`with_trusttunnel`、`with_awg` 已纳入默认发布档位，具体平台能力仍按[构建档位](docs/BUILD-PROFILES.md)选择。

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

XHTTP 的上传控制同时覆盖待发送与正在发送的数据。同一 Box 内多个 XHTTP 节点共用上传预算，避免节点越多、各自缓冲上限相乘；主池、开场控制池和短流保留分别保障大流吞吐与新请求进展。逻辑额度先于分配申请，底层数组按实际容量记账，共享切片不重复计费，替换期间的新旧数组同时计入峰值。

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

官方对照使用 SDK v3.38.1，Mihomo v1.19.32 使用 SDK v3.38.0。1200 字节 UoT 帧微基准中，适配层写入为 118.8 ns/op，官方包装路径为 666.1 ns/op；分配量为 64 / 1280 B/op，读取分配次数为 0 / 2。当前基础 SDK 已升级至 v3.39.0，实际消费上述 fork；历史矩阵尚未按当前 pin 全量重测。完整结果见 [Mieru 性能对照](docs/PERFORMANCE-COMPARISONS.md#3-mieru官方-sdk官方客户端与-mihomo-对照)与 [集成说明](KNOWLEDGE/modules/mieru.md)。

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

- **双栈与真实创建**：HY2/TUIC 在地址族竞争的真实会话创建处接准入，复用 stream 不重复计费；失败族、落败连接与换网后迟到的结果按轮次和对象身份释放。
- **QUIC 发送调度**：sing-quic / quic-go 修正 ACK-only 包与拥塞控制 pacing 的衔接，并处理混淆数据报的有效长度及载荷所有权，减少错误调度和不必要的数据修改。
- **共享会话生命周期**：网络代次、异步认证与结果发布协调，避免已经退出的创建工作重新交付旧连接。

- Hysteria2/TUIC 通过 sing-quic 与 quic-go fork 衔接数据报所有权、UDP socket、会话复用和网络变化，减少载荷复制及接收队列对大缓冲区的长期引用。
- 嵌套代理时，将已知下层容量传到 QUIC 包大小约束；Hysteria2 的硬上限在 ChromeParrot 指纹覆盖后应用，TUIC 同样区分初始包偏好与持续约束。
- WireGuard 协调暂停、重新绑定与关闭，设备发布时继承已经生效的暂停状态；避免在睡眠或停止后残留底层 bind。
- MASQUE、WireGuard、Tailscale、OpenVPN 和 OpenConnect 等 endpoint 按自身能力与 HostPort/TUN 协作，使端点本地流量使用相应地址族和路由上下文。

隧道包大小、重建时机和平台接口分别按协议处理。详见 [MTU 与缓冲区几何](KNOWLEDGE/modules/mtu-and-geometry.md)、[endpoint 数据面](KNOWLEDGE/modules/masque-connectip.md)和 [生命周期](KNOWLEDGE/modules/core-lifecycle-runtime.md)。

### 协议性能与并发传输

协议优化同时关注吞吐、并发扩展和每单位业务的成本。WG/AWG 修复发送空间的等待/唤醒链：原先在同机双进程、用户态隧道上传测试中停滞的并发组合，修复后能够完整传输。WG 四流零间隔启动达到 **49.8–54.8 MiB/s**，AWG 同组达到 **41.5–53.8 MiB/s**；两协议、三种启动间隔、两种并发度各重复三次的矩阵为 **36/36 完成**。

下表记录协议台架的单流与四流表现。箭头表示 **1 → 4 条并发流**，不是版本升级倍数；环境为 WSL2 同机 loopback、真实客户端/服务端进程、5 秒载荷、多次运行中位数。

| 协议/承载 | 上传 MiB/s（1 → 4 流） | 下载 MiB/s（1 → 4 流） |
| --- | ---: | ---: |
| VLESS Vision | 264.7 → 809.1 | 542.3 → 1409.0 |
| REALITY | 266.2 → 814.2 | 528.0 → 1455.5 |
| AnyTLS | 255.9 → 769.4 | 538.9 → 1234.3 |
| TrustTunnel H2 | 172.5 → 213.5 | 140.3 → 140.7 |
| TrustTunnel H3 | 145.7 → 202.2 | 194.8 → 233.0 |
| Naive H2 | 130.5 → 238.2 | 234.6 → 371.6 |
| Naive H3 | 58.5 → 78.1 | 91.5 → 76.4 |
| XHTTP H2 | 32.7 → 126.3 | 89.6 → 368.6 |
| XHTTP H3 | 32.8 → 127.4 | 119.8 → 203.9 |
| MASQUE | 70.1 → 80.5 | 72.3 → 79.2 |
| Mieru TCP | 122.6 → 483.1 | 103.5 → 689.4 |
| Mieru UDP | 30.4 → 73.0 | 31.2 → 39.9 |

吞吐之外，同一台架四流客户端的上传/下载 CPU 成本分别为：Vision **5.21/3.00 s/GiB**、REALITY **5.16/2.92 s/GiB**、AnyTLS **5.47/4.11 s/GiB**。这些数据描述指定本机环境下的实现表现，公网速度仍取决于链路、服务器和配置。

原始条件与分项结果见 [协议性能报告](docs/final-audit/PERFORMANCE.md)、[最终台架对照](docs/final-audit/closure/PERF-COMPARISON.md)与 [WG/AWG 并发修复](docs/final-audit/closure/WG-AWG-B7.md)。

## 网络链路优化

### 路径模型与启动前校验

PhysicalPath 描述数据实际经过的代理与隧道，ControlPath 描述代理组选择等控制决策。两者共同组织多层代理、detour、目标解析和传输能力，避免把组选择顺序误当成实际网络路径。

- 启动前只读检查出站引用、跨类型循环、路径截断和能力要求；检查不主动拨号，也不推进组的轮询位置。
- 网络需求按实际可达路径判断，目标 DNS 归属与代理服务器解析分开，避免无关节点的能力要求影响其他路径。
- 嵌套路径沿已知下层容量传递 MTU 与封装约束；只读逐跳视图帮助定位结构和失败归属。
- 动态 Provider 发布与组成员替换沿用引用和路径检查，旧对象由实际引用及在途拨号租约保护。

详见 [路径选择与校验](KNOWLEDGE/modules/path-selection-validation.md)和 [链路实现说明](docs/ARCHITECTURE-AND-NETWORK-PATH.md)。

### 入口与首包处理

- **Go TUN 栈**：以 Go 栈连接设备收包、路由、出站与回包，处理 TCP、UDP、IPv4 分片及 ICMP 包大小反馈；队列预算、栈解绑与设备关闭沿实际数据所有权清理。
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

- **匹配解析与拨号分开**：`resolve.match_only` 将解析地址用于后续规则判断，同时保留原有目标寻址语义，适合由远端代理解析最终目标的路径。
- **关键词匹配实测**：1,024 个关键词的专项测试中，未命中从逐项匹配的 13.1–14.5 µs 降至编译匹配的 188–243 ns；末尾命中从 15.1–15.3 µs 降至 187–189 ns，两条路径均为零分配。该数字对应匹配组件，不是完整路由耗时。
- **大型关键词集合**：`domain_keyword` 使用有界的不可变编译匹配结构，小集合与超界输入回退既有匹配方式；查询热路径保持零分配。
- **策略组展示**：`hidden` 控制适用客户端的组展示，保留组的真实选择、引用和运行关系。
- **显式省电 failover**：稳定健康时集中探测稳定槽位，退化后扩大候选，故障与恢复分别确认；省略配置保留原有测速策略。
- **共享测量**：等价探测合并真实工作，轮入口与 worker 协调任务生产；取消一个等待者不终止其他调用者仍需要的测量。

- **规则匹配**：使用紧凑域名 / IP 匹配结构；更新时先构建并校验新规则，再发布切换，失败保留旧代规则。大规则集的加载优化见下方低内存设计。
- **路径预检**：启动时检查出站引用、跨类型循环和能力要求，尽早发现无效代理链；检查过程不发起网络访问，也不推进组的轮询位置。
- `urltest` 先预热再测后续请求延迟，支持 `expected_status` 表达式，如 `204`、`200-299`、`200/204/301-399`。
- `loadbalance` 支持 `round_robin`、`consistent_hashing` 和 `sticky_sessions`，按 TCP 连接或 UDP 会话选择成员。
- 相同测试条件可共享健康记录，手动测速显示与自动选路状态分别管理。

详见 [负载均衡与故障重试](docs/FORK-CONFIGURATION.md#负载均衡与故障重试)、[规则加载与内存测量](docs/PERFORMANCE-COMPARISONS.md#1-代表性的优化前后)及 [路径校验](KNOWLEDGE/modules/path-selection-validation.md)。

### 出站 Provider 与动态策略组

- 从远程订阅、本地文件或内联配置导入出站节点。节点以 `provider/node` 命名，订阅中的 DNS、路由及入站设置不会覆盖主配置。
- selector 和 URLTest 可以同时使用静态节点与 Provider 节点。远程订阅异步下载、缓存并定期刷新；节点变化后更新组成员，无需重新启动内核。未变化的节点保留现有连接。
- 纯 Provider 组在首次下载前可以等待节点，也可以作为其他出站的 `detour` 目标。动态发布会检查出站与 DNS 依赖环；替换时旧节点至少保留到组成员切换和在途拨号交接完成，仍被其他对象直接引用的节点会继续保留。

Provider 支持解析的客户端配置与订阅格式：

| 客户端 / 格式 | 可读取的内容 |
| --- | --- |
| sing-box / JiejieBox JSON | `outbounds` 中的代理节点 |
| Clash / Mihomo（Clash Meta）YAML | `proxies` 中解析器支持的代理节点；不导入 `proxy-groups`、规则和 DNS 设置，也不递归拉取其中的 `proxy-providers` |
| Shadowsocks SIP008 JSON | `servers` 节点列表 |
| 通用分享链接列表（包括 v2rayN 等客户端导出的对应链接） | 明文或 Base64 编码的 `ss://`、`vmess://`、`vless://`、`trojan://`、`tuic://`、`hysteria://`、`hy2://` / `hysteria2://`、`anytls://` 链接 |

上述兼容范围是 **Provider 提取节点**，不代表客户端可以把这些文件直接作为完整主配置运行，也不代表兼容所有客户端扩展字段。V2Ray / Xray 原生 JSON、Surge、Quantumult X 的完整配置未接入解析；请改用上表支持的订阅格式。WireGuard / Tailscale endpoint 暂不由 Provider 发布，节点协议的实际可用性还取决于平台与构建档位。

**机场节点未拉取到时**：若首次导入本地配置并启动后，机场节点分组仍为空，请先停止 VPN，在 **设置 → 核心 → 清理工作目录** 后，重启 VPN 再重试。

若订阅下载需要经过本次订阅才会出现的节点，首次下载会形成引导环。可为下载配置独立的 `http_client`，并检查订阅域名的 DNS 解析路径。见 [Provider 配置参考](docs/configuration/provider/index.zh.md)、[远程订阅与链式代理示例](docs/FORK-CONFIGURATION.md#远程订阅与链式代理)和 [v0.1.44 更新记录](CHANGELOG.md)。

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
- **生命周期收尾**：Scope 按资源依赖组织清理，关闭与在途创建协调，迟到取得的资源由所属 owner 收尾；阶段记录和有预算的 watchdog 帮助观察等待释放的对象。
- **TCP 与 TLS 尾部**：先交付缓存首包和有效尾部数据，再报告后续错误；链式包装保留半关闭与复制能力，连接接管后沿原有跟踪、释放链清理资源，避免提前关闭或重复释放。

详见 [缓冲区所有权](KNOWLEDGE/modules/buffer-ownership.md)、[MTU 与包大小](KNOWLEDGE/modules/mtu-and-geometry.md)及 [链路实现说明](docs/ARCHITECTURE-AND-NETWORK-PATH.md)。

### 网络切换与休眠恢复

运行时协调器统一提供网络代次、重建所有权和后台工作许可；协议仍负责自己的连接、池和设备。初始网络环境在启动交接中建立，后续网络事件由所属监视器发布，避免旧监视器和旧重建操作影响当前实例。

- **隔离旧结果**：DNS 查询、缓存提交、拨号和隧道重建携带网络代次，避免旧网络的迟到结果覆盖当前状态；重建租约合并同一窗口内的重复恢复。
- **按状态处理复用**：短暂休眠保留可用池；需要退役时，支持排空的协议池停止承接新流，已有流按协议约定结束，空闲资源随后释放。网络变化与普通空闲清理使用不同动作。
- **缓解集中重连**：路径退役后的短窗口对普通与批量拨号实施有等待预算的软限制，交互 / 实时流优先；槽位释放通过事件通知等待者，减少轮询唤醒。
- **池取用与结果发布**：HTTP/2、gRPC-lite、XHTTP 在借用和响应发布处核对代次；双栈竞争按轮次与 client 身份清理落败和迟到结果，避免旧清理误伤新对象。
- **重置与永久关闭分离**：VLESS、VMess、Trojan 在换网时使用适用 transport 的 reset 能力，保留后续重建连接的客户端寿命；活跃 QUIC 流按自身迁移能力处理。
- **启动与关闭交接**：合并 TUN 启动前的待处理网络更新，在启动后复查；关闭及重载时取消旧任务、结清资源所有权，防止迟到回调继续操作新实例。

详见 [运行时与生命周期](KNOWLEDGE/modules/core-lifecycle-runtime.md)和 [拨号调速器](KNOWLEDGE/modules/data-path-optimization.md#part-c--拨号调速器the-dial-governor)。

## 低内存与低功耗设计

资源管理由设备状态、真实业务活动和内存压力共同驱动，覆盖创建、传输、更新和释放全过程。既控制单条流的缓冲大小，也关注多节点、多流以及新旧代同时存在时的占用。

### 控制增长与实际占用

| 环节 | 当前机制 | 作用范围 |
| --- | --- | --- |
| 高成本创建 | 在真实 session/socket/握手创建接点准入，复用不重复计费，嵌套调用继承许可 | 已接入的协议创建路径与适用平台策略 |
| XHTTP 上传 | 主池、开场控制池、短流保留；逻辑字节与底层数组容量分别记账 | 同一 Box 的 XHTTP clients 共用上传预算 |
| relay 缓冲 | 按 copy 方向领取增长许可，压力升高时限制新扩容，无许可仍用小缓冲传输 | 每个 ConnectionManager；许可持有至对应 copy 结束 |
| TUN / UDP | 单会话、NAT 聚合与 Memory TUN 设备级字节边界，入队前预留、出队后归还 | 对应队列及设备实现 |
| 规则与订阅 | 共享构建准入、流式临时产物、校验后发布、旧版本租约 | Provider 与远程规则构建/加载 |
| 驻留与回收 | 区分 active、idle、draining、terminated，记录回收前后收益 | 协议 owner、连接池与现有 OOM 执行器 |

XHTTP 先取得逻辑额度再分配数组，等待中的大写入不预占一批空数组；同一数组的多个切片只按真实所有权记容量，新旧数组替换的重叠也计入峰值。TUN/NAT 的读取快速路径、取消、detach 与关闭分别归还额度，等待记录随连接迁移和退出清理。

网络变化后，旧池停止承接新工作，活动流按合同排空；最后使用者离开、资源真正释放后才结束驻留记录。构建准入上限、实际在途数与观察到的重叠峰值分别呈现，压力变化不会让账面提前消除仍在使用的内存。

这些预算约束各自可追踪的对象和字节，不等同于进程 RSS 的统一硬上限。内存诊断同时观察创建、队列、构建、驻留与回收，回收历史采用固定环形记录，避免观测自身无限增长。

### 字节预算、背压与释放

- **XHTTP 双重记账**：pending/in-flight 逻辑字节与仍持有的数组容量分别控制。一个确定性样本中逻辑占用为 65,536 B，真实数组为 81,920 B；容量账记录后者，避免小切片掩盖大数组。最后引用退出才归还容量，失败、取消、部分写入和 Close 均结算。
- **控制请求保留空间**：长流不能花掉开场控制池；等待大写入先等逻辑额度，不预先占住空数组。分配不足等待真实容量变化，避免领取/回滚额度形成自唤醒忙等。
- **relay 按需扩容**：普通可增长 copy 方向默认由 Normal 的 256 降为 Armed 的 128，Critical 停止新的普通增长；交互额外保留分别为 16、8、0。未获得增长许可仍用小缓冲完成传输，两个方向独立结算，已增长缓冲持牌至 copy 结束。
- **UDP NAT 分层上限**：对应依赖实现默认每会话 256 KiB、NAT 聚合 8 MiB；Memory TUN 默认设备级队列预算 8 MiB。额度在入队前申请，普通读取、read waiter 与批量快速读取均在出队后归还。
- **反压与跨队列唤醒**：适用的 TCP 写入在容量不足时等待；UDP 按数据报语义丢弃并计数。同设备其他队列释放空间后通知等待者，按实际包长判断能否继续。
- **完整退出**：等待需求归属于连接，迁移前撤销旧记录；取消和关闭清理全部等待者。栈 detach 逐帧归还，设备关闭不提前清零仍由调用者队列持有的数据。

上述数字分别对应 copy 方向和特定队列，不能互换为连接总数或进程物理内存上限。协议所需的强制缓冲几何扩容保留独立处理。

### 更新峰值、共享工作与长期驻留

- **下载与构建**：规则正文流式落盘并计算摘要，校验后发布；Provider 与远程规则共用构建准入，压力收紧后仍记录已有在途和新旧版本重叠。
- **版本租约**：旧规则仍有读者时继续保留，最后使用者退出后才可清理；恢复使用配置对应的精确规则和 Provider 产物引用。
- **减少重复工作**：等价测速和 DNS 查询共享实际工作，调用者独立取消；显式省电 failover 在稳定状态减少全组探测，进程查询合并同 UID 工作并按需获取路径。
- **驻留按实际对象计数**：session、connection、generation、stream、device 分开观察。旧池从可借用列表移除后仍计 draining，真正释放后才计 terminated。
- **回收反馈**：沿用同一压力来源与回收执行器，记录执行层、前后 footprint、低收益和冷却；最近动作固定保留 8 条，避免诊断历史持续增长。
- **观测预算**：DNS 历史限制条数、字节和 TTL，诊断事件限制队列与保留容量，历史分页、连接快照分片，控制读取诊断时的额外分配。

这些机制共同控制突发建连、慢速接收、集中订阅更新和反复换网时的资源叠加。局部缓冲预算、累计分配与进程 footprint 分别测量，避免把某个队列下降直接表述为整机内存下降。

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

### 高成本创建准入与移动端压力策略

Network Extension 模式使用 OOMKiller 已有压力状态，限制 AnyTLS TCP/UoT 与共享 TLS 握手的创建并发、等待队列和等待时长，缓解突发建连阶段的内存压力。压力升高时逐级收紧额度：

| 默认压力状态 | 普通创建并发 | 等待队列 | 最大等待 |
| --- | ---: | ---: | ---: |
| Normal | 32 | 256 | 5 s |
| Armed | 16 | 128 | 2.5 s |
| Critical | 8 | 32 | 100 ms |

交互流量另有 4 个预留额度；流量类别沿拨号上下文传递，TLS、AnyTLS 与多跳 detour 共用已持许可，避免嵌套调用重复排队。许可在创建返回后释放，不占用整个连接生命周期；压力更新带版本校验，诊断记录排队、拒绝、取消和在途计数。

创建接点覆盖适用的 QUIC、TrustTunnel、AnyTLS、Mieru 与 Cronet 路径。准入位置随协议真实资源边界确定，不把每个复用 stream 当作一次昂贵创建；NE 的严格压力配额与其他平台策略分别处理。并发额度描述创建控制，不能直接换算为进程 footprint 或设备耗电降幅。

### 后台任务与设备唤醒

低功耗 governor 汇集设备休眠、真实业务流量、深度空闲截止时间与后台工作许可，协调各模块的暂停与恢复。屏幕关闭不直接等同于清空连接池，业务活动可以推迟深度空闲。

- **分批恢复**：后台检查、规则刷新和统计写入按工作窗口恢复，减少唤醒瞬间集中建连；旧截止时间回调不能推进新状态。
- **按需隧道活动保护**：适用路径以实际 payload 活动判断活跃豁免，活动窗口到期重新裁决，协调业务连续性与闲置释放。

- 暂停或深度空闲时抑制非必要 DNS 乐观刷新；规则集与 Dashboard 周期更新等待适当工作窗口，首次必要初始化照常执行。
- 屏幕亮起、解锁、扩展 resume 与真实睡眠边界分别处理，避免重复退役连接或错误计算休眠时长。
- 内存看门狗按压力自适应采样；必要的恢复观察继续运行，非必要后台任务不主动唤醒按需隧道或重新拨号。

设计与适用范围见 [低内存缓冲区](KNOWLEDGE/modules/mtu-and-geometry.md#5-低内存几何-profilethe-low-memory-geometry-profile)、[分层内存管理](docs/FAST-PATH-AND-MEMORY.md#内存管理的分层设计)、[创建准入](KNOWLEDGE/modules/resource-admission.md)和 [移动端电源策略](KNOWLEDGE/modules/power-mobile-lifecycle.md)。

## 配置管理、诊断与客户端协作

### 配置与订阅更新

- **分层分析**：轻量配置分析检查声明、引用与能力要求；内容级变更计划区分可应用的变化和需要重启的部分。
- **明确应用结果**：Provider 更新报告逐项提交、保旧和持久化状态，客户端可以区分请求已接收与更新实际完成。
- **版本化产物**：规则下载流式写入临时文件，计算摘要并校验后发布；租约保护正在读取的版本，清理按实际引用执行。
- **精确恢复**：配置提交记录对应的规则与 Provider 产物引用，候选失败保留可用版本，恢复按配套版本组合进行。

### 诊断与运行状态

- **DNS 与路径解释**：提供只读路由/DNS 预览、查询事件和历史，保留实际执行环境及请求归因。
- **主动连接诊断**：沿真实路由执行适用的连接步骤，输出阶段、事件与结构化结果。
- **连接事件与快照**：共享事件扇出，订阅记录丢失后可通过分片快照重同步，支持客户端重新连接。
- **有界观测**：历史条数、字节、TTL、事件队列与返回结果分别设边界，查询使用分页，避免长时间运行持续积累诊断对象。

### 跨端请求与回执

Apple 与 Android 将平台状态接入共享移动端策略，区分持续状态快照与一次性恢复事件，避免重读状态反复触发恢复。Libbox、桌面守护进程和各端桥接提供对应的管理入口，客户端与内核按配套接口构建。

切组与重载关联请求、配置 revision、服务实例或隧道 session；切组后读取真实选择，客户端按所属请求结算，减少旧回执覆盖新操作。Apple App / Network Extension、Android、Satelite、Desktop / Dashboard 通过各自桥接和管理入口消费这些能力。Libbox 保留字符串、错误链与 ABI 的配套检查。

架构与接口入口见 [生命周期](KNOWLEDGE/modules/core-lifecycle-runtime.md)、[知识库任务索引](KNOWLEDGE/INDEX.md)与 [配置参考](docs/configuration)。

<a id="ios-本地回环"></a>

## iOS 本地回环续签（SideStore / LiveContainer）

本项目通过 sing-box TUN 入站的 `loopback_address` 实现 iOS 本地回环，可配合 SideStore / LiveContainer 完成应用续签，无需额外启用 LocalDevVPN。

### 1. 开启本地回环

在 JiejieBox 中进入 **更多 → 连接行为 → 配置覆盖 → 本地签名回环**，打开开关后等待 VPN 重载；尚未连接时，在下一次启动 VPN 时生效。iPad 在“配置覆盖”中查找同名开关。

该开关默认关闭，开启后会在本次运行的 TUN 中加入 `10.7.0.1`，并配置对应的 iOS 主机路由，不会改写订阅或原始配置文件。也可以在现有 TUN 入站中手动加入 `"loopback_address": ["10.7.0.1"]`；已有该字段时，将地址追加到列表中即可。

[`loopback_address`](docs/configuration/inbound/tun.zh.md#loopback_address) 自 sing-box 1.12.0 起提供，可将指向指定地址的 TCP 连接重定向回本机。本项目默认 `go` 栈还支持 UDP、ICMPv4/v6 反射；旧配置若显式选择其他栈，请参阅[适用范围](docs/IOS-SELFSIGN-AND-LOOPBACK.md#当前兼容性与续签方式)。

### 2. 设置 SideStore / LiveContainer

进入续签工具的 **Settings → Connection Config**，选择 **Remote Endpoint** 连接方式，按下表配置。不同版本的设置入口可能位于 **Developer Options** 中。

| 设置项 | 推荐值 |
| --- | --- |
| Use Local VPN | **关闭（OFF）** |
| Device IP | `10.7.0.1`，与 TUN 的 `loopback_address` 保持一致 |
| RemotePair Port | 留空，使用自动发现 |
| Reachable | 应显示 `Yes` |

使用本项目的回环时，**必须关闭 Use Local VPN**，避免进入 Local VPN 自动检测模式。RemotePair 端口优先使用自动发现，不建议固定为 `49152`，实际端口可能变化。

设置完成后，在 SideStore 的 **My Apps** 中刷新应用，或在 LiveContainer 中执行相应的续签操作。**Reachable 显示 `Yes` 只表示连接可达，仍需实际完成一次刷新，确认续签成功。**

### 3. 续签失败时检查

1. 确认 JiejieBox VPN 已连接，本地签名回环已生效，有效 TUN 配置包含 `"loopback_address": ["10.7.0.1"]`。
2. 确认续签工具的 **Use Local VPN** 已关闭，**Device IP** 与回环地址一致。
3. 在 **Settings → Experimental Features → Network Discovery** 中查询 `_remotepairing._tcp.`，核对实际 RemotePair 端口。自动发现失败时，可手动填写查询到的端口。
4. 检查配对文件是否有效，并确认与当前设备、系统及 SideStore 版本匹配。
5. 使用 IPv4/IPv6 双栈配置时，检查 SideStore / LiveContainer 的 IPv6 连接设置。SideStore 版本若提供 **Developer Options → Connection Config → Accept IPv6 Config**，可开启后保留 TUN IPv6 再重试；出现 `DeviceEndpointNotInitialized` 时也应检查这一项。

本地回环负责续签工具与设备之间的连接路径，不会替代所需的配对文件、签名和续签流程。更多设置见[本地签名回环说明](docs/IOS-SELFSIGN-AND-LOOPBACK.md#打开本地签名回环)。

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

项目维护 sing、sing-tun、sing-quic、quic-go、uTLS、wireguard-go、AnyTLS、Mieru、cronet-go 等依赖 fork，将协议改动延伸到 socket、TUN、QUIC 和原生引擎。构建固定 Go 模块、客户端及原生库来源，测试覆盖真实载荷、协议互通、网络切换、取消关闭、缓冲区交接与故障注入。

依赖与主内核的协作包括：

- **sing / sing-tun**：缓冲区所有权、并行拨号、批处理、GSO/UDP 回包、队列字节预算与等待唤醒。
- **sing-quic / quic-go / uTLS**：数据报与混淆边界、ACK-only pacing、QUIC 创建接点，以及 ClientHello 预设和传输参数的衔接。
- **wireguard-go / AnyTLS / Mieru**：WG/AWG 并发唤醒、session 创建与复用分开计费、underlay 附着与关闭协调。
- **Cronet 与客户端**：原生绑定、ABI、来源锁与配套消费版本；构建记录区分 Go 层、原生产物和各平台客户端。

[关联仓库](FORK_REPOSITORIES.md) · [依赖整合](docs/fork/UPSTREAM-DEPENDENCY-INTEGRATION.md) · [测试范围与验收记录](KNOWLEDGE/ACCEPTANCE.md)

## 文档

- **配置与使用**：[配置参考](docs/configuration) · [扩展配置示例](docs/FORK-CONFIGURATION.md) · [出站 Provider](docs/configuration/provider/index.zh.md) · [NXDOMAIN](docs/DNS-NXDOMAIN.md) · [本地回环](docs/IOS-SELFSIGN-AND-LOOPBACK.md#打开本地签名回环)
- **构建与发布**：[构建档位](docs/BUILD-PROFILES.md) · [Apple TestFlight](docs/APPLE-BETA-RELEASE.md) · [更新日志](CHANGELOG.md)
- **性能与设计**：[链路实现说明](docs/ARCHITECTURE-AND-NETWORK-PATH.md) · [性能对照](docs/PERFORMANCE-COMPARISONS.md) · [性能参数](KNOWLEDGE/PERFORMANCE-FACTORS.md) · [Fast Path 与内存管理](docs/FAST-PATH-AND-MEMORY.md) · [工程架构评估](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)
- **开发维护**：[Fork 差异](docs/FORK-DIFF.md) · [知识库](KNOWLEDGE/README.md) · [任务索引](KNOWLEDGE/INDEX.md) · [维护指引](KNOWLEDGE/REFRESH.md)

开发及 AI 助手请遵循本页开头的[阅读顺序](#ai-与维护者阅读指引)，并在更新实现时同步检查配置页及其入口引用。

- [Release 构建指引](docs/RELEASE-WINDOWS.md)：在 Windows 发布并验收内核正式版，随后构建、发布并验收 Satelite 安卓客户端。
- [TF 构建指引](docs/TESTFLIGHT-MAC.md)：内核正式版发布并验收后，在 Mac 构建并上传 TestFlight。

## 致谢与许可

感谢 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 及相关上游项目和贡献者。本项目采用 [GNU GPL v3.0 或更新版本](LICENSE)。
