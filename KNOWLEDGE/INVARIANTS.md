# 不变量（Invariants）

## 当前跨模块边界（Current cross-module boundaries, 2026-10-29）

* 端点本地流量在 system 模式下保留 `ActionAccept`；没有 routed port、协议不支持或缺少
  对应地址族时，也必须沿用回退行为。非 system 模式需要交给 TUN 时，使用相同地址族的
  已注册 TUN 地址，并保留目的端口。修改这段行为时，应同时调整端点和协议栈测试。
* 发布冻结时，根模块及测试模块的 replace、客户端 gitlink 和 lock 文件必须一致。受控的
  Cronet 原生归档不能静默更新；Dashboard 的嵌套 gitlink 由其所在仓库管理。Apple framework
  的来源需要核对父提交 SHA 和版本。客户端扫描被跳过、门禁未运行或 CI 被取消，都不算通过。

本文件按规则强度分类，避免把一般约定误当成不可改变的限制，也避免漏掉必须遵守的正确性要求。

只有以下要求才列为**硬性不变量**：协议和格式正确性、缓冲区所有权、ABI 与客户端契约、
平台硬性限制、发布产物完整性、已确认并发或生命周期缺陷的防护，以及明确的上游与 fork
所有权边界。其他内容按约定、策略或实现细节记录。

---

## A. 硬性不变量（Hard invariants）

每条都写清：规则本身、它为什么必须成立、违反后会怎样、由哪个提交或测试证明，以及
怎么复检。

### A1 — 作用域按注册逆序清理；关闭后的注册同步执行（Scope-owned cleanups drain in reverse; late cleanup runs on the registering goroutine）

`Scope.Close` 按注册的相反顺序执行清理函数，包括 `Scope.Start` 创建的子作用域。
关闭开始后注册的清理会**在调用方的 goroutine 中同步执行**；`Close` **不会**等待
正在进行的 `Start`。如果清理函数 panic，后续清理会中断，之后直接调用 Scope 的
`Close` 可能一直等待。`Box.Close` 会捕获并记录错误，但不会继续执行中断的清理。

* **原因：** 每个由作用域持有的资源都需要由实际所有者负责清理。其他所有者也可能关闭同一资源，因此仅凭某处没有直接调用 `scope.Add`，不能断言发生了泄漏。将迟到的清理改为异步，还会改变资源释放时机。
* **违反后果：** 已清理的 `*Inbound` 可能仍有 observer，或监听器在作用域排空之后才完成绑定。
* **证明：** `adapter/lifecycle.go:112-138`、`:218-283`；`adapter/lifecycle_close_boundary_test.go`；
  提交 `53a3f97e8`、`f2c728d10`、`e3349044a` —— **这三个 SHA 在本克隆中无法解析**
  （重写前的历史，只出现在 `docs/fork/lifecycle-scope-*.md` 里）。当前的证明是源码锚点
  和测试。
* **复检：** `go test -count=1 ./adapter/...`
* **类型：** 生命周期缺陷防护。**归属：** `modules/core-lifecycle-runtime.md`

### A2 — “一次只做一次恢复”是一种租约身份，而不是布尔值（"One recovery at a time" is a lease identity, not a boolean）

`Registration.lease` 是一个 `*RebindLease`；租约被取代之后，`Complete()` 是空操作，但**总是
会取消**；租约不再是当前所有者时，`Expired()` 为 true。

* **原因：** 一次已被取代的恢复，不得完成、释放或取消一个更新的恢复。
* **违反后果：** 旧的重新绑定覆盖了新的绑定，并取消了活动租约的 context。
* **证明：** `common/runtimecoord/coordinator.go:191-238`、`:282-310`；`coordinator_test.go`、
  `lease_test.go`。
* **复检：** `go test -count=1 ./common/runtimecoord/...`
* **类型：** 并发缺陷防护。**归属：** `modules/core-lifecycle-runtime.md`

### A3 — 预览不得消耗一个选择槽位（A preview must not consume a selection slot）

