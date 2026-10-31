# 数据路径优化：Direct Offload、traffic class 与上传调度器（Data-path optimization: Direct Offload, traffic class, upload scheduler）

**`011f3fdcd99` 处的跨模块区分：** 当链上没有显式的 traffic-class 策略时，`route/traffic_class.go` 仍使用自动标签分类。这与已移除的 `fast_path` AI 标签回退（AI-tag fallback）是相互独立的机制；不要因为把两者混为一谈而移除或重新引入其中任何一个。两者都按流（per flow）决策，但它们回答的是不同的问题。

**范围（Scope）：** `common/dialer/profile.go`、`protocol/direct/outbound.go`、`route/bypass_verdict.go`、`common/trafficclass`、`common/trafficsched`、`route/dial_governor.go`、`route/traffic_class.go`。

**来源（Provenance）：** `common/trafficsched`（31 个提交、`0067b03` 处 18 个文件）与 `common/trafficclass` 均为 **fork 创建**；`traffic_scheduler` 与 `traffic_class` **在官方 sing-box 中不存在**，因此使用其中任一者的配置在上游无法加载。Direct Offload 是 fork 扩展，其资格规则比上游*更严格*。

**本子系统对 CI 至关重要：** `verify.yml:486` 对 `./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/...` 运行一个专用的 `-race` 步骤，并且 `scripts/ci/verify-upstream-assumptions.sh` 为其维护一份绊线（tripwire）清单（`docs/UPSTREAM-SYNC.md`）。

**在改动以下内容时加载本模块：** `traffic_scheduler.upload_rate`、`traffic_class`、旁路（bypass）决策、TUN `ActionBypass` 处理，或任何“让操作系统来路由这个”的优化。

---

## Part A — 直接卸载（Direct Offload）

### A.1 规则（The rule）

```text
SEMANTIC EQUIVALENCE, NOT CONFIG EMPTINESS
```

一个拨号选项只有在**用户态路径确实会把该选项应用到这条流上**时，才会使该流失去资格。一条流永远不会触及的选项，不构成拒绝的理由。

这不是纸上谈兵。随附的拓扑恰好只声明了一个 direct 出站：

```json
{ "type": "direct", "tag": "direct", "domain_resolver": "local-agh" }
```

把该配置与一份空配置相比，会拒绝**每一条**经过它的流，包括那些从不咨询解析器的字面 IP 流。

### A.2 两层，两个问题（Two layers, two questions）

| 层 | 问题 | 归属 |
|---|---|---|
| 路由器 | 给定其入站、嗅探出的域名、FakeIP 状态、tracker 与规则判定，这条**流**是否可以跳过用户态路径？ | `route/route.go` — `canFastBypass` |
| 拨号 profile | 对这条流，这套**套接字配置**是否会被平台自身的 connect 复现？ | `common/dialer/profile.go` — `SocketSemantics` |

两者都不重新实现对方。一个试图回答策略问题的 profile，只能对它看不见的元数据做猜测；一个重新读取拨号选项的路由器，则成为这些选项的第二种解释。

### A.3 分类及其来源（The classification, and where it came from）

该表是从 `common/dialer/default.go` 与 `resolve.go` 读出来的——**看的是每个字段实际被应用的位置**，而不是字段名。

| 作用域 | 选项 | 为何使其失去资格 |
|---|---|---|
| 始终 | `detour`, `bind_interface`, `inet4/6_bind_address`, `bind_address_no_port`, `protect_path`, `routing_mark`, `netns`, `connect_timeout`, `network_strategy`, `network_type`, `fallback_network_type`, `fallback_delay` | 一次绑定、一个 mark、一个命名空间、一个拨号截止时间或接口选择——平台自行执行 connect 时都无法复现 |
| 仅 TCP | `tcp_fast_open`, `tcp_multi_path`, `disable_tcp_keep_alive`, `tcp_keep_alive`, `tcp_keep_alive_interval` | 作用于 TCP 拨号器；经同一出站的 **UDP** 流仍然只是一次普通 connect |
| 仅 UDP | `reuse_addr`, udp source port, `udp_fragment` | `reuse_addr` 会到达 `listenConfig`，从不进入 `dialer.Control`；`udp_fragment` 是数据报套接字选项 |
| 仅解析 | `domain_resolver`, `domain_strategy` | 只在解析名称时被咨询——见例外 |
| 忽略 | `udp_fragment_default` | direct 构造函数会设置它；若把它当作运维者的显式声明，本 fork 中每个 direct 出站都会被拒绝 |

