# 04 生命周期矩阵（Lifecycle matrix）

> 阶段划分按施工指令 §4.3：入口成本 / 昂贵初始化 / 持续占用 / 控制面 / 释放与回收。
> 重点回答："资源何时创建、谁拥有、谁能取消、压力解除后如何回落"。

## 1. UDP 逻辑流：从入口到释放

```text
入口（协议选择前）
  TUN 包 → UDPNat.getOrCreate
    ├─ NAT entry（LRU 容量内）
    ├─ packetChan(cap 64) + 字节预留
    ├─ 每会话 goroutine（handler.NewPacketConnectionEx）
    ├─ 首包 snapshot/IncRef（直到 dial 完成）
    └─ flow judgement（adapter.JudgeFlow，在 LRU 构造器内、持 shard 锁）

昂贵初始化（协议创建）
  route.NewPacketConnection
    ├─ [本轮补齐] trafficclass + dial gate + setup gate
    ├─ outbound.ListenPacket / ListenPacketWithDestination
    │    ├─ guard.AdmitCreation（创建前，创建后释放）
    │    ├─ 新建 TCP+TLS / QUIC / UoT / SDK underlay
    │    └─ 失败：CloseOnHandshakeFailure → 关闭入站侧
    └─ 成功：N.ReportPacketConnHandshakeSuccess

持续占用
  route 连接登记（受管节点)
  两个 copy goroutine + trafficsched flow
  copy growth budget（每个增长方向一个 permit）
  udpTimeout（默认 5 min）与 idle 观测

释放
  LRU 淘汰 / timeout / NAT Close / 网络切换 drain / CloseAll / 配置 reload
    ├─ 取消在途 setup（setup gate 代次）
    ├─ 关闭受管连接（处置预算 64，单 worker 串行）
    └─ 关闭 NAT entry 与 cleanup 队列
```

证据：`<sing-tun>/udp_nat.go:383-455`、`route/conn.go:749-917`、`route/conn_reclaim.go:330-406`、`route/dial_setup.go:89-128`、`route/dns.go:91`。

## 2. 阶段 × 层 矩阵

| 阶段 | TUN/UDP NAT | route | 协议适配 | 物理传输 | 回收 |
|---|---|---|---|---|---|
| **入口** | NAT entry、channel、goroutine、首包引用、flow judgement | 构造 `InboundContext`，解析 class | — | — | — |
| **昂贵初始化** | — | setup gate、dial gate、连接登记票据 | guard permit；session 池决定"复用 or 创建" | TCP+TLS / QUIC / HTTP / SDK underlay | 失败关闭、许可释放 |
| **持续占用** | NAT map 条目、字节账本 | 受管节点、copy goroutine、增长 permit | live session/stream 表 | socket、crypto 状态、流控 | idle 缓存、idle session |
| **控制面** | DNS hijack（独立 256 上限） | dial governor（3 s / 24） | URLTest、测速、provider build（`buildgate` 2/8） | 测速连接 | OOMKiller idle 机会 |
| **释放/回收** | LRU 淘汰、timeout、cleanup goroutine | drain/CloseAll、处置预算与串行 close | Close/CloseIdleConnections、shed hook | 关闭 socket/QUIC | OOMKiller 执行器、guard shed |

## 3. 关键交叉场景的实际语义

