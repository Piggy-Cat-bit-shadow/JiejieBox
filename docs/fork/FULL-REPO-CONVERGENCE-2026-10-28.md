# 全仓库上游融合、分支收敛与发布前依赖冻结 — 施工交接报告

- 施工日期：2026-10-28
- 主仓库：`Piggy-Cat-bit-shadow/sing-box`，产品分支 `testing`
- 起点：`a0bbf666`（v0.1.33）；本报告在每个仓库的"结束 SHA"一节给出最终提交
- 性质：实际实施记录。每一项都注明证据级别；**没有执行的验证一律标 `NOT_RUN`，不用"其余正常"概括**

---

## 1. 执行摘要

这一轮解决的不是"某个 Pin 落后了一次"，而是**同一个源码事实被三套互不相见的规则决定**：

| 源码 | 由谁决定 | 出过的具体后果 |
|---|---|---|
| 六个 Go Fork + cronet 31 个模块 | 根 `go.mod` 的 replace | 与 `test/go.mod` 可能不一致；`utls` 少 3 个上游提交 |
| 三个客户端 | superproject gitlink | v0.1.33 的 Windows 安装包漏掉 Desktop 的订阅 UA 修复 |
| Apple 客户端 | `apple-client-refs.env` 在构建开始时解析 `main` | 同一个版本里 gitlink 一个 SHA、实际产物另一个 SHA |
| Desktop 的 dashboard | Desktop 仓库内部的嵌套 gitlink | 落后 dashboard 主线 2 个提交 |

做完的事：

1. **uTLS**：镜像 `mirror/meta-v1.9.0` 快进到真实上游 `a6a364af`（只快进），`main` 用常规 merge `3ab4752` 吸收同一条线的 3 个提交；新增 2 个回归测试（`2da328d`），两条都先在旧镜像上验证会失败。消费方 `go.mod` / `test/go.mod` 同步更新。
2. **Android**：五条分支收敛为唯一产品 `main`。`dev` → 合入 fix 分支的 3 个 StringBox ABI 提交 → 合入 SagerNet `dev` 的 12 个提交（其中 3 个此前已等价覆盖、2 个是版本元数据、7 个真正吸收）。GitHub 默认分支改为 `main`，四条旧分支删除并留 `archive/*` 标签。
3. **Apple**：`main` 合入上游 13 个提交（7 个真吸收、2 个已覆盖、4 个元数据跳过）；已合并的 `fix/compact-outbound-mode-ui` 删除。
4. **Desktop / Dashboard**：dashboard→desktop→内核 顺序更新；`clients/desktop` 从 `eb2dbf8` 前移到 `6d4ccf8`（含订阅 UA 修复与 dashboard 新 Pin）。
5. **发布前依赖冻结（最高价值产出）**：新增 `release/dependencies.json`、`scripts/ci/release-freeze.py`、`.github/workflows/release-prepare.yml`。它解析 11 个依赖仓库的真实分支头、与树里实际 Pin 比对、只把"落后"的 Pin 前移（分叉一律 fail closed）、从**实际 Pin** 生成 `release/source-lock.json`，在拿到候选提交的 exact-SHA CI 证据后才允许打 Tag。
6. **11 仓库全量审计 + 分支分类**，`sing-box` 的 `pending/device-axis-reload-window-tests` 经复核后保留（理由见 §4）。

`release-prepare.yml` 已经能在 dry run 下跑完整条链路；是否在本轮真实打 Tag **故意不做**（本轮目标是建立流程，不是发布新版本）。

---

## 2. 11 仓库全量审计表

"上游关系"一列是 2026-10-28 重新 fetch/compare 的实际结果，不是文档里的旧数字。

| # | 仓库 | 产品分支 | 开始 SHA | 结束 SHA | 上游目标 | 与上游关系 | 本轮变更 | 分支状态 | 消费方 Pin 前 → 后 | 验证级别 | 远端 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | `sing-box` | `testing` | `a0bbf666` | `e814b2ba`（本报告所在提交；Apple Gitlink 未动，见 §7.1） | `SagerNet/sing-box:testing` `e5d91fa7` | behind 0 | 6 个提交：utls Pin、android gitlink、desktop gitlink、发布冻结工具链、报告、冻结工具修复 | `testing` 保留；`pending/...` 保留（§4） | utls / android / desktop 三个 Gitlink 与 replace 前移 | `go build` 双模式 PASS；`check-go-module-integrity.sh` PASS（见 §5） | 已推送并回读 |
| 2 | `sing` | `dev` | `1729220c` | 未变 | `SagerNet/sing:dev` `03a6cce6` | behind 0 / ahead 55 | 无 | 单分支 | `v0.9.6-0.20261027185736-1729220c46c4` 不变 | 复核结论 | — |
| 3 | `sing-quic` | `main` | `1ef2597c` | 未变 | `sagernet/sing-quic:main` `7e0a5869` | behind 0 / ahead 7 | 无 | 单分支 | `v0.7.2-0.20261024181351-1ef2597cc129` 不变 | 复核结论 | — |
| 4 | `sing-tun` | `dev` | `8b9a3800` | 未变 | `SagerNet/sing-tun:dev` `5b0a1a7a` | behind 0 / ahead 98 | 无 | 单分支 | `v0.0.0-20261028061444-8b9a38008705` 不变 | 复核结论 | — |
| 5 | `quic-go` | `sagernet` | `2daa76f5` | 未变 | 最新 mod tag `v0.61.0-sing-box-mod.12` | behind 0 / ahead 18 | 无 | 单分支（SagerNet 模块线，未动 MetaCubeX 线） | `v0.61.1-0.20261027185738-2daa76f5bd52` 不变 | `gh api compare` | — |
| 6 | `cronet-go` | `main` | `c9c0e6ec` | 未变 | `SagerNet/cronet-go:main` `049f2909` | 上游是祖先，behind 0 | 无（29 个原生模块 Pin 仍为 `8c68ce89873c`） | 单分支 | 根/all `c9c0e6ec`，29 lib `8c68ce8` 不变 | `merge-base --is-ancestor` = True | — |
| 7 | `utls` | `main` | `1bcb8e4d` | `b63a2e9a` | `MetaCubeX/utls:v1.9.0-mod-meta` `a6a364af` | merge 后 behind 0（原 behind 3 / ahead 65） | 镜像快进、merge `3ab4752`、回归测试 `2da328d`、UPSTREAM.md `b63a2e9` | `main` + 镜像 `a6a364af` 均保留 | `v1.8.8-0.20261025012215-1bcb8e4d74d2` → `v1.8.8-0.20261028133053-2da328dfb92f` | 见 §5.1（含负向证明） | 已推送并回读 |
| 8 | `sing-box-for-apple` | `main` | `16cf87b4` | `be3258a`（候选，未合 `main`） | `SagerNet/sing-box-for-apple:dev` `0d1b59fc` | 候选分支 merge 后 behind 0（原 behind 13 / ahead 178）；产品 `main` 未动 | 13 个上游提交在候选分支；`fix/...` 删除 | 只剩 `main` + `sync/upstream-2026-10-28` | `aeccc754` → `16cf87b4`（fork 自己的 UA 修复，即 v0.1.33 实际构建的源码） | 见 §5.3 | 已推送并回读 |
| 9 | `sing-box-for-android` | `main`（原 `dev`） | `4085557`（dev） | `c7837107` | `SagerNet/sing-box-for-android:dev` `a548a790` | merge 后 behind 0（原 behind 12 / ahead 5） | 收敛 5 条分支；12 个上游提交 | 只剩 `main`，另有 4 个 `archive/*` 标签 | `9d15717a` → `c7837107` | 见 §5.2（ABI 扫描 309 文件 0 违规） | 已推送并回读 |
| 10 | `sing-box-for-desktop` | `main` | `d9231d9e` | `6d4ccf8c` | `SagerNet/sing-box-for-desktop:main` `8aab8eab` | behind 0 / ahead 35 | dashboard 嵌套 Pin `d452c24e` → `5e9271e5` | 单分支 | `eb2dbf8b` → `6d4ccf8c` | 见 §5.4 | 已推送并回读 |
| 11 | `sing-box-dashboard` | `main` | `5e9271e5` | 未变 | `SagerNet/sing-box-dashboard:main` `3e4bf08e` | behind 0 / ahead 4 | 无（只被接受为嵌套 Pin） | `main` + `gh-pages`（部署，保留） | — | 两个提交只改 `.github/workflows/`（`git diff --stat` 32 行） | — |

