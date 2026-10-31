# 主分支架构收敛实施报告（最终）

> **本报告已根据第二轮审计后的代码更正。** 审计以收敛阶段的 11 个提交为对象，随后又产生 8 个提交。
> 其中两项推翻了报告最初对 D10 协议选择和 D11 shim 报告叶子的决定：前者导致其他包中的生产行为发生回归，
> 且没有明显报错。下文 D10、D11 描述最终状态；原有结论以“审计后更正”标注保留，供追溯。
> 审计细节见 `POST_REFACTOR_AUDIT_2026-10-17.md` 与 `POST_REFACTOR_AUDIT_ADDENDUM_2026-10-17.md`。

- **仓库**：`Piggy-Cat-bit-shadow/sing-box`（工作树 `C:\Deepseek\integration-release-next`）
- **分支**：`testing`（该 fork 的主分支，`origin/HEAD -> origin/testing`）
- **起点**：`396f2ae9826`（与 `origin/testing` 完全一致，工作区干净）
- **收敛轮终点**：`a61eb701c89`（11 个提交）
- **当前分支 HEAD**：`9e5a74a5056`（收敛轮 + 对抗审计 7 + rule-set 审计 1 = 19 个提交，未推送）
- **范围**：收敛轮 47 个文件 / +2159 −553 / 生产文件 17 / 新增测试 29；审计轮只加测试与两处更正
- **工作区**：干净

> 本轮是 **behavior-preserving architecture convergence**。除两处**已被本仓库既有测试判定为违规、
> 且方向为"多拒绝"**的漏执行（D5、D6）外，没有改变任何真实 flow 的结论。

---

## 一、已确认并修复的 11 项缺陷

| # | 缺陷 | 提交 |
|---|---|---|
| D1 | 两个 capture 函数分别推导当前网络状态，各自维护优先级 | `200f3b217d3` |
| D2 | 候选调度器分两次读取同一时点的 environment 和 verdict | `200f3b217d3` |
| D3 | 仅提供 settled 标志的数据源可能错误放行切换期间的拨号 | `200f3b217d3` |
| D4 | DIRECT 的候选规划能力靠运行期断言发现，且带一条不可达的 fallback | `f40ed4c6d92` |
| D5 | family strategy 在第三个 packet 入口点未执行，userspace 与 bypass 分歧 | `ecf8025f3b9` |
| D6 | bypass profile 对"未分类且为零值"的字段静默放过（+ 两条 walk 与测试不一致） | `8889cedee33` |
| D7 | 未将运行中的 power report 计为统计信息使用方，bypass 会丢失统计 | `3ff16b8047f` |
| D8 | `httpclient` 将 dialer 保存到窄类型字段，但从不读取 | `5999fe1804a` |
| D9 | `dns.Client` 只认 lock-free getter；只有 snapshot 的 manager 被当作"永远稳定" | `68c7cf9229a` |
| D10 | 地址族判定无法穿过 outbound hop，probe 链因此静默使用零值策略 | `c4f363fbe56` |
| D11 | failover shim 每漏一个能力就要手写一个转发（已发生两次） | `831749d3ae0` |

### D1/D2/D3 — 一次解析、一次 capture（`200f3b217d3`）

原先 `DefaultDialer` 分别保存四种可选能力，`captureEpoch` 和 `captureLocalFamilyPolicy` 各自维护优先级，同类缺陷因而需要在两处修复。重构后，`networkObserver` 在构造时按 family 观测、transition pair、两个 getter、单个 getter、无接口的顺序选择可用能力；`captureEpoch` 使用同一次 capture 中的所有权信息。

调度器原先通过两次读取拼出健康状态命名空间，其中 environment 仅用作 map key。重构后改用同一命名空间的 generation，并与 capture 一起传递，减少一个重复的事实来源。

### D4 — 构造期 dial surface（`f40ed4c6d92`）

`dialer.LocalDialSurface{Attempt, Network, Resolve, LocalFamily}`：`Attempt`/`Network` 必需，缺则构造
错误并把原因指向 dialer factory。`direct.Outbound` 只持有 surface；`scheduleNetwork` 里再无断言、再无
fallback（那条 fallback 唯一能执行的方式就是工厂坏掉）。

### D5 — 第三个 packet 入口点补上策略（`ecf8025f3b9`）

