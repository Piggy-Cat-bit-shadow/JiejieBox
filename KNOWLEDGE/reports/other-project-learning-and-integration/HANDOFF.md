# HANDOFF —— 阶段交接（当前：04 批 2 → 批 3）

本文件给下一阶段可直接使用的 API/owner/调用点、阅读缓存与未确认事项。**新上下文先读本文件与 `stages/03.md`，再按 `KNOWLEDGE/INDEX.md` 的导航补本阶段源码增量。**

## 1. 阅读缓存（不要重复全文读入）

### 已读文档/小节

| 文档 | 读到的范围 | 用途 |
|---|---|---|
| `AGENTS.md` | 全文（含 2026-11-08 更新后的 Release/TestFlight 段） | 入口、溯源/证据/tag 三陷阱 |
| `KNOWLEDGE/README.md` | 全文 | 基线指针、证据标签、刷新策略 |
| `KNOWLEDGE/BASELINE.md` | v0.1.39/v0.1.34/v0.1.9 段、溯源检验、增量工作流 | 确定知识覆盖与增量命令 |
| `KNOWLEDGE/CALIBRATION-2026-11-08.md` | 全文 | 当前专题目标 `3b6ec64`、子仓 pin、未覆盖边界 |
| `KNOWLEDGE/INDEX.md` | 全文 | 任务/关键词/路径/符号路由 |
| `KNOWLEDGE/modules/README.md` | 全文 | 模块注册与 owner 地图 |
| `KNOWLEDGE/ARCHITECTURE.md` | 全文 | 11 条边界 |
| `KNOWLEDGE/modules/resource-admission.md` | 全文 | 准入 owner、默认参数、A1–A6 约束、验证命令 |
| `KNOWLEDGE/modules/power-mobile-lifecycle.md` | §4（含本轮新增小节）、§1–2、§7 | OOM 回收协调、四种池、复用分带 |
| `KNOWLEDGE/PERFORMANCE-FACTORS.md` | 全文 | 测量纪律（交错/时间戳/证据等级） |
| `KNOWLEDGE/ACCEPTANCE.md` | 已知失败集合、Windows 复检段 | 3 个 `TestWindowsClient*TLS13` 既有失败 |

### 已读源码（沿符号）

`common/resourceguard/gate.go`（`Gate.Acquire`/`grantLocked`/`Stats`/`Permit`）、`guard.go`（`observe`/`apply`/`Stats`/`useClock`）、`held.go`；`service/oomkiller/service.go`、`timer.go`（`adaptiveTimer`、`poll`、回收执行器、`runIdleReclaimLocked`、`runPressureReclaim`、`runCrisisReclaim`）、`recorder.go`（`SetExtraMetadata`/`mergeExtraMetadata`/`ReportStatus`）、`policy.go`、`reclaim_observation.go`（本轮新增）；`adapter/network.go`（`TrimMemory`/`ReleaseMemory`/`CacheMemoryReleaser`）、`adapter/router.go`（`TrimIdleResources`/`ExpiredCacheReleaser`）；`route/reference.go`（`retireIdleResources`/`TrimIdleResources`）、`route/network.go`（`TrimCaches`）、`route/router.go`（`TrimIdleResources`）。

### revision 锚点

