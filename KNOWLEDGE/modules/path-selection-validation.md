# 路径选择与启动期校验（Path selection and start-time validation）

## v0.1.9 之后的 AnyTLS 服务器解析与失败归因（AnyTLS server resolution and failure attribution after v0.1.9）

在 `011f3fdcd99` 上，`protocol/anytls/outbound.go` 让拨号器在解析器策略为 `AsIS` 时优先对代理
服务器名使用 IPv4。显式解析器或旧式策略仍然优先；该偏好仅在 `NewWithOptions` 构造本地解析
拨号器的地方生效，并不会普遍应用于经 detour 的查询或目标目的地。这是一个偏好，而不是“IPv4 是
唯一可行路径”或“在每个网络上吞吐都会提升”的证明。

对于 TCP 与 UoT 的 `DialContext`，AnyTLS 客户端错误会被包装上 outbound 标签与服务器名，同时
保留其成因；这可以识别出某个代理报告了远端解析错误，但并不会修复那个解析器。`ListenPacket`
直接返回其 UoT 客户端的结果，不经过这个命名包装。参见
`common/dialer/server_preference_test.go` 与 `protocol/anytls/proxy_failure_naming_test.go`。
在本次刷新中，两个包都通过了未加过滤的本地测试；这不是实时代理互通或性能证据。

## v0.1.9 之后的 Mieru 传输失败边界（Mieru transport failure boundaries after v0.1.9）

在 `011f3fdcd99` 上为 **VERIFIED_SOURCE**：`protocol/mieru/fallback.go` 把调用方取消、本地
生命周期、endpoint、传输、目标与歧义失败分别归类。它在 SDK 扁平化错误链之前保留带类型的
socket 观测；一次 SOCKS 拒绝是目标侧证据，绝不能据此把底层传输标记为不健康。在 SOCKS 响应
读取的分类中，裸 TCP EOF 属于歧义情形，而 UDP 情形则被归类为传输失败；请检查确切的分支，而
不是把所有 EOF 一视同仁。Mieru 的 inbound 与 outbound 把 close 注册到各自的 scope
（`protocol/mieru/inbound.go`、`outbound.go`）；一次关闭或代次替换不得被解释为传输失败。这
不是对端互通性或每一种失败模式的证明；请查阅 fallback 与早期失败测试以了解其覆盖范围。

### 并行候选的组转发（Group forwarding of parallel candidates）

`Selector`、`URLTest` 与 `LoadBalance` 先选定一个成员，再把目的地候选列表、网络策略、接口类型
与 fallback 延迟通过 `DialParallelNetwork` 与 `ListenSerialNetworkPacket` 转发下去；嵌套的、
具备该能力的组会转发这些参数，而不是把计划压缩成一个地址。参见
`protocol/group/parallel_passthrough_test.go`。未加过滤的 group 包测试在 `cb71cf80d72` 上于
本地通过；这并不确立真实网络切换或全部对端行为。

### 远程二进制规则集发布（Remote binary rule-set publication）

`common/srs/binary.go` 把 zlib 流排空到其自然结束，以在返回解析后的规则集之前校验截断与
校验和；解压工作单独设界，并使用一个独立的 `ErrDecompressedTooLarge`。
`route/rule/rule_set_remote.go` 在没有缓存文件时走流式路径，在发布之前排空 HTTP body 以捕获
分帧错误，然后构建并校验规则，最后才发布。`Close` 把该集合标记为已关闭并取消其上下文；发布
在锁下检查关闭状态。不要把“完成规则解析”等同于“校验了外层流”，也不要在关闭之后发布一次迟到
的抓取。这些在 `011f3fdcd99` 上是 `VERIFIED_SOURCE`；本地 `./route/rule/...` 测试在本次刷新
中通过，这不是对实时远程服务器或设备的保证。

远程规则集的 HTTP 客户端现在有五分钟超时（`rule_set_remote.go`），因此旧审计中“没有逐请求
超时”的说法已经过时。更新器仍然串行处理各个集合，并且 `Close` 只取消其上下文而不等待循环结束
（join）；一个慢请求可以把后续集合延迟到有效超时为止。发布时会检查关闭状态，但一个已经被复制
且已经越过检查的回调并不会被普遍撤回。TUN 的 `updateRouteAddressSet` 与自动重定向关闭通过
`autoRedirectAccess` 串行化，并在调用重定向之前重新检查关闭状态。聚焦的
`TestInFlightCallbackUnderCloseIsOrdered` 在本次刷新的 Windows 上通过；该测试不是 Linux
自动重定向集成测试，也不是对每个观测者的证明。

