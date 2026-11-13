# Release 构建指引

从 Windows 执行整轮 Release。先发布并回读内核仓库正式版，再启动、发布并回读 Satelite 安卓客户端。两个仓库各自发布资产，整轮 Release 须两边都验收通过。用户明确要求仅发内核时才跳过 Satelite，并在交接中注明。Mac TF 可在内核正式版验收后独立开始，与 Satelite 互不等待。

本页是**操作指引，不是发布授权**。最近完成版本及失败复盘见 [0.1.46 交接](releases/RELEASE-0.1.46-HANDOFF.md)；阅读前核对[知识库基线](../KNOWLEDGE/BASELINE.md)、[CI 与发布模块](../KNOWLEDGE/modules/ci-release.md)及当前远端 `testing`。知识库的固定专题 SHA 不是自动选用的发布 SHA。

## 1. 锁定本轮候选

从 [调查基线](RELEASE-PLAYBOOK.md) 查旧事故和证据。每次记录本轮 Git SHA、对照基线和观察时间。检查基线到目标提交的 workflow、发布脚本、依赖声明、客户端 gitlink 与产物差异。当前构建入口见 [Kernel release](../.github/workflows/kernel-release.yml)，公开资产契约见 [release-artifacts.sh](../scripts/ci/release-artifacts.sh)。以候选源码为准。

- 核对主仓 `testing`、目标版本、远端 tag 与正式 Release。选择尚未使用的版本，更新 `release/JIEJIE_VERSION`，提交并推到 `testing`；Release preparation 会根据实际固定的依赖生成 `release/source-lock.json`。工作区应干净，不要从仍指向旧版本或旧 pin 的快照启动正式构建。
- **提前准备面向读者的版本说明。** `.github/RELEASE_NOTES.md` 按现有自动发布契约保持空文件，但这不代表公开 Release 页面应一直空白。按根目录 `CHANGELOG.md` 的既有排版起草本版内容；正式构建通过、附件公开并回读后，把同一版说明写入 `CHANGELOG.md` 和 GitHub Release 正文。发布后的文档提交不移动已冻结 tag。
- 对照 `release/dependencies.json`、根和 test 模块、Apple/Android/Desktop gitlink、嵌套 dashboard 及 Cronet native provenance。检查跟踪分支 tip，记录采用或保留理由。dashboard 变更先进入 Desktop 再冻结主仓。Cronet 原生库按受控流程验证字节。
- **核对 pin 的执行证据。** `release/source-lock.json` 证明冻结版本与源码一致；`release/pin-runs.json` 记录真实成功运行所覆盖的 pin 集合、分支头观察和未覆盖项。在候选上运行 `python scripts/ci/check-pin-runs.py`，核对 run ID、`head_sha`、结果与该 run 提交内的 source lock。浅克隆可按脚本说明使用 `--fetch-missing`。若 preparation 前移 pin，旧 run 不自动覆盖新集合：先让新 pin 的构建和验证真实运行，按结果更新记录，再取得新候选的 Verify 和 Release contract 证据。不能借用旧 SHA 的绿色运行。
- 运行 `python scripts/ci/check-knowledge-facts.py`，检查知识库入口、版本、source lock 与报告索引的一致性；该门禁不替代协议互通、设备或发布产物验证。
- 在正式构建前运行 Release preparation，**同时指定 `dry_run=false` 和 `create_tag=false`**，取得最终候选提交。该运行会前移仅落后的 update pin、重新生成锁文件、在有变更时提交推送，并等待最终 SHA 的门禁。默认 `dry_run=true` 只生成报告，不会更新 pin 或锁文件；不能把它的起始 SHA 当成最终候选。分叉的 pin 会提前失败，须先查明并处理。若 Apple 客户端修复尚未进入父仓 `clients/apple` gitlink，此步不可跳过。

```powershell
$repo = 'Piggy-Cat-bit-shadow/JiejieBox'
gh workflow run release-prepare.yml -R $repo --ref testing -f dry_run=false -f create_tag=false
```

