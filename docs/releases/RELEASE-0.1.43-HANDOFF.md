# v0.1.43 内核正式发布交接记录（2026-11-07）

本记录由 Windows 正式版任务生成，供 [TF 构建指引](../TESTFLIGHT-MAC.md) 消费；Satelite 安卓客户端
已于本轮完成，记录见下文。表中的摘要来自公开 Release 实际字节的本地回读，不是构建日志的转述。

## 发布身份

| 项 | 值 |
| --- | --- |
| 版本 | `0.1.43` |
| tag | `v0.1.43`（附注标签对象 `67aad85adb90c38956c5d8097b3e46ed76b61839`） |
| tag 剥离后的完整 SHA | `0d63a7a16dd488d952b4d899ebc1a2542cfb7532` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.43 |
| 状态 | 公开、非 draft、非 prerelease；发布时正文为空（0 字节）；`2026-11-07T06:06:38Z` 由独立更新日志任务补充发布说明（见下） |
| 运行 | Kernel release `42343308769` attempt 1，`2026-11-07T05:34:32Z` → `2026-11-07T05:59:26Z`（24 分 54 秒） |
| 公开时间 | `2026-11-07T05:59:22Z`（13:59:22 Asia/Shanghai） |
| 回读时间 | `2026-11-07T06:05:20Z`（14:05:20 Asia/Shanghai） |

`v0.1.43` 是 0.1.42 之后第一个公开正式版，发布内容包含 Outbound Provider 功能及其两轮审计修复。
本轮依赖 pin 与 0.1.42 一致，`release-freeze.py resolve/check` 报告无 pin 需要移动，未运行
Release preparation；`release/source-lock.json` 仅更新 `release_version`。

## 源码链

| 提交 | 内容 |
| --- | --- |
| `10296629c` | `feat(provider): port outbound providers and wire them into groups` |
| `3d8c33580` | `fix(provider): harden untrusted input, downloads, cache and file watch` |
| `08bb7d67d` | `fix(provider): close the remaining lifecycle and cache gaps` |
| `cd8c61e12` | `docs: refresh outbound provider knowledge and configuration [skip ci]` |
| `0d63a7a16` | `release(0.1.43): cut 0.1.43`（版本文件、source lock 版本；tag 目标） |

## 公开资产（8 项）

全部 8 个附件已下载到本地并逐个计算 SHA256：7 个二进制与 `SHA256SUMS` 一致，`SHA256SUMS`
自身摘要与 Release 记录（`BUNDLE_HASHES`）一致；GitHub API 记录的每个附件 sha256 也与清单一致。

| 资产 | 字节 | SHA256 |
| --- | --- | --- |
| `jiejie-sing-box-linux-amd64-v0.1.43.tar.gz` | 28,255,147 | `35c9c354243d542d8e87cb881a79db6b010bdc41073947dbbe081604de403a2f` |
| `jiejie-sing-box-macos-arm64-v0.1.43.tar.gz` | 30,167,700 | `8a3c68996fb2b64a137ae146efc6b483ddeb97d258dbffa359f7ad8561678ed2` |
| `jiejie-sing-box-windows-amd64-v0.1.43.tar.gz` | 28,596,579 | `94e760ac9d8ed315b0374075ebef470d54048d33b202f6832a25a5db1baa9d3f` |
| `jiejie-sing-box-android-arm64-v0.1.43.tar.gz` | 29,261,972 | `5f6eb71069596802297a6c6b0545a3a12683b0be2564768573f0bd8d80898489` |
| `jiejie-libbox-apple-0.1.43.tar.gz` | 72,514,026 | `d2e07b8edd5d7fcc18431967a546128dee6de2340df0cb6325ce6088cf688cb5` |
| `Jiejiebox-v0.1.43-windows-x64.exe` | 116,173,328 | `0256b543dbfed89c5e76fb9c95f008f6ac6a153dcdccb81d4a07145833014435` |
| `jiejie-ios-selfsign-unsigned-0.1.43.ipa` | 44,768,901 | `4dd49c80b1874211f673b2c85bde7cfdb1492ec64db3f39176a33dcb23a7f03d` |
| `SHA256SUMS` | 746 | `d31b1e6dd22fd3615577b5f846dcdd101abe4b4d7f39f61eee4a69115f46a8ea` |

## Apple 交接

| 项 | 值 |
| --- | --- |
| Apple 客户端 SHA | `0c7b10be05811cdd3f31a48a7cbe49f061852084`（`sing-box-for-apple` 的 `main`） |
| Libbox 版本 | `0.1.43+jiejie.0d63a7a` |
| Libbox manifest `artifact_sha256` | `e820577ab9ff2ce2e61b10fe4654993a9e39a666d233f06c9d96d435dedddef8` |
| slices | `ios-arm64`、`macos-arm64` |
| 构建环境 | Xcode 26.6、Go 1.25.5；run `42343308769` attempt 1，`2026-11-07T05:44:16Z` |
| 公开载体 | `jiejie-libbox-apple-0.1.43.tar.gz`（含 `Libbox.xcframework.tar.gz`、`manifest.json`、`SHA256SUMS-LIBBOX.txt`、`BUILD-INFO-LIBBOX.txt`） |

