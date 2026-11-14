# 03 资源预算矩阵（Resource budget matrix）

> 判别维度按施工指令 §4.2：**对象数量**、**字节预算**、**在途并发**、**等待队列**。
> 限速率与限并发是不同机制，不能互相替代；本表只写当前 HEAD 真实存在的约束。

## 1. 总表

| 层 / 机制 | 对象数量 | 字节预算 | 在途并发 | 等待队列 | 证据 |
|---|---|---|---|---|---|
| ResourceGuard 创建准入 | 受 permit 约束的创建并发 | 无字节账本 | Normal 32 / Armed 16 / Critical 8（+ interactive reserve 4） | Normal 256 / Armed 128 / Critical 32，等待 5 s / 2.5 s / 100 ms | `common/resourceguard/guard.go:90-99`、`:112-138` |
| Gate 快速失败 | — | — | — | 4 次 <250 ms 连续失败后 100 ms–2 s 指数退避（±20%） | `common/resourceguard/rate.go:75-79` |
| relay copy 增长预算 | 已增长方向的 copy（每方向一个 permit） | 每方向约一个放大读缓冲（~64 KiB 量级） | Normal 256（+16 保留）/ Armed 128（+8）/ Critical 0 | 无队列（拒绝即保持小缓冲） | `common/resourceguard/growth.go:140-179`、`route/conn.go:201-207` |
| route 连接登记/处置 | 受管连接节点 | 无字节账本 | 处置积压 ≤ 64（pending+condemned+syncing+admitted） | 等待 `roomSignal` 或 ctx 取消 | `route/conn_reclaim.go:583`、`:415-458`、`:651-654` |
| route dial governor | — | — | 3 s 窗口内 24 次拨号（软限，超预算后不再延迟） | 无队列 | `route/dial_governor.go:44-49`、`:126-131` |
| TUN UDP NAT 会话 | iOS 4096 / 其他 16384（LRU 硬容量） | 每会话 256 KiB；每 NAT 实例 8 MiB；channel cap 64 包 | 每新会话一个 goroutine | 包队列满即丢弃并计数，不等待 | `<sing-tun>/udp_nat.go:73-76`、`:140-149`、`:396-406`、`:516-533`、`:583-603` |
| TUN UDP NAT 字节账本的范围 | — | **每 NAT 实例** 8 MiB；当 `Mapping=AddressAndPortDependent` 拆成 `natCount` 个实例时，会话数被拆分，**字节预算不拆分** | — | — | `<sing-tun>/stack_go.go:167-189` 与 `udp_nat.go:167-175`（默认值在每个实例上重新应用） |
| DNS hijack | 每包一个解析 goroutine | 无 | 256 | 超限直接丢弃（Debug 日志） | `route/dns.go:91`、`:130-143` |
| DNS 非 hijack 查询 | 每次 exchange 一个 goroutine | 无 | **无统一上限** | 无 | `dns/transport/multiplexer.go`、`dns/router.go`（见 `08` 开放问题） |
| TUN go stack（iOS profile） | `goFlowCapacity = 1024` | `goReceiveCapacityMax = 256 KiB` | — | — | `<sing-tun>/stack_go_profile_ios.go:6-13` |
| AnyTLS session | 受 guard 创建并发约束；**无 live session 数上限** | 无字节账本 | 创建 32 并发（gate） | 256（gate） | `protocol/anytls/admission.go:41-59`、`guard.go:90-99` |
| 共享 QUIC（Hysteria/Hysteria2/TUIC） | 每 outbound 一条共享连接 | 由 QUIC 自身流控 | 每 outbound 单飞；跨 outbound 由 guard | 256（gate） | `<sing-quic>/quic.go:364-382` |
| v2rayquic 共享连接 | 每 transport 一条 | 由 QUIC 流控 | 每 transport 单飞；跨 transport 由 guard（本轮补齐） | 256（gate） | `transport/v2rayquic/client.go:529-548` |
| Mieru underlay | SDK 内部 Mux 控制 | 无字节账本 | 每 underlay 创建受 gate | 256（gate） | `protocol/mieru/dialer.go:85-122` |
| XHTTP/XMUX 物理连接 | 默认 3；`max_concurrency` 且未设 `max_connections` 时由并发需求决定（每连接并发满即新开） | 上行队列字节有界（实现内） | 由 XMUX 配置 | 见 `08` | `transport/v2rayxhttp/xmux.go:761-799`、`:811-814`、`:847` |
| MASQUE tunnel | 每 endpoint 一条当前 session | 无字节账本 | 每 endpoint 单飞 | 无 | `transport/masque/client.go:356-390` |
| trafficsched | 每 flow 一个调度对象 | 速率由 RateSource 决定（默认无速率＝全放行） | — | 有界调度队列（实现内） | `route/conn.go:153-168` |

