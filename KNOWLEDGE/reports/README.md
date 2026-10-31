# 历史报告：审计、验证与发布记录

本目录保存项目在各个时间点形成的架构审计、专项验证、发布收尾和客户端调查报告。它们集中归档于此，方便按时间、审阅对象和主题追溯。

## 如何使用历史报告

> **报告记录的是当时检查过的事实，不能直接当作当前源码的结论。**

- 知识库描述的源码版本由 `BASELINE.md` 确定；当前知识的入口是 `INDEX.md` 和 `modules/`。报告不能覆盖源码或这些入口文档。
- 报告与源码不一致时，以源码为准，并记录差异及其原因。例如，`convergence/POST_REFACTOR_AUDIT_2026-10-17.md` 记录了后续审计发现的回归和对先前报告的更正。
- 新增报告不等于刷新知识库，也不会移动生产基线。
- 引用报告时使用其中标明的证据等级；未注明等级的内容，最多按 `VERIFIED_SOURCE` 对待。工作流声明不代表实际运行。后续独立核实的 CI 运行记录只适用于其对应提交和作业。
- `-race`、设备行为与跨平台验证的范围以各报告为准；因环境限制未运行的检查应记为 `BLOCKED_BY_ENVIRONMENT`，不能当作通过。

## 目录结构

| 目录 | 内容 |
|---|---|
| `convergence/` | 架构收敛、后续对抗审计及专项审计 |
| `core/` | 核心包、构建发布配置及知识库增量审计；以 `_` 开头的文件是工作笔记 |
| `release/` | 发布阶段、分支收敛、交接与收尾记录 |
| `windows-client/` | Windows 客户端安装、卸载、诊断和共存调查 |
| `anywhere-round2/` | Anywhere 第二轮研究及 DNS、规则集、MASQUE、QUIC 所有权相关记录 |
| `anywhere-round3/` | Anywhere 第三轮审计、实现和调试交接 |
| `tun-fast-path/` | TUN Fast Path 的价值评估与验收记录 |

## 报告索引

已纳入版本控制的报告使用提交日期，后来归档的报告使用文件记载日期。表中“当时有效”指报告审阅时的状态，不表示今天仍然有效；后续改动可能已经取代其中的结论。已知替代关系会保留在索引中，使用时请对照当前模块和源码。

### convergence/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `ARCHITECTURE_CONVERGENCE_2026-10-17.md` | 2026-10-17 | 11 个提交的架构收敛：构造期确定 dialer 能力、每次操作只观察一次网络、旁路与用户态行为一致、L3 生产对象图测试，以及 `NetworkManager` 增长上限。审计后**已更正**：D10/D11 描述最终设计，保留被取代方案供追溯 | 更正后当时有效 |
| `POST_REFACTOR_AUDIT_2026-10-17.md` | 2026-10-17 | 对抗审计：发现一处生产回归（outbound 响应仓库级能力接口，改变了 WireGuard bind），一处未覆盖目标路径的修复，以及一个仅靠直接设置字段验证的守卫 | 当时有效 |
| `POST_REFACTOR_AUDIT_ADDENDUM_2026-10-17.md` | 2026-10-17 | 汇总审计提出的覆盖缺口、fail-open / fail-closed 清单和 `common/runtimecoord` 边界结论 | 当时有效 |
| `RULE_SET_LIFECYCLE_AUDIT.md` | 2026-10-17 | 规则集生命周期专项审计：所有权、重载清理、updater/watcher/goroutine 退出和 GeoIP/GeoSite 可达性。当时未发现缺陷，新增七项调用链测试 | 当时有效 |