审计命令（可复核）：

```sh
# 每仓库的产品分支与上游关系
gh api repos/<upstream>/compare/<branch>...Piggy-Cat-bit-shadow:<fork-branch> --jq '.behind_by,.ahead_by,.status'
# cronet-go（compare 因 diff 过大返回 422，改用祖先判定）
git fetch --filter=blob:none --no-tags upstream main
git merge-base --is-ancestor upstream/main origin/main
```

---

## 3. 上游逐项融合账本

分类用施工令的术语：`MERGE_CLEAN` / `CHERRY_PICK` / `SEMANTIC_PORT` / `HYBRID_INTEGRATION` / `ALREADY_COVERED` / `REJECT_WITH_EVIDENCE` / `HOLD`。

### 3.1 uTLS（3 个提交，全部吸收）

| 上游提交 | 解决的问题 | Fork 是否已覆盖 | 策略 | 改动 | 证据 |
|---|---|---|---|---|---|
| `72aa2193` fix BuildHandshakeState error handle for UConn | `handshakeContext` 直接 `return err`，绕过了清理：`quic.blockedc`/`signalc` 从未关闭，`UQUICConn.Start` 永久阻塞、`Close` 收不到 goroutine | NOT_PRESENT（fork 与上游基线一致） | `MERGE_CLEAN` | `u_conn.go` | `u_quic_build_error_test.go`：旧代码上 30s 超时失败，合并后 PASS |
| `183cd009` keep in sync of UConn and Conn | 0-RTT early-data transcript 在 ECH 下应哈希 **inner** ClientHello | NOT_PRESENT | `MERGE_CLEAN` | `u_handshake_client.go` | 合并后全量 `go test -short ./...` PASS；见下方"为什么不需要 fork 适配" |
| `a6a364af` Only compress ALPN for standard ClientHellos | 自定义 ClientHello 的 inner/outer ALPN 可以不同，压缩会破坏它 | NOT_PRESENT（该分支被写成 `echInner && false`，即永不压缩） | `MERGE_CLEAN` | `handshake_messages.go` | `u_ech_alpn_test.go`：旧代码上"标准 hello 应压缩 ALPN"失败，合并后三种形态全 PASS |

**为什么不需要为 `183cd009` 加 fork 适配（与直觉相反，所以写下来）：**fork 会构建 preset ClientHello，看起来 `ech.innerHello` 可能为 nil。实际不可能：`c.echCtx` 只在两处赋值——`buildHandshakeState`（HelloGolang 分支）和 `MarshalClientHello`——两处都先设置 `ech.innerHello`。因此 `ech != nil` 蕴含 `ech.innerHello != nil`，加防御分支只会掩盖真实不变量。

**镜像纪律：**`mirror/meta-v1.9.0` 只允许快进，且 HEAD 必须与上游严格相等。本轮 `8853e107 → a6a364af` 是 fast-forward，推送后回读一致；`main` 与镜像没有互相污染。

### 3.2 Apple（13 个提交）

| 提交 | 问题 | Fork 覆盖情况 | 策略 |
|---|---|---|---|
| `9e920f3` user service endpoint registration | App 拨**无版本号**的 mach service，扩展注册的是 `<group>.system.<version>`；接受 endpoint 后没有刷新 WiFi 状态 | NOT_PRESENT | `MERGE_CLEAN` |
| `4faffeb` user service connection lifecycle | 客户端缓存一个 registry 取来的连接，endpoint 变了也不重建；teardown 清 registry 时旧连接还活着 | NOT_PRESENT | `SEMANTIC_PORT`（见下） |
| `cf2e1f7` Remove useProcFS | 内核已不再声明 `UseProcFS`，Swift 侧是死代码 | NOT_PRESENT；**内核侧已确认移除**（`experimental/libbox/platform.go`） | `MERGE_CLEAN` |
| `f3c1019` Bump build number | TestFlight build number | PARTIAL | `SKIP_WITH_EVIDENCE`（发布构建由 `release/JIEJIE_VERSION` 全局覆盖） |
| `6d04061` update authorization blocking main thread | `AuthorizationCopyRights` 同步模态框跑在 MainActor 上 | NOT_PRESENT | `MERGE_CLEAN` |
| `1e2204b` duplicate startup error alert | 启动失败弹两次 | PARTIAL | `SEMANTIC_PORT`（上游组件吸收，Hako 组件**故意保留**，见下） |
| `55c8fe7` iOS share sheet profiles | 扩展里 `extensionContext.open` 无效，激活规则也被"Open in"抢走 | NOT_PRESENT | `MERGE_CLEAN`（新增 `ShareExtension/Info-iOS.plist`、`Jailbreak/Action.plist`，pbxproj +5 行） |
| `b4b8d03` stale Tailscale pages | peer/logout 页面持有值快照，登出后仍显示旧数据 | NOT_PRESENT | `MERGE_CLEAN` |
| `ff0de06` / `2dd154e` / `0d1b59f` | 版本与 marketing 字符串 | PARTIAL | `SKIP_WITH_EVIDENCE`（4 条真冲突，见 §4；发布时整体覆盖） |
| `20547d4` platform auto redirect stub | 满足新的 `LibboxPlatformInterface` 成员 | **已覆盖**：`ExtensionPlatformInterface.swift:970-978` 与内核 `platform.go:32-33` 一致 | `ALREADY_COVERED` |
| `1675552` process paths through helper XPC | `processPath: String` → `processPaths: [String]` | **已覆盖**：`ShellHelperXPC.swift:56-73`、`ConnectionLookup`、两个 helper 都已用 `[String]` | `ALREADY_COVERED` |

