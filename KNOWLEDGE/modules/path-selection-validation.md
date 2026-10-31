# 路径选择与启动期校验（Path selection and start-time validation）

## v0.1.9 之后的 AnyTLS 服务器解析与失败归因（AnyTLS server resolution and failure attribution after v0.1.9）

在 `011f3fdcd99`，当解析器策略为 `AsIS` 时，`protocol/anytls/outbound.go` 会让拨号器
优先使用 IPv4 解析代理服务器名。显式解析器或旧式策略仍然优先。这个偏好只作用于
`NewWithOptions` 构建本地解析拨号器的路径，不能推广到所有 detour 查询或目标地址。
它也不代表 IPv4 是唯一可用路径，更不能证明所有网络上的吞吐都会提高。

对 TCP 与 UoT 的 `DialContext`，AnyTLS 客户端错误会带上 outbound 标签与服务器名，同时保留
原始错误原因，便于定位是哪个代理的服务器解析失败。`ListenPacket` 直接
返回 UoT 客户端的结果，不经过这层命名包装。参见
`common/dialer/server_preference_test.go` 与 `protocol/anytls/proxy_failure_naming_test.go`。
在本次刷新中，两个包都通过了未加过滤的本地测试；这不是实时代理互通或性能证据。

## v0.1.9 之后的 Mieru 传输失败边界（Mieru transport failure boundaries after v0.1.9）

在 `011f3fdcd99` 上为 **VERIFIED_SOURCE**：`protocol/mieru/fallback.go` 把失败归成几类——
调用方取消、本地生命周期、endpoint、传输、目标与歧义。它在 SDK 扁平化错误链之前保留带类型的
socket 观测。一次 SOCKS 拒绝只是目标侧证据，绝不能凭它把底层传输标记为不健康。读 SOCKS 响应
时，裸 TCP EOF 属于歧义情形，UDP 情形则归为传输失败；具体判断应以对应分支为准，不能把所有 EOF 一视同仁。Mieru 的 inbound 与 outbound 把 close 注册到各自的 scope（`protocol/mieru/inbound.go`、
`outbound.go`）；关闭或代次替换不能解释为传输失败。这并不证明对端互通性，也不覆盖每一种失败
模式；请查阅 fallback 与早期失败测试以了解覆盖范围。

### 并行候选的组转发（Group forwarding of parallel candidates）

`Selector`、`URLTest` 与 `LoadBalance` 先选定一个成员，再通过 `DialParallelNetwork` 与
`ListenSerialNetworkPacket` 把目的地候选列表、网络策略、接口类型与 fallback 延迟转发下去。
嵌套且具备该能力的组同样转发这些参数，不会把计划压成一个地址。参见
`protocol/group/parallel_passthrough_test.go`。group 包的未加过滤测试在 `cb71cf80d72` 上本地
通过；这不证明真实网络切换，也不覆盖全部对端行为。

### 远程二进制规则集发布（Remote binary rule-set publication）

`common/srs/binary.go` 把 zlib 流排空到自然结束，再返回解析后的规则集，期间校验截断与校验和。
解压另有单独的边界，并使用独立的 `ErrDecompressedTooLarge`。`route/rule/rule_set_remote.go`
在没有缓存文件时走流式路径：先排空 HTTP body、捕获分帧错误，再构建并校验规则，最后才发布。
`Close` 把集合标记为已关闭并取消上下文；发布时在锁下检查关闭状态。不要把“完成规则解析”当成
“校验了外层流”，也不要在关闭之后再发布迟到的抓取。以上在 `011f3fdcd99` 上为
`VERIFIED_SOURCE`；本地 `./route/rule/...` 测试在本次刷新中通过，但不保证实时远程服务器或
设备上的行为。

远程规则集的 HTTP 客户端现在有五分钟超时（`rule_set_remote.go`），所以旧审计里“没有逐请求
超时”的说法已经过时。更新器仍然串行处理各个集合；`Close` 只取消上下文，不等待循环结束
（join），于是一个慢请求能把后续集合一直拖到有效超时为止。发布时会检查关闭状态，但一个已被复制、
已经越过检查的回调，并不会被普遍撤回。TUN 的 `updateRouteAddressSet` 与自动重定向关闭都由
`autoRedirectAccess` 串行化，并在调用重定向之前重新检查关闭状态。聚焦测试
`TestInFlightCallbackUnderCloseIsOrdered` 在本次刷新的 Windows 上通过；它不是 Linux
自动重定向集成测试，也不证明每个观测者都如此。

