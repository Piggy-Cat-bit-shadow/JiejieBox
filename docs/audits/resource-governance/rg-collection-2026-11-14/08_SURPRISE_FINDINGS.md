# 08 意外发现与下一轮研究问题（Surprise findings）

> 标注：`CONFIRMED` 源码/测试直接证据；`INFERRED` 有机制依据但未测量；`UNKNOWN` 无证据。
> 每条格式：证据 → 机制 → 影响 → 可行方案 → 代价/风险 → 建议。

## F1. 准入队列限制的是"创建"，但等待者上游已经持有 NAT 会话、goroutine 和首包

- **证据**：TUN 的 `UDPNat.getOrCreate` 在协议选择前创建 entry/channel/goroutine 并保留首包（`sing-tun udp_nat.go:383-455`；`stack_go_udp.go:221`、`:459-464`）；route 的准入发生在 outbound 创建 seam（`common/tls/client.go:197`、`protocol/anytls/admission.go`）。本轮实测 1000 并发 dial 有 189–255 个等待者，但 harness 里等待者不持有 NAT 资源。
- **机制**：permit 队列按"创建"计数；每个等待的 UDP 逻辑流在上游已经是一个 NAT entry + goroutine + 首包。
- **影响**：iOS 上 256 个排队 → 最多 256 套上游资源仍驻留；这是"局部有界、组合失控"的最直接形态。`CONFIRMED`（结构），具体字节量 `INFERRED`。
- **可行方案**：在 TUN→route 边界引入"逻辑流准入"（entry 前），或让 UDP NAT 把 entry 数与首包字节纳入统一预算；两者都需要先测出真实乘数。
- **代价/风险**：改依赖仓库或新增第二道 gate；排队位置变化会影响延迟与语义。
- **建议**：`KEEP` 为下一轮第一优先；先做设备/loopback 乘数测量。

## F2. 链式代理：一份 permit 可以对应多条物理连接

- **证据**：`HeldPermit` 的不变量是"一个逻辑拨号最多一份许可"（`common/resourceguard/held.go:5-31`、`common/tls/client.go:206-214`）；detour 链每跳都会建立自己的物理连接，但内层看到 HeldPermit 后不再申请。
- **机制**：gate 计的是逻辑拨号并发，不是物理 socket 数；depth=d 的链最多 d 条物理连接共享 1 个 permit。
- **影响**：32 个 permit 在 2 跳链上最多 64 条物理连接；预算语义在链式场景下被低估。`CONFIRMED`（代码），实际分布 `INFERRED`。
- **可行方案**：显式区分 "logical dial permit" 与 "physical creation permit"（例如每跳一个 cost token），或在 report 中记录 chain depth。
- **代价/风险**：两层 permit 会重新引入自阻塞风险（注释里已记录过）；需要新的 marker 语义。
- **建议**：`DEFER`，先把 chain depth 计入观测。

## F3. 后台测速与 DNS 刷新不读内存压力

- **证据**：`protocol/group/urltest.go` 与 `common/urltest/*` 没有 pressure/resourceguard 引用；`dns/client.go` 的 optimistic refresh 使用 power governor（设备状态），不是内存压力（`common/power` 使用方为 box/dns/provider）。`CONFIRMED`。
- **机制**：内存压力只作用于 admission、TUN 池与 reclaim executor；后台工作没有"让路"策略。
- **影响**：Critical 期间后台 probe 仍会创建连接（受 gate 限流，但不会被暂停），与用户交互流竞争。
- **可行方案**：给后台 probe 一个"压力态暂停/降频"策略（读同一个 `Service.pressure` 或 guard Level），而不是新状态机。
- **代价/风险**：低；需要一个共享的 "background allowed" 判定，避免每个组件各自读 atomic。
- **建议**：`KEEP`，下一轮小改动（与 F8 一起）。

## F4. Crisis 回收会 reset 网络，可能与平台网络切换叠加成双重重连

