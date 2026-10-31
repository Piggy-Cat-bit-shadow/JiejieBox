# CI、证据与发布（CI, evidence and release）

## v0.1.34 Apple 检查与证据（v0.1.34 Apple checks and evidence (2026-10-31)）

父仓库中的 Apple gitlink、ABI 检查常量和冻结锁文件现在都指向 `044e6d5447eb`；
此前的候选版本已合入 Apple `main`。

Libbox 的构建和打包迁移到共享 composite action 后，`check-apple-source-selection.sh` 和
`test-apple-beta-publish.sh` 的断言也随之更新：工作流不再内联构建或打包命令；action 中各有
一份实现，工作流从两个位置调用它。`check-release-workflows.py` 会检查这一结构。结构检查
通过不能证明应用已在设备上运行。

在父级 `0067b03` 上，[Verify 40819594893](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819594893)
与 [iOS Apple 运行 40819615363](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819615363)
成功。后者使用 Apple `044e6d5`，构建了 Libbox 与 iOS，跳过了 macOS。另一次成功的
[macOS 运行 40776674407](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40776674407)
用的是父级 `ed0f3dc`，而不是 `0067b03`，它的 iOS 任务被跳过。[Release 40824078971](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40824078971)
在 `0067b03` 上成功。以上都是先前观测到的运行，本次刷新没有重新派发。

## 2026-10-29 目标下的发布冻结与 Apple 验收（Release freeze and Apple acceptance at the 2026-10-29 target）

`release/dependencies.json` 声明各依赖的来源仓库、分支和更新策略；实际使用的版本则记录在
Go replace 指令和客户端 gitlink 中。`scripts/ci/release-freeze.py` 负责解析分支头、比较固定
版本、应用符合条件的更新，生成并验证 `release/source-lock.json`。只有来源可比较时才能
更新 `update` 类依赖；分支缺失或发生分歧会导致检查失败。

29 个 Cronet 原生归档的策略是 `controlled`，不会由该流程自动更新。Desktop 内的 Dashboard
只作 `report`，因此需要按 Dashboard → Desktop → 内核的顺序修复。锁文件记录构建使用的
固定版本，不记录不断变化的分支头；验证覆盖根模块和 test 模块的 Go replace、Cronet 一致性
及客户端 gitlink。如果 Desktop 未检出，嵌套 Dashboard 检查会标记为 SKIPPED；版本号不一致
只产生备注，不使验证失败。

`.github/workflows/release-prepare.yml` 只能手动触发，并不自动发布。它解析并检查来源、应用
允许的更新、整理依赖、生成快照并执行门禁；只有内容发生变化时才提交和推送。打标签之前，
流程要求候选提交的精确 SHA 已有成功的验证证据。dry run 不会创建候选提交，不能当作候选
提交的验证证据。

Apple ABI pin 检查在候选提交之后执行，因为它读取 `HEAD`。准备实际发布前应重新检查工作流；
不能仅凭版本文件或 lock 文件判断发布已经完成。

`client-apple.yml` 只支持手动触发。输入 `apple_client_sha` 要求完整的 40 位小写 SHA；
如果它与仓库变量冲突，工作流会拒绝执行。没有覆盖值时，工作流在本次运行中解析一次 refs
文件指定的分支头，其结果可能与父仓库 gitlink 不同；正式发布需要选择冻结的版本。

同时构建 iOS 和 macOS 时，两者共用一次构建的 Libbox 产物。只构建 macOS 时，同一个 composite
action 会在 macOS 任务内构建 Libbox，省去额外的 macOS runner。此模式允许上游 Libbox 任务
被跳过，但不允许它失败后继续。两条路径最终都验证 manifest 并安装产物。结构检查不能证明
macOS 工作流已执行，更不能证明设备行为。

历史 `dbcfc1d` 目标上已有的 [Verify 运行 40765511913](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40765511913)
是取消，不是通过。刷新本身请用只改文档的 `[skip ci]` 提交；绝不要只为验证一次知识更新
去手动派发 Actions。

> **范围更正，2026-10-28：** 下面的 v0.1.9 工作流普查只属于历史，不是
> 当前树的清单。尤其 `.github/workflows/windows-boxdd-tests.yml`
> 声明了一个 Windows runner、受路径过滤的 push/PR，以及针对 boxdd 测试的手动派发。
> 这是 `VERIFIED_SOURCE`，不能证明任何一次具体运行执行过或通过。如果要依赖下文所述的其他门禁覆盖范围，
> 请先重新读当前工作流与运行记录。
>
> 当前树有 **19** 个工作流文件。五个不在普查中，也没有归属小节：
> `apple-tls-verify.yml`、`linux-dns-verify.yml`、`linux-masque-verify.yml`、
> `linux-netns-verify.yml`、`linux-platform-verify.yml`。它们只是已声明的源码，不能证明
> 运行过；声称 CI 已覆盖之前，请直接检查它们。

**更晚的运行证据：** GitHub Actions [Verify 40602193497](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40602193497)
与 [Android Core ARM64 40602193557](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40602193557)
都报告在 `1ff0af39110d4bd4044c817a0bf209ff01e21f7a` 处成功。对这些工作流与这个 SHA，
这些运行级结果是 `VERIFIED_CI`；但它们不能确立后续的 Cronet 固定值行为，
也不能确立单个测试覆盖、产物发布或设备行为。

**v0.1.31 运行证据：** [Verify 40605271219](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271219)
在 `cb71cf80d72` 上成功完成。race 任务的日志对所声明的 route、
dialer、scheduler、Mieru 与 adapter 包报告 `ok`；带标签的 `common/ktls` 单元包也报告
`ok`。这些都不证明真实的内核 splice，也不证明所有平台。[Apple client 40606786576](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40606786576)
报告成功，但 ABI 日志显示 **PARTIAL**：Swift 访问器探针因引用源文件缺失而被跳过；
运行解析出 Apple 源码 `3aedd7417488`，绑定与 gitlink 检查都报告通过。签名预检在未签名
模式下被跳过。成功的 [Release 40609827203](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40609827203)
报告，下载、验证与发布步骤对这个 SHA 都成功。以上是运行记录，不是对已下载资产字节或
设备行为的独立本地检查。

