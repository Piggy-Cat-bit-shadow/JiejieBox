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

### 1.1 三种账的协作草案（`ADOPTED_IN_02`，顺序未改）

02 的第一个交付就是“三种账的协作接口”；下面这版是 01 提出、02 确认可执行的最小形状（02 未修改顺序，只补了本地拒绝分类与快速失败速率）：

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

## 3.1 快速失败与本地拒绝分类（02 落地）

### 数据形状

`resourceguard.Guard` 在容量账之外新增一条**独立**的速率账（`RateStats`）：

| 字段 | 含义 |
|---|---|
| `Delay` | 当前对新普通创建施加的等待（窗口未激活时为 0） |
| `Windows` | 连续快速失败触发退避窗口的次数 |
| `Delayed` | 真正等待过窗口的创建数 |
| `Refused` | 窗口超过调用方剩余预算而被本地拒绝的数 |
| `FastFailures` | 达到“快速失败”证据门槛的失败数 |
| `Recovered` | 清除 streak/窗口的成功数 |

`GuardStats.Rate` 与 `GuardStats.Gate` 并列，报告 `resourceGuard` 增加
`rateDelayMs`、`rateWindows`、`rateDelayed`、`rateRefused`、`rateFastFailures`、`rateRecovered`。
**速率拒绝与容量拒绝分开计数**：报告必须能区分“limit 挡住了”与“现在开始也是浪费”。

### 规则

1. **只在连续快速失败后启用**：同一 seam 上连续 `RateLimitAfter`（默认 4）次失败且每次耗时
   `< FastFailWindow`（默认 250ms）才开窗；成功清零并重置退避级别。
2. **退避有界、带抖动**：从 `RateLimitBase`（默认 100ms）指数增长到 `RateLimitMax`（默认 2s），
   每窗口 ±`RateLimitJitter`（默认 20%）随机；不会无限增长，也不会让所有重试同步。
3. **交互类别豁免**：`ClassInteractive` 不等待速率窗口（但仍受容量 gate 约束），保证有人等待的
   流不被后台节点的失败风暴拖慢。
4. **等待可取消、有预算**：窗口短于调用方 deadline 时按窗口等待（受 ctx 取消）；窗口长于剩余
   预算时立即返回 `ErrRateLimited`，不制造一个注定超时的等待。
5. **本地拒绝 = Caller/Local**：`resourceguard.IsLocalRejection(err)` 覆盖
   `ErrQueueFull`/`ErrWaitTimeout`/`ErrRateLimited`/`context.Canceled`/`context.DeadlineExceeded`。
   消费方（当前是 Mieru 的 `classifyDialError`）必须把它归为 Local/Caller，**不得**据此判节点死亡、
   切换传输或触发重连风暴。SDK 把 underlay 错误扁平化成文本时，靠 `dialObservation` 里保存的
   带类型错误恢复分类，不做字符串匹配。

### 接线点（生产调用方）

| seam | 取许可后回报结果 |
|---|---|
| `protocol/anytls/admission.go` `dialWithAdmission` | `guard.ObserveOutcome(started, dialErr)` |
| `common/tls/client.go` `handshakeWithAdmission` | `resourceguard.FromContext(ctx).ObserveOutcome(started, dialErr)` |
| `protocol/mieru/dialer.go` `serverDialer.DialContext/ListenPacket` | `d.guard.ObserveOutcome(started, dialErr)`，并把拒绝错误写入 `observeSocketDial` |

**未接线的真实创建**（QUIC 共享 offer、Cronet 原生连接）见 `MASTER_PLAN.md` 的 G03/G04 与
`explorations/` 的两份报告；不得在没有 hook 时用调用方包装冒充覆盖。

## 3.2 字节账（03 落地，XHTTP）

### 容量定义（`transport/v2rayxhttp` packet-up）

| 量 | 定义 | 备注 |
|---|---|---|
| `maxPending` | `clamp(2 × sc_max_each_post_bytes, 64 KiB, 8 MiB)` | 每 flow 的排队预算 |
| `PendingBytes` | 已接受、未交给 POST 的逻辑字节 | 队列长度，非数组容量 |
| `InFlightBytes` | 已出队、仍被一个 POST 持有的切片 | 与 Pending 相加才是真实保留 |
| `RetainedCapacity` | `cap(pending)` | 数组保留容量，单独报告，**不是 footprint** |
| `AcceptedBytes` / `PostedBytes` | 接受/成功 POST 的累计 | 失败字节不记 posted |
| 高水位 | pending/in-flight 的最大值 | 回答“最坏时刻” |

### 转移与释放时序

`Write`（可多次分块，任何时刻 pending ≤ maxPending）→ `AcceptedBytes += n` → flusher 取走
`payload`（`InFlightBytes = len`，pending 减少）→ POST 成功（`PostedBytes += len`，in-flight 归零）
或失败（字节随失败丢失、不记 posted，`fail(err)` 唤醒所有写者）。

