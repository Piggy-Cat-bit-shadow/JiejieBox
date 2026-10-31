# NaiveProxy 第一轮改动：专项排查与验收报告

- **日期**：2026-10-20
- **类型**：针对性排查、修复与独立验收；本轮不做第二次架构审计，也不新增性能功能
- **范围**：以第一轮实际变更为准，涵盖 P1（CONNECT 握手与 FailureFacts）、P3（H3→H2 回退、cronet-go 单次拨号 API 与根模块依赖固定）、P9（padding 全零测试）和 P7（文档）。P2 受原生 API 限制未实施（`BLOCKED_BY_NATIVE_API`）；P8 未修改默认值。
- **结论**：`READY_WITH_LIMITATIONS`（见 §8）

---

## 1. 变更范围与版本

```text
MAIN_REPO:                 Piggy-Cat-bit-shadow/sing-box
BRANCH:                    testing
IMPLEMENTATION_BASE_SHA:   11c4c1b27ff87a6ee686c9c1b578a333bbb726d4
IMPLEMENTATION_END_SHA:    d43e03c0f6fab7d6a3824fc0937e74b4d649a4ca
DEBUG_START_SHA:           d43e03c0f6fab7d6a3824fc0937e74b4d649a4ca
CRONET_FORK_OLD -> NEW:    8c68ce8 -> fd2f2582  (branch feat/per-dial-quic, forked FROM 8c68ce8)
NATIVE CHROMIUM:           154.0.8037.49  (运行时 Engine().Version(); Native 二进制未改)
DIRECT CHANGED FILES:
  protocol/naive/failure.go        (new)   P1
  protocol/naive/failure_test.go   (new)   P1
  protocol/naive/h3_fallback.go    (new)   P3
  protocol/naive/h3_fallback_test.go(new)  P3
  protocol/naive/outbound.go       (mod)   P1+P3
  protocol/naive/dependency_pin    (none)  pin test unchanged, still passes
  option/naive.go                  (mod)   P3 option
  docs/configuration/{inbound,outbound}/naive.md (mod) P7+P3
  protocol/naive/padding_zero_fill_test.go (new) P9
  go.mod / go.sum / test/go.mod    (mod)   root cronet-go re-pin only
PUBLIC API / JSON / TAGS / PINS:
  new JSON option: quic_fallback_to_h2 (outbound; default false)
  new Go API in dependency: (*NaiveClient).DialEarlyWithQUIC
  build tags unchanged; all/lib modules unchanged
CONCURRENT OTHER WORK:     clients/apple, clients/desktop submodule pointers (not this round)
BASELINE:                  第一轮报告 §5/§7/§10 + 审计报告 §7/§12
```

### 1.1 本轮排查与修复

| 提交 | 内容 |
|---|---|
| 本报告之前的 `0ef38b8ff` | 第一轮 P3（被审对象） |
| `98387b223` | 本轮修复：`h3ProbeContext` 预算切分 + 5 组对抗测试 + fake conn 写入计数 |
| 本报告所在提交 | 补跑 `-race`（含 64 并发 verdict）、重新生成 `docs/schema.json`、本报告 |

---

## 2. 边界场景与验证结果

> 下表记录实际执行的边界场景，以及明确标记为未测或部分验证的项目。`H*` 对应第二份提示词 §3.2 的编号。

### 2.1 H3→H2 回退（专项 A）