**作用域拒绝不属于选项分类。** `BlockerNetworkUnsupported` 与 `BlockerDestinationInvalid` 来自该流的目标（`common/dialer/profile.go:152-153`），`BlockerLocalFamilyUnavailable` 来自机器本身；它们在 A.8/A.9 中归属，而不在此表中。

**容易被搞错的例外：** 一个**硬性单族**策略（`ipv4_only`、`ipv6_only`）也会过滤**字面**目标——拨号路径会把它应用于最初的尝试，而不只是应用于已解析的候选。停留在“字面地址不需要解析器”的 profile，会放过一条用户态路径本会拒绝拨号的连接。因此有效策略是向**活动拨号器**询问的，而且只有*限制性*策略才算数：`prefer_ipv4` 是一种偏好，允许一切。

### A.4 两次失败即拒绝（Failing closed, twice）

1. **运行时。** 一个已设置但不在分类表中的字段会产生 `BlockerUnclassified`，因此这种遗漏的代价是错过一次优化，而不是丢弃一条套接字语义。
2. **评审时。** `TestEveryDialerOptionIsClassified` 对选项结构体做反射，直到每个字段都被分类才通过，并在失败消息中打印各作用域的含义。该表也不得命名已不存在的字段，因此一次重命名不会留下陈旧条目。

### A.5 存在哪些层，以及旁路实际发生在哪里（Which layers exist, and where a bypass actually happens）

| 层 | 谁承载字节 | 对本进程的每字节成本 | 在何处被兑现 |
|---|---|---|---|
| **L0** | 路由集地址策略（`route_address_set` / `route_exclude_address_set`） | 被旁路的流量不经过用户态路由器 | 非 auto-redirect 的设置可以把前缀传给 TUN 选项；auto-redirect 在路由器预匹配之前评估路由集。实际的路由安装与判定消费取决于入口与平台 |
| **L1** | 路由器的语义旁路——本节 | 零成本，**仅在 redirect 模式**下 | Linux `auto_redirect`（nfqueue） |
| **L2** | 在平台、套接字类型与连接路径允许时的内核辅助拷贝 | 取决于路径 | 有条件的，并非对每条流或每个 OS 都保证 |
| **L3** | 经缓冲区的用户态拷贝 | 取决于路径的拷贝与 I/O 成本 | 在适用处作为回退 |

> **L1 在 TUN 模式下不会旁路。** `tun.ActionBypass` 由 Linux nfqueue 处理器兑现，在那里它映射为带输出 mark 的 `NfRepeat`，数据包沿其原始路径离开。在 pinned sing-tun fork 所提供的每一个 TUN 栈中，`flow_dispatch.go:575-615` 都把 `ActionBypass` 送到它的 `default:` 分支 → `installAccept`（`:612-613`、`:625`），而三个栈都使用同一个 dispatcher（`stack_system.go:274`、`stack_go.go:202`、`stack_gvisor.go:103`）。只有 `nfqueue_linux.go:334-335` 兑现它。
>
> 读者应带上两点告诫：**gVisor 栈已被构建标签退役**（`cmd/internal/build_libbox/tag_policy_test.go:16-83`；`go.mod:367-386`），并且 `stack_gvisor_icmp.go:126`、`:188` 把 `ActionBypass` 与 `ActionFlow` 归为同一组——但一个**不含 Port** 的判定（也就是 `adapter/router.go:131` 发出的那种）会以完全相同的方式落到 accept。
>
> **因此对 TUN 客户端而言，这个资格决策没有数据面影响。** 一条被它标记为可旁路的流，仍然在用户态被代理。该决策只在 Linux redirect 模式中才有真实效果。

这些结论由针对 pinned sing-tun 修订版的行为绊线固定，而不是靠阅读代码固定：`TestANewTrackerOnABypassIsNeverCreated`、`TestABypassFlowIsNeverCounted`（`protocol/tun`），以及 `native_bypass_dispatcher_test.go`、`native_bypass_trace_test.go`。