`resolveDialer.ListenPacket` 的 literal 分支现在用与 stream/候选路径同一个 `effectiveFamilyStrategy()`，
wildcard 豁免。这是 packet 候选路径**已经执行且已经测试**的策略，不是新策略。

### D6 — profile 的 fail-closed 三处漏洞（`8889cedee33`）

- 未分类字段**不再看是否为零值**：`if !field.IsZero()` 让"零值即生效"的字段永久隐形；
- 运行时 walk 不再跳过非导出字段（它只需要名字与零值性，`IsZero` 对非导出字段合法），
  而覆盖测试按类型走、本来就不跳——两者曾可永久不一致；
- 嵌入式**指针** struct 现在会被走（nil 视作其元素类型的零值继续走），运行时与测试都走；
- 覆盖测试不再自带第二套 walk 实现，改为按类型枚举 + 断言"两套 walk 看到完全相同的字段名"。

### D7 — power report 也需要流量统计（`3ff16b8047f`）

`canFastBypass` 早已因 tracker 拒绝（"不能为优化而静默丢失统计信息"），但 power report 是**另一个**
统计信息使用方，并由 build 安装到每个 userspace 连接上。新增 verdict `BypassRefusedPowerReport`，按 **recorder**
（而非 manager）判定。`PowerReportEnabled` 由客户端显式开启，因此只在用户要求统计时让出 fast path——
与 tracker 拒绝同一个取舍。

### D8 — 删除死字段（`5999fe1804a`）

`ManagedTransport.dialer` 存了整条链却无人读取。preflight 不需要它：它问的是 outbound manager 的
tag，正是 `DetourDialer.init` 在首次拨号时问的同一个 manager，两次查找不可能分歧。

### D9 — DNS 也读 snapshot（`68c7cf9229a`）

`NetworkTransitionState` 与 family snapshotter **互相独立**，所以只报告 snapshot 的 manager 会被 DNS
当作"永远稳定"，而在 dialer 眼里不是。getter 仍**优先**（那是 hot path 的无锁读），snapshot 只在
getter 缺失时作为兜底。

### D10 — 穿过 outbound hop（`c4f363fbe56`，协议选择经 `06fb3ebc46a` 更正）

`localFamilyOwner`/`familyHealthOwner` 是非导出接口，跨包类型无法实现——上一轮把它记为"没有局部修法"
的边界。其实能力从未缺失，只是链没有说清它在哪：`direct.Outbound` 现在报告它所拨的那条链，三处查找
（`dialerLocalFamilyPolicy`、`findFamilyHealthOwner`、`resolveDialer.newScheduler`）统一**走链**，带
深度上界，自引用链返回"无 owner"。

**审计后更正（协议本身）**：最初用 `Upstream() any` 报告，那是 `common.Cast` 的**仓库级**协议，代价是
WireGuard endpoint 在"detour 指向非空 direct outbound"这条链上突然透过 hop 发现叶子的 `UDPListener`，
从 `ClientBind` 切到 `StdNetBind` 并丢失 `netns`——改动在 `protocol/direct`，行为变化在三个包之外。
现在改用只有 `common/dialer` 知道的 `dialChainReporter`（`DialChain() N.Dialer`），`common.Cast` 重新在
每个 outbound 处停下；`unwrapDialer` 还显式拒绝穿过 `*DetourDialer`（它的 `Upstream` 会触发惰性解析，
且 detour 之下是**对端**的拨号路径）。教训写在接口文档里：一个任何对象都能回答的能力协议，必须窄到
"回答它不可能改变实现者从未听说过的代码"。

### D11 — failover shim 的能力可见性（`831749d3ae0`，结论经 `06fb3ebc46a` 更正为"不报告链"）

shim 内嵌 `adapter.Outbound`，对嵌入**接口**做断言只提升该接口声明的方法——`EarlyConnectionBufferGrowth`
与 `ParallelNetworkDialer` 已经各被手写转发一次，审计又找出 8 个被隐藏的能力（当前都不可达）。