**范围（Scope）：** `common/physicalpath`、`common/routeflow`、`adapter/outbound/manager.go`
中的启动期校验、`detour` 解析、outbound 组、环、failover。

**来源（Provenance）：** `common/physicalpath` 与 `common/routeflow` 是 **fork 创建**的（在
`e4b7e692` 处不存在）；`protocol/group/loadbalance*.go` 与 `route/failover.go` 是 fork 新增。
`common/physicalpath` 是最大的 fork 原创子系统：v0.1.9 的统计为 **35 个提交、8,543 行 Go 代码
（含 16 个测试文件）**；到 `0067b03` 已是 37 个提交、21 个 `.go` 文件、9,407 行 Go 代码
（17 个测试文件）。

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

一份把组报告成一跳、或把跳列表反向报告的诊断，**描述的是流量并未走的路径**——这比没有诊断
更糟。

### 命名——务必理解正确，这是一个已被记录的陷阱（Naming — get this right, it is a documented trap）

| 类型 | 字段 | 顺序 | 锚点 |
|---|---|---|---|
| `Path`（一次 `Build` 的结果） | `ControlPath []string` | **下降序**（root → 最内层组） | `physicalpath.go:316` |
| `Path`（一次 `Build` 的结果） | `Hops []Hop` | **数据包序**（最靠近本设备者在前） | `physicalpath.go:320` |
| `PathNode`（遍历的节点） | `PhysicalPath []string` | 数据包序 | `leaves.go:56` |

**不存在 `PhysicalPath` 类型，也不存在 `Path.PhysicalPath` 字段。** `PhysicalPath` 是
`PathNode` 的一个字段，由 `Resolver.Hops()`/`Leaves()` 产生（`leaves.go:215`、`:257`）。

### 为什么 `ControlPath` 不反转——以及那份说法相反的文档（Why ControlPath is NOT reversed — and the document that says otherwise）

`reversePacketOrder`（`physicalpath.go:564-600`）反转 `Hops` 并重编号 `Position`，然后有意
**把 `ControlPath` 留在下降序**（`:571-587`），再用同一置换重映射 `Unknown.Position`
（`:592-599`）。

* 确立这一点的是修复 `d46717dbd`——“fix(path): ControlPath is the decision order, not the
  packet order”，2026-10-10 12:54:53，而它是发布基线的祖先。
* **`docs/fork/physicalpath-controlpath-model.md` 在此处已过时。** 它明确写着该修复“also
  reverses `ControlPath`”。但这份文档最后一次由 `e201b5add`（10:03:43）改动，比 `d46717dbd`
  **早 51 分钟**。请把那一段当成已被取代的说法；代码才是权威。
* 由 `common/physicalpath/physicalpath_test.go:462-482`
  （`TestControlPathIsShorterThanThePhysicalPath`）固定。注意这个测试函数的文档注释仍带着旧
  名称 `TestControlPathOrderIsNotPacketOrder`。断言是 `ControlPath == ["outer","inner"]`
  （下降序），而 `hopTags(path) == ["exit","member"]`（数据包序）——两个列表在**成员与顺序**
  上都不同。

### 方向证明，以及它是如何被决定的（The direction proof, and how it was decided）

配置的 `detour` 是一条**依赖**边：`X.detour = Y` 表示 X 消费 Y。代理 outbound 把用户的*目标*
封装进协议内部发送，只用自己的拨号器到达**它自己的服务器**：

```go
// sing/protocol/socks/client.go
:162  tcpConn, err := c.dialer.DialContext(ctx, N.NetworkTCP, c.serverAddr)   // the SERVER
:198  response, err = ClientHandshake5(tcpConn, command, address, ...)        // the TARGET, in the request
```

