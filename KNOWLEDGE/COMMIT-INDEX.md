# 提交导航（Commit navigation）

## 2026-10-29 增量导航（delta navigation）

| 提交 | 变更行为 |
|---|---|
| 子仓库 Apple `aeccc75`, `16cf87b`；Android `c783710`；Desktop `6d4ccf8` | 已纳入父仓库的客户端变更 → `modules/child-repositories.md` |
| 子仓库 sing-tun `f9d880f`, `8b9a380`；uTLS `3ab4752`, `2da328d` | 路由端口与 TLS 修复 → `modules/child-repositories.md` |
| Apple `044e6d5`, 父仓库 `a741e3e916f` | 集成已合并，并由 v0.1.34 使用 → `modules/child-repositories.md` |
| `e61f9d5160e`, `14e21140564`, `0067b03e377` | Apple 构建/打包/拓扑 fixture 与结构守卫修正 → `modules/ci-release.md` |
| `93b2e4f5eec` | 端点本地的流可以使用 TUN 路由端口 → `modules/masque-connectip.md` |
| `ab5fcb59abd` | 常规流与宿主流共用同一个流日志器 → 同一模块 |
| `8dac10c14c8`, `62f5ecefdf6`, `d0bccc9bf2a`, `3c6ca2ea336` | 发布冻结、伪版本（pseudo-version）、幂等性与已提交的 ABI 门禁 → `modules/ci-release.md` |
| `23730fffed3`, `fc1567f4c4d`, `dbcfc1d359d` | Apple 单 runner 路径、scratch 目录与 composite-action 工具链检查 → `modules/ci-release.md` |

## v0.1.9 之后的导航锚点（Post-v0.1.9 navigation anchors，测试目标 011f3fdcd99）

这些提交只是导航辅助，替代不了当前源码，本身也确立不了
外部来源或测试结果。

| 提交 | 主题与归属 |
|---|---|
| `01be4b9d42d` | 按流的候选准入（candidate admission）→ `modules/dns-demand.md` |
| `c10ff9186cf` | 空 DNS 地址族的日志记录 → `modules/dns-demand.md` |
| `c78edf21084` | 删掉无效的 AI 标签回退 → `modules/fast-path-group-policy.md` |
| `cd2316a704a` | 消费者 detour 能力警告 → `modules/path-selection-validation.md` |
| `0ef38b8ff91`, `98387b223d8` | Naive H3/H2 重试与预算 → `modules/naive-http-transport.md` |
| `20482fa067d` | FakeIP 嗅探、映射地址与 kTLS 边界 → `modules/sniff.md` |
| `3145d7dbdfc`, `1ff0af39110` | TUN 交接与启动后重置 → `modules/libbox-platform-contract.md` |
| `3f67490206d`, `e1efe4cafdc` | Mieru 集成与按需启用的 TCP/UDP 回退 → `modules/mieru.md` |
| `5034c5ee046`, `08699fd2c40` | 半关闭（half-close）观测与结算 → `modules/core-lifecycle-runtime.md` |
| `3edcf4fa226`, `c8f3a740710`, `08ff473b5a5` | 串行化的 OOM 回收与重载意图 → `modules/power-mobile-lifecycle.md` |
| `a1b2174654a` | 通配监听器的地址族选择 → `modules/core-lifecycle-runtime.md` |
| `7c0cc0bacaa` | Windows TUN 读取错误分类；显式接受的值比注释里写的更窄 → `modules/core-lifecycle-runtime.md` |

这份演进索引用相关提交解释行为的由来，并非完整变更日志。每条记录对应当时的评审点；判断历史行为是否仍然有效时，应先核对当前源码。

## 如何使用（How to use it）

1. 找到准备修改的行为。
2. 将 **最终行为（Final behaviour）** 理解为评审时的结论；使用前对照当前源码和 `BASELINE.md`。
3. 需要了解决策过程时，按顺序用 `git show <sha>` 查看相关提交。
4. 留意 **被取代（Superseded）** 的早期方案，了解它被替换的原因，避免重复相同问题。

## 阅读此处任何 SHA 之前需要知道的两件事（Two things to know before reading any SHA here）

* 从当前上游引用可达（reachability）只是祖先关系检查，证明不了作者身份。上游
  历史被重写过；判定来源之前，请比较补丁、blob 和历史证据，
  带更旧来源标签的条目也一样。
