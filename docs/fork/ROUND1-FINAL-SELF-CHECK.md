# Round 1 最终自查（07）

> 状态限定 `PASS` / `FAIL` / `BLOCKED` / `NOT_APPLICABLE`。本表不是装饰：每项给出证据路径或命令；自行发现项单列。

## A. 工作现场与 Git 保护

| 检查 | 状态 | 证据 |
|---|---|---|
| 前六份施工与 06 审计已结束，未与其他 Agent 写同一分支 | PASS | 主仓 `testing`/`integration` 由本会话维护；`feature/proxy-group-hidden` 是其他工作，只读观察，未 checkout/推送 |
| 改动都在正确仓库、canonical 分支 | PASS | 本轮改动只在主仓 `integration/0.1.45-final`，随后快进 `testing`；依赖仓库无改动 |
| 没有覆盖用户未提交/未推送工作 | PASS | `git status` 干净后才提交；未 reset/stash/clean；其他 worktree 未动 |
| HEAD/origin/testing/integration/upstream 完整记录 | PASS | `ROUND1-FINAL-FACTS.json` §main；当前 `40dfed2`，upstream `a13dbe1` |
| merge 两父、pre/final merge-base 标签准确 | PASS | `ROUND1-FINAL-FACTS.json`；`303fbb4` parents `4810371`+`a13dbe1`；pre `8eb4395`；final `a13dbe1` |
| ahead/behind 方向统一 | PASS | 门禁 `rev-list --left-right --count HEAD...upstream/testing` = `3738 0`；Compare 方向 `upstream...fork` → `ahead_by=0` |
| Compare base/head/time/URL 对得上远端 | PASS | `audit-evidence/upstream/round1-github-compare.txt`（2026-11-11T11:34:50+08:00） |
| 无 force push/reset/历史重写 | PASS | 仅普通 push；backup 恢复为 add-only push |

## B. 108 冲突与来源证据

| 检查 | 状态 | 证据 |
|---|---|---|
| 原始列表与 CSV 路径集合一致、无重复遗漏 | PASS | 门禁正常模式；`round1-main-merge-conflicts.txt` 108，CSV 108 |
| CSV 分类合计 == MD == 原始数 | PASS | 81+20+3+2+1+1=108；MD 同 |
| CSV/MD 同一路径状态一致 | PASS | MD 由 CSV 生成并按 CSV 复核 |
| 未把 RECONSTRUCTED 伪装成逐 hunk 验证 | PASS | 70 行 `RECONSTRUCTED`，仅 38 个高风险 `VERIFIED` |
| 高风险 VERIFIED 有可复核测试/源码证据 | PASS | 见 CSV `validation_or_source_evidence`；dialer/listener/httpclient/route/masque/fakeip/schema 等 |
| 门禁能检测 ≥6 类数据错误 | PASS | `--self-test` 6 类注入全部 rejected（见下） |

## C. Dashboard

| 检查 | 状态 | 证据 |
|---|---|---|
| 95 上游 SHA 唯一且映射可追溯 | PASS | `ROUND1-DASHBOARD-COMMIT-MAP.csv` 95 行；`git rev-list --count upstream/main`=95 |
| 27/61/7 统计在 CSV/MD 一致 | PASS | CSV 状态计数；MD 结论 |
| 文件级映射与行为级验证未偷换 | PASS | MD 明确“文件级存在/22 差异”；行为级仅引用 97/97、98/98，不把文件存在当行为通过 |
| 高风险功能有分组测试或明确缺口 | PASS | Taildrop/Tailscale/Terminal/Settings/Profiles/PWA/USB 分组；CSS lint 明示 baseline |
| main/消费线测试使用对应 SHA | PASS | main `b592c65` 97/97；integration `9bde90e` 98/98；日志 `round1-dashboard-*` |
| lint:css 修复或 baseline 验证 | BLOCKED（真实技术债） | `round1-dashboard-csslint.txt` 4 个 Fork-only `ProfileViews.module.css` 规则；未改设计值、未禁用规则 |

## D. Race、Windows、httpclient

