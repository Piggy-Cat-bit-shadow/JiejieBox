# 跨协议资源预算闭环：第一层观测与决策（2026-11-14）

> 目标不是让所有协议共用一个连接数，而是把三层预算分开：
> **逻辑流（TUN/NAT/Dispatcher）→ 物理创建（ResourceGuard）→ 稳态资源（Session/Stream/Pool）**。
> 本轮按施工边界只做第一层的**观测与测量**，并给出"是否需要在 TUN→Route 加逻辑流准入"的决策；没有新增预算抽象，也没有改 XMUX 预算、Mieru、UI 或长期入口架构。

## 0. 结论

| 项 | 结果 |
|---|---|
| sing-tun UDP NAT 观测 | **DONE**：`UDPNat.Stats()`：entries / max / sessionsCreated / queuedBytes / caps / drops |
| sing-tun TCP Dispatcher 观测 | **DONE**：`TCPDispatcherStats()`：accepted / active；四个 dispatch 点统一走 `goDispatch` |
| 每条逻辑流消耗 | **MEASURED（host Go runtime）**：1 entry + 1 dispatcher goroutine + 首包（约 1.2 KiB 队列）+ 约 3.3–3.8 KiB heap；1000 流约 3.7 MiB heap + 1.2 MiB 队列 + 1000 goroutine 栈 |
| 是否做 TUN→Route 逻辑流准入 | **NO（本轮）**：证据表明放大存在但未超预算阈值；施工边界明确排除长期入口架构；给出触发条件 |
| XMUX 物理连接 | **INVENTORY EXISTS**：`xmuxPoolStats` 已统计 Generation/Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released；**Creating 不是可持久状态**（创建在 pool 锁内串行）；本轮不新增预算 |
| HTTP/2 请求体 | **NOT_REPRODUCED / OPEN**：现有请求体由 `net/http` 客户端拥有并在 RoundTrip 结束时关闭；没有复现泄漏，不做无证据修改 |
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
udp flows=200  entries=200  sessions=200  queued=240000  goroutines+200  heap=663824 B  3319 B/flow
udp flows=1000 entries=1000 sessions=1000 queued=1200000 goroutines+1000 heap=3707312 B 3707 B/flow
tcp dispatches=1000 active=1000 goroutines+1000 heapDelta=-2054848 B (GC 归还，按 0 记)
```

解读：

- 每条 UDP 逻辑流 = 1 个 NAT entry + 1 个 dispatcher goroutine + 首包（示例 1200 B，仍在 NAT 队列账本内）+ 约 3.3–3.8 KiB Go heap。
- 1000 条流 ≈ 3.7 MiB heap + 1.2 MiB 队列 + 1000 个 goroutine（栈按 2 KiB 起算约 2 MiB）。合计约 7 MiB，仍明显低于 50 MiB 观测预算，但它与物理连接数无关——这正是"三层分开"的理由。
- TCP 侧每个 dispatch 固定 1 个 goroutine；TCP 状态机的内存不在这个计数器里，属于协议/栈的稳态资源。
- 这些是 **host Go runtime** 数字；iOS 上 NAT 是 sing-tun 的 Go 栈，goroutine 与 heap 的形态相同，但 `phys_footprint` 还包含 PacketFlow、native 和 runtime 预留，未测。

## 2. 入口准入决策：本轮不做

**证据**：1000 条逻辑流约 7 MiB（Go 侧），低于 50 MiB 预算；真正放大来自"逻辑流数 ≫ 物理连接数"的协议（AnyTLS/HY2/TUIC/v2rayquic 已复用），而不是入口本身无界。当前入口已有 `MaxSize`（iOS 4096）与 NAT 字节上限（8 MiB）作为硬边界。

**决策**：不在 TUN→Route 增加逻辑流 gate。理由：

1. 施工边界明确排除"sing-tun 长期入口准入架构"；
2. 没有 iOS 真机数据证明入口是主导项；
3. 增加第二道 gate 会改变 UDP 首包语义和失败路径，收益未被证据支持。

**触发条件（任一）**：单次 burst > 2000 新 UDP 流；实测 per-flow（含栈/缓冲）> 8 KiB；iOS 诊断显示 dial 等待期有无法解释的 MiB 级增长；`udp_nat_max` 被提高。

## 3. XMUX：已有 inventory，Creating 不是持久状态

`transport/v2rayxhttp/xmux.go` 的 `xmuxPoolStats` 已经精确统计：`Generation / Pooled / Active / Idle / Draining / DrainingOldGeneration / Opened / Released`，并且 `stats()` 在 pool 锁内执行、先 prune 再快照。

**关键发现**：`get()` 在 `m.access` 锁内调用 `newClientLocked()`，创建是**串行**的；因此在 XMUX 里不存在"Creating 但未发布"的可观测状态——创建要么在锁内完成并立即进入 `clients`，要么返回错误。把 Creating 单独计入预算会是在统计一个不可能持续存在的状态。

**本轮不做**：没有新增 XMUX 预算/重构（施工边界：XMUX 全面资源预算重构不做）。现有 inventory 已能回答 Active/Idle/Draining/跨 Generation 的问题；若后续要做预算，入口应在此 inventory 之上，而不是再造一套计数。

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
