# Release 构建指引

本任务从 Windows 发起 GitHub Actions，完成内核仓库正式 Release 并回读后交接。Satelite 和 Mac TF 都在主仓发布后各自处理，互不等待。

## 1. 锁定本轮候选

从 [调查基线](RELEASE-PLAYBOOK.md) 查旧事故和证据。每次记录本轮 Git SHA、对照基线和观察时间。检查基线到目标提交的 workflow、发布脚本、依赖声明、客户端 gitlink 与产物差异。当前构建入口见 [Kernel release](../.github/workflows/kernel-release.yml)，公开资产契约见 [release-artifacts.sh](../scripts/ci/release-artifacts.sh)。以候选源码为准。

- 核对主仓 `testing`、目标版本、远端 tag 与正式 Release，更新 `release/JIEJIE_VERSION` 和 `release/source-lock.json`。工作区应干净，目标必须已推到 `testing`。
- **更新日志留空。** 本轮 `.github/RELEASE_NOTES.md` 保持空文件，GitHub Release 正文留空。发布前清除模板中的旧版内容，并在发布回读时确认正文为空。
- 对照 `release/dependencies.json`、根和 test 模块、Apple/Android/Desktop gitlink、嵌套 dashboard 及 Cronet native provenance。检查跟踪分支 tip，记录采用或保留理由。dashboard 变更先进入 Desktop 再冻结主仓。Cronet 原生库按受控流程验证字节。
- 若需刷新 pins，先运行 Release preparation，显式用 `create_tag=false`，检查最后产生的提交和锁文件。编排入口核对已提交的快照并复查分支 tip。落后的 update pin 或分叉会提前失败。
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

## 3. 验收、恢复和交接

- 检查全部必需 job 与 staging 结果。stage 固定本次 run 的 artifact ID、归档摘要和实际字节。正式运行下载同一份 staging bundle，复核 SHA256SUMS，创建或回读 tag，再通过 draft 上传、逐项核验摘要、公开并回读。公开状态、目标 SHA 和实际附件清单必须一致。
- 暂时故障优先在同一 run 重跑失败 job。发布步骤中断时优先重跑 publish，使用已验证的 bundle。现有 draft 只补缺失的文件，若已有文件摘要不一致则停下调查。记录 queue、实际 job 时间和阶段间等待，按冻结至公开回读的墙钟时间衡量优化。
- 需要改代码、pin 或工具时重新冻结并构建。若正式 tag 已指向不同提交，使用新版本。旧 tag 的历史恢复入口为 Release (tagged recovery)，按对应提交的工作流契约和产物来源恢复。
- 交接记录应包含版本、tag 剥离后的完整 SHA、Release URL、公开资产和校验和、Apple SHA、Libbox 摘要、run ID/attempt、回读时间。主仓完成后把交接交给独立的 [Mac TF 任务](TESTFLIGHT-MAC.md)。Satelite 后续按已发布 core SHA 对齐自身依赖、`versionName`、递增 `versionCode`、`coreCommit`，核对双 ABI APK、签名和校验和。