`Selector.Selected` 返回已加载的原子值，**忽略 network 参数**；`URLTest.Selected`
只读历史，不回写；`LoadBalance.Selected` **仅**在 `commit` 为 true
时推进轮询游标。

* **原因：** 诊断或校验如果推进游标，就会改变后续流的成员选择。
* **违反后果：** 报告的出口与实际拨号成员不一致，或两条流消耗三个轮询槽位。
* **证明：** `protocol/group/loadbalance.go:259-261`、`:337-341`；`route/physicalpath_route_test.go`
  （`TestPhysicalPathTakesNoSelectionPreviewThroughRealGroups`、
  `TestTwoFlowsAdvanceTheCursorExactlyTwiceWithAPreviewInBetween`），并用一个**变异（mutation）**验证过
  —— 把 `commit=true` 设上，就能看到 `CommittedSelections()` 变了。
* **复检：** `go test -count=1 ./route/... ./common/physicalpath/...`
* **类型：** 已确认行为防护。**归属：** `modules/path-selection-validation.md`

### A4 — 组是控制平面对象，永远不是物理跳（A group is a control-plane object, never a physical hop）

`Hop.IsGroup` 从不被设置；组只对 `ControlPath` 有贡献。`Hops` 按报文顺序
（离设备最近的在最前）；`ControlPath` 是下行顺序，**刻意不反转**。

* **原因：** 将组报告为物理跳，或颠倒物理跳的顺序，都会使诊断路径偏离实际流量路径。
* **违反后果：** 状态视图可能将正常的节点误报为故障节点。
* **证明：** `common/physicalpath/physicalpath.go:261-264`、`:564-600`；`leaves.go:305-310`；
  `physicalpath_test.go:462-482`；`dependency_on_group_test.go:148-155`；
  `route/physicalpath_route_test.go`。方向修正为 `524ea41e`；`ControlPath` 修正为
  `d46717dbd`。**方向修正落地时有九个测试变红。**
* **复检：** `go test -count=1 ./common/physicalpath/...`
* **类型：** 正确性。**归属：** `modules/path-selection-validation.md`

### A5 — 启动期校验不得拨号、解析 DNS 或消耗选择（Start-time validation must not dial, resolve DNS or consume selection）

`validatePhysicalPaths` 不拨号、不解析任何东西、不改任何选择，也不消耗任何轮换。
对一次真实启动做 dry run，会断言 committed == 0 **且** previewed == 0。

* **原因：** dry run 必须能在离线状态下出判定，既不改变实际选择，也不启动流量。
  它仍可能留下下文说的有限日志与记忆化（memoisation）效果。
* **违反后果：** 启动一个 box 就成了有副作用的操作；一次校验遍历把
  selector 挪了。
* **可从对象图证明：** `Resolver` 只持有一个 registry 查找、一个 `Snapshot` 和一个 network
  名称——没有 dialer、没有 logger、没有 context、没有 clock。
* **适用范围：** 确实存在、也允许有两个副作用：一条弃用日志
  （`common/dialer/dialer.go:187`）和一次记忆化的 detour 标签→对象解析
  （`common/dialer/capability.go:100` → `detour.go:63`）。两者都不拨号。`Box.Status` 是
  另一个例外：它会为目的地取一次实时预览。
* **证明：** `adapter/outbound/manager.go:235-237`；`manager_physicalpath_test.go:357-373`；
  `TestBuildNeverCallsADialingMethod`；`TestDryRunLeavesTakeNoSelectionPreview`。
* **复检：** `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...`
* **类型：** 正确性。**归属：** `modules/path-selection-validation.md`

### A6 — 缓冲区所有权：借用的视图、未池化的工作副本、原子块（Buffer ownership: borrowed views, unpooled working copies, atomic chunks）

六条规则并作一条：

1. `payloadReader` 游标不能在创建它的 `PeekStream` 调用结束后继续使用（`sniff.go:88-91`）；
2. `takePacketBytes` 返回的视图只在数据报存活期间有效（`quic.go:36-54`）；
3. QUIC 工作副本每次调用都通过 `make` 新建，不放入池；保留的片段
   与**工作副本**共享底层内存，不引用调用方数据报（`quic.go:183-184`、`:305-306`、`:472-476`）；
