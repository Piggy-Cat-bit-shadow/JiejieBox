# 0.1.46 发布后专题刷新（2026-11-13）

本次按 REFRESH 小增量流程审阅 `v0.1.45`（`97091ce3637e68533c060ebac4ff9e309e512f25`）至 `v0.1.46`（`c795bb5cbbcd9b6a4d2d2f18e854b629915335f5`）以及随后发布文档。按 Git 差异分类：生产改动集中于 TrustTunnel option、outbound、会话与 QUIC 错误分类；其余为回归测试、Linux QUIC 测试接线、版本/锁版本、账本及文档。客户端 gitlink 和 Go 依赖 pin 未因本版回退改动前移。0.1.45 的旧专题目标不是正式 tag，本次发布复盘另核对两个版本的交接记录。

## 更新范围与证据

- 协议：重新审阅 `connectSession`、`fallbackEligible`、代次写入、回退会话退休和配置校验；来源提交、取代关系与局限写回[协议模块](modules/protocols-0145.md)。更新中英文配置和首页 TrustTunnel 相关文字。
- DNS：`c795bb5cb` 只增加测试完成屏障，生产选举实现不变；本轮发布时已做 4,000 次定向重复和包级 20 次回归，正式 macOS 通过，见发布交接。
- CI：核对实际 workflow、Maintenance ledger 独立触发、Apple attempt 身份与两版失败记录；补入[发布模块](modules/ci-release.md)、操作指引和历史调查入口。发布前置表是操作要求，本次没有改 workflow。
- 发布：内核最终 run 43404981823 / attempt 1，Satelite run 43408906275 / attempt 1，公开状态、来源和资产记录见 [0.1.46 交接](../docs/releases/RELEASE-0.1.46-HANDOFF.md)。API 元数据回读不是本机逐字节验包，CI 不证明 TF 或设备行为。
- 一致性：更新 README/BASELINE 当前专题、INDEX 路由、ACCEPTANCE 与 INVARIANTS；知识事实门禁和文档差异检查用于交叉核对。0.1.45/AWG 历史内容仍保留各自目标，不改成全仓 0.1.46 认证。

## 发布耗时教训

先做账本和配置等低成本检查；Apple pin/API 与实例生命周期有变化时，提前取得真实编译或目标平台冒烟证据；并发测试按真实完成条件同步并定向复检。重跑前先判断 attempt 依赖，不能默认 failed-only 安全。排队、失败构建、人工修复、最终构建和 Satelite 分开记录。没有完整任务起点时不补造耗时数字。

```text
KNOWLEDGE_REFRESH = COMPLETE (TrustTunnel / release scoped delta only)
OLD_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28 (unchanged; broad modules not re-audited)
SCOPED_TARGET = c795bb5cbbcd9b6a4d2d2f18e854b629915335f5
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
DELTA_CLASSIFIED = YES
AFFECTED_SUBSYSTEMS_REAUDITED = YES
COMMIT_PROVENANCE_UPDATED = YES
INVARIANTS_REAUDITED = YES
ACCEPTANCE_UPDATED = YES
HISTORICAL_DOCS_RECONCILED = YES
CROSS_FILE_CONSISTENCY = PASS
FUTURE_AI_INCREMENTAL_READ = PASS
```
