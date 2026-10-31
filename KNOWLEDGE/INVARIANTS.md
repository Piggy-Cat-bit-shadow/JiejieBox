# 不变量（Invariants）

## 当前跨模块边界（Current cross-module boundaries, 2026-10-29）

* 端点本地的流处理必须保留系统模式的 `ActionAccept` 边界，以及
  no-routed-port、unsupported-protocol 与 missing-family 回退；非系统流的目的地址
  使用匹配族的已注册 TUN 地址并保留其端口。这是对当前实现的
  描述；要改变它，须有意识地配合端点与栈测试。
* 冻结的发布必须保持根/测试替换项与已记录的 client gitlink 与
  lock 一致。不要静默更新受控的 Cronet 原生归档，也不要更新父树中嵌套的
  dashboard。Apple framework 的来源要对照父 SHA 与版本进行校验。被跳过的
  client 扫描、未运行的 gate 或被取消的 CI 运行都不算成功。

三份列表，按强度递减排列。**把规则放进错误的列表，正是本文件要防止的失败**：
一条被过度提升的约定会挡住未来一次正当的变更，而一条被提升不足的
正确性规则则会放行它。

一条规则只有在属于以下之一时才属于**硬性不变量**：协议/格式正确性、缓冲区
所有权正确性、ABI 或客户端契约、平台硬约束、发布产物
完整性、针对**已确认**的并发/生命周期缺陷的防护，或明确的上游/fork
所有权边界。其他一切只是约定、策略或实现细节。

---

## A. 硬性不变量（Hard invariants）

每一行包含：规则本身、它为何必须成立、违反后会发生什么、证明它的提交或测试，以及如何
复检。

### A1 — 作用域持有的清理逆序排空；迟到的清理在注册它的 goroutine 上运行（Scope-owned cleanups drain in reverse; late cleanup runs on the registering goroutine）

`Scope.Close` 会排空它注册的函数，包括由 `Scope.Start` 创建的子作用域；
在 close 返回之后注册的清理会**在调用方的 goroutine 上同步运行**；`Close`
**不会**等待尚在进行中的 `Start`。清理函数发生 panic 会中断排空，并可能让
随后一次直接的 Scope close 处于等待状态；`Box.Close` 会 recover 并记录一个错误，而不是完成完整清理。

* **原因：** 每个由作用域持有的资源都需要一个注册给其真实所有者的清理。其他
  所有者也可能关闭资源；仅缺少一次直接的 `scope.Add` 并不能证明存在泄漏。把
  迟到的清理改为异步，会改变那个迟到资源被释放的时机。
* **违反后果：** 一个已被拆解的 `*Inbound` 仍保留着 observer；一次监听在排空之后才绑定。
* **证明：** `adapter/lifecycle.go:112-138`、`:218-283`；`adapter/lifecycle_close_boundary_test.go`；
  提交 `53a3f97e8`、`f2c728d10`、`e3349044a` —— **这三个 SHA 在本克隆中无法解析**
  （重写前的历史；它们只出现在 `docs/fork/lifecycle-scope-*.md` 中）。源码锚点与
  测试才是当前的证明。
* **复检：** `go test -count=1 ./adapter/...`
* **类型：** 生命周期缺陷防护。**归属：** `modules/core-lifecycle-runtime.md`

### A2 — “一次只做一次恢复”是一种租约身份，而不是布尔值（"One recovery at a time" is a lease identity, not a boolean）

`Registration.lease` 是一个 `*RebindLease`；对已被取代的租约，`Complete()` 是空操作，但**总是
会取消**；当租约不再是当前所有者时，`Expired()` 为 true。

* **原因：** 一次已被取代的恢复不得能够完成、释放或取消一个更新的恢复。
* **违反后果：** 一次旧的重新绑定覆盖了新的绑定，并取消了活动租约的 context。
* **证明：** `common/runtimecoord/coordinator.go:191-238`、`:282-310`；`coordinator_test.go`、
  `lease_test.go`。
* **复检：** `go test -count=1 ./common/runtimecoord/...`
* **类型：** 并发缺陷防护。**归属：** `modules/core-lifecycle-runtime.md`

### A3 — 预览不得消耗一个选择槽位（A preview must not consume a selection slot）

