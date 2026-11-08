# DECISIONS —— 01 的采纳、调整与否决

格式：编号 / 决定 / 依据 / 代价 / 删除或重开条件。只记录会影响后续阶段的决定；一般实现细节见代码注释。

## D-01-01 回收反馈进 metadata 的有界环，不逐 pass 写 events.jsonl

- **决定**：`reclaimObserver` 用 8 条固定环 + 累计计数，经 `Recorder.SetExtraMetadata("memoryReclaim")` 进 `metadata.json`。
- **依据**：`events.jsonl` 是“改变状态的事件”流；一次 idle pass 可以每秒发生而不改变任何状态。逐 pass 追加会让报告随会话增长，且 metadata 只在事件/快照时重写，成本低。现有 `reset` 事件继续承载破坏性动作的状态事实。
- **代价**：报告只能看到最近 8 条动作明细；更早的只有累计值。
- **重开条件**：若 08/12 需要逐动作取证（例如证明某次尾延迟对应某次 GC），为 reclaim 增加有界分段文件（固定条数上限 + 轮转），仍不得无界。

## D-01-02 保留 L1 的真实 cache 条目数，并区分“未知”与“零”

- **决定**：`trimCaches` 从 `func(context.Context)` 改为 `func(context.Context) int`，把 `adapter.CacheMemoryReleaser.TrimCaches` 已返回的条目数带到记录（`CacheEntriesRemoved` + `CacheEntriesKnown`）。
- **依据**：这是唯一真实的“对象终结”计数；`-1/false` 与 `0/true` 是不同事实，若都写 0 会在报告里制造不存在的事实。
- **代价**：5 处测试 fixture 与 1 处字段类型调整；无生产调用方改动（`route.NetworkManager.TrimCaches` 早已返回 int）。
- **删除条件**：不删。若未来 L2/L3 也能给出真实计数，增加同名字段而不是改语义。

## D-01-03 01 不实现 active/idle/draining 驻留计数

- **决定**：不在 01 用 `route.ReferenceManager.retireIdleResources` 的返回值拼一个“资源数/终结数”指标。
- **依据**：该返回值是“被访问到的 keeper 数”，不是被关闭的资源数；把它当驻留或终结会形成误导性的第二真相源。覆盖矩阵已把该字段交给 R01/R07（04），04 需要让 keeper 返回真实释放计数。
- **代价**：01 的观测面在“驻留”一栏仍为空；`MASTER_PLAN.md` 明确记录归属，不留隐形 TODO。
- **重开条件**：04 实现 `residency` 时更新 `CONTRACTS.md` 与本表；在此之前任何报告不得声称有驻留计数。

## D-01-04 保留 8 条上限，不做可配置环大小

- **决定**：`reclaimActionRingSize = 8` 常量。
- **依据**：够覆盖一次 cache→pools→runtime 阶梯和若干重复 pass；可配置会引入第二个“报告 schema 参数”，收益不足。
- **重开条件**：04/08 证明需要更长窗口时，随有界分段文件方案一起改。

## D-01-05 给回收执行器加时钟接缝，而不是接口

- **决定**：`adaptiveTimer.now func() time.Time`，nil 时 `time.Now`；`clockNow()` 供 cadence/记录使用。
- **依据**：30 s 冷却必须在模拟时间上可证，不能 sleep；接口化会让每次采样多一层间接且无其他实现者。与 `resourceguard.Guard.useClock` 保持同一模式。
- **代价**：每次 poll 多一次 nil 检查（每秒一次，非热路径）。
- **删除条件**：不删；若未来有生产级可注入时钟需求，再抽到 `common/clock` 并同步 guard。

## D-01-06 负载定义用测试（打印指标）+ 微基准，不新建 harness 二进制

- **决定**：`common/resourceguard/load_definition_test.go`、`service/oomkiller/reclaim_load_definition_test.go` 固定参数并 `t.Logf` 结果；`BenchmarkGateAcquireRelease*` 记录微基准。
- **依据**：本阶段要的是可重放、可比较的负载形状与当前基线；单独二进制会形成第二套运行入口，且 CI 不会执行。测试参数固定，后续改动可直接对照。
- **代价**：负载测试进入默认 `go test`（admission burst 约 14 ms，reclaim 循环 < 1 ms），可接受。
- **重开条件**：若 12 需要跨机/跨时段的性能对照，再在 `docs/PERFORMANCE-COMPARISONS.md` 的既有体系里记录，不复制第二套结论。

## D-01-07 不采纳“第二压力采样器/独立内存预算平台”（明确边界）

- **决定**：不因参考项目（oixCloud 慢读取/旧 H3 池线索、Ghostlane 单 Go Runtime 经验、LX 的 lazy/resident 库存）而新建第二个内存预算控制器、采样器或常驻管理面。
- **依据**：本仓已有唯一压力源、OOMKiller、ResourceGuard、`runtimecoord`、`Scope` 与 `ReferenceManager`；参考项目的历史设备数字不是本项目实测。01 的观测通道足够让 02–04 在同一 owner 上扩展。
- **代价**：某些参考机制（例如动态 available-memory 控制）暂不落地。
- **重开条件**：02 在真实设备上证明现有 owner 无法表达预算层级，且新机制能复用同一压力源与执行器；否则按 `00_性能取舍` 的“特别大且收益不相称”走 `NOT_ADOPTED_WITH_EVIDENCE`，写入 `explorations/`。

## D-01-08 报告键加性、无版本号、不改客户端 schema

- **决定**：`memoryReclaim` 作为顶层兄弟键加入 metadata；不修改 `ReportStatus`/客户端文档结构，不引入键版本。
- **依据**：`Recorder.mergeExtraMetadata` 的既有合同：非 object 文档原样返回、客户端键不移动、未知键被旧读者忽略。schema 的权威在客户端侧，核心不应复制。
- **代价**：键形状一旦发布就应稳定；变化需新键。
- **删除条件**：不删。
