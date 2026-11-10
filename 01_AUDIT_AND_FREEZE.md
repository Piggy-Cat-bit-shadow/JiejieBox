# 01｜仓库盘点、安全冻结与集成基线建立

- 执行时间：2026-11-11 00:00–00:35（Asia/Shanghai）；远端快照由 `git ls-remote` 于 2026-11-10 23:47（UTC+8）前后采集。
- 本阶段性质：只读审计 + 隔离工作区建立。没有 stash / reset / clean / checkout 覆盖任何用户工作区，没有推送任何远端引用。
- 结论先行：
  1. 远端状态与旧审计快照一致但**不是照抄**：`testing` 仍为 `4152caf0e58f`，`candidate/w07-2026-11-10` 仍为 `0b1b6a3b2f0d`，远端 15 条分支（旧快照称"约 15 条"）。
  2. 主仓库集成分支 `integration/0.1.45-final` 已从候选 tip `0b1b6a3b2f0d2e3f3d2f580a4bc77d769ff57e20` 建立，工作区 `C:\Deepseek\_int0145\jiejiebox`，干净。
  3. 13 个仓库均在隔离路径建立 `integration/0.1.45` worktree，起始 commit 全部等于主仓库树中记录的 pin。
  4. 112 个本地 clone/worktree 中 7 个非干净，全部**原样保留**并做了独立备份（见 §4）。
  5. 发现旧快照未记录的本地未推送工作：主仓库 6 条本地分支（`candidate-r6`、`next/config`、`next/protocols`、`review/task09-dual-stack`、`trial/task08-h3-h2-merge`、`wip/p0ab-stale-base`）；Desktop `next/client-consumption`（4 提交）；Apple 子模块 `hotfix/ios-command-socket`（1 提交）；cronet-go `feat/per-dial-quic`（2 提交，含 1.8 GB 原生库）。
  6. **旧快照的一处误判已纠正**：`backup/02-ecfffe7d0`（本地别名 `next/routing-dns`）不是"大型旧基线备份"。其 tip 时间为 2026-11-10 21:42，比候选 tip（22:05）早 23 分钟，44 个提交与所有 topic 分支 **0 个 patch 等价**，是一条**仍在进行的并行工作线**，不得按"旧备份"处理。
  7. **pin 与声明不一致（需第 05/06 阶段处理）**：Apple `clients/apple` 的 pin `7fce7117b33a` 位于 `next/client-consumption`，**不在 `main` 上**（`main`=`97b35d1ce242`，是 pin 的祖先），与 `release/dependencies.json` 声明的 `branch: main` 不符。
  8. `wireguard-go` 出现在 `go.mod`/`source-lock.json`，但 `release/dependencies.json` 的 `go_modules` 未声明它（旧快照已提到，本次复核确认仍存在）——第 06 阶段必须补齐或移除。

---

## 1. 阶段目标与实际范围

目标：摸清真实施工现场，建立可回滚、可审计的集成基线，输出仓库/分支/依赖矩阵与集成计划修正。

实际执行：
- 重新 `ls-remote` 全部 13 个仓库并核对默认分支；
- 对 12 个本地主 clone 执行 `git fetch --all --tags`（只更新 remote-tracking refs，不动工作区），另对被 `--remotes` 漏判的 2 个子模块 clone 补充显式 refspec fetch；
- 扫描 112 个本地 clone/worktree 的 `git status`（porcelain v1）与本地分支；
- 对主仓库 24 条 topic/local 分支重算 merge-base、left/right、patch-id、文件差异；
- 建立主仓库 `integration/0.1.45-final` 与 12 个依赖仓库 `integration/0.1.45` worktree；
- 输出本报告与 `INTEGRATION-LOG.md`。

未执行（明确不在本阶段）：任何依赖合并、任何 `go build`/`go test`、任何 pin 修改、任何推送。

---

## 2. 主仓库远端真实状态（重新核实）

| 项 | 值 |
|---|---|
| 仓库 | `Piggy-Cat-bit-shadow/JiejieBox`（旧名 `sing-box`，旧 URL 仍重定向） |
| 默认分支 | `testing`（`git ls-remote --symref` 确认，不是 `main`） |
| `testing` | `4152caf0e58f0cca9ce8044416c7c0b8d9960428`（与旧快照一致） |
| `candidate/w07-2026-11-10` | `0b1b6a3b2f0d2e3f3d2f580a4bc77d769ff57e20`（与旧快照一致，未被改写） |
| 远端分支数 | 15 |
| 本次集成基线 | `candidate/w07-2026-11-10` @ `0b1b6a3b2f0d2e3f3d2f580a4bc77d769ff57e20` |

远端全部分支（2026-11-11T00:00 前后，12 位缩写）：

| 分支 | SHA | 分支 | SHA |
|---|---|---|---|
| `testing` | `4152caf0e58f` | `candidate/w07-2026-11-10` | `0b1b6a3b2f0d` |
| `fix/task09-repair` | `31a3e9acea1b` | `task08/h3-h2-fallback-2026-11-10` | `684ebf516767` |
| `fix/task10-apple-ne-runtime-final` | `a3340d1a759d` | `next/network` | `b9bc2bc25981` |
| `closeout/resources` | `aafc4c11f022` | `next/resources` | `b00dfe1f0e08` |
| `integration/next-round` | `94c86b9d1f1f` | `docs/task09-knowledge-0.1.45` | `595d65154d84` |
| `task08/knowledge-0.1.45` | `99cc3b514327` | `kb/0.1.45-02` | `8405d1bbb986` |
| `next/client-consumption-candidate` | `09fa790dfb35` | `pending/device-axis-reload-window-tests` | `0477e327e5da` |
| `backup/02-ecfffe7d0` | `ecfffe7d0691` | — | — |

`testing` 相对候选：候选包含 `testing` 全部 206 个提交，`testing` 另有 2 个仅存于自身的文档提交：

- `63e0614ed89 docs(knowledge): archive task 10 (Apple NE / Libbox) under release/0.1.45`
- `4152caf0e58 docs(knowledge): move the task 10 archive to reports/0.1.45 and register it`

这 2 个提交同时出现在 `kb/0.1.45-02` 中，第 06 阶段合并文档时一并处理。

---

## 3. 仓库清单与本地状态

### 3.1 本地 clone 拓扑（关键）

主仓库的 43 个 worktree 共用**同一个对象库** `C:\Deepseek\singbox-consolidation\.git`（其中包括 `.git/modules/clients/apple`、`.git/modules/clients/desktop` 以及 Desktop 内嵌的 `.git/modules/clients/desktop/modules/dashboard`）。因此：

- 在这些 worktree 中看到的本地分支是**同一份** 25 条分支；
- 新建 `integration/0.1.45-final` 后，全部 43 个 worktree 都能看到该分支；
- 子模块在**每个** superproject worktree 下有独立的 module 对象库（`.git/worktrees/<wt>/modules/...`），因此同一客户端仓库在磁盘上存在多份 clone，各有各的本地分支——这是本次发现"隐藏本地分支"的原因（§4.3）。

独立 clone（不与共享对象库相连）还有：`jiejiebox-docs`（含 `next/routing-dns` worktree）、`_final_20261028/sing-box`、`_perf026`、`_rel0126/kernel`、`_release_kernel`、`_ui_cleanup_verify_20261025`、`_ui_closeout_20261025/{core,core_final_verify}`、`singbox-pin`、`ne-hardening/p3A`。

### 3.2 主仓库本地分支（全量，按提交时间）

