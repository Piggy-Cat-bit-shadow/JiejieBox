# 0.1.45 归档：配置规则与恢复、两端预览语义与采样验收

- **归档目录名**：`0.1.45` 由交接提示词指定；**实际构建与发布版本是 `0.1.44`**——`release/source-lock.json` 的 `release_version`、标签 `v0.1.44`、Windows 安装包与 Apple 产物的 `BUILD-INFO` 均为 `0.1.44`。截至归档时没有 `0.1.45` 的构建或发布记录；本目录名只表示"面向下一版的收尾材料"，不表示该版本已存在。
- **源码基线**：集成分支 `integration/next-round`，归档提交前 HEAD 为 `edbfe8e0b2b`（工作树干净）。报告中另列的 SHA 都是实际消费或实际构建的提交，见各节。
- **证据等级**：与 `KNOWLEDGE/README.md` 一致使用 `VERIFIED_SOURCE` / `VERIFIED_GIT` / `VERIFIED_UNIT` / `VERIFIED_INTEGRATION` / `VERIFIED_CI` / `VERIFIED_ARTIFACT` / `VERIFIED_DEVICE` / `INFERRED` / `UNVERIFIED` / `BLOCKED_BY_ENVIRONMENT`。CI 通过一律附 run ID 与被审计 SHA。
- **脱敏**：本机路径、用户名、设备名与内部地址已替换为 `<workdir>`、`<evidence>`、`<machine>` 等占位符；保留公开仓库名、提交 SHA、CI run ID、产物 SHA256、指标与结论。原始日志未纳入本目录，只保留可复核的数字与命令。

## 文件

| 文件 | 内容 |
|---|---|
| [`CONFIG-RECOVERY-AND-PREVIEW-2026-11-10.md`](CONFIG-RECOVERY-AND-PREVIEW-2026-11-10.md) | 结果报告：Windows 配置恢复入口回归与产品夹具、两端 `started` 预览修复、消费构建、D 采样、Apple 构建与包标识 |
| [`DEBUG-SELF-REVIEW-2026-11-10.md`](DEBUG-SELF-REVIEW-2026-11-10.md) | 定向自审：10 条反例/发现、负向对照、“绿了但没生效”排查与证伪记录 |
| [`HANDOFF-ACCEPTANCE.md`](HANDOFF-ACCEPTANCE.md) | 三项外部条件验收交接：环境、责任窗口、下一条命令、通过标准、产物与 SHA 映射 |
| [`EVIDENCE-KIT.md`](EVIDENCE-KIT.md) | 采样/判定工具的接口、源码 SHA256、重建命令与原始数据位置（工具不入库） |

## 一句话状态

本窗口能做的验证已完成：配置恢复的真实入口回归（含损坏存储与重启重放）、两端预览语义修复与回归、Windows 安装包与运行时审计、Apple iOS/macOS 无签名构建与包标识核对、D 的采样协议与判定器自证。仍缺三件**外部条件**：提权+认证客户端的 Windows 产品进程 A/B/C、可证明独占的采样窗口、可安装的 Apple 设备/签名环境。详见交接文件。
