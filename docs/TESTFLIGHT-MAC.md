# TF 构建指引

在 Mac 独立执行。开始条件是指定的内核仓库正式 Release 已公开、全部契约资产已回读验收，并收到 [Windows 交接记录](RELEASE-WINDOWS.md)。Satelite 可在其自己的时间发布。

0.1.45 的知识库专题 SHA 只是文档校准点。TF 使用**已公开 Release 的 tag 剥离 SHA**、本次发布的 Libbox manifest 和其中记录的 Apple 客户端 SHA；不要从当前 `testing` 或客户端 `main` 重新选源码。[Apple 构建技术说明](APPLE-BETA-RELEASE.md)中的提前 `--follow-run` 是历史/诊断模式，本轮正式版使用下面的 Release 来源入口。

## 1. 固定版本与已公开 Libbox

根据交接记录选择 tag、完整 core SHA、Apple SHA 和 Libbox 摘要。用 GitHub 回读指定版本的发布状态、正文及附件清单，确认非 draft、非 prerelease，核对 `SHA256SUMS` 中的 Libbox 摘要与交接一致。回看对应内核 `Kernel release` 的 freeze、Verify、Release contract、Apple、stage、publish 结果；其中 source lock、pin-runs 与知识库事实门禁属于发布候选证据，不可用当前分支上的新运行代替。当前版本和构建来源从目标 tag 和已发布的 Libbox manifest 读取。

在已认证的 Mac shell 运行。替换交接版本与 SHA；用干净的独立检出。

```bash
repo=Piggy-Cat-bit-shadow/JiejieBox
tag=vX.Y.Z
expected_sha=替换为交接中的完整SHA
gh release view "$tag" --repo "$repo" --json isDraft,isPrerelease,url,body,assets
git fetch origin "refs/tags/$tag:refs/tags/$tag"
test "$(git rev-parse "$tag^{commit}")" = "$expected_sha" || exit 1
git switch --detach "$tag"
git submodule update --init --recursive
test -z "$(git status --porcelain)" || exit 1
```

检查 Xcode、签名证书、profile、Team、Bundle ID、App Group 与 App Store Connect 账号和构建号。签名相关设置见 [Apple 构建与签名说明](APPLE-BETA-RELEASE.md)。归档前确认 `CFBundleShortVersionString` 对齐内核正式版本，`CFBundleVersion` 在 Apple 对应平台可用。

## 2. 在 Mac 签名、归档和上传

```bash
./scripts/publish-apple-beta.sh --libbox-source release
```

该入口默认构建两个平台，下载指定 Release 的 Libbox，验证 core SHA、版本、摘要、slices 以及 manifest 的 `build_tags`（必须包含 `with_trusttunnel`），从 manifest 读取 Apple 源码并把同一份已发布库装入两个客户端。仅需一个平台时，在包含该参数的发布提交上加 `--platform ios` 或 `--platform macos`；单平台选项要求 `--libbox-source release`。旧 tag 按该 tag 的脚本能力处理。若 Release 资产缺失或校验失败，先停止并回查内核发布，不切换到另一次 Actions Libbox。**没有 `build_tags` 字段的旧 Libbox 会被拒绝**：v0.1.46 的已发布 Libbox 在 mobile tag 集缺少 `with_trusttunnel` 时构建，不能用于 TrustTunnel；必须使用从修复后内核重新发布、manifest 中带有 `with_trusttunnel` 的 Libbox。

在 App Store Connect 分别确认上传、处理、测试组分配与测试者可用状态。记录 Release URL、core 和 Apple SHA、Libbox 摘要、平台、版本、构建号、App Store Connect 处理结果和测试组可见时间；Actions 通过或上传命令退出成功不等于测试者可用。上传超时先查询 Apple 是否已接收再决定重试。若只有一个平台失败，用已发布的相同来源恢复对应平台，并确认新构建号不会与已接收的构建冲突。
