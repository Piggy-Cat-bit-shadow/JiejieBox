# JiejieBox 并发安全专项审计报告

- **日期**：2026-11-13
- **范围**：主仓库 `testing`（v0.1.44 → v0.1.45，含 v0.1.46 后续改动）
- **审计目标**：以 `dual_stack_recovery.go` 的并发 Map 崩溃为切入点，排查 0.1.45 大规模更新引入的同类并发缺陷，优先处理可能杀死 iOS Network Extension 进程的问题
- **结论**：已确认并修复 **6 类**并发/生命周期缺陷（1 Critical、2 High、3 Medium）；另有 **9 项**已定位但未修复的残余风险，均已给出触发窗口与建议。0.1.45 中高风险新增模块的 `-race` 基线通过。

## 1. 基线与范围

| 项目 | 值 |
| --- | --- |
| v0.1.44 | `3b6ec64f7145a2add99656d47caaa4f22a1cc478` |
| v0.1.45 | `97091ce3637e68533c060ebac4ff9e309e512f25` |
| v0.1.46 | `c795bb5cbbcd9b6a4d2d2f18e854b629915335f5` |
| 审计起点 | `testing` @ `9ee6e36bc7b`（第一项修复已在此提交） |
| 最终 HEAD | `512615444f8`（本轮 4 个审计修复提交；前置 P0 修复为 `9ee6e36bc7b`） |

`v0.1.44..v0.1.45` 共 **818** 个提交、**762** 个 Go 文件、**+125,795 / -2,524** 行；其中非测试生产文件 **273** 个包含新增并发信号（goroutine、锁、channel、callback、map 等）。`v0.1.45..v0.1.46` 为 **14** 个提交，6 个生产文件，集中在 TrustTunnel H3→H2 回退。

子仓 gitlink 在同一窗口变化：

- `clients/android`：`c783710` → `9816e00`
- `clients/apple`：`e328310` → `aa8e16a`
- `clients/desktop`：`41722b0` → `a658231`

依赖 pin 同步更新（`sing`、`quic-go`、`sing-quic`、`sing-tun`、`wireguard-go`、`tailscale` 等）；本次只对与并发结论直接相关的 pinned SDK 行为（`sing-quic` 的 `Client.CloseWithError`/offer 生命周期）做了源码核对，**依赖仓库全量审计未执行**。

### 方法

1. 用 git diff 确定 0.1.45/0.1.46 的真实生产改动，而不是按提交数推断。
2. 对每个可疑路径沿 **goroutine 创建 → 回调执行 → 共享状态访问 → 取消/关闭** 追踪生命周期；既查未加锁 Map，也查已有锁/Atomic 的作用域、锁序和 TOCTOU。
3. 本机具备 cgo/race 条件（MinGW-w64 gcc + Go 1.25.5），对高风险包执行 `go test -race`；对确认缺陷用 barrier/确定性交错测试做红→绿验证。
4. 全量 `go vet`（changed package trees）作为 `copylocks`/`lostcancel`/`loopclosure` 类问题的补充。

## 2. 已修复缺陷（按严重度）

### P0 — 恢复路径 `seen` Map 并发写（已修复）

- **位置**：`common/dialer/dual_stack_recovery.go`（`deliver` 的 `seen` 读/判/写与 batch 发布）
- **引入版本**：0.1.45 窗口（该文件在该窗口新增 291 行；`LookupFamilies` 并发发布语义）
- **触发**：DNS 流式路由器在两个 goroutine 中发布 A/AAAA 结果；两个 family 同时应答时，`deliver` 并发访问 `map[netip.Addr]recoveredStamp`。
- **根因**：代码注释错误地假设 `seen` 只被“lookup goroutine”访问；`adapter.DNSDualStackRouter` 明确允许 `publish` 从不同 goroutine 调用。IPv4 与 IPv4-mapped IPv6 的 canonical 去重进一步扩大了同一 Map 的写窗口。
- **影响**：Go runtime `fatal error: concurrent map writes`，iOS Network Extension 进程被终止。
- **证据**：去掉锁的负向测试在旧代码上报 `WARNING: DATA RACE`，位置正是旧行 251（`seen` 读）与 259（`seen` 写）；`go test -race ./common/dialer ./dns` 通过。
- **修复**：per-lookup `sync.Mutex` 串行化 `deliver`；锁覆盖读/比较/写和 batch 发布。锁跨 `inner` 发送持有，但两个 send 都有 `streamCtx.Done()` 分支，`stop()` 先取消再等待，因此不会被阻塞取消。保留 A/AAAA 并行、代次、去重与双栈恢复。
- **提交**：`9ee6e36bc7b`（含三项回归测试：并发双栈发布/去重、最新来源代次、取消释放持锁回调）。

