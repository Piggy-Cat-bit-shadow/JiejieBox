# 高成本创建准入与 OOM 协作（Resource admission and OOM coordination）

## 范围与当前目标（Scope and target）

本模块对照 `v0.1.39` / `fc7e400243d46a451b2dffbe68cb5a3147c66289` 的源码校准，覆盖 `common/resourceguard`、共享 TLS、AnyTLS、`trafficclass` 与 OOMKiller 接线。证据为 `VERIFIED_SOURCE` / `VERIFIED_GIT`；历史测试与 CI 各自限定在对应提交。本次文档维护没有重新执行压力实验或 iOS 真机测试。

## 最终设计及原因（Current design and rationale）

已有 OOMKiller 在 `policyModeNetworkExtension` 安装进程级 guard，使用其既有 atomic pressure 来源。guard 本身不采样内存，不拥有常驻 goroutine 或 timer。没有 guard 的进程通过 nil-safe `Acquire` 保留原有路径。

准入限制的是**创建阶段的并发**：AnyTLS 拨号在 SDK 决定复用或新建前进入 `dialWithAdmission`，TCP 与 UoT 共用；TLS-based 调用通过 `common/tls.handshakeWithAdmission`。许可在拨号返回后释放，不能包装返回连接、持有到整个会话结束，否则会改变连接数语义并遮蔽 buffer/geometry 等接口。

| 默认状态 | 普通创建额度 | 队列 | 最大等待 |
| --- | ---: | ---: | --- |
| Normal | 32 | 256 | 5 s |
| Armed | 16 | 128 | 2.5 s |
| Critical | 8 | 32 | 100 ms |

默认交互预留 4、降级连续低压观察窗口 2 s、等待老化 1 s。精确边界在 `DefaultNetworkExtensionConfig`、`Config.planFor` 与 `Gate`，表格不是不可改变的硬约束。派生计划必须单调收紧，退化的小 limit 也不能在升压后反而扩大额度。

路由层在 TCP 和 UDP 拨号处写入 `trafficclass.WithClass`；AnyTLS/TLS 在各自入口读取相同类别，interactive/realtime 才使用预留。不能从“存在预留字段”推断调用链已传入类别。

## 演进与来源（Evolution and provenance）

| 演进 | 提交 / 路径 | 保留的原因 |
| --- | --- | --- |
| 首次进程准入 | `45af5b39c`，`e58a1c9fc`，`e501b8068` | 从真实 iOS burst 诊断扩展到昂贵创建前的限制 |
| 评审修复与边界保护 | `f621ba433`、`badb7bf35` | 交互预留、队列、压力迟滞与账本不能仅在顺序调用时成立 |
| 共享 TLS 与类别传播 | `ab24a1086`、`72e566305`、`3ca4e23b7` | 不在每个 TLS 协议复制一套策略；两入口类别映射一致 |
| 多跳只持一份许可 | `688184eb8`、`3c0c780c2` | 外层等内层、内层等外层许可会自阻塞，AnyTLS 与 TLS 都需识别标记 |
| 有版本的压力发布 | `882f6047d` | `observe` 与 `apply` 间可能被调度打断，陈旧结果不能覆盖新计划或重复 shed |
| OOM 诊断 | `2eed08a4d`、`service/oomkiller/recorder.go` | 在既有文档追加准入计数，保留原有字段与无注册快路径 |

来源类型：主仓准入实现为 `OWN`；依赖 SDK 的协议和会话复用为继承的外部行为，不能归入 guard 原创协议能力。

## 必须保留的约束（Hard invariants）

1. 同一逻辑拨号经过 TLS、AnyTLS、detour 或再次进入已有入口时，最多持有一份准入许可；标记必须传给真正执行内层拨号的 Context。
2. 成功、失败、取消、排队超时与关闭路径均应结清许可和等待者；计数与 gate 状态必须能解释。
3. 旧观察不得发布比最新状态更陈旧的计划。状态版本与计划在同一发布锁下验证，不能只比较枚举值。
4. 同一 Critical episode 不因 Stats/Level 读取或陈旧 apply 再触发破坏性回收；恢复后再次 Critical 属于新事件。
5. guard 不新建第二套压力采样和重型回收执行器；昂贵回收保持已有 OOMKiller 所有权。
6. 限制创建不能靠隐藏返回连接的能力接口来限制存量；非 NE 不能无意进入移动端严格配额。