**范围（Scope）：** `common/physicalpath`、`common/routeflow`、`adapter/outbound/manager.go`
中的启动期校验、`detour` 解析、outbound 组、环、failover。

**来源（Provenance）：** `common/physicalpath` 与 `common/routeflow` 是 **fork 创建**的（在
`e4b7e692` 处不存在）；`protocol/group/loadbalance*.go` 与 `route/failover.go` 是 fork 新增。
`common/physicalpath` 是最大的 fork 原创子系统：在 v0.1.9 的统计中为 **35 个提交、8,543 行
Go 代码（含 16 个测试文件）**；在 `0067b03` 处为 37 个提交、跨 21 个 `.go` 文件的 9,407 行 Go
代码（17 个测试文件）。

**在改动以下内容时加载本模块：** 启动期校验、`detour`、selectors/urltest/loadbalance、
`destination_dns_ownership`、`PathNode.RequiredNetworks`，或 failover。

---

## 1. 三件并不相同的事（The three things that are not the same）

```text
CONTROL PATH   who SELECTS what — the routing rule's outbound root, then every group entered,
               in descent order. A group is NOT a physical hop. Selection groups can still wrap
               the returned connection for interruption/lifecycle management; see selector.go and
               urltest.go. Physical-path representation must not erase that separate contract.

PHYSICAL PATH  the hops the bytes actually traverse, in PACKET order:
               Hops[0] = nearest this device, Hops[len-1] = the exit.
```

一个把组报告为一跳、或把跳列表反向报告的诊断，**描述的是流量并未走的路径**——这比没有诊断
更糟。

### 命名——务必搞对，这是一个已被记录的陷阱（Naming — get this right, it is a documented trap）

| 类型 | 字段 | 顺序 | 锚点 |
|---|---|---|---|
| `Path`（一次 `Build` 的结果） | `ControlPath []string` | **下降序**（root → 最内层组） | `physicalpath.go:316` |
| `Path`（一次 `Build` 的结果） | `Hops []Hop` | **数据包序**（最靠近本设备者在前） | `physicalpath.go:320` |
| `PathNode`（遍历的节点） | `PhysicalPath []string` | 数据包序 | `leaves.go:56` |

**不存在 `PhysicalPath` 类型，也不存在 `Path.PhysicalPath` 字段。** `PhysicalPath` 是
`PathNode` 的一个字段，由 `Resolver.Hops()`/`Leaves()` 产生（`leaves.go:215`、`:257`）。

### 为什么 `ControlPath` 不反转——以及那份说法相反的文档（Why ControlPath is NOT reversed — and the document that says otherwise）

`reversePacketOrder`（`physicalpath.go:564-600`）反转 `Hops` 并重编号 `Position`，然后有意
**把 `ControlPath` 留在下降序**（`:571-587`），并通过同一置换重映射 `Unknown.Position`
（`:592-599`）。

* 确立这一点的修复是 `d46717dbd`——“fix(path): ControlPath is the decision order, not the
  packet order”，2026-10-10 12:54:53，它是发布基线的祖先。
* **`docs/fork/physicalpath-controlpath-model.md` 在此处已过时。** 它明确写着该修复“also
  reverses `ControlPath`”，但它最后一次被 `e201b5add`（10:03:43）改动，比 `d46717dbd` **早
  51 分钟**。请把那一段落读作已被取代；代码才是权威。
* 由 `common/physicalpath/physicalpath_test.go:462-482`
  （`TestControlPathIsShorterThanThePhysicalPath`）固定——注意该测试函数自己的文档注释仍带着
  旧名称 `TestControlPathOrderIsNotPacketOrder`；断言是 `ControlPath == ["outer","inner"]`
  （下降序），而 `hopTags(path) == ["exit","member"]`（数据包序），即这两个列表在**成员与
  顺序**上都不同。

### 方向证明，以及它是如何被决定的（The direction proof, and how it was decided）

配置的 `detour` 是一条**依赖**边：`X.detour = Y` 表示 X 消费 Y。代理 outbound 把用户的*目标*
封装在协议内部发送，而只用自己的拨号器到达**它自己的服务器**：

```go
// sing/protocol/socks/client.go
:162  tcpConn, err := c.dialer.DialContext(ctx, N.NetworkTCP, c.serverAddr)   // the SERVER
:198  response, err = ClientHandshake5(tcpConn, command, address, ...)        // the TARGET, in the request
```