`Selector.Selected` 返回已加载的原子值，并**忽略 network 参数**；`URLTest.Selected`
读取历史且不回写；`LoadBalance.Selected` **仅**当 `commit` 为 true 时
才推进其轮询游标。

* **原因：** 一次移动了游标的诊断或校验遍历，会把下一次流交给一个不同于它刚刚
  报告过的成员。
* **违反后果：** 报告的出口不是实际拨号的成员；两条流消耗了三个槽位。
* **证明：** `protocol/group/loadbalance.go:259-261`、`:337-341`；`route/physicalpath_route_test.go`
  （`TestPhysicalPathTakesNoSelectionPreviewThroughRealGroups`、
  `TestTwoFlowsAdvanceTheCursorExactlyTwiceWithAPreviewInBetween`），并由一个**变异（mutation）**验证——它把
  `commit=true` 设上，并观察到 `CommittedSelections()` 发生变化。
* **复检：** `go test -count=1 ./route/... ./common/physicalpath/...`
* **类型：** 已确认行为防护。**归属：** `modules/path-selection-validation.md`

### A4 — 组是控制平面对象，永远不是物理跳（A group is a control-plane object, never a physical hop）

`Hop.IsGroup` 从不被设置；组只对 `ControlPath` 有贡献。`Hops` 是报文顺序
（离设备最近者在前）；`ControlPath` 是下行顺序，且**刻意不反转**。

* **原因：** 把组报告为跳、或把跳列表反向报告的诊断，描述的是流量并不经过的
  路径——这比没有诊断更糟。
* **违反后果：** 状态视图把健康的一跳指认为损坏的那一跳。
* **证明：** `common/physicalpath/physicalpath.go:261-264`、`:564-600`；`leaves.go:305-310`；
  `physicalpath_test.go:462-482`；`dependency_on_group_test.go:148-155`；
  `route/physicalpath_route_test.go`。方向修正为 `524ea41e`；`ControlPath` 修正为
  `d46717dbd`。**方向修正落地时有九个测试变红。**
* **复检：** `go test -count=1 ./common/physicalpath/...`
* **类型：** 正确性。**归属：** `modules/path-selection-validation.md`

### A5 — 启动期校验不得拨号、解析 DNS 或消耗选择（Start-time validation must not dial, resolve DNS or consume selection）

`validatePhysicalPaths` 不拨号、不解析任何东西、不改变任何选择，也不消耗任何轮换。
对一次真实启动执行 dry run 会断言 committed == 0 **且** previewed == 0。

* **原因：** dry run 必须能在离线状态下判定，既不改变实际选择也不启动流量。
  它仍可能产生下文所述的有限日志与记忆化（memoisation）效果。
* **违反后果：** 启动一个 box 变成有副作用的操作；一次校验遍历移动了
  selector。
* **可从对象图证明：** `Resolver` 只持有一个 registry 查找、一个 `Snapshot` 和一个 network
  名称——没有 dialer、没有 logger、没有 context、没有 clock。
* **诚实的范围：** 确实存在并允许有两个副作用——一条弃用日志
  （`common/dialer/dialer.go:187`）和一次记忆化的 detour 标签→对象解析
  （`common/dialer/capability.go:100` → `detour.go:63`）。两者都不拨号。`Box.Status` 则另有
  目的地取一次实时预览。
* **证明：** `adapter/outbound/manager.go:235-237`；`manager_physicalpath_test.go:357-373`；
  `TestBuildNeverCallsADialingMethod`；`TestDryRunLeavesTakeNoSelectionPreview`。
* **复检：** `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...`
* **类型：** 正确性。**归属：** `modules/path-selection-validation.md`

### A6 — 缓冲区所有权：借用的视图、未池化的工作副本、原子块（Buffer ownership: borrowed views, unpooled working copies, atomic chunks）

六条规则合一：

1. `payloadReader` 游标不得比创建它的 `PeekStream` 调用活得更久（`sniff.go:88-91`）；
2. `takePacketBytes` 返回的视图仅在数据报存活期间有效（`quic.go:36-54`）；
3. QUIC 的工作副本是每次调用新建的 `make`，刻意不池化，而被保留的片段
   会以**它**为别名，绝不是调用方的数据报（`quic.go:183-184`、`:305-306`、`:472-476`）；
4. 头部保护**原地**写 4 个字节，因此无法覆盖到它们的 `Length` 会被拒绝
   （`quic.go:125-131`）；
