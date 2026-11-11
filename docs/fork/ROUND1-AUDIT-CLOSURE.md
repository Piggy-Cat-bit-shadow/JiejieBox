# Round 1 审计闭环（06）

执行时间：2026-11-11
对象：第一轮上游合并完成后的验收与收口；**不启动第二轮追新**，不重做 01–05 施工。

## 0. 现场

- 主仓 `Piggy-Cat-bit-shadow/JiejieBox`，`testing` = `integration/0.1.45-final` = `2528511d90ab876c6c853f23d6ae1deee083027e`。
- `HEAD...upstream/testing` = **3737 / 0**；`upstream/testing` = `a13dbe1f785f`（本轮观测窗口内未再前进）。
- merge commit `303fbb47dfd`：parents `48103716845e`（合并前 fork）+ `a13dbe1f785f`（上游）。
- 审计期间发现新远端分支 `feature/proxy-group-hidden`（11:31 推送，5 个 hidden-group 提交）。它不是本轮产物，基于当前 `testing`，保留给对应工作，不删除/不覆盖。

## 1. 问题状态表

| ID | 优先级 | 旧报告断言 | 复验结果 | 状态 |
|---|---|---|---|---|
| P0-01 merge-base / HEAD / CI 口径 | P0 | `8eb4395` 写作“最终 merge-base” | 真实 Git 图：pre-merge base = `8eb4395`；合并后 final merge-base = `a13dbe1`；`303fbb4` 第二父 = `a13dbe1`。报告/JSON 已拆分字段 | **CLOSED_VERIFIED** |
| P0-02 108 冲突逐文件证据 | P0 | 有 108 路径列表，无完整裁决矩阵 | 新增 108/108 CSV + MD；路径集与原始列表完全一致、无重复/遗漏；分类总和 108；38 个高风险 VERIFIED、70 个 RECONSTRUCTED | **CLOSED_VERIFIED** |
| P0-03 Dashboard 95 上游提交映射 | P0 | 97/97、98/98 PASS，但无 95 提交功能映射 | 95/95 逐项映射；27 完全一致、61 fork 编辑、7 重命名/被取代；22 个最终树差异文件逐一分类；无上游独有文件缺失 | **CLOSED_VERIFIED** |
| P0-04 sing-tun 完整 race 失败 | P0 | “基本仅 unreachable 失败” | A/B/C/D 同环境：plain 两版都仅 unreachable；race 两版都出现 SACK/SequenceWrap/MemoryDial 同类失败；非本轮回归，完整 race 不能记 PASS | **BASELINE_CONFIRMED** |
| P1-01 Windows 三个包 | P1 | 3 个已知失败 | 合并前 4810371 与最终树失败测试名、错误完全一致；windivert SCm access denied，tls handshake EOF | **BASELINE_CONFIRMED** |
| P1-02 httpclient 负载 flake | P1 | 全量偶发 1 次，单独 20+5 通过 | `common/httpclient` 在合并前后代码完全相同；失败断言 `broken==1`；单独 20 plain + 5 race 全过；全量并发下可复现一次 | **BASELINE_CONFIRMED（负载敏感）** |
| P1-03 Apple pin / Xcode / ABI | P1 | pin `7fce711`，main `97b35d1`，无 Xcode | pin 是 main 之后 9 个产品提交；`fix/task10`/`integration-ne-fusion`/`next` 又在 pin 之后 16/22/6 个 NE 修复；source-lock/gitlink 一致；无 macOS runner，无法构建/XCTest | **ENVIRONMENT_BLOCKED** |
| P1-04 Cronet 平行 generation | P1 | `go` behind 2，称旧源 generation | 确认 fork `7ce9c49` 从 `4c96192` 生成，`4c96192` 包含上游 `73349ff`；上游 `095cb9e` 从更早 `73349ff` 生成；合并会覆盖 29 个 native | **EXCEPTION_JUSTIFIED** |
| P1-05 WireGuard / AWG 分支 | P1 | 只验证了 awg3-inject ancestry 与 device 测试 | `awg3-inject` 0 behind `origin/main`，22 ahead；`awg-base` 2 个独有、`awg-graft` 3 个独有（含 AWG graft），均为独立实验；内核只消费 awg3-inject | **DEFERRED_WITH_REASON** |
| P1-06 分支清理 | P1 | 删除 5 分支、4 tag，保留 2 分支 | 4 个 archive tag peeled commit 与删除前 head 完全一致；主仓远端现有 2 个正式分支 + 审计期间新出现的 `feature/proxy-group-hidden`（其他工作，保留）；其他仓库分支逐一列出 | **VERIFIED（新 feature 分支 DEFERRED）** |
| FINAL source-lock / modules / CI | — | — | source-lock verify PASS；root/test `go mod tidy -diff` 空；`go mod verify` PASS；Verify/Android 在 `b473c21` success，Mobile 在 `08b52a3` success | **PASS** |
| FINAL Round 1 audit | — | — | 上述闭环，保留 sing-tun race、Apple、Windows/httpclient、Dashboard lint 等限制 | **VERIFIED_WITH_LIMITATIONS** |

