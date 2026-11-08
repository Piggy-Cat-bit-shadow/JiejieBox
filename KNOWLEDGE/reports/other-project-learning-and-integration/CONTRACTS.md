# CONTRACTS —— 三种账、观测接口与所有权合同

本文件是 01 交给 02–12 的确定合同。**目标：后续阶段不再为三种预算重复搭底座。** 新增字段必须复用这里的所有者和通道；改变语义时必须先改本文件并同步全部消费者。

## 1. 三种账（互不替代，合计不重复）

| 账 | 计量对象 | 现有 owner | 当前观测 | 后续阶段 |
|---|---|---|---|---|
| A. 创建许可 | “昂贵创建的并发数/等待数”，不是内存 | `common/resourceguard.Guard` + `Gate`，由 `service/oomkiller.Service` 安装（仅 NE） | `GuardStats` → 报告 metadata key `resourceGuard` | 02 扩展真实阶段；03/04 消费 |
| B. 排队/积压字节 | 已接收但尚未交付下游的字节 | 各数据面协议本地（XHTTP `pending`、TUN/UDP 队列等），**尚无统一账本** | 无统一导出；局部计数不完整 | 03（B01–B04）建账；不得新建第二个压力源 |
| C. 资源驻留 | 实际占用内存/租约/句柄的对象，跨节点跨代 | `route.ReferenceManager`（idle/suspect walk）、`common/runtimecoord`（epoch/lease）、`Scope` | 只有 walk 次数可推断；**无 active/idle/draining 计数** | 04（R01/R05/R07）建账；01 只定义字段 |

**计费边界：**

- A 的许可在“创建阶段”持有，`Dial`/`Handshake` 返回即释放；不能包装返回连接、持有到会话结束（会改变连接数语义）。
- B 的字节在“进入排队边界”记账、在“被下游真实消费或丢弃”销账；`len(queue)` 不等于字节数，buffer 借用/移交必须按 `buffer-ownership` 的所有权规则。
- C 的驻留从“对象被发布/被租用”起算，到“最后一个引用释放且资源真实关闭”为止；从候选列表移除 ≠ 释放，`draining` 与 `idle` 是不同状态。
- 同一个字节/对象只能进一种账；跨层可以统计但不能重复计入（例如 cache entry 数与 footprint 是两种事实，见 §3）。

### 1.1 三种账的协作草案（`DRAFT_FOR_02`，02 定稿）

02 的第一个交付就是“三种账的协作接口”；这里先给 01 能确认的最小形状，避免 02 重新发明：

| 阶段 | A（创建许可） | B（排队字节） | C（驻留库存） |
|---|---|---|---|
| 真实昂贵工作开始前 | **必须持有**（每个逻辑任务一份） | 不适用（还没有字节） | 不适用（还没有对象） |
| 创建成功、对象发布 | 已释放（返回即释放） | 不适用 | **在此记账**（实例/租约/句柄） |
| 数据进入排队边界 | 不适用 | **在此记账**（容量 + 已接受未交付） | 不适用 |
| 真实消费/丢弃 | 不适用 | **在此销账** | 不适用 |
| 最后一个引用释放/真实关闭 | 不适用 | 不适用 | **在此销账** |

五条协作规则（供 02 确认或修正后生效）：

1. **顺序**：`A →（创建）→ C`；B 在数据面独立于 A/C。拿到 A 之后才允许做昂贵分配；C 在对象发布时记，不允许“先记 C 再创建”。
2. **不得持 A 阻塞等待 B/C**：A 只覆盖创建阶段。若创建后的发布/入队可能阻塞，必须先释放 A 再等待；否则队列满会变成“创建许可被队列占用”，形成跨账死锁。
3. **不得持 C 阻塞等待 A**：驻留对象若要重建底层（例如新 underlay），先释放旧 C 记录或明确“重建中”状态，再取 A；否则旧库存会锁住新创建。
4. **失败顺序**：创建失败 → 只释放 A；发布失败 → 释放 C（若已记）；取消 → 释放已记的 B/C；迟到成功必须由 owner 接纳为真实对象或销毁，不能“账上释放、对象仍活着”。
5. **观测**：三账各有自己的 metadata key（`resourceGuard` / `byteLedger` / `residency`），可以同报告快照并列，**不得相加成单一“总预算”**。任何跨账数字必须标注来源与口径。