| 分支 | SHA | upstream / 状态 |
|---|---|---|
| `wip/p0ab-stale-base` | `98b07085befc` | 无 upstream；**5 个未推送提交**（2026-10-14，陈旧） |
| `testing` | `ce362dbde084` | `origin/testing`，behind 2 |
| `next/protocols` | `54872ee150d9` | 无 upstream；**2 个未推送提交**（已被基线吸收，见 §5.3） |
| `next/config` | `eef4eb585198` | 无 upstream；**6 个未推送提交**（已被基线吸收） |
| `candidate-r6` | `262204f738c4` | 无 upstream；**3 个未推送提交**（旧客户端 pin） |
| `candidate-r7` | `b4cb5482662b` | upstream 设成 `origin/candidate/w07-2026-11-10`，ahead 1 / behind 31 |
| `fix/task09-dual-stack` | `7023b4a33320` | 无 upstream |
| `fix/task09-dual-stack-review` | `7023b4a33320` | 与上同点 |
| `review/task09-dual-stack` | `283fe791cfd1` | 无 upstream；**2 个未推送探针提交** |
| `review/task09-final` | `1f01dbd02883` | 无 upstream |
| `fix/task09-final` | `f232cc2160eb` | 无 upstream |
| `fix/task10-apple-ne-runtime` | `a3340d1a759d` | 与远端 `fix/task10-apple-ne-runtime-final` 同点 |
| `fix/task10-apple-ne-runtime-final` | `a3340d1a759d` | `origin/fix/task10-apple-ne-runtime-final` |
| `next/resources` | `b00dfe1f0e08` | 无 upstream（= 远端同点） |
| `candidate-r8` | `09fa790dfb35` | 无 upstream（= `origin/next/client-consumption-candidate`） |
| `candidate/w07-2026-11-10` | `0b1b6a3b2f0d` | `origin/candidate/w07-2026-11-10` |
| `fix/task08-h3-h2` | `684ebf516767` | 无 upstream（= 远端 `task08/h3-h2-fallback-2026-11-10`） |
| `task08/h3-h2-fallback-2026-11-10` | `684ebf516767` | `origin/task08/h3-h2-fallback-2026-11-10` |
| `trial/task08-h3-h2-merge` | `0bbe20321a60` | 无 upstream；**1 个未推送 merge 提交** |
| `fix/task09-repair` | `31a3e9acea1b` | `origin/fix/task09-repair` |
| `closeout/resources` | `aafc4c11f022` | 无 upstream（= 远端同点） |
| `integration/next-round` | `94c86b9d1f1f` | `origin/integration/next-round` |
| `docs/task09-knowledge-0.1.45` | `595d65154d84` | `origin/docs/task09-knowledge-0.1.45` |
| `task08/knowledge-0.1.45` | `99cc3b514327` | 无 upstream（= 远端同点） |
| `next/network` | `b9bc2bc25981` | `origin/next/network` |
| **`integration/0.1.45-final`** | **`0b1b6a3b2f0d`** | **本阶段新建**，无 upstream |

只有 `candidate-r6`、`next/config`、`next/protocols`、`review/task09-dual-stack`、`trial/task08-h3-h2-merge`、`wip/p0ab-stale-base` 这 6 条含 origin 上不存在的提交。

### 3.3 13 个仓库逐项记录

| 仓库 | 默认分支 | 远端分支（tip） | 主要负责本地 clone | 当前分支 / HEAD | 与主仓库 pin 关系 | worktree 安全性 |
|---|---|---|---|---|---|---|
| JiejieBox | `testing` | 15 条（见 §2） | `_p01_main`（共享库） | `testing` / `ce362dbde084`，clean，behind 2 | 基线本体 | 可；已建新 worktree |
| sing | `dev` | `dev` `1729220c46c4` | `sing-fork` | `dev` / `1729220c46c4`，clean | **pin = dev tip** | 可 |
| sing-tun | `dev` | `dev` `57e9d17fa852` | `sing-tun-fork` | `dev` / `57e9d17fa852`，clean | pin `9c3dcb2b1edf` = dev−13 | 可 |
| sing-quic | `main` | `main` `1ef2597cc129` | `_deps/sing-quic` | `main` / `1ef2597cc129`，clean | **pin = main tip** | 可 |
| quic-go | `sagernet` | `sagernet` `2daa76f5bd52`；`network/path-length-fix` `0e7c69dacb54`；`fix/task08-h3-usecount` `0b8709ebb15e` | `_deps/quic-go` | `network/path-length-fix` / `0e7c69dacb54`，clean | pin `bca854425f4d` 在两条功能分支上；`sagernet` 比 pin 少 7 个提交 | 可 |
| utls | `main` | `main` `b63a2e9a7b1e`；`mirror/meta-v1.9.0` `a6a364afbd13` | `_dep_audit/utls` | `main` / `b63a2e9a7b1e`，clean | **pin = main tip** | 可 |
| cronet-go | `main` | `main` `4c96192d73f6`；`go` `7ce9c49b464a` | `cronet-fork` | `main` / `4c96192d73f6`，clean | root pin `362e63b14ce2` = main+1（在 `go` 上），all pin `7ce9c49b464a` = `go` tip | 可 |
| wireguard-go | `awg-base` | `awg-base` `ca3bc60c4ce7`；`awg-graft` `52a27657d805`；`awg3-inject` `a765d957fd81` | `_scratch/wgmerge`（remote 名 `fork`） | `awg3-inject` / `a765d957fd81`，clean | **pin = awg3-inject tip** | 可 |
| sing-box-for-apple | `main` | `main` `97b35d1ce242`；`next/client-consumption` `7e2f5c43633a`；`fix/task10-apple-ne-runtime-final` `b2596b50fb6a` | `IOS客户端/sing-box-for-apple`（16 worktree） | `main` / `e3283104c984`，clean，behind 3 | pin `7fce7117b33a`：在 `next/client-consumption` 上，= main+9、= next−6 | 可 |
| sing-box-for-android | `main` | `main` `c7837107dc01` | `_dep_audit/sing-box-for-android` | `main` / `c7837107dc01`，clean | **pin = main tip** | 可 |
| sing-box-for-desktop | `main` | `main` `aa63764a61c3`；`next/client-consumption` `28dc4a973bcd` | 子模块 `modules/clients/desktop` | `main` / `23c2e155eab4`，clean，behind 4 | **pin = main tip**；本地 `next/client-consumption` = main+4，**未推送** | 可 |
| sing-box-dashboard | `main` | `main` `fc1dbb4a48bb`；`next/client-consumption` `62f158255ce9`；`gh-pages` `787d540e0167` | 嵌套子模块 `.../modules/dashboard` | detached `09eadbc443bd`，clean | **nested pin = main tip**；`next` = main+1 | 可 |
| satelite-one | `main` | `main` `c9a07350e4c7`；`next/subscription-refresh` `9cdf782b8c8c` | `satelite-one`（11 个 clone） | `main` / `c9a07350e4c7`，clean | 独立产品仓库，不是 gitlink | 可 |

说明（与旧快照的差异）：

- `sing`/`sing-quic`/`utls`/`android`：pin 与远端分支 tip 一致，**无新提交、无 pin 漂移**。
- `sing-tun`：`dev` = pin + 13，仍成立。
- `quic-go`：`sagernet`（`2daa76f5bd52`）比 pinned `bca854425f4d` **旧 7 个提交**；pin 位于两条功能分支之上。
- `wireguard-go`：`awg3-inject` 与 pin 相同；`awg-base` 相对 pin 2/25、`awg-graft` 3/25，均分叉。
- `cronet-go`：main → `362e63b`（Build from 4c96192d）→ `7ce9c49`（Generate all package）顺序成立；`go.mod` 中 29 个 `cronet-go/lib/<platform>` 与 root 同 pin。
- `satelite-one`：`next/subscription-refresh` = main + 16，已推送。

