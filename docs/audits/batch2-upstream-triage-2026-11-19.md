# 第二批上游 Issue/PR（128 条）分诊：哪些与 JiejieBox 有关，哪些已经被依赖吸收

本页是**分诊**，不是缺陷清单。它回答三个问题，并且只按有证据的部分回答：

1. 这条上游问题所在的代码，是否在 JiejieBox 的构建/依赖图里？
2. 它描述的机制，在当前源码里是否存在（存在才可能复现）？
3. 上游是否仍然开着（开着才可能仍然存在于我们的树里）？

状态标签只用四种：**已核对（有证据）**、**已由依赖吸收（按日期推断，未逐提交核对）**、
**不适用（机制不存在或角色/代码库不同）**、**未核对**。`SKIP`、未运行、未读都不算通过。

材料：`JiejieBox_第二批上游Issue_PR研究清单_2026-11-19.md`（633 行）与
`JiejieBox_第二批上游Issue_PR索引_2026-11-19.csv`（128 行）。批次自述：新增 128 条，已与
2026-11-18 首批 128 条按 URL 去重；P0 38 / P1 90；Issue 103 / PR 25。去重与优先级是批次给的，
本页没有复核。

## 1. 判定的三个闸门

| 闸门 | 怎么判 | 本页怎么标 |
| --- | --- | --- |
| G1 血缘 | 主仓代码（sing-box 一脉，25 条）／`replace` 依赖 pin（sing-tun 11、quic-go 22、mieru 14）／独立代码库（TrustTunnel、hysteria、Xray、mihomo 共 56） | 血缘或依赖 = 适用候选；独立代码库 = 只按"机制"判 |
| G2 机制 | 在 `C:\Deepseek\JiejieBox` 的源码里 grep 到对应机制，并记录 `file:line` | 机制不存在 = 不适用（**结构性**），比"没复现"强 |
| G3 上游状态 | GitHub API 取 `state`/`closed_at`，与 pin 日期比较（sing 2026-11-10、sing-tun 2026-11-16、quic-go 2026-11-16、mieru 2026-11-16） | 关闭早于 pin 日期 = 依赖里大概率已有修复（按日期推断） |

G3 是这次最省力的发现：**128 条里很多是 2024–2025 年的老 Issue，修复早已进入我们 pin 的依赖**。
真正值得动手的是"上游仍开着 + 机制在我们树里"的那一小撮。

## 2. 全量 P0 的上游状态（38 条，2026-11-19 取）

| ID | 上游 | state | reason | closed_at |
| --- | --- | --- | --- | --- |
| JB-STAB-129 | SagerNet/sing-box#4515 | open | - | - |
| JB-STAB-130 | SagerNet/sing-box#4686 | open | - | - |
| JB-STAB-131 | SagerNet/sing-box#4678 | open | - | - |
| JB-STAB-132 | SagerNet/sing-box#4665 | open | - | - |
| JB-STAB-133 | SagerNet/sing-box#4386 | open | - | - |
| JB-STAB-135 | SagerNet/sing-box#4511 | closed | completed | 2026-09-19 |
| JB-STAB-137 | SagerNet/sing-box#3878 | open | - | - |
| JB-STAB-138 | SagerNet/sing-box#4410 | open | - | - |
| JB-STAB-139 | SagerNet/sing-box#4444 | open | - | - |
| JB-STAB-145 | SagerNet/sing-box#3872 | closed | completed | 2026-03-18 |
| JB-STAB-154 | SagerNet/sing-tun#110 | open | - | - |
| JB-STAB-156 | SagerNet/sing-tun#72 | open | - | - |
| JB-STAB-162 | SagerNet/sing-tun#91 | closed | - | 2026-09-16 |
| JB-STAB-169 | quic-go/quic-go#5490 | closed | completed | 2025-12-20 |
| JB-STAB-170 | quic-go/quic-go#5341 | closed | completed | 2025-10-08 |
| JB-STAB-171 | quic-go/quic-go#4843 | closed | completed | 2025-01-08 |
| JB-STAB-172 | quic-go/quic-go#4704 | closed | completed | 2024-10-19 |
| JB-STAB-173 | quic-go/quic-go#3883 | **open** | - | - |
| JB-STAB-174 | quic-go/quic-go#5402 | closed | completed | 2026-01-04 |
| JB-STAB-180 | quic-go/quic-go#6136 | closed | completed | 2026-11-08 |
| JB-STAB-191 | enfein/mieru#297 | closed | completed | 2026-08-17 |
| JB-STAB-192 | enfein/mieru#261 | closed | completed | 2026-04-09 |
| JB-STAB-193 | enfein/mieru#277 | closed | completed | 2026-06-15 |
| JB-STAB-194 | enfein/mieru#264 | closed | completed | 2026-04-18 |
| JB-STAB-205 | HyNetworks/hysteria#1677 | open | - | - |
| JB-STAB-206 | HyNetworks/hysteria#1682 | open | - | - |
| JB-STAB-208 | HyNetworks/hysteria#1510 | open | - | - |
| JB-STAB-220 | XTLS/Xray-core#7398 | open | - | - |
| JB-STAB-221 | XTLS/Xray-core#7443 | closed | not_planned | 2026-11-19 |
| JB-STAB-222 | XTLS/Xray-core#6870 | closed | completed | 2026-10-05 |
| JB-STAB-223 | XTLS/Xray-core#6850 | closed | completed | 2026-09-29 |
| JB-STAB-231 | XTLS/Xray-core#7120 | closed | completed | 2026-10-12 |
| JB-STAB-232 | XTLS/Xray-core#6591 | closed | not_planned | 2026-08-04 |
| JB-STAB-233 | XTLS/Xray-core#5504 | closed | not_planned | 2026-01-08 |
| JB-STAB-242 | MetaCubeX/mihomo#3273 | open | - | - |
| JB-STAB-243 | MetaCubeX/mihomo#3343 | open | - | - |
| JB-STAB-244 | MetaCubeX/mihomo#3411 | open | - | - |
| JB-STAB-245 | MetaCubeX/mihomo#3186 | open | - | - |