5. MASQUE 批量发送是**按块全有或全无**；被接受的前缀属于传输层，
   不得重放（`transport/masque/session.go`、`writeBatchOwned`）；
6. 在拥有所有权的数据报路径上，所有权**在成功时转移**，释放操作被跳过
   （`session.go:484-489`）。

这些是所有权规则，而不是恰好一次投递：被固定版本的 HTTP/3 批量拷贝回退可能
在后续错误把所有 payload 的所有权交还调用方之前，先排队一个前缀。

* **原因：** 每条规则之所以存在，都是因为违反它曾产生过真实缺陷。
* **违反后果：** 被保留的游标读到已被回收的池内存；池化的工作副本会让
  片段比其所有者活得更久；在拥有所有权的路径上执行释放是**双重释放**。
* **证明：** 地址范围测试 `quic_ownership_test.go:314-350`；数据报投毒测试
  `:206-281` 与 `quic_boundary_test.go:427-456`；`cap` 上界
  `sniff_bounded_copy_internal_test.go:120-156`；`route/udp_batch_capability_test.go`。
* **复检：** `go test -count=1 ./common/sniff/...` 以及
  `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...`
* **类型：** 缓冲区所有权正确性。**归属：** `modules/buffer-ownership.md`

### A7 — Context-ID 替换会释放原对象，并且必须拒绝其所在的块（A context-ID substitution releases the original and must refuse its chunk）

`PrependContextID` 可能分配一个替代对象并**释放原对象**，因此一次替换会**在尝试之前**
就拒绝受影响的块，并原地更新调用方的切片。此前
已被接受的块保持已转移状态；只有尚未转移的剩余部分可以回退。

* **原因：** 这是一个有记录的真实缺陷。被拒绝的批次保留了 context ID，回退逻辑又前置了一个
  新的，`packetAddresses` 读到了错误的版本半字节，于是**每一个报文都被静默丢弃
  ——没有错误，也没有 PTB**。
* **违反后果：** 隧道上出现静默的全量丢包。
* **证明：** `transport/masque/session.go:551-579`，缺陷记录于 `:539-549`。
* **复检：** 在相应 tag 下运行 MASQUE 测试套件。
* **类型：** 缓冲区所有权正确性 + 已确认缺陷。**归属：** `modules/masque-connectip.md`

### A8 — 空闲退役断言的是 `IdleConnectionReleaser`，绝不是捆绑的 `IdleConnectionKeeper`（Idle retirement asserts `IdleConnectionReleaser`, never the bundled `IdleConnectionKeeper`）

* **原因：** 断言捆绑接口**在每一次遍历时都在运行期静默失败**，因此 HTTP
  client 连接池历经四轮评审都未能被触及。刻意不要求完整的 keeper：
  某些传输没有安全的“no new stream”状态。
* **违反后果：** 受影响的池可能被漏掉，而其他符合条件的资源仍被裁剪。
* **证明：** `route/reference.go:440-494`，理由见 `:479-484`；提交 `93823c3d`、
  `8735b1d`；`route/reference_coverage_stress_test.go`。
* **复检：** `go test -count=1 ./route/...`
* **类型：** 已确认缺陷防护。**归属：** `modules/core-lifecycle-runtime.md`

### A9 — 内存裁剪仅限空闲；`ReleaseMemory` 才是可能重置网络的路径（Memory trim is idle-only; `ReleaseMemory` is the path that may reset the network）

裁剪不得重置网络、拨号、唤醒被挂起的工作或终止活动流。
`ReuseSuspect.RetireSuspect()` 是更强的**睡眠/复用边界**动作，它在
`CloseIdleConnections` **之前**运行。

* **原因：** 一次会重连的裁剪，把对内存压力的应对变成了一个流量事件。
* **违反后果：** 空闲期裁剪新建一个队列并耗费射频时间；裁剪变成
  重连触发器——这正是内存路径绝不能有的形态。
* **诚实的范围：** `service/oomkiller/timer.go` 的危急回收可以调用 `ReleaseMemory`，后者会重置
  网络，这是有意为之（`route/conn_reclaim.go`）。该服务是否存在、危急处理
  是否仅上报，取决于宿主配置与选项；单靠 `json:"-"` 两者都不成立。
  **裁剪（Trim）≠ 释放（Release）。**