| 检查 | 状态 | 证据 |
|---|---|---|
| SACK IPv4/IPv6 前后隔离重复测试 | PASS | `round1-race/old-race.txt` / `new-race.txt`，各 6 个 mode；A/B/C/D |
| SequenceWrap/unreachable 新旧差异分类 | PASS | `ROUND1-FAILURE-BASELINE-MATRIX.md` §1 |
| 完整 race 未过时不写 PASS | PASS | 明确 **NOT PASS / BASELINE_CONFIRMED** |
| Windows TLS/WinDivert 根因分清 | PASS | `round1-premerge-windows.txt`；TLS EOF、WinDivert SCM access denied |
| httpclient 偶发有可复核解释/限制 | PASS | 文件前后未变；1 次全量失败 + 20 plain/5 race 复跑；标 BASELINE_CONFIRMED |
| 未用 timeout/跳过/-vet=off 制造绿灯 | PASS | 未改测试或 tag；完整 race 保持 FAIL |

## E. 依赖、平台、CI

| 检查 | 状态 | 证据 |
|---|---|---|
| root/test/nested go.mod、replace、source-lock、gitlink 一致 | PASS | source-lock verify PASS；`go mod tidy -diff` 空；facts JSON clients |
| Cronet root/all/native 实际消费与例外准确 | PASS | facts §dependencies；`362e63b`/`7ce9c49`；29 native；`go` behind 2 例外 |
| Apple pin/可测试目标/Xcode/ABI 风险明确 | BLOCKED（环境 + 产品决策） | pin `7fce711` vs main `97b35d1`；无 Xcode；release branch main 解析，见 closure §4.2 |
| AWG 独有分支按用途分类、未误删 | PASS | awg3 消费；awg-base/awg-graft 保留；facts §cleanup |
| CI run URL/status/head 可复核 | PASS | Verify `42955409204`、Android `42955409234` @40df；Mobile `42942954909` @08b52a3 |
| CI 后文档提交与代码提交边界自动检查 | PASS | 门禁 `b473..HEAD` only docs/audit/script；最新代码树 `b473` |
| 最终代码/脚本修改已跑必要门禁 | PASS | 本轮无产品代码；门禁脚本 `py_compile` + normal + self-test |

## F. 远端与清理

| 检查 | 状态 | 证据 |
|---|---|---|
| 拟删分支检查独有提交/tag/引用/worktree | FAIL（已纠正） | backup/02 被误判为旧备份；01 审计记录其为活跃并行线，已恢复 |
| archive tag 指向删除前 commit 并验证 | PASS | 4 个 tag peeled commit = 删除前 head |
| 活跃 feature/发布/上游参考/独有实验分支保留 | PASS | feature `75f6080` 未动；Apple/WG/Cronet 例外保留；backup 恢复 |
| 删除后 ls-remote 重新采集 | PASS | `ROUND1-FINAL-REMOTE-AND-CI-EVIDENCE.md`；当前 4 个 heads |
| 正式分支最后 push SHA == 报告 SHA | PASS | `testing`/`integration` = `40dfed2`；facts 记录 |

## G. 终验质量

| 检查 | 状态 | 证据 |
|---|---|---|
| 当前状态字段来自单一结构化真值源 | PASS | `ROUND1-FINAL-FACTS.json`；报告引用它 |
| 主报告无互相矛盾最终 SHA/测试/统计 | PASS | closure + facts；旧 3736/3737/2528511/4ec0 标为历史 |
| 每个 P0 有闭环证据；未解决 P0 不宣称 VERIFIED | PASS | 108/Dashboard/merge-base CLOSED；sing-tun 明确 BASELINE_CONFIRMED |
| 例外含原因/影响/证据/解除条件 | PASS | closure §4/§5；facts exceptions |
| 自发现遗漏已加入并处理/分类 | PASS | backup 恢复；Apple release authority 风险；CI 40df 重跑 |
| 独立对抗复核 + 机器门禁 + 反例 FAIL | PASS | `--self-test` 6/6 rejected；normal PASS |
| 报告不再要求泛泛下一轮收尾 | PASS | closure §6 |

## H. 自主探索题