- **证据**：`adapter/network.go:46-49` 明确 `ReleaseMemory` 是 "resets the network"；crisis 路径 `runCrisisReclaim` 在 armed 线以上调用 `releaseNetworkMemory`（`timer.go:983-985`）；route 的 transition drain 同时会关闭受管连接并打开 dial governor。
- **机制**：一次 Critical 可能同时由内存触发 reset、由平台触发 transition；两套重连来源，但共享 guard 32 与 dial governor 24/3s。
- **影响**：恢复期重连峰值可能高于单一来源；目前只有软限，无统一"重连预算"。`CONFIRMED`（机制），峰值 `UNKNOWN`。
- **可行方案**：把 dial governor 从 route 提升为 network generation 级预算，或让 crisis reset 复用一个 generation 租约。
- **代价/风险**：中；涉及 runtimecoord 与 route 协作。
- **建议**：`DEFER`，先加"reset 来源"计数（哪个来源、几次）再决定。

## F5. 诊断/报告在压力期有成本，但目前是有界的

- **证据**：压力时 `recorder.snapshot(SnapshotReasonPressure, sample, false, false)`（`service_darwin.go:240`）不写 heap dump；heap dump 只在手动 `WriteReport`（`recorder.go:328`）；自动快照限速 + 上限；timeline 在压力期切换为密集采样。
- **机制**：报告自身会分配与写盘，但没有 heap dump 级别的放大；日志在压力路径以 Debug 为主。
- **影响**：有界，但仍是 Critical 期间的额外 I/O。`CONFIRMED`（成本存在、有界），真实分配量 `INFERRED`。
- **可行方案**：若设备证据显示报告成本显著，降低压力期 snapshot 频率或延迟到恢复后。
- **建议**：`DEFER`，设备测量优先。

## F6. XMUX 在一种合法配置下没有物理连接硬上界

- **证据**：`max_connections` 与 `max_concurrency` 互斥；设 `max_concurrency` 且不设 `max_connections` 时，每次池内连接都达到并发上限就新建（`xmux.go:778-792`）。默认 `max_connections=3`（`:811-814`）。
- **机制**：连接数由需求驱动，不是常量上限。
- **影响**：突发下可创建与并发 flow 同量级的 TLS/QUIC 连接（H2 走 gate，H3 不走）。`CONFIRMED`。
- **可行方案**：为 iOS 增加一个"物理连接上界"的显式配置/默认值，或把 H3 创建纳入 gate。
- **代价/风险**：改变 Xray 兼容行为；需要互通证据。
- **建议**：`DEFER`（第一阶段已记录）。

## F7. 观测缺口集中在依赖仓库与 Swift/native 侧

- **证据**：`UDPNat` 无导出计数；DNS cache 无状态 API；buffer pool 无统计；PacketFlow 队列在 Apple 客户端仓库；Cronet 完全在 Go 外（`raw/metrics` 的 UNAVAILABLE 列）。
- **机制**：现有观测被设计在 Go 侧的 guard/route/recorder，上下游资源没有对应的 owner 上报。
- **影响**：下一轮若要做统一预算，会缺"NAT 条目/首包字节/池高水位"三个输入。`CONFIRMED`。
- **可行方案**：`sing-tun` 增加只读 status（条目数、会话字节、drop）；`adapter` 增加 buffer pool 高水位；不改热路径。
- **代价/风险**：依赖 bump + 适配；需证明读取成本可忽略。
- **建议**：`KEEP`（观测，不是策略）。

## F8. "更简单的替代"：singleflight 比更大的 governor 更值得先做

- **证据**：AnyTLS 并发首流仍各自创建（`sing-anytls/client.go:102-156`、`session.go:165-177`）；gate 只限速不合并。实测 1000 并发下 288–306 个创建被准入，其余拒绝——若合并同键创建，创建数可降到个位数。
- **机制**：创建风暴的根因之一是"同键并发创建没有 future"，而门限只是削峰。
- **影响**：同样的内存峰值可以用更少的 session 达到；比扩大 governor 更简单、收益更直接。
- **可行方案**：SDK 内 pending-creation future（等待者共享一次创建），或 Gate 增加 per-key singleflight（不改变 permit 语义）。
- **代价/风险**：跨仓/语义变更；需要失败重试与取消语义。
- **建议**：`KEEP`（下一轮第一优先候选，与 F1 并列）。

