# 验收交接：三项外部条件

- 基线：`integration/next-round` @ `edbfe8e0b2b`
- 已完成的本地/CI 证据见 `CONFIG-RECOVERY-AND-PREVIEW-2026-11-10.md`；自审发现见 `DEBUG-SELF-REVIEW-2026-11-10.md`
- 三项目前都是 `BLOCKED_BY_ENVIRONMENT`，不是失败，也不能按通过计

## 1｜Windows 产品进程 A/B/C（真实服务 + 认证客户端）

**已有证据（不重复施工）**

- 入口回归 4 条：解析拒绝、构建失败恢复、重启重放、损坏存储。
- 产品夹具与期望矩阵：`daemon/testdata/product-abc/`（A/B/C_parse/C_build + `canned_proxy` + README）。
- 真实 daemon 的三个只读 RPC 已在开发传输上实跑；`configtx` 子进程与 installer 生命周期分别记账。

**缺什么**：提权（Administrator）安装隔离服务 + 与 daemon 同签名或被 peer 认证接受的客户端。本机 console 模式被 `daemon working directory is not owned by SYSTEM` 拒绝；`--listen` 无 peer 身份；命名管道需要管理员安装服务。

**下一条命令（在提权环境）**

1. 专用空目录安装候选包 daemon 为服务，`-D <data-dir>`；记录 PID、启动时间、日志。
2. 启动 `canned_proxy`（两个回环节点），产品客户端提交 `A.json`。
3. 按 `daemon/testdata/product-abc/README.md` 的矩阵执行：A → B（对数据目录拒绝写入，期望 `persistence=degraded`、磁盘仍 A）→ `C_parse.json`（报错、无恢复事件、B 仍在服务）→ `C_build.json`（恢复 A）→ 结束进程、同目录重启、重放 `C_build.json`。

**通过标准**

- 每一步的端口矩阵与 README 一致（`node-A`/`node-B`/拒绝）。
- 报告字段：B 的 `persistence=degraded`；C_parse 的 `failed`/`retained=true`/`activeRevision=B`；C_build 的 `recovered`/`activeRevision=A`/`failedRevision=C_build`。
- 磁盘：`commit.json` 始终为 A 组合，`commit.candidate` 不残留；重启重放后仍由磁盘恢复到 A。
- 保留退出/重启、认证、RPC 返回、HTTP 响应与磁盘文件的原始记录。

## 2｜D 合格独占窗口

**已有证据**：d5 低负载对照（1,926 次/边、三列中位数与秩检验）；d6 判定器自检 10/10、门禁 fixture、四类负向实跑拒收、独立复算一致。

**缺什么**：可证明的安静窗口——开跑前 30 秒全机均值 <5%，且排除被测与采样器后任一其他进程 ≤2%。

**下一条命令**

```text
run_d6_wait.ps1 -Hours 9 -Rounds 6 -ShortRounds 2
```

门禁每约 40 秒采样一次，命中后先跑 6+6，全批不可用才跑 2+2 以区分"合法路径坏了"与"窗口中途关闭"。

**通过标准**

- `d6/batch-*/batch.json` 的 `testCompleted=true`、`environmentQualified=true`、两面各 ≥6 轮 `valid`、`usableForComparison=true`。
- 每轮 321 行/321 成功、零延期/失败/取消/超时；原始样本覆盖 ≥80% 且无 >3 个采样间隔的空洞。
- `analyze_d6.py` 输出三列中位数、效应量与秩检验；有限样本下不显著只记"本批未检出回退"。

## 3｜Apple 运行期

**已有证据**：iOS IPA `4d5b0b31…`、macOS DMG `8122922f…`（均 `VERIFIED_ARTIFACT`，无签名）；包内标识、扩展点与 App Group 已核对；配方已按真值修正。

**缺什么**：Mac + Xcode 或可安装设备/自签环境。

**下一条命令（在 macOS）**

1. 用与构建一致的标识重签：iOS `io.nekohasekai.sfamt` / `io.nekohasekai.sfamt.extension` / `group.io.nekohasekai.sfamt`；macOS `io.nekohasekai.sfamt.standalone` / `io.nekohasekai.sfamt.system`。iOS 应用路径 `Payload/sing-box.app`，macOS 为 `SFM.app`。
2. `xcrun devicectl device install app --device <UDID> Payload/sing-box.app`；macOS 拖入 Applications 并允许系统扩展。
3. 打开 Core 配置页，对照同一会话的 `PreviewServiceChange` / `GetLastServiceChange` / `GetLastConfigRecovery` 三份 JSON 与 UI 三行。

**通过标准**

- 首次启动、已有实例的在线变更、需重启、`unchanged`、`stale`、无恢复事件、旧内核（空串/`Unimplemented`）各分支的显示与 JSON 一致。
- 扩展状态为已连接；App Group 下配置与 Provider 文件可读写；日志中实例 SHA/标识与安装包一致。

## 产物与 SHA 映射

| 项 | 值 |
|---|---|
| 集成 HEAD（归档前） | `edbfe8e0b2b` |
| Windows 安装包 | run `42735401015`；core `5778c8417d3` / desktop `aa63764` / dashboard `fc1dbb4`；artifact `6599cf6f…` |
| Windows 审计 | run `42736254206`（gate 5/5b/6/7 + 共存冒烟） |
| Apple 源码 | `7fce711`（gitlink 与发布锁一致） |
| iOS 产物 | run `42740100569`，IPA `4d5b0b31…` |
| macOS 产物 | run `42742558200`，DMG `8122922f…` |
| 归档版本 | 目录 `0.1.45`；实际发布版本 `0.1.44` |