4. 头部保护**原地**写 4 个字节，所以无法覆盖到它们的 `Length` 会被拒绝
   （`quic.go:125-131`）；
5. MASQUE 批量发送是**按块全有或全无**；被接受的前缀属于传输层，
   不得重放（`transport/masque/session.go`、`writeBatchOwned`）；
6. 在拥有所有权的数据报路径上，所有权**在成功时转移**，释放操作被跳过
   （`session.go:484-489`）。

这些是所有权规则，不是恰好一次投递：固定版本的 HTTP/3 批量拷贝回退，可能
在后续错误把所有 payload 的所有权交还调用方之前，先排队一个前缀。

* **原因：** 每条规则之所以存在，都是因为违反它曾产生过真实缺陷。
* **违反后果：** 被保留的游标读到已回收的池内存；工作副本一旦池化，片段就会活得
  比所有者更久；在拥有所有权的路径上做释放就是**双重释放**。
* **证明：** 地址范围测试 `quic_ownership_test.go:314-350`；修改原始数据报的测试
  `:206-281` 与 `quic_boundary_test.go:427-456`；`cap` 上界
  `sniff_bounded_copy_internal_test.go:120-156`；`route/udp_batch_capability_test.go`。
* **复检：** `go test -count=1 ./common/sniff/...` 以及
  `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...`
* **类型：** 缓冲区所有权正确性。**归属：** `modules/buffer-ownership.md`

### A7 — Context-ID 替换会释放原对象，并且必须拒绝其所在的块（A context-ID substitution releases the original and must refuse its chunk）

`PrependContextID` 可能分配一个替代对象并**释放原对象**，所以一次替换会**在尝试之前**
就拒掉受影响的块，并原地更新调用方的切片。此前
已被接受的块保持已转移状态；只有尚未转移的剩余部分能回退。

* **原因：** 这是一个有记录的真实缺陷。被拒绝的批次保留了 context ID，回退逻辑又前置了一个
  新的，`packetAddresses` 读到的是错误的版本半字节，于是**每个报文都被静默丢弃
  ——没有错误，也没有 PTB**。
* **违反后果：** 隧道上静默地全量丢包。
* **证明：** `transport/masque/session.go:551-579`，缺陷记录于 `:539-549`。
* **复检：** 在相应 tag 下运行 MASQUE 测试套件。
* **类型：** 缓冲区所有权正确性 + 已确认缺陷。**归属：** `modules/masque-connectip.md`

### A8 — 空闲退役断言的是 `IdleConnectionReleaser`，绝不是捆绑的 `IdleConnectionKeeper`（Idle retirement asserts `IdleConnectionReleaser`, never the bundled `IdleConnectionKeeper`）

* **原因：** 断言捆绑接口**在每一次遍历时都在运行期静默失败**，所以 HTTP
  client 连接池过了四轮评审都没被碰到。有意不要求完整的 keeper：
  有些传输根本没有安全的“no new stream”状态。
* **违反后果：** 受影响的池可能被漏掉，而其他符合条件的资源仍被裁剪。
* **证明：** `route/reference.go:440-494`，理由见 `:479-484`；提交 `93823c3d`、
  `8735b1d`；`route/reference_coverage_stress_test.go`。
* **复检：** `go test -count=1 ./route/...`
* **类型：** 已确认缺陷防护。**归属：** `modules/core-lifecycle-runtime.md`

### A9 — 内存裁剪仅限空闲；`ReleaseMemory` 才是可能重置网络的路径（Memory trim is idle-only; `ReleaseMemory` is the path that may reset the network）

裁剪不得重置网络、拨号、唤醒被挂起的工作，也不得终止活动流。
`ReuseSuspect.RetireSuspect()` 是更强的**睡眠/复用边界**动作，在
`CloseIdleConnections` **之前**运行。