### 3.4 嵌套子模块与新增依赖（`.gitmodules` 扫描发现）

任务清单列出的 13 个仓库之外，`.gitmodules` 还揭示了 4 个嵌套依赖。它们不在 Go 模块图中，但影响客户端/原生构建与可复现性，必须纳入盘点：

| 父仓库 | 路径 | URL | 固定 SHA | 集成工作区状态 |
|---|---|---|---|---|
| `clients/apple` | `Frameworks/Runestone` | `nekohasekai/Runestone` | `baeb3cbf8332d26b4f7b4c175a01f288ebbf3e7f` | ✅ 已初始化（本地 clone，`go`/Swift 源码仓库） |
| `clients/desktop` | `dashboard` | `Piggy-Cat-bit-shadow/sing-box-dashboard` | `fc1dbb4a48bb06358d549de05eb90df17ab7110d` | ✅ 已初始化 |
| `clients/desktop/dashboard` | `vendor/iterm2-color-schemes` | `mbadolato/iTerm2-Color-Schemes` | `8c84dd1a859f36ec8601b082bd97b4ec888f0f4d` | ⚠️ 未初始化（远端 233 MB；且本机无 Node/pnpm，dashboard 构建本就 BLOCKED） |
| `cronet-go` | `naiveproxy` | `Piggy-Cat-bit-shadow/naiveproxy` | `0ca66a00765564593eb4d2e6d061fe950ac7cce2` | ⚠️ 未初始化：本地 3 个 clone 均为 partial clone，不能作克隆源；网络克隆 12 分钟无 pack 产出后中止（不影响 Go 依赖，见 §12.1） |

- 这 4 个 SHA 只存在于各自父仓库的 tree 中；`release/dependencies.json` 与 `release/source-lock.json` 都没有覆盖这一层。第 06 阶段冻结时必须明确 `source-lock` 是否记录嵌套子模块（尤其 `vendor/iterm2-color-schemes`）。
- `naiveproxy` 是 cronet-go 的原生子模块（仓库约 161 MB），与 Go 依赖 pin 无关，但重建 cronet 原生归档时可能需要；`policy: controlled` 的任何移动都要先确认它。
- `Runestone` 与 `iterm2-color-schemes` 都不参与内核构建，只影响 Apple/Dashboard 客户端。

---

## 4. 本地工作保护（未改动任何用户工作）

### 4.1 扫描结果

- 扫描对象：14 个 slug 下 **112 个本地 clone/worktree**（含 43 个主仓库 worktree、16 个 Apple worktree、各个 release/audit 副本）。
- 结果：**105 个完全干净**；**7 个非干净**，逐个记录如下（全部原样保留）：

| # | 工作区 | 分支/HEAD | 非干净内容 |
|---|---|---|---|
| 1 | `C:\Deepseek\_desktop_check` | `main` / `41722b030ea4` | index 中 138 条整树删除（`D `），工作区无改动。属损坏的临时 clone，非源码工作 |
| 2 | `C:\Deepseek\_p2_bench_base` | detached `c32c210b0dbb` | 未跟踪 `common/dialer/family_capability_bench_test.go`（7 239 B） |
| 3 | `C:\Deepseek\_perf026` | `testing` / `eb0b5857994a` | 未跟踪 `out/`（构建产物） |
| 4 | `C:\Deepseek\jiejiebox-docs` | `testing` / `25f5c9a638bd`（behind 130） | 3 个已修改：`README.md`、`docs/configuration/inbound/anytls.zh.md`、`clients/apple` gitlink（+28/−1） |
| 5 | `C:\Deepseek\jiejiebox-work\baseline-r5` | detached `2171167bcb39` | 未跟踪 2 个 Kotlin 文件：`InterfaceListenerRegistry.kt`、`InterfaceListenerRegistryTest.kt` |
| 6 | `C:\Deepseek\ne-hardening\p3A` | detached `4c7d96c0fbca` | 未跟踪 3 个测试：`common/runtimecoord/device_axis_reload_window_test.go`、`experimental/libbox/device_axis_reload_window_test.go`、`experimental/libbox/zz_window_seam_test.go` |
| 7 | `C:\Deepseek\singbox-consolidation` | detached `1f9d77ba197` | 仅 2 个子模块指针被改动（`clients/apple`、`clients/desktop`），非源码编辑 |

主仓库关键 worktree 复核（未被本阶段触碰）：`_p01_main` 仍为 `testing@ce362dbde084`、`_w07/integration` 仍为 `candidate/w07@0b1b6a3b2f0d`、`sing-tun-fork` 仍为 `dev@57e9d17fa852`、`IOS客户端/sing-box-for-apple` 仍为 `main@e3283104c984`、Desktop 子模块仍为 `main@23c2e155eab4`——全部 `git status` 干净，与扫描前一致。

### 4.2 保护备份（不写入用户仓库）

