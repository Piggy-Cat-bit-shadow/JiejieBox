# 验收索引（Acceptance index）

## 2026-10-29 增量证据（delta evidence）

| 需求 | 所述目标上的证据 | 限制 / 复检 |
|---|---|---|
| 端点本地裁决与回退 | `adapter/host_test.go`；本地 `go test ./adapter` 通过 | 仅单元裁决；`go test ./adapter -run TestJudgeHostFlow -count=1` |
| 冻结的 pin 与代码树一致 | 本地 `release-freeze.py verify --lock release/source-lock.json` 通过 | Desktop 嵌套 dashboard 因检出缺失而 SKIPPED；版本不一致仅作备注 |
| 冻结行为 | `scripts/ci/test-release-freeze.py`：本地 35 项通过 | 夹具使用录制来源；未执行在线发布 |
| 发布/Apple 工作流结构 | `scripts/ci/check-release-workflows.py`：本地 159 项通过 | 结构性检查，不是执行或产物证据 |
| Apple 输入冲突规则 | `scripts/ci/test-apple-client-input.sh`：本地 13 项通过 | 不构建 Apple 应用 |
| 客户端 ABI gitlink | `check-libbox-abi.sh --pin-only` 在 `0067b03` 处本地通过，覆盖 Apple `044e6d5` 与 Android `c7837107` | 仅 pin 比较；完整扫描是另一项 CI 结果 |
| Apple 客户端构建 | [macOS run 40776674407](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40776674407) 在父仓库 `ed0f3dc` 处成功；[iOS run 40819615363](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819615363) 在父仓库 `0067b03` 处成功，Apple 为 `044e6d5` | 每次运行都跳过了另一平台的作业；不是设备证据 |
| 历史目标 CI | `dbcfc1d` 处的 Verify run 40765511913 已取消 | 对该 SHA 不构成通过 |
| Apple beta 夹具套件 | 本地 Windows 运行：99 项通过，2 项因 `ditto` 不可用而失败 | 安装/复制断言 BLOCKED_BY_ENVIRONMENT；需在 macOS 上重跑 |
| v0.1.34 目标 CI | [Verify 40819594893](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819594893) 在 `0067b03` 处成功 | 运行级 CI 证据；不是设备行为 |

每一行都是一个**需求**、该需求已有的证据、它的**证据级别**，以及一条**用于复检它的命令**。没有命令的行不算已验证；
诚实的写法是 `UNVERIFIED`，下面确实有好几行正是如此。

## 如何阅读本文件（How to read this file）

在 Windows 上使用本地缓存的 Go 1.26.10 工具链，在包含 `cb71cf80d72` 的较新分支状态下，`./adapter` 的 `TestScopeClose` 测试、
三个 `./protocol/tun` 的当前 FakeIP 范围 L0 测试，以及 `./experimental/cachefile` 的 `TestFakeIPSaveMetadataReportsAFailedFlush` 均通过。
这些单元测试既不能确立已退役范围的 L0 防护，也不能确立物理设备行为。
使用同一工具链，`go test -tags p2ios ./experimental/libbox -run TestPlatformMonitor -count=1` 借助测试替身通过了监控顺序与刷新期间关闭的测试。
三个针对惰性默认值、优先级下限与被拒绝数据包所有权的 `./common/trafficsched` 聚焦测试也在 Go 1.26.10 下通过；
它们是单元证据，不是吞吐基准。
`./protocol/tun` 的 `TestInFlightCallbackUnderCloseIsOrdered` 也在 Go 1.26.10 下通过；它不执行 Linux 自动重定向会话，
也不能证明关机时每个规则集观察者都被汇合。
在同一分支状态下，`scripts/ci/check-libbox-abi.sh --pin-only` 使用 MSYS2 bash 通过：Android 与 Apple 的 gitlink 与脚本期望的修订一致。
它没有运行完整的 ABI 或客户端调用点扫描，其输出报告 gomobile pin 为 unknown。
`TestTrackedSchemaMatchesTheGenerator` 在 Go 1.26.10 与 `DEFAULT_BUILD_TAGS_OTHERS` 下本地通过；它针对本代码树比较受跟踪的 schema 与生成器。
在 Go 1.26.10、MSYS2 bash 与 `GOPROXY=off` 下，`check-go-module-integrity.sh --require-checked test` 对全部三个发现的模块通过。
它检查了 tidy/只读模块图以及根包/测试 fork 替换的一致性（包括 31 条 Cronet 路径）；未运行 `--verify`，也未做原生产物完整性检查。
三个针对 memo 增长、键规范化与无匹配行为的 `./route` 数据包目的地守卫测试在 Go 1.26.10 下通过。增长测试直接插入键，不是攻击测试。
两个具名的可复用 owner 能力测试与真实 HTTP 管理器退役遍历测试也在 Go 1.26.10 下本地通过；它们并未枚举所有可能的 owner。
选定的 `./protocol/mieru` 回退选项、冷却/探测、启动回滚与过期代次测试在 Go 1.26.10 下通过；这不能确立与外部的 Mieru 互操作。
`./protocol/tailscale` 的 `TestTailnetDomainsMatch` 与三个具名的系统绑定 getter 清理测试在 Go 1.26.10 下带 `with_tailscale` 通过，
使用的是本地测试夹具而非 tailnet。
三个针对并行能力转发、普通叶子拨号与数据包路径边界的 `./route` failover-shim 测试在 Go 1.26.10 下通过。
匹配 `^TestQUICSniffContext` 的 `./common/sniff` 测试在 Go 1.26.10 下通过；旧的 `TestQUICOwnership` 过滤没有匹配到任何测试，已在下方更正。
未过滤的 `./service/oomkiller`、`./common/listener`、`./transport/device` 与 `./protocol/ssh` 包测试在 Windows、Go 1.26.10 下通过。
它们不运行仅限 Linux 的 namespace 或通配符冲突测试、Darwin 压力集成测试，也不使用真实 TUN 设备。
设备分类器显式的 Windows errno 覆盖范围比其注释所暗示的更窄；见 `modules/core-lifecycle-runtime.md`。
在该主机上，聚焦的 HTTP/SSH 嗅探分片、半关闭生命周期与 XMUX 默认/退役测试也通过；它们不能确立真实设备恢复或对端互操作。

