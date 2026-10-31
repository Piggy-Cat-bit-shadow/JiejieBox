# 数据路径优化：Direct Offload、traffic class 与上传调度器（Data-path optimization: Direct Offload, traffic class, upload scheduler）

## 入口、嗅探与系统代理路径

以下为 `0067b03e377` 生产基线的源码范围；不同入口共用路由规则，但不共享所有数据面快捷路径。

| 路径 | 行为与优化 | 限制及源码 / 测试入口 |
|---|---|---|
| TUN 与 endpoint 本地目的地址 | `protocol/tun/inbound.go` 在栈实现 `tun.RoutedPort` 时注册端口；MASQUE、WireGuard、Tailscale 非 system 模式的本地流量由 `adapter.JudgeHostFlow` 交给同地址族的栈地址，保留目标端口。 | 无端口、缺地址族或不支持的协议回退 `ActionAccept`；system 模式保留原路径。见 `adapter/host.go`、`adapter/host_test.go`、`masque-connectip.md`。 |
| TUN 网络变化 | 启动前的网络更新通过 pending 标记与启动后的复查交接；栈启动后每次接受的更新各自触发重置。 | 见 `protocol/tun/inbound.go`、`protocol/tun/reset_before_start_test.go`；这不是设备网络切换验证。 |
| TUN 回包与直连 | 单包单写回复用调用方切片；受条件约束的 TCP 接管失败后回退复制。 | TUN 普通栈的 `ActionBypass` 不实现隐式系统绕行；Linux nfqueue / auto-redirect 的执行边界见本模块 A.5。接管不等于已发生内核 splice。 |
| 系统代理 / Mixed | `protocol/mixed/inbound.go` 以一个 reader 分辨 SOCKS 与 HTTP；`GuardSOCKS5Address` 在缓冲前安装，`NewFirstPayloadConn` 在握手之后交出缓存的首包；没有配置用户时跳过认证上下文读取。TCP / UDP 分别送入 `RouteConnectionEx` / `RoutePacketConnectionEx`。 | 保留已配置用户的认证、连接所有权、首包与路由元数据；系统代理入口不走 TUN `PreMatch` / TUN 专用 splice 接管。见 `e2e/system_proxy_chain_test.go` 与 `e2e/tun_splice_dataplane_test.go`。 |
| HTTP / TLS / QUIC 嗅探 | `common/sniff/http.go` 用有界头部先排除确定不是 HTTP 的输入并重放已读内容；`common/sniff/tls.go` 只对完整可判定的首记录快速拒绝，取到 ClientHello 后停止多余握手；分片续读只重跑 QUIC，新增读取最多 64 轮。 | 已有 FakeIP 域名仅在 `packetSniffUsesKnownFakeIPDomain` 条件成立时提前结束等待。保留超时、部分头部和缓存所有权；详见 `sniff.md`。 |

`e2e/system_proxy_chain_test.go` 用 loopback 上真实 mixed、DNS 与目的端验证路径、选择及元数据；
`e2e/tun_chain_test.go` 使用 MemoryTun 和 Go 栈，不证明真实设备路由。2026-10-31 在 Windows
本地 Go 1.26.10 下，`go test ./e2e -run '^(TestSystemProxyChain|TestSystemProxyTCPSpliceIsRefusedAtTheSource)$' -count=1`
通过；这是 loopback 和测试替身证据，不是系统代理设置或真机 TUN 验证。历史基准与具体运行结果
应从相应记录核对，不把单机基准扩展为真实网络吞吐承诺。


**`011f3fdcd99` 处的跨模块区分：** 链上没有显式的 traffic-class 策略时，`route/traffic_class.go` 仍会使用自动标签分类。这套机制独立于已移除的 `fast_path` AI 标签回退（AI-tag fallback）；不要把两者混为一谈，进而移除或重新引入其中任何一个。两者都按流（per flow）决策，但回答的是不同的问题。

**范围（Scope）：** `common/dialer/profile.go`、`protocol/direct/outbound.go`、`route/bypass_verdict.go`、`common/trafficclass`、`common/trafficsched`、`route/dial_governor.go`、`route/traffic_class.go`。

**来源（Provenance）：** `common/trafficsched`（31 个提交、`0067b03` 处 18 个文件）与 `common/trafficclass` 均为 **fork 创建**；`traffic_scheduler` 与 `traffic_class` **在官方 sing-box 中不存在**，所以只要配置用了其中任何一个，上游就加载不了。Direct Offload 是 fork 扩展，资格规则比上游*更严格*。

**本子系统对 CI 至关重要：** `verify.yml:486` 对 `./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/...` 运行一个专用的 `-race` 步骤；`scripts/ci/verify-upstream-assumptions.sh` 还为其维护一份绊线（tripwire）清单（`docs/UPSTREAM-SYNC.md`）。

