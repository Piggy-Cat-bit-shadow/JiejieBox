# DOCUMENT_INDEX —— 本轮文档、脱敏状态与提交/远端核验

本文件登记本轮工程产生的全部文档。**本地写文件或 commit 不等于已上传**；远端核验记录在文末，并随实际推送更新。

## 文档清单（01）

| 文档 | 内容 | 覆盖 revision | 脱敏 | 本地提交 | 远端 |
|---|---|---|---|---|---|
| `README.md` | 专题入口、阅读顺序、阶段状态 | 25f5c9a / 3b6ec64 | `SANITIZED` | 本目录文档提交 | 见文末 |
| `MASTER_PLAN.md` | 55 项覆盖账、G01 范围拆分、阶段顺序 | 25f5c9a / 3b6ec64 | `SANITIZED` | 同上 | 见文末 |
| `SOURCES.md` | 参考项目/依赖/客户端锁/闭源边界来源核验 | 25f5c9a；远端 refs 2026-11-08 | `SANITIZED` | 同上 | 见文末 |
| `CONTRACTS.md` | 三种账、观测键、回收反馈、后续阶段接点、测试接缝 | 25f5c9a + 503a18b | `SANITIZED` | 同上 | 见文末 |
| `DECISIONS.md` | 8 条采纳/调整/否决与重开条件 | 25f5c9a + 503a18b | `SANITIZED` | 同上 | 见文末 |
| `EVIDENCE.md` | 命令、revision、环境、结果、证据等级、未验证边界 | 0de1c13→25f5c9a；503a18b | `SANITIZED` | 同上 | 见文末 |
| `HANDOFF.md` | 阅读缓存、接口、02 清单、阻断、恢复方式 | 25f5c9a + 503a18b | `SANITIZED` | 同上 | 见文末 |
| `stages/01.md` | 阶段 01 改动、验证、终态 | 同上 | `SANITIZED` | 同上 | 见文末 |
| `explorations/README.md` | 探索结案规范；01 无条目 | — | `SANITIZED` | 同上 | 见文末 |
| `DOCUMENT_INDEX.md`（本文件） | 文档清单与核验 | — | `SANITIZED` | 同上 | 见文末 |

同步修改的既有文档：

| 文档 | 修改 | 脱敏 |
|---|---|---|
| `KNOWLEDGE/modules/power-mobile-lifecycle.md` | §4 新增“回收动作的报告反馈（2026-11-08）” | `SANITIZED` |
| `KNOWLEDGE/INDEX.md` | 新增“2026-11-08 本轮工程导航”表 | `SANITIZED` |
| `KNOWLEDGE/reports/README.md` | 目录结构表新增本专题一行 | `SANITIZED` |

## 代码提交

`503a18b55a7cb53ca8aade3b90e69d5683832552` —— `feat(oomkiller): record what each memory-reclaim pass actually returned`（8 files, +904/−22）。

## 脱敏检查

- **范围**：上述新文档全文、被修改段落的 diff、命令块、表格与链接；不含原始日志附件、截图或配置全文。
- **方法**：模式扫描 + 人工语义复核。扫描类别：凭据/token/API key/私钥/认证头/cookie、私有 IP 段与内部域名、设备/账号标识、用户名绝对路径。主机绝对路径统一改写为 `<workspace>`/`<toolchain>`；公开仓库 URL、公开提交 SHA、公开 Release 编号与主机性能数字保留（必要且有复现价值）。
- **结果**：未发现需要移除的秘密值；未删除任何真实值（因此无可披露的删除项）。本地 `git status` 显示的 `clients/android` 子模块回退不属于本轮文档，未提交。
- **限制**：模式扫描不等于取证审计；本轮没有对历史提交做泄露扫描，也没有把整个仓库重新认证。

## 远端核验记录

| 项 | 值 |
|---|---|
| 本地文档提交 | 见本文件所在提交（`git log -1 --format=%H -- KNOWLEDGE/reports/other-project-learning-and-integration/DOCUMENT_INDEX.md`） |
| 推送目标 | `origin` / `testing`（`https://github.com/Piggy-Cat-bit-shadow/sing-box.git`） |
| 推送结果 | 待推送后回填 |
| 远端回读 | 待推送后回填（`git ls-remote` + 远端文件内容/提交页核对） |
| 状态 | `LOCAL_COMMITTED`（推送与回读完成后改为 `REMOTE_VERIFIED`） |