### P1 — TUIC/Hysteria2 首次建连竞态互相关闭对方 winner（已修复）

- **位置**：`protocol/tuic/family.go`、`protocol/hysteria2/family.go` 的 `familyCarrier.race` / `retireLosers` / `retireFamily`
- **引入版本**：0.1.45，`5d9ab12f4b8`（family race）、`b5e38ec4169`（generation 清理）
- **触发**：启动或网络切换后，两个 `DialConn` 同时看到 `established=false`，各自对同一对 client 发起 race。若两个 family 的握手几乎同时成功，A race 读 family1 为 winner、B race 读 family2 为 winner；两边都进入 `retireLosers`，各自把对方的 winner client `CloseWithError` 掉。返回给用户的两个“成功”连接实际落在已被对方关闭的 session 上。
- **根因**：`generation`/实例检查只能隔离 `CloseWithError` 前后的代次；同一代内没有“首次创建单飞”锁，`race` 也没有在发布前复查 `established`。两族 client 自身的 single-flight 只共享底层 session 创建，不能保护 carrier 的 winner 清理。
- **影响**：首连被并发杀死、额外握手；极端情况下 `established=true` 但两个 client slot 均为 nil，下一 demand 重新 race。属于用户可见的连接失败。
- **证据**：负向测试（移除 `creationAccess`）在一次受控交错中记录到 4 次 attempt，而正确行为应为 3 次（第一 race 两次 + 第二 demand 在 winner 上的一次 stream dial）。修复后 `go test -race ./protocol/tuic ./protocol/hysteria2` 通过。
- **修复**：新增 `creationAccess` 单飞首次 race；等待者在锁内复查 `established`，若已有 winner 则**释放创建锁后**在 winner 上开新 stream（避免把启动 burst 串行化在一条 stream 后面）。保留 winner/代次/实例检查。
- **提交**：`96f40558ffb`（含并发回归测试）。

### P1 — `GenerationGate.Close` 并发读写 `g.remove`（已修复）

- **位置**：`common/runtimecoord/generation_gate.go:89-97`
- **引入版本**：0.1.45，`f564cb89fa0`
- **触发**：Scope 清理与 endpoint manager 的 duplicate-tag loser 同时关闭同一 endpoint（`protocol/masque/client.go` 与 `adapter/endpoint/manager.go`）。
- **根因**：`Close` 的 `if g.remove != nil { g.remove(); g.remove = nil }` 是无同步的读-改-写。API 文档却承诺 `Close` 幂等。
- **影响**：数据竞争；两个 closer 可能重复执行 unregister，func 值字段的无同步访问是 Go 内存模型违规，race detector 会报告。
- **证据**：审计中用并发 `Close` 复现 `WARNING: DATA RACE`（read at generation_gate.go:93 / previous write at :95）；修复后 32 goroutine 回归测试断言 remove 恰好执行一次。
- **修复**：`sync.Once` 包住一次性 unregister。
- **提交**：`3f568bc0af7`。

### P2 — TUIC/Hysteria2 全失败与晚到 session 泄漏（已修复）

- **位置**：同 family 文件，`race` 的全失败分支与 `retireFamily` 的代次检查
- **触发**：
  1. 两个 family 都失败时，`settled` 被丢弃，两个 failed client 仍挂在 slot 上；若失败是调用方 ctx 先结束，SDK 的 offer 仍由 client 生命周期 context 持有，可在之后建立一条 carrier 不可见的 session。
  2. race 快照后、attempt 首次 `offer` 前发生 `CloseWithError`：client 已被移出 slot，但 attempt 仍可在旧 client 上建立 session；旧 `retireFamily` 因 generation 不匹配直接返回，session/socket/3 个 SDK goroutine 永久不可达。