**`4faffeb` 的语义移植（真正的坑）：**git 报告 2 个冲突文件，其中 `ExtensionProvider.swift` 那个是"fork 把 macOS teardown 移进了 identity-checked 块，上游还在改尾部那段"→ 保留 fork 的删除。但**另一个问题不是 git 冲突**：上游删掉了 `UserServiceEndpointRegistry` 整个类，而 fork 自己搬走的那段仍在调用 `UserServiceEndpointRegistry.shared.clear()`。直接 `git merge` 会得到一个**macOS 编译不过**的树而 git 说"冲突已解决"。处理后 `git grep UserServiceEndpointRegistry` 为空。

**`1e2204b` 为什么只对上游组件生效：**`HakoStartStopButton.swift` 是**另一份**状态机副本（由 `scripts/dev/port_hako_components.py` 从 `c1935cf` 生成，文件头写明了"两套实现刻意分开"）。它的本地 alert 覆盖的是**瞬时失败**——隧道没有经过 `.connecting` 就失败，而 `GlobalChecksModifier` 只在观察到 `notStarted`（即进过 `.connecting`）后才检查，所以删掉它会让手机端在这条路径上**一个提示都没有**。这是有意的产品行为，记录为偏差而不是缺陷。

### 3.3 Android（12 个提交）

| 上游提交 | 结论 | 证据 |
|---|---|---|
| `1a79a5c` connection owner legacy | `MERGE_CLEAN`（7 个真吸收之一） | `dev` 里还有 `useProcFS()`；合并后为 0 |
| `f041963` 应用列表被截断 | `MERGE_CLEAN` | 合并后两套 flavor 的 `getInstalledPackages` 都是新签名，无 2 参残留 |
| `04bd5b1` 扫描竞态 | `MERGE_CLEAN` | — |
| `8103b7e` ParceledListSlice 错误 | `MERGE_CLEAN` | — |
| `7b6d223` 服务启动前等 libbox | `MERGE_CLEAN`（三条生命周期链的第 1 条） | `suspend fun setupLibbox` |
| `a04705b` 每次启动探测服务模式 | `MERGE_CLEAN`（第 2 条） | `detectServiceMode()` 存在，`rebuildServiceMode()` 改返回 Unit |
| `1becd23` dashboard 切配置后应用 | `MERGE_CLEAN`（第 3 条，依赖第 2 条） | `applySelectedProfile` 3 处调用，`RequestReconnectService` 0 处 |
| `f87c2eb` 1.14.4 版本 | `NOT_APPLICABLE` | 被下一条覆盖 |
| `df64e82` auto redirect | **`ALREADY_COVERED`**（`c9b510f`，+268 行完全一致） | `git diff` 行集相同，patch-id 因上下文行/偏移不同而不同 → 不能只用 `git cherry` |
| `70fe45b` libghostty | `ALREADY_COVERED`（`eebd6c2`） | patch-id 相等 |
| `197240e` DNS search domains | `ALREADY_COVERED`（`7eeced6`） | patch-id 相等 |
| `a548a79` alpha.13 | `NOT_APPLICABLE`，但**版本号采用上游值** | 见下 |

**版本号决策：**`version.properties` 是 Android 客户端唯一版本源（`app/build.gradle.kts` 读它，没有 `buildSrc`）。合并结果取上游 `747 / 1.15.0-alpha.13`，不是本线原来的 `744 / 1.15.0-alpha.11`：客户端自带的更新检查用 `Libbox.compareSemver` 与上游 release 比较，保留 744 会(a)把自己排在上游 alpha 之下、(b)`VERSION_CODE` 小于已安装的上游包，Android 不允许"降级升级"。fork 自己的版本号应当在**下一次**版本步骤里抬到上游之上，不属于这次合并。

### 3.4 其余六个 Go Fork

`sing` / `sing-tun` / `sing-quic` / `quic-go` / `cronet-go` 对既定跟踪目标的已审查提交**没有落后**（见 §2 表）。`sing-quic` 的 7 个 ahead 是 fork 的 ceiling/owned-datagram 工作，`sing-tun` 的 98 个 ahead 是 fork 的 TUN 生命周期改造，都**不是**待清理的落后。`quic-go` 只跟 SagerNet 模块线；MetaCubeX 线**未触碰**。

### 3.5 `sing-box/testing` 自身的 pending 分支

`pending/device-axis-reload-window-tests`（`0477e327`）唯一提交是 891 行测试，落在三个文件上。复核结论：**保留，不删除，也不合入**，理由是可验证的：

- 该测试依赖一个"seam"（`seamDeviceAxisGate`）来在 Windows 上穿过平台门，但 `git grep seamDeviceAxisGate 0477e327 -- '*.go'` 只命中测试文件本身——**分支自己的 `command_server.go` 从未调用它**。也就是说 `TestWindowReconciliationClosesTheWindow` / `...DoesNotManufactureASleepEdge` 在这条分支上也无法真正驱动被测代码。
- 现行 `testing` 已经用**真实 pause manager**覆盖了设备轴语义（`box_lifecycle_test.go`、`box_lifecycle_ordering_test.go`、`box_lifecycle_stress_test.go`，含"display 打开不得释放设备轴""reload 不得继承无人发布的 pause"）。
- 分支与 `testing` 没有共同祖先（`git merge-base` 为空），是该仓库历史上"重写前孤儿"的一部分，因此移植需要重写而不是 cherry-pick。

结论：`HISTORICAL_UNIQUE`，保留为现场记录；不删除，也不为了减少分支数丢掉它。

---

## 4. 分支清理台账