| # | 测试输入 | 预期行为 | 实际观测 | 结论 |
|---|---|---|---|---|
| H1 | 服务端 tcp+udp，`quic:true, quic_fallback_to_h2:true` | 返回 H3，**不尝试 H2** | 日志 `response received, protocol: quic/1+spdy/3, status: 200`，无 retry 行 | PASS |
| H2 | 服务端 tcp-only（UDP 端口无监听） | 一次有界等待后 H2 成功；窗口内后续直接 H2 | 第 1 次 **200 / 4063 ms**，随后 9 次 **200 / 28–35 ms**；retry 日志恰好 1 条 | PASS |
| H4 | H3 握手耗尽**我们自己的**预算（fake 阻塞握手，预算 20 ms） | 本次回退 H2，但**不** arm 窗口 | `TestH3FallbackProbeExpiryIsAmbiguous`：choices `[true,false]`，`available()==true`，H3 流被 Close | PASS（**本轮新修**） |
| H6 | 错误密码（407），`quic:true, fallback` 开 | 不重试、不 arm 窗口、错误透传 | 请求走 H3 得 407（8 ms）；`retry-lines=0`；curl 502；日志含 `naive CONNECT handshake: unexpected response status: 407` | PASS |
| H7 | 其它状态码（fake 403） | 不重试、无 fact | 单测：`naiveHandshakeFailureFacts` 无 fact；`h3TransportFailure` false | PASS |
| H8/H9 | caller 在 probe 中取消 | 不开第二个 dial，返回 caller 错误 | `TestH3FallbackCallerCancelDuringProbeDoesNotRetry`：choices `[true]`，`errors.Is(context.Canceled)` | PASS |
| H10 | 网络切换 | 窗口清零，旧 attempt 不能重臂 | `TestH3FallbackResetOnNetworkChange` + `(*Outbound).InterfaceUpdated` 调 `reset()` | PASS（单元级） |
| H11 | 并行失败不延长窗口 | 一次事件一次决策；64 并发只 arm 一次 | `TestH3FallbackDoesNotExtendOnASecondFailure` + **`TestH3FallbackConcurrentFailuresArmOnce`（64 goroutine，`-race` ×3）** | PASS |
| H12/H13 | 旧失败晚于新成功 / 旧成功晚于新失败 | 只接受较新 attempt | `TestH3FallbackIgnoresOutOfOrderOutcomes`（两个方向） | PASS |
| H14 | 窗口到期 | 允许再试 H3 | `TestH3FallbackWindowExpires`（20 ms 窗口） | PASS |
| H15 | Close/teardown 与 dial 并发 | 不记 Path、不重试 | `TestH3FallbackTransportFailureMatrix` 的 teardown/cancel 行 | PASS |
| H17 | 已写应用字节后失败 | **绝不**重试 | 单元：回退发生在 `DialContext` 内，`fakeNaiveConn.writes==0`；真实链路：`route/conn.go` 的 cached 首包在 DialContext 返回后才写 | PASS（结构保证 + 测试） |
| H18 | Alt-Svc 让非 forced 请求仍走 QUIC | 未见 H2 不得宣称回退成功 | **未测**：本地服务端不发 Alt-Svc；cronet-go#11 已把风险写进 `DialEarlyWithQUIC` 文档 | `NOT_RUN` |
| H19 | H2 也失败 | 返回真实失败组合，不无限重试 | `TestH3FallbackBothTransportsFail`：返回 H2 的错误；真实链路（无服务端）502 | PASS |
| H20 | 同 Engine H3→H2→H3 | 不会遗留错误的失效状态 | **部分**：同 Engine 内 H3 失败→H2 成功已实测；H2→H3 复苏只有单元级（进程级需要可控 UDP 开关） | `PARTIAL` |

### 2.2 FailureFacts 与真实消费者（专项 B）

| 检查项 | 结果 |
|---|---|
| 把 facts 只包在 `DialEarly` 的返回上（第一轮前的形态） | 这是本轮修复的缺陷：此前没有调用 `HandshakeContext`（审计 §3.1），修复后才能将 407 识别为错误 |
| 错误密码时 group 是否看到 fact | 端到端日志：`open connection to ... using outbound/naive[n]: naive CONNECT handshake: unexpected response status: 407`；`RetryThisFlow`/`PenalizeMemberGlobally` 的真实 fixture `TestFailureFactsDriveEachPolicyWithItsOwnAnswer` 覆盖 `proxy_handshake_fact`（retry=true, penalty=false） |
| 407 被标 Path | 单测矩阵 + `h3TransportFailure` 矩阵均显式排除；`PenalizeMemberGlobally` 对 `{Handshake,Proxy}` 返回 false |
| 取消/teardown 被标 Path | `TestNaiveHandshakeFacts` 与 `TestH3FallbackTransportFailureMatrix` 的 cancel/teardown 行 |
| facts 存到共享 Outbound | 未发现共享状态：facts 只通过 `adapter.WrapFailure` 附加到本次错误，不修改 Outbound 字段 |
| 包装破坏 `errors.Is/As` | `TestNaiveHandshakeFactKeepsTheErrorChain`：`errors.Is(err, cronet.NetErrorConnectionRefused)` 仍成立 |
| 日志泄露凭据 | 全链路日志只有 `status: 407`、`protocol:`、错误文本；`proxy-authorization` 从不打印 |

