# 测试证据（闭环轮）

> 命令在 branch HEAD 的全新 clone 中执行；原始输出在 `raw/regression.txt`（最终树）与 `raw/regression_first_run.txt`（首轮，含被修正的失败）。

## 1. 新增测试

| 改动 | 测试 | 证明 |
|---|---|---|
| H3 准入（httpclient） | `common/httpclient/h3_admission_test.go`: `TestH3CreationIsRefusedBeforeTheDial`、`TestNilGuardKeepsTheOldPath` | 预算饱和时 UDP dial 调用 0 次、返回 `ErrQueueFull`；无 guard 保持旧路径 |
| H3 准入（XHTTP） | `transport/v2rayxhttp/http3_admission_test.go`: `TestH3CreationIsRefusedBeforeTheUDPDial`、`TestH3RefusalIsClassifiedAsLocalRejection` | 同上；拒绝被分类为本地背压；pooled conn 未被发布 |
| DNS 并发 | `dns/router_query_gate_test.go`: `TestDNSQueryGateIsCreatedWithTheDocumentedPlan`、`TestSaturatedDNSGateRefusesBeforeTheClientExchange`、`TestSaturatedDNSGateRefusesTheAsyncExchange` | 256/256/2 s 的 gate；同步/异步饱和时 client 不执行、回调收到错误；完成后 inflight 归零 |
| 会话 coalescing（SDK） | `sing-anytls/coalescing_test.go`（依赖分支） | 50 并发 → 1 次 dial；取消不拆共享创建；失败唤醒全部；Close 释放全部；admission 一次 |
| 会话 coalescing（主仓集成） | `protocol/anytls/coalescing_integration_test.go` | 200 并发生产 outbound 流 → gate `Admitted=1/Released=1`、200/200 成功、物理 accept **恰好 1** 次（用 `PendingCreationWaiters` 等全部附着后放行；`-race` 同样通过） |

## 2. 回归与构建（最终树）

| 范围 | 命令 | 结果 |
|---|---|---|
| AnyTLS | `go test ./protocol/anytls -count=1` | `ok ... 6.8s` |
| DNS | `go test ./dns/... -count=1` | `ok`（dns 8.6 s + 全部子包） |
| H3 客户端 | `go test -tags with_quic ./common/httpclient -count=1` | `ok ... 0.8s` |
| XHTTP | `go test -tags "with_xhttp,with_quic" ./transport/v2rayxhttp -count=1` | `ok ... 20.6s` |
| 全仓构建 | `go build -tags with_quic,...,with_xhttp,... ./...`（`DEFAULT_BUILD_TAGS_OTHERS`） | 退出码 0 |
| 主仓竞态 | `go test -race ./common/httpclient ./dns ./protocol/anytls` | 通过（anytls 含 200 流 burst 的精确断言） |
| SDK 单元 + 竞态 | `go test ./...` / `go test -race ./...`（sing-anytls） | 均通过 |

**首轮的一次环境失败**：`common/httpclient` 的 `TestTransportFactoryServesFromH2WhenUDPIsSilent` 在首轮因 Windows `bind: access permissions`（UDP 端口分配）失败，重跑通过；与本轮改动无关，保留在 `raw/regression_first_run.txt`，不记为通过。

## 3. 既有测试的基线更新（不是放宽）

| 测试 | 旧断言 | 新断言 | 原因 |
|---|---|---|---|
| `TestSessionBurstWithoutGuardReproducesTheAmplification` → `TestSessionBurstSharesOneSessionWithoutGuard` | 200 并发 → 峰值创建 ≈200 | 200 并发 → **1** 次创建、**1** 个物理连接 | coalescing 消除了它要复现的放大 |
| `TestSessionBurstWithGuardBoundsConcurrentCreation` → `TestSessionBurstWithGuardChargesOneCreation` | guard 限制并发创建 ≤8 | 生产 hook 只收 **1** 次费；`MaxInflight=1`；200/200 成功 | 同上；改用真实 hook 而非旧的 whole-dial wrapper |
| `TestTLSHandshakeBurstAmplificationIsBounded` → `TestTLSHandshakeBurstIsBoundedBySessionCoalescing` | 未 guard 峰值 >32；guard ≤8；guard 峰值堆更低 | 无 guard 与有 guard 均 **1** 次握手/1 条连接 | 放大被消除而不是被节流 |

## 4. 过程中抓到并修复的缺陷（自证）

**D-closure-1：共享创建丢掉了调用者 deadline。**
首版用 `context.WithoutCancel(首个调用者)` 同时丢掉了 deadline 与取消。生产 `DialOut` 的 TLS 握手靠 ctx deadline 收敛；对静默服务端，创建会无限挂起（`TestTLSHandshakeBurstAmplificationIsBounded` 因此 2 分钟超时）。修复：`d3e6d49` 保留首个调用者 deadline；无 deadline 时 30 s 上限；取消仍分离。修复后 AnyTLS 全包 6.8 s 通过。

这条记录是必须的：它说明 coalescing 的"保护等待者"不能以移除所有上界为代价；也说明旧测试确实在守一个真实契约。

## 5. 未运行 / 设备项

| 项 | 原因 |
|---|---|
| 真实 iOS NE 上的 H3/MASQUE 创建与 footprint | `NEEDS_DEVICE` |
| 真实 TUN 下 NAT 首包字节 | `NEEDS_DEVICE`；本机无 TUN |
| Xray H3 互通 | 未改动协议线格式；只在创建 seam 外层加入准入 |

## 6. 依赖分支

`github.com/Piggy-Cat-bit-shadow/sing-anytls` → `work/creation-coalescing-2026-11-14`
- `8df0170e72119839233d2726942b4e492eff2721` client: share one session creation
- `d3e6d499914be45a45825e3ccc89e364b763e5ca` client: keep the caller deadline
- `8473492a5afe56222de4da366c3859b560127842` client: expose the pending-creation waiter gauge

主仓 `go.mod`/`go.sum` pin `v0.0.0-20261114030206-8473492a5afe`。