* **证明：** `route/reference.go:154-197`、`:394-411`、`:496-511`、`:547-556`；
  `route/reset_entrypoints_test.go:95`；`route/reset_entry_matrix_test.go:103`。
* **复检：** `go test -count=1 ./route/... ./common/power/...`
* **类型：** 生命周期防护。**归属：** `modules/power-mobile-lifecycle.md`、`modules/core-lifecycle-runtime.md`

### A10 — 平台快照只表达级别；当前状态的读取不得变成一个边沿事件（Platform snapshots are level-only; a current-state read must not become an edge）

`reconcileScreenState` / `reconcileLockState` 协调的是一个级别。显示器**打开**并不是
解锁。`resumed()` 不发布任何东西。

* **原因：** 在真实设备上测得——一次 28 m 24 s 的睡眠期间出现 13 次 resume 回调，产生了 13 个
  锚点和一个 28.29 s 的边界，把这次测量毁掉了。而且锚点与按事件（per-episode）的
  边界锁存器是**同一个字段**，于是用“已暂停”状态去门控睡眠边沿，在十分钟锁屏期间产生了**零个**
  边界——比原来的缺陷更糟——因此被回退。
* **违反后果：** 睡眠时长被错误上报；复用边界被“看一眼屏幕”（screen glances）
  这样的操作消耗掉。
* **证明：** `box_lifecycle.go:141`、`:173-199`、`:228-252`；提交 `645d96b4`、`60681f27`、`0ff90482`；
  `box_lifecycle_ordering_test.go`；`e20875359` → `e661258a6`（回退，两者均已归档）；
  `docs/fork/p1c-iphone-device-closure.md:289-291`。
* **复检：** `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run 'TestTheReuseVerdict|TestTheSleepEdge' .`
* **类型：** 已确认缺陷防护 + 平台约束。**归属：** `modules/power-mobile-lifecycle.md`

### A11 — libbox ABI 接口面与 Apple 单一来源固定（The libbox ABI surface and the Apple single-source pin）

`experimental/libbox.DeprecatedNote.Message()` 返回 `*StringBox`，**不是** `string`，因为裸的
`string` 会把一个 Go 指针放进 gomobile 的打包结果帧，从而产生
`fatal error: bulkBarrierPreWrite: unaligned arguments`。`adapter.NewAdapterWithDialerOptions` 多带
一个额外的可变参数。Apple 工作流为一次构建解析出唯一的 client 源码 SHA，并在各作业之间
共享它；默认情况下它来自 client 仓库所配置的分支，而不是父仓库的
gitlink。显式覆盖是受支持的，且对于源码选择而言 gitlink 相等是可选的。

* **原因：** 前者是无可恢复的崩溃；后者正是阻止 iOS 与 macOS 从
  两个不同源码构建出来的机制。
* **违反后果：** 针对上游 libbox 构建的 client 会崩溃；两个 Apple 产品从
  两个不同的修订版本发布。
* **固定这一半已接线；client 扫描不在推送门禁上。** `check-libbox-abi.sh
  --pin-only` 在 `verify.yml` 中运行，当某个 client gitlink 与其 `VERIFIED_*_SHA` 不一致时失败。
  这一半捕获的是单独落地的 gitlink 移动，而它正是在这种情况发生过之后才加入的：
  Apple gitlink 因 v0.1.28 的路径监视金丝雀测试从 `6e559a4` 移动到 `3aedd741`，
  而 `VERIFIED_APPLE_SHA` 没有跟着更新，因此完整门禁即使干净地跑一遍也会在第 5 层失败，
  尽管根本没有任何东西去运行它。完整门禁（第 1-4 层，含 client 调用点扫描）需要 cgo、
  Darwin 构建配置和一个 client 检出，因此它声明在 `client-apple.yml` 中，针对其
  已检出的源码运行。gitlink/常量匹配并不能证明独立解析出的
  构建源码是同一个 SHA。`client-apple.yml` 仍不在发布验收必需集合中（A12）；那是
  剩余的落地缺口，而不是缺少检查。gitlink 缺失的情形是**失败**，而不是
  跳过：gitlink 存在于超级项目树中，所以一个被删除的 gitlink 不能解释为“一个
  尚未初始化的 client 检出”（"a client checkout that was not initialised"）。`test-check-libbox-abi.sh` 同时断言正确配对与
  被删除的 Apple gitlink 两种情况；`verify.yml` 会运行它。