**L0 不是“先路由器、再旁路”。** 它在路由器*之前*运行，因此只凭一个地址、别无其他来决策。它无法表达域名、进程或协议条件，也不得用来把一条 `DIRECT` 规则编译进路由集，除非该规则本身已经是权威的且仅涉及 IP。

**L0 的唯一边界——FakeIP。** 因为 L0 先运行，一个 FakeIP 地址（本进程铸造的占位符，在平台路由表中没有路由，且只有路由器才能还原其域名）会被交给操作系统并被黑洞（black-holed）。两种普通配置会走到这一步：配置了 `route_address_set`（其外的一切都被旁路），以及一个恰好覆盖 `198.18.0.0/15` 的 `route_exclude_address_set`。因此对于落在当前 FakeIP 存储范围内的地址，`JudgeFlow` 会下落到路由器。这一 L0 防护只查询 `fakeIPStore.Contains`，而不查询路由器的签发台账或退役范围历史：当前范围之外的地址仍可能在路由器执行那些检查之前就被路由集旁路。未配置 FakeIP 传输时该防护被跳过。不同的路由器检查见 `dns-demand.md` §3。

**DNS 始终优先。** `JudgeFlow` 中的顺序是：已配置的 DNS 地址 → 按端口识别 DNS → 路由集 → 路由器。正是这个顺序让基于 DNS 的广告过滤继续工作。它**不**声称能检测 DoH/DoT/DoQ，也不阻止 UDP/443 以拦截 QUIC。

### A.6 两个已修复的静默空操作——陷阱素材（Two silent no-ops that were fixed — trap material）

* 一个携带 direct 出站 **ICMP `Port`** 的旁路，会被 pinned sing-tun 的 `ForwardStage.judge`（`flow_dispatch.go:432-434`）改写为 `ActionFlow`。裁定是：携带 `Port` 的 `ActionBypass` 被改写为 `ActionFlow`；旁路静默变成用户态流，正是这个 bug 曾进入生产的方式。由 `TestBypassWithAPortBecomesAFlow`（`protocol/tun`）与 `fast_bypass_verdict_test.go`（`route`）守护。
* `JudgeFlow` 在 `dnsHijackByPort` **之前**检查路由地址集，因此 `8.8.8.8:53` 完全离开了 sing-box（`497d37727`）。实测为 4 次劫持，而期望是 5 次（或 0 次）。

### A.7 测量了什么（What is measured）

真实预匹配入口点上的决策命中率，基于一个模型化的流混合：

| 流形态 | 权重 | minimal | dashboard-enabled |
|---|---|---|---|
| 字面 IPv4 TCP | 55 | 允许 | 拒绝：tracker |
| 字面 IPv6 TCP | 15 | 允许 | 拒绝：tracker |
| 字面 IPv4 UDP | 15 | 允许 | 拒绝：tracker |
| 嗅探出域名的 TCP | 8 | 拒绝：嗅探域名 | 拒绝：嗅探域名 |
| FakeIP TCP | 5 | 拒绝：FakeIP | 拒绝：FakeIP |
| connected UDP | 2 | 拒绝：UDPConnect | 拒绝：UDPConnect |
| **合计** | **100** | **6 种形态中 3 种，占 85% 权重** | **6 种形态中 0 种** |

**dashboard 那一列的数字就是结论。** tracker 防护是绝对的：它依据 tracker 的*存在*而拒绝，而不是依据 tracker 会记录什么——因此接上 Clash API 或 dashboard 就会对所有流禁用 L1。这是正确的第一轮选择（静默丢失连接统计不是可接受的优化），但它意味着**L1 的收益只对没有 API 的设置才真实存在**。

L0 在其所在之处测量：无路由集 → 0/4 形态；`route_address_set: 10/8` → 2/4；`route_exclude_address_set: 198.16/12` → 1/4；位于当前 FakeIP 存储范围内的地址会下落到路由器，而不是走这条 L0 路由集旁路。

成本，来自 Apple M1 上的 `route/direct_offload_bench_test.go`：