- **影响**：半开 session、socket 与 goroutine 泄漏，长期运行稳定性受损；下一 demand 可能继承失败 client。
- **证据**：全失败负向测试在修复前断言失败（client 未 nil）；晚到 session 由 pinned `sing-quic` 的 `CloseWithError` 非 latch 语义与代码路径交叉确认（未做真实网络的晚到建立复现，见 NOT_RUN）。
- **修复**：全失败分支同步调用 `retireLosers(..., winner=0, unread=0)` 退休 settled client；`retireFamily` 在 generation 已变化时仍关闭**精确旧实例**（新代次已安装新指针，不会误关），同代但已非当前实例时保持 no-op。
- **提交**：`96f40558ffb`。

### P2 — `ConnPool.AcquireFresh` 归属 Map 泄漏（已修复）

- **位置**：`dns/transport/conn_pool.go:145-171, 266-300`
- **引入版本**：0.1.45，`9c974b3a5aa`
- **触发**：DNS transport 的 stale-connection 重试调用 `AcquireFresh`；每次新连接都被 `trackingDial` 记入 `p.dialPaths`，但 fresh 连接从不进入 pool 的 `state.all`，Release/eviction/Reset/Close 都不会删除该 entry。
- **影响**：每个 DNS 重试泄漏一个 dead `*multiplexConn` + step slice，长寿命 transport 持续增长。
- **证据**：审计子任务在 100 次 `AcquireFresh` + caller close 后测得 `len(dialPaths)==100`；新增回归测试做 32 次并断言为 0，同时断言 pool 没有 close caller-owned conn。
- **修复**：`AcquireFresh` 直接 `dial(ctx)`，不再走 pool 的归属记录；调用方自己的 `DialPath` recorder 仍从 context 收到 steps。
- **提交**：`008021ab1f6`。

### P2 — 非 health 的 `MeasurementShare.Publish` 关闭已关闭 channel（已修复）

- **位置**：`common/urltest/measurement_share.go`
- **引入版本**：0.1.45，`2c1628765cb` 等
- **触发**：`Join` 对 `PurposeDisplay`/`PurposeDiagnostic` 返回一个 `done` 已关闭、未注册的 leader wait；调用方按 `Join` 文档执行“leader 然后 Publish”时，`Publish` 的 `wait.closed` 仍为 false，进入 `close(wait.done)`。
- **影响**：确定性 `panic: close of closed channel`，panic 在测量 goroutine 中会终止进程。当前生产 `Measure` 只在 `PurposeHealth` 时 `Join`，因此属于 latent API 崩溃；但这是公开 API 的文档契约陷阱。
- **证据**：修复前临时测试直接 panic，栈指向 `measurement_share.go:188`；修复后 `NotPanics` 回归测试通过。
- **修复**：非 health wait 在 `Join` 时标记 `closed: true`，`Publish` 变为 no-op；`Await/leave` 行为不变。
- **提交**：`512615444f8`。

## 3. 未修复的已确认缺陷与潜在风险

以下均未改动代码，按严重度排序；触发窗口与建议已写明。

### R1（Medium）XHTTP XMUX：`get()` 与 `addOpenUsage` 之间的复位窗口

- **位置**：`transport/v2rayxhttp/xmux.go:699-766`（`get` 解锁后返回）、`transport/v2rayxhttp/client.go:301-305`（`addOpenUsage(1)` 在 pool 锁外）、`xmux.go:119-137`（closed 后 Add 会“复活”）
- **触发**：dial A 通过 `get()` 拿到 idle client 后被调度走；同时网络切换的 `RetireSuspect`、trim 的 `CloseIdleConnections` 或 `Client.Close` 把该 client `close()`（openUsage=0 时已 `releaseConn`）；A 随后 `addOpenUsage(1)` 把它重新计数，`dialMode` 在该已关闭 transport 上发请求。
- **影响**：generation 检查会让 A 最终返回 stale error，但请求可能已经发出；Close 后更严重的是 transport 可能新建一条无人拥有的 HTTP/2 连接，直到自身超时才消失。
- **现状**：现有 `xmux_close_boundary_test.go` 只测 `get()` 在 Close 之后，不测 `get()`→`addOpenUsage` 窗口；未修复。
- **建议**：把“closed 检查 + openUsage++”做成 `tryAcquire()`，在 pool 锁内或与 close 同一 client 锁内完成；DialContext 失败则重取。不要简单把 `addOpenUsage` 放进 pool 锁，避免大范围测试改动。