**在改动以下内容时加载本模块：** `traffic_scheduler.upload_rate`、`traffic_class`、旁路（bypass）决策、TUN `ActionBypass` 处理，或任何“让操作系统来路由这个”的优化。

---

## Part A — 直接卸载（Direct Offload）

### A.1 规则（The rule）

```text
SEMANTIC EQUIVALENCE, NOT CONFIG EMPTINESS
```

一个拨号选项只有在**用户态路径确实会把该选项应用到这条流上**时，才会让这条流失去资格。一条流永远触及不到的选项，不构成拒绝的理由。

例如，随附拓扑只声明了一个 direct 出站，并为其配置了解析器：

```json
{ "type": "direct", "tag": "direct", "domain_resolver": "local-agh" }
```

如果仅因配置非空就拒绝旁路，**所有**经过该出站的流都会失去资格，包括根本不需要查询解析器的字面 IP 流。因此必须判断选项是否实际影响当前流。

### A.2 两层，两个问题（Two layers, two questions）

| 层 | 问题 | 归属 |
|---|---|---|
| 路由器 | 给定入站、嗅探出的域名、FakeIP 状态、tracker 与规则判定，这条**流**能否跳过用户态路径？ | `route/route.go` — `canFastBypass` |
| 拨号 profile | 对这条流，这套**套接字配置**能否被平台自己的 connect 复现？ | `common/dialer/profile.go` — `SocketSemantics` |

两者都不重新实现对方。profile 若去回答策略问题，只能猜它看不见的元数据；路由器若重新读拨号选项，就成了这些选项的第二种解释。

### A.3 分类及其来源（The classification, and where it came from）

这张表来自 `common/dialer/default.go` 与 `resolve.go`——**依据是每个字段实际被应用的位置**，不是字段名。

| 作用域 | 选项 | 为何使其失去资格 |
|---|---|---|
| 始终 | `detour`, `bind_interface`, `inet4/6_bind_address`, `bind_address_no_port`, `protect_path`, `routing_mark`, `netns`, `connect_timeout`, `network_strategy`, `network_type`, `fallback_network_type`, `fallback_delay` | 一次绑定、一个 mark、一个命名空间、一个拨号截止时间或接口选择——平台自行执行 connect 时都无法复现 |
| 仅 TCP | `tcp_fast_open`, `tcp_multi_path`, `disable_tcp_keep_alive`, `tcp_keep_alive`, `tcp_keep_alive_interval` | 作用于 TCP 拨号器；经同一出站的 **UDP** 流仍然只是一次普通 connect |
| 仅 UDP | `reuse_addr`, udp source port, `udp_fragment` | `reuse_addr` 会到达 `listenConfig`，从不进入 `dialer.Control`；`udp_fragment` 是数据报套接字选项 |
| 仅解析 | `domain_resolver`, `domain_strategy` | 只在解析名称时被咨询——见例外 |
| 忽略 | `udp_fragment_default` | direct 构造函数会设置它；若把它当作运维者的显式声明，本 fork 中每个 direct 出站都会被拒绝 |

**作用域拒绝不属于选项分类。** `BlockerNetworkUnsupported` 与 `BlockerDestinationInvalid` 来自这条流的目标（`common/dialer/profile.go:152-153`），`BlockerLocalFamilyUnavailable` 来自机器本身；它们归在 A.8/A.9，不在这张表里。

**容易误解的例外：** **硬性单族**策略（`ipv4_only`、`ipv6_only`）也会过滤**字面**目标——拨号路径把它用在最初的尝试上，不只是已解析的候选。profile 若停在“字面地址不需要解析器”，就会放过一条用户态路径本该拒绝拨号的连接。所以有效策略要向**活动拨号器**询问，而且只有*限制性*策略才算数：`prefer_ipv4` 只是偏好，什么都允许。

### A.4 两次失败即拒绝（Failing closed, twice）

1. **运行时。** 字段已设置却不在分类表里，就会产生 `BlockerUnclassified`；这种遗漏的代价是错过一次优化，而不是丢掉一条套接字语义。
2. **评审时。** `TestEveryDialerOptionIsClassified` 对选项结构体做反射，每个字段都分类后才会通过，失败消息里会打印各作用域的含义。表里也不得出现已不存在的字段，于是一次重命名不会留下陈旧条目。

### A.5 存在哪些层，以及旁路实际发生在哪里（Which layers exist, and where a bypass actually happens）

| 层 | 谁承载字节 | 对本进程的每字节成本 | 在何处被兑现 |
|---|---|---|---|
| **L0** | 路由集地址策略（`route_address_set` / `route_exclude_address_set`） | 旁路掉的流量不经过用户态路由器 | 非 auto-redirect 的设置可以把前缀传给 TUN 选项；auto-redirect 在路由器预匹配之前评估路由集。实际的路由安装与判定如何生效取决于入口与平台 |
| **L1** | 路由器的语义旁路——本节 | 零成本，**仅在 redirect 模式**下 | Linux `auto_redirect`（nfqueue） |
| **L2** | 在平台、套接字类型与连接路径允许时的内核辅助拷贝 | 取决于路径 | 有条件的，并非对每条流或每个 OS 都保证 |
| **L3** | 经缓冲区的用户态拷贝 | 取决于路径的拷贝与 I/O 成本 | 在适用处作为回退 |