- **在最终候选提交上复核锁和 gitlink。** 在干净检出执行 `git submodule update --init --recursive`、`python scripts/ci/release-freeze.py verify --lock release/source-lock.json --strict`，并用 `git ls-tree HEAD clients/apple clients/android clients/desktop` 对照锁文件。v0.1.42 的 preparation 曾用 `git add -A` 把已暂存的新 gitlink 覆盖成旧 checkout。发现不一致时先修候选，确认根仓提交和子模块实际检出与锁文件一致，再启动正式构建。参见 [v0.1.42 交接记录](releases/RELEASE-0.1.42-HANDOFF.md)。
- **在 preparation 可能前移 Apple gitlink 时，先比较跟踪分支头与 `scripts/ci/check-libbox-abi.sh` 的已验证 Apple SHA。** v0.1.44 首次 preparation 前移 gitlink 后因 ABI 常量仍指向旧客户端而失败，且尚未推送候选。先对目标客户端运行 `python scripts/ci/probe-accessors.py --contract docs/fork/libbox-abi-contract.tsv --lang swift --root <Apple checkout>`，结合客户端差异核对 Libbox 消费者，再更新已验证 SHA；重新运行 preparation。最终候选上仍要跑 `scripts/ci/check-libbox-abi.sh --pin-only`。改动 Apple workflow、Libbox 消费者或 TF 参数时，在正式构建前跑 `scripts/ci/test-apple-beta-publish.sh`，确认回归断言跟上实现。
- **确认最终候选有同一 SHA 的 Verify 与 Release contract 成功运行。** preparation 若移动 pin 并由 `GITHUB_TOKEN` 推送候选，GitHub 可能不会自动创建这两个工作流运行；含 `[skip ci]` 的提交也需要为正式候选显式取得门禁。先从 preparation 的输出取得最终候选 SHA；缺失时在 `testing` 上手动派发 `verify.yml` 与 `release-contract-verify.yml`，确认各自 `head_sha` 等于候选且所有必需作业成功，再重跑 preparation。Verify 内的 pin-runs 与知识库事实门禁也须通过；不能借用移动 pin 前的绿色记录。
- 重新枚举本轮公开附件。当前是 Linux amd64、macOS arm64 CLI、Windows amd64 core、Android ARM64 AAR、Apple Libbox、Windows x64 安装器、自签 unsigned IPA 和 SHA256SUMS。新增或移除产物时同时更新编排、产物契约和验收。Android AAR 与独立仓 Satelite APK 分别记录。

### 正式构建前的检查与止损

先用便宜、定向的检查排除已知问题，再占用整轮平台构建。**Verify / Release contract 通过不等于所有平台冒烟与维护账本都通过。** 为本轮记录“候选 SHA、检查、成功 run 或本地结果、未覆盖项”，有已知阻断时不启动正式编排。

| 检查时机 | 前置动作 | 对应教训 |
| --- | --- | --- |
| 每个最终候选 | 完整 Git 历史下执行 `python scripts/ci/check-upstream-retirement.py` 与 `python scripts/ci/check-knowledge-facts.py`；账本漂移先用 `python scripts/ci/upstream-retirement-batch2.py --write` 重生成、审阅差异，再复检 | 0.1.46 marker 行号和测试文件计数过期；该门禁在独立 Maintenance ledger 中，不能用 Verify 绿色替代 |
| Apple pin、Libbox API 或调用方改变 | ABI 探针之外，先取得实际目标客户端 iOS/macOS 编译证据；语法解析与 pin-only 不能替代 Swift 类型检查和链接。核对已有成功运行的源码与 pin，已有适用证据时不重复生产 | 0.1.45 到整轮构建才发现未穷尽分支、类型及绑定方法调用错误 |
| Native API、实例生命周期、事件订阅改变 | 在目标平台提前运行对应 headless 冒烟，包含独立 `run` 的 `SubscribeConnections` 初始快照；使用当前平台工作流中的实际命令 | 0.1.45 实例身份未初始化，直到 macOS 冒烟才报 `service is not running` |
| 并发竞速、选举、取消或相关测试改变；或最近出现同类失败 | 对受影响用例做有界重复运行，改变 CPU 并发度；等待业务完成条件而非请求已发出。修复后保留原业务断言 | 0.1.45 HTTP 竞速断言和 0.1.46 DNS 收集器同步缺陷；不能简单延长 sleep 或碰运气重跑 |
| 准备重跑 Apple 作业 | 先查 resolver 身份、Libbox manifest 和下游所需的 `run_id` / `run_attempt`，确定能否同一 attempt 消费 | 0.1.46 failed-only 重跑把 attempt 1 身份与 attempt 2 的库混用 |

DNS 历史故障的定向复检示例（有相关改动或复发时使用，不要求每版跑全仓压力测试）：

```powershell
go test -run '^TestFastestWinExpiryTriggersReelection$' -count=1000 '-cpu=1,2,8,32' -failfast -timeout=90s ./dns/transport/group
```