* **原因：** 裁剪本应回收空闲资源，不应触发网络重连。
* **违反后果：** 空闲期裁剪可能触发新连接和后台工作，增加网络与设备功耗。
* **适用范围：** `service/oomkiller/timer.go` 的危急回收可以调用 `ReleaseMemory`，后者会重置
  网络，这是有意为之（`route/conn_reclaim.go`）。该服务是否存在、危急处理
  是否只上报，取决于宿主配置与选项；光看 `json:"-"` 推不出这两点。
  **裁剪（Trim）≠ 释放（Release）。**
* **证明：** `route/reference.go:154-197`、`:394-411`、`:496-511`、`:547-556`；
  `route/reset_entrypoints_test.go:95`；`route/reset_entry_matrix_test.go:103`。
* **复检：** `go test -count=1 ./route/... ./common/power/...`
* **类型：** 生命周期防护。**归属：** `modules/power-mobile-lifecycle.md`、`modules/core-lifecycle-runtime.md`

### A10 — 平台快照只表达级别；当前状态的读取不得变成一个边沿事件（Platform snapshots are level-only; a current-state read must not become an edge）

`reconcileScreenState` / `reconcileLockState` 协调的都是一个级别。显示器**打开**不等于
解锁。`resumed()` 不发布任何东西。

* **原因：** 在真实设备上测得：一次 28 m 24 s 的睡眠期间出现了 13 次 resume 回调，产生 13 个
  锚点和一个 28.29 s 的边界，导致测量失真。锚点与每个休眠时段的边界锁存器共用一个字段；
  后续尝试以“已暂停”状态控制睡眠边沿时，十分钟锁屏期间没有产生任何边界，因此该修改被回退。
* **违反后果：** 睡眠时长上报错误；短暂亮屏可能错误地消耗复用边界。
* **证明：** `box_lifecycle.go:141`、`:173-199`、`:228-252`；提交 `645d96b4`、`60681f27`、`0ff90482`；
  `box_lifecycle_ordering_test.go`；`e20875359` → `e661258a6`（回退，两者均已归档）；
  `docs/fork/p1c-iphone-device-closure.md:289-291`。
* **复检：** `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run 'TestTheReuseVerdict|TestTheSleepEdge' .`
* **类型：** 已确认缺陷防护 + 平台约束。**归属：** `modules/power-mobile-lifecycle.md`

### A11 — libbox ABI 接口面与 Apple 单一来源固定（The libbox ABI surface and the Apple single-source pin）

`experimental/libbox.DeprecatedNote.Message()` 返回 `*StringBox`，**不是** `string`，因为裸 `string`
会把一个 Go 指针放进 gomobile 的打包结果帧，触发
`fatal error: bulkBarrierPreWrite: unaligned arguments`。`adapter.NewAdapterWithDialerOptions` 多带
一个可变参数。Apple 工作流为每个构建解析出唯一的 client 源码 SHA，并在各作业之间
共享；默认它来自 client 仓库配置的分支，而不是父仓库的
gitlink。显式覆盖是支持的，而且就源码选择而言，gitlink 相等是可选项。

* **原因：** 前者是无法恢复的崩溃；后者正是拦住 iOS 与 macOS 用
  两份不同源码构建的机制。
* **违反后果：** 按上游 libbox 构建的 client 会崩溃；两个 Apple 产品从
  两个不同的修订版本发布。
* **检查范围：** `verify.yml` 运行 `check-libbox-abi.sh --pin-only`，比较客户端 gitlink 和 `VERIFIED_*_SHA`；gitlink 缺失或版本不一致均应失败。`test-check-libbox-abi.sh` 覆盖正确配对与 Apple gitlink 被删除的情况。

  这项检查用于发现只有 gitlink 更新、常量未更新的情况。例如 v0.1.28 路径监视金丝雀测试将 Apple gitlink 从 `6e559a4` 移至 `3aedd741`，当时 `VERIFIED_APPLE_SHA` 没有同步更新。

  完整检查的第 1–4 层包括客户端调用点扫描，需要 cgo、Darwin 构建配置及客户端检出，由 `client-apple.yml` 针对已检出源码运行。gitlink 与常量一致，并不能证明工作流独立解析的构建源码是同一 SHA；该工作流也不在独立发布验收的必需集合中（A12）。应分别核实工作流的实际运行结果。
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

