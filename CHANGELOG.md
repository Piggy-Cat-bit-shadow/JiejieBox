# JiejieBox 更新日志

按正式版本倒序记录主要改动。历史实验、互通和 CI 结论仅适用于各自记录的提交、平台与测试条件。配置字段的当前用法请参阅[扩展配置示例](docs/FORK-CONFIGURATION.md)。

[正式发布与下载](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [完整历史明细与实验记录](docs/archive/2026-10-dev-cycle/CHANGELOG-DETAIL-2026-11-01.md) · [工程架构与改造评估](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)

## [v0.1.36](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.36) — 2026-11-01

Windows 启动 Apple 构建后，Mac 可以跟随同一次运行提前获取 Libbox、开始 TestFlight 归档。版本、核心提交、客户端源码与正式 Release 的产物一起固定，上传前再检查标签和相应平台的验证结果。

- Apple 构建先记录版本、核心与客户端提交；Libbox 就绪后，Mac 即可下载并并行归档。
- TF 上传前核对标签和所选平台的 CI；正式 Release 使用同一次运行和重跑次数的产物。
- 单平台发布不再准备另一个平台；同提交的在途构建可复用，发布仓库明确取自 origin。

## [v0.1.35](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.35) — 2026-11-01

本版整理 Apple 和桌面客户端的源码入口，修复 Apple 构建中的链接处理，并更新项目文档。相对 v0.1.34，内核没有 Go 源码改动。

- 修复 Apple 源码链接检查，同时识别仓库旧名和现名；桌面 Dashboard 同步源码入口。
- 更新客户端与 ABI 固定点，并整理首页、配置示例和知识库。
- 相对 v0.1.34，内核 Go 源码没有改动；本版包含多平台内核、Libbox 和 Windows 安装包。

## [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34) — 2026-10-29

本版统一配套客户端的订阅请求行为，并整合 Apple 客户端的源码选择、构建和发布流程。

- 订阅请求使用固定的 `sing-box/1.15.0` User-Agent；用量和到期信息依赖服务端返回有效的 `Subscription-Userinfo`。
- 同步 Apple、Android、桌面客户端与 uTLS 固定版本，修复 Apple 端点关闭及构建接线。
- 增加单 macOS runner 验证路径，修复共享构建迁移后的检查位置和临时目录问题。

## [v0.1.33](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.33) — 2026-10-28

这是与 satelite-one v0.1.33 配套发布的内核版本，主要完成版本标识与客户端配套。

- 更新内核版本标识，与 satelite-one v0.1.33 配套发布。

## [v0.1.32](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.32) — 2026-10-28

本版让 endpoint 本地产生的流量通过 TUN 栈处理，并更新配套客户端，便于沿用内核的流量处理和日志上下文。

- endpoint 本地产生的流量通过 TUN 栈处理，并共享流量日志上下文。
- 更新 Apple 客户端引用和模块依赖，纳入 TUN MTU 限制相关修改。

## [v0.1.31](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.31) — 2026-10-28

本版集中处理内核审计发现的问题，覆盖网络接口变化、TUN 与 FakeIP、代理组、TLS / QUIC、嗅探和连接生命周期。

- 修复网络接口更新、TUN 重置交接与 DNS 劫持的启动和关闭边界。
- selector、URLTest、loadbalance 向选中成员传递完整候选地址；一条流不会因逐地址尝试而反复选组。
- 不支持 ECH 的 uTLS 指纹在配置阶段明确拒绝；同时修复 Naive、TUIC、规则集和资源释放边界。
- 补充嗅探、连接关闭、配置生效和数据完整性的回归检查。

## [v0.1.30](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.30) — 2026-10-28

本版修复双栈选路、macOS 网络恢复和连接关闭中的数据保留问题，也补充了相应回归检查。

- 统一流和数据包路径的地址族策略，规范化 IPv4 映射地址，并调整 AnyTLS 代理服务器的地址选择。
- 可信 FakeIP 域名已经存在时，避免继续等待分片 QUIC 嗅探。
- 处理 TLS close-notify 后的已读数据、CommandServer 所有的 Box 关闭，以及 macOS 唤醒网络重置。

## [v0.1.28](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.28) — 2026-10-27

本版调整网络交接的状态发布顺序，并完善旧连接的清理，减少切网时新旧状态交错。

- 网络交接先发布默认网络状态，再唤醒等待操作；跨接口切换时清理旧连接。
- UDP 收到返回数据才构成存活证据；Apple 路径监控跟随活动接口。

## [v0.1.27](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.27) — 2026-10-26

本版修复双栈 DNS 对空结果与查询失败的区分，避免一个地址族没有记录就掩盖另一地址族的结果。

- 某个地址族没有记录时，不再直接把整个双栈解析判为失败；保留另一地址族的有效结果。
- 增加空结果与查询错误交错的回归检查。

## [v0.1.26](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.26) — 2026-10-25

本版更新默认 Dashboard 的来源和品牌界面，并加入来源回归检查，防止后续合并时无意恢复旧界面。

- 默认 Dashboard 切换到本项目维护的版本，展示品牌与订阅配额。
- 验证真实归档的目录裁剪、入口页面、静态资源和 ETag，避免 HTTP 200 但页面空白。
- 同步 sing、uTLS 与 Apple 客户端引用，检查主模块和测试模块的版本一致性。

## [v0.1.25](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.25) — 2026-10-25

本版结合上游修复和六个底层依赖 fork 的整合，处理 DNS、Tailscale、TUN、路由、低内存与资源回收之间的兼容和生命周期问题。

- 整合六个底层 fork 与上游相关修复，核对主模块、测试模块和实际构建使用的依赖版本。
- 修复 DHCP 搜索域、systemd-resolved、Tailscale 短名称与 DNS 缓存过滤。
- 调整 TUN、路由、MASQUE、WireGuard、UDP 批处理与低内存资源管理。
- 完善关闭屏障、并行拨号取消和内存回收；显式 `fast_path: false` 优先于自动授权。
- 快速路径按入口是否能够消费 bypass 等真实能力判断；平台名称本身不能证明发生绕行。

## [v0.1.24](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.24) — 2026-10-22

本版改善 Direct TCP 接管和半关闭后的存活观察，让对端离开后的资源可以回收，同时保留连接交接的原有职责。

- Direct TCP 被接管后没有普通复制循环，不能依靠复制方向退出触发半关闭观察。
- 复用 TCP keepalive 观察对端失效，通过原有 detach、release 和统计关闭链回收残留流；不增加抢读数据的观察者。

## [v0.1.23](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.23) — 2026-10-21

本版收紧 ShadowTLS 认证前连接和握手目标的资源边界，避免异常连接长期占用资源。

- 为 ShadowTLS 认证前读取设定超时，降低异常连接长期占用资源的风险。
- 区分握手目标的真实超时与普通探测，释放异常路径残留连接，并保留包装后需要的能力接口。

## [v0.1.22](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.22) — 2026-10-21

本版增加可选的 Naive H3 到 H2 回退，并改进 MASQUE 数据报、TUN 回包、启动校验和协议关闭路径。

- Naive 可显式启用 H3 到 H2 回退；后续尝试保留剩余时间预算，CONNECT 握手完成后才交付连接。
- MASQUE CONNECT-UDP 接入拥有数据包所有权的发送路径；完善隧道、SSH 会话和作用域关闭。
- 优化 TUN 回包分配并补充启动校验；回退字段及其适用条件见历史明细。

## [v0.1.21](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.21) — 2026-10-20

本版引入 Mieru 入站和出站及可选的 TCP / UDP 回退，同时推进 IPv6 入口、跨协议故障识别、MASQUE、DNS、规则集和数据路径修复。

- 基于官方 Mieru SDK 增加入站和出站，接入 DNS、Dialer、Listener、路由和连接生命周期。
- 可选 TCP / UDP 回退作用于新连接，并包含冷却、退避、恢复探测与网络代次隔离。
- 引入自适应 IPv6 入口和跨协议 FailureFacts，协助代理组区分路径故障、目标错误与取消。
- 修复 MASQUE、HTTP/3、DNS、QUIC、Hysteria2、TUIC 和 TUN 边界，优化规则集加载及数据包所有权。
- 历史条目保留当时的互通、性能测试条件和已知限制；不能据此推断所有平台均已验证。

## [v0.1.20](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.20) — 2026-10-18

本版整理单条流从路由、解析到拨号的地址族准入：为一次执行捕获一致的策略，并让流与数据包路径遵守同一网络事实。

- 每条流捕获一份候选准入策略，用于域名与字面地址、串行与并行拨号以及 UDP 路径。
- 修复未连接 UDP 分支遗漏策略、串行分支重复捕获和取消后迟到候选的问题。
- 增加地址族策略矩阵、网络变化与取消交错测试，并修复测试夹具中的竞争。

## [v0.1.19](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.19) — 2026-10-17

本版减少设备休眠期间不必要的 DNS 和 Dashboard 网络活动，让后台工作遵守设备状态。

- DNS 后台刷新和 Dashboard 下载根据设备休眠状态延后，减少无业务时的网络唤醒。
- 修复 DNS 休眠状态来源的初始化顺序。

## [v0.1.18](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.18) — 2026-10-17

本版处理复杂代理链和网络切换中的能力传递、双栈候选、路由与资源归属，避免一层的状态被误用于另一层。

- 限制代理链能力读取的跳点边界，避免将出站能力误当作底层拨号器能力。
- 一次候选计划只捕获一次网络归属，统一双栈策略并修复被排除候选重新加入的问题。
- 改进规则集重载、更新器与关闭交接，区分嗅探未运行、未匹配和需要更多数据等状态。

## [v0.1.17](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.17) — 2026-10-17

本版保存和识别退役 FakeIP 地址范围，并修复 Windows 客户端安装与旧服务清理中的问题。

- 持久化退役 FakeIP 范围，并按实际签发游标约束，避免旧占位地址被误发给代理对端。
- 修复 Windows 客户端安装身份、注册表视图、工作目录和旧服务清理；调整 Apple 发布源码选择。

## [v0.1.16](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.16) — 2026-10-17

本版汇总 v0.1.10 后的多项改造：本地 DNS 广告拦截、双栈与网络状态修复、XHTTP 性能和故障验证，以及 Windows 安装与共存处理。

- 新增 DNS `adblock` 动作：规则命中后本地返回带 SOA 的 NXDOMAIN，并区分本地拦截与上游错误。
- 修复双栈监听、网络能力、FakeIP、DNS 缓存和设备休眠状态的多个交错问题。
- 完成 XHTTP 性能重构及真实故障、数据完整性和长时负载验证；结果须按当时实验条件阅读。
- 完善 Windows 客户端共存、订阅、安装卸载，以及 Apple 构建与源码选择。

## [v0.1.10](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.10) — 2026-10-15

本版修复透明代理组对 Direct Fast Path 授权的传递，并完善 Windows 守护进程的信任检查。授权传递后仍需逐流判断。

- 透明选择组包装 direct 时保留快速路径的固有授权，但仍执行 FakeIP、绑定、统计等逐流资格检查。
- 调整 Windows WinTrust 时间错误处理，并覆盖篡改、未签名、证书用途及签名者边界。
- 这次修复不将具有额外拨号或重试职责的复杂组一律视为透明组。

## [v0.1.9](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.9) — 2026-10-14

本版将发布资产清单、草稿发布和完整性检查的顺序收拢，减少正式页面出现缺失或错误资产的机会。

- 以统一清单检查公开资产，先创建草稿并完成上传与核对，再公开 Release。
- 发布参数和最终资产集合由同一份契约约束，减少漏传与提前公开。

## [v0.1.8](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.8) — 2026-10-14

本版为 selector 和 URLTest 增加可选的 `fast_path` 策略，并完善 Apple 客户端源码选择和多平台构建。策略允许尝试不等于流量已经发生内核绕行。

- selector 与 URLTest 增加可选 `fast_path`：`true` 允许符合条件的流尝试，`false` 明确禁止，未设置时采用自动策略。
- 组级授权不会把代理节点改成直连，也不会取代 FakeIP、跟踪和其他逐流判断。
- 完善 Apple 客户端来源选择和多平台构建；这是 JiejieBox 扩展字段，需使用对应内核。

## [v0.1.6](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.6) — 2026-10-12

这是一次贯穿入站、路径证明、代理链、协议传输和客户端的综合改造。XHTTP、REALITY、DNS、MTU、网络恢复与资源生命周期共同纳入验证。

- 沿真实 TUN 路径验证 TCP Reset、黑洞、UDP 过期与重建、IPv4 分片和 ICMP 包过大反馈。
- 接入 XHTTP 客户端的四种模式、HTTP 版本和 XMUX 生命周期；完善 REALITY / VLESS 握手与兼容路径。
- 引入 DNS 分组、代理组有限故障切换和启动期路径检查，处理嵌套隧道 MTU 与解析归属。
- 统一网络代次、恢复租约和资源所有权，同时衔接休眠、网络切换、Libbox 与移动端客户端。
- 补充嗅探、FakeIP、TLS / QUIC、协议互通及关闭交错检查；扩展字段与当时的验证范围见历史明细。

## [v0.1.5](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.5) — 2026-10-07

本版引入结合设备状态与真实业务流量的低功耗管理，减少锁屏和后台阶段不必要的周期性工作，并区分活跃连接与可回收资源。

- 设备状态和真实业务流量共同驱动低功耗策略；业务活动可推迟进入深度空闲。
- 休眠期间延后规则集刷新与统计写入，恢复时分批执行后台工作。
- 屏幕关闭不直接清空连接池；按空闲条件回收资源，并修复过期回调影响新状态的问题。

## [v0.1.4](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.4) — 2026-10-07

本版改进网络切换后的分级排空、重连和 DNS 复用，分别处理较轻的路径变化与持续的网络中断。

- 区分路径变化与持续中断，分级排空旧连接；设备暂停不自动等同于破坏性回收。
- 限制恢复时的重连并发，将 UDP 返回活动纳入存活判断，减少集中拨号和无意义唤醒。
- 限制 DNS 串行传输重复建连，并同步 TUN Forward NAT 的映射与分片修复。

## [v0.1.3](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.3) — 2026-10-07

本版围绕上传调度、业务分类、负载均衡、URLTest、双栈 DNS 与拨号和 Direct Offload 展开，同时完善移动端低内存配置。

- 引入可配置的用户态上传调度与 `traffic_class`；未配置速率时不建立整形队列。
- 新增轮询、一致性哈希、粘性会话等负载均衡策略，并完善 URLTest 健康记录与状态码配置。
- 改进 DNS 双栈增量解析、NXDOMAIN 负缓存、网络切换和移动端低内存处理。
- Direct Offload 分别检查路由资格与 socket 语义；实验数字只适用于当时的测试条件。

## [v0.1](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1) — 2026-09-30

首个正式版本以 Native Naive、MASQUE / CONNECT-IP、共享 HTTP/3 与数据包路径、SOCKS 代理链为重点，同时恢复上游协议注册、CLI 和 Clash API 能力。

- 提供 Native Naive 认证 CONNECT、Padding / Framing、UoT 与可选认证前资源限制。
- 完善 MASQUE 的 CONNECT、CONNECT-UDP、CONNECT-IP、DNS 分配、路由通告和 H3 生命周期。
- 改进数据报所有权、缓冲区与复制路径；SOCKS5 可选预连接仅作用于相应 TCP CONNECT 场景。
- 恢复完整上游协议注册、CLI 和 Clash API，并补充真实隧道及资源释放检查。