**审计后更正**：原方案让 shim 用 `Upstream` 报告叶子，**错了两处**。其一是语义：内嵌的成员是**首次尝试**
的叶子，而 group 每次尝试重新解析成员，穿过去会读到"可能根本不拨号的那个成员"的能力与健康历史。其二
是协议形状：`Upstream` 是仓库级协议（见 D10 的回归）。现在 shim **不报告任何链**，改为：把 `adapter.Outbound`
之外真正被消费的能力手写转发并以**编译期断言验证**（`N.Dialer`、`ParallelNetworkDialer`、
`ConnectionCopyTuner`），文件末尾写下给下一位读者的规则——需要新能力时在这里转发并加 pin，不要重新引入
链报告。

---

## 二、决策记录（不是待办，是我做的判断）

### 1. `auto_detect_interface` / auto-redirect mark 不作为 ambient blocker

`NetworkPolicyBlockers` 只判 **已配置**的 ambient 策略。自动绑定走的是**默认接口**，在普通单网卡主机上
与平台自行连接的选路一致；一见到就拒绝会让 TUN 的**默认配置**失去 fast path，而这正是它的全部用户。
两者只在"路由表为目的地选择非默认接口"（多网卡/源路由宿主）时分歧，而要证明 redirect 路径在这种拓扑下
的行为，需要带已发布 nftables 规则的 Linux 内核——单元测试无法提供。

结论已写进函数文档（`common/dialer/profile.go`），并由
`TestAutomaticBindingIsDeliberatelyNotABlocker` **钉住**：将来若有人测量后决定反方向，测试会失败，
注释会说明要重新论证什么。这是"决策 + 证据 + 可失败测试"，不是留给审阅的空白。

### 2. 同一次 dial 内的重复 capture 保持现状

literal recovery 路径中 `dialerLocalFamilyPolicy()` 会被调用 2 次。**安全**：hand-over 的所有权检查会
拒绝任何跨 transition 的连接，重复 capture 只影响"用哪一代判决过滤候选"，不可能产出被放行的错误连接。
把一份 policy 穿过 6 个签名是一次高风险的恢复路径重构，收益仅是"少一次短锁"，本轮不做——理由记录在此，
而不是留作"待定"。

### 3. `NetworkManager` 不再拆分

判据：拆出后是否减少"一个对象同时理解多个无关状态机"。观测表示已在消费侧收敛；transition ownership 与
capability 派生必须与 claim 共用同一把锁，拆开只会把同一临界区搬到两个 package。

### 4. `protocol/tun` 的 L0 route-set bypass 保持独立

它是**产品决策**（`inbound.go:976-1001` 明确写了"L0 是权威的 IP-only pre-router 策略"），不是遗漏。
本轮没有证据推翻它。

---

## 三、行为保持证明

### 既有测试

| 包 | 结果 |
|---|---|
| `common/dialer` | PASS（含 `-count=2`） |
| `protocol/direct` | PASS |
| `route` | PASS（423s，含全部 transition 时序/死锁/generation matrix） |
| `dns` | PASS |
| `adapter` / `common/httpclient` / `common/netcap` | PASS |
| `.`（root Box）/ `protocol/group` / `experimental/libbox` | PASS |

### 新增测试（29 个），关键几类

```
解析与观测    TestTheRichestInterfaceIsAlwaysTheSource / TestTheHealthNamespaceComesFromTheCapture
              TestTheHandOverCheckIsTheGatesOwnCapture / TestASettledFlagOnlyManagerCannotLaunderADuringCapture
能力表面      TestTheProductionChainYieldsACompleteSurface（L3）/ TestASurfaceWithoutThePlanningCapabilityIsAnError
              TestAChainWithoutThePlanningSurfaceIsRefusedAtConstruction
策略一致性    TestListenPacketRefusesALiteralTheStrategyExcludes / TestTheUDPArmAppliesTheSamePolicy
profile       TestAnUnclassifiedFieldRefusesEvenWhenItIsZero / TestTheRuntimeWalkSeesEveryFieldTheCoverageWalkDoes
              TestTheOptionWalkerFollowsEveryKindOfEmbed
会计          TestARunningPowerReportRefusesTheFastPath / TestThePowerReportRefusalKeepsTheOrderOfReasons
DNS           TestTheSettledStateIsReadFromTheSnapshotWhenTheGetterIsAbsent
              TestTheLockFreeGetterIsPreferredWhenBothArePresent
穿链          TestAFamilyVerdictIsReachedThroughAnOutboundHop / TestTheWalkIsBoundedAndSurvivesACycle
              TestAChainOverAHopGetsASchedulerBoundToTheOwner / TestTheCapabilityWalkDoesNotCrossADetour
              TestProductionOutboundReportsTheChainBelowIt（L3，断言两半：报告链，且 common.Cast 不得穿过）
              TestTheShimForwardsItsCapabilitiesAndReportsNoChain
ambient       TestEveryAmbientPolicyConditionRefuses / TestAutomaticBindingIsDeliberatelyNotABlocker
审计补充      TestUDPListenerDiscoveryDoesNotCrossAHop（WireGuard 消费方的回归）/
              TestOnePlanCostsOneOwnershipRead（一份计划一次所有权读取）/
              TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits（真实 socket 跨 commit）/
              TestTheRouterTakesThePowerReportFromTheServiceContext（真实接线）/
              TestAStrictFamilyPolicyIsAppliedOnEveryPath（四条字面量路径并排）/ 各生产对象编译期 pin
```