所以 `exit.detour = entry` 会让设备**先进入 entry、再进入 exit**。这个结论来自一次线缆（wire）
观测，而不是对上面几行代码的推理：`common/dialer/detour_wire_order_test.go` 用真实的
`NewDetour` 构建拨号器，换上一个会记录的对端，记下进入的是哪一跳，以及**它被请求的目标
地址**——只看地址分不清两种情况：“entry 被要求承载一条通往 exit 服务器的连接”，与“设备直接
拨号到 exit”。

固定这一点的是这些测试：`TestDetourTwoHopReachesTheDetourFirst`、
`TestDetourThreeHopKeepsTheOrder`、`TestOutboundToEndpointReachesTheEndpointFirst`、
`TestHopZeroIsTheNearestEntryForTheWholeMatrix`。其中两个测试被**改名**，因为它们断言的内容
与名称相反——较早的读法曾对外发布，后来由 `524ea41e` 修正。

**源码自己写明的限制**（`physicalpath.go:551-554`）：顺序由每一跳声明的拨号边派生。对一条
转发代理链（`detour` 表达的正是这种东西）它是精确的。它不为“替并非其依赖所命名的服务器承载
流量”的跳建模，也看不到配置未曾声明的路由。

---

## 2. 启动期校验——实际检查了什么，以何种顺序（Start-time validation — what is actually checked, and in what order）

`adapter/outbound/manager.go` 的 `Start`（`:168-263`）按以下顺序运行：

1. `lintOutbounds`（`:210`；定义在 `:417`）——依赖存在性检查与无环排序。
2. `validatePhysicalPaths`（`:231`）——可达叶（reachable-leaf）试运行。
3. `startOutbounds`（`:249`）。

顺序很关键：试运行在排序**之后**运行，所以它取代不了排序更精确的消息；排序算出的顺序直接传给
`startOutbounds`，不会被重新计算。

### 由代码陈述的只读契约（The read-only contract, stated by the code）

`manager.go:267-268` 将试运行定义为只读操作：不拨号、不解析、不改变选择，也不消耗轮换。`Resolver`（`physicalpath.go`）只持有注册表查询、`Snapshot` 和网络名，没有拨号器或时钟，其结构支持这一契约。这里的只读并不意味着整个校验过程没有日志或其他副作用，详见下一节。

`Build` 不会克隆报告中的最终出站；`TestTheReportedLeafIsTheSelectedLeaf` 通过 `require.Same` 验证结果与注册表中是同一个对象。

`TestTheDryRunConsumesNoRotationOnARealStart`（`adapter/outbound/manager_physicalpath_test.go:398-414`）同时断言 committed 和 previewed 均为 0，验证试运行不会消耗轮换。如果调用方不希望进行预览，可以设置 `Snapshot.Selections`，或传入 `""` 表示成员不可知（`TestASnapshotCanSayTheLeafIsNotKnowable`）。

叶不可知时，遍历会追加 `Unknown{Node, Position, Reason}` 然后**停止**。只要还有一个 unknown，
`Path.Exit()` 就报告 `ok=false`，因此部分验证的路径不会被当成已验证的路径
（`TestAnUnresolvableDependencyIsUnknownAndNotInvented`）。

### 校验仍有的副作用（But validation is not side-effect-free — the honest correction）

校验路径上确实有两个副作用。两者都不拨号；之所以都记下来，是因为“只读”这个笼统说法超出了
代码事实：

1. `common/dialer/dialer.go:217` 调用 `deprecated.Report(ctx, OptionMissingDomainResolver)`
   ——这是启动期校验期间发出的一条**弃用日志**（可达：`box.go:558` 传入
   `newDialer=false`）。
2. `common/dialer/capability.go:100` 调用 `detourDialer.Dialer()`，它把一次**记忆化的
   detour 解析**提交进 `sync.Once`（`common/dialer/detour.go:63`）。这次解析只把标签映射成
   对象，不拨号。

另外，`Box.Status`（`box.go:1396`）**有意**取一次实时的 `Selected()` 预览。那只是状态读取，
不是校验。

### 每项检查捕获什么（What each check catches）

