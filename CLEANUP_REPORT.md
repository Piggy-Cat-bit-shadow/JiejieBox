# CLEANUP REPORT — 0.1.45 分支清理审计

## 1. 本轮实际动作

| 动作 | 结果 |
|---|---|
| archive tags（本地） | 已创建 20 个，覆盖主仓库 13 个候选与 5 个子仓库的 9 个候选 |
| 本地 topic ref 删除 | **0**（每个候选仍有 worktree，或 archive 证明刚建立；按纪律不删正在使用/仍有引用的 ref） |
| 远端分支删除 | **0**（见 §5：RC 与 archive tags 尚未推送，条件 6 不满足） |
| 最终集成分支 | 保留 `integration/0.1.45-final`，未推送 |
| `testing`/`main`/`dev`/`gh-pages`/release tags | 未触碰 |

## 2. 候选与证明

主仓库远端真实 head（`git ls-remote https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git`）与本地 archive tag：

| 分支 | 远端 SHA | 独有工作去向 | 证明 | archive tag |
|---|---|---|---|---|
| fix/task09-repair | `31a3e9ace` | 阶段 04，27 pick | 40/40 文件与 tip 一致 | `archive/0.1.45/jiejiebox/fix-task09-repair` |
| task08/h3-h2-fallback-2026-11-10 | `684ebf516` | 阶段 04，35 pick | 40 文件中 35 一致，5 pin 本树更新 | `…/task08-h3-h2-fallback-2026-11-10` |
| fix/task10-apple-ne-runtime-final | `a3340d1a7` | 阶段 05，8 pick | 23 文件中 21 一致，2 处本树更新 | `…/fix-task10-apple-ne-runtime-final` |
| next/network | `b9bc2bc25` | 阶段 03，34 pick | 43 文件 39 一致，4 本树更新 | `…/next-network` |
| closeout/resources | `aafc4c11f` | 阶段 03，3 测试文件 | 字节相同 | `…/closeout-resources` |
| next/resources | `b00dfe1f0` | 阶段 03，6 提交 | cherry-pick 记录 | `…/next-resources` |
| integration/next-round | `94c86b9d1` | 配置恢复测试 3 提交（已在 task09 历史）+ 文档 | 三处 blob 相同；文档已归档 | `…/integration-next-round` |
| docs/task09-knowledge-0.1.45 | `595d65154` | 文档归档 | 4 份报告并入 `KNOWLEDGE/reports/0.1.45/` | `…/docs-task09-knowledge-0.1.45` |
| task08/knowledge-0.1.45 | `99cc3b514` | 文档归档 | H3/H2 报告并入 | `…/task08-knowledge-0.1.45` |
| kb/0.1.45-02 | `8405d1bbb` | 文档归档 | 02 报告 + TASK10 三份并入 | `…/kb-0.1.45-02` |
| next/client-consumption-candidate | `09fa790df` | 未使用（客户端在阶段 05 单独集成） | 无独有功能需吸收 | `…/next-client-consumption-candidate` |
| pending/device-axis-reload-window-tests | `0477e327e` | 阶段 03，测试已移植（4/4 PASS） | 测试文件已在本树 | `…/pending-device-axis-reload-window-tests` |
| backup/02-ecfffe7d0 | `ecfffe7d0` | 备份；44 个独有提交中仅 route diagnose/process-cache 等未纳入，属产品范围决定 | 阶段 03 记录 | `…/backup-02-ecfffe7d0` |

子仓库：

