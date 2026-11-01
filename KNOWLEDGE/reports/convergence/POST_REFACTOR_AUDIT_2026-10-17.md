# 架构收敛后的深度排查与审计（第二阶段）

- **仓库**：`Piggy-Cat-bit-shadow/sing-box`，工作树 `<WORKSPACE_ROOT>\integration-release-next`
- **分支**：`testing`（主分支）
- **起点**：`a61eb701c89`（第一阶段 11 个提交的终点，工作区干净）
- **终点 HEAD**：`06fb3ebc46a`
- **本轮新增**：6 个提交，18 个测试函数；累计（相对 `396f2ae9826`）17 个提交、53 个文件、+3222/−559、17 个生产文件
- **工作区**：干净

---

## A. 结论

**审计认可第一阶段的架构收敛方向，同时发现一处生产回归和一处未覆盖实际调用路径的修复。** 两项问题均已修复，并分别增加了能够检出回归的测试。

第一阶段统一了观测与能力事实的来源，但使用仓库级协议 `Upstream()` 表达包内需求，使一个 outbound 新增方法影响了其他包中的 WireGuard 端点 bind 实现。此外，family health owner 的查找虽然修正了两处，却遗漏了生产探针实际使用的第三处。

---

## B. 本轮发现

### B1. P0：仓库级能力协议导致生产回归

**症状**：`protocol/direct.Outbound` 增加 `Upstream() any` 后，`common.Cast`（仓库内通用的解包协议）会穿过 outbound 继续向下走。

**真实消费点**：`transport/wireguard/endpoint.go:202`

```go
udpListener, isUDPListener := common.Cast[dialer.UDPListener](e.options.Dialer)
```

- 改前：`Cast` 走到 `*DetourDialer` 后停在被 detour 的 outbound —— `*direct.Outbound` 没有 `Upstream()` —— `isUDPListener = false` → `NewClientBind`，包通过 outbound 的 dialer 发出。
- 改后：`Cast` 一路走到 `*DefaultDialer`（它实现 `UDPListener`）→ `isUDPListener = true` → `NewStdNetBind` + egress pool，**`netns` 丢失**（`netns` 在 `DefaultDialer.ListenPacket` 里应用，不在 listener control 里）。

**触发条件**：WireGuard endpoint 的 `detour:` 指向一个**非空** direct outbound（`DetourDialer.init` 只拒绝 `IsEmpty()`）。

**为什么原有测试没有发现**：改动只是为 outbound 增加方法，行为变化却通过泛型协议影响了其他包中的调用方；仅测试 `protocol/direct` 无法观察到这一变化。

**修复**：新增包内协议 `dialChainReporter`（`DialChain() N.Dialer`），名字只有 `common/dialer` 知道。

- `protocol/direct.Outbound` 用 `DialChain()` 报告 surface；
- `route.failoverDialer` **不再报告任何链**（它还多一条独立理由：它描述的是"首次尝试的成员"，而 group 每次尝试重新解析成员，穿过去会读到可能根本不拨号的成员的能力）；
- `common.Cast` 因此重新在每个 outbound 处停下，与第一阶段之前完全一致。

**回归测试**：`TestUDPListenerDiscoveryDoesNotCrossAHop`（在 `common/dialer` 里，因为协议住在那里），加正控制 `TestUDPListenerDiscoveryStillWorksWithoutAHop`。**mutation 已验证**：把 `Upstream()` 加回 hop，测试立刻失败。

### B2. P1：D10 修复未覆盖两条预期调用链

`newScheduler`（`resolve.go:801`）是第三个问"family health owner 在哪"的地方，也是**唯一**仍然直接断言 `d.dialer` 的地方。而它正是 daemon 探针链（`NewResolveDialer(outbound, parallel=true)`）唯一会走到的构造点：那条链拿到的是 `*resolveDialer`，`newScheduler` 返回一个没有 health、并且通过 `newDualStackScheduler` 也**没有 `localFamily`** 的裸 scheduler —— 于是同一个对象上，"gate 知道这台机器不能拨 IPv6"而"计划层完全不知道"，候选照旧被 race、然后在 socket 处被拒。