| 仓库 | 分支 | 删除前 SHA | 独有提交 | 是否已合并/引用 | 恢复锚点 | 动作与结果 |
|---|---|---|---|---|---|---|
| `sing-box` | `pending/device-axis-reload-window-tests` | `0477e327` | 1（891 行测试） | 无共同祖先 | 分支本身保留 | **保留**（§3.5） |
| `sing-box` | `testing` | `a0bbf666` → `8dac10c1` | — | 产品分支 | — | 保留 |
| `sing-box-for-android` | `fix/libbox-stringbox-callsites` | `9d15717` | 3 | 已并入 `c7837107`（`git merge-base --is-ancestor`=True） | `archive/fix-libbox-stringbox-callsites-9d15717` | **已删除**，远端回读仅剩 `main` |
| `sing-box-for-android` | `stable` | `092aefd` | 0 | 完全包含 | `archive/stable-092aefd` | **已删除** |
| `sing-box-for-android` | `wip` | `2ce3c74` | 2 | **未合并**，已失效（`ec8294f` 终态在 `dev`；`2ce3c74` 被上游 `b8c7451` 吸收，`dev` 已有 `localDNSTransport`） | `archive/wip-2ce3c74` | **已删除**（带理由标签） |
| `sing-box-for-android` | `dev` | `4085557` | 5 | 已全部包含在 `main` | `archive/dev-4085557` | **已删除**；默认分支已改为 `main` |
| `sing-box-for-apple` | `fix/compact-outbound-mode-ui` | `3cc3aae` | 0 | 是 `main` 的祖先；0 个 open PR | SHA 仍在 `main` 历史里 | **已删除** |
| `sing-box-for-apple` | `sync/upstream-2026-10-28` | `be3258a` | 1（合并提交） | 候选分支 | 分支本身 | 合入 `main` 后删除（见 §5.3） |
| `utls` | `mirror/meta-v1.9.0` | `8853e107` → `a6a364af` | 0（纯镜像） | 上游 HEAD | — | 保留（审计基线） |
| `sing-box-dashboard` | `gh-pages` | — | — | 部署分支 | — | 保留（部署用途） |

Android 的删除顺序刻意是：先建 4 个 `archive/*` 标签 → 推送标签 → 主仓库 gitlink 移到 `c7837107` → 设置默认分支 `main` → 才删分支。所以 `9d15717` 这个曾被主仓库消费的 SHA **始终可 fetch**。

---

## 5. 验证矩阵（真实执行了什么）

### 5.1 uTLS

| 检查 | 结果 | 级别 |
|---|---|---|
| 上游祖先与镜像纯度 | `git diff upstream/v1.9.0-mod-meta..mirror` 为空；镜像 = `a6a364af` | `VERIFIED_GIT` |
| `go test -short ./...`（合并前基线） | PASS（exit 0） | `VERIFIED_UNIT` |
| `go test -short ./...`（合并 + 测试后） | PASS | `VERIFIED_UNIT` |
| `TestECHCompressesALPNOnlyForAStandardClientHello` | 新代码 PASS；**旧代码 FAIL**（"标准 hello 必须压缩 ALPN"失败，outer_extensions = `[10 13 43 51 45]`） | `VERIFIED_UNIT` + 负向 |
| `TestQUICStartReportsABuildHandshakeStateError` | 新代码 PASS；**旧代码 30s 超时 FAIL**（Start 永久阻塞） | `VERIFIED_UNIT` + 负向 |
| 消费方 | `go build ./...`、`go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...`、`(cd test && go vet ./...)`、`go mod tidy -diff` 双模块全 PASS；`./common/tls` 失败集与旧 Pin **完全一致**（3 个 Windows TLS1.3 已知失败，先 stash 复测确认） | `VERIFIED_UNIT` |

### 5.2 Android

| 检查 | 结果 | 级别 |
|---|---|---|
| 分支祖先关系 | `dev`/`fix`/`stable` 都是新 `main` 的祖先；`wip` 不是（符合预期） | `VERIFIED_GIT` |
| ABI 调用点扫描（CI 同款扫描器） | `probe-accessors.py --lang kotlin`：**309 文件，0 违规，3 个 interface 方法** | `VERIFIED_SOURCE` |
| 9 处 StringBox 调用点 | `RootServer:341`、`VPNService:77/83/127/135/173`、`Wrappers:49`（扩展接收者）、`PlatformInterfaceWrapper:445`（实现者）、`DashboardViewModel:222` 全部 `.value` | `VERIFIED_SOURCE` |
| `useProcFS` | 0 处 | `VERIFIED_SOURCE` |
| 生命周期链 | `detectServiceMode` 存在、`applySelectedProfile` 3 处、`RequestReconnectService` 0 处、`suspend fun setupLibbox` 存在 | `VERIFIED_SOURCE` |
| AIDL | `startAutoRedirect = 9` 只出现一次 | `VERIFIED_SOURCE` |
| 订阅 UA | `HTTPClient.SUBSCRIPTION_USER_AGENT = "sing-box/1.15.0"`，只在 5 处订阅正文请求传入；默认 UA 仍是 `SFA (...)` | `VERIFIED_SOURCE` |
| **Kotlin/Gradle 真实编译** | **PASS**：`./gradlew --no-daemon assembleOtherDebug`（JDK 17.0.20.1 Temurin、compileSdk 37、SDK `C:\Deepseek\sdk`）→ `BUILD SUCCESSFUL in 4m 16s`，`48 actionable tasks: 48 executed`，产出 5 个 ABI 的 debug APK（`SFA-1.15.0-alpha.13-*-debug.apk`，最大 68.1 MB）。所用 AAR 是父仓库 `android-core-arm64.yml` 为 core `8dac10c1` 构建的产物，`artifact_sha256` 与其自带记录一致（`25f43868…`），`BUILD-INFO-ANDROID.txt` 的 `git_commit` 为 `8dac10c1` | `VERIFIED_UNIT`（编译级） |
| **负向对照** | 用**同一份新 AAR** 编译旧 `main`（`ae93152`）：`:app:compileOtherDebugKotlin FAILED`，**11 个 `e:` 错误**，`BUILD FAILED in 1m 41s`，命中的文件正是本轮修掉的：`PlatformInterfaceWrapper.kt`（`'useProcFS' overrides nothing` + `fun name(): String` 不是 `StringBox!` 的子类型）、`RootServer.kt:255`、`VPNService.kt:20/77/83/127/135/173`、`Wrappers.kt:49`、`DashboardViewModel.kt`、`ProxyService.kt`。同一 AAR 下新树 PASS、旧树 FAIL ⇒ AAR 确实是迁移后的 ABI，且合并树是它能编译的原因 | `VERIFIED_UNIT` + 负向 |
| 真机/VPN/TUN/root/auto_redirect | `NOT_RUN`（无设备） | 缺口 |

### 5.3 Apple

