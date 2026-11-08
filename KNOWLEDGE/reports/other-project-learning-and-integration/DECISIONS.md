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

## D-02-01 快速失败速率策略放进 `Guard`，不新建 limiter

- **决定**：`common/resourceguard/rate.go` 在既有 `Guard` 内实现 fast-fail 窗口/退避/抖动/上限；提供 `ObserveOutcome`。
- **依据**：`Guard` 已是进程创建政策 owner，压力和容量都在这里；另起 limiter 会成为第二个“是否允许开始”的真相源，并需要自己的生命周期与观测接线。
- **代价**：`Acquire` 增加一次原子读（无窗口时）；A/B 基准未测到回归。
- **重开条件**：若 03/04 需要按字节/驻留维度联合限速，先扩展本 owner，不并行。

## D-02-02 本地拒绝用类型判定，不用错误字符串

- **决定**：`IsLocalRejection` 识别四个本地错误与两类 ctx 错误；Mieru 在 SDK 扁平化文本后，用 `dialObservation` 保存的类型化错误恢复分类。
- **依据**：本仓已因 Windows 本地化错误文本吃过亏；字符串匹配在跨平台/跨语言下不可靠。
- **代价**：Mieru seam 多写一次 `observeSocketDial`；分类器多一个早返回。
- **删除条件**：不删。

## D-02-03 Mieru 许可放在 `serverDialer`（SDK newUnderlay 接点）

- **决定**：在 `serverDialer.DialContext/ListenPacket` 取许可，返回即释放；Mux/加密 session 在其上，不覆盖。
- **依据**：这是 SDK 真实打开 underlay socket 的唯一仓侧接点；Mux/session 属 SDK 内部，无回调。
- **代价**：只覆盖 socket 打开，不覆盖其后的 Mieru 加密握手；已在 CONTRACTS 注明，不虚报。
- **重开条件**：若 SDK 暴露 session 建立完成回调，再覆盖握手阶段。

## D-02-04 QUIC 共享 offer 的跨仓 hook 本轮不实施

- **决定**：Hysteria2/TUIC/Hysteria 的共享 `completeOffer` 准入留待 fork hook + pin + release freeze，状态 `NOT_ADOPTED_WITH_EVIDENCE`。
- **依据**：仓侧包装（调用方/dialer）都不能覆盖共享任务生命期，是错误形状；正确 hook 需要 fork 提交、`source-lock` 重建，并依赖 TUIC 异步认证/0-RTT 的完成语义，本环境无互通/真机证据。
- **代价**：QUIC 创建风暴仍无准入；已用探索报告记录设计、证据与重开条件，不把接口当完成。
- **重开条件**：`sing-quic` 有新 pin 可用、TUIC 认证完成点可证明、有互通环境。

## D-02-05 Cronet 原生连接不加 per-stream 伪 gate

- **决定**：不因为“要有个接点”就在 `DialEarly` 外包 gate；记录为 `BLOCKED_EXTERNAL`。
- **依据**：Naive 流在共享引擎上复用原生连接，per-stream gate 限的是调用并发而非 native 创建，可能削减健康吞吐并制造错误覆盖的印象。
- **代价**：Cronet 原生创建仍不受 `Guard` 约束；边界报告明确写了控制/不控制。
- **重开条件**：`cronet-go` 暴露原生连接创建/关闭回调，且 native 归档可重建复验。

## D-02-06 速率窗口默认等待、预算不足才拒绝

- **决定**：普通类别等待窗口（可取消），窗口长于调用方 deadline 时返回 `ErrRateLimited`。
- **依据**：立即拒绝会把失败风暴变成调用方的重试风暴；固定 sleep 又会拖慢所有请求。等待 + 预算判断让“慢下来”与“别让调用方白等”同时成立。
- **代价**：窗口期内普通创建有最多 2s 的额外等待；交互类别不受影响。
- **重开条件**：若真实负载显示等待伤害大于收益，改为按比例抽样拒绝，并保留同一计数与分类。

## D-03-01 XHTTP 超大 Write 在队列内分块，而不是短写返回

- **决定**：一次 `Write` 按剩余预算分块追加并等待腾空，返回完整 `n`；队列任何时刻 ≤ maxPending。
- **依据**：旧实现“空队列接受整个 Write”让上限对单次大写入失效；短写虽合法，但仓内既有测试/调用假设完整写入。分块既守住预算又保持调用方契约。
- **代价**：一次超大 Write 的完成时间被 POST 节奏拉长（这正是背压）。
- **重开条件**：若上层明确按短写重试且能证明无数据丢失，可改成短写。

## D-03-02 字节账只做每 flow 快照，进程级聚合留 04

- **决定**：`packetUplink.stats()` 记录 pending/in-flight/retained/高水位/accepted/posted；不新增跨 outbound 的 `byteLedger` 接口。
- **依据**：没有天然的进程级 owner；造一个只有测试消费者的接口违反“已采纳接口必须有生产调用方”。
- **重开条件**：04 建驻留账本时定义聚合入口，XHTTP 作为被聚合者接入。