**修复**：三处统一走 `findFamilyHealthOwner`（唯一实现）。**回归测试**：`TestAChainOverAHopGetsASchedulerBoundToTheOwner`，mutation 已验证。

顺带确认了 review 的 R1b：探针走的是 **domain** 路径，而 `dialerLocalFamilyPolicy` 只服务 literal；`newScheduler` 修好后，domain 路径的候选过滤也拿到了同一个 capture——这条链现在真的生效了。

### B3. P1 —— 上一轮的一个守卫只有"直接设字段"的测试

`route/bypass_power_report_test.go` 直接给 `router.powerReport` 赋值，所以**删掉 `NewRouter` 里那行 service 查找，所有测试仍然全绿**，而生产上守卫什么也不做。这正是本轮要猎的"修复正确但不可达"。

**修复**：`TestTheRouterTakesThePowerReportFromTheServiceContext` —— 走真实 `NewRouter`，断言字段来自 service registry，并在**真实接线**上跑一次 recorder 开启后的拒绝。mutation 已验证（删除那行 → 测试失败）。

### B4. P2 —— 候选过滤在同一份计划里读了两次网络所有权

`filterLocalFamily` 每个候选调用一次 `hardUndialable`，每次都重读 ownership。一份 4 候选的计划会读 4 次同一瞬间；中间落进一次 transition，就会按两代网络过滤同一份计划（头部按过期判决丢弃、尾部按"无意见"保留）。

**修复**：`localFamilyPolicy.gate()` 一次求值，`filterLocalFamily` 用同一 gate 判所有候选。**回归测试**：`TestOnePlanCostsOneOwnershipRead`（计数），mutation 已验证。

**刻意保留**：late candidate 仍逐次重读——那是一次相隔数秒的新决定，用 race 开始时的 gate 会把过期判决套在刚发现的地址上。

### B5. P1 —— `unwrapDialer` 会穿过 detour（本轮早段自捕并修复）

`DetourDialer.Upstream()` 会触发惰性 outbound 解析，且 detour 之下是**对端**的拨号路径：地址族判定套到目的地地址上会拒绝对端本可服务的连接。现在显式拒绝。回归测试 `TestTheCapabilityWalkDoesNotCrossADetour`（同时断言"提问没有触发解析"），mutation 已验证。

---

## C. 针对覆盖缺口补充的测试

| 缺口 | 补的测试 | 说明了什么 |
|---|---|---|
| 三个 packet/stream 入口从未并排驱动 | `TestAStrictFamilyPolicyIsAppliedOnEveryPath` | 严格策略在 **stream TCP / connected UDP / 带候选 / 不带候选** 四条路径上给同一个答案，且每条都带"允许族仍可通过"的控制腿 |
| 同上，地址族能力行 | `TestALocalFamilyFastFailIsAppliedOnEveryPath` | 报告过的 Windows 停机矩阵化：全局 IPv6 拒绝 + LAN/链路本地/公网 IPv4 豁免 |
| **connected UDP 从未被 matrix 驱动** | 上面两个测试的 `stream connected UDP` 列 | 发现并写明了地址族判定在三条路径上由**三个不同层**执行（scheduler / leaf packet gate / leaf literal gate） |
| **无候选列表 + domain** 这条臂无人驱动 | `TestADomainDestinationOnTheLiteralArmResolvesAndBinds` + 字面量不查 DNS 的控制 | `route/conn.go:601` 用 `len(DestinationAddresses) > 0` 选臂，domain 恰好属于候选列表为空的情况 |
| DURING → commit 只有断言层测试 | `TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits` | 用真实 Control hook 卡住 socket，transition 在窗口内提交且 **epoch 不动**；变异测试验证它能检出忽略开始状态的问题 |
| 三个生产对象没有编译期 pin | `DefaultDialer` / `direct.Outbound` / `failoverDialer` 的 `var _` | 之前只有 `resolveParallelNetworkDialer` 有 pin，double 比生产对象管得更严 |
| ambient 策略只在 dialer 单测里验证 | `production_services_test.go`（真实 constructor + 注册 NetworkManager） | 测试 context 与生产 context 注入服务不同，是"生产专用缺陷"的经典盲区 |