基线后检查于 2026-10-28 在目标 `011f3fdcd99` 上本地运行（单元测试，不是设备或 CI 证据）：

| 需求 | 测试 | 结果 |
|---|---|---|
| TUN 启动/重置交接与启动后更新 | `go test ./protocol/tun -run TestInterfaceUpdated -count=1` | PASS |
| 双栈 family 空应答的日志记录 | `go test ./dns -run 'TestFamilyProbeEmptyAnswerIsNotAnError\|TestUnquietEmptyAnswerStillLogsAnError' -count=1` | PASS |
| 已知 FakeIP 域 / 分片嗅探 | `go test ./route -run 'TestPacketSniffKnownFakeIPDomainDecision\|TestKnownFakeIPDestinationStopsFragmentedSniffOnFirstDatagram' -count=1` | PASS |
| family 查找的代次与稳定转换所有权 | `go test ./dns ./common/dialer -run 'TestLookupFamiliesDoesNotMixGenerations\|TestDialEpochPredicate\|TestDialPrefersTheAtomicSnapshotWhenOffered' -count=1` | PASS |
| 快速路径默认值与显式 false | `go test ./route -run 'TestFastPathUnsetAndTrueAreIndistinguishable\|TestFastPathFalseOnlySuppressesTheIntrinsicShape\|TestDirectFastPathSurvivesTransparentSelectorWithNoPolicy\|TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt' -count=1` | PASS |

上面的竖线转义是 Markdown 表格转义；在 shell 正则中请使用普通的 `|`。

在同一 Windows 主机与目标上，以下包的未过滤包测试通过：`./dns`、
`./dns/transport/...`、`./route/rule/...`、`./common/dialer`、`./common/physicalpath`、
`./protocol/tun`、`./common/sniff`、`./common/bufgeom`、`./common/runtimecoord`、`./common/power`、
`./protocol/masque`、`./transport/masque`、`./transport/http`、`./protocol/vless/...` 与
`./protocol/anytls`。`./protocol/snell` 报告没有测试文件。这些本地包结果不能确立互操作，
也不能确立另一操作系统上的行为。

分支推进到 `cb71cf80d72` 并更改 `cronet-go/all` pin 之后，未过滤的
`go test ./protocol/group ./common/httpclient ./protocol/tuic -count=1` 也通过。这并未测试 Cronet 原生客户端。

在 `DEFAULT_BUILD_TAGS_OTHERS` 下，根包测试 `TestADNSTransportDetourMustCarryTheQuery` 与
`TestAnHTTPClientDetourMustCarryTheConfiguredTransports` 在较新分支状态下通过。Mieru
的失败分类与回滚测试也在本地通过；它们不能确立互操作。
在较新分支状态下，未过滤的 `go test ./transport/v2rayxhttp -count=1` 也在本地通过，包括无需额外构建标签即可运行的 packet-up 测试。
在同一分支状态下 `go test ./option ./adapter/outbound ./common/trafficclass -count=1`
通过；这是本地单元证据，不是平台或部署认证。
三个根包 `TestBoxClose*` 测试（针对重复/并发关闭与错误保留）在 `DEFAULT_BUILD_TAGS_OTHERS` 下通过；
这次未启用 race 的运行不能证明每一种 panic 或并发交错。
四个选定的 `./route` 测试（针对通知合并、确认离线恢复、短间隔与跨接口交接）也在本地通过；
它们不能替代真实设备切换测试。
三个 `./route` 电源上报 Fast Path 拒绝/排序/上下文测试也在本地通过。
两个应用层 AAAA 抑制/缓存测试与两个 TUN 仅 IPv4 选项归约测试在本地通过；这确认的是这些测试路径，
而不是设备上的运行时地址重配。
选定的 `./protocol/shadowtls` 首记录超时、监听器清理与握手目标清理测试在本地通过；这不是与外部回退服务器互操作的证据。
Tailscale、OpenVPN 与 OpenConnect 的端点自有解析器退役测试在 `with_tailscale` 下本地通过；
它们覆盖的是目标池遍历，而不是真实网络切换。
选定的 `common/srs` 截断、校验和与解压预算测试以及 `route/rule` 远程流测试在本地通过；它们不能确立对每一个在线服务器的行为。
`TestCommandServerCloseClosesTheBox` 在此 Windows 主机上带 `p2ios` 与其他平台构建 profile 通过；
该测试不是移动设备生命周期运行的证据。

`go test -tags with_quic ./transport/masque -run 'TestWriteBatchChunksABurstAboveTheQueueCapacity|TestWriteBatchFallsBackOnlyForTheRefusedTail' -count=1 -v`
通过了这两个具名测试。未加标签的运行不会选择其中任何一个测试，因此上文未加标签的包结果不能作为块所有权的证据。
带标签的 HTTP/3 默认值测试 `TestHTTP3MigrationEnabledByDefaultForClient`、
`TestMASQUEH3PathManagerDefaultsToEnabled` 以及 `TestParseBBRProfileUnsetKeepsTheLibraryDefault`
也在本地通过；它们不能证明真实网络上的迁移。
Naive 的 `TestH3Fallback` 各用例在 `with_naive_outbound,with_purego` 下通过，使用了一个临时 modfile
将干净的本地 Cronet 检出替换到 pin 的 `c9c0e6ec8845`。这是针对本地源码的单元证据，
不能证明已测试下载的模块、原生 ABI 或在线对端。未过滤的 Naive 包套件在同一本地替换设置下也通过。

在此 Windows 主机上，未过滤的 `go test ./route -count=1` 在 570 秒后**失败**：
`TestTeardown_OneSidedCompletionNeverRunsOnClose` 在等待对端接收请求时于 120 秒后超时。
随后对该测试的单独运行在 0.01 秒内通过。套件失败不构成通过，其成因也未被单独重跑所确立。