读法：**8 条 sing-box P0 仍开着**，而且它们全在网络恢复、DNS 语义、路由/连接生命周期——正是这轮
深度调试反复改动的区域。quic-go 的 7 条 P0 里 6 条在 pin 之前就关了（#6136 关于 pin 前 8 天），
mieru 的 4 条 P0 全部关在 2026-04–08。Xray/mihomo 的开着项属于别人的代码库，只按机制映射。

## 3. 逐仓库结论

### 3.1 sing-box（25 条：10 P0 + 15 P1）——血缘最近，优先

**8 条上游 P0 仍开着，且机制在当前树里都能找到：**

| 上游 | 机制锚点（当前源码） | 判定 |
| --- | --- | --- |
| #4515 / #4686 Tailscale endpoint/listener 不随默认网络恢复 | `protocol/tailscale/` 存在；接口事件消费见 `adapter/lifecycle.go:170` | 适用候选；Android 特定，但"适配器切换后 endpoint 恢复"可在 Windows 上先做 |
| #4678 仅 DNS 变更重置已建 TCP | `route/network.go`、`adapter/network.go:308`（search domain）、`common/networkmanager` | 适用候选；**测试设计**：只改 DNS 服务器顺序，断言现有 TCP 不被重置 |
| #4665 WireGuard endpoint 经 OpenVPN detour 不恢复 | `adapter/endpoint/`、`adapter/outbound/cross_kind_cycle.go` | 适用候选；跨 kind 依赖已有环检测，恢复路径需单独测 |
| #4386 TFO 在域名经 route action 解析时破坏 direct | `common/dialer/default.go:288`（tfo.Dialer）、`:151`（与 bind/network_strategy 的冲突校验） | 适用候选；TFO 与解析后的地址、路由动作三者组合 |
| #3878 Linux TUN + hijack-dns 回环到 127.0.0.1:53 | `adapter/router.go:21` 的 hijack 选项、`protocol/dns/` | 适用候选；需要 netns（可在 WSL 里做） |
| #4410 本地 DNS search-domain 的 NXDOMAIN 抢答 | `dns/transport/local/`（含 `systemconfig/source_darwin.go` 等按平台的 search 列表） | 适用候选；**测试设计**：多 search domain + 受控 NXDOMAIN 时序 |
| #4444 拨号侧域名解析不能走 DNS 规则 | `adapter/dns.go:32`（domain_strategy）、`common/dialer/admission.go:39` | 适用候选；节点域名与目标域名用不同 DNS 规则 |
| #3872 ListenNetworkNamespace 泄漏 fd | `adapter/netns.go`、`cmd/sing-box/cmd_netns_holder.go`、`common/netns/manager_linux.go` | 上游已关（2026-03-18）；**未核对**我们树里是否含该修复 |