* **不要按 SHA 统计上游工作量。** 上游会 rebase `testing`，所以一个逻辑上的上游变更，可能以不同 SHA
  在该范围内出现多达 11 次。见 `BASELINE.md` §provenance。

## 来源词汇表（Provenance vocabulary）

| 标签 | 含义 |
|---|---|
| `OWN` | 在 fork 分支中自行编写 |
| `UPSTREAM-INHERITED` | 存在于 `e4b7e692` 之下的上游提交 |
| `UPSTREAM-ABSORBED` | fork 重新应用的上游变更；**其内容已在基线中** |
| `EXTERNAL-ADAPTED` | 派生自具名的外部来源，并在提交或模块中被引用 |
| `SUPERSEDED` | 变更已不在代码树中；为保留推理而留着 |
| `ARCHIVE-ONLY` | 被回退的变更，刻意保留 |

类型词汇：`FEATURE`、`FIX`、`HARDENING`、`PERF`、`MEMORY`、`ZERO-COPY`、`COMPAT`、`LIFECYCLE`、
`ROUTING`、`DNS`、`SNIFF`、`CHAIN-PROXY`、`MASQUE`、`NAIVE`、`PLATFORM`、`LIBBOX`、`REFACTOR`、`TEST`、
`INTEROP`、`CI`、`BUILD`、`RELEASE`、`DOC`、`REPOSITORY-HYGIENE`。

---

## 1. 启动期证明与路径模型（Start-time proof and the path model）→ `path-selection-validation.md`

**初始问题：** 配置可以声明一条链路，而这条链路静默地承载不了被路由到它上面的流量。
故障要到拨号时、在用户的连接上才暴露出来。

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `01262510` | FEATURE | OWN | 引入 `PhysicalPath`/`ControlPath` 模型和启动期可达叶子（reachable-leaf）试运行 |
| `524ea41e` | FIX | OWN | 让 `Hops` 变成**包顺序（packet order）**，从拨号边沿推导出来。**九个测试变红**；其中两个被改名，因为它们断言的内容正好与名字相反。 |
| `d46717dbd` | FIX | OWN | `ControlPath` 是**决策顺序（decision order）**，不是包顺序——此前 51 分钟，`e201b5add` 记的正好是反过来的说法 |
| `e3399187` | FIX | OWN | 解析器状态按遍历（per-walk）保存；菱形（diamond）按路由分别校验 |
| `fc4bdd416` | FIX | OWN | 三条需求事实；记下 `RequiredNetworks` 是**只写的（write-only）** |
| `8299f30e` | FIX | OWN | 导出的试运行直接拒绝被截断的路由，不再留给 lint |
| `8ae8f10d` | FIX | OWN | 被截断的路由不能躲在同级路由后面 |
| `91f595906` | FIX | OWN | `destination_dns_ownership` 对本地拨号器是空的 |
| `cf5a25bbe` | FIX | OWN | 为**每一个**已证明的本地拨号器补齐所有权一致性 |
| `33eba5bf7` | FIX | OWN | 按**可达的流类型**校验能力，不按入口能力 |
| `0dfedb382` | FIX | OWN | 只在运行时路由器真正停下的地方消费协议 |
| `04591f8` | FIX | OWN | 解析器只有一个事实来源，以及它消掉的误拒（false deny） |
| `4e228f53` | FIX | OWN | **默认解析器边沿是多计（over-charged）的，不是少计（under-charged）**——推翻了先前的决定 |
| `31f250ff` | FIX | OWN | 定下三条契约：只证明可证明的需求、组并集（group union）、图边（graph edges） |
| `ff004007` | FIX | OWN | 在图实际遍历的适配器上发布解析器事实 |
| `5ad4c032` | FIX | OWN | 在每个构造点填充服务器域（server-domain）事实 |
| `d95e256b7` | REFACTOR | OWN | 删掉两个死掉的辅助函数——**还额外加了与代码矛盾的孤儿注释** |

**最终行为（Final behaviour）：** 校验是只读的、消费者优先的，而且只依据可证明的事实；`Hops` 是包顺序，
`ControlPath` 是下降顺序（descent order）；控制组永远不是一个 hop；`route.final` 的残差按已证明计入；
`RequiredNetworks` 是只写的。

