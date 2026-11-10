# FINAL 0.1.45 集成报告

- 生成时间：2026-11-11（Asia/Shanghai）
- 集成分支：`integration/0.1.45-final`
- Release Candidate：包含本报告与 `release/source-lock.json`（`69e8dcb457a`）的提交；见文末 SHA。
- 版本：`release/JIEJIE_VERSION` = **0.1.45**。
- 结论：**内核与可执行客户端（Dashboard/Desktop）的 0.1.45 RC 集成完成**；Apple、Android/satelite-one 与 Windows 管理员用例因环境缺失保持 BLOCKED，未冒充 PASS（详见 `FINAL_TEST_MATRIX.md`）。

---

## 1. 六个阶段的范围与结果

| 阶段 | 内容 | 结果 |
|---|---|---|
| 01 | 仓库盘点与安全冻结 | 44 worktree / 子模块 / 备份完成；未触碰用户工作 |
| 02 | 依赖收敛 | sing-tun `57e9d17`、quic-go `ca9992bc`（含 usecount/path 修复）落地并验证 |
| 03 | 网络生命周期与资源治理 | 34 个 network 提交 + 6 个资源测试；FD/准入/回收/关闭语义 |
| 04 | 双栈 DNS 与 H3/H2 回退 | 27 + 35 个提交；联合回归；真实 netns 验收 |
| 05 | NE/Libbox 与客户端 | 8 个内核提交；Dashboard/Desktop 真正构建+测试；Apple/Android/satelite 阻塞 |
| 06 | 冻结、验收、清理 | source-lock 由树生成并 verify 通过；全量 Windows/Linux；归档标签与清理审计 |

各阶段的源 SHA、pick 映射与决策见 `INTEGRATION-LOG.md` 与 `0X_*` 报告。

---

## 2. 分支吸收方式

### 2.1 merge
**没有整支 merge。** 所有来源分支的基线落后 42–52 个提交，整支 merge 会把旧基线带回。

### 2.2 cherry-pick / 语义移植（有逐文件或逐提交证据）

| 来源 | 独有提交 | 处理 | 证据 |
|---|---|---|---|
| `next/network` | 34 | cherry-pick | 43 文件对比：39 相同，4 处为本树更新（go.mod/sum、conn.go、xhttp） |
| `closeout/resources` | 3 测试文件 | 直接采用（字节相同） | 逐文件 blob 比较 |
| `next/resources` | 6 | cherry-pick（含 aggregation） | 阶段 03 记录 |
| `fix/task09-repair` | 27 非 merge | cherry-pick，0 冲突 | 40/40 文件与分支 tip 一致 |
| `task08/h3-h2-fallback` | 35 内容 | cherry-pick，0 冲突 | 40 文件中 35 一致，5 个 pin 文件本树更新 |
| `fix/task10-apple-ne-runtime-final` | 8 | cherry-pick，0 冲突 | 23 文件中 21 一致，2 处为本树更新的 `SelectOutboundResponse`/连接流 API |
| `pending/device-axis-reload-window-tests` | 1 | 移植测试 | 4/4 PASS |
| `integration/next-round` 配置恢复测试 | 3 | 已包含在 task09 历史中 | 三处 blob 相同 |

### 2.3 知识库文档（不整支合并）

`integration/next-round`、`docs/task09-knowledge-0.1.45`、`task08/knowledge-0.1.45`、`kb/0.1.45-02` 的共享报告按 blob 取最新相同版本，独有的 7 份报告并入 `KNOWLEDGE/reports/0.1.45/`，INDEX 与 reports/README 改写为 0.1.45 RC 归档；未保留四套重复段落。

### 2.4 客户端

- Dashboard：`fc1dbb4` → **`62f1582`**（fast-forward，98/98 tests + build PASS）。
- Desktop：`aa63764` → **`28dc4a9`**（fast-forward，59/59 tests + typecheck + build PASS），dashboard gitlink `62f1582`；内核 `clients/desktop` → `28dc4a9`（`1fe0d9a6432`）。
- Apple：pin `7fce711` 未动（trial merge 3 冲突；无 Xcode）。
- Android：pin `c783710` = main，未动。
- satelite-one：未合并（缺内核 `libbox.aar`，JDK17/SDK 已具备）。

---

## 3. 最终 SHA

| 对象 | SHA |
|---|---|
| 内核集成分支（RC 前） | `69e8dcb457a41695224a094411a32c80fd39e09d`（source-lock 快照提交） |
| source-lock 版本 | `0.1.45` |
| sing | `1729220c46c4cf750726b1057514a8d52a1f5e3b` |
| sing-tun | `57e9d17fa852eafbc6849ae508c8b772e9fa1a66` |
| sing-quic | `1ef2597cc1298146fcdddfeaa0384999f890d0c3` |
| quic-go | `ca9992bc795e8699db2b22ad6459216e6f13649d` |
| utls | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` |
| wireguard-go | `a765d957fd814e5c0ee579936a0780e73d66e533` |
| cronet-go root / all / 29 native | `362e63b14ce2...` / `7ce9c49b464a...` / `362e63b14ce2...`（controlled） |
| Apple / Android / Desktop | `7fce7117b33a` / `c7837107dc01` / `28dc4a973bcd` |
| Desktop nested dashboard | `62f158255ce9` |
| Dashboard 集成分支 | `62f158255ce9` |
| satelite-one | `c9a07350e4c7`（未合并） |

机器可读清单：`release/dependency-final-shas.json`；锁：`release/source-lock.json`。

---

## 4. 可复现性

```sh
# 依赖意图与锁
python scripts/ci/release-freeze.py resolve --out /tmp/resolve.json
python scripts/ci/release-freeze.py check --resolve /tmp/resolve.json
python scripts/ci/release-freeze.py verify --lock release/source-lock.json

# 内核
go build ./...
go test -vet=off -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./...   # Darwin profile; 按目标平台选 profile

# 客户端（本机已执行）
node --version   # 26.7.0
cd clients/desktop && pnpm install --frozen-lockfile --ignore-scripts && pnpm generate && pnpm typecheck && pnpm test && pnpm build
cd dashboard && pnpm install --frozen-lockfile && pnpm generate && pnpm test && pnpm build
```

---

## 5. 完成标准对照

| 标准 | 状态 |
|---|---|
| 最终集成结果可复现 | ✅ 提交历史 + pick 映射 + source-lock + 报告 |
| source-lock 与实际依赖一致 | ✅ `release-freeze.py verify` PASS |
| 关键平台验收有证据 | ⚠️ 内核 Windows/Linux、Dashboard/Desktop 有；Apple/Android 无（环境） |
| 清理没有丢失独有工作 | ✅ 仅创建 archive 标签；未删除任何分支/远端 |
| 不自动合入 testing / 不建正式 release | ✅ 未推送、未合并 |

**判定**：作为 **0.1.45 RC**，内核与 Windows/macOS 客户端可执行部分达到集成完成；Apple/Android/satelite-one 的构建与真机验收属于外部环境阻塞，已逐项列出，不能据此宣称全平台通过。

---

## 6. 最终提交

本报告、`FINAL_DEPENDENCY_LOCK_REPORT.md`、`FINAL_TEST_MATRIX.md`、`CLEANUP_REPORT.md` 与 `release/dependency-final-shas.json` 所在提交即 RC 提交；`git log --oneline -1` 可读。其父提交 `69e8dcb457a` 为 source-lock 快照提交。