`C:\Deepseek\_int0145\backup\`：

| 文件 | 内容 | 说明 |
|---|---|---|
| `unpushed_main_20261111.bundle` | 49 KiB | 主仓库全部未推送提交（`--branches --not --remotes=origin`） |
| `unpushed_desktop_20261111.bundle` | 15 KiB | Desktop `next/client-consumption` 4 个提交 |
| `unpushed_cronet-go_20261111.bundle` | 295 MiB | cronet-go `feat/per-dial-quic` 2 个提交（含 29 个原生 `.a/.so/.dll` 归档，约 1.8 GB 未压缩） |
| `jiejiebox-docs-worktree.diff` | 4.5 KiB | 上述 3 个已修改文件的二进制 diff |
| `loose\...` | 64 KiB | 6 个未跟踪源码文件原样副本（`family_capability_bench_test.go`、2 个 Kotlin、p3A 的 3 个测试） |

**未执行**：`git stash`、`git reset`、`git clean`、`git checkout`、分支删除、远端推送。

### 4.3 隐藏的本地分支（子模块 module 对象库）

主仓库子模块在每个 worktree 下各有独立对象库，旧快照未覆盖。补充扫描 12 个 module 仓库后确认：

| 位置 | 分支 | tip | 状态 | 内容 |
|---|---|---|---|---|
| `.git/modules/clients/desktop` | `next/client-consumption` | `28dc4a973bcd` | **4 提交未推送** | profiles 应用/提供者竞态修复链 |
| `.git/worktrees/_p01_main/modules/clients/apple` | `hotfix/ios-command-socket` | `ceffc5b743c0` | **1 提交未推送** | `ExtensionProvider.swift` +13：无共享容器时明示 |
| `.git/worktrees/_p01_main/modules/clients/apple` | `experiment/selfsign-compat`、`feat/ios-local-signing-loopback`、`hotfix/ios-command-socket` | — | 其余已在 origin | — |
| `.git/worktrees/_p01_main/modules/clients/android` | `fix/libbox-stringbox-callsites` | `9d15717a85c5` | 复核后确认 **在 `origin/main` 上**（该 clone 原先只 fetch 了 `dev`，属跟踪引用不全，非未推送） | StringBox 调用点修复 |
| `.git/worktrees/_p01_main/modules/clients/desktop` | `main` | `41722b030ea4` | clean，比远端 main 旧 | — |
| `.git/modules/clients/apple` | `main` | `3aedd7417488` | clean | — |

> 结论：客户端侧真正的未推送工作是 **Desktop 4 个提交** 与 **Apple hotfix 1 个提交**，已进第 05 阶段的待合并清单；Android 无未推送工作。

---

## 5. 分支关系矩阵（相对候选 base `0b1b6a3b2f0d`）

计算方式：`git merge-base`、`git rev-list --left-right --count`、`--cherry-pick`、`git patch-id --stable`（对 `base..topic` 与 `topic..base` 两侧补丁求交集）、`git diff --numstat base...topic`。原始数据：`_audit01/matrix/matrix.tsv` 与 `_audit01/matrix/files_*.txt`。

| 分支 | tip | merge-base | base 独有 | topic 独有 | 文件 | +/− | 重复补丁 | 判断 |
|---|---|---|---:|---:|---:|---|---:|---|
| `testing` | `4152caf0e58f` | `ce362dbde084` | 206 | 2 | 5 | +233/0 | 0 | 候选已是其超集；2 个文档提交留给第 06 阶段 |
| `fix/task09-repair` | `31a3e9acea1b` | `e9f48e225fe1` | 42 | 31 | 40 | +5977/−370 | 0 | **未吸收**，第 04 阶段主来源 |
| `task08/h3-h2-fallback-2026-11-10` | `684ebf516767` | `a91adb07e102` | 52 | 39 | 40 | +5348/−188 | 0 | **未吸收**，第 04 阶段主来源 |
| `fix/task10-apple-ne-runtime-final` | `a3340d1a759d` | `efd8d3b2eae1` | 44 | 8 | 23 | +1957/−83 | 0 | **未吸收**，第 05 阶段内核 NE 来源 |
| `next/network` | `b9bc2bc25981` | `c4f46af9e1f8` | 193 | 36 | 43 | +7105/−88 | 0 | **未吸收**，基线严重落后，禁止整支 merge；第 03 阶段 |
| `closeout/resources` | `aafc4c11f022` | `95ed81392563` | 67 | 21 | 8 | +611/−34 | 0 | **未吸收**，第 03 阶段资源来源 |
| `next/resources` | `b00dfe1f0e08` | `c968d135046b` | 186 | 6 | 3 | +603/−26 | 0 | **是 `closeout/resources` 的祖先**（已含于其中），只需审其独有测试意图 |
| `integration/next-round` | `94c86b9d1f1f` | `e9f48e225fe1` | 42 | 7 | 16 | +873/0 | 0 | 文档 + 配置恢复测试；第 04 阶段参考、第 06 阶段文档 |
| `docs/task09-knowledge-0.1.45` | `595d65154d84` | `e9f48e225fe1` | 42 | 8 | 16 | +919/0 | 0 | 文档；第 06 阶段 |
| `task08/knowledge-0.1.45` | `99cc3b514327` | `e9f48e225fe1` | 42 | 6 | 15 | +876/0 | 0 | 文档；与 `integration/next-round` 互有 3/2 提交差异，需去重 |
| `kb/0.1.45-02` | `8405d1bbb986` | `ce362dbde084` | 206 | 3 | 6 | +348/0 | 0 | 基于 `testing` 的文档分支，含 §2 的 2 个提交 |
| `next/client-consumption-candidate` | `09fa790dfb35` | `c46a523659f1` | 31 | 6 | 3 | +19/−3 | 0 | 客户端 pin 候选（本地 `candidate-r8`），第 05 阶段 |
| `pending/device-axis-reload-window-tests` | `0477e327e5da` | `4c7d96c0fbca` | 1906 | 1 | 3 | +891/0 | 0 | 极度陈旧；3 个测试文件，本地 `p3A` 有同名未跟踪副本；第 03 阶段审查意图 |
| `backup/02-ecfffe7d0` | `ecfffe7d0691` | `b4e128907be6` | 176 | 44 | 94 | +15092/−906 | 7 | **不是旧归档**：活跃并行线（见 §5.2） |

本地分支（未推送）：

| 分支 | tip | merge-base | base 独有 | topic 独有 | 文件 | +/− | 重复补丁 | 判断 |
|---|---|---|---:|---:|---:|---|---:|---|
| `candidate-r6` | `262204f738c4` | `ce362dbde084` | 206 | 3 | 3 | +15/−3 | 0 | 旧客户端 pin 候选（round 6/7），已被 r8 取代，仅归档 |
| `candidate-r7` | `b4cb5482662b` | `c46a523659f1` | 31 | 1 | 3 | +11/−3 | 0 | r8 的前身 |
| `next/config` | `eef4eb585198` | `6c5229976b5a` | 185 | 6 | 23 | +2440/−223 | **6** | **6/6 patch 已在基线** → 已被吸收，语义仍需第 03 阶段抽查 |
| `next/protocols` | `54872ee150d9` | `ba309306f167` | 201 | 2 | 3 | +518/−27 | **2** | **2/2 patch 已在基线** → 已被吸收 |
| `fix/task09-dual-stack` | `7023b4a33320` | `5778c8417d31` | 46 | 8 | 19 | +2681/−287 | 0 | task09 链最早一环，已含于 `fix/task09-repair` |
| `review/task09-dual-stack` | `283fe791cfd1` | `5778c8417d31` | 46 | 10 | 23 | +3205/−287 | 0 | 其中 **2 个提交只存在于本地**，仅新增 4 个 `zz_review_probe*_test.go` |
| `fix/task09-final` | `f232cc2160eb` | `cd3956ad1586` | 43 | 17 | 27 | +4333/−362 | 0 | 是 `fix/task09-repair` 的祖先（+15） |
| `review/task09-final` | `1f01dbd02883` | `cd3956ad1586` | 43 | 14 | 24 | +3968/−359 | 0 | 是 `fix/task09-final` / `fix/task09-repair` 的祖先 |
| `trial/task08-h3-h2-merge` | `0bbe20321a60` | `e9f48e225fe1` | 42 | 44 | 54 | +6114/−188 | 0 | merge commit（`4c5cca544b5` + `684ebf51676`），本地试合并证据 |
| `wip/p0ab-stale-base` | `98b07085befc` | `ccab3645d60e` | 1728 | 5 | 16 | +1677/−1148 | 3 | 2026-10-14 的陈旧 WIP，仅归档 |

### 5.1 包含关系（`merge-base --is-ancestor` 实测）

task09 家族是一条链的多个快照：

```
fix/task09-dual-stack (7023b4a3)
  └─ review/task09-dual-stack (+2，仅本地探针测试)
  └─ review/task09-final (+9)
       └─ fix/task09-final (+3)
            └─ fix/task09-repair (+15，已推送，31 ahead of base)