| 检查 | 结果 | 级别 |
|---|---|---|
| 上游祖先 | merge 后 `git rev-list --count HEAD..upstream/dev` = 0 | `VERIFIED_GIT` |
| 冲突 | 5 个 hunk / 2 文件（4 个 `MARKETING_VERSION`、1 个 teardown），全部人工判定 | `VERIFIED_SOURCE` |
| 静默破坏（非冲突） | `UserServiceEndpointRegistry.clear()` 调用未随类删除 → 已修，`git grep` 为空 | `VERIFIED_SOURCE` |
| ABI 调用点（Swift） | `probe-accessors.py --lang swift`：**422 文件，0 违规** | `VERIFIED_SOURCE` |
| UI 边界审计 | `audit_apple_ui_boundary.py`：PASS 16 / FAIL 2 / UNKNOWN 2，**与 `origin/main` worktree 的基线完全一致**（2 个 FAIL 是既有的共享页注释提到 Hako 符号） | `VERIFIED_SOURCE`（相对基线无退化） |
| 结构门 | `check_platform_structure.py` PASS、`check_swift_structure.py` PASS、`_safety_gate.py` PASS | `VERIFIED_SOURCE` |
| 品牌/UA 边界 | `INFOPLIST_KEY_CFBundleDisplayName = "Jiejiebox"` ×6、`PRODUCT_NAME = "JiejieBox"`、`HTTPClient.subscriptionUserAgent` 及其 Hako 订阅测试保留 | `VERIFIED_SOURCE` |
| 真实编译（iOS） | Apple client 运行 `40698718663`：`resolve` 成功（`explicit-sha` → `be3258ab57dd`）、`libbox` 成功、**`ios` 成功**；macOS 见下一行 | `VERIFIED_CI` |
| 真实编译（macOS） | **`NOT_RUN`**：四次派发、两个 runner 标签（`macos-latest`、`macos-14`）全部在排队约 15 分钟后被平台取消且一个 step 都没跑（`40698718663` 的 `macos`、`40704313767`、`40707022945`、`40710418326` 的 `libbox`） | 缺口（账号级 macOS 限制） |
| 合并树的 Swift 语法 | 本地 Swift 6.3.3 `swiftc -parse -enable-bare-slash-regex` 对候选树 **422 个 .swift 文件全部通过**（含被合并改动的 15 个：`PKGInstaller.swift`、`ExtensionProvider.swift`、`TailscalePeerView.swift`、`UserServiceXPC.swift`、`CommandXPC.swift`、`ShareView*.swift` 等）。注意这只是**语法**，不做类型检查；`GitHubEmoji.swift` 需要那个 flag 因为用了裸正则字面量，main 上同样如此。类型检查与链接仍然只有 macOS runner 能给 | `VERIFIED_SOURCE`（语法级） |
| 候选树的 **Libbox 与 ABI 门**（macOS runner 拿到的那一次） | run `40730518254` 的 `libbox` 作业 **success**（16 steps，runner `GitHub Actions 1000010961`）：`build Libbox from this fork` 用 `make lib_apple` 产出 iOS+macOS 两个切片的 xcframework（即**内核这一侧对 macOS 是能构建的**），随后 `libbox ABI gate against the client call sites` **success** —— 这是层 1-5 的完整门，作用于候选 Apple 客户端 `be3258a` 的树。**仍未证明的是 macOS 客户端 app 本身能否编译**：同一 run 的 `macos` 作业 17:18:42 排队、约 15 分钟后再次因未分配 runner 被取消（runner 空、0 step） | `VERIFIED_CI`（内核侧 + 客户端 ABI 扫描） |
| 合并树的订阅/UA **行为**测试 | 用本地 Swift 6.3.3 跑候选树的 SPM 包 `Tests/HakoSubscriptionUsage`：**37 个用例执行，0 失败，5 个按其自身守卫跳过**（`UAGatedPanelTests` 需要 iOS 的 URLProtocol 环境）。其中与施工令直接相关的三条都过了：`testTheSubscriptionBodyIdentityIsTheStableSingBoxAgentAndCarriesNoClashMarker`（正文身份是 `sing-box/1.15.0` 且不带 Clash 标记）、`testSubscriptionMetadataUserAgentCarriesAClashCompatibleMarker`（**配额/元数据探测保留 Clash 兼容 UA**，即"不是所有请求都被改"）、`testSubscriptionRequestsExplicitlyBypassTheHTTPCache`；另有 `RemoteRefreshPolicyTests` 8 条覆盖首拉/刷新/校验失败不落库。**平台垫片（仅本地运行，未提交）**：`Libbox` stub 的 `NSErrorPointer` → `UnsafeMutablePointer<NSError?>?`、`FoundationNetworking` 条件导入、去掉 `Profile.swift` 里未使用的 `import Network`、并在临时包清单里排除依赖 Combine 的 `ProfileUnderTest`/`ProfilePersistenceTests` | `VERIFIED_UNIT`（行为级，带平台垫片） |
| Apple gitlink 前移的 ABI 依据 | `probe-accessors.py --lang swift` 对 **`16cf87b4` 这一确切提交**：423 文件 / 0 违规 / 3 interface 方法；且 `git diff --name-only aeccc754..16cf87b4` 只有 UA 修复与测试/stub，没有契约命名的文件 | `VERIFIED_SOURCE` |
| Hako UI 门 | `gate_hako_platform_imports.py` 报 `undecidable on tvos` 62 项（本机无 tvOS SDK 知识），**未写入任何文件**，与本轮改动无关 | `NOT_RUN`（环境） |

### 5.4 Desktop / Dashboard

| 检查 | 结果 | 级别 |
|---|---|---|
| dashboard `d452c24..5e9271e` | 只改 `.github/workflows/ci.yml`(+13)、`deploy.yml`(+19)，`dashboard/src` 未动 | `VERIFIED_SOURCE` |
| desktop UA 常量 + 测试 | `src/shared/subscription.ts:186` `SUBSCRIPTION_USER_AGENT = "sing-box/1.15.0"`；`subscriptionFetch.test.ts:81-82` 断言常量与请求头 | `VERIFIED_SOURCE` |
| 嵌套 Pin | desktop 内 `dashboard` = `5e9271e5`；`release-freeze.py` 复核为 `identical` | `VERIFIED_GIT` |
| 前端构建 / 桌面打包 | **`NOT_RUN`**：本机无 `node_modules`，内置 pnpm 是 broken release（`@pnpm/exe` 缺二进制） | 缺口 |

### 5.5 发布冻结工具链