`release.yml` 为每个产品工作流**针对确切的 SHA** 挑一次成功运行，从
这些运行里下载，再按平台把二进制 + `.sha256` + `BUILD-INFO` 对照真实的 `sha256sum -c` 校验；
平台信息从文件自身的头部读取，并要求记录的提交与之匹配。

* **原因：** 元数据来源不等于二进制来源。
* **违反后果：** 发布里混进了没人构建过、也没人校验过的字节。
* **适用范围：** 它覆盖的只是计划步骤挑出来的内容；要检查每个生产者的触发条件以及
  被选中的运行，不要假定每个生产者都只能手动派发。另一个独立的
  `verify-release-acceptance.sh` 不包含 `client-apple.yml`，也不由
  发布路径调用。这条关于发布路径的具体陈述是
  `VERIFIED_SOURCE`；`modules/ci-release.md` 里单独链接的 CI 运行只适用于各自的 SHA，
  本身验证不了一个发布产物。
* **证明：** `release.yml:144-204`；`scripts/ci/release-artifacts.sh`；
  `scripts/ci/verify-release-acceptance.sh:99-102`、`:151`。
* **复检：** 阅读该工作流；真实运行需要 `gh` 和网络。
* **类型：** 发布产物完整性。**归属：** `modules/ci-release.md`

### A13 — 上传调度器在未设置时不起作用，拨号调速器在其窗口之外不起作用（The upload scheduler is inert when unset, and the dial governor is inert outside its window）

`traffic_scheduler` 缺失、为零或为空 ⇒ 门禁照样观察每个受管理的字节、全部放行，而且
**不启动任何队列、任何锁和任何定时器**。拨号调速器的零值不作用于任何对象。

* **原因：** 没设置整形策略，就不该多出整形队列或速率限制；而一个永久的拨号
  限制，会在预期的恢复窗口之外持续造成开销。这并不证明整份配置的
  兼容性，也不证明与上游端到端行为一致。
* **违反后果：** 一份什么都没要求的配置被整形了；在正常网络里，每次拨号
  都在为并不存在的突发付代价。
* **证明：** `common/trafficsched/doc.go:93-97`；
  `common/trafficsched/scheduler.go`（`Options.RateSource`、`SetRateSource`）；
  `route/dial_governor.go:22-24`、`:55-56`；`option/traffic_scheduler_test.go:31`。
* **复检：** `go test -count=1 ./common/trafficsched/... ./route/...`
* **类型：** 兼容性/ABI 相邻（配置可加载性）。**归属：** `modules/data-path-optimization.md`

### A14 — 每个 `DialerOptions` 字段在能启用绕过之前都必须先被分类（Every `DialerOptions` field must be classified before it can enable a bypass）

某条流上带了未分类的选项，就会产生 `BlockerUnclassified`，这个绕过也就被拒绝；一个反射
测试会在任何字段还没分类时失败，而且表格里不得列出已不存在的字段。

* **原因：** 新加的拨号选项，绝不能默认就让绕过变得**安全**。
* **违反后果：** 快路径静默丢掉了操作者要求的某项 socket 语义。
* **证明：** `common/dialer/profile.go`；`TestEveryDialerOptionIsClassified`；
  `TestFamilyStrategyAppliesToALiteralAddress`；`TestZeroProfileRefuses`。
* **复检：** `go test -count=1 ./common/dialer/...`
* **类型：** 正确性防护。**归属：** `modules/data-path-optimization.md`

### A15 — 上游/fork 所有权边界：固定替换集是一条含四个成员的路径前缀规则（Upstream/fork ownership boundary: the pinned-replace set is a prefix rule with four members）

