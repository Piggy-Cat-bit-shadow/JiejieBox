# INTEGRATION-LOG.md

0.1.45 多仓库集成施工日志。每个阶段追加一条记录，并在条目中写清：基线、来源分支、最终 SHA、验证结果、遗留项。
阶段报告的正文在仓库根目录，文件名见各阶段条目。

## 使用约定

- 主仓库施工分支：`integration/0.1.45-final`；依赖仓库分支：`integration/0.1.45`。
- 主仓库集成工作区：`C:\Deepseek\_int0145\jiejiebox`；依赖工作区：`C:\Deepseek\_int0145\<repo>`。
- 任何 pin、gitlink、source-lock 的改动只在第 06 阶段冻结；前五个阶段只产出依赖侧最终 SHA 与证据。
- 报告中的测试状态只有 `PASS` / `FAIL` / `BLOCKED` / `NOT RUN` 四种；未执行的一律不得写成 PASS。
- 本日志不复制 `source-lock.json` 的 SHA——SHA 的唯一事实来源是树本身与 `release/source-lock.json`。

---

## Stage 01 — 仓库盘点、安全冻结与集成基线建立

- 执行日期：2026-11-11（Asia/Shanghai）
- 阶段报告：`01_AUDIT_AND_FREEZE.md`
- 阶段目标：盘点 13 个仓库的真实本地工作与远端状态；不丢失用户工作；建立隔离的集成工作区；重建分支与依赖矩阵；修正后续阶段计划。本阶段不做任何合并。

### 基线

| 项 | 值 |
|---|---|
| 主仓库 | `Piggy-Cat-bit-shadow/JiejieBox`（旧 URL `sing-box` 仍重定向） |
| 远端默认分支 | `testing` |
| `testing` | `4152caf0e58f0cca9ce8044416c7c0b8d9960428` |
| 候选基线 | `candidate/w07-2026-11-10` = `0b1b6a3b2f0d2e3f3d2f580a4bc77d769ff57e20` |
| 集成分支 | `integration/0.1.45-final`，从候选 tip 新建，未推送 |
| 依赖基线 | 各仓库 `integration/0.1.45` 的起始点 = 主仓库 candidate 树中记录的 pin（见下表；本阶段未改任何 pin） |

依赖 `integration/0.1.45` 起始点（= 主仓库 candidate 树中的 pin）：

