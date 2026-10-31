# 双栈系统 + 全内核跨组件架构一致性：只读深度审计报告

> **归档记录（2026-10-20）**：本文正文在"先不提交、不推送"的只读指令下完成；随后按用户的显式指令归档进 `KNOWLEDGE/reports/core/` 并推送。正文里"未 commit / 未 push / 仅未跟踪文件"的描述记录的是**审计当时**的工作树状态，不是归档状态；归档提交见 `git log -- KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md`。

| 字段 | 值 | 复现命令 |
|---|---|---|
| 仓库 | `Piggy-Cat-bit-shadow/sing-box` | `git remote -v` |
| 审计分支 | `testing` | `git branch --show-current` |
| **审计 SHA（工作树 HEAD）** | `b721b988fefec4a4da3b25ad320fa6a1cad8a7ee` = tag `v0.1.21` | `git rev-parse HEAD` / `git rev-parse v0.1.21^{commit}` |
| 审计时远端 tip | `56c550a9c613d24714d2466fb96f6d02c343c63b`（`origin/testing`，本地落后 44 个提交） | `git log -1 --format=%H origin/testing` |
| HEAD → 远端 tip 的生产代码增量 | **6 个 `.go` 文件**，全部在 `protocol/naive`、`protocol/masque`、`transport/http`、`option/`；**`common/dialer` / `common/netcap` / `route/` / `dns/` 零改动** | `git diff --name-only HEAD origin/testing -- '*.go' ':(exclude)*_test.go'` |
| 上一个 release tag | `v0.1.21`（即本 HEAD）；`v0.1.20` … `v0.1.17` 为历史 | `git tag --sort=-creatordate` |
| 工作树状态（审计开始） | clean（`git status --porcelain` 空） | `git status --short --branch` |
| 工作树状态（审计结束） | 仅新增本报告一个未跟踪文件；**未 stage、未 commit、未 push**（按用户本轮指示） | `git status --porcelain` |
| Go 工具链 | `go1.25.5 windows/amd64`，`C:\Deepseek\_gotool\go` | `go version` |
| 构建 tags | `release/DEFAULT_BUILD_TAGS_OTHERS` | `Get-Content release/DEFAULT_BUILD_TAGS_OTHERS` |
| 外部对照 SHA | Mihomo `94b13aed49b8dc41973e4bb3f721c5fa2c5c5c1f`（Alpha, 2026-10-13）<br>Xray-core `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`（v26.10.17）<br>上游 sing-box `dfb80349bf5779ec0a3f834c9ec2794345bff8f0`（2026-10-13） | `git log -1`（各自的浅克隆） |
| 证据等级用语 | `VERIFIED_SOURCE`（读源码）/ `VERIFIED_UNIT`（本次实际运行测试）/ `VERIFIED_REPRO`（本次实际运行探针）/ `VERIFIED_GIT` / `BLOCKED_BY_ENVIRONMENT` | 见 §13 |
| 远端 tip 的独立校验 | `git archive` 导出到仓库外 → `go build ./...` **PASS**；`protocol/naive`、`protocol/masque` 测试 **PASS**；`transport/http` 一条**计时断言**在并发负载下越界（详见 §13.5，未计入基线回归） | `git archive --format=zip -o … origin/testing` |

> **本轮唯一持久化动作**：新增本 Markdown 报告。按用户指示**不 commit、不 push**（任务书 §1.2 原本授权文档归档提交，但用户本轮指令优先），因此报告以**未跟踪文件**形式留在工作树，云端链接与 commit SHA 本轮不存在——这不是遗漏，是执行约束。§14.4 提供了可直接落地的索引条目。

---

## 00 执行摘要与结论边界

### 0.1 一句话结论

**在本次审计的版本上，双栈准入逻辑已经收敛。** 每条流持有一个不可变的 `admissionPolicy`，将配置策略与本机能力快照及其网络代际合并判断。初始过滤、recovery 准入、late 候选、调度器结构守卫、最终 socket 检查、四条 packet 路径和 bypass 判据均使用这一准入值。上一轮报告的微信 literal + sniffed-domain 问题，在当时的生产对象图中已通过构造期校验得到处理：缺少候选规划能力的链会被拒绝。

审计确认 **一处诊断信息丢失**（DS-01，不影响连通性）和**两项高风险**（组 outbound 的接口能力降级、recovery 流生产者未 join），没有发现需要大规模调整的架构缺陷。上述结论只针对报告所列审计版本。

### 0.2 结论计数（严格按等级，不混计）

| 等级 | 数量 | 编号 |
|---|---|---|
| `CONFIRMED_BUG` | **1** | DS-01（诊断/错误分类；有 `VERIFIED_REPRO` 复现） |
| `HIGH_CONFIDENCE_RISK` | **2** | DS-02（selector/urltest/loadbalance 不转发 `ParallelNetworkDialer`）、DS-03（`recoverCandidates` 流式生产者未 join） |
| `UNVERIFIED_HYPOTHESIS` | **2** | DS-04（回退层相乘的放大风险）、DS-05（entry presentment 锁存期间的长期语义，需实机） |
| `ALREADY_FIXED` | **7** | §01.2 的 7 项 |
| `DESIGN_TRADEOFF`（合理取舍，不建议改） | **6** | §11.2 |
| `NOT_A_BUG`（推测被反证） | **4** | §10.5 |
| `EXPECTED_BEHAVIOR` | **2** | 广告 NXDOMAIN；IPv4-only 机器直连 AAAA-only 目标失败 |

### 0.3 对"是否已经收敛"的明确评价（分层）

| 层级 | 判定 | 依据（源码） |
|---|---|---|
| **策略/能力合成（谁是权威）** | **已统一** | `common/dialer/admission.go:44-47`（两项组成）、`:140-145`（唯一的 `admitCandidates`）、`:237-254`（唯一的 `verdict`）；旧版在 4 个调用点手写同一组合，两处对两处错 |
| **本机能力观测（谁是事实源）** | **已统一** | `route/network_family_capability.go:91-116` 一次观测 → `common/netcap/netcap.go:428-483` → `adapter/network.go:192-197` 一个快照三元组（epoch/settled/capability）→ `common/dialer/network_observation.go:103-163` 构造期解析出唯一来源 |
| **候选规划/调度（谁决定顺序与胜负）** | **已统一（Direct 路径）** | `common/dialer/dual_stack.go:128-189`、`dual_stack_scheduler.go:151-612`；`resolve.go:219-505` 与 `dual_stack_parallel_network.go:193-228` 都走同一 scheduler |
| **代理出口（谁决定远端目标）** | **分层正确，非统一** | 本地 family 判定只作用于"本机创建的 socket"：`family_capability.go:57-68` 说明 detour 结构上到不了 `DefaultDialer.DialContext`；代理 outbound 不实现 `ParallelNetworkDialer`（§08 census），目标名交给对端 |
| **组 outbound（能力转发）** | **存在降维断层** | DS-02：`selector`/`urltest`/`loadbalance` 只委托 `DialContext`，`dialer.DialSerialNetwork` 因此退化到逐地址串行 |
| **生命周期（代际/取消/关闭）** | **已统一** | `route/dial_setup.go:89-198`、`route/network.go:1069-1173`、`family_capability.go:29-52`（ownership 三态） |
| **诊断（失败原因保真）** | **仍有一处断层** | DS-01：流式 hostname 路径把"本机拒绝"退化为 `no dial candidates` |

### 0.4 任务书 §2 十个核心问题的直答

| # | 问题 | 答案 | 证据 |
|---|---|---|---|
| 1 | 是否已有唯一/分层准入权威？ | **是。** `admissionPolicy` = 配置层（strategy，每 dialer 记忆一次）+ 事实层（localFamily + ownership，每流捕获一次）；`familyHealth` 只是调度器软偏好，**不能禁止候选**（`dual_stack_scheduler.go:854-901` 明确"soft, does not disable"） | `admission.go:44-102`、`family_capability.go:91-130`、`dual_stack_scheduler.go:834-852` |
| 2 | 是否仍有 richer interface 被降维的路径？ | **有一处生产可达**：组 outbound（DS-02）。Direct 路径已被构造期保证（`surface.go:65-88`），`failoverDialer` 也有逐能力转发与编译期钉住（`route/failover.go:127-138`）。TLS 包装链丢 `Parallel*` 但**不需要**它（服务器地址由 `ResolveDialer` 解析，走 `DialContext` 的 hostname 分支） | `default_parallel_network.go:15-44`、`group/selector.go:386-399`、`common/tls/client.go:130-155` |
| 3 | 同一地址族决定在 TCP/UDP/QUIC/Direct/代理上会不会矛盾？ | **Direct 上不会**（一个 `admissionPolicy` 贯穿四个写入点）；**代理上"矛盾"是设计**（本机能力不该限制对端能到达的目标）。合理传输语义差异见 §06 | `resolve.go:121-203/839-913`、`dual_stack_parallel_network.go:157-187`、`family_capability.go:57-68` |
| 4 | 元信息在哪产生/修改/丢失？ | `Destination`：inbound 写入 → FakeIP 复原改写（`route.go:1145-1158`，同时写 `OriginDestination`）→ route 重写；`Domain`：sniff / FakeIP / reverse-mapping 三源（`route.go:1150-1197`）；`DestinationAddresses`：`actionResolve` 完整解析 + `mergeOriginalDestination` 排序（`route/route.go`）。**丢失点**：多个代理 outbound 不调用 `ExtendContext`（§08.3），但它们的 `Destination` 不参与本地解析，故不影响恢复路径 | §03/§08 |
| 5 | 谁创建/取消/关闭？有无双重所有者/复活？ | 见 §03 所有权表。**一处缺口**：recovery 流式生产者未 join（DS-03）。其余（scheduler worker、feed、producer、setup gate、tracker）都有显式 join/close 与代际校验 | §03、`dial_setup.go`、`dual_stack_parallel_network.go:288-328` |
| 6 | 错误分类是否足以区分六类失败？ | **五类可区分，一类不可**：DNS 无记录（`E.New("no address for …")`）、配置排除（`excluded by strategy …`）、本机族缺失（`… no usable ipv6 path …`）、本机有 IPv6 无公网出口（`… no public ipv6 path …`）、连接失败（`E.Errors(failures…)`）、取消（`ctx.Err()`）；**流式 hostname 全被能力拒绝时退化为 `no dial candidates`**（DS-01） | §5.4 表、DS-01 |
| 7 | 是否有重复维护/绕行代码？ | 有 3 处（`attempts` 字段、`direct.domainStrategy` 字段、`resolveParallelNetworkDialer.DialParallelInterface` 的 domain 分支无生产调用者）。**都不值得为"统一"而改**，理由见 §11 | §11 |
| 8 | Mihomo/Xray/上游解决了什么？ | 上游把严格族策略放在 **DNS 层**（只发一个 qtype），**literal 不做校验**；Xray 的 `LookupForIP` fallback 是**查询回退**不是 socket 回退；Mihomo 的 `DisableIPv6` 是**全局配置开关**而非设备事实；三者都**没有本机能力模型**、没有 literal 的域名恢复。可复用原则见 §09 | §09 |
| 9 | 测试对象图与生产对象图是否一致？ | **基本一致，且有专测**：`TestDirectOutboundGraphRecoversTheWeChatLiteral`、`TestNetworkEntryPointRecoversTheWeChatLiteral`、`TestTheProductionChainYieldsACompleteSurface` 用真实 constructor 组装。**唯一 test-only 形态**：`resolveParallelNetworkDialer.attempts` 与 `dialer` 不同（生产构造器恒相同） | §08.2、§11.1 |
| 10 | 最小长期契约/回归/指标？ | §12.3 给出 5 条契约 + 5 个指标 + 3 个回归测试 | §12.3 |

---

## 01 上一轮验收与版本区分

### 1.1 旧日志不是当前 HEAD（版本必须分开写）

任务书里的两份用户日志来自 **v0.1.17** 时代的运行。当前审计对象是 **v0.1.21**（`b721b988f`），`v0.1.18 → v0.1.21` 之间双栈相关生产文件被重写了多轮：

```text
v0.1.17..HEAD 的生产 .go 变更（按目录，节选）
  common/dialer            12 个文件
  route/                    8 个文件（network.go, network_family_*.go, dial_setup.go, conn.go, route.go, failover.go, dns.go）
  dns/                      2 个文件（router.go, entry_ipv6.go）
  common/netcap             1 个文件
```

因此：**旧日志中出现的 `no dial candidates`、`no interface ... carries an ipv6 address` 等语句，不能直接当作当前缺陷**。本报告对每一条历史症状都重新在当前源码上取证；并在 §10.5 记录被**反证**（`NOT_A_BUG`）的推测。

### 1.2 A2 七项逆向验收（`CONFIRMED / PARTIALLY_CONFIRMED / UNPROVEN / REGRESSED`）

| # | 任务书 A2 项 | 判定 | 当前源码证据 | 测试证据（本次实际运行） |
|---|---|---|---|---|
| A2-1 | 微信 TUN IPv6 literal + sniffed domain 的 recovery 是否已能被 Direct 真正调用 | **CONFIRMED** | `protocol/direct/outbound.go:87-93` 构造期用 `dialer.NewLocalDialSurface` 解析出能力面，**缺 `ParallelNetworkDialer` 直接构造失败**；`:351-353` `scheduleNetwork` 走 `surface.Network.DialParallelNetwork`；恢复以 **late candidate** 注入（`dual_stack_parallel_network.go:136-142`）；`:403-405` packet 臂对称 | `TestDirectOutboundGraphRecoversTheWeChatLiteral`、`TestNetworkEntryPointRecoversTheWeChatLiteral`、`TestDirectOutboundPacketGraphRecoversTheSniffedDomain`（`common/dialer` 372 个测试全绿） |
| A2-2 | `DialParallelNetwork` / `DialSerialNetwork` / `DialParallel` / 各 packet 路径的分派；是否修在真实入口；有无递归/detour 回环 | **CONFIRMED**（分派正确；`DialParallel` 的 `preferIPv6` 参数在 `direct.Outbound.scheduleNetwork` 中未使用，属遗留参数，不构成回环） | `default_parallel_network.go:22-23`（`ParallelNetworkDialer` 优先 → `ParallelInterfaceDialer` → 普通串行）；`dual_stack_parallel_network.go:386-391` `attemptDialer()` 明确"attempt 落到叶子而非 outbound" | `TestNetworkEntryPointRefusesToRecurse`、`TestDialDoesNotGainAProducerOnTheNoRecoveryPath`、`TestTheHopProtocolIsStillFollowedByTheDialerWalks` |
| A2-3 | Original literal 作为主候选、不被异步 recovery 阻塞；strict family 与 local capability 一致传播到 recovered/late | **CONFIRMED** | `resolve.go:133-148` literal 立即拨；`:716-731` 才用 `fallbackDelayOrDefault` 给 recovery 一个"抢跑窗口"；`:604-621` 原地址先过 strategy、再过能力；`dual_stack_parallel_network.go:337-349` merge 时同样过滤；late 侧 `dual_stack_scheduler.go:457-465` 过能力、`:463-465` 过策略 | `TestLiteralDialIsNotBlockedBySlowRecovery`、`TestAStrategyExcludedOriginalIsNeverPromotedBack`、`TestALateCandidateTheStrategyExcludesIsNotAttempted`、`TestRecoveredUndialableFamilyIsDroppedBeforeTheRace` |
| A2-4 | DNS producer 与 scheduler 的取消/join；winner/loser 是否真关闭；error reason 是否正确传播 | **PARTIALLY_CONFIRMED** | hostname 流：`resolve.go:485-486` `cancelResolution(); producers.Wait()`；late feed：`dual_stack_parallel_network.go:141` `defer feed.stop()`（= cancel + `producers.Wait()`）；scheduler：`dual_stack_scheduler.go:304-308` `workerGroup.Wait(); closeLosers()`。**两处欠账**：`dual_stack_recovery.go:129-146` 的 recovery 流生产者未 join（DS-03）；全被能力拒绝的 late 流丢失原因（DS-01） | `TestRecoveryProducerIsJoined`、`TestLateFamilyAnswerDoesNotStrandTheProducer`、`TestRaceReturnsExactlyOneConnection`、`TestLateStreamCloseDoesNotStrandTheRace` |
| A2-5 | Fake-IP retired generation / cursor / restart / A与AAAA / stale synthetic 保护；是否误伤普通私网 literal | **CONFIRMED**（且"误伤"方向有正控） | `route/route.go:1122-1198` 四态处理 + issuance ledger（`adapter/fakeip_issuance.go`）+ 持久化 retired interval（`adapter/fakeip.go:70-139`）；只有"本实例确实发过 + 无记录"才拒绝；`198.18.0.0/15` 未配置时按普通 literal 通行 | `adapter/fakeip_issuance_test.go`、`experimental/cachefile/fakeip_retired_test.go`、`e2e/zz_fip_placeholder_escape_test.go`（含 `TestFIPLiteralWithoutIssuanceStaysReachable`） |
| A2-6 | network generation、connection setup ownership、family health 是否一致 | **CONFIRMED** | `dial_setup.go:167-180` `advanceAndBump` 把"计数 +1"与"换代"放进同一次临界区（原先是两步，测到 200/200 的错配窗口）；`admission.go:73-75` `generation()` 就是能力的 ownership epoch（不是第二次读 manager）；`default.go:907-914` 调度器的 `networkGeneration` 与 `localFamily` 同源 | `TestOneFlowCapturesThePolicyExactlyOnce`、`TestTheFlowsPolicyAndItsSchedulerShareOneGeneration`、`TestAdmissionPolicyGenerationIsTheCapabilityOwnership` |
| A2-7 | 旧版 `no dial candidates` 诊断缺陷是否已修 | **PARTIALLY_CONFIRMED** | 已修 5 处：serial hostname 分支（`resolve.go:184-194`）、`raceCandidatesExcluding`（`:773-785`）、`DialParallelNetwork`（`dual_stack_parallel_network.go:130-132`）、其 packet 臂（`:170-185`）、`ListenPacket` hostname 臂（`resolve.go:904-907`）。**未修 1 处**：流式 hostname 的 late 全拒绝（DS-01） | 定向探针复现（§13.3）：同一台机器上 literal 与 packet 臂给出"no public ipv6 path"，stream hostname 臂给出 `no dial candidates` |

### 1.3 A3 用户日志的独立诊断（含证据缺口）

**原始日志文件不可得。** 我在仓库（`KNOWLEDGE/`、`test/`、根目录）与 `C:\Deepseek`、桌面目录做了定向检索（`hif-dliq` / `szminorshort` / `no dial candidates`），命中全部是源码注释与测试 fixture，没有完整运行日志。因此：