所以 `exit.detour = entry` 会让设备**先进入 entry、再进入 exit**。该结论依赖一次线缆（wire）
观测，而不是对上述代码行的推理：`common/dialer/detour_wire_order_test.go` 用真实的
`NewDetour` 构建拨号器，替换一个会记录的对端，并记录进入的是哪一跳以及**它被请求的目标
地址**——仅凭地址无法区分“entry 被要求承载一条通往 exit 服务器的连接”与“设备直接拨号到
exit”。

固定这一点的测试：`TestDetourTwoHopReachesTheDetourFirst`、
`TestDetourThreeHopKeepsTheOrder`、`TestOutboundToEndpointReachesTheEndpointFirst`、
`TestHopZeroIsTheNearestEntryForTheWholeMatrix`。其中两个测试被**改名**，因为它们断言的内容
与名称相反——较早的读法曾经发布，并由 `524ea41e` 修正。

**源码中陈述的诚实限制**（`physicalpath.go:551-554`）：顺序由每一跳所声明的拨号边派生。对于
一条转发代理链（这正是 `detour` 所表达的东西）它是精确的。它不为“为并非其依赖所命名的服务器
承载流量”的跳建模，也无法看到配置未曾声明的路由。

---

## 2. 启动期校验——实际检查了什么，以何种顺序（Start-time validation — what is actually checked, and in what order）

`adapter/outbound/manager.go` 的 `Start`（`:168-263`）按以下顺序运行：

1. `lintOutbounds`（`:210`；定义在 `:417`）——依赖存在性检查与无环排序。
2. `validatePhysicalPaths`（`:231`）——可达叶（reachable-leaf）试运行。
3. `startOutbounds`（`:249`）。

顺序很关键：试运行在排序**之后**运行，因此它无法取代排序更精确的消息；排序计算出的顺序被传给
`startOutbounds`，而不是被重新计算。

### 由代码陈述的只读契约（The read-only contract, stated by the code）

`manager.go:267-268`：*“It is read-only: it dials nothing, resolves nothing, changes no
selection and consumes no rotation.”*（中文：它是只读的：不拨号、不做解析、不改变任何选择、
不消耗任何轮换。）这一点可以从对象图证明：`Resolver`（`physicalpath.go`）持有一个注册表查询、
一个 `Snapshot` 与一个网络名——解析器本身没有任何拨号器或时钟。该结构支持“不拨号/不做选择”
的契约；它并不证明整个校验调用没有日志或其他附带效应，下一节会说明。`Build` 不会克隆所报告
的叶：`TestTheReportedLeafIsTheSelectedLeaf` 对注册表中的对象断言 `require.Same`。

轮换另行固定：`TestTheDryRunConsumesNoRotationOnARealStart`
（`adapter/outbound/manager_physicalpath_test.go:398-414`）断言 committed == 0 **并且**
previewed == 0。连一次预览都不接受的调用方会设置 `Snapshot.Selections`，或传入 `""` 来声明
该成员**不可知（NOT KNOWABLE）**（`TestASnapshotCanSayTheLeafIsNotKnowable`）。

当叶不可知时，遍历会追加 `Unknown{Node, Position, Reason}` 并**停止**；只要存在任何 unknown，
`Path.Exit()` 就报告 `ok=false`，因此部分验证的路径不会被误认为已验证的路径
（`TestAnUnresolvableDependencyIsUnknownAndNotInvented`）。

### 但校验并非无副作用——这次诚实的更正（But validation is not side-effect-free — the honest correction）

校验路径上存在两个真实的副作用。两者都不拨号；之所以都记录下来，是因为“只读”这一笼统说法强于
代码事实：

1. `common/dialer/dialer.go:217` 调用 `deprecated.Report(ctx, OptionMissingDomainResolver)`
   ——这是一条在启动期校验期间发出的**弃用日志**（可达：`box.go:558` 传入
   `newDialer=false`）。
2. `common/dialer/capability.go:100` 调用 `detourDialer.Dialer()`，它把一次**记忆化的
   detour 解析**提交进 `sync.Once`（`common/dialer/detour.go:63`）。它解析一个标签 → 对象；
   并不拨号。

另外，`Box.Status`（`box.go:1396`）**有意**获取一次实时的 `Selected()` 预览。那是一次状态
读取，不是校验。

### 每项检查捕获什么（What each check catches）

| 检查 | 锚点 | 捕获 |
|---|---|---|
| 成员存在 | 既有的排序 | 某个组所声明的成员没有指向任何东西 |
| 成员能承载其根路由给它的每个网络 | 试运行 | 在 TCP 流所走的链中放一个仅 UDP 的成员 |
| endpoint 参与 | 试运行 | 一个标签被两个不同对象声明 |
| 所声明的 `destination_dns_ownership` 已实现 | 试运行 | 关于对端接收到什么的承诺，而该对象无法兑现 |
| 所声明的所有权有解析器 | 试运行 | 那条失败即拒绝的路径，它会破坏每一个具名目的地 |