| | 成本 |
|---|---|
| 决策，所有条件通过 | 14–41 ns，**0 次分配** |
| 决策，被一个廉价条件拒绝 | ~5–11 ns，0 次分配 |
| 仅出站 profile | 5.3 ns，0 次分配 |
| **流建立，已旁路** | **119 ns，0 次分配** |
| **流建立，用户态** | **1461 ns、2856 B、26 次分配** |
| TCP 拷贝，内核 socket 对（L2） | 4 / 32 MiB 时为 4742 / 5001 MB/s |
| TCP 拷贝，用户态缓冲区（L3） | 4 / 32 MiB 时为 3952 / 5567 MB/s |
| UDP 每数据报（1400 B） | ~66 ns，1 次分配 |

> **“用户态流建立”那一行是一个已知有问题的数字，在此引用只是为了把它退役。** `route/direct_offload_bench_test.go:139-141` 点明了它：旧基准测量的是它自己的 `net.Pipe` 夹具，*"reported 26 allocations and 2856 B per operation, and the code under test was none of them. A benchmark whose number describes its own fixture is worse than no benchmark, **because it is quoted**."*（中文：每次操作报告 26 次分配和 2856 B，而被测代码与其中任何一项都无关。一个数字描述的是自身夹具的基准，比没有基准更糟，**因为它会被引用**。）它在本文档引用它两天**之后**才由 `22d46966c` 修复，而 `docs/fork/direct-offload.md:225` 至今仍带着它。重新测量：**104 B、2 次分配**。其余各行是历史的 M1 数字，只会在其他硬件上大致复现——引用前请重新测量。
>
> **唯一有真实测试套件强制的成本主张是一个 `TEST`**，而不是基准：`TestBypassDecisionUsesNoAllocations`（`direct_offload_bench_test.go:354`）使用四次 `testing.AllocsPerRun` + `require.Zero` 调用，覆盖 `canFastBypass`、`CanBypass`、一次 FakeIP **拒绝**，以及 profile 查找。

**L2 与 L3 在 loopback 上无法区分，而这不是基准的缺陷。** Loopback 对用户态拷贝是最友好的情形，对零拷贝则是最不利的情形。每个基准都断言自己测量的是哪条路径，因此它们不会静默地收敛。

一项被发现并修复的成本：合格路径会记录一行 debug 日志，而它的参数是在 logger 判定该级别关闭**之前**就构造好的——每条合格流 5 次分配、约 95 ns，比决策本身还贵，而且恰好发生在本特性所针对的那些流上（`e27d58711`）。

### A.8 范围，以及刻意不在范围内的部分（Scope, and what is deliberately not in it）

卸载适用于：TUN 入站、TCP 或 UDP、字面目标、叶子实现 `adapter.BypassableOutbound` 的**单出站链**，以及通过全部资格条件的流。

本轮不包含：原生路径上的 tracker 记账（有 tracker 就拒绝）；`UDPConnect`（它有自己的套接字与 NAT 语义）；traffic class（原生 direct 流按构造就在上传调度器的托管域之外）。

> **在 `v0.1.9` 基线上已修正。** 本节过去把**出站链**列为超出范围，理由是*"a group introduces selection, lifecycle and accounting semantics of its own, and the selected leaf happens to be direct does not make those equivalent."*（中文：一个 group 会引入它自己的选择、生命周期与记账语义，被选中的叶子恰好是 direct 并不会使两者等价。）该理由对一个**不透明** group 仍然成立——而这正是 `loadbalance` 仍然拒绝的原因：它可能拥有所选中流的拨号与重试，因此它不是透明的。
>
> 改变之处在于 `fast_path` 特性增加了一个**狭窄且显式**的例外：当叶子之前的**每一个**元素都是透明的纯选择 group，**且**叶子仍然通过未作改动的 `BypassableOutbound` / `CanBypass` 检查时，`route/route.go` 现在会放宽 `BypassRefusedOutboundChain` 阻断，并且只对一个声明了 `FastPathTransparent() bool` 的 group 生效。没有该例外，这个特性会在所有 group 配置上都不起作用，因为被路由进 group 的流必然有 `len(chain) >= 2`。
>
> 在当前目标上，第一个显式的 group 策略获胜；没有显式策略时，通往真实 direct 叶子的透明链具有内在授权。显式 false 会抑制它。不透明 group 维持拒绝。授权仍然不能取代叶子与元数据资格检查。归属方是 `fast-path-group-policy.md` §6，能力契约及其按类型的表就在那里——本模块只记录 Direct Offload 的判定改变了形态。

