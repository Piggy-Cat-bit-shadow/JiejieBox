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

`go build` / `go test` / lint / race / CI：**NOT RUN**（本阶段无代码变更，无需执行）。

### 提交

- Stage 01 内容提交：`c30c63e8eadd02f2b4ad2e57c6e268fab6b3c866`（本行由紧随其后的回填提交写入）
- 本阶段未推送任何远端引用。

### 遗留 / 下一阶段注意

1. 主仓库集成 worktree 的子模块尚未初始化，第 05 阶段开始前必须 `submodule update --init`（期望落点见报告 §7）。
2. `backup/02-ecfffe7d0` 按并行工作线处理，不得整支 merge，也不得当旧备份忽略。
3. cronet-go 的 29 个原生模块保持 controlled；本地 `feat/per-dial-quic` 携带的重建归档来源待查证。
