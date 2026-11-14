# 跨协议资源预算闭环：第一层观测与决策（2026-11-14）

> 目标不是让所有协议共用一个连接数，而是把三层预算分开：
> **逻辑流（TUN/NAT/Dispatcher）→ 物理创建（ResourceGuard）→ 稳态资源（Session/Stream/Pool）**。
> 本轮按施工边界只做第一层的**观测与测量**，并给出"是否需要在 TUN→Route 加逻辑流准入"的决策；没有新增预算抽象，也没有改 XMUX 预算、Mieru、UI 或长期入口架构。

## 0. 结论

| 项 | 结果 |
|---|---|
> 状态口径（两份报告统一）：`FIXED` 代码已改且有测试；`VERIFIED` 行为已确认（无代码改动）；`MEASURED` 有原始测量；`DECISION` 明确决定（含不做）；`PARTIAL` 部分覆盖；`NOT_VERIFIED` 未验证；`NOT_RUN` 未执行；`BLOCKED_BY_ENVIRONMENT` 环境阻止；`UNAVAILABLE` 本环境不可观测。

| sing-tun UDP NAT 观测 | **FIXED**：`UDPNat.Stats()`：entries / max / sessionsCreated / queuedBytes / caps / drops |
| sing-tun TCP Dispatcher 观测 | **FIXED**：`TCPDispatcherStats()`：accepted / active handler 调用；四个 dispatch 点统一走 `goDispatch` |
| 每条逻辑流消耗 | **MEASURED（host Go runtime）**：每条 UDP 流 ≈ 3.4–3.7 KiB HeapAlloc（**已包含** 1.2 KiB 首包）+ 约 7.1–8.0 KiB goroutine 栈；结构成本 heap−queued ≈ 2.2–2.5 KiB/flow。1000 流 ≈ 3.7 MiB heap + 7.1 MiB 栈 ≈ **11 MiB**（不是首版写的 7 MiB） |
| 是否做 TUN→Route 逻辑流准入 | **DECISION（本轮不做）**：修正前的 7 MiB 低估了栈；实测约 11 MiB/1000 流、10.8–11.4 KiB/flow 已越过 8 KiB/flow 触发线，因此该问题升级为**下一轮第一优先决策**；本轮施工边界仍排除长期入口架构 |
| XMUX 物理连接 | **PARTIAL**：`xmuxPoolStats` 统计的是 **Pool Entry（transport wrapper）**，不是物理 socket；已统计 Generation/Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released；**Creating 不是可持久状态**；本轮不新增预算 |
| HTTP/2 请求体 | **OPEN**：现有请求体由 `net/http` 客户端拥有并在 RoundTrip 结束时关闭；没有复现泄漏，不做无证据修改 |
| 其它 Owner 报告 | NAT/Dispatcher/XMUX 有；AnyTLS Session、QUIC 物理连接/流、Mieru Underlay、MASQUE Tunnel、PacketFlow、Native Engine 见 §5 |
| Critical + Network Change 组合测试 | **NOT_RUN**（计划见 §6；原因：需要可控的网络切换与压力注入，超出本轮可安全验证范围） |
| 两个已知小缺口 | 上一轮已修复（H3 本地拒绝不污染故障状态、DNS Lookup 走 gate），本轮复核测试仍在 |

## 1. 第一层观测：实现与证据

### 1.1 UDP NAT（sing-tun 分支 `work/flow-observation-2026-11-14` @ `6e7483019752`）

```go
type UDPNatStats struct {
    Entries            int    // cache.Len(), the datapath's own view
    MaxEntries         int
    SessionsCreated    uint64 // cumulative
    QueuedBytes        int64  // currently held by per-session queues
    MaxQueuedBytes     int64  // NAT-wide cap
    MaxBytesPerSession int
    DroppedPackets     uint64 // existing process-wide drop counter
}
func (s *UDPNat) Stats() UDPNatStats
```

这些值直接来自既有的账本（`queuedBytes`、`maxQueuedBytes`、cache），不是新增的平行计数器；`SessionsCreated` 只在新建 session 的构造闭包里递增。

### 1.2 TCP Dispatcher

```go
type DispatcherStats struct {
    TCPAccepted uint64 // cumulative handler calls
    TCPActive   int64  // handler calls currently in flight
}
func TCPDispatcherStats() DispatcherStats
```

四个 dispatch 点（gvisor `TCPForwarder.Forward`、go stack `stack_go_tcp_input`、system stack、redirect server）统一改为 `goDispatch(func(){ handler.NewConnectionEx(...) })`，计数与真实 goroutine 生命周期一致：进入前 +1，handler 返回时 -1。

### 1.3 每流成本（本机 Go runtime，**不是 iOS phys_footprint**）

