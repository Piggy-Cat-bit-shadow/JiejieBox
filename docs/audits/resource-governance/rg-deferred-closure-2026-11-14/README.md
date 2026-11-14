# 遗留方向闭环（rg-deferred-closure-2026-11-14）

> 对象：第一阶段台账 D11–D13 中标记 `DEFERRED` 的四条研究方向
> 分支：`work/resource-governance-closure-2026-11-14`（同一 feature branch）
> 依赖分支：`Piggy-Cat-bit-shadow/sing-anytls` → `work/creation-coalescing-2026-11-14`
> 判定：**3 项 `CLOSED_FIXED`，1 项 `CLOSED_REJECT`（带量化边界与明确设计）**。没有留下第五个 DEFER。

## 闭环台账

| 方向 | 判定 | 落点 | 证据 |
|---|---|---|---|
| 会话创建 coalescing | `CLOSED_FIXED` | `sing-anytls` 8473492（8df0170 共享创建 → d3e6d49 期限修正 → 8473492 waiter gauge；分支已 push）+ 主仓 pin + `protocol/anytls/coalescing_integration_test.go` | 200 并发首流 → **1** 次 admission、0 失败、物理 accept 通常 1 次（`-race` 下调度滞后者可能建第 2–3 条；SDK 确定性测试用 waiter hook 断言恰好 1 条） |
| H3 物理创建准入 | `CLOSED_FIXED` | `common/httpclient`（共享 H3 客户端，覆盖 MASQUE/DoH3/provider）+ `transport/v2rayxhttp` H3 | 饱和时构造器不执行（两个包各一组测试）；官方 HTTP/3 包全绿 |
| DNS 并发 | `CLOSED_FIXED` | `dns.Router` 查询 gate（256 并发 / 256 队列 / 2 s） | 同步与异步 Exchange 在饱和时快速拒绝、客户端不执行；`dns` 全包回归通过 |
| NAT 首包账本 | `CLOSED_REJECT`（带边界与设计） | 不修改生产代码 | 首包归 gvisor `UDPBackWriter.packet` / go stack snapshot 所有，释放点是 `HandshakeSuccess/Failure`；跨两套 stack 与 route handshake 桥接，无 TUN 环境验证；量化上界见 `04_NAT_FIRST_PACKET_DECISION.md` |
| 后台工作在 Critical 让路 | `CLOSED_FIXED` | `resourceguard.BackgroundAllowed` + URLTest 周期轮询 + DNS optimistic refresh | Critical 抑制、Armed 放行；三包测试与 `-race` 通过；CI 43550248961 |
| 准入队列高水位 | `CLOSED_FIXED` | `Gate.maxWaiters` → `Stats.MaxWaiters` | 1 槽 + 3 排队 → 3，排空后保持 |

## 1. 会话创建 coalescing（`CLOSED_FIXED`）

**问题**：`takeIdleSession` 只返回已完成过 stream 的 session；突发首流在每个 goroutine 里都看到空池，各自建连、握手、首写。gate 只限速（32 并发），不减少总量（200 flow ≈ 200 次握手）。

**实现**（依赖仓库：`8df0170e` 共享创建；`d3e6d49` 保留首个调用者 deadline / 无 deadline 时 30 s 上限；`8473492a` 暴露 `PendingCreationWaiters` 诊断 gauge）：
- Client 增加单一在途创建 `pendingCreation` 与客户端生命周期 `creationCtx`；
- `sessionForCreation`：空闲池未命中才进入；首个调用者创建，其余等待同一个结果并在成功后直接 `openStream`；
- 创建使用 `context.WithoutCancel(首个调用者)` + 客户端生命周期：等待者取消不会拆掉共享创建；值（traffic class、HeldPermit）保留；
- 失败：同一错误唤醒全部等待者，不在本层重试；成功但 session 在 waiter 使用前关闭：允许一次新创建；
- `Close` 取消创建生命周期并唤醒所有等待者。