### 尚未做的

进程级 `byteLedger` 聚合未接：当前没有跨 outbound 的 owner 入口，04 建驻留账本时一起定；
在此之前不得把单 flow 的 stats 说成全进程字节预算。其余积压点（TUN/UDP、MASQUE、relay 增长）
见 `explorations/data-plane-byte-budgets-2026-11-08.md` 的重开条件。

## 3.3 驻留账（04 批 1 落地）

### `residency` metadata：owner 可达数

| 项 | 值 |
|---|---|
| 写入通道 | `Recorder.SetExtraMetadata("residency", …)` 顶层加性键；无 reporter 时不写；客户端既有键不变 |
| 生产者 | `adapter.ResidencyReporter`，唯一实现 `route.ReferenceManager`（`Start` 中按接口键注册） |
| 形状 | `measure: "owner-reach"`；`outbounds`/`endpoints`/`dnsTransports`/`httpClients` 各含 `reachableOwners`、`releasableOwners` |
| 语义 | **owner 可达/可释放数**。不是 active/idle/draining，不是连接/stream/session/字节数，也不是终结数；retire 调用成功不证明资源已释放 |
| owner 枚举 | `route.ReferenceManager.eachReusableOwner`：两个 retire walk 与快照共用；新增 kind 未映射到报告字段时 panic，而不是静默消失 |

### XHTTP XMUX 池库存（包内，未导出）

| 量 | 定义 |
|---|---|
| `Generation` | **池本地**复用边界计数（`RetireSuspect` +1），不是 runtimecoord 的网络代次 |
| `Pooled` / `Active` / `Idle` | 池内连接；Active = 有 stream，Idle = 无 stream（trim 可回收） |
| `Draining` / `DrainingOldGeneration` | 已 detach、仍有 stream、teardown 延迟到最后一个 stream 释放的连接；旧代子集 |
| `Opened` / `Released` | 生命周期计数；`Released` 只在底层 `conn.Close()` 真正执行时 +1，一次连接最多一次 |
| 不变量 | 每个状态点 `Opened == Pooled + Draining + Released` |
| 边界 | HTTP/2 库内部 idle 缓存 `NOT_VERIFIABLE_HERE`；不覆盖 H2/QUIC/AnyTLS/Cronet/Mieru/WG |

### 每类真实 owner 的驻留观测边界（04 结案）

| owner | 可观测事实 | 边界 |
|---|---|---|
| XHTTP XMUX | 池库存/代次/真实 `Close` 完成（`transport/v2rayxhttp` 的 `stats()`） | HTTP/2 库内 idle 缓存 `NOT_VERIFIABLE_HERE` |
| v2rayquic | 共享 conn 的 streams/一次 close；waiter 独立 | 无 idle 池账，连接级即可 |
| WireGuard | device suspend/wake、rebind、listen_port、Close（R04） | 冷/热设备成本未测（`BLOCKED_BY_ENVIRONMENT`） |
| AnyTLS | 每次连接/marker scope；关闭由 `Scope` | 无复用池，无需 resident 计数 |
| Mieru | SDK runtime/mux | `BLOCKED_EXTERNAL`（SDK 内） |
| Cronet/Naive | 引擎/原生连接 | `BLOCKED_EXTERNAL`（G04） |
| H2（v2rayhttp 等） | 连接生命周期由库管理 | `NOT_VERIFIABLE_HERE` |

### 关闭诊断（`adapter.Scope`）

- drain 记录 total/completed/current；watchdog 默认 `constant.StopTimeout`，每 drain **一次性**，超时输出
  owner/阶段、已用时、pending 与 4 KiB 预算的 all-goroutine 栈；`defer` 停表（panic 路径同样停）。
- 纯诊断：不改 `Close` 结果、错误过滤、清理顺序或并发 `Close` 汇合；不新建生命周期控制器。
- `Scope.AddNamed` 为 raw cleanup 命名，`Scope.Start` 自动命名 component close；`Box` 顶层阶段只写一条
  debug 耗时，不参与决策。

## 3.4 共享创建与迟到成功（04 批 2 落地，v2rayquic）

| 项 | 值 |
|---|---|
| 入口 | `transport/v2rayquic.Client.offer(ctx)`：首个需求创建 pending，后来者附着同一 pending |
| 等待者 | 每个 waiter 用自己的 ctx select `pending.done` / `ctx.Done()`；取消/超时只结束该 waiter |
| dial owner | client 生命周期上下文（`NewClient` 派生、`Close` 取消）；**不使用**首个 flow 的 ctx |
| 发布 | `completeOffer` 在 `connAccess` 下发布：成功进 `c.conn`；失败置 `pending.err`；新 pending 替换前旧的已完成 |
| 丢弃 | `Close` 标记 `discarded` 并取消；迟到成功由 owner 调 `closeConn` **一次**关闭，不发布 |
| 关闭确认 | `clientConnection.closeConn` 用 `sync.Once`；确认来自真实结束点 |
| 会话元数据 | `adapter.KeepSessionFromContext` 在 `DialContext` 内按 flow 读取，保存在该 flow 的 stream 闭包，不写入共享 conn |
| 边界 | sing-quic HY2/TUIC 的 pending 是库内实现（02 源审计）；Mieru 在 SDK 内；两者不属于本仓合同 |