### R2（Medium-low）TrustTunnel：在途 CONNECT 未计入 stream

- **位置**：`transport/trusttunnel/client.go:796`（`streams.Add(1)` 在 200 之后）、`:677-704`（CloseIdle/RetireSuspect）、`:860-869`（fallback retire）、`:1005-1025`（idleWatcher）
- **触发**：A 的 CONNECT 已上线但尚未收到 200；B 的 RetireSuspect/CloseIdle 看到 `streams==0`，关闭 session。A 可能收到传输错误（openStream 重试可能产生重复 CONNECT），或在 200 返回后拿到一个 session 已关闭的 stream。
- **影响**：重复 CONNECT/一次失败流；与“retiring 保留已承载 stream”的文档承诺冲突。
- **建议**：在进入 `roundTrip` 前先 `streams.Add(1)`，所有错误/取消路径 `Add(-1)` + `drainIfUnused`；需要小心重试路径的计数恰好一次。

### R3（Medium-low）`FailoverState` 与 `HealthSnapshot`/`ApplyFailoverPolicy` 的并发访问

- **位置**：`common/urltest/failover_policy.go:131-178, 185-336`；`protocol/group/urltest.go:1249-1338`
- **触发**：health round 在 `Decide` 中修改 `stable/challenger/widened/...`，并发 sampler 调 `HealthSnapshot`（读 `Stable/Widened/Probes`）；或 `ApplyFailoverPolicy` 在 round 运行时替换 `g.failover` 指针。
- **现状**：审计中确认这是未同步的数据竞争路径；当前树没有这些 API 的生产调用者（`HealthSnapshot` 主要出现在 opt-in 的 `SING_BOX_RECOVERY_TEST` 测试），因此未在生产触发。`ApplyFailoverPolicy` 的文档称只在构造期调用一次。
- **建议**：如果这些 API 要面向客户端/诊断暴露，给 `FailoverState` 加锁或让 group 持锁快照；在文档中明确单写者/仅构造期约束。不要在不清楚调用方的情况下直接把整段状态机改成锁实现。

### R4（Medium-low）v2rayquic `ResetContext` 可能退役/迁移刚建立的新代连接

- **位置**：`transport/v2rayquic/client.go:603-646`
- **触发**：`ResetContext` 清掉 `c.pending` 后、获取 `migrateAccess` 前，新 demand 完成 `completeOffer` 并发布新连接；Reset 随后 `c.conn.Load()` 看到它，按无 stream 退役或按迁移处理。若 `migrateAccess` 被上一轮迁移持有（约 4 秒窗口），概率上升。
- **影响**：新网络上的 session 被旧网络切换处理器关闭/迁移，刚拿到连接的 demand 可能得到死连接或二次握手。
- **建议**：在 `migrateAccess` 内重新读取并校验该连接的 generation/建立时间；或让 `completeOffer` 的 publish 与 reset 使用同一个边界令牌。

### R5（Low-medium）libbox diagnostic emitter：`Publish` 在 `Close` 之后追加事件

- **位置**：`experimental/libbox/diagnose_emitter.go:128-136, 170-185`
- **触发**：`Publish` 在锁内读到 `closed=false`，解锁后 `Close` 设置 closed 并追加 finding/snapshot/end；前者的 `appendAndOffer` 再把 stage 追加到 end 之后。第二个并发 `Close` 还会在 `finished` 关闭前返回，读到半截 Snapshot。
- **影响**：live sink 在 `Finished` 后停止，丢弃 end 后的事件，而报告快照仍包含它；目前生产路径单 goroutine 发布 + defer Close，未触发。
- **建议**：`Publish` 与 `Close` 共用一个“确定终止”临界区（closed 检查 + append + send 原子化），或由单一 finaliser 负责关闭。