| 检查 | 锚点 | 捕获 |
|---|---|---|
| 成员存在 | 既有的排序 | 某个组声明的成员没有指向任何东西 |
| 成员能承载根路由给它的每个网络 | 试运行 | 在 TCP 流经过的链里放一个仅 UDP 的成员 |
| endpoint 参与 | 试运行 | 两个不同对象声明了同一个标签 |
| 声明的 `destination_dns_ownership` 已实现 | 试运行 | 对端能收到什么做了承诺，而该对象兑现不了 |
| 声明的所有权有解析器 | 试运行 | 那条失败即拒绝的路径会破坏每一个具名目的地 |

detour 链上的每个节点都在**各自的位置**被检查，不只是出口：中间跳可能承载不了这条流，即使
出口能承载。这类能力发现只在启动时发出警告；受影响的流在运行时仍可能失败。见紧接下面的
严重性表。

试运行新增了一条有意的拒绝：在未实现 `destination_dns_ownership` 的 outbound 类型上声明这个
字段。该字段只存在于 fork，且在同一周期加入，因此**早于它的任何配置都不可能包含它**。

### 严重性：能力类发现警告，结构类发现拒绝（2026-10-20）（Severity: capability findings warn, structural findings refuse (2026-10-20)）

`Failure` 带一个 `Kind`，它在发现产生的地方就已决定，不靠匹配消息决定：

| 类别 | 示例 | 启动裁决 |
|---|---|---|
| `FailureCapability` | 一个不承载某条被路由网络的出口；一个无法服务该流的未选中成员；一个成员承载不了所投递网络的组 | **WARN**，启动继续 |
| `FailureStructure` | 一个并不存在的已声明成员；一个被 outbound 与 endpoint 同时声明的标签；一条永远到不了远端的路由 | **ERROR**，启动被拒绝 |
| `FailurePromise` | 在兑现不了它的类型上声明 `destination_dns_ownership` | **ERROR**，启动被拒绝 |

`Report.Err()` 只返回致命部分，`Report.Warnings()` 返回能力部分；outbound 管理器用注入的
日志器把后者记录一次（`SetLogger`，由 `box.go` 接线）。运行时没有变化：一条流到达承载不了
它的对象，仍然和以前一样失败。这个区分之所以存在，是因为能力类发现说的是**可能到来**的流，
而结构类发现说的是配置能否被构建。

同样的划分也适用于两项消费方检查：一个 HTTP 客户端有 detour，但这个 detour 服务不了每个已
配置传输；一个 DNS 传输有 detour，但它承载不了查询——两者都算警告。而一个不指向任何东西的
detour 标签仍然致命。

### 它取代的一处潜在 panic（A latent panic it replaced）

历史上的启动排序以**标签**标记已启动节点，并在标签计数达到**节点数**时停止。同一标签对应两个对象时，计数可能提前满足，留下未访问的节点；随后报告依赖问题的代码解引用 nil，在 `lintOutbounds` 中触发 `invalid memory address or nil pointer dereference`。当前 `lintOutbounds` 会在排序前检查重复的对象标识和标签。

此前引用的 `TestDuplicateMemberTagIsRefusedRatherThanCrashingTheSort` 不在当前代码树中。`adapter/outbound/tag_collision_test.go` 描述了命名空间冲突的设置，但本身不能证明 lint 路径；找到并运行对应测试之前，该行为应标记为源码已核实。

### 唯一一处构造侧效应（The one construction side effect）

`adapter/outbound/manager.go:170-188`：如果 `route.final` 为空，就会**创建**一个 `direct`
fallback 对象。这是这条路径上唯一的构造。

---

## 3. `common/routeflow` —— 可证明的需求（provable demand）

`common/routeflow/reach.go`（352 行，**没有测试文件**）。

`Reach(rules, finalOutbound) Demand`（`:95`）。`Demand`（`:278-302`）携带 `Proven`、
`Possible`、`Unclaimed`、`Reached`。`Demand.NetworksFor` 只返回 **`Proven`**
（`:302-307`），从不返回 `Possible`。

**UNKNOWN 策略**陈述在 `:47-70`，是一条关键规则：

> 未知规则在**两个方向上都是透明的**——既不给某个标签计费，也不移除它的网络。

值得了解的推论：

