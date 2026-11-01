# JiejieBox 工程架构、设计演变与系统性改造最终评估报告

> **报告日期：** 2026 年 11 月 1 日  
> **项目：** [Piggy-Cat-bit-shadow/JiejieBox](https://github.com/Piggy-Cat-bit-shadow/JiejieBox)（基于 [SagerNet/sing-box](https://github.com/SagerNet/sing-box) 的持续开发项目）  
> **讨论中核对的主仓库分支：** `testing`；最后一次读取到的 HEAD 为 `a0490ad214b72eee4eb57d01ee8a23d0dbefa097`  
> **已确认的最近正式发布：** [v0.1.36](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.36)，发布于 2026 年 11 月 1 日  
> **知识库生产基线：** `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`（对应 v0.1.34；**不是**上面所述的最新分支 HEAD）  
> **文档性质：** 综合技术评估、设计思想梳理、历史施工与发布回顾、证据边界记录。主要依据本轮长对话中已核对的 GitHub 主仓库源码、知识库、专项报告、提交记录和正式 Release Notes 汇编；**并非重新执行的完整代码审计或最新全平台性能测试**。  
> **整理原则：** 尽量完整保留前述回答中的事实、技术细节、历史实验与评价，不刻意压缩；将重复内容归并，明确哪些属于历史测试、源码证据、尚未闭环或被否决的方案。

---

## 目录

1. [执行概述与项目定位](#一执行概述与项目定位)
2. [为什么早期对项目的判断出现系统性偏差](#二为什么早期对项目的判断出现系统性偏差)
3. [JiejieBox 的总体设计思想](#三jiejiebox-的总体设计思想)
4. [共享核心与协议—架构的协作关系](#四共享核心与协议架构的协作关系)
5. [双栈设计的多轮演变与一致性](#五双栈设计的多轮演变与一致性)
6. [链式代理、PhysicalPath 与运行时选路](#六链式代理physicalpath-与运行时选路)
7. [NaiveProxy：原生服务端、Cronet 与安全边界](#七naiveproxy原生服务端cronet-与安全边界)
8. [MASQUE：CONNECT-IP、双栈、DNS 与 QUIC 数据路径](#八masqueconnect-ip双栈dns-与-quic-数据路径)
9. [XHTTP：移植、跨层修复与专项性能重构](#九xhttp移植跨层修复与专项性能重构)
10. [Mieru：SDK 深度整合与跨协议故障事实](#十mierusdk-深度整合与跨协议故障事实)
11. [其余协议与 TLS/HTTP 共享传输](#十一其余协议与-tlshttp-共享传输)
12. [TUN、Fast Path、Zero-copy 与缓冲区正确性](#十二tunfast-pathzero-copy-与缓冲区正确性)
13. [DNS、FakeIP、嗅探与路由元数据](#十三dnsfakeip嗅探与路由元数据)
14. [性能、规模、内存与低功耗专项](#十四性能规模内存与低功耗专项)
15. [大规模 Bug 修复、LX 经验吸收与对抗审计](#十五大规模-bug-修复lx-经验吸收与对抗审计)
16. [六大依赖 Fork、上游整合与 v0.1.25](#十六六大依赖-fork上游整合与-v0125)
17. [Apple、Android、Windows、Dashboard 与发布工程](#十七appleandroidwindowsdashboard-与发布工程)
18. [版本里程碑与 v0.1.5 → v0.1.6](#十八版本里程碑与-v015--v016)
19. [证据等级、技术限制与未完成验证](#十九证据等级技术限制与未完成验证)
20. [综合评价与最终结论](#二十综合评价与最终结论)
21. [参考资料与源码索引](#二十一参考资料与源码索引)

---

## 一、执行概述与项目定位

JiejieBox 不应被简单地定位为“增加大量协议、跑分优秀的 sing-box Fork”，更不能用某几个协议模块、性能微基准或上游代码来源来概括整个工程。

**更准确的定位是：JiejieBox 是围绕 sing-box 及其底层依赖生态进行的一项持续性网络系统重构与集成工程。其核心成果在于共享运行时与数据路径的优化、协议和架构的协作关系、跨组件语义统一、系统性缺陷治理、底层依赖深度维护、跨平台客户端适配，以及能够反复验证这些修改的工程体系。**

项目的改造覆盖了一条网络流的完整生命周期：

```text
应用 / 操作系统网络请求
          │
          ▼
系统代理 / TUN / Mixed / 专用 Inbound
          │
          ▼
DNS · FakeIP · 解析环境 · 协议嗅探
          │
          ▼
规则匹配 · Metadata · Proxy Group · PhysicalPath / ControlPath
          │
          ▼
双栈候选准入 · DNS 增量结果 · Dialer · 网络绑定
          │
          ▼
协议握手 · TLS / HTTP / QUIC · 加密与多路复用
          │
          ▼
共享数据路径 · 复制 / splice · MTU / PTB · 流量调度
          │
          ▼
网络切换 / 休眠恢复 / 资源退役 / 异常关闭
          │
          ▼
平台 Libbox / Apple NE / Android / Windows / Release
```

这并不表示所有流量在运行时都必然经过上述所有步骤；它表达的是整个工程中需要协调的责任范围。

从不同方向观察，可以识别六类彼此依赖的成果：

| 维度 | 主要目标 | 代表性工程 |
|---|---|---|
| 架构统一 | 消除互相矛盾的状态来源和决策中心 | `Scope`、`RuntimeCoordinator`、`PhysicalPath`、`routeflow` |
| 跨组件协调 | 使协议与核心共享事实，但保留协议状态差异 | DNS、双栈、Dialer、HTTP/QUIC、`FailureFacts`、资源退役 |
| 端到端效率 | 消除无效计算、复制、重建、唤醒和流量排队 | XHTTP POST 聚合、MASQUE Owned DATAGRAM、SRS 流式加载、上传整形 |
| 协议和平台正确性 | 保障真实协议、嵌套链路与异步网络生命周期 | Naive、MASQUE、Mieru、XHTTP、REALITY/Vision、TUN、移动端 |
| 缺陷治理与依赖维护 | 吸收外部缺陷经验，并维护自己的底层 Fork | LX 修复多阶段、六大依赖 Fork、上游差异审计 |
| 验证和交付体系 | 在持续升级中保留正确性和可追溯性 | 变异测试、源码锁、ABI 检查、CI、真机与互通报告、Release |

项目的价值并不只体现在这六个维度分别取得了什么成绩，而在于**它们共同服务于同一个目标：让一个复杂的多协议网络系统更加一致、可控、高效，并在后续升级中尽量保留这些性质**。

## 二、为什么早期对项目的判断出现系统性偏差

此前讨论中的核心错误不是忽略某个协议，而是采用了不正确的审计入口：

- **错误地以功能为单位评价。** 把 XHTTP、MASQUE、Naive、Mieru、双栈等当成孤立的新增功能，没有先建立完整架构图和共享运行时的职责图。
- **错误地以亮眼数字为主轴。** 关注某个吞吐提升或微基准，却未同时检查其缓冲区所有权、异常路径、故障恢复和长期回归成本。
- **缺少设计演变视角。** 看到当前实现，却没有追溯它经历的多轮施工、失败假设、被否决方案及修改原因。
- **低估重复验证与缺陷治理。** 将某次 LX 残余审计的少数修复当成此前整个 Bug 工作量，忽略集中修复阶段、后续 P1 审计和依赖内部的缺陷修复。
- **低估基础设施级成果。** 共享 DNS/Dialer、Scope、HTTP 传输、TUN 回包、跨平台 ABI、上游来源锁和发布流程，不像新协议一样显眼，却决定了整个系统能否长期维护。
- **把历史证据当作现状。** 部分报告只适用于特定 SHA 和平台；某项旧审计发现可能已被后续修复，某个尚未执行的数据路径也不能因为名称叫 Fast Path 就宣称加速。
- **未遵守项目自己的阅读流程。** 仓库的 `AGENTS.md` 已要求先读 `KNOWLEDGE/README.md`、生产基线、索引、模块地图、架构不变量及增量；还明确警告历史报告不能自动代表当前源码。

尤其应纠正两项认识：

**一是“统一”并不等于合并一切。** 项目多次拒绝万能连接池、第二套恢复权威或统一所有协议的状态机。目标是共享真正的公共能力，而不是隐藏协议差异。

**二是“Fast Path 获得资格”并不等于实际已经绕过用户态。** 普通 TUN 的 `ActionBypass` 与实际 Linux `auto_redirect` 内核绕行是不同层面的机制，后续 splice 又是另一个独立的数据路径优化。这一点必须按实际消费者和字节路径评价。

项目自己的 [知识库刷新协议](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/REFRESH.md) 已明确要求：重要工程工作应当按**“初始问题 → 设计演变 → 被否决方案 → 最终行为 → 证据”**记录，而不是只列提交和功能。这也应是理解项目的正确方法。

## 三、JiejieBox 的总体设计思想

从工程笔记、架构边界、不变量和提交演变中，可以归纳出以下设计主张：

> 在尽量保留 sing-box 原有协议体系与配置兼容性的前提下，重新厘清整个系统的决策权、资源所有权和生命周期，让不同组件既能独立实现自身功能，又能在全局约束下正确协作；以可验证的真实收益为依据优化数据与控制路径，并拒绝不必要的新状态机和资源体系。

此处是对项目设计的归纳，不是作者原文中的逐字宣言。

### 3.1 统一的是契约，不是每个协议的实现

新协议必须接受统一 DNS、Dialer、Route、平台事实和生命周期管理，但不意味着 MASQUE、XHTTP、AnyTLS、Mieru 必须共用同一个连接池或恢复策略。

Mieru 的两套 SDK Runtime、MASQUE 的 H3/H2 连接状态、XHTTP 的 XMUX 逻辑流都拥有不同的实际资源与关闭语义。强行将它们纳入万能恢复管理器，可能增加隐式耦合，而不是简化工程。

### 3.2 状态必须具有来源、有效时间和所有者

一项网络能力、一次 DNS 查询、一个恢复租约、一个 QUIC 失败、一个地址候选，都需要回答：

- 事实来自哪个网络和哪种来源？
- 属于哪一个 Generation、Epoch 或操作？
- 哪个组件拥有更新权？
- 何时可以用于新连接，何时必须退役？
- 后续错误能否归因给当前代理线路？

因此项目持续引入 Snapshot、Generation、Lease、Ownership、Commit、Retire 等概念。它们不是为了制造抽象复杂度，而是处理异步网络系统中的**旧事实干扰新执行**。

### 3.3 正确性优先于局部性能

零拷贝必须满足缓冲区交接契约；Fast Path 必须与正常路径语义等价；DNS 的过期缓存不能覆盖新环境；协议连接关闭不能丢掉最后的尾部数据；性能测试的测试工具本身必须能够被证伪。

项目多次接受极小的额外锁或分配，以换取正确性；也多次因为缺少证据主动拒绝看似更激进的优化。

### 3.4 保守策略不是全局的，而是按错误后果制定

这是一个特别有代表性的设计特点：

- **双栈能力未知时**：不擅自宣布 IPv6 不可用，避免错误拒绝可用连接。
- **Direct Fast Path 遇到未分类选项时**：保守拒绝绕行，避免静默丢失 Socket 语义。
- **启动期路径证明遇到未知叶子时**：报告 Unknown，而非将不完整信息当成确定不可能。
- **DNS 解析器暂时不可用时**：内部域名所有权不能自动转移到公共 DNS，否则可能泄漏。

这不是简单的全局 fail-open 或 fail-closed，而是按照**职责边界和误判的具体后果**选择安全方向。

### 3.5 通过减少无效工作，改善整体效率

你的性能优化不只是减少函数执行时间，更是在检查整条链路中有没有不必要的：

- DNS 重复查询及跨环境缓存污染；
- 协议间重复建立连接池、后台定时器和恢复状态机；
- 失败重试放大、空转和无意义的后台唤醒；
- 数据复制、对象分配与缓冲区替换；
- 只改变控制标记但不改变真实数据路径的“伪优化”；
- 不必要的多次选择、重复计数和无证据的大重构。

**核心原则：先定位真实成本，再在不破坏语义的前提下消除它。**

### 3.6 测试必须能够证明自己真的覆盖了故障

旧红新绿、变异测试、真实配置解码、真实对象图、错误注入、完整字节交付、负向测试、网络切换竞态、发布来源检查，都属于这个原则。

“测试通过”并不自动代表测试有效。一个永远为真的断言、从未走到目标分支的测试、拿错依赖模块版本的源码扫描，甚至绿色 CI 中实际跳过的目标，都不能算可靠证据。

参考：[工程笔记](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/ENGINEERING-NOTES.md)、[11 条架构边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/ARCHITECTURE.md)、[硬性不变量](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/INVARIANTS.md)、[提交索引](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/COMMIT-INDEX.md)。

## 四、共享核心与协议—架构的协作关系

### 4.1 整体组织方式

```text
协议与网络服务
  XHTTP / VLESS / Vision / Naive / MASQUE / Mieru / AnyTLS
  Hysteria2 / TUIC / WireGuard / DNS / Rule Set / Proxy Group
                        │
             共享事实、接口和生命周期契约
                        │
                        ▼
                 JiejieBox 共享核心
  ┌───────────────────────────────────────────────┐
  │ DNS / FakeIP / 地址族与 Dialer                │
  │ Router / PhysicalPath / ControlPath / 规则    │
  │ HTTP / QUIC 的公共传输能力                    │
  │ Scope / RuntimeCoordinator / ReferenceManager│
  │ Power Governor / 网络状态与恢复              │
  │ FailureFacts / 流量统计 / 上传调度           │
  │ TUN / Buffer / MTU / 复制与 splice           │
  └───────────────────────────────────────────────┘
                        │
             依赖 Fork、Libbox 和平台能力
                        │
                        ▼
 sing / sing-tun / sing-quic / quic-go / uTLS / Cronet
 Apple NE / Android / Windows / 服务端 / CI / Release
```

这是**职责关系示意**，不是每个协议都直接调用每个模块，也不是所有协议共用同一连接池。

### 4.2 三个关键公共设施

**`route/reference.go` / ReferenceManager：** 统一发现多个组件持有的可复用资源，按具体能力完成空闲释放、怀疑旧连接退役以及内存回收。`TrimIdleResources`、`CloseIdleConnections` 与 `RetireSuspect` 不是互相替代的命令：它们对活跃流和网络代次的要求不同。

**`common/runtimecoord/coordinator.go`：** 统一网络 Generation、恢复租约与过渡协调。旧租约被替代后不能释放新租约；发布新网络代次不等于立刻销毁所有旧资源。网络变化、休眠后复用和资源压力属于不同的更新原因。

**`common/httpclient/manager.go`：** 提供可共享的 HTTP 客户端与传输生命周期管理，但不会粗暴合并所有 H3 判定或每种协议的逻辑会话。复用能力还必须匹配目标 Origin、当前连接状态和协议需要的真实 HTTP 能力。

### 4.3 共享核心如何向不同协议产生收益

| 共享核心能力 | 被多个协议使用的意义 | 必须保留的差异 |
|---|---|---|
| DNS / Dialer | 同一网络能力、DNS 归属、接口绑定和目标解析语义 | MASQUE Bootstrap 与 Inner DNS 不能混为一谈；Cronet 的原生解析不一定受 Go Resolver 控制 |
| Scope / 运行时协调 | 统一启动、关闭、网络切换与异步旧任务约束 | 每个协议的实际连接池、会话或 SDK Runtime 仍各自负责 |
| 路由 / 路径证明 | 区分真实跳点与控制组，尽可能把错误前置到启动期 | 组选择、WireGuard Endpoint、SOCKS 与 CONNECT 的目标语义不同 |
| FailureFacts | 上层按可靠的失败事实决定重试和惩罚 | 协议内部仍负责判断自己的故障阶段 |
| 缓冲区和回包路径 | 公共层的一次减分配可能影响多个数据面 | 协议边界未提供 `WriteBuffer` 时，不能强行假定零拷贝 |
| Power / ReferenceManager | 减少后台活动和旧连接遗留 | 活跃流、网络恢复探针与空闲池不能用同一删除策略 |
| 流量计数、上传调度 | 让多协议共用一致的流量信息和可选整形 | 未配置速率时，应保持接近无干预；不同传输的 per-write 开销不同 |

### 4.4 横向七数据面审计

2026 年 10 月 20 日的[《六协议统一架构与执行效率全面审计报告》](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/SIX_PROTOCOL_UNIFICATION_EFFICIENCY_AUDIT_2026-10-20.md)虽名为“六协议”，实际按七个数据面对象检查：XHTTP、VLESS/REALITY/Vision、AnyTLS、Naive 的 Go 入站与 Cronet 出站、MASQUE L4、MASQUE CONNECT-IP、Mieru。

报告的重要结论不是“应该再创建一个抽象框架”，而是**当前最有价值的改动集中在少数已确认的边界成本和能力未接线处**。

例如，在那个历史快照：

- AnyTLS 帧头与 CONNECT-IP Owned DATAGRAM 已能直接使用缓冲区能力；XHTTP、Vision 等部分边界仍退化成字节流语义，这是一项有成本的协议特性，不一定值得重写。
- MASQUE L4 CONNECT-UDP 当时已有整批锁，却没有使用 CONNECT-IP 已验证的 Owned DATAGRAM 发送路径；这是明确的跨协议机制复用机会。
- 公共 TUN 回包 `portReturn.ReturnPackets` 存在额外分配；这是共享路径的开销，而不是单个协议的问题。
- Mieru 双 Runtime 是控制面统一、数据面独立的正例，**不应合并**。
- XHTTP 的逻辑流重置不能升级为共享 HTTP 连接故障；XMUX 三段式关闭是需要保留的正确性契约。

这份报告明确拒绝：万能连接池、第二路由缓存、统一所有失败记忆、强制 Vision 的额外 `io.Reader/Writer` 包装，以及给没有真实批次来源的 Hysteria2/TUIC 生搬硬套 Owned Batch。

**共享核心的优化既是复用收益，也是公共路径成本审计。公共层出现一个缺陷，就可能让多个协议共同承担；因此公共层必须比局部优化更重视正确性、边界和回归。**

## 五、双栈设计的多轮演变与一致性

双栈设计是理解 JiejieBox 工程思想最有代表性的入口之一。它不仅是 Happy Eyeballs 的参数优化，而是持续多轮重构、交叉审计和状态一致性验证形成的子系统。

### 5.1 可追溯的多轮演进

| 时间和阶段 | 主要工作 | 核心问题 |
|---|---|---|
| 2026-10-18 Phase 1 | 网络能力转移与观察 | 平台提供的 IPv4/IPv6 事实如何正确表达，何时有效 |
| Phase 2 | 候选准入与所有权 | 将网络能力、配置策略与候选集合联结，保护异步任务 |
| Phase 3 | 对抗性调试 | 验证“一条流只读取一次策略”，发现重复读取的真实问题 |
| Phase 4 | 完整验收与切网测试 | 验证网络变化时的状态发布、任务取消及资源交接 |
| 2026-10-20 | 跨组件架构审计与收尾 | 从 DNS、Dialer 延伸到代理组、SSH、恢复生产者及平台状态 |
| 2026-10-27 | 端到端补充审计 | 修复候选顺序覆盖优先级、本地绑定选择和 IPv4-mapped IPv6 判断等缺陷 |
| v0.1.31 前后 | 代理组候选穿透的继续修复 | 避免首个地址黑洞让另一地址族根本没有机会尝试 |

这些阶段不是四次全部推倒重写；其中既有设计施工，也有针对已有设计的反证、细化和验收。

### 5.2 双栈系统的分层架构

```text
网络能力事实层
  ├─ IPv4 / IPv6 地址与接口
  ├─ 全局 / 局域网 / Link-local 能力
  └─ 网络 Generation 与事实有效性
               │
               ▼
按流准入策略 admissionPolicy
  = 用户配置策略 + 网络能力快照 + 所有权信息
               │
               ▼
DNS 增量解析与候选规划
  ├─ A、AAAA 各自返回
  ├─ 域名 / 字面地址 / 恢复候选
  └─ 健康反馈与错峰竞速
               │
               ▼
候选执行与叶子拨号
  ├─ TCP / UDP
  ├─ 串行 / 并行
  ├─ Proxy Group / detour
  └─ 绑定实际 Socket 的拥有者
               │
               ▼
成功归属、败者取消、旧网络结果丢弃、后续重建
```

**网络事实不能代替用户策略。** 配置明确要求严格 IPv4 或 IPv6 时，候选恢复不能绕过该要求；但网络能力为 `Unknown` 时，不能自动等同 `Unavailable`。

**本地网络能力不能自动代表远端代理能力。** 本机没有全局 IPv6 出口，不意味着一个经远端代理访问的 IPv6 地址不可达。Link-local、Site-local 与公网 IPv6 也不应被一个布尔值粗暴概括。

**决策时间点应当稳定。** 一条连接的候选计划捕获一次 `admissionPolicy`，不能让不同候选在切网过程中混合读取两代网络事实；但阶段性的入口资格守卫依然必要，因为迟到、恢复、缓存和叶子拨号有不同的进入渠道。

**健康反馈只能调度，不能擅自改写硬性策略。** 某地址族近期失败，可以调整竞争节奏，但不能永久拉黑整个地址族；一次后续成功还需要能够修复健康判断。

### 5.3 关键设计细节

**增量 DNS：** A 和 AAAA 可以分别完成，较早候选先参与连接竞争，迟到地址可加入尚未结束的计划；不能为了“完整结果”阻塞用户连接，也不能让迟到结果越过旧 Generation 的所有权边界。

**并行 Dial 与错峰：** 快速失败时及时推进其他候选。无论是否处于同一代理组，一条流的超时、取消与候选胜负都要共享同一逻辑预算。

**连接交付：** 某次尝试返回错误却同时附带了连接，必须清理连接；旧网络完成的迟到连接也不能交付给新网络。

**地址规范化：** IPv4-mapped IPv6 在地址族判断之前应归类为 IPv4，避免严格策略绕过和错误筛选。

**代理组：** URLTest、Selector、LoadBalance 应将完整候选地址集合、逐流拨号选项和网络策略传给选中成员，不能在组边界只保留第一个 IP 后浪费共享超时。

### 5.4 对抗验证和成果边界

历史报告记录了 Phase 1 的 19 项变异检查、Phase 2 的 17,570 组能力/策略组合、Phase 3 的七种连接形态一次性策略捕获，以及 Phase 4 的切网压力验证和测试夹具竞态修复。

`admissionPolicy.admits` 的历史受控微基准约为 **11–22 ns、零分配**，说明增加严格决策边界不必引入高频额外任务。不过微基准不能证明全部网络环境的端到端时延收益。

历史审计还记录过代理组候选串行化、TUN 入口 IPv6 呈现能力启动期固定等边界。后来 v0.1.31 进一步修复代理组候选传递；判断当前是否完全闭环，仍须在对应 SHA 检查生产代码、平台和真实网络测试，不能用旧缺陷清单直接推断现状。

**工程价值：** 双栈改造通过明确事实来源、单流策略快照、阶段守卫和资源交接，让 DNS、Dialer、Proxy Group、网络切换和 TUN 在同一套地址族语义下协作。

来源：[四阶段最终验收](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/NETWORK_CAPABILITY_TRANSITION_PHASE04_FINAL_2026-10-18.md)、[跨组件收尾](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_CLOSURE_2026-10-20.md)、[双栈端到端审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/dual-stack-audit-2026-10-27.md)、[DNS 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/dns-demand.md)。

## 六、链式代理、PhysicalPath 与运行时选路

这部分不能被概括为“支持 detour 和多跳代理”。实际工作围绕**一条代理链的控制决策、物理跳转、目标解析归属、逐流策略传递、失败预算和数据路径能力**展开。

### 6.1 控制路径与物理路径是不同的对象

对于 `exit.detour = entry`，配置依赖的读取方向与真实数据先通过 `entry` 再抵达 `exit` 的方向不同。

项目将 `PhysicalPath` 和 `ControlPath` 分开建模，避免把 Proxy Group 当成一条真实网络跳点，也避免把跳点顺序倒过来。真实 SOCKS 服务器的逐跳链路测试，会检查每个节点实际收到的目标地址与配置预期是否一致。

选择器、URLTest、LoadBalance 是控制面上的选择与包装，不代表它们一定构成独立物理 hop。不过某些代理组仍会包装返回连接以管理生命周期，不能因此说组完全不参与数据路径。

### 6.2 DNS 的所有权必须沿链明确传递

连接下一跳代理服务器，与解析用户最终想访问的目标，是两件不同的事情。

项目针对 SOCKS4、SOCKS5、HTTP CONNECT 和 `destination_dns_ownership` 建立了更清楚的规则：代理服务器域名由该出站自身指定的 Resolver 处理；最终目标既可以保持域名交由远端处理，也可以按明确配置由本地解析。默认不能擅自改写已有远端解析语义。

这避免了双重解析、错误 DNS 泄漏，以及因为代理链包装而丢掉目标域名的问题。

### 6.3 配置路径证明与真实执行分离

`common/physicalpath` 与 `common/routeflow` 支持启动期的结构和能力检查。它们并不通过真实拨号、DNS 解析或消耗代理组轮询来“验证配置”。

- 缺失引用、无法成立的结构性要求和真实循环应拒绝启动。
- 能力不匹配可能只是警告，不能都当成致命问题。
- 无法证明的路径应报告 Unknown，而不是把局部检查写成确定成功或确定失败。
- 预览选择不应推进 LoadBalance 轮询游标；只有正式提交该流的选择才消耗对应位置。

`common/physicalpath` 在早期知识审阅中已记录约 **8,543 行 Go 代码（含测试）**，这不是一个只画路径图的轻量辅助函数。它代表一套提前证明配置和物理执行条件的基础设施。

### 6.4 嵌套代理组的失败与连接所有权

代理组的故障切换要满足两个约束：

1. **一条用户流共享一个有界重试预算。** 不能每进入一层嵌套组，就重新获得一套超时和重试次数。
2. **一旦提交选择，既有连接不能被后台健康变化任意迁移。** 一致性哈希的成员空间、Sticky Session 归属和实际叶子节点的健康惩罚需要有明确关系。

故障还必须区分：目标拒绝、代理链路失效、DNS 失败、本地接口不可用和用户取消。错误归因若不精确，自动切换就可能从保护机制变成故障放大器。

### 6.5 链式传输包装必须保留能力

链式代理的包装器若只保留 `Read/Write/Close`，就可能在不影响功能测试的情况下，丢失 `syscall.Conn`、`ReaderFrom`、`WriterTo`、半关闭或批量数据包能力，使原有 splice 和直接复制退化。

因此项目把能力在包装层之间正确传递，作为数据路径契约进行测试。

### 6.6 真实端到端验证

在[真实链路调试报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/traffic-path-chain-debug-report.md)中，项目构建真实 JSON → Box → SOCKS5/HTTP CONNECT/UDP/DNS 以及实际字节流的 E2E 框架。历史记录提及 47 个子测试，检查完整 payload、目标归属、路由 Metadata 和层级选路。

但内存中模拟的 TUN 和真实物理设备并非同一证据等级，不能因为这里的字节 E2E 通过就声称所有平台的 TUN 原生行为已经验证。

**工程价值：** 链式代理的改造是在建立“从配置、目标 DNS、选择、物理链、Socket 能力到错误恢复”的完整契约，不只是增加多跳配置灵活性。

## 七、NaiveProxy：原生服务端、Cronet 与安全边界

Naive 是项目中另一个具有完整协议栈和系统协作意义的子系统。必须严格区分两套实现：**Go 原生 Naive 服务端入站**与**依赖 Cronet Fork 的客户端出站**。

### 7.1 工程范围与历史规模

历史知识库审阅区间记录：

- `protocol/naive` 从上游祖先的 **4 个文件**发展为 **31 个文件**，涉及 **64 个 Fork 相关提交**。
- `transport/http` 同区间从约 **20 个文件**发展为 **90 个文件**，涉及 **137 个提交**；后续知识校准中，该模块的生产 Go 文件数和提交数又更新为 **97 个文件、149 个提交**（适用于对应基线）。

这些计数属于历史代码树的目录比较，不能直接解释为当前净新增代码量或全部原创工作，却能表明 Naive 与共享 HTTP 传输的修改范围。

### 7.2 原生服务端协议能力

服务端实现包括认证 CONNECT、H1/H2/H3、HTTP/2 padding、UoT v1/v2、伪装响应和访问控制等功能。关键不在于“有这些开关”，而是各条路径的语义和数据安全必须不同：

- HTTP/1 CONNECT 的原始载荷处理与 H2/H3 的帧化 Padding 不同。
- 前几个带 Padding 的帧需要遵守协议计数与长度边界，不能因为短写或失败刷新造成双方帧状态失步。
- UoT 的逐数据报模式允许目标逐包变化，不能只在认证时检查第一个目标。
- 伪装响应和认证失败路径不能错误暴露代理隧道行为。
- 预认证资源控制、来源 IP 上限、请求头超时和 HTTP 接收窗口都要在实际服务端生效，而不是配置解析器只是接受字段。

### 7.3 Naive 服务器安全工程

项目针对 SSRF、DNS Rebinding、代理递归回环、目标白名单与地址分类、逐包目标校验做了大量防护。

一个具体例子是服务端自身公网地址的 CONNECT：如果配置允许代理访问自己的公网 443，经过反向代理又转回 Naive，就可能产生递归代理链。项目使用限定地址和内部 Web ingress 重写，而不是直接放开该地址。

UoT 逐数据报目标检查还要考虑批量写入绕过普通写包装器的可能；“完成会话认证”绝不等于“后续所有目标都自动安全”。

### 7.4 Padding / Framing 的几何与协议完整性

Padding 需要严格限定完整帧预算、短写检测、刷新错误以及 headroom/rear room。某些历史优化最终被撤销，因为它虽然看起来更简洁，却改变了参考实现的帧行为。

相应审计记录包括 **3 B 头 + 65,278 B payload + 255 B padding = 65,536 B** 的几何约束。它不是为了凑一个漂亮的数字，而是防止上层缓冲区参数与协议帧实际容量不一致。

### 7.5 Cronet 出站与异步原生缓冲区

Naive 出站使用经过修改的 Cronet Go/Native 绑定。其关键困难不在于发起一个 HTTPS CONNECT，而是跨 Go/native 边界：

- 缓冲区交给异步回调后，必须保证回调真正结束前内存仍然有效。
- 取消、关闭、错误、失败回收和重复回调都要正确处理资源所有权。
- 首包缓存交接不能在回调仍读取时提前释放。
- 多 Engine 与单 Engine+Isolation Key 是不同的复用模型，不可把配置并发数直接当作实际物理连接数。
- HTTP/3→HTTP/2 可选回退必须保留调用者剩余时间预算，并正确区分 CONNECT 握手完成、目标拒绝和底层传输错误。

### 7.6 对共享核心的影响

Naive 服务端复用 HTTP 传输、Masquerade 与网络配置；Cronet 出站则需接受主内核的路由、错误报告、网络切换和资源退役契约，但其中 Chromium 内部的 DNS、CPU/RSS 与连接迁移并不能全部由 Go 侧直接观察。

因此“Naive 已接入共享核心”并不等于 Cronet 内部所有状态都被统一接管。

**工程价值：** 这是一项服务端协议正确性、安全边界、原生客户端桥接以及跨平台资源生命周期的联合施工，不是简单将 Caddy 的 Naive 代理拼到 sing-box 上。

来源：[Naive 与 HTTP 传输设计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/naive-http-transport.md)、[Cronet 底层集成](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/phase-04-cronet-apple-platform.md)、[v0.1.22 发布说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.22)。

## 八、MASQUE：CONNECT-IP、双栈、DNS 与 QUIC 数据路径

MASQUE 是体现 JiejieBox 协议实现、共享核心、双栈、零拷贝和资源生命周期协作的代表性系统。

### 8.1 历史工程规模与继承边界

知识库在上游祖先 `e4b7e692` 与历史审阅点 `a267e99` 的比较中记录：

| 范围 | 文件数量变化 | Fork 相关提交 |
|---|---:|---:|
| `protocol/masque` | 3 → 29 | 52 |
| `transport/masque` | 7 → 57 | 70 |

两个目录的提交可能重叠，也包含继承、适配或重写工作，不能相加成独立原创提交数。

MASQUE 的基础端点骨架和 DATAGRAM 概念存在上游来源；本 Fork 在其基础上建立和扩展了大量 CONNECT-IP 数据面、控制 Capsule、地址分配、DNS_ASSIGN、PREF64、MTU/PTB、会话和所有权能力。

### 8.2 CONNECT-IP 与 MASQUE L4 必须区分

MASQUE L4 CONNECT/CONNECT-UDP 处理的是 TCP 隧道或 `(destination, payload)` 数据报；CONNECT-IP 处理完整 IP 包，还涉及内部 IP 头解析、路由匹配、TTL、ICMP PTB、地址分配和设备/TUN 交互。

两者可以共享 HTTP/3、连接状态与错误分类，但不应被假定共用相同的数据平面或缓冲区路径。

### 8.3 Bootstrap DNS、内层 DNS 与 DNS_ASSIGN

```text
Bootstrap DNS：在隧道建立前解析 MASQUE 服务器
      │
      ▼
QUIC/H3/H2 建链，竞争候选以真实握手为完成标准
      │
      ▼
Session Configuration：DNS_ASSIGN、地址、路由、PREF64
      │
      ▼
DNS Policy：确定哪个域名由哪个解析环境拥有
      │
      ▼
DNS Execution：已有传输 / 隧道内部解析器 / Same-Connection DoH
```

这几层不能合并。

- Bootstrap 不能依赖尚未建立的隧道内部 DNS，否则形成循环依赖。
- DNS_ASSIGN 配置以不可变快照发布，能力切换、连接重试时仍应遵守对应会话的归属。
- PREF64 独立保存状态，不等于自动实现 DNS64 合成器。
- 内部域名解析器暂时不可用时，不能自动转给公共 DNS，造成所有权泄漏。
- 只有当前隧道**实际上**运行在满足条件的 H3 连接上，才能复用同一连接执行 DoH；“支持 H3”“池中存在 H3”“当前隧道为 H3”是不同事实。
- H3 回退 H2 后，DNS_ASSIGN 本身可能未变，但可执行的内部 DNS 传输能力已变，必须重新评估。

Bootstrap 的地址族竞速以 QUIC 握手完成，而不是 UDP Socket 创建为成功标准；未胜出的尝试要关闭，重新连接时重新读取可用 DNS 候选。

### 8.4 Owned DATAGRAM 与缓冲区所有权

MASQUE 发送路径经历了从多次复制到 Owned DATAGRAM 的改造。通过预留 Headroom 添加 Context ID、Quarter Stream ID 等前缀，成功交付后将数据包所有权转移给 QUIC 队列。

但是必须回答：整批发送中哪些包已被接受、哪些仍由调用方负责、失败回退是否正确撤销已经写入的前缀。项目曾修复过回退时 Context ID 重复添加，导致错误解释包头并静默丢包的问题。

历史 1280 字节数据报微基准显示，Owned DATAGRAM 路径相对于复制路径的处理速度约 **2.7 倍**；这是特定热路径微基准，不是公网吞吐提升倍数。

项目还观察到：单包走批量路径有时比直接发送慢，因此只在真正存在多个数据包时启用 batch。减少一次复制并不值得牺牲正确的所有权和完整错误语义。

### 8.5 MTU、外层 QUIC 与 PTB

至少有三个不能混淆的量：

1. **外层链路 MTU**——物理链路能够承载什么。
2. **外层 QUIC 包大小/UDP Payload Ceiling**——受封装和已证明 detour 容量影响。
3. **内层隧道 MTU**——通过 CONNECT-IP 向内层 IP 系统呈现的大小。

项目会根据已知的 detour 容量限制 MASQUE 的初始 QUIC 包设置，但历史报告明确指出：**初始包被限制，不代表所有后续 QUIC 包都有相同的硬上限**。

数据报过大时，还要按照 Context ID、QUIC DATAGRAM 容量和 ICMP PTB 规则决定反馈；不能一律静默丢包或错误转为 Capsule。

### 8.6 控制 Capsule 与恶意输入资源上界

Capsule 地址范围重叠检查曾采用二次复杂度实现。历史测试中，大量条目校验耗时从约 **13.5 秒**降到线性检查的约 **16 毫秒**，并对条目数量施加上限（8,192）。

这不是普通微调，而是避免异常输入让服务器在“拒绝恶意配置”的过程中消耗巨大 CPU 的安全工程。

### 8.7 会话生命周期与跨组件合作

MASQUE 的 Session、Endpoint、H3/H2 连接、TUN Device、网络 Generation 和资源池分别拥有不同的责任：

- CONNECT 返回 200 意味着隧道建立，而非普通 HTTP 请求生命周期结束。
- H3 旧连接退役不能错误关闭仍有使用权的逻辑流。
- Suspend/Resume、RestartSession 和 Scope Close 需要区分暂停、怀疑失效与真正网络重建。
- Endpoint 本地产生的流量后来接入 TUN 栈，共享流量日志上下文（v0.1.32）。
- QUIC 依赖 Fork 上的 DATAGRAM、EMSGSIZE 和路径大小语义必须与上层一致。

真实 IPv6 H3 PTB、WAN PMTU、特定独立对端的 CONNECT-IP 互通与移动网络迁移存在历史未完全覆盖的边界，不能用本地测试替代。

**工程价值：** MASQUE 不只是一个协议。它把 IP 网络端点、HTTP/3、DNS 归属、双栈、缓冲区所有权、嵌套隧道容量以及统一资源生命周期集成到一个能够持续维护的系统中。

来源：[MASQUE 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/masque-connectip.md)、[MASQUE 文档](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/masque.md)、[QUIC 双依赖 Fork](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/phase-03-quic-dual-fork-masque.md)、[MTU/几何契约](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/mtu-and-geometry.md)。

## 九、XHTTP：移植、跨层修复与专项性能重构

XHTTP 的工作不能只描述为支持 `packet-up`、`stream-up`、`stream-one` 和 `auto`。它包含适配既有成熟实现、确保与 VLESS/REALITY 的生产链路真实可达、XMUX 复用与连接生命周期、POST 上传数据路径重构以及大量互通和对抗测试。

### 9.1 适配与真实配置接线

XHTTP 的主要客户端传输实现来源于 LX 的成熟版本适配；这与项目独立完成的集成、Bug 治理和性能设计应分开归因。

实际集成涉及 H1/H2/H3 选择、XHTTP Session ID 与序号、URL 路径、查询参数、Header/Cookie、X-Padding、REALITY/ALPN/Vision 兼容、Build Tag、Schema 和 JSON 配置解码。

Phase 3.5 曾发现一个很有代表性的产品 Bug：**传输组件和测试已存在，但真实生产 JSON 解码器不认识 `type: xhttp`，导致用户无法加载配置。** 后来修复真实解码入口，并补齐配置往返测试。

### 9.2 LX 故障经验与 XMUX 生命周期

| 历史缺陷 | 处理的实际问题 | 需要保持的契约 |
|---|---|---|
| LX 061 | 下载响应等待造成建链死锁 | 上传建立后可以交付连接，下载响应随后关联 |
| LX 077 | 拨号 Context 结束误杀长连接 | 建立期 Context 与连接存活期 Context 分离 |
| LX 076 | XMUX 失败后无界重试 / CPU 空转 | 失败熔断、退避、成功恢复和请求预算 |
| LX 082 | HTTP/2 StreamError 类型泄漏 | 在传输边界处理，避免上层误读和循环空转 |
| LX 094 | 本地主动关闭被归因为线路失效 | 区分本地取消、EOF、超时和远端故障 |
| Phase 3.5 | packet-up 已取消调用仍可能返回连接 | 热池状态下依然遵守调用方取消 |

这些修复是相互关联的：提前交付连接之后，不能继续让建立 Context 控制整个长期会话；退役 XMUX 时也不能把一条逻辑流的 Reset 错误扩大到共享 H2/H3 连接。

XMUX 接入 Idle Connection Keeper、ReuseSuspect 与网络重置契约。空闲连接可回收，可疑旧连接停止接收新逻辑流，但现有流可以排空。历史设计中还使用“释放逻辑使用权 → 延迟关闭 → 退役后只关闭空闲资源”的三段式关闭策略。

### 9.3 2026 年 10 月 15 日的重大性能重构

最重要的瓶颈定位是：历史实现的 `packet-up` 可能每次 Write 都同步提交一个独立 HTTP POST，并等待响应；默认约 30 ms POST 间隔导致 VLESS 请求头与首包数据产生重复往返和节流成本。

项目不是单纯降低间隔，而是重新设计为：

```text
用户流 Write
    │
    ▼
有界字节队列（容量与背压）
    │
    ▼
单个 Flusher 读取并 Gather
    │
    ▼
合并成较大的有序 HTTP POST
    │
    ▼
XMUX Slot / H2 或 H3 传输
    │
    ▼
按序消费响应、失败中止并保持尾部数据完整
```

与真实 **Xray v26.9.30** 参考服务端的历史受控测试：

| 指标 | 改造前 | 改造后 | Xray 参考客户端 | 说明 |
|---|---:|---:|---:|---|
| packet-up 单流上传 | 2.12 MB/s | **26.8 MB/s** | 22.9 MB/s | TLS/HTTP2、相同参考服务端 |
| 下载 | 137 MB/s | 140 MB/s | — | 基本保留原有下行水平 |
| 建连速率 | 31.3 次/秒 | 325–744 次/秒 | — | 不同历史测量配置中的范围 |
| 建连 p99 | 31.7 ms | 约 4.3 ms | 约 4.35 ms | 与聚合首包例外相关 |

上传从 2.12 到 26.8 MB/s 约为**12.6 倍**，改造后在该受控测试条件下高于 Xray 参考客户端约 17%。不能将其写成所有公网、所有工作负载或当前版本的统一收益。

来源：[性能重构提交](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/1f86a13fe324eb98fb15baebfb892d3c1ed396cb)。

### 9.4 性能优化后的数据正确性

引入异步批量队列之后，新增的风险包括：

- 并发 Write 的顺序和数据是否完整；
- POST 某一序号失败后，是否还会错误发送后续序号；
- Close 是否会丢弃尚未 Flush 的尾部数据；
- 队列满时是否背压，而非无限增长；
- `RST_STREAM`、`GOAWAY` 和 HTTP 重连是否产生死锁；
- HTTP 传输和 XMUX 对连接数、逻辑流和统计的理解是否一致。

项目通过专门测试覆盖这些问题。历史记录中，512 次每次 8 KiB 的小写入可以合并为约 5–6 次 POST，并进行完整数据字节与顺序检查。

### 9.5 聚合窗口的反证实验

为了提高 POST 填充率，项目曾增加短暂 Gather 窗口，但发现它可能拖慢 VLESS 握手的最初两次小写入。因此前两次写入豁免等待，从后续稳态数据才开始聚合。

另一项“所有小 POST 都不等待”的提案最终被拒绝：测试发现碎小剩余负载被独立发送，造成约 **13% 上传吞吐损失**。

高 RTT 下进一步并发流水线化多个未完成 POST 的方案也受到测试约束：

| 模拟 RTT | JiejieBox | Xray 参考 |
|---:|---:|---:|
| 0 ms | 26.93 MB/s | 22.84 MB/s |
| 30 ms | 24.25 MB/s | 19.67 MB/s |
| 50 ms | 15.57 MB/s | 14.53 MB/s |
| 100 ms | 7.88 MB/s | 7.80 MB/s |
| 200 ms | 3.92 MB/s | 4.04 MB/s |

两者随 RTT 增大的衰减趋势接近，证据不足以证明引入更复杂的 POST 序号和失败状态机值得。因此**未以无充分证据的复杂优化替换现有机制**。

### 9.6 70 分钟历史混合负载验收

10 月 15 日的历史收尾记录包括约 **70 分钟、681 轮混合负载、0 次记录失败**，覆盖短请求、Ping-pong、上下行、并发流、空闲、故意终止参考服务器以及恢复；还针对 H3 注入延迟、丢包、周期性和永久中断。

测试期间还修复了基准工具自身的一个问题：Ping-pong 模式漏设读取 Deadline，导致服务端失联可能被误判成客户端卡死。增加 watchdog 并修复测量工具后，才能把客户端缺陷与测量装置故障区分开。

这种做法说明项目关注的不是一次性能数字，而是**优化之后的异常路径、资源上界和证明质量**。70 分钟不等于全部生产场景长期无泄漏。

来源：[对抗测试提交](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/31ba7ecc55f5deff6fceb43d77bebe2fc99cb7a9)、[专项收尾提交](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/b5bc2a8de42b683d620a94d4452b87d6d43c8988)、[XHTTP 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/xhttp-client-transport.md)。

**工程价值：** XHTTP 是“移植成熟协议 → 完整生产接线 → 解决跨层故障 → 识别真实瓶颈 → 重建上传路径 → 对抗审计与 A/B → 统一连接生命周期”的完整工程案例。

## 十、Mieru：SDK 深度整合与跨协议故障事实

Mieru 并非“调用官方 SDK + 增加 TCP/UDP 回退”。其工作覆盖架构选型、SDK 桥接、入站/出站、数据报优化、并发所有权、双 Runtime、网络重置和跨协议错误归因。

### 10.1 五类已有实现的比较与取舍

| 对照实现 | 吸收的优点 | 刻意避免的模式 |
|---|---|---|
| 官方 `enfein/mieru` | 协议本体、握手、MUX、TCP/UDP Underlay | 在主内核中再次实现协议 |
| mbox | 配置与适配思路 | 构造阶段启动、不完整 Close 和资源清理 |
| Hiddify | 握手/监听适配 | 无条件 Accept 退避拖慢正常客户端 |
| sing-box-extended | 在 Start 阶段启动 SDK | 预先固定第一个 IP、绕过双栈候选策略 |
| Mihomo | Stop、Listener Factory 和 Dialer 桥接 | 首次 Dial 才启动 SDK 和单 IP 决策 |

核心设计不是“选一个最好抄的实现”，而是判断如何让官方 SDK 遵守 JiejieBox 已有的 DNS、Socket、监听、路由、双栈、平台和生命周期契约。

### 10.2 SDK 桥接与网络能力归属

项目让实际 Socket 通过 `common/dialer` / `common/listener` 创建，保留接口绑定、路由标记、Namespace、TCP 参数和已有 DNS 策略。

SDK 的 UDP Underlay 对服务器地址有自己的预解析要求，项目采用桥接满足它的校验，同时不剥夺 JiejieBox 的真实拨号决策权。

### 10.3 UDP-over-Stream 路径优化与并发修复

在历史 Windows/Ryzen 4800H 的 1200 字节 UDP 帧微基准中：

| 操作 | JiejieBox 适配层 | 官方 SDK 包装路径 |
|---|---:|---:|
| 写入 | **118.8 ns** | 666.1 ns |
| 写入分配 | **64 B** | 1,280 B |
| 读取 | **79.83 ns** | 86.93 ns |
| 读取分配 | **0 次** | 2 次 |

项目以池化缓冲区和每个 UDP Association 自己的 Framer 降低额外复制。后来发现并发 `ReadPacket`/`ReadFrom` 可能竞争 Framer 暂存空间，于是加读取互斥，接受约 9 ns/包的微基准成本以保障正确性。

这些数字对应帧处理本身，不是完整 Mieru 网络吞吐的提升比例。

### 10.4 TCP/UDP 自动回退是独立的选择机制

Fallback ON 时使用两套 `transportRuntime`，分别持有自己的 Client、Config、Dialer、UDP Registry 与会话；选择器只保存偏好、冷却、探测和 Generation，不额外启动后台探测 goroutine。

- 发生可归因的主传输故障时，新连接可以尝试备用 Runtime。
- 历史设计初始冷却约 30 秒，并指数增加至 5 分钟上限。
- 冷却结束后的首条业务连接承担恢复探测，并发流继续使用当前备用通路，避免探测风暴。
- **偏好翻转不迁移、不停止已有会话**。
- 旧网络 Generation 的迟到结果不得改变新网络偏好。
- Fallback 关闭时不应构建第二套 SDK 对象树。

Mieru 的 TCP/UDP 回退，与 MASQUE H3/H2 回退和代理组多节点重试在概念上类似，但不是同一个状态机。强制合并可能导致不同资源间互相污染。

### 10.5 FailureFacts：从 SDK 局部缺陷抽象为公共能力

官方 SDK 某些路径用 `fmt.Errorf("%v")` 包装错误，丢失类型化 Socket 错误。Windows 系统错误文本还可能受语言影响，不能可靠地通过英文字符串分类。

项目在自己拥有的 Dialer 边界记录类型化拨号证据，再通过 `adapter.FailureFacts` 向 LoadBalance、URLTest 等消费者提供故障事实，而不是让它们猜测 SDK 的错误字符串。

| 故障事实 | 应有行为 |
|---|---|
| 代理服务器成功连接但目标拒绝 | 不惩罚代理服务器，也不必无意义切换 Underlay |
| 代理服务器 Socket 确认拒绝 | 可作为代理通路/备用传输的恢复证据 |
| 本地接口不可用 | 归为本地原因，不错误惩罚远端 |
| 域名解析失败 | 可以按已有目标/端点策略尝试替代，但不直接宣告服务器坏 |
| 用户取消 | 停止重试，不将取消归因为线路错误 |
| 无上下文的 EOF | 保持 Unknown，不制造确定性健康结论 |

两次尝试都失败时，后一个模糊超时也不能覆盖前一个已经确认的拒绝证据。

这项工作是典型的**协议问题反向推动共享核心演进**：Mieru 暴露了错误事实的表达缺陷，最终使多个代理组消费者受益。

### 10.6 资源生命周期与网络重建

项目修复了 SDK Stop 后 UDP `PacketConn` 可能遗留、部分启动失败未释放 Listener、`RouteConnectionEx` 返回后过早关闭仍交由 Router 使用的连接等问题。

SDK Client 不能简单 Stop 后重新 Start，因此网络切换采用新 Runtime 构造、启动成功、Generation 交接、旧 Runtime 清理的方式；新建失败不应直接破坏仍可用的旧实例。

### 10.7 互通、稳定性及明确限制

历史验证覆盖官方 `mieru`/`mita`、TCP/UDP Association、IPv6 回环、错误密码、Mihomo 对照和多轮压力测试。记录包括两轮短连接测试合计约 **6,000 次，0 次记录失败**；Fallback Selector 微基准约 **26.6 ns、零分配**。

报告也如实记录了落后结果：官方 SDK 同版本对照中某个 TCP Underlay 上传场景约有 6.1% 的差距，部分真实 `mita` 场景的首字节和 UDP 上传也不占优。SDK 固有的部分握手等待边界没有被假装完全解决。

**工程价值：** JiejieBox 没有重新发明 Mieru 加密协议，而是把官方协议 SDK 深度纳入共享网络运行时，并以此完善统一故障事实与恢复契约。

来源：[Mieru 集成总报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/mieru-integration.md)、[Mieru 最终专项验收](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/mieru-masque-final-acceptance.md)、[跨协议 FailureFacts](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/cross-protocol-failure-facts.md)。

## 十一、其余协议与 TLS/HTTP 共享传输

在强调 XHTTP、MASQUE、Naive、Mieru 时，也不能再反过来忽略项目对其他协议及通用 TLS/HTTP 栈的改造。许多真正影响全局效率的修复发生在**协议和公共复制、加密、Socket、复用接口之间**。

### 11.1 VLESS、REALITY 与 Vision

项目不仅提供基础 VLESS 与 REALITY，还实现了与新版兼容机制相关的加密能力、Key Share 处理、首飞 TLS 分片，以及 Vision 的数据写入路径。

REALITY 历史互通修复中，实际出现过：

- AEAD 附加认证数据错误地使用了另一份 Session ID 状态；
- uTLS 配置第二次克隆时丢失 REALITY Verifier；
- 最终形成 `reality verification failed`，需要对照真实 Xray 进行红绿互通验证。

Vision 又涉及 `sing-vmess` 内部数据布局和 `go:linkname`/反射偏移桥。为了避免依赖升级后静默破坏 ABI，项目对适用的依赖版本建立 Tripwire。

这种方案本身带有维护风险：编译通过不能代表内部字段布局仍正确；替换依赖时必须重新检查真实布局与安全边界。

### 11.2 uTLS、TLS 指纹与 ECH

项目维护自己的 uTLS Fork，处理过 Firefox 148、Safari 26.3 和随机指纹池的混合密钥交换兼容性问题。

一项重要的正确性修复是：**配置明确要求 ECH，但当前 uTLS 指纹实际上不支持 ECH 时，不能静默接受配置并继续运行。** 项目后来将这些组合提前拒绝，避免把“配置已加载”误写成“安全能力已生效”。

TLS 引擎还会根据 Apple/Windows/Go 平台能力做选择。ECH Retry 的触发需要确实拿到支持重试的错误和服务器提供的重试信息，不能将任何握手失败都升级为无界重复连接。

### 11.3 AnyTLS 与 ShadowTLS

AnyTLS 的会话是一条 TLS 物理连接承载多个逻辑流，因此某个逻辑流失败不能直接等同底层 Session 失效。

AnyTLS 已进行缓冲区帧头预置、Session 池、开流守卫、空闲退役、地址族策略和错误分类的多轮改进。其写入仍需要整条 Session 的写序列化，因为帧顺序属于协议正确性约束；不能为了看似更高并发将其改成独立流锁。

历史六协议审计指出，一缓冲深的 `push` 机制可能使单个慢读者阻塞共享 ReadLoop，且半关闭表达存在边界。这属于需要按当前实现和真实负载继续验证的方向，并非已修复的成果。

ShadowTLS 方面还有公网服务端安全工作：认证前读取设置超时、握手目标连接失败要清理资源，避免将真实超时误判为探测；连接包装器必须保留后续处理所需能力。

### 11.4 Hysteria2、TUIC、QUIC 双依赖链

项目并非只提升 QUIC 协议吞吐。`sing-quic` 与 `quic-go` 的双 Fork 涉及：

- Owned DATAGRAM 和批量数据包所有权；
- QUIC 包大小、初始包配置与路径已知硬上限的区别；
- `EMSGSIZE` 这类错误能否被解释为可恢复的路径事件；
- Trust Anchors、TLS 指纹、代理嵌套和消息归因；
- 网络切换、Session 生命周期和平台上的 UDP Buffer 语义。

例如 Hysteria2 的 `ChromeParrot` 历史实现会覆盖初始包大小，导致先前确认的路径上限失效；对应改造将硬上限作为独立配置能力应用。TUIC 则在后续版本完善了从已知下层隧道容量到 QUIC 数据包硬限制的传递。

但是 MASQUE、Hysteria2、TUIC 的 QUIC 版本路径不能被简单视为同一机制：它们接入的 API、真实 packet writer、Endpoint 和重建时机不同。

### 11.5 SOCKS、HTTP CONNECT、Snell、Shadowsocks 等既有协议

项目对既有协议同样修过实际缺陷和效率问题：

- SOCKS4 用户 ID、缓冲区首包交接、代理目标 DNS 归属。
- SOCKS5 可选认证完成的 TCP 预连接池，但不能套用到 UDP ASSOCIATE 或 UoT。
- Mixed/HTTP/SOCKS 的目标、缓存、半关闭、流量统计与异常分支。
- Shadowsocks 的 MTU、缓冲区几何、原地加密限制与测量后保留的必要复制。
- Snell 不同版本的跨平台协议与复制路径适配。
- WireGuard 的暂停、重新绑定、关闭/恢复和 Go TUN Stack 的协作。

这里的重要结论不是“每个协议都做了同样的优化”，而是：**公共层收敛之后，已有协议也必须重新检查是否真正消费了这些公共能力，能否正确传递上下文与资源契约。**

来源：[六协议统一审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/SIX_PROTOCOL_UNIFICATION_EFFICIENCY_AUDIT_2026-10-20.md)、[REALITY 互通修复](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/reality-interop-fix-report.md)、[uTLS 指纹决策](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/utls-fingerprint-decision.md)。

## 十二、TUN、Fast Path、Zero-copy 与缓冲区正确性

### 12.1 TUN 从“能收发包”演进为共享数据路径

JiejieBox 逐步使 TUN、Host Flow、端点本地流量、代理转发、协议包装和数据回传使用更明确的共享接口。

默认 TUN 栈在相关历史审计中是 Fork 的 Go Stack，而不是 gVisor。实际路径有包收取、JudgeFlow、NAT、路由预匹配、完整路由、协议拨号、回包和数据资源释放等步骤。

这使性能讨论不能直接套用别的 TUN 栈的内存、ACK 或 Packet Batch 结果。

### 12.2 Direct Offload 与真实 Fast Path 的区别

项目曾投入大量工作完善 Direct 的逐流资格判定，包括：

- Route 决策与真实 Socket 选项的两层资格；
- 接口绑定、Routing Mark、Namespace、FakeIP、目标改写、Metadata 与观察要求；
- 对不同协议真正生效的 DialerOptions 进行预计算；
- 没有分类的新字段默认拒绝绕行；
- 代理组显式策略不得覆盖用户的拒绝意图。

这些都属于**控制面授权**。但实际执行绕行必须由平台 TUN/redirect 消费者完成。

[《TUN Fast Path 实际价值审计》](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/tun-fast-path-value-review.md)给出了一个重要而克制的结论：

> `D0_VALUE = NO_DISTINCT_FAST_PATH`（针对普通 TUN 的组级 Fast Path 声称）。

它的含义是：

- 普通 TUN 的 `ActionBypass` 与 `ActionAccept` 最终进入相同用户态数据路径，不构成第二条真实高速通道。
- Linux `auto_redirect` 的 `nfqueue` 能实际消费旁路判定，将适用数据包按对应 mark 交还内核路由。
- TCP/UDP splice 是建立连接之后尝试的另一种自动加速，不依赖同一个组开关；是否成功取决于平台 I/O 能力。
- 系统代理本身通常不经过 TUN，已经是一条不同、较轻的数据入口；不能把“不进 TUN”重复计为 TUN Fast Path 收益。

组级 `fast_path: false` 的优先级后来被修正：对经过透明组的 Direct 流，它可以明确禁止旁路尝试；未设置与 `true` 在当时可达的普通形态中等价。对于普通 TUN，该改变影响控制判定，不改变实际字节路径。

项目没有为了宣传加速而新增无意义的自动/手动调度器。这项反证结果本身有重要工程价值。

### 12.3 Zero-copy 要看所有权，不是复制次数越少越好

几个核心例子：

**MASQUE Owned DATAGRAM：** 已接受数据报的内存归 QUIC 队列所有；失败批次中仍属于调用者的部分必须正确保留、回退和释放。

**AnyTLS 帧头：** 在原有 Buffer 预留空间直接写入帧头，可避免额外复制，但前提是底层有足够 Headroom。

**Naive Padding：** 在完整帧长度与尾部空间允许时就地构造，不能为了省拷贝跨越 Padding 几何约束。

**VLESS Vision：** 专有写路径需要遵守加密层和 TLS 层要求；强行塞入通用 Wrapper 可能导致额外分片、丢失半关闭或破坏内部契约。

**TUN ReturnPackets：** 公共回包路径的临时切片分配可能对所有数据面造成成本。10 月 20 日审计曾观察到单包约 166 ns/3 allocs 的历史实现；在 v0.1.22 的后续真实 dispatcher+portNAT 测试中，相关 Fork 改造记录从约 236–238 ns、72 B/2 allocs 收敛到约 **156 ns、0 B/0 allocs**。这两套数字来自不同轮次的基准记录，不应硬拼成同一 A/B。

**Shadowsocks：** 某些稳态读侧复制经测量和语义审计决定保留；去掉复制并非必然有端到端收益。

### 12.4 kTLS 与数据尾部完整性

10 月 28 日全内核审计发现过 Linux kTLS + splice 的潜在 panic 和 TLS 控制记录处理问题。后续源码审计说明部分相关路径已在固定 `sing` 依赖中修复，读取时还必须保证：收到有效应用数据后，即使随后遇到 close-notify 或控制错误，也应先正确交付尾部数据。

Linux kTLS 的真实运行时路径和 KeyUpdate 互通，不应被 Windows 上的跨编译或静态源码检查冒充为设备 E2E 通过。

### 12.5 MTU 与 Buffer Geometry 是共享契约

`common/bufgeom` 用于集中管理不同平台和协议的缓冲区几何限制，同时考虑头部、尾部、池化上限和 Race 构建路径。

JiejieBox 对 MTU 的统一不是把所有值都叫 MTU，而是严格区分链路、QUIC 包与内层隧道的不同层次。只有来源和单位明确的容量证明，才能传递给下游 QUIC 限制或 ICMP PTB。

**工程价值：** 项目既寻找能够跨协议共享的零拷贝能力，也持续检查公共复制器和 TUN 回包本身的成本；对“实际有没有执行更快的数据路径”保持基于源码与字节测试的判断，而非基于名称。

来源：[缓冲区所有权](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/buffer-ownership.md)、[MTU 与几何](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/mtu-and-geometry.md)、[TUN Fast Path 审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/tun-fast-path-value-review.md)。

## 十三、DNS、FakeIP、嗅探与路由元数据

### 13.1 DNS 是独立且广泛连接其他组件的子系统

DNS 的改造覆盖：

- 多上游 `stable`、`fastest`、`parallel` 分组；
- 同一查询协调与后台刷新去重；
- A/AAAA 地址族增量结果；
- 网络环境、策略 Epoch 与查询完成时的缓存提交检查；
- DNS 连接池及空闲退役；
- Endpoint 私有解析器和 DNS 所有权；
- QNAME 级 NXDOMAIN 跨记录类型负缓存；
- `adblock` 本地 NXDOMAIN 与真实上游错误的区分；
- FakeIP 签发、反向映射、持久化游标及退役地址归属；
- systemd-resolved、Tailscale 和平台 DNS 环境同步。

历史知识库统计有 **94 个 Fork 提交涉及 `dns/`**，并新增 `dns/transport/group`。这个数字体现广泛改动，但不能直接当作独立 Bug 数量或原创作者统计。

### 13.2 DNS 正确性的关键：结果归属与提交时效

异步 DNS 结果即使在旧网络上完成，也不能在新网络中无条件写回缓存。查询的执行环境、策略 Epoch、客户端子网和具体 Resolver 共同决定缓存键的作用范围。

这里需要区分：

- **DNS 查询允许交付给原调用者**，不代表它仍然可以向当前共享缓存提交。
- **DNS 客户端能够使用某个缓存结果**，不代表所有其他消费者都可以直接使用其地址族子集。
- **终止性本地策略结果**不能与真实上游 DNS 故障等同；否则会错误影响健康检查和备用解析选择。

### 13.3 QNAME 级 NXDOMAIN 负缓存

RFC 2308 意义上的 NXDOMAIN 是“该名字不存在”，因此在满足条件时，A 查询确认 NXDOMAIN 后，可为同一名字的 AAAA、HTTPS、SVCB 等其他类型节省重复上游请求。

但项目没有把所有负响应都扩大成名字级否定。需要单问题、空 Answer、有效 SOA、正的负 TTL 等条件，避免将 NODATA、SERVFAIL、REFUSED、CNAME 目标缺失等错误扩大。

缓存命中时按当前请求 ID、当前 QTYPE 和剩余 TTL 重建响应，不能简单重放旧包或无限延长缓存时间。

历史审计也指出 `rewrite_ttl` 与 SOA 头部 TTL 为零之间的组合还需补充精准回归测试，不能把未经验证的边界宣称已完全关闭。

### 13.4 `adblock` 与 `reject` 是不同的 DNS 语义

本地 `adblock` 按 DNS 规则或 Rule Set 直接返回携带 SOA 的 NXDOMAIN，减少真实上游请求，并允许客户端按负 TTL 缓存。

`reject` 表示拒绝当前请求，客户端可能更换解析器或继续重试；`adblock` 表示本地认为名称不存在。这两者的缓存及重试语义不同。

项目明确区分本地广告拦截结果和真实上游失败，不把本地拦截塞入普通上游负缓存。它不是新建一个完全独立的 DNS 拦截引擎，而是接入已有 DNS 规则系统。

### 13.5 FakeIP 不应变成第二个路由决策中心

FakeIP 负责地址到域名的映射、签发与退役，不缓存最终路由裁决。路由选择属于 Router，不能因为 FakeIP 命中了域名就把一次旧策略决策当成长期真理。

特别是网络切换、旧 FakeIP 地址退役、损坏的持久化游标和已经删除的规则，都会让“缓存最终路由”变成潜在的一致性问题。

### 13.6 协议嗅探的 Buffer 与 Metadata 契约

嗅探相关历史工程记录包括 **25 个 Fork 提交、新增 22 个测试文件**，不只是新增 WireGuard 嗅探器。

其工作涉及 HTTP/TLS/QUIC 解析、分片 QUIC ClientHello、初次嗅探计划与后续只针对 QUIC 的补充读取、缓冲区只借用视图、错误路径释放、SNI 域名规范化以及与 FakeIP 的已知目标交互。

历史测试注释记录过分片 QUIC 不受控时产生巨大的缓冲区放大；项目收紧了逐包拷贝范围、续传轮次和未能嗅探时释放路径。当前源码中可见 `maxSniffRounds = 64` 的边界，但某些历史数值缺少可在当前树中重跑的同等测试，不能视为当前性能证明。

嗅探只是提供 Routing Metadata，不应默认修改 Destination；真正的 FakeIP 反向映射转换与显式的目标改写动作应另外区分。

**工程价值：** DNS、FakeIP、Sniff 既是大量协议共用的前处理路径，也是网络切换、隐私、资源和路由正确性的集中交界处；对此的任何优化都可能同时影响整个内核。

来源：[DNS 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/dns-demand.md)、[Adblock 设计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/dns-adblock-action.md)、[Sniff 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/sniff.md)。

## 十四、性能、规模、内存与低功耗专项

### 14.1 效率目标不只包括吞吐

JiejieBox 的效率工作至少涵盖连接建立、数据复制、CPU、内存常驻、加载峰值、后台流量、设备唤醒、资源退役、并发冲突和真实网络拥塞。

不同指标之间不能互相替代：单连接 CPU 占用低不代表吞吐一定高；微基准 0 allocs 不代表网络端到端零拷贝；改进路由选择也不必然缩短接收端 RTT。

### 14.2 上传流量调度与共享瓶颈

项目建立了 `common/trafficsched`、`common/trafficclass` 和相关路由接线，通过显式配置启用聚合上传速率整形。

它不是单纯的“AI 高优先级先发”调度：当内核或网络发送队列已经充满，用户态优先级调整可能根本无法减少接收端延迟。

历史受控竞争台架中，4 条普通流共同争用约 2 MB/s FIFO，容量 85% 的持续聚合整形后：

| 指标 | 无持续整形 | 启用持续整形 |
|---|---:|---:|
| 高优先级接收端 p99 | 146 ms | **9 ms** |
| 平均队列积压 | 257 KiB | **15 KiB** |
| 持续上传后首条交互消息 p50 | 127 ms | **2 ms** |

项目还验证单纯改变排序、等交互流量到达才开始限速等方案效果不明显，因而保留持续整形，并拒绝缺乏可靠带宽估计的复杂自学习控制器。

这些结果只适用于当时明确的受控竞争场景，不应作为任意公网链路改善百分比。

### 14.3 HAKO / ProjectClash 规模审计

2026-10-17 的[HAKO 规模审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md)研究的是规则规模、节点冷启动和后台任务，而非单条代理链速度。

在历史规则集测试中：

| 配置 | 稳态存活内存 | 加载期累计分配/峰值测试量 | 查找成本 |
|---|---:|---:|---:|
| 100,000 域名 | 约 887 KiB | 约 4.1 MiB | 命中约 419 ns |
| 1,000,000 域名 | 约 8.3 MiB | 约 42.5 MiB | 命中约 429 ns |
| 100,000 IP-CIDR | 约 1.0 MiB | 约 7.8 MiB | 命中约 79 ns |

该基准将加载过程中禁用 GC 的累计分配作为保守峰值指标，不能直接等同任意生产环境的实际 `phys_footprint`。

节点冷启动测量表明，历史场景下 1,000 个已启动但未使用的 Trojan 节点没有带来额外 goroutine，常驻平均约 **2,035 B/节点**。这证明多数轻节点不应该在配置阶段就创建完整的运行时会话；但 WireGuard、Tailscale、MASQUE Endpoint、Tor 等重型端点可能具有不同的启动成本。

项目没有为了适配百万节点就立即重写所有存储结构，而是先区分已有的紧凑 matcher、加载期间的瞬时数组增长和确实提前启动的端点资源。

### 14.4 HAKO 对后台工作的两项实际施工

规模审计找到两个原先未受设备休眠约束的网络工作者：

1. **DNS 乐观缓存刷新。** 旧缓存允许服务时，后台刷新属于可以暂时推迟的非必要网络工作。它在设备暂停/深度空闲时被抑制，唤醒后恢复，且不影响正在使用的缓存结果交付。
2. **Dashboard 自动下载。** 非首次更新等待设备合适的工作窗口；首次更新不能被延后，否则新配置的界面可能始终空白。

两者都直接复用已有 Power Governor，而不是增加新 Listener 或第二套定时器。测试还验证关闭门禁后的突发网络访问确实会出现，避免“门禁测试永远通过”。

一条仍负责 WireGuard 恢复的后台工作在历史审计中被刻意保留，因为一刀切地屏蔽它可能损害隧道活性。**省电不能以停止必要的恢复观察为代价。**

### 14.5 Anywhere 低内存技术对照与实际收益

项目曾深入研究 Anywhere 的 Buffer Ledger、Arena、Timer、MultiplexerPool、流式输入和 DNS 缓存理念。

最终没有生搬硬套 Swift 式 Arena、全局字节 Ledger、新 Timer Scheduler 或第二连接池，因为已有 Go Buffer Ownership 和共享运行时可以覆盖相同需求，且缺少额外机制的真实收益证据。

真正发现并施工的问题是：远程二进制 Rule Set 在没有 `cache_file` 时先 `io.ReadAll` 整体下载，再进行本来就支持流式的 SRS 解析，产生无意义的完整下载副本。

项目改为直接解析 `response.Body`，再通过共同的 `publishRules` 发布；存在 Cache File 的路径保持原有行为。历史构造的有效 `.srs` 加 4 MiB 非压缩尾部负载中：

- 改造前累计分配约 **21,229,120 B**。
- 首轮改造后约 **108,376 B**。
- 对抗审计发现流式 EOF 截断可能被遗漏，修复完整性后约 **125 KB**。

修复不仅减少内存，还确保截断下载不能发布不完整规则，旧 Rule Set Generation 继续可用。

这是一项有反证与拒绝记录的真实施工，而不是“引入 Anywhere 的整个架构”。

来源：[Anywhere 交付总结](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/anywhere-delivery-summary.md)、[Anywhere 深度研究](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/anywhere-deep-study.md)。

### 14.6 内存压力、休眠恢复与低功耗

项目逐步区分：

- **网络变化**：旧网络事实和连接可能需要更强退役。
- **设备休眠/息屏**：减少无必要唤醒，不能直接破坏仍有效的 Session。
- **唤醒后复用可疑**：新流不应继续分配到可能失效的旧连接，但活跃流应按契约排空。
- **一般内存裁剪**：只裁剪空闲缓存和池，不应随意推进网络 Generation。
- **严重 OOM**：在有序、可协调的执行器里推进更强级别回收。
- **首次必要初始化**：不能因为设备当前不活跃就全部推迟，造成核心启动、Dashboard 首屏或必要数据缺失。

项目还涉及 Android 分级 Trim、Apple NE 内存预算和 `phys_footprint`、低内存 Buffer 几何以及定时器普查。

这套工作关心的不是某次 GC 之后 RSS 好不好看，而是：**一个长期运行的跨平台代理内核在无业务、后台、网络切换和内存压力下应该实际执行多少工作。**

### 14.7 性能方法论

[PERFORMANCE-FACTORS.md](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/PERFORMANCE-FACTORS.md) 强调测量时要：

- 区分建连、吞吐、上下行、单流/多流和真实底层 TCP 连接数量；
- 用交错 A/B/A/B 多轮中位，而不是先测完 A 再测 B；
- 确认没有额外 TUN/代理隐藏在测试链路中；
- 区分服务端缓冲区、反向代理、RTT、丢包和客户端内核瓶颈；
- 将原始历史测量与当前部署、当前依赖、当前构建的结果明确分开；
- 不为了更好看的数值盲目增加大缓冲、更多连接或关闭正确性校验。

**工程价值：** 这套效率工程既包含大量正收益，也包含主动否定无效优化。能测量并拒绝复杂度，和实现一种更快的代码路径同样重要。

## 十五、大规模 Bug 修复、LX 经验吸收与对抗审计

如果只看“新增协议”和“专项跑分”，就会严重低估 JiejieBox 的工作强度。项目有一条长期、持续且有方法论的工程主线：**已知缺陷的复现与修复、外部项目故障经验的吸收、跨组件对抗审计、测试本身的反证、后续残余问题清理和依赖内核的修复。**

### 15.1 历史提交数据说明 Bug 工作的规模，但不能直接当成独立 Bug 数

`KNOWLEDGE/PROJECT.md` 记录了历史区间 `e4b7e692..a267e99` 的 **1,834 个提交标题**：

| 分类 | 标题数量 | 区间比例 |
|---|---:|---:|
| `fix` | 538 | 29.3% |
| `test` | 316 | 17.2% |
| `docs` | 282 | 15.4% |
| `ci` + `build` + `chore` + `style` + `bench` | 204 | 11.1% |
| 上述流程/证据相关小计（test + docs + CI 组） | 802 | 43.7% |
| `feat` | 76 | 4.1% |
| `perf` | 37 | 2.0% |
| 其他或无标签主题 | 381 | 20.8% |

**注意：** 802 属于已列类别的小计，不能再次叠加。提交标题并不等于修复的唯一 Bug 数，历史重写会使一个上游逻辑改动对应多个 SHA；来源必须结合补丁、实际 Blob、历史文档和可信背景裁决。

即便如此，这组历史记录已足以说明项目不是“少量新功能配一些修复”，而是一项**大量测试、审计、修复和工程协调驱动的长期改造**。

### 15.2 LX 集中修复：不是后续残余报告里的四项

此前一度错误地只引用 `LX Residual Bug Sweep` 的四项修复。实际上，集中吸收 LX 的工作至少包括以下多阶段记录：

| 阶段 | 审计与施工范围 | 历史报告中的记录量 |
|---|---|---|
| LX Stability Audit Phase 1 | DNS、TLS、REALITY、WireGuard、SOCKS、TUN/Box、崩溃/死锁/竞态 | **15 条 FIX** |
| Runtime Lifecycle Phase 1.5 | 吸收 LX 041，扩展网络恢复与资源协调 | 关键恢复机制及配套改造 |
| Protocol Compatibility Phase 2 | XHTTP、REALITY、VLESS 加密、HTTP/QUIC 兼容 | **13 条 LX 条目标记为已修复或补全** |
| Phase 3.5 Adversarial Debug | 对既有实现和跨层接线对抗检查 | **10 项 P1**，另发现生产 XHTTP 配置接线问题 |
| LX Residual Bug Sweep | 检查早期集中修复之后的残余问题 | **4 项新增确认修复**，还有已覆盖、延期及不适用项 |

这些阶段之间存在重叠：有的是移植外部修复，有的是预防相同缺陷，有的是对移植后架构的新增审计。**不能简单相加为独立 Bug 数。**

### 15.3 第一阶段的具体缺陷示例

- **LX 046：** DNS Hijack 可能阻塞整个 TUN 包处理循环。
- **LX 045：** Trojan/VLESS TLS 禁用时仍构造 TLS Dialer，触发空指针崩溃。
- **LX 090：** REALITY Short ID 解码前未正确验证缓冲区容量，可能越界。
- **LX 071/072：** WireGuard 拨号、关闭或 Rebind 互相阻塞；关闭后接收循环出现 CPU 空转。
- **LX 070：** Box 并发 Close 与 WireGuard Device 发布存在竞态。
- **LX 085：** SOCKS5 UDP Relay 将未指定地址错误理解为本地目标，产生静默错路由。
- **LX 082：** HTTP/3 协议错误被误判为正常关闭，影响后续处理。
- **LX 069：** IPv6 Socket 初始化失败后错误地破坏仍然可用的 IPv4 Socket。

这些缺陷覆盖真实崩溃、活性、数据错误、协议兼容和跨平台接口，不是简单的代码风格修改。

来源：[LX 第一阶段完整报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/lx-stability-audit-phase1.md)。

### 15.4 第二阶段：带着故障历史设计新协议

LX 的历史 Bug 被用于约束 XHTTP、REALITY 和 VLESS Encryption 等后续改造：

| 代表性条目 | 设计时需要主动避免的错误 |
|---|---|
| LX 061 | XHTTP 下载响应等待造成 Dial 死锁 |
| LX 076 | XMUX 失败后无限重试 / CPU 空转 |
| LX 077 | Dial Context 结束后误关闭已经交付的连接 |
| LX 082 | H2 `StreamError` 被错误传播，引发上层空转 |
| LX 094 | 本地主动 Close 被误认为服务端故障 |
| LX 104 | H1/H2/H3 行为与 Xray 参考实现不一致 |
| LX 105 | VLESS Encryption 与 Vision 组合后的接线错误 |

这意味着你的工作不只是“看到了一个 Bug，就移植一个补丁”，而是**将外部故障经验提前转化为协议设计约束、状态机边界和回归测试**。

来源：[协议兼容第二阶段报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/protocol-compatibility-phase2-report.md)。

### 15.5 Phase 3.5：移植后再次审计系统耦合

这一轮尤其能体现你的工程方法：原有协议修复完成，不代表这些机制放进新的共享核心之后仍然不会出错。

历史 10 项 P1 修复包括 WireGuard 恢复触发丢失、RebindLease Generation ABA、DNS 错误记录被取消、H3 严格回退绕过、嵌套组消耗重复重试预算、DNS Election 跨 Generation 冲突、XHTTP Packet-up 调用方取消失效等。

同时发现 XHTTP 已经有传输实现和内部测试，但真实 `type: xhttp` JSON 不能通过生产解码器。针对这个问题补的是**真实产品调用链**，不是再增加一个只测试构造函数的单元测试。

来源：[Phase 3.5 交叉审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/phase3.5-consolidation-debug-report.md)。

### 15.6 LX 残余修复及判断纪律

后续 Residual Sweep 中确认并修复过 `libbox` 并发 Map 迭代、H2 Close 与阻塞写者死锁、WireGuard 被误识别为 uTP、Tailscale 运行图循环导致栈溢出等问题。

同一报告也把其他候选分别归为测试已有覆盖、延期或不适用。**没有缺陷不代表一定要修改；一个外部缺陷模式不适用当前 Fork，就不该为了“修了多少”而造补丁。**

### 15.7 依赖内部的集中 Bug 修复也不可忽略

例如：

- **LX 040 / sing-tun：** System Stack 的 TCP accept loop 遇到错误后退出，后续新连接持续失败；通过监听器重建、退避、关闭中断和回归保护修复。
- **LX 048 / gVisor：** TCP 握手状态和握手对象生命周期不一致，可能引起 nil pointer 崩溃；当时建立相应 Fork 和测试，后来产品逐步迁移 Go TUN Stack，未再将该 Fork 作为发布依赖。
- **sing：** SOCKS 解析、已缓冲首包、TCP 并行拨号取消、pause.Manager、splice 与关闭边界。
- **Cronet：** Native 异步 Buffer、失败路径资源释放、首包和握手对象生命周期。
- **quic-go/sing-quic：** QUIC 数据报所有权、数据包上限、错误恢复和 HTTP3 语义。

早期为某个架构阶段完成、后来因设计演进不再发布的修复，也仍然是实质工程工作；但不能误写成当前仍由该 Fork 提供能力。

### 15.8 10 月 28 日全内核生产级审计

[《全内核与依赖组合生产级审计报告》](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/KERNEL_FULL_AUDIT_REPORT_20261028.md)在当时 v0.1.30 附近记录：

- **P0：0**，未发现可可靠触发的全局级缺陷；
- **P1：2**，包括显式 TUN DNS 地址下的 DNS Hijack 绕过，以及 Linux kTLS splice 的特定 panic；
- **P2：10**，涉及竞态、QUIC 所有权、组候选、连接停滞、入口地址族、证书选项、ECH 和 Native 启动等；
- **P3：3**，涉及容量证明、关闭错误和规则刷新等边界。

报告对相关 15 项候选问题的置信度分别标注源码与测试证据，并明确哪些缺少真实设备和 Linux kTLS 环境。

后来的 v0.1.31 及相关提交修复了其中多项：TUN DNS Hijack、TUN Reset、代理组地址列表穿透、Naive 安全配置拒绝、TLS/kTLS 尾部数据、规则集关闭及部分 QUIC 所有权边界。

**不能把旧报告中的所有条目当成现在仍未修复，也不能因为后续有修复提交就把真实设备验证直接视为 PASS。**

### 15.9 工程层面的意义

这些工作共同形成一套相对成熟的流程：

```text
发现真实故障模式
      ↓
复现 / 来源与适用性裁决
      ↓
界定负责组件与真正所有权
      ↓
最小必要修复或底层 Fork 改造
      ↓
旧红新绿、对抗和平台编译测试
      ↓
跨协议 / 跨平台整合验证
      ↓
后续残余审计与回归约束
```

它与“多打几个 fix 提交”不同。其真正的工程价值是**在大规模持续修改中，使系统能够主动发现、解释并阻止类似故障再次进入生产路径**。

## 十六、六大依赖 Fork、上游整合与 v0.1.25

### 16.1 底层 Fork 不是被动的版本 Pin

主仓库的 `go.mod`、`release/dependencies.json`、`release/source-lock.json` 与专项报告记录了六条重要依赖维护线：

| 底层依赖 | 主要改造 / 对主内核的贡献 |
|---|---|
| `sing` | Socket/缓冲区/Copy、SOCKS、pause、WireGuard 和并行拨号取消等基础设施 |
| `sing-tun` | Go TUN Stack、Host Flow、NAT/Listener 生命周期、TUN Reset 和回包效率 |
| `sing-quic` | 向 Hysteria2/TUIC/MASQUE 传递 QUIC 包上限、DATAGRAM 与错误事实 |
| `quic-go` | Owned DATAGRAM、HTTP/3、错误分类、TLS 与 QUIC 缓冲区所有权 |
| `uTLS` | 指纹、Hybrid KeyShare、ECH/REALITY 与内部兼容性约束 |
| `cronet-go` | Native Naive 客户端、缓冲区异步所有权、Go/native ABI 和多平台资产 |

部分依赖存在多条独立开发线，尤其 `quic-go`。历史审计明确指出两条开发线之间有大量生产文件冲突，不能为了“统一依赖”就盲目合并。

### 16.2 一次依赖升级的真实工作流程

你的升级方式并不是看到新版本后简单更新 `go.mod`：

1. 检查上游真实祖先、History Rewrite 和同义 Patch，避免把“在当前 SHA 中不存在”误判成“逻辑未实现”。
2. 区分已继承、独立修复、需要吸收、需要适配和刻意拒绝的行为。
3. 分依赖处理版本差异、Go Module Path 和 ABI。
4. 在主内核中重新验证编译、关键资源边界和协议互通假设。
5. 在 Apple、Android、Desktop 的固定源码与发布资产中协调版本。
6. 保留真实 CI / 构建 / 字节资产证据，避免只凭工作流定义宣称已验证。

### 16.3 v0.1.25：跨依赖兼容性整合与稳定性收敛

2026 年 10 月 25 日的 [v0.1.25 Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.25) 是代表性的系统级维护里程碑，覆盖：

- `sing`、`sing-tun`、`sing-quic`、`quic-go`、uTLS、Cronet 的上游差异和具体同步；
- DNS 搜索域、Tailscale、系统 DNS 管理和解析缓存；
- TCP Fast Open、并行拨号取消、网络命名空间、协议关闭和 TUN 边界；
- 内存闲时释放、Darwin 内存压力与紧急回收执行器；
- Box Close/Reload 屏障和旧恢复任务隔离；
- QUIC、MASQUE、WireGuard 与平台环境相关的回归验证。

项目当时发现过上游 `testing` 历史重写：一个补丁可能已被 Fork 独立实现或以不同 SHA 吸收，不能盲目 Cherry-pick。

### 16.4 依赖施工阶段

历史六阶段整合报告包括：

| 阶段 | 主要改造 |
|---|---|
| Phase 01 / sing-tun | TUN Stack / NAT 关闭、TCP/UDP 生命周期、异步输入与回包 |
| Phase 02 / sing | pause.Manager、WireGuard、上下文取消和跨平台一致性 |
| Phase 03 / QUIC 双 Fork | Owned DATAGRAM、MTU、EMSGSIZE、MASQUE/TUIC/Hysteria2 相关接口 |
| Phase 04 / Cronet | 原生异步 Buffer、Naive、Apple 平台和 Native 模块资产 |
| Phase 05 / Tailscale / DNS | 采用适用的官方 Tailscale 开发版本，处理 systemd-resolved、解析和接口交接 |
| Phase 06 / Final Acceptance | 依赖 Pin、适用缺陷差异、单测、CI 和版本来源收尾 |

随后还有 Phase 09/10 的 `sing` 上游修复吸收与源代码收尾，并非 Phase 06 之后就停止维护。

### 16.5 Cronet 原生资产与 ABI 的特殊性

Cronet 的 Go 绑定、根/聚合模块、29 个受控 Native 子模块及大量平台 `.a/.so/.dll` 资产，必须保持相互可解释的版本关系。

历史报告验证过 **37 个 Native 文件与既定生成资产的字节一致性**，但这不意味着那轮在本机重新编译过全部 Chromium 原生库。必须把“固定字节一致”与“重新从源码构建并通过设备运行”严格区分。

### 16.6 来源锁定与持续发布

`release/dependencies.json` 表达计划采用的依赖版本；`release/source-lock.json` 冻结特定 Release 所需主内核、客户端、Go Fork、Cronet Native 模块及 Dashboard 的版本。

这让发布流程可以按相同版本和构建任务复用产物，避免同一内核 SHA 对应的不同工作流资产被误选。

历史报告的 [Phase 06 验收](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/phase-06-final-acceptance.md)记录了依赖版本一致、适用的缺失修复收敛、固定来源和 CI 证据，同时保留了真实设备和原生库重建限制。

**工程价值：** v0.1.25 应被评价为“跨依赖、跨平台、跨生命周期的一次系统性兼容与正确性整合”，而不只是一次普通依赖更新。

来源：[上游依赖集成总说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/UPSTREAM-DEPENDENCY-INTEGRATION.md)、[最终依赖集成报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/upstream-integration-final-report.md)、[Source Lock](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/release/source-lock.json)、[Fork 仓库列表](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/FORK_REPOSITORIES.md)。

## 十七、Apple、Android、Windows、Dashboard 与发布工程

这部分不是内核工作的附属品。统一核心的设计如果不能通过平台桥接、签名、构建和客户机生命周期正确兑现，产品行为仍可能与源码设计偏离。

### 17.1 Libbox 与 ABI 是必须独立维护的边界

主内核与 Apple/Android 之间经 gomobile / Libbox 交换数据，其对象布局、返回值类型和回调时序都可能形成平台特有问题。

历史实例之一是 `DeprecatedNote.Message()`/`MessageWithLink()` 从裸 `string` 改成 `*StringBox`，因为某种 gomobile 返回参数帧可能触发 `fatal error: bulkBarrierPreWrite: unaligned arguments` 这样的不可恢复错误。

这说明某些看起来“不必要”的接口改动，实际是在跨语言 ABI 边界上阻止致命错误。

### 17.2 Apple NE 与系统生命周期

Apple 平台提供网络事实、休眠/唤醒、接口变化和内存压力输入，内核应决定如何响应，而不是让 Swift 客户端自行重建一套独立的网络策略。

相关工作包括：

- NE 网络状态、接口能力和按流恢复的事实传递；
- SetupOptions、ABI、StringBox 与关闭事件通知；
- 减少不必要的 Network Extension 后台工作和低内存风险；
- Apple Client Source Pin、同一 Libbox 产物的复用；
- App 和 Extension 的版本、签名及 TestFlight 上传条件；
- 防止旧 Endpoint Registry 回调错误清除新连接。

真实设备验证和编译/模拟器验证仍应分别记录。

### 17.3 Android 的启动与 TUN / 核心生命周期

Android 侧也存在独立的 Service、JNI/AAR、VPN 生命周期、配置切换和原生 ABI 风险。

v0.1.34 的相关改动让 Android 服务在启动之前等待 Libbox 初始化完成，切换配置时区分相同服务模式下的 Reload 与不同模式下的 Stop→Wait→Start，避免资源仍在使用时重复创建 VPN 或核心实例。

历史工作还有 startProxy 的生命周期守卫、PlatformFacts 重入、按 ABI 的 libbox.so 与 AAR 一致性检查。它们与主内核运行时协调不是重复，而是平台启动和原生资源交接的独立边界。

### 17.4 Windows 特权服务安全与安装器

Windows 部分不能只被看成 GUI + 核心进程。

项目维护了独立 Daemon 安全模型，涉及：

- 每用户 Worker、命名管道及服务进程；
- 管道权限与实际 Peer 身份验证是两个阶段；
- 可执行文件/进程身份、Authenticode、签名者 Pin 和旧服务清理；
- 服务无法停止时安装/卸载不能静默报告成功；
- 证书过期与文件签名完整性、签名者身份要分别裁决；
- 平台构建标签、TUN 驱动、Namespace/Socket 错误和本地化错误归因。

相关历史审计可支持源码与局部签名验证结论，但不能直接证明每个安装版本都已完成整套 Daemon 端到端握手实验。

来源：[Windows Daemon 安全边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/windows-daemon-security.md)。

### 17.5 Dashboard、订阅与真实产品路径

v0.1.26 将默认 Dashboard 改为项目自维护版本，整合品牌与订阅配额，并增加真实打包归档、路径裁剪、首页、静态资源和 ETag 的验收。

这一点同样体现了“源代码存在不等于产品真的可用”：即使 HTTP 返回 200，归档路径错误也可能导致实际页面空白。

订阅的 User-Agent、`Subscription-Userinfo` 响应头和各客户端展示行为，需要明确谁负责生成、消费和校验。单纯修改所有 HTTP 请求的 UA 并不等于修复订阅用途的身份兼容。

### 17.6 CI、源码锁和 Release 是工程成果本身

近期版本体现了发布流程的持续优化：

- 同一 SHA 的 Libbox 只构建和复用一次，避免跨平台重复占用稀缺 macOS Runner。
- 从 Windows 发起 Apple 工作流，Mac 可以在 Libbox 就绪时跟随构建 TestFlight，而不必等待全部正式 Release 结束。
- TestFlight 与正式发布复用同一内核、Apple Client、版本号、构建任务 Run ID 和 Attempt。
- 上传前核对 Tag、CI 状态、最终 App/Extension 版本及构建号。
- 发布流程避免在全部资产就绪前提前公开不完整 Release。
- 构建标签区分默认、Darwin、Windows、Android 和裁剪版本，验证真正可达的协议而非仅源码中存在的注册函数。

v0.1.36 正式说明明确**没有修改代理核心数据处理或配置语法**，其工程价值是提高 Apple 产物复用、发布来源一致性和交付效率。

不能把“CI 配置声明了某个 Gate”当作“此 Gate 确实运行并通过”。项目知识库也明确记录有尚未接线的校验门禁、过时的 verify-evidence，和必须按实际 SHA 确认的发布结果。

### 17.7 跨平台协同的价值

统一核心和平台原生层之间必须有清晰边界：

- 平台提供事实，核心制定策略；
- 平台生命周期变化需要以明确事件/快照进入内核；
- 核心发出资源退役与网络恢复决策，平台执行真正可用的动作；
- ABI 和源代码 Pin 必须与实际发布产物一一对应；
- 跨仓库变更的验收不能只在主仓库上完成。

来源：[Libbox 平台契约](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/libbox-platform-contract.md)、[CI 与发布](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/ci-release.md)、[v0.1.36 Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.36)。

## 十八、版本里程碑与 v0.1.5 → v0.1.6

整个项目的长期改造不能只按单协议划分。正式 Release Notes 提供了一条比较清楚的工程演进时间线：系统先扩展协议与共享数据路径，再强化运行时生命周期、路径证明、网络恢复、性能和依赖一致性，最后进一步建立可持续的多平台交付体系。

### 18.1 重要版本时间线

| 版本 | 发布日期（按 GitHub 记录） | 工程主题 | 不能误解为 |
|---|---|---|---|
| [v0.1](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1) | 2026-09-30 | Native Naive、MASQUE/CONNECT-IP、共享 HTTP3/数据包路径、SOCKS 链、恢复上游注册 | 仅新增几个协议 |
| [v0.1.3](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.3) | 2026-10-06 | 上传整形、Traffic Class、LoadBalance、URLTest、双栈增量 DNS、Direct Offload | 一次参数调优 |
| [v0.1.4](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.4) | 2026-10-07 | 网络切换后的分级排空、重连、DNS 复用 | 简单断线重连 |
| [v0.1.5](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.5) | 2026-10-07 | 平台事实、真实流量和 Power Governor 驱动的低功耗机制 | 只降低定时器频率 |
| [v0.1.6](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.6) | 2026-10-12 | 全链路 TUN、路径证明、链式代理、XHTTP/REALITY、Lifecycle、移动端与构建整合 | 一次普通补丁版本 |
| [v0.1.8](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.8) | 2026-10-14 | 代理组 Fast Path 策略、Apple 源码与构建 | 所有 TUN 均直接旁路 |
| [v0.1.9](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.9) | 2026-10-14 | 发布资产集合、Draft-first、完整性与顺序修正 | 单纯换版本号 |
| [v0.1.10](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.10) | 2026-10-14 | 透明组 Direct 授权、Windows 信任检查 | 无条件放开 bypass |
| [v0.1.16](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.16) | 2026-10-16 | XHTTP 吞吐/长测、DNS Adblock、Windows 安装、FakeIP 和网络状态修复 | 只有 XHTTP 优化 |
| [v0.1.17](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.17) | 2026-10-17 | FakeIP 退役范围、Windows 旧服务清理 | 普通缓存维护 |
| [v0.1.18](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.18) | 2026-10-17 | 复杂代理链、双栈候选、能力传递与网络状态一致性 | 单一地址族策略修复 |
| [v0.1.19](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.19) | 2026-10-17 | 休眠期间 DNS、Dashboard 等后台网络行为治理 | 对所有后台任务一刀切停用 |
| [v0.1.20](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.20) | 2026-10-17 | 按流候选准入、单次策略快照、串并行/流包路径一致性 | 仅 Happy Eyeballs |
| [v0.1.21](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.21) | 2026-10-19 | Mieru 与回退、自适应 IPv6 入口、FailureFacts、MASQUE/QUIC/TUN | 仅添加 Mieru |
| [v0.1.22](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.22) | 2026-10-20 | Naive H3→H2、MASQUE L4 Owned DATAGRAM、TUN ReturnPackets 零分配 | 一次小型内存优化 |
| [v0.1.23](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.23) | 2026-10-21 | ShadowTLS 认证前连接与目标资源安全 | 仅调整 Timeout |
| [v0.1.24](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.24) | 2026-10-22 | TCP Half-close 存活观察与 Direct 交接后的资源释放 | TCP Close 参数变化 |
| [v0.1.25](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.25) | 2026-10-25 | 六大依赖 Fork 的系统更新、DNS/Tailscale、TUN、OOM、关闭屏障 | 只更新 go.mod |
| [v0.1.26](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.26) | 2026-10-25 | Dashboard 源码、品牌与订阅配额、真实资产验证 | 只替换网页样式 |
| [v0.1.27](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.27) | 2026-10-25 | DNS 单地址族空结果与真实失败区分 | 普通日志优化 |
| [v0.1.28](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.28) | 2026-10-27 | 网络交接顺序、旧连接/数据报退役、UDP 存活证据 | 直接杀掉所有旧连接 |
| [v0.1.30](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.30) | 2026-10-27/28 | 双栈、macOS 网络恢复、关闭数据保留 | 只有 AnyTLS 微调 |
| [v0.1.31](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.31) | 2026-10-28 | 全内核审计后 TUN、FakeIP、TLS/QUIC、组候选与生命周期修复 | 仅 Bug 数量统计 |
| [v0.1.32](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.32) | 2026-10-28 | Endpoint Host Flow 经 TUN Stack，共享流日志上下文 | 简单新增一个入口 |
| [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34) | 2026-10-29 | Apple/Android/桌面接线、订阅 UA、源码 Pin、构建流程 | 只有订阅 UA 修改 |
| [v0.1.35](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.35) | 2026-10-31 | Apple/桌面固定版本、文档与发布接线；内核无 Go 源码变更 | 新的数据平面升级 |
| [v0.1.36](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.36) | 2026-11-01 | Apple 单运行器、TestFlight 跟随构建、产物复用、标签/版本核验 | 改变代理核心协议行为 |

**时间备注：** 以上为发布历史的工程里程碑汇总；GitHub 页面 UTC 发布时刻与更新日志采用的北京时间可能跨日。个别条目在讨论中显示的日期以对应 Tag 页面为准，表中不代替正式发布时间的精确核验。

### 18.2 v0.1.5：设备生命周期与低功耗统一

v0.1.5 的关键并不是减少几个定时器，而是将平台设备状态、真实流量和后台工作许可接入统一运行时。

- 设备暂停状态与真实业务活动共同决定是否进入更深的空闲。
- 远程规则集更新、统计写入、某些非必要后台刷新可以延迟，但必须保留首次必要工作。
- 网络事件、唤醒和旧连接复用要有明确边界；不能把屏幕关闭等同于所有网络连接已经失效。
- 用户业务流量可以推迟进入深度休眠，但不能要求所有协议各自实现一套独立 Power Loop。

这为后续 Phase 1.5、统一 Scope、ReferenceManager、Mieru 传输恢复、XHTTP XMUX、MASQUE Endpoint 和移动端平台事件的接线提供基础。

### 18.3 v0.1.5 → v0.1.6：一次系统性架构整合

从 GitHub 比较接口的历史记录，`v0.1.5...v0.1.6` 范围内可见约 **602 个提交**。该数字包含各种类型的提交和可能吸收的上游内容，**不是 602 项原创功能**，但足以说明这是一次相当大的工程变更。

v0.1.6 的内容主要覆盖：

**从入站到传输的完整调用链：** 真实 TUN 收包、协议嗅探、规则、双栈 Dialer、出站传输与返回数据路径的联动测试。避免孤立组件测试通过，却在实际 Box 运行中无法交付数据。

**代理链与 PhysicalPath：** 控制选择方向、物理跳点、DNS 所有权、嵌套代理组、路径证明和启动前检查。

**XHTTP / REALITY / VLESS：** XHTTP 客户端初始集成、HTTP 版本选择、LX 兼容修复、REALITY 混合密钥交换、Vision 与 Encryption 的接口配合。

**DNS 与 Proxy Group：** DNS Group、缓存及健康状态、有界故障切换、共享重试预算、实际叶子节点健康归属。

**运行时协调：** 统一 Network Generation、RebindLease、恢复任务、Power Governor、旧连接退役和 Scope 关闭。

**TUN / 底层网络：** Go TUN Stack、Host Flow、嵌套 MTU、QUIC Packet Ceiling、真实 E2E 数据验证。

**客户端与构建：** Libbox 返回值适配、Apple/Android 桥接、Windows Daemon 以及跨平台 Build Tag/CI 和固定版本。

**尤其重要的时间边界：** v0.1.6 于 10 月 12 日发布，而 XHTTP 的大规模 packet-up 性能重构、10 月 15 日的对抗审计和 70 分钟长测，发生在此之后。不能把后续全部成果都算进 v0.1.6，也不能只读 v0.1.6 日志就遗漏之后的重大改造。

### 18.4 后续版本不是“修修补补”的尾声

v0.1.16 的 XHTTP 性能与 Windows 安装整合、v0.1.18 的双栈代理链、v0.1.20 的单流准入、v0.1.21 的 Mieru/FailureFacts、v0.1.22 的公共 TUN 回包和 Owned DATAGRAM、v0.1.25 的依赖大更新、v0.1.31 的全内核审计落实、v0.1.36 的 Apple 发布流水线，都属于不同方向的系统性演进。

因此不能假定“v0.1.6 完成架构、后面只是零碎功能”。后续不断有跨组件的缺陷发现、设计修正和公共基础设施改进。

## 十九、证据等级、技术限制与未完成验证

一份可信的项目评估不能把所有来源都写成同样的确定性。

### 19.1 证据等级说明

| 类型 | 可以证明什么 | 不可以直接证明什么 |
|---|---|---|
| `VERIFIED_SOURCE` | 某 SHA 下生产源码存在明确的控制流、所有权与错误处理 | 真实设备、真实公网或所有分支的行为均正确 |
| `VERIFIED_UNIT` | 指定测试在指定环境/源码下通过 | 与第三方协议或真实网络必然互通 |
| `MEASURED_LOCAL` | 某设备、负载、环境下确实测到指标 | 所有平台或用户都能获得相同比例收益 |
| `VERIFIED_CI` | 指定 SHA 的指定工作流实际运行且满足门禁 | 所有可选功能、未接线 Gate 或真机测试均已通过 |
| `RECORDED_HISTORY` | 历史提交/报告确实记录了设计和结果 | 当前 `testing` 仍具相同代码和性能表现 |
| `BLOCKED_BY_ENVIRONMENT` | 有明确未能执行的测试及原因 | 可以把跳过视为 PASS |
| `UNVERIFIED` / `UNKNOWN` | 证据不足或范围未覆盖 | 自行填补积极或消极结论 |

### 19.2 必须保留的具体限制

**真实跨平台：** 部分 Apple NE、Android VPN、Windows 真实 Daemon 安装和长时间网络迁移的 E2E 仍无法由源码、模拟器或交叉编译替代。

**QUIC/MASQUE：** 已限制的初始外层 QUIC 包不等于所有后续包都有硬上限；特定独立对端、NAT64/DNS64、真实 WAN PMTU/PTB 与迁移行为需要单独验证。

**Mieru 与 Cronet：** Go/native 错误传播和生命周期可以进行源码及部分互通检查，但 Native CPU/RSS、全部平台静态库重建及当前发布 ABI 字节内容不能凭主仓库代码推断。

**TUN Fast Path：** 普通 TUN 的 Bypass Verdict 并不构成真实内核绕行；splice 的成功交接还取决于平台与 IO，不能将尝试等同已执行加速。

**测试体系：** 某些历史测试未触及实际分支、使用过过期依赖路径、存在无法失败的断言或只在特定设备执行。项目已经多次修复这些问题，但不能将所有测试抽象称为“全部验证完成”。

**旧 Bug 报告：** 10 月 28 日审计中的多个缺陷在后续版本已修复或部分修复。当前 Bug 状态须按目标 SHA 对照源码，不能引用旧报告的严重等级当作现状。

**历史性能：** XHTTP 的 12.6 倍、MASQUE 的 2.7 倍、Mieru 的帧处理微基准、HAKO 的规则内存和上传调度器的 p99 改善，都与各自特定测试配置绑定，不是 v0.1.36 的普适测量成绩。

**Fork 来源：** upstream 继承、外部适配、独立实现、测试重写与后续 Revert 应分开。Git SHA 数、目录文件数和 Commit 标题本身不能裁定完整原创归属。

### 19.3 当前仍值得持续检查的工程风险

这些并非“当前全部确认存在的 Bug”，而是由本轮核对的历史报告和架构复杂性提示出的持续审计重点：

1. **共享核心的回归放大效应。** 改变 Dialer、HTTP Transport、Scope 或 TUN 返回路径，可能同时改变多个协议的真实行为。
2. **跨平台版本与 ABI 漂移。** Apple/Android/Windows 的实际产物、Libbox、Native Library、Pinned Go Fork 必须对应同一发布意图。
3. **异步状态时效性。** 网络变化、唤醒、DNS Cache、连接池、重载和恢复租约之间容易出现旧结果覆盖新状态。
4. **协议差异被过度抽象。** 为 DRY 而合并不同类型的连接池、失败记忆和复用状态，可能破坏真正的资源所有权。
5. **对测量装置的过度信任。** 缺乏真实 E2E 数据、基准工具 Bug、错误的对照基线或隐藏代理层，可能制造漂亮但没有意义的结论。
6. **知识库自身漂移。** 历史报告、README、知识基线和当前代码可能在不同 SHA，必须按 `AGENTS.md` 和 `REFRESH.md` 的流程核对。

### 19.4 建议采用的长期复检方法

对重大变更，建议始终维持：

```text
目标分支 / Tag / 精确 SHA
        ↓
生产基线与其后增量
        ↓
实际 Go Module 和子仓 Pin
        ↓
本次涉及的共享契约与不变量
        ↓
真实生产调用链 + 相关依赖源码
        ↓
明确的旧红新绿 / 互通 / 性能对照
        ↓
目标平台 CI / ABI / Native / 设备证据
        ↓
注明限制，并更新最终结论
```

这与项目现有的知识库方法论一致，不需要再创建一套相互竞争的“AI 审计系统”。

## 二十、综合评价与最终结论

### 20.1 首先应该明确：这不只是一个多协议 Fork

JiejieBox 当然保留 sing-box 的架构和众多上游实现，也吸收 LX、官方 Mieru、QUIC 社区、其他项目和已有网络协议栈的经验。

但是**来源归属和集成工程价值是两回事**。继承成熟协议不代表将它正确纳入复杂的路由、DNS、生命周期、Buffer、网络恢复和客户端体系就没有成本；反过来，也不能因为投入了大量适配就将外部协议的原创性归到本项目。

更准确的评价对象，是项目如何处理这些能力组合之后出现的问题。

### 20.2 最有代表性的设计特征

**第一，统一事实来源，而不是建立第二个决策中心。** DNS、Platform、Router、Dialer、Scope 都有各自的所有权，不允许某个协议越权推断另一个组件的状态。

**第二，统一跨层契约，而不是统一全部状态机。** 共享连接能力、错误事实、资源回收、网络 Generation；保留 Mieru 双 Runtime、XHTTP XMUX、MASQUE H3/H2 与 AnyTLS Session 的必要差异。

**第三，优化真实瓶颈，而不是追求概念上更先进。** XHTTP POST 聚合、MASQUE Owned、公共 TUN 回包去分配、SRS 流式解析有明确成本与效果；万能连接池、假 Fast Path、无证据的全局 Ledger、无收益的流水线化被拒绝或撤销。

**第四，正确性不是最后才补的测试。** 数据包归属、连接关闭、异步迟到、配置实际生效、第三方互通、文件截断与恶意输入上界，都被纳入设计而不是事后零碎打补丁。

**第五，维护成本被当成架构因素。** 上游差异、多个底层 Fork、Native ABI、客户端版本、Build Tag、Git 历史重写和发布资产固定，都有专门的施工记录与验收流程。

### 20.3 代表性工程案例如何彼此关联

| 看起来是独立工作的案例 | 实际延伸到的共享系统 |
|---|---|
| 双栈多轮改进 | DNS、网络能力事实、Dialer、代理链、TUN 与平台恢复 |
| 链式代理 | PhysicalPath、ControlPath、DNS 所有权、Proxy Group、FailureFacts、MTU |
| XHTTP | VLESS/REALITY、共享 HTTP Transport、XMUX、Buffer、Power、ReferenceManager |
| MASQUE | QUIC Fork、DNS_ASSIGN、PREF64、双栈、TUN、Owned Buffer、ICMP PTB |
| Mieru | 官方 SDK、统一 Listener/Dialer、独立 Runtime、FailureFacts、网络 Generation |
| Naive | Go HTTP 服务端、Cronet Native、SSRF/ACL、TLS、安全、连接生命周期 |
| HAKO / Anywhere | 规则集表示、加载峰值、后台预算、低内存档位、现有 Scope/Buffer 复用 |
| LX 与 Bug 集中修复 | 协议兼容、底层依赖、跨组件对抗审计和回归证据 |
| v0.1.25 依赖升级 | 六 Fork、ABI、TUN/DNS/Tailscale、资源执行器、发布锁 |
| v0.1.36 发布流程 | 固定源码、已验证 Libbox、Apple 签名、CI、Tag 与版本身份 |

这些改造形成一条持续的工程闭环：

```text
建立新能力 / 发现旧能力的局限
                ↓
识别共享核心和协议之间的责任边界
                ↓
发现真实跨层成本、故障或语义冲突
                ↓
最小必要的架构或数据路径改造
                ↓
对抗验证、真实 A/B 与外部互通
                ↓
吸收至多平台和多依赖协作体系
                ↓
通过持续回归和发布机制保留成果
```

项目的开发并不严格遵守上述线性顺序，而是多条主线反复迭代、互相影响。图示强调的是**方法的一致性**。

### 20.4 既要肯定成果，也要避免过度拔高

现有资料能够支持的判断是：

- JiejieBox 的实际施工范围远超典型“功能型 Fork”。
- 共享核心的优化与协议的深度集成是核心贡献之一，而不是附加工作。
- 大规模缺陷治理、跨层回归和多依赖维护是项目不可忽略的主要工程投入。
- 在多个受控实验里，项目确实取得了可量化的性能、内存或延迟改善。
- 多项被拒绝的优化及历史反证说明，项目有意识地控制新增复杂度。
- 多平台源码、ABI、发布资产与测试证据的管理，是持续维护这些成果的重要基础。

但仍不能在没有同构 A/B 的情况下断言：

- JiejieBox 在所有协议、所有平台、所有网络下都比原版 sing-box 更快；
- 所有历史 Bug 都已经在最新分支与真机上彻底关闭；
- 任意目录中的所有提交都是作者从零独立实现；
- 所有 QUIC、Cronet、Apple NE 或 Windows Daemon 路径都完成了生产级验收；
- 所有架构抽象都必然降低未来维护成本，而不会产生新的耦合风险。

严格保留上述边界，不会削弱项目已有成果的工程价值，反而能让评价可信且可复检。

### 20.5 最终定位

> **JiejieBox 是基于 sing-box 及其依赖生态的持续性网络系统工程改造项目。它以共享核心效率和跨协议正确性为中心，在保留协议特有语义的前提下，持续统一网络事实、拨号和路由决策、连接与资源所有权、生命周期及跨平台执行契约；通过大规模缺陷治理、底层 Fork 维护、真实性能实验、对抗性验证和固定来源发布，努力让复杂多协议能力能够高效、可靠、长期共存。**

**它的核心价值，不是某一项协议、某一次 10 倍性能提升或某个新框架，而是持续让这些复杂能力成为一个相互协作、行为可解释、资源可控、能够验证与维护的完整网络系统。**

这也是此前讨论中反复被低估的重点：**统一性、协调性与整体效率，并不是项目成果表中的三个单独条目，而是贯穿架构、协议、数据路径、客户端、依赖和交付流程的同一条设计主线。**

---

## 二十一、参考资料与源码索引

以下链接用于回看设计原文、历史测试和源码，不意味着链接目标的当前 `testing` 内容与当时报告 SHA 完全一致。评估新的版本时应以当前目标源码和独立测试结果为准。

### A. 项目总览、设计方法与来源

- [项目 README](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/README.md)
- [AGENTS.md：AI/维护者阅读入口](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/AGENTS.md)
- [KNOWLEDGE/README.md：知识库说明与证据等级](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/README.md)
- [KNOWLEDGE/BASELINE.md：生产基线](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/BASELINE.md)
- [KNOWLEDGE/PROJECT.md：相对上游的核心增量](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/PROJECT.md)
- [KNOWLEDGE/ARCHITECTURE.md：11 条架构边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/ARCHITECTURE.md)
- [KNOWLEDGE/INVARIANTS.md：硬性不变量与约定](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/INVARIANTS.md)
- [KNOWLEDGE/INDEX.md：模块与源码导航](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/INDEX.md)
- [KNOWLEDGE/COMMIT-INDEX.md：按主题分类的提交历史](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/COMMIT-INDEX.md)
- [KNOWLEDGE/REFRESH.md：知识刷新与来源裁决](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/REFRESH.md)
- [2026-10-31 知识库校准复审](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/CALIBRATION-2026-10-31-REVIEW.md)

### B. 共享核心与路径

- [六协议统一架构与执行效率审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/SIX_PROTOCOL_UNIFICATION_EFFICIENCY_AUDIT_2026-10-20.md)
- [运行时生命周期模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/core-lifecycle-runtime.md)
- [路径选择与启动证明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/path-selection-validation.md)
- [数据路径与流量调度](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/data-path-optimization.md)
- [缓冲区所有权与 splice](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/buffer-ownership.md)
- [Fast Path 的真实价值审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/tun-fast-path-value-review.md)
- [MTU、PTB、Buffer Geometry](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/mtu-and-geometry.md)
- [真实代理链调试报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/traffic-path-chain-debug-report.md)

### C. 双栈、DNS 和嗅探

- [双栈 Phase 4 完整验收](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/NETWORK_CAPABILITY_TRANSITION_PHASE04_FINAL_2026-10-18.md)
- [跨组件双栈架构审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md)
- [双栈跨组件收尾](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_CLOSURE_2026-10-20.md)
- [2026-10-27 双栈专项审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/dual-stack-audit-2026-10-27.md)
- [DNS 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/dns-demand.md)
- [DNS Adblock 设计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/dns-adblock-action.md)
- [Sniff 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/sniff.md)

### D. 重点协议

- [XHTTP 完整模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/xhttp-client-transport.md)
- [XHTTP 性能重构提交](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/1f86a13fe324eb98fb15baebfb892d3c1ed396cb)
- [XHTTP 对抗测试提交](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/31ba7ecc55f5deff6fceb43d77bebe2fc99cb7a9)
- [XHTTP 长测收尾提交](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/b5bc2a8de42b683d620a94d4452b87d6d43c8988)
- [MASQUE 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/masque-connectip.md)
- [MASQUE 配置和行为文档](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/masque.md)
- [Naive 与 HTTP 模块](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/naive-http-transport.md)
- [Mieru 集成报告](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/mieru-integration.md)
- [Mieru/MASQUE 最终验收](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/mieru-masque-final-acceptance.md)
- [跨协议 FailureFacts](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/cross-protocol-failure-facts.md)
- [REALITY 互通修复](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/reality-interop-fix-report.md)
- [uTLS 指纹兼容裁决](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/utls-fingerprint-decision.md)

### E. 缺陷治理、规模与依赖

- [LX Stability Audit Phase 1](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/lx-stability-audit-phase1.md)
- [LX Protocol Compatibility Phase 2](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/protocol-compatibility-phase2-report.md)
- [Phase 3.5 Adversarial Debug](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/phase3.5-consolidation-debug-report.md)
- [LX Residual Bug Sweep](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/lx-residual-bug-sweep.md)
- [全内核与依赖组合生产级审计](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/KERNEL_FULL_AUDIT_REPORT_20261028.md)
- [HAKO Scale Architecture Audit](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md)
- [HAKO Adversarial Debug](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/reports/core/HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md)
- [Anywhere Delivery Summary](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/anywhere-delivery-summary.md)
- [Anywhere Deep Study](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/anywhere-deep-study.md)
- [依赖整合总说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/UPSTREAM-DEPENDENCY-INTEGRATION.md)
- [依赖 Phase 06 最终验收](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/fork/phase-06-final-acceptance.md)
- [子仓库和 Fork 总表](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/FORK_REPOSITORIES.md)

### F. 跨平台与发布

- [Libbox / Platform Contract](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/libbox-platform-contract.md)
- [移动端电源与资源生命周期](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/power-mobile-lifecycle.md)
- [Windows Daemon Security](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/windows-daemon-security.md)
- [Build / Pruning / Release Profiles](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/build-pruning-release.md)
- [CI / Release 证据与门禁](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/KNOWLEDGE/modules/ci-release.md)
- [正式 Release 列表](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases)
- [完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md)
- [v0.1.5](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.5) · [v0.1.6](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.6) · [v0.1.16](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.16) · [v0.1.21](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.21) · [v0.1.25](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.25) · [v0.1.31](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.31) · [v0.1.36](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.36)

---

**报告结语**

> JiejieBox 的成果不能用“新增了多少协议”来理解，也不能用“修了多少 Bug”来完全衡量。它更重要的工程贡献，是在持续变更的基础上重新厘清共享核心与协议的边界：让共同能力真正复用，让状态与所有权不再互相冲突，让局部优化不会破坏整体正确性，并通过跨平台依赖、对抗测试和发布溯源将这些成果长期保留下来。
>
> 
> **统一性、协调性和整体效率，不是三个可选的附加目标，而是整个 JiejieBox 设计演变的共同主题。**

*本文根据上述长对话整理。所有历史基准、提交数量和阶段结论均应按其标注的源码和环境解释；后续版本发生变化时，以目标源码、固定依赖和新验证结果为准。*