`REQUIRED_FORK_REPLACES` 列出了 `sing`、`sing-tun`、`quic-go` 和 `sing-quic`；`cronet-go` 发布的是一棵
**树**状的模块集合，所以规则必须是**路径前缀**，不是一个列表。

* **原因：** 一条 `replace` 只重写**一个**模块路径。只固定根模块，嵌套模块
  就会解析到上游——这个配对没人构建过、也没人测过，在 linux/amd64 上甚至链接不起来。
  `sing-quic` 是承载硬性报文大小上限的透传层，所以嵌套模块一旦解析到上游，
  **已被证明的 MTU 上限就会静默消失**。
* **违反后果：** fork 自己的绑定跟上游的原生归档配到了一起。
* **证明：** `scripts/ci/check-go-module-integrity.sh:105-124`；其自身历史 `:60-66` 记录道，
  `test` 模块曾经静默链接到上游的 `sing-tun`，而 *"the old code could not fail on it, by
  construction"*。
* **复检：** `bash scripts/ci/check-go-module-integrity.sh --require-checked test`
* **类型：** 上游/fork 所有权边界。**归属：** `modules/build-pruning-release.md`

### A16 — 守护运行期可用性问题的 tag 桩必须失败，而不是缺席（A tag stub that guards a runtime availability question must FAIL, not be absent）

`include/quic_stub.go:48` 装的是一个**会失败**的 `ConfigureHTTP3ListenerFunc`，不是把该
字段留成 nil，这样 `decideHTTP3Availability` 才能区分“absent”和“present but broken”。

* **原因：** 字段为 nil 时，这两种状态分不开，缺失的可选传输也跟损坏的传输
  混在一起——于是致命/非致命的判定没了输入。
* **违反后果：** 要么每个缺失的 HTTP/3 传输都拖垮 TCP CONNECT，要么损坏的传输
  被静默容忍。
* **证明：** `include/quic_stub.go:48`；`protocol/naive/inbound.go:212-236`、`:316-334`。
* **复检：** 分别在带与不带 `with_quic` 的情况下构建，并断言判定结果不同。
* **类型：** 平台/构建约束。**归属：** `modules/naive-http-transport.md`、`modules/build-pruning-release.md`

### A17 — 除非显式配置，嗅探元数据不是目的地址重写（Sniff metadata is not a destination rewrite unless explicitly configured）

嗅探到的域名会作为 DNS 规划与拨号候选项进入路由；单靠嗅探不会重写
`Destination`，除非显式启用已弃用的 `override_destination`。还有一项独立机制：FakeIP
反向映射保存原始地址，并在 `route/route.go` 中把它替换成 FQDN。

* **原因：** 照嗅探到的名字去重写目的地址，会改变哪些规则命中、哪个地址被
  拨号——这是把策略变更伪装成便利功能。
* **另外：** 一个被嗅探到的域名**会让这条流失去 Direct Offload 快路径的资格**
  （`BypassRefusedSniffedDomain`）。
* **违反后果：** 一条只是看起来像某物的流，规则语义就变了。
* **证明：** `route/route.go:1437-1444`、`:541-546`、`:1255-1260`、`:1405-1410`；`route/route.go:791-796`；
  `route/bypass_verdict.go:29`、`:54`。
* **复检：** `go test -count=1 ./route/... ./common/sniff/...`
* **类型：** 正确性。**归属：** `modules/sniff.md`

### A18 — 分片的 QUIC 仅在 `ErrNeedMoreData` 时继续，且有界、缓冲区会被释放（Fragmented QUIC continues only on `ErrNeedMoreData`, bounded, with the buffer released）

`maxSniffRounds = 64` 限制的是新读取的续读轮次；到上限时，刚取出的缓冲区
会被释放，该流按不可嗅探处理。解析器给出的非“需要更多数据”判定，会记进
`metadata.SniffError`；取消以及非超时的报文读取错误仍可能致命。这个上限
并不约束此前对 `inputPacketBuffers` 的重放。

