# MASTER_PLAN —— 覆盖矩阵与本轮状态

本表是 `00_完整覆盖矩阵.md` 55 项在目标仓库内的执行账。**所有方向都要有结论，只有已采纳方案要求生产代码闭环。** 状态取值：`PENDING`、`IMPLEMENTED_VERIFIED`、`ALREADY_PRESENT_VERIFIED`、`NOT_ADOPTED_WITH_EVIDENCE`、`BLOCKED_EXTERNAL`。证据等级见 `EVIDENCE.md`。

## 当前阶段与实测 revision

| 项 | 值 |
|---|---|
| 阶段 | 05（P01–P04、D01–D02）——**IN_PROGRESS**（P01 首批：交互优先 + 后台老化队列；其余未开始，见 `stages/05.md`） |
| 02 结束 revision | `afdd3b3cf7c` |
| 04 进入时 HEAD | `a6f1ca55f5455fdaf88d41b030579ac1b41119df`（= `origin/testing`，无需同步） |
| 03 代码提交 | `cd1d8160e3c` |
| 03 阶段结束 HEAD | 以 `git log -1` 与 `stages/03.md` 为准 |
| 04 批 1 代码提交 | `6c10b42df37`（residency owner 盘点）、`f523e49ece3`（XMUX 池库存）、`9a04cc1c1e8`（有界关闭诊断） |
| 04 批 1 HEAD | 以 `git log -1` 与 `stages/04.md` 为准（文档提交后回填 `DOCUMENT_INDEX`） |
| 04 批 2 代码提交 | `64cfa9a8630`（v2rayquic 共享 offer + 测试） |
| 04 批 2 HEAD | 以 `git log -1` 与 `stages/04.md` 为准 |
| 05 阶段基线 | `0dfb5e41734`（04 收尾 = `origin/testing`，工作树干净） |
| 05 P01 首批代码提交 | `f59f339ab396df4839a11f32536e9e308e3b81b8`（Coordinator 交互优先/老化 + 测试） |
| 知识库覆盖目标 | v0.1.44 专题目标 `3b6ec64f7145a2add99656d47caaa4f22a1cc478`；广泛基线 `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`（v0.1.34），本轮不移动 |
| 03 触及区域的知识增量 | `transport/v2rayxhttp`（packet-up 预算）；其余 B 项见 `explorations/data-plane-byte-budgets-2026-11-08.md` |

## 55 项覆盖矩阵