另外两条已关的 P0：#4511（macOS 睡眠/唤醒后 WireGuard 握手卡住，2026-09-19 关）与 #3872 一样，
属于"上游修了，我们的快照是否包含"的问题——需要用各自的复现用例反证，不能按日期推断（我们的
sing-box 是 fork，不是上游镜像）。

15 条 P1 按机制分组（不是逐条核对）：DNS race/armed rule（#4358，`dns/` race 逻辑）、
h2mux+TCP Brutal session 失效（#4373，`common/mux`）、interrupt group 关闭顺序（#4329，
`common/interrupt/group.go`）、SRS 长度预分配（#4399，`cmd/sing-box/cmd_rule_set_compile.go`）、
HUP reload 的 TUN 关闭顺序（#4123）、smux+yamux 与 WebSocket early data（#4452）、
Naive inbound padding 复用缓冲（#4002，`transport/naive`）、进程匹配的 netlink 条目
（#4501，`common/process/searcher_linux.go`）、netns 下的 TProxy UDP 缓存（#3876）、
netns 下的 TUN（#4550）、OpenVZ venet 默认接口（#4708）、link-local IPv6 + zone（#4669）、
Qualcomm QoS mark（#4701，Android）、iOS 热点（#4551，设备）。

### 3.2 sing-tun（11 条：3 P0 + 8 P1）——pin 是重写过的 fork，逐条可判

`C:\Deepseek\sing-tun` @ `c0855e6`（pin）。这不是上游代码的镜像：monitor、TCP NAT、redirect 都是
重写过的，所以"上游仍开着"不等于"我们有"。

| 上游 | 判定（证据） |
| --- | --- |
| #110 monitor 在 netlink 订阅关闭后 100% CPU | **发现（F1）**：重写版不会忙循环，但 `monitor_linux.go` 的 `loopRead` 在**任何**非 `ENOBUFS` 读错误上记一行日志后 `return`，没有重开 socket、没有退避；此后 `loopUpdate` 永远收不到事件。对上游是"把自旋换成静默死亡"，见 §5 |
| #91 消除忙循环（PR） | 已由重写吸收：`loopUpdate` 只在事件/定时器上工作，1s 去抖；`monitor_linux_test.go` 用 4096 条路由制造 overrun，断言 2s 内 CPU < 200ms（root-only） |
| #72 / #156 gVisor HandleLocal 破坏 WireGuard userspace | **结构性不适用**：全树没有 `HandleLocal`；gVisor 栈走 `stack_gvisor.go:249` 的 `SetPromiscuousMode(true)` |
| #78 / #164 TCPNat 用源+目的作 flow key | **已实现**：`stack_system_nat.go` 的 `tcpNatKey{Source, Destination}`，且带 FIN/RST 关闭跟踪与 closing 超时 |
| #102 / #158 Windows RAS/PPPoE 下 TUN 默认路由 | **已处理**：`tun_interface_windows.go` 的 `enforceInterfaceOptions` 专门处理 RAS 给其它接口设 `DisableDefaultRoutes` 并把 metric 打回自动值的情况 |
| #245 Windows 绑定出站接口的 IPv4 forwarding 造成 TUN 自回环 | **已处理为告警**：同一函数对"默认接口 forwarding 打开"给出一次警告（`forwardingWarned`），解释 `IP_UNICAST_IF` 会被忽略 |
| #106 系统栈 UDP 回包清 ECN | **未核对**：树里没有写 ECN/TOS 的代码（`tun_linux_flags.go` 只有常量），但回包是否复制请求的 TOS 未读 gtcpip 路径 |
| #107 对端主动 UDP 流的回包不进 TUN | **未核对** |
| #108 单包写跳过 GRO bookkeeping | **未核对**（`fdbased_darwin/packet_dispatchers.go` 是另一套路径） |
| #82 Docker 防火墙策略绕过（auto_redirect） | **未核对**：需要 netns + iptables 才能判 |
| #73 Android TUN 未 UP 就 AddrAdd | **设备/平台**：无 Android 设备 |

### 3.3 quic-go（22 条：7 P0 + 15 P1）——依赖层，多数已由 pin 吸收

pin `d51ec71b`（2026-11-16，fork）。7 条 P0 中 6 条在 2024-10 至 2026-11-08 之间关闭，**最后一条
#6136 关于 pin 前 8 天**，按日期推断在 pin 里（未逐提交核对）。仍开着的是 **#3883
（bufferPool 内存泄漏，2024 年起开着）**。