detour 链上的每个节点都在**其自身的位置**被检查，而不仅是出口：可能存在一个中间跳无法承载该
流、即使出口可以承载。这一能力类发现在启动时只发出警告；受影响的流在运行时仍可能失败。见紧接
下面的严重性表。

试运行新增了一项有意的拒绝：在不实现 `destination_dns_ownership` 的 outbound 类型上声明该
字段。该字段仅存在于 fork，并在同一周期内加入，因此**早于它的任何配置都不可能包含它**。

### 严重性：能力类发现警告，结构类发现拒绝（2026-10-20）（Severity: capability findings warn, structural findings refuse (2026-10-20)）

`Failure` 带有一个 `Kind`，它在发现产生之处就被决定，而不是通过匹配消息来决定：

| 类别 | 示例 | 启动裁决 |
|---|---|---|
| `FailureCapability` | 一个不承载某条被路由网络的出口；一个无法服务该流的未被选中成员；一个其成员无法承载所投递网络的组 | **WARN**，启动继续 |
| `FailureStructure` | 一个并不存在的已声明成员；一个同时被 outbound 与 endpoint 声明的标签；一条永远到不了远端的路由 | **ERROR**，启动被拒绝 |
| `FailurePromise` | 在无法兑现它的类型上声明 `destination_dns_ownership` | **ERROR**，启动被拒绝 |

`Report.Err()` 只返回致命部分，`Report.Warnings()` 返回能力部分，outbound 管理器通过其注入的
日志器把后者记录一次（`SetLogger`，由 `box.go` 接线）。运行时没有改变：一条到达无法承载它的
对象的流，仍然和以前一样失败。这一区分之所以存在，是因为能力类发现是对一条**可能到来**的流的
陈述，而结构类发现是对配置能否被构建的陈述。

同样的划分也适用于这两项消费方检查：一个 detour 存在但不能服务每个已配置传输的 HTTP 客户端，
以及一个 detour 存在但不能承载其查询的 DNS 传输，都属于警告；而一个不指向任何东西的 detour
标签仍然是致命的。

### 它取代的一处潜在 panic（A latent panic it replaced）

启动顺序排序按**标签**标记节点已启动，并在已启动标签数等于**节点**数时停止。同一标签下的两个
对象即可满足该计数，却仍有一个未被访问，随后报告依赖问题的代码解引用了那个 nil 结果——实测为
`invalid memory address or nil pointer dereference`（位于 `lintOutbounds` 内），而不是一个
错误。当前的 `lintOutbounds` 在排序之前检查重复的对象标识与重复的标签。先前被引用的
`TestDuplicateMemberTagIsRefusedRatherThanCrashingTheSort` 在此代码树中不存在；
`adapter/outbound/tag_collision_test.go` 记录了命名空间冲突的设置，但其本身并不证明 lint
路径。除非找到并运行了一个真正匹配的测试，否则请把 lint 行为视为源码已核实
（source-verified）。

### 唯一一处构造侧效应（The one construction side effect）

`adapter/outbound/manager.go:170-188`：如果 `route.final` 为空，则会**创建**一个 `direct`
fallback 对象。这是该路径上唯一的构造。

---

## 3. `common/routeflow` —— 可证明的需求（provable demand）

`common/routeflow/reach.go`（352 行，**没有测试文件**）。

`Reach(rules, finalOutbound) Demand`（`:95`）。`Demand`（`:278-302`）携带 `Proven`、
`Possible`、`Unclaimed`、`Reached`。`Demand.NetworksFor` 只返回 **`Proven`**（`:302-307`）
——从不返回 `Possible`。

**UNKNOWN 策略**在 `:47-70` 处陈述，是承重的规则：

> 未知规则在**两个方向上都是透明的**——它既不向某个标签计费，也不移除其网络。

值得了解的推论：

* **`route.final` 确实以 PROVEN 需求被计入残余**（`:72-89`、`:177-183`）。较早的版本把
  final 当作“什么都不证明”，并固定了一个测试，要求“仅 TCP 的 final 必须能启动”；那是一次
  **实测的误拒绝（false deny）**，由 `133e6ed4f` 反转。它当时固定的测试
  （`TestFinalProvesNothingAboutDelivery`）**已不存在**——如果你在
  `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` 的
  §B/§H 中看到它被引用，该文档在那里的内容已被取代。