* **证明：** `docs/fork/libbox-abi-contract.tsv`；`release/apple-client-refs.env`；
  `check-apple-source-selection.sh:124-136`、`:209-210`；`check-libbox-abi.sh`（`--pin-only`，第
  1-5 层）；`test-check-libbox-abi.sh`；`verify.yml` 的步骤 "client gitlinks match the ABI contract pins"
  与 "ABI pin gate fails closed on a missing gitlink"；`client-apple.yml` 步骤 "libbox ABI gate
  against the client call sites"；`apple-client-source.sh:88-105`；
  `docs/fork/apple-artifact-verification.md:609-616`。
* **复检：** `./scripts/ci/check-libbox-abi.sh --pin-only` 可在任何装有 `git` 的宿主上运行；完整门禁是
  在装有 cgo 的 macOS 宿主上运行 `./scripts/ci/check-libbox-abi.sh --client-root <apple-checkout>`。
* **类型：** ABI/client 契约。**归属：** `modules/libbox-platform-contract.md`

### A12 — 发布按平台重新校验字节；绝不重新构建（Release re-verifies bytes per platform; it never rebuilds）

`release.yml` 为每个产品工作流**针对确切的 SHA** 选取一次成功运行，从
这些运行中下载，并按平台把二进制 + `.sha256` + `BUILD-INFO` 对照真实的 `sha256sum -c` 进行校验，
平台信息从文件自身的头部读取，并要求记录的提交与之匹配。

* **原因：** 元数据来源不等于二进制来源。
* **违反后果：** 一次发布附上了没有任何东西构建或校验过的字节。
* **诚实的范围：** 它覆盖的是计划步骤所选出的内容；应检查每个生产者的触发条件以及
  被选中的运行，而不是假定每个生产者都只支持手动派发。另一个独立的
  `verify-release-acceptance.sh` 不包含 `client-apple.yml`，也不是由
  发布路径调用的。这条关于发布路径的具体陈述是
  `VERIFIED_SOURCE`；`modules/ci-release.md` 中独立链接的 CI 运行只适用于它们各自的 SHA，
  单凭它们本身并不能验证一个发布产物。
* **证明：** `release.yml:144-204`；`scripts/ci/release-artifacts.sh`；
  `scripts/ci/verify-release-acceptance.sh:99-102`、`:151`。
* **复检：** 阅读该工作流；真实运行需要 `gh` 和网络。
* **类型：** 发布产物完整性。**归属：** `modules/ci-release.md`

### A13 — 上传调度器在未设置时不起作用，拨号调速器在其窗口之外不起作用（The upload scheduler is inert when unset, and the dial governor is inert outside its window）

缺失/零值/空的 `traffic_scheduler` ⇒ 门禁观察每一个受管理的字节、全部放行，并且
**不启动任何队列、任何锁和任何定时器**。拨号调速器的零值不作用于任何对象。

* **原因：** 未设置的整形策略不应增加整形队列或速率限制；一个永久的拨号
  限制会在预期的恢复窗口之外强加代价。这并不是对整份配置
  兼容性、或与上游端到端行为一致的证明。
* **违反后果：** 一份什么都没要求的配置得到了整形；在正常网络上每一次拨号
  都在为并不存在的突发付出代价。
* **证明：** `common/trafficsched/doc.go:93-97`；
  `common/trafficsched/scheduler.go`（`Options.RateSource`、`SetRateSource`）；
  `route/dial_governor.go:22-24`、`:55-56`；`option/traffic_scheduler_test.go:31`。
* **复检：** `go test -count=1 ./common/trafficsched/... ./route/...`
* **类型：** 兼容性/ABI 相邻（配置可加载性）。**归属：** `modules/data-path-optimization.md`

### A14 — 每个 `DialerOptions` 字段在能启用绕过之前都必须先被分类（Every `DialerOptions` field must be classified before it can enable a bypass）

某条流上设置了未分类的选项会产生 `BlockerUnclassified`，从而拒绝该绕过；一个反射
测试会在每个字段都被分类之前失败，而且该表格不得列出已不存在的字段。

