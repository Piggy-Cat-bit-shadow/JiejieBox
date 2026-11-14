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
| 每条逻辑流消耗 | **MEASURED（host Go runtime）**：每条 UDP 流 ≈ 3.2–3.7 KiB HeapAlloc（**已包含** 1.2 KiB 首包）+ 约 6.4–7.5 KiB goroutine 栈（run 间有 GC/栈增长波动）；结构成本 heap−queued ≈ 2.0–2.5 KiB/flow。1000 流实测 heap 3.53 MiB + 栈 7.19 MiB ≈ **10.72 MiB**（不是首版写的 7 MiB） |
| 是否做 TUN→Route 逻辑流准入 | **DECISION（本轮不做）**：修正前的 7 MiB 低估了栈；实测约 10.72 MiB/1000 流、9.4–11.0 KiB/flow 已越过 8 KiB/flow 触发线，因此该问题升级为**下一轮第一优先决策**；本轮施工边界仍排除长期入口架构 |
| XMUX 物理连接 | **PARTIAL**：`xmuxPoolStats` 统计的是 **Pool Entry（transport wrapper）**，不是物理 socket；已统计 Generation/Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released；**Creating 不是可持久状态**；本轮不新增预算 |
| HTTP/2 请求体 | **PARTIAL**：先写测试发现 `readCloser.Close` 是 no-op，已委托 `io.Closer` 并加 3 条测试；H2 取消/RST 端到端 `NOT_RUN` |
| 其它 Owner 报告 | NAT/Dispatcher/XMUX 有；AnyTLS Session、QUIC 物理连接/流、Mieru Underlay、MASQUE Tunnel、PacketFlow、Native Engine 见 §5 |
| Critical + Network Change 组合测试 | **NOT_RUN**（计划见 §6；原因：需要可控的网络切换与压力注入，超出本轮可安全验证范围） |
| 两个已知小缺口 | 上一轮已修复（H3 本地拒绝不污染故障状态、DNS Lookup 走 gate），本轮复核测试仍在 |

## 1. 第一层观测：实现与证据

### 1.1 UDP NAT（sing-tun 分支 `work/flow-observation-2026-11-14`，tip `938b89dcb159`；观测 API 提交 `6e7483019752`）

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

`go test . -run 'TestUDPNatStatsReportLogicalFlows|TestUDPNatPerFlowCost|TestTCPDispatcherPerDispatchCost' -v`
（原始输出：`raw/flow_cost.txt`，对应当前依赖提交）：

```text
udp flows=200  entries=200  sessions=200  queued=240000  goroutines+200
  heap=642704 B (3213 B/flow, includes queued) structural=402704 B (2013 B/flow)
  stack=1277952 B (6389 B/flow) queued=1200 B/flow
udp flows=1000 entries=1000 sessions=1000 queued=1200000 goroutines+1000
  heap=3705168 B (3705 B/flow, includes queued) structural=2505168 B (2505 B/flow)
  stack=7536640 B (7536 B/flow) queued=1200 B/flow
tcp dispatches=1000 active=1000 goroutines+1000 (dispatcher goroutines only; no per-dispatch heap claim)
```

口径（修正了首版的重复计算）：

- `heap` 是 `HeapAlloc` 增量，**已经包含**队列里的首包（1200 B/flow）；首包是 heap 的子集，**不能**再加到 heap 上。
- `structural = heap − queued`，是本层 entry/表结构/map 等剩余结构成本（本次 2.0–2.5 KiB/flow）。
- `stack` 是 `StackInuse` 增量，**不在 HeapAlloc 里**，必须单独计；每条被 park 的 dispatcher goroutine 约 6.4–7.5 KiB 栈（Go 栈按需增长，run 间会波动）。
- 因此每条 UDP 逻辑流本次实测 ≈ **3.2–3.7 KiB heap + 6.4–7.5 KiB stack ≈ 9.4–11.0 KiB**；1000 流 ≈ **10.7 MiB**（heap 3.53 MiB + 栈 7.19 MiB）。数字随 GC/栈增长有 run 间波动，报告不把它压成单一常数。
- TCP 计数器含义：`TCPAccepted` 是累计的 **handler 调用次数（逻辑 dispatch）**；`TCPActive` 是当前 in-flight 的 handler 调用数（≈ 正在处理的 TCP 逻辑流）。它**不是** TCP 栈 flow-table entry，也**不是** socket 数；四个 dispatch 点包含 system/redirect，所以是进程级而非仅 TUN。该测试只测 dispatcher 的 goroutine 开销，不把空闭包 handler 的 heap 当作 per-dispatch 成本。
- 全部是 **host Go runtime**；iOS `phys_footprint` 还含 PacketFlow、native、runtime 预留，未测。