| ID | 范围 | 主责 | 类型 | 状态 | 证据 / 备注 |
|---|---|---:|---|---|---|
| S01 | 参考项目、关联依赖、客户端锁文件与闭源边界的来源核验 | 01 | I | `IMPLEMENTED_VERIFIED` | `SOURCES.md`：remote-verified HEAD、目标 pin/source-lock/子仓锁、oixCloud 闭源边界；`VERIFIED_GIT` |
| G01 | 唯一压力来源、资源观测、实际回收收益反馈与平台内存口径 | 01 | I | `IMPLEMENTED_VERIFIED` | 见下节“G01 范围拆分”；调用图/锁序见 `ANALYSIS.md`；代码 + 单测 + race + 负载定义，`VERIFIED_SOURCE`/`VERIFIED_UNIT` |
| G02 | 预算三类所有权、层级守恒、公平性与本地资源错误分类 | 02 | I | `IMPLEMENTED_VERIFIED` | `resourceguard.IsLocalRejection` + Mieru `FailureFacts` 接点（预算拒绝不判节点）；公平/预留/老化/队列/等待上限沿用并复核；`CONTRACTS.md` §1.1 三账协作 |
| G03 | TLS/AnyTLS 与 QUIC 真实共享创建、异步认证和 0-RTT 阶段准入 | 02 | I | `IMPLEMENTED_VERIFIED`（TLS/AnyTLS）；QUIC 子项见右 | TLS/AnyTLS 许可生命周期与 `HeldPermit` 作用域已审计并加 `marker_scope_test.go`；QUIC 共享 offer 的跨仓 hook 本轮 `NOT_ADOPTED_WITH_EVIDENCE`→[探索报告](explorations/quic-shared-offer-admission-2026-11-08.md) |
| G04 | Cronet 引擎初始化、原生连接创建和复用的真实覆盖 | 02 | I | `BLOCKED_EXTERNAL` | 引擎/原生连接在 `cronet-go` 内，29 个 native 模块 `controlled`、本仓无 Chromium 工具链；不把 Start gate 冒充 native 覆盖→[边界报告](explorations/cronet-native-creation-coverage-2026-11-08.md) |
| G05 | Mieru Runtime、Mux 新 underlay 与重建路径的真实准入 | 02 | I | `IMPLEMENTED_VERIFIED` | `serverDialer` 在 SDK 的真实 newUnderlay 接点接 `Guard`（TCP/UDP），`HeldPermit` 防嵌套重复；`admission_test.go` 证明并发上限与标记 |
| G06 | 快速失败的创建速率、退避、嵌套许可和请求类别继承 | 02 | I | `IMPLEMENTED_VERIFIED` | `Guard` 内 fast-fail 窗口 + 指数退避/抖动/上限 + 交互豁免；三处 seam 回报结果；`rate_test.go` + `resourceGuard` metadata 新增 rate 字段 |
| B01 | XHTTP pending/in-flight/大 Write/共享数组容量的有界记账 | 03 | I | `IMPLEMENTED_VERIFIED` | 超大 Write 分块入队不越界；pending/in-flight/retained/高水位/accepted/posted 快照 + 测试；进程级 `byteLedger` 聚合留 04 |
| B02 | TUN、UDP 解析嗅探转发、控制队列与跨 Flow 聚合边界 | 03 | I | `NOT_ADOPTED_WITH_EVIDENCE` | TUN 栈在 `sing-tun` pin、MASQUE 队列矩阵需 04 生命周期；见探索报告 |
| B03 | relay 按需扩容共享预算、回落及压力联动 | 03 | I | `NOT_ADOPTED_WITH_EVIDENCE` | `route/conn.go` copy loop 的按 flow 阈值保留；共享额度需 04 反馈与长流基准 |
| B04 | 真实消费才返窗口额度、慢流反压与共享控制帧进展 | 03 | I | `ALREADY_PRESENT_VERIFIED`（XHTTP） | `packetConn.Read` 无预取/提前返 window；H2/QUIC 库内缓存标 `NOT_VERIFIABLE_HERE` |
| B05 | reF1nd 缓存首包 read waiter/batch 适配及完整包装链 | 03 | I | `NOT_ADOPTED_WITH_EVIDENCE` | 跨 sing fork pin + 包装矩阵；保留 experiments 数据与重开条件 |
| B06 | 传输抽象的半关闭、部分写、datagram 与所有权能力契约 | 03 | I | `IMPLEMENTED_VERIFIED` | 未新增 wrapper；XHTTP packetConn 能力矩阵逐项列于探索报告 |
| R01 | 跨节点跨代 active/idle/draining 驻留计费与真实终结 | 04 | I | `IMPLEMENTED_VERIFIED` | 采纳范围：`residency` owner 盘点（`measure=owner-reach`）+ XHTTP XMUX 池库存/真实 Close 释放确认。H2/QUIC 库内缓存 `NOT_VERIFIABLE_HERE`，Mieru SDK `BLOCKED_EXTERNAL`，WG 由 R04 覆盖 |
| R02 | 纯探测会话短驻留、业务升级与实际路径资源归属 | 04 | I | `ALREADY_PRESENT_VERIFIED` | 探测用途由每次调用的 context 标记（`adapter.IsBackgroundProbe`），悬挂 endpoint 拒绝背景探测、业务 dial 通过 gate；证据保留与新鲜度跳过已测。见 `stages/04.md` 批 2、E22；真实 NE 未验 |
| R03 | 稳定节点描述符、重型协议惰性 Runtime 与可回收目录 | 04 | I | `NOT_ADOPTED_WITH_EVIDENCE` | `DISPROPORTIONATE_REFACTOR`：descriptor/runtime 拆分跨 outbound/Provider/group/route 对象模型；未测到显著收益。见 `explorations/lazy-heavy-runtime-2026-11-08.md` |
| R04 | WG 惰性设备、完整 teardown/重建与驻留库存预算 | 04 | I | `ALREADY_PRESENT_VERIFIED` | 现有 suspend/wake/rebind/listen_port/teardown/并发关闭均通过 WG 全套测试；never-built 惰性 device 不采纳（同报告） |
| R05 | 共享任务独立等待者、取消与晚到成功资源接纳/丢弃 | 04 | I | `IMPLEMENTED_VERIFIED` | v2rayquic 共享 pending offer（独立 cancel/deadline、Close 丢弃迟到成功一次、失败可重试）+ 测试；sing-quic HY2/TUIC 复用 02 源证据；Mieru SDK 未测 |
| R06 | 共享隧道内部 underlay 身份与首个业务流上下文隔离 | 04 | I | `IMPLEMENTED_VERIFIED` | XHTTP 每 flow 上下文/槽位（既有测试）、v2rayquic `keep_session` 每 flow、external 标记按 caller ctx 读取；MASQUE/Mieru 库内部分未加运行测试 |
| R07 | 关闭 pending/阶段耗时/有预算堆栈 watchdog | 04 | I | `IMPLEMENTED_VERIFIED` | `adapter.Scope` 一次性有界诊断（completed/pending/current、耗时、4 KiB 栈、defer 停表）+ Box 阶段耗时 + AddNamed；协议级慢关闭注入留 08 |
| R08 | 独立 endpoint 子 scope 有界并行关闭的收益与依赖验证 | 04 | E | `NOT_ADOPTED_WITH_EVIDENCE` | 合成上界 8×10ms serial 84ms vs parallel 10ms；Scope 无独立性元数据、跨 endpoint 依赖不可推断。见 `explorations/endpoint-close-parallel-2026-11-08.md` |
| P01 | 全 Box 有界探测生产/队列/worker 与交互公平调度 | 05 | I | `PENDING` | 批 1：`common/urltest.Coordinator` 补交互优先 + 每 3 次交互授予老化一个后台 waiter + 取消清理，`Measure` 按 probe origin 选队列；剩余 2000/10000 饱和回放与各触发入口逐一核对 |
| P02 | 选中/故障候选优先、分轮全覆盖、精确去重与新鲜度 | 05 | I | `PENDING` | — |
| P03 | 显式省电 failover/稳定槽位、退化确认与健康作用域 | 05 | I | `PENDING` | — |
| P04 | procfs 同 UID 合并、按需路径、多 owner 与缓存提交代次 | 05 | I | `PENDING` | — |
| D01 | DNS 组请求预算、选举 token、输家取消与 survival 协调 | 05 | I | `PENDING` | — |
| D02 | DNS 共享查询 key、响应条件、旧结果与解析所有权保护 | 05 | I | `PENDING` | — |
| F01 | 大 domain_keyword 编译自动机、小集合回退与规则语义 | 06 | I | `PENDING` | 包内原型 50,000 次差分通过；生产未接 |
| F02 | 纯规则子结果的版本化缓存评估及正确 key | 06 | E | `PENDING` | — |
| F03 | Provider/规则下载解析构建的瞬时预算、延期与旧结果 | 06 | I | `PENDING` | **G01 的“构建占用”字段由本项承接** |
| F04 | 流式文件产物、兼容 manifest、mmap 租约与崩溃恢复 | 06 | I | `PENDING` | — |
| F05 | 应用侧预编译与扩展加载兼容协议的落地评估 | 06 | E | `PENDING` | — |
| C01 | 轻量 ConfigDocument/Analysis 与完整校验分层 | 07 | I | `PENDING` | — |
| C02 | 复用 schema/registry/平台事实生成生效与能力报告 | 07 | I | `PENDING` | — |
| C03 | Provider 部分更新与提交状态 ApplyReport | 07 | I | `PENDING` | — |
| C04 | ChangePlan 按域执行、依赖闭包和多种 revision/epoch | 07 | I | `PENDING` | 01 核实 epoch 与资源身份分离已存在于 runtimecoord |
| C05 | 常驻管理面、候选验证、last-good、恢复与依赖文件快照 | 07 | I | `PENDING` | — |
| C06 | 配置 Operations、模块依赖包和有限 IR 组织方式 | 07 | E | `PENDING` | — |
| O01 | 基于真实 evaluator 的无副作用 DNS/Route Explain | 08 | I | `PENDING` | 01 交付的 `memoryReclaim`/`resourceGuard` 是 08 的事实来源之一 |
| O02 | 主动分阶段诊断、根因/no_ack/reused/findings 与有界队列 | 08 | I | `PENDING` | — |
| O03 | DNS 结构化事件、订阅门控与实际路径归因 | 08 | I | `PENDING` | — |
| O04 | 有预算历史、top-K/分页/指标、敏感维度与能力发现 | 08 | I | `PENDING` | — |
| O05 | 复用原生连接事件、reset/closed/掉事件重同步和兼容适配 | 08 | I | `PENDING` | — |
| O06 | UTF-8/typed-nil 错误桥接、错误链与 ABI 版本兼容 | 08 | I | `PENDING` | — |
| X01 | QUIC 流式 detour 超长 datagram 与内核截断语义契约 | 09 | I | `PENDING` | — |
| X02 | QUIC 关闭真实中断底层读、deadline 和建连锁契约 | 09 | I | `PENDING` | — |
| X03 | MTU 三种量、协议承载能力与现有多跳/路径图协调 | 09 | I | `PENDING` | — |
| X04 | TrustTunnel/AWG3/REALITY/XHTTP/VLESS/路由器嗅探等剩余扩展逐项评估 | 09 | E | `PENDING` | — |
| X05 | MITM/脚本/整栈重写与 Ghostlane 单 Go Runtime 经验的取舍 | 09 | E | `PENDING` | 01 已记录 Ghostlane 只作边界证据，不作实测数字 |
| M01 | 闲置维护 timer 合并、生命周期普查和回收反馈 | 09 | E | `PENDING` | 01 的回收反馈是 M01 的输入 |
| M02 | hotfix 退役条件、上游同步、平台 tag/schema/依赖来源锁 | 09 | I | `PENDING` | 01 核实 `release/source-lock.json` 与 `dependencies.json` 的 intent/derived 分工 |
| M03 | 必要客户端/桥接/配置入口实际接通及发行档位验证 | 09 | I | `PENDING` | — |
| V01 | 账本边界、输入放大、失败注入与测试检出能力 | 10 | I | `PENDING` | 10 专项 |
| V02 | 跨机制状态交错、资源身份和整系统协调攻击 | 11 | I | `PENDING` | 11 专项 |
| V03 | 协议互通、性能/内存价值、真机证据与最终全项闭环 | 12 | I | `PENDING` | 12 专项；真机/互通仍须独立证据 |