| 仓库 | 起始 SHA | 说明 |
|---|---|---|
| sing | `1729220c46c4cf750726b1057514a8d52a1f5e3b` | = `origin/dev` tip |
| sing-tun | `9c3dcb2b1edfde31798981aa05f08851c0eee401` | pin；`dev` 领先 13 |
| sing-quic | `1ef2597cc1298146fcdddfeaa0384999f890d0c3` | = `origin/main` tip |
| quic-go | `bca854425f4daf5818ef57c6702de548b27f222b` | 两条功能分支共同基点 |
| utls | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` | = `origin/main` tip |
| cronet-go | `7ce9c49b464ad90629a13061309a58c89890fea6` | `all` pin；含 root pin `362e63b14ce2` |
| wireguard-go | `a765d957fd814e5c0ee579936a0780e73d66e533` | = `fork/awg3-inject` tip |
| sing-box-for-apple | `7fce7117b33a38ef4c73c81db67c49f0be7a7dad` | 在 `next/client-consumption` 上，不在 `main` |
| sing-box-for-android | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | = `origin/main` tip |
| sing-box-for-desktop | `aa63764a61c312562a348e174522d1dc1c86d954` | = `origin/main` tip |
| sing-box-dashboard | `fc1dbb4a48bb06358d549de05eb90df17ab7110d` | = `origin/main` tip（Desktop 内嵌 pin 同点） |
| satelite-one | `c9a07350e4c7a4d60a918488478893de5a49b6ac` | = `origin/main` tip；独立产品仓库 |

### 本阶段动作

1. 重新 `ls-remote` 全部 13 个仓库并核对默认分支；`testing`、候选 tip 与旧快照一致，候选未被改写。
2. `git fetch --all --tags` 更新 12 个主 clone 的 remote-tracking refs（未触碰工作区）。
3. 扫描 112 个 clone/worktree：105 干净，7 个非干净，全部原样保留；把未推送提交与未跟踪文件备份到
   `C:\Deepseek\_int0145\backup\`（bundle、diff、loose 文件）。
4. 重算主仓库 24 条 topic/local 分支的 merge-base、left/right、patch-id、文件差异（数据：`_audit01/matrix/`）。
5. 建立主仓库 `integration/0.1.45-final` + 12 个依赖仓库 `integration/0.1.45` worktree，全部 `git status` 干净。
6. 复核 `release/dependencies.json`、`.gitmodules`、`go.mod`、`test/go.mod`、`release/source-lock.json`、
   `release/apple-client-refs.env` 与客户端 gitlink 的一致性。

### 关键结论（详见阶段报告）

- 候选基线未变；`testing` 另有 2 个文档提交（由 `kb/0.1.45-02` 携带），第 06 阶段吸收。
- `next/config`（6 提交）、`next/protocols`（2 提交）与基线 patch 全部等价 → 已被吸收，无需重复移植。
- `next/resources` 是 `closeout/resources` 的祖先。
- `fix/task09-repair` 已包含本地 `review/task09-final` → `fix/task09-final` 全部内容；唯一未含的是
  `review/task09-dual-stack` 的 2 个 review 探针提交（4 个 `zz_review_probe*_test.go`）。
- **`backup/02-ecfffe7d0`（本地别名 `next/routing-dns`）不是旧归档**：tip `ecfffe7d0691`，时间为 2026-11-10 21:42，
  44 个提交与任何 topic 分支 0 个 patch 等价，是活跃并行线（libbox/daemon 流控、连接快照）。
- **Apple pin 不在 `main` 上**：`7fce7117b33a` ∈ `next/client-consumption`，`main`=`97b35d1ce242` 是其祖先。
- `wireguard-go` 在 `go.mod`/`source-lock` 中存在，但 `release/dependencies.json` 的 `go_modules` 未声明。
- 本地未推送工作：主仓库 6 条分支、Desktop 4 个提交、Apple 子模块 1 个提交、cronet-go 2 个提交（含重建原生库）。

### 文件变更

- 新增 `01_AUDIT_AND_FREEZE.md`
- 新增 `INTEGRATION-LOG.md`（本文件）
- 未修改任何源码、配置、pin 或 gitlink。

### 验证

| 命令 | 退出码 | 结果 |
|---|---|---|
| `git ls-remote --symref`（13 仓库） | 0 | 与记录一致 |
| `git fetch --all --tags`（12 clone） | 0 | 仅更新 remote-tracking refs |
| `git status --porcelain=v1`（112 clone/worktree） | 0 | 105 干净 / 7 非干净（已备份） |
| `git worktree add`（13） | 0 | 全部干净、HEAD = 预期 pin |
| `git bundle create`（5） | 0（3 个非空） | 备份完成 |
| 子模块初始化（本地 clone + `protocol.file.allow=always`） | 0 | 5 个嵌套子模块按 gitlink 检出 |
| `go build ./constant ./log ./common/...` | 0 | 集成基线冒烟构建通过（7.8 s） |
| `go test ./common/runtimecoord`（pending 测试 @ 候选） | 0 | 4/4 PASS |
| `go test ./experimental/libbox`（pending 测试 @ 候选） | 1 | 3 PASS / 4 FAIL；同组测试在分支 base 上同样 4 FAIL |
| `go vet ./common/dialer ./dns`（review 探针 @ task09-repair） | 0 | 探针可编译 |

以上构建/测试只用于审计判定（工具链、吸收证据、分支可移植性），**不是** 0.1.45 验收测试：`go test ./...`、`-race`、lint、CI、平台构建均为 **NOT RUN / BLOCKED**（缺 Xcode、JDK 17、Node/pnpm）。

### 收尾补做（同日追加，详见报告 §12）

- 子模块：`clients/{apple,android,desktop}`、`desktop/dashboard`、`apple/Frameworks/Runestone` 已按 gitlink 初始化；
  `dashboard/vendor/iterm2-color-schemes` 与 `cronet-go/naiveproxy` 保持未初始化并记录原因。
- 新发现 4 个嵌套依赖并登记（Runestone、iterm2-color-schemes、naiveproxy + dashboard 本身）。
- `next/config`、`next/protocols`：逐个 blob 与候选比对（22/23、2/3 完全相同，其余为候选更新的测试），确认**已完全吸收**。
- `pending/device-axis-reload-window-tests`：本地未跟踪文件与分支文件逐字节相同；`runtimecoord` 4/4 PASS，`libbox` 4 FAIL 且在其 base 上同样 4 FAIL → 不是候选回归。
- `review/task09-dual-stack` 的 4 个探针文件在 `fix/task09-repair` 上可编译（`go vet` 退出 0）。
- 工具链：Go 1.25.5 @ `C:\Deepseek\_gotool\go\bin`，GOMODCACHE 13.5 GB，gcc @ `C:\msys64\mingw64\bin`；无 Node/pnpm、无 JDK 17、无 macOS。
- 临时 worktree（`_probecheck*`）已删除；证据输出在 `_int0145/audit-evidence/`。

### 提交

- Stage 01 内容提交：`c30c63e8eadd02f2b4ad2e57c6e268fab6b3c866`（本行由紧随其后的回填提交写入）
- 收尾补做提交：见 `git log --oneline`（本文件所在分支）
- 本阶段未推送任何远端引用。

### 遗留 / 下一阶段注意

1. `dashboard/vendor/iterm2-color-schemes` 与 `cronet-go/naiveproxy` 未初始化；前者随第 05 阶段 dashboard 构建处理，后者随第 06 阶段 native 重建处理（命令与阻断原因见报告 §12.1）。
2. `backup/02-ecfffe7d0` 按并行工作线处理，不得整支 merge，也不得当旧备份忽略。
3. cronet-go 的 29 个原生模块保持 controlled；本地 `feat/per-dial-quic` 携带的重建归档来源待查证。
4. 平台构建在本机 **BLOCKED**（无 Xcode/JDK 17/Node），第 05/06 阶段不得标 PASS。

---

## Stage 02 — 底层依赖仓库收敛

- 执行日期：2026-11-11（Asia/Shanghai）
- 阶段报告：`02_DEPENDENCY_INTEGRATION.md`；机器可读清单：`release/dependency-final-shas.json`
- 阶段目标：把 sing-tun、quic-go、wireguard-go/AWG、cronet-go 与其余依赖收敛到可验证 SHA；**不改主仓库 pin**（留第 06 阶段）。

### 基线与结果

| 仓库 | 施工前 pin | 最终 SHA | 动作 |
|---|---|---|---|
| sing-tun | `9c3dcb2b1edf` | `57e9d17fa852eafbc6849ae508c8b772e9fa1a66` | fast-forward（13 提交） |
| quic-go | `bca854425f4d` | `ca9992bc795e8699db2b22ad6459216e6f13649d` | ff 到 path-length-fix + cherry-pick `0b8709eb` |
| wireguard-go | `a765d957fd81` | `a765d957fd81` | 不集成（awg-base/awg-graft 已被 pin 覆盖） |
| sing / sing-quic / uTLS / Android | 各自 pin | 不变（= 远端 tip） | 仅复核 |
| cronet-go | root `362e63b14ce2` / all `7ce9c49b464a` / 29 native `362e63b14ce2` | 不变 | controlled，未动 |
| satelite-one | 独立产品仓库 | `c9a07350e4c7` | 只登记 |

主仓库 `go.mod`、`test/go.mod`、gitlink、`source-lock.json` **未改**。

### 关键判断

- sing-tun 13 个提交是同一条 tun-memory/UDP-NAT 修复线（close 清理 waiter、跨队列字节预算唤醒、detach 归还 quota、caller queue 记账、单帧超预算错误、UDP NAT 出队归还），5 生产文件 +379/−48、13 个测试文件；没有夹带实验，因此整体接受。
- quic-go 两条分支共享 2 个提交、各 1 个独有提交且文件不重叠：ff 带入共享部分，cherry-pick task08 的 http3 引用计数修复；融合后内核 `transport/v2rayquic` 的迁移/关闭 race 套件（26 测试）全通过。
- wireguard-go：内核确实使用 `Device.InputPackets` / `Peer.WritePackets`，但 pin `awg3-inject` 已包含 awg-base 的两个独有提交（PSK 懒建 peer、阻塞注入）与 awg-graft 的全部 `obf_*.go`，且更完整；两分支标记 superseded，待第 06 阶段归档。
- cronet-go：root/all 顺序与 29 个 native 模块一致；本地 `feat/per-dial-quic` 的原生归档与 `release/cronet-provenance.txt` 字节不同，controlled 策略下不得采纳。

### 验证

| 命令 | 退出码 | 结果 |
|---|---|---|
| sing-tun 定向 tun-memory/UDP-NAT 测试 | 0 | 49 PASS / 0 FAIL |
| sing-tun `./...` minus admin-only | 0 | 4 包 ok |
| sing-tun `-race ./...` minus admin-only | 0 | 4 包 ok |
| sing-tun `./...`（含 admin-only） | 1 | `TestGoKernel*`（Access denied）与 winipcfg 系统变更测试 —— BLOCKED_BY_ENVIRONMENT |
| quic-go `go test -vet=off ./...` | 0 | root/http3/handshake/utils/tree 全 ok |
| 内核全量构建（Windows tag profile，新依赖） | 0 | 35.5 s，整棵树通过 |
| 内核 `protocol/tun` | 0 | 含 `TestPinnedSourceIsTheReplacedFork` |
| 内核 `common/httpclient` | 0 | ok |
| 内核 `transport/v2rayquic -race` | 0 | 26 测试全过 |
| 内核 AWG 相关测试 | 0 | 8 PASS |

### 提交

- 主仓库：本阶段报告 + `release/dependency-final-shas.json` 的文档提交（见 `git log --oneline`）。
- sing-tun：分支 = `dev` tip，无额外提交（最终 pin 直接使用已推送的 `57e9d17fa852`）。
- quic-go：`ca9992bc795e`（本地，**未推送**）。
- 其他依赖：无提交。

### 收尾补做（同日追加，详见报告 §8）

- quic-go `integration/0.1.45`（`ca9992bc795e`）已推送到 `Piggy-Cat-bit-shadow/quic-go`，推送后核对远端 SHA 一致；canonical pseudo-version `v0.61.1-0.20261110165647-ca9992bc795e` 已从 GitHub 解析成功。
- 清除了早先由本地 partial clone 造成的 `GOMODCACHE/cache/vcs/058385…` 损坏缓存；重新从 GitHub 克隆后解析正常（该目录只是缓存）。
- 用两个**真实 pseudo-version** 重跑了主仓库验证：全量 Windows-profile 构建 exit 0、`protocol/tun` exit 0、`common/httpclient` exit 0、`transport/v2rayquic -race` 26 测试 exit 0。
- 证据更新在 `_int0145/audit-evidence/stage02/06a..06d`。

### 遗留 / 下一阶段注意

1. quic-go 的推送已完成，第 06 阶段冻结 pin 时无需再推送；sing-tun 的 `57e9d17fa852` 本来就在 `origin/dev` 上。
2. 第 03/04 阶段可以直接用两个真实 pseudo-version（`v0.0.0-20261110141903-57e9d17fa852`、`v0.61.1-0.20261110165647-ca9992bc795e`），预览构建不再需要本地 replace。
3. `awg-base`/`awg-graft`、cronet 的 `feat/per-dial-quic` 保持原样，交给第 06 阶段按证明流程处理。
4. 平台构建（Apple/Android/Desktop/Dashboard）仍 BLOCKED（缺 Xcode/JDK 17/Node），本阶段未执行、未标 PASS。
---

## Stage 03 — 内核网络生命周期、连接管理与资源治理

- 执行日期：2026-11-11（Asia/Shanghai）
- 阶段报告：`03_NETWORK_RESOURCE_INTEGRATION.md`；状态/所有权：`03_STATE_AND_OWNERSHIP.md`
- 阶段目标：把 `next/network` 的网络生命周期与 `closeout/resources`（含 `next/resources`）的资源治理作为一个系统集成；依赖使用阶段 02 的 sing-tun/quic-go 集成 SHA，正式 pin 留第 06 阶段。

### 基线

| 项 | 值 |
|---|---|
| 阶段依赖基线 | `ea5651f2b5ba92e2544dc9546a9890dbee3b5115`（go.mod/test 切到阶段 02 SHA） |
| `next/network` | `b9bc2bc25981e4f4606eaa5298ded56fde8d312d`（36 独有） |
| `closeout/resources` | `aafc4c11f022a32de82e065ad03450183b716d97`（21 独有） |
| `next/resources` | `b00dfe1f0e08`，closeout 的祖先 |
| 结果 | 内容提交 42 个（依赖基线与文档提交另计），代码与测试文件 45 个（+7980/−111） |

### 本阶段动作

1. `go.mod` / `test/go.mod` 的 sing-tun、quic-go replace 提升到阶段 02 集成 SHA（预览 pin，source-lock 未手改）。
2. `next/network`：36 个独有提交中 34 个原样 cherry-pick（0 冲突）；`dad5fc31bc6` 为纯 pin 丢弃；`ce4ae88a5e0` 只取 TrustTunnel 测试 hunk（pin 由阶段 02 覆盖）。
3. `closeout/resources`：取 6 个内容提交（xhttp shaped A/B、resourceguard relay A/B、e2e UDP detour + 窄 MTU）；10 个 sing-tun pin 递增提交与 6 个 merge 提交证明为纯 pin/聚合，不移植。
4. `backup/02-ecfffe7d0`：逐项审查，独有内容（route diagnose API、process-cache reset epoch、客户端流控）不在本阶段范围，记录给第 05/06 阶段。
5. `pending/device-axis-reload-window-tests`：移植 `common/runtimecoord` 测试（4/4 PASS）；两个 libbox 测试在其自身 base 即红，拒绝移植。
6. 输出状态机、所有权表、加锁顺序与不变量清单（`03_STATE_AND_OWNERSHIP.md`）。

### 关键判断

- 网络分支与候选的两处"更新内容"（`resourceguard.GrowthBudget`、xhttp `sharedUplinkBudget`）被保留：逐文件比对 43 个分支文件，39 个一致，4 个差异全部是"我们更新"（go.mod/go.sum + 这两个文件），没有旧实现覆盖新实现。
- 网络切换不再把旧 generation 的物理关闭放进过渡体：drain → DNS 固定 → 提交 → 并发监听器（统一 deadline）→ 单一 disposer 按连接预算关闭。
- 准入票据与 shutdown 原子；控制平面 DNS 有独立 4 槽预留；backlog 满时新拨号背压可取消。

### 验证

| 命令 | 退出码 | 结果 |
|---|---|---|
| `go build -tags <Windows profile> ./...` / `go build ./...` | 0 / 0 | 通过 |
| `go test ./route`（全量） | 0 | 700 个测试函数通过（447.9 s） |
| 网络相关 24 包（route/rule、dialer、adapter、dns、daemon、transport） | 0 | 全部 ok（75.8 s） |
| `-race` 28 个新生命周期测试 | 0 | 28/28 PASS |
| `-race` v2rayquic/v2rayxhttp/dialer/resourceguard | 0 | 4 包 ok |
| 12 个协议包（carrier recovery 等） | 0 | 全部 ok（naive 需 pin 的 libcronet.dll 在 PATH） |
| e2e `TestTunUDPDetourChain` | 0 | 3 个子测试 PASS（含窄 MTU） |
| daemon selector | 0 | 6/6 PASS |
| runtimecoord（pending 移植） | 0 | 全部 PASS |
| 广域回归 `./common/... ./config/... ./provider/... ./option/... ./constant/...` | 1 | 32 包 ok；`common/tls`、`common/tlsspoof`、`common/windivert` 失败 |
| 根包 `github.com/sagernet/sing-box`（全量） | 0 | 通过（69.4 s，测试 22.6 s）——修好 control-plane wrapper 后 |
| Windows 全量 `./...` | 1 | 106 包 ok；仅 `common/tls`、`tlsspoof`、`windivert` 三个既有/权限失败 |
| Linux 构建（WSL，Go 1.25.5） | 0 | `go build ./...` 与 `-tags <others>` 均通过 |
| Linux FD ledger `-race` | 0 | PASS（账本 = `/proc/self/fd`） |
| Linux 真实 TUN（`unshare -Urn`） | 0 | 3/3 PASS（dead-flow release 实测 5 ms） |
| Linux route 全量 / race batch / e2e | 0 / 0 / 0 | 通过（443.5 s / 4 包 / 15.5 s） |
| Linux 全量 `./...` | 1 | 113 包 ok；`common/netns TestUnshareNamespace` 为 WSL 环境限制（基线同样失败） |

失败归因：`tlsspoof`/`windivert` 是 WinDivert 驱动的 SCM 权限拒绝（BLOCKED_BY_ENVIRONMENT）；`common/tls` 的 Windows TLS1.3 客户端失败在阶段依赖基线 `ea5651f2b5b` 上**同样复现**，属既有问题，非本阶段引入。

未执行：Windows 管理员用例（`TestGoKernel*`、WinDivert/tlsspoof）与平台构建保持 BLOCKED/NOT RUN；Linux-only 用例已在本机 WSL 补跑（见上表）。

### 提交

- 主仓库：本阶段内容提交 42 个（34 个 network cherry-pick + 1 个 TrustTunnel hunk + 6 个资源测试 + 1 个 pending runtimecoord），另有 1 个依赖基线提交与文档提交，见 `git log --oneline`。
- 集成回归修复：`a51c3dac340` `fix(dns): keep the control-plane wrapper transparent to the detour preflight`。
  `controlPlaneDialer` 缺少 `Upstream()`，隐藏了 `DetourDialer`：detour 不在 Start 初始化，"does not carry" 警告被静默丢弃。用 `git bisect` 定位到 `412ad7742a1` 的 cherry-pick（`d93ea4e63f5`），补 `Upstream()` 并加 `dns/transport_dialer_test.go` 链契约测试；根包全量恢复通过。证据：`audit-evidence/stage03/12-integration-regression.txt`。
- 依赖仓库：无新增提交（阶段 02 已完成）。

### 遗留 / 下一阶段注意

1. `go.mod` 的两个 replace 是预览 pin，`release/source-lock.json` 仍是 0.1.44 快照；第 06 阶段必须用 release-freeze 生成，不得手改。
2. Linux 用例已在本机 WSL 补跑（FD ledger、真实 TUN、route 全量、race batch、e2e）；第 06 阶段仍应在目标 Linux runner 复跑。Windows 管理员用例仍需有权限环境。
3. backup 的 route diagnose、process-cache reset epoch、客户端流控未集成；若 0.1.45 需要，需单独立项。
4. 第 04 阶段把 H3/H2 回退并入后，要复核监听器 deadline 预算；不要用旧分支文件覆盖 `route/conn.go`、`transport/v2rayxhttp/client.go` 中候选已有的 GrowthBudget / sharedUplinkBudget。
5. 根包 `github.com/sagernet/sing-box` 的测试已列入常规矩阵；它曾在按包 sweep 之外，导致 `dns/transport_dialer.go` 的 wrapper 回归漏检。后续阶段每次改 `common/dialer`、`dns`、`route` 接口都要跑根包。
---

## Stage 04 — 双栈 DNS、拨号恢复与 H3/H2 回退集成

- 执行日期：2026-11-11（Asia/Shanghai）
- 阶段报告：`04_DUALSTACK_H3H2_INTEGRATION.md`
- 阶段目标：先集成双栈 DNS/拨号恢复，再集成 H3/H2 回退，最后联合回归；依赖沿用阶段 02 的 sing-tun/quic-go 集成 SHA。

### 基线

| 项 | 值 |
|---|---|
| 施工基线 | `e8a473447c31487656e8ecd3119ce26e8a23e7b2`（阶段 03 收尾） |
| `fix/task09-repair` | `31a3e9acea1b`（31 独有：27 非 merge + 4 merge） |
| `task08/h3-h2-fallback-2026-11-10` | `684ebf516767`（39 独有：35 内容 + 4 pin） |
| 结果 | 62 个内容提交，75 文件（47 新增 / 28 修改），+11317/−550 |

### 本阶段动作

1. task09：27 个非 merge 提交全部 cherry-pick（0 冲突）；4 个 merge 跳过。随后把分支触及的 40 个文件与 `fix/task09-repair` tip 逐个比对，**40/40 相同**。
2. task08：35 个内容提交全部 cherry-pick（0 冲突）；4 个 quic-go pin/source-lock 提交跳过（阶段 02 的 `ca9992bc` 已含其代码）。分支 40 个文件中 35 个一致，5 个是 pin 文件（本树更新）。
3. 配置恢复测试去重：`integration/next-round`、`docs/task09-knowledge-0.1.45` 与本树的 `daemon/config_recovery_*` 及 `daemon/testdata/product-abc/**` 为同一批 blob，只保留一份；两分支文档差异归第 06 阶段。
4. 联合回归 + race + Windows/Linux 全量；两个疑似回归经重复测量证明是既有测试脆弱性（见报告 §7.5）。

### 验证

| 命令 | 退出码 | 结果 |
|---|---|---|
| 构建（Windows profile / 无 tag） | 0 / 0 | 通过 |
| 双栈相关 15 包（Windows） | 0 | 全部 ok |
| H3/H2 相关 6 包（Windows） | 0 | 全部 ok（transport/http 74.7 s） |
| 联合回归 9 包（根包+daemon+dialer+dns+httpclient+transport/http+naive+masque） | 0 | 全部 ok |
| `-race` dialer/dns/httpclient/naive + transport/http 子集 | 0 / 0 | 通过 |
| 根包全量（Windows） | 0 | 通过 |
| Linux 定向（双栈/H3-H2/race） | 0 | 通过 |
| Linux 全量（干净、无并发负载） | 1 | 112 包 ok；`common/netns`（WSL 环境）与 2 个既有 flaky goroutine 测试失败（两者已在收尾中处理，见下） |
| Linux 全量（收尾修复后） | 1 | **113 包 ok**；仅 `common/netns TestUnshareNamespace`（WSL 不允许命名空间内 fork） |
| Windows 全量（干净） | 1 | **106 包 ok**；仅 `common/tls`（既有）、`tlsspoof`/`windivert`（权限） |

未执行：平台构建（Apple/Android/Desktop/Dashboard）保持 BLOCKED。真实 netns 设备验收已在收尾中跑通（见下）。

### 关键判断

- **不整支 merge**：两条来源分支基线分别落后 42/52 个提交；逐个 cherry-pick 保留提交语义，且文件级比对证明没有遗漏 merge 解决内容。
- **pin 提交按内容跳过**：task08 的 4 个 pin/source-lock 提交指向 `4b11a7be`，阶段 02 的 quic-go `ca9992bc` 已包含；不重复改动 pin。
- **配置恢复只留一份**：三方 blob 相同，避免同名测试堆叠。
- **疑似回归不误判**：goroutine 计数测试在基线隔离运行 5/5 失败，且 `NumGoroutine` 高于 `runtime.Stack` 实际列表，属测试脆弱性；e2e 超时是并发负载导致（空闲 5/5 通过）。未为"变绿"改产品代码或弱化断言。

### 遗留 / 下一阶段注意

1. 真实 netns 设备验收已在本机 WSL 跑通（非特权 `unshare -Urnm` + 真实 veth/命名空间），第 06 阶段仍应在目标 runner 复跑。
2. 根包两个 goroutine 计数测试的脆弱性已在收尾中修复（`a2e24231eaf`，仅测试；断言不变），Linux 隔离 5/5、根包全量、Windows 3/3 均通过。
3. `source-lock.json` 仍是 0.1.44 快照，第 06 阶段统一生成；依赖 pin 维持阶段 02 值。
4. 第 05 阶段不要替换 `common/httpclient`/`transport/http` 的 H3 verdict 分类、body 释放路径与 H2 gate；客户端集成应复用这些语义。

### 收尾补跑（2026-11-11 04:00）

- **真实 netns 设备验收 PASS**：在 WSL2 内用非特权 `unshare -Urnm` 搭真实 fixture（`t09peer`/`t09target` veth + `2001:db8:99::/64`、root↔`t09rmirror` veth 且 mirror 无路由、`nodad` 地址、显式 `ip link set lo up`），两个测试都通过：
  - 正向：host 仍按 family verdict 拒绝；namespaced 拨号的 socket 本地地址为 `[2001:db8:99::2]`，`pong` 往返成功；
  - 反向：host 连到 `10.99.0.1:9001`；mirror 命名空间以 `ENETUNREACH` 失败。
  证据：`stage04/linux/15-netns-device-real.log`。
- **goroutine 计数测试修复** `a2e24231eaf`（仅测试）：共享 `settleGoroutines` 等 8 次连续相同采样（200 ms 稳定窗，5 s 上限），断言不变。Linux 隔离 5/5 PASS、根包全量 PASS；Windows 3/3 PASS。
- **最终全量**：Linux **113 包 ok**（仅 `common/netns` WSL 环境项）；Windows **106 包 ok**（仅 `common/tls` 既有、`tlsspoof`/`windivert` 权限）。
---

## Stage 05 — NE/Libbox 运行时与客户端集成

- 执行日期：2026-11-11（Asia/Shanghai）
- 阶段报告：`05_CLIENT_RUNTIME_INTEGRATION.md`；接口契约：`05_CLIENT_RUNTIME_CONTRACT.md`
- 施工基线：`7192a10cc9c`（阶段 04 收尾）

### 内核 NE/Libbox

| 项 | 结果 |
|---|---|
| 源分支 | `fix/task10-apple-ne-runtime-final` @ `a3340d1a759d`（8 独有，23 文件，+1957/−83） |
| 集成 | **8/8 cherry-pick，0 冲突**；21/23 文件与分支 tip 一致 |
| 2 个差异文件 | `daemon/started_service.go`、`experimental/libbox/command_client.go`：本树带更新的 `SelectOutboundResponse` 与连接流 instance/sequence；源分支的 apply 报告/取消守卫/overrides 改动已全部落地，属“本树更新”而非丢失 |
| ABI | libbox 仅新增 `CommandServer.ApplyServiceChangeReport`；daemon 新增 `ApplyServiceChange`/`ApplyProviderDomainContext`；无删除/签名变化；proto 未变 |
| 构建 | `go build ./...` 与 `-tags <win>` 均 0 |
| 定向测试 | daemon / provider/remote / provider/local / libbox / boxdd 全部 ok（36.0 s） |
| race | `-race daemon provider/remote libbox` 全部 ok（162 s） |
| Windows 全量 | 106 包 ok；仅 `common/tls`（既有）、`tlsspoof`/`windivert`（权限） |
| Linux 全量 | 见 `05_CLIENT_RUNTIME_INTEGRATION.md` §7.1 |

### Dashboard / Desktop（已真正构建+测试）

- 便携 Node **v22.23.4** 与项目声明的 **v26.7.0**（两个都跑通）+ pnpm **11.13.0**（`_int0145/tools/`，不进仓库）；`@bufbuild/buf` 生成 protobuf；dashboard 的 `vendor/iterm2-color-schemes` 子模块初始化。
- **Dashboard**：`integration/0.1.45` 由 `fc1dbb4` fast-forward 到 **`62f1582`**；在 Desktop 布局中 **98/98 tests PASS**、`pnpm build` PASS。
- **Desktop**：`integration/0.1.45` 由 `aa63764` fast-forward 到 **`28dc4a9`**（4 提交），dashboard gitlink `62f1582`；`pnpm install/generate/typecheck/test/build` 全 0，**59/59 tests PASS**。
- 内核 `clients/desktop` gitlink：`aa63764` → **`28dc4a9`**（提交 `1fe0d9a6432`）。
- 备注：Dashboard 独立仓库跑测试会因 `@shared/*` 别名（指向 `../src/shared`）失败，验收必须在 Desktop 布局内进行。

### Apple（BLOCKED，未合并）

- `main=97b35d1`、`next/client-consumption=7e2f5c4`（+15）、`fix/task10-apple-ne-runtime-final=b2596b5`（+25）；next-only 5 / NE-only 15。
- Trial merge：**3 个冲突文件**（`Profile+Update.swift`、`RemoteRefreshApplier.swift`、`RemoteProviderRefreshTests.swift`），34 个自动合并。
- 本机无 macOS/Xcode：不合并、不构建；pin 保持 `7fce711`；分析见 `audit-evidence/stage05/03-apple-analysis.txt`。

### Android / satelite-one

- Android：pin `c783710` = `origin/main`，无改动。
- satelite-one：`main=c9a0735`、`next/subscription-refresh=9cdf782`（+16）；有本地 `integration/0.1.45` worktree 但仍在 main。缺 JDK17 + Android SDK（本机 Java 1.8，无 ANDROID_HOME），**BLOCKED**，未合并/未构建。

### 遗留

1. Apple 语义融合需要 macOS + Xcode；trial merge 的 3 个冲突文件是入口。
2. ~~Desktop `engines.node=26.7.0` vs Node 22~~：已在声明的 **26.7.0** 上重跑全部通过（见下方收尾补跑），无遗留。
3. `pnpm generate` 产物不入库，CI/打包必须先 generate。
4. `release/dependencies.json` 的客户端 intent 仍是 `branch: main`；第 06 阶段 freeze 以 gitlink 为准或先合并到 main，不能按 intent 解析出旧 tip。
5. `source-lock.json` 仍是 0.1.44 快照；第 06 阶段统一生成。

### 收尾补跑（2026-11-11 05:00）

- **Node 26.7.0 验证**：按 desktop `engines` 声明的版本重跑 install/generate/typecheck/test/build，9 个步骤全部 exit 0；dashboard **98/98**、desktop **59/59** 不变。证据 `stage05/07-clients-node26.log`。
- **satelite-one 尝试**：补装 JDK **17.0.21**（Temurin 便携版），复用已有 `C:\Deepseek\sdk`（Android 36 / build-tools 36）与缓存 Gradle 9.7，跑 `:app:testDebugUnitTest`/`:app:assembleDebug`。**失败在 `app/libs/libbox.aar` 缺失**（`verifyCoreProvenance` 输入不存在），Kotlin 编译报 libbox 符号未解析。阻塞点是内核 Android AAR（gomobile + NDK + provenance），不是 JDK/SDK；未合并。证据 `stage05/08-satelite-jdk17.log`。

---

## Stage 06 — 最终冻结、全量验收与分支清理

- 执行日期：2026-11-11（Asia/Shanghai）
- 报告：`FINAL_0.1.45_INTEGRATION_REPORT.md`、`FINAL_DEPENDENCY_LOCK_REPORT.md`、`FINAL_TEST_MATRIX.md`、`CLEANUP_REPORT.md`

### 冻结

- `release/JIEJIE_VERSION` → **0.1.45**；`release/dependencies.json` 补 `wireguard-go`（`awg3-inject`），quic-go 声明改为 `integration/0.1.45`。
- `release-freeze.py resolve/check`：8 个 Go module + 3 client + nested dashboard + 29 controlled natives；**PASS**，无 diverged、无 pin 需要移动。
- `release-freeze.py snapshot` 由真实树生成 `release/source-lock.json`（`release_version=0.1.45`，提交 `69e8dcb457a`）；`verify --lock` **PASS**。
- 知识库：四个文档分支的共享报告按 blob 取最新相同版本，独有 7 份并入 `KNOWLEDGE/reports/0.1.45/`；INDEX/reports README 改写为 0.1.45 RC 归档，未堆叠重复段落。文档提交 `d6e2b12de60`。

### 全量验收（最终树）

| 项 | 结果 |
|---|---|
| 内核构建（无 tag / Windows profile） | PASS |
| Windows 全量 `./...` | 106 包 PASS；3 个既有/权限失败（tls/tlsspoof/windivert） |
| Linux 全量 `./...` | 113 包 PASS；仅 `common/netns`（WSL 环境）失败 |
| 客户端 Dashboard / Desktop | 98/98、59/59、typecheck、build 全 PASS（Node 26.7.0） |
| Apple / Android / satelite-one | BLOCKED（Xcode / AAR / Android 构建链） |
| release-freeze verify | PASS |

### 清理

- 创建 20 个本地 archive tag（主仓库 13 + Apple 2 + Desktop/Dashboard/satelite 3 + quic-go 2 + wireguard 2）。
- **未删除任何分支**（本地或远端）：RC 与 archive tags 未推送，按阶段 06 安全条件延期；`CLEANUP_REPORT.md` 给出逐分支证明与后续精确命令。
- 发现并记录：主仓库 `origin` remote 误指向 `sing-box-for-desktop`，推送必须使用显式 JiejieBox URL。

### RC

- source-lock 快照 `69e8dcb457a`；最终报告与 `dependency-final-shas.json` 所在提交为 RC 提交（见 `git log`）。
- 未推送、未合入 `testing`、未创建正式 release。

### Stage 06 收尾补跑（2026-11-11 05:40）

- RC 推送：`integration/0.1.45-final` → `b810d0c2603f11eb2127adf816eb9e2b080b4094`，远端 SHA 已核对（`jiejiebox` remote，显式 URL）。
- 20 个 archive tag 推送并核对；删除 15 个已吸收远端 topic 分支（详见 `CLEANUP_REPORT.md`），保留 backup/client-candidate/Apple/satelite/wireguard。
- Android core AAR：`build_libbox -target android`（JDK17 + NDK 28）产出 `libbox.aar` / `libbox-legacy.aar`，provenance `b810d0c`（version=SHA）。
- satelite-one：unit tests + assembleDebug **BUILD SUCCESSFUL**，`verifyCoreProvenance: OK`，integration ff 到 `9cdf782`。
- 官方 Android 客户端：`:app:assembleDebug` **BUILD SUCCESSFUL**（8m10s，universal/arm64/armv7/x86/x86_64）。
- 产物：`_int0145/artifacts/0.1.45/`（AAR ×2、APK ×2、provenance、SHA256SUMS）。

---

## 上游同步 Stage 02 — sing 与 sing-tun 融合

- 日期：2026-11-11
- 报告：`02_SING_SINGTUN_UPSTREAM_INTEGRATION.md`；台账：`docs/fork/UPSTREAM-INTEGRATION.md`

| 仓库 | 合并提交 | 上游 | 结果 |
|---|---|---|---|
| sing | `4abfaed41727`（父 `1729220c46c4` + `27b73774b240`） | `SagerNet/sing:dev` | 27 提交（19 新内容 + 8 等价），13 冲突融合；Compare ahead_by=0 |
| sing-tun | `99abf3c27666`（父 `57e9d17fa852` + `79ed0653fdc6`） | `SagerNet/sing-tun:dev` | 42 提交（8 新内容 + 34 同名），31 冲突（18 fork 保留 / 6 上游 / 13 融合）；Compare ahead_by=0 |
| 内核消费 | `33d6e4c62a4` | — | pin 更新 + `ReadRetry.Wait()`/`ErrTooManySegments` 适配 |

- 测试：sing build/`./...`/race（排除既有 checkptr）；sing-tun build/linux build+vet/根套件 112 PASS/race；内核 build（含 Windows tags）/`transport/device`/`protocol/tun`/`route` 全量 446.2 s 全过。
- 阻塞：`TestGoKernel*`（需管理员）、Linux/Darwin 特权与 syscall 路径仅交叉编译。
- 新增测试：sing CachedConn barrier 竞态 + 所有权；sing-tun GSO 跨批续切与 UDP 回复 IPv4 ID，均做 mutation 验证。

---

## 上游同步 Stage 03 — QUIC / TLS / WireGuard

- 日期：2026-11-11
- 报告：`03_QUIC_TLS_WIREGUARD_UPSTREAM_INTEGRATION.md`；台账：`docs/fork/UPSTREAM-INTEGRATION.md`

| 仓库 | 合并/最终提交 | 上游 | 结果 |
|---|---|---|---|
| sing-quic | `4a95023f3e26`（merge `7075cd7` + Salamander 测试） | `SagerNet/sing-quic:main` @ `5253224b67` | 4 提交，0 冲突；Compare ahead_by=0 |
| quic-go | `sagernet` `f864f2f1`（merge `7fde0eb4` + CUBIC 测试）；消费线 `158445cd` | `SagerNet/quic-go:dev` @ `2e73222e96` | 1 提交，0 冲突；Compare ahead_by=0；全量测试 PASS |
| utls | `e14b88879d80` | `MetaCubeX/utls:v1.9.0-mod-meta` @ `89a07ad380` | 1 提交，1 冲突（保留 fork marshal-time helper）；Compare ahead_by=0；全量测试 PASS |
| WireGuard | `awg3-inject` `a765d957` | `sagernet/wireguard-go:main` @ `e4d85ef2ed` | 已 0 落后，无需合并；device 测试 PASS |
| 内核消费 | `a6450700129` | — | sing-quic / quic-go / utls pin 更新；传输层消费者全过 |

- 新增测试：sing-quic Salamander 短包边界（旧代码失败、新代码通过）；quic-go CUBIC ACK-only pacing（旧顺序失败、新顺序通过）。
- 内核全量：仅 `common/tls`/`common/tlsspoof`/`common/windivert` 三个既有基线失败。
- 风险：Darwin msgx/sendto 路径无本机验证；sing-quic 三个 sender 的 pacing 由 quic-go 测试与消费者间接覆盖。

---

## 上游同步 Stage 04 — Naive/Cronet/XHTTP/Mieru 与客户端

- 日期：2026-11-11
- 报告：`04_NAIVE_CRONET_XHTTP_MIERU_CLIENTS_UPSTREAM_INTEGRATION.md`

- XHTTP：stream-up 非 200 已由 `96bd665` 修复、`669fa275` 覆盖；race PASS；未改代码。
- Naive/Cronet：root `362e63b` + all `7ce9c49` + 29 native `362e63b`；生成源 `4c96192` 已含上游 `73349ff`；上游平行 generation 不合并。
- Mieru v3.39.0 已是最新；MASQUE 无变化；消费者测试全过。
- Android `main` → `ba5c6b1`（upstream 5 提交），assemble 8m28s + unit 27s PASS，compare ahead_by=0。
- Dashboard `main` → `b592c65` 双父合并，97/97 + build PASS；消费线 `9bde90e` 98/98 PASS；compare ahead_by=0。
- Desktop 59/59 + build PASS，0 behind；Apple 0 behind，Xcode BLOCKED。