02 若证明该顺序不成立（例如某协议必须持 B 才能创建），必须修改本节并同步 03/04，而不是在协议里私开一条顺序。

## 2. 唯一压力源

进程压力事实只有一个：`service/oomkiller.Service.pressure`（`atomic.Uint32`，值域为 `tun.MemoryPressure`）。

- 消费方：TUN 的 `MemoryPressure(ctx)`、`resourceguard.Guard` 的 level 映射（`guard_wiring_test.go` 固定全映射）、回收状态机。
- **禁止**新增第二采样器、第二回收执行器或在协议层自行判断“是否在 NE”。
- `os_proc_available_memory` 只作为**观测/建议**（`ReportStatus.AvailableKnown`、`ObservedBudget`），不得单次采样直接扩张/收紧硬额度；如要作为控制输入，必须有滤波、滞回和保守余量，并在 02 记录。
- GOMEMLIMIT 只约束 Go 运行时托管内存，不等于进程 footprint；两者分列（`policy.go` 注释与 `recorder.timelineRow` 字段）。

## 3. 回收反馈（本轮落地）

### 数据形状

`service/oomkiller/reclaim_observation.go` 的 `reclaimActionRecord`：

| 字段 | 含义 | 事实等级 |
|---|---|---|
| `Kind` | `idle` / `pressure` / `crisis` | 分支事实 |
| `Stage` | `cache` / `pools` / `runtime` / `cache+gc` / `network-reset` / `report-only`（含 `(crisis pending)`） | 分支事实 |
| `At` | 动作完成时间（走 `adaptiveTimer.clockNow()`） | 采样 |
| `BeforeBytes` / `AfterBytes` / `GainBytes` | 动作前后 footprint 采样与差值（floor 0） | **归因到动作的信用值，不是 malloc/RSS 精确归因** |
| `CacheEntriesRemoved` + `CacheEntriesKnown` | L1 真实删除条目数；`-1/false` 表示该 pass 没有这个计数 | 真实对象计数 |
| `LowYield` | 是否未达到收益门槛（与 `finishIdleReclaim` 的决策同源） | 决策事实 |
| `CooldownUntil` | 若触发冷却，冷却到期时间 | 决策事实 |
| `Mode` | `policyMode.String()`（`memory_limit`/`network_extension`/`available`/`none`） | 环境事实 |

聚合（`reclaimObserver`）：`actions`、`idleActions`、`pressureActions`、`crisisActions`、`lowYieldActions`、`totalGainBytes`、`coolingDown`、`cooldownRemainingMs`、`recent`（**固定 8 条**，最旧在后）。

### 写入点与消费方

| 写入点 | 何时 | 语义 |
|---|---|---|
| `adaptiveTimer.finishIdleReclaim` | L1/L2/L3 任一结束（含 crisis pending 提前结束） | 一个 idle pass 一条 |
| `adaptiveTimer.runPressureReclaim` | Darwin critical 通知的 `releaseMemory` 前后 | 一个 pressure pass 一条 |
| `adaptiveTimer.runCrisisReclaim` | 重置/报告完成后（含被 armed 线跳过的情况**不**记录） | 一个 crisis 动作一条 |

`Service.startTimer` 在 `start()` 之前把 observer 挂到 timer；`Service.reclaimMetadata` 通过 `Recorder.SetExtraMetadata("memoryReclaim", …)` 写入报告 metadata。**observer 没有任何调度能力**，不得新增 timer/goroutine/回调去“自动优化”。

### 报告通道规则

- `metadata.json`：低频、有界、快照式事实；键必须是顶层、加性、客户端不认识时被忽略（`Recorder.mergeExtraMetadata` 已固定：非 object 文档原样返回，客户端键不移动）。
- `events.jsonl`：只记改变状态的事件（start/stop/state/reset/snapshot/report）。**不为每个 reclaim pass 追加事件**——那会让报告随会话增长。
- `timeline.jsonl`：按采样间隔的行，包含 footprint、available、Go 托管内存、goroutine、连接数、GOMEMLIMIT 账户；不得在其中逐包/逐流写行。