### 1.4 Go Stack UDP 首包快照的生命周期（仅当前生产 Go Stack）

**对象**：`GoPacketConn.snapshot`（`slices.Clone(data.packet)`，堆上的 `[]byte`，用于握手失败时构造 ICMP unreachable），不是引用计数 buffer；"释放"= 丢弃最后一个引用。

**核查前**：

- `HandshakeSuccess`：`w.snapshot = nil`，释放。
- `HandshakeFailure`：取出 snapshot、置 nil，再构造 ICMP；无论 reply 是否构造成功，引用都已释放。
- `Close`：**没有清 snapshot**。它只把 flow 从 `directory.udpFlows` 删除并处理 splice。若 flow 在握手回调之前被关闭，且 `GoPacketConn` 仍被 NAT 侧对象引用，首包会一直保留到该对象不可达（GC 才能回收）。

**这是保留窗口，不是已证明的泄漏**：NAT 释放 conn 后对象不可达，GC 会回收；没有证据显示跨 Generation 或永久持有。但 Close 是显式结束点，不应该依赖 GC 时机。

**本轮最小修复**：`Close` 在 `snapshotAccess` 锁内清空 snapshot，与两个握手回调使用同一把锁；没有改 gVisor（已退休）、没有改 NAT 账本。

**证据**（`raw/go_snapshot.txt`）：

- `TestGoPacketConnSnapshotReleasedOnEveryEnding`：success / failure（含 `BuildICMPError` 拒绝路径）/ Close 三条路径后 `len(snapshot)==0`，重复调用安全；
- `TestGoPacketConnSnapshotCloseRacesHandshake`：Close 与 HandshakeSuccess 并发 100 次，`-race` 通过；
- 仅 Go Stack；gVisor 未触碰。
### 1.5 2000 / 4096 流的突发、稳定持有与集中释放

`go test . -run 'TestUDPNatFlowCostAtScale' -v`（`raw/scale_cost.txt`，branch tip `ab63d63`）：

| 流数 | BURST HeapAlloc / HeapObjects / StackInuse | STABLE（GC 后）HeapAlloc / StackInuse | 集中释放后（3×GC）entries / queued / goroutines / HeapAlloc / HeapObjects / StackInuse |
|---|---|---|---|
| 2000 | 9.63 MiB / 26688 / 15.91 MiB | 8.39 MiB / 15.91 MiB | 0 / 0 / 2（baseline 3）/ 1.33 MiB / 3889 / 5.44 MiB |
| 4096 | 17.68 MiB / 51525 / 34.47 MiB | 16.97 MiB / 34.47 MiB | 0 / 0 / 2（baseline 3）/ 2.44 MiB / 6812 / 9.00 MiB |

解读：

- 2000/4096 规模下每流 ≈ **12.44–13.04 KiB**（heap ≈4.3–4.4 KiB + 栈 ≈8.1–8.8 KiB；按 burst/stable 实测字节换算）；比 1000 流的 ~10.7 KiB 高，原因是 goroutine 栈在高并发下增长到 8 KiB 档位，而不是线性泄漏。
- 4096 流时 host Go 侧 ≈ **52.1 MiB**（burst；stable 51.4 MiB = heap 16.97 + stack 34.47 MiB），已超过 50 MiB 观测预算；这仍然**不是** iOS `phys_footprint`，但说明"逻辑流规模"本身在该数量级已不可忽略。
- 集中释放后：NAT entries、queued bytes、dispatcher goroutine 全部回到 baseline；live objects 从 50k 降到 ~6k；`HeapReleased` 显示页归还。**一次 GC 后 HeapAlloc 仍高是 runtime 缓存/未归还页，不是对象泄漏**；三次 GC 后回落，测试按这个口径记录。
- 没有发现跨 Generation 或释放后残留的 NAT entry / queued buffer。
## 2. 入口准入决策：本轮不做，但触发线已被越过