在 Windows 10 Enterprise LTSC 2021 build 19044、Go 1.25.5 上，范围更广的
`go test -tags with_utls ./common/tls -count=1` **失败**：`TestWindowsClientHandshakeTLS13`、
`TestWindowsClientRoundtripTLS13` 与 `TestWindowsClientTLS13PostHandshakeConcurrentWrite`
返回 `tls handshake: read handshake: unexpected EOF`；一次过滤重跑复现了全部三个失败。
成因尚未确立。同一主机上，过滤后的 uTLS 指纹/ECH 与首飞变换测试通过。两项结果都不能确立其他 Windows 版本上的行为。

* 确定当前工具链，并从仓库根目录运行根包命令；`modules/ci-release.md` §4 中的设置是历史示例，不是可移植的要求。
* `$OTHERS` = `release/DEFAULT_BUILD_TAGS_OTHERS` 的内容。**大多数命令需要它**，没有它根包的测试会失败。
* `BLOCKED_BY_ENVIRONMENT` 表示某项检查在所述主机或运行中不可用；它不是通过，也不意味着其他环境无法运行它。
* 下面的历史行并不因为声明了工作流就成为 `VERIFIED_CI`。单独核对的发布运行及其确切 SHA 见 Windows daemon 模块。
* 除非某行指明了更新的目标或运行，其测试结果是历史的 v0.1.9 证据；为复检列出的命令并不能证明它在本次校准期间被执行过。

## 下面每条命令里的句点都至关重要（The `.` in every command below is load-bearing）

有好几行都用 `go test -count=1 -tags "$OTHERS" -run <TestName> .` 复检。末尾的 `.` 是**根包**，
而这些命令按**测试函数名**过滤，不是按文件。仓库整理（repository-tidy）过程绝不能忽略两个后果：

1. **重命名根包 `*_test.go` 文件对这里的每条命令都不可见**——它不改变任何函数名。
   重命名是安全的，而且 `.github/`、`Makefile` 或 `scripts/` 中都没有引用任何根包测试文件名，所以不会破坏任何东西。
2. **移动一个文件则不是。** 把测试迁到子包会悄悄让其 `-run` 过滤器指向一个不包含该测试的包：
   命令仍以 0 退出，但此时什么也证明不了。因此一次“整理式”移动就是验收覆盖的静默丢失，比难看的文件名更糟。

历史上的卫生审计检查了 **28 个根包测试文件**。它们的测试名与包归属是那个修订上命令的一部分；
在使用这些数字之前，请重新盘点当前代码树：

* **9 个是 `package box`（内部包），原则上无法移动。** 它们触及仅根包可用的标识符：
  `&Box{ctx:, scope:, statusView:, statusResolver:}` 这类基于未导出字段的复合字面量，外加
  `newStatusSurface`、`releasePowerGovernor`、`applyPauseEvent`、`lifecycle{}`、`deviceAxis`、
  `reconcileScreenState`、`reconcileLockState`、`ensureURLTestServices`。没有任何导出构造器会产生这些形态，
  因此这些测试根本不存在外部形式——改动 package 子句同样不可能。
* **19 个是 `package box_test`，它们受机械性阻碍，而非语义性阻碍。** 这十九个都调用同一个仅根包可用的夹具
  `newBoxFromConfig`（`box_cross_kind_cycle_test.go:91`）。其函数体只使用导出 API，因此该辅助函数是可移植的——
  但每一种方案（按目的地各复制一份、抽取一个共享的测试支持包，或把十九个一起移动）都是对测试**内容**的改动，
  超出卫生审计的范围。宿主文件的影响面最大，若真要移动必须最后移。

一次干净的 `go vet -tags "$OTHERS" .` 证明第二组不触及任何根包内部：Go 会把 `box`
与 `box_test` 作为同一目录下的两个包编译，并禁止外部包命名未导出标识符。已验证，退出码 0；
并且十九个文件都不含 `reflect`、`unsafe` 或 `go:linkname`。

**覆盖范围说明，2026-10-28：** `verify.yml` 在 `cd test` 之后运行 `TestNetLogEventCount`，
所以那是独立测试模块的根包，而不是主模块的根包。主模块未加标签的 `go test -run '^$' ./...` 仅做编译，
其列出的 race 包也不包含主根包。所链接的 Verify 运行有自己的 SHA；在把具体测试覆盖归因于
该运行或更晚的修订之前，请检查它的作业与命令。

---

## 1. 核心生命周期与运行时协调（Core lifecycle and runtime coordination）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 1.1 | 在正在关闭的 Scope 上注册的清理函数会在注册它的 goroutine 上同步执行，且绝不会被丢弃 | `adapter/lifecycle.go:112-138`；`adapter/lifecycle*_test.go` | VERIFIED_UNIT + VERIFIED_SOURCE | `go test -count=1 ./adapter/...` | Go |
| 1.2 | `Scope.Close` 不等待进行中的 `Start`，也不并入迟到的清理错误 | `adapter/lifecycle_close_boundary_test.go`（4 个具名测试） | VERIFIED_UNIT | `go test -count=1 -run 'TestScopeClose' ./adapter/` | Go |
| 1.3 | 纪元单调递增；被取代的租约无法完成或释放更新的租约 | `common/runtimecoord/coordinator.go:80-104`, `:282-310`；`coordinator_test.go`, `lease_test.go` | VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` | Go |
| 1.4 | 退役遍历覆盖全部四个池（outbound、endpoint、DNS 传输、HTTP 客户端管理器） | `route/reference.go:440-494`；`route/reference_coverage_stress_test.go` | VERIFIED_UNIT | `go test -count=1 ./route/...` | Go |
| 1.5 | XHTTP 客户端保留 `ReuseSuspect` 能力，且 HTTP 管理器仍然可达 | `route/reusable_owner_inventory_test.go` 断言的是具名 owner，而不是全局计数 | VERIFIED_UNIT（仅限已命名用例的本地结果） | `go test -count=1 -run 'TestTheReusableOwnersTheWalkIsDocumentedToReachKeepTheirCapabilities\|TestTheWalkReachesTheRealHTTPClientManager' ./route/` | Go |
| 1.6 | 启动/关闭顺序（`EstablishInitialNetworkEnvironment` 最后；governor 先于 scope） | `box.go:1166-1168`, `:1210-1224`；`box_close_test.go` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -run TestClose .` | Go + 构建标签 |
| 1.7 | 100 次休眠/唤醒循环与 100 次网络切换不泄漏 goroutine；`Close` 之后不会复活 | `box_lifecycle_stress_test.go` | VERIFIED_UNIT（未启用 `-race`） | `go test -count=1 -tags "$OTHERS" -v -run 'TestOneHundred\|TestNoResurrectionAfterClose' .` | Go + 构建标签 |
| 1.8 | adapter 与 route 的 race 检查 | [Verify 40605271219](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271219) 的 race 作业在 `cb71cf80d72` 处记录 `ok` | 针对这些包与已测试交错的 VERIFIED_CI；Windows 本地 race 运行受阻 | `go test -race -count=1 ./adapter/... ./route/...`，在 Linux/macOS 上运行 | cgo |