`go test . -run 'TestUDPNatStatsReportLogicalFlows|TestUDPNatPerFlowCost|TestTCPDispatcherPerDispatchCost' -v`：

```text
udp flows=200  entries=200  sessions=200  queued=240000  goroutines+200
  heap=814576 B (4072 B/flow, includes queued) structural=574576 B (2872 B/flow)
  stack=1605632 B (8028 B/flow) queued=1200 B/flow
udp flows=1000 entries=1000 sessions=1000 queued=1200000 goroutines+1000
  heap=3668480 B (3668 B/flow, includes queued) structural=2468480 B (2468 B/flow)
  stack=7143424 B (7143 B/flow) queued=1200 B/flow
tcp dispatches=1000 active=1000 goroutines+1000 (dispatcher goroutines only; no per-dispatch heap claim)
```

口径（修正了首版的重复计算）：

- `heap` 是 `HeapAlloc` 增量，**已经包含**队列里的首包（1200 B/flow）；首包是 heap 的子集，**不能**再加到 heap 上。
- `structural = heap − queued`，是本层 entry/表结构/map 等剩余结构成本（≈2.5–2.9 KiB/flow）。
- `stack` 是 `StackInuse` 增量，**不在 HeapAlloc 里**，必须单独计；每条被 park 的 dispatcher goroutine 实际约 7.7–8 KiB 栈。
- 因此每条 UDP 逻辑流 ≈ **3.4–3.7 KiB heap + 7.1–8.0 KiB stack ≈ 10.8–11.4 KiB**；1000 流 ≈ **11 MiB**（heap 3.7 MiB，栈 7.1 MiB），不是首版写的 7 MiB。
- TCP 计数器含义：`TCPAccepted` 是累计的 **handler 调用次数（逻辑 dispatch）**；`TCPActive` 是当前 in-flight 的 handler 调用数（≈ 正在处理的 TCP 逻辑流）。它**不是** TCP 栈 flow-table entry，也**不是** socket 数；四个 dispatch 点包含 system/redirect，所以是进程级而非仅 TUN。该测试只测 dispatcher 的 goroutine 开销，不把空闭包 handler 的 heap 当作 per-dispatch 成本。
- 全部是 **host Go runtime**；iOS `phys_footprint` 还含 PacketFlow、native、runtime 预留，未测。
## 2. 入口准入决策：本轮不做，但触发线已被越过

**修正后的证据**：1000 条 UDP 逻辑流 ≈ **11 MiB** Go 侧（heap 3.7 + 栈 7.1），约为 50 MiB 观测预算的 23%；per-flow ≈ 10.8–11.4 KiB，**已经越过首版设定的 8 KiB/flow 触发线**。首版"约 7 MiB、未超阈值"的结论来自两个口径错误：把队列首包重复计入总量、把 goroutine 栈漏掉。

**决策**：本轮仍不在 TUN→Route 增加逻辑流 gate，但理由变为"边界与验证条件"而不是"证据不足"：

1. 施工边界明确排除"sing-tun 长期入口准入架构"；
2. 没有 iOS 真机数据证明这 23% 在设备上就是主导项（Go 栈在 iOS 的 phys_footprint 中如何计入未测）；
3. 第二道 gate 会改变 UDP 首包语义与失败路径，必须有设备证据才值得承担。

**下一轮第一优先决策**（不再是模糊的触发条件）：

- 先做 2000/4096 流的同口径测量（heap + StackInuse + queued），确认总量与 GC 行为；
- 在 iOS 真机上测同一 burst 的 `phys_footprint`，确认真实占比；
- 若设备上仍 >20%，再设计 TUN→Route 入口预算（只限制"未准入的逻辑流"，不合并三层）。

**保留的硬边界**：`MaxSize`（iOS 4096）与 NAT 8 MiB 字节上限仍有效；本轮不新增 gate。
## 3. XMUX：统计口径是 Pool Entry / Transport Wrapper，不是物理 Socket

`transport/v2rayxhttp/xmux.go` 的 `xmuxPoolStats` 统计 Generation/Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released。**单位已修正并在代码注释中写明**：

- 一个 **Pool Entry** = 一个 `xmuxClient` = 一个 HTTP transport wrapper；
- H3 wrapper 自带一条 QUIC 连接；**H2 wrapper 持有 `http2.Transport`，其内部 pool 可能有多条 TCP/TLS socket**；net/http/http2/http3 才是 socket 的 owner，这里不统计、也不能把 entry 数当 socket 数；
- `Pooled = Active + Idle` 是 entry 数；`Draining` 是 detached entry；`Opened/Released` 是 wrapper 的创建/实际 Close 数，不是 socket 数。