## 快速失败速率与本地拒绝分类（2026-11-08，阶段 02）

`common/resourceguard/rate.go` 在既有 `Guard` 内增加第二条、独立的创建政策：连续快速失败后对
**新普通创建**施加有界退避，避免“释放后立刻重试”的高速循环绕过并发上限。

- 触发：同一 seam 连续 `RateLimitAfter`（默认 4）次失败且耗时 `< FastFailWindow`（默认 250ms）。
- 退避：`RateLimitBase` 100ms 指数增长到 `RateLimitMax` 2s，每窗口 ±20% 抖动；成功清零并重置级别。
- 交互类别（`ClassInteractive`）不等待窗口；等待可被 ctx 取消；窗口超过调用方剩余预算时返回
  `ErrRateLimited`，不制造注定超时的等待。
- 无窗口热路径只做一次原子读（`rateUntil`），主机 A/B 基准未测到回归。
- 观测：`GuardStats.Rate` 与报告 `resourceGuard` 的 `rate*` 字段；与容量计数分开。

`resourceguard.IsLocalRejection` 覆盖 `ErrQueueFull`/`ErrWaitTimeout`/`ErrRateLimited`/
`context.Canceled`/`context.DeadlineExceeded`。Mieru 的 fallback 分类在 SDK 把 underlay 错误扁平化成
文本后，靠 `dialObservation` 里保存的带类型错误恢复为 Local，不判节点死亡、不切换传输。

Mieru 的真实 `newUnderlay` 接点（`serverDialer.DialContext/ListenPacket`）现在接同一 `Guard`：
许可在 socket 创建前取得、返回即释放；`HeldPermit` 只标记本 seam 准入的 dial。Mieru 的 Mux/加密
session 在 underlay 之上，尚未覆盖。

TLS/AnyTLS 的 `HeldPermit` 作用域有测试固定（`common/tls/marker_scope_test.go`、
`protocol/anytls/marker_scope_test.go`）：调用方 ctx 不残留标记、第二次 dial 重新领取、嵌套只领一份。

## 验证与重查（Verification）

```sh
go test ./common/resourceguard ./common/trafficclass ./protocol/anytls ./service/oomkiller -count=1
go test ./common/tls -run 'HandshakeWithAdmission|ChainedDial|HandshakeMayUseTheReserve|BothSeams' -count=1
# Linux + 可用 cgo 工具链；先按仓库当前 CI 标签核对依赖
go test -race ./common/resourceguard ./common/trafficclass ./protocol/anytls -count=1
```

关键文件：`guard_publish_race_test.go`、`gate_reserve_test.go`、`held.go`、`handshake_admission_test.go`、`handshake_class_test.go`、AnyTLS admission/nested/burst/stall/soak 测试。`.github/workflows/resource-guard.yml` 定义专项门禁，实际运行需按目标 SHA 查 CI，不以工作流存在证明测试已执行。

## 已知限制与 AI 注意事项（Limits and AI cautions）

- AnyTLS 当前在确认 idle session 复用前领取许可，复用也可能排队。简单加非原子的 `HasIdleSession` 不能解决竞争，接管 SDK 需要独立证据与维护决定。
- 此机制没有为所有独立 QUIC/Cronet/Mieru 客户端的新连接提供统一预算；“每 outbound 通常复用”不能证明多节点重连风暴安全。
- 不将 UDP NAT 条目有上限等同于 iOS footprint 有保证；依赖 `sing-tun@8f65451175c0` 的 200/2000 流测试与设备 Jetsam 是不同层级。
- 默认 32 等参数是当前策略，不是协议常量。真实 iOS 诊断中的 201 个创建不能直接换算成修复后节省多少 MiB。
- `Stats` 是观察入口，不能悄悄成为额外回收循环。不要用 sleep 修复 observe/apply 的因果次序。

历史材料：[初次设计](../../docs/fork/ios-resource-guard.md)、[协议覆盖矩阵](../../docs/fork/resourceguard-protocol-coverage.md)、[红队及版本发布修复](../../docs/fork/resourceguard-adversarial-debug.md)。这些报告中的“当时未覆盖”须结合本模块与现有依赖测试核对，不应机械重做已合入修复。