> **L1 在 TUN 模式下不旁路。** `tun.ActionBypass` 由 Linux nfqueue 处理器兑现，在那里映射为带输出 mark 的 `NfRepeat`，数据包沿原路离开。pinned sing-tun fork 提供的每个 TUN 栈里，`flow_dispatch.go:575-615` 都把 `ActionBypass` 送进它的 `default:` 分支 → `installAccept`（`:612-613`、`:625`），三个栈共用同一个 dispatcher（`stack_system.go:274`、`stack_go.go:202`、`stack_gvisor.go:103`）。只有 `nfqueue_linux.go:334-335` 兑现它。
>
> 有两点需要留意：**gVisor 栈已被构建标签退役**（`cmd/internal/build_libbox/tag_policy_test.go:16-83`；`go.mod:367-386`），并且 `stack_gvisor_icmp.go:126`、`:188` 把 `ActionBypass` 与 `ActionFlow` 归为同一组——而一个**不含 Port** 的判定（也就是 `adapter/router.go:131` 发出的那种）会以完全相同的方式落到 accept。
>
> **所以对 TUN 客户端来说，这个资格决策没有数据面影响。** 被它标为可旁路的流，仍然在用户态被代理。这项决策只在 Linux redirect 模式里才有真实效果。

这些结论靠针对 pinned sing-tun 修订版的行为绊线固定，不是靠读代码：`TestANewTrackerOnABypassIsNeverCreated`、`TestABypassFlowIsNeverCounted`（`protocol/tun`），以及 `native_bypass_dispatcher_test.go`、`native_bypass_trace_test.go`。

**L0 不是“先路由器、再旁路”。** 它在路由器*之前*运行，所以决策时手里只有一个地址，别的什么都没有。它表达不了域名、进程或协议条件；也不该用它把一条 `DIRECT` 规则编译进路由集，除非该规则本身已经权威且只涉及 IP。

**L0 的唯一边界——FakeIP。** L0 先运行，所以一个 FakeIP 地址会被交给操作系统并黑洞（black-holed）——它是本进程铸造的占位符，在平台路由表里没有路由，只有路由器能还原它的域名。两种常见配置会走到这一步：配置了 `route_address_set`（集合之外的一切都被旁路），以及一个恰好覆盖 `198.18.0.0/15` 的 `route_exclude_address_set`。因此地址只要落在当前 FakeIP 存储范围内，`JudgeFlow` 就会下落到路由器。这个 L0 防护只查 `fakeIPStore.Contains`，不查路由器的签发台账或退役范围历史：当前范围之外的地址，仍可能在路由器做那些检查之前就被路由集旁路。没配置 FakeIP 传输时跳过该防护。路由器侧的不同检查见 `dns-demand.md` §3。

**DNS 始终优先。** `JudgeFlow` 的顺序是：已配置的 DNS 地址 → 按端口识别 DNS → 路由集 → 路由器。正是这个顺序让基于 DNS 的广告过滤继续生效。它**不**声称能检测 DoH/DoT/DoQ，也不会拦下 UDP/443 去拦 QUIC。

### A.6 两个已修复的静默空操作——陷阱素材（Two silent no-ops that were fixed — trap material）

* 携带 direct 出站 **ICMP `Port`** 的旁路，会被 pinned sing-tun 的 `ForwardStage.judge`（`flow_dispatch.go:432-434`）改写成 `ActionFlow`。结论很明确：带 `Port` 的 `ActionBypass` 会被改写为 `ActionFlow`；旁路悄悄变成用户态流，正是这个 bug 混进生产的方式。由 `TestBypassWithAPortBecomesAFlow`（`protocol/tun`）与 `fast_bypass_verdict_test.go`（`route`）守护。
* `JudgeFlow` 在 `dnsHijackByPort` **之前**检查路由地址集，因此 `8.8.8.8:53` 完全离开了 sing-box（`497d37727`）。实测 4 次劫持，期望是 5 次（或 0 次）。

### A.7 测量了什么（What is measured）

真实预匹配入口点上的决策命中率，基于一个建模的流混合：

| 流形态 | 权重 | minimal | dashboard-enabled |
|---|---|---|---|
| 字面 IPv4 TCP | 55 | 允许 | 拒绝：tracker |
| 字面 IPv6 TCP | 15 | 允许 | 拒绝：tracker |
| 字面 IPv4 UDP | 15 | 允许 | 拒绝：tracker |
| 嗅探出域名的 TCP | 8 | 拒绝：嗅探域名 | 拒绝：嗅探域名 |
| FakeIP TCP | 5 | 拒绝：FakeIP | 拒绝：FakeIP |
| connected UDP | 2 | 拒绝：UDPConnect | 拒绝：UDPConnect |
| **合计** | **100** | **6 种形态中 3 种，占 85% 权重** | **6 种形态中 0 种** |