对我们是"依赖内部行为"，可做的不是读 quic-go 源码，而是**消费者侧的可观察判定**：这轮已经加过
QUIC 发送队列恢复、PMTU 黑洞、stream 隔离的测试；本批对应的新面是 #3883 类的**长时内存基线**
（固定吞吐升降载后 heap/RSS 是否回落）与 #6170 类的**队列上限**（socket buffer 与实际并发发包）。
两者都属于 soak 类，建议单独一轮，不在发布冻结期做。

### 3.4 TrustTunnel（4 条，全 P1）——服务端问题为主

上游 TrustTunnel 与服务端实现，我们的 `transport/trusttunnel` 是客户端。4 条里只有 **#153
（H2 tunnel 的 UDP 速度）** 落在我们的数据路径上（H2 隧道承载 UDP 的排队/吞吐），属于测量项；
#173（HEAD 请求不该等 body）、#138（allow_private_network_connections 影响 reverse proxy）、
#151（认证失败不可见、凭据不可热更）都在服务端。判定：**不适用（角色不同）**，除 #153 记为测量项。

### 3.5 mieru（14 条：4 P0 + 10 P1）——客户端库，服务端条目 N/A

pin `04259b1`（`v3.39.1-0.20261116170915`）。4 条 P0 全部关闭于 2026-04–08，早于 pin：
**已由依赖吸收（按日期推断）**。其余条目里，`mita`（服务端）的多用户/CPU/内存类（#261/#264/#273/
#277/#297/#223/#305）在客户端角色下**不适用**；客户端相关的是 #171（长时间运行后断开）、
#112（测速导致断连/零上传）、#318（underlay idle timeout）、#320/#288（重试与 underlay 回收），
映射到我们 `protocol/mieru/` 的拨号路径，属**未核对**。

### 3.6 hysteria（15 条：3 P0 + 12 P1）——独立客户端，只有行为可映射

上游是 Rust/Go 客户端应用，我们是 `protocol/hysteria2/` 的原生实现。代码级**不适用**；行为级可映射的
四条：

- **#1682 / #1717**：服务端 accept 了 TCP stream 却永不回应时，客户端不能永久占住 goroutine ——
  对应我们 hysteria2 出站的 TCP 读超时/取消；
- **#1723**：UDP 报文分片数超过 `uint8` 上限时必须先校验再分配 —— 对应我们的 UDP 帧解析；
- **#1696 / #1716**：重连后旧 QUIC transport 要关闭；stream-limit 错误不能被当成整条连接关闭。

其余（SOCKS5 UDP 关联空闲超时 #1677、端口跳变 65535 #1240、Android 锁屏 #1510、WSL2 #1474、
GRE/AF_INET6 #1599、WireGuard over UDP 首包 #1513、大 UDP 包 #733、SOCKS5+UDP 监听 #1403、
proxymux #1719、UDP fragment 上限 #1716 以外的）属于对方应用层；#1677 的同类风险在我们
`protocol/socks` 入站（关联生命周期），**未核对**。

### 3.7 Xray（22 条：7 P0 + 15 P1）——不同代码库，只有机制类比

判定基线：Xray 的 Go 代码不在我们的依赖里，标题相同不等于根因相同。P0 里 2 条 `not_planned`、
3 条已关（2026-09/10），2 条仍开（#7398 TLS 证书热更新/OCSP 与握手的数据竞争、#7443 Vision
状态竞争已关 not_planned）。可类比的机制：

- **#227 FakeDNS 在 Start 之前被查询会 panic** → **本轮已修（F2）**：`dns/transport/fakeip` 的
  `Create`/`Lookup`/`Reset` 在 `storage == nil` 时返回错误/未命中，不再 nil panic，见 §4；
- **#220 证书热更新与握手竞争** → 我们的 `common/certificate/store.go`（**未核对**）；
- **#229 / #239 Vision padding 越界 panic** → 我们的 `protocol/vless` padding 路径（**未核对**）；
- **#225 REALITY 拒绝 > 8192 字节的证书 record** → 我们的 REALITY 服务端（**未核对**）；
- **#241 XHTTP/3 在 QUIC 拨号失败时关闭 UDP socket** → `transport/v2rayxhttp`（**未核对**）；
- **#232 observatory 卡住的 TLS 握手泄漏探测 goroutine** → 我们的 `common/urltest` 探测超时（**未核对**）；
- **#234 UDP inbound 16 KiB DiscardOverflow** → 我们 hysteria2/其他 UDP 入站的队列上限（**未核对**）。