* 可证明性意味着 scope 为 `Always` **并且** `terminates()`（`:214-228`），这与 `matchRule`
  一致（`route/route.go:1279`）。
* **ICMP 被有意排除在外**（`:338-347`）。

### 三类按类别的能力检查（The three per-class capability checks）

| 消费方类别 | 检查 | 锚点 |
|---|---|---|
| 拨号器 / detour | `CheckDetourCapability` | `common/dialer/capability.go:40` |
| HTTP 客户端 | `HTTPClientTransportRequirement` | `common/httpclient/transport_capability.go:30`，调用点在 `common/httpclient/manager.go:122` |
| DNS 传输 | 按 DNS 类型的要求 | `dns/transport/capability.go:34` |

**整棵代码树中没有任何 `Consumer`/`ConsumerProvider` 类型**（`grep -i ConsumerProvider`
→ 零命中）。较早把该设计称为“consumer-first”的措辞，其实是在命名**拨号链的构造顺序**
（`route/route.go:248`、`:267`），而不是校验模型。

---

## 4. 组：选择在被提交之前只是一次预览（Groups: selection is a preview until it is committed）

| 组 | `Selected(network)` 行为 | 锚点 |
|---|---|---|
| `Selector` | 返回已载入的原子值；**忽略 network 参数**；未选中时 `Network()` 报告 `{tcp,udp}`（即“blanket”） | `protocol/group/selector.go:232-234`（`Selected`）、`:173-179`（`Network`） |
| `URLTest` | 按网络过滤，读取历史存储，**不写回任何东西** | `protocol/group/urltest.go:222-239`（`Selected`；`Select` 在 `:745+`） |
| `LoadBalance` | 文档描述为“a pure preview”（纯预览）；委托给 `SelectForFlow(…, commit=false)`；轮询（round-robin）分支用 `Load()` 取得游标，只有在 `commit` 为 true 时才用 `Add(1)` 推进 | `protocol/group/loadbalance.go:255-261`（文档）、`:262-264`（`Selected`）、`:339-344`（游标） |

**`commit` 标志是运行时控制面/物理面的分离**（`route/route.go:218-246`）。预览不得消耗一个
槽位：一个已经消耗了槽位的诊断会给下一条流分配与它刚刚报告过的成员不同的成员。

在**真实**组（而非 fixture）之上固定：`route/physicalpath_route_test.go`
（`TestPhysicalPathTakesNoSelectionPreviewThroughRealGroups`、
`TestTwoFlowsAdvanceTheCursorExactlyTwiceWithAPreviewInBetween`），并通过一次反向破坏
（reverse-break）验证：把 `loadbalance.go` 的 `Selected` 改成传入 `commit=true`，观察
`CommittedSelections()` 发生变化。

`TestControlGroupIsNotAPhysicalHopThroughRealGroups` 断言：两个控制节点对应一跳。

### 运行时拨号所有权与 failover（Runtime dial ownership and failover）

`route/failover.go`：

* `routeDialOwner`（`:49-58`）要求 `FailoverEnabled()`。
* `routeDialDecision`（`:78-97`）：**目的地地址多于一个 ⇒ 没有所有者**（`:80-82`）；叶是
  `ConnectionHandler` ⇒ 以 `commit=true` 重新解析（`:87-95`）。
* `failoverDialer`（类型定义 `:121-125`）内嵌该叶，用于标签/网络/拷贝层面的调优，但通过
  `group.DialWithFailover` 拨号（`DialContext`/`DialWithFailover` `:141-143`）。

在测试目标上，该 shim 显式实现 `ParallelNetworkDialer`：它把候选列表、网络策略、接口偏好与
fallback 延迟携带在 failover 上下文中，以便被选中的成员可以使用它们。其 packet-listen 方法
委托给内嵌的叶；生产环境的 packet 路径本身直接传递该叶，不使用该 shim 做组重试。该 shim
有意不暴露通用的拨号链，因为其内嵌的第一个叶可能并不是后续某次尝试所用的成员。参见
`route/failover.go` 与 `failover_shim_surface_test.go`；针对性的测试在本地通过，不是真实
网络切换测试。

failover 预算属于**用户流**，而不是组调用：一条 `outer → inner` 链总共只花费**一次**备选。
按组设限会成倍放大。

---

## 5. `PathNode.RequiredNetworks` —— 一个实现细节，不是契约（an implementation detail, NOT a contract）

在整棵树上执行 `git grep -n RequiredNetworks` 有 15 处命中：