### A.9 归因（Attribution）

* `route.BypassVerdict` 命名作出拒绝的路由器条件。
* `(*direct.Outbound).BypassBlockers` 命名 `CanBypass` 背后的套接字级原因集合，包括环境网络策略与自身地址守卫。它还携带 `BlockerLocalFamilyUnavailable`：**主机没有通往目标地址族的接口**（`common/dialer/profile.go:106-119`）。这是机器的属性，不是出站选项的属性，并且与已配置的 `BlockerFamilyStrategy` 不同——不要把两者合并。

两者都是诊断信息。**没有任何按流记录日志的行为**，而且该决策不构造任何字符串。

---

## Part B — traffic class 与上传调度器（traffic class and the upload scheduler）

### B.1 流水线是一个整体（The pipeline is ONE unit）

```text
common/trafficclass          → route/dial_governor.go        → common/trafficsched
classify the flow              bound the post-transition        pace the managed upload
                               dial burst
```

`route/traffic_class.go` 持有策略映射；`option/traffic_class.go` 持有配置。不要把这三者当作三个独立的包：改动 class 词表会同时牵动三者。

**Class 解析：** 对已解析的链按**从最外层到叶子**扫描；第一个显式策略获胜；一个显式的 `default` **会抑制整条链的自动检测**。因为 class 是从流实际经过的那条链解析出来的，所以经同一个 group 的不同成员的两条流可以携带不同的 class。

### B.2 上传调度器（The upload scheduler）

**托管域：** 仅限用户态代理的上行流。只有当内核 splice 拷贝路径对该流不可用时（`route.ConnectionManager.uploadStreamGate`），流才会到达本包，因此这里没有任何东西会与 splice 或原生 direct 旁路竞争。它**不是**全机 QoS。

**`upload_rate` 完全按所写的内容生效。** 这里**没有隐藏的安全系数**：配置 `8 MiB/s` 就放行 `8 MiB/s` 的托管上传。

**缺省、为 `0`，或 `traffic_scheduler` 段为空，都让调度器保持惰性**——它会观察到每一个托管字节并立即全部放行，**没有队列、没有锁、没有定时器**。不要求整形的配置就不会得到整形。这并不证明一份使用 fork 专有字段的完整配置可以被官方 sing-box 加载。

**当确实配置了速率时，整形器会持续运行**，只要进程存在就运行。这是刻意的；见下面的测量。

可接受的写法：`8388608` 与 `"8388608"`（字节/秒）；`"8 MiB/s"`、`"8MiB"`（二进制单位，`/s` 只是装饰——该字段始终是速率）；`"20 MB/s"`（十进制）；`"20 Mbps"`、`"16000kbps"`（比特，按 8 比特折一字节换算）。负值、未知单位、小数值，或会溢出 `int64` 的换算，都会**使配置失败**，而不是被接受后忽略——对速率限制而言，一个被静默忽略的值意味着配置声称了进程并不执行的策略。

### B.3 定下设计的四次测量（The four measurements that settled the design）

记录在 `common/trafficsched/doc.go`，实验在 `contention_test.go`/`rig_test.go`。指标是竞争下小体积高优先级消息对接收方可见的投递延迟，因为那才是产品目标。

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

前四行与什么都不做无法区分，而且它们不可能更好：正在延迟高优先级消息的那些字节已经处在发送方的接受窗口之内，超出用户态的可及范围。

**2. 在交互流量到达时才启动的 pacer 保护的是第二次请求。** 测量对象是一秒不间断批量上传之后的第一次 256 字节高优先级写入：无门禁 p50 127 ms；`ModeAdmission`（已武装）128 ms；`ModePacedNormalOnly`（持续运行）**2 ms**；`ModePaced`（聚合、持续运行）**2 ms**。因此：两种 paced 模式根本不需要武装。