### R6（Low-medium）route `CloseAll` 同步关闭记账窗口

- **位置**：`route/conn.go:234-249`
- **触发**：CloseAll 在 `m.access` 下取走全部连接并解锁，之后才 `beginSynchronousCloses`；并发 `ConnectionManager.Close` 的 `waitForDisposals` 可能在 A 还没开始 close 时观察到全 0 并返回。
- **影响**：Close 早于物理 socket 关闭返回，违反“Close 返回即全部关闭”的承诺；Box 的正常 Scope 清理通常是串行的，窗口窄。
- **建议**：把 `beginSynchronousCloses(len(closers))` 移到解锁之前（与 Reclaim 的 `stageDisposals` 一致）。

### R7（Low-medium）admission 快路径忽略已取消 ctx / settled ticket 不接管 socket

- **位置**：`route/conn_reclaim.go:415-453, 518-554`；`common/dialer/default.go:816-819`
- **触发**：dial setup ctx 已被 CloseAll/Reclaim 取消但仍有 admission 空间时，`AcquireConnection` 直接发 ticket，`TrackConn` 只检查 `m.closed`，可能把 socket 装在已经 drain 过的 manager 上；`TrackConn` 在 settled ticket 上返回 error 但不 close/queue conn，测试自己 close 掩盖了这个问题。
- **影响**：前者主要被 dialer 的 epoch 检查兜底；后者是 API 契约不一致 + 潜在 socket 泄漏（生产不会 double-track）。
- **建议**：`AcquireConnection` 快路径检查 `ctx.Err()`；`TrackConn` 对任何失败都取得 socket 所有权（关闭或进 pending）。

### R8（Low）MASQUE flowRegistry 的注册/退役顺序

- **位置**：`protocol/masque/server.go:309-335`（先还地址池、后 ReleaseSession）、`:465-483`（add 无 closed 检查）、`protocol/masque/flow_registry.go:31-38, 100-120`
- **触发**：地址回收后新 session 复用同一地址并在 `ReleaseSession` 前注册 flow，旧 session 的 `closeFor(addr)` 会把新 flow 关掉；Close 的 `closeAll` 之后仍可通过 `NewConnectionEx` 注册 flow，创建新 map 且无人再关。
- **影响**：极小窗口下错误关闭新 flow；teardown 后泄漏 inner socket。当前测试未构造出端到端。
- **建议**：`ReleaseSession` 先于地址池 release；flowRegistry 增加 closed 状态，close 后 add 直接关闭传入 conn。

### R9（Low）零散项

- `common/httpclient/http3_transport.go:330-383`：H3 attempt 的 `cancelProbe`/`cancelAttempt` 在成功路径只 `timer.Stop()`，未调用 cancel，长期存活父 context 下保留子 context；建议 defer cancel。
- `common/artifact/store.go:112-118`：`Version.Release` 的 `released bool` 非原子；当前调用方单 goroutine，但“幂等”注释未覆盖并发。
- `common/urltest/coordinator.go:225-229`：已取消 ctx 仍可在快路径拿到 slot；只是浪费一轮测量，无泄漏。
- `common/runtimecoord/generation_gate.go:73-79`：`AfterDial` 用完成时的 `Epoch()` 确认，而非本次 dial 起点的 generation；跨 bump 的成功连接可能把新代标为 current。各 transport 有自己的 generation 检查，listener 也会复位，故列为低风险假设。
- `protocol/group/provider_members.go:23-53`：`providerUpdateCheckScheduler` 无调用点（dead code），不影响正确性。
- `transport/trusttunnel/client_fallback_test.go:582-636`：`TestFallbackOldFailureAfterResetCannotCordonTheNewNetwork` 阻塞的是第一个 `client.now()`（`h3Cordoned`），未真正覆盖 `cordonForGeneration` 内的 generation 检查；该保护目前缺少有效测试证据（代码阅读认为逻辑正确）。

## 4. 已排除项（重点核查后未发现缺陷）