**被取代（Superseded）：** `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` §B/§H（其中的
`TestFinalProvesNothingAboutDelivery` 已不存在）；`KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §4/§9（三条 `= NO` 结论后来都关掉了）；
两份 `docs/fork/v016-*` 报告在 J-1/J-2/J-3 都已存在时，还写着
“NOT IMPLEMENTED”。

**证据：** 10 个根目录 `box_static_validation_*_test.go` 文件、5 个 `box_physicalpath_*_test.go`、
`box_cross_kind_cycle_test.go`、16 个 `common/physicalpath` 测试文件，
以及四项已记录的**变异（mutation）**对照。

---

## 2. 核心生命周期与运行时协调（Core lifecycle and runtime coordination）→ `core-lifecycle-runtime.md`

**初始问题：** 拆除按组件各干各的，不可靠；而一次网络切换会让
所有资源同时重建。

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `8c7b230d` | LIFECYCLE | OWN | 引入资源生命周期协调器 |
| `5f56f02c` | MEMORY | OWN | 内存偏高但还没到危急时，裁掉空闲资源 |
| `09964183` | FIX | OWN | 阻止内存裁剪另建一个传输 |
| `379b21161` | FIX | OWN | 基于 generation 的重绑定预算 |
| `93823c3d` | MEMORY | OWN | 退役唤醒后**不可达**的连接池；能排空的就排空 |
| `8735b1d` | MEMORY | OWN | 让退役遍历覆盖到 HTTP 客户端连接池（此前从未覆盖） |
| `1331e8567` | FIX | OWN | `TrimMemory` 不推进 generation；`ReleaseMemory` 推进 |
| `1f85dbc94` | FIX | OWN | 从边界遍历可达**全部四个**连接池 |
| `7e13f461` | FIX | **OWN, not upstream** | 清除**由所有者注册的**进程级接口 getter——提交正文说，是 fork 自己的重构把它丢掉的 |

**最终行为（Final behaviour）：** epoch 只有一条且单调；重建靠**租约身份（lease identity）**，不是一个标志位；
退役断言的是 `IdleConnectionReleaser`，不是 `IdleConnectionKeeper`；迟到的 `scope.Add` 在注册它的
goroutine 上同步运行。

**被取代：** 无。**曾经写错的措辞是：**“`7e13f461` 吸收了一次上游的
接口 getter 重置”——它修的是 fork 自己引入的泄漏。

**证据：** `adapter/lifecycle*_test.go`、`common/runtimecoord/*_test.go`、
`route/reference_coverage_stress_test.go`、`route/reusable_owner_inventory_test.go`。

---

## 3. 嗅探（Sniff）→ `sniff.md`

**初始问题：** 嗅探会复制整个数据报，还能被驱动到内存无界增长。

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `e053cc733` | PERF + REFACTOR | OWN | 显式的 plan 列表；`payloadReader`；TLS 头部字段门禁及其哨兵值；包嗅探器（packet-sniffer）的间接层 |
| `300b167b4` | PERF | OWN | 重试由上一轮失败驱动 |
| `912778dd6` | FIX | OWN | 加入 **WireGuard** 嗅探器，并挪到 UTP 之前（UTP 此前会抢走握手） |
| `91577eaa2` | PERF | OWN | HTTP 门禁 + 重放读取器（replay reader） |
| `4dff25c90` | PERF | OWN | `takePacketBytes` 返回**视图（views）** |
| `9167595c7` | PERF | OWN | `frameSummary` 替掉了帧类型切片 |
| `bfe721454` | PERF + HARDENING | OWN | 以包为边界的复制**以及** `Length` 下界守卫——移植自 `1b0cedf40` |
| `578663e0` | HARDENING + FIX | OWN | `maxSniffRounds = 64`、放弃时释放（release-on-abandon），以及 `matchRule` 错误路径上的缓冲区释放 |
| `1292a85a5` | TEST | OWN | `wireguard.go` 的注释块（0 行非注释代码） |
| `a05a6954f` | **UPSTREAM-ABSORBED** | — | “Fix DNS sniffer matching non-DNS packets”：`dns.go` 在两端逐字节相同 |
| `b93c30f2a` | **UPSTREAM-ABSORBED** | — | “Fix protocol input validation”：CRYPTO 长度守卫在 `e4b7e692` 处已存在 |
| `dd4f62edc` | **UPSTREAM-ABSORBED** | — | “Rewrite HTTP proxy protocol”：`http.go:14`, `:95` 在 `e4b7e692` 处已存在 |

**最终行为（Final behaviour）：** TLS 嗅探不做服务器握手；QUIC 用限定到当前包的数据报视图来检视；
分片循环只在 `ErrNeedMoreData` 时继续，上限 64。

**证据：** 22 个新增测试文件、两个 fuzz 目标、地址范围与修改原始数据报的所有权
证明。**上限和放弃时释放只有 `VERIFIED_SOURCE`**——没有测试覆盖它们。

---

## 4. DNS → `dns-demand.md`

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `5b0a33c` | FEATURE | OWN | `dns/transport/group`——stable / fastest / parallel 传输组 |
| `a123864b` | FIX | OWN | 策略 epoch 接管**缓存提交**，不只是检查 |
| `ffec0df1` | FIX | OWN | 只涉及 DNS 的环境变化，获得**自己的**代次 |
| `8fad3e9b` | FIX | OWN | FakeIP 服务器不得应答目的地查询，而 `domain_resolver` 可能指定这样一个服务器 |
| `296bb6f94` → `3711437d4` | FIX/MEMORY | OWN | DNS 缓存、池化与代次这一族（17 个提交） |
| `1b405d865` → `c443a8df7` | FEATURE/REFACTOR | OWN | DNS 族流式处理；收集器后来成了死代码，被移除（`721e6c4db`） |

**当前行为（Current behavior）：** 一次已安定启动（settled-start）的 DNS 查询，可能在切换之后才交付它带时间戳的
答案，但代次变了就不能提交；切换期间启动的查询不会作为稳定结果交付。
FakeIP 服务器解析不了目的地；DNS 传输需求的 TCP 段是 `Conditional`，
因为只有截断时才重试。

**已知缺陷——已修复；说它没修的文档已经过时：** 记在
`docs/RELEASE-CERTIFICATE.md:70-85` 的 `dns/transport/local.(*DBusResolvedResolver).loopUpdateStatus` goroutine 泄漏
已由 **`6b495134c`** 修复（`git log -S'signalLoopStop' e4b7e692..a267e99` 恰好只返回该提交）。
`VERIFIED_SOURCE`；执行需要 Linux + systemd-resolved。

---

## 5. MASQUE / CONNECT-IP → `masque-connectip.md`

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `b83089ec` | — | **UPSTREAM-INHERITED** | “Add MASQUE support”，世界，2026-09-21——**存在于 `e4b7e692` 之下**，不是 fork 的功能 |
| `d53c867b7` | FEATURE | OWN | **fork 自己创作的第一个提交**——入站伪装（masquerade）处理器 |
| `7e57fe8c7` | HARDENING | OWN | 历史上的限流器工作；现在连接数超限会在 accept 时直接关闭，不返回 429。见 `modules/naive-http-transport.md` |
| `b83695603` → `1511c1c6a` | FEATURE/FIX | OWN | H3 连接池在**并列最小值（tied-minimum）**集合内轮转（此前槽位 0 服务所有隧道） |
| `0271b145a` | HARDENING | OWN | CONNECT 以 `HandshakeComplete` 为门禁（0-RTT 重放） |
| `9491008a9` | FIX | OWN | `http3_fallback` 是死配置 |
| `e4f847ecf` | COMPAT | **EXTERNAL-ADAPTED** | QUIC 默认值取自 `quic-go/masque-go` + `connect-ip-go` |
| `969909bc1` → `b656b062f` | HARDENING → PERF | OWN | 跨协议重叠检查；*正确*的二次方版本曾经是一场 **800× CPU DoS**（13.5 s 对 16 ms） |
| `a529b68c8` | HARDENING | **EXTERNAL-ADAPTED** | 8192 条目的 capsule 上限（此前为 149,796 条目 / 17.65 MiB）；常量来自 `connect-ip-go` |
| `e4d2f6c1a` | FIX | OWN | 在服务器自身地址上满足 `Contains() == lookup()` |
| `6d94da584` → `b5a25edd8` | PERF/ZERO-COPY | OWN | 无逐包分配；`errors.As(&tooLarge)` **即便在 `err == nil` 时**也会逃逸到堆上 |
| `8870623cf` | FEATURE | OWN | bootstrap 缓存 |
| `8f1ba42a9` → `1d87f0cf7`, `e93e297aa` | FEATURE/REFACTOR | OWN | DNS 角色显式化；不可变快照取代了被删掉的 `assigned_dns_*` 模型 |
| `2118ee40` | FEATURE | OWN | 引入 MASQUE detour 容量处理；见下文关于当前 initial size 的限定说明 |
| `0d28c26d` | FIX | OWN | 使 H3 回退可达；允许隧道段分片 |
| `a367bbe3` | FIX/HARDENING | OWN | 关闭就绪窗口；每次 DNS 失效串行化 |
| `a00a2fe6` | FIX | OWN | H3 获取是**关闭决策**的一部分 |
| `9235f90` | FIX | OWN | 服务器端点由负责关闭它的 `Scope` 清理 |
| `c3b4f567` | FIX | OWN | 释放**在 `Scope.Close` 返回之后**获取的资源 |

**当前行为：** 每个批处理块（batch chunk）在所有权上都是全有或全无；被接受的前缀会转移所有权，
只有剩下的缓冲区能回退。上下文 ID 替换会拒绝受影响的块。
HTTP/3 复制回退可能在后续错误之前先排入若干副本，所以这不是恰好一次（exactly-once）交付。
外层 QUIC 的 initial size 受已证明的 detour 容量限制，但后续包有没有硬上限，没有证明；
见 `modules/mtu-and-geometry.md`。

---

## 6. 原生 Naive（Native Naive）→ `naive-http-transport.md`

**初始问题：** fork 需要一个带 fork 资源控制的 NaiveProxy **服务端**入站，
而填充/分帧契约必须匹配一个它并不拥有的客户端。

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `45f6686a6` | FEATURE | **EXTERNAL-ADAPTED** | 原生 Naive 入站（参考：`klzgrad/forwardproxy@naive d62c80d3…`，在第 320/352 行引用） |
| `23af897bf` | HARDENING | OWN | 移除 `-connect-authority` |
| `593f634a1` | FIX | UPSTREAM-ABSORBED-style | 劫持（hijack）已缓冲的字节 |
| `fb90272a5` | FIX | OWN | 一次 `io.Writer` 短写（short write）导致**帧不同步** |
| `b16660ed1` | HARDENING | OWN | 去掉一处时序信号 |
| `3c6e0aad3` → `fe5789539` | FIX → FIX | OWN | 先加了个截断（clamp），随后又**撤销**；完整的 0..255 填充范围才对。所谓“panic 修复”**本身就是一次协议变更**——有个测试误用了 `buf.Resize(start, end)`，第二个参数其实是**长度**。 |
| `e9d329380` | COMPAT | UPSTREAM-ABSORBED-style | 响应中的 `Padding` 头变为无条件发送 |
| `d4ead1498` | HARDENING | OWN | 引入 `packetDestinationGuard` |
| `7029722bf` | HARDENING | OWN | 唯一一个需要合取条件（conjunctive）、仅限 TCP 的 self-SSH (port elided) 例外 |
| `1dc734f75`, `3bbf17907`, `10e1f2a95`, `d1f3798a6`, `2ec06e451` | HARDENING | OWN | 守卫顺序与 IPv4 映射地址（IPv4-mapped）规范化 |
| `724035c76` | REFACTOR | OWN | `server_limits.idle_timeout` 被解析却**从没被读取**——连同推理一起删了 |
| `bbfc31582` → `968395304` | REFACTOR | OWN | Clash API 先被移除（−1908），随后**恢复**（+2254） |

**最终行为：** 解析 → 认证 → 隧道；填充经协商并应用到前 8 个帧；
目标 ACL 先解析再检查；逐数据报的 UoT 守卫在**读**侧拦截，
没有规则匹配就放行。

**测试完整性的瑕疵：** 先前那个“v2 非 Connect 是 P0 缺陷”的结论，其实是个
**测试 fixture 错误**。

---

## 7. 数据路径：卸载、类别与调度器（Data path: offload, class, scheduler）→ `data-path-optimization.md`

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `d2b34f1cf` | FEATURE | OWN | bypass/socket 语义的 profile |
| `3dea03fd5` | FEATURE | OWN | **按流而不是按配置回答 bypass 问题** |
| `8a6e5f3cc` | FEATURE | OWN | `BypassVerdict` + 成本基准测试 |
| `143f869a9` | TEST | OWN | `ActionBypass` 追踪 |
| `ff80ddea6` | FIX | OWN | 携带 ICMP `Port` 的 bypass 被改写为 `ActionFlow` |
| `497d37727` | FIX | OWN | 把 DNS 劫持排在路由地址集之前（`8.8.8.8:53` 此前会离开 sing-box） |
| `386e95a70` | DOC | OWN | 记录 TUN 模式下的限制 |
| `e27d58711` | PERF | OWN | 为那条每个合格流耗费 5 次分配 / 约 95 ns 的调试日志行加上门禁 |
| `544c1ab5e` | FEATURE | OWN | 流量类别（traffic class） |
| `94df98f92` | FEATURE | OWN | 调度器与门禁 |
| `4e34d0091` | FEATURE | OWN | 新增 `route.traffic_scheduler.upload_rate` |
| `bd7b1ed19` | FIX | OWN | `writePeriod` 采用饱和处理，不再把 `float64` 纳秒转成 `Duration` |
| `bb21a854c` | TEST | OWN | **`trafficresearch` 构建标签**——该原型在 Linux 上确定性失败 |
| `4f999d461` | TEST | OWN | HIGH 对 HIGH 的门禁从 p99 改为基于队列与中位数 |
| `bcc715a72`, `5ab49bdcb` | FEATURE/FIX | OWN | 拨号调速器（dial governor），随后是以等待代替轮询 |
| `58050da3c` | DOC | OWN | **引用了那个伪造的 fixture 基准测试** |
| `22d46966c` | FIX | OWN | 两天后修好了那个基准 fixture——但文档始终没更新 |

**最终行为：** 按语义等价判断，不看配置是否为空；未分类的选项会被拒绝；这个裁决**只**在 Linux
`auto_redirect` 下才是真正的 bypass；调度器未设置时保持惰性，
配置后连续工作。

**已知伪造：** `direct-offload.md:210` 的 `2856 B / 26 allocs`。**已过期：** `direct-offload.md:106` 的
sing-tun 锚点（`d769a7080ca2`）已落后两个 pin。

---

## 8. 电源与移动生命周期（Power and mobile lifecycle）→ `power-mobile-lifecycle.md`

**初始问题：** 睡眠时长测错了，而一次恢复（resume）又把它重新锚定。

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `aac340808` | LIFECYCLE | OWN | 睡眠权威（sleep authority） |
| `d93b68560`, `89b4b2c25`, `6ebb5799a` | FIX | OWN | 在 `DEEP_IDLE` 时释放连接池；不可达的 `DEEP_IDLE` 分支；一个过期的截止时间 |
| `0ff90482` | FIX | OWN | 把设备恢复边沿用在**边沿锁存（edge latch）**上，不用在屏幕级别上 |
| `e4327dca` | LIFECYCLE | OWN | 接通 Apple 设备状态的事件发布，并在解锁时解除暂停 |
| `4c7d96c0` | FIX | OWN | 将 Apple 设备状态处理纳入**发布分支**，而非仅保留在单独分支上 |
| `645d96b4` | FIX | OWN | 当前状态屏幕读取会**校准级别**，而不是启动一次睡眠 |
| `60681f27` | FIX | OWN | 恢复不会结束一个睡眠片段（sleep episode），所以不得消耗它的边界 |
| `e20875359` | FEATURE | **SUPERSEDED** | 把睡眠边沿门禁在 `IsDevicePaused()` 上 |
| `e661258a6` | **REVERT** | **ARCHIVE-ONLY** | 依据设备证据回退 `e20875359`：十分钟锁屏期间**0 个边界**，严格劣于原来的 bug。两者都保留。 |
| `12aa6db064f` | FEATURE | OWN | 第一个**消费者**：乐观 DNS 刷新改成先问权威，而不是唤醒无线电——在 `QUIESCENT`/`DEEP_IDLE`/已关闭状态下被拒绝，在 `WAKING` 频段经 `Allow().NetworkProbe` 放行。测试在 `dns/optimistic_refresh_power_test.go` |
| `dbf8de8b2a5` | FEATURE | OWN | 第二个消费者：仪表盘下载在 `WaitProviderRefresh` 上**等着**，不在整个睡眠期间去抓取，第一轮还刻意设计成不延迟。测试在 `service/api/dashboard_power_test.go` |
| `ff897d35772` | FIX | OWN | DNS governor 在构造期解析，不在 `Start` 里，这样刷新 goroutine 读到的字段就是不可变的，不依赖顺序。见 `reports/core/HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` |

**最终行为：** 级别/边沿/快照是三件不同的事；屏幕点亮只发布一个恢复边沿和复用 epoch；
**只有解锁才释放级别**；在 5 s 与 15 s 处有三个复用频段（bands）；
裁剪路径从不重置网络，释放路径会。**两个定时的出站抓取现在都服从权威**
（DNS 乐观刷新和仪表盘下载）；WireGuard 恢复轮询刻意保持不加门禁，
原因记在 `power-mobile-lifecycle.md` §6 和
`reports/core/HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md` §6。

**设备证据（此处记录的唯一一次物理设备运行）：** iPhone 17 Pro Max、iOS 26.4.2——
修复前 28 m 24 s → 12 个边界 → 28.29 s；修复后 13 m 13 s → 1 个边界 → 13 m 13.492448 s。

---

## 9. 构建、裁剪与发布（Build, pruning and release）→ `build-pruning-release.md`, `ci-release.md`

| SHA | 类型 | 来源 | 它做了什么 |
|---|---|---|---|
| `f857f0814`, `d535f106f` | BUILD | UPSTREAM-ABSORBED | 移除 `with_gvisor` |
| `0e0063530` | REFACTOR | OWN | 移除 `with_clash_api` 和**整个 LXD 守护进程**（−26,481 行，最大的改动事件，存续约 25 小时） |
| `76cd68eaf` | FIX | OWN | 恢复 Clash API，并声称与上游逐字节相同 |
| `379b21161` | BUILD | OWN | **新增 `with_xhttp`——唯一由 fork 发明的 profile 条目** |
| `b0e93f894` | BUILD | OWN | 从全部三个 profile 文件中移除 `tfogo_checklinkname0`（该标签现在随标志一起传递） |
| `3bab501db`, `dcd8b10cd`, `4c39a835b`, `b175f04dd`, `523db47e4` | REFACTOR | OWN | 整个**产品专用注册表 + 能力允许列表**架构退役 |
| `108439963`, `2937cb71e`, `6c5fa3a20` | CI | OWN | 检测上游假设变化的门禁（tripwire） |
| `e63ce659` | CI | OWN | 失败即拒绝（fail-closed）的发布验收门禁 |
| `9aef595e` | CI | OWN | 每次 push 都运行该验收门禁的自检 |
| `d660c979` | FIX | OWN | 拿发布门禁跟各工作流声明的名字比对 |
| `a267e99e0` | RELEASE | OWN | v0.1.6：包含 Windows 与 Android 产物——**却没有扩展发布树允许列表** |
| `27d8bb599`, `12ee55eef` | REPOSITORY-HYGIENE | OWN | 纯粹为了清掉 GitHub 那个过时的“落后于 upstream”状态而做的祖先记录合并。**无树变更**（`12ee55eef` 记录 `PRE_SYNC_TREE == post-merge tree`）。 |
| `7f31bfd5e` | RELEASE | OWN | 把 `clients/apple` gitlink 从 `d0524912` 推进到 `5cab4ec5`，**别无其他**——`1 file changed, 1 insertion(+)`。这是 pin 而不是代码：没有任何 workflow、refs 文件或构建脚本跟着动。 |
| `ece0fb9d2` | FIX + CI | OWN | 两处为 `release-apple.sh testflight` 解除阻塞。refs 守卫测的是调用方的**环境**，不是 refs **文件**，于是 `resolve -> export -> assert-shared-source` 把 `resolve` 刚打印出来的值拒了；另外 `VERIFIED_APPLE_SHA` 比 gitlink 落后一个 pin 代次，ABI 门禁的第 5 层因此失败。该提交从 `24d55d64b` cherry-pick 而来，常量按*当前* gitlink 重新推导，不是按原提交所指的那个。 |

**最终行为：** 上游的注册表与 profile、一个 fork 发明的标签、版本戳与
Apple pin 元数据；发布流程下载并重新验证，而不重新构建。

**历史缺陷，已在 v0.1.9 评审时修复：** 发布树允许列表遗漏了额外的生产者文件。
当前的允许列表和发布工作流必须一起检查；v0.1.31 的发布运行报告验证成功。
该修订和证据局限见 `ci-release.md` §5。

**被取代：** 基于环境的 refs 守卫（被 `ece0fb9d2` 取代）。它就是未来变更最容易
重复出现的问题：在*环境*中检查被 source 的文件所定义的变量，看起来等同于读取该文件，
其实并不等价——`resolve` 和 `release-apple.sh` 合法地带着同一个名字。
原始 cherry-pick 引用的 `24d55d64b` 不在当前父仓库的对象数据库里；
依赖它之前，请先查它原本的分支或补丁。同样被取代的还有两个 Apple 客户端
`VERIFIED_APPLE_SHA` 值：`90a0e9ae` 与 `d0524912`。每个值对它自己的 gitlink 都是对的，
而 pin 一动它就错——
这正是该常量固有的失败模式。

---

## 10. 被取代的工作、回退与死胡同——陷阱素材（Superseded work, reverts and dead ends — the trap material）

**在 1,834 个提交中只有三次显式回退：**

| 回退 | 被回退者 | 原因 |
|---|---|---|
| `e661258a6` | `e20875359` | **设备证据。** 把睡眠边沿门禁在 `IsDevicePaused()` 上，在十分钟锁屏期间产生了 0 个复用边界——严格劣于原来的 bug。那一轮修不了，因为锚点与每次睡眠的锁存器是**同一个字段**。 |
| `4b75589b1` | `f1ea985d2` + `e92dff18b` | *“The defect they fixed does not exist.”* 该修复引入的可变生命周期暴露了**连接路径上一个未加锁的读取者**，而作者自己的注释是假的。 |
| `eb3b09033` | `5096ac91a` | nil-`SO_MARK` 的 panic 不可达（构建标签互为精确补集），而该竞态只在人为的并发 `Register` 循环下被展示过。 |

`ARCHIVE-ONLY`：`e20875359`, `f1ea985d2`, `e92dff18b`, `5096ac91a`。

**移除后又恢复：** Clash API；MASQUE 客户端传输；Apple/Android/desktop 客户端线；
桌面安装器（`43f4048ac` → `7a46125de` “recover verbatim”）。

**永久移除：** `lxd/`；产品注册表与能力允许列表；`-connect-authority`；
`server_limits.idle_timeout`；MASQUE PREF64 清除路径；一条 MASQUE 的死选择规则；
DNS 流式收集器；一个误留的 21 MB `masque.test` 二进制。

**被取代的成对提交：** `3c6e0aad3` → `fe5789539`（那个截断）；`969909bc1` → `b656b062f`（二次方重叠检查）；
`b51d3cbe5`（“set version 0.1.15”）→ `e70f3d2c8`（“correct version to v0.1.5”）；
`splice_diagnostics` 的 `Successes`/`Ratio` 重命名，在合并整理时被回退，
现在由源码级测试守护。

**曾反复修正的实现：** 路径模型经历 5 轮，其中两轮引入误拒；静态校验经历 5 轮；电源 governor 经历 7 步，最终包含一次回退；Naive 填充路径也曾由一次修复引入缺陷。查看这些提交时，应结合后续更正理解最终行为。

**不可能失败的测试**——整整一族，每一个后来都被修复：`48dd4469e`, `e5b0897f4`, `12c0a9e60`,
`3c3dd135b`, `f8b0af5f6`, `10f9e17d2`, `eac9cc83d`, `b5c0009d1`, `95159084c`, `666470cbe`,
`4cdb250b0`, `c2d93396e`, `da14572a7`, `b5c39a11e`, `675d72bc5`, `6140afcc5`。
最有教育意义的一个：一个 TUN 测试**直接**调用了 `inbound.Close()`——产品从不这样做——
所以产品泄漏时它照样绿着。

**范围震荡：** `5ce3c4d67`（“refactor!: narrow the fork to VPS-only”）移除了 iOS/客户端线
以及整个 MASQUE 客户端传输（−13,122 行，+270 行）。**这些现在全都回来了。** 这次收窄
是被下一次上游同步撤销的，不是被某个决策撤销的——`transport/masque/server.go` 由一个
上游提交加入，一小时后被删除，又被下一个上游提交重新加入，然后被吸收。**未来读到那条提交
信息的 AI 会相信曾做出过一次刻意的范围决策。事实并非如此。**

---

## 11. 接下来去哪里看（Where to look next）

| 你想要 | 去哪里 |
|---|---|
| 发布身份与来源检验 | `BASELINE.md` |
| 哪个模块负责你的任务 | `modules/README.md` |
| 某条规则是不变量还是约定 | `INVARIANTS.md` |
| 如何复检某项需求 | `ACCEPTANCE.md` |
| 代码为何采用当前设计 | 该模块的**来源（Provenance）**表 |
| 你即将做出的“合理但错误”的修改 | 该模块的 **AI 注意事项（AI cautions）** |