正式运行前查看当前同分支的排队/运行任务，复用适用的门禁证据，避免重复派发与并发组互相取消；只取消已确认被新候选取代的本轮任务。runner 容量无法由源码预检保证：超过约 10 分钟仍未分配 runner 时，核对 job 状态和 annotation，区分排队、环境失败与实际测试失败，记录等待时间；不要通过重复启动整轮构建抢资源。该时间是调查提醒，不是自动取消或完成承诺。

## 2. 从 Windows 启动

在有 Git 和已登录 GitHub CLI 的 Windows PowerShell 里执行其中一个入口。

```powershell
$repo = 'Piggy-Cat-bit-shadow/JiejieBox'
# 仅构建与验收
gh workflow run kernel-release.yml -R $repo --ref testing -f dry_run=true

# 一次运行完成构建、验收和发布
gh workflow run kernel-release.yml -R $repo --ref testing -f dry_run=false
```

发起后在 Actions 核对 run 的 `head_sha` 等于计划 SHA。只验收时用 dry run。已明确要正式发版时直接发起正式运行，避免例行先干跑再从头构建。dry run 不建 tag 或 Release，它的产物当前没有跨 run 晋升入口。

冻结检查通过后，源码与契约检查、Linux、macOS CLI、Windows core、Windows 安装器、Android AAR、Apple workflow 并行启动。`freeze` 严格核对 source lock、嵌套 gitlink、远端分支关系与 pin-runs 记录，并上传 `release-pin-report`；应回读该报告。Windows 安装器在候选模式绑定版本、源码、工具和桌面客户端来源。Apple 先固定客户端并构建一份 Libbox，然后 iOS 验证、macOS GUI 验证和自签 IPA 分别消费同一次运行的库。Apple 的单平台手动入口用于诊断。正式编排要求两平台和自签 IPA 完成；正常的 `Kernel release` 路径已包含自签 IPA 构建，不需要另行运行 self-sign 工作流。

## 3. 内核正式版验收、恢复和交接

- 确认本次 run 的必需作业、stage 和 publish 均通过。产物与摘要校验由 Actions 完成。发布者只需在 GitHub 回读公开状态、tag 对应的提交、附件清单和发布正文。无需再次下载附件或在本地复算校验和。
- **公开页面必须有本版更新说明。** 当前 `Kernel release` 自动创建的 Release 正文为空；附件验收后，从 `CHANGELOG.md` 取当前版本的概述和各节内容，去掉日志自身的版本标题，将相对文档链接改为可在 GitHub 页面打开的绝对链接，保存为 UTF-8 正文文件，再执行 `gh release edit v<version> -R $repo --notes-file <正文文件>`。随后用 `gh release view v<version> -R $repo --json body,isDraft,isPrerelease,assets` 回读正文非空、版本与附件不变，并在交接记录中区分初次公开和正文补写的时间。不能只提交 `CHANGELOG.md` 就宣称 Release 页面已有说明。v0.1.44 曾因遗漏此步留下空白页面，现已补写。
- 暂时故障先检查产物与身份依赖，确认原成功产物可被新 attempt 合法消费后，才在同一 run 重跑失败 job；Apple 身份耦合按下一条处理。发布步骤中断时优先重跑 publish，使用已验证的 bundle。现有 draft 只补缺失的文件，若已有文件摘要不一致则停下调查。记录 queue、实际 job 时间和阶段间等待，按冻结至公开回读的墙钟时间衡量优化。
- **Apple Libbox 尚未产出时，局部重跑须同时重建发布身份。** `resolve the Apple client source` 上传的 `apple-release.json` 和 Libbox manifest 都绑定 `run_attempt`。v0.1.46 在 runner 容量失败后仅重跑失败 job，沿用了 attempt 1 的身份，却构建 attempt 2 的库，因 `Libbox run_attempt differs from the frozen release` 被正确拒绝。此时不能继续只重跑 Libbox；须重跑整次 `Kernel release`，或使用已确认能把身份解析与全部 Apple 下游放在同一 attempt 的恢复入口。需要修改源码时仍按下一条重新冻结和完整构建，不改写身份文件或放宽校验。详见 [v0.1.46 交接记录](releases/RELEASE-0.1.46-HANDOFF.md)。
- 需要改代码、pin 或工具时重新冻结并构建。若正式 tag 已指向不同提交，使用新版本。旧 tag 的历史恢复入口为 Release (tagged recovery)，按对应提交的工作流契约和产物来源恢复；该恢复入口不重新构建，要求同一提交已有成功的 self-sign 产物。缺少时先从同一 SHA 的 Apple client self-sign 入口取得产物，再运行恢复流程。这是历史恢复的前置条件，不是正常 `Kernel release` 的额外步骤。
- v0.1.44 的首轮正式构建在 `contract / release helper tests` 被误判阻断：测试用 `grep -v | grep -q` 查找 workflow 调用，`set -o pipefail` 下前一个进程偶发收到 SIGPIPE，虽然 `release.yml` 确实调用了 `evidence`。遇到类似失败，先核对失败用例、实际 workflow 文本与同 SHA 的独立契约运行；修复检查器后必须提交、重新运行门禁和 preparation，再启动新的正式构建。v0.1.44 还遇到 Verify 的并发测试丢失对端读取信号：确认是测试同步缺陷后修复，不能把测试失败直接当作可忽略的偶发错误。
- 交接记录应包含版本、tag 剥离后的完整 SHA、Release URL、公开资产和校验和、Apple SHA、Libbox 摘要、run ID/attempt、回读时间。内核验收后可把这份记录交给独立的 [TF 构建指引](TESTFLIGHT-MAC.md) 执行者，同时继续第 4 节的 Satelite 发布。

