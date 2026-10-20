# 六协议统一架构与执行效率全面审计报告（Phase 1）

**审计类型：** 只读架构 / 性能 / 跨协议整合机会审计（**本轮不施工**）
**审计快照：** `sing-box@5064f20a97880d3d5cfe1624c7f79da8d3b03b5c`，分支 `testing`
**快照时间：** 2026-10-20（Asia/Shanghai）
**依赖快照：** `sing@baed8c3b4a34` / `sing-tun@a7bd7facc7cc` / `quic-go@7e08ecab` / `sing-quic@d0deb035` / `cronet-go@8c68ce89` / `sing-anytls@580984e4` / `sing-vmess@9b95ab8c`
**环境：** Windows 10 19044，Ryzen 7 4800H（8C/16T），Go 1.25.5 windows/amd64，`CGO_ENABLED=0`
**证据标签：** `MEASURED_LOCAL` / `VERIFIED_SOURCE` / `VERIFIED_SOURCE_DEP` / `RECORDED_HISTORY` / `NOT_TESTED` / `BLOCKED_BY_ENVIRONMENT`（含义见 §1.4）

> **本报告的唯一持久化产物就是这份文件。** 没有新建分支，没有修改产品代码、测试、配置、依赖、
> CI、子模块或既有知识库内容，也没有改动 `KNOWLEDGE/INDEX.md` / `BASELINE.md` / `README.md`
> （本任务的写权限只覆盖这一个新文件，见 §附录 D）。

---

## 摘要

### 审计范围与已覆盖的路径

七个**不可互换**的数据面对象（不是六个），全部按客户端出站 / 服务端入站 / 上行 / 下行 / 冷 / 热分开：

1. XHTTP（`packet-up` / `stream-up` / `stream-one` / `auto`，H1/H2/H3，XMUX）
2. VLESS + REALITY + Vision（含 `encryption` 变体）
3. AnyTLS（一条 TLS 连接 + 多逻辑流）
4. Naive 的**两套**实现（Go 原生 H1/H2/H3 入站；Cronet 原生出站）
5. MASQUE **L4**：HTTP CONNECT / CONNECT-UDP（H3 优先、H2 回退）
6. MASQUE **CONNECT-IP**：Endpoint / Tunnel / IP 包 / DATAGRAM+Capsule
7. Mieru（官方 SDK + fork 适配层 + 条件性 TCP↔UDP fallback）

**已确认可达**（`VERIFIED_SOURCE` + Registry/构建标签）：1/2/3/4-Inbound/5/6/7 均在默认或
`with_quic` 构建中可达；4-Outbound（Cronet）仅 Apple 平台且 `with_naive_outbound`；
Mieru 的 fallback 默认关闭。没有任何一侧被本文写成"存在"而实际不可达。

### 最重要的八条发现

| # | 发现 | 等级 | 影响 |
|---|---|---|---|
| F1 | **七个 surface 里只有两个是 buffer-native**（AnyTLS 的 `ExtendHeader` 预置帧头、CONNECT-IP 的 owned buffer）。XHTTP、VLESS/Vision、Naive-Go、MASQUE-L4 在 relay 边界都退化为 `[]byte` 语义，`WriteBuffer` 最终变成一次 `buffer.Bytes()` + `Write` | `VERIFIED_SOURCE` | 这是"统一税"的真正大头：不是某个组件慢，而是**协议边界的表达能力**决定了上层无法零拷贝 |
| F2 | **唯一的跨协议 per-write 公共层是上传调度 gate**，它对所有非 syscall 流程安装、默认 inert，成本约 4 个原子操作 + 分支（无锁、无 timer、无分配） | `VERIFIED_SOURCE` | "关闭时接近零"这一条**成立**；但它仍然存在，且一旦配置速率就会 park，需要 per-protocol 的 A/B |
| F3 | **默认 TUN 栈是 fork 自己的 Go 栈**（`NewStack("")` → `NewGo`），不是 gVisor | `VERIFIED_SOURCE` | 所有"TUN 侧"的性能讨论必须以 Go 栈的 64 包读突发、25 ms delayed ACK、direct-to-app-buffer 为前提 |
| F4 | **`ForwardStage.Flush` 在持锁状态下调用一个可能阻塞的 QUIC DATAGRAM 入队**（队满 32 帧时 `Add/AddBatch` 阻塞），capsule 回退更是在流控写上阻塞 | `VERIFIED_SOURCE`（活性未复现） | 这是本轮唯一的 P0 级候选，但**必须先有确定性复现**才允许改 |
| F5 | **MASQUE L4 已有整批锁，但没有 owned 所有权**：`WriteConnectedPacketBatch` 整批拿一次锁，却每包 `SendDatagram` 复制 | `VERIFIED_SOURCE` | 与 CONNECT-IP 的非对称是**同一仓库内已验证机制漏接一条路**，是最便宜的收益 |
| F6 | **Mieru 的双 runtime 边界是正确的**：两个 `transportRuntime` 各自持有 client/config/dialer，选择器无 I/O、无 goroutine、锁不跨 dial，preference 翻转**不迁移、不停止**已有会话 | `VERIFIED_SOURCE` | 这是"控制面统一、数据面独立"的正面样本，**不应合并** |
| F7 | **Fake-IP 不持有任何路由裁决，且 `adapter.InboundContext` 的四个 per-connection 匹配缓存字段是死代码** | `VERIFIED_SOURCE` | "域名→裁决缓存"这条路此前被设计过又放弃；任何重启它的提案必须先解释这 4 个字段 |
| F8 | **XHTTP 的 stream-reset 安全性是三段式不变式**（只 `release` → 延迟关闭 → 退役后仅空闲关闭），单次 reset 甚至不会触发退役（阈值 3） | `VERIFIED_SOURCE` | 这是"逻辑流故障不得升级为共享连接故障"的正例，下一轮应以**差异测试**而非新机制来保护它 |

### 最值得施工的 1–3 项

1. **`portReturn.ReturnPackets` 去分配 + `protocol/bridge` 的逐包调用**（`sing-tun`，L1）——
   实测每包 3 次分配 / 166 ns，改动面只有一个函数。
2. **L4 CONNECT-UDP 接入 owned DATAGRAM**（`transport/http`，L2）——把 CONNECT-IP 已验证的
   `SendDatagramOwned`/`SendDatagramsOwned` 接到 L4；需要把 `FrontHeadroom` 从 6 提到 9。
3. **`domain_keyword` 关键词小写化 +（有条件时）AC 自动机**——前者是 3 行正确性修复（大写关键词
   永不匹配）；后者实测**稠密表在 K≥32 时才有 3× 以上收益、K=8 时反而更慢**，且稠密表 1 KiB/节点
   （K=1 024 → 2.19 MB），**必须用 CSR 布局**并先拿到 K 分布。K≤8 的配置做了就是净倒退。

### 强烈建议不要动的 3–5 项

| 不要动 | 理由 |
|---|---|
| MASQUE 的 owned/batch 契约、`visibleFault` 错误分类、H3 verdict 的 claim+CAS | 每一条都修过一个已测量的历史故障，重写只会把故障带回来 |
| XHTTP 的 XMUX 三段式关闭、packet-up 的聚合窗口 | 前者是正确性不变式；后者的"去掉窗口"已被仓库自己的 A/B 记录为 −13 % |
| Mieru 的双 runtime 与"preference 不迁移会话" | 合并会让一个传输的故障污染另一个；且 SDK 不提供迁移能力 |
| Vision 的 `go:linkname` + reflect 偏移桥 | 它 pin 住了 `sing-vmess` 的内部布局（`expectedSingVMessVersion` tripwire），"整理"它等于放弃 ABI 保护 |
| AnyTLS 的 session 写锁（channel 作为 mutex） | 它序列化了**整条 TLS 连接**的写；换成 per-stream 锁会打乱帧顺序 |

### 大重构还是边界精修？

**边界精修。** 本轮没有找到任何"需要大重构"的证据。相反，找到的四处高价值点全部是
"某个已被验证的机制少接了一条路"或"某个局部容器多分配了一次"。
唯一需要架构级判断的是 F4（锁跨阻塞），而它的正确做法是**先证明红灯**，
而不是先设计新的完成状态机。

### 数据充分性与高风险空白

- **充分**：源码级所有权、调用链、错误分类、构建可达性、单机微基准。
- **空白**（`NOT_TESTED`）：真实 WAN 的 p95/p99、移动端内存/能耗、Cronet 的 native CPU/RSS、
  MASQUE 与真实独立对端的互通、Mieru 与官方 `mieru`/`mita` 的当前版本互通、
  `-race`（本机无 cgo）、以及任何"施工前后"的 A/B（本轮禁止施工，**不存在**本轮自测的收益数据）。
- **收尾补充（真实代码夹具）**：数据面本体的两项关键数字——`ReturnPackets` 的分配与
  `Flush` 的 K 扫描——已用**真实 `ForwardDispatcher`** 而非复刻测得（§2.5.1）；
  `Flush` 持锁跨越阻塞端口调用的**因果关系**也已确定性复现（同上）。仍缺的是这些成本的
  **真实拥塞/真实流量分布**。

---

## 第一篇：架构现状与真实数据路径

### 1.1 审计单位：七个 surface，不是六个

| 核心对象 | 实际承载 | 不能与什么混淆 | 真实可达性 |
|---|---|---|---|
| XHTTP | 一次 dial = 一个逻辑会话；`packet-up` 的每次上行是一个带单调 seq 的 HTTP POST；`stream-up`/`stream-one` 是长请求体；所有请求骑在同一个 XMUX slot 的 H1/H2/H3 连接上 | 一个 HTTP 请求 ≠ 一个物理连接；`packet-up` ≠ 双工流 | `with_xhttp`；H3 需 `with_quic` |
| REALITY Vision | VLESS 会话 + TLS/REALITY 握手 + Vision 专用读写（XTLS direct）| 普通 TCP 包装；Vision 只在 `flow=xtls-rprx-vision` 且底层 TLS 可识别时启用 | 默认 |
| AnyTLS | 一条 TLS 连接 = 一个 session；session 上多逻辑流（`cmdSYN/PSH/FIN/ALERT`）| 子流断开 ≠ 底层连接重建；而"多路复用"实际受 session 池约束 | 默认 |
| Naive（Go inbound） | `net/http` H1 直通 CONNECT / h2c / H3；H2/H3 上按协商启用 padding 帧 | Go inbound ≠ Cronet outbound；窗口上限 ≠ 已分配内存 | 默认（inbound） |
| Naive（Cronet outbound） | Chromium 网络栈 + `cronet-go` bridge；N 个 Engine 或 1 Engine + N isolation key | 配置的 concurrency ≠ 实际 Engine 数 | `with_naive_outbound`，Apple 平台 |
| MASQUE L4 | HTTP CONNECT（TCP 隧道）与 CONNECT-UDP（`/.well-known/masque/udp/<host>/<port>/`）| CONNECT 200 之后隧道仍活 ≠ 普通请求结束；L4 不是 IP 层 | 经 `http` outbound 的 `ListenPacket`/`DialContext` |
| MASQUE CONNECT-IP | Endpoint/TUN + IP 包 + Session + DATAGRAM/Capsule + 控制 capsule | 内层 IP MTU ≠ 外层 QUIC 包；一个 IP 包 ≠ 一个 HTTP Stream | `protocol/masque` 出站；服务端同理 |
| Mieru | fork adapter → 官方 SDK Client/Server → Mux → TCP/UDP underlay | 逻辑会话 ≠ underlay；fallback OFF/ON 是**两棵对象树** | 默认（fallback 默认关） |