**关键发现**：`get()` 在 `m.access` 锁内调用 `newClientLocked()`，创建串行；不存在"Creating 但未发布"的可持久状态。因此 P1 要求把 Creating 计入预算，在 XMUX 里没有对应状态——除非重做创建模型，那属于被排除的全面预算重构。

**本轮不做**：没有新增 XMUX 预算。现有 inventory 足以报告 entry 的 Active/Idle/Draining/跨代；物理 socket 需要 HTTP transport 层自己报告，当前 `NOT_RUN`。
## 4. HTTP/2 请求体

`newRequest` 把 body 包成 `readCloser` 并交给 `http.Request`；请求由标准 `http.Client`/RoundTripper 执行，按 net/http 契约在 RoundTrip 返回时关闭 `Request.Body`，取消/超时通过 request context 传播，远端关闭由 transport 结束响应体。仓库内没有发现"请求体在取消/超时后仍被持有"的直接证据，本轮**不修改**（无证据的改动会风险大于收益）。

**状态**：`NOT_REPRODUCED / OPEN`。若要闭环，最小实验是：本地 H2 服务端收到 headers 后不读 body 并 RST_STREAM，客户端取消，使用 finalizer/引用计数断言 body 的 `Close` 恰好一次；该实验未在本轮执行（`NOT_RUN`）。

## 5. Owner 报告矩阵

| Owner | 资源 | 状态 |
|---|---|---|
| sing-tun UDP NAT | Entries / Buffer（queued bytes）/ caps | **DONE**（`UDPNat.Stats()`） |
| sing-tun TCP Dispatcher | Accepted / Active | **DONE**（`TCPDispatcherStats()`） |
| XMUX | 物理连接 Active/Idle/Draining/跨代 | **EXISTS（内部）**；未导出为公共 API |
| AnyTLS | Session 数 | **PARTIAL**：SDK 有 `PendingCreationWaiters`，无 Session 计数；本轮未改（观察项） |
| QUIC（HY2/TUIC/v2rayquic） | 物理连接 / Stream | **NOT_RUN**：quic-go 有 `ConnectionStats`，仓内未聚合成 owner 报告 |
| Mieru | Underlay / Session | **UNAVAILABLE**：SDK 内部，未改加密实现 |
| MASQUE | Tunnel / Capsule | **NOT_RUN**：仓内无 tunnel 计数 |
| PacketFlow | 队列 | **UNAVAILABLE**：Apple 客户端仓库/NE，本机无法观测 |
| Native Engine（Cronet/Apple TLS） | native 内存 | **UNAVAILABLE**：无 Apple 工具链/真机 |

## 6. Critical + Network Change 组合测试（NOT_RUN）

需要同时可控地注入"内存压力 Critical"与"网络切换"，并统计重复 Reset。现有测试分别覆盖两者（`service/oomkiller` 的压力测试、`route` 的 transition 测试），但没有一个测试同时驱动两者；组合测试需要可控的 network manager 与 reset 计数钩子，超出本轮"不改架构、不加监控平台"的边界。

**最小方案（下一轮）**：在 `route` 的 transition 测试里注入一个 `pressure()` 返回 Critical 的 guard，统计 `ResetNetwork` 调用次数，断言一次切换只产生一次 reset（或明确记录两次来源）；不新增生产抽象。

## 7. 验证

- sing-tun 定向测试：3 个全部 PASS（`raw/flow_cost.txt`）。
- sing-tun 全包：**BLOCKED_BY_ENVIRONMENT**——Windows 上 `stack_go_integration_test.go` 的 raw socket/TUN 用例 `Access is denied`（需要管理员/Npcap），不是代码失败；原始输出在 `raw/full_package_env.txt`，未记为 PASS。
- 主仓：pin 到 `v0.9.7-0.20261114041520-6e7483019752`；`go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...` exit 0。
- 本轮无 CI 触发（路径过滤不含 `go.mod`/docs；最近代码 CI 仍是 `43555135120` @ `324a3b011`，success）。
- 未运行：iOS 真机、Apple 构建、XMUX 预算、HTTP/2 body 实验、组合测试——全部明确 `NOT_RUN`/`UNAVAILABLE`，没有用 host 数据替代。

## 8. 交付边界

- 改了什么：sing-tun 两个只读观测 API + 四个 dispatch 点计数 + 三个测量测试 + 主仓 pin + 本文档。
- 没改什么：没有 TUN→Route gate、没有 XMUX 预算、没有改 Mieru/HTTP2/XHTTP/UI、没有 ResourceGuard/OOMKiller 重构。
- 三个预算层的下一层入口：本层证据已足以说明"逻辑流 ≠ 物理连接"，下一轮若做准入，应在此观测之上，而不是再造一套全局连接数。