* **原因：** 修复前的行为被测过：1,000 个精心构造的数据报留下了 1,000 个缓冲区
  （存活 +16.7 MB），每轮的 QUIC 缓冲区从 33 KB 长到 1.06 MB。
* **违反后果：** 恶意对端就能驱动无界内存增长和池化缓冲区泄漏。
* **证据限制：** 该上限与放弃时的释放都**仅为 `VERIFIED_SOURCE`**——树里没有测试，
  也没有可复现的测量。
* **证明：** `route/route.go:1211-1217`、`:1340-1347`；`common/sniff/sniff.go:182-199`；提交 `578663e0`。
* **复检：** `go test -count=1 ./common/sniff/...`（**不**覆盖该上限）。
* **类型：** 针对已确认测量的加固。**归属：** `modules/sniff.md`

### A19 — Apple 快照协调仅表达级别**（见 A10）**（Apple snapshot reconciliation is level-only **(see A10)**）

原文单独列出这一条，是因为它同时也约束*客户端*：客户端不得发送自己没
观察到的边沿事件，核心也不得从级别合成出边沿事件。

* **类型：** ABI/client 契约。**归属：** `modules/power-mobile-lifecycle.md`、`modules/libbox-platform-contract.md`

### A20 — 发布验收是失败即拒绝的（Release acceptance is fail-closed）

失败、被取消、超时或被跳过的产品作业，都不能算成功。一个成功的
作业或步骤，同样不能被报告成“证明了它明确跳过的那一层”。

* **原因：** `scripts/ci/apple-ci-job-gate.py` 的 docstring 把这类问题说得最清楚：一次带
  `build_ios: false` 的派发，**会在某个平台从未被构建的情况下得出成功结论**，而被跳过的
  作业不会因此失败。这也是 `check-libbox-abi.sh` 打印 `PARTIAL` 却以退出码 0 结束的原因。
* **发布路径的权威是 `release-artifacts.sh`，由 `release.yml` 调用。** 它给
  每个目标工作流挑最新的运行，遇到已完成但非成功或被取消的运行就失败，要求每个
  产品都有产物和已记录的摘要，并把 `client-apple.yml` 纳入
  `RELEASE_EXTRA_PLATFORMS=true` 之下。这是作业/产物层面的条件，不等于每个
  内部检查都跑过：v0.1.31 的 Apple 工作流就成功了，尽管 Swift ABI 探针
  被标成 SKIP、ABI 检查器报的是 PARTIAL。那次运行的具体情况见 `modules/libbox-platform-contract.md`。
* **尚未覆盖：** 另一个独立的严格交接检查器 `verify-release-acceptance.sh`，并没有被
  发布路径调用——只有它的自检在 `release-contract-verify.yml` 里跑。它的默认必需
  集合也偏窄（`verify.yml`、`interop-xray.yml`、`server-linux-amd64.yml`）。这是两套检查之间的
  工具/归属缺口，而**不是**发布路径没有保护的证据；
  评判一次发布，看的是发布路径自己的门禁。
* **证明：** `release.yml`；`release-artifacts.sh`（`TARGET_WORKFLOWS`、`RELEASE_EXTRA_PLATFORMS`）；
  `verify-release-acceptance.sh`；`release-contract-verify.yml` 自检；`check-libbox-abi.sh
  --pin-only` + `client-apple.yml`（用于 ABI 固定）。
* **类型：** 发布产物完整性。**归属：** `modules/ci-release.md`

### A21 — 只有当响应证明该名字不存在时，NXDOMAIN 才放宽到 QNAME（NXDOMAIN widens to the QNAME only when the response proves the name does not exist）

只有单问题、空答案的 `NXDOMAIN`，且携带非零 `min(SOA TTL, SOA MINIMUM)` 时，才能
进 `nxdomainCache`。NODATA、`SERVFAIL`/`REFUSED`/`FORMERR`、超时、传输错误以及
以 CNAME 终止的答案，永远不放宽。命中项按**当前**问题重建，并报告
剩余 TTL；键仍按传输、client 子网与环境分别隔离，这些判定
永不持久化。`rewrite_ttl` 与零头部 TTL 的交互是一个**未决的源码问题**
（见 `modules/dns-demand.md`），不属于本不变量的证明范围。