`Libbox.xcframework.tar.gz` 已从发布资产解出，本地复核摘要等于 manifest 的
`artifact_sha256`。TF 任务按 [TF 构建指引](../TESTFLIGHT-MAC.md) §1 用该 tag、完整 SHA 和已发布
Libbox；Apple 源从 Manifest 读取，不要在 Mac 重新解析 `main`。

## Satelite 安卓客户端（整轮 Release 完成）

| 项 | 值 |
| --- | --- |
| 版本 | `0.1.43`（`versionCode=54`，上一发布为 53） |
| tag | `v0.1.43`（附注标签对象 `b09508141698ddfa200f7d68b63fb74d1c890eff`） |
| tag 剥离后的客户端 SHA | `b52c438a8ba2d6b7c58367d61e2ff16f36391346` |
| 内核固定点 | `0d63a7a16dd488d952b4d899ebc1a2542cfb7532`（`Piggy-Cat-bit-shadow/sing-box` 的 `v0.1.43`） |
| Release | https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.43 |
| 运行 | Release APK `42346052629` attempt 1，`2026-11-07T06:13:26Z` → `2026-11-07T06:40:29Z`（build 26 分 27 秒，publish 18 秒） |
| 状态 | 公开、非 draft、非 prerelease；正文 298 字节（工作流按提交生成的更新摘要） |
| 公开时间 | `2026-11-07T06:40:29Z`（14:40:29 Asia/Shanghai） |

| 资产 | 字节 | SHA256 |
| --- | --- | --- |
| `satelite-one-0.1.43-arm64-v8a-signed-v0.1.43.apk` | 30,587,343 | `32e6e4921a0bd08446171ae9101a38dc59e8fd5247ed47b10c4103dd84e7ce17` |
| `satelite-one-0.1.43-x86_64-signed-v0.1.43.apk` | 32,560,906 | `915f4a844b9a72fe985178a3a2e9961fc9d779dd456f78b4449ecde650252c69` |
| `SHA256SUMS.txt` | 227 | `55f64515f8813dcecdc70f9b26f6cb7bfabad34921254f691ec1d87dab99a353` |
| `build-info.json` | 1,352 | `6030f909544d921e857cd68327826477b28fc82313901ddc63fe8ff80409488e` |
| `apksigner-verify.txt` | 2,844 | `3394627b1680feb43d5ce6f6ba163850d8a558b0977996ead2504dbefd0aba7d` |

验收结果：

- 两个 APK 的本地 SHA256 与 `SHA256SUMS.txt`、`build-info.json` 的 `apk_*_sha256` 全部一致。
- `build-info.json`：`app_version=0.1.43`、`app_version_code=54`、`app_commit=b52c438a8`、`core_commit=0d63a7a16dd488d952b4d899ebc1a2542cfb7532`、`core_aar_sha256=382333b3fcb95e06fe4c9560c9b0121f2700d21e45db753f80b0224682e17122`（121,842,246 字节）、`run_id=42346052629`、`signature_state=signed-by-user`。
- ABI：arm64 APK 仅含 `lib/arm64-v8a/*`，x86_64 APK 仅含 `lib/x86_64/*`；两者 `libbox.so` 的本地摘要分别等于 `core_engine_sha256.arm64-v8a=77e7ed01…` 与 `.x86_64=7cb19d57…`。
- 签名：流水线 `apksigner verify` 实测两个 APK 均 `Verifies`，APK Signature Scheme v2 为 true，单签名者，证书 `CN=satelite-one, OU=Release Signing, O=satelite-one`（非 debug），RSA 4096，证书 SHA-256 `dfebfb1d07cce60c7f64d277f985f9485823f32286388be40760d60409d8718d`。本机无 Android SDK/apksigner，签名复核采用流水线实测记录（`VERIFIED_CI`）；SHA256、ABI 与引擎摘要为本地实测（`VERIFIED_STATIC`）。

## 验收结果

- 运行 `42343308769` 全部 16 个 job 均为 `success`：freeze、verify（含 race）、contract、Linux、
  macOS CLI、Windows core、Windows 安装器、Android AAR、Apple（resolve、libbox、macOS、iOS、
  自签 IPA）、stage、publish。
- tag 剥离后 SHA 等于本轮候选 `0d63a7a16`，tag 内 `release/JIEJIE_VERSION` 为 `0.1.43`。
- Release 在发布回读时正文为 0 字节；附件 8 项与 `SHA256SUMS` 的 7 条二进制逐条一致，无缺失或
  多余条目。
- 发布回读后，独立更新日志任务 `b8b37650c`（`docs: add v0.1.43 and v0.1.42 release notes
  [skip ci]`）在 `CHANGELOG.md` 增加本版说明，并在 `2026-11-07T06:06:38Z` 把同等内容写入
  Release 正文（现为 1,263 字节，作者为发布机器人账号）。这是发布后的编辑行为，不影响 tag、
  附件与校验和。

## 后续任务

- **Mac TF**：按 [TF 构建指引](../TESTFLIGHT-MAC.md) 独立执行；开始条件是本记录及上面的
  回读结果。
- **Satelite**：已完成。双 ABI APK、签名与校验和均已验收（见上一节），整轮 Release 状态为
  「内核与 Satelite 均完成」。