### 2.3 Cross-repo / pin / native（专项 E）

| 检查 | 结果 |
|---|---|
| `go list -m github.com/sagernet/cronet-go` | `Replace` = `Piggy-Cat-bit-shadow/cronet-go v0.0.1-...20261020055023-fd2f25825e4f` |
| 产物二进制内嵌模块 | `go version -m sing-box-client.exe`：`=> ...fd2f2582 h1:vvQIAUyMZM5X...`，hash 与 `go.sum` 一致 |
| `all`/29 个 `lib/*` | 仍指向 `8c68ce8`（Native 二进制未变）；`Engine().Version()` 仍是 `154.0.8037.49` |
| Windows purego 链接 | `go build -tags WINDOWS ./cmd/sing-box` 成功；真实加载 DLL 并建连（本节所有实测都在这个产物上） |
| Linux 服务端 profile | `go build`/`go test` 均通过（不含 `with_naive_outbound`，符合设计） |
| 旧配置兼容 | `quic:false`（默认）→ H2 200；`quic:true`（无 fallback）→ H3 200；二者与第一轮前行为一致（G2） |
| 无效组合 | `quic_fallback_to_h2: true` + `quic:false` → 构造期报错（`sing-box check` 失败的路径已测） |

### 2.4 Native lifetime / engine 清理（专项 D 的最小集）

| 检查 | 结果 |
|---|---|
| 10 次拨号（1 次 H3 失败 + 9 次 H2）后线程/句柄 | threads `24→26→26`，handles `324→351→351`（空闲 4 s 后不变） |
| H3 失败流是否被关闭 | 单测断言 `h3Closed==true`；真实链路 retry 后无重复 stream |
| 是否新增 Engine | 否：同一 `*cronet.NaiveClient` 上运行两次 CONNECT（日志同一 outbound tag、同一进程，无第二个 engine 启动行） |
| buffer pin/unpin | 本轮未改 `bidirectional_conn.go`；`failure.go`/`h3_fallback.go` 不接触 buffer |

---

## 3. 真实新 Bug

### BUG-D1（本轮引入 → 本轮修复）：H3 预算饥饿使回退可能永远失败

- **可复现的旧失败**：`h3ProbeBudget` 不存在时，H3 尝试直接继承 caller 的 deadline。Chromium 的连接尝试预算在 UDP 被拒/黑洞时约 4.5 s（本机实测），于是 caller 给 5 s 预算时，H3 烧掉 4.5 s，H2 只剩 0.5 s —— 在一台 H2 **完全健康**的服务端上，用户拿到的是失败。这不是理论：把 probe 改回 caller ctx 后，`TestH3FallbackProbeExpiryIsAmbiguous` 在 12 s 测试超时里**挂死**（H3 永不放弃，H2 永不发生）。
- **根因**：回退的两个尝试共享同一个预算，而第一个尝试的失败预算由第三方（Chromium）决定，代码假设了"失败很快"。
- **修复**：`h3ProbeContext` 给 H3 一个**可推导**的窗口：`min(10s, caller_remaining/2)`。窗口由我们自己的 bound 触发时判为 **AMBIGUOUS**：本次仍回退 H2，但**不 arm 窗口**（慢响应不是坏路径），下次仍试 H3。
- **red→green**：变异（把 `probeCtx` 换回 `ctx`）→ 超时 FAIL（见上）；修复版 → PASS。
- **延伸检查**：caller deadline 已过期时不尝试 H3、直接走 H2 并返回 caller 错误；窗口只在 Chromium 给出真实传输错误时 arm（H2 语义不受影响）。

### BUG-D2（第一轮引入、本轮以变异复核）：teardown 判定吞掉 QUIC 失败