## F9. 逻辑流 ≫ 物理连接：五个协议是 N:1

- **证据**：AnyTLS、HY2、TUIC、v2rayquic、Mieru、XHTTP(H1/H2 池)、MASQUE tunnel 都是复用；只有 shared TLS 与 plain SS/Snell/Trojan 是 1:1（`02_PROTOCOL_POOL_MATRIX.csv`）。
- **影响**：以"连接数"估算内存会低估 SDK 内部 stream/state；以"flow 数"估算会高估物理连接。
- **建议**：观测必须同时报告 logical flows 与 physical conns；`CONFIRMED`。

## F10. 一个逻辑流最多触发的物理创建数取决于协议组合

- **证据**：AnyTLS 内部再走 TLS（同一 socket）；detour 链每跳一条；Naive/Cronet 引擎 socket 由引擎决定；MASQUE H3 tunnel + 内部目标连接。
- **建议**：预算按"物理创建"计量需要协议级 cost 表，而不是全局一个数；`INFERRED`。

## F11. 池高水位不自动缩小的候选

- **证据**：`sing/common/buf` 的池按 size class 保留；XMUX pool 关闭时释放连接但 Go heap 由 GC 决定；NAT LRU 容量固定。
- **影响**：关闭/空闲后 RSS 可能滞后回落；这不是泄漏，但会让"恢复后 footprint 回落"看起来慢。`INFERRED`，本机 harness 的 working set 在最终阶段仍约 27 MB（Go runtime 未归还）。
- **建议**：`DEFER`；设备上用 footprint 时间序列判断是否需要强制 scavenge（现有 reclaim 已有 FreeOSMemory 层）。

## F12. 交互类可以被配置滥用，但只影响 reserve 与 rate 豁免

- **证据**：`traffic_class` 来自规则；`ClassInteractive` 使用 4 个创建 reserve + 16 个增长 reserve，并跳过 fast-fail rate window（`guard.go:308-323`）。
- **影响**：把大量流量标为 interactive 会吃掉 reserve，但不会突破绝对上限（limit+reserve）；还会绕过失败退避。
- **建议**：`DEFER`；若下一轮扩大 reserve，需要一条"交互类比例"上限。

## 对施工指令 §9 十二问的直接回答

1. 最容易被低估：**上游 per-flow 资源**（NAT entry + goroutine + 首包）与 SDK 内部 stream state（F1/F9）。
2. 跨边界所有权：PacketFlow/NE（Swift）、Cronet、Apple TLS、Mieru/SDK 内部（F7）。
3. 逻辑流远大于物理连接：AnyTLS/HY2/TUIC/v2rayquic/Mieru/XHTTP/MASQUE（F9）。
4. 一个逻辑流触发多个物理创建：链式代理（F2/F10）。
5. 池容量高水位不自动缩小：buffer pool 与 Go heap 归还滞后（F11，`INFERRED`）。
6. 队列限连接数却保留大量内存：准入队列的等待者在 TUN 侧已持有 NAT 资源（F1）。
7. 复用命中前 Acquire：AnyTLS/HY2/TUIC/v2rayquic 已排除；H3 未接入（不是误计费）。
8. 网络切换 + 内存压力双重重连：存在两套来源（F4），有软限无统一预算。
9. 日志/pprof/metrics 放大：报告有界（F5）；heap dump 仅手动。
10. 更简单的替代：同键创建 singleflight（F8）。
11. 最值得纳入统一预算：NAT entry + 首包字节、SDK session/stream 数、buffer pool 增长字节（F1/F7/F11）。
12. 应明确排除：Direct、WireGuard device、SSH/Tor/OpenVPN/OpenConnect/Tailscale/AWG/USBIP/Cloudflared 这类无共享昂贵创建的路径（`02` 表）。