* **`route.final` 确实以 PROVEN 需求计入残余**（`:72-89`、`:177-183`）。较早的版本把
  final 当成“什么都不证明”，还固定了一个测试，要求“仅 TCP 的 final 必须能启动”；那是一次
  **实测的误拒绝（false deny）**，由 `133e6ed4f` 反转。当年被固定的测试
  （`TestFinalProvesNothingAboutDelivery`）**已不存在**——如果你在
  `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` 的
  §B/§H 中看到它被引用，那份文档在那里的内容已被取代。
* 可证明性意味着 scope 为 `Always` **并且** `terminates()`（`:214-228`），与 `matchRule`
  一致（`route/route.go:1279`）。
* **ICMP 被有意排除在外**（`:338-347`）。

### 三类按类别的能力检查（The three per-class capability checks）

| 消费方类别 | 检查 | 锚点 |
|---|---|---|
| 拨号器 / detour | `CheckDetourCapability` | `common/dialer/capability.go:40` |
| HTTP 客户端 | `HTTPClientTransportRequirement` | `common/httpclient/transport_capability.go:30`，调用点在 `common/httpclient/manager.go:122` |
| DNS 传输 | 按 DNS 类型的要求 | `dns/transport/capability.go:34` |

**整棵代码树里没有 `Consumer`/`ConsumerProvider` 类型**（`grep -i ConsumerProvider`
→ 零命中）。较早把该设计称作“consumer-first”的说法，其实指的是**拨号链的构造顺序**
（`route/route.go:248`、`:267`），不是校验模型。

---

## 4. 组：选择在被提交之前只是一次预览（Groups: selection is a preview until it is committed）

| 组 | `Selected(network)` 行为 | 锚点 |
|---|---|---|
| `Selector` | 返回已载入的原子值；**忽略 network 参数**；未选中时 `Network()` 报告 `{tcp,udp}`（即“blanket”） | `protocol/group/selector.go:232-234`（`Selected`）、`:173-179`（`Network`） |
| `URLTest` | 按网络过滤，读取历史存储，**不写回任何东西** | `protocol/group/urltest.go:222-239`（`Selected`；`Select` 在 `:745+`） |
| `LoadBalance` | 文档描述为“a pure preview”（纯预览）；委托给 `SelectForFlow(…, commit=false)`；轮询（round-robin）分支用 `Load()` 取得游标，只有在 `commit` 为 true 时才用 `Add(1)` 推进 | `protocol/group/loadbalance.go:255-261`（文档）、`:262-264`（`Selected`）、`:339-344`（游标） |

**`commit` 标志就是运行时控制面与物理面的分离**（`route/route.go:218-246`）。预览不得消耗
槽位：诊断一旦消耗了槽位，下一条流分到的成员就与它刚报告过的成员不同。

这些行为固定于**真实**组（而非 fixture）：`route/physicalpath_route_test.go`
（`TestPhysicalPathTakesNoSelectionPreviewThroughRealGroups`、
`TestTwoFlowsAdvanceTheCursorExactlyTwiceWithAPreviewInBetween`），并用一次反向破坏
（reverse-break）验证——把 `loadbalance.go` 的 `Selected` 改成传入 `commit=true`，观察
`CommittedSelections()` 发生变化。

`TestControlGroupIsNotAPhysicalHopThroughRealGroups` 断言：两个控制节点对应一跳。

### 运行时拨号所有权与 failover（Runtime dial ownership and failover）

`route/failover.go`：

* `routeDialOwner`（`:49-58`）要求 `FailoverEnabled()`。
* `routeDialDecision`（`:78-97`）：**目的地地址多于一个 ⇒ 没有所有者**（`:80-82`）；叶是
  `ConnectionHandler` ⇒ 以 `commit=true` 重新解析（`:87-95`）。
* `failoverDialer`（类型定义 `:121-125`）内嵌该叶，用于标签/网络/拷贝层面的调优，但通过
  `group.DialWithFailover` 拨号（`DialContext`/`DialWithFailover` `:141-143`）。