### L3 覆盖（真实 constructor）

`NewDefault` + `NewResolveDialer` + `NewLocalDialSurface`（与 `NewWithOptions` 对 direct 的分支一致）；
`direct.NewOutbound` 的 surface、`DialChain` 报告、以及 `common.Cast` **不得**穿过 outbound 的反向断言；
选项矩阵逐一保持表面完整。审计后又补了两类真实对象测试：`protocol/direct/production_services_test.go`
（注册真实 `NetworkManager` 后的 ambient 半区）与 `route/bypass_power_report_test.go` 里走真实 `NewRouter`
的接线段。

### mutation-style 验证（收敛轮 7 条 + 审计轮 5 条，每条都在还原后确认测试转绿）

| 变异 | 实测 |
|---|---|
| 短路 family 观测的解析优先级 | 3 个测试失败 ✓ |
| 删掉 "began settled" 项 | 3 个测试失败（含既有测试）✓ |
| `scheduleNetwork` 还原成历史写法 | 2 个测试失败（且不先收窄类型无法编译）✓ |
| 健康命名空间不取自 capture | 命名空间测试失败 ✓ |
| 让 `ListenPacket` 的 strategy 检查失效 | 新回归失败（literal 到达 leaf）✓ |
| 恢复 `if !field.IsZero()` 的未分类规则 | 新回归失败 ✓ |
| 删掉 walk 的穿链步骤 | 穿链回归失败 ✓ |
| 删掉 detour 拒绝 | 穿链边界测试失败（且"提问触发了惰性解析"也被断言）✓ |
| 把 hop 协议换回 `Upstream()` | `TestUDPListenerDiscoveryDoesNotCrossAHop` 失败（WireGuard bind 回归）✓ |
| 让候选过滤逐候选重读所有权 | `TestOnePlanCostsOneOwnershipRead` 失败（计数 2≠1）✓ |
| 删掉 `NewRouter` 里的 power-report 查找 | 接线测试失败 ✓ |

---

## 四、性能影响

| 位置 | 变化 |
|---|---|
| 每次 dial 的网络观测 | `transitionAccess.Lock` + `stateAccess.RLock` → **仅** `transitionAccess.Lock` |
| network dial 的类型断言 | −2（dispatch 改为字段直读） |
| packet literal dial | +1 次 `AddressAllowedByStrategy`（两次比较，无分配），与 stream 同价 |
| DNS 查询 | 不变：lock-free getter 仍优先，snapshot 只在 getter 缺失时取锁 |
| 构造期 | profile walk 增加一次 nil 指针的零值走查（仅当选项结构出现指针嵌入时）；`NewLocalDialSurface` 一次 |
| 每包开销 | 无。`networkObservation` 按值返回，无堆分配；packet hot path 未改动 |

被**否决**的更慢方案：让 snapshot 读同时取 `stateAccess.RLock`（该锁在 `recomputeNetworkEnvironment`
中跨越接口枚举被持有，Windows 上是若干系统调用）。那会让 dial 阻塞在枚举后面，因此改为删除该事实。

---

## 五、剩余架构债（全部已完成决策，无待审项）

