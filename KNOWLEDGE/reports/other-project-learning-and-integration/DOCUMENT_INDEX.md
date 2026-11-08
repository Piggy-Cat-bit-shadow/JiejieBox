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
| `stages/03.md` | 阶段 03 改动（B01–B06）、验证、终态 | afdd3b3 + cd1d816 | `SANITIZED` | 收尾提交 | 见文末 |
| `stages/04.md` | 阶段 04 批 1–3：residency owner 盘点、XMUX 库存、关闭诊断、共享 offer、R03/R04/R08 结案 | 2d9b848 + 64cfa9a + 文档 | `SANITIZED` | 批 1 `91c0c292db6`；批 2–3 见下 | 见文末 |
| `stages/05.md` | 阶段 05 P01：探测队列交互优先/后台老化 + 每轮有界生产 | 0dfb5e4 + f59f339/ae185ac | `SANITIZED` | 收尾提交 | 见文末 |
| `stages/06.md` | 阶段 06 F01 预置：domain_keyword 编译匹配 + 阈值/回退 | b804292 + 26296c3 | `SANITIZED` | 见下 | 见文末 |
| `explorations/lazy-heavy-runtime-2026-11-08.md` | R03 descriptor/runtime 拆分与 R04 never-built 惰性 device 的不采纳评估 | c77671b + WG 复跑 | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/endpoint-close-parallel-2026-11-08.md` | R08 endpoint 子 scope 并行关闭：合成测量、依赖与不采纳条件 | c77671b | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/data-plane-byte-budgets-2026-11-08.md` | 字节预算：XHTTP 落地、其余积压点边界与重开条件 | afdd3b3 + cd1d816 | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/quic-shared-offer-admission-2026-11-08.md` | QUIC 共享 offer 准入的跨仓 hook：不实施证据 | 11933a6 + pin 1ef2597 | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/cronet-native-creation-coverage-2026-11-08.md` | Cronet 原生创建覆盖的边界与阻断 | 11933a6 + pin 362e63b | `SANITIZED` | 收尾提交 | 见文末 |
| `explorations/README.md` | 探索结案规范；01 无条目 | — | `SANITIZED` | 同上 | 见文末 |
| `DOCUMENT_INDEX.md`（本文件） | 文档清单与核验 | — | `SANITIZED` | 同上 | 见文末 |

同步修改的既有文档：

| 文档 | 修改 | 脱敏 |
|---|---|---|
| `KNOWLEDGE/modules/power-mobile-lifecycle.md` | §4 新增“回收动作的报告反馈（2026-11-08）” | `SANITIZED` |
| `KNOWLEDGE/modules/core-lifecycle-runtime.md` | §5 race 证据行：本机已有 gcc，`BLOCKED_BY_ENVIRONMENT` 的旧前提不再成立（04 批 1 实测） | `SANITIZED` |
| `KNOWLEDGE/INDEX.md` | 新增“2026-11-08 本轮工程导航”表；04 批 1 行 | `SANITIZED` |
| `KNOWLEDGE/reports/README.md` | 目录结构表新增本专题一行 | `SANITIZED` |

## 代码提交