**为什么必须拆开 MASQUE**：L4 的数据单元是 `(destination, payload)`，CONNECT-IP 的数据单元是
**完整 IP 包**，后者的热路径包含 IP 头解析、路由匹配、TTL 递减、ICMP PTB 构造，前者完全没有。
两者共享 `transport/http` 的连接与错误分类，但**没有**共享数据面代码。把它们合成一条"MASQUE 热路径"
会立刻丢掉 `SendDatagramsOwned` 的所有权契约（见 §1.5）。

### 1.2 客户端：一次连接的建立顺序（六协议横向）

```
应用 → inbound → sniff → {PreMatch} → route.matchRule → outbound 根
  → group 选择（selector/urltest/loadbalance，不产生物理 hop）
  → DialContext → dialer（DNS / bind_interface / routing_mark）
      → 协议握手：
          VLESS:  [TLS/REALITY 握手] → VLESS 请求头 → (Vision) 直通
          XHTTP:  [TLS] → 选 slot → 建 H2/H3 → 各 mode 的首请求
          AnyTLS: [TLS] → 密码认证帧 → cmdSYN
          Naive:  [Cronet native] → CONNECT + Padding 头
          MASQUE: [TLS] → H3(优先) / H2 → CONNECT(-UDP) 或 CONNECT-IP + capsules
          Mieru:  [SDK underlay] → SOCKS5 请求 → 响应
  → 数据面
```

**建链期最容易重复的三件事**（本轮逐一核对）：

| 重复 | 是否真的发生 | 证据 |
|---|---|---|
| DNS 解析两次 | 只在 `masque` 的 bootstrap vs inner resolver 之间**故意**分离，不是重复 | `protocol/masque/client.go` bootstrap 先于 tunnel；inner 走 device |
| 两条规则扫描 | **是**：`PreMatch` + `matchRule` 两次整表 | `route/route.go:490-617` 与 `:1258-1322` |
| 两次拨号 | 否；但 Mieru fallback ON 时一次用户 Dial 会先选一个 runtime，失败后才用另一个 | `protocol/mieru/fallback.go:485-504` |

### 1.3 上行：每协议的每写成本

| 协议 | 一次 `Write` 的真实动作 | 每写分配 | 频率 |
|---|---|---|---|
| XHTTP packet-up | mutex + `append` 到 `pending` + `Signal`（`conn.go:1033-1060`） | 0（容量足够时） | 每 relay 写 |
| XHTTP 流模式 | 写入 `io.Pipe` → 另一 goroutine 读出写 HTTP body（`conn.go:606-608,743`） | 1 次 goroutine 切换 | 每写 |
| VLESS/Vision | Vision 内部按 `xrayChunkSize = 8192` 重切；`encryption` 变体每 ≤8192 B 一次 AEAD | 依实现 | 每 8 KiB |
| AnyTLS | `putFrameHeader(buffer.ExtendHeader(12))` 零拷贝预置 → `conn.Write` | 0（缓冲路径） | 每帧 |
| Naive Go inbound | padding 帧：3 B 头 + payload + ≤255 B padding；`writeFull` 保证整帧 | 窗口内 1 次 | 每写 |
| MASQUE L4 | `PrependContextID` → `SendDatagram(bytes)`（**复制**）或 capsule | 1 次 payload 拷贝 | 每包 |
| CONNECT-IP | `PrependContextID` → `SendDatagramOwned`（**不复制**） | 0 | 每包 |
| Mieru UoT | 一个 pooled buffer + 一次 payload 拷贝 | 1 次 | 每数据报 |

**结论（F1 的由来）**：能否零拷贝不取决于"统一层做了多少"，而取决于
**协议边界是否暴露 buffer 能力**。XHTTP 的 conn 只有 `Read/Write/CloseWrite`，
VLESS 的 `*CommonConn` 连 `WriteBuffer` 都没有，所以上层 `bufio.ExtendedConnWrapper`
只能退化成 `buffer.Bytes()` + `Write`（`sing@baed8c3b:common/bufio/conn.go:158-161`）。
这**不是缺陷**（协议封装确实需要连续字节），但它是"统一收益上限"的物理原因。

### 1.4 下行与返回路径

| 协议 | 下行交付 | 回到 TUN/应用的方式 |
|---|---|---|
| XHTTP | 一个 GET 的响应体（packet-up 亦然） | relay 读 → `WritePacketBuffers`/`Write` |
| Vision | TLS 记录解密后按 Vision 读路径 | 同上 |
| AnyTLS | session readLoop 解帧 → `stream.push`（**一缓冲深，会阻塞整个 readLoop**） | 同上 |
| Naive Go inbound | H2 stream / H3 stream 读 | relay |
| MASQUE L4 | `packets` channel（容量 64）→ `ReadPacket` | `returnPath.ReturnPackets` 或 relay |
| CONNECT-IP | quic-go DATAGRAM → 校验 → session 路由 → `WriteInboundBuffers` | `transport/device` → `ReturnPackets(批)` |
| Mieru | SDK → `readFrame`（per-association mutex） | relay |

**新发现（本轮）**：返回路径的统一入口 `portReturn.ReturnPackets` **每次调用都分配**
（`[]returnBatch` + 每批 packet 切片），而 `protocol/bridge` 是**逐包**调用它。
实测 1 包 = 166 ns / 3 allocs。详见 §3.2 的 A 类条目。这属于"统一层本身的成本"，
而不是任何协议的问题——正好是本次审计要回答的问题类型。

### 1.5 MASQUE 五项专审

#### M1. H3 verdict 与回退的因果链

- 状态：`transport/http.Client` 上的 `http3Broken`（绝对截止时间）+ `http3Backoff`（退避档位），
  5 s → 5 min；`common/httpclient` 里另有一套 **按 authority** 的 5 min→48 h 记忆（两者不同源）。
- 写入点（`markHTTP3Broken`）：tunnel/CONNECT-UDP 建立、CONNECT（TCP）拨号、通用请求失败。
- **关键顺序**：先 `claimHTTP3Outcome`（对"最新一次尝试的起始"做 CAS），再分类，
  再落 verdict。这样"后到先得"不会覆盖一个更新尝试的结论。
- **慢 200 vs 失败**：`errHTTP3ResponsePending` 是一个**故意不等于** `ErrHTTP3Unavailable`
  的哨兵；分类 switch 把它映射到 `noteHTTP3AmbiguousOutcome`（占位但不写 verdict）。
  这一条正是 2026-10 那次"200 慢于建链窗口却 markHTTP3Broken"修复的落点。
- **strict 模式**在分类**之前**判断，且不 arm 任何状态。
- **typed-before-generic**：`classifyH3Error` 的顺序是
  TransportError → `http3.Error` → ApplicationError → StreamError → StatelessReset →
  VersionNegotiation → idle/handshake timeout → ctx → **最后**才是 `io.EOF`/`net.ErrClosed`。
  反过来会把 `PROTOCOL_VIOLATION` 洗成"正常关闭"。
- **stream-local vs connection-wide**：`StreamError`/`H3_REQUEST_CANCELLED` 只影响该流，
  **不会**关闭 ClientConn；TransportError(code 0)/AppError(code 0)/idle timeout 视为连接级但"预期"；
  协议违规/StatelessReset/VersionNegotiation 才是"可见故障"。

**判断：KEEP。** 这套三段式（attempt 排序、哨兵、typed 分类）是本仓库最成熟的控制面逻辑之一。

#### M2. CONNECT-IP TX 的所有权与批处理

- headroom 预算：`PacketHeadroom = max(1 + 8, 1 + 5) = 9`
  （Context ID 1 + Quarter Stream ID 8；capsule 头 6）。生产者（TUN）按 9 预留。
- 单包：`SendDatagramOwned`；批：`SendDatagramsOwned`，**≤32/块**（= quic-go 的
  `maxDatagramSendQueueLen`），**全有全无**，拒绝的块原样回退到逐包路径。
- 失败归属表（`session.go:436-446` 原文）：owned+成功 → 传输层所有；owned+错误 → 仍是调用者
  （且前缀必须 `Advance` 回滚）；非 owned+成功 → 本函数释放；非 owned+错误 → 从未发出。
- **批内混排顺序**：`writeBatchOwned` 返回"已转移前缀长度"，调用者从 `buffers[consumed:]` 继续，
  **绝不重放**。这一条修过一个"拒绝的批被重放 → 内层 IP 头错位 1 字节 → 全部静默丢弃"的真实 Bug。

**判断：KEEP。** 但注意仓库自己的 mock benchmark 显示 batch 在 mock 下并不更快（§2.3），
所以"batch 有收益"这句话只能在真实 quic-go 队列上成立。

#### M3. IP 数据与安全可达性

- `contextID != 0` 一律丢弃；`len(datagram) == contextLength` 丢弃；capsule 超过 1 MiB 是**致命**错误。
- PTB 归因：客户端回到"失败包自己的源"；服务端用 `buildICMPErrorTo` 明确发给**该 session 的对端**，
  不跨会话广播。
- 路由匹配：`configuration.RoutesAdvertised` + 预编译 matcher（二分），无每包分配。

#### M4. DNS / Bootstrap / DoH

四个平面必须分开看：
1. **Server Bootstrap**：在任何 tunnel 之前解析 MASQUE 服务器；有 winner 缓存（8 条）+ 竞速；
2. **DNS_ASSIGN**：不可变快照，空赋值 = 撤回（草案语义），发布后在 handler 外调用；
3. **名称所有权 / origin 能力**：编译期算一次（`dohpath` + 显式 `alpn=h3` + tunnel 是 H3 + 完整 origin 含端口）；
4. **实际 DNS 执行**：same-connection DoH 只在上述三事实同时成立时可用，失败可回落同 resolver 的普通 DNS。

`PREF64` 是**独立状态**（空 = 撤回），**不是** DNS64/NAT64 已实现。

#### M5. 网络、移动与长连接资源

- 网络切换：没有 `runtimecoord` 注册；走 `NetworkManager` → `InterfaceUpdateListener` →
  `ClientEndpoint.InterfaceUpdated` → `RestartSession`（cancel session + `ResetConnections`）。
- idle：MASQUE 只实现 `SetKeepIdleConnections`，**不**实现 `CloseIdleConnections`，
  这是**故意**的（on-demand tunnel 不该被 TrimMemory 直接关掉）。