* **原因：** 新加入的拨号选项绝不能默认就让绕过变成**安全的**。
* **违反后果：** 快路径静默丢弃了操作者要求的某项 socket 语义。
* **证明：** `common/dialer/profile.go`；`TestEveryDialerOptionIsClassified`；
  `TestFamilyStrategyAppliesToALiteralAddress`；`TestZeroProfileRefuses`。
* **复检：** `go test -count=1 ./common/dialer/...`
* **类型：** 正确性防护。**归属：** `modules/data-path-optimization.md`

### A15 — 上游/fork 所有权边界：固定替换集是一条含四个成员的路径前缀规则（Upstream/fork ownership boundary: the pinned-replace set is a prefix rule with four members）

`REQUIRED_FORK_REPLACES` 列出了 `sing`、`sing-tun`、`quic-go` 和 `sing-quic`；`cronet-go` 发布的是一棵
**树**状的模块集合，所以要求是一条**路径前缀**，而不是一个列表。

* **原因：** 一条 `replace` 只重写**一个**模块路径。只固定根模块会让嵌套模块
  解析到上游——这是一种没有任何东西构建或测试过的配对，在 linux/amd64 上甚至无法链接。
  `sing-quic` 是承载硬性报文大小上限的透传层，因此一个解析到上游的嵌套模块
  **会让已被证明的 MTU 上限静默消失**。
* **违反后果：** fork 自己的绑定被配上上游的原生归档使用。
* **证明：** `scripts/ci/check-go-module-integrity.sh:105-124`；其自身历史 `:60-66` 记录道，
  `test` 模块曾经静默链接到上游的 `sing-tun`，而 *"the old code could not fail on it, by
  construction"*。
* **复检：** `bash scripts/ci/check-go-module-integrity.sh --require-checked test`
* **类型：** 上游/fork 所有权边界。**归属：** `modules/build-pruning-release.md`

### A16 — 守护运行期可用性问题的 tag 桩必须失败，而不是缺席（A tag stub that guards a runtime availability question must FAIL, not be absent）

`include/quic_stub.go:48` 安装一个**会失败**的 `ConfigureHTTP3ListenerFunc`，而不是让该
字段为 nil，这样 `decideHTTP3Availability` 才能区分“absent”与“present but broken”。

* **原因：** 字段为 nil 时这两种状态无法区分，缺失的可选传输也就
  无法与已损坏的传输区分开——于是致命/非致命的判定没有输入。
* **违反后果：** 要么每个缺失的 HTTP/3 传输都会拖垮 TCP CONNECT，要么一个损坏的传输
  被静默容忍。
* **证明：** `include/quic_stub.go:48`；`protocol/naive/inbound.go:212-236`、`:316-334`。
* **复检：** 分别在带与不带 `with_quic` 的情况下构建，并断言判定结果不同。
* **类型：** 平台/构建约束。**归属：** `modules/naive-http-transport.md`、`modules/build-pruning-release.md`

### A17 — 除非显式配置，嗅探元数据不是目的地址重写（Sniff metadata is not a destination rewrite unless explicitly configured）

被嗅探到的域名会作为 DNS 规划与拨号候选项进入路由；仅靠嗅探不会重写
`Destination`，除非通过显式且已弃用的 `override_destination`。另有一项独立机制：FakeIP
反向映射会保存原始地址，并在 `route/route.go` 中替换为 FQDN。

* **原因：** 依据嗅探到的名字重写目的地址，会改变哪些规则匹配、哪个地址被
  拨号——这是伪装成便利功能的策略变更。
* **另外：** 一个被嗅探到的域名**会使其失去 Direct Offload 快路径的资格**
  （`BypassRefusedSniffedDomain`）。
* **违反后果：** 对于一条只是看起来像某物的流，规则语义发生了变化。
* **证明：** `route/route.go:1437-1444`、`:541-546`、`:1255-1260`、`:1405-1410`；`route/route.go:791-796`；
  `route/bypass_verdict.go:29`、`:54`。
* **复检：** `go test -count=1 ./route/... ./common/sniff/...`
* **类型：** 正确性。**归属：** `modules/sniff.md`

### A18 — 分片的 QUIC 仅在 `ErrNeedMoreData` 时继续，且有界、缓冲区会被释放（Fragmented QUIC continues only on `ErrNeedMoreData`, bounded, with the buffer released）