- `route/conn_reclaim.go`：`m.access → disposal.mu` 是唯一锁序；Acquire/Track/CloseAll/Reclaim、pending/condemned/worker、sweep timer 与 Close 的交互未发现死锁或丢唤醒；`stageDisposals` 不等待内核 Close。
- `route/conn_halfclose.go`：观察者单次 settle、teardown once、observer map 加锁、timer 不保存句柄；未发现 close/send 竞态。
- `common/urltest`：health path 的 MeasurementShare 参与者计数、last-leave cancel、abandoned leader identity、slotPool 的 queue/overflow/demotion/release 均经压力与 `-race` 检查通过；URLTest 的 `selectionCommit → updateAccess → history` 锁序未发现反向路径。
- `dns/EventBus` / `dns/History`：8 个并发 emitter vs 4 个 subscribe/unsubscribe/read/clear 的压力测试无 race；History 查询返回副本，字节会计一致。
- `common/trafficcontrol` eventFanout：join/leave、subscribe/drain/unsubscribe、close-vs-publish 压力测试无 race；channel 从不 close，只用 done 终止。
- `adapter.DialPath`：所有记录/读取在 mutex 内；DNS transport group 的并发 dial 不会撕裂 steps。
- `transport/trusttunnel` H3→H2：generation bump/clear 在同一 `serviceAccess`；`cordonForGeneration`/`clearCordonForGeneration` 复查 generation；`publishOffer` 的 discarded/close 恰好一次；QUIC socket watcher 受 `quicConn.Context().Done()` 约束。
- `common/httpclient` H3 verdict：`broken` map、attempt 序号、per-authority outcome 在同一 mutex；claim/clear 顺序正确。
- `protocol/naive` H2 fallback：`getContext` single-flight，Close during creation 会释放 late client；取消只结束等待者。
- `protocol/masque` flowRegistry 的 map 操作有锁（但见 R8 生命周期顺序）。
- `common/trafficsched`、`common/resourceguard`、`service/oomkiller`、`common/power`：相关 `-race` 套件通过，未发现新竞态。
- `Go vet`：`./common/... ./dns/... ./protocol/... ./transport/... ./route/... ./daemon/... ./adapter/... ./experimental/libbox/...` 全部通过（无输出）。

## 5. 验证结果

### 已执行（PASS）

```text
go test -race -count=1 ./common/dialer ./dns
  ok  common/dialer  23.410s
  ok  dns            9.873s

go test -race -count=1 ./common/runtimecoord ./common/urltest ./protocol/tuic \
  ./protocol/hysteria2 ./dns/transport
  ok  common/runtimecoord  1.161s
  ok  common/urltest       13.336s
  ok  protocol/tuic        2.182s
  ok  protocol/hysteria2   2.341s
  ok  dns/transport        7.134s

go test -race -count=1 ./common/dialer ./dns ./common/runtimecoord ./common/urltest \
  ./protocol/tuic ./protocol/hysteria2 ./dns/transport ./transport/trusttunnel \
  ./common/httpclient ./protocol/naive ./protocol/masque ./protocol/group \
  ./common/artifact ./route/rule ./experimental/libbox ./daemon \
  ./common/trafficcontrol ./adapter ./service/oomkiller ./common/power
  ok  common/dialer          23.583s
  ok  dns                    10.133s
  ok  common/runtimecoord     1.221s
  ok  common/urltest         15.309s
  ok  protocol/tuic           2.422s
  ok  protocol/hysteria2      2.673s
  ok  dns/transport           7.264s
  ok  transport/trusttunnel  24.031s
  ok  common/httpclient       1.141s
  ok  protocol/naive          2.409s
  ok  protocol/masque         4.305s
  ok  protocol/group         11.151s
  ok  common/artifact         1.820s
  ok  route/rule             13.741s
  ok  experimental/libbox   10.323s
  ok  daemon                 24.681s
  ok  common/trafficcontrol   4.661s
  ok  adapter                 5.946s
  ok  service/oomkiller       1.803s
  ok  common/power            5.760s

go test -race -count=1 -run 'Test(Admission|Close|Ticket|Disposal|Reclaim|Transition|Sweep|HalfClose|Drain|DeadPath|ConcurrentTrack|FinalClose|ConsecutiveReclaims|TheDisposalBacklog)' ./route
  ok  route  5.368s

go vet ./common/... ./dns/... ./protocol/... ./transport/... ./route/... ./daemon/... ./adapter/... ./experimental/libbox/...
  PASS（无输出）
```