### core/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `_root_test_audit.md` | 2026-10-14 | 根包测试整理审计，只读 | 历史记录 |
| `_boxdd_audit.md` | 2026-10-14 | `experimental/boxdd` 在 `8a008c5d02d2` 的审计 | 历史记录 |
| `_ci_reaudit.md` | 2026-10-14 | `ci-release` 在 `8a008c5d02d2` 的复审 | 历史记录 |
| `_kb_delta_map.md` | 2026-10-14 | 知识库增量 `a267e99e0 -> 5fc4f9366` | 已由后续基线取代 |
| `HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md` | 2026-10-17 | `42222237d` 上的大配置成本：最高 100 万域名的规则集对象成本（稳定态 8.3 MiB、加载 42.5 MiB）、加载瞬态分解、冷节点普查（每个 2,035 B 且不创建线程）、出站静态与运行时及后台 worker 清单；据此让乐观 DNS 刷新和 dashboard 下载遵循 `common/power`。 | 当时有效 |
| `HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` | 2026-10-17 | `e092ecec7` 的对抗审计：两个休眠门控及 closed 等状态、拒绝后的在途注册、等待循环退出和工厂接线；将 DNS governor 提前到构造期解析；5 项变异、热路径 A/B 与 `-race` 的 `BLOCKED_BY_ENVIRONMENT`。 | 当时有效 |
| `NETWORK_CAPABILITY_TRANSITION_PHASE01_2026-10-18.md` | 2026-10-18 | 四阶段网络能力切换的第一阶段，目标 `f868a8fb4`：事件图、单一能力来源、generation 与稳定性语义、转换矩阵（含同接口路由变化不推进 generation）、逐流捕获、familyHealth、恢复与缓存边界、热路径基准。19 项变异中 2 项原先存活，新增测试后可被检出；记录 7 项发现，含一项 Darwin 中风险；未修改生产文件。 | 当时有效 |
| `CANDIDATE_ADMISSION_FLOW_LIFECYCLE_PHASE02_2026-10-18.md` | 2026-10-18 | 第二阶段，目标 `c449256be3`：逐流 `admissionPolicy` 与入口单次捕获、生命周期和拒绝规则、保留的五处阶段门控，以及两个互不比较的 generation 计数器。修复未连接数据包的域名分支未执行策略的问题；15 项变异中 11 项被检出、3 项等价、1 项初版无效后已修正；`admits` 基准为 11–22 ns、零分配，动态问题交至第四阶段。 | 当时有效 |
| `CANDIDATE_ADMISSION_FLOW_LIFECYCLE_PHASE03_DEBUG_2026-10-18.md` | 2026-10-18 | 第三阶段，目标 `4e86eae6df7`：在所有者接口处统计捕获次数，发现并修复串行分支重复捕获 `captureAdmissionPolicy()` 的问题；记录 §3 前提更正、epoch 变化与生命周期取消的关系、200 对 × 6 阶段一致性矩阵、4 项均被检出的新变异，以及已覆盖范围与交给第四阶段的五项风险。 | 当时有效 |
| `NETWORK_CAPABILITY_TRANSITION_PHASE04_FINAL_2026-10-18.md` | 2026-10-18 | 第四阶段及四阶段验收，目标 `d385f5dff17`：说明本环境可验证的确定性阶段和未运行的 `-race`、真实系统事件顺序、1,000 流压力测试；记录真实消费者的 IPv4-only→双栈→同 epoch 下能力变化、Settling、familyHealth 重定键、100 次切换的 goroutine 数量、23 节证据映射、作者修正的四项判断，以及一个已取消 late candidate 仍启动但无影响的未决项；最终验收列出五项不作保证的范围。 | 当时有效 |
| `NAIVEPROXY_DEEP_AUDIT_2026-10-20.md` | 2026-10-20 | `7e42ca38e` 的 Cronet 与 Naive 审计：原生 Chromium 为 154.0.8037.49；按 CONNECT 选择协议；不可达 UDP 上强制 H3 约 4 秒失败；记录空闲 Engine 成本、绕过 DNS 策略的 DoH bootstrap、与 Caddy forwardproxy 的 padding 对照和 Go 服务端 `KEEP` 结论。互操作及 WAN 为 `NOT_RUN`。 | 当时有效 |
| `NAIVEPROXY_PERFORMANCE_INTEGRATION_IMPLEMENTATION_2026-10-20.md` | 2026-10-20 | 实现轮次 `11c4c1b27`→`0ef38b8ff`：P1 完成 CONNECT 握手并映射 `FailureFacts`，修复 407 未被识别的问题；P3 加入默认关闭的逐 CONNECT H3→H2 回退；P9 增加零 padding 回归测试；P7 修正文档默认值；P2 为 `BLOCKED_BY_NATIVE_API`。CONNECT 握手的 loopback 成本为 +0.519 ms。 | 当时有效 |
| `NAIVEPROXY_PERFORMANCE_INTEGRATION_DEBUG_2026-10-20.md` | 2026-10-20 | 该轮对抗调试与验收：H1–H17 回退攻击矩阵（含逐 CONNECT 选择、探测超时、407、乱序、64 并发失败和网络切换）、`h3ProbeContext` 预算切分及 NetError 与 teardown 分类顺序修复、verdict 的 `-race` 测试。结论为 `READY_WITH_LIMITATIONS`，WAN、macOS 和 Alt-Svc 未验证。 | 当时有效 |
| `DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md` | 2026-10-20 | `b721b988f`（v0.1.21）双栈跨组件只读审计及远端增量测量：准入、调用图、状态所有权、DNS/FakeIP、候选调度、地址族健康、生命周期、29 个出站/端点和外部实现对照。记录 1 项 `CONFIRMED_BUG`（流式 hostname 路径丢失本地地址族拒绝原因，并由仓库外探针复现）、2 项 `HIGH_CONFIDENCE_RISK`（组未转发候选规划能力、恢复流 producer 未等待退出）、7 项 `ALREADY_FIXED`；当时在 MSYS2 mingw gcc 16.2.0 下运行 `-race` 并通过。 | 当时有效 |
| `DUAL_STACK_CROSS_COMPONENT_CLOSURE_2026-10-20.md` | 2026-10-20 | 收尾复核：DS-01 在审计基线不可复现且已正确；测量并保留 DS-02 串行拨号；通过三个解析器矩阵确认 DS-03 producer 已等待退出；修复 SSH 未加锁关闭，并记录 17 个 `InterfaceUpdated` 实现、DS-04 尝试次数和 DS-05 边界。 | 适用于当时交付版本 |
| `DS03_RECOVERY_PRODUCER_CLOSURE_2026-10-20.md` | 2026-10-20 | DS-03 专项收尾：literal 与 parallel-network 恢复链的所有权表，以及配合、慢速配合和忽略取消三类解析器下 `LookupFamilies` goroutine 的测量；结论为 `NO_PRODUCTION_CHANGE`，并说明契约边界。 | 适用于当时交付版本 |
| `STARTUP_VALIDATION_COMPAT_CONSTRUCTION.md` | 2026-10-20 | 启动校验兼容性：能力不足告警后继续启动，缺失成员、身份冲突及无法兑现的承诺仍拒绝启动；记录 17 个检查范围、触发场景、测试和限制。 | 适用于当时交付版本 |
| `STARTUP_VALIDATION_COMPAT_DEBUG.md` | 2026-10-21 | 启动校验改造的对抗调试：START/WARN/RUNTIME 攻击矩阵、仍为致命错误的场景、已修复的消息缺陷及运行时 / CI 限制。 | 适用于当时交付版本 |
| `NAIVEPROXY_PERFORMANCE_READONLY_AUDIT_2026-10-20.md` | 2026-10-20 | `5b46c36d3` 的原生端到端性能复审：Chromium 154 客户端与 Go 服务端在 loopback 各测 5 轮并校验载荷。H2 为 457.7 MiB/s、5.5 CPU·s/GiB；H3 为 158.2、17.0；内核直连为 868.7，裸 TCP 为 3285.7。每 32 MiB 有 523 次写入和 523 次 flush；单个 H2 session 服务六次 CONNECT；短请求 814 req/s，p50 1.02 ms。Engine 取 2 时为 665 MiB/s；冷 H3→H2 回退约 4 秒。发现 `TestNaiveSelfInsecureConcurrency` 仍使用旧 NetLog ID 249（固定版本为 267），因此尚未验证连接池隔离；另有历史数字过期的文档缺陷。服务端结论 `KEEP / STOP`。 | 当时有效 |