**证据**（依赖仓库 `coalescing_test.go`，全部通过，含 `-race`）：
50 并发 → 1 次 dial；waiter 取消不影响创建；失败唤醒全部且下一次重新创建；Close 释放全部等待者；admission 只调用一次；`-race` 通过。**回归修正**：初版共享创建用 `context.WithoutCancel` 连 deadline 一起丢掉，静默服务端可让创建无限挂起（主仓 TLS burst 测试由此超时）；`d3e6d49` 保留首个调用者 deadline，无 deadline 时 30 s 上限，取消仍然分离。
**主仓集成**：`protocol/anytls/coalescing_integration_test.go`：200 并发生产 outbound 流 → gate `Admitted=1/Released=1`、200/200 成功、物理 accept **恰好 1**。测试通过 `PendingCreationWaiters` 等到 199 个等待者全部附着后才放行创建，因此断言不受调度影响（`-race` 同样通过）。

**pin**：`github.com/sagernet/sing-anytls => github.com/Piggy-Cat-bit-shadow/sing-anytls v0.0.0-20261114030206-8473492a5afe`（`go.sum` 已更新）。

## 2. H3 物理创建准入（`CLOSED_FIXED`）

**问题**：H1/H2 经 `common/tls` 计费，H3 直接 `quic.DialEarlyConn`，完全绕过 gate。XHTTP H3 与 MASQUE H3 都受影响（MASQUE 的 tunnel 客户端由 `common/httpclient` 构建）。

**实现**：
- `common/httpclient`：抽出 `newHTTP3DialFunc`，在 UDP dial + QUIC handshake 外包 `Guard.AdmitCreation`；`NewTransport` 从构造 ctx 取 guard，nil 时保持旧路径（一次 nil 判断）。
- `transport/v2rayxhttp`：同样抽出 `newHTTP3DialFunc` 并在 `NewClient` 取 guard。
- 两个 `!with_quic`/`!with_xhttp` stub 同步签名，三种标签组合都能构建。

**证据**（`common/httpclient/h3_admission_test.go`、`transport/v2rayxhttp/http3_admission_test.go`）：
- 饱和时 `ErrQueueFull`，假 dialer 调用 **0** 次，pooled conn 未被发布；
- 释放后创建一次、permit 归还；
- nil guard 保持旧行为；
- 拒绝仍被 `IsLocalRejection` 分类为本地背压（不会计入节点健康）。

## 3. DNS 并发（`CLOSED_FIXED`）

**问题**：hijack 入口有 256 上限，但 rule 解析、sniff、provider、双栈 A/AAAA 探针都无统一上限；client 每次 exchange 新建 goroutine，双栈查询再乘 2。

**实现**：`dns.Router` 增加 `queryGate = NewBoundedGate(256, 256, 2s)`，在 `Exchange`/`ExchangeAsync` 的 early-return 之后、任何 client 调用之前获取；异步路径用 `sync.Once` 保证恰好一次归还；手工构造的 Router（nil gate）保持旧行为。
**设计取舍**：gate 不读内存压力——DNS 是恢复路径的控制面工作，不应该因为内存压力被额外掐死；它是 goroutine/队列上界，不是政策旋钮。

**证据**（`dns/router_query_gate_test.go`）：gate 按文档 plan 创建；同步饱和 → `ErrQueueFull` 且 client 未执行；异步饱和 → 回调收到 `ErrQueueFull` 且 client 未执行；完成后 inflight 归零。`go test ./dns/...` 全绿。

## 4. NAT 首包（`CLOSED_REJECT`，带边界）

见 `04_NAT_FIRST_PACKET_DECISION.md`。结论：**本轮不做账本**，原因是所有权在两套 stack writer、释放点是 handshake 回调、主仓无 TUN 可验证；量化上界在观测预算内（200 flow ≈ 0.3 MiB，1000 flow ≈ 1.4 MiB，硬上界 `MaxSize × 最大 UDP 报文`）。这是决定，不是延期。

## 5. 测试与构建

见 `05_TEST_EVIDENCE.md` 与 `raw/`。所有改动在 `with_quic`、`with_xhttp`、无标签三种构建形状下编译；受影响包与依赖分支均有测试。