在测试目标上，这个 shim 显式实现 `ParallelNetworkDialer`：它把候选列表、网络策略、接口偏好与
fallback 延迟带在 failover 上下文里，好让被选中的成员用得上。它的 packet-listen 方法委托给
内嵌的叶；生产环境的 packet 路径直接传这个叶，不用 shim 做组重试。shim 有意不暴露通用拨号链，
因为内嵌的第一个叶未必就是后续某次尝试所用的成员。参见 `route/failover.go` 与
`failover_shim_surface_test.go`；针对性测试在本地通过，不是真实网络切换测试。

failover 预算属于**用户流**，而不是组调用：一条 `outer → inner` 链总共只花费**一次**备选。
按组设限会成倍放大。

---

## 5. `PathNode.RequiredNetworks` —— 一个实现细节，不是契约（an implementation detail, NOT a contract）

在整棵树上执行 `git grep -n RequiredNetworks` 有 15 处命中：

* **写入方（生产代码，2 处）：** `leaves.go:359`、`leaves.go:446`
* **读取方（测试，5 处）：** `diamond_routes_test.go:105,107,134,169`、
  `p1_contract_diagnostic_test.go:54`
* **读取方（生产代码，0 处）**

校验器**不**读它。它读 `HopCheck.Requirement`（`dryrun.go:71`），这个值由**函数参数**经
`nodeRequirementFor`/`selfRequirementFor` 算出来。

fork 自己的提交 `fc4bdd416` 就是这样说的：*“physicalpath.RequiredNetworks is still
write-only. It is populated by leaves.go and read nowhere … should be wired or removed by
the file's owner.”*（中文：physicalpath.RequiredNetworks 仍然只写。它由 leaves.go 填充，
无人读取……应由文件所有者接线或删除。）`docs/fork/v016-correction-round-ledger.md:249-251`
补充说，它的文档注释*“is actively wrong until the owner wires or deletes it.”*（中文：在
所有者接线或删除它之前，这个注释是明显错误的。）

**同一个包里共存着两种需求模型，而且互相矛盾。** `RequiredNetworks` 给**依赖**跳计入业务网络
（`leaves.go:480` 原样下传 `required`）；校验器却**豁免**这些跳（除非 `businessEntry`，否则
`nodeRequirementFor` 返回 nil，`dryrun.go:533-539`）。

请把它当作**留给文件所有者的后续事项（FOLLOW-UP）**，绝不要当作契约。不要拿它当“依赖跳会被
计入网络”的证据。

### `dryrun.go` 内部第二处带日期的矛盾（A second, dated contradiction inside dryrun.go）

`dryrun.go:543-556` 是一个**孤立注释块**：`:542` 的空行把它和 `nodeRequirementFor` 在
`:541` 的右花括号分开。注释声称窄读法曾被“tried and REVERSED … The union therefore
STAYS”。可 `:384-387` 的代码在 `!requirementProven` 时**恰好**实现了那种窄化
（`effective = selfRequirementFor(node)`），而 `selfRequirementFor` 自己在 `:568-589` 的
文档又和这条孤立注释正相反。

提交顺序可以证明这一点：`d95e256b7`（2026-10-12 04:49:42）**删除**了 `selfRequirementFor` 并
加入这条注释；`31f250ff1`（06:08:04）**重新加入**辅助函数，同时保留了注释
（`git merge-base --is-ancestor d95e256b7 31f250ff1` → 是）。**这条注释已死，代码已定。绝不
要恢复并集读法。**

### 一条永远不会触发的诊断分支（A diagnostic branch that can never fire）

`requirementClause` 里声明的分支（`dryrun.go:1016-1023`）**不可达**：`selfRequirementFor`
= `decidedNetworks(networksOf(node.Outbound))`（`:590-592`），而 `decidedNetworks` 返回的是
**子集**（`:838-850`）。所以 `!requirementProven` 时，需求是该节点自身 `Network()` 的子集，
`validateNode` 第二步的失败分支（`:862-920`）——它唯一的调用点（`:893`、`:918`）——便无法
触发。推论：凡是要构建消息的地方，`HopCheck.RequirementProven` 恒为 true。