`maxSniffRounds = 64` 限定新读取的续读轮次；达到上限时，刚取出的缓冲区
会被释放，该流按不可嗅探处理。解析器给出的非“需要更多数据”判定会被记录为
`metadata.SniffError`；取消以及非超时的报文读取错误仍可能是致命的。该上限
并不限定此前对 `inputPacketBuffers` 的重放。

* **原因：** 修复前的行为已被测量——1,000 个精心构造的数据报保留了 1,000 个缓冲区
  （存活 +16.7 MB），且每轮的 QUIC 缓冲区从 33 KB 增长到 1.06 MB。
* **违反后果：** 一个恶意对端驱动了无界内存增长与池化缓冲区泄漏。
* **诚实的证据：** 该上限与放弃时释放都**仅为 `VERIFIED_SOURCE`**——树中不存在测试，
  也不存在可复现的测量。
* **证明：** `route/route.go:1211-1217`、`:1340-1347`；`common/sniff/sniff.go:182-199`；提交 `578663e0`。
* **复检：** `go test -count=1 ./common/sniff/...`（**不**覆盖该上限）。
* **类型：** 针对已确认测量的加固。**归属：** `modules/sniff.md`

### A19 — Apple 快照协调仅表达级别**（见 A10）**（Apple snapshot reconciliation is level-only **(see A10)**）

在原文中单独列出，是因为它同时也约束了*客户端*：客户端不得发送它没有
观察到的边沿事件，核心也不得从一个级别合成出一个边沿事件。

* **类型：** ABI/client 契约。**归属：** `modules/power-mobile-lifecycle.md`、`modules/libbox-platform-contract.md`

### A20 — 发布验收是失败即拒绝的（Release acceptance is fail-closed）

失败、被取消、超时或被跳过的产品作业不得被视为成功。一个成功的
作业或步骤同样不得被报告为证明了它明确跳过的那一层。

* **原因：** `scripts/ci/apple-ci-job-gate.py` 的 docstring 是对这一类问题最清晰的陈述：一次带
  `build_ios: false` 的派发**会在某个平台从未被构建的情况下得出成功结论**，而被跳过的
  作业不算失败。`check-libbox-abi.sh` 打印 `PARTIAL` 并以退出码 0 结束，正是出于这个原因。
* **发布路径的权威是 `release-artifacts.sh`，由 `release.yml` 调用。** 它选取
  每个目标工作流最新的运行，遇到已完成但非成功或被取消的运行即失败，要求每个
  产品的产物和已记录的摘要，并把 `client-apple.yml` 纳入
  `RELEASE_EXTRA_PLATFORMS=true` 之下。这是一个作业/产物层面的条件，而不是每个
  内部检查都运行过的证明：v0.1.31 的 Apple 工作流在 Swift ABI 探针被标记为 SKIP、
  ABI 检查器报告 PARTIAL 的情况下仍然成功。关于那次确切的运行，见 `modules/libbox-platform-contract.md`。
* **诚实的缺口：** 另一个独立的严格交接检查器 `verify-release-acceptance.sh` 并未被
  发布路径调用——只有它的自检在 `release-contract-verify.yml` 中运行。其默认的必需
  集合也偏窄（`verify.yml`、`interop-xray.yml`、`server-linux-amd64.yml`）。这是两套检查之间的
  工具/归属缺口，而**不是**发布路径未受保护的证据；
  评判一次发布所依据的，是发布路径自身的门禁。
* **证明：** `release.yml`；`release-artifacts.sh`（`TARGET_WORKFLOWS`、`RELEASE_EXTRA_PLATFORMS`）；
  `verify-release-acceptance.sh`；`release-contract-verify.yml` 自检；`check-libbox-abi.sh
  --pin-only` + `client-apple.yml`（用于 ABI 固定）。
* **类型：** 发布产物完整性。**归属：** `modules/ci-release.md`

### A21 — 只有当响应证明该名字不存在时，NXDOMAIN 才放宽到 QNAME（NXDOMAIN widens to the QNAME only when the response proves the name does not exist）