**修正后的证据**：1000 流 ≈10.7 MiB；**2000/4096 流 ≈24.3/51.4 MiB（stable）**（host Go），4096 流已超过 50 MiB 预算，per-flow ≈12.44–13.04 KiB，**早已越过 8 KiB/flow 触发线**。首版"约 7 MiB、未超阈值"的结论来自两个口径错误：把队列首包重复计入总量、把 goroutine 栈漏掉。

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
## 4. HTTP/2 请求体生命周期（先测试，发现一处真实缺陷）

**先写的测试**（`request_body_lifecycle_test.go`）：

- `TestRequestBodyWrapperClosesTheUnderlyingBody`：统计 Close 次数的 body 包进 `readCloser`（`newRequest` 实际交给 net/http 的类型），断言 wrapper 的 Close 必须传到 body。
- `TestClosingTheRequestBodyUnblocksTheUploadPipe`：Close 后向 `io.Pipe` 写入必须得到 `ErrClosedPipe`。
- `TestRequestBodyWrapperAcceptsANonClosableBody`：非 Closer body 不 panic。

**测试结果**：第一条断言 `closes=0` 失败——`readCloser.Close` 是 no-op，只 `return nil`，根本不调用底层 body。net/http 会在请求结束（成功、取消、超时、对端 RST）时关闭 `Request.Body`，因此这一层此前把"请求生命周期关闭"吞掉了；上传 pipe 只靠 conn teardown 关闭。

**最小修复**：`readCloser.Close` 在 body 实现 `io.Closer` 时委托调用，否则保持 no-op。修复后三条测试 PASS，XHTTP 全包（`with_xhttp,with_quic`）`ok 20.4s`，`-race` 通过。

**仍未验证（NOT_RUN）**：真实 H2 服务端"收到 Headers 后不读 Body"、服务端主动 RST_STREAM、客户端超时/连接关闭三种场景下的端到端 body Close 次数与 goroutine/缓冲区回收。wrapper 层的委托已证明正确；端到端需要 H2 TLS loopback harness，本轮未执行。

**状态**：`HTTP2_BODY = PARTIAL`（wrapper 缺陷已修复并有测试；端到端场景 `NOT_RUN`）。
## 5. Owner 报告矩阵

| Owner | 资源 | 状态 |
|---|---|---|
| sing-tun UDP NAT | Entries / Buffer（queued bytes）/ caps | **FIXED**（`UDPNat.Stats()`） |
| sing-tun TCP Dispatcher | Accepted / Active | **FIXED**（`TCPDispatcherStats()`） |
| sing-tun Go Stack 首包 | snapshot 释放（success/failure/Close） | **FIXED**（Close 补清空；两条测试 + `-race`） |
| XMUX | Pool Entry（transport wrapper）的 Active/Idle/Draining/跨代；物理 socket 不在本层 | **PARTIAL**；内部 inventory，未导出公共 API |
| AnyTLS | Session 数 | **PARTIAL**：SDK 有 `PendingCreationWaiters`，无 Session 计数；本轮未改（观察项） |
| QUIC（HY2/TUIC/v2rayquic） | 物理连接 / Stream | **NOT_RUN**：quic-go 有 `ConnectionStats`，仓内未聚合成 owner 报告 |
| Mieru | Underlay / Session | **UNAVAILABLE**：SDK 内部，未改加密实现 |
| MASQUE | Tunnel / Capsule | **NOT_RUN**：仓内无 tunnel 计数 |
| PacketFlow | 队列 | **UNAVAILABLE**：Apple 客户端仓库/NE，本机无法观测 |
| Native Engine（Cronet/Apple TLS） | native 内存 | **UNAVAILABLE**：无 Apple 工具链/真机 |

## 6. Critical + Network Change 组合测试（NOT_RUN）

需要同时可控地注入"内存压力 Critical"与"网络切换"，并统计重复 Reset。现有测试分别覆盖两者（`service/oomkiller` 的压力测试、`route` 的 transition 测试），但没有一个测试同时驱动两者；组合测试需要可控的 network manager 与 reset 计数钩子，超出本轮"不改架构、不加监控平台"的边界。