当前测试文件准确地讲了这段故事，并且**不再断言那条不可达的字符串**：
`box_physicalpath_route_aware_test.go` 把 `NotContains` 放在 `:738-740`；它在 `:743-753` 的
注释明确说，仅含对象那类措辞“is therefore unreachable through this shape”。`:757-768` 的
fixture 只断言 Start 成功，不声称任何证明。不要复活旧的“注释与自己的 fixture/断言矛盾”的
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

环检测复用**既有的**图（声明的成员关系 ∪ `adapter.Referrer.References`），也就是
`cross_kind_cycle.go` 在启动时校验的那组边。遍历自己的下降过程*就是*环记录，正是
`route.resolveOutbound` 已经在用的形状。**没有引入第二张图**；请保持这样。

`default_domain_resolver` 被建模成一条图的边（`box.go:616-618`：`SetDefaultDomainResolver`
安装一个**闭包**，所以答案始终实时）。无条件计入它，曾是一次**实测的误拒绝**
（`cross_kind_cycle.go:288-294`）；省略它，又会接受一个首次拨号就挂起的环
（`box.go:606-612`）。两个方向各错过一次。

---

## 7. 接缝——每处只有一个生产调用点（Seams — one production call site each）

| 接缝 | 消费方 | 锚点 |
|---|---|---|
| `EnablePhysicalPathValidation` | `box.New` | `manager.go:101` ← `box.go:507` |
| `SetResolverAuthority` | `box.New` | `manager.go:127` ← `box.go:544` |
| `EnablePhysicalPathDelivery` | **仅**在 `router.Initialize` 成功之后 | `manager.go:149` ← `box.go:622` |
| `SetDefaultDomainResolver` | `box.New` | `manager.go:54` ← `box.go:637` |
| `ValidateRoots` | `Manager.Start` | `dryrun.go:256` ← `manager.go:365` |

`box.go:616-622` 指出，`EnablePhysicalPathDelivery` 只在 `router.Initialize` 成功之后调用，
因为只有那一刻才能从**已编译**规则推出“路由已证明的投递”。

**这些声明都是选择加入（opt-in）的。** 一个从未获得这些声明的 `Manager` 完全像以前一样运行；
打开试运行的开关是 `EnablePhysicalPathValidation`，而调用它的只有 `box.New`
（`TestNoDryRunWithoutTheDeclarationsKeepsTheManagerUnchanged`）。

---

## 8. 证据、来源与注意事项（Evidence, provenance, cautions）

### 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| 试运行不拨号、不解析、不消耗轮换 | `VERIFIED_UNIT`（+ 变异对照） | `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...` |
| `ControlPath` 是下降序，`Hops` 是数据包序 | `VERIFIED_UNIT` | `physicalpath_test.go:462-482` |
| 控制组永远不会成为一跳 | `VERIFIED_UNIT`，在真实组上 | `route/physicalpath_route_test.go` |
| `route.final` 以已证明的方式计入残余 | `VERIFIED_UNIT` | `box_physicalpath_unconstrained_test.go:62` |
| 误拒绝矩阵（11 组接受/拒绝孪生） | `VERIFIED_UNIT`（需要 tags） | `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestFalseDenyMatrix .` |
| 跨类型环在启动时被拒绝 | `VERIFIED_UNIT` | `box_cross_kind_cycle_test.go`, `box_static_validation_final_wire_test.go` |
| `RequiredNetworks` 语义 | **仅 `VERIFIED_SOURCE`**——只写、无生产读取方、不构成契约 | `git grep -n RequiredNetworks` |
| 跨类型图**整体**的健全性 | **`UNKNOWN`**——CK1–CK7 从未运行；只闭合了被点名的缺口 | — |

### 来源（Provenance）

| 提交 | 类型 | 它做了什么 |
|---|---|---|
| `01262510` | FEATURE | 实现 `PhysicalPath`/`ControlPath` 模型与启动期可达叶试运行 |
| `524ea41e` | FIX | 让 `Hops` 成为数据包序，由拨号路径而非遍历决定。**九个测试变红**；其中两个被改名，因为它们断言的内容与名称相反。 |
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
| `d95e256b7` | REFACTOR | 删掉两个死掉的辅助函数——**并加入 §5 描述的孤立注释** |
| `d2b34f1cf`…`386e95a70` | FEATURE/FIX/DOC | Direct Offload 链——参见 `data-path-optimization.md` |