**范围：** `.github/workflows/*`、`scripts/ci/*`、`verify-evidence/*`、`release/*`，以及未来的
AI 可以或不可以声称“已验证”的内容。

**在修改工作流、门禁脚本、发布步骤时加载本模块，或**在引用任何
既有证据之前**加载它。**

**本模块中唯一最重要的一条规则：***CI 任务的声明不是这个任务已运行的证据，
单元测试不是互操作，静态审计不是设备行为，一个平台的 PASS 不是
全平台 PASS。* 下文每一行都按这个标准分级。

---

## 1. v0.1.9 处的历史工作流普查（Historical workflow census at v0.1.9）

**在 `v0.1.9` 基线上实测。普查中有四道自动门禁。** 本节早先的版本称
`verify.yml` 是“唯一的自动门禁”，并列出九个工作流；当时树里有十二个，
其中三个完全不在这份列表中。相信旧表格的读者会以为，一次动到发布工具链、移动端契约、
或 Windows 运行时审计的 `push` 没有被检查。

| 工作流 | push | PR | dispatch | `workflow_run` | 备注 |
|---|---|---|---|---|---|
| **`verify.yml`**（“Verify”） | `testing` | 全部 | 是 | — | **源码**门禁 |
| **`release-contract-verify.yml`**（“Release contract verification”） | `testing`、`release/**`、`fix/release-asset-contract` —— **受路径过滤** | 受路径过滤 | 是 | — | **发布工具链**门禁；见下文 |
| **`mobile-contract.yml`** | 是 | — | 是 | — | 移动端契约 |
| `android-core-arm64.yml` | `testing` —— **受 `paths:` 过滤** | — | 是 | — | Android ABI |
| `client-desktop-windows-runtime-audit.yml` | — | — | 是 | **是** | 在桌面产者之后运行，不在 push 时 |
| `client-apple.yml` | — | — | 是 | — | |
| `client-desktop-windows.yml` | — | — | 是 | — | |
| `client-macos.yml` | — | — | 是 | — | |
| `interop-xray.yml` | — | — | 是 | — | |
| `release.yml` | — | — | 是 | — | |
| `server-linux-amd64.yml` | — | — | 是 | — | |
| `windows-core-amd64.yml` | — | — | 是 | — | |

必需检查与分支保护配置**不在仓库中**；不要猜。仓库里*确实*有的，是可能贡献一项
检查的工作流集合，也就是上表。

### push 门禁受路径过滤，而过滤器就是契约（The push gate is path-filtered, and the filter is the contract）

`release-contract-verify.yml` 的 push 触发器只关注发布工具链相关路径。产品源码、文档和报告不在该检查的输入范围内；如果一次 push 没有涉及列出的路径，就不会触发这项检查。

路径列表通过 YAML anchor `&release_tooling_paths` 统一声明，再供 `pull_request:` 引用，避免 push 与 PR 的过滤范围不一致。`workflow_dispatch` 也需要保留：带 `[skip ci]` 的依赖同步不会触发 push 检查，冻结候选仍需要能够按需取得对应 SHA 的发布证据。

> **两个路径过滤器，两种不同的陷阱。** `android-core-arm64.yml` 与
> `release-contract-verify.yml` 都收窄了 push 触发器。因此新增一个顶层目录，
> 可能**静默丢掉**一次确实影响它的改动所触发的 Android ABI 验证或发布契约验证。
> 假设任一者已被检查之前，先看列表 —— 如果你添加了发布路径依赖的文件，
> 请在同一次改动中把它加进 `release_tooling_paths`。

### `verify.yml` —— 源码门禁，逐步（`verify.yml` — the source gate, step by step）

| 步骤 | 运行什么 | 证明 | 无法证明 |
|---|---|---|---|
| 发布门禁自检 | `scripts/ci/test-release-artifacts.sh` | 发布选择逻辑会拒绝坏证据 —— 针对一个 **mock `gh`** | 真实的 `gh`/API 也如此行为 |
| gofmt | `gofmt -l` 作用于 10 个目录 | 格式 | 任何语义 |
| gomobile 单一来源 | `test-gomobile-toolchain.sh` | 单一声明之外没有版本字面量 | 网络部分靠 `GOMOBILE_TOOLCHAIN_TEST_INSTALL=1` **可选加入**，此处未运行 |
| 嵌套模块完整性 | `check-go-module-integrity.sh --require-checked test` | `test` 模块解析到与根相同的 fork 固定值 | 运行时行为 |
| test 模块类型检查 | 在 `test/` 内运行 `go vet` | 嵌套模块能针对**本**树编译 | 测试通过 |
| 构建标签策略 + linkname | `go test -count=1 ./cmd/internal/...` | 标签组合与 `-checklinkname=0` 契约 | — |
| 固定上游假设 | `scripts/ci/verify-upstream-assumptions.sh` | 改变*含义*的上游修订变更 | — |
| **无标签构建** | `go build ./...` **以及 `go test -run '^$' ./...`** | 每个包在无标签下都能构建，且**每个测试二进制都能编译并链接** | **任何测试会 PASS** —— `-run '^$'` 运行零个测试 |
| 带标签构建 | `go build -tags … ./...` | 在服务器 profile 下可链接 | — |
| 构建矩阵 | 为 8 个包树交叉构建 linux/amd64、windows/amd64、freebsd/amd64、darwin/arm64 | 那些包按 GOOS 编译 | 什么都不运行 |
| cronet 来源 | `verify-cronet-provenance.sh apple` | `replace` 指令解析到 fork 归档的字节 | — |
| **race，fork 数据路径** | `go test -race … ./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/... ./protocol/mieru/... ./protocol/tun/... ./route/... ./adapter/...` | 该任务运行时指定树中的 race 检查 | **根包不在列表中**，`./protocol/masque` 也不在 |
| race，配置表面 | `go test -race … ./option/...` | `option` 中的竞态 | — |

> **历史 v0.1.9 观察：** 根验收套件只被编译，当时声明的源码门禁并没有执行它。
> 当前 `verify.yml` 在 `cd test` 之后运行 `TestNetLogEventCount`：那属于独立的测试模块，
> 不是本仓库的主根包。把根验收覆盖归因于它之前，请先检查当前命令与
> 确切运行记录。历史本地命令见 §4。