只有一个单问题、空答案的 `NXDOMAIN`，在携带非零 `min(SOA TTL, SOA MINIMUM)` 时才可以
进入 `nxdomainCache`。NODATA、`SERVFAIL`/`REFUSED`/`FORMERR`、超时、传输错误以及
以 CNAME 终止的答案永远不会放宽。命中项会针对**当前**问题重建，并报告
剩余 TTL；键保持传输、client 子网与环境的作用域隔离，且这些判定
永不持久化。`rewrite_ttl` 与零头部 TTL 之间的交互是一个**未决的源码问题**
（见 `modules/dns-demand.md`），不属于本不变量证明的一部分。

* **原因：** RFC 2308 让 `NXDOMAIN` 成为关于该名字的陈述。放宽任何更弱的响应，会把一次
  短暂的上游故障或某一类型特有的缺失变成整个名字范围的故障，也会把 CNAME 目标的
  缺失变成别名本身的缺失。
* **违反后果：** 对已存在名字的 A/AAAA/HTTPS 查询在本地失败；一次解析器故障被
  放大到每个 QTYPE；关于一个问题的一个答案被当作另一个问题的答案重放。
* **证明：** `dns/client_negative.go:139`（`storeNXDomain` 守卫）、`:205`（`loadNXDomain`）、`:254`
  （`nxdomainResponseFor`）；`dns/client_negative_test.go`（23 个 `TestNXDomain_*` 测试）。
* **复检：** `go test -count=1 -run TestNXDomain ./dns/` —— 在 2026-10-31 的评审中**未运行**
  （没有 Go 工具链）：`BLOCKED_BY_ENVIRONMENT`，而不是通过。
* **类型：** 协议正确性。**归属：** `modules/dns-demand.md`

---

## B. 当前约定（Current conventions）

成熟实践，而非不可更改的架构。偏离时须说明理由。

* **让测试与其变更同行**，并优先使用行为断言而不是源码文本守卫
  （`docs/UPSTREAM-SYNC.md:89-94`）。
* **使用针对具体能力的退役接口**——实现 `IdleConnectionReleaser`，而不是
  声称一个完整的 keeper。
* **把源码/构建来源保存在产物中**（`BUILD-INFO`、`.sha256`）。
* **对照退役遍历及其所有权契约，评审每个新的可复用池。** 那个已命名的
  所有者测试并不能证明不存在第二个 `ReuseSuspect` 实现。
* **防止顺序断言变得空洞**——断言那个频段或路径确实被
  实际执行过，正如 `box_lifecycle_ordering_test.go` 所做的那样。
* **不要用睡眠状态去门控看门狗。** 一个停止观察的看门狗不是省电。
* **可以推迟周期性定时器，但绝不推迟它的首次执行。**

---

## C. 当前优化策略（Current optimization strategies）

本质上是暂定的。每一条背后都有一次测量，若测量结果改变，每一条都可能被重新
审视。

* `ModePaced` 聚合整形是已发布的调度器模式；各排序模式经测量
  与什么都不做无法区分。
* 只有当缓冲区**多于一个**时才走 MASQUE 批量路径。
* `common/bufgeom` 的分配上限只在 `!race` 构建中断言。
* Shadowsocks 稳态读侧的拷贝是**经测量保留（measured keep）**的。
* QUIC 单帧 `ClientFirefox` 启发式保持原样；修复超出了范围。
* L2 与 L3 拷贝路径在回环上无法区分，这是测试装置的性质，而不是缺陷。

---

## D. 实现细节（不是规则）（Implementation details (not rules)）

* `PathNode.RequiredNetworks` **在生产中是只写的**——两个写入者，零个生产读取者。
  校验器读取的是由某个函数参数计算出来的 `HopCheck.Requirement`。该 fork 自己的提交
  `fc4bdd416` 说它 *"should be wired or removed by the file's owner"*。**这是一个 FOLLOW-UP。不要
  把它当作契约引用，也不要基于它造成的双需求模型张力去构建规则。**
* `common/sniff` 的嗅探是尽力而为的；`metadata.SniffError` 只作信息用途。
* `adapter/prestart.go` 只有一行 `package` 声明，是残留物。
* `trafficresearch` 构建标签没有被任何东西引用，并且如果研究控制器
  变为可达，测试就会失败。
* `docs/fork/physicalpath-controlpath-model.md` 关于 `ControlPath` 顺序的内容已过时（见 A4）。
* `dryrun.go:470-493` 是一段与其旁边代码相矛盾的孤立注释；代码本身已有定论。