* 对日志的"逐 ID 精确关联"（任务书 §A3 的要求）**`BLOCKED_BY_ENVIRONMENT`**：没有原始文件就无法排除图片识别引入的 `0ms`/空格/大小写误差，也无法证明同域名多次出现属于同一连接。
* 替代证据：用**同构对象图**复现（§13.3 探针 + 仓库内 `wechatFlow` fixture）。结论不依赖对日志的时间戳解读，只依赖"这条代码路径给出什么错误串"。

**微信 IPv6 literal（有/无 sniffed domain 的对照）**

* 有合法 sniffed domain：当前**不再绕过 recovery**。生产路径是 `ConnectionManager.NewConnection` → `DialSerialNetwork` → Direct outbound 的 `DialParallelNetwork` → `resolveParallelNetworkDialer.DialParallelNetwork` → 立即调度 literal + 后台 late feed（`dual_stack_parallel_network.go:88-143`）。旧症状"日志里没有 `dns: lookup domain` 行"在新代码里不可能重现：recovery 的查询在 `recoverCandidates` 内以 debug 级发出（`dual_stack_recovery.go:92`），并带 recursion 标记。
* 无 sniffed domain：literal 被本机能力拒绝而**快速失败**是**正常行为**（`EXPECTED_BEHAVIOR`），不是 bug；当前会给出原因（`errLocalIPv6EgressUnavailable` 或 `errLocalIPv6Unavailable`，`family_capability.go:339-345`）。

**`hif-dliq.deepseek.com:443`（AAAA 两个地址、A 空、Direct 报 `no dial candidates`）**

* 现象本身**完全合理**：IPv4-only/无公网 IPv6 出口的本机，直连一个只有 AAAA 的目标，没有任何 NAT64/代理路径时**不可能**建立连接。这是网络约束，不是缺几个 `if`（任务书 §10 的原则）。
* **真正的缺陷是诊断**：这一形状在当前 HEAD 上仍然输出 `no dial candidates`（DS-01，已用探针复现），而不是"本机没有公网 IPv6 出口"。所以用户看到的是"没有候选"，而事实是"候选有，但被本机能力拒绝"。这正是任务书 §D3 要求区分六种 `no dial candidates` 成因的落点，见 §05.4。
* **DNS 泄漏不成立**：该查询走的是配置的 `adapter.DNSRouter`（`dual_stack_recovery.go:27-32` 明确"永不使用系统解析器"），没有证据表明域名被发到隧道外。仅凭"看到一次域名查询"不能判定泄漏，本报告也不这么判定。

**广告拦截的 `NXDOMAIN` 是预期行为**：未计入任何问题数量，也不建议关闭过滤（任务书明令）。

### 1.4 上一轮残余风险（PHASE01 §V.1–V.8）的当前状态

上一轮（`NETWORK_CAPABILITY_TRANSITION_PHASE01_2026-10-18.md` §V）留了 8 条残余风险。本审计逐条给出当前状态，避免"历史未决项被当成新发现"或"已关项被重复报告"：

| 上轮编号 | 内容摘要 | 本审计状态 | 依据 |
|---|---|---|---|
| V.1 | 路由单独变化时发布"混合瞬时"快照（新路由 + 最多 1 个 debounce 的旧地址） | **仍存在**（采集侧窗口，方向 fail-open）。本审计未改变结论 | `network_environment.go:261-291`（compute 与 claim 同临界区，但输入仍是同一次抽取） |
| V.2 | 无生产者侧静默期（dwell），路由抖动会让 `GlobalIPv6Egress` 在一个代际内翻转两次 | **仍存在**；本审计未发现 dwell 机制 | 同文件；`netcap.Evaluate` 无时间维度 |
| V.3 | `DeadPath` 不在能力模型里，真正离线的设备报 `Unknown/Unknown`，仍会发起不可能成功的尝试 | **仍存在**（设计取舍：把 path 事实编码进 addressing 事实会错得更远） | `netcap.go:130-168`（`StateValidated/StateDegraded` 预留未产出） |
| V.4 | Darwin 平台监控客户端无独立路由回调，同接口的 IPv6 路由变化可能到不了生产者 | **仍存在，需实机** | §14.3-5（跨平台运行未验证） |
| V.5 | `-race` 未验证 | **已关闭（本轮）**：`CC=C:\msys64\mingw64\bin\gcc.exe` 后可跑，`common/dialer`（×3）、`common/netcap`、`dns`、`protocol/direct`、`route` 全绿 | §13.4 |
| V.6 | 两个无生产调用者的 `invalidateGeneration/invalidateAll` 提供"第二条清除路径" | **已关闭（上一轮）**：本审计确认现在只有注释说明"故意不提供"，无该方法 | `common/dialer/dual_stack_scheduler.go:981-991` |
| V.7 | Windows 的 IPv6-only 网络被上层读成 dead path | **仍存在**（上游依赖，非本 fork 可控）；本审计未复测 | 任务书 §D2 亦列为平台限制 |
| V.8 | 非默认接口的地址变化不进能力，直到默认接口变化 | **在非平台接口路径上未再复现**（收尾时补核）：`notifyInterfaceUpdate:1467` 与 `updateInterface:1530` 都在入口 `observeNonPlatformInterfaces()`，且回调签名 `(_ *control.Interface, _ int)` 与具体接口无关；`UpdateInterfaces:635-669` 是"先刷 finder 再发布再 recompute"。**平台接口客户端**的刷新频率取决于 `libbox/monitor.go:144` 的调用节奏，本审计未验证 | `route/network.go:1457-1492,1514-1531,635-669`、`experimental/libbox/monitor.go:144` |

本审计的 DS-01/DS-02/DS-03 **不与上轮任何 V.x 重复**：V.x 全是"能力生产/观测侧"的残余风险，DS-01/DS-03 在**消费与错误构造**侧，DS-02 在**组 outbound 的接口转发**侧。

---

## 02 真实 production 调用图

以下每条都标注**实际函数、字段类型、接口断言、构造点与调用位置**。图中 `→` 表示直接调用，`⇒` 表示经接口分派。

### 2.1 入口矩阵（inbound → 第一跳）

| inbound / 入口 | 进入点 | 第一跳 | 源码 |
|---|---|---|---|
| TUN（TCP） | `ConnectionManager.NewConnection` | `metadata.DestinationAddresses` 非空或 `Destination.IsIP()` → `dialer.DialSerialNetwork`；否则 `this.DialContext` | `route/conn.go:316-338` |
| TUN（UDP connected） | `NewPacketConnection`（`metadata.UDPConnect`） | 同上二分；`DialSerialNetwork(N.NetworkUDP, …)` | `route/conn.go:559-591` |
| TUN（UDP unconnected） | `NewPacketConnection` 的 else 臂 | `DestinationAddresses` 非空 → `dialer.ListenSerialNetworkPacket`；否则 `PacketDialerWithDestination` 或 `this.ListenPacket` | `route/conn.go:610-617` |
| Mixed / SOCKS / HTTP / TProxy / Redirect | 各自 inbound → 同一个 `ConnectionManager` | 同上 | `route/conn.go` |
| 内部 service / 探针 | `daemon/started_service.go:1653,1699` | `dialer.NewResolveDialer(ctx, outbound, true, …)`（**包在 outbound 外面**，plain resolveDialer，非 parallel 变体） | 同左 |
| DNS transport 自身 | `dns.NewRemoteDialer` → `dialer.NewWithOptions{RemoteIsDomain:true}` | resolve 链 → 服务器名解析 | `dns/transport_dialer.go:23` |
| 代理 endpoint（wireguard/masque/openvpn/…） | `dialer.NewWithOptions` | 服务器地址解析（`RemoteIsDomain` 由各包计算） | §08.3 |

### 2.2 图 1：Direct TCP，literal 目标，无 sniffed domain（最常见）

```text
inbound
  → ConnectionManager.NewConnection                     route/conn.go:316
     setupCtx, finishSetup = dialSetups.begin(ctx)      dial_setup.go:89
     DialSerialNetwork(this=*direct.Outbound, …)        conn.go:335
       assert this.(dialer.ParallelNetworkDialer)       default_parallel_network.go:22   [direct.Outbound 实现]
       → (*direct.Outbound).DialParallelNetwork         protocol/direct/outbound.go:297
          surface.Network.DialParallelNetwork           outbound.go:352                  [构造期保证非 nil]
          → (*resolveParallelNetworkDialer).DialParallelNetwork
                                                        dual_stack_parallel_network.go:88
             admission = d.captureAdmissionPolicy()     admission.go:55   ← 本流唯一一次策略/能力捕获
             candidates = withoutFamilyExcluded(list)   dual_stack_recovery.go:319
             plan = planCandidates(candidates, original, …)   dual_stack.go:128
             scheduler = d.schedulerFor(fallbackDelay, admission)
                                                        dual_stack_parallel_network.go:399
               → (*DefaultDialer).newDualStackScheduler (health + generation + localFamily 同源)
                                                        default.go:907
             scheduler.dialWithLateCandidatesAdmitting  dual_stack_scheduler.go:151
               filterLocalFamily(plan) → gate.undialable  family_capability.go:178-198
               startAttempt → DialParallelInterface(leaf)   default_parallel_network.go:214
                  → (*DefaultDialer).DialParallelInterface   default.go:409
                     captureEpoch (同一 policy)              default.go:410
                     d.networkStrategy==nil → DialContext      default.go:378-398
                        localFamily.hardUndialableError       default.go:375  ← 叶子 socket 门
                        dialer4/dialer6.DialContext           default.go:380-392
                     stillCurrentEpoch(tracked)               default.go:702
     finishSetup()；releaseDial()                         conn.go:344-345
```

### 2.3 图 2：Direct TCP，literal 目标 + sniffed domain（微信事故形状）

```text
        原字面地址 ──── 立即开始拨号 ───────────────────────────────┐
        sniffed domain ── recoverCandidates（后台，带 recursion 标记）┤ 谁先成功谁赢
                          └→ mergeRecovered → late candidates ────────┘

resolve.go:560-734 dialLiteralWithRecovery
  originalAllowed = admission.strategyAllows(addr)                 :605
  if !hardUndialable(addr) → go originalWorkers{DialContext}       :618-628
  else → original <- literalDialResult{err: 本机/策略原因}          :644-651
  go recoverCandidates(recoveryCtx …)                              :664-667
  select {
    case original 成功  → claimed=true; return                    :670-674
    case original 失败  → 等 recovered（受 caller ctx 约束）       :685-690
    case recovered 非空 → fallbackTimer = fallbackDelayOrDefault    :716-731
                        → raceWithPendingOriginal（双发，显式 join）:1057-1165
  }
```

**关键不变量**：原字面地址**永不**被异步 DNS 延迟（`:133-148` 注释 + `TestLiteralDialIsNotBlockedBySlowRecovery`）；recovery 只作 fallback，`promoteOriginal` 保证原地址在**本族内**领先（`dual_stack.go:263-285`）。

### 2.4 图 3：hostname 目标（streaming，DS-01 所在路径）

```text
resolve.go:219-505 raceResolvedName
  router.(adapter.DNSDualStackRouter)            :224   [route.Router 实现]
  producer-1 goroutine: LookupFamilies(publish)  :296-312
  producer-2 goroutine (feeder): 优先族先发、非优先族最多 hold 50ms
                                                 :316-445（preferredFamilyGrace = 50ms, :989）
  plan = planCandidates(nil, Invalid, strategy)  :471   ← 空计划，一切靠 late
  scheduler.dialWithLateCandidatesAdmitting(     :478
      plan, candidates, admission.lateAdmission(), …)
  cancelResolution(); producers.Wait()           :485-486
```

**DS-01 的机制**：空计划 → `filterLocalFamily` 无候选可拒（`refused` 保持 `familyInvalid`）→ 每个 late 候选在 `:457` 被能力静默 `continue` 丢弃 → 流关闭时 `started == 0` 且 `refused.refused()==false` → `:407 return E.New("no dial candidates")`。

### 2.5 图 4：UDP 三臂（写法差异是设计，不是遗漏）

| 臂 | 决策者 | 为什么不能复用另一臂 |
|---|---|---|
| connected UDP（`UDPConnect`） | 与 stream 同一条 `DialSerialNetwork` | 它是 `DialContext` 语义，可竞争、可取消 |
| unconnected + 候选表 | `ListenSerialNetworkPacket` → `ListenSerialInterfacePacket` | **socket 创建不是成功判据**，不能赛跑（`default_parallel_network.go:232-261` 解释）；因此是串行走位，唯一 fallback 是下一个候选；`DefaultDialer.listenSerialInterfacePacketEpoch:589-593` 在**建 socket 之前**做能力门（这是唯一能把流移到另一族的东西） |
| unconnected + 无候选表（bare literal） | `resolveDialer.ListenPacket:839-882` | 第三处策略执行点；曾漏（`ipv4_only` + IPv6 literal 建出被禁止族的 socket），现已补 |

### 2.6 图 5：代理出口（不在本机 gate 之下，且这是正确的）

```text
ConnectionManager → DialSerialNetwork(this=*trojan.Outbound, …)
  this 不实现 ParallelNetworkDialer / ParallelInterfaceDialer
  → 普通串行循环：for addr { this.DialContext(ctx, net, SocksaddrFrom(addr, port)) }
        → trojan.DialContext → 把目标（域名或 IP）交给对端
```

* 本地 capability **不**参与：`family_capability.go:57-68` 用结构论证——detour/代理链不创建本机 socket，能力读不到远端目标。
* 但**代理服务器自身地址**是本机 dial，能力**必须**参与（`family_capability.go:66-68`），实测覆盖 `TestProxyEndpointDialIsSubjectToTheLocalVerdict`、`TestIPv6OnlyProxyEndpointFailsFastWithAReason`。

### 2.7 图 6：native bypass（快路径必须与慢路径同判据）

```text
route fast-path 判定 → (*direct.Outbound).CanBypass   outbound.go:440-442
  BypassBlockers:                                     outbound.go:459-493
    semantics.Blockers(facts)          ← 由 DialerOptions 静态剖面决定（profile.go）
    NetworkPolicyBlockers(network)     ← mark / auto_redirect 冲突
    surface.LocalFamily.LocalFamilyBlocked(dest)  ← 与本机 gate 同一 verdict
    isMyLoopbackAddress(dest)          ← 防止 TUN 自环
```

`LocalFamilyBlocked` 的**极性**与 dial gate 相反（不确定时返回 false=不阻止快路径），这一点在 `family_capability.go:99-118` 明确论证：bypass 的 fail-open 方向是"放行"，dial gate 的 fail-open 方向是"拒绝"。**同一个 policy，两种极性，各有理由**——这是本审计认为**不应统一**的一处。

### 2.8 分派交汇表（谁在什么条件下被调用）

| 入口 | 若对象实现 `ParallelNetworkDialer` | 否则若实现 `ParallelInterfaceDialer` | 否则 |
|---|---|---|---|
| `dialer.DialSerialNetwork` (`default_parallel_network.go:15`) | `DialParallelNetwork`（候选表整体入调度器） | 逐地址 `DialParallelInterface` | 逐地址 `DialContext` |
| `dialer.ListenSerialNetworkPacket` (`:222`) | `ListenSerialNetworkPacket`（可带恢复） | 逐地址 `ListenSerialInterfacePacket` | 逐地址 `ListenPacket` |
| `dialer.DialParallelNetwork` (`:64`) | — | 绑定 owner 的 health + 本流 admission → `dialParallelNetwork` | — |
| `ConnectionManager`（TCP/UDP connected） | 不直接断言（`conn.go:568-586` 记录了一次误用窄接口的回归） | — | — |

---

## 03 状态权威与生命周期所有权表