发布路径只用显式选定运行的证据。不要说每个产物工作流都只能手动派发：
`android-core-arm64.yml` 也声明了到 `testing` 的受路径过滤 push，而它的注释把用于
产生发布证据的手动派发单独区分出来。请检查每个工作流当前的触发器与实际运行，
不要从一次合并推断执行。

---

## 2. 门禁接线 —— 什么在运行、什么被声明、什么仅仅是存在（Gate wiring — what runs, what is declared, and what is merely present）

**以下核对针对 `v0.1.9` 基线。** 旧版曾将十二个脚本列为没有运行入口，但其中四个在该基线上已有调用方。下表列出几项更正；工作流有可能继续变化，使用前应重新核对目标版本。

| 脚本 | 先前的说法 | 现在实测 |
|---|---|---|
| `verify-release-acceptance.sh` | “在一条注释中被提及，从未被调用” | 独立的自检由 `release-contract-verify.yml` 调用；`verify.yml` 当前在注释里提到这个检查器，而不是直接调用 |
| `verify-fork-handoff.sh` | “同上 —— 只有注释” | **被调用** —— `verify.yml` |
| `check-macos-app-name.sh` | “只有注释” | **被调用** —— `client-apple.yml` |
| `check-macos-client-config.sh` | “只有注释” | **仍然只有注释** —— `client-macos.yml:46` 是一条注释，且没有工作流或脚本调用它 |

### 旧列表所混淆的那一区分，以及它为何仍然重要（The distinction the old list collapsed, and which still matters）

`verify-release-acceptance.sh` 是一个容易误读的例子：它已被调用，但**不是发布路径上的门禁**。`release-contract-verify.yml:311` 中的步骤只是自检，验证严格验收检查器仍会拒绝 18 个 API 快照中的无效证据；发布路径本身没有调用该验收引擎。

因此有**三种不同状态**，把任意两种混为一谈都会得出错误答案：

```text
WIRED        something in a triggered workflow invokes it      -> a red run is possible
DECLARED     a workflow comments that it is a gate             -> a red run is NOT possible
PRESENT      the script exists and nothing references it       -> nothing happens at all
```

因此，`verify-release-acceptance.sh` **已接入自检，但验收引擎没有接入发布决策**。发布路径以 `release.yml` 和 `release-artifacts.sh` 为准（§5）。不要把工作流对门禁的文字声明当成发布时确实执行该检查的证据。

### 在此基线上仍确实未接线的（Still genuinely unwired at this baseline）

`check-satelite-one-core-pin.py`、`test-apple-links.py`、`verify-cronet-ios-archive.sh`、
`run-jiejie-suite.sh`。

`check-libbox-abi.sh` 不属于未调用脚本：`verify.yml` 运行 `--pin-only`，通过 Git 比较 gitlink 与 `VERIFIED_*_SHA` 常量；`client-apple.yml` 则针对已检出的客户端源码运行完整检查。后者通过该脚本调用 `apple-abi-provenance.sh`、`apple-abi-probe.sh` 和 `probe-accessors.py`。声明调用不等于某次运行实际执行或通过，还需核对日志。

**在 Windows 表面审计中被删除**（`refactor/windows-upstream-minimal-surface`）：
`ci/windows/Test-DaemonServiceRegister.ps1` 与 `ci/windows/Test-InstallerLegacyAdoption.ps1`
都处于 PRESENT 状态 —— 脚本存在，却没有任何东西引用 —— 因此被移除，而不是当成死代码
留着。同一趟还检查了
`clients/desktop/scripts/test-legacy-service-ownership.ps1` 与
`test-install-directory.ps1`，两个都**保留**：前者被 `verify-desktop-identity-consistency.py:348`
引用，后者是安装目录宏唯一的回归覆盖。两者也都没有被任何工作流调用，因此仍是 PRESENT，
需要接线，而不是删除。

**历史 v0.1.9 清单；当前工作流引用请重新复检：** 当时没有任何东西引用的测试标签 ——
`trafficresearch`（`common/trafficsched/adaptive_research_test.go`）、
`p2ios`（三个 `experimental/libbox` 文件）、`tlsfragment_integration`
（`common/tlsfragment/integration_test.go`，还受 `t.Skipf` 门控）、`force_stdio`（只被一个
本身已死的 `Makefile` 目标引用）。**在那次历史审查中，这些文件里的测试函数从未在任何
已声明配置中运行过。**

**一个已死的 `Makefile` 目标** —— `make test` 跑不通：第一条命令不带标签，而 `TAGS_TEST`
（用在两行上）**在仓库里任何地方都没有定义过**，展开后为空。

重新检查时，不要只提取简单的 `run:` 行：多行 shell 步骤中调用的脚本可能被漏掉，例如 `server-linux-amd64.yml:742` 处的 `check-reference-coverage.sh`。旧列表也早于 `release-contract-verify.yml`，不能用于判断该工作流中的调用。应逐项检查目标版本的工作流和调用链。

---

## 3. `verify-evidence/` —— 按其自己写下的规则已过期（`verify-evidence/` — stale by its own written rule）

`verify-evidence/README.md:5-8` 写明了这个目录的完整性规则：

> *"…A log whose header names a different `HEAD` than the branch tip is stale and must be regenerated
> rather than trusted."*

（中文：……头部所指的提交与分支顶端不同的日志已经过期，必须重新生成，
不能直接信任。）

**13 份日志都记录 `### HEAD: cfc77dc51ab7322e993b8ae0b94b96db59e48f47`。** 该提交比 `a267e99` 落后 101 个提交，比当前基线落后 1,214 个提交；它是严格祖先，没有发生分叉。

不仅源码版本较旧，相关门禁文件也发生了变化：`git diff --name-status cfc77dc5 HEAD -- scripts/ci .github/workflows release clients`
返回 **15 个修改 + 1 个新增**文件，包括 `check-libbox-abi.sh`、
`check-apple-source-selection.sh`、`test-apple-signing.sh`、`apple-client-source.sh`、
`release-artifacts.sh`、`apply-desktop-branding-overlay.py`（自这份记录以来已退役 —— 静态
品牌现在位于固定的 desktop fork 中，`inject-desktop-release-version.py` 取代了它）、`build-apple-libbox.sh`
**以及 `clients/apple` 本身**。因此这些证据描述的既是一棵更旧的树，**也**是
它所覆盖门禁的旧版本。

