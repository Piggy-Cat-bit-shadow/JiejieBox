# Release 构建指引

从 Windows 执行整轮 Release。先发布并回读内核仓库正式版，再启动、发布并回读 Satelite 安卓客户端。两个仓库各自发布资产，整轮 Release 须两边都验收通过。用户明确要求仅发内核时才跳过 Satelite，并在交接中注明。Mac TF 可在内核正式版验收后独立开始，与 Satelite 互不等待。

## 1. 锁定本轮候选

从 [调查基线](RELEASE-PLAYBOOK.md) 查旧事故和证据。每次记录本轮 Git SHA、对照基线和观察时间。检查基线到目标提交的 workflow、发布脚本、依赖声明、客户端 gitlink 与产物差异。当前构建入口见 [Kernel release](../.github/workflows/kernel-release.yml)，公开资产契约见 [release-artifacts.sh](../scripts/ci/release-artifacts.sh)。以候选源码为准。

- 核对主仓 `testing`、目标版本、远端 tag 与正式 Release，更新 `release/JIEJIE_VERSION` 和 `release/source-lock.json`。工作区应干净，目标必须已推到 `testing`。
- **更新日志留空。** 本轮 `.github/RELEASE_NOTES.md` 保持空文件，GitHub Release 正文留空。发布前清除模板中的旧版内容，并在发布回读时确认正文为空。
- 对照 `release/dependencies.json`、根和 test 模块、Apple/Android/Desktop gitlink、嵌套 dashboard 及 Cronet native provenance。检查跟踪分支 tip，记录采用或保留理由。dashboard 变更先进入 Desktop 再冻结主仓。Cronet 原生库按受控流程验证字节。
- 若需刷新 pins，先运行 Release preparation，显式用 `create_tag=false`，检查最后产生的提交和锁文件。编排入口核对已提交的快照并复查分支 tip。落后的 update pin 或分叉会提前失败。
- **在最终候选提交上复核锁和 gitlink。** 执行 `python scripts/ci/release-freeze.py verify --lock release/source-lock.json`，并用 `git ls-tree HEAD clients/apple clients/android clients/desktop` 对照锁文件。v0.1.42 的 preparation 曾用 `git add -A` 把已暂存的新 gitlink 覆盖成旧 checkout。发现不一致时先修候选，确认根仓提交和子模块实际检出与锁文件一致，再启动正式构建。参见 [v0.1.42 交接记录](releases/RELEASE-0.1.42-HANDOFF.md)。
- 客户端 gitlink 变化时，发版前跑 `scripts/ci/check-libbox-abi.sh --pin-only`。如果核验过的 ABI pin 需要更新，先针对新客户端版本按 ABI 检查要求复验，再更新常量。改动 Apple workflow、Libbox 消费者或 TF 参数时，在正式构建前跑 `scripts/ci/test-apple-beta-publish.sh`，确认回归断言跟上实现。
- 重新枚举本轮公开附件。当前是 Linux amd64、macOS arm64 CLI、Windows amd64 core、Android ARM64 AAR、Apple Libbox、Windows x64 安装器、自签 unsigned IPA 和 SHA256SUMS。新增或移除产物时同时更新编排、产物契约和验收。Android AAR 与独立仓 Satelite APK 分别记录。

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

冻结检查通过后，源码与契约检查、Linux、macOS CLI、Windows core、Windows 安装器、Android AAR、Apple workflow 并行启动。Windows 安装器在候选模式绑定版本、源码、工具和桌面客户端来源。Apple 先固定客户端并构建一份 Libbox，然后 iOS 验证、macOS GUI 验证和自签 IPA 分别消费同一次运行的库。Apple 的单平台手动入口用于诊断。正式编排要求两平台和自签 IPA 完成。

## 3. 内核正式版验收、恢复和交接

- 确认本次 run 的必需作业、stage 和 publish 均通过。产物与摘要校验由 Actions 完成。发布者只需在 GitHub 回读公开状态、tag 对应的提交、附件清单和发布正文。无需再次下载附件或在本地复算校验和。
- 暂时故障优先在同一 run 重跑失败 job。发布步骤中断时优先重跑 publish，使用已验证的 bundle。现有 draft 只补缺失的文件，若已有文件摘要不一致则停下调查。记录 queue、实际 job 时间和阶段间等待，按冻结至公开回读的墙钟时间衡量优化。
- 需要改代码、pin 或工具时重新冻结并构建。若正式 tag 已指向不同提交，使用新版本。旧 tag 的历史恢复入口为 Release (tagged recovery)，按对应提交的工作流契约和产物来源恢复。
- 交接记录应包含版本、tag 剥离后的完整 SHA、Release URL、公开资产和校验和、Apple SHA、Libbox 摘要、run ID/attempt、回读时间。内核验收后可把这份记录交给独立的 [TF 构建指引](TESTFLIGHT-MAC.md) 执行者，同时继续第 4 节的 Satelite 发布。

## 4. 发布并验收 Satelite 安卓客户端

1. 内核的 `Kernel release` 运行完成后，从 `publish` 作业摘要取 Satelite 交接信息。打开 `Piggy-Cat-bit-shadow/satelite-one`，核对远端 `main`、最新客户端发布和已有 tag。以本轮**已公开并验收**的内核版本及其 tag 剥离后的完整 SHA 为输入。在 Satelite 的 `version.properties` 中令 `versionName` 等于内核版本，`coreCommit` 等于上述完整 SHA，`versionCode` 大于此前已分发的最高构建号。核对该仓依赖与内核兼容性，记录 Satelite 自己的候选提交。
2. 将对齐后的 Satelite 提交推到 `main`，确认 `v<versionName>` 尚未被占用，再把 Satelite 自己的正式 tag 打到**该客户端提交**并推送至 Satelite 的 `origin`。这是 `.github/workflows/release-apk.yml` 的正式签名构建和发布入口。手动 dry run 只能用于预检。正式发版提交和 tag 推送应允许 Actions 运行，避免在发版提交消息中加入 `[skip ci]`。
3. 确认 `Release APK` 是本轮 Satelite tag push 触发，run 的 `head_sha` 等于该客户端提交。等待构建、签名与发布完成。若 tag 已存在，先核对目标 SHA、已有运行和 Release 状态。相同提交的暂时故障优先在原运行重跑失败任务，已有 tag 指向其他提交时停止并查明原因。
4. 确认本次 `Release APK` 运行的构建、产物校验、签名检查和发布均通过。回读 Satelite 的指定公开 Release，核对 tag、客户端提交和附件清单。清单应包含 `arm64-v8a` 和 `x86_64` 两个签名 APK、`SHA256SUMS.txt`、`build-info.json` 与 `apksigner-verify.txt`。版本和内核来源以本次运行记录及已核对的客户端提交为准，无需下载附件重复验包。
5. 把 Satelite Release 链接、tag、客户端 SHA、内核 SHA、`versionCode`、run ID、APK 校验结果与内核记录汇总。只完成内核时，状态写「内核已发布，Satelite 待完成」，直到两个仓库的资产都验收通过才记录整轮完成。主仓 Android ARM64 AAR 和 Satelite 的两个 APK 分别在各自仓库验收。
