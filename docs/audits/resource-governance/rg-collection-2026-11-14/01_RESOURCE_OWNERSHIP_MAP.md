# 01 完整资源所有权地图（Resource ownership map）

> 依据：当前 branch HEAD（`f1f0ac4e9`）源码 + 已 pin 依赖源码（`sing-tun@v0.9.7-0.20261112091034-3c0ead27405b`、`sing-anytls@v0.0.0-20261112100203-f4acc22de770`、`sing-quic@v0.7.2-0.20261112101651-c1b074f1f868`、`mieru/v3@v3.39.1-0.20261112135526-b5934f7136b4`）。
> 标记：`MEASURED` 真实采集；`COUNTED` 对象数可精确统计；`ESTIMATED` 按容量/对象估算；`UNOBSERVED` 无可信观测；`NOT_APPLICABLE` 该路径不存在；`OUT_OF_REPO` 代码不在本仓库（Apple 客户端/Swift）。

## 1. 真实调用图（Go 侧）

```text
[iOS Swift NE / PacketFlow]                              OUT_OF_REPO (sing-box-for-apple)
        │  packets over the libbox platform bridge
        ▼
[experimental/libbox]  platform interface / TUN inbound   libbox/{...,tun}.go
        │
        ├─ System Proxy entry: OS proxy set by common/settings/system_proxy.go,
        │  traffic arrives on the configured inbound (mixed/http/socks)   [Go]
        ▼
[TUN inbound] protocol/tun/inbound.go                     [Go]
        │  stack handler
        ▼
[sing-tun stack]  "go" stack is the default (stack.go NewGo)         [Go, dependency]
        ├─ UDP: UDPNat.getOrCreate → UDPNatConn + packetChan(64) +
        │        per-session goroutine + byte reservation
        ├─ TCP: FlowDispatcher/forwardFlow (go stack) or TCPNat (system stack)
        └─ DNS hijack → route/dns.go HijackDNSPacket
        ▼
[route.Router] route/router.go, route/rule                                [Go]
        ├─ snapshot: sniff / FakeIP / pre-match / rules / traffic_class
        ▼
[route.ConnectionManager] route/conn.go / conn_reclaim.go                 [Go]
        ├─ stream:  enterDialGate → dialSetups.begin → outbound dial
        └─ packet:  UDPConnect arm (gate) / non-connected arm (round-1 gate)
        ▼
[common/dialer]  family policy, epoch checks, AdmitConnection/track      [Go]
        ▼
[protocol outbound / transport]                                           [Go]
        ├─ AnyTLS        → pinned sing-anytls Client.createSession
        ├─ Hysteria2/Hysteria/TUIC → pinned sing-quic offerNew → QUIC handshake
        ├─ TrustTunnel   → shared session client
        ├─ Mieru         → SDK newUnderlay (Mux/encrypted session above it)
        ├─ v2rayquic     → Client.offerNew (shared QUIC connection)
        ├─ TLS-based     → common/tls handshakeWithAdmissionFor
        ├─ XHTTP/XMUX    → H1/H2 via common/tls, H3 via quic.DialEarlyConn
        ├─ MASQUE        → tunnel client (H2 via common/tls, H3 direct QUIC)
        └─ Naive/Cronet  → native engine socket callback (Darwin builds only)
        ▼
[creation admission] common/resourceguard                                  [Go]
        ├─ guard.AdmitCreation / AcquireFor (permit, bounded queue)
        └─ pressure from service/oomkiller (single atomic)
        ▼
[streams / datagrams / buffers]                                            [Go]
        ├─ route copy: bufio.CopyWithIncreateBuffer + GrowthBudget permits
        ├─ sing buffer pool (common/buf) — pooled, not owner-tracked
        └─ per-protocol stream/session tables
        ▼
[close / cancel / drain / retire / reload / network change]                [Go]
        ├─ route.ConnectionManager.CloseAll / Reclaim / drain sweep
        ├─ dialSetupGate generation cancel
        ├─ runtimecoord generations + ReferenceManager retire
        └─ protocol client Close / CloseIdleConnections
        ▼
[iOS process phys_footprint]   TASK_VM_INFO (sing/common/memory memory_darwin.go)
        + native allocations: Swift/ObjC/C, Cronet engine, PacketFlow queues   OUT_OF_REPO
```

**跨 Go/native 边界的三处**：