`README.md:53-59` 还记录：修复前的记录被**有意删除**，免得这个目录
读起来像当前结果与被取代结果的混合 —— 这意味着复检者看不到
失败，只看得到修复后的通过。而 `check-libbox-abi.log` 的结尾是
`PARTIAL: … the call sites were NOT verified by this run`，`gates.tsv` 却把这一行记为退出码 0
—— 一道跳过却报告成功的门禁，正是
`check-go-module-integrity.sh:165-167` 指出的失败模式。

> **证据级别：`cfc77dc5` 处的 `VERIFIED_ARTIFACT`，且已过期。它不是针对 `a267e99` 的证据。**
> 生产基线上**只**靠 `verify-evidence/` 的任何东西，在生产基线上都是 `UNVERIFIED`。

### 历史 v0.1.9 审查中缺失的证据（Evidence absent from the historical v0.1.9 review）

本表记录那次审查在当时树里发现的内容。它不是“后来的 GitHub Actions 运行或外部证据
不存在”的主张；按范围限定的运行记录见上文，受审目标的相关运行请自行检查。

| 主张 | 在何处被宣告 | 实际存在什么 |
|---|---|---|
| 对若干分支线触及的 30 个包的压力测试结果 | `KNOWLEDGE/reports/release/BRANCH_CONSOLIDATION_TO_TESTING_FINAL.md:452-458` | **什么都没有。** 第 451 行字面上就是 `<!--STRESS_RESULTS-->`，下一节从 `:453` 开始。这是任何根报告中唯一的 HTML 注释占位符。 |
| 任何包的 `-race` 结果 | 所有报告 | 哪里都没有；一致报告为未运行，而本应产生它们的 CI 步骤在树里不留记录。 |
| Apple 子模块可达性 | `KNOWLEDGE/reports/release/CORE_0.1.6_FINAL_CLOSURE_REPORT.md:132-138` | 需要一台带这个子模块、并且有网络的主机。 |
| 设备 / 模拟器 / iOS / Android 运行时运行 | 各 Apple 报告 | 没有。`KNOWLEDGE/reports/release/APPLE-PARENT-FINALIZE-REPORT.md:198-203` 把门禁层记录为 `SKIP`，把整体裁决记录为 **`PARTIAL`**，因为有一层被跳过，*"which is deliberately not reported as PASS"*（中文：这一层有意不报告为 PASS）。 |
| 实时 Xray / quic-go / masque-go 互操作 | `interop-xray.yml`、`server-linux-amd64.yml` | 仅工作流声明。`verify-evidence/` 中没有产物。QUICHE 实时互操作测试已被**删除**（`0a3993a8c`）。 |
| Windows PE / MSI / Authenticode / 驱动签名 / 服务生命周期 | 九个 `scripts/ci/windows/*.ps1`、`client-desktop-windows.yml` | 仅声明。`windows-core-amd64.yml` 在 **ubuntu-latest** 上运行且只检查符号；桌面工作流只能手动派发；树里没有归档任何 `.ps1` 输出。 |
| Docker 支撑的 fixture | `test/docker_test.go`、`test/openvpn_test.go` | 未运行 —— 没有 daemon。注意 `test/docker_test.go` **没有跳过守卫**。 |

### 仓库自己的发布裁决（The repository's own release verdict）

历史审查时，`docs/RELEASE-CERTIFICATE.md` 开头是：

```text
NOT READY FOR A STABLE MILESTONE
```

并列出那次审查缺失的数据面与 Apple 签名证据。不要把这些历史缺失主张推广到更晚的
发布；请针对确切的目标与平台取证。

**这份文档的第三条主张现在已过期，而它最有可能被重复。** 文档
记录 `dns/transport/local.(*DBusResolvedResolver).loopUpdateStatus` 中一处*已知、未修复的* goroutine
泄漏，并据此把 `test/` 模块的 box 级测试排除在 push CI 之外。
**泄漏已修复** —— 由 `6b495134c` 修复，这个提交添加了 `signalLoopStop`/`signalLoopDone`/`signalLoopOnce`、一个
停止通道 `select`、`RemoveSignal`，以及 `Close()` 中的一次**汇合（join）**
（`local_resolved_linux.go:60-62`、`:188-189`、`:221-223`、`:356`、`:367`）。见 `dns-demand.md` §6。

> **两个后果：** 不要再报告这个泄漏，也**不要采用那份文档提出的修复** ——
> 关闭 dbus 信号通道会导致 panic，因为 dbus 是发送方。它据此论证过的 CI 排除
> 可以重新考虑；那是 CI 决策，不是知识决策。

这也是本模块那条一般规则的好例证：**本仓库里的一份文档是带日期的主张，不是事实。**
它引用的路径（`:347-349`、`:216`）已经不再指向它所描述的代码。

---

## 4. 复检者必须实际运行的命令（The commands a re-checker must actually run）

下面的块记录一次历史 Windows 环境搭建与测试选择。这些路径与标签选择
不是可移植的默认值，更晚的工作流也已把 `protocol/mieru` 纳入 race 列表。复用之前，
请先确定当前的 Go 工具链、目标 profile 与工作流命令；本地 2026-10-28 的
pin-only 检查用的是 MSYS2 bash，记录在 `ACCEPTANCE.md` 中。