现有键：`resourceGuard`（准入）、`memoryReclaim`（回收）。**键名稳定，不改形状**；形状需要变化时新增兄弟键并在本文件登记。

## 4. 后续阶段的接口草案

### 02（预算与真实高成本创建）

- 复用 `Guard`/`Gate`/`HeldPermit`；新增真实阶段（QUIC/0-RTT、Cronet 引擎、Mieru underlay）时，准入必须发生在**创建前**、许可随创建返回释放。
- 观测扩展：只向 `Gate.Stats`/`GuardStats` 加字段，并同步 `guardMetadata`；不得在协议包内自建计数器作为第二真相源。
- 若引入“创建速率/退避”，必须复用同一 owner（`Guard`）并说明与 `limit`/`queue`/`wait` 的关系。

### 03（字节预算与反压）

- 字节账本必须由**实际排队边界**的 owner 记账（XHTTP pending、TUN/UDP 队列、relay 扩容），并在真实消费/丢弃时销账。
- 观测：低频有界聚合，复用 metadata 通道（建议键名 `byteLedger`，形状在 03 开工时定稿并回写本文件）；不得逐包采样。
- 只读 `len(pending)` 会漏记在途切片/共享数组，03 必须同时记“容量”和“已接受未交付”。

### 04（驻留与生命周期）

- `residency` 观测必须由**能真实关闭资源的 owner** 产出：每个 generation 的 active/idle/draining、本次 walk 真实终结数、关闭完成与耗时。
- 现有 `route.ReferenceManager.retireIdleResources` 只返回 keeper 数，不能直接当资源数；04 需要新增能力接口（例如让 keeper 返回释放计数）并在 `CONTRACTS.md` 更新。
- 关闭 watchdog 的“pending/阶段/耗时”必须复用既有 `Scope`/`reclaim` 屏障，不得新开第二关闭控制器。

### 08（诊断/解释/桥接）

- 被动解释直接读 `resourceGuard`、`memoryReclaim`、未来的 `residency`/`byteLedger` 快照；不得为诊断另建采样器、不得在解释路径 DNS/拨号。
- 报告中的 footprint 收益是动作信用值，不是分配归因；解释文案必须保持这个限定。

### 12（价值/真机）

- 主机基准与设备证据分级记录；`memoryReclaim` 的 `GainBytes` 不能当“节省的 RSS”；`resourceGuard` 的 `maxInflight` 不能当吞吐结论。

## 5. 测试接缝（生产默认无侵入）

| 接缝 | 位置 | 默认 | 用途 |
|---|---|---|---|
| `Guard.useClock` / `Gate.now` | `common/resourceguard` | `time.Now` | 证明滞回/老化，不 sleep |
| `adaptiveTimer.now` | `service/oomkiller` | `time.Now` | 证明 30 s 低收益冷却（simulated clock） |
| `sampleFunc` / `readUsage` | `adaptiveTimer` | 真实 `memory.Total()` | 精确控制 trigger 与每层收益 |
| `trimCaches` / `trimMemory` / `freeOSMemory` / `releaseNetwork` | `adaptiveTimer` | 真实 capability | 分层动作与失败路径 |
| `runtimeReclaimable` | `adaptiveTimer` | `/memory/classes/heap/free` | L3 门控 |
| `cleanupHook` / `afterDecisionHook` | `adaptiveTimer` | nil | 确定性交错（stop/关闭） |
| fake dialer / 假 guard | `protocol/anytls`、`common/tls` 现有测试 | — | 不建真连接验证准入语义 |

所有接缝只在测试中赋值；生产路径是一串 nil 检查，未新增 goroutine/timer/全局单例。

## 6. 不变量核对（01 触及面）

- **A1/A2（Scope 与 epoch）**：未改；回收仍走既有 executor 与 `releaseAccess`。
- **A6/A7（缓冲区所有权）**：未触及数据面。
- **本阶段新增约定**：报告 metadata 只加顶层键；回收记录不改变执行顺序/冷却判定；`trimCaches` 的返回值来自既有 `CacheMemoryReleaser` 合同，不改变其实现。