| 状态 | 生产者 | 唯一权威 | 读者 | 写者 | capture 时刻 | generation | 失效条件 | 关闭/清理责任 | 关键测试 |
|---|---|---|---|---|---|---|---|---|---|
| **准入策略** `admissionPolicy` | `resolveDialer.captureAdmissionPolicy` | 该流的值本身（不可变） | 所有 gate/planner/scheduler | 无（值） | 每次 `DialContext`/`DialParallelNetwork`/`ListenPacket` **开头一次** | `localFamily.ownership.epoch` | 随流结束 | 无需 | `TestOneFlowCapturesThePolicyExactlyOnce`、`TestNoLongLivedStructureHoldsAPolicy` |
| **本机能力快照** `adapter.NetworkFamilyCapability` | `NetworkManager.storeFamilyCapability`（`network_family_capability.go:146`） | `familyCapability atomic.Pointer` | `NetworkFamilyCapabilitySnapshot` / 观察者 | 环境 recompute（`network_environment.go:261-291`） | 每次 recompute（接口事件或指纹变化） | 与 transition epoch 同临界区 | 新一代覆盖；零值=无意见 | 进程生命周期 | `route/network_family_state_test.go`、`..._capability_test.go`、`..._lifecycle_test.go` |
| **网络代际** `transitionOwner` + `transitionStable` | `beginTransition`（`network.go:1069`）/ `commitTransition`（`:1155`） | `transitionAccess` 保护的二元组 | dialer ownership 谓词、DNS、transport | 同上 | 每次 `NetworkFamilyCapabilitySnapshot()` 一次读三元组 | 单调 +1 | 新 transition claim | 无 | `TestAllResetEntriesShareOneSerialisation`、`route/transition_*_test.go` |
| **dial setup 所有权** | `ConnectionManager.dialSetups.begin`（`dial_setup.go:89`） | `dialSetupGate.current`（每代一个 ctx） | 每次 dial | `advance`/`close` | 每次 dial | 由 `advanceAndBump` 与计数同临界区 | 建连成功即转交（`stopGenerationWatch`）；transition 取消 | `finishSetup` 必调 | `TestEveryAttemptCarriesTheSetupsContext`、`TestASetupHeldAcrossACloseDoesNotDeadlockShutdown` |
| **DNS cache / 反向映射** | 各 DNS transport | transport 自身 | DNS router | transport | 查询时 | DNS 层 generation（`dns` 包） | `ResetNetwork` | transport 生命周期 | `dns/router_*_test.go`、`dns/*generation*_test.go` |
| **Fake-IP mapping** | `fakeip` store | store（内存）+ cache_file（持久） | `route.prepareMatchMetadata`（`route.go:1145`） | store | 查询/分配时 | 由 cursor + metadata 表达 | 范围迁移 / `FakeIPReset` | store | `dns/transport/fakeip/*_test.go`、`experimental/cachefile/fakeip*_test.go` |
| **Fake-IP 已发放区间** `FakeIPIssuanceLedger` | `adapter.NewFakeIPIssuanceLedger`（每进程序列） | ledger | `route.issuedFakeIPAddress` | `RecordSeed`/`RecordIssued`/`Advance` | 每次 Box 启动/分配 | `Sequence()` 自增（有上限 `MaxIssuanceGenerations`） | `Clear()` / 进程结束 | 无（有界） | `adapter/fakeip_issuance_test.go`、`adapter/agentf_ledger_bounds_attack_test.go` |
| **Fake-IP retired interval** | store（持久化） | `FakeIPStorage.FakeIPRetired()` | `route.retiredFakeIPAddress` | `FakeIPRetireInterval` | 范围迁移时 | — | 上限 `FakeIPMaxRetiredRanges=8` | 持久化文件 | `experimental/cachefile/fakeip_retired_test.go`、`e2e/zz_fip_placeholder_escape_test.go` |
| **metadata.Domain** | sniff / FakeIP 复原 / reverse mapping | `adapter.InboundContext` | recovery、日志、powerreport | 上述三处 | 逐步 | 无 | 流结束 | 无 | `route/sniff_failure_report_test.go`、`common/dialer/recovery_*_test.go` |
| **metadata.DestinationAddresses** | `Router.actionResolve` | metadata | `DialSerialNetwork`/`ListenSerialNetworkPacket` | route action | resolve 时刻 | 无（属该 flow） | 流结束 | 无 | `route/action*_test.go`（`TestActionResolvePlansByEffectiveStrategy` 等） |
| **candidate plan** | `planCandidates` | 该 scheduler 调用 | scheduler | 无（构建后只读；late 追加到 `launch`） | 每次调度 | 继承 admission | 调度结束 | 栈上值 | `TestPlanCandidates*`（11 个） |
| **family health** | `candidateScheduler` 的 winner/failure 路径 | `familyHealth`（按 generation 分桶） | 下一次同 dialer 调度 | 同上 | 结果产生时 | `admission.generation()` | 15s 窗口 / 成功清除 / 256 桶上限 | 无 goroutine | `TestFamilyHealthIsScopedToTheNetworkGeneration`、`TestTheFamilyHealthTableStaysBounded` |
| **socket（TCP/UDP）** | `DefaultDialer.trackConn/trackPacketConn` | `ConnectionManager` 连接表 | drain/CloseAll | tracker | 建连后、交回前 | `m.generation` 写入 `managedConnState` | reset 关闭 / 流结束 | tracker + caller | `TestCase3TrackedConnectionIsVisibleToTheReset`、`TestTrackedConnectionIsClosedByReset` |
| **scheduler 定时器** | `resetTimer/stopTimer`（`dual_stack_scheduler.go:346-368`） | 该 race | 该 race | 同上 | — | — | `defer stopTimer()` | 同函数 | `TestACandidateDeliveredAfterTheCancelAddsNoAttempt`、`TestContinuousLateArrivalsDoNotStarveTheTimerPath` |
| **preferred-family grace 定时器** | feeder（`resolve.go:323`） | 该 flow | feeder | 同上 | — | — | `defer graceTimer.Stop()` | 同 goroutine | `TestPreferredGraceHasOneOwner` |
| **dial governor** | `ConnectionManager.enterDialGate` | `dialGovernor` | 每次 dial | `open/release` | 每次 dial | 3s 窗口 | 窗口过期 | 无 | `route/dial_governor_test.go`（`TestDialGovernorNeverDelaysInteractive`、`TestDialGovernorSoftBoundGivesUp`、`TestTransitionOpensTheGovernorWindow`） |
| **网络重置通知** | `NetworkManager.beginTransition` → `runtimeCoordinator.Advance` | coordinator | 注册的资源 | transition | claim 后**锁外**广播 | 同一 epoch | — | 资源各自 | `TestTransitionPublishesTheCoordinatorAtTheSameEpoch`（`route/runtime_coordinator_integration_test.go:60`） |

**重复所有权风险盘点**

1. **无**：scheduler 的 winner 唯一（`successes` 长度守卫 `dual_stack_scheduler.go:531-565`），loser 在 `defer` 里统一关闭；feed 的 `stop()` 同时 cancel 与 `Wait`；setup gate 的 `finishSetup` 必须调用（`conn.go:344`）。
2. **一处未 join**：`recoverCandidates` 的流式 goroutine（DS-03）。它不是"无人拥有"，而是"拥有者没有等待"——上层 `dialLiteralWithRecovery` 在返回前只 cancel 了 `recoveryCtx`，没有 join。
3. **一处只有 test 能触发的形态**：`resolveParallelNetworkDialer.attempts != dialer`（§11.1）。
4. **QUIC / mux 会话**：由各协议自己拥有，统一入口是 `InterfaceUpdated`（`protocol/` 下 27 个实现：5 入站 + 1 内部节点 + **21 个出站/端点**；不实现的 `socks`/`shadowtls`/`tor`/`dns`/`block`/`bridge`/组类型不持有长生命周期会话，见 §08.2 ③）。本审计**只抽样阅读**了 `direct`/`naive`/`masque`/`wireguard` 的回调内容，其余未逐个验证，见 §14.3。

---

## 04 双栈 DNS、Fake-IP、地址族策略专项

### 4.1 A/AAAA 与流式语义

* `dns.Router.LookupFamilies`（`dns/router_dual_stack.go:34-120`）**复用** `Router.Lookup`，每族一次调用，因此规则、传输选择、响应检查、缓存、singleflight、ECS、负缓存全部生效；严格策略只发**一个** qtype（`:51-66`）。
* 完整 `Lookup` 与流式 `LookupFamilies` 的语义差异只有"何时发布"，两者都经 `lookupWithRules`（`dns/router.go:1163-1219`），因此 `resolveLookupStrategy` 的解析结果一致（`:1123`）。
* **单族失败不影响另一族**：`succeeded` 计数（`:91-94`），只有"两族都没地址"才返回错误（`:113-118`）。

### 4.2 "AAAA 先返回但本机不可拨，A 稍后返回"——专项核验（任务书 §C.2）

**结论：已消除，且有两道独立防线。**

| 路径 | 防线 | 源码 | 测试 |
|---|---|---|---|
| recovery 选族 | 只有**准入可用**的族才能结束 lookup：`admittedRecoveredCandidates` = `admitCandidates`（能力 + 策略） | `dual_stack_recovery.go:155-165`、`:199-202` | `TestRecoverySkipsAnUndialableAnswerToReachAUsableOne`、`TestRecoveryUsesTheFlowsCapturedVerdict` |
| scheduler late 流 | 能力拒绝的 late 候选被丢弃，流关闭**前**不会提前结束等待 | `dual_stack_scheduler.go:457-459`、`:173-182` | `TestUnavailableFamilyIsRefusedFromTheLateStream`、`TestLateCandidateCanJoinOnceItsFamilyBecomesAvailable` |
| 优先级倒置 | 优先族中途到达时，held 批次**先释放再发 held**，避免"非优先族抢到首个 slot" | `resolve.go:406-435` | `TestPreferredFamilyArrivingWithinGraceIsDialledFirst`、`TestFeederEmitsThePreferredFamilyFirst` |

**反向缺口**：如果两族**都**被能力拒绝，当前报 `no dial candidates`（DS-01）。这是"正确行为 + 错误原因"的组合。

### 4.3 DomainStrategy 五值 × 六个消费点矩阵

| 消费点 | AsIS | PreferIPv4 | PreferIPv6 | IPv4Only | IPv6Only | 源码 |
|---|---|---|---|---|---|---|
| 有效策略解析（每 dialer 一次） | 问 router，否则沿用调用方 | 原值 | 原值 | 原值 | 原值 | `resolve.go:545-558` |
| literal 原地址 | 允许 | 允许 | 允许 | 仅 v4 | 仅 v6 | `resolve.go:604-621` |
| 完整 Lookup（hostname） | 两族 | 两族 | 两族 | **只发 A** | **只发 AAAA** | `dns/router.go:1163-1179` |
| 流式 LookupFamilies | 两族并发 | 两族 | 两族 | 单族发布 | 单族发布 | `dns/router_dual_stack.go:51-66` |
| planner 偏好 | 原地址族优先 | v4 族先 | v6 族先 | 单族 | 单族 | `dual_stack.go:128-189` |
| 调度器 late 守卫 | nil（全放行） | nil | nil | 只放行 v4 | 只放行 v6 | `admission.go:153-161` |
| packet 三臂 | 同 admission | 同 | 同 | 同 | 同 | `resolve.go:874-880/904-907`、`dual_stack_parallel_network.go:163-185` |
| native bypass 剖面 | 同 effective strategy | 同 | 同 | 同 | 同 | `outbound.go:468-475`（`surface.Resolve.EffectiveFamilyStrategy()`） |

> `AsIS` 的语义是"用 resolver 的默认"，因此**不是**"无偏好"：读原始 option 会把 `prefer_ipv6` 的默认当成"不偏好 v6"（`resolve.go:276-287` 记录了这一点，`actionResolve` 同样按有效策略排序）。

### 4.4 DNS 权责：谁解析什么

| 域名 | 解析者 | 是否受本机能力约束 | 源码 |
|---|---|---|---|
| 应用目标（Direct） | 本机 `adapter.DNSRouter` | **是**（能力 + 策略） | `resolve.go:219-505`、`dialLiteralWithRecovery` |
| 应用目标（代理） | 对端（域名原样转发）或 `domain_resolver` | **否**（结构上到不了本地 gate） | `family_capability.go:57-68` |
| 代理服务器名 | 本机 resolve 链 | **是**（它是本机 socket） | `dialer.go:65-100`、`TestProxyEndpointDialIsSubjectToTheLocalVerdict` |
| DNS transport 自身 | `NewResolveDialer` 包裹 | 是 | `dns/transport_dialer.go:23` |
| WireGuard **inner** 目标 | 全局 DNS 规则（空 `DNSQueryOptions{}`） | 是（隧道内，本机发起） | `protocol/wireguard/endpoint.go:501-509`，注释明确 inner/outer 分工 |
| Tailscale/MASQUE/OpenVPN 内部名 | 各自 resolver | 部分 | §08.3 |

### 4.5 Fake-IP：四态 + 正控

`route/route.go:1122-1198` 把 Fake-IP 目标分成四态：

1. 当前 store 有映射 → 改写为域名（`metadata.OriginDestination` 同时保存原地址）；
2. 在当前范围内但无映射 → 拒绝（`missing fakeip record`，提示开启 cache_file）；
3. 在当前范围外且**无法证明**本实例发放过 → **普通 literal**，逐字节放行（防止把 `198.18.0.0/15` 当 blanket guard）；
4. 范围外但 issuance ledger **证明**本进程发过 → 拒绝。

第 4 态还叠加"持久化 retired interval"（`retiredFakeIPAddress`，`:1246-1256`），覆盖"进程重启后 ledger 为空、客户端仍握着旧 FakeIP"的窗口（TTL 跨度）。**该机制只覆盖被证明发放过的区间**，且 `FakeIPRetiredCovers` 要求位宽一致（`adapter/fakeip.go:125-139`），所以 `::ffff:198.18.0.2` 与 `198.18.0.2` 的比较行为有专测（`experimental/cachefile/fakeip_retired_test.go`）。

**误伤方向的正控**：未配置 FakeIP 的机器上 `198.18.0.x` 必须照常可连（`e2e/zz_fip_placeholder_escape_test.go:1011 TestFIPLiteralWithoutIssuanceStaysReachable`；源码注释 `route.go:1175-1187` 明确引用该正控）。

### 4.6 Sniff / Fake-IP / reverse-mapping 的域名来源与可信度

* sniff 域名为**攻击者可影响**输入：`validRecoveryDomain`（`dual_stack_recovery.go:414-443`）拒绝空、超长（>253）、含控制字符、地址字面量、单标签、非法 label 字符。这是安全边界，不是格式洁癖。
* Fake-IP 复原的域名来自本进程 store → 可信度高；reverse mapping 来自 DNS 响应缓存 → 仅在 `metadata.Domain == ""` 时补位（`route.go:1192-1198`），不覆盖 sniff 结果。
* **literal 恢复不得覆盖应用自选 endpoint**：`promoteOriginal` 只把原地址在本族内提前，不会把它提升到另一族（`dual_stack.go:263-272` 的族检查）。

### 4.7 DNS 递归守卫与残留

* 守卫：`withoutAddressRecovery` 标记（`dual_stack_recovery.go:42-56`），只包住 recovery 的 lookup，不污染 sibling flow（context 派生，不修改父 ctx）。`TestAddressRecoveryDisabledByMarker`、`TestRecoveryRefusesToRecurse`、`TestNetworkEntryPointRefusesToRecurse`。
* 残留：hostname 流的两生产者显式 join（`resolve.go:485-486`）；recovery 流**未 join**（DS-03），但它的 publish 回调在 `select` 里同时等 `streamCtx.Done()`（`dual_stack_recovery.go:135-138`、`:142-145`），所以一旦 `cancelStream` 触发，goroutine 不会永久阻塞在 channel 上，只会在 router 返回前存活。生产 `route.Router.LookupFamilies` 全程尊重 ctx（`dns/router_dual_stack.go` 的两次 `r.Lookup` 都传 ctx），故实际窗口是"一次 DNS 查询的剩余时间"。

### 4.8 查询放大与缓存

* 一个有 sniffed domain 的 literal 流：literal 立即拨 + 1 次 recovery lookup（≤2 个 qtype）。
* 一个 hostname 流：1 次 `LookupFamilies`（≤2 qtype）。
* 若配置了 `route action: resolve`，则在 route 阶段先做完整 Lookup，dialer 侧因 `DestinationAddresses` 非空而走 `DialParallelNetwork`，`isBareLiteralList` 为 false → **不再重复 recovery**（`dual_stack_parallel_network.go:438-450`）。因此没有"同一名字被解析两次"的放大路径。
* 缓存/singleflight 由 DNS 层统一提供（`LookupFamilies` 复用 `Lookup`），无需本模块重复。

### 4.9 DNS 泄漏判定

本报告只在能追到**transport 与出口**时才判定泄漏。审计到的实现：recovery 只走配置的 router（`dual_stack_recovery.go:27-32`），系统解析器不被使用；`dns` 传输的 detour 由 `dialer.NewWithOptions` 构造（`dns/transport_dialer.go`），因此 DNS 服务器的连接同样受 mark/interface/能力约束。**未发现泄漏证据**；也不以"看到域名查询"为据。

### 4.10 HTTPS/SVCB hints、IPv4-mapped、NAT64/464XLAT

| 项 | 现状 | 判定 |
|---|---|---|
| IPv4-mapped IPv6 | `classifyAddress` 归为 IPv4（`dual_stack.go:66-74`）、`unmapAddress` 规范化去重（`:78-83`）、`ClassifyScope` 同样按 v4 处理（`netcap.go:114-127`）、FakeIP 比较先 unmap（`route.go:1141-1144`） | **已正确处理**（测试 `TestMappedIPv4IsAlwaysAnIPv4Destination`、`TestPlanCandidatesTreatsMappedIPv4AsIPv4`） |
| NAT64 / 464XLAT | 无显式支持代码；`TestANAT64MachineKeepsUsingTheNAT64Prefix` 只保证"不误伤" | **设计范围外**，非 bug |
| SVCB/HTTPS hints | 未实现；`dns` 层无 hint 解析路径 | **设计范围外** |

---

## 05 Candidate discovery / scheduler / family health

### 5.1 候选来源与权属

| 来源 | 生产者 | 进入点 | 准入检查 |
|---|---|---|---|
| 应用自选 literal | inbound metadata | `dialLiteralWithRecovery`（原地址）/ `mergeRecovered`（合并时） | strategy（`:605`）+ capability（`:618`）+ `promoteOriginal` 的族检查 |
| `DestinationAddresses`（route resolve） | `actionResolve` | `DialParallelNetwork` 的 list | `withoutFamilyExcluded` + 调度器 gate |
| A/AAAA（hostname） | `LookupFamilies` | late stream | 调度器 gate（每到达一次重读策略）+ `admit` |
| sniffed-domain recovery | `recoverCandidates` | late stream（stream 臂）或同步（packet 臂） | 选族时 `admitCandidates`；再经 merge 的 strategy/capability |
| Fake-IP 域名复原 | `prepareMatchMetadata` → `Destination` 变域名 | 走 hostname 路径 | 同上 |

**initial 与 late 使用同一 policy**：`admission` 在函数入口捕获一次，planner/scheduler/late guard 都收它（`admission.go:94-114` 的注释解释了为什么必须是值而不是"再读一次"）。

### 5.2 调度器不变量（逐条核对）

| 不变量 | 实现 | 证据 |
|---|---|---|
| winner 只返回一个 | `successes` 只在第一帧返回，其余进 `collectLoser` | `dual_stack_scheduler.go:530-565` |
| loser 的成功 socket 必须关闭 | `collectLoser` + `closeLosers` 在 `defer` 里 | `:280-308`、`:564`、`:574-576` |
| 取消 / 网络切换的错误不得学习为族失败 | `parentAlive` 检查 + `isFamilyPathFailure` 排除 `context.Canceled`/`net.ErrClosed`/`io.EOF`；errno 分类按平台拼写 | `:243-266`、`:1007-1061`、`windows_socket_errors.go` |
| 不得返回空连接假成功 | 成功分支要求 `err == nil && conn != nil` | `:531` |
| 错误聚合不得吞掉拒绝原因 | 有 refused 时返回 `localFamilyRefusalError`；否则 `E.Errors(failures...)` | `:400-414`、`:590-596` |
| 每 fallback interval 只启一个 | `nextIndex` 单调 + 单 timer owner | `:339-380`、`:580-609` |
| late 候选不得打乱已启动顺序 | `launch` 只 append，从不重排 | `:310-320`、`:481` |
| late 流不因持续到达而饿死 pending launch | timer 只在启动后重置，不在到达时重置 | `:516-528`（注释记录了一次实测饿死） |

### 5.3 family health 语义

* **只优化顺序，不禁止候选**：唯一消费点是 `fallbackImmediately`（`:834-852`）——让两族首候选一起启动，省掉一次 fallback delay。
* **按代际分桶**：`networkGeneration = admission.generation()`；新网络=新桶，旧桶只在被再次访问时清理，并有 256 桶上限（`:823`、`:936-951`）。
* **窗口** = `C.TCPTimeout`（15s，`:996`）。
* **两类"阴性"分开记**：`recordFailure`（内核明确报路不通）与 `recordStalledFamily`（对方先连上、本族沉默），后者是软判定（`:854-934`）。

### 5.4 `no dial candidates` 的六种成因 × 当前输出