以上全部为 **OWN**。`d2b34f1cf`… 与 `data-path-optimization.md` 共享。

### 已被取代的文档——不要把它们当作当前内容引用（Superseded documents — do not cite them as current）

| 文档 | 在何处被取代 | 被谁取代 |
|---|---|---|
| `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` §B（`:141-142`）、§H（`:350-367`） | `route.final` 是已证明还是未证明；`TestFinalProvesNothingAboutDelivery` **已不存在** | `133e6ed4f`；`reach.go:177-183`；`box_physicalpath_unconstrained_test.go:62` |
| 同一文档 §C（`:169-170`） | “'does not carry' 恰好只在一种构造里出现”在全树范围内为假 | 还有 `common/dialer/capability.go:59`、`dns/transport/capability.go:45`、`common/httpclient/transport_capability.go:44` |
| `KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §4、§9 | `CROSS_KIND_GRAPH_SOUND=NO`、`ENDPOINT_RESOLVER_EDGE_SOUND=NO`、`DEFAULT_RESOLVER_EDGE_SOUND=NO`、`GROUP_FALSE_DENY_REMAINS=YES` | `ff0040079`、`4e228f539`、`31f250ff1`；`manager_physicalpath_test.go:249-271` |
| `docs/fork/v016-five-workstream-final-report.md:565-587`、`docs/fork/v016-overnight-implementation-report.md:479` | 声明 J-1/J-2/J-3 未实现（NOT IMPLEMENTED） | 三者均已存在（`01262510`、`common/physicalpath/status.go`、`box.go:1318`） |
| `docs/fork/v016-correction-round-ledger.md:252-254` | “`Selector.References()` lies before Start” | 由 `96e6dc419` 修复（`selector.go:269-274`） |

`KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §1 的组表**仍然正确**。

### 代码自身内部的过时引用（Stale citations inside the code itself）

`common/physicalpath/physicalpath.go` 的包文档在 `:57/:61/:77/:80/:85` 处引用
`common/dialer/detour.go`（实际为 `:67/:71/:87/:90/:95`），并在 `:247/:266/:899/:956` 处引用
`route/route.go`（实际为 `:248/:267/:914/:971`）。请靠阅读核实，不要跟着注释走。

### AI 注意事项（AI cautions）

1. **预览绝不能消耗一个轮换槽位。** 传入 `commit=false`；断言 `CommittedSelections()` 未变。
2. **组不是一跳。** `Hop.IsGroup` 从未被设置（`physicalpath.go:261-264`）；组只对
   `ControlPath` 有贡献。
3. **不要反转 `ControlPath`。** 两个相邻字段用相反方向描述同一条路径，正是当前代码避免的。
4. **不要靠解析一个它无法知道的标签来“改进”试运行。** 追加一个 `Unknown` 并停止；`Exit()`
   必须报告 `ok=false`。
5. **不要让校验去拨号、解析或选择**以回答一个它无法离线决定的问题。记录在案的两个副作用（一条
   弃用日志、一次记忆化的标签→对象解析）就是完整清单；添加第三个就是 bug。
6. **不要把 `RequiredNetworks` 当作契约。** 它是只写的。
7. **不要在 `dryrun.go` 中恢复并集读法。** `:470-493` 处的孤立注释说的相反，而且它已死。
8. **不要无条件计入 `default_domain_resolver` 边**，也不要省略它——两个方向都各被实测错过
   一次。
9. **不要教校验器用 `Selector` 做预览选择。** 它的 `Selected` 忽略 network 参数；
   “blanket” `{tcp,udp}` 是有意的，并由 `protocol/group/selector_edge_test.go:11-30` 固定。
10. **不要把不带 `<tags>` 的测试运行当作该子系统的证据。** 根契约测试需要
    `DEFAULT_BUILD_TAGS_OTHERS`；参见 `ci-release.md`。
11. **不要为环检测引入第二张图。** 复用声明的成员关系 ∪ `adapter.Referrer.References`。
12. **没有应用 `d46717dbd` 的更正，就不要为 `ControlPath` 顺序引用
    `docs/fork/physicalpath-controlpath-model.md`。**
