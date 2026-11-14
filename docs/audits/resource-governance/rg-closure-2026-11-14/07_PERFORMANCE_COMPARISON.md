# 07 性能对照（Performance comparison）

> **本轮没有运行吞吐/延迟基准。** 这一节不把"结构上不该回归"写成"已证明没有回归"。
> 施工指令要求保留正常路径性能；本节给出的是**改动面的结构性论证**与**下一轮应如何测量**。

## 1. 结论先行

| 项 | 状态 |
|---|---|
| iOS 真机吞吐/延迟 | `NEEDS_DEVICE` |
| 主机侧 micro-benchmark（setup 路径） | `UNTESTED`（本轮未建 harness） |
| 稳态数据面（copy / packet batch / splice） | **未修改**（结构性事实，见 §2） |
| 热路径新增分支 | 每 UDP flow **setup 一次**；每共享 QUIC **创建一次**；每 poll 一次 bool 判断 |

## 2. 每个改动对正常路径的实际影响

### 2.1 `route/conn.go`（非 connected UDP setup）

改动只发生在 `NewPacketConnection` 的 else 分支里、三个 listen 调用之前和之后：

```go
ctx = trafficclass.WithClass(ctx, metadata.TrafficClass)   // 一次 context value
releaseDial := m.enterDialGate(ctx, metadata.TrafficClass) // 迁移窗口内才延迟
setupCtx, finishSetup := m.dialSetups.begin(ctx)           // 一次 context + 一个 AfterFunc
... ListenPacket(setupCtx, ...) ...
finishSetup(); releaseDial()                               // 建立或失败即释放
```

- **频率**：每个新建 UDP 流一次，不是每个数据包一次。
- **稳态数据面**：`packetConnectionCopy`、batch writer、splice、growth budget 均未改动。
- **不可取消的代价**：在切换窗口内，`enterDialGate` 与 connected UDP/TCP 分支一致，最多 500 ms 软延迟。
- **语义收益**：切换时不再有"拨号永远挂着"的 goroutine + 首包 + NAT entry。

### 2.2 `service/oomkiller/timer.go`

- `poll` 每次多一个 `sample.footprintUnavailable` 判断（bool 字段，无分配）。
- 正常样本（`usage > 0`）路径与之前完全一致：`sampleFromTotal` 只是把 `memory.Total()` 的返回值放进 struct 并比较 `> 0`。
- 只有采样失败时才走新分支（ARMED/HOLD），这不是性能路径。

### 2.3 `transport/v2rayquic/client.go`

- `offer()` 每次**共享创建**构建一次 `newOfferContext`：`context.WithoutCancel` + `WithCancelCause` + `AfterFunc`，比原先的 `WithCancelCause(c.ctx)` 多一次包装与一个注册（创建返回时 `stop()` 释放）。
- **频率**：每次物理连接创建一次，不是每次 stream dial 一次（`DialContext` → `offer` 命中池直接返回，不构建）。
- `createConnection` 在无 guard 时是**一个 nil 判断**后直接调用 `dialConn`，与改动前等价。
- 有 guard 时多一次 `AdmitCreation` 调用与 permit 释放（创建返回即释放），不在 stream 数据路径上。

## 3. 为什么本轮不做主机基准

1. 被改的三条路径都不是字节搬运路径；用 `ns/op` 测"每 flow setup 多两次 context 分配"得到的数字无法外推到 iOS footprint。
2. 仓库现有的性能证据结构是"设备/回环整链路 + 固定负载"（见 `docs/PERFORMANCE-COMPARISONS.md`），单包 micro-benchmark 容易被误读为端到端结论。
3. 本轮没有 iPhone，任何"iOS 上无可测回归"的说法都会越过证据边界。

## 4. 下一轮建议的测量

| 目标 | 方法 | 通过标准 |
|---|---|---|
| setup 路径开销量级 | 在 `route` 包加 `BenchmarkPacketSetupGate`（stub dialer，`-benchmem`），基线 vs 分支 | 分配增加 ≤ 2 次/flow、耗时增加 < 5 %（同机） |
| iOS 突发 footprint | 真机 NE + 200/500/1000 UDP flow 注入，记录 `timeline` 的 `memoryBytes` / `goroutines` / `connections` | 峰值不再逼近 50 MiB 预算；压力解除后 30 s 内回落 |
| 复用是否被误计费 | 对比 `resourceGuard.admitted` 与真实握手数（协议日志/服务端计数） | 复用期间 `admitted` 不增长 |
| 稳态吞吐 | 现有 loopback harness（AnyTLS/XHTTP/Mieru）基线 vs 分支 | 中位数吞吐在噪声范围内 |

## 5. 历史数据的边界

施工指令 §3 引用的 iOS 诊断（15 MiB → 43.5/47.7 MiB、446/392 goroutines、200 UDP + 29 TCP、201 次 createSession）是**历史诊断环境**的采样，不是本轮结果。本轮没有复现该场景，也没有声称"修复后节省了多少 MiB"。