## 2. 本轮修正

1. **merge-base 语义修正**
   - `05_MAIN_KERNEL_UPSTREAM_ZERO_BEHIND_ACCEPTANCE.md` 拆成 `pre-merge merge-base` 与 `final merge-base at observation`。
   - `docs/fork/UPSTREAM-SYNC-2026-11-11-AUDIT.json`、`release/dependency-final-shas.json` 增加 `pre_merge_merge_base` / `upstream_merged_sha` / `final_merge_base_at_observation`。
   - `docs/fork/UPSTREAM-SYNC-2026-11-11-AUDIT.md` 加口径说明：审计时 `8eb4395`，合并后 `a13dbe1`。
   - 旧 ZIP 是用户提供的**历史快照**，不做假称已修改；仓库内新文档为修正版。
2. **新增 108 冲突矩阵**
   - `docs/fork/ROUND1-MERGE-CONFLICT-RESOLUTION.csv`（108 行）
   - `docs/fork/ROUND1-MERGE-CONFLICT-RESOLUTION.md`
   - 原始路径集：`docs/fork/round1-main-merge-conflicts.txt`
3. **新增 Dashboard 映射**
   - `docs/fork/ROUND1-DASHBOARD-COMMIT-MAP.csv`（95 行）
   - `docs/fork/ROUND1-DASHBOARD-UPSTREAM-MAPPING.md`
4. **新增失败基线矩阵**：`docs/fork/ROUND1-FAILURE-BASELINE-MATRIX.md`
5. **新增最终矩阵**：`docs/fork/ROUND1-FINAL-GIT-CI-MATRIX.md`
6. **新增清理复核**：`docs/fork/CLEANUP-REPORT-ROUND1.md`
7. **新增一致性门禁**：`scripts/ci/check-round1-audit.py`
   - 校验 108 路径/分类/merge 父提交/pre-merge base/final base/HEAD/behind/CI 文件边界/gitlink/source-lock；运行结果 **PASS**。

## 3. 五个问题的回答

### 3.1 第一轮合并是否真的没有丢失上游或 Fork 的高价值行为？

- 主仓：108 冲突全部有裁决；38 个高风险项有测试/源码证据（`go test -race ./route` 464s、dialer/listener/httpclient/masque/fakeip/http 等）；其余 70 个按最终树等于父提交做 `RECONSTRUCTED`，不冒充逐 hunk 实测。
- 依赖：sing/sing-tun/sing-quic/quic-go/uTLS 各自的修复都有针对性测试和旧代码负对照；WireGuard 只确认 ancestry 与 device 测试，AWG 独立提交未逐条语义审计（DEFERRED）。
- Dashboard：95/95 映射；22 个差异文件全部解释；没有上游独有文件缺失；测试 97/97 与 98/98。
- **边界**：不能宣称“所有行为都经过测试”；`RECONSTRUCTED` 和 `BASELINE_CONFIRMED` 的行就是边界。

### 3.2 108 冲突、Dashboard 95 提交怎样核对？有没有未闭环？

- 108：路径集完全一致；用 merge 双父树比较分类（81 FORK / 20 SEMANTIC_FUSION / 3 UPSTREAM / 2 OBSOLETE / 1 GENERATED / 1 SAME），再对高风险项补测试/源码证据。**0 个路径遗漏**；70 个为事后结果审查。
- Dashboard：95 个 commit 逐项列在 CSV，主题分组 + 22 文件差异表；**0 个未归因**。
- 未闭环：sing-tun race 的根因性能问题（已确认非本轮回归，但未优化）；Apple 无 Xcode；Dashboard CSS lint 4 个既有错误。

### 3.3 sing-tun Race、Windows、httpclient 是回归还是旧基线？

