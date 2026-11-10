# CLEANUP REPORT — 0.1.45 分支清理

## 1. 实际动作

| 动作 | 结果 |
|---|---|
| RC 推送 | `integration/0.1.45-final` → `b810d0c2603f11eb2127adf816eb9e2b080b4094`（remote SHA 已核对） |
| archive tags 推送 | 20 个全部推送并核对：主仓库 13、Apple 2、Desktop 1、Dashboard 1、satelite 1、quic-go 2、wireguard-go 2 |
| 远端 topic 分支删除 | **15 个**（见 §2），每个删除前都核对了远端 SHA = archive tag 指向的 SHA |
| 本地 ref 删除 | 0（候选分支仍被 worktree 或 archive 证明使用；删除本地 ref 无收益） |
| `testing`/`main`/`dev`/`gh-pages`/release tags | 未触碰 |

## 2. 已删除的远端分支

主仓库（`Piggy-Cat-bit-shadow/JiejieBox`，11 个，删除时 SHA 与 tag 一致）：

| 分支 | 删除前 SHA | archive tag |
|---|---|---|
| fix/task09-repair | 31a3e9acea1b | archive/0.1.45/jiejiebox/fix-task09-repair |
| task08/h3-h2-fallback-2026-11-10 | 684ebf516767 | …/task08-h3-h2-fallback-2026-11-10 |
| fix/task10-apple-ne-runtime-final | a3340d1a759d | …/fix-task10-apple-ne-runtime-final |
| next/network | b9bc2bc25981 | …/next-network |
| closeout/resources | aafc4c11f022 | …/closeout-resources |
| next/resources | b00dfe1f0e08 | …/next-resources |
| integration/next-round | 94c86b9d1f1f | …/integration-next-round |
| docs/task09-knowledge-0.1.45 | 595d65154d84 | …/docs-task09-knowledge-0.1.45 |
| task08/knowledge-0.1.45 | 99cc3b514327 | …/task08-knowledge-0.1.45 |
| kb/0.1.45-02 | 8405d1bbb986 | …/kb-0.1.45-02 |
| pending/device-axis-reload-window-tests | 0477e327e5da | …/pending-device-axis-reload-window-tests |

子仓库（4 个）：

| 仓库 | 分支 | 删除前 SHA | archive tag |
|---|---|---|---|
| Desktop | next/client-consumption | 28dc4a973bcd | archive/0.1.45/desktop/next-client-consumption |
| Dashboard | next/client-consumption | 62f158255ce9 | archive/0.1.45/dashboard/next-client-consumption |
| quic-go | fix/task08-h3-usecount | 0b8709ebb15e | archive/0.1.45/quic-go/fix-task08-h3-usecount |
| quic-go | network/path-length-fix | 0e7c69dacb54 | archive/0.1.45/quic-go/network-path-length-fix |

删除前的吸收证明见各阶段报告：文件逐字节一致（task09 40/40、h3 35/40+5 pin、task10 21/23+2 更新、next/network 39/43+4 更新）或 cherry-pick/测试映射（closeout 字节相同、resources 阶段 03、docs 归档、pending 4/4 PASS）。删除后远端 tag 仍可恢复任何分支：

```sh
git push <url> refs/tags/archive/0.1.45/<repo>/<branch>:refs/heads/<branch>
```

## 3. 保留的分支及原因

| 分支 | 原因 |
|---|---|
| `testing`、`main`、`dev`、`sagernet`、`gh-pages`、`mirror/*` | 永远不自动清理 |
| `integration/0.1.45-final` | 交付分支（已推送） |
| `candidate/w07-2026-11-10` | 基线参照 |
| `backup/02-ecfffe7d0` | 备份；含 route diagnose/process-cache 等未纳入内容，保留到 RC 稳定后人工决定 |
| `next/client-consumption-candidate` | 客户端候选，独有工作未证明已被吸收，保留 |
| Apple `next/client-consumption`、`fix/task10-apple-ne-runtime-final` | 未合并（无 Xcode），只归档；语义融合分支 `integration/0.1.45-ne-fusion` @ `9330d90` 已推送（syntax/typecheck/探针通过，Xcode 构建未执行） |
| satelite-one `next/subscription-refresh` | 已在其仓库 fast-forward 到 `integration/0.1.45`（`9cdf782`），远端分支保留为产品分支；tag 已推送 |
| wireguard-go `awg-base`、`awg-graft` | 延期；被 pin `a765d957` 取代，保留 refs |
| quic-go `integration/0.1.45` | 依赖 pin 所在分支，保留 |
| Desktop/Dashboard `main` | 产品主线 |

## 3b. 收尾补充（2026-11-11 05:55）

- 主仓库 `origin` 已从误指向的 `sing-box-for-desktop` 修正为 `https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git`；`jiejiebox` remote 保留为显式别名。修正前所有推送均使用显式 URL，未受影响。
- satelite-one `integration/0.1.45`（`9cdf782`）已推送到其 origin 并核对，产品交付分支不再只存在于本地。
- `next/client-consumption-candidate` 审计结论：6 个提交只是客户端 gitlink 提案（Apple `7e2f5c4`、Desktop `28dc4a9`）与 `check-libbox-abi.sh` 的 `VERIFIED_APPLE_SHA` 更新；其中 Apple 部分依赖尚未完成的 Xcode 验证，Desktop 部分已由阶段 05 以 `28dc4a9` 落地。为避免把未验证的 Apple pin 写入 ABI 门禁，该分支不吸收、保留归档。
## 4. 恢复入口

- 主仓库 tag 前缀：`archive/0.1.45/jiejiebox/`
- 子仓库 tag 前缀：`archive/0.1.45/<repo>/`
- 推送后已逐项用 `git ls-remote --tags` 核对；删除后用 `git ls-remote --heads` 确认分支已消失、tag 仍在。

## 5. 结论

清理满足阶段 06 的全部前置条件：RC 已推送并核对、archive tags 已推送并核对、分支 SHA 与 tag 一一对应、独有工作有文件级或测试级证明。共删除 15 个远端 topic 分支，保留 2 个需人工决定的候选（backup、client-consumption-candidate）与全部未合并/延期分支。