- 会话隔离：服务端每 session 独立 stream + 独立发送队列 + 独立地址所有权；
  **但同一 H3 连接上的所有 session 共享一个 32 帧 DATAGRAM 发送队列**（见 §3.4 的 R 类条目）。

### 1.6 Mieru 五项专审

#### R1. SDK 边界

官方 `github.com/enfein/mieru/v3` 拥有协议、密码学、Mux/underlay。fork 只拥有：
配置、DNS 桥、Dialer/Listener 工厂、生命周期、UoT 适配、可选 fallback。
**任何把 SDK Mux 改造成 sing-box 复用器、或 fork SDK 的提议都应直接否决**（本轮已按此处理）。

#### R2. 两套独立瓶颈

- TCP stream：瓶颈可能在 SDK underlay / 共享 Copy Engine / GC / ChaCha20，
  **不在** adapter（历史 CPU/GiB 0.681 与低 Mux 上传 0.939 都指向 underlay）；
- UDP UoT：每数据报 1 个 pooled buffer + 1 次拷贝（写入侧），读侧 0 拷贝；
  per-association mutex 是**保护真实 race 的必要成本**（历史 ~9 ns/datagram），**不得去掉**。

#### R3. Fallback 双实例

- OFF：单 `transportRuntime`，与旧路径完全一致；
- ON：两个 generation，各自 client/config/dialer/socket registry；selector 只写状态；
- **preference 翻转不迁移、不停止**已有会话（`outbound.go:51-55` 原文）；
- 迟到的旧 generation 结果直接关闭（`acceptGeneratedConn`）。

#### R4. 错误真值表（要点）

| 来源 | 事实 | 外层 group 的行为 |
|---|---|---|
| target SOCKS refusal | Target/Target | 不重试、不惩罚 |
| 连接被拒 | Dial/Path | 可重试、可惩罚 |
| 握手失败 | Handshake/Path | 可重试、可惩罚（除非仍像 timeout） |
| caller cancel | Dial/Caller | 中性 |
| 本地 DNS / 权限 | Dial/Local | 中性 |
| **socket timeout** | **无事实（Unknown）** | 既不能重试也不能惩罚 |
| **SDK 10 s 阻塞** | 无事实 | 同上；wall time 由 SDK 决定 |

**这是本轮最值得讨论的"策略可见性"问题**：节点内部已经切了 secondary，外层却看到 Unknown。
它不是 Bug（Unknown 的保守语义是有意的），但它让"节点内 fallback"与"组级 failover"之间的信息不对称。

#### R5. 资源规模

未 Dial 的节点不创建 underlay（懒启动）；fallback ON 时**每个节点**两套 SDK generation 的
对象树（client/config/dialer），但 socket 仍按需创建。`configured ≠ active` 成立。

### 1.7 公共组件接入矩阵

**图例**：`E` 高效整合 / `Q` 已接入但成本可疑 / `D` 重复实现有整合价值 /
`N` 不适用或刻意独立 / `M` 未接入且可能有缺口 / `U` 证据不足

| 组件 \ Surface | XHTTP | Vision | AnyTLS | Naive-Go | Naive-Cronet | MASQUE-L4 | CONNECT-IP | Mieru(TCP/UDP) |
|---|---|---|---|---|---|---|---|---|
| `adapter.Scope` 生命周期 | E | E | E | E | E | E | E | E |
| 网络代际 / `InterfaceUpdated` | Q¹ | N² | Q¹ | N | U³ | E | E | E |
| Buffer 能力（`WriteBuffer`/`Upstream`） | M⁴ | M⁵ | **E**⁶ | **E**⁷ | U | M⁸ | **E**⁹ | Q¹⁰ |
| Owned datagram / batch | N | N | N | N | N | M¹¹ | **E** | N¹² |
| 连接 / 会话池 | E（XMUX） | N | E（session 池） | N（net/http） | E（Engine/隔离键） | E（H3+H2） | E | E（SDK） |
| DNS / Fake-IP / sniff / detour | E | E | E | E | U¹³ | E | E（另有 4 平面） | E（DNS 桥） |
| 错误分类 / `FailureFacts` | Q¹⁴ | E | Q¹⁴ | Q | U | **E**（typed H3） | **E** | **E** |
| 上传调度 gate | Q¹⁵ | Q¹⁵ | Q¹⁵ | N¹⁶ | N | Q¹⁵ | Q¹⁵ | Q¹⁵ |
| 流量计数 | E¹⁷ | E¹⁷ | E¹⁷ | E¹⁷ | U | E¹⁷ | E¹⁷ | E¹⁷ |
| 平台能力 / power | E | E | E | E | U¹⁸ | E | E | E |
| 构建标签 / native ABI | `with_xhttp` | `with_utls`¹⁹ | — | — | `with_naive_outbound`+Apple | `with_quic` | `with_quic` | — |

脚注：
1. 经 outbound 的 `InterfaceUpdated` 间接生效，但 XHTTP/AnyTLS 自身不持有代际；
2. Vision 在已建立连接内无代际概念；
3. Cronet 的网络切换由 Chromium 内部处理，Go 侧不可观测；
4. `packetConn`/`streamConn` 只有 `Read/Write/CloseWrite`；上层 `ExtendedConnWrapper` 退化为拷贝；
5. `*CommonConn`（encryption）无 buffer 方法；Vision 自身按 8 KiB 重切；
6. `putFrameHeader(buffer.ExtendHeader(...))` 零拷贝预置；
7. `naiveH2Conn.WriteBuffer` 在原缓冲内就地加帧，headroom 3 / rear 255；
8. `http3PacketConn` 只有 `WritePacket(*buf.Buffer, addr)`；内部 `SendDatagram(bytes)` 复制；
9. `PacketHeadroom = 9`，`SendDatagramOwned`；
10. UoT 写入 1 个 pooled buffer + 1 次拷贝，读 0 拷贝；
11. L4 有"整批一次锁"但**没有** owned；见 §3.2 A2；
12. HY2/TUIC 的 `writePackets` 是分片辅助函数，正常路径长度为 1（**假前提**，见 §3.5 D2）；
13. Cronet 出站的 DNS 在 Chromium 内部，fork 的 resolver 是否参与**本轮未确认**（按 `U` 记录），因此不写成"已接入"；
14. XHTTP/AnyTLS 的失败最终走通用错误路径，不产生 `FailureFacts`（**这可能是缺口，也可能是刻意**：只有 Mieru 需要"节点内换传输"，其他协议没有第二传输可选）；
15. gate 安装条件 = 两端不同时具备 syscall 能力；对非 syscall conn 一律安装，默认 inert；
16. Naive 是**入站**，上传方向是客户端→服务端，不经过 fork 的上行 gate；
17. 计数在 inbound 读侧，gate 会保留已有计数器（`route/conn.go:446-465`）；
18. Cronet 的 CPU/RSS 在 Go pprof 之外；
19. REALITY 依赖 `metacubex/utls` 的 fork。

**这张矩阵的两个非平凡结论**：
- "buffer 能力"这一行是**协议属性**，不是接入质量。把 XHTTP 改成 buffer-native 需要重写它的 conn 语义，
  收益上限是每次 relay 少一次 1 400 B memcpy（约 30–50 ns），**不值得**。
- "DRY 的机会"只有两处：L4 的 owned（M11）与错误事实（Q14）。前者是真机会，后者需要先证明
  "XHTTP/AnyTLS 有第二种传输可换"——目前没有。

### 1.8 资源与状态所有权树（客户端）

```
Box
├─ adapter.Scope                        （关停顺序的唯一权威）
├─ route.Router
│   ├─ 规则表（不可变，热更新时整体替换）
│   ├─ FakeIP store（地址↔域名；**不含裁决**）
│   └─ process LRU（256）
├─ outbound 根
│   ├─ XHTTP client：XMUX manager（连接池）+ 每 dial 一个 slot + packetUplink（队列+flusher）
│   ├─ VLESS/Vision：TLS/REALITY conn + VLESS 帧 + Vision 直通
│   ├─ AnyTLS：session（TLS conn + 写信号量 + 流表）+ 会话池
│   ├─ Naive：cronet client（native engine / 隔离键）
│   ├─ MASQUE：httpClient（H3 conn + H2 pool）+ session + device（TUN）
│   └─ Mieru：1 或 2 × transportRuntime（client/config/dialer/UDP registry）+ selector
└─ inbound（TUN）
    ├─ Go stack（默认）：每 engine 一个 ForwardStage + slab pool + 流表
    └─ 每个 stage：ports[]（**只增不减**）+ table + writebackBatch + segment 缓冲
```

**唯一权威没有被复制**：Scope 管生命周期、Router 管裁决、`NetworkManager` 管代际、
`ForwardStage` 管 TUN 侧所有权。本轮**没有**发现第二个决策中心，也**不支持**新增。

### 1.9 数据面包装与调用频率成本账本

| 协议/方向 | 步骤与锚点 | 频率 | 成本 | 必要？ | 证据 |
|---|---|---|---|---|---|
| 全部上行 | 上传 gate（`route/conn.go:436-465` → `common/trafficsched/gate.go:74-86`） | 每写 | ~4 原子 + 分支，0 分配；配速率时 mutex+park | 是（整形需要） | `VERIFIED_SOURCE` |
| 全部 | 统计计数 | 每读 | 原子加 | 是 | `VERIFIED_SOURCE` |
| TUN→协议 | `ForwardStage` 线性找 port | 每包 | 6.4 ns（K=1）→ 85 ns（K=256） | 可优化 | `MEASURED_LOCAL` |
| 协议→TUN | `portReturn.ReturnPackets` 分组 | 每批/每包 | 166 ns + 3 allocs（1 包） | **可去掉分配** | `MEASURED_LOCAL` |
| CONNECT-IP TX | `PrependContextID` + owned | 每包 | 0 拷贝 | 是 | `VERIFIED_SOURCE` |
| MASQUE L4 TX | `PrependContextID` + `SendDatagram` | 每包 | 1 次拷贝 | **可改为 owned** | `VERIFIED_SOURCE` |
| XHTTP POST | `Header.Clone` + `url.URL` + padding 字符串 + timer + ctx | 每 POST | 数百分 ns + 100–1 000 B padding | 部分是协议要求（padding） | `VERIFIED_SOURCE` |
| Naive 帧 | 3 B 头 + ≤255 B padding，就地写 | 每写（窗口内） | ~670 ns/1 400 B（含 codec） | 是 | `MEASURED_LOCAL` |
| AnyTLS 帧 | 帧头预置 + 一次 `conn.Write` | 每帧 | 0 拷贝 + 一次信号量 | 是 | `VERIFIED_SOURCE` |
| Mieru UoT | 1 pooled buffer + 1 拷贝 | 每数据报 | 118.8 ns/64 B/Win（历史） | 是 | `RECORDED_HISTORY` |

---

## 第二篇：性能基线与测量可信度

### 2.1 本轮的测量边界（必须先读）

**本轮禁止施工，因此不存在"施工前后"的 A/B。** 下面所有数字只有三类来源：

