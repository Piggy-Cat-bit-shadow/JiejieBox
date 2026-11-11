# Round 1 最终闭环（07）

**状态：VERIFIED_WITH_LIMITATIONS**

本文件是本轮唯一综合结论；机器可读当前状态以 `ROUND1-FINAL-FACTS.json` 为准。产品源码在本轮**没有**变更；本轮改动是审计事实、清理更正、门禁与报告。

---

## 1. 真值与最终 SHA

| 项 | 值 |
|---|---|
| 主仓 | `Piggy-Cat-bit-shadow/JiejieBox` |
| canonical | `testing` = `integration/0.1.45-final` |
| 最终文档 HEAD | `40dfed27474eaf8d1219fc3bb13e17015c06e5fe` |
| 被 CI 覆盖的代码树 | `b473c21f4803c0196e3eb920d530602d5f92867f` |
| 审计/CI 交叉验证 commit | `2528511d90ab876c6c853f23d6ae1deee083027e` |
| 上游 | `SagerNet/sing-box:testing` @ `a13dbe1f785f66c86acbf3985c33fdafea0cbfd6` |
| ahead / behind | **3738 / 0** |
| pre-merge merge-base | `8eb4395a391b775ad240433a7009f39357c1eba0` |
| final merge-base | `a13dbe1f785f66c86acbf3985c33fdafea0cbfd6` |
| merge commit | `303fbb47dfdca4520dd201fea8bd48b3db1f1010` |
| source-lock | verify PASS |

**标签规则**：`8eb4395` 只表示合并前共同祖先；合并后 `merge-base HEAD upstream/testing = a13dbe1`。旧报告把前者写成“最终 merge-base”是标签错误，已在 `05`、审计 JSON、dependency JSON 中拆开；历史快照不重写。

### CI（真实 run）

| workflow | run | 结果 | head |
|---|---|---|---|
| Verify | 42955409204 | success | `40dfed27474` |
| Android Core ARM64 | 42955409234 | success | `40dfed27474` |
| Mobile contract | 42942954909 | success | `08b52a3`（其后仅文档/CI 脚本） |
| 较早代码树 | 42945339834 / 42945339735 | success | `b473c21` |

`40df` 的 Verify/Android 已实际重跑，不是拿旧 SHA 冒充；本轮后续文档/门禁提交若再产生，属于 docs-only，不再追写自身 SHA。

---

## 2. 问题闭环

| ID | 状态 | 结论 |
|---|---|---|
| P0-01 merge-base/SHA 一致性 | **CLOSED_VERIFIED** | pre/final 两个 base 已分离；旧 3736 是历史快照，当前 3738；CI/HEAD 标签有文件级证据 |
| P0-02 108 冲突覆盖 | **CLOSED_VERIFIED** | 108/108；81 FORK / 20 FUSION / 3 UPSTREAM / 2 OBSOLETE / 1 GENERATED / 1 SAME；38 项行为/源码证据，70 项如实标 RECONSTRUCTED |
| P0-03 Dashboard 95 提交 | **CLOSED_VERIFIED** | 95/95；27 exact / 61 fork-edited / 7 renamed-or-superseded；22 个最终树差异文件解释；97/97、98/98 |
| P0-04 sing-tun 完整 race | **BASELINE_CONFIRMED（不是 PASS）** | A/B/C/D 同环境：plain 两版都仅 unreachable；race 两版都出现 SACK/SequenceWrap/MemoryDial；完整 race 仍 NOT PASS |
| P1-01 Windows 三包 | **BASELINE_CONFIRMED** | 合并前与最终失败测试名/错误一致；WinDivert 权限、TLS 握手 EOF |
| P1-02 httpclient 偶发 | **BASELINE_CONFIRMED（负载敏感）** | 文件前后未变；全量 1 次，单独 20 plain + 5 race 全过 |
| P1-03 Apple | **ENVIRONMENT_BLOCKED + RELEASE_SOURCE_AUTHORITY_RISK** | 无 Xcode；pin `7fce711` 比 `main` `97b35d1` 多 9 个提交；release 默认按 `main` 解析，source-lock 记 gitlink，二者不是同一 SHA |
| P1-04 Cronet go generation | **EXCEPTION_JUSTIFIED** | 平行旧源 generation；合并会覆盖 29 个 native；解除条件已写 |
| P1-05 WireGuard/AWG | **DEFERRED_WITH_REASON** | 消费线 awg3 0 behind；awg-base/awg-graft 是独立实验 |
| P1-06 分支清理 | **VERIFIED（含一处更正）** | backup/02 是活跃并行线，已从 archive tag 恢复；其余 4 个删除项有 tag；活跃 feature 未触碰 |
| FINAL source-lock/modules | **PASS** | root/test `go mod tidy -diff` 空；`go mod verify` PASS；gitlink 与 lock 一致 |
| FINAL Round1 | **VERIFIED_WITH_LIMITATIONS** | 见 §5 |

---

## 3. 本轮实际修改

1. 新增 `ROUND1-FINAL-FACTS.json`：唯一结构化当前事实源（Git、依赖、客户端、CI、测试、例外、证据路径）。
2. 新增本文件、`ROUND1-FINAL-SELF-CHECK.md`、`ROUND1-FINAL-REMOTE-AND-CI-EVIDENCE.md`。
3. 修正 `CLEANUP-REPORT-ROUND1.md`：
   - **恢复 `backup/02-ecfffe7d0`**（add-only push，SHA `ecfffe7d0691`）。01 审计明确它是活跃并行线（44 个独有提交），上一轮按“旧备份”删除是分类错误；archive tag 保证了对象未丢，但分支代表活跃工作，已恢复。