**最小方案（下一轮）**：在 `route` 的 transition 测试里注入一个 `pressure()` 返回 Critical 的 guard，统计 `ResetNetwork` 调用次数，断言一次切换只产生一次 reset（或明确记录两次来源）；不新增生产抽象。

## 7. 版本与证据对应

| 对象 | 值 |
|---|---|
| 主仓代码 HEAD（本报告测量时） | `fd5eec54b3d54b43486e4b1eef3e19e35f63aa89` |
| sing-tun 分支 | `work/flow-observation-2026-11-14` |
| sing-tun 观测提交 | `6e7483019752f4fc3d37fafc97af80bcb3eedc6d`（API + 计数） |
| sing-tun 口径修正提交 | `b253963c99275edb29639472065e777f44a3b2a0`（heap/stack/TCP 语义） |
| sing-tun 首包修复提交 | `938b89dcb159ff8bf55b2c24e832f02374241e33`（Close 清 snapshot） |
| sing-tun 规模测试提交（仅测试） | `ab63d63ac306ec315d5a89e8057d5f6ea0157fa2`（2000/4096 流） |
| 主仓 pin | `v0.9.7-0.20261114050708-938b89dcb159` |
| pseudo-version 时间戳 | 2026-11-14T05:07:08Z = 提交时间 13:07:08+08:00 |
| flow cost 原始日志 | `raw/flow_cost.txt`（在 938b89d 树上执行） |
| snapshot 原始日志 | `raw/go_snapshot.txt`（在 938b89d 树上执行） |

## 8. 验证

- sing-tun 定向测试：3 个流成本测试 + 2 个 Go Stack 首包快照测试 + 1 个 2000/4096 规模测试全部 PASS（`raw/flow_cost.txt`、`raw/go_snapshot.txt`、`raw/scale_cost.txt`）。
- `-race` 原始输出：Go Stack snapshot `raw/go_snapshot_race.txt`（全量 verbose）；XHTTP 全包 `raw/xhttp_race.txt`（986 行，`ok ... 21.956s`）。报告里的 PASS 均可在这些日志中复核。
- sing-tun 全包：**BLOCKED_BY_ENVIRONMENT**——Windows 上 `stack_go_integration_test.go` 的 raw socket/TUN 用例 `Access is denied`（需要管理员/Npcap），不是代码失败；原始输出在 `raw/full_package_env.txt`，未记为 PASS。
- 主仓：pin 到 `v0.9.7-0.20261114050708-938b89dcb159`；`go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...` exit 0。
- 本轮无 CI 触发（路径过滤不含 `go.mod`/docs；最近代码 CI 仍是 `43555135120` @ `324a3b011`，success）。
- 未运行：iOS 真机、Apple 构建、XMUX 预算、H2 端到端 body 实验、组合测试——全部明确 `NOT_RUN`/`UNAVAILABLE`，没有用 host 数据替代。

## 9. ExchangeAsync 的补充说明（非重写）

`Router.ExchangeAsync` 的 admission 是**同步**的：调用可能先在 query gate 队列等待（最多 gate 的 wait budget）；拒绝时，或 client 同步完成时，回调会在函数返回前执行。此语义已在 `dns/router.go` 的 doc comment 写明，并由 `TestExchangeAsyncRefusalCallsBackBeforeReturning` 与 `TestExchangeAsyncMayCallBackBeforeReturning` 钉住；本轮只补说明，没有改行为、没有重写。

## 10. 交付边界

- 改了什么：sing-tun 两个只读观测 API + 四个 dispatch 点计数 + 三个测量测试 + 主仓 pin + 本文档。
- 没改什么：没有 TUN→Route gate、没有 XMUX 预算、没有改 Mieru、没有改 HTTP/2 协议或 XHTTP 结构（只修了请求体包装的 Close 委托）、没有 UI、没有 ResourceGuard/OOMKiller 重构。
- 三个预算层的下一层入口：本层证据已足以说明"逻辑流 ≠ 物理连接"，下一轮若做准入，应在此观测之上，而不是再造一套全局连接数。