1. **仓库既有 benchmark 的复跑**（`MEASURED_LOCAL`，但夹具是 mock/进程内）；
2. **仓库外独立 harness 对本轮候选所含原语的微基准**（`MEASURED_LOCAL`，单机、单函数）；
3. **仓库注释/历史证据里的记录值**（`RECORDED_HISTORY`，**未复测**）。

**没有任何端到端吞吐/延迟数据**。真实 WAN、移动端、Cronet native 成本、MASQUE 与独立对端互通
全部是 `NOT_TESTED`。任何把下面数字外推成"整机提速 X %"的说法都是错的。

### 2.2 环境

| 项 | 值 |
|---|---|
| OS / Arch | Windows 10 Pro 19044 / amd64 |
| CPU | AMD Ryzen 7 4800H，8C/16T，`GOMAXPROCS=16` |
| RAM | 15.4 GiB |
| Go | go1.25.5 windows/amd64 |
| CGO | `CGO_ENABLED=0` |
| 构建标签 | 无 tag（默认）；`with_quic`/`with_low_memory` 未用于本轮基准 |
| 命令约束 | 仓库内一律 `GOFLAGS=-mod=readonly`；临时 harness 在 `C:\tmp\r3probe`（独立 module，`replace` 指向锁定版本） |
| 未测 | `-race`（无 cgo）、iOS/Android/macOS、真实 WAN |

### 2.3 复跑：MASQUE 数据面（仓库既有 benchmark）

```
GOFLAGS=-mod=readonly go test -run '^$' -bench 'BenchmarkDataplaneOutboundBatchSize$' \
  -benchmem -benchtime 300x -count 3 ./transport/masque/
```

| 批量 | per-packet（3 轮 ns/包） | batched（3 轮 ns/包） | allocs/op（per-packet → batched） |
|---|---|---|---|
| 4 | 124.6 / 128.4 | 130.2 / 139.5 / 142.3 | 5 → **6** |
| 8 | 105.8 / 121.8 / 126.9 | 108.3 / 109.4 / 120.4 | 9 → **10** |
| 16 | 117.2 / 117.3 / 126.6 | 101.4 / 115.5 / 128.3 | 17 → **18** |
| 32 | 99.96 / 101.3 / 107.0 | 97.14 / 101.3 / 132.2 | 33 → **34** |

**限定性解释**：sink 是进程内 mock，**不含** quic-go 发送队列锁、唤醒、TLS、内核、RTT。
batch 路径省掉的正是 mock 不模拟的那部分，所以这张表**不能**用来证明或否定 batch 的端到端收益；
它只能证明两件事：(a) mock 下没有稳定收益，(b) batch 每批多 1 次 `prepared` 切片分配
（`transport/masque/session.go:604`）。**这是本轮对"batch 一定更快"这一直觉的第一次证伪。**

### 2.4 复跑：Naive padding codec（仓库既有 benchmark）

```
GOFLAGS=-mod=readonly go test -run '^$' -bench 'BenchmarkPaddingWrite|BenchmarkPaddingRead|BenchmarkHeadroom' \
  -benchmem -benchtime 2000x -count 2 ./protocol/naive/
```

| 基准 | 结果 |
|---|---|
| `PaddingWriteFramed`（1 400 B） | 669.6 / 674.8 ns/op，2 090 MB/s，65–67 B/op，1 alloc/op |
| `PaddingWriteRaw`（窗口之后） | 511.6 / 144.5 ns/op（噪声大），1 alloc/op |
| `PaddingReadFramed`（4 096 × 1 400 B） | 540.5 / 517.2 µs/op，10 608 MB/s，1 408 B/op，1 alloc/op |
| `HeadroomDecisions` | 0.30 ns/op，0 B/op，0 allocs/op |

**限定**：`io.Discard` / `bytes.Reader` 夹具，无 socket、无 TLS、无对端、无流控。
它证明的是"padding 帧本身的 CPU 成本 ≈ 每 1 400 B 670 ns（≈ 2 GB/s 单核）"，
**不是** Naive 的代理吞吐。

### 2.5 仓库外：本轮候选原语的微基准

| 基准 | 结果 | 用途 |
|---|---|---|
| `stagePort` 线性扫描（K=1/8/64/256） | 6.35 / 15.4 / 31.5 / 85.0 ns | 判断"线性 vs map"的阈值 ≈ K≥32 |
| `map[*portNAT]int` 查找（K=1/8/64/256） | 7.9 / 16.6 / 13.6 / 13.9 ns | 同上 |
| `ReturnPackets` 分组（1/4/64 包） | 166 / 165 / 1 097 ns，3/3/7 allocs | A1 的依据 |
| `time.Sleep(50 µs)` | **518.6 µs** 均值，p99 589 µs | XHTTP gather 的真实行为 |
| TCP 段头编码（1 400 B 全量 vs 字节模板） | 62.4 vs 31.9 ns | TCP PacketTemplate 的收益上界 |
| 1 400 B payload 校验和 | 19.8 ns（≈ 70 GB/s） | 说明模板消不掉的部分有多大 |
| `domain_keyword` 线性扫描（K=1/32/1 024） | 9.9 / 320 / 11 300 ns | AC 的收益门槛 |
| 稠密表 Aho–Corasick（K=8/32/128/1 024） | **87 / 94 / 95 / 95 ns**（与 K 无关） | 交叉点 K≈16–32；K=1 024 时 117× |
| `map` 边 Aho–Corasick（K=8/32/128/1 024） | 826 / 968 / 952 / 968 ns | **布局决定一切**：小 K 时慢于线性 |
| AC 构建（K=128 / K=1 024） | 0.53–0.59 ms / 3.5–3.8 ms；稠密表 356 KB / **2.19 MB** 常驻 | 移动端必须用 CSR，不能照搬稠密表 |

**真代码夹具（收尾补充，§2.5.1）**：上面最后四行以及 R3-3 的复现，都来自
`C:\tmp\r3real`——一个直接复制本仓库完整 `require`/`replace` 图的外部模块，
实例化**真实的** `tun.NewForwardDispatcher` / `ForwardStage`，用假 `Port`/`PacketBufferPort`
驱动，用 `gtcpip/header` 构造合法 IPv4/UDP 包。主仓库零改动。

#### 2.5.1 真代码夹具的结果（不是复刻）

| 项 | 结果 |
|---|---|
| 真 `ReturnPackets`（1 包，`protocol/bridge` 的形状） | 236.3 / 239.8 / 249.5 ns，**72 B / 2 allocs** |
| 真 `ReturnPackets`（64 包） | 11 042 / 11 071 / 11 152 ns，3 480 B / 8 allocs |
| 真 `Flush`（64 包/次，0 allocs）K=1 / 8 / 64 / 256 | 171.8–174.2 / 321.5–331.3 / 1 625–1 661 / 1 636–1 648 ns |
| 折算每包 | **2.7 / 5.1 / 25.7 / 25.7 ns** |
| R3-3 活性复现 | 端口阻塞期间 `Close()` 300 ms 不返回；释放后 **301.24 ms** 完成 |

K=256 与 K=64 同值这一条是本轮对"K 会随节点数爆炸"的直接否证：
**一次 64 包的突发最多触及 64 个端口**，所以线性扫描的上界是 `min(已选端口数, 每批包数)`。

### 2.6 必须并列的历史记录（未复测）

| 记录 | 值 | 出处 |
|---|---|---|
| XHTTP packet-up 上传（有/无聚合窗口） | 26.1–27.8 vs 23.3 MB/s | `transport/v2rayxhttp/conn.go:977-983` 注释 |
| XHTTP 连接建立（修复前/后 p99） | 31.7 ms → 1.6 ms | `conn.go:1257-1259` 注释 |
| MASQUE DATAGRAM owned vs 复制 | ~2.7×（1 280 B 夹具） | round2 报告 |
| Mieru UoT 写入 | 118.8 ns / 64 B / 1 alloc（SDK 666.1 ns） | `verify-evidence/mieru/2026-10-18-84a38ca85f7/decisions.md` |
| Mieru 低 Mux TCP 上传 | 0.939×（官方 435.0 / fork 408.25 MiB/s） | 同上 `comparison.csv` |
| Mieru UDP-underlay TCP 上传 | 0.739×（vs 真实 mita 3.38.1） | `phase3-debug/mita/comparison.csv` |
| Naive Cronet 首包 | 64 B 470→214 ns；1 400 B 482→216 ns；1 alloc→0 | `docs/naive.md:285-287` |

**这些数字全部 pin 在旧树上**（Mieru 证据 pin 在 `84a38ca85f7`，Cronet 数字是更早的本地基准）。
本轮**没有**复测，也没有把它们当作当前性能。

### 2.7 覆盖矩阵（哪些格是空的）

| 维度 | XHTTP | Vision | AnyTLS | Naive-Go | Naive-Cronet | MASQUE-L4 | CONNECT-IP | Mieru |
|---|---|---|---|---|---|---|---|---|
| 单机 codec 微基准 | ✗ | ✗ | ✗ | ✓ | ✗ | ✓（mock） | ✓（mock） | ✓（历史） |
| 进程内 E2E | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | 部分（历史） |
| 真实 WAN 吞吐 | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | 部分（历史） |
| 真机内存/能耗 | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| 协议互通 | 历史 CI 证据 | 历史 CI 证据 | 未测 | 未测 | 未测 | 未测 | 未测 | 历史（mita 3.38.1） |

**这张表本身就是本轮结论之一**：六协议的性能证据极不对称，任何"某协议更快"的横向排名
都是证据不足的。

---

## 第三篇：问题清单、原创发现、取舍与下一阶段候选

> 每条按 §10 模板给出：类别 / 涉及 / 现状 / 证据 / 频率 / 开销模型 / 统一机会 / 方案 A（最小）/
> 方案 B（保持现状）/ 新引入风险 / 不变量 / 预期收益 / 证伪与验收 / 级别 / 建议。

### 3.1 Q1（P0，A 类，活性）：`ForwardStage.Flush` 持锁调用可能阻塞的 QUIC 入队

- **类别**：正确性/活性风险（不是已确认 Bug）。
- **涉及**：全部走 TUN→`tun.Port` 的路径；对本报告而言是 **CONNECT-IP**（`protocol/masque` 实现
  `tun.Port`，被 `JudgeFlow` 选为 Port 时）。
- **现状**：`sing-tun@a7bd7fac:flow_dispatch.go:772-786` 在 `s.access` 下调用
  `bufferPort.WritePacketBuffers(...)`；`protocol/masque/client.go:675-680` →
  `transport/masque/client.go:467-516` → `session.writePackets` → `SendDatagramOwned`；
  `quic-go@7e08ecab:datagram_queue.go:61-89` 在 32 帧满时**阻塞**。
  另一条更容易触发的是 capsule 回退：`session.go:540-542` 在 `writeAccess` 下做**流控写**。