| 成因 | 当前输出 | 是否可区分 | 源码 |
|---|---|---|---|
| DNS 无任何记录 | `no address for <host>`（由 `LookupFamilies` 返回） | ✅ | `dns/router_dual_stack.go:113-118`、`resolve.go:188-194` |
| DNS 只回答了被策略排除的族 | `destination … excluded by strategy ipv4_only: …` | ✅ | `dual_stack_parallel_network.go:130-132`、`:183-185`、`resolve.go:780-783` |
| 本机完全没有该族地址 | `local network has no usable ipv4/ipv6 path: no interface …` | ✅ | `family_capability.go:339-345` |
| 本机有 IPv6 地址但无公网出口 | `local network has no public ipv6 path: …` | ✅ | 同上 |
| 候选都存在但全部 connect failed | `dial <addr>: <err>; …`（聚合） | ✅ | `dual_stack_scheduler.go:590-596` |
| **流式 hostname 的全部候选被本机能力拒绝** | **`no dial candidates`** | ❌ **DS-01** | `dual_stack_scheduler.go:400-407` + `:457-459` |
| 竞态后策略中途变化（capture 失主） | `no dial candidates`（有意：不把已变化的机器状态当解释） | ✅（有意） | `dual_stack_scheduler.go:658-671` |

### 5.5 缓存切片别名 / 去重 / 乱序

* 不允许改写解析器返回的数组：`withoutUndialable`/`withoutFamilyExcluded` 只在新切片上工作（`dual_stack_recovery.go:224-277`、`:298-348`），测试 `TestWithoutUndialableDoesNotWriteTheCallersArray`、`TestRecoveryFilterAlsoLeavesTheResolversArrayAlone`、`TestStrictFamilyFilterDoesNotMutateItsInput`。
* 去重按规范化地址（`collectFamily` 的 `seen` + `unmapAddress`），scheduler 另有 `seen`（含 late 与 original）。`TestLateCandidatesDeduplicateCanonicalForm`。
* 乱序：resolver 族内顺序被保留（`collectFamily` 按输入序），跨族交错（`interleaveCandidates`），原地址在其族内提前（`promoteOriginal`）。

---

## 06 TCP / UDP / QUIC / 数据面 / 跨平台

### 6.1 "成功"的定义（按传输，逐处标注）

| 传输 | 成功判据 | 源码 | 风险 |
|---|---|---|---|
| TCP | `DialContext` 返回无错连接 | `default.go:378-398` | — |
| connected UDP | `DialContext("udp")` 返回 | `default.go:381-386` | 内核只校验路由/源地址，不证明对端可达（已被文档化，`default_parallel_network.go:232-261`） |
| unconnected UDP | **socket 创建成功**即胜出，唯一 fallback 是下一个候选 | `default_parallel_network.go:262-280` | 这正是"UDP socket 成功≠可达"的落点；对策是**建 socket 之前**的能力门（`default.go:589-593`），不是"再加一次握手" |
| QUIC/H3 | 各协议自己的 handshake 完成 | 出站各自实现（tuic `outbound.go:145`、hysteria2 `:199-201`、naive 远端 tip 的 `dialWithH3Fallback`） | 本审计未逐个验证握手后的数据面，见 §14.3 |
| 代理隧道 | 代理协议握手成功 | 各 outbound | — |
| native bypass | 平台 `connect` 返回 | `outbound.go:440-493` | 判据必须与 userspace 等价（已做） |

### 6.2 connected UDP / unconnected UDP 的 arm 差异（合理）

* connected：与 stream 共用 `DialSerialNetwork` → 可竞争、可取消、带 setup context。
* unconnected：`ListenSerialNetworkPacket` → **串行**，因为"第一个 socket 成功者胜出且不可替换"（`default_parallel_network.go:234-261`）。这是**传输语义差异**，不是遗漏。
* 三个 packet 臂（literal / 候选表 / hostname）现在都问同一 `admissionPolicy`（`TestEveryPacketArmAnswersTheSamePolicy`、`TestTheSameGateAnswersBothDialAndPacketPaths`）。

### 6.3 数据面：MTU / PMTUD / 封装开销

* `common/dialer/path_mtu.go` 提供 `PacketOverheadCeiling` / `PathCapacity` / `QuicPayloadCeiling` / `ClampToCeiling` / `CeilingSuppliesTheSize`，并有 `PortMTUProvider`/`PortEncapOverheadProvider` 可选接口；`MinimumQUICInitialPacketSize = 1200`（`:41`）。
* 审计到的语义是"上限是**消费者契约**，不是线上保证"（`TestACeilingIsAConsumerContractNotAWireGuarantee`）。ICMPv6 PTB 的处理在 sing-tun/quic-go 侧，本仓库只提供 ceiling 计算——**本审计未在真机验证 PMTUD**（§14.3）。

### 6.4 平台差异（源码可证部分）

| 平台 | 能力观测 | 路由观测 | 特殊事实 |
|---|---|---|---|
| Windows | `network_family_route_windows.go`（GetIpForwardTable2） | `machineGlobalIPv6Route` | **WSA errno 与 syscall 常量不同**：`syscall.ENETUNREACH (0x20000043)` 永不等于 10051（`windows_socket_errors.go`，`dual_stack_scheduler.go:1011-1020` 引用），旧代码的相关判断在 Windows 上是死代码，现已按 WSA 码分类 |
| Windows（multicast 陷阱） | `usableGlobalIPv6Route:76-105` | 同上 | Windows 在**每个** IPv6 接口装 `ff00::/8` 路由；不做 multicast 排除时 `GlobalIPv6Egress` 恒为 Provisioned，egress 项在该平台**完全失效**（注释记录了实测：7 条 ZONE-ROW 被接受） |
| Linux | netlink dump + 自定义表也被接受（`linuxRouteQualifies` 忽略 table 参数） | `network_family_route_linux.go` | 只读 `RT_TABLE_MAIN` 会在策略路由机器上误报 RouteAbsent（注释 `:157-163` 明确） |
| Darwin | BSD RIB | `network_family_route_darwin.go` | 平台接口列表只给名字/索引，地址要回读系统视图（`network_family_entry.go:220-239` 同处理） |
| 跨平台构建 | `go build ./...`（others tags）通过 | — | **只证明可构建，不证明运行**（§13） |

### 6.5 代理边界与 Remote DNS（明确的设计边界）

* 本地代理 endpoint 受本机能力约束（正确）。
* 代理后的远端 target 由对端能力处理（正确，且**不能**被本机 gate 改写）。
* UDP relay 的远端目标同理；例外：若某协议在本地解析目标（WireGuard inner、SOCKS4 等），则回到"本机解析"语义并受本机策略（`TargetQueryOptions` 文档 `dialer.go:251-292`）。

### 6.6 Native bypass（快速路径的等价性）

`BypassBlockers` 的四个组成部分见 §2.7。审计确认：bypass 只在"literal 无候选、无 sniffed domain、无 rewrite"时被咨询（`outbound.go:461-465` 注释），且策略/能力/自地址三项与 userspace 同源。测试：`TestProductionDirectOutboundBecomesBypassableForLiteralFlows`、`TestNoAmbientPolicyStillAllowsTheBypass`、`TestEveryAmbientPolicyConditionRefuses`、`TestBlockersNameEveryBit`。

---

## 07 网络切换与生命周期专项

### 7.1 setup 所有权（已收敛）

* `dialSetupGate.begin`（`dial_setup.go:89-128`）：每代一个 ctx，`context.AfterFunc(generation, cancelSetup)`。
* `finishSetup`：先 `stopGenerationWatch()`，再 `cancelSetup()`。注释说明，前者不会阻止 transition 取消已建立的连接，因为连接不派生自 setup；它的作用是限制注册状态的累积（`:108-125`）。这一点也经过变异测试。
* **建连成功即转交**：`NewConnection` 在两个 dial 调用之后立即 `finishSetup()`（`conn.go:344`），此后 transition 不再能通过任何本层创建的 context 触达该连接。

### 7.2 代际与提交（已收敛）

* `beginTransition`（`network.go:1069-1139`）：先 `transitionStable=false`，再 `transitionOwner = generation+1`，最后在**释放锁之后**（deferred）`runtimeCoordinator.Advance`。注释记录了一个真实的锁序缺陷（两个 defer 的 LIFO 导致 publication 在持锁时运行，实测死锁）。
* `commitTransition`（`:1155-1173`）：token 比对与 settled 置位在同一临界区，防止两个并发的 transition 之一"替另一个提交"。
* `NetworkFamilyCapabilitySnapshot`（`:1221-1225`）：三元组在 `transitionAccess` 下读出，避免"capability 先读、epoch 后读"的撕裂。

### 7.3 能力重算（何时重新观测）

`network_environment.go:241-313`：**即使环境指纹不变也会重算能力并存储**（`:254-269` 解释：地址可能在同一个网络内变化，例如 DHCPv6 租约到期）。指纹变化时：compute → 写指纹 →（测试钩子）→ `beginTransition` → `storeFamilyCapability` → 解锁 → 后续 reset body。因此"新网络的能力"与"新代际"是**一次观测**。

### 7.4 已建连接与半开连接的不同权责

* 半开 dial：由 `dialSetupGate` 负责（transition 取消 setup）。
* 已建连接：由 `ConnectionManager` 的连接表 + `managedConnState.generation` 负责（`initConnState` 写入当时 generation，`conn.go:276-283`）；drain 扫描据其分类。
* **不追溯杀死已建流**：`dial_setup.go:40-46` 明确"transition 永不追溯杀死 established flow"。
* **drain 策略（本轮已逐段复核，`route/conn_reclaim.go`）**：
  * `reclaimPolicyFor`（`:224-235`）按原因分三档：`ReclaimNetworkTransition` = drain（不释放 shaper flow）、`ReclaimIdle` = 只回收"可证明空闲"的、`ReclaimDeadPath`/默认 = 全关并先 `scheduler.ReleaseAll()`（`:245-252`）。
  * drain 的资格是**两道条件同时成立**：`state.generation < staleGeneration`（`:307-312`，否则会把新网络上的连接当旧的关掉）**且** `idleFor` 已知且 ≥ `drainIdleGrace`（20s，`:57`、`:313-317`）。活动不可观测的连接**不**被关（`!known` → 不 eligible），这正是"不杀正在传输的流"的实现。
  * 未关闭的旧代际连接计入 `retained` 并启动 sweep（`:331-340`）；sweep 用**退避**（15s 起，每次无进展翻倍，上限 5 分钟，`:367-377`、`:400-443`），一旦回收成功立刻回到基础间隔；`Close` 时 `stopDrainSweep`（`:447-454`），所以不会出现"定时器打进已销毁的管理器"。
  * 阈值与 sweep 都打开 `dialGovernor` 的窗口（`:335`），与 §7.5 的 3s/24/500ms 相接。
  * 计数在**本次扫描内**统计（`seen/retained`，`:289-298`），不再用 `m.Count()` 冒充 drain 结果（注释记录了"真实设备上重连立刻开始，会把新连接的数算成被保全的旧连接"）。


### 7.5 幂等 / 顺序 / 资源上限

| 操作 | 幂等 | 顺序保证 | 上限 |
|---|---|---|---|
| `dialSetupGate.close` | 是（`closed` 早退） | 先置 closed → 再 cancel previous | — |
| `advanceAndBump` | 每次换新 ctx | bump 与换代同临界区 | — |
| `commitTransition` | 非 owner 直接返回 | 后 claim 者决定 settled | — |
| `familyHealth` | `recordSuccess` 可重复 | — | 256 桶，满则 clear |
| `FakeIPIssuanceLedger` | 序列自增 | — | `MaxIssuanceGenerations` |
| `dialGovernor` | `open` 取 max(end) | — | 24 槽 / 500ms 预算 / 3s 窗口 |

### 7.6 错误分类与健康学习一致性

见 §5.2 第 3 条与 `isFamilyPathFailure`。补充：**本机能力拒绝不进入健康学习**（`default.go:358-364` 明确：它不是 syscall 错误、不是 timeout、不是取消，因此不记录），这是正确的——"没有地址"不是"最近不走运"。

### 7.7 平台生命周期（需实机的部分）

| 项 | 源码可证 | 需实机 |
|---|---|---|
| Windows service / Apple NE / Android VPN 的生命周期通知 | 各平台 stub/实现存在（`network_environment_*.go`） | 事件丢失、重启后状态、VPN 重连时的 RA/DHCPv6 时序 |
| TUN 重绑 | entry presentment 明确"固定到重启"（`network_family_entry.go:25-41`） | 重绑后的用户可见行为 |
| 路由表"假默认路由" | predicate 已排除 host route / multicast / own interface | 真实 CPE 的路由形状 |

---

## 08 全内核跨组件契约审计

### 8.1 覆盖范围（本审计实际读到的）

* 出站/端点：`direct`、`socks`、`http`、`shadowsocks`、`vmess`、`vless`、`trojan`、`tuic`、`hysteria`、`hysteria2`、`anytls`、`naive`、`mieru`、`snell`、`shadowtls`、`ssh`、`tor`、`dns`、`block`、`bridge`、`group`（selector/urltest/loadbalance）、`wireguard`、`tailscale`（endpoint + tailcat）、`masque`、`openvpn`、`openconnect`；`route/failover.go` 与 `transport/http`、`transport/v2rayxhttp` 作为传输层参照。
* 入站：TUN、Mixed、SOCKS、HTTP、TProxy/Redirect、内部 service —— 只追到 `ConnectionManager` 为止（入站本身不在本次双栈范围内）。
* 平台：Windows/Linux/Darwin 的能力与路由观测（§6.4）。

### 8.2 五大主轴逐条结论

**① 接口降维**

| 位置 | 现状 | 判定 |
|---|---|---|
| `direct.Outbound` 的 dialer 字段 | 构造期解析成 `LocalDialSurface`（`Attempt`/`Network`/`Resolve`/`LocalFamily`），缺 `Network` 直接报错 | **已消除**（上一轮的核心成果） |
| `route/failover.go` 的 shim | 显式转发 `ParallelNetworkDialer` + `ConnectionCopyTuner`，编译期钉住（`:134-138`），且**故意不报告 dial chain**（`:211-242`） | **已消除**，且记录了"报告 `Upstream()` 引发跨包回归"的历史 |
| `selector`/`urltest`/`loadbalance` | 只委托 `DialContext`/`ListenPacket`（`selector.go:386-399`、`urltest.go:326-386`、`loadbalance.go:694-728`） | **DS-02 断层** |
| TLS 包装（anytls/trojan/vless/vmess） | `common/tls.defaultDialer` 只实现 `N.Dialer`（`common/tls/client.go:130-155`） | **不是缺陷**：它拨的是**服务器地址**，hostname 分支本身有完整调度；`Parallel*` 在这里没有被需要的语义 |
| `command`/`daemon` 探针 | `dialer.NewResolveDialer(ctx, outbound, true, …)` 包在 outbound 外层 | **已由 walk 修复**：`dialerLocalFamilyPolicy`/`findFamilyHealthOwner` 沿 `Upstream()`/`DialChain()` 找到真正的 owner（`dual_stack_recovery.go:377-390`、`default_parallel_network.go:104-116`） |

**② 重复权威**

| 主题 | 是否有第二权威 | 证据 |
|---|---|---|
| 地址族策略 | **无**（唯一 `effectiveFamilyStrategy`，每 dialer 记忆一次） | `resolve.go:545-558`、`TestTheEffectiveStrategyIsResolvedOncePerDialer` |
| 本机能力 | **无**（唯一 `netcap.Evaluate` ← 唯一 recompute） | `network_family_capability.go:91-116` |
| 网络代际 | **无**（`admission.generation()` 就是能力 ownership） | `admission.go:73-75` |
| retry / fallback | **有多个层，但语义分层**（见 DS-04） | §9.5 |
| DNS 本地/远端解析决策 | **两处**：`ResolveAuthority`（`dialer.go:144-189`）与静态校验器 `common/physicalpath`；注释说明二者现在**问同一函数** | `dialer.go:104-121`、`TestResolveAuthorityWithoutATransportManager` |

**③ 资源所有权**

已核（§03）。**`InterfaceUpdated` 的覆盖面（本轮实测统计）**：`protocol/` 下共 **27 个实现**——5 个属于入站/转发器（`direct/inbound`、`hysteria2/inbound`、`redirect/tproxy`、`tailscale/tailcat_inbound`、`tun/inbound`），1 个是 tailscale 内部节点（`tailscale/tailcat_node:625`），**其余 21 个是出站/端点对象**：`anytls`、`direct`、`group/urltest`、`http`、`hysteria`、`hysteria2`、`masque`、`mieru`、`naive`、`openconnect`、`openvpn`、`shadowsocks`、`snell`、`ssh`、`tailscale`(endpoint)、`tailcat_outbound`、`trojan`、`tuic`、`vless`、`vmess`、`wireguard`(endpoint)。
**没有实现该回调的出站**是 `socks`、`shadowtls`、`tor`、`dns`、`block`、`bridge`、`selector`、`loadbalance`、`cloudflare`；这些对象**不持有长生命周期 socket 或复用会话**（每次 flow 经共享 dialer 新建），因此"不实现"是合理的，而不是漏接。**本审计只对 `direct`、`naive`、`masque`、`wireguard` 的回调内容做了抽样阅读**（wireguard 在 `endpoint.go:204`），其余 17 个的**具体动作**未逐个验证——这是本报告 §14.3 的残留缺口，而不是"已验证"。

**④ 错误与回退语义**

* 取消 / 关闭 / EOF / 拒绝 / 超时 / 路径不可达在 `isFamilyPathFailure` 中被分开（`:1007-1061`），并有 Windows 专用 errno 表。
* 三层 fallback 的权责：**调度器内**（同族下一个候选）→ **调度器间**（族间 race）→ **组 outbound**（换成员）→ **协议内**（naive H3→H2、MASQUE H3→H2）。见 DS-04。
* 一处**已知且有意**的语义：`direct.Outbound.DialParallel` 传入的 `preferIPv6` 在 `scheduleNetwork` 中未被使用（`outbound.go:294/311` → `:351-353`），实际偏好由 effective strategy 决定。不是 bug，但是**死参数**，见 §11.1。

**⑤ 生产/测试对象图偏差**

* 生产图测试存在且用真实构造器：`TestDirectOutboundGraphRecoversTheWeChatLiteral`、`TestNetworkEntryPointRecoversTheWeChatLiteral`、`TestDirectOutboundPacketGraphRecoversTheSniffedDomain`、`TestTheProductionChainYieldsACompleteSurface`、根包 `box_*` 系列。
* **唯一 test-only 形态**：`attempts != dialer`（§11.1）。
* 反向证据：`surface_test.go:19-28` 明确说明它**不是** `NewLocalDialSurface` 的单元测试，而是用生产构造路径组装。

### 8.3 census 摘要（29 个 outbound/endpoint + 4 个非 outbound 参照）

