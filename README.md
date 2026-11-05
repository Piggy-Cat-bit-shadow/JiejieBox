# JiejieBox

基于 [sing-box](https://github.com/SagerNet/sing-box) 持续开发的跨平台代理内核，扩展 XHTTP、Mieru、VLESS 加密与 Native Naive，优化 DNS、双栈拨号、链式代理、网络恢复及资源管理，并提供配套桌面端和移动端集成。

[下载](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [更新日志](CHANGELOG.md) · [Android 客户端](https://github.com/Piggy-Cat-bit-shadow/satelite-one) · [配置文档](docs/configuration) · [问题反馈](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/issues)

## 客户端与命令行内核

| 平台 | 获取方式 |
| --- | --- |
| **Windows x64** | Release 提供桌面客户端安装包和独立 `windows-amd64` 命令行核心。 |
| **Android arm64** | 使用 [satelite-one 客户端](https://github.com/Piggy-Cat-bit-shadow/satelite-one)。本仓库的 `android-arm64` 附件为 AAR 开发集成包。 |
| **Linux amd64** | Release 提供命令行核心；部署见 [Linux 服务端文档](docs/JIEJIE-SERVER.md)。 |
| **macOS arm64** | Release 提供命令行核心；参见 [macOS 运行说明](docs/JIEJIE-MACOS-CLIENT.md)。GUI 与 TestFlight 构建见 [Apple 发布说明](docs/APPLE-BETA-RELEASE.md)。 |
| **iOS / iPadOS arm64** | Release 提供完整未签名 IPA，支持自行签名安装；TestFlight 见 [Windows 构建 → Mac 签名与上传指南](docs/APPLE-BETA-RELEASE.md)。 |
| **Apple 开发集成** | Release 提供 Libbox；iOS 与 macOS 客户端使用固定源码和经过来源校验的库。 |

按 Release 附件选择平台与架构，并核对 `SHA256SUMS`。Android AAR 不可直接作为 APK 安装，iOS 未签名 IPA 需自行签名；macOS DMG 是否提供以相应版本附件为准。

**Apple TestFlight 构建**：在 Windows 发起 Apple Actions，Mac 通过 [`scripts/publish-apple-beta.sh`](scripts/publish-apple-beta.sh) 跟随指定运行，获取同次 Libbox 并完成签名、归档与上传。支持 iOS、macOS 或同时构建；Mac 需配置 Apple 签名与 App Store Connect。具体参数和操作步骤见 [TestFlight 构建说明](docs/APPLE-BETA-RELEASE.md)。

## 协议与网络能力

### XHTTP

- 支持 `packet-up`、`stream-up`、`stream-one`、`auto` 四种客户端模式，覆盖 HTTP/1.1、HTTP/2 和 HTTP/3。
- 支持 X-Padding、上传分块与 XMUX 连接复用，传递拨号取消和 deadline。
- packet-up 使用有界队列与单一 flusher 聚合写入，保留首包低延迟、顺序、背压及尾部数据完整性。
- 连接池接入网络切换、休眠恢复和空闲清理，处理上传拒绝、H3 故障与关闭边界。

VLESS 将空闲清理和复用退役请求传递给 XHTTP，再由 XMUX 区分空闲物理连接与活跃逻辑流：内存整理只释放空闲项，唤醒后的可疑连接停止承接新流，已有流按契约排空。单流重置保留其错误范围，避免误将其他复用流一并关闭。上传聚合与 HTTP 传输的连接数、取消和响应消费共同维护有界背压。

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

SDK 的 listener/dialer 桥接保留内核的 socket 策略，适配层在自己拥有的拨号边界记录错误，避免 SDK 文本包装丢失类型后无法判断失败来源。启用回退时，TCP 与 UDP 分别持有运行时和会话，选择器管理偏好与探测；接口变化通过新运行时启动、代次交接和旧资源清理完成重建，防止迟到拨号修改新网络状态。

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

REALITY 的密钥份额、Verifier 与 uTLS 配置克隆需要贯通真实握手；VLESS 应用层加密、Vision 与 TLS 则分别维护自己的帧和数据状态。项目核对连接包装后的缓冲区交接、半关闭及专用写入能力，并对 Vision 使用的依赖内部布局设置兼容检查，避免升级后编译通过却破坏数据路径。

XHTTP 和 VLESS 加密扩展以客户端为主，不包含对应服务端实现。参见 [配置示例](docs/FORK-CONFIGURATION.md)及 [传输模块](KNOWLEDGE/modules/xhttp-client-transport.md)。

### Native Naive 与 HTTP 服务端

- 原生 Naive 入站支持认证 CONNECT、padding、UoT v1/v2、伪装响应和认证前资源限制。
- 对 UoT 非固定目标会话实施逐数据报目标检查，保留认证身份与路由上下文。
- HTTP/2、HTTP/3 提供并发流、流控窗口与请求阶段限制，处理非法 CONNECT、短写和关闭边界。
- 客户端使用自定义 Cronet fork，保留 host dialer、可配置窗口及可选 H3→H2 回退。
- Chromium 154.0.8037.49 已吸收 NaiveProxy patch5 相关证书封锁、QUIC 窗口与自动调节修复，并修复 Linux DNS 桥的匿名 socketpair 回复。

Cronet 通过 host dialer 接入本项目的服务器解析和双栈策略，Go 与原生侧共同处理连接、异步缓冲区及回调生命周期。出站完成 CONNECT 握手后再返回连接，并将可确定的认证或拨号失败传给代理组；H3→H2 回退区分传输故障、HTTP 响应与调用方取消。服务端 padding 的就地构造与 UoT 目标检查分别遵守帧容量和逐包目标语义。

能力由实际构建标签决定：Linux 默认服务端档包含 Naive 入站，未启用 Cronet 出站；Windows 使用 `with_purego` 加载原生库。参见 [Naive 说明](docs/naive.md)、[HTTP 传输模块](KNOWLEDGE/modules/naive-http-transport.md)和 [Cronet 修复记录](KNOWLEDGE/reports/release/cronet-patch5-linux-dns-2026-11-05.md)。

### MASQUE / CONNECT-IP

- 完善 CONNECT、CONNECT-UDP 与 CONNECT-IP 数据面，处理地址分配、路由通告、DNS_ASSIGN 和 PREF64。
- 区分连接代理服务器的 Bootstrap DNS 与隧道内部解析，支持已有 H3 连接上的 DoH 及解析状态快照。
- 在 QUIC 握手阶段竞争候选，回收落败连接；H3 建连保留独立尝试窗口，为 UDP 黑洞下的 H2 回退预留时间。
- 优化 DATAGRAM 所有权与批处理，限制 Capsule 长度和条目数量；按已知下层容量处理包大小与 ICMP Packet Too Big。

CONNECT-IP 将远端分配的地址、路由与内部 DNS 同本地 endpoint/TUN 状态衔接；Bootstrap DNS 始终负责建立外层连接。数据报在 HTTP/3、QUIC 和 TUN 边界明确转移所有权，部分发送成功后仅回退或释放仍由调用方持有的部分。H3 故障判断区分 QUIC 建连失败和 CONNECT 响应缓慢，避免一次应用层延迟影响后续传输选择。

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
- **HTTP / TLS 嗅探**：通过有界头部检查提前排除不匹配输入，TLS 取到 ClientHello 后停止额外握手处理；已读字节继续用于后续转发。
- **QUIC 分片嗅探**：补充读取只重试需要更多分片的 QUIC 解析，限制轮数和工作副本范围；可信 FakeIP 域名满足条件时提前结束等待，错误或放弃时归还缓冲区。

详见 [入口与数据路径](KNOWLEDGE/modules/data-path-optimization.md#入口嗅探与系统代理路径)及 [嗅探实现](KNOWLEDGE/modules/sniff.md)。

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

配置见 [上传调度与 Fast Path](docs/FORK-CONFIGURATION.md)，实现范围见 [数据路径优化](KNOWLEDGE/modules/data-path-optimization.md)和 [Fast Path 设计](docs/FAST-PATH-AND-MEMORY.md)。

### 回包、复制与连接关闭

- **TUN 回写**：常见单包写回复用调用方切片，减少临时切片与对象；批量数据报按下层容量分块提交，仅对尚未交接的部分回退和释放。
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

较小档位降低并发流的工作集，同时改变单次读取容量和加密构帧边界。协议按实际头尾预留空间计算载荷，确保缓冲区可归还池中，避免边界溢出或逐包重新分配；这是编译期选择，不能通过运行配置切换。历史每流驻留对照见下方性能表。

### 减少分配与长期驻留

- **大规则集**：受限扩容减少一次性大数组和反复增长；无 `cache_file` 的远程二进制 SRS 直接解析下载流，省去整份下载副本，并保留截断、解压预算和发布前完整性检查。iOS 还使用规则集 mmap 路径，其他平台按自身实现处理。
- **QUIC 接收内存**：乱序小帧在底层数组容量明显大于有效数据时收缩复制，尽早归还大池缓冲；QUIC / HTTP/3 数据报出队时清空原槽位，避免队列已空却继续引用已交付载荷。
- **在途数据**：嗅探续读、XHTTP 上传队列、Capsule 解析和批量数据报均按各自上界处理；首包与数据报只在生命周期允许时借用或转移，失败、取消和关闭时释放仍由本层持有的部分。
- **按需运行**：轻量出站避免仅因配置存在就建立完整网络会话，复用连接按需创建；Mieru 未使用前不因接口通知重建 SDK Runtime。重型 endpoint 的启动成本按协议分别处理。

### 分层回收与平台预算

- **先清缓存，再清空闲池**：依次尝试过期条目、空闲连接资源，收益足够即停止；不足时才根据压力和 runtime 可回收页面决定强制 GC / 归还页面。低收益整理进入冷却，减少反复 GC。
- **共用回收执行器**：空闲整理、系统压力与危机回收串行协调，紧急请求优先；真正重置网络前重新检查当前压力，避免已过期的危机请求打断恢复后的连接。
- **普通整理保留业务**：`TrimMemory` 清理空闲项，不重置网络或主动拨号；严重压力下的 `ReleaseMemory` 可执行更强释放与网络重置。回收遍历覆盖出站、endpoint、DNS 传输和 HTTP 客户端池。
- **平台分别治理**：Apple 同时观察进程 footprint 与 Go runtime 软限制，计入原生侧内存的差异；Android 按系统 trim 等级处理，界面隐藏本身不触发内存回收。关闭与重载等待已接受的回收结束，丢弃旧实例待执行请求。

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

**当前仍存在兼容性问题**：根据现有真机反馈，WLOC 的开发者模式虚拟定位可正常使用，但使用本项目回环进行 **SideStore 续签仍有问题**。续签时请切换至 **LocalDevVPN** 建立本地隧道，再回到 SideStore 刷新应用，操作参见 [SideStore 官方指南](https://docs.sidestore.io/docs/installation/install)。

上述结果仅对应已反馈的设备与使用场景。默认 `go` 栈支持 TCP、UDP、ICMPv4/v6 反射；其他显式栈尚无完整同等实现。配置与排障见 [本地回环说明](docs/IOS-SELFSIGN-AND-LOOPBACK.md#打开本地签名回环)。

## 性能与内存对照

以下为历史基准与受控实验。表中分别标明负载和测量层级，完整日期、基线、对照及复测方法见 [性能专页](docs/PERFORMANCE-COMPARISONS.md)。

| 场景 | 优化前 | 优化后 | 条件 |
| --- | ---: | ---: | --- |
| 上传拥塞下交互消息 p99 | 146 ms | 9 ms | 4 条普通流竞争约 2 MB/s FIFO，按容量 85% 持续整形 |
| 持续上传后首条交互消息 p50 | 127 ms | 2 ms | 同组整形实验 |
| 每流活跃缓冲区驻留 heap | 64.1 KiB | 32.1 KiB | 默认与 low-memory 构建的缓冲区基准 |
| 百万域名加载累计分配 | 42.5 MiB | 20.6 MiB | 同机同样例；加载 GC 由 10 次降至 5 次 |
| 10 万域名加载累计分配 | 4.1 MiB | 2.9 MiB | 有界几何扩容 |
| 无缓存远程 SRS 加载累计分配 | 21,229,120 B | 约 125 KB | 有效规则附加 4 MiB 非压缩尾部，保留完整性检查 |
| TUN 单包回写 | 约 236–238 ns、72 B、2 次分配 | 约 156 ns、0 B、0 次分配 | dispatcher + portNAT 微基准 |
| 非 HTTP 输入经过 HTTP 嗅探 | 4,889 B、9 次分配 | 205 B、3 次分配 | DNS-over-stream 样例 |
| CONNECT-UDP 包装边界 | 409–428 ns、1345 B、2 次分配 | 83–85 ns、64 B、1 次分配 | 1200 B 载荷，owned 路径微基准 |
| CONNECT-UDP 实际 quic-go 连接 | 10.6 µs/包 | 9.5 µs/包 | 1000 B、5000 次发送 |

XHTTP 与 Mieru 的实现对照见上方协议章节。各项数据不构成所有网络或当前版本的统一性能保证；吞吐、累计分配、稳态内存与设备 footprint 应分别测量。

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

## 配置、依赖与验证

常规配置以已纳入的 sing-box `v1.15.0-alpha.13` 为参考，后续修复按补丁语义选择性适配。当前 Naive 引擎为 Chromium `154.0.8037.49`，包含 NaiveProxy patch5 相关修复；Mieru SDK 为 `v3.39.0`。扩展字段和实际协议能力受平台、构建标签及客户端版本约束。

项目维护 sing、sing-tun、sing-quic、quic-go、uTLS 和 cronet-go 等依赖 fork。根模块、独立测试模块、客户端 gitlink、原生库 provenance 与 source-lock 共同固定构建来源；源码版本、构建成功和已发布附件分别记录。

测试覆盖协议互通、真实载荷、网络切换、取消与关闭、缓冲区所有权、配置校验及故障注入。测试结果按提交、平台和场景记录，设备行为与性能实验保留各自验证范围。参见 [验收索引](KNOWLEDGE/ACCEPTANCE.md)、[关联仓库](FORK_REPOSITORIES.md)和 [依赖整合](docs/fork/UPSTREAM-DEPENDENCY-INTEGRATION.md)。

## 文档

- **配置与使用**：[配置参考](docs/configuration) · [扩展配置示例](docs/FORK-CONFIGURATION.md) · [NXDOMAIN](docs/DNS-NXDOMAIN.md) · [本地回环](docs/IOS-SELFSIGN-AND-LOOPBACK.md#打开本地签名回环)
- **构建与发布**：[构建档位](docs/BUILD-PROFILES.md) · [Apple TestFlight](docs/APPLE-BETA-RELEASE.md) · [更新日志](CHANGELOG.md)
- **性能与设计**：[链路实现说明](docs/ARCHITECTURE-AND-NETWORK-PATH.md) · [性能对照](docs/PERFORMANCE-COMPARISONS.md) · [性能参数](KNOWLEDGE/PERFORMANCE-FACTORS.md) · [Fast Path 与内存管理](docs/FAST-PATH-AND-MEMORY.md) · [工程架构评估](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)
- **开发维护**：[Fork 差异](docs/FORK-DIFF.md) · [知识库](KNOWLEDGE/README.md) · [任务索引](KNOWLEDGE/INDEX.md) · [维护指引](KNOWLEDGE/REFRESH.md)

开发及 AI 助手从 [AGENTS.md](AGENTS.md) 进入，先确认知识库基线与 Git 增量，再按任务查阅模块。

## 致谢与许可

感谢 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 及相关上游项目和贡献者。本项目采用 [GNU GPL v3.0 或更新版本](LICENSE)。