### release/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `APPLE-PARENT-FINALIZE-REPORT.md` | 2026-10-11 | Apple 父仓库收尾 | 历史记录 |
| `B_GIT_EVOLUTION_REPORT.md` | 2026-10-12 | Android fork 的 Git 演进与来源 | 历史记录 |
| `BRANCH_CONSOLIDATION_TO_TESTING_FINAL.md` | 2026-10-12 | 将分支收敛到 `testing` | 历史记录 |
| `CORE_0.1.6_FINAL_CLOSURE_REPORT.md` | 2026-10-12 | 0.1.6 集成收尾 | 历史记录 |
| `GROUP_CROSSKIND_BRIDGE_CLOSURE.md` | 2026-10-12 | 组回退、跨类型依赖与 bridge 收尾 | 历史记录 |
| `ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` | 2026-10-12 | 路由感知的出站能力校验 | 历史记录 |
| `CI_RELEASE_POST_GREEN_OPTIMIZATION_REPORT.md` | 2026-10-14 | CI 通过后的构建与发布优化 | 历史记录 |
| `PHASE1_INTEGRATION_REPORT.md` | 2026-10-16 | 第一阶段拓扑审计、合并与收尾 | 历史记录 |
| `PHASE1_INTEGRATION_REPORT_ADDENDUM.md` | 2026-10-16 | 最终提交、发布情况与 CI 限制 | 历史记录 |
| `PHASE2_MERGED_STATE_AUDIT.md` | 2026-10-16 | 第二阶段合并状态审计与回归 | 历史记录 |
| `PHASE3_COMBINATION_DEBUG.md` | 2026-10-16 | 第三阶段集成树组合调试 | 历史记录 |
| `PHASE4_ACTIONS_RELEASE_AUDIT.md` | 2026-10-16 | 第四阶段自 v0.1.10 起的 CI / Release 差异审计 | 历史记录 |
| `PHASE5_BISECT_BLOCKED.md` | 2026-10-16 | 发布身份约束导致安装器二分排查受阻 | 历史记录 |
| `PHASE5_GATE6_DIAGNOSIS.md` | 2026-10-16 | 通过对照实验将 gate 6 问题定位为产品回归 | 历史记录 |
| `PHASE5_RELEASE_HELD.md` | 2026-10-16 | 发布在发布前门禁处暂停 | 历史记录 |
| `PHASE5_V0.1.15_CORE_FIX_STATUS.md` | 2026-10-16 | v0.1.15 内核吞错修复已合入，门禁仍失败 | 历史记录 |
| `PHASE5_V0.1.15_HANDOFF.md` | 2026-10-16 | v0.1.15 交接 | 历史记录 |
| `PHASE5_V0.1.15_STATUS.md` | 2026-10-16 | v0.1.15 状态 | 历史记录 |
| `PHASE5_V0.1.15_UNINSTALL_VERDICT.md` | 2026-10-16 | 经测量确认卸载等待问题属于产品缺陷 | 历史记录 |
| `PHASE5_FINISH_ASSESSMENT.md` | 2026-10-17 | 发布收尾：gate 6 对比受到混杂因素影响，挂起现象真实存在 | 历史记录 |
| `V018_RELEASE_HANDOFF.md` | 2026-10-14 | v0.1.8 发布交接 | 历史记录 |
| `V0.1.16_BRANCH_CONSOLIDATION_RELEASE_CLOSEOUT.md` | 2026-10-17 | v0.1.16 分支收敛与发布收尾 | 历史记录 |
| `LX_BUG_ORACLE_ROUND2_ADDENDUM.md` | 2026-10-15 | LX bug-oracle round 2 addendum | 历史记录 |