在该测试拓扑中，tracker 的存在本身就会阻止 L1 旁路，代码不判断它实际记录哪些数据。因此启用 Clash API 或 dashboard 后，表中的流均不能使用 L1；这是为了保留连接统计。该结果适用于所测配置，不应不加核对地推广到所有部署。

L0 只在它能起作用的地方测量：无路由集 → 0/4 形态；`route_address_set: 10/8` → 2/4；`route_exclude_address_set: 198.16/12` → 1/4；落在当前 FakeIP 存储范围内的地址会下落到路由器，而不是走这条 L0 路由集旁路。

成本，来自 Apple M1 上的 `route/direct_offload_bench_test.go`：

| | 成本 |
|---|---|
| 决策，所有条件通过 | 14–41 ns，**0 次分配** |
| 决策，被低成本条件拒绝 | ~5–11 ns，0 次分配 |
| 仅出站 profile | 5.3 ns，0 次分配 |
| **流建立，已旁路** | **119 ns，0 次分配** |
| **流建立，用户态** | **1461 ns、2856 B、26 次分配** |
| TCP 拷贝，内核 socket 对（L2） | 4 / 32 MiB 时为 4742 / 5001 MB/s |
| TCP 拷贝，用户态缓冲区（L3） | 4 / 32 MiB 时为 3952 / 5567 MB/s |
| UDP 每数据报（1400 B） | ~66 ns，1 次分配 |

> **表中“用户态流建立”的旧数字无效，仅为追溯保留。** `route/direct_offload_bench_test.go:139-141` 指出，26 次分配和 2856 B 来自 `net.Pipe` 测试 fixture，而非被测代码。`22d46966c` 修正基准后重新测得 **104 B、2 次分配**；`docs/fork/direct-offload.md:225` 仍保留旧值。其他行同样是历史 M1 测量，引用到其他环境前应重新测试。
>
> **唯一被真实测试套件强制的成本主张是一个 `TEST`**，不是基准：`TestBypassDecisionUsesNoAllocations`（`direct_offload_bench_test.go:354`）使用四次 `testing.AllocsPerRun` + `require.Zero` 调用，覆盖 `canFastBypass`、`CanBypass`、一次 FakeIP **拒绝**，以及 profile 查找。

**L2 与 L3 在 loopback 上分不出来，这不是基准的缺陷。** Loopback 对用户态拷贝最友好，对零拷贝最不利。每个基准都断言自己量的是哪条路径，所以它们不会悄悄收敛。

还有一项成本被发现并修掉：合格路径会打一行 debug 日志，参数在 logger 判定该级别关闭**之前**就构造好了——每条合格流 5 次分配、约 95 ns，比决策本身还贵，而且正好落在本特性针对的那些流上（`e27d58711`）。

### A.8 范围，以及刻意不在范围内的部分（Scope, and what is deliberately not in it）

卸载的候选流需要来自 TUN 入站，使用 TCP 或 UDP，目标为字面 IP，且最终出站实现 `adapter.BypassableOutbound`。除单出站链外，符合下述 `fast_path` 策略的透明组链也可能获得资格；所有流仍须通过其余资格检查。

本轮不包含：原生路径上的 tracker 记账（有 tracker 就拒绝）；`UDPConnect`（它有自己的套接字与 NAT 语义）；traffic class（原生 direct 流按构造就在上传调度器的托管域之外）。

> **组链的适用范围已在 `v0.1.9` 基线上修正。** 组可能带来额外的选择、生命周期和记账语义，不能仅因最终选中 direct 就认为可以旁路。`loadbalance` 可能管理拨号与重试，因此仍被视为不透明组并遭到拒绝。
>
> `fast_path` 为符合条件的透明选择组提供了例外：最终出站之前的每个组都必须声明 `FastPathTransparent() bool`，且最终出站仍须通过原有的 `BypassableOutbound` / `CanBypass` 检查。满足条件时，`route/route.go` 可放宽 `BypassRefusedOutboundChain`；否则，进入组后的 `len(chain) >= 2` 会使链被拒绝。
>
> 对当前目标，从外向内遇到的第一个显式组策略决定授权。没有显式策略时，通往真实 direct 出站的透明链具有默认授权；显式 false 会取消它。不透明组仍被拒绝，获得授权也不能跳过最终出站和元数据的其他资格检查。完整契约及各组类型见 `fast-path-group-policy.md` §6。

### A.9 归因（Attribution）