| 场景 | 当前行为 | 证据 | 等级 |
|---|---|---|---|
| **网络切换时 stream 在拨号** | setup gate 按代次取消调用方 setup；已建立连接不受影响 | `route/dial_setup.go:130-180`、`TestNetworkTransitionCancelsAnInFlightBlackholedDial` | `VERIFIED_UNIT` |
| **网络切换时 UDP setup 在拨号** | **本轮修复**：非 connected UDP 现在同样被 setup gate 取消 | `route/conn.go:827-838`、`TestNetworkTransitionCancelsAnInFlightPacketSetup` | `VERIFIED_UNIT` |
| **网络切换时 UDP 已建立** | 不在 setup 权限内；由 drain/idle 策略处理，setup 取消不会关闭 socket | `TestEstablishedPacketFlowSurvivesATransition` | `VERIFIED_UNIT` |
| **取消风暴 + 等待准入** | waiter 有界、可取消；granted-but-abandoned 会把槽位转交 | `common/resourceguard/gate.go:241-296` | `VERIFIED_UNIT` |
| **Critical + reload + close 并发** | guard 的 observe/apply 有版本号；stop 等 poll 的 barrier | `guard_publish_race_test.go`、`poll_stop_race_test.go` | `VERIFIED_UNIT` |
| **压力解除** | 上升立即、下降需 `DemoteAfter` 2 s 连续低压；grow budget 已有 permit 不回收 | `guard.go:326-369`、`growth.go:67-76` | `VERIFIED_UNIT` |
| **footprint 采样失败** | **本轮修复**：保持/升到 ARMED，不清除已测得的 Triggered | `service/oomkiller/timer.go:1191-1203`、`:1414-1433` | `VERIFIED_UNIT` |
| **Darwin 压力通知** | 排队执行 cache flush + GC，并发布 Critical；不 reset 网络 | `timer.go:943-971`、`:1157-1173` | `VERIFIED_SOURCE` |
| **阈值危机** | 重新读取样本并确认仍在 armed 线上才做网络 reset | `timer.go:973-1024` | `VERIFIED_UNIT` |
| **NE 停止** | Stop 取消 poll barrier、停止执行器、发布最终状态 | `timer.go`（stop 路径）、`triggered_stop_test.go` | `VERIFIED_UNIT` |
| **iOS Jetsam / 真实 NE 停止** | 无设备 | — | `NEEDS_DEVICE` |

## 4. 已确认的重复/竞争点（审计结论）

| 现象 | 结论 | 证据 |
|---|---|---|
| `Guard.runShed` 与 OOMKiller 回收执行器是否重复回收？ | 不是同一批对象：shed hooks 只关闭"空闲 session"；执行器走 idle/crisis 分层。但两者**并行**运行，`releaseAccess` 不覆盖 shed，因此存在并发 trim 的可能 | `guard.go:408-416`、`timer.go:943-1024` |
| `observe/apply` 是否会被陈旧状态覆盖？ | 已被版本号修复；`Stats` 与 `apply` 共用发布锁 | `guard.go:376-400`、`guard_publish_race_test.go` |
| 是否存在第二个压力状态机？ | 压力事实只有 OOMKiller 一个 atomic；但 build gate 复用 guard 的 `Level()`，会驱动同一个 guard 的转移与 shed | `box_buildgate.go:59-67`、`guard.go:284-291` |
| `Permit.Wait()` 是否把持有时间算进等待？ | 是（`acquired` 在 Acquire 入口记录，`Wait()` 用 `time.Since`）。生产代码无调用者，只有测试在 Acquire 后立即调用，因此未污染生产指标 | `common/resourceguard/gate.go:409-413` |
| 每个 UDP flow 几个 goroutine？ | 至少 1 个 NAT goroutine；建立后 route 再起 2 个 copy goroutine（加上 outbound 内部） | `<sing-tun>/udp_nat.go:455`、`route/conn.go:915-916` |

## 5. 生命周期风险清单（按本轮证据）

| 风险 | 状态 | 说明 |
|---|---|---|
| 非 connected UDP setup 不受代次取消 | 已修复 | 本轮提交 1 |
| 采样失败 → 压力事实变成 None | 已修复 | 本轮提交 2 |
| v2rayquic 创建不计费 | 已修复 | 本轮提交 3 |
| UDP NAT 首包引用不在字节账本内 | 未修（`DEFER`） | 依赖仓库行为，影响有限但真实 |
| 8 MiB NAT 字节预算按实例拆分 | 未修（`DEFER`） | 依赖仓库行为 |
| shed 与执行器并发 trim | 未修（`DEFER`） | 未观测到故障；加锁序风险需独立实验 |
| 同键 session 创建未合并 | 未修（`DEFER`） | 收益最大、改动跨 SDK，见 `05` §4 |
| DNS 非 hijack 查询无并发上限 | 未修（`DEFER`） | 需要独立的 DNS 压力实验 |