| 维度 | 统计（HEAD，已验证） |
|---|---|
| 用 `dialer.New`/`NewWithOptions` 构造链 | 全部 24 个 `outbound.Register` + 5 个 endpoint client |
| `RemoteIsDomain` 取值 | 字面 `true`（direct/naive/tailscale/tailcat/cloudflare）、字面 `false`（tor）、`ServerIsDomain()`（多数）、peer 推导（wireguard）、URL 推导（openconnect）、helper（openvpn）、OR realm（hysteria2）、`M.IsDomainName`（mieru） |
| 自己实现 `ParallelNetworkDialer` | **仅 `direct`**（`outbound.go:32`） |
| 自建 socket 绕过 `common/dialer` | `tor`（`N.SystemDialer` 连本地 SOCKS，`outbound.go:201`）、`tailscale`/`tailcat`（netstack `ListenUDP`，自有用户态栈）、`wireguard`（pinned wireguard-go `StdNetBind`，但 ClientBind 走链） |
| 调 `adapter.ExtendContext` 写 metadata | 约一半（direct/socks/http/shadowsocks/snell/anytls/mieru/shadowtls；vmess/vless/trojan 只在 inner 类型里）。**未写的都是"不本地拨目标"的 outbound**，故不影响 sniff 恢复 |
| 自带族策略 | masque（`client.go:861/875`、`bootstrap_race.go:463/481`）、hysteria2 realm resolver（`:120-122`）、tailscale（`destination.IsIPv6()` 选本地地址） |

### 8.4 合理特例（明确"已核对且正确，不应改"）

1. **WireGuard inner/outer 分工**：inner 目标用全局 DNS 规则（空 `DNSQueryOptions{}`），peer 走 `ResolvePeer`（`wireguard/endpoint.go:501-530`）。这不是"丢了 domain_resolver"，而是两个不同的 DNS 平面。
2. **tor 的 `N.SystemDialer`**：连的是本机 `127.0.0.1` 的 Tor SOCKS 端口，属 loopback，**不应**受 mark/interface 策略（否则 Tor 自己会被路由进隧道）。
3. **tailscale/tailcat 的 netstack `ListenUDP`**：是它自己的用户态网络栈，不是本机 underlay。
4. **L3 endpoint（openvpn/openconnect/masque）不实现 `Parallel*`**：目标名/地址由隧道承载，候选竞争无意义。
5. **`direct.Outbound.DialChain()` 而非 `Upstream()`**：`Upstream()` 会被 `common.Cast` 全仓遍历，曾导致 WireGuard bind 选择被改变（`outbound.go:161-168`、`default_parallel_network.go:128-141`）。

### 8.5 远端 tip（44 提交）新增内容的评估

远端 tip 上新增了 `protocol/naive/h3_fallback.go`（H3→H2 回退）与 `transport/http/capsule.go`。naive 的 H3 回退实现质量良好，且**明确不构成第二个健康系统**：

* 窗口 30s（`h3FallbackWindow`），单 outbound 私有状态，无 per-target 状态；
* 用**尝试序号**保证"更早开始、更晚返回"的失败不能覆盖更晚的成功（`begin/claim/markBroken/clear`）；
* 网络切换时 `reset()`（`outbound.go:314`），且 reset 通过 `f.outcome = f.attempt` 防止旧的在途尝试在重置后重新启用窗口；
* 失败分类用 Chromium `NetError` 码，**先判码再判 `Is(net.ErrClosed)`**（注释记录了"顺序反了会永久关掉重试"的陷阱）；
* H3 尝试有自己的预算切分（`h3ProbeContext`，最多 10s，且把剩余预算对半分给 H2 重试），`98387b223d8` 的修复正是"让 H2 重试有机会成功"。

**它属于 §9.5 的"协议内传输回退"层**，与族/候选/组回退正交。见 DS-04 的相乘风险（未实测）。

---

## 09 Mihomo / Xray / 上游 sing-box 对照

**固定引用（本轮实际浅克隆并读过文件）**

| 项目 | 引用 | 日期 |
|---|---|---|
| Mihomo | `MetaCubeX/mihomo` @ `94b13aed49b8dc41973e4bb3f721c5fa2c5c5c1f`（branch `Alpha`） | 2026-10-13 |
| Xray-core | `XTLS/Xray-core` @ `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`（v26.10.17） | 2026-10-17 |
| 上游 sing-box | `SagerNet/sing-box` @ `dfb80349bf5779ec0a3f834c9ec2794345bff8f0`（`testing`） | 2026-10-13 |
| 当前 fork | `Piggy-Cat-bit-shadow/sing-box` @ `b721b988fef…`（v0.1.21） | 2026-10-19 |

### 9.1 逐项对照表

| 机制 | Mihomo（源码语义） | Xray（源码语义） | 上游 sing-box（源码语义） | 当前 fork（源码语义） | 差异性质 | 可复用价值 | 侵入度 | 风险 |
|---|---|---|---|---|---|---|---|---|
| **族策略执行位置** | 全局 `DisableIPv6` 开关，在 **resolver 查询层**跳过 AAAA（`component/resolver/resolver.go:32,107,161-167`） | `LookupForIP` 用 `IPOption{IPv4Enable,IPv6Enable}` 在 **DNS 层**决定查哪族（`transport/internet/dialer.go:87-95`） | `lookupWithRules` 对 `IPv4Only/IPv6Only` **只发一个 qtype**（`dns/router.go:988-999`） | 策略是**每流的准入值**，在**每一个本机 dial 臂**执行（literal / 完整 / 流式 / 三 packet 臂 / bypass） | fork 是**socket 层**决策；三者是 **DNS 层**决策 | 无（fork 已更强） | — | 若把 fork 的策略"优化"回 DNS 层，literal 会失去保护（上游正是如此） |
| **literal 的严格策略** | literal 不经 resolver，直接 `parseAddr` 返回（`dialer.go:68-75`），无族校验 | literal 直接入 dial，无族校验（`dialer.go:251` 只在 `dest.Address.Family().IsDomain()` 时套策略） | `!destination.IsDomain()` → **直接** `d.dialer.DialContext`（`common/dialer/resolve.go:99-101`），无校验 | `admission.strategyAllows(addr)` 对原地址生效（`resolve.go:604-621`），且 packet literal 臂也生效（`:874-880`） | **fork 独有** | 无 | — | — |
| **本机能力模型** | 无（`DisableIPv6` 是用户配置，不是设备事实） | 无 | 无 | `netcap.Evaluate` 从接口地址 + 路由表推导，仅允许"否定"为硬判定（`netcap.go:369-483`） | **fork 独有** | 无 | — | 平台观察缺失时必须 fail-open（已实现） |
| **候选调度粒度** | 每族一个 racer，族内**串行**（`dialer.go:224-269`） | 每 IP 一个 goroutine，`TryDelayMs` 错峰 + `MaxConcurrentTry` 上限（`happy_eyeballs.go:17-98`） | 每族一个 racer，族内串行（`default_parallel_network.go:47-130`） | 每**候选**一个 attempt，交错 + 每 `fallbackDelay` 启一个 + 失败即刻推进（`dual_stack_scheduler.go:151-612`） | fork 粒度最细 | Xray 的 `maxConcurrentTry` 上限思想（fork 目前以候选总数自然有界，无显式上限） | 低 | 引入并发上限会改变"快速失败推进"语义，需实测 |
| **迟到族（A/AAAA 一先一后）** | 不支持：先完整解析再拨（`parseAddr` → `ips`） | 不支持：`TcpRaceDial` 的 ips 在拨号前定稿 | 不支持：`group.Run` 等两族都完成（`dns/router.go:1015-1019`） | 支持：`LookupFamilies` 流式 + late candidate（`resolve.go:296-445`、调度器 `:386-528`） | **fork 独有** | 无 | — | 已用 50ms grace + timer 单所有者约束 |
| **literal 的域名恢复** | 无 | 无（只有 `AddressPortStrategy` 的地址/端口改写，`dialer.go:138-149`） | 无 | sniffed domain → late candidates（`dual_stack_parallel_network.go:266-328`） | **fork 独有** | 无 | — | 需 recursion guard 与域名校验（已实现） |
| **DNS 回退 vs socket 回退** | 两者都没有（`DisableIPv6` 之外） | **只有 DNS 查询回退**：`LookupForIP:96-103` 在空结果时再查 fallback 族 | 只有 DNS 层单族选择 | 既有 DNS 单族选择，也有 **socket 级**候选竞争 | fork 区分了两者 | Xray 的 `FallbackIP4/6` 是**查询语义**，不能当作连接失败后的切换（任务书明确要求区分） | — | — |
| **失败聚合** | `errors.Join(errs...)`（`dialer.go:313`），primary 错误加前缀（`:302`） | 返回 `r.err`（最后一条）或 handshake 错误 | **只返回 `primary.error`**，fallback 的错误被丢弃（`default_parallel_network.go:123`） | `E.Errors(failures...)`，每个错误带 address（`dual_stack_scheduler.go:569`）；DNS 失败优先于"没候选"（`resolve.go:488-503`） | fork 最完整 | 无 | — | — |
| **UDP 成功定义** | socket 创建即成功（`dialer.go:79-88` ListenPacket） | 同（`system_listener.go`） | 同 | 同，但**建 socket 之前**有族门 + 串行走位（`default_parallel_network.go:232-280`），并在文档中明确"UDP socket 成功≠可达" | fork 多一道**前置**拒绝 | 无 | — | 若未来给 UDP 加握手语义，会改变发送路径 |
| **生命周期/代际** | 无代际；`DisableIPv6` 全局 | 无代际；mark/interface 每拨读取 | 有 `NetworkResetCounter` 等能力，但无 per-flow 能力快照 | **每流一次捕获** + 代际校验（`admission.go:73-75`、`dial_setup.go`、`network_observation.go:190-217`） | fork 独有 | 无 | — | 已用"一次捕获"消除两次读 |
| **扩展协议复用公共机制** | outbound 各自建 dialer，族逻辑集中在 `component/dialer` | `DialSystem` 是所有 transport 的唯一出口（`dialer.go:226-282`），协议经 `RegisterTransportDialer` 注册 | 全部经 `dialer.NewWithOptions` | 全部经 `dialer.NewWithOptions`（§08 census），**仅 `direct` 拿到候选规划面** | 与上游同构，fork 多一层 surface | 无 | — | — |

### 9.2 七项逐条结论（任务书 §10 要求）

1. **DNS A/AAAA 与 family strategy 的执行位置**：Mihomo=resolver；Xray=DNS lookup（IPOption）；上游=DNS lookup（单 qtype）；fork=**socket 前准入**（且 DNS 层也按策略只发一族）。**结论**：fork 把"策略"从"解析什么"提升为"允许拨什么"，因此 literal 也受约束；这是三者都没有的语义。
2. **Direct/TCP 的 single-entry dialer、Happy Eyeballs、回退顺序、取消**：Mihomo 用 300ms 族级 fallback ticker；Xray 用 `TryDelayMs`+`MaxConcurrentTry`+RFC8305 排序，且**未配 HappyEyeballs 时随机挑一个 IP 只拨一次**（`dialer.go:262-263`）；上游用"两族 racer + 族内串行"。fork 用候选级调度器，并对 loser 做 `workerGroup.Wait()` + 统一 close。
3. **IPv4/IPv6-only 网络与路由/能力判断**：三者都**没有**能力模型；Xray 通过 `localAddr`（outbound 的 gateway）间接影响 `IPOption`（`dialer.go:92-95`），这不是设备能力；fork 是唯一从"接口地址 + 路由表"推导并只允许否定判定的实现。
4. **UDP/QUIC 与 TCP 是否共享错误的成功定义**：Mihomo/Xray/上游对 **unconnected UDP** 都以 socket 创建为成功；fork 同样，但**在创建前拒绝可证明不可达的族**，并在注释里写明这不是"可达证明"。QUIC 的成功在各协议自己的 handshake。
5. **连接/DNS/网络切换生命周期**：三者都没有 per-flow 能力快照或 setup 所有权转移；fork 用 `dialSetupGate`（setup-only 所有权）+ 代际三元组快照解决。可复用价值：**没有**——这是 fork 为解决自身事故引入的机制。
6. **失败原因聚合与可观测性**：上游丢 fallback 错误（`primary.error`）；Xray 返回最后一条；Mihomo join 但无原因分类；fork 有 `localFamilyRefusal`（family+address 一起走）与 `isFamilyPathFailure` 分类。**fork 最强，但仍漏一条路径（DS-01）**。
7. **扩展协议如何复用公共机制、是否有生产入口绕行**：三者都经各自统一 dialer；fork 的 census 显示 24/24 走 `dialer.New*`，绕行只有 tor 的 loopback（合理）、tailscale/tailcat 的用户态栈（合理）、wireguard 的 pinned bind（其 ClientBind 仍走链）。

### 9.3 应该借鉴的原则

* **Xray 的 `MaxConcurrentTry`**：为"候选极多"的目标给并发尝试一个显式上限。fork 目前以"每 `fallbackDelay` 启一个"自然有界，但在 `fallbackDelay` 很小、候选很多时会同时在途较多。**仅作为可选优化**，且必须保留"快速失败即刻推进"语义。
* **Xray 的 RFC8305 排序细节**（interleave 计数、`PrioritizeIpv6`）：fork 的 `interleaveCandidates` 是 1:1 交错，Xray 支持 `Interleave` 比例。差异仅在多地址同族时的启动顺序，**当前证据不足以证明需要**。
* **Mihomo 的"fallback 成功先持有、到 ticker 才返回"**：fork 用 `preferredFamilyGrace`（DNS 层）与调度器 cadence 表达同一意图，语义更清晰，**不建议改成 Mihomo 的形状**。

### 9.4 不值得复制 / 不能复制的实现

* **Xray 的"随机挑一个 IP"**：它是"不做 socket 级回退"的另一种说法，与 fork 的失败分类目标相反。
* **把 `DisableIPv6` 当设备能力**：那是配置开关，无法表达"这台机器有 IPv6 地址但没有公网路由"。
* **用 DNS 查询回退冒充连接回退**：Xray `LookupForIP:96-103` 是查询语义；任务书明确禁止这种等同。
* **在 IPv4-only 的 Direct 上"想办法连 IPv6-only 目标"**：没有 NAT64/代理路径时这是网络约束，不是缺 `if`（任务书 §10 原则，本报告完全同意）。

### 9.5 回退层次图（用于 DS-04）

```text
① DNS 层        ipv4_only / ipv6_only 只发一族；prefer_* 只影响排序
② 候选层        scheduler：同族下一个候选；fallbackDelay 节流；失败即刻推进
③ 族层          preferred family 先发；非优先族 50ms grace；health 软偏好
④ 接口层        network_strategy primary → fallback（per-interface racer / serial）
⑤ 协议层        naive H3→H2（30s 窗口）、MASQUE H3→H2（transport/http）
⑥ 组层          urltest / loadbalance / failover：换成员
```

六层**语义正交**，但**失败会逐层放大尝试次数**：一次用户连接最多可能经历 ①1 次查询 → ②N 个候选 → ③2 族 → ④2 组接口 → ⑤2 种传输 → ⑥组内 M 个成员。最坏情况的乘积需要实测才能断言（DS-04，`UNVERIFIED_HYPOTHESIS`）。

---

## 10 问题证据清单

> 编号规则：`DS-xx`（dual-stack）。每条按任务书 §13.10 的模板写全；证据等级严格按 §12.3 的定义使用，`CONFIRMED_BUG` 与 `HIGH_CONFIDENCE_RISK` 不混计。

### DS-01 `CONFIRMED_BUG`｜流式 hostname 路径丢失"本机拒绝"的原因，退化为 `no dial candidates`

**证据等级**：`CONFIRMED_BUG`（`VERIFIED_SOURCE` + `VERIFIED_REPRO`）
**严重性**：低（诊断/可观测性；**不改变连通性行为**，不误驱动 retry/health）
**用户可感知影响**：IPv4-only 或"有 IPv6 地址但无公网 IPv6 出口"的机器上，访问 AAAA-only 域名（`hif-dliq.deepseek.com` 形状）时，日志写 `open connection to …: no dial candidates`，而事实是"本机没有公网 IPv6 路径"。运维会去查 DNS 和节点，而真正原因是本机网络能力。

**生产调用链**

```text
inbound → ConnectionManager.NewConnection            route/conn.go:316
  metadata.Destination 是域名 → this.DialContext      conn.go:337
    → (*direct.Outbound).DialContext                  protocol/direct/outbound.go:221
      → surface.Attempt.DialContext                   outbound.go:235   [= resolveParallelNetworkDialer]
        → (*resolveDialer).DialContext                resolve.go:121
          非 literal → parallel=true → raceResolvedName   resolve.go:149-202
            plan = planCandidates(nil, Invalid, strategy)  :471
            scheduler.dialWithLateCandidatesAdmitting      :478
              late 到达 → s.localFamily.hardUndialable(addr) → true → continue   dual_stack_scheduler.go:457-459
              （无任何 refused 记录；filterLocalFamily 面对空计划返回 familyInvalid  :693-702 + :718-720）
              流关闭 → started==0 && !refused.refused() → return E.New("no dial candidates")  :400-407
            err 非空 → lookupErr==nil（AAAA 有地址，succeeded>0）→ 原样返回 err  :488-503
```

**触发条件**

1. 目标为**域名**（不是 literal）；
2. router 实现 `DNSDualStackRouter`（`route.Router` 恒实现）；
3. 目标的所有应答族都被**本机能力**拒绝（典型：AAAA-only + `GlobalIPv6Egress=Unavailable`，或某族 `StateUnavailable`）；
4. 策略未参与（否则会走 strategy 分支并得到正确文案）。

**源码位置**
`common/dialer/dual_stack_scheduler.go:151-212`（早期分支）、`:400-414`（流关闭结论）、`:444-465`（late 静默丢弃）、`:639-651`（`refused()` 语义）、`:693-748`（`filterLocalFamily` 对空计划无拒绝）。
另：`common/dialer/resolve.go`（raceResolvedName 的空 `plan` 构造）。

**日志/测试/实验依据**（本次实跑，`VERIFIED_REPRO`）

在外置临时副本 `C:\Deepseek\_audit_copy_20261020`（仓库外，未进入审计工作树）中加入探针 `common/dialer/zz_audit_probe_test.go`，用它构造报告的机器（`outageDialer(windowsLinkLocalOnly())`：IPv6 provisioned + egress unavailable）与只在 AAAA 应答的 `fakeDomainRouter`：

```text
AUDIT_PROBE hostname error = "no dial candidates"
AUDIT_PROBE hostname names-the-machine = false (egress), false (family)
AUDIT_PROBE literal  error = "local network has no public ipv6 path: this machine has ipv6 addressing
                              but no route to a global ipv6 destination (link-local and unique-local
                              destinations are unaffected)"
AUDIT_PROBE packet   error = "local network has no public ipv6 path: …"   （同 literal）
```