Mux/XUDP/XDNS/balancerTag/WireGuard-gVisor/gRPC API 等条目（#6870/#6850/#7120/#7336/#6442/#5901/
#6642/#7349/#6749/#6509/#4194/#5504/#7089/#7413 等）判**不适用（代码库不同）**。

### 3.8 mihomo（15 条：4 P0 + 11 P1）——不同代码库，四条机制真正对应

4 条 P0 全开，但 mihomo 的代码不是我们的。按机制筛出四条**确实落在我们树上**的：

| 上游 | 我们的对应机制 | 判定 |
| --- | --- | --- |
| #3343 TProxy UDP 的 listenLocalConn goroutine/socket 泄漏 | `protocol/redirect/tproxy.go` + `common/listener` | 适用候选；**测试设计**：每秒建/关大量 UDP flow，看 socket/goroutine 是否回落 |
| #3411 Windows TUN auto-route 在接口变化后丢聚合前缀 | sing-tun 的 Windows 路由代码（pin） | 适用候选；需要 Windows 真机改默认接口 |
| #3186 Windows 绑定出站接口 + IPv4 forwarding 造成 TUN 自回环 | sing-tun `tun_interface_windows.go` 的转发告警 | **已处理为告警**（同 §3.2 #245） |
| #3273 DoH 经静默 gRPC 路径的拨号永不取消 | `dns/transport/https` 的拨号超时/取消 | 适用候选；**测试设计**：静默 peer + 重复查询，看 goroutine 是否回落 |

其余 P1 里：**#3313 Fake-IP reload 可能清空仍在使用的缓存文件**（我们刚改过 reload 与 ledger，
值得单独对照）、**#3337 URL-test 组在节点失效后不切换**（`common/urltest`）、**#3421 并发拨号中
落败的尝试未取消**（`common/dialer`）、**#3340 半关闭 TCP relay 堆积 FIN_WAIT_2**（relay 路径）、
**#3163 rule-provider 以 urltest 组为代理时下载失败**（`box.go:272` 的 rule-set 下载器）、
**#3293 proxy-server-nameserver-policy 在启动/reload 时被忽略**（DNS 规则），均为**未核对**。
#3135/#2907/#2544/#2762/#2837/#250? 属 Linux UID/系统 DNS/macOS/Windows 证书/配置回滚，按平台归
"未核对或设备绑定"。

## 4. 本轮实际做的检查与改动

执行过的（可复核）：

1. 解析 CSV，得到 8 个仓库、38 P0 / 90 P1、103 Issue / 25 PR 的分布。
2. 用 GitHub API 取全部 38 条 P0 的 `state`/`state_reason`/`closed_at`（表格见 §2），与 pin 日期比。
3. 在 `C:\Deepseek\sing-tun`（pin `c0855e6`）逐条查 §3.2 的机制；读 `monitor_linux.go`、
   `monitor_linux_test.go`、`stack_system_nat.go`、`tun_interface_windows.go`。
4. 在 `C:\Deepseek\JiejieBox` 用机制矩阵确认 §3.1 的锚点存在（tailscale、search domain、
   hijack-dns、TFO、endpoint detour、netns、interrupt、srs、mux、tproxy、DoH、urltest 等）。
5. 在 pinned 依赖的 git 历史里按 issue 编号检索（quic-go/mieru/sing-tun 均无命中，说明用
   `git log --grep` 判修复不可行，改用 §2 的日期法并在文中标注为"按日期推断"）。

改动（本 commit）：

- **F2：`dns/transport/fakeip` 的 Start 前查询不再 panic。**
  `Create` 在 `storage == nil` 时返回 `the fakeip store is not started`；`Lookup` 返回未命中；
  `Reset` 返回错误。新增 `TestAQueryBeforeStartAnswersInsteadOfPanicking`。
  诚实标注：**上线路径不会在 Start 前查询**，这是防御性（panic → error），不是可达缺陷；
  它对应本批 Xray #227 的同一失败模式。

## 5. 本轮发现（F1）