```powershell
# --- environment (Windows amd64) ---
$env:GOROOT='C:\src\_toolchain\goroot'; $env:GOPATH="$env:USERPROFILE\go"
$env:GOMODCACHE="$env:GOPATH\pkg\mod"; $env:GOTOOLCHAIN='local'; $env:CGO_ENABLED='0'
$env:PATH="C:\src\_toolchain\goroot\bin;C:\src\MinGit\cmd;$env:PATH"
Set-Location 'C:\src\kb-stage2'
$OTHERS = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
$ALL    = (Get-Content release\DEFAULT_BUILD_TAGS -Raw).Trim()

# 1. THE ROOT DESIGN-GUARANTEE SUITE. Correct tag set is OTHERS, not DEFAULT_BUILD_TAGS.
#    DEFAULT_BUILD_TAGS carries with_naive_outbound, whose cronet dependency cannot build here.
#    Measured at the release baseline: ok, 5.459 s, exit 0.
go test -count=1 -timeout 10m -tags "$OTHERS" .

# 2. Named contract tests only
go test -count=1 -tags "$OTHERS" -v -run 'TestFalseDenyMatrix|TestConsumerDemandContractIsProvableOnly|TestTheCrossKindGraphReadsPublishedAdapterFacts|TestDestinationDNSOwnershipIsVacuousForEveryLocalDialer' .

# 3. Lifecycle ordering + stress + goroutine census
go test -count=1 -tags "$OTHERS" -v -run 'TestTheReuseVerdict|TestTheSleepEdge|TestOneHundred|TestNoResurrectionAfterClose|TestTheBridgeUnderConcurrent' .

# 4. Subsystem suites that are green untagged (measured)
go test -count=1 ./common/sniff/... ./common/runtimecoord/... ./common/physicalpath/... ./adapter/... ./route/... ./dns/... ./experimental/libbox/... ./common/dialer/...

# 5. MASQUE + bufgeom NEED tags. Measured: 412 s, 1317 PASS / 0 FAIL / 0 SKIP, exit 0.
#    (bufgeom 0.09 s, transport/masque 8.0 s, protocol/masque 408 s — the last is dominated by
#     TestMasqueRestartCyclesDoNotLeakLoops at 240 s and TestMasqueLifecycleKeepsOneLoop at 150 s.)
go test -count=1 -timeout 15m -tags "$OTHERS" -v ./common/bufgeom/... ./transport/masque/... ./protocol/masque/...

# 6. The scheduler (slow: 106 s, dominated by four contention experiments)
go test -count=1 ./common/trafficsched/... ./common/trafficclass/...

# 7. Race — needs cgo; BLOCKED on a host without a C compiler
go test -race -count=1 -timeout 30m -tags "$ALL" ./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/... ./protocol/tun/... ./route/... ./adapter/... ./option/...

# 8. Gates — every *.sh needs bash, absent in a MinGit-only environment
bash scripts/ci/verify-upstream-assumptions.sh
bash scripts/ci/check-go-module-integrity.sh --require-checked test
bash scripts/ci/verify-cronet-provenance.sh apple
bash scripts/ci/check-apple-source-selection.sh
bash scripts/ci/check-libbox-abi.sh --pin-only  # wired in verify.yml; full gate in client-apple.yml
```

**本次审计实测，以便复检者知道“预期”是什么样：**

| 命令 | 结果 |
|---|---|
| `go build ./...` | **退出 0** |
| `go build -tags "$OTHERS" ./...` | **退出 0** |
| `go build -tags "$ALL" ./...` | **退出 1** —— cronet 构建约束，符合设计 |
| `go test -count=1 .` | **退出 1** —— 12 个标签缺失引起的失败 |
| `go test -count=1 -tags "$ALL" .` | **环境搭建失败** —— 测试二进制无法构建 |
| `go test -count=1 -tags "$OTHERS" .` | **退出 0**，`ok 5.459 s` |
| `go test -count=1 ./...`（无标签，168 个包） | **退出 1** —— 78 ok / 3 FAIL（`root`、`protocol/masque`、`common/tls`）/ 87 个无测试文件 |
| `go test -count=1 ./common/sniff/...` | **退出 0**，`ok 5.83 s`；文档记载的偶发失败**未**复现 |
| `go test -count=1 ./experimental/libbox/...` | **退出 0**，`ok 0.708 s`；verbose 模式 39 PASS / 0 FAIL / 0 SKIP |
| `go test -count=1 -tags "$OTHERS" -v ./common/bufgeom/... ./transport/masque/... ./protocol/masque/...` | **退出 0**，412 s，1317 PASS / 0 FAIL / 0 SKIP |
| `go test -count=1 ./common/trafficsched/... ./common/trafficclass/...` | **退出 0**，106.4 s / 0.03 s；51 个测试，0 跳过 |
| `go test -race …` | **BLOCKED** —— `go: -race requires cgo` |

**复发时需要调查的历史失败与偶发失败：** 先前的观测不能证明原因，也不能排除在其他
目标或主机上出现新的回归。

| 项目 | 历史观测 | 范围与后续 |
|---|---|---|
| `common/tls` `TestWindowsClient*TLS13` | 本地握手测试在这台 Windows 主机上以 `unexpected EOF` 失败；个别用例可复现 | 原因未知；不要从这次观测推断缺少 TLS 1.3 支持，也不要推断是远程网络故障 |
| `common/sniff` `TestAgentFFrameShape*`、`TestTheSniffCensusMovesForExactlyOneLeakedGoroutine` | **对负载敏感**；失败测试的名称每次运行都不同 | 在修复版与未改动基线上各自隔离运行均有 5/5 通过的记载。此处一次绿色运行**并不**证明非偶发性。 |
| `protocol/group` `TestRecheckSingleFlightCollapsesABurst` | 对负载敏感 | 在两棵树上连续 20 次隔离运行均干净 |
| `common/trafficsched` 在 `-count=10` 下 | 约 18 分钟的人为 sleep 会触到超时 | 每个单独的测试都通过 |
| `protocol/masque` `TestMasqueRestartCyclesDoNotLeakLoops` | **单独 330 s** —— 慢，但没有挂死 | 已实测 |
| `common/trafficsched` 墙钟时间 | 106.2 s 对 106.4 s 基线 | 已实测 |

**与主机相关的那一组复现不了。** 报告把 `common/windivert`、`common/tlsspoof` 与
`common/tls` 称作“3 个既有的环境性失败”。此处实测：`common/windivert` → **25 PASS /
0 SKIP / 0 FAIL**，`common/tlsspoof` → **9 PASS / 0 SKIP / 0 FAIL**，包括报告称为
`windivert: open SCM: Access is denied` 的每一个 `TestIntegration*`。应这样理解：
这条主张**与主机相关，与树无关** —— 它描述的是某台机器的 WinDivert/管理员
状态，换一台主机就不成立。

