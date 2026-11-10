# FINAL 依赖与 source-lock 报告（0.1.45）

## 1. 输入与产出

- 输入：阶段 02 的依赖 SHA、阶段 05 的客户端 SHA、当前树（`go.mod`、`test/go.mod`、gitlinks）。
- 工具：`scripts/ci/release-freeze.py`（resolve / check / snapshot / verify）。
- 产出：`release/source-lock.json`（由树生成，非手工填写）、`release/dependency-final-shas.json`。

## 2. 清单修正（解决阶段 01 审计问题）

| 问题 | 处理 |
|---|---|
| `wireguard-go` 在 go.mod/source-lock 中存在，但 `dependencies.json` 未声明 | 新增条目：`Piggy-Cat-bit-shadow/wireguard-go`，branch `awg3-inject`，policy `update`；pin `a765d957fd81` 与该分支 tip 相同 |
| quic-go 声明 branch `sagernet`，但集成 pin 实际在 `integration/0.1.45` | 声明改为 `integration/0.1.45`；resolve 结果 `ca9992bc795e` 与 pin **identical** |
| sing-tun 旧 pin `9c3dcb2` | 已为阶段 02 的 `57e9d17fa852`（origin/dev tip，identical） |
| cronet-go 29 个 native modules | 保持 controlled `362e63b14ce2`，未移动 |
| `dependencies.json` 复制 SHA | 没有；该文件只记录 repo/branch/policy |

## 3. resolve / check 结果

`check` 输出（`audit-evidence/stage06/check2.json`）：

| module | pinned | branch tip | relation |
|---|---|---|---|
| sagernet/sing | 1729220c | 1729220c | identical |
| sagernet/sing-tun | 57e9d17f | 57e9d17f | identical |
| sagernet/sing-quic | 1ef2597c | 1ef2597c | identical |
| sagernet/quic-go | ca9992bc | ca9992bc | identical |
| metacubex/utls | b63a2e9a | b63a2e9a | identical |
| sagernet/wireguard-go | a765d957 | a765d957 | identical |
| sagernet/cronet-go / all | 362e63b / 7ce9c49 | 4c96192 | ahead（发布顺序；不移动） |
| clients/apple | 7fce711 | 97b35d1 (main) | ahead（pin 在 next 线） |
| clients/android | c783710 | c783710 | identical |
| clients/desktop | 28dc4a9 | aa63764 (main) | ahead（集成提交） |
| desktop:dashboard | 62f1582 | fc1dbb4 (main) | ahead（集成提交） |

**结论**：`PASS: every target is comparable and none is diverged. no pin needs to move.`
没有执行 `apply`（没有需要移动的 pin）。

## 4. source-lock

- `snapshot` 从树生成：`release/source-lock.json`，`release_version: 0.1.45`，8 个 Go module、3 个 client、nested dashboard、29 个 controlled native。
- `verify --lock release/source-lock.json`：**PASS**（"every pin in the tree is the pin the lock records"）。
- 快照提交：`69e8dcb457a41695224a094411a32c80fd39e09d`（其父为 0.1.45 版本与清单提交 `86ca1544296`）。

## 5. 已知不一致与处理

1. **Apple pin 不在声明分支 main 上**：pin `7fce711` 位于 `next/client-consumption`（tip `7e2f5c4`，main `97b35d1` 是其祖先）。本阶段没有 Apple 构建环境，不移动 pin、不改声明分支；在 `FINAL_0.1.45_INTEGRATION_REPORT.md` §5 标为 BLOCKED。
2. **Desktop/Dashboard 集成提交在 main 之前**：check 报 ahead，不视为 diverged；pin 是实际构建测试过的 commit。若发布流程要求 pin 必须在 main，则需先把集成分支合入 main（本阶段不做）。
3. **Cronet root/all ahead of main**：符合该仓库“main → root publish → all regen”的发布顺序；29 个 native 保持 controlled。
4. **satelite-one 不是 gitlink**：未写入内核 source-lock；其自身分支未合并。

## 6. 复现命令

```sh
python scripts/ci/release-freeze.py resolve --out /tmp/resolve.json
python scripts/ci/release-freeze.py check   --resolve /tmp/resolve.json
python scripts/ci/release-freeze.py snapshot --resolve /tmp/resolve.json --out /tmp/source-lock.json
python scripts/ci/release-freeze.py verify  --lock release/source-lock.json
```

网络需求：`git ls-remote` 访问 GitHub（resolve）；verify 只读本地树与锁。