| 检查 | 结果 | 级别 |
|---|---|---|
| 行为测试 | `scripts/ci/test-release-freeze.py`：**33 断言全 PASS**（本机 Python 3.12） | `VERIFIED_UNIT` |
| CI 上执行 | Release contract verification run **`40702192446`**：步骤 "the freeze helper's behavioural suite" **success** | `VERIFIED_CI` |
| 工作流结构检查 | `check-release-workflows.py`：**143 PASS / 0 FAIL**（含新增 10 项） | `VERIFIED_SOURCE` |
| 真实仓库上 `check` | 报告 3 个落后目标（utls Pin、`clients/apple`、nested dashboard 在修复前），`PASS: every target is comparable and none is diverged` | `VERIFIED_SOURCE` |
| 真实仓库上 `verify` | `PASS: every pin in the tree is the pin the lock records`（7 go modules / 3 clients / 29 native / nested 已解析） | `VERIFIED_SOURCE` |
| `release-prepare.yml` dry run | **成功**：run `40706074269`（提交 `62f5ecef`），preflight→resolve→check→apply→source 门→snapshot→verify 全绿，dry run 不提交 | `VERIFIED_CI` |
| `release-prepare.yml` **非 dry-run**：真的前移 Pin 并推送 | **成功产出冻结提交**：run `40713977139`，在 `testing` 上推出 `325f3158`（utls → `v1.8.8-0.20261028133436-b63a2e9a7b1e`，双模块 go.mod/go.sum + 锁）。该 run 随后停在证据步骤——见下一行的竞争条件 | `VERIFIED_CI` + `VERIFIED_GIT` |
| 证据步骤与推送的竞争 | 第一次非 dry-run 在推送后 **1 秒**就报 `no run of verify.yml exists for commit 325f3158`。已修：工作流先等 `Verify` 与 `Release contract verification` 两条 run 在 API 里**出现**，再交给 `evidence` 等结论；若 10 分钟仍不出现则明确失败并给出 dispatch 命令 | `VERIFIED_CI` |
| 幂等性 + 完整链路（含精确 SHA 证据） | **全绿**：run `40718334061`（提交 `3c6ca2ea`，17 个步骤）`apply` 判定没有落后目标 → snapshot 与已提交的锁**逐字节相同** → `nothing to commit` / `nothing to push` → ABI 门在 HEAD 上通过 → 等到 `Verify`/`RCV` 两条 run 出现 → **`PASS: every required evidence gate succeeded, every job included, for 3c6ca2ea…`**（Verify `40718300787` #670、RCV `40718300537` #218） | `VERIFIED_CI` |
| 主干在运行期间被推新提交 | run `40715812910` 的推送被 `! [rejected] HEAD -> testing (fetch first)` 拒绝，**没有强推**；失败信息已改成"分支在运行期间移动了，请重跑"，且证据步骤前增加 `git ls-remote` 对照 | `VERIFIED_CI` |
| **门禁顺序漏洞（真实发现）** | 第一版把 `check-libbox-abi.sh --pin-only` 放在提交之前：它读 `git ls-tree HEAD`，而 `apply` 写的是索引，于是它对着**旧 gitlink** 报 PASS，放过了正在准备的 Pin 前移。工作流已改为 `提交 → ABI 门 → 推送`，让这道门真正检查要被 Tag 的那个提交 | `VERIFIED_CI` |
| 该 dry run 抓到的真实缺陷 | 第一次 dry run（`40704668286`）在 `apply` 失败：`go list -m github.com/metacubex/utls@<fork-sha>` 问的是**被 replace 的模块路径**，上游没有这个提交 → `unknown revision`。修复 `62f5ecef`（改为问 replace 目标 `Piggy-Cat-bit-shadow/utls`），并让测试里的假 `go` 拒绝非 fork 路径，防止回归 | `VERIFIED_CI` + `VERIFIED_UNIT` |
| 修复后 dry run 真的前移了 Pin 并冻结 | 下载 run `40706074269` 的 `release-freeze-report` 产物：runner 上的 `source-lock.json` 里 utls = `v1.8.8-0.20261028133436-b63a2e9a7b1e`，relation `identical`，`derived_at=62f5ecef`。即 `apply`（含 `go mod tidy`）→ gates → `snapshot` → `verify` 在实测里成立，不只是单测 | `VERIFIED_CI` + `VERIFIED_ARTIFACT` |

---

## 6. 发布链路证明

### 6.1 调用链

```
维护者: gh workflow run release-prepare.yml -f dry_run=false [-f create_tag=true]
  │
  ├─ preflight        branch=testing / 工作区干净 / version==release/JIEJIE_VERSION / tag 不存在
  ├─ resolve          release-freeze.py resolve        → 11 个仓库的精确分支头 (build/resolve.tsv)
  ├─ check            release-freeze.py check          → identical/behind/ahead/diverged；diverged 立即失败
  ├─ apply            release-freeze.py apply          → 只前移 behind 的 Pin；cronet 29 原生模块不动
  ├─ go mod tidy ×2   → go.sum 跟上 replace 的版本
  ├─ gates            check-go-module-integrity.sh / check-libbox-abi.sh --pin-only / go build ×2 / go vet
  ├─ snapshot         release-freeze.py snapshot       → release/source-lock.json（**从实际 Pin 派生**）
  ├─ verify           release-freeze.py verify         → 锁与树必须一致
  ├─ commit+push      **只在 Pin 真的变了时**；否则不产生空提交、不重复触发 CI（幂等）
  ├─ evidence         release-artifacts.sh evidence --sha <candidate>
  │                     → 要求 verify.yml + release-contract-verify.yml 在**该 SHA**上最新一次 run
  │                       全部 job success；缺失/skip/失败都失败，不回退到旧绿灯
  └─ tag (create_tag) → 再次确认 tag 不存在 → 在 candidate 上打 annotated tag → 回读 peeled SHA 必须相等
        │
        └─ 之后的正式发布仍由 release.yml 完成（下载 CI 已构建的产物、逐平台校验、draft→公开）
```

### 6.2 各环节负责人

| 环节 | 负责者 | 失败时会发生什么 |
|---|---|---|
| 解析 fork 分支头 | `release-freeze.py resolve` | 分支不存在 → `MISSING`，`check` 失败 |
| 分类（含分叉） | `release-freeze.py check` | `DIVERGED` → 退出 1，**不写任何文件**，报告里给出 repo/旧 SHAA/新 SHA/建议 |
| 前移 Pin | `release-freeze.py apply` | Go 版本用 `go list -m` 计算；`controlled`/`report` 从不改写 |
| 冻结快照 | `release-freeze.py snapshot` | 有不可比目标时拒绝生成锁 |
| Push 门 | `verify.yml` 的 `release-freeze.py verify --lock` | 有人改了 Pin 却没重生成锁 → 立刻红 |
| Tooling 门 | `release-contract-verify.yml` 的冻结行为测试 | 工具本身退化 → 红 |
| 候选证据 | `release-artifacts.sh evidence`（既有机制，未另造） | 缺 run / job skip / 失败 → 失败 |
| Tag | `release-prepare.yml` 的 tag 步骤 | tag 已存在 / 落点不符 → 失败；不做 `--force` |
| 发布 | `release.yml`（未改动） | 仍是唯一发布者；Tag 之后不再拉起任何浮动源码 |

### 6.3 已经拿到的真实证据

| 事实 | 证据 |
|---|---|
| 我的 `testing` 提交触发了四套门 | Verify `40702192442`、RCV `40702192446`、Android Core `40702192584`、Mobile contract `40702192400` |
| 最终冻结树的 Verify 全绿 | 提交 `2de3764a`：Verify `40725323789` = `completed/success`（含新增的 `release-freeze.py verify` 步骤） |
| **Verify 在推送提交上全绿**（含新增的 `release-freeze.py verify` 步骤） | Verify `40702192442` = `completed/success` |
| 冻结测试在 Linux CI 上真的执行并通过 | RCV `40702192446` 的步骤 "the freeze helper's behavioural suite" = success |
| 冻结工具在真实仓库上复现了本轮诊断 | `check` 输出：`BEHIND utls`、`BEHIND clients/apple`、`BEHIND clients/desktop`、`BEHIND nested dashboard`（其中 desktop 已在本轮修复，复核后为 `identical`） |
| Apple 候选源码解析正确 | Apple client `40698718663`：`resolve` success（`resolved: be3258ab57dd (explicit-sha)`）、`libbox` success、**`ios` success**；`macos` 被平台取消（无 step 执行），其后四次重派（含 `macos-14` 探针与收尾时的 `40722121237`）同样在 15 分钟处被取消 |
| 非 dry-run 的发布准备真的推出了冻结提交 | `325f3158`（run `40713977139`），并且 `40718334061` 走完全链路并拿到精确 SHA 证据（`3c6ca2ea`：Verify `40718300787` + RCV `40718300537`，每个 job 都成功）——幂等与证据都成立 |