---

## 5. 发布来源（Release provenance）

### 资产列表的单一权威（The single authority for the asset list）

**`scripts/ci/release-artifacts.sh` 拥有规范的公开资产列表，而且只在一个函数里推导它。**
这条规则是 `v0.1.8` 发布缺陷换来的：

> **一个事实 = 一个所有者（ONE FACT = ONE OWNER）。** `public_asset_names()`（`release-artifacts.sh:1706`）遍历
> `TARGET_WORKFLOWS`，为每个目标输出 `tarball` 名称加两个非 tarball 产物。四个
> 子命令都消费它，没有谁重新推导：`expected-assets`（`:1723`）、`check-upload`
> （`:1830`）、`publish-args`（`:1754`）与 `verify-public-release`（`:1798`）。

**出了什么问题，因为这是要避免重复的失败模式。** `v0.1.8` 发布时只有**六**个
资产，契约却要求**七**个（缺 `Libbox.xcframework.tar.gz`）。原因
不是缺少检查 —— 而是**对一个事实有三个权威**：`check-upload` 从
`TARGET_WORKFLOWS` 推导列表，通过了；`release.yml` 的 `gh release create` 带着一份含六个
文件名的*硬编码字面量*，上传了六个；`release.yml` 的回读又带着*同样的六个字面量*，于是
六个对六个，工作流变绿。**自洽地错误。** 只统计数量的检查
发现不了这一点，所以回读**按名称**比较。

现在禁止这个工作流持有第二份副本：`check-release-workflows.py` 断言
发布步骤*从* `publish-args` *取*列表，而且其中**没有**任何字面资产文件名。
把字面量加回来就是回归到那个确切的缺陷，检查器会失败。

### `release.yml` —— 草案优先，以及为什么顺序就是安全属性（`release.yml` — draft-first, and why the order is the safety property）

只支持 `workflow_dispatch`，要求 `inputs.tag`；`dry_run` 默认 **false**。**十七个步骤**，
顺序如下，而这个顺序本身至关重要：

```text
 1 check out the tagged commit                    (the SHA is the identity)
 2 check out the release tooling at that revision
 3 bind the tooling identity to the released commit
 4 require exact-SHA source and tooling evidence
 5 upload the evidence record
 6 resolve and validate the tag                   check-tag
 7 resolve the release version                    release-version
 8 decide whether this run may publish            publish-flag: anything but true/false dies
 9 select the CI runs for this commit             plan: ONE successful run per product workflow,
                                                  for THIS exact SHA
10 download the verified artifacts                from the SELECTED runs, not a fresh query
11 stage the Windows desktop installer
12 verify the downloaded artifacts                per platform: binary + .sha256 + BUILD-INFO, a real
                                                  sha256sum -c, platform read from the file's own
                                                  header, recorded commit must match
13 flatten the release assets
14 confirm the upload set is exactly the public contract        check-upload
─── the irreversible half ───
15 create the release (draft) and upload the canonical asset set   --draft, assets from publish-args
16 verify the draft carries exactly the public contract             verify-public-release
17 publish the draft as the stable release                          gh release edit --draft=false
                                                                    then verify-public-release AGAIN
```

**`publish-args` 是全有或全无。** 它验证契约中的每个资产都在磁盘上，失败时
**什么都不打印**，因此部分列表永远到不了 `gh release create`。这是
有意的属性，不是实现细节：验证时逐步打印路径，会让发布
带着失败之前恰好检查过的那些东西被创建出来。

**草案优先正是有缺陷的发布没被公开的原因。** 发布先创建为
**草案**，契约**按名称**验证这个草案，只有这时才发布。因此
缺一个资产，会停在公众看不到的草案上。`v0.1.8` 是反例：它
不是草案优先，六资产缺陷在任何东西把它与契约比较之前就已经公开了。

> **因此，“产品 CI 生产/证明产物；发布下载并重新验证那些确切的
> 字节，而不是重新构建它们”这一主张，只是限定于 `plan` 所选择平台的源码级描述。
> 请检查每个产者的触发器与所选运行：Android 也有受路径过滤的
> testing-push 触发器，所以并非每个产者都只能手动派发。

**平台覆盖。** `release.yml` 设置 `RELEASE_EXTRA_PLATFORMS: true`，`release-artifacts.sh`
只接受字符串 `true`/`false`（其他任何值都会终止）。设置之后，**Windows 与 Android
产物确实在发布集合中**；Apple IPA/DMG 仍被排除。

### 本模块记录过的一个缺陷 —— 现已修复（A defect this module documented — now FIXED）

本节早先的修订描述过一个**活动缺陷**：发布树的允许列表按每个目标构建成四个名称，
从未为 Windows 与 Android 产者上传的额外文件扩展，因此只要
`RELEASE_EXTRA_PLATFORMS=true`（发布工作流自己就会设置它），`verify` 就会以
`unexpected file(s) in the release tree` 终止。

**这在 `v0.1.9` 基线上已修复。** 允许列表现在有一个 Apple 分支
（`release-artifacts.sh:1378`、`Libbox.xcframework.tar.gz SHA256SUMS-LIBBOX.txt manifest.json
BUILD-INFO-LIBBOX.txt`）与按目标的各行并列（`:1381`），`die` 移到了 `:1396`。同一文件里
另外两处过期陈述也一起更正：说 Windows/Android *"deliberately NOT here"*（中文：有意不在此处）
的头部注释现在记录了自己曾这样说（`:79`），打印*"both platforms"*（中文：两个平台）的
循环现在解释了为什么那是错的（`:1403`）。门禁自己的自检扩成了一份**六条目**的规范
`SHA256SUMS`（`test-release-artifacts.sh:1909-1930`），旧 fixture 只断言两行，根本捕获不到
这个缺陷。

**这里把缺陷记为已修复而不是删掉记录，因为这种形状会复发：** 允许列表与产者的
上传集合是对同一事实的两种陈述，它们的失败方式正是彼此一致
却两者都错。

### 不在范围内的是什么（What is out of scope）