- **注意（本轮修正了一个容易搞错的点）**：CONNECT-IP endpoint **自己的内层 device** 用的是
  `transport/device` + `SetPacketWriter`（`protocol/masque/client.go:323-330`），那条路**没有**
  `ForwardStage` 锁。危险只出现在"外层 TUN 把它当作 `tun.Port` 转发"的那条路。
- **频率**：每个 TUN 转发突发一次（Go 栈 `goReadBatch = 64`；gVisor 模式下**每包**一次 Flush）。
- **开销模型**：正常情况下是几十 ns 的入队；拥塞时是"一个 RTT 到若干秒"的持锁等待。
- **统一机会**：没有。这不是统一问题，是所有权与阻塞边界的问题。
- **方案 A（最小）**：**不改**。先按 §附录 A 的复现计划拿到红灯。
- **方案 B（保持现状）**：如果复现失败，记录为"已知的锁跨 I/O"，并在注释里写明前提。
- **新引入风险**：任何"锁外写入"的改法都要新增 in-flight 状态，最危险的是 exactly-once 与有序写入。
- **不变量**：staged buffer 的"移交后绝不回看"、Flush 与 Close 的串行、包顺序。
- **预期收益**：只有消除"关机/切网被拥塞拖住"这一类体验问题，**没有吞吐收益**。
- **证伪与验收**：假 `PacketBufferPort` 阻塞 + 并发 `Close`，断言 `Close` 有界返回。
- **本轮已完成复现（收尾补充）**：`C:\tmp\r3real` 里用真实 `ForwardDispatcher` + 阻塞的
  `PacketBufferPort` 跑出——**`Close()` 在 300 ms 后仍未返回；释放端口后 301.24 ms 完成**。
  即"锁被持到下游返回"已从推理变成实测（详见 round3 报告 §12.8 第 1 项）。
- **仍未测**：真实 QUIC 拥塞下这个阻塞的**时长分布**（需要在真实连接上把 32 帧队列打满）。
- **级别**：P0（风险等级）/ 证据 `VERIFIED_SOURCE` + **活性 `MEASURED_LOCAL`（耦合已证实）**。
  **建议：INVESTIGATE（下一步是拥塞时长分布，不是改锁）。**

### 3.2 Q2（P2，A 类）：返回路径的每次调用分配

（详细测量与设计见 `KNOWLEDGE/reports/anywhere-round3/` §6.1；此处只给跨协议结论。）

- **类别**：已证性能瓶颈（小）。
- **涉及**：**全部六个协议**共享的返回入口 `portReturn.ReturnPackets`。
- **频率**：每批（`transport/device`）或每包（`protocol/bridge`）。
- **开销模型**：1 包 166 ns / 3 allocs；64 包 1 097 ns / 7 allocs / 3 432 B。
- **统一机会**：不是"统一更多"，而是"统一层自己少分配一次"。
- **方案 A**：栈上内联 `[2]returnBatch` + 每批内联小缓冲，超出退化。
- **预期收益**：GC 压力下降；**不是**吞吐提升。
- **证伪**：若 `allocs/op` 仍 >0，说明内联数组逃逸，方案失败。
- **真代码实测（收尾补充）**：1 包 = **72 B / 2 allocs**（236 ns），64 包 = 3 480 B / 8 allocs
  （11.0 µs）。两次分配就是 `[]returnBatch` 与那个 batch 的 packet 切片，**全部来自分组本身**；
  时间上 1 包的 236 ns 主要花在 parse/rewrite，所以这条候选的收益是**分配次数**而非吞吐。
- **级别**：P2。**建议：CHANGE_LATER（可与下一次 sing-tun pin 合并）。**

### 3.3 Q3（P2，A 类）：MASQUE L4 缺 owned DATAGRAM

（详见 round3 报告 §6.2。）

- **类别**：统一机会（同一仓库内的机制漏接）。
- **现状**：`transport/http/capsule.go:954-971` 整批一次锁；`:978-1005` 每包 `SendDatagram(bytes)` 复制；
  `:683-685` `FrontHeadroom()` = 6（capsule 预算），而 owned 需要 9。
- **频率**：每个 CONNECT-UDP 数据报。
- **开销模型**：1 次 payload memcpy（1 400 B ≈ 30–50 ns）+ 1 次队列锁/信号。
- **方案 A**：单包 `SendDatagramOwned`；批按能力预分组，datagram 组 `SendDatagramsOwned(≤32)`，
  capsule 组保持顺序；`FrontHeadroom` 6→9。
- **方案 B**：保持复制。理由：L4 的 relay 本来就是 `[]byte` 语义（`bufio.CopyPacket`），
  收益可能被上游拷贝掩盖。
- **新引入风险**：批内顺序、拒绝回滚、headroom 变更的连带影响。
- **不变量**：wire format、capsule 回退决策、错误不被降级成 capsule。
- **预期收益**：未测（需 `payloadcopies/op` 与队列信号计数）。
- **级别**：P2。**建议：INVESTIGATE → CHANGE_LATER。**

### 3.4 Q4（R 类）：同一 H3 连接上所有 CONNECT-IP session 共享一个 32 帧 DATAGRAM 队列

- **类别**：正确性/公平性风险（未覆盖）。
- **现状**：`quic-go@7e08ecab:datagram_queue.go:15,61-89`：每条连接一个队列，容量 32，
  满则 `Add/AddBatch` 阻塞。`transport/masque` 的 session 只持有 `quic.Conn`，**没有** per-session 队列。
- **后果**：一个 session 打满队列时，另一个 session 的 `WritePacketBuffers` 会被阻塞；
  服务端多租户场景下这是**跨 session 的队头阻塞**。
- **证据**：源码确认；**没有任何测试覆盖**（现有隔离测试用假 stream）。
- **方案 A**：不改数据面；在服务端做"单 session 在途 datagram 上限"（应用层背压），
  或在 `session` 层记录被阻塞时长并暴露指标。
- **方案 B**：保持现状（QUIC 本身没有 per-stream DATAGRAM 队列；要隔离只能多连接）。
- **不变量**：不能因为隔离而给每个 session 开一条 QUIC 连接（那会破坏 CONNECT-IP 的单连接语义）。
- **证伪**：两 session，一个持续打满，另一个测 p99 延迟。
- **级别**：P1（风险）/ 证据 `VERIFIED_SOURCE`。**建议：INVESTIGATE（先测）。**

### 3.5 Q5（P3）：`domain_keyword` 的两件事（其中大小写是上游语义）

- **Q5a 可用性**：关键词未小写化 → 大写关键词永不匹配（`route/rule/rule_item_domain_keyword.go:15-17,29`）。
  **但该文件与 `upstream/testing` 逐字节相同**，是上游语义；fork 单方面修会让同一配置在
  fork/上游行为不同。建议只记账（或加一条配置期 warning），不改变匹配语义。
- **Q5b 性能**：线性 `strings.Contains`；实测 K=1 024 时 11.3 µs/次。
  AC 语义**完全等价**（该 item 是纯布尔"任一子串命中"），门槛 K≈128。
- **方案 A**：Q5a **不改匹配语义**，只在文档/校验期提示；Q5b 等 K 分布。
- **级别**：Q5a P3（上游语义分支）；Q5b P3。**建议：Q5a KEEP + 记账，Q5b INVESTIGATE。**

### 3.6 Q6（P1，研究假设）：`PreMatch` + `matchRule` 的双整表扫描

- **类别**：已证性能瓶颈（未量化）。
- **现状**：`route/route.go:490-617` 与 `:1258-1322` 各含一次 `prepareMatchMetadata` + 完整 `for range r.rules`。
- **频率**：每个"先判 flow 再进用户态"的连接一次×2。
- **为什么只是假设**：没有 profile 证明它在总 CPU 中占比。
- **可以安全复用的中间信息**：连接级常量（源地址/端口、inbound、network）——正好对应
  `adapter/inbound.go:163-188` 那 4 个**从未被写入**的布尔字段。
- **方案 A**：先删除死字段（减少误导），再评估"两段扫描共享一次判定"。
- **方案 B**：保持现状（规则表通常 < 100 条，两次扫描可能只有几 µs）。
- **级别**：P1（若 profile 证实）/ 目前 `SUSPECTED`。**建议：INVESTIGATE。**

### 3.7 Q7（P3）：`retiredFakeIPAddress` 的成本取决于存储后端（已实测）

见 round3 报告 §7.3。本轮用真实 `fakeip.NewStore` 与 bbolt 读事务代理测出：

| 后端 | 每次 out-of-range 目的地 | 每连接（PreMatch + matchRule） |
|---|---|---|
| 内存（默认） | 25–28 ns，0 allocs | ~55 ns，0 allocs |
| `store_fakeip: true` | ~590 ns，608 B，7 allocs | **~1.2 µs，14 allocs** |

**结论**：默认配置下这不是问题（此前"每连接重建状态"的描述过于悲观）；只有启用
`store_fakeip` 时它才是"每连接两次 bbolt 读事务"的真实成本。**建议：内存后端 KEEP；
`store_fakeip` 用 atomic 快照 + 三个既有失效点（Start / 范围变更 / flush）改造，B+。**

### 3.8 Q8（P2）：Mieru `dialSocketTimeout` 无事实导致的策略不对称

- **类别**：统一机会（错误事实层，不是实现层）。
- **现状**：`protocol/mieru/fallback.go:417-421` 明确不给事实；`:527-539` 又据此切换 secondary。
- **后果**：外层 group 看到 Unknown，既不重试也不惩罚。
- **方案 A**：给 socket timeout 附加 `{Dial, Path}`，但**保留**"Unknown 不是结论"的语义
  （timeout 是"可证事实"还是"未知"本身就是一个产品判断，必须由数据决定）。
- **风险**：可能让"目标站点慢"被误判为"节点坏"——这正是 `adapter/failure.go` 设计时刻意避免的。
- **级别**：P2。**建议：INVESTIGATE（先看真实流量里 timeout 的归因分布）。**

### 3.9 Q9（P3）：XHTTP 每个 POST 的固定成本

- **现状**：`Header.Clone` + `url.URL` + `strings.Repeat` padding（100–1 000 B）+
  `time.NewTimer` + `context.WithTimeout`（`conn.go:1196-1216`、`client.go:396-423`、`xpadding.go:42-53`）。
- **判断**：这些**大部分是协议要求**（padding 是 Xray 的兼容特性），
  真正可优化的是 timer 复用与 `Header.Clone`。**在 POST 频率被 30 ms 下限约束的前提下，收益很小。**
- **级别**：P3。**建议：KEEP。**

### 3.10 Q10（P2）：AnyTLS 的一缓冲深 `push` 造成 session 级 HOL

- **现状**：`sing-anytls@580984e4:stream.go:59-84`：`push` 在 `s.readPending != nil` 时循环等待；
  而 `session.readLoop` 顺序处理所有流（`session.go:276-296`）。
  即**一个慢读者会阻塞整条 TLS 连接的读循环**。