* **写入方（生产代码，2 处）：** `leaves.go:359`、`leaves.go:446`
* **读取方（测试，5 处）：** `diamond_routes_test.go:105,107,134,169`、
  `p1_contract_diagnostic_test.go:54`
* **读取方（生产代码，0 处）**

校验器**不**读它。它读 `HopCheck.Requirement`（`dryrun.go:71`），该值由**函数参数**经
`nodeRequirementFor`/`selfRequirementFor` 计算而来。

fork 自己的提交 `fc4bdd416` 就是这样说的：*“physicalpath.RequiredNetworks is still
write-only. It is populated by leaves.go and read nowhere … should be wired or removed by
the file's owner.”*（中文：physicalpath.RequiredNetworks 仍然只写。它由 leaves.go 填充，
无人读取……应由文件所有者接线或删除。）`docs/fork/v016-correction-round-ledger.md:249-251`
补充说，它的文档注释*“is actively wrong until the owner wires or deletes it.”*（中文：在
所有者接线或删除它之前，这个注释是明显错误的。）

**包内共存着两种需求模型，而且它们互相矛盾。** `RequiredNetworks` 向**依赖**跳计入业务网络
（`leaves.go:480` 原样下传 `required`）；而校验器**豁免**这些跳（除非 `businessEntry`，
`nodeRequirementFor` 返回 nil，`dryrun.go:533-539`）。

请把它当作**留给文件所有者的后续事项（FOLLOW-UP）**，绝不要当作契约。不要把它引用为“依赖跳
会被计入网络”的证据。

### `dryrun.go` 内部第二处带日期的矛盾（A second, dated contradiction inside dryrun.go）

`dryrun.go:543-556` 是一个**孤立注释块**——`:542` 处的一个空行把它与 `nodeRequirementFor` 在
`:541` 处的右花括号分开——其中声称窄读法曾被“tried and REVERSED … The union therefore
STAYS”。而 `:384-387` 处的代码在 `!requirementProven` 时**恰好**实现了那种窄化
（`effective = selfRequirementFor(node)`），并且 `selfRequirementFor` 自己在 `:568-589` 处
的文档与该孤立注释说的正相反。

由提交顺序可证明：`d95e256b7`（2026-10-12 04:49:42）**删除**了 `selfRequirementFor` 并加入
该注释；`31f250ff1`（06:08:04）**重新加入**该辅助函数并保留了这条注释
（`git merge-base --is-ancestor d95e256b7 31f250ff1` → 是）。**该注释已死；代码已定。绝不
要恢复并集读法。**

### 一条永远不会触发的诊断分支（A diagnostic branch that can never fire）

`requirementClause` 所声明的分支（`dryrun.go:1016-1023`）是**不可达的**：`selfRequirementFor`
= `decidedNetworks(networksOf(node.Outbound))`（`:590-592`），而 `decidedNetworks` 返回的是
一个**子集**（`:838-850`），所以当 `!requirementProven` 时，需求是该节点自身 `Network()` 的
子集，于是 `validateNode` 第二步的失败分支（`:862-920`）——它唯一的调用点（`:893`、`:918`）
——无法触发。推论：凡在构建消息之处，`HopCheck.RequirementProven` 恒为 true。

当前的测试文件准确地讲述了这段故事，并且**不再断言那条不可达的字符串**：
`box_physicalpath_route_aware_test.go` 把 `NotContains` 放在 `:738-740`，其在 `:743-753` 的
注释明确说，仅对象措辞“is therefore unreachable through this shape”。`:757-768` 处的
fixture 只断言 Start 成功，且不声称任何证明。不要复活旧的“注释与其自身 fixture/断言矛盾”的
描述——那个矛盾已经不存在了。

---

## 6. 环检测——三处实现，两处只读守卫（Cycle detection — three implementations, two read-only guards）

| 位置 | 锚点 |
|---|---|
| outbound 依赖排序 | `adapter/outbound/manager.go:477-479` |
| 跨类型（outbound ↔ DNS 传输） | `adapter/outbound/cross_kind_cycle.go:98` `ValidateCrossKindCycles`，图 `validate()` `:318-380` |
| 路由遍历 | `route/route.go:264-266` |
| 物理路径遍历 | `physicalpath.go:700-702` |
| 叶枚举 | `leaves.go:385-387`、`:470-476` |

环检测复用**既有的**图（声明的成员关系 ∪ `adapter.Referrer.References`）——即
`cross_kind_cycle.go` 在启动时校验的同一组边。遍历自身的下降过程*就是*环记录，也就是
`route.resolveOutbound` 已经在用的形状。**没有引入第二张图**；请保持如此。