### 负向对照（修复前失败/报告）

- `dual_stack_recovery`：移除 mutex 后 `WARNING: DATA RACE`，位置为旧 `seen` 读/写（251/259），exit 1。
- TUIC family：移除 `creationAccess` 后并发测试记录 4 次 attempt（期望 3），exit 1。
- `MeasurementShare`：非 health `Publish` 直接 `panic: close of closed channel`（`measurement_share.go:188`）。
- `GenerationGate`：并发 `Close` 在旧代码由 race detector 报 read/write（generation_gate.go:93/95）。
- `ConnPool.AcquireFresh`：审计子任务测得 100 次 fresh 调用后 `dialPaths=100`。

### NOT_RUN（不得视为 PASS）

- 完整 `go test -race ./route`（全量套件超出本环境时间预算，被终止；只跑了上列定向并发测试）。
- iOS 真机 / Network Extension 运行时验证（当前 Windows 环境，无 Xcode/iOS 设备）。
- 子仓 Swift/客户端并发审计（`clients/apple`/`desktop`/`android` 只核对了 gitlink 与 pin，未逐行审计其 goroutine/Task/回调模型）。
- 依赖仓库全量审计（仅按调用点核对了 pinned `sing-quic` 的 offer/CloseWithError 语义）。
- TUIC/Hysteria2 晚到 session 泄漏的真实网络复现（只在代码/SDK 语义层确认；回归测试覆盖了并发 race 与全失败路径）。
- `FailoverState` 并发 sampler 的 opt-in 测试（`SING_BOX_RECOVERY_TEST=1`，未跑）。
- 任何性能前后对比（未建立同机基线；本次改动均为低频路径的一次锁/一次判断，不宣称量化收益）。

## 6. 修复提交

| 提交 | 内容 |
| --- | --- |
| `9ee6e36bc7b` | `fix(dialer): serialize recovery identity state across concurrent family publishes`（P0） |
| `3f568bc0af7` | `fix(runtimecoord): make GenerationGate.Close idempotent under concurrency`（P1） |
| `96f40558ffb` | `fix(tuic,hysteria2): single-flight the first family race and retire late clients`（P1/P2） |
| `008021ab1f6` | `fix(dns): stop attributing caller-owned fresh connections in ConnPool`（P2） |
| `512615444f8` | `fix(urltest): make publishing a non-health shared wait inert`（P2） |

已推送 `origin/testing`（`512615444f8`）；工作区仅保留原有 `clients/*` 子模块改动，未纳入提交。

## 7. 剩余风险与建议

1. **最高优先**：R1（XHTTP XMUX get/addOpenUsage）与 R2（TrustTunnel 在途 CONNECT 不计 stream）都属于真实网络切换/trim 路径的生命周期窗口，建议下一轮优先修，并用确定性 fake transport 测试覆盖。
2. **FailoverState 并发 API**：在把 `HealthSnapshot`/`ApplyFailoverPolicy` 暴露给客户端或诊断前，先确定单写者契约或加锁；否则一个采样器就可能触发 race。
3. **v2rayquic Reset 窗口**：用 generation token 把“新连接发布”和“旧 reset 的 conn 读取”统一到一个边界；补“reset 与 dial 并发”的 fake 测试。
4. **测试证据**：TrustTunnel 的 generation 测试目前没有真正覆盖 `cordonForGeneration` 的检查点，建议增加第二/第三次 clock 调用的 seam；完整 `./route -race` 应在 CI/更长窗口补跑。
5. **设备级验证**：iOS NE 只做了单元/race 级验证；需要在真实设备上复跑启动 burst、网络切换、睡眠/唤醒与 DNS 双栈场景，确认无用户可见回归。
6. **依赖与子仓**：`sing-quic`/`quic-go`/客户端 Swift 并发模型仍未全量审计；建议把本次 family/offer 相关 SDK pin 与客户端调用链纳入下一轮专项。

## 8. 2026-11-13 补充修复