- **对比**：XHTTP 每个 mode 有自己的 reader goroutine；MASQUE 每 session 有独立队列。
- **方案 A**：不改协议实现（这是 `sing-anytls` 的上游语义）；
  在本仓库层面无解，除非改依赖。
- **方案 B**：保持现状，但在文档/知识库里明确"AnyTLS 不适合单会话多流且流速率差异大的场景"。
- **证伪**：两流（一大一小）+ 小流停读，测大流吞吐。
- **本轮尝试过（收尾补充）**：用 `net.Pipe` + 真 `sing-anytls` 搭了两流 stand，
  但**对照组**（持续 drain 第一条流）都无法让第二条流回显，说明夹具没有建立可用基线；
  按本仓库规则记为 `NOT_TESTED`，**不把红灯当结论**。
- **级别**：P2 / `VERIFIED_SOURCE`（机制），影响 `NOT_TESTED`。**建议：INVESTIGATE（先修 stand）。**

### 3.11 原创探索假设（≥5 个，均可证伪）

| ID | 假设 | 如何证伪 | 若成立的价值 | 当前证据 |
|---|---|---|---|---|
| **H1** | 连接建立期的**双规则扫描 + 两次 `prepareMatchMetadata`** 是"高连接速率"场景的第一大 CPU 项，占比 > 协议握手 | 用 `pprof` 在 5 000 conn/s 的合成负载下取 CPU，比较 `route.PreMatch`/`matchRule` 与 TLS 握手 | 若成立，去重是**跨全部六协议**的收益 | 未测 |
| **H2** | AnyTLS 是七 surface 中**唯一**存在"单流慢读者阻塞整连接"的协议；因此在混合流负载下它的尾延迟最差 | 两流 stand：一个停读，测另一个；与 XHTTP/ MASQUE 对比 | 决定是否在文档里限制 AnyTLS 的使用场景 | 机制已确认，影响未测 |
| **H3** | 上传 gate 的**安装**（而非其运行）改变了 conn 的能力链，使某些本可 `splice` 的路径退化 | 检查七 surface 是否有任何一个能同时满足"syscall.Conn + WriterReplaceable" | 若无一满足，则 gate 无害，可以永久关闭这个怀疑 | 本轮已初步证伪：全部 TLS 包装 ⇒ 无 syscall 能力（`sing@baed8c3b:common/network/direct.go:222-238`） |
| **H4** | 同一 H3 连接的 32 帧共享 DATAGRAM 队列让 MASQUE 服务端吞吐随 session 数**次线性** | 2/4/8 session 并发 stand，测总吞吐与单 session p99 | 若是，需要应用层背压或分连接策略 | 机制已确认，影响未测 |
| **H5** | `sc_max_each_post_bytes` 很小时，XHTTP packet-up 变成 **CPU/POST 速率受限**而不是带宽受限 | 固定总字节，扫 `sc_max_each_post_bytes ∈ {4K, 64K, 1M}`，看 POST/s 与 CPU | 决定默认值是否需要按场景调整（**不是**改默认） | 未测 |
| **H6** | VLESS + `encryption` 的 8 KiB 重切让每字节 AEAD 开销高于普通 TLS，从而在高吞吐时 CPU/GB 更高 | 同机同 payload，`encryption` on/off，测 CPU/GB | 若是，应把它定位为"抗探测优先"而非"性能优先" | 未测（需要能跑的 harness） |
| **H7** | Naive H1 raw CONNECT 在**小传输/短连接**上快于 H2 padded（少一层 framing + flush 语义） | 冷启动 + 1 KB 传输，H1 vs H2，测 p50/p99 | 影响"要不要给 H1 保留优先级" | 未测 |

**其中 H3 已在本轮被自己证伪**（所有六协议都被 TLS 包装，不可能有 syscall 能力），
这正好说明"统一税"的怀疑必须逐条验证，不能靠直觉。

### 3.12 MASQUE 专有深挖问题（≥5，附证据与可证伪性）

| ID | 问题 | 现有证据 | 如何证伪 |
|---|---|---|---|
| **M-Q1** | `ForwardStage.Flush` 的锁是否真的跨到了 quic-go 的阻塞点？ | 源码链完整（§3.1）；内层 device 路径**不**持锁 | 用假 `PacketBufferPort` + 并发 Close 复现 |
| **M-Q2** | L4 的 owned 化是否值得？ | `FrontHeadroom` 6 vs 9；`SendDatagram` 复制；`WriteConnectedPacketBatch` 已批量化锁 | 在 `protocol/http` outbound 上做 `payloadcopies/op` A/B（需施工，本轮不做） |
| **M-Q3** | 混合批次（datagram + capsule）的顺序是否可控？ | 现有 `writeOnePacketLocked` 逐包决策 | 构造一半超大包，校验对端收到的顺序 |
| **M-Q4** | `http3Broken` 的 5 s→5 min 与 `httpclient` 的 5 min→48 h 两套记忆是否会互相矛盾？ | `transport/http/client.go:132-133` vs `common/httpclient/http3_transport.go:30-41` | 同一 authority 先经 tunnel 失败、再经普通请求成功，观察两套记忆 |
| **M-Q5** | Bootstrap winner 缓存（8 条）与 inner resolver 的代际是否同步？ | `bootstrap_cache.go:53,59`；inner 走 device | 切换网络后代际变化，检查 winner 是否仍被复用 |
| **M-Q6** | `DNS_ASSIGN` 快照在 A/AAAA 并发查询下是否可能跨两个 assignment？ | `client.go:772-773` 一次捕获；注释解释了并发 | 构造 A/AAAA 并发 + mid-flight 重新赋值 |
| **M-Q7** | learned DATAGRAM ceiling 被永久下调后，是否有恢复路径？ | round2 已否决"永久下调"，当前实现需要复核 | 丢包事件后观察 ceiling 是否单调下降 |

### 3.13 Mieru 专有深挖问题（≥5）

| ID | 问题 | 现有证据 | 如何证伪 |
|---|---|---|---|
| **R-Q1** | `dialSocketTimeout` 不带事实，是否让真实故障永远得不到组级惩罚？ | `fallback.go:417-421,527-539`；`loadbalance_failover.go:303-305` | 统计真实超时事件的最终归因 |
| **R-Q2** | SDK 的 10 s 握手与 adapter 的 5 s drain 是两个时钟，串起来会不会 > 10 s？ | `outbound.go:25-29`；`dialDrainTimeout = 5s` | 构造"SDK 阻塞 + Close"并量 wall time |
| **R-Q3** | fallback ON 时每节点两条对象树，1 000 节点配置的实际常驻内存？ | 懒启动；socket 按需 | 冷节点 census（round2 的方法可复用） |
| **R-Q4** | preference 翻转后两个 runtime 永不停止，是否需要 idle teardown？ | `outbound.go:51-55`（刻意） | 长跑观察两套 Mux 的空闲资源 |
| **R-Q5** | UDP 桥"报告 SDK 期望地址"是否削弱了源校验？ | `packetconn.go:166-172,187-193` | 伪造源地址的包能否穿透 |
| **R-Q6** | `closePacketConns` 无生产调用者（死代码）是否意味着重建路径漏关 socket？ | `dialer.go:137-142` | 重建后统计 fd 数 |

### 3.14 KEEP / NO CHANGE / REJECT 清单

**KEEP（明确不要改）**

1. MASQUE owned/batch 契约（all-or-nothing、前缀回滚、替换拒绝）。
2. `visibleFault` + typed-before-generic 的 H3 错误分类。
3. H3 verdict 的 claim+CAS + ambiguous outcome + 单临界区 reset。
4. `DNS_ASSIGN` 不可变快照、`PREF64` 独立存储、same-conn DoH 三事实门。
5. XHTTP XMUX 的三段式关闭 + 阈值 3。
6. AnyTLS 的 session 写信号量（channel-as-mutex）。
7. Mieru 的双 runtime + "preference 不迁移会话" + Unknown 语义。
8. Vision 的 `go:linkname` + 版本 tripwire。
9. 上传 gate 的"安装但 inert"设计。
10. `transport/device` 的 `SetPacketWriter` 路径（内层 device 不该有 stage 锁）。

**NO CHANGE（已经足够好，不要再投入）**

1. `common/srs` 的 128 MiB 预算 + 探针。
2. `route/rule` 的 domain/domain_suffix matcher。
3. DNS 读侧的 `dns.Msg.Copy()`（有防污染语义）。
4. TCP 收包合并（Go 栈已有 direct-to-app + 25 ms delayed ACK）。
5. XHTTP packet-up 的聚合窗口（去掉会掉 13 %）。

**REJECT（本轮明确否决的提案）**

1. 新增"统一连接池 / 万能 Timer / 第二路由缓存"。
2. 用 owned batch 替换 HY2/TUIC 的写路径（无批次来源）。
3. 把 H3 verdict 统一到 `common/httpclient` 的按 authority 记忆（作用域不同）。
4. 给 Vision 强加 `io.Reader/Writer` 包装以"统一"。
5. 用 Mieru 的 selector 模型统一 MASQUE 的 H3→H2 与 group failover。
6. 在 Fake-IP 里缓存最终裁决。

### 3.15 下一轮最小施工候选（按 Pareto 排序）

| 顺序 | 候选 | 为什么先做 | 要验证什么 |
|---:|---|---|---|
| 1 | `ReturnPackets` 去分配 | 改动 1 个函数、语义不变、已实测 | `allocs/op == 0` + 顺序断言 |
| 2 | **`equalFoldASCII` 的 `'B'` → `'A'`**（一个字符，fork 自有代码） | 最小、无分叉风险 | 大小写用例 + 现有 DoH origin 测试 |
| 3 | MASQUE L4 owned | 同仓库已验证机制漏接 | `payloadcopies/op` + 混合批次顺序 |
| 4 | `Flush` 锁阻塞**时长分布** | 唯一 P0 风险；耦合已复现（301 ms），只剩频率 | 真实拥塞下 `WritePacketBuffers` 的 p95/p99 阻塞 |
| 5 | 共享 DATAGRAM 队列的多 session 隔离测试 | 未覆盖的公平性 | 两 session p99 |

**如果只做一件**：做 1。**如果只做一件有产品感知的**：做 3。
**如果审计结论是"暂不施工"**：完全成立——第 4、5 项都还缺证据，第 1、2 项是小改动，
可以搭下一次 `sing-tun` pin 的车。

### 3.16 四协议平衡检查（XHTTP / Vision / AnyTLS / Naive 各自专有结论）

> 这一节是为了防止"新增的 MASQUE/Mieru 吸走了全部注意力"。四协议各给：已做的优化、
> 真实热路径、公共组件的使用与拒绝理由、三个风险、最值得 KEEP 的设计、下一轮一项可证伪问题。

**XHTTP**

- 已做（带证据）：packet-up 把"每次 Write 一个 POST"改成有界队列 + 合并 POST
  （`conn.go:908-947` 记录了 2.12 → 22.86 MB/s 的前后对比）；首两次写豁免节流
  （`conn.go:1250-1268`，p99 31.7 ms → 1.6 ms）；XMUX 三段式关闭；POST 体完整消费并传播读错误。