**没有做、也不该在这一轮做的**：没有创建新 release tag，没有修改/移动 `v0.1.33`，没有发布。

---

## 7. 失败、遗留风险与下一步

按"可执行的下一步"写，不写"其余正常"。

1. **Apple 上游融合的 macOS 编译无证据 → 上游合并停留在候选分支（HOLD）；但 `clients/apple` 已经前移到 `16cf87b4`（fork 自己的 UA 修复，见 §5.3 的 ABI 依据）。**
   - 已拿到：iOS 目标在候选 `be3258a` 上**构建成功**（Apple client run `40698718663` 的 `ios` 作业 = success），因此共享 Swift 代码、`libbox` 链接、`StringBox` ABI 与 `ShareExtension/Info-iOS.plist` 接线都经过真实编译。
   - 没有拿到：`macos` 目标。**六次尝试，两个 runner 标签，全部在排队 15 分钟后被取消，且一个 step 都没有执行**：`40698718663` 的 `macos`、`40704313767` 的 `libbox`、`40707022945` 的 `libbox`、换成 `macos-14` 的探针 `40710418326` 的 `libbox`（探针分支随后已删除）、`40722121237` 与 `40727482951` 的 `libbox`（后者 16:35:54 排队、16:50:56 取消）。

   - 取证结论（排除法，而不是猜测）：被取消的 `libbox` 作业 `runner_name` **为空、`steps` 为 0**，即从未分配到 macOS runner；同一时段 GitHub 状态页 `All Systems Operational`（Actions operational，无事故）；仓库本身是 **public fork**，所以这不是"用完了 macOS 分钟数"那种故事。结论：这些作业根本没能拿到 macOS runner，仓库侧没有任何东西能改变它。前两次 macOS 作业（`libbox` 13:53 成功、`ios` 14:09 成功）说明在这之后就再也没排到过。
   - 因此：`apple/main` **保持 `16cf87b4`**（不含上游合并），13 个上游提交的合并留在候选 `sync/upstream-2026-10-28`（`be3258a`，Swift 语法层已验证：422 文件解析通过）。这是施工令要求的"无法可靠编译就停留在旧正式 Pin，报告候选与失败证据"，而不是把未验证的 Swift 变更合进产品线。**注意**：`clients/apple` gitlink 已经前移到 `16cf87b4`，因为那只是 fork 自己的 UA 修复、且已随 v0.1.33 构建过；层 4 扫描（423 文件 / 0 违规）与 `VERIFIED_APPLE_SHA` 在同一个提交里更新。另外记录一个**真实发现**：第一次非 dry-run 的 `release-prepare` 把 gitlink 写进索引后，`check-libbox-abi.sh --pin-only` 仍然通过，因为它读的是 `git ls-tree HEAD`（旧提交）而不是索引——**门禁被自己的执行顺序绕过了**。工作流已改成 提交 → ABI 门 → 推送，让这道门真正检查要推出去的那个提交。
   - 恢复条件：macOS 配额恢复后，`gh workflow run client-apple.yml -f build_ios=false -f build_macos=true`（仓库变量 `APPLE_CLIENT_SHA=be3258ab57dd…`）。绿了以后：fast-forward `apple/main` → 更新 `clients/apple` gitlink 与 `VERIFIED_APPLE_SHA` → 重跑 `release-freeze.py snapshot` → 删除候选分支。
1b. **准备运行期间主干被推新提交时，工作流会失败而不是强推**（本轮实测：`40715812910` 的推送被 `! [rejected] HEAD -> testing (fetch first)` 拒绝）。这是设计：冻结是对某个 head 算出来的，换成新 head 必须重算。失败信息已改成明确的"分支在运行期间移动了，请重跑"，并在证据步骤前增加一次 `git ls-remote` 对照，避免把本地提交当成发布候选。
2. **`release-prepare.yml` 已真实 dry run 通过**（run `40706074269`），但它抓到过一个只有真跑才会暴露的缺陷（伪版本算在被 replace 的路径上），已修（`62f5ecef`）。教训保留在工作流里：`apply` 之后仍要 `verify`，两者不能互相替代。
3. **Android 的 Kotlin 编译本轮已补上，但真机行为仍未验证。** 用 JDK 17 + `libbox.aar`（core `8dac10c1` 的真实产物，checksum 与 `BUILD-INFO-ANDROID.txt` 自洽）在本机跑通 `assembleOtherDebug`；同一份 AAR 对旧 `main`（`ae93152`）做负向对照，`compileOtherDebugKotlin FAILED`、11 个 `e:` 错误、`BUILD FAILED`。仍未做：真机上的 VPN/TUN、service start/stop、root/auto_redirect、默认接口切换、睡眠唤醒、运行中配置切换。这些需要设备，不是自动化能替代的。
4. **Desktop 前端/打包 `NOT_RUN`**：内置 pnpm 是 broken release，`node_modules` 不存在。而 `client-desktop-windows.yml` **不能**用来验证这个新 Pin：它的 `core_sha` / `core_version` 是必填输入，并且会与 `refs/tags/v<version>` 的目标提交交叉校验（见该工作流头部注释）。也就是说它只能针对**已存在 tag** 的提交构建，而本轮没有也无法打 tag。结论：desktop 的 installer 级证明要等到下一次真实发布，或由一个允许"候选 SHA + 不校验 tag"的入口提供。这是本轮明确留下的缺口，不是"跑不跑得动"的问题。
5. **`release-prepare.yml` 在 dry-run 下跳过证据步骤**：这是有意的（dry run 不产生候选提交，声称证据就是假证据），但意味着"证据链"本身只有真实运行才验证。本轮没有跑到那一步。
6. **既有的 Apple mach service 版本不一致（`R3`）**：`AppConfiguration.systemExtensionMachServiceName` 用 `Bundle.main.version`，而扩展的 `NEMachServiceName` 用 `MARKETING_VERSION`，本地未签名构建下两者可能不同（release 构建会被 `release/JIEJIE_VERSION` 全局覆盖，所以不影响正式包）。不是本轮引入，未在本轮修（需要产品决策），记录在此。
7. **utls Pin 落后一个"纯文档"提交**：`main` 现在 `b63a2e9`（UPSTREAM.md 修正），Pin 在 `2da328d`（生产合并 + 测试）。按该仓库 UPSTREAM.md 的规则，文档提交不构成改 Pin 的理由，所以本轮刻意不追；dry run 里 `apply` 已证明它能正确地把 Pin 前移到 `b63a2e9`（伪版本 `v1.8.8-0.20261028133436-b63a2e9a7b1e`），只是没有推送。下一次真实 `dry_run=false` 会完成它。若希望"只有生产变更才算落后"，需要在 `release-freeze.py` 里按 patch 内容分类——**没有实现，因为那是把简单规则换成需要维护的启发式**。
8. **`resolve` 依赖 GitHub API 的 compare 端点**（祖先判定）。网络抖动会表现为 `UNKNOWN` 并 fail closed（不会误放行），但会挡住一次准备。重跑即可。
9. **Apple"正式发布 SHA == Snapshot"还没有在发布物件层面被强制，目前只有记录。** `release/source-lock.json` 记录 `clients/apple.pinned`，`release-prepare.yml` 每次都会重新派生；但 `release.yml` 发布的 Apple 物件只有 `Libbox.xcframework.tar.gz`（从**内核**构建，带内核 SHA），客户端 app 走 TestFlight 的 `scripts/publish-apple-beta.sh` 路径，两者都不与锁里的 Apple 客户端 SHA 比对。`apple-client-source.sh` 支持 `APPLE_CLIENT_SHA` 覆盖（本轮三次 macOS 派发就是这么指向候选的），所以补法是：候选发布用 `APPLE_CLIENT_SHA=<lock 的 clients/apple.pinned>` 派发，并在 `publish-apple-beta.sh` 或 `verify-release-acceptance.sh` 里把该值与会话记录比对。**本轮没改这条路径**：它属于 macOS 构建/签名链，而本轮拿不到 macOS runner，改了也无法验证——"记录 + 明确下一步"比"改了但没跑过"更安全。

