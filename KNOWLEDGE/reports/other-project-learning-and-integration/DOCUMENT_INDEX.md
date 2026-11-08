# DOCUMENT_INDEX —— 本轮文档、脱敏状态与提交/远端核验

本文件登记本轮工程产生的全部文档。**本地写文件或 commit 不等于已上传**；远端核验记录在文末，并随实际推送更新。

## 文档清单（01）

| 文档 | 内容 | 覆盖 revision | 脱敏 | 本地提交 | 远端 |
|---|---|---|---|---|---|
| `README.md` | 专题入口、阅读顺序、阶段状态 | 25f5c9a / 3b6ec64 | `SANITIZED` | `dd6fd606bcf9` | 见文末 |
| `MASTER_PLAN.md` | 55 项覆盖账、G01 范围拆分、阶段顺序 | 25f5c9a / 3b6ec64 | `SANITIZED` | 同上 | 见文末 |
| `ANALYSIS.md` | 三类资源调用图、锁序、生命周期、作用域、未审计区域 | 25f5c9a + 503a18b | `SANITIZED` | 收尾提交 | 见文末 |
| `SOURCES.md` | 参考项目/依赖/客户端锁/闭源边界来源核验 | 25f5c9a；远端 refs 2026-11-08 | `SANITIZED` | 同上 | 见文末 |
| `CONTRACTS.md` | 三种账、观测键、回收反馈、后续阶段接点、测试接缝 | 25f5c9a + 503a18b | `SANITIZED` | 同上 | 见文末 |
| `DECISIONS.md` | 8 条采纳/调整/否决与重开条件 | 25f5c9a + 503a18b | `SANITIZED` | 同上 | 见文末 |
| `EVIDENCE.md` | 命令、revision、环境、结果、证据等级、未验证边界 | 0de1c13→25f5c9a；503a18b | `SANITIZED` | 同上 | 见文末 |
| `HANDOFF.md` | 阅读缓存、接口、02 清单、阻断、恢复方式 | 25f5c9a + 503a18b | `SANITIZED` | 同上 | 见文末 |
| `stages/01.md` | 阶段 01 改动、验证、终态 | 同上 | `SANITIZED` | 同上 | 见文末 |
| `stages/02.md` | 阶段 02 改动（G02–G06）、验证、终态 | 11933a6 + 99fd6ee | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/quic-shared-offer-admission-2026-11-08.md` | QUIC 共享 offer 准入的跨仓 hook：不实施证据 | 11933a6 + pin 1ef2597 | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/cronet-native-creation-coverage-2026-11-08.md` | Cronet 原生创建覆盖的边界与阻断 | 11933a6 + pin 362e63b | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/README.md` | 探索结案规范；01 无条目 | — | `SANITIZED` | 同上 | 见文末 |
| `DOCUMENT_INDEX.md`（本文件） | 文档清单与核验 | — | `SANITIZED` | 同上 | 见文末 |

同步修改的既有文档：

| 文档 | 修改 | 脱敏 |
|---|---|---|
| `KNOWLEDGE/modules/power-mobile-lifecycle.md` | §4 新增“回收动作的报告反馈（2026-11-08）” | `SANITIZED` |
| `KNOWLEDGE/INDEX.md` | 新增“2026-11-08 本轮工程导航”表 | `SANITIZED` |
| `KNOWLEDGE/reports/README.md` | 目录结构表新增本专题一行 | `SANITIZED` |

## 代码提交

| 阶段 | 提交 | 内容 |
|---|---|---|
| 01 | `503a18b55a7cb53ca8aade3b90e69d5683832552` | `feat(oomkiller): record what each memory-reclaim pass actually returned`（8 files, +904/−22） |
| 02 | `99fd6ee23d986617afa3ef4d5e776eb1a874bcc3` | `feat(resourceguard): slow creation after a fast-fail streak…`（fast-fail 速率/退避、本地拒绝分类、Mieru underlay 准入、marker 生命周期测试） |

## 脱敏检查

- **范围**：上述新文档全文、被修改段落的 diff、命令块、表格与链接；不含原始日志附件、截图或配置全文。
- **方法**：模式扫描 + 人工语义复核。扫描类别：凭据/token/API key/私钥/认证头/cookie、私有 IP 段与内部域名、设备/账号标识、用户名绝对路径。主机绝对路径统一改写为 `<workspace>`/`<toolchain>`；公开仓库 URL、公开提交 SHA、公开 Release 编号与主机性能数字保留（必要且有复现价值）。
- **结果**：未发现需要移除的秘密值；未删除任何真实值（因此无可披露的删除项）。01 收尾后父仓工作树干净；02 期间检测到外部进程把 `clients/apple` 改到 `97b35d1`（Apple `origin/main` 头）并改写 source-lock 与 ABI 脚本，本阶段未提交该更新，收尾时已恢复父仓 pin `e328310`，工作树与子模块重新干净。
- **限制**：模式扫描不等于取证审计；本轮没有对历史提交做泄露扫描，也没有把整个仓库重新认证。

## 远端核验记录

| 项 | 值 |
|---|---|
| 本地文档提交 | `dd6fd606bcf903c645377534a35dbc91d9eed7bd`（本文件所在提交；本表的核验回填为其后一次小提交） |
| 推送目标 | `origin` / `testing`（`https://github.com/Piggy-Cat-bit-shadow/sing-box.git`，远端提示仓库已迁移为 `Piggy-Cat-bit-shadow/JiejieBox`） |
| 推送结果 | `25f5c9a638b..dd6fd606bcf  testing -> testing`（`git push` exit 0） |
| 远端 refs | `git ls-remote origin refs/heads/testing` → `dd6fd606bcf903c645377534a35dbc91d9eed7bd`（随后核验回填提交 `fbb339e6212f86f7f1a85998632826f1c76f949e` 亦已推送并回读本文件） |
| 远端回读 | raw 文件 HTTP 200：`https://raw.githubusercontent.com/Piggy-Cat-bit-shadow/JiejieBox/dd6fd606bc…/KNOWLEDGE/reports/other-project-learning-and-integration/README.md`，内容与本地一致；GitHub API commit `503a18b5…` 存在（parent `25f5c9a6…`，stats +904/−22） |
| CI 核验（01） | `Verify` run [42471833521](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42471833521)，head `37b666f7a6b`，`verify` 与 `race checks` 均 success（`VERIFIED_CI`，仅对该 SHA 的树） |
| 02 推送结果 | `11933a68239..d14933eada2  testing -> testing`（`git push` exit 0；包含代码提交 `99fd6ee23d98` 与本文档提交 `d14933eada2`） |
| 02 远端 refs | `git ls-remote origin refs/heads/testing` → `d14933eada21a958c36a4490fa8b34bd930af945` |
| 02 远端回读 | raw 文件 HTTP 200：`.../d14933eada2…/KNOWLEDGE/reports/other-project-learning-and-integration/stages/02.md`，内容与本地一致 |
| 02 CI 核验 | `Verify` run [42474314237](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42474314237)，head `d14933eada2`，`verify` 与 `race checks` 均 success（`VERIFIED_CI`，仅对该 SHA 的树；其后的记录回填提交只改文档） |
| 工作树收尾（01/02） | 01 收尾后干净；02 收尾把外部 Apple pin 改动恢复后再次干净：`git status --porcelain` 为空、三个子模块无 `+`、专题链接检查 0 处失效 |
| 状态 | `REMOTE_VERIFIED`（`SANITIZED` / `LOCAL_COMMITTED` / `REMOTE_VERIFIED`） |