### windows-client/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `WINDOWS_AUDIT_STATUS.md` | 2026-10-16 | Windows 客户端审计及交接状态 | 历史记录 |
| `WINDOWS_AUDIT_P0_STATUS.md` | 2026-10-16 | P0-1 至 P0-4 状态，其中一项确认为真实缺陷 | 历史记录 |
| `WINDOWS_AUDIT_P04_STATUS.md` | 2026-10-16 | P0-4 加固已实现，但受更严格的补丁解析器阻塞 | 历史记录 |
| `WINDOWS_AUDIT_P04_COMPLETE.md` | 2026-10-16 | P0-4 已完成，并在生成的 NSIS 中验证 | 历史记录 |
| `WINDOWS_AUDIT_DIAGNOSTIC_CHANNEL.md` | 2026-10-16 | 卸载器未写入内容，文件大小不增长 | 历史记录 |
| `WINDOWS_AUDIT_CHANNEL_FIXED.md` | 2026-10-16 | 诊断通道已修复，但卸载器仍无输出 | 历史记录 |
| `WINDOWS_AUDIT_FIRST_UNINSTALL.md` | 2026-10-16 | 首次卸载跟踪：选项已传入、`customUnInit` 已调用，但没有写入 | 历史记录 |
| `WINDOWS_AUDIT_UNINSTALLER_PROVENANCE.md` | 2026-10-16 | 卸载器来源记录，假设尚未测试 | 历史记录 |
| `WINDOWS_COEXISTENCE_SUBSCRIPTION_DEBUG_REPORT.md` | 2026-10-15 | Windows 共存与订阅流量：定向调试 | 历史记录 |
| `WINDOWS_COEXISTENCE_SUBSCRIPTION_FINAL_REPORT.md` | 2026-10-15 | Windows 共存与订阅流量：交付报告 | 历史记录 |
| `WINDOWS_COEXISTENCE_FINAL_CLOSEOUT_REPORT.md` | 2026-10-15 | Windows 共存收尾 | 历史记录 |