1. `PacketFlow` / NE 生命周期与进程 footprint（Swift/ObjC，本仓库只能通过 `adapter.PlatformInterface` 读取平台事实）。
2. Cronet（`github.com/sagernet/cronet-go`，C++ 引擎，Go 侧只能看到 socket 回调与引擎句柄）。
3. Apple TLS engine（`common/tls` 的 apple engine，TLS 状态在 native）。

## 2. 每层资源清单与计量状态

| 层 | 对象 | 创建函数 | 所有者 | 释放函数 | 计量 | 计量来源 | 上限 |
|---|---|---|---|---|---|---|---|
| PacketFlow（native） | packet queue | `PacketFlowBridge.swift` | Apple client | NE stop / backpressure | `UNOBSERVED`（OUT_OF_REPO） | — | 客户端有界队列（本仓库不可见） |
| TUN inbound | handler / goroutine | `protocol/tun/inbound.go` | TUN 服务 | `Close` | `COUNTED`（goroutine 数） | runtime | 平台 |
| UDP NAT | entry | `UDPNat.getOrCreate` | `UDPNat` LRU | LRU 淘汰/`Close` | `UNOBSERVED`（依赖未导出计数） | 只有内部 drop 计数 | `MaxSize`（iOS 4096 / 其他 16384） |
| UDP NAT | per-session goroutine | `handler.NewPacketConnectionEx` | `UDPNatConn` | conn close | `COUNTED`（间接：Goroutine 总数） | runtime | 随 `MaxSize` |
| UDP NAT | packet buffer / channel | `new conn` | `UDPNatConn` | 出队/close | `COUNTED`（字节预留） | `queuedBytes` 内部 | 256 KiB/会话、8 MiB/NAT 实例 |
| UDP NAT | cleanup goroutine / heap | `udp_nat_cleanup.go` | `UDPNat` | `Close` | `COUNTED`（1 个/NAT） | 源码 | 1 |
| TCP NAT / flow dispatch | forwardFlow / dispatcher | `flow_dispatch.go` | stack | close/evict | `UNOBSERVED` | — | 16384 entries / 1024 per stage |
| DNS | query goroutine | `route/dns.go` / `dns/*` | route/dns | 查询结束 | `COUNTED`（hijack in-flight） | `dnsHijackConcurrency` 计数 | 256（仅 hijack 入口） |
| DNS | cache / response | `dns/cache.go` | DNS client | LRU/expire | `UNOBSERVED`（无导出条目数） | — | 配置有界 |
| Router | dial attempt / task | `route/conn.go` | `ConnectionManager` | 返回/取消 | `COUNTED`（受管连接） | `ConnectionManager.Count()` | 处置预算 64；dial governor 24/3 s |
| ResourceGuard | permit | `Guard.AcquireFor` | guard | `Permit.Release` | `MEASURED` | `GuardStats.Gate` | 32/16/8 |
| ResourceGuard | waiter | `Gate.Acquire` | gate | grant/withdraw | `MEASURED` | `Gate.Stats.Waiters`（峰值由采样估计） | 256/128/32 |
| ResourceGuard | queue | waiter list | gate | 同上 | `MEASURED`（数量） | `Gate.Stats` | 同上；无字节账本 |
| shared TLS | TCP+TLS conn | `handshakeWithAdmissionFor` | 调用方 | copy 结束 close | `COUNTED`（`Admission`/`Released`） | guard | 创建并发 32 |
| QUIC transports | physical conn | `offerNew` / `DialEarlyConn` | SDK client | `Close`/idle | `PARTIAL`：`COUNTED`（residency ledger，部分 client）；`UNOBSERVED`（transport 内部） | `adapter.ResidencyLedger` | 创建并发 32 |
| HTTP/XMUX | H1/H2/H3 conn | `xmux.newClientLocked` | XMUX pool | pool close/drain | `COUNTED`（residency ledger） | `transport/v2rayxhttp` residency | 默认 3；`max_concurrency` 模式无硬上界 |
| Protocol SDK | runtime/client/underlay | 各 `NewClient`/`newUnderlay` | 协议 outbound | `Close`/`Stop` | `UNOBSERVED`（SDK 内部） | — | SDK/配置 |
| Buffer pool | slab / pooled bytes | `buf` 包 | `buf` 全局池 | `Release` | `UNOBSERVED`（无池统计） | — | 池按需增长 |
| Go runtime | heap/stack/sys | — | Go runtime | GC | `MEASURED` | `runtime.MemStats` / `runtime/metrics` | GOMEMLIMIT（仅 Go） |
| 进程（Windows） | working set / private | — | OS | — | `MEASURED`（本机） | `sing/common/memory.Total()` = workingSet | — |
| 进程（iOS NE） | `phys_footprint` | — | OS | — | `MEASURED`（仅设备） | `TASK_VM_INFO` | 平台（约 50 MiB 观测预算） |
| Native（Cronet/Swift） | engine / buffers | — | native | native | `UNOBSERVED` | — | 平台/SDK |