即：同一条链上，**literal 臂与 packet 臂都正确**，**只有流式 hostname 臂退化**。

**为何现有测试没有发现**
现有 `refusal_verdict_test.go` 的两条关键用例都建立在**非空计划**上（计划被能力过滤后 `refused.refused()==true`，`:92-116`），以及"空计划 + 什么都没拒绝"必须保持 `no dial candidates`（`:118-135`）。`TestUnavailableFamilyIsRefusedFromTheLateStream`（`family_capability_test.go:219`）覆盖的是"一族被拒、另一族可用"，**恰好没有覆盖"全部 late 候选都被拒"**。因此 372 个 `common/dialer` 测试全绿而该路径无断言。

**根因归属**：**本地实现**（本仓库的错误构造），不是组件契约缺陷，也不是环境约束。

**最小安全修复方向（本轮不执行）**

在 `candidateScheduler` 的 late 分支记录第一枚被拒候选（复用已有 `localFamilyRefusal{family,address}`），并在 `started==0` 的结论处优先返回该拒绝（与 `refused` 同等对待）：

```text
// 概念补丁，不改本轮源码
if s.localFamily.hardUndialable(candidate.address) {
    if !lateRefusal.refused() { lateRefusal = localFamilyRefusal{family: candidate.family, address: candidate.address} }
    continue
}
…
if started == 0 {
    if lateRefusal.refused() { return localFamilyRefusalError(lateRefusal, s.localFamily) }
    if refused.refused() { … }
    return E.New("no dial candidates")
}
```

同时补一条回归：空计划 + late 流只含被能力拒绝的候选 + 流关闭 ⇒ 错误必须 `errors.Is(err, errLocalIPv6EgressUnavailable)` 且**不含** `no dial candidates`。

**替代方案与权衡**

* 在 `raceResolvedName` 层把 `admission.localFamily` 的判定提前到 feeder（每个 family 结果先问一次能力）：能给出同样文案，但把"能力判定"从"每个候选到达时重读"改为"feeder 捕获时读一次"，**削弱了 superseded capture 的 fail-open 语义**（`dual_stack_scheduler.go:450-456` 明确为什么要在到达时重读）。**不推荐。**
* 改文案为 "no admissible dial candidates"：只改善措辞，不提供原因。**不解决。**

**风险/波及面**：改动只在失败构造路径，不触碰任何尝试/调度逻辑；唯一风险是"多记一个字段"被误解为健康状态（不会——它不写入 health）。建议回归：`TestARefusalThatDroppedNothingKeepsItsOwnMessage` 必须仍绿（防止把"什么都没拒绝"误报成拒绝）。

**可供下一阶段验证的步骤**：用 §13.3 的探针（可直接移植为仓库内测试）在修复前后各跑一次，断言三个臂的错误串一致。

---

### DS-02 `HIGH_CONFIDENCE_RISK`｜组 outbound 不转发 `ParallelNetworkDialer`，候选表退化为逐地址串行

**证据等级**：`HIGH_CONFIDENCE_RISK`（代码路径确定；缺"在生产网络上造成秒级延迟"的实测）
**严重性**：中（延迟/鲁棒性；无正确性错误）
**用户可感知影响**：`selector` / `urltest` / 非 failover 的 `loadbalance` 后面挂 `direct` 成员时，一个 dual-stack 域名解析出 `[v6, v4]`，而 v6 黑洞：**先等满该 attempt 的 connect 超时**才轮到 v4；同一配置若直接把 `direct` 当出站，则由调度器在 `fallbackDelay`（默认 `N.DefaultFallbackDelay`）内并发尝试。两条路径的用户体验不同。

**生产调用链**

```text
ConnectionManager.NewConnection               route/conn.go:334-335
  dialer.DialSerialNetwork(this=*group.Selector, …)      default_parallel_network.go:15
    this 不实现 ParallelNetworkDialer / ParallelInterfaceDialer   （selector 只实现 DialContext/ListenPacket）
    → 普通串行循环 for addr { this.DialContext(ctx, net, SocksaddrFrom(addr, port)) }   :34-42
       → (*Selector).DialContext → selected.DialContext → 成员（如 direct）的 resolve 链
```

**触发条件**：目标有多个候选地址（`action: resolve` 或域名解析后的 `DestinationAddresses`）+ 出站是组 + 首选候选族的路由黑洞/不可达。

**源码位置**
`protocol/group/selector.go:386-399`、`protocol/group/urltest.go:326-386`、`protocol/group/loadbalance.go:694-728`（只委托 `DialContext`/`ListenPacket`）；`common/dialer/default_parallel_network.go:15-44`（串行退化）；对照 `route/failover.go:127-138`（failover 组**有**逐能力转发 + 编译期钉住）。

**日志/测试/实验依据**：源码（`VERIFIED_SOURCE`）。仓库内 `TestTheFailoverShimDoesNotHideTheParallelDialSurface`（`route/failover_shim_surface_test.go:121`）与 `TestTheShimForwardsItsCapabilitiesAndReportsNoChain`（`:204`）专门测"shim 隐藏 `ParallelNetworkDialer` 会让 `DialSerialNetwork` 退化"，但**没有**针对 selector/urltest/loadbalance 的同类测试。

**为何现有测试没有发现**：`failover` 组的 shim 有专测；普通组的接线**从未被断言**为"必须转发能力面"，而组包自身的测试只验证"委托给了选中成员"。

**根因归属**：**组件契约**（组 outbound 的抽象只声明 `adapter.Outbound`，没有"必须转发拨号能力面"的契约）；与上游行为一致，所以不是本轮引入的回归。

**最小安全修复方向（本轮不执行）**
给 `Selector`/`URLTest`/`LoadBalance` 增加 `DialParallelNetwork`/`ListenSerialNetworkPacket` 方法，转发给**当前选中/候选成员**（与 `failoverDialer` 同一模式），并用编译期断言钉住：

```go
var (
    _ dialer.ParallelNetworkDialer = (*Selector)(nil)
    _ dialer.ParallelNetworkDialer = (*URLTest)(nil)
    _ dialer.ParallelNetworkDialer = (*LoadBalance)(nil)
)
```

**替代方案与权衡**

* 只做 `selector`（最常用）；`urltest` 的成员选择是**已定**的（`selected`），转发同样安全。
* 若组需要"每次尝试重新选成员"（loadbalance），转发时必须把成员解析放在 `DialParallelNetwork` 里，逐 attempt 重新选——与 `failoverDialer` 的做法相同（`route/failover.go:184-193` 把选项放 ctx 交给 group）。
* **不做**的代价：行为与上游一致，且用户有 workaround（把 direct 直接作为出站，或用 failover 组）。这是它可以停在 `HIGH_CONFIDENCE_RISK` 而不是 `CONFIRMED_BUG` 的原因。

**风险/波及面**：组是热路径；转发能力面会改变"谁来决定 attempt 顺序"。建议先加**只读断言测试**（组必须实现该接口），再改行为；改动后必须回归 `TestAPlainLeafStillDialsThroughTheFailoverGroup` 与组内所有选择测试。

---

### DS-03 `HIGH_CONFIDENCE_RISK`｜recovery 流式生产者未 join，goroutine 可活过本次 dial

**证据等级**：`HIGH_CONFIDENCE_RISK`（代码事实确定；"实际泄漏/影响"缺 runtime 证据）
**严重性**：低
**用户可感知影响**：无直接可见影响；在极端情况下（DNS transport 不尊重 ctx、或查询长时间不返回）会有一个短期 goroutine 与一个 1 槽 channel 存活到查询返回。

**生产调用链**

```text
resolve.go:660-667
  recoveryCtx, cancelRecovery := context.WithCancel(ctx)
  defer cancelRecovery()
  go func() { recovered <- d.recoverCandidates(recoveryCtx, …) }()      ← 无 WaitGroup
       → dual_stack_recovery.go:124-169 流式分支
            go func() { _ = LookupFamilies(streamCtx, …); collected <- nil }()   :129-146   ← 也未 join
```

**触发条件**：任一 literal + sniffed domain 的流（与 DS-01 同一批流量）；需要底层 `LookupFamilies` 慢于调用方返回。

**源码位置**：`common/dialer/resolve.go:660-667`；`common/dialer/dual_stack_recovery.go:124-169`。
**对照（已正确）**：`resolve.go:294-312 + 485-486` 对 hostname 的两生产者做了 `producers.Wait()`；`dual_stack_parallel_network.go:288-328` 的 `recoveryFeed.stop()` 也 cancel + Wait。

**日志/测试/实验依据**：源码（`VERIFIED_SOURCE`）。`TestRecoveryProducerIsJoined`（`common/dialer/recovery_producer_ownership_test.go:110`）覆盖的是 **feed** 的生产者（写 channel 的那层），不是 `recoverCandidates` 内部的 streaming goroutine。

**为何现有测试没有发现**：断言"goroutine 已退出"需要一个可观测的 join 点；`recoverCandidates` 没有返回 join 句柄，因此测试只能数 goroutine 或观察 channel 行为，两者都不稳定。

**根因归属**：**本地实现**（所有权不完整），影响有界。

**最小安全修复方向**：给流式分支加 `sync.WaitGroup`（或复用 `recoveryFeed` 的模式），在 `recoverCandidates` 返回前 `cancelStream(); producers.Wait()`。注意：`collected` 是 1 槽缓冲，join 前必须先 cancel，否则生产者可能在 `collected <- result` 上阻塞——现有 `select` 已含 `<-streamCtx.Done()`，故 cancel 后可退出。

**风险/波及面**：只在 recovery 内部；对正确性无影响，对"取消后立即释放"有改善。回归：`TestRecoveryProducerIsJoined`、`TestARouteFlapLeavesNoGoroutineOrTimerBehind`。

---

### DS-04 `UNVERIFIED_HYPOTHESIS`｜六层回退相乘导致的尝试放大

**证据等级**：`UNVERIFIED_HYPOTHESIS`（结构确定，放大倍数未实测）
**严重性**：低-中（延迟、流量；无正确性错误）
**内容**：§9.5 的六层都**独立**决定"再试一次"，没有全局预算。最坏形状：组内 M 个成员 × 每成员 2 种传输（H3→H2）× 每传输 2 族 × 每族 N 个候选。现有约束：scheduler 每 `fallbackDelay` 才启一个候选；naive 的 H3 窗口 30s 且 per-outbound；MASQUE 的 H3 窗口在 `transport/http`（5 分钟级，源码注释引用了该窗口）；`dialGovernor` 只在 transition 后 3s 内限流 24 个并发。

**为什么不定为风险以上**：没有任何日志/实测显示放大发生；`urltest`/`loadbalance` 的成员切换本身有预算；naive 的 `h3ProbeContext` 已把 H3 尝试预算切成一半，保证 H2 有机会成功。

**建议的测量方法（不实施）**：在测试网络里让 v6 黑洞 + H3 被 UDP 阻断 + 组内 3 个成员，测量"用户可见失败时间"与"实际 socket 尝试次数"（`splice` 诊断或 `powerreport` 计数可复用）。若乘积 > 3× 单路径，再考虑**全局尝试预算**（一个新状态，需谨慎）。

---

### DS-05 `UNVERIFIED_HYPOTHESIS`｜entry presentment 锁存与网络变化的长期语义

**证据等级**：`UNVERIFIED_HYPOTHESIS`（设计已自述，用户影响未实测）
**内容**：`route/network_family_entry.go:25-41` 明确：entry IPv6 判定**在启动时取一次并锁存到重启**；dial gate 是实时的。因此存在两个方向：

* 启动在 IPv4-only 网络（commit `SuppressIPv6`，TUN 以 IPv4-only 打开）→ 用户换到 dual-stack 网络后，**DNS 仍对应用抑制 AAAA**，直到重启；dial gate 已允许 IPv6，但 TUN 没有 IPv6 地址，应用也拿不到 AAAA。**不产生错误连接**，只是"IPv6 到重启为止不可用"。
* 启动在 dual-stack → 用户换到 IPv4-only：TUN 仍有 IPv6 地址、DNS 仍给 AAAA，但 live gate 会快速拒绝全局 IPv6（`errLocalIPv6EgressUnavailable`），应用回退 v4。**每次连接多一次快速失败**。

**为什么不是 bug**：第 25-41 行把取舍写清楚了（"no split state" vs "restart to apply"），并且两个方向都 fail-safe（不产生假 IPv6 连接）。**需要实机**才能判断用户是否能接受"切网后需重启才获得 IPv6"。

---

### 10.5 被反证（`NOT_A_BUG`）的推测

| 推测 | 反证 | 证据 |
|---|---|---|
| `localFamilyPolicy` 会把自身 TUN 的 `::/0` 当作 underlay 能力 | **不会**：`usableGlobalIPv6Route` 显式排除 own interface（按 index 与 name 两道）与 `/128` host route，并排除 multicast | `route/network_family_route.go:112-124`、`:76-105`；`machineGlobalIPv6Route:223-236` 收集 own index/name；`route` 测试 `TestAnOnLinkHostRouteIsNotAWayOut`、`TestAGenerationWithoutAGlobalIPv6RoutePublishesTheEgressVerdict` |
| `admissionPolicy` 会把代理远端目标当成本地直连端点 | **不会**：detour/代理链结构上到不了 `DefaultDialer.DialContext`；`family_capability.go:57-68` 给出结构论证，`TestRemoteTargetIsNeverRewrittenByLocalVerdict` 钉住 | 同上 + `TestDetourDestinationIsNotFilteredAtTheUpperLayer` |
| WireGuard inner 目标用空 `DNSQueryOptions{}` 是"丢了 domain_resolver" | **不是**：inner/outer 是两个 DNS 平面，代码注释与 `ResolvePeer` 分工明确 | `protocol/wireguard/endpoint.go:501-530` |
| Fake-IP 范围应被整体拒绝（否则 synthetic 地址会外泄） | **不是**：整体拒绝会误伤真实 routed space；只有"本实例发放过 + 无记录"才拒绝，且有 `198.18.x` 正控 | `route/route.go:1175-1191`、`e2e/zz_fip_placeholder_escape_test.go:1011` |
| `direct.Outbound.DialParallel` 丢掉了 preferIPv6（回退偏好失效） | **不影响结果**：真正偏好由 `effectiveFamilyStrategy` 决定（`resolve.go:545-558`），`DialParallelNetwork` 的包装层自己按策略规划；`preferIPv6` 是遗留参数 | `outbound.go:294/311`、`dual_stack_parallel_network.go:212`（`domainStrategyFor(len>0 && candidates[0].Is6())`） |

---

## 11 架构统一与减法建议

### 11.1 有证据支持的收敛机会（3 项，全部"可删/可简化"，不新增状态）

| # | 现状 | 建议 | 是否新增状态 | 复杂度/性能 | 兼容性 | 证据 |
|---|---|---|---|---|---|---|
| C-1 | `resolveParallelNetworkDialer.attempts` 与 `dialer` 在生产恒相等（`NewResolveDialer:62-76` 两者传同一对象）；`attemptDialer()` 的分支与 `dual_stack_parallel_network.go:371-391` 的长注释是为一个**生产构造器产生不出的形态**写的 | **保留字段但修正文档**，或删除字段并让 `attemptDialer()` 直接返回 `d.dialer`。删除前必须确认没有外部构造 | 否 | 删除 1 个字段 + 1 个分支；零风险 | 内部字段，无 API 影响 | `NewResolveDialer` 是唯一构造点；`attempts` 只在 `dual_stack_parallel_network_test.go:180` 被设为不同值 |
| C-2 | `direct.Outbound.domainStrategy` 写入后**无读取者**（包内 grep 只有 `:63` 声明与 `:100` 赋值） | **删除字段与赋值**。真正的策略来自 `surface.Resolve.EffectiveFamilyStrategy()`（`:474`） | 否 | 删 2 行 | 内部字段 | 同左 |
| C-3 | `resolveParallelNetworkDialer.DialParallelInterface` / `ListenSerialInterfacePacket` 的 **domain 分支**在生产无调用者（生产要么走 `DialParallelNetwork`，要么由叶子自己处理 literal） | **保留方法**（接口契约），但把 domain 分支的注释改为"接口完整性，非生产路径"，避免下一位读者以为它被用到 | 否 | 0 | — | §02.8 分派表 + census：`DialParallelInterface` 的生产调用点只有 `default_parallel_network.go:28/214` 与 `dual_stack_parallel_network.go:222`，三者传的都是 **literal** socksaddr |

> **不建议**为这 3 项做任何"统一重构"。它们合计约 40 行，且 C-1/C-3 的注释包含真实事故记录。删除的收益远小于误改的风险——这正是任务书"不要为统一而统一"的反面案例。

### 11.2 明确"保持现状"（理由 + 证据）

| 机制 | 为什么不应该动 |
|---|---|
| `admissionPolicy` 作为**值**（而不是带行为的管理器） | 它确保每条流使用独立且固定的策略快照；改成对象会引入生命周期与第二次读取，违背本轮的设计目标（`admission.go:30-43`） |
| `localFamilyGate` 的**一次 ownership 读** | 逐候选读 ownership 会让一个 plan 由两个代际组成（`family_capability.go:162-184`）；测试 `TestOnePlanCostsOneOwnershipRead` |
| `LocalFamilyBlocked` 的**反向极性**（不确定=不阻止） | bypass 是"优化开关"，fail-open 方向是放行；dial gate 相反。两者共用同一 policy 但极性不同，**这是设计**（`family_capability.go:99-118`） |
| `familyHealth` 只做**软偏好** | 它能省一次 fallback delay，不能禁止候选；改成硬判定会把"一次失败"放大成"族被禁用"（`dual_stack_scheduler.go:903-923`） |
| `netcap` 只允许**否定**为硬判定 | 探针/周期探测会引入常驻成本与误判；正例交由真实流量学习（`netcap.go:20-31`，`StateValidated`/`StateDegraded` 故意保留未用） |
| entry presentment 的**锁存** | 实时重算会让 DNS 与 TUN 地址不一致（"DNS 说 IPv6 回来了，接口没有"），这是已付出过的代价（`network_family_entry.go:25-41`） |
| `dialSetupGate` 的 `stopGenerationWatch` | 注释说明它**不**阻止 transition 取消已建连接（连接不派生自 setup），而是限制注册状态累积；删掉后会积累注册（`dial_setup.go:108-125`） |
| `direct.Outbound.DialChain()` 而非 `Upstream()` | `Upstream()` 被 `common.Cast` 全仓遍历，曾把 WireGuard 的 bind 从 `ClientBind` 改成 `StdNetBind` 并丢 netns（`outbound.go:161-168`） |
| `route/failover.go` 不报告 dial chain | 它描述的成员不是实际拨号的成员（组每次重选），跨过它会读到错的 health（`failover.go:211-242`） |
| 组 outbound 的**串行**（除 DS-02） | UDP/packet 的"首个 socket 成功即胜出"与组的"每次尝试重新选成员"都要求串行语义；改成并发会重复建连 |
| `preferredFamilyGrace` 与 `fallbackDelay` 两个常量分开 | 前者是**解析时间尺度**（等另一族答案），后者是**连接时间尺度**（错峰尝试）；合并会让 literal recovery 提前 6 倍启动（`resolve.go:982-989`、`:1213-1229`） |