**3. 整形也必须覆盖高优先级通道。** 两条 HIGH 批量流加一个小的 HIGH 探测：无门禁 p99 114 ms；`ModePacedNormalOnly` @85% 73 ms；`ModePaced`（聚合）@85% **8 ms**。只整形 NORMAL 通道会让队列深度与原来完全一样。实际交付的是聚合模式。

**4. 大于桶容量的写入要记账，而不是被豁免。** 记账被拆分：共享桶支付该写入必须看到的部分（至多一个突发，因此任何流都无法挟持另一条流），而发出该写入的流在其自身的未来中支付其余部分。一条每次写 256 KiB 进入 64 KiB 桶的流，在配置 1.70 的情况下被放行到 1.84 MB/s，而未整形的对照组达到 2.10。

### B.4 学习型速率经过研究，但未交付（A learned rate is researched and NOT shipped）

`adaptive_research_test.go` 被 `trafficresearch` 构建标签守护，**而没有任何东西引用该标签**——没有工作流、没有 Makefile 目标、没有脚本、没有发布 profile。本包中的 51 个测试运行时**零跳过**；研究类测试根本没有被编译进来。

**如果研究控制器有一天可以从生产代码到达，某个测试会失败。** 请精确地阅读这条主张，因为它比听起来更窄：

* 该守卫（`common/trafficsched/adaptive_controller_guard_test.go:20-63`、`TestAdaptiveControllerIsNotInProduction`）**没有构建标签，因此在每一次发布运行中都会运行**；
* 它是对那一个目录中非 `_test.go` 文件的**源码文本 grep，查找标识符 `adaptiveRate`**；
* 它带有一个阳性对照（该标识符必须存在于 `adaptive_research_test.go` 中），把 `//go:build trafficresearch` 钉在研究文件上，并把 `scheduler.go` 中的 `type RateSource interface` 钉住——因此它所保护的**接缝**同样被守护。

它**不会**捕获的：换个名字、放在别的包里的学习型控制器，或从 `box.go` 通过 `SetRateSource` 接进来的控制器。它只是针对一个标识符的源码文本守卫，而这正是 `docs/UPSTREAM-SYNC.md:89-94` 所反对的做法——诚实的解读是：在这里它是最*便宜*的可用控制手段，而不是行为性控制。

**该标签为何存在**（`bb21a854c`，*"test(traffic): stop the adaptive characterisation gating releases"*）：在完全相同的源码下，`TestAdaptiveRatePrototype` **在 GitHub 的 Linux runner 上确定性地失败，而在 macOS 上通过**——85% 的判定阈值报告 1.7719296 MB/s，而要求是 1.8605。把它打标签排除之外的替代方案是弱化断言，而该提交明确拒绝那样做。

**未来的 AI 若不知道这件事会失去什么：** 它会重新推导 AIMD 控制器、重跑同一个实验、看到 `-tags trafficresearch` 在 Linux 上失败，然后通过放宽判定阈值来“修好”它。

学习型速率为何不交付：显而易见的观测量——一次写入阻塞了多久——测的是**竞争**造成的等待，而不是路径的速率。在一条 2.00 MB/s 的链路上，一条流时它报告 1.29 MB/s，四条流时报告 0.35 MB/s，两次都非常稳定：一个稳定、精确、却**错误**的数字。一个 AIMD 控制器原型能跟随变化中的容量，但会**稳定在**容量处，而在容量处队列就建立了：

| | HIGH p95 | 批量 |
|---|---|---|
| 固定速率，容量的 85% | **8 ms** | 1.69 MB/s |
| 固定速率，容量的 100% | 42 ms | 1.99 MB/s |
| 学习型速率（AIMD 原型） | 62 ms | 稳定在 100% |

> AIMD 需要队列存在才能检测到自己太快，而本特性的存在目的正是消除队列。

**那些表是历史记录。** 它们可以从 `contention_test.go` 复现，但它们是用 `t.Logf` 输出的，而断言针对的是*其他*量（探测点之前的中位字节数、桶的上界、`rate × 1.15`、队列均值的比率）。**把它们当作用断言保证的数值来读是一个错误。**

### B.4b 模式阶梯——以及唯一交付的模式（The mode ladder — and the only mode that ships）