`scripts/ci/verify-release-acceptance.sh` 强制一套**默认的必需工作流集合**，即
`verify.yml`、`interop-xray.yml` 与 `server-linux-amd64.yml`。**`client-apple.yml` 不在这套
集合里。** `verify.yml` 现在跑 Apple ABI 门禁的 **pin-only** 一半，完整门禁在
`client-apple.yml` 中运行；两者都能捕获 gitlink/常量不一致，但发布验收本身在
结构上仍然注意不到 Apple 固定值漂移，因为**发布路径根本不调用**
这个引擎；发布路径的权威是 `release.yml` + `release-artifacts.sh`。

### Apple 源码权威 —— 在此基线上被反转（The Apple source authority — INVERTED at this baseline）

**本模块早先的修订写了与事实相反的内容，所以请把这里读作一次更正，
而不是一次新增。** 它说 refs 文件*"declares the `clients/apple` gitlink as the sole
authority"*（中文：把该 gitlink 声明为唯一权威），而且这个 refs 文件*"forbidden from recording a SHA"*
（中文：被禁止记录 SHA）。那是更旧的模型。
`release/apple-client-refs.env` 现在自己的说法是：

> *"the authority is the client repository: its tip is resolved to an exact SHA at the start of a"*
> build —— `apple-client-refs.env:31`

（中文：权威是客户端仓库：构建开始时把它的顶端解析成一个确切的 SHA。）

以及

> *"The submodule and its gitlink still exist and still work: the local default checkout, the … [gitlink
> is no longer the] authority, and a published build is no longer asserted to equal the gitlink — that
> assertion is precisely what made the pin manual. Set `APPLE_CLIENT_REQUIRE_GITLINK=1` to get [the hard
> equality back]"* —— `apple-client-refs.env:42-45`

（中文：子模块及其 gitlink 还在，也仍然可用：本地默认检出，……[gitlink 已不再是]权威，已发布的
构建也不再被断言等于该 gitlink —— 正是这条断言让固定变成手工操作。要恢复[硬性相等]，
请设置该环境变量。）

**为什么反转。** 在 gitlink 权威模型下，每次客户端发布都是
**双仓库操作**：构建只能镜像别人已经手工提升的子模块，因此每个客户端提交都要在这里
再有一次提交和一次推送。工作流直白地写明了替代模型：

> *"The AUTHORITY is the Apple client repository, not this repository's gitlink, so this gate compares
> the checkout against the RESOLVED source rather than against the gitlink. Requiring the gitlink made
> every client release a two-repository operation."* —— `client-apple.yml:268`

（中文：权威是 Apple 客户端仓库，不是本仓库的 gitlink，因此门禁比较的是检出
与已解析的源码，而不是检出与 gitlink。要求 gitlink，曾让每次客户端发布都成为双仓库操作。）

**现在的两个权威：**

| | 权威 | 如何使用 |
|---|---|---|
| **构建选择** | **客户端仓库顶端**，构建开始时解析为确切 SHA | 已发布的 Apple 产物实际由什么构建 |
| **Gitlink**（`clients/apple`） | 本地默认检出；**不是**构建权威 | 与已解析源码相等只是一条**备注**，不是失败 |
| `APPLE_CLIENT_REQUIRE_GITLINK=1` | 可选加入 | 恢复**硬性** gitlink 相等（`client-apple.yml:319`、`:455`） |

**实测的固定值。** `scripts/ci/check-libbox-abi.sh` 中的
`VERIFIED_APPLE_SHA="044e6d5447eb1285c950bd12cee10effab641f5a"`，与 `git ls-tree HEAD clients/apple` 是**同一个提交**。
这个值在 2026-10-27 之前是 `6e559a4`，之后在 v0.1.28 路径监视器金丝雀中是 `3aedd741`，
v0.1.34 Apple 集成中是 `044e6d5`；常量与 gitlink 一起移动。但 gitlink 已不再
*选择*构建，**这种一致性是比本模块早先声称的更弱的证据**
—— 它只说明两者被人为地一起移动，不能证明构建用了它。

### `check-apple-source-selection.sh` 已被重写并且确实已接线（`check-apple-source-selection.sh` was rewritten and IS wired）

早先的修订说这个脚本*"could not have caught [pin drift] **by design** — every one of
its checks is self-referential to the gitlink"*（中文：**按设计**无法捕获[固定值漂移] —— 它的每一项检查
都自指于 gitlink），而且**没有任何东西运行它**。这两半现在都错了：

* **它已接线** —— `client-apple.yml:265` 运行它（`./scripts/ci/check-apple-source-selection.sh`）。
* **它被重写**，断言的恰恰是旧文本所假设内容的*反面*。单副本
  方案检查保留了下来（`:124-129`“refs 文件不记录 SHA”、`:209-210`“两个 SHA 都不得被硬编码”），
  因为这些检查仍能防止*重复*权威 —— 但它现在还断言**移动 gitlink 不会移动
  iOS 源码**，以及**丢弃 gitlink 不会改变已解析的
  源码**。这正是反转后的权威模型所依赖的属性。

**实测：** 审计时在本机运行了脚本，结果为 **72 通过、0 失败**，退出码 0。这些断言属于 `VERIFIED_UNIT`，但只能证明脚本检查到的关系，不能证明解析出的修订就是使用者实际期望的版本。

因此，不能再说 Apple 固定值完全没有验证器：源码选择器及其测试已有调用方。剩余的限制是，这些检查不能独立证明解析出的修订就是使用者预期的版本。

### refs 守卫：它读取的是调用方环境，而不是文件（The refs guard: it read the caller's environment, not the file）

`scripts/ci/apple-client-source.sh` 在 source refs 文件之后，**在环境里**测试
`MACOS_APPLE_SHA` / `MACOS_APPLE_BRANCH`，判断这个文件是否被改回了
双来源模型。它分不清文件里的赋值与调用方的赋值，而调用方合法地
同时带着这两个名称：

```text
apple-client-source.sh resolve   emits MACOS_APPLE_SHA / MACOS_APPLE_BRANCH as historical aliases
                                 (:547-548, so an eval'ing caller survives `set -u`)
release-apple.sh                 exports them on to the builders                (:181)
```