* `route.BypassVerdict` 命名作出拒绝的路由器条件。
* `(*direct.Outbound).BypassBlockers` 命名 `CanBypass` 背后的套接字级原因集合，包括环境网络策略与自身地址守卫。它还携带 `BlockerLocalFamilyUnavailable`：**主机没有通往目标地址族的接口**（`common/dialer/profile.go:106-119`）。这是机器的属性，不是出站选项的属性，也和已配置的 `BlockerFamilyStrategy` 不同——不要把两者合并。

两者都只是诊断信息。**没有任何按流记录日志的行为**，决策也不构造任何字符串。

---

## Part B — traffic class 与上传调度器（traffic class and the upload scheduler）

### B.1 流水线是一个整体（The pipeline is ONE unit）

```text
common/trafficclass          → route/dial_governor.go        → common/trafficsched
classify the flow              bound the post-transition        pace the managed upload
                               dial burst
```

`route/traffic_class.go` 持有策略映射；`option/traffic_class.go` 持有配置。不要把这三者当作三个独立的包：改动 class 词表会同时牵动三者。

**Class 解析：** 对解析出的链按**从最外层到叶子**扫描；第一个显式策略获胜；显式的 `default` **会抑制整条链的自动检测**。class 按流实际经过的链解析，所以经同一个 group 不同成员的两条流可以带着不同的 class。

### B.2 上传调度器（The upload scheduler）

**适用范围：** 调度器只处理用户态代理的上行流。`route.ConnectionManager.uploadStreamGate` 仅在当前流无法使用内核 splice 拷贝路径时介入；它不管理 splice 或原生 direct 旁路流量，也不是全机 QoS。

**`upload_rate` 完全按写下的内容生效。** 这里**没有隐藏的安全系数**：配置 `8 MiB/s`，托管上传就放行 `8 MiB/s`。

**缺省、为 `0`，或 `traffic_scheduler` 段为空，调度器都保持惰性**——它看着每一个托管字节并立即放行，**没有队列、没有锁、没有定时器**。不要求整形，就不会得到整形。这并不证明一份用了 fork 专有字段的完整配置能被官方 sing-box 加载。

**确实配置了速率时，整形器就持续运行**，进程活着它就跑。这是刻意的；见下面的测量。

可接受的写法：`8388608` 与 `"8388608"`（字节/秒）；`"8 MiB/s"`、`"8MiB"`（二进制单位，`/s` 只是装饰——这个字段始终是速率）；`"20 MB/s"`（十进制）；`"20 Mbps"`、`"16000kbps"`（比特，按 8 比特折一字节换算）。负值、未知单位、小数值，或会溢出 `int64` 的换算，都会**让配置失败**，而不是接受后忽略——对速率限制来说，一个被静默忽略的值意味着配置声称了进程并不执行的策略。

### B.3 定下设计的四次测量（The four measurements that settled the design）

实验记录在 `common/trafficsched/doc.go`，代码在 `contention_test.go`/`rig_test.go`。指标是竞争状态下小体积高优先级消息对接收方的可见送达延迟，因为那才是产品目标。

**1. 重排序不起作用，整形才起作用。**（4 条 NORMAL 流通过每条流 64 KiB 的接受窗口，灌满一个 2 MB/s 的共享 FIFO）

| 模式 | p99 | 队列均值 | 批量 |
|---|---|---|---|
| 无门禁（基线） | 146 ms | 257 KiB | — |
| 已装门禁、无限速 | 150 ms | 257 KiB | — |
| `ModeAdmission` | 145 ms | 257 KiB | — |
| `ModeService` | 155 ms | 257 KiB | — |
| `ModePacedNormalOnly` @ 85% | 14 ms | 17 KiB | 1.74 MB/s |
| `ModePaced`（聚合）@ 85% | **9 ms** | 15 KiB | 1.72 MB/s |
| `ModePaced`（聚合）@ 70% | 8 ms | 14 KiB | 1.42 MB/s |

前四行和什么都不做分不出来，也不可能更好：拖住高优先级消息的那些字节已经在发送方的接受窗口之内，用户态够不着。

**2. 等交互流量到来才启动 pacer，无法保护首次请求。** 测量对象是持续一秒批量上传后的首次 256 字节高优先级写入：无门禁 p50 为 127 ms，`ModeAdmission`（已启用）为 128 ms，持续运行的 `ModePacedNormalOnly` 和聚合 `ModePaced` 均为 **2 ms**。

**3. 整形也必须覆盖高优先级通道。** 两条 HIGH 批量流加一个小的 HIGH 探测：无门禁 p99 114 ms；`ModePacedNormalOnly` @85% 73 ms；`ModePaced`（聚合）@85% **8 ms**。只整形 NORMAL 通道，队列深度和原来一模一样。实际交付的是聚合模式。