---

## 12 施工优先级草案（等待用户审核，**未获实施授权**）

### 12.1 优先级

| 优先级 | 项 | 预期修改范围 | 验证方案 | 回退路径 |
|---|---|---|---|---|
| **P0（先补测试，再改）** | DS-01 诊断保真 | `common/dialer/dual_stack_scheduler.go`（约 +8 行）+ 1 个新测试（从 §13.3 探针移植） | 新测试 + 现有 `refusal_verdict_test.go` 三条全绿 + `-race` | 纯新增分支；删掉即回退 |
| **P1** | DS-03 join recovery 流生产者 | `common/dialer/dual_stack_recovery.go`（+ WaitGroup）+ `resolve.go:664-667` 同步等待 | `TestRecoveryProducerIsJoined` 扩展 + `-race` | 加 join 不改语义；可单独回退 |
| **P2** | DS-02 组转发能力面 | `protocol/group/{selector,urltest,loadbalance}.go` + 编译期断言 + 接线测试 | 先加"必须实现"断言（不改行为）→ 再转发 → 组回归 + `-race` | 逐步：断言可独立合入；转发行为可回退为直接委托 |
| **P2** | §11.1 C-1/C-2 清理 | 2 个文件，删除死字段/修文档 | 编译 + 全量 `common/dialer` + `protocol/direct` | 纯删除 |
| **观察项** | DS-04/DS-05 | 不施工，先测量（DS-04 方法、DS-05 需实机） | — | — |

### 12.2 必须实机验证的项

1. Windows/Android 上"有 IPv6 地址、无公网 IPv6 出口"的真实形状（本报告只用单测模拟）。
2. entry presentment 锁存导致"切网后 IPv6 需重启"的用户可接受度。
3. 组 outbound 转发能力面后，多成员场景的实际 P50/P95 连接时延。
4. PMTU / ICMPv6 PTB 被阻断时的握手成功但数据失败（§6.3 未验证）。

### 12.3 最小长期契约、回归与指标（任务书问题 10）

**契约（5 条，建议写进 `KNOWLEDGE/INVARIANTS.md` 的候选）**

1. **单一准入值**：任何本机 dial 臂在做"是否尝试这个地址"的决定时，必须问 `admissionPolicy`（或其派生 gate），**不得**自行组合 strategy 与 capability。
2. **能力只允许否定**：`StateUnavailable`/`GlobalIPv6Egress=Unavailable` 才能硬拒绝；`Unknown`/`Provisioned` 一律放行。
3. **一次捕获，一次代际**：一个 flow 只能捕获一次 policy；policy、scheduler 的 `networkGeneration`、health 的桶必须来自同一次捕获。
4. **所有权转移**：setup 上下文在连接建立后**没有**任何权威；任何 transition 不得追溯杀死已建连接。
5. **原因保真**：任何"没有可用候选"的结论必须能回答"是 DNS 没给、策略排除、本机不可用、还是全部连接失败"；不允许退化为不带原因的 `no dial candidates`（**DS-01 即此契约的违反**）。

**集成回归（3 条，建议新增）**

* `TestEveryRefusalPathNamesItsCause`：对 {literal, hostname-streaming, hostname-complete, packet-literal, packet-hostname} × {strategy-excluded, family-absent, egress-absent} 的矩阵，断言错误身份（`errors.Is`）与文案，而不是只断言"有错误"。
* `TestGroupForwardsTheDialSurface`：selector/urltest/loadbalance 必须实现 `dialer.ParallelNetworkDialer`（编译期断言即可）。
* `TestRecoveryStreamProducerIsJoined`：用一个"忽略 ctx 的 router"证明 `recoverCandidates` 返回前 goroutine 已退出（需先修 DS-03）。

**观测指标（5 个，均可用现有日志/计数实现，不新增常驻探测）**

1. 每连接的 `admission verdict` 分布（allowed / strategy-excluded / no-local-family / no-global-egress）——当前只在拒绝路径可观测，建议在 debug 级对每 flow 记一行。
2. `no dial candidates` 的出现次数（修复后应趋近 0，且每次都能回答"是哪一类"）。
3. 每 flow 的实际 attempt 数（可复用 `powerreport.CountDial` 或 scheduler 的诊断钩子）——用于 DS-04 的放大测量。
4. family health 命中率（`fallbackImmediately==true` 的比例）——衡量软偏好是否真的在省时间。
5. `entry presentment` 的取值与"实际拨号被 gate 拒绝"的次数——衡量 DS-05 的用户影响。

---

## 13 测试与实验实际执行记录

**环境**：Windows x64，`go1.25.5 windows/amd64`，`GOPATH=C:\Deepseek\_gotool\gopath`，`GOFLAGS=-mod=readonly`，tags = `release/DEFAULT_BUILD_TAGS_OTHERS`（`with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname`）。

**对工作树的写入**：本审计只做了两类写入——(1) 用 `git fetch --all --prune` 更新远端跟踪引用（不改工作树、不改 `testing` 分支、不产生对象之外的副作用）；(2) 新增本报告。**结束时 `git status --porcelain` 仅显示本报告一个未跟踪文件**，无 staged、无 commit、无 push。

**一次性环境清理**：探针用的仓库副本 `C:\Deepseek\_audit_copy_20261020`（212 MB，24 144 个文件）与远端 tip 的导出目录 `C:\Deepseek\_audit_tip_20261020`（约 13.7 MB zip 展开）都在结果入库后删除；探针源文件保留在仓库外的 `C:\Deepseek\_audit_20261020\zz_audit_probe_test.go`（2 773 字节），全部运行日志（含 `tip_build.log`、`tip_test.log`、`race_count20.log`、`e2e_fip.log`、`build_linux.log`、`build_darwin.log`）也保留在同一目录，以便复现而不污染仓库。

### 13.1 构建与静态检查

| 命令 | 结果 | 等级 |
|---|---|---|
| `go build -tags $TAGS ./...` | **PASS**（exit 0，含首次模块下载） | `VERIFIED_UNIT`（可构建） |
| `go vet -tags $TAGS ./common/dialer/ ./common/netcap/ ./dns/ ./protocol/direct/` | **PASS**（无输出） | `VERIFIED_UNIT` |

### 13.2 单元测试（无 race）

| 命令 | 结果 |
|---|---|
| `go test -tags $TAGS -count=1 ./common/dialer/` | **PASS** `ok … 16.224s`（372 个测试） |
| `go test -tags $TAGS -count=1 ./common/netcap/` | **PASS** `ok … 0.025s` |
| `go test -tags $TAGS -count=1 ./dns/` | **PASS** `ok … 8.026s` |
| `go test -tags $TAGS -count=1 ./protocol/direct/` | **PASS** `ok … 0.022s` |
| `go test -tags $TAGS -count=1 -skip TestBackpressure ./route/` | **PASS** `ok … 37.574s`（554 个测试，扣除 backpressure 长测） |
| `go test -tags $TAGS -count=1 -run 'TestFIP' ./e2e/` | **PASS** `ok … 11.951s`（Fake-IP 端到端，详见 §13.5） |

> `./route/` **不跳过** `TestBackpressure_*` 时，本机在 `TestBackpressure_KernelGiveUpWindow` 上长时间不收敛（它与双栈无关，靠真实内核缓冲行为），整包运行被迫中止。**这不是被测代码的失败**，本报告不把它记为 FAIL，只记录为"该用例不适用于本机环境"。

### 13.3 外置复现探针（`VERIFIED_REPRO`）

位置：`C:\Deepseek\_audit_copy_20261020`（**仓库之外**的副本，`robocopy /XD .git`，24 144 个文件）。新增文件：`common/dialer/zz_audit_probe_test.go`（仅存在于副本，**未进入审计工作树**）。

```text
go test -tags $TAGS -run 'TestAuditProbe' -v -count=1 ./common/dialer/

--- PASS: TestAuditProbeHostnameOnlyRefusedFamily
    AUDIT_PROBE hostname error = "no dial candidates"
    AUDIT_PROBE hostname names-the-machine = false (egress), false (family)
--- PASS: TestAuditProbeLiteralRefusedFamily
    AUDIT_PROBE literal error = "local network has no public ipv6 path: …"
    AUDIT_PROBE literal names-the-machine = true (egress)
--- PASS: TestAuditProbePacketHostnameOnlyRefusedFamily
    AUDIT_PROBE packet error = "local network has no public ipv6 path: …"
    AUDIT_PROBE packet names-the-machine = true (egress)
```

该探针**不修改任何生产代码**，只构造对象并读取错误串；`PASS` 表示"观测到了预期形状"，不是"系统正确"。

### 13.4 `-race`：**上一轮的 BLOCKED 在本环境已解除**

上一轮（`NETWORK_CAPABILITY_TRANSITION_PHASE04_FINAL_2026-10-18.md`）记录 `-race = BLOCKED_BY_ENVIRONMENT`（无 mingw-w64 gcc）。本审计实测：

| 尝试 | 结果 |
|---|---|
| `CGO_ENABLED=1 go test -race -c ./common/dialer`（默认 `CC=gcc`） | **失败**：`cgo: C compiler "gcc" not found` |
| `CC=<Swift clang>` | **失败**：`clang: error: unsupported option '-mthreads' for target 'x86_64-unknown-windows-msvc'` |
| `CC=C:\msys64\mingw64\bin\gcc.exe`（`gcc 16.2.0`，MSYS2） | **成功**：`go test -race -c` 产出 22 827 323 字节的测试二进制 |

因此本轮**实际运行**了 race：

| 命令 | 结果 |
|---|---|
| `go test -tags $TAGS -race -count=1 ./common/dialer/` | **PASS** `ok … 20.149s` |
| `go test -tags $TAGS -race -count=3 ./common/dialer/` | **PASS** `ok … 50.473s` |
| `go test -tags $TAGS -race -count=20 -run 'Scheduler\|LateCandidate\|LateFamily\|LateStream\|Literal\|Recovery' ./common/dialer/` | **PASS** `ok … 154.016s`（任务书建议的 `-count=20` 高频并发用例） |
| `go test -tags $TAGS -race -count=1 ./common/netcap/ ./dns/ ./protocol/direct/` | **PASS**（`1.046s` / `9.000s` / `1.062s`） |
| `go test -tags $TAGS -race -count=1 -skip TestBackpressure ./route/` | **PASS** `ok … 46.555s` |

**证据等级**：`VERIFIED_UNIT`（`-race` 下通过）。**仍不等同于 CI**：本仓库内没有任何 workflow-run 记录，故不使用 `VERIFIED_CI`。

**对上一轮记录的修正**：`BLOCKED` 的原因是"`CC` 未指向已安装的 mingw gcc"，而不是"机器上没有 mingw-w64"。`C:\msys64\mingw64\bin\gcc.exe` 一直存在。后续轮次应直接设置 `CC`/`PATH` 后跑 `-race`，不要再把它记为环境阻塞。

### 13.5 收尾补充运行（本轮完成）

| 命令 | 结果 | 说明 |
|---|---|---|
| `go test -tags $TAGS -count=1 -run 'TestFIP' ./e2e/` | **PASS** `ok … 11.951s` | Fake-IP 逃逸的**端到端**套件在本机通过（此前报告只引源码与单测）|
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags $TAGS ./...` | **PASS**（exit 0） | 只证明可构建，**不证明 Linux 运行行为** |
| `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -tags $TAGS ./...` | **PASS**（exit 0） | 同上；Darwin 的 `network_family_route_darwin.go` 参与编译 |
| 远端 tip 导出（`git archive --format=zip origin/testing`）后 `go build -tags $TAGS ./...` | **PASS**（exit 0） | 44 个提交的增量可构建；导出目录在仓库外，未改动审计分支 |
| 同上 `go test ./protocol/naive/ ./protocol/masque/` | **PASS**（`naive 0.589s`、`masque 60.809s`） | 远端 tip 上新增的 naive H3→H2 回退与 masque 变更**其自身测试通过** |
| 同上 `go test ./transport/http/`（整包） | **1 次 FAIL**：`TestActiveInboundStopsBecauseThePeerIsStarved` 测得 `12.331s`，断言要求 `<9s` | 见下方"计时断言"说明 |
| 同上 `go test -count=2 -run 'TestActiveInboundStopsBecauseThePeerIsStarved' ./transport/http/` | **PASS ×2**（`8.32s` / `8.15s`） | 单独重复运行通过 |

**关于那次 FAIL 的诚实判定**：该测试断言的是**墙钟上界**（关闭必须在 9s 内发生），而首跑时本机同时有 3 个 Go 构建/测试任务在并发（本次收尾的 e2e、两个交叉编译、tip 构建）。同一条测试单独重复两次都在 8.2s 左右通过。因此判定为**host-load-sensitive 的计时断言在高负载下越界**，不是确定性的实现缺陷；但它确实说明该断言在 CI 并发环境下有偶发失败的可能。**另注**：该测试文件与用例在审计 HEAD（`b721b988f`）上**不存在**，是远端 44 个提交新增的（`transport/http/close_liveness_mechanism_test.go`），因此不属于本次审计基线的回归。

### 13.6 未运行 / 受限

| 项 | 状态 | 若要恢复需要 |
|---|---|---|
| 不做 `-skip` 的 `./route/` | 未完成（`TestBackpressure_KernelGiveUpWindow` 在本机不收敛） | 有真实阻塞式内核缓冲行为的环境，或单独隔离该测试 |
| 跨平台**运行**验证 | 未做（只有 Windows 本机测试 + Linux/Darwin 交叉编译） | Linux/Darwin/Android 实机 |
| 真机 PMTUD / ICMPv6 | 未做 | 受控 MTU 环境 |
| 其余 17 个 outbound 的 `InterfaceUpdated` 具体动作 | 未逐个阅读（已抽样 4 个：direct/naive/masque/wireguard） | 逐包阅读会话关闭/重建代码 |
| GitHub Actions | **未触发、未查看**（任务书禁止） | — |

---

## 14 证据索引 / 审计缺口 / 待确认问题

### 14.1 源码索引（按主题）

| 主题 | 文件:行 |
|---|---|
| 准入策略与三种错误 | `common/dialer/admission.go:44-47,55-60,94-114,140-145,153-161,181-193,237-275` |
| 本机能力策略与极性 | `common/dialer/family_capability.go:29-52,91-130,143-149,162-212,243-274,286-293,305-307,339-376` |
| 能力模型 | `common/netcap/netcap.go:57-127,130-168,191-239,269-287,300-356,428-510,531-551` |
| 观察者与所有权谓词 | `common/dialer/network_observation.go:28-49,80-88,103-163,190-217` |
| 单目标 dial / 恢复 / 竞争 | `common/dialer/resolve.go:121-203,219-505,545-558,560-734,738-791,839-913,1057-1165,1230-1235` |
| 候选规划 | `common/dialer/dual_stack.go:38-83,96-111,119-189,263-285,294-331` |
| 候选调度 | `common/dialer/dual_stack_scheduler.go:62-99,107-212,229-308,310-380,381-528,530-612,639-672,693-748,808-951,953-1061` |
| 网络入口的发现/调度 | `common/dialer/dual_stack_parallel_network.go:88-143,157-187,193-228,251-328,330-349,357-369,386-391,399-419,438-450` |
| 恢复与过滤器 | `common/dialer/dual_stack_recovery.go:42-56,63-184,199-202,249-277,289-296,319-348,377-390,404-406,414-443` |
| 叶子门与代际 | `common/dialer/default.go:318-402,409-463,465-516,542-622,633-708,754-767,907-914,916-924` |
| 分派与 owner 查找 | `common/dialer/default_parallel_network.go:15-44,64-94,104-116,118-141,143-189,192-220,222-281` |
| 接口竞争 | `common/dialer/default_parallel_interface.go:16-109,111-191,193-223,225-290` |
| 能力面 | `common/dialer/surface.go:43-88`、`common/dialer/capability.go:36-114` |
| Direct 出站 | `protocol/direct/outbound.go:31-45,51-115,131-171,221-251,253-312,314-353,355-405,407-494` |
| 网络管理器 | `route/network.go:1046-1077,1069-1139,1141-1173,1175-1206,1208-1239,1241-1249` |
| 能力发布 | `route/network_environment.go:241-314`、`route/network_family_capability.go:11-148` |
| 路由观测 | `route/network_family_route.go:10-126,186-238`、`route/network_family_route_{windows,linux,darwin}.go` |
| 入口判定 | `route/network_family_entry.go:13-41,55-122,124-163,165-186,188-263,265-292` |
| dial setup | `route/dial_setup.go:8-80,82-128,130-198` |
| dial governor | `route/dial_governor.go:11-53,74-81,89-161,203-213` |
| 连接回收 / drain | `route/conn_reclaim.go:25-80,80-148,196-235,243-346,348-396,398-443,445-476` |
| TLS 包装拨号器 | `common/tls/client.go:125-161`（`defaultDialer` 只实现 `N.Dialer`，`:155` 调内层 `DialContext`） |
| 接口事件入口 | `route/network.go:635-669,1457-1492,1514-1531`、`experimental/libbox/monitor.go:144` |
| 连接分派 | `route/conn.go:285-314,316-355,551-637` |
| Fake-IP | `route/route.go:1122-1198,1207-1227,1229-1256`、`adapter/fakeip.go`、`adapter/fakeip_issuance.go` |
| DNS 流 | `dns/router_dual_stack.go:34-123`、`dns/router.go:1123-1219`、`dns/entry_ipv6.go:1-95` |
| 组 outbound | `protocol/group/selector.go:386-399`、`protocol/group/urltest.go:326-386`、`protocol/group/loadbalance.go:694-728`、`protocol/group/loadbalance_failover.go:901-904` |
| failover shim | `route/failover.go:106-111,121-138,140-159,161-209,211-242` |
| naive H3 回退（远端 tip） | `protocol/naive/h3_fallback.go`（`origin/testing` 上的文件）、`protocol/naive/outbound.go:45-47,286,314,338` |

### 14.2 外部源码索引

| 项目 | 文件:行 | 内容 |
|---|---|---|
| Mihomo | `component/dialer/dialer.go:25,224-314,316-355,380` | 300ms 族回退、族级 racer、每 IP 并发 |
| Mihomo | `component/resolver/resolver.go:32,107,161-167,190,210,247` | 全局 `DisableIPv6` 与族排序 |
| Xray | `transport/internet/dialer.go:87-109,138-149,226-282` | `LookupForIP`（含查询回退）、AddressPort 策略、`DialSystem` 单出口 |
| Xray | `transport/internet/happy_eyeballs.go:17-98,100-157` | RFC8305 式错峰竞争与排序 |
| 上游 sing-box | `common/dialer/resolve.go:94-112` | literal 直连、无策略校验 |
| 上游 sing-box | `common/dialer/default_parallel_network.go:47-130` | 两族 racer，只返回 `primary.error` |
| 上游 sing-box | `dns/router.go:948-1020` | 严格策略单 qtype、两族 `group.Run` |

### 14.3 审计缺口（本轮收尾后的准确清单）

**本轮已关闭的缺口**（收尾时补齐，见 §13.5 与 §07.4）：

| 原缺口 | 关闭方式 |
|---|---|
| `e2e/` 套件未运行 | `-run 'TestFIP'` **PASS 11.951s**；Fake-IP 逃逸现在有端到端证据 |
| `route/conn_reclaim.go` 未复核 | 已逐段阅读并按策略/sweep/上限写进 §07.4 |
| 远端 tip 的 naive/masque 新代码未跑测试 | tip 导出后 `go build` PASS、`protocol/naive`/`protocol/masque` 测试 PASS |
| `common/tls.client` 只按普查引用 | 已读 `common/tls/client.go:125-161`，确认 `defaultDialer` 只有 `N.Dialer` + `DialTLSContext`，`dialContext` 在 `:155` 调内层 `DialContext` |
| 跨平台"未验证" | 追加 `GOOS=linux`、`GOOS=darwin` 交叉编译 **PASS**（仍**不**等于平台运行验证） |
| V.8（非默认接口地址变化不进能力） | 在非平台接口路径上**未再复现**：`notifyInterfaceUpdate:1467` 与 `updateInterface:1530` 都先 `observeNonPlatformInterfaces()`，回调与具体接口无关；`UpdateInterfaces:663-669` 也是"先刷 finder 再发布"。**平台接口客户端**的刷新频率取决于 `libbox/monitor.go:144` 的调用节奏，本审计未验证 |

**仍然存在的缺口**（如实列出，不掩饰）：

1. **原始用户日志不可得**（`hif-dliq` / `szminorshort`）→ 无法做逐 ID 关联（§1.3）。需要用户提供原始日志文件。
2. **`./route/` 在不跳过 `TestBackpressure_*` 时未跑完**（`TestBackpressure_KernelGiveUpWindow` 在本机不收敛；环境行为，非测试失败）。
3. **其余 17 个 outbound 的 `InterfaceUpdated` 具体动作未逐个阅读**（已抽样 `direct`/`naive`/`masque`/`wireguard`）；QUIC/mux 的关闭/重建语义因此仍属未验证（§03 表末行、§08.2 ③）。
4. **PMTU / ICMPv6 / NAT64 真实行为未验证**（仓库只提供 ceiling 计算）。
5. **跨平台运行未验证**（交叉编译通过不等于行为通过）。
6. **计时断言的环境敏感性**：远端 tip 的 `TestActiveInboundStopsBecauseThePeerIsStarved` 在并发构建负载下测得 12.331s（上限 9s）而 FAIL，单独重复两次 8.32s/8.15s 通过。它不在审计 HEAD 上（增量新增），本审计不把它计为基线回归，但记为**CI 并发下的偶发风险**。

### 14.4 建议的索引条目（本轮**未提交**，供用户审核后自行落地）

`KNOWLEDGE/reports/README.md` 的 `### core/` 表末尾可追加：

