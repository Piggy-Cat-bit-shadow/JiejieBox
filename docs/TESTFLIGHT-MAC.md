# TF 构建指引

在 Mac 独立执行。开始条件是指定的内核仓库正式 Release 已公开、全部契约资产已回读验收，并收到 [Windows 交接记录](RELEASE-WINDOWS.md)。Satelite 可在其自己的时间发布。

## 1. 固定版本与已公开 Libbox

根据交接记录选择 tag、完整 core SHA、Apple SHA 和 Libbox 摘要。用 GitHub 回读指定版本的发布状态和附件清单，核对 `SHA256SUMS` 中的 Libbox 摘要与交接一致。当前版本和构建来源从目标 tag 和已发布的 Libbox manifest 读取。

在已认证的 Mac shell 运行。替换交接版本与 SHA；用干净的独立检出。

```bash
repo=Piggy-Cat-bit-shadow/JiejieBox
tag=vX.Y.Z
expected_sha=替换为交接中的完整SHA
gh release view "$tag" --repo "$repo" --json isDraft,isPrerelease,url,assets
git fetch origin "refs/tags/$tag:refs/tags/$tag"
test "$(git rev-parse "$tag^{commit}")" = "$expected_sha" || exit 1
git switch --detach "$tag"
```

检查 Xcode、签名证书、profile、Team、Bundle ID、App Group 与 App Store Connect 账号和构建号。签名相关设置见 [Apple 构建与签名说明](APPLE-BETA-RELEASE.md)。归档前确认 `CFBundleShortVersionString` 对齐内核正式版本，`CFBundleVersion` 在 Apple 对应平台可用。

## 2. 在 Mac 签名、归档和上传

```bash
./scripts/publish-apple-beta.sh --libbox-source release
```

该入口默认构建两个平台，下载指定 Release 的 Libbox，验证 core SHA、版本、摘要和 slices，从 manifest 读取 Apple 源码并把同一份已发布库装入两个客户端。仅需一个平台时，在包含该参数的发布提交上加 `--platform ios` 或 `--platform macos`。该选项继续消费已发布的库。旧 tag 按该 tag 的脚本能力处理。

在 App Store Connect 分别确认上传、处理、测试组分配与测试者可用状态。记录 Release URL、core 和 Apple SHA、Libbox 摘要、平台、版本、构建号和处理结果。上传超时先查询 Apple 是否已接收再决定重试。若只有一个平台失败，用已发布的相同来源恢复对应平台。