- **现象**：`cronet.NetError` 的 `Is()` 把 `CONNECTION_CLOSED`/`CONNECTION_RESET` 映射为 `net.ErrClosed`；若先判 `adapter.IsOwnTeardown`，一次死掉的 QUIC 尝试会被当成"我们自己关的"，**静默禁用回退**。
- **修复**（第一轮已含）：先按 NetError 码分类，再做 teardown 判断。
- **本轮复核**：把顺序改回 teardown 优先，`TestH3FallbackTransportFailureMatrix/connection_closed` 与 `/connection_reset` 立刻 FAIL；恢复后 PASS。这是本轮唯一一次由测试抓到实现顺序错误的记录。

### 未发现的新 Bug（列出被证伪的怀疑）

| 怀疑 | 证伪方式 |
|---|---|
| 回退会重放首包 | 回退在 `DialContext` 内部完成；`fakeNaiveConn.writes==0`；cached 首包在 DialContext 返回后写出 |
| 407 会触发 group 重试/惩罚 | 单测 + 真实 407 链路 + group fixture |
| probe 过期会 arm 窗口导致永久 H2 | `TestH3FallbackProbeExpiryIsAmbiguous` 明确断言 `available()` 仍为 true |
| facts 存到共享 Outbound 造成跨目标污染 | 代码只附加到 error；无共享字段 |
| 包装破坏 `errors.Is` | 单测 |
| 新增 Engine 或句柄泄漏 | 真实进程线程/句柄稳定 |
| 旧 attempt 覆盖新结果 | 两个方向的有序性单测 |

---

## 4. P1 / P2 / P3 的真实验收

| 项目 | 状态 | 验收证据 |
|---|---|---|
| **P1 握手 + facts** | 真实验收 | H2 200 / H3 200 / 407→502（错误透传）/ facts 矩阵 14 行 / 真实消费者 fixture / `errors.Is` |
| **P3 回退** | 真实验收（含限制） | H1/H2/H4/H6/H8/H11–H15/H17/H19 已测；H18、H20 部分；预算切分为本轮修复 |
| **P2 DoH** | `NOT_CHANGED` | 第一轮未施工；本轮复核：`quic:false` 下仍出现 `2001:4860:4860::8888:443` 的 stub 连接（与审计一致），不宣称已修 |
| `transport/http` 公共 verdict | `NOT_CHANGED` | 本轮未改；`go test ./transport/http/ ./common/httpclient/` 仍 PASS（回归保护） |

---

## 5. 公共层回归

| 包 | 命令 | 结果 |
|---|---|---|
| `protocol/naive` | `go test -tags OTHERS ./protocol/naive/...` | PASS |
| `protocol/naive`（Windows profile） | `go test -tags WINDOWS ./protocol/naive/` | PASS |
| `protocol/naive`（**race**） | `CGO_ENABLED=1 go test -race -tags WINDOWS ./protocol/naive/` | **PASS**（含 16×200 次混合并发 verdict 访问） |
| `option` | `go test -tags OTHERS ./option/` | PASS |
| `protocol/group` | `go test -tags OTHERS ./protocol/group/` | PASS（含 facts 消费者 fixture） |
| `transport/http` | `go test -tags OTHERS ./transport/http/` | PASS（未改，仅回归） |
| `common/httpclient` | `go test -tags OTHERS ./common/httpclient/` | PASS（未改，仅回归） |
| `adapter` | `go test -tags OTHERS ./adapter/` | PASS |

---

## 6. 性能账本

| 维度 | 第一轮前 | 第一轮后（本机 loopback） | 说明 |
|---|---|---|---|
| 建连新增等待 | 0 | **+0.519 ms 中位数**（`DialEarly` vs `DialEarly+CONNECT 200`，20 轮） | 就是一次代理往返；这是 P1 让 407 可见的代价 |
| H3 正常 | H3 200 | H3 200（8 ms，`quic/1+spdy/3`） | 默认路径不变 |
| H2 正常 | H2 200 | H2 200（8 ms） | 默认路径不变 |
| 回退首次 | 不存在 | 4063 ms 后 200（H3 预算 + H2 成功） | 首次一次性成本 |
| 回退后续 | 不存在 | 28–35 ms（窗口内直接 H2） | 窗口有效 |
| alloc/CPU | — | 本轮只加分类与 mutex（每次 dial 一次），无热路径分配 | 未做 benchstat（无 before 数据），不宣称收益 |