**4. 超过桶容量的写入也必须计入限速。** 系统分两段记账：共享桶先承担本次写入中最多一个突发容量的部分，避免单条流长期占用共享额度；剩余部分计入发起写入的流后续额度。实验中，每次写入 256 KiB、桶容量为 64 KiB、配置速率为 1.70 MB/s 时，实测放行 1.84 MB/s；未整形对照组达到 2.10 MB/s。

### B.4 学习型速率经过研究，但未交付（A learned rate is researched and NOT shipped）

`adaptive_research_test.go` 由 `trafficresearch` 构建标签守护，**而没有任何东西引用这个标签**——没有工作流、没有 Makefile 目标、没有脚本、没有发布 profile。本包 51 个测试全部执行、**零跳过**；研究类测试根本没有编进来。

**生产代码有一项针对研究原型的静态守卫，但覆盖范围有限：**

* 守卫本身（`common/trafficsched/adaptive_controller_guard_test.go:20-63`、`TestAdaptiveControllerIsNotInProduction`）**没有构建标签，所以每次发布运行都会执行**；
* 它是对该目录里非 `_test.go` 文件做**源码文本 grep，找标识符 `adaptiveRate`**；
* 守卫含阳性对照：要求 `adaptive_research_test.go` 中存在该标识符，并检查研究文件的 `//go:build trafficresearch` 和 `scheduler.go` 的 `type RateSource interface`。

这个守卫**无法发现**换名或放在其他包里的学习型控制器，也无法发现从 `box.go` 经 `SetRateSource` 接入的控制器。它只检查一个源码标识符，并非行为测试；`docs/UPSTREAM-SYNC.md:89-94` 对此类文本守卫的局限也有说明。

**构建标签的由来：** 提交 `bb21a854c` 记录，同一份源码的 `TestAdaptiveRatePrototype` 在 GitHub Linux runner 上失败、在 macOS 上通过：85% 阈值要求 1.8605 MB/s，Linux 实测为 1.7719296 MB/s。该提交将研究测试移到标签后，而没有放宽断言。

后续重新评估 AIMD 控制器时，应先阅读这些实验及跨平台差异，不要仅为消除 Linux 上的测试失败而放宽阈值。

学习型速率为什么不交付：最直观的观测量——一次写入阻塞了多久——量到的是**竞争**造成的等待，不是路径的速率。一条 2.00 MB/s 的链路上，一条流时它报 1.29 MB/s，四条流时报 0.35 MB/s，两次都非常稳定：一个稳定、精确、却**错误**的数字。AIMD 控制器原型能跟住变化中的容量，但会**稳定在**容量处，而一到容量，队列就建立起来了：

| | HIGH p95 | 批量 |
|---|---|---|
| 固定速率，容量的 85% | **8 ms** | 1.69 MB/s |
| 固定速率，容量的 100% | 42 ms | 1.99 MB/s |
| 学习型速率（AIMD 原型） | 62 ms | 稳定在 100% |

> AIMD 需要队列存在才能检测到自己太快，而本特性的存在目的正是消除队列。

**那些表是历史记录。** 它们能从 `contention_test.go` 复现，但由 `t.Logf` 输出，而断言针对的是*其他*量（探测点之前的中位字节数、桶的上界、`rate × 1.15`、队列均值的比率）。**把它们当成由断言保证的数值来读，就是读错了。**

### B.4b 模式阶梯——以及唯一交付的模式（The mode ladder — and the only mode that ships）

一共四种模式（`common/trafficsched/scheduler.go:29-51`）。**生产代码里唯一提到模式的地方是 `route/conn.go:147` = `ModePaced`。** 其余三种只出现在测试与手动 harness 中，充当被测对照组。`option/traffic_scheduler.go` **只有一个字段**（`UploadRate`，`:50`）——**没有 `mode` 键；加上它，就会暴露一个经测量和什么都不做分不出来的模式。**

### B.4c paced 路径的历史测量（Historical measurements of the paced path）

在发布基线上用 `BenchmarkStreamWriteTax/gate-paced-generous` 测量（速率 `1<<40`，即一个永远不会生效的速率）：

| 写入大小 | paced 门禁 | 惰性门禁 |
|---|---|---|
| 256 B | 5,462 ns，1 B/op，**0 次分配** | 比裸 writer 高约 9.8 ns |
| 4 KiB | **1,423,806 ns**，257 B/op，**3 次分配/op** | — |
| 64 KiB | **1,404,768 ns**，255 B/op，**3 次分配/op** | — |

两个后果，两者都重要：