---

## D. 证据等级

| 项目 | 状态 |
|---|---|
| `go build ./...`（`DEFAULT_BUILD_TAGS_OTHERS`） | **PASS** |
| `go vet`（`./...`） | 仅 `daemon`/`libbox`/`boxdd` 三处既存 `unsafe.Pointer` 提示，与本轮无关 |
| `go test`：`common/dialer`、`protocol/direct`、`route`（423s）、`dns`、`transport/wireguard`、`adapter`、`common/httpclient`、root、`protocol/group`、`experimental/libbox` | **PASS** |
| mutation 验证（本轮新增 9 条，累计 16 条） | 每条都在还原前确认失败 |
| `-race` | **BLOCKED_BY_ENVIRONMENT**：本机无 C 工具链（`gcc`/`cl` 缺失，`CGO_ENABLED=1` 时 `runtime/cgo` 构建失败） |
| 真机 / 跨平台行为（Windows 多网卡、Linux redirect 路径、Android protect） | **BLOCKED_BY_ENVIRONMENT**：需要带已发布 nftables 规则的 Linux 内核或真机，本环境无法构造 |
| 完整 CI | **BLOCKED_BY_ENVIRONMENT**（树内无 workflow-run 记录） |

本轮所有结论的证据等级是 **`SOURCE + LOCAL TEST`**，没有任何一条达到 `VERIFIED_CI` 或设备级。

---

## E. 性能

测量方法：`-benchmem`，与本轮之前的同机数据（`396f2ae9826` 独立 worktree）对照。

| 路径 | 改前 | 改后 | 判断 |
|---|---|---|---|
| `captureLocalFamilyPolicy`（每次 dial 一次） | 89.7 ns / 32 B / 1 alloc | 101 ns / 64 B / 1 alloc | +11 ns / +32 B，控制面，每次 dial 一次 |
| 同上，无 capability 的 manager | 122 ns / 0 B | 122.7 ns / 0 B | 不变 |
| 候选计划的所有权读取 | **1 + N** 次 manager 读（N=候选数） | **1** 次 | 每次 dial 少 N−1 次短锁；2 候选取样：4 → 2 |
| `LocalFamilyGate`（每候选判定） | — | 12–23 ns / **0 alloc** | 纯能力比较 |
| `LocalFamilyFilter`（单候选，无能力） | — | 12.2 ns / 0 alloc | 快路径未变 |
| `BypassDecision`（每 flow，含新增 power-report 判定） | 32.05 ns / 0 alloc（同机基准见下） | 32.05 ns / 0 alloc | 一次原子指针读，未可测地增加 |
| `CanFastBypassEligible`（每 flow） | — | 16.85 ns / 0 alloc | — |

**结论**：控制面增加了每次 dial 一次 32 字节分配（closure 变大），代价在同一轮里由"每计划一次读取"抵消且净收益为正；数据面（每包）**零变化**，没有任何新增锁或断言进入 per-packet / per-read-write 路径。

**明确记录的一处退化**（review R2，见 F1）：family-health 的命名空间从"网络指纹"改为 generation，代价是**每次 reset（含 `ReleaseMemory`）都会丢弃一次 soft bias**；收益是那一次跨代的第二次读取消失。这是有意的交换，不是遗漏。

---

## F. 剩余风险（分类）

### F1. 已决策接受（不再作为待办）