1. **最可能导致发布失败的单点？** Apple 来源权威：release 默认 `APPLE_CLIENT_BRANCH=main`（97b35d1），source-lock/gitlink 是 `7fce711`（比 main 多 9 个提交）；若 release 不显式传 `APPLE_CLIENT_REF/SHA`，Apple 产物与 source-lock 不一致。证据：`release/apple-client-refs.env`、`client-apple.yml` 的 resolve step、Apple pin/main 差异。处理：标 `BLOCKED`，不擅自改 release 或 pin；解除条件为明确 SHA 或 macOS 验证后对齐。次要单点：Cronet native generation，已用例外隔离。
2. **原始日志里还有摘要未提的 FAIL/panic/race/timeout？** 搜索 `audit-evidence/upstream/**`：`round1-race` 的 timeout 已全部归因到 SACK/SequenceWrap/MemoryDial；`main-merge-full-windows.txt` 的 FAIL 只在三个已知包；`main-conflict-dump-mine.txt` 的匹配是冲突标记文本而非运行 panic。未发现被摘要遗漏的真实 panic/data race 被写成 PASS。
3. **哪个 Fork pinned commit 与正式分支不同？** quic-go 消费线 `158445cd` vs `sagernet` `f864f2f1`（消费线含更多 fork 修复，设计）；Apple pin `7fce711` vs main `97b35d1`（产品线，但 release authority 风险）；Desktop `e8b48be` vs main `aa63764`（dashboard bump）；Dashboard `9bde90e` vs main `b592c65`；Cronet root/all/native 同代；WireGuard awg3 vs main。除 Apple 外均有消费线说明，不是拿错分支。
4. **报告 PASS 但 CI SHA 不等于源码 SHA？** 已拆分：代码树 `b473` 有 Verify/Android success；审计 commit `2528511` 另有 success；最终 docs HEAD `40df` 也已实际重跑 success；Mobile `08b52a3` 后只有文档/CI 脚本。没有把 b473 的 run 冒充 40df。
5. **被删分支仍被构建/source-lock/PR/子模块引用？** 搜索所有 md/json/yml/sh/py/go：backup 只在审计记录中，已恢复；client-candidate 只在历史报告/审计 JSON；docs/next-round 只在历史报告；没有构建脚本、source-lock、子模块引用这些分支名。
6. **高风险 108 项哪些只靠源码对照？** CSV 中 70 个 `RECONSTRUCTED` 全部是父侧等价审查；38 个 `VERIFIED` 中仍有部分为生成/配置/静态边界（如 release tags、go.mod、schema），已在 `validation_or_source_evidence` 写明，不声称全部行为测试。
7. **哪些 BASELINE_CONFIRMED 只做一轮、证据不足？** sing-tun A/B/C/D 每个 arm 各 1 次，但失败类别跨模式一致且旧/新同败；Windows 只做 1 次 pre/post，但失败测试名/错误逐字一致；httpclient 是 1 次全量失败 + 20/5 复跑。均标为“基线证据”，不升级为统计结论。
8. **本轮是否引入新文档/脚本/生成物不一致？** 新增 facts JSON、closure、self-check、remote evidence、增强门禁；没有改 schema/生成物/产品代码。门禁检查 CI 文件边界、gitlink、source-lock；路径存在性由 normal 模式读取。
9. **门禁能否被“改一个字段”骗过？** `--self-test` 注入 6 类错误全部 rejected；但门禁只覆盖它读取的字段，不覆盖 Dashboard 95 行内容/CI run 真实性，后者依赖原始日志/URL，不能靠单字段绕过。
10. **只拿 ZIP + 仓库能否独立复验？** 可以：facts JSON、CSV、failure matrix、remote evidence、source-lock、门禁与原始日志均在包内；Apple 需要外部 macOS/Xcode，属于明确 BLOCKED。

## 自行发现并处理

| 发现 | 处理 |
|---|---|
| `backup/02-ecfffe7d0` 被误删，实为活跃并行线 | 从 archive tag 恢复远端分支 `ecfffe7d0691`；更新 CLEANUP/FACTS；不合并 44 个独有提交，留待产品决定 |
| Apple release 默认解析 main，与 source-lock pin 不一致 | 记录为 `RELEASE_SOURCE_AUTHORITY_RISK`；不擅自改 release/pin；给出解除条件 |
| 旧报告 3736/3737/2528511/4ec0 多值并存 | 统一到 facts JSON：当前 3738/0、docs 40df、code b473、audit 2528511，旧值标历史 |
| 门禁只有正向检查 | 增加 6 类反例自测，全部 rejected |
| CI 最新 head 40df 未验证 | 实际查询并记录 Verify/Android success（不是旧 run 冒名） |