于是 `resolve -> export -> assert-shared-source` 拒绝了 `resolve` 片刻之前刚打印的值，
把责任推给一个并不含这一行的 refs 文件。**`release-apple.sh testflight` 在它自己的输出上死锁**
—— 这是真实的发布阻塞，不是文档缺陷。`ece0fb9d2` 把那个测试换成
`refs_declares_platform_source()`：一个在 source **之前**先问**文件**的文本测试，因此
原始意图（“每个子命令都必须拒绝双来源的 refs 文件”）保留了，而答案不再
取决于谁调用它。refs 文件仍然不得声明按平台的来源；`export
NAME=` 这种写法也被拒绝。

证据：守卫的文本是 `VERIFIED_SOURCE`（`:88-105`）；行为是 `VERIFIED_UNIT` ——
守卫直接用十个用例进行了检验（三个是修复前守卫错误拒绝的、四个是它必须保留的拒绝、
三个是失败即拒绝/注释的近似命中），另有三个旧守卫对照用例
复现那次死锁。十三个全部通过。已发布的脚本本身需要 `bash`，审计主机上没有，因此
端到端运行 `apple-client-source.sh` 是 `BLOCKED_BY_ENVIRONMENT`。

---

## 6. 构建 profile（概要 —— 完整细节见 `build-pruning-release.md`）（Build profiles (summary — full detail in `build-pruning-release.md`)）

| profile | 内容 |
|---|---|
| `DEFAULT_BUILD_TAGS` | **Darwin** profile。包含 `with_naive_outbound`；当前 Windows Cronet 文件还要求 `with_purego`。 |
| `DEFAULT_BUILD_TAGS_OTHERS` | 同上**减去** `with_naive_outbound`。**这是通用/服务器集合。** |
| `DEFAULT_BUILD_TAGS_WINDOWS` | `DEFAULT_BUILD_TAGS` 加上 `with_purego` |

`verify.yml:337-338` 在树内声明 `DEFAULT_BUILD_TAGS` *"deliberately NOT used here: it
carries with_naive_outbound, and no Linux product links cronet."*（中文：有意不在此处使用：它
带 with_naive_outbound，而没有任何 Linux 产品链接 cronet。）这些名字有误导性，仓库
自己也知道。历史
`KNOWLEDGE/reports/release/CORE_0.1.6_FINAL_CLOSURE_REPORT.md` 中记录的 Windows 环境搭建失败，是对那个环境与
修订的证据，不能当作“当前 Cronet 缺少所有非 Apple 目标”的主张。当前固定 fork 的平台标签
与 Windows purego 测试的范围，见 `build-pruning-release.md`。

**两个 profile 只差一个条目。** 实测，逐字：

```text
DEFAULT_BUILD_TAGS        with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,
                          with_tailscale,with_ccm,with_ocm,with_cloudflared,with_naive_outbound,
                          with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname
DEFAULT_BUILD_TAGS_OTHERS … the same list with with_naive_outbound removed
```

> **`docs/BUILD-PROFILES.md:43-44` 说*客户端* profile“添加了 `with_utls`”。那是错的：**
> `with_utls` 在**两个**文件里都有。`with_naive_outbound` 是唯一的差异。相信
> 那句话的读者会把一次标签改动的作用范围定错。

---

## 7. AI 注意事项（AI cautions）

1. **不要把一项声明变成 `VERIFIED_CI`。** 引用确切的运行、SHA 与观测到的结果；
   上面链接的更晚运行是按范围限定的证据，不能证明每个工作流或产物。
2. **不要为生产基线引用 `verify-evidence/`。** 它自己的 README 就取消了它的资格。
3. **不要从仅编译的 CI 或另一个被过滤的根测试推断根 `box_*_test.go` 套件运行过。**
   请检查当前命令与它的运行记录。
4. **选择平台的实际标签 profile。** `DEFAULT_BUILD_TAGS_OTHERS` 省略 Naive；
   `DEFAULT_BUILD_TAGS_WINDOWS` 包含它并添加 `with_purego`。依赖与原生库
   是否可用，仍是独立的构建/运行前提。
5. **不要从无标签运行推断功能覆盖。** 部分 MASQUE 测试要求 `with_quic`；
   根包检查使用相应的 profile。请诊断实际失败，不要假设
   是回归或标签问题。
6. **不要把 `SKIP` 当作通过。** 本 fork 的跳过措辞（“NOT a pass”）是有意的。
7. **不要把一个平台的 PASS 当作全平台 PASS。**
8. **不要把 Apple ABI 状况描述为“一道坏掉的门禁”。** 见 §5。
9. **不要把 `release.yml` 描述为覆盖所有平台。** 它覆盖的是 plan 步骤所选择的内容，
   在 4–5 次手动派发之后。
10. **不要假设存在的脚本就是会运行的门禁。** 请先检查可达性。
11. **在实际目标与环境上调查复发的失败。** 历史偶发失败是
    背景，不能证明“新失败无害或原因相同”。
12. **不要在未检查 `android-core-arm64.yml` 的路径过滤器时就添加顶层目录。**
13. **不要静默删除 `<!--STRESS_RESULTS-->` 式的占位符。** 未填充的占位符是一条
    “这项证据从未被产生”的记录；那是信息。
14. **不要把本 fork 的 `docs/changelog.md` 呈现为本 fork 的变更日志。** 它是上游的。
15. **不要只读一份账本（ledger）的正文而不读它的横幅。** `docs/fork/v016-final-closure-ledger.md`
    在 `:465` 带有一节声明 *"HY2 ChromeParrot is BLOCKED on the pinned quic-go fork"*（中文：HY2 ChromeParrot 在固定的
    quic-go fork 上被 BLOCKED），而它自己的头部在 `:4-13` 恰好撤回了这一说法，声明
    `Config.InitialPacketSizeCeiling` 是在 ChromeParrot 覆盖**之后**应用的，因此一个被证实的 1232
    会到达线缆，并指向 `protocol/hysteria2/hard_ceiling_wire_test.go`。跳到
    这一小节的读者会重开一个已关闭的发现。这是这里每一份长账本的普遍形状：
    **横幅是当前的，正文是记录。**