## D-03-03 B02/B03/B05 证据关闭而非仓促实现

- **决定**：TUN/UDP（子仓 pin）、relay 共享增长额度（需 04 反馈与长流基准）、reF1nd cached 首包（跨 fork pin + 包装矩阵）本轮不实施。
- **依据**：仓侧没有可验证接点/基准；错误形状（调用方包装、短写假设）会带来比不改更大的风险。
- **重开条件**：见 `explorations/data-plane-byte-budgets-2026-11-08.md` 每项。

## D-04-01 `residency` 报告 owner 可达数，资源账留在各 owner

- **决定**：顶层 `residency` metadata 只报四类 owner 的 `reachableOwners`/`releasableOwners`，并带
  `measure: "owner-reach"`。真实 active/idle/draining、generation 与终结数由各 owner 自己产出，后续批次
  逐 owner 接入。
- **依据**：旧 walk 的单个整数被读成资源数；在还没有 owner 级账本时先造“半指标”会重演同一错误。
  纯读快照无副作用，能先回答“谁可被问”，且不改变任何关闭行为。
- **代价**：报告暂不能回答“现在有多少驻留资源”，只能回答“哪些 owner 可被回收”。
- **重开条件**：某 owner 提供真实关闭完成计数后，可在同一键下增加该 owner 的资源字段；不得把可达数改义。

## D-04-02 XHTTP XMUX 用池本地库存与“真实 Close 完成”作为释放确认

- **决定**：池内新增 `Generation/Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released`；
  `Released` 只在底层 `conn.Close()` 真正执行时 +1；`Generation` 是池本地边界计数，不复用
  runtimecoord 的网络代次。
- **依据**：deferred close 让“已退役”与“已释放”分离；把 `closed` 当 released 会谎报终结。该池是
  R01 首个可完全观测的真实 owner，先在此证明计数/不变量，再谈其它协议。
- **代价**：增加一个 `atomic.Uint64` 计数与 detached 列表（按真实 draining 连接数有界，统计/边界时清理）；
  不覆盖 HTTP/2 库内部缓存。
- **重开条件**：若库提供可观测的连接关闭回调，可把库内缓存纳入同一 owner；否则维持
  `NOT_VERIFIABLE_HERE`。若 detached 列表在极端压力下增长，改为带上限的环形记录并保留计数。

## D-04-03 关闭诊断放在 `adapter.Scope`，一次性、有预算、只观察

- **决定**：`Scope.Close` 记录 completed/pending/current 并在超过 `constant.StopTimeout` 时报告一次；
  栈用 4 KiB 缓冲截断；`defer` 停表（含 panic）；不取消/跳过/重排 cleanup，不新建关闭控制器。
  Box 顶层阶段只写 debug 耗时。
- **依据**：drain 是关闭链中唯一无界段，诊断必须与它同层；让 watchdog 改变关闭行为会制造“报告成功
  但没关完”的谎报。`Close` 的完成边界是已测试契约，不能被诊断改义。
- **代价**：all-goroutine 栈在超时瞬间有一次性开销；慢 cleanup 会多一条 debug 行。
- **重开条件**：若 08 需要结构化关闭报告，可把同一记录导出为字段；不得因此让诊断成为调度输入。

## D-04-04 v2rayquic 共享创建采用 pending offer + 每 waiter 上下文

- **决定**：`offer(ctx)` 以首个需求创建 pending，后来者附着；每个 waiter 用自己的 ctx 等待；dial 用 client
  生命周期上下文；`Close` 取消并标记 discarded，迟到成功由 owner 关闭一次。
- **依据**：旧实现在互斥锁内同步 dial，等待者无法用自己的 deadline/cancel 退出，Close 也会被在途 dial
  卡住；`sing-quic` 的同形实现（02 已审计）证明该形状能与现有生命周期共存，不需要新建 singleflight。
- **代价**：多一个 pending 状态与一次 goroutine（dial 本身）；`Close` 不再等待 dial 结束，迟到资源由
  completeOffer 收尾。
- **重开条件**：若某 dialer 不响应 ctx 取消导致 goroutine 滞留，需要在该 dialer 层加硬超时，而不是让
  Close 等待。

## D-04-05 R02 不新增“探测资源”生命周期，用途由调用上下文携带

- **决定**：保持 `adapter.IsBackgroundProbe` 的每次调用标记；不加持久 “probe purpose” 字段，也不为探测
  单独建资源 owner。
- **依据**：现有 URLTest/WG 测试已覆盖背景探测拒绝、业务唤醒、证据保留与新鲜度跳过；探测自身不持有跨阶段
  资源，“探测结束就关”的失败形状在本架构不成立。新增持久用途状态反而会成为第二个身份源。
- **代价**：无法在资源上查询“这是探测还是业务”，只能从调用上下文判断；对 SDK 内部探测（Mieru）不可见。
- **重开条件**：若出现探测建立的长寿命资源（如 SDK 内部 session）需要跨调用复用，才在对应 owner 增加显式
  用途与升级点，并复用同一 context 标记。