## G01 范围拆分（为什么不是“所有观测都已导出”）

01 的实现范围是**唯一压力源 + 观测通道 + 实际回收收益反馈 + 平台内存口径**：

1. **唯一压力源**：`service/oomkiller.Service.pressure` 是进程压力事实；TUN 的 `MemoryPressure`、准入 guard 的 level、回收状态机都读它。01 未新增采样器。
2. **观测通道**：`Recorder.SetExtraMetadata` 的顶层 key（`resourceGuard` 已有；本轮新增 `memoryReclaim`），由拥有该事实的包提供，客户端 schema 不变。
3. **回收收益反馈**：`reclaimObserver` 记录每次 idle/pressure/crisis 动作的 kind、stage、前后 footprint、收益、真实 cache 条目数、低收益判定与冷却到期；行为仍由既有 `lowYield → cooldown` 驱动。**新增记录不构成第二回收控制环**：observer 不能启动/停止/重排任何 pass。
4. **平台内存口径**：`ReportStatus.ObservedBudget`/`AvailableKnown`/timeline 的 GOMEMLIMIT 记账已在既有实现中，本轮只核实并用测试固定，未改成控制输入。

**仍不具备**的两类字段按覆盖矩阵归后续 owner，不在 01 造“半指标”：

- `active/idle/draining` 跨代数量、真实终结、关闭完成 → **R01/R07（04）**。现有 `route.ReferenceManager.retireIdleResources` 返回的是“访问到的 keeper 数”，把它当资源数或终结数会误导。
- 排队字节/构建占用 → **B01–B04（03）/F03–F04（06）**。01 只固定“必须复用同一条 recorder 通道、低频有界聚合、不逐包采样”的合同。

## 阶段顺序

01（现状与观测）→ 02（创建与预算合同）→ 03（可积压字节与能力）→ 04（驻留、惰性、共享、关闭）→ 05（探测/选路/进程/DNS）→ 06（构建/规则/产物）→ 07（配置应用/恢复）→ 08（诊断/事件/桥接）→ 09（协议边界与全范围收口）→ 10–12（Debug 与真机验收）。

后阶段可以回修前阶段；回修时必须同步更新本表、`CONTRACTS.md`、`DECISIONS.md` 与相关模块，不允许绕开错误另加全局单例。