## 2. 路径选择与启动期校验（Path selection and start-time validation）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 2.1 | 校验不发起拨号、不做解析、不改变选择、不消耗轮换 | `manager.go:235-237`；`manager_physicalpath_test.go:357-373` 断言 committed == 0 **并且** previewed == 0；`TestBuildNeverCallsADialingMethod` | VERIFIED_UNIT（+ 变异对照） | `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...` | Go |
| 2.2 | `Hops` 是数据包顺序（离设备最近者在前），`ControlPath` 是下行顺序，不是反转 | `physicalpath.go:564-600`；`physicalpath_test.go:462-482` | VERIFIED_UNIT | `go test -count=1 -run TestControlPathIsShorterThanThePhysicalPath ./common/physicalpath/` | Go |
| 2.3 | 在**真实**组之上，控制组永远不会是物理跳 | `route/physicalpath_route_test.go` | VERIFIED_UNIT | `go test -count=1 -run TestControlGroupIsNotAPhysicalHop ./route/` | Go |
| 2.4 | 校验器不会过度拒绝：11 个配置各自被接受，且各有一个被拒绝的孪生配置 | `box_static_validation_false_deny_test.go`（`TestFalseDenyMatrix`） | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run TestFalseDenyMatrix .` | Go + 构建标签 |
| 2.5 | 消费者需求只能是可证明的；默认解析器是一条图边 | `box_static_validation_true_final_test.go` | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run 'TestConsumerDemandContractIsProvableOnly\|TestTheDefaultDomainResolverIsAGraphEdge' .` | Go + 构建标签 |
| 2.6 | 跨类型环（outbound ↔ DNS 传输）在启动时被拒绝 | `box_cross_kind_cycle_test.go`；`box_static_validation_final_wire_test.go` | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run TestCrossKind .` | Go + 构建标签 |
| 2.7 | `common/routeflow` 的需求语义 | **包内没有测试文件**；仅有 `common/routeflow/reach.go` | VERIFIED_SOURCE | `go test -count=1 ./common/routeflow/` → 报告 `[no test files]` | Go |
| 2.8 | 跨类型图**整体**上的健全性 | CK1–CK7 从未运行；只封闭了具名的缺口 | **UNKNOWN** | — | — |
| 2.9 | `PathNode.RequiredNetworks` 的语义 | **在生产中只写**：2 个写入方、0 个生产读取方、5 个测试读取方 | VERIFIED_SOURCE — **FOLLOW-UP，不是契约** | `git grep -n RequiredNetworks` | git |

## 3. 嗅探（Sniff）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 3.1 | QUIC 包/数据报边界、合并包、畸形首部、`Length < 4` | 真实测试 + 两个 fuzz 目标 | VERIFIED_UNIT | `go test -count=1 ./common/sniff/...` | Go |
| 3.2 | 被保留的 QUIC 分片绝不会与调用方的数据报互为别名 | `TestQUICSniffContextOwnershipEndToEnd`, `TestQUICSniffContextFragmentsDoNotAliasTheDatagram` | VERIFIED_UNIT（针对这些测试的本地结果） | `go test -count=1 -run '^TestQUICSniffContext' ./common/sniff/` | Go |
| 3.3 | TLS 与 HTTP 门禁只在已测试的解析器边界内拒绝 | `sniff_gate_internal_test.go`, `http_gate_internal_test.go`；超过 500 个前缀 | VERIFIED_UNIT（针对所选用例） | `go test -count=1 -run 'Test(TLSFirstRecordGate\|TLSClientHelloGate\|HTTPGate)' ./common/sniff/` | Go |
| 3.4 | WireGuard 会被嗅探；uTP 的歧义是结构性的，且刻意不做收窄 | `TestSniffWireGuardUTPAmbiguityIsStructural` | VERIFIED_UNIT | `go test -count=1 -run TestSniffWireGuard ./common/sniff/` | Go |
| 3.5 | 分片 QUIC 的轮次上限为 64，放弃时会释放缓冲区 | `route/route.go:1211-1217`, `:1340-1347` | **仅 VERIFIED_SOURCE** — 无测试、无可复现测量 | 在 `route/*_test.go` 中 grep `maxSniffRounds` → 无结果 | — |
| 3.6 | 嗅探 goroutine 普查 | 在某次审计运行中为绿色；记录为**对负载敏感，且失败测试名会漂移** | VERIFIED_UNIT（单次运行）— **不能证明其不抖动** | 在负载下运行 `go test -count=5 ./common/sniff/...` | Go |
| 3.7 | QUIC 单帧 `ClientFirefox` 误报 | 已测量并记录；修复不在范围内 | VERIFIED_UNIT（证明其存在） | `quic_ownership_test.go:271-280` | Go |

## 4. DNS

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 4.1 | 来自 settled 起点查询的带时间戳应答可以投递，但不得在代次已改变的情况下被提交；unsettled 起点的结果不会作为稳定结果投递 | `dns/client.go`；`dns/cross_generation_answer_contract_test.go` | VERIFIED_UNIT | `go test -count=1 ./dns/...` | Go |
| 4.2 | FakeIP 服务器无法应答目的地查找 | `8fad3e9b`；DNS 需求测试 | VERIFIED_UNIT | `go test -count=1 -run FakeIP ./dns/...` | Go |
| 4.3 | DNS 规则求值、缓存、反向映射与 FakeIP 代次 | `go test ./dns/...` 通过 | VERIFIED_UNIT | `go test -count=1 ./dns/...` | Go |
| 4.4 | `systemd-resolved` 的 goroutine 泄漏已修复 | **它确实已修复**，修复提交为 `6b495134c`（`signalLoopStop`/`signalLoopDone`/`signalLoopOnce`、stop-channel `select`，以及在 `Close` 中的**汇合**）—— `docs/RELEASE-CERTIFICATE.md:70-85` 已**过时** | VERIFIED_SOURCE；执行侧为 `BLOCKED_BY_ENVIRONMENT` | `git log -S'signalLoopStop' e4b7e692..a267e99`；然后需要 Linux + systemd-resolved | Linux |
| 4.5 | 多平台 DNS 环境矩阵 | 从未运行 | **UNVERIFIED** | — | 多平台 |
| 4.6 | 只有真实、单问题、应答段为空且 SOA 负缓存 TTL 非零的 `NXDOMAIN` 才会泛化到 QNAME；NODATA、错误 rcode、超时以及以 CNAME 终结的应答都不会。`rewrite_ttl` 的零 TTL 交互是一个**未决的**源码问题，此处未予证明 | `dns/client_negative.go`（`storeNXDomain`）；`TestNXDomain_NoDataIsNotNameWide`, `...CNAMEChainIsNotWidenedToQNAME`, `...ZeroSOATTLIsNotPromotedByNameWide`, `...ErrorRcodesAreNotCached` | VERIFIED_SOURCE；测试存在，但在 2026-10-31 复核中**未重跑** | `go test -count=1 -run 'TestNXDomain_(NoData\|CNAME\|ZeroSOA\|Error)' ./dns/` | Go |
| 4.7 | 负缓存命中会针对当前问题重建，并报告剩余 TTL；键按传输、ECS 子网与环境做命名空间隔离；容量为 `min(cache_capacity, 2048)` | `dns/client_negative.go`（`loadNXDomain`, `nxdomainResponseFor`, `nxdomainCacheForCapacity`）；`TestNXDomain_AnsweredForEachTypeSeparately`, `...ReportsRemainingTTLNotOriginal`, `...DifferentTransportDoesNotShare`, `...ECSSubnetNamespaceIsSeparate`, `...EnvironmentNamespaceIsSeparate` | VERIFIED_SOURCE；测试存在，**未重跑** | `go test -count=1 -run 'TestNXDomain_(AnsweredForEachType\|ReportsRemainingTTL\|DifferentTransport\|ECSSubnet\|Environment)' ./dns/` | Go |

## 5. MASQUE / CONNECT-IP

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 5.1 | Capsule 解析/序列化、地址与路由类型、跨协议重叠 | `transport/masque` 的 capsule 测试 | VERIFIED_UNIT | `go test -count=1 -timeout 8m -tags "$OTHERS" ./transport/masque/...` | Go + 构建标签 |
| 5.2 | 每个批块在所有权上是原子的；已接受的前缀不会被重放；替换会拒绝其所属的块 | `batch_chunk_test.go`, `batch_order_test.go` | VERIFIED_UNIT（就所有权而言），不是恰好一次传输 | 同上 | Go + 构建标签 |
| 5.3 | 会话/流拆除与关闭边界 | `shutdown_test.go`, `close_boundary*.go`, `scope_ownership_test.go` | VERIFIED_UNIT | 同上 | Go + 构建标签 |
| 5.4 | PTB 与三个 MTU 量值 | 五个 MTU 测试文件 | VERIFIED_UNIT | 同上 | Go + 构建标签 |
| 5.5 | 针对独立实现的 CONNECT-IP 互操作 | `test/jiejie/reference/connect_ip_go_interop_test.go`——本次校准未运行该参考套件；Verify 在独立模块中运行的是另一项选定测试 | **此处 UNVERIFIED** | `bash scripts/ci/run-jiejie-suite.sh` | bash + 网络 |
| 5.6 | macOS / H3 设备行为 | 从未运行 | **UNVERIFIED** | — | 设备 |
| 5.7 | 该包自身的测试套件 | 历史带标签结果；在较新目标上本地未加标签的整包测试通过 | 对应各次运行为 VERIFIED_UNIT；未加标签不覆盖受标签门控的用例 | `go test -count=1 -timeout 8m -tags "$OTHERS" ./protocol/masque/...` | Go + 构建标签 |

## 6. 原生 Naive 与 HTTP 传输（Native Naive and HTTP transport）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 6.1 | 写入契约：短写不会被静默截断；帧计数器仅在整个帧完成后推进 | `audit_write_contract_test.go` | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" ./protocol/naive/...` | Go + 构建标签 |
| 6.2 | 填充是协商的，前 8 帧，0..255 从不做钳制 | `padding_test.go`, `padding_segmentation_test.go`, `padding_zero_frame_test.go`, `fuzz_padding_test.go` | VERIFIED_UNIT | 同上 | Go + 构建标签 |
| 6.3 | 会话目标 ACL 重新绑定的示例 | `TestJiejieTargetACLRebindingCannotBypass` | 针对其用例的历史测试证据，不能证明每个 outbound 或每条数据报路径 | `cd test; go test -count=1 -run TestJiejieTargetACL ./jiejie/` | 独立测试模块 + 网络 |
| 6.4 | UoT 目的地守卫会检查新的目的地键，并在没有规则匹配时放行 | `route/packet_destination_guard.go` 与守卫测试 | VERIFIED_SOURCE + 选定的本地单元测试；决策会被缓存，且域名地址不会被守卫固定 | `go test ./route -run TestPacketDestinationGuard -count=1` | Go |
| 6.5 | 该守卫不会破坏服务器内部后端 | `TestJiejieTargetACLDoesNotBreakMasqueradeBackend` | VERIFIED_UNIT | 同上 | Go |
| 6.6 | 与官方 NaiveProxy 客户端及 Caddy 的互操作 | `test/` 中存在互操作套件；Verify 在该模块中运行的是另一项选定测试 | **此处 UNVERIFIED** — 本次校准未运行该参考套件 | `bash scripts/ci/run-jiejie-suite.sh`（约 20 分钟） | bash + 参考实现二进制 |
| 6.7 | 真实网络 STUN、独立 IPv6 UDP 源、Caddy 性能对比 | 记录为未完成 | **UNVERIFIED** | — | 网络 |
| 6.8 | `host's own public address` 位于拒绝集合中 | 记录为**未完成**——该地址被刻意不予猜测 | **UNVERIFIED / FOLLOW-UP** | 在该主机上确认 | 该主机 |

## 7. 数据路径：卸载、流量类别、调度器（Data path: offload, traffic class, scheduler）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 7.1 | 每个 `DialerOptions` 字段都被分类；未分类的字段会拒绝旁路 | `TestEveryDialerOptionIsClassified`（反射，双向） | VERIFIED_UNIT | `go test -count=1 ./common/dialer/...` | Go |
| 7.2 | `ActionBypass` 不被任何 TUN 栈响应；旁路上绝不会创建 tracker | `protocol/tun/native_bypass_*_test.go` | VERIFIED_UNIT（行为轨迹） | `go test -count=1 ./protocol/tun/...` | Go |
| 7.3 | 携带 `Port` 的旁路会变成 `ActionFlow` | `TestBypassWithAPortBecomesAFlow` | VERIFIED_UNIT | 同上 | Go |
| 7.4 | 旁路决策**以测试形式**断言 0 次分配 | `route/direct_offload_bench_test.go`, `route/bypass_capability_cost_test.go` | VERIFIED_UNIT | `go test -count=1 -run Bypass ./route/` | Go |
| 7.5 | 调度器整形聚合流量，不允许超速，并在状态转换时释放 | `common/trafficsched` 门禁测试（11 个） | VERIFIED_UNIT | `go test -count=1 ./common/trafficsched/...` | Go |
| 7.6 | 研究性控制器无法进入生产 | 若接入生产，测试会失败 | VERIFIED_UNIT | 同上 | Go |
| 7.7 | 对已声明数据路径包的 race 检查 | [Verify 40605271219](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271219) 的 race 作业在 `cb71cf80d72` 处成功 | 对已声明包与已测试交错的 VERIFIED_CI；并非普遍无竞态 | 检查 `verify.yml` 与运行日志 | cgo |
| 7.8 | 真实链路上的整形吞吐与 AI 通路的 p95 结论 | 历史测量记录于 `doc.go`；该 harness 是手动工具 | **此处 UNVERIFIED** | 在真实链路上运行 `go run ./common/trafficsched/harness` | 两台主机 |

## 8. 缓冲区所有权与内存（Buffer ownership and memory）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 8.1 | 几何缓冲区可精确池化，且 `Advance`/`Prepend` 从不重新分配 | `geometry_test.go` | VERIFIED_UNIT | `go test -count=1 ./common/bufgeom/...` | Go |
| 8.2 | 分配上限 | 仅在 **`!race`** 构建中断言 | VERIFIED_UNIT（`!race`）；`race` 那一半需要 cgo | 同上 | Go |
| 8.3 | 批处理能力在计数器包装后依然保留 | 3 个测试，带真实的红色对照 | VERIFIED_UNIT | `go test -count=1 -run PacketBatch ./route/` | Go |
| 8.4 | 内核 splice 确实到达 `unix.Splice()` | sing fork 声明了 `linux-splice-proof`；本次校准未检查到匹配的运行 | 对该 pin 与环境为 **UNVERIFIED** | 检查针对精确 pin 版本的运行 | Linux |
| 8.5 | `Pipe2` 设置失败时不会接管 splice，以便调用方回退 | pin 的 `sing` 提交 `1729220c46c4`，`common/bufio/splice_linux.go`；`splice_actual_linux_test.go` | 在该 pin 上为 VERIFIED_SOURCE；本次刷新未执行 Linux 侧运行 | 运行所 pin 的 sing Linux splice 测试 | Linux |
| 8.6 | kTLS 缓冲数据与排队读取错误 | [Verify 40605271219](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271219) 对带标签的 `./common/ktls` 报告 `ok` | 单元包为 VERIFIED_CI；未证明真实内核 splice 或在线 KeyUpdate | 在受支持的 Linux Go 上运行 `go test -tags badlinkname ./common/ktls` | Linux |
| 8.7 | 零拷贝级别 3–5 | **从 sing-box 结构上不可观察** | **按构造即 UNKNOWN** | — | — |

## 9. 电源与移动端生命周期（Power and mobile lifecycle）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 9.1 | 复用裁决在级别被释放**之前**发布 | `box_lifecycle_ordering_test.go`，带空真守卫 | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run TestTheReuseVerdict .` | Go + 构建标签 |
| 9.2 | 休眠边沿在级别被暂停**之前**记录 | 同一文件，`probingAxis` | VERIFIED_UNIT | `-run TestTheSleepEdge` | Go + 构建标签 |
| 9.3 | 复用档位来自策略，未知休眠时长采用严格裁决 | `common/power` + `box` 测试 | VERIFIED_UNIT | `-run 'TestReuseBandsAreThePolicy\|TestTheBridgeUsesThePolicyBandsAndNothingElse'` | Go + 构建标签 |
| 9.4 | 可疑档位不排空任何东西 | `route.TestTheSuspectBandDrainsNothing` | VERIFIED_UNIT | `go test -count=1 -run TestTheSuspectBandDrainsNothing ./route/` | Go |
| 9.5 | 裁剪阶梯映射每个级别，未知级别被忽略 | `common/runtimecoord/platform_events.go:152-220` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` | Go |
| 9.6 | 真实设备上的 Apple 设备轴 | 一次 **iPhone 17 Pro Max / iOS 26.4.2** 运行：锁屏 13 分 13 秒 → 1 个边界 → `13m13.492448s` | **VERIFIED_DEVICE**（仅对该设备） | — | 该设备 |
| 9.7 | iPad、macOS、其他操作系统构建 | `NOT_RUN` / `SIGNING_OUT_OF_SCOPE` | **UNVERIFIED** | — | — |
| 9.8 | 真实 TUN、真实 NetworkExtension、网络切换 | 本次 Windows 校准未进行设备执行；请检查针对具体目标和环境的证据 | **此处 UNVERIFIED** | `docs/fork/real-tun-validation.md` §1–§7 | 相关平台与设备 |

## 10. MTU 与几何（MTU and geometry）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 10.1 | 三个 MTU 量值分别计算并分别受限 | MASQUE + WireGuard + `common/bufgeom` 源码 | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/bufgeom/...` 以及 MASQUE 套件 | Go + 构建标签 |
| 10.2 | 已知 detour 容量时，MASQUE 初始 QUIC 包大小会被钳制 | `protocol/masque/client.go`；`outer_quic_mtu_test.go` | VERIFIED_SOURCE；测试证据限于已测用例 | 带标签的 MASQUE 套件 | Go + 构建标签；不能证明后续数据包存在硬上限 |
| 10.3 | 开启 ChromeParrot 时，hysteria2 硬上限会到达线缆 | `protocol/hysteria2/hard_ceiling_wire_test.go`；`docs/fork/v016-final-closure-ledger.md:4-13` 处的撤回横幅 | VERIFIED_UNIT | `go test -count=1 -run HardCeiling ./protocol/hysteria2/` | Go |
| 10.4 | 低内存（16 KiB）几何由产品所用的同一来源选择 | `cmd/internal/appletags -mobile-only`；为空时 `verify.yml:252-258` 会失败 | VERIFIED_SOURCE | `go run ./cmd/internal/appletags -mobile-only` | Go |

## 11. libbox 与平台契约（libbox and platform contract）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 11.1 | 导出的 libbox 表面与 `SetupOptions` 契约 | `experimental/libbox/*`；包测试通过 | VERIFIED_UNIT + VERIFIED_SOURCE | `go test -count=1 ./experimental/libbox/...` | Go |
| 11.2 | 核心负责对平台级别分类；客户端只上报它们 | `common/runtimecoord/platform_events.go:152-220`；`experimental/libbox/platform_events.go` | VERIFIED_SOURCE | 阅读两者 | — |
| 11.3 | ABI 版本被提供并被消费 | `daemon/started_service.go:39` `APIVersion = 4`；`experimental/libbox/command_client.go:760` | VERIFIED_SOURCE | `git grep -n APIVersion` | git |
| 11.4 | Apple 客户端是 iOS 与 macOS 的同一份源码 | 两个作业中的 `assert-shared-source`；选择脚本的本地夹具在 MSYS2 与内置 Python 下通过 72 项检查 | VERIFIED_SOURCE + 本地夹具结果；不是发布产物检查 | `bash scripts/ci/check-apple-source-selection.sh` | bash + Python + git |
| 11.5 | Apple gitlink 与其 ABI 契约 pin 一致 | `verify.yml` 中声明了 `check-libbox-abi.sh --pin-only`；本地 pin-only 检查对 `044e6d5447eb` 通过 | VERIFIED_SOURCE + 本地脚本结果；不是完整 ABI 扫描，也不是发布产物证明 | `bash scripts/ci/check-libbox-abi.sh --pin-only` | git + bash |
| 11.5b | 恢复双源 Apple pin 的 refs 文件会被拒绝，且该拒绝不依赖调用方的环境 | `apple-client-source.sh:110-133` 在 source 之前先 grep **文件**；`resolve`（`:547-548`）与 `release-apple.sh:181` 合法地导出同名变量，修复前的环境测试把它误当作文件声明 | VERIFIED_SOURCE + VERIFIED_UNIT（`ece0fb9d2`） | 检查 `refs_declares_platform_source`（`:121`）及它在 `. "$refs_file"` 之前的调用 | sed |
| 11.6 | 当前 pin 版本上的 ABI 调用点兼容性 | Android 本地静态探针：309 个文件，零违规；[Apple run 40606786576](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40606786576) 报告 PARTIAL，Swift 探针因文件缺失被跳过 | 仅 Android 静态扫描；本地 Swift 零退出并不完整，因为 Windows 会把符号链接物化为文本，且有一个目标缺失 | `python3 scripts/ci/probe-accessors.py --contract docs/fork/libbox-abi-contract.tsv --lang swift --root clients/apple` | Python + 有效的客户端检出 |
| 11.7 | Android AAR 的来源与架构 | 工作流声明了按路径过滤的测试推送与 dispatch；run 40602193557 在 `1ff0af39110` 处成功 | **VERIFIED_CI**（针对该次运行）；后续产物内容未经独立核对 | `android-core-arm64.yml` 以及 `modules/ci-release.md` 中链接的运行 | CI |

## 12. 构建、CI 与发布（Build, CI and release）

| # | 需求 | 证据 | 级别 | 复检 | 环境 |
|---|---|---|---|---|---|
| 12.1 | 未加标签的 `go build ./...` | 在本次审计中运行 | **VERIFIED_UNIT** | `go build ./...` | Go |
| 12.2 | 服务器 profile 可构建 | 在本次审计中运行 | **VERIFIED_UNIT** | `go build -tags "$OTHERS" ./...` | Go |
| 12.3 | 目标专用 profile 与依赖约束 | 历史 Windows 失败；当前 Windows profile 增加了 `with_purego`，且精确 pin 的本地 Cronet 源码通过了选定的 purego 单元测试 | 历史失败不能证明当前 Windows 产物状态 | 检查当前 profile 与 pin 的源码 | 目标专用工具链 |
| 12.4 | 根包设计保证套件**在正确标签下**通过 | 在本次审计中运行：`ok 5.459s` | **VERIFIED_UNIT** | `go test -count=1 -tags "$OTHERS" .` | Go + 构建标签 |
| 12.5 | Verify 所声明的主根包测试覆盖 | 未加标签的仅编译命令运行零个测试；`TestNetLogEventCount` 在 `cd test` 之后、在独立模块中运行 | 声明的 VERIFIED_SOURCE；不能证明主根包套件运行过 | 检查 `.github/workflows/verify.yml` 与确切的运行 | — |
| 12.6 | 标签组合与 `-checklinkname=0` 契约 | CI 步骤 `:168` | VERIFIED_UNIT | `go test -count=1 ./cmd/internal/...` | Go |
| 12.7 | 上游 pin 假设的绊线 | [Verify 40605271219](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271219) 报告该步骤在 `cb71cf80d72` 处成功 | 对该次运行为 VERIFIED_CI；不能证明后续 pin | `bash scripts/ci/verify-upstream-assumptions.sh` | bash + 网络 |
| 12.8 | 必需的根包/测试 fork 替换一致 | 本地离线完整性脚本检查了全部三个模块图与 36 条根包/测试替换条目 | 本地脚本 PASS；不是归档字节校验 | `bash scripts/ci/check-go-module-integrity.sh --require-checked test` | Go + bash |
| 12.9 | 发布是对选定产物重新校验，而不是重新构建 | [Release 40609827203](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40609827203) 报告下载、校验与发布步骤在 `cb71cf80d72` 处成功 | 对该次运行为 VERIFIED_CI；无独立的本地资产字节校验 | 阅读工作流与运行记录 | — |
| 12.10 | 发布验收拒绝劣质证据 | `release-contract-verify.yml` 声明了针对 mock `gh` 的自测；发布路径使用其独立的产物/证据门禁 | 声明的 VERIFIED_SOURCE，不能证明当前运行 | `bash scripts/ci/verify-release-acceptance-test.sh` | bash |
| 12.11 | `verify-evidence/` 描述发布代码树 | **它并没有**——全部 13 份日志都指向 `cfc77dc5`，落后 `a267e99` 101 个提交、落后当前基线 1,214 个提交，且此后有 15 个被覆盖文件发生变化 | **STALE — 不构成证据** | `head -3 verify-evidence/*.log` | git |
| 12.12 | §7.4 的压测结果 | 一个**空的 `<!--STRESS_RESULTS-->` 占位符** | **EVIDENCE ABSENT（证据缺失）** | — | — |
| 12.13 | Windows 安装器 / 服务 / 签名门禁 | 存在九个 `.ps1` 门禁；没有归档输出 | **UNVERIFIED（仅声明）** | `client-desktop-windows.yml`（dispatch） | Windows + 管理员权限 |
| 12.14 | Docker 支撑的夹具 | 未运行；`test/docker_test.go` **没有跳过守卫** | **BLOCKED_BY_ENVIRONMENT** | `docker info && go test ./test/...` | Docker |
| 12.15 | Xray 参考互操作 | 仅 dispatch；QUICHE 在线互操作测试已被**删除** | **UNVERIFIED（仅声明）** | dispatch `interop-xray.yml` | Linux + 网络 |

---

## 已知失败与已知不稳定集合（Known-failing and known-flaky set）

这些是历史观察，不能证明当前的失败具有相同原因。在归类某个失败之前，请复现并比较当前的目标与环境。

| 条目 | 历史观察 | 范围与后续 |
|---|---|---|
| `common/tls` `TestWindowsClient*TLS13` | 本次刷新期间，在 Windows 10 build 19044 上本地测试服务器握手以 `unexpected EOF` 失败 | 成因未确立；见上文当前结果 |
| `common/sniff` `TestAgentFFrameShape*`、`TestTheSniffCensusMovesForExactlyOneLeakedGoroutine` | **对负载敏感；失败测试名每次运行都可能不同** | 在修复版与基线上单独运行均 5/5 通过 |
| `protocol/group` `TestRecheckSingleFlightCollapsesABurst` | 对负载敏感 | 在两棵代码树上连续 20 次单独运行均干净 |
| `common/trafficsched` 在 `-count=10` 下 | 约 18 分钟的刻意休眠会触发超时 | 每个单独测试都通过 |
| `protocol/masque` `TestMasqueRestartCyclesDoNotLeakLoops` | **单独运行 330 秒**——慢，但没有挂起 | 已测量 |
| 未加标签的根包 | 12 个因缺少标签导致的失败 | 使用 `$OTHERS` |
| 未加标签的 `protocol/masque` | 历史性的缺少标签失败 | 未过滤的整包测试在 2026-10-28 目标上通过；需分别核对所需特性与标签 |
| `common/windivert`、`common/tlsspoof` | 报告称其为“3 pre-existing environmental failures”（中文：3 个先前存在的环境性失败） | **它们在此处通过**（25 PASS / 9 PASS，0 FAIL）——该说法是主机特定的，不是代码树特定的 |

---

## 本 fork 自身长期存在的限制（The fork's own standing limits）

这些不是靠测试来关闭的验收缺口；它们是已记录的限制。

* 设备与特权数据面行为需要针对具体构建和平台的证据；本次本地 Windows 刷新未实际运行真实 TUN、Linux kTLS 或 Apple NetworkExtension。
* Apple 产物相关结论需要与确切目标相匹配的工作流运行和产物溯源。
* 一条历史性的 Linux `cronet-go/lib/linux_amd64` `-race` 链接失败记录在其先前的 pin 上。
  所引用的 Linux profile 省略了 `with_naive_outbound`；这不能确立所有构建或当前 Cronet pin 都不受影响。请复检所选 profile、修订与工具链。
* **`systemd-resolved` 的 goroutine 泄漏已修复**（`6b495134c`）。`docs/RELEASE-CERTIFICATE.md:70-85`
  仍称其未修复；该文档已过时。历史上对 box 级测试的排除正是以此泄漏为由；
  当前 Verify 在该独立模块中只运行一项选定测试，而不是其完整的 box 级套件。任何覆盖范围的变更都是 CI 决定，而不是知识库决定。
* 历史性的 `Pipe2` 误交接缺陷已在 pin 的 `sing` 源码中修复；另有一个独立的通用活动流停滞仍是设计限制，如 `modules/core-lifecycle-runtime.md` 所述。
* Apple pin 在 `verify.yml` 中有一个已声明的 `--pin-only` 校验器；完整的客户端检查有
  不同的环境和工作流。声明不是运行结果。
* **`RequiredNetworks` 是只写的。**