**未测**：120 ms RTT / 1% loss 的真实 A/B（无独立 WAN 端）；macOS/iOS footprint；H3 活跃负载下的 CPU/GB。

---

## 7. NOT_RUN / BLOCKED

| 项 | 状态 | 需要什么 |
|---|---|---|
| H18 Alt-Svc 诱导 QUIC | `NOT_RUN` | 需要广告 Alt-Svc 的第三方 Naive 服务端；本地服务端不发 |
| H20 同 Engine H3→H2→H3（进程级） | `PARTIAL` | 需要可在运行中关闭/恢复服务端 UDP 的 harness |
| `-race` | **PASS（本轮补跑）** | 用 MSYS2 gcc 16.2 + `CGO_ENABLED=1 -tags WINDOWS` 跑通：`go test -race ./protocol/naive/` 全绿，含 64 并发 verdict 与混合并发访问 3 轮 |
| macOS/iOS 构建与 footprint | `NOT_RUN` | 无环境 |
| WAN 性能 A/B | `NOT_RUN` | 无独立试验端 |
| `docs/schema.json` | **已重新生成** | `go run -tags WINDOWS ./cmd/sing-box schema` 与提交版只差新增字段 3 行，已随本报告提交 |

---

## 8. 验收表（G0–G10）

| Gate | 要求 | 证据 | 结论 |
|---|---|---|---|
| G0 | `testing` 正确、并行变更保留、归属清楚 | 起止 SHA、`git status`、提交列表 | PASS |
| G1 | 本轮改动有真实覆盖，Bug 已修 | 14+10+5 组测试；BUG-D1/D2 的变异 red→green | PASS |
| G2 | Naive 旧 H2/H3 默认行为不变 | `quic:false`→H2 200；`quic:true`→H3 200（均为 `protocol:` 实证） | PASS |
| G3 | 回退无误清/误判/重放 | H1/H2/H4/H6/H17 实测与单测 | PASS（H18 未测） |
| G4 | facts 被真实消费者看见且不误重试 | 407 链路 + `TestFailureFactsDriveEachPolicyWithItsOwnAnswer` | PASS |
| G5 | DNS 改动承诺 | `NOT_CHANGED`（P2 未施工），stray 仍在，未宣称修复 | N/A |
| G6 | Native buffer/lifetime/close | 未改 buffer；10 次拨号线程/句柄稳态；H3 流被 Close；**`-race` 全绿** | PASS |
| G7 | 性能无回退、收益可重复 | +0.519 ms 建连（一次往返）；回退首次/后续数据；WAN 未测 | PASS_WITH_LIMITATIONS |
| G8 | 依赖/模块/native/pin 一致 | `go list -m`、`go version -m`、DLL 154、三 profile 构建 | PASS |
| G9 | 修复可回滚、测试不虚报 | 两个修复各自独立提交；SKIP/NOT_RUN 单列 | PASS |
| G10 | 最小上下游回归，无全仓扩散 | 只改 `protocol/naive` + `option` + 文档 + root pin；回归 7 个包 | PASS |

**最终判定：`READY_WITH_LIMITATIONS`** —— 本轮改动在本环境可验证的范围内正确、有回归保护、默认行为不变，`-race` 与 schema 已在本轮补齐；剩余限制只有 WAN/macOS/Alt-Svc 三类真实环境项与 H20 的进程级 UDP 开关，均已列明，不阻塞跨平台验证。

---

## 9. 收尾

- 代码修复、对抗测试与本报告随 `testing` 一起提交并推送；工作树除并行窗口的 submodule 指针外干净。
- 逐项可回滚：BUG-D1 的修复是 `h3ProbeContext` 一个函数 + 调用点；BUG-D2 的修复是分类顺序两行。
- 已把"下一轮/跨平台"剩余项写进 §7，不需要用户批准即可继续由任意窗口接手。