### anywhere-round2/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `README.md` | 2026-10-19 | 本轮索引、依赖 pin 和交付内容 | 当时有效 |
| `anywhere-second-round-research.md` | 2026-10-19 | 独立审计：DNS 缓存所有权、TUN→QUIC 数据路径、QUIC ACK/重传、队列与预算、大型规则集加载，以及 A/B/C/D 分级 | 历史记录（描述 `ccc2812e45d`） |
| `anywhere-round2-construction.md` | 2026-10-19 | 实现矩阵：A0/A1/B2–B8、提交、前后测量与回滚 | 适用于当时交付版本 |
| `anywhere-round2-debug-audit.md` | 2026-10-19 | Red→Green 证据、对抗与跨组件测试，以及 race / 平台限制 | 适用于当时交付版本 |
| `anywhere-round2-final-summary.md` | 2026-10-19 | 提交、依赖 pin、测量结果、CI 链接和剩余风险 | 适用于当时交付版本 |

### anywhere-round3/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `README.md` | 2026-10-20 | 本轮索引：被实测否定的方案、值得实现的方向和未运行的检查 | 当时有效 |
| `anywhere-round3-independent-deep-audit.md` | 2026-10-20 | `5064f20a9` 上的独立审计：测量 TCP `PacketTemplate`、XHTTP gather 轮询、HY2/TUIC 批处理和 TUN 批次分组四个候选；记录两个被否定的前提、两个 A 级候选（`portReturn.ReturnPackets` 每次调用的分配、L4 CONNECT-UDP 缺少 owned DATAGRAM 路径）、两个确认缺陷（`equalFoldASCII` 大小写折叠基值、`domain_keyword` 未转小写）、`ForwardStage.Flush` 持锁阻塞入队的活性风险、七项自主发现、微基准记录与后续方案。 | 仅适用于 `5064f20a9`，**不代表后续 HEAD** |
| `anywhere-round3-construction.md` | 2026-10-20 | 实现记录：A3-1 `ReturnPackets`（2 → 0 次分配，以及针对修改前代码的 Red→Green 验证）、A3-2 L4 owned DATAGRAM 和真实 HTTP/3 端到端测试、关闭路径依赖图及其两个边界测量、P3，以及被否决的 batch-owned 方案与测量结果 | 适用于当时交付版本 |
| `anywhere-round3-debug-handoff.md` | 2026-10-20 | 独立对抗审计交接：改动、命令、危险边界、可疑路径、依赖版本、CI 链接和未决事项 | 适用于当时交付版本 |
| `anywhere-round3-close-liveness-final.md` | 2026-10-20 | 关闭活性反例尝试：禁用空闲超时的对端、单向断链、测量时间线、缺少本端 ACK 时入站流量无法持续的原因，以及已提交的机制测试 | 适用于当时交付版本 |
| `anywhere-round3-debug-report.md` | 2026-10-20 | 验收记录：关闭等待的实测边界、阻塞端口后排队的操作、L4 所有权矩阵复核及剩余风险的解除条件 | 适用于当时交付版本 |

### tun-fast-path/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `README.md` | 2026-10-24 | 本轮索引：快照、文件、结论与限制 | 仅适用于 `32c37219c59` |
| `tun-fast-path-value-review.md` | 2026-10-24 | D0 价值评估、路径矩阵、`fast_path` 优先级和决策 | 仅适用于 `32c37219c59` |
| `tun-fast-path-adversarial-debug.md` | 2026-10-24 | 对抗验收、具体判定与发现记录 | 仅适用于 `32c37219c59` |

## 新增报告

1. 按主题放入相应目录，并在文件名中写明审计内容和日期：正式报告使用 `SUBJECT_YYYY-MM-DD.md`，工作笔记使用 `_subject.md`。
2. 在上表记录报告、日期、审阅版本、覆盖范围和已知替代关系；不要把历史报告标成对未来任意版本都有效。
3. 报告与模块结论冲突时，应修正模块或在报告中记录冲突，不要让两者无说明地并存；见 `AGENTS.md` 工作规则 1。
4. 新增报告不会移动 `BASELINE.md`，也不等同于知识刷新。