| 项 | 状态 |
|---|---|
| `auto_detect_interface` / auto-redirect mark 与 bypass 的等价性 | **已决策并由测试验证**；反方向需要带 nftables 的 Linux 主机测量 |
| 同一次 dial 内的重复 capture | **已论证安全**（hand-over 拒绝跨代连接），重构收益不抵风险 |
| `NetworkManager` 是否拆分 | **已决策不拆**，理由见上 |
| `protocol/tun` L0 bypass 独立生产者 | **产品决策**，本轮无证据推翻 |
| `common/dialer` 与 `protocol/direct` 的 `module` 边界 | 结构性存在（`common/dialer` 不能 import 上层 outbound），用**包内** `dialChainReporter` 协议（`DialChain()`）而非导出 hook 解决；`common.Cast` 的仓库级 `Upstream` 明确不参与 |

**明确不应继续扩张的区域**：不要再给 `NetworkManager` 加协议恢复/重试语义；不要把 DNS 的 policy /
environment generation 并入网络 epoch；不要为了"统一"在 `commitTransition` 侧发布完整四元组
（会暴露"新 environment + settled"这一唯一危险组合）；不要用新的 judge 模型替代真实路径。

---

## 六、提交信息与验证状态

| 提交 | 内容 |
|---|---|
| `200f3b217d3` | 一次解析 + 一次 capture；健康命名空间改取 generation |
| `f40ed4c6d92` | 构造期 dial surface 取代运行期断言与 fallback |
| `b476e2f74ee` | 注释与实测口径随 key 语义修正 |
| `ecf8025f3b9` | 第三个 packet 入口点补上 family strategy |
| `8889cedee33` | 未分类选项无条件拒绝；两套 walk 合一 |
| `3ff16b8047f` | 运行中的 power report 拒绝 fast path |
| `5999fe1804a` | 删除 httpclient 的只写 dialer 字段 |
| `68c7cf9229a` | DNS 也会读 snapshot；删除死接口 |
| `c4f363fbe56` | 地址族判定穿过 outbound hop（协议选择后被更正） |
| `831749d3ae0` | failover shim 的能力可见性（结论后被更正为"不报告链"） |
| `a61eb701c89` | ambient 策略覆盖 + 两处排除的决策记录 |

### 收敛轮之后落地的 8 个提交（对抗审计，同一分支同一分支线）

| 提交 | 内容 |
|---|---|
| `8164c004bed` | 一份候选计划只读一次网络所有权（否则计划可跨两代） |
| `d40299ca4c7` | 能力走链拒绝穿过 detour（不纯 + 语义错误） |
| `e316185da4b` | 四条字面量路径并排覆盖（parity matrix）+ 无候选 domain 臂 |
| `350dba659bc` | 生产对象编译期 pin + ambient 半区走真实 outbound |
| `929871f67a0` | 真实 socket 跨 transition commit（DURING 开始、提交后返回） |
| `06fb3ebc46a` | **更正 D10/D11**：`DialChain` 取代仓库级 `Upstream`；补上第三处 owner 查找 |
| `dbfcca7deb1` | 竞速 reset 的**结果**断言 + 命中率测试改用真实 outbound（去影子模型） |
| `9e5a74a5056` | rule-set 生命周期接线测试（窄范围审计轮的产出，纯测试） |

- 主分支 HEAD：`9e5a74a5056`；工作区干净；`testing` ahead of `origin/testing` by 19，**未推送**
- 验证：

| 项目 | 状态 |
|---|---|
| `go build ./...`（`DEFAULT_BUILD_TAGS_OTHERS`） | **PASS** |
| `go vet`（本轮触及的 6 个包） | **PASS**（`./...` 仅剩 `daemon`/`libbox`/`boxdd` 三处既存 `unsafe.Pointer` 提示） |
| `go test`（dialer / direct / route / dns / adapter / httpclient / netcap / root / group / libbox / wireguard / tun / rule / daemon） | **PASS** |
| `-count=2`（dialer / direct / dns / httpclient / rule） | **PASS** |
| 时序类新测试重复运行（route 20× / rule 10× / daemon 5×） | **PASS**，无 flake |
| mutation 验证 ×12（收敛轮 7 + 审计轮 5） | **PASS**，每条都在还原前确认失败 |
| `-race` | **BLOCKED_BY_ENVIRONMENT**：本机无 C 工具链（`gcc`/`cl` 均不可用，`CGO_ENABLED=1` 时 `runtime/cgo` 构建失败）。替代证据：单次 capture + 一次短锁的结构性收敛，以及 route 包既有的时序/死锁测试。 |
| 完整 CI | **BLOCKED_BY_ENVIRONMENT**（树内无 workflow-run 记录） |