**估算字节不得写成真实 Footprint**：本表只有 `MEASURED` 的 runtime 与进程数值可作为内存证据；其余均为对象数或容量上限。

## 3. 每个逻辑流 → 物理资源的真实映射（当前构建）

| 入口 | 一个逻辑流是否创建物理连接 | 复用 | 证据 |
|---|---|---|---|
| AnyTLS TCP | 否（stream 开在共享 session 上；session 不存在时才创建） | 是，session 池 | `sing-anytls/client.go` `DialContext`→`takeIdleSession`→`createSession`；`protocol/anytls/admission_reuse_test.go` |
| AnyTLS UoT | 否（UoT 流复用同一 session） | 是 | `uot.Client` + `dialAdmitted` |
| Hysteria2/Hysteria/TUIC | 否（QUIC stream；连接不存在时才创建） | 是，每 outbound 一条 | `sing-quic` `offerNew` + `TestOneSharedCreationChargesOneAdmission` |
| v2rayquic | 否（stream 开在共享 conn 上） | 是 | `transport/v2rayquic/client.go` `offer` + `TestEstablishedSessionIsReusedWithoutASecondPermit` |
| Shared TLS outbounds | **是**（每流一条 TCP+TLS） | 否（无池） | `common/tls/client.go:152-216` |
| XHTTP H1/H2 | 否（XMUX 池按 concurrency 复用） | 是 | `transport/v2rayxhttp/xmux.go` |
| XHTTP H3 | 否（H3 transport 池） | 是 | `http3.go` |
| Mieru | 否（Mux/加密 session 复用；underlay 满/回退时新建） | 是 | `protocol/mieru/dialer.go:85-122` |
| MASQUE | 否（一条 tunnel 承载多流） | 是 | `transport/masque/client.go:356-390` |
| Naive/Cronet | 否（HTTP/2/3 CONNECT 复用） | 是 | `protocol/naive/native_creation.go` |
| WireGuard | 否（device 内多流） | 是（device 级） | `transport/wireguard/endpoint.go` |
| Snell/SS2022/Trojan(无传输) | **是**（每流一条 TCP/UDP，无共享 session） | 否 | `protocol/{snell,shadowsocks,trojan}` |
| Direct | 是 | 否 | `protocol/direct/outbound.go` |

## 4. 观测缺口（下一轮是否补 instrument 的事实依据）

| 缺口 | 应该补在哪一层 | 代价/风险 |
|---|---|---|
| `UDPNat` 条目数、每会话字节、drop 明细没有导出 | `sing-tun`（依赖仓库）+ 一个只读 status 接口 | 低（需依赖版本 bump 与一处适配） |
| PacketFlow 队列深度/字节不可见 | Apple 客户端 `PacketFlowBridge` | 中（Swift 侧跨仓） |
| DNS cache 条目/字节不可见 | `dns` 包状态接口 | 低 |
| Buffer pool 高水位不可见 | `sing/common/buf`（依赖）或 `bufgeom` 侧计数 | 中（热路径成本需测量） |
| XMUX / MASQUE H3 物理连接数只有部分 residency ledger | 各 transport 已有/可补 ledger | 低-中 |
| Cronet/native 内存在 Go 侧完全不可见 | 平台层（Cronet 引擎统计） | 高（跨语言） |
| `queue_bytes`（准入队列）没有字节账本 | 不需要：waiters 不持 buffer；如未来排队发生在分配之后才需要 | 低，但需先证明需要 |
| Guard 峰值 waiter 只能采样估计 | `Gate` 增加 `maxWaiters`（3 行） | 低 |
| iOS `phys_footprint` 采样失败不可表达（已有 `footprintUnavailable` 但无“当前不可用”状态位） | `service/oomkiller` 报告元数据 | 低 |

这些缺口在本阶段只记录，不实现（施工指令 §0 明确不要临时搭建新的监控系统）。