**F1（跨仓，未修）：sing-tun 的 netlink monitor 在订阅出错后不会恢复。**
`monitor_linux.go` 的 `loopRead` 对 `ENOBUFS` 容忍，对其它读错误记录一行日志后 `return`；
`Start` 只有一次 `unix.Socket`/`Bind`，没有重开路径，`Close` 之外没有任何代码能让 monitor 复活。
后果不是 CPU 100%（上游 #110 的症状），而是**此后所有网络变化事件静默消失**：TUN/路由/出站接口
不再跟随系统变化，直到进程重启。上游 #110 仍开着，我们的重写把"自旋"换成了"静默死亡"，两种都
不满足"事件源要能自愈"。

**证据边界**：这里核对的是 `sing-tun` 这个类型本身（`monitor_linux.go` 的读循环与 `Start`）。
消费者（JiejieBox 的 `common/networkmanager`）是否在别处重建 monitor 没有核对；即使有，重建点也
不在这个类型里，且没有任何日志能把"订阅已死"暴露给上层。这条按"机制缺口"记录，不按"已证实的
现网故障"记录。

建议补丁（未实施，需单独的 sing-tun revision + pin + CI）：

- `loopRead` 出错时 `m.socket.Close()`，记录错误，**重开 socket 并重新 Bind**，指数退避（上限如 30s），
  期间保留 `close` 通道退出；重开后触发一次 `emit()`，让上层重新同步接口与路由；
- 在 `monitor_linux_test.go` 增加一条"外部关闭 socket 后 monitor 仍能收到后续 link change"的用例
  （现有用例只覆盖 overrun 与正常 link change）。

## 6. 建议的下一轮攻击清单（按"机制在我们树上 × 上游仍开着"排序）

| 序 | 条目 | 测试设计（可观察判定） |
| --- | --- | --- |
| 1 | #4678 DNS-only 变更重置 TCP | 只改 DNS 顺序，长连接维持；断言已建 TCP 不被重置、无新拨号 |
| 2 | #4410 search-domain NXDOMAIN 抢答 | 多 search domain + 受控时序；断言 QNAME 还原、答案归属正确 |
| 3 | #4444 拨号侧解析与 DNS 规则 | 节点域名/目标域名不同规则；断言拨号解析确实走规则 |
| 4 | #4386 TFO × route-action 解析 | 域名经 route action 得地址 + TFO 开；断言首包与连接建立 |
| 5 | #3878 hijack-dns 回环 | WSL netns + systemd-resolved；断言无 127.0.0.1:53 回环 |
| 6 | #4515/#4686 Tailscale 网络切换 | Windows 上禁用/启用适配器；断言 endpoint/listener 重建、旧 socket 释放 |
| 7 | #4665 endpoint detour 恢复 | WireGuard→OpenVPN detour，断网/换网；断言握手重试与取消传播 |
| 8 | mihomo #3343 TProxy UDP 生命周期 | 每秒钟级建/关 flow；断言 socket/goroutine 回落（before/after 计数） |
| 9 | mihomo #3313 fake-IP reload 与缓存文件 | reload 交错写盘与退出；断言运行池读到完整快照 |
| 10 | Xray #220 证书热更新竞争 | 并发 reload + 握手，`-race`；断言证书快照原子 |
| 11 | Xray #229/#239 padding 边界 | 对 vision padding 做长度 fuzz；断言不 panic、错误可分类 |
| 12 | hysteria #1682/#1717 有界等待 | 服务端 accept 不应答；断言客户端在读超时后返回 |
| 13 | quic-go #3883 长时内存基线 | 固定吞吐升降载；断言 heap/RSS 回落（soak，非冻结期） |

## 7. 证据缺口（明确记录，不当作通过）

- 90 条 P1 没有逐条取上游状态，只有 §3 的机制分组；其中标"未核对"的条目需要单独一轮。
- quic-go "已由依赖吸收"是**按关闭日期与 pin 日期推断**，没有逐提交核对代码；#6136 距 pin 仅 8 天，
  风险最高，值得在 pin 升级时单独验。
- 没有阅读 TrustTunnel / hysteria / Xray / mihomo 的上游实现；对这些仓库的判定只基于**我们自己的**
  机制与角色，不基于对方代码。
- 平台/设备行（Android、iOS、macOS 睡眠、Windows TUN 真机、netns+iptables）在本环境不可测，
  不得由 Windows 的结果推断。
- 本页没有对 8 条 sing-box 开放 P0 做复现；它们需要 §6 的独立测试，不能按"上游开着"就判定我们有 bug。