共存在四种模式（`common/trafficsched/scheduler.go:29-51`）。**生产代码中唯一提到某种模式的地方是 `route/conn.go:147` = `ModePaced`。** 其余三种只存在于测试与手动 harness 中，作为被测量的对照组。`option/traffic_scheduler.go` **只有一个字段**（`UploadRate`，`:50`）——**没有 `mode` 键；加上它就会暴露一个经测量与什么都不做无法区分的模式。**

### B.4c paced 路径的历史测量（Historical measurements of the paced path）

在发布基线上用 `BenchmarkStreamWriteTax/gate-paced-generous` 测量（速率 `1<<40`，即一个永远不会生效的速率）：

| 写入大小 | paced 门禁 | 惰性门禁 |
|---|---|---|
| 256 B | 5,462 ns，1 B/op，**0 次分配** | 比裸 writer 高约 9.8 ns |
| 4 KiB | **1,423,806 ns**，257 B/op，**3 次分配/op** | — |
| 64 KiB | **1,404,768 ns**，255 B/op，**3 次分配/op** | — |

两个后果，两者都重要：

1. **那个基准观察到了分配**，在 4 KiB 及以上时每次写入约 3 次——而 `bench_test.go:81-83` 声明整形写入绝不能分配（*"a shaped write that allocated would turn a rate limit into a garbage source"*，中文：一次发生分配的整形写入会把速率限制变成垃圾来源），且 `doc.go:99-106` 的“无竞争路径上不分配”**只对惰性路径成立**。没有任何 CI 测试覆盖 paced 路径的分配。
2. **那次运行在宽松的配置速率下，对其 4 KiB 与 64 KiB 用例观察到每次写入约 1.40 ms**，≈1.4 × `paceTick`（`scheduler.go:78`，1 ms 的 tick）。推断的机制是：`chargeLocked` 把 `f.owedUntil` 设为提前几纳秒（`:662-677`），随后 `flowTimeReadyLocked` 报告未就绪（`:701-710`），而唤醒路径包含 1 ms 的 `paceWakeLoop` tick（`:869-892`）。相应的粗略 47 MB/s 算术描述的是那一次具体测量，而不是可移植的吞吐上限：主机的唤醒延迟、调度、写入大小、配置与实现变更都会影响结果。

那次历史运行即使在宽松速率下，每次写入的成本也明显高于其惰性对照。在作出当前的性能主张之前，请重新测量目标构建与环境。

### B.5 无竞争时的成本（Cost when nothing contends）

在**惰性**路径上，一次写入比写入裸 writer 多约 **9 ns**，**没有分配**。这个数字可以复现（256 B 时 +9.8 ns，4 KiB 时 +5.6 ns）。更精细的主张——“约 2.5 ns 是包装器，其余是两次原子加载”——按所述方式无法复现：把该拆分当作历史上的 M1 分解，而不是一次测量。

---

## Part C — 拨号调速器（the dial governor）

`route/dial_governor.go`。

一次路径切换会成批地使连接失效：所有正在使用旧路径的流几乎在同一时刻失败，又在同一时刻重新拨号——TCP、TLS 与隧道握手的惊群（thundering herd）落在无线已经忙于重建的时刻。

**常量：** 窗口 **3 s**、槽位上限 **24**、预算 **500 ms**。窗口**只由一次排空式转换**打开（`route/conn_reclaim.go:348-352`），并在 `route/conn.go:384`、`:632` 处应用。

**窗口之外没有需要限制的东西。** 零值即为惰性，而且这是可证明的而非断言的：`open := time.Now().Before(g.windowEnd)` 对**零值 `time.Time` 为 false**，因此 `enter` 返回 `noopRelease`（`dial_governor.go:114`、`:126-128`）。由 `dial_governor_test.go:40` 固定。

**交互流量永远不会被延迟。** 类别表明有人在等待的拨号——`interactive` 或 `realtime`——永远不会排队，无论调速器正在做什么（`:89-91`）。只有对延迟没有期望的类别才需要等待：`default` 与 `bulk`。**`ClassDefault` 被刻意放在受限一侧**（`:85-88`），因为未配置的流解析到的就是它（大多数流量），而豁免它会让调速器只覆盖运维者碰巧标注过的流量——而惊群并不来自那里。

