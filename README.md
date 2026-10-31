# JiejieBox

**多协议代理内核，专注复杂网络下的连接恢复、智能选路与移动端资源管理。**

JiejieBox 基于 [sing-box](https://github.com/SagerNet/sing-box) 持续开发。在上游协议与路由能力之上，扩展 XHTTP、Mieru、VLESS 加密、DNS 分组和流量调度，并围绕网络切换、休眠唤醒、嵌套代理与低内存场景深入改进。支持服务端部署，也为桌面端和移动端提供共享内核。

[下载与更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [Android 客户端](https://github.com/Piggy-Cat-bit-shadow/satelite-one) · [配置文档](docs/configuration) · [反馈问题](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/issues)

## 客户端

| 平台 | 获取与构建方式 |
| --- | --- |
| **Windows** | 从 [Releases](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) 下载已发布的 Windows 客户端安装程序。 |
| **Android** | 使用 [satelite-one](https://github.com/Piggy-Cat-bit-shadow/satelite-one) 仓库中的 Android 客户端。 |
| **iOS / macOS** | 在 Mac 上使用本仓库的 [TestFlight 发布脚本](scripts/publish-apple-beta.sh) 构建、签名并上传。完整流程见 [Apple TestFlight 构建说明](docs/APPLE-BETA-RELEASE.md)。 |

Apple 平台以 `./scripts/publish-apple-beta.sh` 为入口，使用对应提交的已验证 Libbox，再执行客户端归档、签名和 TestFlight 上传；本地需准备 Apple 签名及 App Store Connect 配置。

## 项目特点

### 丰富的协议与传输能力

在 sing-box 原有协议基础上，持续扩展现代代理协议与组合场景：

| 能力 | 特点 |
| --- | --- |
| **XHTTP 客户端** | 支持 `packet-up`、`stream-up`、`stream-one`、`auto`，覆盖 HTTP/1.1、HTTP/2、HTTP/3，并接入连接复用与网络恢复。 |
| **Mieru 客户端与服务端** | 基于官方 SDK，支持 TCP / UDP 传输及可选的传输故障回退，接入统一 DNS、拨号和连接生命周期。 |
| **REALITY 与 VLESS** | 支持混合密钥交换、可配置的 `key_share`、首包分片，以及 VLESS 客户端应用层后量子加密。 |
| **Native Naive** | 提供原生服务端、填充、UoT、伪装与认证前资源控制；客户端使用对应平台的 Cronet 能力。 |
| **MASQUE / CONNECT-IP** | 支持隧道地址与路由管理，完善 H3 / H2 回退、DNS 分配及数据报处理。 |

协议扩展与内核的路由、DNS、网络切换和关闭流程共同工作，减少多种协议叠加时各自管理状态产生的问题。具体能力由平台和构建标签决定。

### 按连接选路，故障时有边界地恢复

- **三种负载均衡策略**：轮询、一致性哈希和粘性会话，适合分散新连接、保持目标亲和或维持会话习惯。
- **共享健康证据**：负载均衡与 URLTest 复用节点测速记录，减少各自维护一套判断带来的不一致。
- **有限故障切换**：可选启用备用节点重试，多层代理组共享逐流预算，避免一次连接失败触发层层重试。
- **区分故障来源**：结合已确认的连接失败信息，区分本地网络、代理线路、目标拒绝和主动取消，减少健康节点被误判。
- **保留双栈候选**：完整地址候选和网络策略可经过嵌套代理组传递，让另一地址族仍有机会参与连接。

选路以一条 TCP 连接或 UDP 会话为单位，既保持连接稳定，也让新连接能够使用当前可用的线路。

### DNS 可选择稳定、速度或并发

DNS 分组提供 `stable`、`fastest`、`parallel` 三种模式：复用稳定上游、依据近期胜出记录选取上游，或并发查询可用成员。

- 并发冷启动合并竞选，减少重复向全组服务器发送查询。
- 全成员异常时收敛为一次生存尝试，避免断网期间不断放大无效请求。
- 缓存、反向映射和查询结果按网络代次管理，减少 Wi-Fi / 蜂窝切换后旧结果污染新网络。
- DNS 连接复用、记录容量限制和按需刷新共同控制后台连接与内存占用。

### 面向网络切换和休眠唤醒设计

网络变化不只是重新拨号。JiejieBox 对在途请求、旧连接、连接池和新网络状态进行统一协调：

- 切换网络时撤销旧操作的归属，避免迟到的连接与解析结果重新进入新网络。
- 根据切换类型和流量状态排空或回收旧路径，及时处理已失效的数据报会话。
- 为 WireGuard、MASQUE、HTTP 和 XHTTP 等资源接入恢复与回收机制，处理“网络已恢复，旧连接仍不可用”的情况。
- 唤醒时分批恢复后台任务，减少测速、刷新和重连同时启动造成的压力。

### 为移动端减少无效后台工作

核心统一处理设备休眠、屏幕、前后台和内存压力事件，配套客户端负责上报平台状态。

- 休眠期间延后非必要 DNS 刷新、远程规则集刷新和统计写入。
- 后台测速不主动唤醒已经暂停的昂贵网络资源。
- 区分短时休眠、长时间空闲和内存压力，按条件保留可复用连接或释放空闲资源。
- 低内存配置调整缓冲区尺寸和内存管理策略，减少常驻与临时内存开销。

这些机制旨在减少无业务时的网络活动和资源驻留；实际功耗与内存表现取决于设备、配置及业务流量。

### 优化实际数据路径

- **上传流量调度**：按业务类别管理用户态上传，可配置速率整形，减少大流量上传对交互请求的影响。
- **嗅探开销控制**：TLS 识别避免执行完整服务端握手，QUIC 分片读取有界，HTTP 提前排除不匹配输入。
- **减少复制与分配**：改进 MASQUE、QUIC、TUN 等路径的数据包所有权与缓冲区复用。
- **大型规则集加载**：优化扩容和无缓存场景下的流式读取，减少加载期临时分配，并检查截断、校验和及解压边界。
- **Direct Offload**：Linux `auto_redirect` 下，满足语义等价条件的直连流可交给系统路由处理；保留逐流资格检查。

优化也包括容易被忽略的边界：数据包交给下一层后由谁释放、批量发送部分成功时哪些数据不能重发、异步 DNS 查询是否仍引用可复用缓冲区，以及网络重置时尚未建立的连接是否能够取消。项目对这些路径分别维护所有权规则和回归测试，降低性能改动带来的丢包、重复发送与资源泄漏风险。

### 已记录的优化效果

以下为项目历史基准与受控实验记录，展示具体优化解决的问题；并非所有设备和真实网络的统一成绩。

| 场景 | 优化前 | 优化后 | 测量范围 |
| --- | --- | --- | --- |
| 上传拥塞下高优先级小消息的 p99 延迟 | 146 ms | 9 ms | 4 条普通流竞争 2 MB/s FIFO，聚合整形配置为容量的 85%。 |
| 持续大流量上传后，首条高优先级消息的 p50 延迟 | 127 ms | 2 ms | 同组实验中的持续整形路径，观察接收端交付延迟。 |
| 每流活跃缓冲区驻留 Heap | 64.1 KiB | 32.1 KiB | 默认与 `with_low_memory` 编译配置的缓冲区基准。 |
| 非 HTTP 流量经过 HTTP 嗅探的分配量 / 次数 | 4,889 B / 9 次 | 205 B / 3 次 | DNS-over-stream 样例，比较启用提前排除逻辑前后。 |

原始条件及限制见 [上传调度实验](KNOWLEDGE/modules/data-path-optimization.md#b3-the-four-measurements-that-settled-the-design)、[低内存缓冲区](KNOWLEDGE/modules/mtu-and-geometry.md#4-commonbufgeom--the-geometry-contract) 和 [嗅探测量](KNOWLEDGE/modules/sniff.md#7-evidence)。上传调度需要显式配置速率；低内存配置由构建选择。

### 复杂代理链更容易检查和定位

- **启动前路径检查**：识别循环依赖、无效引用、截断路径和部分无法满足的能力要求。
- **区分数据路径与控制路径**：为嵌套代理、代理组和 detour 建立统一的逐跳视图。
- **嵌套隧道 MTU 约束**：根据已知封装开销和下层容量，约束上层 QUIC 数据包，减少包过大导致的连接黑洞。
- **明确错误位置**：改进地址族、嗅探、路径拒绝和连接关闭的诊断，帮助定位“配置能加载但实际不通”的问题。

## 持续验证

项目维护协议互通、真实载荷回环、网络切换、取消与关闭交错、资源所有权和配置边界等测试，并在内核、客户端及依赖 fork 之间固定配套版本。各版本完成的工作与验证范围见 [Release 更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases)。

## 文档

- **使用文档**：[配置参考](docs/configuration) · [服务端说明](docs/JIEJIE-SERVER.md) · [Naive](docs/naive.md) · [MASQUE](docs/masque.md)
- **开发文档**：[构建配置](docs/BUILD-PROFILES.md) · [Fork 差异](docs/FORK-DIFF.md) · [关联仓库](FORK_REPOSITORIES.md) · [知识库](KNOWLEDGE/README.md)

`loadbalance`、`traffic_scheduler`、`traffic_class` 等为本 fork 扩展配置，需使用配套内核与客户端。

## 致谢与许可

感谢 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 以及相关上游项目和贡献者。JiejieBox 基于 [GNU GPL v3.0 或更新版本](LICENSE) 发布。