- sing-tun：A/B/C/D 同环境对照，plain 两版都仅 unreachable；race 两版都出现 SACK/SequenceWrap/MemoryDial 同类失败；**BASELINE_CONFIRMED，不是本轮回归**。完整 race 仍 **NOT PASS**，没有用调 timeout 伪装修复。
- Windows：合并前与最终失败测试名/错误完全一致；windivert 需要管理员/驱动，tls 是握手 EOF；**BASELINE_CONFIRMED**。
- httpclient：合并前后文件字节相同；失败是负载敏感时序；**BASELINE_CONFIRMED（负载敏感）**，不把单独 5/5 当作全量通过。

### 3.4 Apple、Cronet、WireGuard、pin、gitlink、CI 的验证边界？

- Apple：gitlink/source-lock 一致；pin 是产品线 `7fce711`，main `97b35d1` 是其祖先；三个分支还有 pin 之后的 NE 修复，但无 Xcode，**ENVIRONMENT_BLOCKED**，不更新 pin。
- Cronet：main 0 behind；`go` 1.9? 不，`go` 是生成线，behind 2 是平行旧源 generation，**EXCEPTION_JUSTIFIED**；29 个 native 与 root/all 同代。
- WireGuard：消费线 0 behind；AWG 分支有独立提交，**DEFERRED_WITH_REASON**，不强行合并/删除。
- gitlink/source-lock：三者一致，`release-freeze verify` PASS。
- CI：Verify/Android 在最终代码 `b473c21` success；Mobile 在 `08b52a3` success；之后仅文档/审计和 ABI 脚本变化，Verify 覆盖脚本。**不把 old run 说成新 HEAD 的完整验证**。

### 3.5 分支保留/清理、零 behind 的时间点与正确上游？

- 主仓保留 `testing`、`integration/0.1.45-final`，删除 5 个临时分支并验证 4 个 archive tag；审计期间新出现的 `feature/proxy-group-hidden` 保留（其他工作）。
- 其他仓库的分支逐一列出；Apple/WG/Cronet 的例外保留。
- 零 behind 的时点：2026-11-11 11:34 +08:00；对 `SagerNet/sing-box:testing` @ `a13dbe1f785f`，main `testing` @ `4ec0dec`，`behind=0`；依赖按各自正确 upstream 分支，API `ahead_by=0`（方向：`upstream...fork`）。

## 4. 证据文件

- `docs/fork/ROUND1-MERGE-CONFLICT-RESOLUTION.csv` / `.md`
- `docs/fork/ROUND1-DASHBOARD-COMMIT-MAP.csv` / `ROUND1-DASHBOARD-UPSTREAM-MAPPING.md`
- `docs/fork/ROUND1-FAILURE-BASELINE-MATRIX.md`
- `docs/fork/ROUND1-FINAL-GIT-CI-MATRIX.md`
- `docs/fork/CLEANUP-REPORT-ROUND1.md`
- `docs/fork/round1-main-merge-conflicts.txt`
- `audit-evidence/upstream/round1-*`（race A/B/C/D、Windows 基线、go vet、GitHub compare）
- `scripts/ci/check-round1-audit.py`（PASS）

## 5. 最终状态

```text
P0-01 merge-base / SHA consistency       = CLOSED_VERIFIED
P0-02 108 conflict resolution coverage   = CLOSED_VERIFIED
P0-03 Dashboard 95 upstream mapping      = CLOSED_VERIFIED
P0-04 sing-tun full race failure analysis = BASELINE_CONFIRMED
P1-01 Windows 3 baseline failures        = BASELINE_CONFIRMED
P1-02 httpclient load flake              = BASELINE_CONFIRMED
P1-03 Apple pin & Xcode / ABI            = ENVIRONMENT_BLOCKED
P1-04 Cronet parallel generation         = EXCEPTION_JUSTIFIED
P1-05 WireGuard/AWG branch status        = DEFERRED_WITH_REASON
P1-06 branch cleanup & archive tags      = VERIFIED (new active feature deferred)
FINAL Source-lock / modules / CI         = PASS
FINAL Round1 audit status                = VERIFIED_WITH_LIMITATIONS
```

**没有伪造 VERIFIED**：sing-tun 完整 race 仍是 NOT PASS；Apple 仍是 ENVIRONMENT_BLOCKED；Windows/httpclient 仍是 BASELINE_CONFIRMED；70 个冲突项是 RECONSTRUCTED 而非逐 hunk 实测。