---

## 8. 用户如何操作（极简）

1. **日常**：只在各仓库的产品分支工作——`sing-box/testing`、`sing/dev`、`sing-tun/dev`、`sing-quic/main`、`quic-go/sagernet`、`cronet-go/main`、`utls/main`、`sing-box-for-apple/main`、`sing-box-for-android/main`、`sing-box-for-desktop/main`、`sing-box-dashboard/main`。不要再手工改 Pin。
2. **发版前**（唯一要做的事）：GitHub → Actions → **Release preparation** → Run workflow →
   - `dry_run` 保持 **true** 先跑一次：它只解析、分类、冻结、跑门，并给出报告；有分叉会直接红。
   - 报告没问题后，`dry_run: false` 跑一次：它会把真正落后的 Pin 前移、提交、推送，并等候选提交的 CI 证据。
   - CI 绿之后，再跑一次 `dry_run: false` + `create_tag: true`：它创建 tag（默认 `v<release/JIEJIE_VERSION>`）。
3. **发布**：`gh workflow run release.yml -f tag=vX.Y.Z`（和以前一样；它只下载 CI 已构建并校验过的产物）。
4. **红灯去哪看**：`release-prepare` 的失败步骤会直接说仓库名、旧/新 SHA、关系（behind/diverged/missing）和下一步；`verify.yml` 里的 `release-freeze.py verify` 红灯意味着"有人改了 Pin 却没重新冻结"。
5. 想手工确认现状：`python3 scripts/ci/release-freeze.py check --resolve <(python3 scripts/ci/release-freeze.py resolve /dev/stdout)`（或先 `resolve --out` 再 `check`）。

---

## 9. 本轮提交清单

| 仓库 | 提交 | 主题 |
|---|---|---|
| `utls` | `3ab4752` | merge：吸收 MetaCubeX v1.9.0-mod-meta `a6a364af`（3 个提交） |
| `utls` | `2da328d` | test：钉住 ECH ALPN 条件与 QUIC 构建错误路径（两个负向证明） |
| `utls` | `b63a2e9` | docs：记录镜像同步与真实消费方 Pin |
| `sing-box-for-android` | `61a58ab` | merge：ABI handover（dev + fix 的 3 个 StringBox 提交） |
| `sing-box-for-android` | `c7837107` | merge：吸收 SagerNet dev 的 12 个提交 |
| `sing-box-for-apple` | `be3258a` | merge：吸收 SagerNet dev 的 13 个提交（候选，待 CI） |
| `sing-box-for-desktop` | `6d4ccf8c` | deps：嵌套 dashboard Pin → `5e9271e5` |
| `sing-box` | `14cfcafd` | deps：utls Pin → `2da328dfb92f`（双模块） |
| `sing-box` | `0adeb465` | deps：`clients/android` → `c7837107`（含 `VERIFIED_ANDROID_SHA`、契约行号、状态更新） |
| `sing-box` | `a27e683c` | deps：`clients/desktop` → `6d4ccf8c` |
| `sing-box` | `8dac10c1` | feat：发布前依赖冻结（manifest + 工具 + 测试 + 工作流 + 锁） |
| `sing-box` | `fad9be7a` | docs：本报告 |
| `sing-box` | `62f5ecef` | fix：伪版本改从 fork 路径计算（dry run 抓到的真实缺陷） |
| `sing-box` | `e814b2ba` | docs：dry run 结果与 Apple Pin 保持不动的理由 |
| `sing-box` | `fb3ca4f3` | docs：Android ABI handover 现在有编译证据（含负向对照） |
| `sing-box` | `328efb29` | docs：Apple"发布 SHA == Snapshot"仍是记录而非门禁 |
| `sing-box` | `3dbd7158` | docs：四次 macOS 尝试、两个标签全部取消（不是标签问题） |
| `sing-box` | `325f3158` | release：冻结提交（由 `release-prepare` 自动生成并推送，utls Pin 前移） |
| `sing-box` | `d0bccc9b` | fix：锁改为只含 Pin（幂等）+ 证据步骤等待 run 出现 |
| `sing-box` | `af3aa579` | deps：`clients/apple` → `16cf87b4`，并记录 ABI 验证依据 |
| `sing-box` | `d0eed4fd` | docs：Apple Pin 前移与其 ABI 依据 |
| `sing-box` | `3c6ca2ea` | fix：ABI 门放在提交之后 + 推送被拒的可执行信息（本报告所在提交见远端 `testing`） |

---

*证据等级用词：`VERIFIED_SOURCE`（读源码/扫描器）、`VERIFIED_GIT`（Git/API 祖先与参考）、`VERIFIED_UNIT`（本机执行的测试）、`VERIFIED_CI`（指定 run id 的 GitHub Actions 执行）、`VERIFIED_ARTIFACT`（真实产物校验）、`NOT_RUN`（附带原因）。本报告没有把任何 `NOT_RUN` 写成 `PASS`。*
