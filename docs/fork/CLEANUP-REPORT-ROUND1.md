# Round 1 分支清理复核

## 主仓（Piggy-Cat-bit-shadow/JiejieBox）

`git ls-remote --heads jiejiebox`（2026-11-11 12:30 +08:00）：

| 分支 | SHA | 处理 |
|---|---|---|
| `testing` | `40dfed27474eaf8d1219fc3bb13e17015c06e5fe` | 默认分支，保留 |
| `integration/0.1.45-final` | `40dfed27474eaf8d1219fc3bb13e17015c06e5fe` | 产品施工线，保留 |
| `feature/proxy-group-hidden` | `75f608074090` | 其他工作的活跃 feature，基于当前 testing；保留，不删除、不覆盖 |
| `backup/02-ecfffe7d0` | `ecfffe7d0691` | **已恢复**：见下方更正 |

### 更正：backup/02 不是旧备份，已恢复

`01_AUDIT_AND_FREEZE.md` 明确记录 `backup/02-ecfffe7d0` 是 2026-11-10 仍在进行的并行工作线（44 个独有提交，与 topic 分支 0 个 patch 等价），不是旧基线备份。上一轮把它当“旧备份”归档并删除，是**分类错误**。archive tag 完整保留了对象，但分支代表活跃工作，不能只靠 tag 代替。

因此本轮已从 `archive/0.1.45/main/backup-02^{commit}` 恢复远端分支：

```text
ecfffe7d06915d5ad5cfef771b39ac73a72696f2  refs/heads/backup/02-ecfffe7d0
```

恢复是 add-only，没有改写历史、没有覆盖当前 testing。该分支相对 HEAD 仍有 44 个独有提交，需后续按 libbox/daemon 主题逐项评估，不并入本次收尾。

### 其余已删除分支与 archive tag

| 原分支 | archive tag（peeled commit） | 验证/处理 |
|---|---|---|
| `docs/03-overnight-gate-0.1.45` | `archive/0.1.45/main/docs-03-overnight-gate` → `38f219f787f` | 与删除前 head 一致；纯 docs，保留 tag |
| `integration/next-round` | `archive/0.1.45/main/integration-next-round` → `eef78712bea` | 一致；历史集成分支，保留 tag |
| `next/client-consumption-candidate` | `archive/0.1.45/main/client-consumption-candidate` → `09fa790dfb3` | 一致；6 个 client pin 候选提交，当前客户端已更新，保留 tag 不恢复分支 |
| `candidate/w07-2026-11-10` | 已是 HEAD 祖先，无需 tag | 0 独有提交 |

没有使用通配符批量删除、force push 或删除默认/上游参考分支。

## 其他仓库远端分支（审计时）

| 仓库 | 远端分支 | 判断 |
|---|---|---|
| sing | `dev` | 唯一正式分支，保留 |
| sing-tun | `dev` | 唯一正式分支，保留 |
| sing-quic | `main` | 唯一正式分支，保留 |
| quic-go | `sagernet`, `integration/0.1.45` | `sagernet` 正式/上游线，`integration` 是内核消费线，均保留 |
| uTLS | `main`, `mirror/meta-v1.9.0` | `main` 产品线；`mirror` 是 1.9 上游镜像参考，保留 |
| cronet-go | `main`, `go` | `main` 源/产品线；`go` 生成线例外（见 FINAL-GIT-CI-MATRIX §4），保留不合并 |
| wireguard-go | `main`, `dev`, `awg-base`, `awg-graft`, `awg3-inject` | `main` 上游参考、`dev` 上游线；`awg3-inject` 是内核消费线；`awg-base`（2 个独有）与 `awg-graft`（3 个独有 AWG graft 提交）是独立实验，保留 |
| apple | `main`, `fix/task10-apple-ne-runtime-final`, `integration/0.1.45-ne-fusion`, `next/client-consumption` | `main` 0 behind；三个分支都包含 pin 之后的 16/22/6 个 Apple NE 修复，未合并到 pin，缺 Xcode 无法验证，保留 |
| android | `main` | 唯一正式分支，保留 |
| desktop | `main`, `integration/0.1.45` | `main` 0 behind；`integration` 是内核 pin 的产品线（dashboard bump），保留 |
| dashboard | `main`, `integration/0.1.45`, `gh-pages` | `main` 产品线；`integration` 是 desktop 消费线；`gh-pages` 部署分支，保留 |

## 未处理的 worktree/本地分支

- 主仓有多个既有施工 worktree（`_next/*`、`_p01_main` 等）。本轮只清理了 `_p01_main` 的子模块 checkout 并按 gitlink 对齐；其他 worktree 属于用户/其他工作，未删除、未 reset。
- 本地 `candidate-r6/r7/r8` 等分支仍未删除；它们有独立 worktree、可能是用户正在使用的候选，本轮不清理。

## 结论

- 主仓远端现有 `testing`、`integration/0.1.45-final` 两个正式分支，`feature/proxy-group-hidden` 一个其他工作的活跃分支，以及**恢复后的 `backup/02-ecfffe7d0`**。后者不是可清理的旧备份，而是 01 审计已识别的并行工作线。
- 其余 4 个删除项均有 archive tag；tag 经 peeled commit 校验，指向删除前真实 head。`next/client-consumption-candidate` 的 6 个 client pin 已被当前客户端更新取代，保留 tag 不恢复；`backup/02` 因仍有独立工作而恢复。
- 没有在未检查独有提交、tag、引用的情况下删除分支；Apple/WG/Cronet 的例外分支都有明确理由。
- 本报告不包含任何 `git push --force`、`git reset --hard`、`git clean` 操作；恢复 backup 使用 add-only push。