## 3.5 探测队列与交互公平（05 P01 首批）

| 项 | 值 |
|---|---|
| owner | `common/urltest.Coordinator`（per-Box，box.go 以 `C.URLTestConcurrency` 注册到 ctx） |
| 入口 | `Measure` 在解析 target 之后、启动探测 deadline 之前取一个槽位；所有真实测量都经此入口 |
| 上限 | `Acquire`/`AcquirePriority` 共享同一 limit；`<=0` 表示不协调 |
| 次序 | 交互 waiter 优先于已排队后台；连续 3 次交互授予后强制服务最早的后台 waiter（`priorityAgingEvery`） |
| 积压预算 | 排队等待不计入探测 timeout；deadline 只从拿到槽位后开始 |
| 取消 | waiter 用自身 ctx 退出，锁内移除；授予与取消同时发生时槽位归还；释放闭包幂等 |
| 优先级来源 | 只读 04 的 `adapter.IsBackgroundProbe(ctx)`（`runtimecoord` probe origin），不新增第二来源 |
| 每轮生产 | `protocol/group/probe_batch.go`：固定 10 worker + 容量 20 交接队列；队列满时遍历阻塞，任务在被接受前受界；`Wait` 幂等、首错取消本轮 |

## 3.6 domain_keyword 编译匹配（06 F01 预置）

| 项 | 值 |
|---|---|
| 触发 | `NewDomainKeywordItem` 在 len(keywords) ≥ `domainKeywordCompileThreshold`（64）时尝试编译 |
| 结构 | Aho–Corasick，构建后冻结为平坦 `nodes`/`edges` 列；只读，无锁 |
| 回退 | <64、>4096 pattern、总字节 >1 MiB、节点 >65,536：`matcher=nil`，走原 `strings.Contains` 循环；回退不丢规则 |
| 语义 | host 只 `ToLower` 一次；keyword 字节精确（不再小写）；空 host=false；空 keyword=任意非空 host；不复制 Anywhere tier/最长后缀 |
| 消费方 | route/DNS/headless 的 `NewDomainKeywordItem` 不变；`String()` 不变 |

## 3.7 配置轻量分析（07 C01 首批）

### 02（预算与真实高成本创建）—— 已完成

- 复用 `Guard`/`Gate`/`HeldPermit`；TLS/AnyTLS 的许可生命周期与嵌套去重已加测试。
- 观测扩展：`RateStats` + `resourceGuard` 的 rate 字段；未另建计数器。
- 速率/退避由 `Guard` 自己拥有（`rate.go`），协议 seam 只回报结果，不实现策略。
- Mieru 在 SDK 的 `newUnderlay` 接点接入同一 `Guard`；QUIC 共享 offer 与 Cronet 原生连接按
  `explorations/` 的报告处理（前者证据性不实施，后者外部阻断）。
- 03/04 接手时：字节账本/驻留账本仍按本节 §1.1 的顺序与 key 约定，不得与速率账相加成单一预算。

### 03（字节预算与反压）

- 字节账本必须由**实际排队边界**的 owner 记账（XHTTP pending、TUN/UDP 队列、relay 扩容），并在真实消费/丢弃时销账。
- 观测：低频有界聚合，复用 metadata 通道（建议键名 `byteLedger`，形状在 03 开工时定稿并回写本文件）；不得逐包采样。
- 只读 `len(pending)` 会漏记在途切片/共享数组，03 必须同时记“容量”和“已接受未交付”。

### 04（驻留与生命周期）—— 批 1 已落地，其余待做

- 已落地合同见 §3.3（`residency` owner-reach、XMUX 池库存、Scope 关闭诊断）。
- 仍待建：每个真实 owner（H2/QUIC/AnyTLS/Cronet/Mieru/WG、TUN/UDP）的 active/idle/draining、
  generation、在途 dial、stream/业务引用与**真实终结数**；进程级驻留/字节聚合入口（B03 重开条件）；
  真实终结计数须由 owner 的关闭完成点产出，而不是由 retire walk 计数。
- `route.ReferenceManager.retireIdleResources` 的返回值继续只是 keeper 可达数；不得改读为资源数。
- 关闭诊断已有 Scope 一处；后续 owner 的关闭完成字段接同一记录，不新开第二关闭控制器。

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