本节记录对第 3 节残余风险的后续处理，提交为 `c5d5b92597de10cda517140a017e8a5b3948effc`。它不改变第 1–5 节的历史审计证据；下列条目的状态以本节为准。

| 原条目 | 状态 | 处理 |
| --- | --- | --- |
| R1 XHTTP XMUX get→addOpenUsage | **已修复** | `acquireUsage()` 在 client 锁内完成 retired 检查与计数；DialContext 在选池循环中预约，失败则 `discardRetired` 后重取；回归测试 `TestXmuxRetiredConnectionCannotBeRevived`。 |
| R2 TrustTunnel 在途 CONNECT | **已修复** | `roundTrip` 在发包前 `streams.Add(1)`，所有失败路径归还并 `drainIfUnused`；回归测试 `TestInFlightConnectIsCountedAsAStream`。 |
| R3 FailoverState 并发 | **已修复** | `FailoverState` 内部 mutex；group 的 failover 指针在 `g.access` 下读写；回归测试 `TestFailoverStateConcurrentSnapshot`、`TestURLTestGroupFailoverInstallAndSnapshotAreConcurrentSafe`。 |
| R4 v2rayquic ResetContext | **已修复** | Reset 开始处捕获 conn，等待 migrateAccess 后只有仍是同一对象才处理；回归测试 `TestResetDoesNotRetireAGenerationCreatedWhileWaiting`。 |
| R5 libbox emitter Publish-after-Close | **已修复** | Publish/Close 共用同一 finalisation 临界区；回归测试 `TestCloseAndPublishShareOneFinalisationBoundary`。 |
| R6 route CloseAll 记账窗口 | **已修复** | 同步关闭计数与“从 tracked 集合摘除”在同一 `m.access` 临界区完成。 |
| R7 admission 已取消 ctx / settled ticket | **部分修复** | `AcquireConnection` 在快路径检查 `ctx.Err()`；回归测试 `TestAcquireConnectionRefusesCancelledContextWithRoom`。settled ticket 的 TrackConn 失败仍按原契约由调用方处理，未改动。 |
| R8 MASQUE 回收/注册顺序 | **已修复** | `ReleaseSession` 先于地址池归还；flowRegistry 增加 closed，teardown 后注册的 flow 立即关闭并回调 onClose；回归测试 `TestSessionReleaseRunsBeforeTheAddressReturnsToThePool`、`TestFlowRegistryRejectsRegistrationAfterCloseAll`。 |
| R9 HTTP/3 attempt context / Version.Release | **已修复** | H3 attempt context 在错误或响应 body Close 时取消（`cancelAttemptOnCloseBody`）；`Version.Release` 使用 atomic one-shot；测试 `TestCancelAttemptOnCloseBodyReleasesTheAttempt`、`TestVersionReleaseIsConcurrentSafe`。 |
| 测试证据缺口 TrustTunnel cordon | **已修复** | 测试改用 `cordonHook`，暂停点位于 `cordonForGeneration` 内部，真正覆盖 generation 检查；`with_quic` 下通过。 |
| `GenerationGate.AfterDial` 使用完成时代次 | **未修复（有意）** | 正确修法需要把每次 dial 的起始代次作为 token 传过 8 个 outbound 的公共 API；当前 listener phase 会对每个注册资源再次执行 reset，是权威兜底。为避免大范围 API 改动和引入新的生命周期风险，保留现状并在残余风险中说明。 |

补充验证（全部 `-race`）：

```text
ok common/artifact 1.793s       ok common/urltest 12.135s
ok protocol/group 11.250s       ok protocol/masque 4.252s
ok experimental/libbox 11.133s  ok transport/v2rayxhttp 21.570s
ok transport/trusttunnel 42.853s (with_quic)
ok transport/v2rayquic 10.619s (with_quic)
ok common/httpclient 2.021s (with_quic)
ok transport/masque 77.193s (with_quic)
ok route (targeted concurrency tests) 5.273s
go vet (all touched packages, QUIC tags included): PASS
```

仍为 **NOT_RUN** 的边界与第 5 节相同：完整 `./route` race 套件、iOS 真机 NE、子仓 Swift 并发与依赖仓库全量审计。`AfterDial` 的代次 token 化是下一轮唯一保留的已识别并发语义改进项。
