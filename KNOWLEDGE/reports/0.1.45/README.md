# 0.1.45 归档：集成收尾报告集（0.1.45 RC）

- **状态**：本目录汇总 2026-11-10 各功能轮次的报告；2026-11-11 的六阶段集成把它们收敛为 **0.1.45 RC**（`release/JIEJIE_VERSION` 已为 `0.1.45`）。报告内出现的 SHA 是各自轮次实际消费或构建的提交，**不代表 RC tip**；最终 pin 以 `release/source-lock.json` 为准，阶段结论以 `INTEGRATION-LOG.md` 与各阶段报告为准。
- **源码基线**：集成分支 `integration/0.1.45-final`。归档目录名 `0.1.45` 沿用交接指定。
- **证据等级**：与 `KNOWLEDGE/README.md` 一致使用 `VERIFIED_SOURCE` / `VERIFIED_GIT` / `VERIFIED_UNIT` / `VERIFIED_INTEGRATION` / `VERIFIED_CI` / `VERIFIED_ARTIFACT` / `VERIFIED_DEVICE` / `INFERRED` / `UNVERIFIED` / `BLOCKED_BY_ENVIRONMENT`。报告记录的是各自轮次的检查结果；未在 RC 上重跑的项不因归档而升级。
- **脱敏**：本机路径、用户名、设备名与内部地址已替换为 `<workdir>`、`<evidence>`、`<machine>` 等占位符；保留公开仓库名、提交 SHA、CI run ID、产物 SHA256、指标与结论。

## 文件

| 文件 | 内容 |
|---|---|
| [`CONFIG-RECOVERY-AND-PREVIEW-2026-11-10.md`](CONFIG-RECOVERY-AND-PREVIEW-2026-11-10.md) | 配置恢复入口回归、产品 A/B/C 夹具、两端 `started` 预览语义、消费构建与产物 |
| [`DEBUG-SELF-REVIEW-2026-11-10.md`](DEBUG-SELF-REVIEW-2026-11-10.md) | 定向自审：反例、负向对照、假绿排查与已修缺陷 |
| [`HANDOFF-ACCEPTANCE.md`](HANDOFF-ACCEPTANCE.md) | 三项外部条件验收交接：环境、责任窗口、通过标准 |
| [`EVIDENCE-KIT.md`](EVIDENCE-KIT.md) | 采样/判定工具的重建命令、源码 SHA256 与原始数据位置 |
| [`DUAL-STACK-SOURCE-AND-RECOVERY-2026-11-10.md`](DUAL-STACK-SOURCE-AND-RECOVERY-2026-11-10.md) | 双栈 DNS 来源、字面恢复、候选 provenance 与验证命令 |
| [`RESOURCE-GOVERNANCE-WAITERS-2026-11-10.md`](RESOURCE-GOVERNANCE-WAITERS-2026-11-10.md) | 资源治理最终报告：等待者归属、队列关闭、relay 缓冲与 A/B 口径 |
| [`任务08_H3到H2回退_收尾报告.md`](任务08_H3到H2回退_收尾报告.md) | H3→H2 回退、请求拒绝分类、body 生命周期与 retry 安全 |
| [`02_测速DNS与诊断_交付收尾报告_2026-11-10.md`](02_测速DNS与诊断_交付收尾报告_2026-11-10.md) | 02 测速、DNS 与诊断交付收尾 |
| [`TASK10-APPLE-NE-README.md`](TASK10-APPLE-NE-README.md) | Apple NE 归档说明：命令目标、正文边界与已知限制 |
| [`TASK10-APPLE-NE-VERDICT.md`](TASK10-APPLE-NE-VERDICT.md) | Apple NE 裁定表：正文提交、回执归属、Provider 边界 |
| [`TASK10-APPLE-NE-HANDOFF.md`](TASK10-APPLE-NE-HANDOFF.md) | Apple NE 交接：内核与客户端 SHA、验证结论 |

## 一句话状态

报告覆盖的验证已在各自轮次完成；集成轮次补充了双栈/资源/H3 的联合回归、真实 netns 设备验收与客户端的 Dashboard/Desktop 构建测试。Apple iOS/macOS 构建、Android/satelite-one 构建与 Windows 管理员用例仍为环境阻塞，按 `BLOCKED_BY_ENVIRONMENT` 记录，不当作通过。
