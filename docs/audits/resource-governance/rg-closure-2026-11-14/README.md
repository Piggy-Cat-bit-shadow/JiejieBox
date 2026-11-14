# 跨层资源预算与生命周期 · 第一轮（rg-closure-2026-11-14）

施工指令：`01_跨层资源预算与生命周期_初步改造.md`
分支：`work/resource-governance-closure-2026-11-14`
基线：`testing` @ `fa61500731b1237e9ef44029b7469ee5373af4ba`

| 文件 | 内容 |
|---|---|
| `00_EXECUTIVE_SUMMARY.md` | 本轮结论、证据状态、边界、下一轮重点 |
| `01_BASELINE_AND_SCOPE.md` | canonical 仓库判定、clone/分支、环境、依赖 pin、范围 |
| `02_RESOURCE_OWNERSHIP_MAP.md` | 端到端链路、每层资源与所有者、每个协议的创建入口与准入结论 |
| `03_RESOURCE_BUDGET_MATRIX.md` | 对象数 / 字节 / 在途并发 / 等待队列 × 各层 |
| `04_LIFECYCLE_MATRIX.md` | 入口 → 昂贵初始化 → 持续占用 → 控制面 → 释放，以及交叉场景语义 |
| `05_DESIGN_DECISIONS.md` | 三个修复的方案选择、被否决方案、候选 KEEP/REJECT/DEFER |
| `06_TEST_MATRIX.md` | 新增测试、RED/GREEN、`-race`、未运行项、复现命令 |
| `07_PERFORMANCE_COMPARISON.md` | 结构性影响；本轮未跑基准，给出下一轮测量方案 |
| `08_OPEN_QUESTIONS.md` | Q1–Q10：下轮攻击面与最小实验 |
| `raw/` | 基线测试、RED、GREEN、构建/vet、全量 route 运行摘要 |

快速验证：

```sh
go test ./route -run 'TestNetworkTransitionCancelsAnInFlightPacketSetup|TestPacketSetupCarriesTheTrafficClass|TestEstablishedPacketFlowSurvivesATransition' -count=1
go test ./service/oomkiller -run 'TestUnmeasurableFootprint|TestFootprintZeroIsUnavailable|TestTimelineReportsUnavailableFootprint' -count=1
go test -tags with_quic ./transport/v2rayquic -run 'TestSaturatedAdmissionRefusesTheSharedCreation|TestEstablishedSessionIsReusedWithoutASecondPermit|TestTheSharedCreationSeesTheTriggeringFlowClass|TestAnAlreadyAdmittedDialIsNotChargedTwice' -count=1
```