1. 该基准在 4 KiB 及以上写入中观察到每次约 3 次分配，与 `bench_test.go:81-83` 对整形写入不分配的描述不一致。`doc.go:99-106` 中无竞争路径不分配的说法，在这次测量里只对惰性路径成立；当时没有 CI 测试覆盖 paced 路径的分配。
2. **那次运行在宽松的配置速率下，4 KiB 与 64 KiB 用例都观察到每次写入约 1.40 ms**，≈1.4 × `paceTick`（`scheduler.go:78`，1 ms 的 tick）。推断的机制是：`chargeLocked` 把 `f.owedUntil` 设到提前几纳秒（`:662-677`），随后 `flowTimeReadyLocked` 报告未就绪（`:701-710`），唤醒路径又包含 1 ms 的 `paceWakeLoop` tick（`:869-892`）。由此粗算出的 47 MB/s 描述的是那一次具体测量，不是可移植的吞吐上限：主机的唤醒延迟、调度、写入大小、配置与实现变更都会影响结果。

那次历史运行即使在宽松速率下，每次写入的成本也明显高于惰性对照。要作出当前的性能主张，请重新测量目标构建与环境。

### B.5 无竞争时的成本（Cost when nothing contends）

**惰性**路径上，一次写入比直接写裸 writer 多约 **9 ns**，**没有分配**。这个数字可复现（256 B 时 +9.8 ns，4 KiB 时 +5.6 ns）。更精细的主张——“约 2.5 ns 是包装器，其余是两次原子加载”——按所述方式复现不出来：把这种拆分当作历史上的 M1 分解，而不是一次测量。

---

## Part C — 拨号调速器（the dial governor）

`route/dial_governor.go`。

一次路径切换会成批地让连接失效：所有还在用旧路径的流几乎同时失败，又同时重新拨号——TCP、TLS 与隧道握手的惊群（thundering herd）正好砸在无线已经忙于重建的时候。

**常量：** 窗口 **3 s**、槽位上限 **24**、预算 **500 ms**。窗口**只由一次排空式转换**打开（`route/conn_reclaim.go:348-352`），作用点在 `route/conn.go:384`、`:632`。

**窗口之外没有什么需要限制。** 零值就是惰性，这可以证明而不只是断言：`open := time.Now().Before(g.windowEnd)` 对**零值 `time.Time` 为 false**，所以 `enter` 返回 `noopRelease`（`dial_governor.go:114`、`:126-128`）。由 `dial_governor_test.go:40` 固定。

拨号调速器不会让 `interactive` 和 `realtime` 类别排队（`:89-91`）；`default` 和 `bulk` 则可能等待。**`ClassDefault` 有意纳入限制**（`:85-88`），因为未配置类别的流会使用它；如果将其豁免，调速器就只能约束显式标注过的流量。

**这个限制是软的。** 500 ms 预算用尽后，拨号不再等待，无限制地继续，所以调速器最多只能抹平一次突发的开端——永远不会无限期推迟恢复，也不会把恢复全局串行化。

**它等的是一个 channel，不是定时器。** 它过去每 6–18 ms 重试一次，直到预算耗尽：一次轮询在整整 500 ms 里制造定时器唤醒、调度器唤醒与互斥锁竞争——而这个 fork 的公开目标正是*减少*手机上的无线电与 CPU 唤醒。现在等待者一直睡着，直到真出了事：槽位被释放、预算到期、调用方的 context 被取消，或窗口关闭。`released` channel 带一个令牌的缓冲，让唤醒可以合并（`:67-70`、`:173-177`）。

`enter` 返回一个 release 函数，调用方**必须**在包括错误在内的每一条路径上恰好调用它一次。

> **调速器自己的注释有两处不准确，记在这里免得被误“修正”。** `dial_governor.go:23-24` 说*"one atomic-free time comparison takes it out of the dial path entirely"*（中文：一次无原子的时间比较就把它完全移出拨号路径）。这对**优先级**路径成立——`isPriority` 在 `:106-108` 先返回，此时什么都还没取——但**窗口之外的受限类别拨号仍要取两把互斥锁**（`:112` → `settings()` 在 `:183-190` 加锁，然后 `:113`），还会**在第一次这类拨号时分配一个 channel**（`:120-122`）。那条注释描述的是常见的交互路径，不是每一条路径。
>
> 另外，`route.isPriority`（`:89`）重复了 `trafficclass.Class.IsHighPriority()`（`common/trafficclass/class.go:88`）——**同一个映射有两份实现**，一份给调速器用，一份给调度器用（`scheduler.go:300`）。只改其中一份是真实的陷阱。

---

## Part D — 证据、来源与告诫（Evidence, provenance, cautions）

### 证据（Evidence）