```

- `review/task09-dual-stack` 的 2 个提交 **不在** `fix/task09-final`/`fix/task09-repair` 中；内容为
  `common/dialer/zz_review_probe_test.go`、`common/dialer/zz_review_probe2_test.go`、
  `dns/zz_review_probe_test.go`、`dns/zz_review_probe2_test.go`（纯 review 探针，`zz_` 前缀）。
  第 04 阶段决定：作为证据归档，还是按需保留其中断言。
- `task08` 家族：`fix/task08-h3-h2` (=`684ebf5`) 是 `trial/task08-h3-h2-merge` 的第一个 parent 之外的合并目标；
  trial 的 parents = `4c5cca544b5`（`integration/next-round` 历史内）+ `684ebf51676`（task08 tip）。
  即：**已有人把 task08 合到 `integration/next-round` 的较新状态上做过一次试合并**，第 04 阶段可直接复用其冲突解决结论，但必须重新验证。
- 资源家族：`next/resources` ⊂ `closeout/resources`（+134）；`next/network` 与两者都**不是**祖先关系。

### 5.2 `backup/02-ecfffe7d0` 的真实性质（纠正旧快照）

- tip `ecfffe7d0691`，提交时间 `2026-11-10 21:42:31 +0800`；候选 tip 为 `2026-11-10 22:05:43`——只早 23 分钟。
- 本地别名：`jiejiebox-docs` clone 中的 `next/routing-dns`（同一点 `ecfffe7d0691`）。
- 相对候选：176 behind / 44 ahead，94 文件 +15092/−906；其中 7 个提交与基线 patch 等价。
- 与 `next/network`、`integration/next-round`、`fix/task09-repair`、`task08/h3-h2`、`closeout/resources`、`testing` 逐一做 `git cherry`：**等价提交数均为 0**。
- 主题分布（44 个提交的文件命中）：`experimental` 67、`daemon` 24、`common` 22、`protocol` 18、`dns` 18、`route` 17。
- 结论：这是一条**独立的 libbox/daemon 流控与连接快照工作线**，基线落后 176 个提交，不能整支 merge，也不能当"旧备份"忽略。建议第 05 阶段（NE/Libbox 运行时）逐提交提取，第 06 阶段再决定归档或清理。

### 5.3 已被基线吸收 / 无需重复移植

- `next/config`：6 个提交全部与基线中提交 patch-id 等价（`git cherry` 输出 6 个 `-`）。净差异 23 文件来自基线后续演进，不是缺失功能。
- `next/protocols`：2/2 patch 等价，已吸收。
- `next/resources`：`closeout/resources` 的祖先，单独合并无意义。
- `fix/task10-apple-ne-runtime` 与 `fix/task10-apple-ne-runtime-final` 同点；`fix/task08-h3-h2` 与远端 `task08/h3-h2-fallback-2026-11-10` 同点；`candidate-r8` 与远端 `next/client-consumption-candidate` 同点。
- 注意：patch-id 等价只证明补丁形式相同，不证明语义在所有边界上等价；第 03/04 阶段仍须按文件复核（尤其 `next/config` 的 configtx 提交）。

---

## 6. 依赖关系与 pin 核对

事实来源（candidate tip 的树）：`release/dependencies.json`、`.gitmodules`、`go.mod`、`test/go.mod`、`release/source-lock.json`、`release/apple-client-refs.env`、客户端 gitlink、Desktop 内嵌 dashboard gitlink。

### 6.1 pin 一致性核对（全部实测存在）

| 依赖 | pin | 存在性 | 所在分支 | 与分支 tip 关系 |
|---|---|---|---|---|
| `sagernet/sing` | `1729220c46c4` | ✅ | `origin/dev` | = tip |
| `sagernet/sing-tun` | `9c3dcb2b1edf` | ✅ | `origin/dev` | tip − 13（`dev`=`57e9d17fa852`） |
| `sagernet/sing-quic` | `1ef2597cc129` | ✅ | `origin/main` | = tip |
| `sagernet/quic-go` | `bca854425f4d` | ✅ | `origin/network/path-length-fix`、`origin/fix/task08-h3-usecount` | 两条分支各 = pin + 3；`origin/sagernet` = pin − 7 |
| `metacubex/utls` | `b63a2e9a7b1e` | ✅ | `origin/main` | = tip |
| `cronet-go`（root） | `362e63b14ce2` | ✅ | `origin/go` | = `main` + 1 |
| `cronet-go/all` | `7ce9c49b464a` | ✅ | `origin/go` | = `go` tip |
| `cronet-go/lib/*`（29 个） | `362e63b14ce2` | ✅ | `origin/go` | 与 root 同 pin（controlled） |
| `wireguard-go` | `a765d957fd81` | ✅ | `fork/awg3-inject`（=`Piggy-Cat-bit-shadow/wireguard-go`） | = tip |
| `clients/apple` | `7fce7117b33a` | ✅ | `origin/next/client-consumption` | = next tip − 6；**不在 `main` 上**（`main`=`97b35d1ce242` 是 pin 的祖先，落后 9） |
| `clients/android` | `c7837107dc01` | ✅ | `origin/main` | = tip |
| `clients/desktop` | `aa63764a61c3` | ✅ | `origin/main` | = tip |
| `clients/desktop:dashboard` | `fc1dbb4a48bb` | ✅ | `origin/main` | = tip；`next/client-consumption`=`62f158255ce9` = main+1 |

`go.mod` 与 `test/go.mod` 的 replace 与 `source-lock.json` 逐项一致；`go.mod` 中 `cronet-go/lib/` 出现 58 次 = 29 个 require + 29 个 replace，与 `dependencies.json` 的 `expected_modules: 29` 一致。

### 6.2 quic-go 两条功能分支的真实拓扑（第 02 阶段施工依据）

```
pin bca854425f4d
  └─ 0028a4c7 fix(path): closing or probing a path after the connection ended must not panic
       └─ 4b11a7be fix(sendqueue): never block the connection's own shutdown on a failure report   ← 两者 merge-base
            ├─ 0e7c69da fix(connid): guard the connection ID manager with one mutex      (path-length-fix)
            └─ 0b8709eb http3: release the pool reference a joining request took when it leaves (task08)
```

- 两条分支在 pin 之上共享 2 个提交，各自 1 个独有提交（不同文件，冲突面极小）。
- `origin/sagernet`（`2daa76f5bd52`）比 pin 少 7 个提交，**不得回退到 sagernet tip**。

### 6.3 依赖侧本地未推送工作

| 仓库 | 分支 | tip | 未推送提交 | 内容 |
|---|---|---|---|---|
| sing-box-for-desktop | `next/client-consumption` | `28dc4a973bcd` | 4 | profiles 应用/提供者竞态修复 |
| sing-box-for-apple（`_p01_main` 子模块） | `hotfix/ios-command-socket` | `ceffc5b743c0` | 1 | `ExtensionProvider.swift` +13 |
| cronet-go（`_np_audit` clone） | `feat/per-dial-quic` | `8e6e52a72b48` | 2 | naive per-CONNECT 传输选择 + 重新构建的 29 个原生归档 |
| 其他 | — | — | 0 | sing/sing-tun/sing-quic/utls/wireguard-go/android/dashboard/satelite-one 均无未推送分支 |

### 6.4 manifest 与现实的偏差（交给第 06 阶段）

1. `release/dependencies.json` 的 `go_modules` **没有 `wireguard-go` 条目**，但 `go.mod` 与 `source-lock.json` 都有该 replace/pin（旧快照已指出，本次复核确认仍存在）。
2. `clients/apple` 声明 `branch: main`、`policy: update`，但实际 pin 在 `next/client-consumption` 上；`release/apple-client-refs.env` 又声明构建权威是 `APPLE_CLIENT_BRANCH=main` 的 tip（构建时解析）。三者目前不自洽，第 05/06 阶段必须统一。
3. `clients/desktop:dashboard` 声明 `policy: report`；当前 nested pin 恰为 dashboard `main` tip，暂无漂移，但 Desktop `next/client-consumption` 已把 gitlink 指到 `62f158255ce9`（main+1）。

---

## 7. 集成工作区

根目录：`C:\Deepseek\_int0145\`（独立于所有既有工作区）。

| 路径 | 仓库 | 分支 | 起始 / 当前 HEAD | 状态 |
|---|---|---|---|---|
| `_int0145\jiejiebox` | JiejieBox | `integration/0.1.45-final` | 起始 `0b1b6a3b2f0d`，此后仅追加本阶段文档提交 | clean，子模块已初始化（§3.4） |
| `_int0145\sing` | sing | `integration/0.1.45` | `1729220c46c4cf750726b1057514a8d52a1f5e3b` | clean |
| `_int0145\sing-tun` | sing-tun | `integration/0.1.45` | `9c3dcb2b1edfde31798981aa05f08851c0eee401` | clean |
| `_int0145\sing-quic` | sing-quic | `integration/0.1.45` | `1ef2597cc1298146fcdddfeaa0384999f890d0c3` | clean |
| `_int0145\quic-go` | quic-go | `integration/0.1.45` | `bca854425f4daf5818ef57c6702de548b27f222b` | clean |
| `_int0145\utls` | utls | `integration/0.1.45` | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` | clean |
| `_int0145\cronet-go` | cronet-go | `integration/0.1.45` | `7ce9c49b464ad90629a13061309a58c89890fea6` | clean（root=`362e63b`+`all` 提交） |
| `_int0145\wireguard-go` | wireguard-go | `integration/0.1.45` | `a765d957fd814e5c0ee579936a0780e73d66e533` | clean |
| `_int0145\apple` | sing-box-for-apple | `integration/0.1.45` | `7fce7117b33a38ef4c73c81db67c49f0be7a7dad` | clean |
| `_int0145\android` | sing-box-for-android | `integration/0.1.45` | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | clean |
| `_int0145\desktop` | sing-box-for-desktop | `integration/0.1.45` | `aa63764a61c312562a348e174522d1dc1c86d954` | clean |
| `_int0145\dashboard` | sing-box-dashboard | `integration/0.1.45` | `fc1dbb4a48bb06358d549de05eb90df17ab7110d` | clean |
| `_int0145\satelite-one` | satelite-one | `integration/0.1.45` | `c9a07350e4c7a4d60a918488478893de5a49b6ac` | clean |

说明：

- 全部通过 `git worktree add <path> -b <branch> <pin>` 建立，未 checkout 任何既有工作区。
- 主仓库 `integration/0.1.45` 命名使用文档要求的 `integration/0.1.45-final`（主仓库专用后缀），依赖仓库统一 `integration/0.1.45`。
- **主仓库集成分支的子模块已在本轮收尾中初始化**（见 §12.1），落点与树中 gitlink 完全一致：
  `clients/apple`=`7fce7117b33a`、`clients/android`=`c7837107dc01`、`clients/desktop`=`aa63764a61c3`、
  `clients/desktop/dashboard`=`fc1dbb4a48bb`、`clients/apple/Frameworks/Runestone`=`baeb3cbf8332`。
  唯一未初始化的是 `clients/desktop/dashboard/vendor/iterm2-color-schemes`（`8c84dd1a859f`，原因见 §3.4）。
- 初始化用本地对象库克隆完成（未重复下载 3 个客户端仓库），随后把每个子模块的 `origin` 与 `submodule.*.url` 恢复为
  `https://github.com/Piggy-Cat-bit-shadow/...`，因此 `git submodule update`、`git fetch` 仍指向规范远端。
- 本阶段**没有推送**任何分支；全部远端引用保持审计时刻状态。

---

## 8. 集成计划修正（供第 02–06 阶段直接使用）

### 第 02 阶段（依赖收敛）

- 基线固定为 §7 的 `integration/0.1.45` 起始 SHA，**不要**用各仓库分支 tip 当基线。
- 工具链（本轮实测）：Go 1.25.5 位于 `C:\Deepseek\_gotool\go\bin`（不在 PATH，需显式加前缀）；`GOMODCACHE` 已有 13.5 GB 预热；`CGO_ENABLED=0` 为默认值，需要 cgo 时用 `C:\msys64\mingw64\bin\gcc.exe`。`go build ./constant ./log ./common/...` 在集成基线上退出 0（§12.6）。
- `cronet-go` 的 `naiveproxy` 子模块 pin `0ca66a007655` 已在本轮初始化；重建任何原生归档前先核对该 SHA。
- `sing-tun`：pin `9c3dcb2b1edf` → `dev` `57e9d17fa852` 的 13 个提交逐条审查（TUN 内存预算、waiter 清理、背压、关闭路径），基线是 pin 而不是 `dev`。
- `quic-go`：以 `bca854425f4d` 为基线，融合 `0e7c69da`（connid mutex）与 `0b8709eb`（http3 pool reference）；两者共享 `0028a4c7`/`4b11a7be`，冲突面小但必须分别验证 path retirement 与 usecount 语义。
- `wireguard-go`：`awg-base`/`awg-graft` 与 pin 分叉（2/25、3/25），先回答主仓库是否真的消费 AWG；未纳入则明确 deferred，不得为清分支而合。
- `sing`/`sing-quic`/`utls`/`android`：pin 与 tip 一致，只做接口一致性核对，不升级。
- `cronet-go`：root/all 顺序已核实；**29 个原生模块不得移动**，除非有 Chromium 工具链重建证据。注意本地 `feat/per-dial-quic` 分支携带一套重建的原生归档，需单独评估其来源与合法性（它在 `_np_audit` clone，未推送、未进任何 pin）。
- `satelite-one`：独立产品仓库，只登记；`next/subscription-refresh`（+16）是否纳入由第 05 阶段决定。

### 第 03 阶段（内核网络与资源）

- 来源：`next/network`（36 独有，基线落后 193）、`closeout/resources`（21 独有，已含 `next/resources`）。
- `pending/device-axis-reload-window-tests` 已在本阶段定案（§12.4）：`ne-hardening/p3A` 的 3 个未跟踪文件与该分支**逐字节相同**，不是额外本地工作；其 `common/runtimecoord` 4 个测试在候选上 **4/4 PASS**（可作纯回归测试移植），而 `experimental/libbox` 的测试在候选上失败、在**该分支自己的 base**（`4c7d96c0fbc`）上也**同样失败** → 不是候选回归，分支本身是红的，libbox 测试不得原样移植。
- `backup/02-ecfffe7d0` 按 §5.2 重新定性：需逐提交评估，不可整支 merge；其中 `experimental/libbox` 部分可能与第 05 阶段重叠。
- 需复核 `next/config`（configtx 6 提交已被 patch 吸收）与 `next/protocols`（trusttunnel 2 提交已吸收）是否还有边界缺口。

### 第 04 阶段（双栈 DNS 与 H3/H2）

- 主来源：`fix/task09-repair`（31 独有；已包含 local `review/task09-final`→`fix/task09-final` 全部内容）。
- 唯一未进 repair 的本地内容：`review/task09-dual-stack` 的 2 个探针提交（4 个 `zz_review_probe*_test.go`）。已验证它们**能编译在 `fix/task09-repair` 上**（`go vet ./common/dialer ./dns` 退出 0，§12.5），所以是可执行的对抗性探针，不再是"未知内容"；第 04 阶段只需决定是否把 4 个测试文件并入。
- H3/H2 主来源：`task08/h3-h2-fallback-2026-11-10`（39 独有）；本地 `trial/task08-h3-h2-merge` 已给出与 `integration/next-round` 的试合并结果，可复用冲突解决但不能复用为最终提交。
- 配置恢复测试去重对象：`integration/next-round`、`fix/task09-repair`、`docs/task09-knowledge-0.1.45`。

### 第 05 阶段（NE 与客户端）

- 内核 NE：`fix/task10-apple-ne-runtime-final`（8 独有）。
- Apple：pin `7fce7117b33a` 在 `next/client-consumption` 上（= 该分支 tip − 6，且不在 `main`）；另有 `hotfix/ios-command-socket`（`ceffc5b`）1 个未推送提交需评估。
- Desktop：`next/client-consumption` `28dc4a973bcd` = main + 4（未推送），且其 dashboard gitlink 已指向 `62f158255ce9`（dashboard main+1）。
- Dashboard：`next/client-consumption` `62f158255ce9` = main + 1。
- Android：pin = main tip，无待合并；`satelite-one next/subscription-refresh`（+16）待决定。
- 客户端侧嵌套依赖：Apple 构建依赖 `Frameworks/Runestone`（`baeb3cbf8332`，集成工作区已初始化）；Dashboard 构建依赖 `vendor/iterm2-color-schemes`（`8c84dd1a859f`，未初始化且本机无 Node）。
- `backup/02-ecfffe7d0` 的 libbox/daemon 流控部分是第 05 阶段的重要候选来源。

### 第 06 阶段（冻结与清理）

- 文档去重：`integration/next-round`、`docs/task09-knowledge-0.1.45`、`task08/knowledge-0.1.45`、`kb/0.1.45-02` + `testing` 的 2 个提交。
- pin 冻结前必须解决 §6.4 的 3 个不一致。
- 分支清理候选见文档；本阶段补充：`candidate-r6/r7/r8`、`next/config`、`next/protocols`、`wip/p0ab-stale-base`、`review/task09-*`、`fix/task09-final`、`trial/task08-h3-h2-merge` 均为本地/远端 topic，需按第 06 阶段证明流程处理；`next/routing-dns` 与 `backup/02-ecfffe7d0` 是同一 tip 的两个名字，只能一起处理。

---

## 9. 测试与验证（本阶段实际执行的命令）

本阶段不涉及代码变更，因此没有 build/test 结论。所有执行过的命令与退出码：

| 命令 | 退出码 | 结果 |
|---|---|---|
| `git ls-remote --symref <13 repos>` | 0 | 与 §2/§3 一致 |
| `git fetch --all --tags`（12 个主 clone） | 0 | 仅更新 remote-tracking refs；`_deps/quic-go` 首次 `--all` 返回 1（多 remote 瞬时失败），分开 fetch 均为 0 |
| 子模块 module repo 显式 refspec fetch（android/apple） | 0 | 修正了跟踪引用不全导致的"假未推送" |
| `git status --porcelain=v1` × 112 | 0 | §4.1 |
| `git rev-list/edit/patch-id/merge-base` × 24 topic | 0 | §5 |
| `git worktree add` × 13 | 0 | §7 |
| `git bundle create` × 5 | 0（3 个非空） | §4.2 |
| `git diff`（备份 jiejiebox-docs） | 0 | 4 566 B |
| 子模块初始化（本地 clone + `-c protocol.file.allow=always`） | 0 | §12.1；落点 = 树中 gitlink |
| `go build ./constant ./log ./common/...`（集成基线） | 0 | 7.8 s，§12.6 |
| `go test ./common/runtimecoord`（pending 测试 @ 候选） | 0 | 4/4 PASS，§12.4 |
| `go test ./experimental/libbox`（pending 测试 @ 候选） | 1 | 3 PASS / 4 FAIL；同一组测试在分支 base 上也 4 FAIL，§12.4 |
| `go vet ./common/dialer ./dns`（review 探针 @ task09-repair） | 0 | §12.5 |

§12 的构建与测试只用于**审计判定**：证明工具链可用、被吸收分支没有残余代码、两个陈旧分支的测试是否可移植。它们**不是** 0.1.45 的验收测试：`go test ./...`、`-race`、lint、完整 CI、Apple/Android/Desktop/Dashboard 平台构建均**未执行**，状态仍为 **NOT RUN**，其中平台构建为 **BLOCKED**（缺 Xcode、JDK 17、Node/pnpm，见 §12.6）。不得把本节任何 PASS 引用为最终验收通过。

---

## 10. 风险、遗留与阻塞

1. **共享对象库风险**：43 个 worktree 共用 `.git`。任何对分支/ref 的破坏性操作会影响全部工作区。本阶段只新增 ref（`integration/*`），未删除或移动任何 ref。
2. **`backup/02-ecfffe7d0` 定性变化**：旧计划把它当"旧备份"，实际是活跃并行线（44 提交，0 patch 等价）。若第 05 阶段按旧假设处理，会丢失 libbox/daemon 流控工作。
3. **Apple pin 三处声明不一致**（`dependencies.json` / 实际 pin / `apple-client-refs.env`）。第 05 阶段若直接按 `main` 构建，会得到与 kernel pin 不同的客户端 SHA。
4. **Desktop 4 个未推送提交 + dashboard gitlink 漂移**：Desktop `next/client-consumption` 依赖 dashboard `62f158255ce9`，而候选 pin 是 `fc1dbb4a48bb`。第 05 阶段必须先处理 Dashboard → Desktop 的顺序。
5. **cronet-go 本地重建的原生归档**（`feat/per-dial-quic`，约 1.8 GB）来源未证明；`policy: controlled` 要求 Chromium 工具链重建证据，第 02 阶段必须先查证来源，不得直接采用。
6. **平台环境缺口（本轮实测）**：无 macOS/Xcode、无 JDK 17（仅 Java 8）、Android SDK 存在但缺 JDK、无 Node/npm/pnpm。因此 Apple/Android/Desktop/Dashboard 的构建与设备测试在本环境 **BLOCKED**；第 05/06 阶段必须按 BLOCKED 记录，不得标 PASS。
7. **两个嵌套子模块未初始化**：`vendor/iterm2-color-schemes`（`8c84dd1a859f`，233 MB）与 `naiveproxy`（`0ca66a007655`，本地 clone 全为 partial clone）。前者与 Node 缺失一起留到第 05 阶段，后者留到第 06 阶段重建 native 时处理；两者都不影响 Go 依赖与内核构建。
8. **`backup/02-ecfffe7d0` 与 `next/routing-dns` 是同一 tip**：清理时必须一起处理，不能只删一个 ref 就认为该工作线已归档。
9. **未做任何 pin 修改**：candidate tip 仍指向 `0.1.44` 的 source-lock（`release_version: 0.1.44`），0.1.45 的 pin 冻结在第 06 阶段。
10. 原风险 6/7（子模块未初始化、`next/config`/`next/protocols` 只有 patch 等价）已在 §12.1/§12.2 消解，不再作为遗留项。

无外部阻塞：网络、GitHub 凭据、`gh` 均可用（`gh auth status`：`Piggy-Cat-bit-shadow`，scopes 含 `repo`/`workflow`）；Go 工具链可用（§12.6）。

---

## 11. 完成条件对照

| 文档要求 | 状态 |
|---|---|
| 所有仓库、分支、本地未提交工作已盘点 | ✅ 13 仓库 + 112 clone/worktree + 12 子模块 module 仓库 |
| 所有基线 SHA 有记录 | ✅ §2/§3/§7 + `_audit01/*.json`/`*.tsv` |
| 集成工作区已隔离 | ✅ §7，`C:\Deepseek\_int0145` |
| 主仓库集成分支从正确候选创建 | ✅ `integration/0.1.45-final` @ `0b1b6a3b2f0d` |
| 依赖关系和分支差异矩阵完成 | ✅ §5/§6 |
| 没有覆盖或丢失用户工作 | ✅ §4，7 个非干净工作区原样保留并备份 |
| 输出 `01_AUDIT_AND_FREEZE.md` 与 `INTEGRATION-LOG.md` | ✅ 本文件 + `INTEGRATION-LOG.md` |
| 收尾：子模块按 gitlink 初始化 | ✅ §12.1（仅 `vendor/iterm2-color-schemes` 例外） |
| 收尾：被吸收分支内容级复核 | ✅ §12.2 |
| 收尾：`pending` / `review` 本地分支判定 | ✅ §12.4 / §12.5 |
| 收尾：工具链就绪与冒烟构建 | ✅ §12.6（平台构建 BLOCKED） |

---

## 12. 收尾补做（本轮追加，仍属阶段 01 范围）

原始清单在 §7/§9 之后已经完成；本轮把"能证明但没证明"的项一次做实，避免把不确定性带进第 02 阶段。所有动作都在隔离工作区或临时 worktree 中完成，未改动用户既有工作区。证据文件在 `_int0145/audit-evidence/`。

### 12.1 子模块初始化与新增依赖

- 主仓库集成分支的 3 个客户端子模块、Desktop 内嵌 dashboard、Apple 内嵌 Runestone 均已按树中 gitlink 检出：
  apple `7fce7117b33a`、android `c7837107dc01`、desktop `aa63764a61c3`、dashboard `fc1dbb4a48bb`、Runestone `baeb3cbf8332`。
- 克隆使用本地对象库：`clients/apple` ← `IOS客户端/sing-box-for-apple`，`clients/android` ← `_dep_audit/sing-box-for-android`，
  `clients/desktop` ← `.git/modules/clients/desktop`，`dashboard` ← `_int0145/dashboard`，`Runestone` ← `jiejiebox-docs/clients/apple/Frameworks/Runestone`；
  用 `git -c protocol.file.allow=always` 绕过 Git 2.38+ 对本地路径子模块克隆的默认禁用（`fatal: transport 'file' not allowed`）。
- 克隆后逐个 `git remote set-url origin <github url>` 并写回 `submodule.<name>.url`，`git submodule status --recursive`
  不再出现 `-` 前缀（`vendor/iterm2-color-schemes` 与 `naiveproxy` 例外，见下）。
- 两个**保持未初始化**的嵌套子模块及原因：
  - `dashboard/vendor/iterm2-color-schemes`（`8c84dd1a859f`）：远端仓库 233 MB，且本机无 Node/pnpm，dashboard 构建本就 BLOCKED。
  - `cronet-go/naiveproxy`（`0ca66a007655`）：三个本地 clone 都是 `remote.origin.promisor=true` 的 partial clone，
    `git clone <local>` 直接报 `early EOF / invalid index-pack output`；从 GitHub 克隆 12 分钟无 pack 产出后中止（不是权限或连通性问题）。
    它不参与 Go 依赖，第 02 阶段核对 cronet pin 不需要它；第 06 阶段若要重建 native，再按该 SHA 单独克隆。
- 新发现的 4 个嵌套依赖已写入 §3.4；它们不在原任务清单内，但 `.gitmodules` 是事实来源，必须登记。

### 12.2 被吸收分支的内容级复核

`next/config`、`next/protocols` 此前只有 patch-id 等价证据。本轮对它们触及的每个文件做 blob 对比：

| 分支 | 触及文件 | 与候选 blob 完全相同 | 不同 | 不同文件与方向 |
|---|---:|---:|---:|---|
| `next/config` | 23 | 22 | 1 | `route/rule/build_pressure_sample_test.go`：候选版把 `Elapsed` 拆成 `Queue/Fetch` 并加了采样器，是**更新的**测试仪器 |
| `next/protocols` | 3 | 2 | 1 | `transport/trusttunnel/service_udp_idle_test.go`：候选版加入时钟粒度 margin 与 `-time.Second` 修正，是**更新的**测试 |

结论：两个分支的产品代码与测试**已在基线中**，残余差异全部是基线后续改进，不存在"分支有、基线无"的内容。第 03 阶段无需移植，只需按需补测；此前的"patch 等价但语义未证"风险就此关闭。

### 12.3 本地未推送分支的定性

| 分支 | 定性 |
|---|---|
| `candidate-r6` / `candidate-r7` | 旧客户端 pin 快照（round 6/7），当前候选为 `candidate-r8`；只归档 |
| `fix/task09-dual-stack` / `review/task09-dual-stack` / `review/task09-final` / `fix/task09-final` | task09 链上的历史快照；前三个内容已在 `fix/task09-repair` 中，仅 review 探针例外（§12.5） |
| `trial/task08-h3-h2-merge` | 本地试合并（parents `4c5cca544b5` + `684ebf51676`）；只保留冲突解决结论，不作为提交来源 |
| `wip/p0ab-stale-base` | 2026-10-14 的 release/CI 工具化改动（workflow、desktop overlay 移除、版本注入脚本、KNOWLEDGE 文档），不触碰产品 Go 代码；3 个 patch 已在基线，其余仅归档 |

### 12.4 `pending/device-axis-reload-window-tests` 判定

- `ne-hardening/p3A` 的 3 个未跟踪文件与该分支的 3 个文件**逐字节相同**（SHA-256 一致），确认不是额外本地工作。
- 把这 3 个测试文件放进候选树（临时 worktree，不入库）后：
  - `common/runtimecoord`：4 个 `TestPlatformMirror*` 测试 **4/4 PASS**；
  - `experimental/libbox`：3 PASS / 4 FAIL（`TestDeviceAxisDirectWriterIsNotReconciled`、`TestDeviceAxisGovernorAgreesWithTheManager`、`TestWindowReconciliationClosesTheWindow`、`TestWindowReconciliationDoesNotManufactureASleepEdge`）。
- 同样 3 个文件放到**该分支自己的 base** `4c7d96c0fbc` 上运行：结果**完全相同**（3 PASS / 4 FAIL）。
- 因此 libbox 的 4 个失败**不是候选引入的回归**，而是分支在其 base 上就是红的。第 03 阶段：`runtimecoord` 测试可作为纯回归覆盖移植；libbox 测试需按当前实现重写或明确丢弃，不得按"候选缺陷"立案。

### 12.5 `review/task09-dual-stack` 探针

- 4 个探针文件在候选上**编译失败**（`undefined: newStreamOrderDialer`、`undefined: familyResults`），因为两个 helper 定义在 task09 线的测试文件里。
- 在 `fix/task09-repair` 上放回这 4 个文件，`go vet ./common/dialer ./dns` **退出 0** → 探针可执行，只是依赖 task09 代码。
- 第 04 阶段可随 task09 一起并入这 4 个测试文件（不改产品代码），也可明确归档；不再是"未知内容"。

### 12.6 工具链与构建冒烟

| 项 | 实测 |
|---|---|
| Go | `go1.25.5 windows/amd64`，位于 `C:\Deepseek\_gotool\go\bin`（不在 PATH） |
| GOMODCACHE | `C:\Users\Jie\go\pkg\mod`，13.5 GB / 174 650 文件（预热） |
| CGO | 默认 `CGO_ENABLED=0`；gcc 在 `C:\msys64\mingw64\bin\gcc.exe` |
| 内核构建冒烟 | 集成基线 `go build ./constant ./log ./common/...` **退出 0**（7.8 s），工作区保持干净 |
| Node/npm/pnpm | **全部缺失** |
| JDK | 仅 `java 1.8.0_461`；无 JDK 17，无 Android Studio |
| Android SDK | 存在（`C:\Deepseek\jiejiebox-work\android-sdk`），但缺 JDK 17 |
| Apple | 无 macOS/Xcode；`Frameworks/Runestone` 已检出源码，但无法构建 |

结论：内核 Go 侧可在本环境编译与测试；客户端平台构建在本环境 **BLOCKED**，第 05/06 阶段必须按 BLOCKED 记录。

### 12.7 临时工作区清理

本轮为验证建立了 3 个临时 detached worktree（`_probecheck`、`_probecheck2`、`_probecheck3`），验证完成后已 `git worktree remove --force` 删除；命令、退出码与完整输出保存在
`_int0145/audit-evidence/{go-build-smoke,pending-tests-on-candidate,pending-tests-on-base,review-probes-vet}.txt`。用户既有 worktree 一个未动。

---

阶段 01 内容提交：`c30c63e8eadd02f2b4ad2e57c6e268fab6b3c866`（本报告与 `INTEGRATION-LOG.md` 的首次提交）；本行由紧随其后的回填提交补充；§12 的收尾补做在后续提交中追加（以 `git log --oneline` 为准）。

**本阶段到此停止，不开始依赖合并。**