`default_domain_resolver` 被建模为一条图的边（`box.go:616-618`，`SetDefaultDomainResolver`
安装一个**闭包**，因此答案保持实时）。无条件地计入它曾是一次**实测的误拒绝**
（`cross_kind_cycle.go:288-294`）；而省略它则接受了一个会在首次拨号时挂起的环
（`box.go:606-612`）。两个方向都曾各错一次。

---

## 7. 接缝——每处只有一个生产调用点（Seams — one production call site each）

| 接缝 | 消费方 | 锚点 |
|---|---|---|
| `EnablePhysicalPathValidation` | `box.New` | `manager.go:101` ← `box.go:507` |
| `SetResolverAuthority` | `box.New` | `manager.go:127` ← `box.go:544` |
| `EnablePhysicalPathDelivery` | **仅**在 `router.Initialize` 成功之后 | `manager.go:149` ← `box.go:622` |
| `SetDefaultDomainResolver` | `box.New` | `manager.go:54` ← `box.go:637` |
| `ValidateRoots` | `Manager.Start` | `dryrun.go:256` ← `manager.go:365` |

`box.go:616-622` 指出，`EnablePhysicalPathDelivery` 只在 `router.Initialize` 成功之后被调用，
因为那是唯一能从**已编译**规则推导出“路由已证明的投递”的时点。

**这些声明是选择加入的（opt-in）。** 一个从未被赋予它们的 `Manager` 完全像以前一样运行；
`EnablePhysicalPathValidation` 才是打开试运行的开关，且只有 `box.New` 调用它
（`TestNoDryRunWithoutTheDeclarationsKeepsTheManagerUnchanged`）。

---

## 8. 证据、来源与注意事项（Evidence, provenance, cautions）

### 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| 试运行不拨号、不解析、不消耗轮换 | `VERIFIED_UNIT`（+ 变异对照） | `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...` |
| `ControlPath` 是下降序，`Hops` 是数据包序 | `VERIFIED_UNIT` | `physicalpath_test.go:462-482` |
| 控制组永远不会成为一跳 | `VERIFIED_UNIT`，在真实组上 | `route/physicalpath_route_test.go` |
| `route.final` 以已证明的方式被计入残余 | `VERIFIED_UNIT` | `box_physicalpath_unconstrained_test.go:62` |
| 误拒绝矩阵（11 组接受/拒绝孪生） | `VERIFIED_UNIT`（需要 tags） | `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestFalseDenyMatrix .` |
| 跨类型环在启动时被拒绝 | `VERIFIED_UNIT` | `box_cross_kind_cycle_test.go`, `box_static_validation_final_wire_test.go` |
| `RequiredNetworks` 语义 | **仅 `VERIFIED_SOURCE`**——只写、无生产读取方、不构成契约 | `git grep -n RequiredNetworks` |
| 跨类型图**整体**的健全性 | **`UNKNOWN`**——CK1–CK7 从未运行；只有被点名的缺口被闭合 | — |

### 来源（Provenance）

| 提交 | 类型 | 它做了什么 |
|---|---|---|
| `01262510` | FEATURE | 实现 `PhysicalPath`/`ControlPath` 模型与启动期可达叶试运行 |
| `524ea41e` | FIX | 使 `Hops` 为数据包序，由拨号路径而非遍历决定。**九个测试变红**；其中两个被改名，因为它们断言的内容与名称相反。 |
| `d46717dbd` | FIX | `ControlPath` 是决策顺序，而不是数据包顺序 |
| `e3399187` | FIX | 按遍历隔离的解析器状态；菱形按路由分别校验 |
| `fc4bdd416` | FIX | 三项需求事实；记录 `RequiredNetworks` 只写 |
| `8299f30e` | FIX | 导出的试运行拒绝被截断的路由，而不是把它留给 lint 处理 |
| `8ae8f10d` | FIX | 被截断的路由无法躲在兄弟分支之后 |
| `91f595906` | FIX | 对本地拨号器而言 `destination_dns_ownership` 是空洞的 |
| `cf5a25bbe` | FIX | 为每个已证明的本地拨号器补齐所有权一致性；拒绝被拆分 |
| `33eba5bf7` | FIX | 按**可达的流类型**校验 outbound 能力，而不是按入口能力 |
| `0dfedb382` | FIX | 只在运行时路由器实际停止处消费协议 |
| `04591f8` | FIX | 单一解析器事实来源，以及它消除的误拒绝 |
| `4e228f53` | FIX | **默认解析器边是被多计而非少计**——对早先决定的翻转 |
| `31f250ff` | FIX | 敲定三项契约：只取可证明的需求、组并集、图的边 |
| `ff004007` | FIX | 在图实际遍历的适配器上发布解析器事实 |
| `5ad4c032` | FIX | 在每个构造点填充服务器域名事实 |
| `d95e256b7` | REFACTOR | 删除两个死掉的辅助函数——**并加入 §5 所描述的孤立注释** |
| `d2b34f1cf`…`386e95a70` | FEATURE/FIX/DOC | Direct Offload 链——参见 `data-path-optimization.md` |

