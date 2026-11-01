# 从 Windows 构建，到 Mac 发布 TestFlight

开发和发起 Actions 在 Windows 上完成；证书、签名、归档和 TestFlight 上传留在 Mac。
Mac 跟随指定的 Apple client 运行，自动使用那次运行的版本、核心提交、Apple 客户端提交和 Libbox。
它不读取“最新 Release”来猜版本，也不要求本机分支恰好同步到发布提交。

## 推荐流程

### 1. 在 Windows 确定发布版本并启动构建

先完成版本修改和依赖冻结，再对确定的候选提交启动 Apple client。版本号的唯一来源是这个提交中的
`release/JIEJIE_VERSION`；例如文件为 `0.1.36`，正式标签就是 `v0.1.36`。

如果使用 Release preparation，应先取得它生成的最终候选提交，再构建该提交。不能先构建旧提交，
随后又让 preparation 修改依赖、生成另一个提交，最后期望两个版本自动对齐。

Apple client 的源码解析任务会立即上传 `jiejiebox-apple-release-<核心 SHA>`，记录：

- 发布版本、预期标签、核心提交和仓库；
- 本次运行实际选定的 Apple 客户端提交、仓库和分支；
- Actions 运行 ID、重跑次数，以及启用的平台。

任务摘要中会显示 Mac 命令，以及正式 Release 应填写的运行 ID 和重跑次数。
iOS 与 macOS 的 Swift 源码来自同一个已解析提交；不同设备展示哪套界面，由客户端运行时决定。

### 2. 在 Mac 启动跟随脚本

首次使用前，更新 Mac 上的工具脚本，并完成 `gh auth login` 和本机 Apple 签名配置。
随后把 `123456789` 换成本次 Apple client 的运行 ID：

```sh
./scripts/publish-apple-beta.sh --follow-run 123456789 --platform ios
```

也可以选择 `--platform macos` 或 `--platform both`。省略平台时默认仅发布 iOS；Actions 必须启用
所选平台。跟随模式不会替你触发或重跑 Actions。

可以在 Windows 刚启动任务时就在 Mac 执行命令。脚本依次执行：

1. 等待本次运行的发布信息，固定核心 SHA、Apple SHA 和版本号。
2. 在 `build/apple-follow/` 下建立独立的 Git worktree，准备对应的客户端源码；原工作目录不切分支。
3. 等待并按不可变的 artifact ID 下载 Libbox，检查版本、核心 SHA、运行 ID、重跑次数和校验和。
4. 使用本次固定的 Apple 客户端源码和预编译 Libbox 开始签名归档，和云端的客户端检查并行进行。
5. 上传前等待所选平台的 CI 通过，并确认远端正式标签指向同一个核心提交，然后上传 App Store Connect。

iOS 上传只等待 Libbox 和 iOS 验证，不等待 macOS、Windows、Linux 或正式 Release 页面。
macOS 单运行器模式会在库完成后先上传 Libbox，再继续构建 macOS 应用；Mac 可以先归档，上传仍需
等待该次 macOS 验证成功。缺少标签时会等待；标签已存在但指向其他提交、所需任务失败或跳过时立即停止。

默认每个等待阶段最多 90 分钟，每 30 秒检查一次；可用 `--timeout 7200 --interval 30` 调整。
跟随期间如果对同一个运行执行了 Re-run，重跑次数会变化，脚本会停止，避免混用两次构建。
需要跟随新的重跑时，重新执行命令。

本机的 `.env.apple.local` 会复制到独立工作目录中，并保持仅本机可读；它不会上传到 Actions。
文件中引用本机证书或其他文件时，应使用绝对路径。成功记录和工作目录会保留，便于排查；不要提交
`build/` 或其中的签名配置。同一运行及重跑次数只能有一个跟随进程，避免同时上传重复构建。
若进程被强制终止，确认进程已结束后，才可删除日志提示的遗留 `.lock` 目录。

### 3. 在 Windows 发布正式 Release

各平台构建通过后，运行 Release，填写：

| 输入 | 取值 |
| --- | --- |
| `tag` | 本次正式标签，例如 `v0.1.36` |
| `apple_run_id` | Mac 跟随的 Apple client 运行 ID |
| `apple_run_attempt` | 该次运行的重跑次数，首次运行是 `1` |

这两个 Apple 输入会锁定正式 Release 的产物来源。即使相同核心提交后来又有新的 Apple 构建，Release
也不会悄悄改选它。选错运行、重跑次数、核心提交、版本或工作流，发布会失败。
带有新发布信息的 Apple 产物必须填写这些输入；历史产物仍支持原来的发布路径。

正式 Release 将本次 Libbox 连同 manifest、校验和与构建记录一起打包为
`jiejie-libbox-apple-<版本>.tar.gz`，不会重新编译。Mac 使用的内层 Libbox 文件与正式 Release
中的文件来自同一次构建；外层发布包有独立的校验和。

## 版本号与构建号

App 的 `CFBundleShortVersionString` 取自固定提交中的 `release/JIEJIE_VERSION`，与正式标签去掉
`v` 后的版本一致。脚本会检查归档后的主应用和各个 `.appex`，防止项目中的硬编码版本覆盖发布版本。

`CFBundleVersion` 是 Apple 的构建号，用于区分同一个版本的多次上传。默认沿用现有脚本的 UTC 时间
生成方式，也可事先设置 `APPLE_BUILD_NUMBER`。一次 `--platform both` 使用同一个构建号。
分别执行两次平台发布时，若希望构建号也相同，需要显式设置同一个 `APPLE_BUILD_NUMBER`。
发布版本相同，不要求 TF 构建号与 GitHub Release 标签相同。

## 已发布版本的原有入口

正式 Release 已存在时，仍可以在对应提交运行：

```sh
./scripts/publish-apple-beta.sh --libbox-source release
```

默认 `auto` 优先读取该版本的正式 Release；资产不存在时才回退到 Actions。资产存在却损坏时直接失败，
不会悄悄换成另一份库。新产物还会把 Apple 客户端源码固定到 manifest 记录的提交，避免重发旧 Release
时解析到今天的分支头。这个入口会发布两个平台；需要提前发布或只发布一个平台时，使用 `--follow-run`。

原有 `--run-id` 入口仍要求整个 Apple 工作流和三个必要任务成功。同一提交已经有任务排队或运行时，
现在会复用并等待该任务，避免重复触发。`--no-dispatch` 可禁止原有入口自动触发构建。

## 检查范围

`test-apple-release.py` 使用离线 fixture 验证提前归档、平台门禁、版本校验、标签与提交匹配、
重跑拒绝和产物身份；`test-release-artifacts.sh` 验证正式 Release 的固定运行选择。
它们不证明真实 Mac 上的签名、Xcode 归档、App Store Connect 接收或设备运行。

相关说明：[开发签名](APPLE-DEVELOPMENT-SIGNING.md)、[底层发布脚本](../scripts/release-apple.sh)。