| 项 | 值 |
|---|---|
| 知识覆盖（专题） | `3b6ec64f7145a2add99656d47caaa4f22a1cc478`（v0.1.44） |
| 知识覆盖（广泛） | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`（v0.1.34，未移动） |
| 本轮施工基线 | `25f5c9a638bdae4a1e2bb460641795f10b169bb2`（origin/testing） |
| 01 代码提交 | `503a18b55a7cb53ca8aade3b90e69d5683832552` |
| 01 文档/收尾提交 | `dd6fd606bcf9`、`fbb339e6212`、`37b666f7a6b`（含一处代码注释修正）、`b8116e0ac4e`、`99095e05d4e`，以及其后的收尾提交（均不改运行时行为） |
| 02 进入时 HEAD | `11933a682398005a079669499b0e4cb51652a2e8`（= `origin/testing`） |
| 02 代码提交 | `99fd6ee23d98` |
| 02 文档/记录提交 | `d14933eada2`、`b004a87c983`、`247496c4872`（均不改运行时） |
| 增量范围 | 02 触及 `common/resourceguard`、`common/tls`、`protocol/anytls`、`protocol/mieru`、`service/oomkiller`；这些路径在 v0.1.39→02 前无生产代码变更 |

### 已确认事实

1. 进程压力只有`service/oomkiller.Service.pressure`一个来源；guard level、TUN `MemoryPressure`、回收状态机都读它。
2. `Gate.Stats` 已导出 requested/admitted/queued/refused/timedOut/canceled/released、inflight/waiters/maxInflight、limit/queue/wait；报告键 `resourceGuard` 已由 `guardMetadata` 写入。
3. 许可语义：`Guard.Acquire` → `Permit.Release`（幂等/nil-safe）；`resourceguard.HasHeldPermit`/`WithHeldPermit` 防止嵌套重复领取；TLS 与 AnyTLS 两个入口分别读取 `trafficclass` 决定 interactive。
4. `TrimCaches(ctx) int` 早已返回删除条目数，`adaptiveTimer` 的 wrapper 之前丢弃；本轮改为记录。
5. 回收三层（cache→pools→runtime）与 pressure/crisis 分支共用单执行器；低收益 → 30 s 冷却；crisis 不走该冷却。
6. 驻留/关闭/构建占用目前**没有**真实计数；`retireIdleResources` 的返回值是 keeper 数。

### 资料冲突（保留，不静默改写）

- `KNOWLEDGE/README.md` 顶部仍写“当前产品目标为 v0.1.39”，而 `CALIBRATION-2026-11-08.md` 与 `BASELINE.md` 顶部已到 v0.1.44。属文档漂移，本轮不改写历史段落；使用基线以 `BASELINE.md` 顶部为准。
- `power-mobile-lifecycle.md` 的旧句记录“Go 1.26.10 下通过”，本机是 Go 1.25.5；旧句保留，本轮新增小节单独标注当前环境。

## 2. 01 交付给 02 的接口（对 03 仍有效）

| 事项 | 位置 | 约定 |
|---|---|---|
| 进程压力 | `Service.pressure` / `Service.MemoryPressure()` / `oomkiller.MemoryPressure(ctx)` | 03/04 不得新增采样器；需要新信号时先改 owner |
| 准入 | `resourceguard.FromContext(ctx)`、`Guard.Acquire(ctx, class)`、`Permit.Release/Wait` | 许可必须在创建返回时释放；不得包装返回连接 |
| 嵌套标记 | `resourceguard.WithHeldPermit/WithHeldPermit(ctx)` | 跨 detour/代理链只持一份 |
| 类别 | `common/trafficclass.WithClass` / `FromContext` | interactive/realtime 才用预留；02 若扩展类别需同步两入口 |
| 观测 | `GuardStats` + `guardMetadata`（`resourceGuard`）；`reclaimObserver` + `reclaimMetadata`（`memoryReclaim`） | 只加性扩展；不新建报告通道 |
| 回收执行器 | `adaptiveTimer` 的 `reclaimWake/pendingReclaim/inFlightReclaim`；`finishIdleReclaim`、`runPressureReclaim`、`runCrisisReclaim` | 02 若在创建路径触发回收，只能 enqueue，不能自建执行器 |
| 时钟/接缝 | `adaptiveTimer.now`、`sampleFunc`、`readUsage`、`trimCaches`、`trimMemory`、`freeOSMemory`、`releaseNetwork`、`runtimeReclaimable` | 测试专用赋值；生产默认真实实现 |

### 2.1 02 的真实创建接点（现仓定位，2026-11-08）

| 真实创建路径 | 现仓接点 | 02 需要注意 |
|---|---|---|
| 共享 TLS 握手 | `common/tls/client.go`：`defaultDialer.DialTLSContext` → `handshakeWithAdmission` | 已准入；许可在握手返回即释放；类别来自 `trafficclass` |
| AnyTLS | `protocol/anytls/outbound.go`：`dialAdmitted` → `protocol/anytls/admission.go`：`dialWithAdmission` | 已准入；`HasHeldPermit` 防嵌套重复领取 |
| Hysteria2 / TUIC | `protocol/hysteria2`、`protocol/tuic` 的客户端拨号；共享 offer/pending 在 pinned `sing-quic` 模块内 | **共享任务**的许可必须在共享工作开始前取得、由任务完成/退出归还；代码在依赖模块，按 `go.mod` replace 的 pin 读，不在本仓 |
| Mieru | `protocol/mieru/dialer.go`；已有 `protocol/mieru/dial_observation.go`（每次 SDK dial 的类型化证据，无 goroutine/timer） | 复用现有 observation，不另建；沿 SDK Runtime→Mux→`newUnderlay` 接线 |
| Cronet / Naive | `protocol/naive`、`transport/http`；`cronet-go` pin（29 个 native 模块 controlled） | 引擎初始化、引擎驻留、native 连接创建是三类事实；`Start` gate 不等于 native 覆盖 |
| 类别绑定 | `route/conn.go` 的 `NewConnection` / `NewPacketConnection`，各一处 `trafficclass.WithClass` | 不要在每个协议里重新判断类别 |

## 3. 02 开工清单（历史保留；03 用 §6/§7）

先做一次启动自检，确认从干净状态开始：

```sh
git status --porcelain=v1          # 应为空（clients/android 已恢复到父仓 pin）
git submodule status               # clients/* 不应有 '+' 前缀
git rev-parse HEAD; git ls-remote origin refs/heads/testing   # 需要时先 ff-only 同步
go test ./service/oomkiller ./common/resourceguard -count=1
```

然后：

1. 读 `CONTRACTS.md` §1/§4、`MASTER_PLAN.md` 的 G02–G06 行、`KNOWLEDGE/modules/resource-admission.md` 的 AI cautions。
2. 用 `git log --oneline 25f5c9a..HEAD` 补 02 开始前的源码增量（若远端又前进，先快进同步，记录新 HEAD）。
3. 真实阶段盘点：QUIC/0-RTT、Cronet 引擎/原生连接、Mieru underlay 目前不在许可覆盖内；按“创建前领取、返回释放”接入，不要包装连接。
4. 观测扩展只改 `Gate/Guard` 与 `guardMetadata`；新增字段在 `CONTRACTS.md` 登记。
5. 验证：`go test ./common/resourceguard ./common/tls ./protocol/anytls ./service/oomkiller -count=1`，`-race` 同包，负载定义同 01。

## 4. 未确认事项 / 阻断

| 项 | 状态 | 说明 |
|---|---|---|
| iOS NE 真机观测 | `BLOCKED_BY_ENVIRONMENT` | 无设备；`memoryReclaim` 在 NE 的真实内容未设备验证 |
| 本提交的 CI（02） | `VERIFIED_CI` | `Verify` run 42474314237，head `d14933e`，`verify` + `race checks` 均 success；其后的记录提交只改文档 |
| 平台标签构建 | `NOT_RUN` | 只跑了默认 `go build ./...` |
| 客户端消费 `memoryReclaim` | `UNKNOWN` | 客户端 schema 不需要它；是否有 UI/诊断消费由 08/客户端决定，核心不假设 |
| 客户端 `clients/android` 本地分支 | 已收尾 | 工作树已恢复到父仓 pin `c783710`（`git status` 干净）。原先检出的本地分支 `fix/libbox-stringbox-callsites`（tip `9d15717`，含 2 个仅本地提交）**未被删除**；需要时 `git -C clients/android checkout fix/libbox-stringbox-callsites` 恢复。02 不需要该分支的代码 |

## 5. 恢复执行方式

- 新上下文：读本文件 → `stages/02.md`（或当前阶段）→ `MASTER_PLAN.md` → 本阶段对应源码符号；不要重新读全部历史报告。
- 若上下文在阶段中途耗尽：先把实际改动、已跑命令、未决问题写入 `stages/NN.md` 与 `EVIDENCE.md`，再从同一份提示词继续，不要跳到下一阶段。
- 任何契约变化：先改 `CONTRACTS.md` 与 `DECISIONS.md`，再改代码和全部消费者。

## 6. 02 → 03 交接

### 新增/变化的可执行接口

| 接口 | 位置 | 03 如何使用 |
|---|---|---|
| `Guard.ObserveOutcome(started, err)` | `common/resourceguard/rate.go` | 新的创建 seam 在真实工作返回后调用；03 的字节路径不调用它（字节不是创建） |
| `GuardStats.Rate` / `resourceGuard.rate*` | `guard.go` / `service/oomkiller/service.go` | 报告读取；03 不得把字节数与速率账相加 |
| `IsLocalRejection(err)` / `ErrRateLimited` | `common/resourceguard/rate.go` | 03 的队列/字节拒绝也应归为 Local，并复用这个判定；新错误类型加入同一集合 |
| `HeldPermit` 生命周期测试 | `common/tls/marker_scope_test.go`、`protocol/anytls/marker_scope_test.go` | 03 若新增共享任务，必须证明“一个逻辑任务一份许可、上下文不残留” |
| Mieru underlay 准入 | `protocol/mieru/dialer.go` `serverDialer.admit` | 03 的字节反压不得绕过它；Mux/session 在其上仍未覆盖 |

### 03 的边界提醒

- 字节账本按 `CONTRACTS.md` §1.1：在真实排队边界记账，真实消费/丢弃销账；键建议 `byteLedger`，形状由 03 定稿后回写 CONTRACTS。
- 不要把 `len(pending)` 当字节数；XHTTP 的在途切片/共享数组仍会计入容量（01 已记录）。
- 速率账与容量账是两回事：03 的拒绝也必须是本地分类，且不能把速率窗口当成字节背压。

### 未关闭项

| 项 | 状态 | 交接 |
|---|---|---|
| QUIC 共享 offer 准入 | `NOT_ADOPTED_WITH_EVIDENCE` | 设计、成本、重开条件在 `explorations/quic-shared-offer-admission-2026-11-08.md`；03 不依赖它 |
| Cronet 原生连接 | `BLOCKED_EXTERNAL` | 边界在 `explorations/cronet-native-creation-coverage-2026-11-08.md` |
| Mieru 加密握手阶段 | 未覆盖 | 许可只到 socket 打开；若 03 需要按 session 字节计费，先确认该阶段归属 |
| 外部进程改 Apple pin/source-lock | 已收尾 | 外部曾把 `clients/apple` 指到 `97b35d1`（`origin/main` 头）并改 source-lock/ABI 脚本；本阶段未提交，工作树已恢复到父仓 pin `e328310` 且干净。release freeze 若跟进，用 `scripts/ci/release-freeze.py` 重建，不要在未跑 Apple 门禁时直接提交 `VERIFIED_APPLE_SHA` |

### 阅读缓存增量（02）

02 读了：`modules/resource-admission.md`、`modules/mieru.md`（按导航核对）、`protocol/anytls/{admission,outbound}.go`、`common/tls/client.go`、`common/resourceguard/{gate,guard,held}.go`、`service/oomkiller/{service,timer,recorder}.go`、`protocol/mieru/{dialer,fallback,dial_observation,outbound}.go`、pinned `sing-quic@{hysteria2,tuic}/client.go` 的 `completeOffer/offerNew`、pinned `sing-anytls/client.go` 的 ctx 使用、`protocol/naive/outbound.go` 的 Cronet 接点、`release/{dependencies.json,source-lock.json}` 与 `scripts/ci/release-freeze.py` 接口。03 只需补字节路径（XHTTP pending、TUN/UDP 队列、relay）的源码增量，不重读上述文件。

## 7. 03 起点（历史保留；04 用 §8）

| 03 关注点 | 已验证锚点 | 说明 |
|---|---|---|
| XHTTP pending/在途 | `transport/v2rayxhttp/conn.go`：`pending []byte`、`maxPending`、`Write` 的等待路径 | 只读 `len(pending)` 会漏记在途切片；03 需同时记账“容量/已接受未交付” |
| relay/按需扩容 | `transport/v2rayxhttp/client.go`、`protocol/socks` 的 UDP relay 测试 | 具体扩容路径由 03 沿调用链确认，不要按名字猜 |
| 调度/类别 | `common/trafficsched`、`common/trafficclass`；`route/conn.go` 两处 `WithClass` | 字节反压不得与类别/优先级脱节 |
| TUN/UDP 队列 | `protocol/tun`（真实栈在 pinned `sing-tun` 模块） | 局部有界，跨流聚合尚无账本 |
| 03 导航 | `modules/buffer-ownership.md`、`modules/data-path-optimization.md`（+ `mtu-and-geometry.md`） | 先读 AI cautions/Provenance，再沿符号读实现 |

03 不要重建创建/速率账：`Guard`/`RateStats`/`ObserveOutcome` 已就绪，字节路径只接自己的账本与本地拒绝分类。

## 8. 03 → 04 交接

| 事项 | 结果 |
|---|---|
| XHTTP 字节预算 | `packetUplink` 队列任意时刻 ≤ `maxPending`（超大 Write 分块）；`stats()` 暴露 pending/in-flight/retained/高水位/accepted/posted，04 可直接复用为驻留观测的输入 |
| 进程级 `byteLedger` | 未接；04 建驻留账本时定义跨 outbound 聚合入口，XHTTP 作为被聚合者接入（不得把单 flow stats 说成全进程预算） |
| B02/B03/B05 | 证据关闭，重开条件在 `explorations/data-plane-byte-budgets-2026-11-08.md`；04 若先落地驻留/回收反馈，B03 的共享增长额度具备重开条件 |
| B04 | XHTTP 无提前返 window；H2/QUIC 库内缓存不可证，04 若要按 stream 计费需先确认库的窗口所有权 |
| B06 | 能力矩阵在探索报告末节；04 新增 wrapper 时必须补同一矩阵（half-close/partial write/datagram/headroom/关闭 owner） |
| 03 代码提交 | `cd1d8160e3c`（其后为文档提交） |

## 9. 04 批 1 → 批 2 交接

| 事项 | 结果 |
|---|---|
| 已落地代码 | `6c10b42df37` residency owner 盘点；`f523e49ece3` XMUX 池库存；`9a04cc1c1e8` 有界关闭诊断 |
| 新接口 | `adapter.ResidencyReporter`/`ResidencySnapshot`/`ResidencyOwnerCount`；`route.ReferenceManager.ResidencySnapshot()`、`eachReusableOwner`；`adapter.Scope.AddNamed` |
| 报告 | `service/oomkiller` 顶层 `residency`：`measure=owner-reach`，四类 owner 各 `reachableOwners`/`releasableOwners`；无 reporter 时不写键 |
| XHTTP | 池内 `Generation/Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released`；`Opened == Pooled+Draining+Released`；HTTP/2 库内缓存 `NOT_VERIFIABLE_HERE` |
| 关闭诊断 | Scope 一次性 watchdog（`C.StopTimeout`、4 KiB 栈、defer 停表、panic 路径同样停）；Box 5 个 raw cleanup 命名；Box 顶层阶段只写 debug 耗时 |
| 证据 | `EVIDENCE.md` E21；`go build ./...`、route/oomkiller/xhttp/adapter 全套测试、`-race`（`CGO_ENABLED=1` + `<toolchain>` gcc）均通过；CI head `205412ba498` 的 `Verify` run 42484328424 `verify`+`race checks` success |
| 未完成 | R01 其余 owner 与真实终结数；R02–R06、R08 全部；R07 协议级慢关闭/挂起注入与 08 交接；进程级驻留/字节聚合 |
| 唯一下一动作 | 04 批 2：R05 共享任务的独立 waiter、迟到成功与预算归还；复用 02 对 `sing-quic` `completeOffer`/`discarded` 的核实，不新建共享任务框架 |

03 的接口仍有效：`packetUplink.stats()` 是驻留观测输入，`Guard`/`RateStats` 不变；不要重做 04-A 的 owner 枚举。

## 10. 04 批 2 → 批 3 交接

| 事项 | 结果 |
|---|---|
| 已落地代码 | `64cfa9a8630`：`transport/v2rayquic` 共享 pending offer（每个 waiter 自己的 cancel/deadline、dial 用 client 生命周期、Close 丢弃迟到成功一次、失败可重试） |
| 新行为 | `Client.offer(ctx)` 共享创建；`clientConnection.ctx`/`closeConn` 显式生命周期与一次关闭；`keep_session` 每 flow 读取，不写入共享 conn |
| R02 | `ALREADY_PRESENT_VERIFIED`：背景探测拒绝、业务唤醒、证据保留、新鲜度跳过均已有测试并复跑；无新代码；真实 NE 未验 |
| R06 | XHTTP 每 flow 上下文/槽位由既有 dial-ctx/deadline 测试覆盖；v2rayquic `keep_session` 已加测试；MASQUE 仅源审计；Mieru 在 SDK 内未测 |
| 证据 | `EVIDENCE.md` E22；`go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...`、`-tags with_quic` 测试与 `-race` 通过 |
| 未完成 | R03/R04、R01 剩余 owner 与真实终结数、R08、R07 协议级注入；sing-quic/Mieru SDK 共享任务 |
| 唯一下一动作 | 04 批 3：R03/R04（WG 冷启动、并发首用、teardown/重建）与 R01 剩余 owner 盘点，最后 R08 |