| 仓库 | 分支 | SHA | 去向 | archive tag |
|---|---|---|---|---|
| Apple | next/client-consumption | `7e2f5c4` | 未合并（BLOCKED） | `archive/0.1.45/apple/next-client-consumption` |
| Apple | fix/task10-apple-ne-runtime-final | `b2596b5` | 未合并（BLOCKED） | `…/apple/fix-task10-apple-ne-runtime-final` |
| Desktop | next/client-consumption | `28dc4a9` | 已 fast-forward 集成 | `…/desktop/next-client-consumption` |
| Dashboard | next/client-consumption | `62f1582` | 已 fast-forward 集成 | `…/dashboard/next-client-consumption` |
| satelite-one | next/subscription-refresh | `9cdf782` | 未合并（BLOCKED） | `…/satelite-one/next-subscription-refresh` |
| quic-go | fix/task08-h3-usecount | `0b8709eb` | 已融合进 `ca9992bc` | `…/quic-go/fix-task08-h3-usecount` |
| quic-go | network/path-length-fix | `0e7c69da` | 已融合进 `ca9992bc` | `…/quic-go/network-path-length-fix` |
| wireguard-go | awg-base | `ca3bc60c` | 被 pin `a765d957` 取代 | `…/wireguard-go/awg-base` |
| wireguard-go | awg-graft | `52a27657` | 被 pin `a765d957` 取代 | `…/wireguard-go/awg-graft` |

**注意**：Apple / satelite / wireguard 的删除候选仍包含**未纳入或明确延期**的工作，按“永远不得自动清理”条款，它们只归档、不删除。

## 3. 保留清单（不清理）

- `testing`、`main`、`dev`、`sagernet`、`gh-pages`、`mirror/*`、正式 release 分支与 tags。
- `integration/0.1.45-final`（交付分支）、`candidate/w07-2026-11-10`（基线）。
- Apple 两条功能线、satelite `next/subscription-refresh`（未合并）。
- wireguard `awg-base`/`awg-graft`（延期，不是已合并）。
- 任何仍有 worktree 的分支。

## 4. 阻塞清理的原因

1. **RC 未推送**：清理前置条件要求“最终集成成果已安全推送且 SHA 已核对”。本阶段未推送 `integration/0.1.45-final`（推送是可选动作，且主仓库 `origin` 配置异常，见 §6）。
2. **archive tags 未推送**：前置条件要求“先推送并验证 archive tag，再删除远端 topic branch”。本轮只创建了本地 tag（20 个）。
3. **远端删除不可逆**：在上述两个条件满足前执行删除，违反阶段 06 的安全条款；按“如果任一证明缺失，保留分支并写入 CLEANUP_DEFERRED”处理。

## 5. 完成远端清理的精确步骤（留给有推送权限的执行者）

```sh
# 0) 主仓库 origin 目前指向 desktop 仓库，必须用显式 URL
git -C C:/Deepseek/singbox-consolidation push \
  https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git \
  integration/0.1.45-final

# 1) 推送 archive tags（主仓库）
git -C C:/Deepseek/singbox-consolidation push \
  https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git \
  'refs/tags/archive/0.1.45/jiejiebox/*:refs/tags/archive/0.1.45/jiejiebox/*'

# 2) 逐个子仓库推送 tag（apple/desktop/dashboard/satelite-one/quic-go/wireguard-go）

# 3) 核对远端 SHA 与 tag 后再删除 topic 分支
git push https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git --delete <branch>
```

**不要**删除 `testing`/`main`/`integration/0.1.45-final`/`candidate/*`，也不要删除 Apple/satelite/wireguard 的未合并分支。

## 6. 本轮发现的额外风险

- `C:\Deepseek\singbox-consolidation` 的 `origin` remote 指向 `sing-box-for-desktop`（不是 JiejieBox），且主仓库分支的远端实际在 `Piggy-Cat-bit-shadow/JiejieBox`。任何 `git push origin` 都会推到错误的仓库。已在 `FINAL_TEST_MATRIX`/本报告记录；清理前必须用显式 URL。
- 各客户端仓库的 archive tag 只存在于本地；删除远端前必须推送。

## 7. 结论

**本轮没有删除任何分支**；创建了可恢复的本地 archive tags，并给出逐分支的吸收证明与远端清理步骤。远端清理因“RC 与 archive tag 未推送”按纪律延期，记入 `CLEANUP_DEFERRED`（本节即记录）。
