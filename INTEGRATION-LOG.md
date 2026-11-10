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