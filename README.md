# JiejieBox

**基于 sing-box 持续开发的网络代理项目，围绕共享核心、跨协议协作和多平台运行进行系统性改造。**

JiejieBox 面向服务端、桌面端和移动端。在上游协议与路由能力之上，项目持续扩展传输协议，改进 DNS、双栈拨号、代理链、数据路径和资源生命周期，并维护与内核配套的客户端及底层依赖。目标是让这些能力能够一起工作：网络变化时状态有明确归属，发生故障时能找到原因，性能优化也不以丢失协议语义为代价。

[下载正式版](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [更新日志](CHANGELOG.md) · [扩展配置示例](docs/FORK-CONFIGURATION.md) · [知识库](KNOWLEDGE/README.md) · [反馈问题](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/issues)

**配置兼容性：** 当前上游基线包含 sing-box `v1.15.0-alpha.13`，常规配置语法以该版本为参考。实际可用功能仍取决于平台、构建标签和上游弃用规则；JiejieBox 新增字段需要配套内核。详见[配置参考](docs/configuration)和[扩展配置示例](docs/FORK-CONFIGURATION.md)。

## 获取与运行

| 平台 | 获取与使用 |
| --- | --- |
| Windows | 从 [Releases](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) 下载客户端安装程序或 `windows-amd64` 内核。 |
| Android | 使用 [satelite-one](https://github.com/Piggy-Cat-bit-shadow/satelite-one) 客户端及配套内核。 |
| Linux | 下载 `linux-amd64` 命令行内核，解压后使用配置文件运行；参见[服务端说明](docs/JIEJIE-SERVER.md)。 |
| macOS | 下载 `macos-arm64` 命令行内核；参见 [macOS 运行说明](docs/JIEJIE-MACOS-CLIENT.md)。 |
| iOS / macOS 客户端 | 在 Mac 上准备签名环境，使用 [TestFlight 脚本](scripts/publish-apple-beta.sh)归档和上传；步骤见[发布说明](docs/APPLE-BETA-RELEASE.md)。 |

## 设计与架构

内核的改造涉及网络流经过的多个阶段：入站接收、DNS 与 FakeIP、嗅探和路由、代理组选路、双栈拨号、协议传输，以及网络变化后的恢复和资源释放。不同流量不一定经过所有阶段。项目关注的是这些阶段之间的事实来源、资源归属和交接关系，而不只是某个协议的单独实现。

DNS、拨号、路由、网络状态和生命周期等公共能力由内核共享。协议仍保留各自需要的连接与会话机制：例如，XHTTP 的 XMUX、Mieru 的 SDK Runtime、MASQUE 的 H3 / H2 连接状态各有不同的职责。网络代次和资源退役帮助组件识别过期结果，也不要求一发生网络变化就关闭所有活跃连接。

架构选择、历史演变和验证边界见[工程评估报告](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)、[架构边界](KNOWLEDGE/ARCHITECTURE.md)与[生命周期模块](KNOWLEDGE/modules/core-lifecycle-runtime.md)。报告记录的是其审阅时的源码和实验，阅读当前行为时应结合源码与知识库基线。

## 协议与传输

在 sing-box 原有能力上，本项目持续维护以下扩展与集成：

| 能力 | 主要内容 |
| --- | --- |
| XHTTP 客户端 | 支持 `packet-up`、`stream-up`、`stream-one` 和 `auto`，配合 HTTP/1.1、HTTP/2、HTTP/3 与 XMUX 管理连接和逻辑流。 |
| Mieru | 基于官方 SDK 提供入站与出站，接入内核的解析、拨号和生命周期；TCP / UDP 回退为可选能力，作用于新会话。 |
| REALITY / VLESS | 维护握手兼容、`key_share` 策略和分片路径，并提供 VLESS 客户端应用层加密扩展。 |
| Native Naive | 提供认证 CONNECT、填充、UoT 和伪装响应，支持认证前资源限制；客户端使用相应平台的 Cronet 能力。 |
| MASQUE / CONNECT-IP | 处理 CONNECT、CONNECT-UDP、CONNECT-IP、地址和路由通告、DNS 分配及数据报；Bootstrap DNS 与隧道内解析分别管理。 |

这些能力的可用范围受构建标签、平台和协议配置影响。XHTTP 与 VLESS 加密扩展以客户端为主；详细行为见 [XHTTP](KNOWLEDGE/modules/xhttp-client-transport.md)、[Mieru](KNOWLEDGE/modules/mieru.md)、[Naive](KNOWLEDGE/modules/naive-http-transport.md) 和 [MASQUE](KNOWLEDGE/modules/masque-connectip.md)。

## DNS 与双栈

DNS 不只是把域名换成地址。JiejieBox 将上游选择、缓存、地址族、网络环境和后续拨号放在一起考虑：

- 多上游组支持 `stable`、`fastest` 和 `parallel` 策略；并发查询与后台刷新按条件协调，减少重复工作。
- 允许双栈的情况下，A / AAAA 结果可分别交给拨号器；较慢的地址族不必总是阻塞较快的候选。严格单地址族配置仍然生效。
- 符合条件的 NXDOMAIN 可跨查询类型复用；本地 `adblock` 规则则直接生成负响应，两者各自保留适用边界。
- DNS 缓存、FakeIP 签发和退役、反向映射与网络代次相互配合，避免把旧网络或旧策略的结果当成当前结果。
- 网络能力未知时不会仅凭不完整的信息禁用 IPv6；本机出口能力也不等同于远端代理的目标访问能力。

[NXDOMAIN 细节](docs/DNS-NXDOMAIN.md) · [DNS 模块](KNOWLEDGE/modules/dns-demand.md) · [DNS 广告拦截](KNOWLEDGE/modules/dns-adblock-action.md)

## 代理链、测速与负载均衡

通过 `detour` 可以组合多跳出站，也可以在链中使用 selector、URLTest 或负载均衡组。链式代理的处理覆盖启动校验、目标解析、地址候选、成员选择和故障恢复：

- 启动时检查引用、环和能力要求；区分代理组的控制路径与流量实际经过的物理路径。路径预览不会消耗轮询位置。
- 代理服务器域名与最终目标的解析各有归属。支持该选项的 SOCKS / HTTP 出站可显式启用 `destination_dns_ownership`，默认不改变远端解析方式。
- 地址候选和网络策略可以穿过代理组，避免包装层丢失候选；可选故障切换在嵌套组中共享逐流预算，并结合失败来源判断是否重试。
- URLTest 使用 Mihomo 风格的预热后延迟测量，`expected_status` 支持 `204`、`200-299` 等状态码表达式。这不意味着整份 Mihomo YAML 可以直接作为本项目配置。
- `loadbalance` 支持轮询、一致性哈希和粘性会话，按 TCP 连接或 UDP 会话选择成员，不会把一条连接拆到多个节点。

[链路与启动校验](KNOWLEDGE/modules/path-selection-validation.md) · [负载均衡配置示例](docs/FORK-CONFIGURATION.md#负载均衡与故障重试) · [MTU 与缓冲区](KNOWLEDGE/modules/mtu-and-geometry.md)

## 数据路径、内存与低功耗

TUN、系统代理 / Mixed、嗅探和协议数据面共享一部分路由与缓冲区能力。项目围绕真实路径减少不必要的分配、复制、等待和后台活动，同时维护数据包所有权与关闭时的数据完整性。

- **TUN 与嗅探：** 检查收包、路由、拨号和回包之间的交接；嗅探结果和缓冲区的使用遵循明确的生命周期。
- **Fast Path 与 splice：** 代理组策略、入口是否能消费 bypass、实际连接接管和内核 splice 是不同环节；具备资格不等于已经发生内核绕行。无法证明语义一致时，路径会保守处理。
- **缓冲区与数据报：** 在接收方支持所有权交接时减少复制；为批处理、队列丢弃、关闭和部分失败保留释放责任。
- **低内存与大规模规则：** 借鉴 HAKO、Anywhere 等项目的经验，改进规则集加载、缓冲区大小和空闲资源管理；具体收益取决于构建配置和使用场景。
- **休眠与网络恢复：** 结合设备状态和真实流量减少无业务时的刷新和唤醒；区分空闲连接、仍有业务的连接以及恢复时需要探测的资源。
- **上传调度：** 可显式配置聚合上传速率与流量类别，缓解共享瓶颈下的排队；未配置速率时不应建立不必要的整形队列。

项目记录过规则集加载、低内存缓冲区、TUN 回包和上传排队的受控实验。结果及测试条件见[性能因素](KNOWLEDGE/PERFORMANCE-FACTORS.md)、[数据路径](KNOWLEDGE/modules/data-path-optimization.md)和[内存与 Fast Path](docs/FAST-PATH-AND-MEMORY.md)；它们不能直接代表所有设备和网络。

以下是历史测试中的几个例子，数字只适用于各自的实验条件：

| 测量场景 | 优化前 | 优化后 | 条件 |
| --- | ---: | ---: | --- |
| 上传拥塞下，高优先级小消息的 p99 延迟 | 146 ms | 9 ms | 4 条普通流竞争 2 MB/s FIFO，聚合整形设置为容量的 85%。 |
| 每流活跃缓冲区驻留 Heap | 64.1 KiB | 32.1 KiB | 对比默认构建和 `with_low_memory` 构建。 |
| 百万域名规则集加载累计分配 | 42.5 MiB | 20.6 MiB | 同机、相同样例下比较规则集几何扩容。 |
| TUN 单包、单写回路径的临时分配 | 72 B / 2 次 | 0 B / 0 次 | 在真实 dispatcher 与 portNAT 回包路径上测量。 |

实验记录和限制见[规则集加载](KNOWLEDGE/reports/anywhere-round2/anywhere-round2-final-summary.md)、[回包路径](KNOWLEDGE/reports/anywhere-round3/anywhere-round3-construction.md)和[上传调度](KNOWLEDGE/modules/data-path-optimization.md#b3-the-four-measurements-that-settled-the-design)。

## 客户端、依赖与发布

共享内核通过 Libbox 与 Apple、Android 等平台对接。平台网络状态、服务启动顺序、休眠事件和 ABI 都需要与核心配合；Windows 客户端还涉及本地服务、安装隔离和身份校验。Dashboard 和订阅路径也随配套客户端维护。

项目同时维护 sing、sing-tun、sing-quic、quic-go、uTLS 和 Cronet 等依赖 fork。主模块、测试模块、客户端 gitlink 与发布源码锁分别记录所用版本；某个 fork 的分支头更新，并不自动表示主仓库已经使用该提交。见[关联仓库](FORK_REPOSITORIES.md)和[子仓库改造与固定版本](KNOWLEDGE/modules/child-repositories.md)。

Apple 发布支持 Windows 启动构建、Mac 跟随指定运行提前准备 TestFlight。Libbox、核心提交、Apple 客户端提交和正式发布版本会一起核对；正式 Release 使用对应运行的产物。见 [Apple 发布流程](docs/APPLE-BETA-RELEASE.md)。

## 验证与资料

项目维护配置、协议互通、数据完整性、网络切换、取消与关闭、资源所有权和跨平台构建等测试。测试、CI、互通、性能实验和真实设备验证各自证明不同的事情；历史报告需结合其中标注的提交、平台和环境阅读。

- **使用与部署：** [扩展配置示例](docs/FORK-CONFIGURATION.md) · [服务端说明](docs/JIEJIE-SERVER.md) · [构建配置](docs/BUILD-PROFILES.md)
- **源码与维护：** [知识库索引](KNOWLEDGE/INDEX.md) · [项目架构](KNOWLEDGE/ARCHITECTURE.md) · [CI 与发布](KNOWLEDGE/modules/ci-release.md) · [完整更新日志](CHANGELOG.md)
- **工程评估：** [架构与改造报告](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md) · [性能因素](KNOWLEDGE/PERFORMANCE-FACTORS.md)

感谢 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 和相关上游项目及贡献者。JiejieBox 以 [GNU GPL v3.0 或更新版本](LICENSE)发布。