4. 加强 `scripts/ci/check-round1-audit.py`：
   - 增加 allowed resolution/review status 校验；
   - 新增 `--self-test`，对重复路径、缺路径、非法分类、非文档 CI 变更、错误 gitlink、错误 final merge-base **6 类注入错误**要求全部拒绝；正常与反例均 PASS。
5. 更新 `05_MAIN_KERNEL...`、`ROUND1-FINAL-GIT-CI-MATRIX`、审计 JSON 等审计口径；没有改产品源码、依赖 pin、ABI 或 source-lock。

---

## 4. 自主发现的新问题

### 4.1 backup/02 被误删（已修复）

`01_AUDIT_AND_FREEZE.md` 记录：`backup/02-ecfffe7d0` tip 为 2026-11-10 21:42，44 个提交，与所有 topic **0 patch 等价**，是“仍在进行的并行工作线”，且是第 05 阶段 libbox/daemon 流控候选来源。上一轮清理把它当“旧备份”归档删除。  
本轮已恢复远端分支；它仍相对 HEAD 有 44 个独有提交，不能凭 tag 当作已吸收。后续应按 libbox/daemon 主题逐项审计，而不是再次删除。

### 4.2 Apple release source authority 与 source-lock 不是同一 SHA

- `release/apple-client-refs.env`：`APPLE_CLIENT_BRANCH=main`；gitlink 明确不再作为 build authority。
- `clients/apple` pin = `7fce711`，`origin/main` = `97b35d1`；pin 比 main 多 9 个产品提交。
- `client-apple.yml` 默认解析 `main` 的 tip，除非 dispatch 传 `apple_client_sha` 或设置 `APPLE_CLIENT_REF/SHA`；`source-lock.json` 记录的是 gitlink。
- 影响：默认 Apple release 的 source provenance 可能与 source-lock 的 pin 不一致（可能发布较旧的 main，而非含 9 个提交的 pin）。这不是已证实的构建失败，但属于发布来源风险。
- 不能在本环境修：无 macOS/Xcode，且合并 pin→main 或改 release authority 都是产品决策。解除条件：明确 release 应使用哪个 SHA，并传入 `APPLE_CLIENT_REF`/`apple_client_sha`，或在 macOS runner 上验证后把 pin 快进到 main。

### 4.3 其他核对

- **日志 anomalie**：`round1-race/*` 的 13 处 timeout 均已归因；`main-merge-full-windows.txt` 的 FAIL 只有三个已知包；未发现未记录的 panic/data race 被写成 PASS。
- **pinned vs formal branch**：quic-go 消费线 `158445cd` 比 `sagernet` 多 fork 修复；Apple pin 比 main 多 9 个；Desktop 产品 `e8b48be` 含 dashboard bump；Dashboard 消费线 `9bde90e`；Cronet root/all 同代。均为消费线设计或已记录例外，不是“拿错分支”。
- **删除分支引用**：除 docs/审计记录外，没有构建脚本/source-lock/子模块引用被删分支；backup 已恢复，client-candidate 仅保留 tag。
- **CI SHA**：40df 的 Verify/Android 已实际成功；b473 是代码树，2528511 是审计 commit；三者的文件差异已由门禁检查，不再混用。

---

## 5. 真实限制（不影响本轮结论，但影响发布/平台）

1. **Apple**：`ENVIRONMENT_BLOCKED`；无 Xcode/macOS，pin、main、三个 NE 分支未在设备上验证；release authority 风险见 §4.2。
2. **sing-tun 完整 `-race`**：`BASELINE_CONFIRMED`、**NOT PASS**；SACK/SequenceWrap/MemoryDial 在合并前已同败，未做性能根因优化。
3. **Windows**：`common/tls`、`common/tlsspoof`、`common/windivert` 三包失败；环境/驱动基线，不是本轮回归。
4. **httpclient**：负载敏感偶发；单独 20 plain + 5 race 通过，未修根因。
5. **Dashboard `lint:css`**：4 个 Fork-only 设计值错误；未为绿灯改设计值。
6. **Cronet `go`**：平行 generation 例外，未合并。
7. **go vet**：三处既有 `unsafe.Pointer` 警告。

---

## 6. 为什么本轮可以结束

- 事实源唯一：`ROUND1-FINAL-FACTS.json` + 本文件；旧报告中的 3736/3737/2528511/4ec0 均标为历史阶段，不再竞争“当前值”。
- 108 与 95 都有机器可读矩阵和人工可读解释；38/70 的证据等级没有伪装。
- 门禁正常 PASS，且 6 类注入反例全部 FAIL；不是只能打印 PASS 的装饰脚本。
- 远端分支、archive tag、恢复的 backup、活跃 feature 均重新采集。
- CI 在最终文档 HEAD 40df 上实际成功；代码树 b473 的三套门禁有独立记录。
- 未解决的项都有明确原因、影响、解除条件；不需要再来一轮泛泛“收尾审计”。

**下一轮只需处理产品决策/环境项（Apple release SHA、backup/02 的 44 个提交评估、sing-tun race 性能根因），不需要重新审计第一轮合并本身。**