**该限制是软的。** 500 ms 预算用尽后，拨号停止等待并无限制地继续，因此调速器最多只能平滑一次突发的开端——永远不会无限期地推迟恢复，也永远不会全局串行化恢复。

**它等待的是一个 channel，不是定时器。** 它过去每 6–18 ms 重试一次，直到预算耗尽：一次轮询在整个 500 ms 内制造定时器唤醒、调度器唤醒与互斥锁竞争——在一个公开目标是*减少*手机上无线电与 CPU 唤醒的 fork 里。现在等待者会一直休眠，直到真的发生了某事：槽位被释放、预算到期、调用方的 context 被取消，或窗口关闭。`released` channel 带有一个令牌的缓冲，使唤醒可以合并（`:67-70`、`:173-177`）。

`enter` 返回一个 release 函数，调用方**必须**在包括错误在内的每一条路径上恰好调用它一次。

> **调速器自身注释中的两处不准确，记录在此以免被误“修正”。** `dial_governor.go:23-24` 说*"one atomic-free time comparison takes it out of the dial path entirely"*（中文：一次无原子的时间比较就把它完全移出拨号路径）。这对**优先级**路径成立——`isPriority` 在 `:106-108` 首先返回，此时尚未取任何东西——但**窗口之外的受限类别拨号仍要取两把互斥锁**（`:112` → `settings()` 在 `:183-190` 加锁，然后 `:113`），并且**在第一次这类拨号时分配一个 channel**（`:120-122`）。该注释描述的是常见的交互路径，而不是每一条路径。
>
> 另外，`route.isPriority`（`:89`）重复了 `trafficclass.Class.IsHighPriority()`（`common/trafficclass/class.go:88`）——**同一个映射的两份实现**，一份由调速器使用，一份由调度器使用（`scheduler.go:300`）。只改其中一份是一个真实的陷阱。

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

除以下一项外全为 **OWN**：`e4f847ecf` 是 **EXTERNAL-ADAPTED**——QUIC 调优的 opt-in 源自阅读 `quic-go/masque-go` 与 `quic-go/connect-ip-go`，它们只设置了 `EnableDatagrams` 与 `InitialPacketSize`。该归因有可引用的来源，这正是使用 `EXTERNAL-ADAPTED` 的原因。

### AI 告诫（AI cautions）

1. **不要把旁路规则收紧成“配置为空”。** 随附的拓扑会拒绝每一条流，包括字面 IP。
2. **不要通过忽略字面目标上的硬性单族策略来放宽它。**
3. **不要添加未做分类的 `DialerOptions` 字段。** `BlockerUnclassified` 会在运行时拒绝，反射测试会在评审时失败——这就是设计。
4. **不要为了让 L1 在 dashboard 设置中“生效”而移除 tracker 防护。** 它刻意依据 tracker 的*存在*而拒绝。
5. **不要把 L1 描述为在 TUN 模式下生效。** 它在那里没有数据面影响；改变这一点的唯一途径是修改 sing-tun。
6. **不要让 L0 了解域名、进程或协议。** 它在路由器之前运行，只有一个地址，别无其他。
7. **不要移除 `JudgeFlow` 中的 FakeIP 下落处理**——FakeIP 目标会被操作系统黑洞。
8. **不要重排 DNS 在路由集之前/之后的位置。** DNS 必须到达 DNS 路由器。
9. **不要给 `upload_rate` 加“安全系数”。** 那个数字就是速率；隐藏的系数会让配置成为谎言。
10. **不要让整形器在竞争发生时才武装。** 测量表明，在交互流量到达时才启动的 pacer 保护的是第二次请求，而不是第一次。
11. **不要只整形 NORMAL 通道。**
12. **不要豁免大于桶容量的写入。**
13. **不要让研究控制器变成可达的。** 一旦这样做，某个测试会失败——原因是它的观测量衡量的是竞争，而不是容量。
14. **不要在 kernel-splice 流上安装调度器门禁。** 它无法延迟这样的流，只会把快路径移除。
15. **不要把 `ClassDefault` 从拨号调速器的受限一侧移除。**
16. **不要把拨号调速器的软限制变成硬限制**，也不要重新引入轮询。
17. **不要在任何面向用户的文字中把 LANE 门禁当作全机 QoS。**