| 保证 | 等级 | 复检 |
|---|---|---|
| `TestEveryDialerOptionIsClassified`（反射，双向） | `VERIFIED_UNIT` | `go test -count=1 ./common/dialer/...` |
| 零值 profile 拒绝；族策略作用于字面地址 | `VERIFIED_UNIT` | `TestZeroProfileRefuses`, `TestFamilyStrategyAppliesToALiteralAddress` |
| 旁路决策成本为 0 次分配（是测试，不只是基准） | `VERIFIED_UNIT` | `route/direct_offload_bench_test.go`, `route/bypass_capability_cost_test.go` |
| 没有任何 TUN 栈兑现 `ActionBypass`；其上的 tracker 永远不会被创建 | `VERIFIED_UNIT`，行为轨迹 | `protocol/tun/native_bypass_*_test.go` |
| 携带 `Port` 的旁路会变成 `ActionFlow` | `VERIFIED_UNIT` | `TestBypassWithAPortBecomesAFlow`, `route/fast_bypass_verdict_test.go` |
| 调度器按聚合整形、不放行超速率、在转换时释放 | `VERIFIED_UNIT` | `go test -count=1 ./common/trafficsched/...` |
| 研究控制器无法到达生产 | `VERIFIED_UNIT`（若到达则 CI 失败） | `common/trafficsched/adaptive_research_test.go` |
| 门禁保留 extended-writer 路径并如实报告 MTU | `VERIFIED_UNIT` | `common/trafficsched` 门禁测试（11 个） |
| trace-class 通道映射 | `VERIFIED_UNIT` | `common/trafficclass`, `route` |
| 这些包的竞态检查 | `ci-release.md` 中链接的 `Verify` 运行在 `1ff0af39110` 处成功；本地 `-race` 因缺少 cgo 工具链而受阻。一次成功运行的范围限于它实际执行的命令与 SHA，并不是普遍无竞态的证明 | Linux runner |
| 真实链路整形吞吐 | **历史记录**——该 harness 是手动工程工具（`common/trafficsched/harness`） | 需要真实链路 |

### 来源（Provenance）

| 提交 | 类型 | 它做了什么 |
|---|---|---|
| `d2b34f1cf` | FEATURE | 旁路/套接字语义 profile |
| `3dea03fd5` | FEATURE | 每流判定 |
| `8a6e5f3cc` | FEATURE | 资格模型 |
| `ff80ddea6` | FIX | 携带 ICMP `Port` 的旁路被改写为 `ActionFlow` |
| `497d37727` | FIX | 路由地址集之前的 DNS 劫持顺序 |
| `386e95a70` | DOC | 记录 TUN 模式的限制 |
| `544c1ab5e` → `94df98f92` | FEATURE | 调度器与 `upload_rate` |
| `4e34d0091` | FEATURE | `upload_rate` 可接受的写法 |
| `bd7b1ed19` | FIX | `writePeriod` 饱和，而不是把 `float64` 纳秒转换成 `Duration` |
| `912ed1efa` | TEST | 记录一次间歇性失败 |
| `a10078557`, `bb21a854c` | FIX | 调度器生命周期与计数器（`Close` 会将计数器清零） |

除以下一项外全为 **OWN**：`e4f847ecf` 是 **EXTERNAL-ADAPTED**——QUIC 调优的 opt-in 来自读 `quic-go/masque-go` 与 `quic-go/connect-ip-go`，这两个库只设置了 `EnableDatagrams` 与 `InitialPacketSize`。这条归因有可引用的来源，所以用了 `EXTERNAL-ADAPTED`。

### AI 告诫（AI cautions）

1. **不要把旁路规则收紧成“配置为空”。** 随附拓扑会拒绝每一条流，包括字面 IP。
2. **不要通过忽略字面目标上的硬性单族策略来放宽它。**
3. **不要添加未做分类的 `DialerOptions` 字段。** `BlockerUnclassified` 会在运行时拒绝，反射测试会在评审时失败——这就是设计。
4. **不要为了让 L1 在 dashboard 设置中“生效”而移除 tracker 防护。** 它刻意只看 tracker 的*存在*就拒绝。
5. **不要把 L1 描述成在 TUN 模式下生效。** 它在那里没有数据面影响；想改变这一点，唯一途径是改 sing-tun。
6. **不要让 L0 了解域名、进程或协议。** 它在路由器之前运行，手里只有一个地址。
7. **不要移除 `JudgeFlow` 中的 FakeIP 下落处理**——FakeIP 目标会被操作系统黑洞。
8. **不要重排 DNS 与路由集的先后。** DNS 必须到达 DNS 路由器。
9. **不要给 `upload_rate` 加“安全系数”。** 那个数字就是速率；藏一个系数，配置就成了谎言。
10. **不要等到竞争发生才启动整形器。** 历史测量显示，等交互流量到来再启动 pacer，无法保护首次请求。
11. **不要只整形 NORMAL 通道。**
12. **不要豁免大于桶容量的写入。**
13. **不要让研究控制器变成可达的。** 一旦这样做，会有一个测试失败——原因是它的观测量衡量的是竞争，不是容量。
14. **不要在 kernel-splice 流上安装调度器门禁。** 它延迟不了这样的流，只会把快路径拿掉。
15. **不要把 `ClassDefault` 从拨号调速器的受限一侧移除。**
16. **不要把拨号调速器的软限制变成硬限制**，也不要重新引入轮询。
17. **不要在任何面向用户的文字中把 LANE 门禁当作全机 QoS。**