- 热路径：`Write`（mutex + append + Signal）→ flusher（gather + 节流 + 请求构建）→
  XMUX `roundTrip` → H2/H3 stream。
- 公共组件：用 XMUX 池、用 gate（inert）、**不用** route 的 buffer 能力（它没有，
  上层因此退化为拷贝——这是协议属性，不是遗漏）。
- 三个风险：① 每个 POST 的固定成本（timer/Header.Clone/padding）；
  ② `io.Pipe` 在流模式下每写一次 goroutine 切换；③ 聚合窗口的"250 µs"在 0.5 ms 时钟平台上
  实际是 ~520 µs（语义漂移，不是性能问题）。
- 最值得 KEEP：XMUX 的 `openUsage + closed + sync.Once` 组合。
- 下一轮一项可证伪问题：**H5**（小 `sc_max_each_post_bytes` 下是否 POST 速率受限）。

**REALITY Vision**

- 已做：`go:linkname` 注册表 + reflect 偏移把 Vision 直接接到 fork 的 `encryption` conn
  （`protocol/vless/encryption/vision.go:62-79`），并 pin 了 `sing-vmess` 版本
  （`expectedSingVMessVersion` tripwire）；REALITY 的首飞（fragment/spoof）只在握手期生效。
- 热路径：VLESS 帧 → Vision（8 KiB 重切）→ XTLS direct write；`encryption` 变体每 ≤8 KiB 一次 AEAD。
- 公共组件：Upstream 链保留；`WriteBuffer` 在 `*CommonConn` 处**丢失**；
  half-close 在 `realityClientConnWrapper.CloseWrite → Close` 处退化为全关闭。
- 三个风险：① ABI 依赖（字段偏移 + `go:linkname`），`-race`/`checkptr` 不覆盖；
  ② 8 KiB 重切使大流量下每字节开销高于普通 TLS；③ XTLS direct write 首次触发有 5 ms 睡眠
  （`sing-vmess:vless/vision.go:235`）。
- 最值得 KEEP：版本 tripwire + 只在握手期做首飞变换。
- 下一轮一项可证伪问题：**H6**（`encryption` 的 CPU/GB 是否显著高于普通 TLS）。

**AnyTLS**

- 已做：帧头零拷贝预置（`stream.go:319-324`）；padding 只作用于前 8 个包；
  会话池 + idle 30 s + 开流 watchdog 3 s + 入站 15 s 首读超时。
- 热路径：`stream.Write` → `writeData`（整批帧进一个 buffer）→ session 写信号量 → `conn.Write`。
- 公共组件：用 gate（inert，但它没有 syscall 能力，所以 gate 一定被安装）、用 session 池；
  **没有** GOAWAY/draining（协议没有）。
- 三个风险：① 一缓冲深 `push` ⇒ 单流慢读者阻塞整个 session read loop；
  ② 无 `CloseWrite` ⇒ 半关闭不可表达；③ 写信号量把整条连接串行化（正确但会放大尾延迟）。
- 最值得 KEEP：channel-as-mutex 的写序列化 + 帧头预置。
- 下一轮一项可证伪问题：**H2**（混合流负载下的尾延迟是否最差）。

**Naive**

- 已做（Go inbound）：H1 直通 CONNECT 不做 padding（与参考实现一致）；H2/H3 按协商启用
  padding；帧预算 3+payload+padding ≤ 65 536、`writerMTU = 65 278`、headroom 3/255；
  短写 → `io.ErrShortWrite`；每写 flush 且 flush 失败即写失败。
- 已做（Cronet outbound）：`concurrency` 个 Engine 或 1 Engine + N isolation key；首包缓存交接。
- 热路径：`naiveH2Conn.Write` → 就地加帧 → `writeFull` → flush。
- 公共组件：只用 `transport/http` 的 masquerade 处理器；**不**经 fork 的路由/传输层
  （它是服务端）。
- 三个风险：① padding 窗口结束后 `writerMTU`/headroom 归零，缓冲区几何会突变；
  ② Cronet 的 native CPU/RSS 在 Go pprof 之外，无法归因；③ `Inbound.h3Server` 从未被赋值，
  `Close()` 关的是 nil（当前无害，但下一次编辑会误导）。
- 最值得 KEEP：帧预算与 headroom 几何（有测试 pin 住 3 + 65 278 + 255 = 65 536）。
- 下一轮一项可证伪问题：**H7**（H1 在小传输上是否快于 H2）。

### 3.17 最终决策建议：十六个判断题

1. **哪条协议最可能存在高频统一税？**
   **CONNECT-IP**：它每次 TUN 突发都要经过 `ForwardStage` 的锁 + 一次 `ReturnPackets` 分配，
   而 L4/XHTTP 等走的是不同的入口。证据等级：`VERIFIED_SOURCE` + 局部 `MEASURED_LOCAL`。
   XHTTP 也存在每 POST 固定成本，但那部分多半是协议要求。
2. **哪个公共组件最值得横向整合？**
   **返回路径（`portReturn.ReturnPackets`）**——它是六协议共享的、且已证明每次调用都分配。
   不是"整合更多协议"，而是"让共享点自己更便宜"。
3. **哪个组件本来优秀、但接入方式不佳？**
   **`transport/http` 的 owned DATAGRAM**：能力已经写好、测试也齐（CONNECT-IP 在用），
   L4 却仍走复制路径。
4. **哪条快路径必须明确保护？**
   **Vision 的 XTLS 直通**与**AnyTLS 的帧头预置**。两者都不能被"统一 Reader/Writer 包装"压扁。
5. **哪些"重复代码"其实是不同语义？**
   MASQUE 的 L4 与 CONNECT-IP；`transport/http` 的 H3 verdict 与 `common/httpclient` 的按 authority 记忆；
   Mieru 的 selector 与 group failover；XHTTP 的 XMUX 与 AnyTLS 的 session 池。
6. **哪项最可能降低移动端内存/耗电，且必须怎么验证？**
   `ReturnPackets` 去分配与 `s.ports` 回收（若 K 大）。验证必须用**真机**
   （`phys_footprint` + 唤醒计数），**不能**用 `TotalAlloc`。
7. **哪项最可能提升跨国高 RTT 吞吐？**
   没有一项是"提速"型。最接近的是 MASQUE L4 的 owned（省一次拷贝），
   但高 RTT 下的瓶颈是拥塞控制与窗口，**不在**这层。诚实结论：**本轮没有高 RTT 提速项。**
8. **当前测试还有哪些"本地全绿、真实网络变慢/出错"的盲点？**
   ① MASQUE 的隔离测试用假 stream，不覆盖共享 DATAGRAM 队列；
   ② XHTTP 的 benchmark 用 in-process 夹具，不覆盖 30 ms 节流下的真实 POST 速率；
   ③ naive 的 bench 无 socket；
   ④ 所有 Mieru 证据 pin 在旧 SHA；
   ⑤ 没有一条测试覆盖"外部 TUN 把 endpoint 当 `tun.Port` 转发"的拥塞路径。
9. **只允许改最少代码，下一轮最多三个项目是什么？**
   ① `ReturnPackets` 去分配；② `equalFoldASCII` 的 `'B'`→`'A'`（fork 自有、一个字符，
   当前不可达但零风险）；③ MASQUE L4 owned。**不把 `domain_keyword` 大小写算进来**——
   那是上游语义，改了是分叉（§3.5）。
10. **是否应该"暂不施工，先扩充测量"？**
    **部分应该**。`Flush` 锁（Q1）、共享队列（Q4）、双规则扫描（Q6）、AnyTLS HOL（Q10）
    都还没有 profile/红灯，贸然改会变成"用新复杂度换未证收益"。
    但 ① ② 是例外：证据已闭合、改动面极小。
11. **MASQUE L4 与 CONNECT-IP 哪些机制应共享 `transport/http`，哪些必须独立？**
    共享：连接/Stream 生命周期、H3/H2 选择、typed 错误分类、连接池、DoH 资格。
    独立：数据单元（`(addr,payload)` vs IP 包）、MTU/PTB、路由匹配、Context ID 之后的语义、
    owned buffer 的所有权树。**现有分层已经是正确的**，不要合并。
12. **DATAGRAM/Capsule、Owned/Batch/Headroom 在端到端链路的哪个环节可能出现性能悬崖？**
    ① headroom 不足时 `PrependContextID` 会**分配替换缓冲并释放原件**（静默的性能悬崖）；
    ② 批量 >32 时整批被拒 → 退化为逐包（已用分块修复）；
    ③ L4 的 `FrontHeadroom = 6` 使 owned 不可能（结构性悬崖）。
    没有任何实测支持"新的零拷贝整合"。
13. **H3→H2 的"版本不健康"与"应用响应慢"是否在每个分支被正确区分？**
    在当前 SHA 的主要分支上**是**（哨兵 + `noteHTTP3AmbiguousOutcome` + strict 先判）。
    仍缺真实网络证据：慢 200 的判定窗口（3 s）在真实 WAN 高 RTT 下是否足够。
14. **Mieru 的瓶颈究竟在哪？**
    证据（历史）指向 **SDK underlay / 共享 Copy Engine**，不在 adapter。
    本轮没有新热点证据；adapter 侧的两个候选（UoT 缓冲、fallback selector）都已被历史基准证明接近最优。
15. **两套 SDK generation + cooldown 与 group failover 是否重复判断？**
    不重复，但**信息不对称**：节点内已判故障的 `dialSocketTimeout` 对外是 Unknown。
    可以只统一"错误事实"，保留算法自主——这正是现有 `adapter.FailureFacts` 的设计意图。
16. **哪些公共能力只需连接建立期生效，哪些必须保留 per-packet 检查？**
    建立期即可：路由裁决、DNS/Fake-IP 反查、能力探测、gate 安装、headroom 协商。
    必须 per-packet：Context ID 合法性、TTL 递减、PTB 归因、owned 所有权转移、
    checksum（若不走 offload）、TUN 侧端口归属。

---

## 附录

### A. Q1（`Flush` 锁）：已执行的确定性复现

**状态：第 1–5 步已在仓库外的一次性模块 `C:\tmp\r3real` 中执行完毕**（假 `PacketBufferPort`
的 `WritePacketBuffers` 在 channel 上阻塞，真实 `ForwardDispatcher` + `ForwardStage`）：

```
MEASURED: Close still blocked after 300ms while WritePacketBuffers blocks; Stage.access IS held across the port call
MEASURED: Close completed 301.2366ms after the port was released
```

即：**耦合已被证实**（`Close` 等满了整个阻塞期），**发生频率仍未测**——真实触发需要发送队列满
（32 帧）或对端停止读取 capsule。下面的第 6–7 步仍未做，也是"能不能升级为 P1"的唯一剩余门槛。
原始计划保留如下，作为可复核的记录：