## 4. 发布并验收 Satelite 安卓客户端

1. 内核的 `Kernel release` 运行完成后，从 `publish` 作业摘要取 Satelite 交接信息。打开 `Piggy-Cat-bit-shadow/satelite-one`，核对远端 `main`、最新客户端发布和已有 tag。以本轮**已公开并验收**的内核版本及其 tag 剥离后的完整 SHA 为输入。在 Satelite 的 `version.properties` 中令 `versionName` 等于内核版本，`coreCommit` 等于上述完整 SHA，`versionCode` 大于此前已分发的最高构建号。核对该仓依赖与内核兼容性，记录 Satelite 自己的候选提交。
2. 将对齐后的 Satelite 提交推到 `main`，确认 `v<versionName>` 尚未被占用，再把 Satelite 自己的正式 tag 打到**该客户端提交**并推送至 Satelite 的 `origin`。这是 `.github/workflows/release-apk.yml` 的正式签名构建和发布入口。手动 dry run 只能用于预检。正式发版提交和 tag 推送应允许 Actions 运行，避免在发版提交消息中加入 `[skip ci]`。
3. 确认 `Release APK` 是本轮 Satelite tag push 触发，run 的 `head_sha` 等于该客户端提交。等待构建、签名与发布完成。若 tag 已存在，先核对目标 SHA、已有运行和 Release 状态。相同提交的暂时故障优先在原运行重跑失败任务，已有 tag 指向其他提交时停止并查明原因。
4. 确认本次 `Release APK` 运行的构建、产物校验、签名检查和发布均通过。回读 Satelite 的指定公开 Release，核对 tag、客户端提交和附件清单。清单应包含 `arm64-v8a` 和 `x86_64` 两个签名 APK、`SHA256SUMS.txt`、`build-info.json` 与 `apksigner-verify.txt`。版本和内核来源以本次运行记录及已核对的客户端提交为准，无需下载附件重复验包。
5. 把 Satelite Release 链接、tag、客户端 SHA、内核 SHA、`versionCode`、run ID、APK 校验结果与内核记录汇总。只完成内核时，状态写「内核已发布，Satelite 待完成」，直到两个仓库的资产都验收通过才记录整轮完成。主仓 Android ARM64 AAR 和 Satelite 的两个 APK 分别在各自仓库验收。

## 5. 发布后文档收尾与耗时复盘

两个仓库验收后，在产品分支补齐本版 `CHANGELOG.md`、GitHub Release 正文、交接记录和受影响的配置/知识库入口；沿用已有日志“目的、协议与配置、修复、验证与发布”的写法，具体程度随改动范围调整。记录固定源码目标、证据级别和未验收范围；专题刷新不推进未重新审阅模块的全局基线。文档提交不移动正式 tag，也不重建已发布附件。

每轮交接记录分别统计：开始准备至整轮验收的总墙钟时间、最终成功编排时间、失败构建与排队时间、人工排障与补证时间、内核完成后的 Satelite 时间。没有起点记录就写未知，不把最终成功 run 的时长当作整轮耗时。0.1.45 最终成功内核运行 28 分 17 秒，0.1.46 为 28 分 38 秒；用户经历的数小时还包含此前失败与重建，必须把可前置发现的缺陷转成上面的检查项。一次正式运行中途需要修代码时，先收集已暴露的其它独立失败，集中修复、复检、重冻，避免修一个就立刻重开整轮。
