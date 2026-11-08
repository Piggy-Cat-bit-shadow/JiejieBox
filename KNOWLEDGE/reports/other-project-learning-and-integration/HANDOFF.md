# HANDOFF —— 01 交接给 02

本文件给下一阶段可直接使用的 API/owner/调用点、阅读缓存与未确认事项。**新上下文先读本文件与 `stages/01.md`，再按 `KNOWLEDGE/INDEX.md` 的导航补本阶段源码增量。**

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
| 增量范围 | `fc7e400…(v0.1.39)..HEAD` 内 `resourceguard`/`oomkiller`/`tls`/`anytls` 无生产代码变更 |

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

## 2. 01 交付给 02 的接口（可直接使用）

| 事项 | 位置 | 约定 |
|---|---|---|
| 进程压力 | `Service.pressure` / `Service.MemoryPressure()` / `oomkiller.MemoryPressure(ctx)` | 02 不得新增采样器；需要新信号时先改 owner |
| 准入 | `resourceguard.FromContext(ctx)`、`Guard.Acquire(ctx, class)`、`Permit.Release/Wait` | 许可必须在创建返回时释放；不得包装返回连接 |
| 嵌套标记 | `resourceguard.WithHeldPermit/WithHeldPermit(ctx)` | 跨 detour/代理链只持一份 |
| 类别 | `common/trafficclass.WithClass` / `FromContext` | interactive/realtime 才用预留；02 若扩展类别需同步两入口 |
| 观测 | `GuardStats` + `guardMetadata`（`resourceGuard`）；`reclaimObserver` + `reclaimMetadata`（`memoryReclaim`） | 只加性扩展；不新建报告通道 |
| 回收执行器 | `adaptiveTimer` 的 `reclaimWake/pendingReclaim/inFlightReclaim`；`finishIdleReclaim`、`runPressureReclaim`、`runCrisisReclaim` | 02 若在创建路径触发回收，只能 enqueue，不能自建执行器 |
| 时钟/接缝 | `adaptiveTimer.now`、`sampleFunc`、`readUsage`、`trimCaches`、`trimMemory`、`freeOSMemory`、`releaseNetwork`、`runtimeReclaimable` | 测试专用赋值；生产默认真实实现 |

## 3. 02 开工清单（建议顺序）

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
| 本提交的 CI | `NOT_RUN` | 本地结果不等于 CI；如需 `VERIFIED_CI` 必须给运行号与 SHA |
| 平台标签构建 | `NOT_RUN` | 只跑了默认 `go build ./...` |
| 客户端消费 `memoryReclaim` | `UNKNOWN` | 客户端 schema 不需要它；是否有 UI/诊断消费由 08/客户端决定，核心不假设 |
| 客户端 `clients/android` 本地分支 | 已收尾 | 工作树已恢复到父仓 pin `c783710`（`git status` 干净）。原先检出的本地分支 `fix/libbox-stringbox-callsites`（tip `9d15717`，含 2 个仅本地提交）**未被删除**；需要时 `git -C clients/android checkout fix/libbox-stringbox-callsites` 恢复。02 不需要该分支的代码 |

## 5. 恢复执行方式

- 新上下文：读本文件 → `stages/01.md` → `MASTER_PLAN.md` → 本阶段对应源码符号；不要重新读全部历史报告。
- 若上下文在 02 中途耗尽：先把实际改动、已跑命令、未决问题写入 `stages/02.md` 与 `EVIDENCE.md`，再从同一份 02 提示词继续，不要跳到 03。
- 任何契约变化：先改 `CONTRACTS.md` 与 `DECISIONS.md`，再改代码和全部消费者。