* **原因：** RFC 2308 让 `NXDOMAIN` 成为关于这个名字的陈述。放宽任何更弱的响应，会把一次
  短暂的上游故障或某一类型特有的缺失变成整个名字范围的故障，也会把 CNAME 目标的
  缺失变成别名本身的缺失。
* **违反后果：** 对已存在名字的 A/AAAA/HTTPS 查询在本地就失败了；一次解析器故障被
  放大到每个 QTYPE；一个问题的答案被当成另一个问题的答案重放。
* **证明：** `dns/client_negative.go:139`（`storeNXDomain` 守卫）、`:205`（`loadNXDomain`）、`:254`
  （`nxdomainResponseFor`）；`dns/client_negative_test.go`（23 个 `TestNXDomain_*` 测试）。
* **复检：** `go test -count=1 -run TestNXDomain ./dns/` —— 2026-10-31 的评审中**未运行**
  （没有 Go 工具链）：`BLOCKED_BY_ENVIRONMENT`，不是通过。
* **类型：** 协议正确性。**归属：** `modules/dns-demand.md`

---

## B. 当前约定（Current conventions）

成熟实践，不是不可更改的架构。偏离时须说明理由。

* **让测试跟着变更一起走**，优先用行为断言，而不是源码文本守卫
  （`docs/UPSTREAM-SYNC.md:89-94`）。
* **用针对具体能力的退役接口**——实现 `IdleConnectionReleaser`，别
  声称自己是完整的 keeper。
* **把源码/构建来源保存进产物**（`BUILD-INFO`、`.sha256`）。
* **新增可复用池时，检查退役遍历与所有权契约。** 具名所有者测试不能证明不存在其他 `ReuseSuspect` 实现。
* **顺序测试应确认目标分支确实执行。** 例如 `box_lifecycle_ordering_test.go` 同时检查执行路径和事件顺序。
* **不要用睡眠状态门控看门狗。** 停止观察的看门狗不叫省电。
* **可以推迟周期性定时器，但绝不推迟它的首次执行。**

---

## C. 当前优化策略（Current optimization strategies）

本质上都是暂定的。每一条背后都有一次测量；测量结果一变，这一条就可能被重新
审视。

* `ModePaced` 聚合整形是已发布的调度器模式；各排序模式经测量
  与什么都不做区分不开。
* 只有缓冲区**多于一个**时，才走 MASQUE 批量路径。
* `common/bufgeom` 的分配上限只在 `!race` 构建中断言。
* Shadowsocks 稳态读侧的拷贝属于**经测量保留（measured keep）**。
* QUIC 单帧 `ClientFirefox` 启发式保持原样；修它超出了范围。
* L2 与 L3 拷贝路径在回环上区分不开，这是测试装置的性质，不是缺陷。

---

## D. 实现细节（不是规则）（Implementation details (not rules)）

* `PathNode.RequiredNetworks` **在生产中是只写的**——两个写入者，零个生产读取者。
  校验器读的是 `HopCheck.Requirement`，它由某个函数参数算出来。这个 fork 自己的提交
  `fc4bdd416` 说它 *"should be wired or removed by the file's owner"*。**这是一个 FOLLOW-UP。不要
  把它当契约引用，也不要基于它造成的双需求模型张力去立规则。**
* `common/sniff` 的嗅探是尽力而为；`metadata.SniffError` 只提供信息。
* `adapter/prestart.go` 只有一行 `package` 声明，是残留物。
* `trafficresearch` 构建标签没有任何东西引用；研究控制器一旦可达，测试就会失败。
* `docs/fork/physicalpath-controlpath-model.md` 关于 `ControlPath` 顺序的内容已过时（见 A4）。
* `dryrun.go:470-493` 是一段跟旁边代码矛盾的孤立注释；代码本身已有定论。
