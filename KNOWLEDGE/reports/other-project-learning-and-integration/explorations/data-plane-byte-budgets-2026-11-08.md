# 数据面字节预算：XHTTP 落地，其余积压点的边界与证据（2026-11-08）

状态：B01 `IMPLEMENTED_VERIFIED`；B02/B03/B05/B06 在本轮以证据关闭（见各节），B04 只做审计。
适用阶段：03。目标 revision：`afdd3b3`。本文是阶段 03 的探索/边界记录，代码改动与验证见 `stages/03.md` 与 `EVIDENCE.md` E18。

## B01 XHTTP packet-up：本轮实际改动

`transport/v2rayxhttp/conn.go` 的 `packetUplink`：

- **容量定义**：`maxPending = clamp(2 × sc_max_each_post_bytes, 64 KiB, 8 MiB)`（原有推导）。
  - `pending` = 已接受、未交给 POST 的逻辑字节；
  - `inFlightBytes` = 已离开队列、仍被一个 POST 持有的切片；
  - `retainedCapacity = cap(pending)` = 数组保留容量，单独报告，不当 footprint。
- **修复**：旧实现在队列为空时接受**整个**超大 Write，队列上限对一次大写入失效。现在一次 Write
  按可用预算分块追加、等 flusher 腾出空间后继续，队列任何时刻不超过上限，同时仍返回完整 `n`，
  不改变调用方对短写的假设。
- **转移/释放时序**：`write` 追加（acceptedBytes += n）→ flusher 取走 `payload`（inFlight=len）→
  POST 成功（postedBytes += len）→ inFlight=0；POST 失败则字节随失败丢失，不记 posted。
- **观测**：`packetUplink.stats()` 快照（pending/in-flight/retained/high-water/accepted/posted），
  由 `packetup_budget_test.go` 断言。**进程级 `byteLedger` 报告未接**：当前没有跨 outbound 的
  owner 聚合入口；04 建驻留账本时再决定聚合点，避免现在造一个只有测试消费者的接口。

## B04 真实消费与窗口返还：审计结论

- XHTTP 的下载侧是 `packetConn.Read` 直接读 response body，没有“先返窗口、数据再入另一队列”的
  预取层；不存在提前返还 window 的代码路径（`VERIFIED_SOURCE`，`conn.go` 的 `Read`）。
- H2/QUIC 连接级流控与单慢 stream 的关系在传输库内部（`transport/http` / quic-go pin），本轮没有
  可证明的 repo 侧接点，未改。
- 结论：`ALREADY_PRESENT_VERIFIED`（XHTTP 路径），库内缓存上限标 `NOT_VERIFIABLE_HERE`，不声称已覆盖。

## B02 TUN/UDP/MASQUE：为什么未接

- TUN 的真实等待/写回在 pinned `sing-tun` 模块；本仓 `protocol/tun` 只是适配层。要接字节预算必须
  改子仓并移动 pin，属于依赖面改动。
- MASQUE DATAGRAM/Capsule 队列在 `transport/masque`，但本轮没有时间做“能力选择阻塞/短等待/丢弃”
  的完整矩阵，且它与 04 的驻留账本共用生命周期。
- 结论：`NOT_ADOPTED_WITH_EVIDENCE`（reason=`DISPROPORTIONATE_REFACTOR`：跨子仓 pin + 多协议语义），
  已确认的局部有界队列不称“无上限”。重开条件：04 驻留账本落地、或 `sing-tun` pin 因其他原因移动。

## B03 relay 按需扩容：现状与未接

- 真实增长点是 `route/conn.go` 的 copy loop：`bufio.CopyWithIncreaseBuffer`，阈值由
  `connectionIncreaseBufferAfter` 按目标 writer 的几何/`adapter.CopyBufferGrowthTuner` 决定。
  它没有“共享增长额度/压力联动/回落”的账。
- 本轮未实施：共享额度需要每个 flow 在 copy 结束时归还（两方向独立），并与压力状态联动；
  没有真实高 RTT/长流基准时给默认值会变成猜配置。
- 结论：`NOT_ADOPTED_WITH_EVIDENCE`（reason=`DISPROPORTIONATE_REFACTOR`），保留现有按 flow 阈值；
  重开条件：有 04 的驻留/回收反馈与可复现高 RTT 长流基准。

## B05 reF1nd CachedPacketConn 首包适配：为什么不移植

- 参考实现的收益（cached→NAT→counter/tracker 形状恢复 batch）已在包内 experiments 记录，但移植点
  在自家 `sing` fork（`sing-tun`/`sing` 包装层），需要改子仓、跑完整包装链矩阵并移动 pin。
- 本轮没有可达的真实服务端与 wrapper 矩阵；仅移植一个 `cache_wait.go` 会把“实验绿”当生产完成。
- 结论：`NOT_ADOPTED_WITH_EVIDENCE`，见 `experiments/udp-wrapper` 历史数据；重开条件：sing fork
  有 pin 窗口 + 单包/batch/connected batch/双向 NAT 矩阵可在本环境跑。

## B06 包装能力矩阵

本轮唯一改动的包装层是 XHTTP `packetConn`（写路径），未新增 wrapper。其能力经既有测试确认：
deadline（`deadline_test.go`）、Read/Write、`Upstream`、batch（`packetup_batch_test.go`）、
half-close/关闭 owner（`packetConn.Close`/uplink close 排空）、partial write（新增分块写测试）。
未新增 `ReaderReplaceable`/headroom 语义，故不声称覆盖。