## 2. 明确"只有数量、没有字节"的地方

施工指令点名的假设需要逐条回答：

- **TUN NAT 上限是否只限数量？** 不是。会话数和字节都有上界（256 KiB/会话、8 MiB/NAT），且入队前预留字节。**但**字节预算的粒度是"每 NAT 实例"，拆分实例后总预算会翻倍；且首包引用不计入字节账本（`sing-tun` 在 `prepare`/`handshake` 期间持有首包）。
- **ResourceGuard 是否限了字节？** 没有。它只限创建并发与等待者数量，这是设计选择（创建成本近似同质），字节由 copy growth budget 与 NAT 字节账本承担。
- **XHTTP 是否有请求队列字节预算？** 上行有界；物理连接数在一种合法配置组合下没有硬上界（见 `08`）。
- **MASQUE / XHTTP H3 是否有任何共享预算？** 没有进入 guard；当前只受配置与单飞约束。

## 3. 压力等级对预算的实际影响

| 等级 | 创建准入 | copy 增长 | 回收动作 | 证据 |
|---|---|---|---|---|
| Normal | 32 / 256 / 5 s | 256（+16） | 仅 idle 机会（空闲缓存/池） | `guard.go:112-138`、`growth.go:160-179` |
| Armed | 16 / 128 / 2.5 s | 128（+8） | 同上；TUN 池按 `MemoryPressureWarning` 收缩 | 同上 + `service/oomkiller/timer.go:1180-1189` |
| Critical | 8 / 32 / 100 ms | 0（不增长） | 进入 Critical 时同步执行 shed hooks（仅关闭空闲 session）；OOMKiller 执行网络 reset/缓存 flush/GC | `guard.go:397-399`、`protocol/anytls/outbound.go:116`、`protocol/naive/outbound.go:323`、`protocol/trusttunnel/outbound.go:153` |

## 4. 本轮新增/改变的预算行为

| 变化 | 影响 |
|---|---|
| 非 connected UDP setup 进入 dial setup gate | 每个在途 UDP setup 多一个代次 watch；建立或失败即释放，不改变稳态预算 |
| 非 connected UDP setup 携带 traffic class | 交互类 UDP 流现在能使用 `InteractiveReserve`（4 个创建槽 + copy 保留 16） |
| 不可测量 footprint → ARMED | 采样失败期间创建上限从 32 降到 16、队列 256→128、等待 5 s→2.5 s；不触发破坏性回收 |
| v2rayquic 共享创建 → guard | 该传输的 QUIC 创建计入 32 并发上限与 256 队列；复用不计费 |

## 5. 没有被预算覆盖的已知项（不冒充已解决）

- 同一目的的 **并发首次 session 创建没有合并**（N 个 flow 可产生 N 次握手，只被限速）。
- XHTTP H3 / MASQUE H3 的物理创建。
- DNS 非 hijack 查询的 goroutine 并发。
- 依赖仓库内部的队列（SDK Mux、quic-go 流控、Cronet 内部缓冲）无法从 Go 侧精确计量。