| 阶段 | 提交 | 内容 |
|---|---|---|
| 01 | `503a18b55a7cb53ca8aade3b90e69d5683832552` | `feat(oomkiller): record what each memory-reclaim pass actually returned`（8 files, +904/−22） |
| 02 | `99fd6ee23d986617afa3ef4d5e776eb1a874bcc3` | `feat(resourceguard): slow creation after a fast-fail streak…`（fast-fail 速率/退避、本地拒绝分类、Mieru underlay 准入、marker 生命周期测试） |
| 04 批 1 | `6c10b42df3718f86e4b6d92d02599ebe8b269eb4` | `feat(route,oomkiller): report the retirement walk's owner inventory`（residency owner 盘点、单一 owner 枚举、metadata 加性键） |
| 04 批 1 | `f523e49ece3ba422a8ba6cd24c23cdfd27a491da` | `feat(xhttp): inventory the xmux pool's draining connections`（池内库存、真实 Close 完成计数、代次） |
| 04 批 1 | `9a04cc1c1e829b9c1e77a5aaac7b360d7deeeabf` | `feat(adapter): bound the close diagnostic to one report per drain`（Scope 一次性 watchdog、4 KiB 栈、AddNamed、Box 阶段耗时） |
| 04 批 1 | `205412ba498426696fea2473415e0f582ad1cae3` | `style(adapter): gofmt the close-watchdog test`（CRLF → LF；内容不变） |
| 04 批 2 | `64cfa9a86304f0951313d845d94052294d6eefbf` | `feat(v2rayquic): share one QUIC creation between waiters`（pending offer、每 waiter ctx、迟到成功接纳/丢弃一次、keep_session 每 flow） |
| 04 收尾 | `80fe4bafe1b268f280ef57d7ac522cebc9e1cf02` | `test(adapter): cover the named component path of the close watchdog`（Start 注册组件的名字进入关闭报告；嵌套 scope 各自一次性） |
| 05 P01 批 1 | `f59f339ab396df4839a11f32536e9e308e3b81b8` | `feat(urltest): order the probe queue by origin with background aging`（交互优先、每 3 次交互老化一个后台、取消清理、幂等释放） |
| 05 P01 有界生产 | `ae185aca19618101c940312691d8ce762eeeb428` | `fix(urltest): bound probe production before spawning work`（每轮 10 worker + 有界交接队列；10,000 节点峰值 goroutines 9,650 → 14，全部测量） |
| 06 F01 预置 | `26296c328f32a2a488e1ab21814b1f536e77eca3` | `feat(rule): compile large domain_keyword sets into a frozen automaton`（≥64 编译、越界回退、随机差分、0 alloc） |

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
| 03 推送结果 | `afdd3b3cf7c..21f383b1fe7  testing -> testing`（含代码 `cd1d8160e3c` 与文档提交 `21f383b1fe7`） |
| 03 远端 refs | `git ls-remote origin refs/heads/testing` → `21f383b1fe7b1d6d7efde2d0e570116e18954b94` |
| 03 CI 核验 | 前一 run 42475816951（head `21f383b`）被记录推送取消；最终 head `5bcded4` 的 `Verify` run [42476106354](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42476106354) **success**（`VERIFIED_CI`，仅对该 SHA 的树；其后的记录回填提交只改文档） |
| 04 批 1 推送结果 | `8022cdf8153..91c0c292db6  testing -> testing`（3 个代码提交 + 文档提交 `91c0c292db6`；远端提示仓库已迁移为 `Piggy-Cat-bit-shadow/JiejieBox`） |
| 04 批 1 远端 refs | `git ls-remote origin refs/heads/testing` → `91c0c292db6b6024bb1a83f619ee7e15d2850665` |
| 04 批 1 远端回读 | raw HTTP 200：`https://raw.githubusercontent.com/Piggy-Cat-bit-shadow/JiejieBox/91c0c292db6…/KNOWLEDGE/reports/other-project-learning-and-integration/stages/04.md`，内容与本地一致 |
| 04 批 1 CI | 首个 head `91c0c292db6` 的 `Verify` run [42483227952](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42483227952) 在 `gofmt` 失败（新增测试文件以 CRLF 提交）；修复提交 `205412ba498` 的 `Verify` run [42484328424](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42484328424) **success**（`verify` 与 `race checks` 均 success；`VERIFIED_CI`，仅对该 SHA 的树） |
| 04 批 1 修复推送 | `4f275b66b6c..205412ba498  testing -> testing`；`git ls-remote origin refs/heads/testing` → `205412ba498426696fea2473415e0f582ad1cae3` |
| 04 批 2 推送结果 | `2d9b8487cd6..5a3add01bca  testing -> testing`（代码 `64cfa9a8630` + 文档 `5a3add01bca`） |
| 04 批 2 远端 refs | `git ls-remote origin refs/heads/testing` → `5a3add01bcaff0586bcf6be5d69f4c3e4741dfda` |
| 04 批 2 远端回读 | raw HTTP 200：`https://raw.githubusercontent.com/Piggy-Cat-bit-shadow/JiejieBox/5a3add01bca…/KNOWLEDGE/reports/other-project-learning-and-integration/stages/04.md`，内容与本地一致 |
| 04 批 2 CI | `Verify` run [42485997186](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42485997186)（head `5a3add01bca`）：`verify` 与 `race checks` 均 **success**（`VERIFIED_CI`，仅对该 SHA 的树；其后的回填提交只改文档） |
| 04 批 3 推送结果 | `c77671b41b8..b8590d75cc8  testing -> testing`（04 结案文档） |
| 04 批 3 远端 refs | `git ls-remote origin refs/heads/testing` → `b8590d75cc8f143f3c5cf0f25ae0243693422926` |
| 04 批 3 远端回读 | raw HTTP 200：`https://raw.githubusercontent.com/Piggy-Cat-bit-shadow/JiejieBox/b8590d75cc8…/KNOWLEDGE/reports/other-project-learning-and-integration/stages/04.md`，内容与本地一致 |
| 04 批 3 CI | `Verify` run [42487268576](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42487268576)（head `b8590d75cc8`）：`verify` 与 `race checks` 均 **success**（`VERIFIED_CI`，仅对该 SHA 的树；其后的回填提交只改文档） |
| 04 收尾推送/CI | `5e967504e40..af8c7f15e01  testing -> testing`；`Verify` run [42488445431](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42488445431)（head `af8c7f15e01`，含命名组件 watchdog 测试与 owner 边界表）：`verify` 与 `race checks` 均 **success**（`VERIFIED_CI`，仅对该 SHA 的树） |
| 05 P01 推送结果 | `0dfb5e41734..3d8c5f998cd  testing -> testing`（代码 `f59f339ab39` + 文档 `3d8c5f998cd`） |
| 05 P01 远端 refs | `git ls-remote origin refs/heads/testing` → `3d8c5f998cd22ce0df3e2871b7ef529237b19f62` |
| 05 P01 CI | `Verify` run [42489837757](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42489837757)（head `3d8c5f998cd`）：`verify` 与 `race checks` 均 **success**（`VERIFIED_CI`，仅对该 SHA 的树；其后的回填提交只改文档） |
| 05 P01 收尾推送/CI | `cde1c8d9e84..52bf2958f72  testing -> testing`；首个结案 head `cde1c8d9e84` 的 race 因 adapter 测试读取 watchdog 两行之间而失败（已修 `52bf2958f72`）；`Verify` run [42492077283](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42492077283)（head `52bf2958f72`）：`verify` 与 `race checks` 均 **success** |
| 06 F01 预置推送 | `b804292572e..40916bf4f7a  testing -> testing`（代码 `26296c328f3` + 文档 `40916bf4f7a`） |
| 06 F01 CI | `Verify` run [42493390111](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42493390111)（head `40916bf4f7a`）被后续文档推送取消；最终 head `c6324ac2cb2` 的 run [42493478151](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42493478151) `verify`+`race checks` success |
| 工作树收尾（01/02） | 01 收尾后干净；02 收尾把外部 Apple pin 改动恢复后再次干净：`git status --porcelain` 为空、三个子模块无 `+`、专题链接检查 0 处失效 |
| 状态 | `REMOTE_VERIFIED`（`SANITIZED` / `LOCAL_COMMITTED` / `REMOTE_VERIFIED`） |
