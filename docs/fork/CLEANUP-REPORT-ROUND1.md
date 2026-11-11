# Round 1 分支清理复核

## 主仓（Piggy-Cat-bit-shadow/JiejieBox）

`git ls-remote --heads jiejiebox`（2026-11-11 11:35 +08:00）：

| 分支 | SHA | 处理 |
|---|---|---|
| `testing` | `4ec0decd9f2` | 默认分支，保留 |
| `integration/0.1.45-final` | `4ec0decd9f2` | 产品施工线，保留 |
| `feature/proxy-group-hidden` | `9ac536420993` | **本轮审计期间出现的新分支**（2026-11-11 11:31 推送，5 个 hidden-group 提交，基于当前 testing）。归属其他工作，保留，不删除、不覆盖 |

上一轮删除的 5 个分支与 archive tag：

| 原分支 | archive tag（peeled commit） | 验证 |
|---|---|---|
| `backup/02-ecfffe7d0` | `archive/0.1.45/main/backup-02` → `ecfffe7d069` | 与删除前 head 一致 |
| `docs/03-overnight-gate-0.1.45` | `archive/0.1.45/main/docs-03-overnight-gate` → `38f219f787f` | 一致 |
| `integration/next-round` | `archive/0.1.45/main/integration-next-round` → `eef78712bea` | 一致 |
| `next/client-consumption-candidate` | `archive/0.1.45/main/client-consumption-candidate` → `09fa790dfb3` | 一致 |
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

- 主仓远端已从 5 个临时分支收敛到 2 个正式分支 + 1 个审计期间新出现、仍在使用的 feature 分支。
- 归档 tag 均经 peeled commit 校验，指向删除前真实 head。
- 没有可安全删除但被遗漏的分支；Apple/WG/Cronet 的例外分支都有明确理由。
- 本报告不包含任何 `git push --force`、`git reset --hard`、`git clean` 操作。