## D-04-06 R03/R04 的惰性重型 Runtime 重构不实施，保留现有 suspend/wake

- **决定**：不做 outbound descriptor/runtime 拆分，也不把 WG device 改成 never-built；保留 WG 的
  `SetIdle/resume`、rebind、listen_port 与 teardown。
- **依据**：descriptor 化要改写 `adapter.Outbound`、Provider 创建/替换、group 与 route 遍历；WG never-built
  要重排 TUN/端口/路由/inbound 的启动与失败语义。两者都属跨 owner 对象模型重写，未测到显著收益；现有空闲
  数据面已由 suspend 停下。WG 全套测试通过。
- **代价**：未使用 endpoint 的静态构造成本仍在；大 Provider 库的启动成本未优化。
- **重开条件**：真实大配置测得构造占启动时间/峰值内存显著比例，或大量未用 WG endpoint 的设备构建内存显著；
  届时有界实施并保留现有启动/失败语义。

## D-04-07 R08 不并行关闭 endpoint 子 scope

- **决定**：保留 `Scope.Close` 的串行逆序与错误聚合；不新增并行组。
- **依据**：合成上界（8×10ms：serial 84ms vs parallel 10ms）只在多个慢 endpoint 时成立；`Scope` 没有独立性
  元数据，跨 endpoint 的端口/tun/runtimecoord/全局次序依赖不可从子 scope 推断。强行并行会改完成边界。
- **代价**：多 endpoint 的关闭时间仍线性累加。
- **重开条件**：真实 `Box.Close` 的显著耗时被 endpoint 子 scope 支配，且能为可并行组提供显式依赖声明；
  届时只在已证明独立的组内并行并聚合错误。

## D-05-01 探测队列用确定性优先级 + 老化，不引入第二个调度器

- **决定**：在既有 per-Box `urltest.Coordinator` 内加入交互优先与“每 3 次交互授予服务一个后台 waiter”的
  老化规则；不新增 worker pool、ticker 或第二压力源。
- **依据**：所有真实测量已通过 `Measure` 取槽位，容量上限已在；缺口只是 FIFO 让后台整轮挡住交互探测。
  确定性规则可测试、无计时器、成本一个整数；老化保证后台最终完成。
- **代价**：交互持续繁忙时后台最多延迟 2 个交互授予；队列本身仍按调用方数量增长（每个 waiter 一个小结构）。
- **重开条件**：若 2000/10000 节点回放显示队列/goroutine 峰值仍不可接受，再评估在批任务生成前
  取槽位或专用 worker，而不是扩大本规则。

## D-05-02 每轮探测用固定 worker + 有界交接队列，不用每任务一 goroutine 的 batch

- **决定**：`urlTestBatch` 的生产改用 `protocol/group/probe_batch.go`（10 worker、队列 20），不再用通用
  `batch.Batch` 的“每任务一 goroutine 再等令牌”。
- **依据**：2,000/10,000 节点回放实测旧实现峰值 1,903/9,650 goroutines，全部停在令牌上；新实现峰值 14，
  且每个成员仍被测量、取消 0.1s 排空。栈内存是 NetworkExtension 预算里的真实成本。
- **代价**：每轮固定 10 个 worker 的 goroutine 常驻到 `Wait`；队列满时遍历被阻塞（这正是有界生产）。
- **重开条件**：若未来需要跨轮共享 worker 池以省掉每轮 10 个 goroutine，可在不改变“接受前受界”的前提下
  复用本轮类型；不得退回每任务一 goroutine。

## D-06-01 domain_keyword 只在阈值以上编译，越界回退循环

- **决定**：≥64 keyword 编译冻结自动机；小集合保持逐条 `Contains`；超 pattern/字节/node 预算一律回退，
  不截断规则、不报错。
- **依据**：原型与本次复测显示 1024 keywords 的 miss 从 14.1µs 降到 199ns、late hit 从 18.5µs 降到 184ns；
  但第一项命中原循环仍在几十 ns，低于阈值编译是净损失。阈值是首版保守值，待真实 corpus 校准。
- **代价**：大集合增加一次构建（约 100ns/keyword 量级）与平坦索引内存；越界集合回到 O(host×keywords)。
- **重开条件**：真实 corpus 测得早命中占比高、阈值以下编译仍净收益，或 node 预算需要上调；调整时保留
  “越界回退、语义不变”的合同。

## D-06-02 F02 纯子结果缓存本轮不采纳

- **决定**：不新增规则子结果缓存；保留现有匹配路径。
- **依据**：无真实 corpus 命中率；256 谓词匹配 356ns，模拟 key+查表 55ns，但未计失效/版本/插入/锁竞争，
  且 key 完整性风险高。
- **重开条件**：真实 trace 显示完整 tuple 重复率 >30% 且规则匹配占路由 CPU 显著比例；先做只读、完整 key、
  显式版本的小缓存。