1. **family-health 命名空间 = generation**（review R2）。每次 reset 开新桶，包括不换网的 reset 与内存释放。代价：一次 fallback delay 的 soft bias 被遗忘；收益：最后一次跨代读取消失。方向安全（只影响快慢，不影响连接与否），桶上限 256 会整体清空。已写在 `familyHealth` 的 Scope 段。
2. **`ListenPacket` 的 literal 策略检查影响面比提交信息宽**（review R4）。它作用于任何 `resolveDialer.ListenPacket` 的 literal 目的地，包括 IPv6 STUN 服务器：`dns.strategy: ipv4_only` 下这类探针会快速失败而不是建 socket。这是与 stream 路径**一致的**拒绝，且比 connect 超时更快给出原因——是想要的行为，不是副作用。
3. **power-report 拒绝的判定顺序**（review R5）。它插在 tracker 拒绝之后、process-metadata 之前，所以 启用 recorder 时，这些场景的 verdict 会报告 `BypassRefusedPowerReport`。只影响归因文字，决定不变。
4. **`route/router.go:103` 现在有接线测试**；但 `dns` 的 snapshot 兜底在当前生产 manager 上仍是死路径（生产 manager 实现了 `NetworkTransitionState`）——它保护的是一种 manager 的接口组合，并非当前生产路径。

### F2. 必须继续修（下一轮）

1. **无**。本轮发现的两个 P0/P1 都已修并验证。

### F3. 明确不修（合理 trade-off）

1. **`DialSerialNetwork` / `ListenSerialNetworkPacket` 对 `ParallelNetworkDialer` 的裸断言 + 串行 fallback**。它的"合法但没有该能力"的调用者只有 detour 链——而 detour 的目的地是对端的业务，逐连接网络选项本来就不该应用在对端地址上；在该场景下串行 fallback 是正确的。真正的保护在 direct 出站（构造期强制 surface）与 failover shim（已转发 + 已 pin）。这是 inventory 里唯一还靠运行期发现的**正确性相关**能力，理由已记录在此。
2. **`failoverDialer` 仍隐藏 8 个能力**。全部当前不可达（路由层对 packet 传 leaf；没有别处从 dialer 参数读它们）。已改成"显式不报告链 + pin 转发集"，并在文件末尾写下给下一位读者的规则：需要新能力时**在这里手写转发并加 pin**，不要重新引入链报告。
3. **`dialerLocalFamilyPolicy` 在一次 dial 内可能被调用两次**（literal recovery 路径）。安全：hand-over 会拒绝跨代连接，重复 capture 只影响"用哪一代判决过滤候选"。穿 6 个签名重构的收益不抵风险。

---

## G. 仓库状态

| 项 | 值 |
|---|---|
| 分支 | `testing`（主分支，未新建分支） |
| HEAD | `06fb3ebc46a` |
| 本轮提交 | `8164c004` 单次所有权读取 · `d40299ca` 拒绝穿过 detour · `e316185` 路径并排覆盖 · `350dba65` 编译期 pin + ambient 生产测试 · `929871f6` 真实 dial 跨 commit · `06fb3ebc` 窄化 hop 协议 + 补第三处查找 |
| 第一阶段提交 | `200f3b21` `f40ed4c6` `b476e2f7` `ecf8025f` `8889cede` `3ff16b80` `5999fe18` `68c7cf92` `c4f363fb` `831749d3` `a61eb701` |
| 工作区 | 干净 |
| 与远端 | 领先 `origin/testing` 17 个提交，**未推送** |
| 兼容层 | 无并行新旧路径。`NetworkTransitionSnapshot` / `NetworkResetCounter` 仍作为**更弱 manager 的降级来源**存在（当前生产环境未使用），`captureEpoch` 是同一 capture 的投影，不是第二套实现 |

**本轮最重要的一条经验**（值得写进 `KNOWLEDGE/`）：一个"任何对象都可以回答"的能力协议，必须窄到"回答它不可能改变实现者从未听说过的代码"。`Upstream()` 属于 `common.Cast` 的仓库级协议，`DialChain()` 属于 `common/dialer` 的包内协议；前者用来表达后者需求，代价是一个 WireGuard bind 静默改变。这与第一阶段那条（"能力不能依赖运行期重新发现"）是同一枚硬币的两面。