1. 在**一次性工作副本**（不是主工作树）里写一个假 `tun.PacketBufferPort`：
   - `PacketHeadroom() = 9`
   - `WritePacketBuffers(bufs)`：把 `bufs` 存起来，然后在 `<-release` 上阻塞（可控）；
   - `WritePackets` 同理。
2. 构造一个 `ForwardDispatcher` + 一个 `ForwardStage`，`Handler.JudgeFlow` 永远返回
   `ActionFlow` 且 `Port` = 上述假 Port。
3. goroutine A：循环 `Dispatch(pkt)` + `Flush()`（模拟 TUN 读循环）。
4. goroutine B：100 ms 后调用 `dispatcher.Close()`（或 `stage` 的关闭路径）。
5. 断言：`Close` 的返回时间 < 2 s；记录 `s.access` 的获取等待。
6. 变体：把假 Port 换成真实 `protocol/masque` 的 `ClientEndpoint` + 一个不读的假 QUIC 对端，
   重复 3–5。
7. **判据**：只有第 5 步超时才升级为 P1；否则把结论写成"锁跨 I/O，但阻塞上界可接受"，
   并在 `flow_dispatch.go:770-772` 的注释里补上这个前提。

### B. 证据索引（可复核锚点）

| 主题 | 锚点 |
|---|---|
| 默认 TUN 栈是 Go 栈 | `sing-tun@a7bd7fac:stack.go:47-66`（`case "", "go": return NewGo`） |
| TUN 读批 64 / iOS 32 | `sing-tun:stack_go_profile_default.go:13-14`、`stack_go_profile_ios.go:11-12` |
| Flush 持锁 | `sing-tun:flow_dispatch.go:772-799` |
| stagedPort 只增不减 | `sing-tun:flow_dispatch.go:699-706,725-731,786-797` |
| ReturnPackets 分配 | `sing-tun:flow_dispatch.go:941-971` |
| bridge 逐包调用 | `sb@5064f20a:protocol/bridge/backend.go:251-263` |
| quic-go 队列满阻塞 | `quic-go@7e08ecab:datagram_queue.go:61-89,113-153` |
| CONNECT-IP owned/batch | `sb@5064f20a:transport/masque/session.go:27-61,436-446,475-543,545-631` |
| L4 批锁 + 复制发送 | `sb@5064f20a:transport/http/capsule.go:572-586,683-685,954-1005` |
| H3 verdict | `sb@5064f20a:transport/http/client.go:339-449,655-658`、`tunnel_client.go:153-182` |
| H3 typed 分类 | `sb@5064f20a:transport/http/h3_error_class.go:50-159`、`stream_error.go:48-226` |
| DNS_ASSIGN / PREF64 / DoH 三事实 | `protocol/masque/dns_assignment.go:519-540`、`pref64.go:40-71`、`protocol/masque/client.go:470-497,826-837` |
| equalFoldASCII `'B'` | `sb@5064f20a:protocol/masque/dns_assignment.go:746-763` |
| XHTTP packetUplink | `sb@5064f20a:transport/v2rayxhttp/conn.go:948-1012,1066-1082,1196-1244,1269-1283` |
| XMUX 不变式 | `sb@5064f20a:transport/v2rayxhttp/xmux.go:78-98,143-158,438-457` |
| Vision 桥 | `sb@5064f20a:protocol/vless/encryption/vision.go:47-79`；`sing-vmess@9b95ab8c:vless/vision.go:67-103,235` |
| AnyTLS 写锁 / push | `sing-anytls@580984e4:session.go:31,446-475`、`stream.go:59-84,283-343` |
| Naive 帧预算 / headroom | `sb@5064f20a:protocol/naive/inbound_conn.go:21,197-215,272-312,330-373`、`docs/naive.md:71-78` |
| Mieru 双 runtime | `sb@5064f20a:protocol/mieru/outbound.go:44-91,141-184`、`fallback.go:449-468,650-692` |
| Mieru 事实表 | `sb@5064f20a:protocol/mieru/fallback.go:389-423`、`protocol/group/loadbalance_failover.go:160-173,287-308` |
| 双规则扫描 | `sb@5064f20a:route/route.go:490-617,1258-1322` |
| 死缓存字段 | `sb@5064f20a:adapter/inbound.go:163-188` |
| domain_keyword | `sb@5064f20a:route/rule/rule_item_domain_keyword.go:15-36` |
| 上传 gate | `sb@5064f20a:route/conn.go:436-465`、`common/trafficsched/gate.go:74-86`、`scheduler.go:373-411` |
| XHTTP buffer 退化 | `sing@baed8c3b:common/bufio/conn.go:158-161` |

### C. 运行过的命令与结果

| 命令 | 结果 |
|---|---|
| `go build ./transport/masque/`（`-mod=readonly`） | 成功；`git status` 无 `go.mod`/`go.sum` 变更 |
| `go test -run '^$' -bench 'BenchmarkDataplaneOutboundBatchSize$' -benchmem -benchtime 300x -count 3 ./transport/masque/` | PASS（见 §2.3） |
| `go test -run '^$' -bench 'BenchmarkPaddingWrite\|BenchmarkPaddingRead\|BenchmarkHeadroom' -benchmem -benchtime 2000x -count 2 ./protocol/naive/` | PASS（见 §2.4） |
| `go test`（`C:\tmp\r3probe`，7 组微基准 + 2 个计时探针） | PASS（见 round3 报告 §12） |
| `go test -run 'TestReturnPacketsConsumes\|TestFlushBlocksUnderLock' -v ./...`（`C:\tmp\r3real`，真实 `ForwardDispatcher` + 假 Port） | PASS；`Close` 被拖住 301.24 ms |
| `go test -run '^$' -bench 'ReturnPacketsReal\|FlushKPorts' ./...`（同上） | 2 allocs/72 B（1 包）；K=1/8/64/256 → 2.7/5.1/25.7/25.7 ns/包 |
| `go test -run 'TestACEquivalence\|TestDenseACEquivalence' ./...` | PASS（AC 与线性在语料上一致） |
| `go test -run '^$' -bench 'ACMisz\|DenseAC\|LinearMiss\|ACBuild\|DenseACBuild' ./...` | 稠密表 95 ns 恒定；`map` 边 826–968 ns |
| `-race` 任何形态 | `BLOCKED_BY_ENVIRONMENT`（`CGO_ENABLED=0`，本机无 cgo） |
| 六协议 E2E / 真实 WAN / 真机 | `NOT_TESTED` |

### D. 工作树变动审计与权限说明

- **本任务被授权写入的范围**：`KNOWLEDGE/reports/core/` 下**这一个**新 Markdown 文件。
- **实际改动**：本任务只写了这一个文件。同一个提交 `2b5e6951d` 里还包含
  `KNOWLEDGE/reports/anywhere-round3/` 的两份文档与 `KNOWLEDGE/reports/README.md` 的一行索引——
  那是**同时执行的另一个任务**（Anywhere 第三轮审计）明确授权的产物，不是本报告的一部分。
  除这四项外，提交里**没有**任何其它路径。
- **未修改**：`KNOWLEDGE/INDEX.md`、`KNOWLEDGE/README.md`、`KNOWLEDGE/BASELINE.md`、
  `KNOWLEDGE/modules/**`、既有报告、任何 `.go`/配置/CI/依赖文件。
- **为什么不在 `KNOWLEDGE/reports/README.md` 里加索引行**：本任务的写权限明确只覆盖这一份报告，
  而 `KNOWLEDGE/reports/README.md` 属于既有知识库内容。因此本报告**没有**索引条目；
  若后续允许，加一行 `core/ | SIX_PROTOCOL_UNIFICATION_EFFICIENCY_AUDIT_2026-10-20.md | 2026-10-20 | …`
  即可（与 `anywhere-round3` 的索引行格式一致）。
- **分支**：`testing`（既有，未新建、未合并、未 rebase、未 force-push）。
- **提交/推送**：见 §E。

### E. 提交与推送结果

| 项 | 值 |
|---|---|
| 审计时的生产代码 SHA（docs 提交之前） | `5064f20a97880d3d5cfe1624c7f79da8d3b03b5c` |
| 归档内容提交 | `2b5e6951dbe370949417de6a316aed69572d0601`（`docs(knowledge): archive Anywhere round-three audit and six-protocol efficiency audit`，与 Anywhere round-three 报告同一个提交；父提交 `5064f20a9`） |
| 本记录提交 | 只补写本表与 §D 的归档事实，不改任何结论；它使 HEAD 前移，远端 HEAD 等于本记录提交 |
| 推送目标 / 方式 | `origin/testing`；普通 `git push`（`5064f20a9..2b5e6951d`），**无** `--force`、**无** rebase、**无** reset |
| 推送后远端一致性 | `git ls-remote origin refs/heads/testing` = `2b5e6951dbe370949417de6a316aed69572d0601`，与本地 HEAD 一致 |
| 并行窗口 | 提交前 `git fetch origin testing`；远端未前移 |

### F. 完成条件自检（对应任务的退出检查表）

| 条件 | 状态 |
|---|---|
| 七个 surface 的双向路径、控制/资源/数据三层差异 | ✅（§1.1–1.6） |
| 公共组件的适用性、真实覆盖与热路径成本 | ✅（§1.7、§1.9） |
| 当前 SHA 与知识库基线的差异，不把历史报告当新事实 | ✅（§2.6 明确区分 `RECORDED_HISTORY`） |
| 成本账本、矩阵、生命周期/所有权说明 | ✅（§1.7–1.9） |
| 六协议各自专有重点与 KEEP/REJECT | ✅（§3.16、§3.14） |
| 对高价值发现反证，并给出"不整合/保留快路径"的案例 | ✅（§3.11 H3 自我证伪；§3.14 REJECT 清单） |
| ≥5 个原创跨协议假设 | ✅（§3.11，H1–H7） |
| MASQUE ≥5 个专有深挖问题 | ✅（§3.12，M-Q1–M-Q7） |
| Mieru ≥5 个专有深挖问题 | ✅（§3.13，R-Q1–R-Q6） |
| 下一轮施工优先级，但**没有修改任何产品代码** | ✅（§3.15；§D） |
| 没有新建分支，没有修改依赖/配置/CI/测试/子模块/既有知识库 | ✅（§D） |
| 仅一份新报告加入 `KNOWLEDGE/reports/core/` | ✅ |
| docs-only commit / push（不覆盖他人提交） | ✅（§E，若被阻塞会在此声明） |
| 最终回复给出结论摘要、路径、SHA、是否 push、未验证项 | ✅（见对话回复） |

### G. 未验证项（`NOT_TESTED` / `BLOCKED_BY_ENVIRONMENT`）

真机内存与能耗、Cronet native CPU/RSS、真实 WAN 吞吐/尾延迟/丢包、
MASQUE 与独立 QUIC/H3 对端的互通、Mieru 与当前官方 `mieru`/`mita` 的互通、
`-race`（无 cgo）、`with_quic`/`with_low_memory`/iOS 标签的完整套件、
以及任何"施工前后"A/B（本轮禁止施工，因此**根本不存在**）。