以上全部为 **OWN**。`d2b34f1cf`… 与 `data-path-optimization.md` 共享。

### 已被取代的文档——不要把它们当作当前内容引用（Superseded documents — do not cite them as current）

| 文档 | 在何处被取代 | 被谁取代 |
|---|---|---|
| `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` §B（`:141-142`）、§H（`:350-367`） | `route.final` 已证明/未证明；其 `TestFinalProvesNothingAboutDelivery` **已不存在** | `133e6ed4f`；`reach.go:177-183`；`box_physicalpath_unconstrained_test.go:62` |
| 同一文档 §C（`:169-170`） | “'does not carry' 恰在一处构造”在全树范围内为假 | 还有 `common/dialer/capability.go:59`、`dns/transport/capability.go:45`、`common/httpclient/transport_capability.go:44` |
| `KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §4、§9 | `CROSS_KIND_GRAPH_SOUND=NO`、`ENDPOINT_RESOLVER_EDGE_SOUND=NO`、`DEFAULT_RESOLVER_EDGE_SOUND=NO`、`GROUP_FALSE_DENY_REMAINS=YES` | `ff0040079`、`4e228f539`、`31f250ff1`；`manager_physicalpath_test.go:249-271` |
| `docs/fork/v016-five-workstream-final-report.md:565-587`、`docs/fork/v016-overnight-implementation-report.md:479` | 声明 J-1/J-2/J-3 未实现（NOT IMPLEMENTED） | 三者均已存在（`01262510`、`common/physicalpath/status.go`、`box.go:1318`） |
| `docs/fork/v016-correction-round-ledger.md:252-254` | “`Selector.References()` lies before Start” | 由 `96e6dc419` 修复（`selector.go:269-274`） |

`KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §1 的组表**仍然正确**。

### 代码自身内部的过时引用（Stale citations inside the code itself）

`common/physicalpath/physicalpath.go` 的包文档在 `:57/:61/:77/:80/:85` 处引用
`common/dialer/detour.go`（实际为 `:67/:71/:87/:90/:95`），并在 `:247/:266/:899/:956` 处引用
`route/route.go`（实际为 `:248/:267/:914/:971`）。请通过阅读来核实，而不是跟着注释走。

### AI 注意事项（AI cautions）

1. **预览绝不能消耗一个轮换槽位。** 传入 `commit=false`；断言 `CommittedSelections()` 未变。
2. **组不是一跳。** `Hop.IsGroup` 从未被设置（`physicalpath.go:261-264`）；组只对
   `ControlPath` 有贡献。
3. **不要反转 `ControlPath`。** 两个相邻字段以相反方向描述同一条路径，正是当前代码所避免的。
4. **不要通过解析一个它无法知道的标签来“改进”试运行。** 追加一个 `Unknown` 并停止；`Exit()`
   必须报告 `ok=false`。
5. **不要让校验去拨号、解析或选择**以回答一个它无法离线决定的问题。已记录的两个副作用（一条
   弃用日志、一次记忆化的标签→对象解析）就是完整清单；添加第三个就是 bug。
6. **不要把 `RequiredNetworks` 当作契约。** 它是只写的。
7. **不要在 `dryrun.go` 中恢复并集读法。** `:470-493` 处的孤立注释说的相反，而且它已死。
8. **不要无条件计入 `default_domain_resolver` 边**，也不要省略它——两个方向都曾被实测为错
   一次。
9. **不要教校验器通过 `Selector` 做预览选择。** 它的 `Selected` 忽略 network 参数；
   “blanket” `{tcp,udp}` 是有意的，并由 `protocol/group/selector_edge_test.go:11-30` 固定。
10. **不要把不带 `<tags>` 的测试运行当作该子系统的证据。** 根契约测试需要
    `DEFAULT_BUILD_TAGS_OTHERS`；参见 `ci-release.md`。
11. **不要为环检测引入第二张图。** 复用声明的成员关系 ∪ `adapter.Referrer.References`。
12. **不要在不应用 `d46717dbd` 更正的情况下，为 `ControlPath` 顺序引用
    `docs/fork/physicalpath-controlpath-model.md`。**