```markdown
| `DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md` | 2026-10-20 | 双栈与跨组件只读审计 at `b721b988f` (v0.1.21)：准入权威收敛的验收、两张系统图、状态所有权表、DNS/Fake-IP/策略专项、候选调度与家庭健康、TCP/UDP/QUIC 成功定义、网络切换生命周期、29 个 outbound 的接入普查、Mihomo/Xray/上游对照。1 个 `CONFIRMED_BUG`（DS-01 流式 hostname 丢失拒绝原因，外置探针复现）、2 个 `HIGH_CONFIDENCE_RISK`（组 outbound 不转发候选能力面；recovery 流生产者未 join）、7 项 `ALREADY_FIXED`。**`-race` 在本环境首次可用**（MSYS2 mingw gcc 16.2.0）并通过 | current |
```

---

## 15 最终审计结论

**问题性质：局部接口/诊断补丁 + 一处组件契约缺口，不存在需要大改的架构缺陷。**

* **只需个别补丁**：DS-01（约 8 行 + 1 个测试）——它不影响任何连接结果，只影响"事故复盘时能不能一眼看到原因"。
* **局部接口收敛**：DS-03（recovery 生产者 join）与 §11.1 的 3 处死代码/死文档。
* **跨组件契约**：DS-02（组 outbound 不转发候选能力面）。这是本报告唯一涉及"多个包"的问题，且**与上游行为一致**，属于长期契约缺位而不是本轮回归。
* **不需要大改**：双栈的准入权威、能力观测、候选规划、调度、代际、setup 所有权**已经收敛**，且每一处收敛都有对应的反例测试与事故注释。把它们推倒重来会同时丢掉这些证据。

**证据强度自评**：DS-01 有外置探针的 `VERIFIED_REPRO`；DS-02/DS-03 是 `VERIFIED_SOURCE`（代码路径确定，影响未实测）；DS-04/DS-05 明确标为未验证。所有"已修复"结论都同时给了源码位置与仓库内测试名，且本报告**实际运行**了这些测试（含 `-race`、`-count=3`、`-count=20` 的高频族/恢复用例）、Fake-IP 端到端套件、以及 Linux/Darwin 交叉编译与远端 tip 的构建/测试。

**收尾后的证据边界**：本报告不能证明的只有五件事——原始日志的逐 ID 关联、`TestBackpressure` 不跳过时的整包运行、其余 17 个 outbound 的 `InterfaceUpdated` 具体动作、PMTU/ICMPv6 的真机行为、跨平台运行行为。这五项都在 §14.3 列明，且都不影响本报告的结论方向。

**一句话**：这是一个已经把"每一族地址能不能拨、为什么不能拨、谁有权决定"做成单一权威的系统；剩下的问题集中在**"拒绝理由的最后一百米"**和**"组的接口转发"**，而不是架构方向。

---

## 附录 A：任务书 §11 的 25 个发散问题逐条回答

| # | 问题 | 结论 | 证据 |
|---|---|---|---|
| 1 | `resolveParallelNetworkDialer` 的恢复是 network-level，不同入口是否只保留 interface-level 能力？ | **是，已完全修复**：Direct 构造期解析 `LocalDialSurface`，缺规划面即构造失败；两条 network 入口（stream/packet）都走 wrapper | `surface.go:65-88`、`outbound.go:87-93/351-353/403-405` |
| 2 | 包装为 detour / outbound chain 后 owner 能否向内传播？ | **能**：`dialerLocalFamilyPolicy`/`findFamilyHealthOwner` 沿 `Upstream()`/`DialChain()` 走；detour 被**拒绝**穿过（语义正确） | `dual_stack_recovery.go:377-390`、`default_parallel_network.go:143-189`、`TestTheCapabilityWalkDoesNotCrossADetour` |
| 3 | AAAA 先返回但本机禁止，迟到 A 是否被错过？ | **不会**：选族用 `admittedRecoveredCandidates`；late 侧每次到达重判 | §4.2、`TestRecoverySkipsAnUndialableAnswerToReachAUsableOne` |
| 4 | initial 与 late 是否同一 policy 快照？ | **是**：`admission` 每流一次捕获，`lateAdmission()` 由它派生 | `admission.go:55-60,153-161`、`TestOneFlowCapturesThePolicyExactlyOnce` |
| 5 | `admissionPolicy` 是否把 proxy remote target 当 local direct endpoint？ | **不会**：结构上到不了；有专测 | `family_capability.go:57-68`、`TestRemoteTargetIsNeverRewrittenByLocalVerdict` |
| 6 | 判 GUA 路由时是否把自身 TUN 路由当 underlay？ | **不会**：own index/name 双排除 + `/128` + multicast 排除 | `network_family_route.go:112-124`、`:76-105` |
| 7 | 被策略排除的候选是否会被 original promotion 再加回？ | **不会**：`plannableOriginal` 先过 `admits`；`mergeRecovered` 同 | `dual_stack_parallel_network.go:208-212,337-349`、`TestAStrategyExcludedOriginalIsNeverPromotedBack` |
| 8 | family health 是否把 caller cancel / loser cancel / 切网当失败？ | **不会**：`parentAlive` + 错误分类排除 | `dual_stack_scheduler.go:243-266,1007-1061`、`TestCancellationDoesNotRecordFamilyFailure` |
| 9 | latency probe / URLTest 是否建立在不同对象图上？ | **URLTest 走的是成员 `DialContext`**，与本报告 DS-02 同源；它不经过候选调度，因此探测与实际业务在"候选顺序"上可能不同（探测只拨一个地址） | `protocol/group/urltest.go:326-386` + §11.2 |
| 10 | Fake-IP 过期/重启后 synthetic 地址是否有路径直达 socket？ | **有保护**：四态判定 + ledger + 持久化 retired interval + 正控 | `route.go:1122-1198`、`adapter/fakeip.go` |
| 11 | DNS leak 判定是否只看域名？ | 本报告**不**这么做；追到 transport/出口才判定 | §4.9 |
| 12 | H3/QUIC 缓存与 generation 冲突时旧 session 如何清理？ | **未逐个验证**（缺口 §14.3-3）。统一入口是 `InterfaceUpdated`（出站/端点侧 21 个实现）；`direct`/`naive`/`masque`/`wireguard` 已抽样阅读，其余 17 个的具体动作未核 | §08.2 ③ |
| 13 | packet listener 先建成功、首包失败，是否被误报为可用族？ | **不会误报为"族可用"**：调度器不做 UDP 竞争；建 socket 前的族门是唯一断言；首包失败的语义由协议层承担 | `default_parallel_network.go:232-261`、`default.go:589-593` |
| 14 | `FallbackDelay=0`、空候选、DNS 成功但全被过滤时是否死等？ | **不死等**：0 → `N.DefaultFallbackDelay`；空计划+流关闭 → 立即结论；策略全排除 → 立即策略错误 | `resolve.go:1230-1235`、`dual_stack_scheduler.go:400-414` |
| 15 | 旧网络的 family-health 是否在新网络造成长期偏置？ | **不会**：按 generation 分桶，跨网不可见；同网内 15s 过期 | `dual_stack_scheduler.go:808-852`、`TestFamilyHealthIsScopedToTheNetworkGeneration` |
| 16 | `context.WithCancel` / `ExtendContext` / metadata 复制是否让 sniffed domain 丢失或越界？ | **不丢失**：`adapter.WithContext` 在 `NewConnection` 入口写一次，recovery 只读；越界风险由 `validRecoveryDomain` 限制 | `conn.go:317`、`dual_stack_recovery.go:78-85,414-443` |
| 17 | 参数 `AsIS` 是否与 DNSRouter 有效策略不一致？ | **已一致**：`effectiveFamilyStrategy` 问 router；`actionResolve` 同样 | `resolve.go:545-558`、`dialer.go:104-121` |
| 18 | 不同 build tag 是否让统一接口"名义存在、语义不同"？ | **有一处真实差异**：Windows 的 WSA errno 与 syscall 常量不同，已用独立表处理；其余平台差异在路由读取器，不在契约 | `windows_socket_errors.go`、`TestWindowsWSAErrorChainIsClassified` |
| 19 | PMTU/ICMPv6 被阻断时，握手成功但数据失败是否被误归类为 DNS/族失败？ | **不会归为族失败**（`isFamilyPathFailure` 只认 path/timeout）；但**是否被恰当上报未验证**（缺口） | `dual_stack_scheduler.go:1007-1061`、§14.3-4 |
| 20 | 多候选并发失败时错误聚合是否只返回最后一个？ | **不是**：`E.Errors(failures...)` 聚合全部，每个带地址 | `dual_stack_scheduler.go:569,590-596` |
| 21 | 上游同步后新增 dialer/packet path 是否进入统一机制？ | census 显示 24/24 走 `dialer.New*`；新增的 `naive` H3 回退是**协议内**层，未绕开公共 dialer | §08.3、§8.5 |
| 22 | 连接池/shared session 下，一个 flow 的策略/sniff/网络上下文是否污染其他？ | **不会**：policy 是值、每流捕获；health 只共享"族的软偏好"，不含目标/域名 | `admission.go:30-43`、`dual_stack_scheduler.go:808-852` |
| 23 | 正确逻辑是否只在 `go test` fixture 里、真实 constructor 不组合？ | **不是**：生产图测试用真实 constructor；唯一 test-only 形态是 `attempts` | §08.2 ⑤ |
| 24 | 六层回退是否互相放大成重试风暴？ | **结构上可能，未实测**（DS-04）；建议先测量 | §9.5、DS-04 |
| 25 | 能否用更少状态/wrapper/重复检查实现同样正确性？ | **基本不能**：现有状态都能对应到一个已发生的事故；只有 3 处死代码可删（§11.1），其余**保留理由充分** | §11 |

---

## 附录 B：任务书 §12.2 十二条不变量逐条核对

| # | 不变量 | 结论 | 证据 |
|---|---|---|---|
| 1 | Strict `IPv4Only` 不得出现最终 IPv6 socket 尝试 | **成立**（三个 packet 臂 + literal + hostname + serial；路由阶段 `LookupFamilies` 只发 A） | `resolve.go:604-621,874-880,904-907`、`dual_stack_parallel_network.go:102-132,163-185`、`TestAStrictFamilyPolicyIsAppliedOnEveryPath`、`TestPacketPathNeverDialsAFamilyTheStrategyExcluded` |
| 2 | 本机明确不可拨的 family 不应造成无意义 timeout；Unknown 保守 | **成立**（`StateUnavailable` 硬拒绝；其余放行；`restrictsNothing` 与 `undialable` 一致性有专测） | `netcap.go:234-239,269-287`、`family_capability.go:191-212,286-293`、`TestRestrictsNothingNeverDisagreesWithTheGate` |
| 3 | 原始 literal 可用时不得因慢 DNS 增加前置延迟 | **成立** | `resolve.go:133-148,716-731`、`TestLiteralDialIsNotBlockedBySlowRecovery`、`TestHealthyLiteralIsNotDelayedByDiscovery` |
| 4 | Late candidate 不得绕过 strict/capability；同步/异步准入含义一致 | **成立**（唯一的 `lateAdmission` + 每到达重判能力） | `admission.go:153-161`、`dual_stack_scheduler.go:457-465`、`TestALateCandidateTheStrategyExcludesIsNotAttempted` |
| 5 | Fake-IP provenance 确认时不得把 synthetic 目标拨到公网 | **成立** | `route.go:1145-1191`、§4.5 |
| 6 | 代理远端 target 的族不受本机 Direct gate 限制 | **成立** | `family_capability.go:57-68`、`TestRemoteTargetIsNeverRewrittenByLocalVerdict` |
| 7 | Network generation N 的能力不能当作 N+1 的事实 | **成立**（ownership 谓词三态；代际快照同临界区） | `family_capability.go:41-52,243-251`、`network_observation.go:190-217`、`TestOldVerdictCannotDenyNewGeneration*`（6 个） |
| 8 | Race winner 返回后，loser/DNS 生产者/timer 有可证明清理 | **基本成立**；缺口 DS-03 | `dual_stack_scheduler.go:304-308`、`resolve.go:485-486`、`dual_stack_parallel_network.go:141` |
| 9 | UDP socket 成功不代表端到端交付成功 | **成立且被文档化**（没有把 UDP 放进竞争；族门在建 socket 前） | `default_parallel_network.go:232-261` |
| 10 | 每条失败保留 DNS/配置/能力/连接/取消的必要信息 | **有一条路径违反（DS-01）**；其余成立 | §5.4 表 |
| 11 | 严格策略不得被 capability 放宽 | **成立** | `admission.go:94-102`、`TestStrictStrategyIsNotWidenedByTheCapability`、`TestUnknownAndProvisionedFamiliesAreNeverFiltered` |
| 12 | 失败不得因包装层丢失根因 | **成立**（`E.Cause` 链 + `localFamilyRefusal` 携带地址） | `resolve.go:488-503`、`dual_stack_scheduler.go:639-672` |

---

## 附录 C：本轮"已做的正确工作"清单（不埋没成果）

1. **构造期能力面**（`LocalDialSurface`）：把"类型降维导致能力丢失"从"审查问题"变成"构造错误"。
2. **准入策略值化**（`admissionPolicy`）：四项判定的唯一组合点，并有"零值=全放行"的降级契约。
3. **能力模型只允许否定**（`netcap`）：`Unknown` 永不拒绝；`Validated/Degraded` 预留而不产出。
4. **三态 ownership**：`owns / startedUnstable / epochMoved`，并把"fail-open"写进 accessor，使调用点无法绕过。
5. **一次观测三元组**（epoch/settled/capability）：管理器的 publish 与 transition claim 在同一临界区。
6. **候选调度器**：交错、节流、快速失败推进、late 候选、winner 唯一、loser 同步关闭、错误聚合、软健康。
7. **literal 恢复**：原地址永不被 DNS 阻塞；recovery 只作 fallback；`promoteOriginal` 保持应用选择优先。
8. **四个 packet 臂的策略统一**：从"三臂手写、一处漏"到"一个 `admitCandidates`"。
9. **Fake-IP 四态 + ledger + 持久化 retired**：既有"不误伤真实路由空间"的正控，也有"证明发放过才拒绝"的负控。
10. **setup 所有权与代际原子性**：`advanceAndBump` 消除"被杀成旧、记账成新"的窗口（实测 200/200 → 0）。
11. **错误消息二分**：`no usable path`（族缺失）vs `no public ipv6 path`（egress 缺失），并有 `ErrorIs` 身份。
12. **native bypass 与 userspace 同判据**：策略、能力、自地址、ambient 政策四项一致。
13. **诚实注释文化**：多处注释记录"上一版错在哪、如何被测出、变异测试结果"，包括承认"这个 stop 并不阻止取消"。
14. **`-race` 可用性**：本轮首次在本环境跑通并全绿（上一轮记为 BLOCKED，真实原因只是 `CC` 未指向已安装的 mingw gcc）。

---

*报告结束。本文件为审计档案，不移动任何基线；`KNOWLEDGE/BASELINE.md` 仍描述 `5fc4f9366`（v0.1.9），本报告描述 `b721b988f`（v0.1.21）及其远端 tip 的增量。*
