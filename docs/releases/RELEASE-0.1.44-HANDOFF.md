# v0.1.44 正式发布交接记录（2026-11-08）

按 [Release 指引](../RELEASE-WINDOWS.md) 分别验收内核与 Satelite，整轮均已公开。下列附件摘要来自各自公开 GitHub Release API；工作流已校验产物与清单，本机未重复下载附件复算。此记录可供独立的 [TF 指引](../TESTFLIGHT-MAC.md) 使用，真机行为仍需设备验收。

## 内核发布身份

| 项 | 值 |
| --- | --- |
| 版本 / tag | `0.1.44` / `v0.1.44`（附注标签对象 `e6a718553ca10b907478bc7457d618505c1bad4d`） |
| tag 剥离后的完整 SHA | `3b6ec64f7145a2add99656d47caaa4f22a1cc478` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.44 |
| 状态 | 公开、非 draft、非 prerelease；初次公开时正文为空，`2026-11-07T17:32:59Z`（2026-11-08 01:32:59 Asia/Shanghai）补写本版说明，当前正文非空 |
| 运行 | [Kernel release 42391992312](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391992312)，attempt 1，`2026-11-07T16:34:41Z` → `2026-11-07T16:54:39Z` |
| 公开时间 | `2026-11-07T16:54:35Z`（2026-11-08 00:54:35 Asia/Shanghai） |
| 发布前门禁 | [Verify 42391024793](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391024793)、[Release contract 42391024692](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391024692)、[preparation 42391062041](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391062041) 均在最终 SHA 成功 |

正式运行的 freeze、contract、各平台构建、Verify（含 race）、stage 和 publish 全部成功。首轮在旧提交 `58fb5d293` 上的正式运行 `42390257395` 因契约测试 SIGPIPE 误报而取消，未进入 stage/publish，也未创建本版 tag；修复检查器后重新冻结和完整构建。另见 [CI/Release 模块](../../KNOWLEDGE/modules/ci-release.md)。

发布后的独立文档提交将本版内容写入 `CHANGELOG.md`，但自动创建的 GitHub Release 正文仍为空。收到用户反馈后，使用同版日志内容补写 [公开 Release 页面](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.44)；API 回读正文为 1,518 字符，8 项附件、tag 目标及公开状态未变。这是发布后文字编辑，不是重新构建或重发资产。

## 公开资产（8 项）

| 资产 | 字节 | GitHub API 的 SHA256 |
| --- | ---: | --- |
| `jiejie-sing-box-linux-amd64-v0.1.44.tar.gz` | 28,290,229 | `2132ea2f59c8aec0047613fa0dc49a18aea660813f9388ca19854acdecfc29cd` |
| `jiejie-sing-box-macos-arm64-v0.1.44.tar.gz` | 30,201,305 | `05b5f697c104eebb07bde4190719327bfb1874b0cce066fa9373bfe1ac3b134b` |
| `jiejie-sing-box-windows-amd64-v0.1.44.tar.gz` | 28,629,115 | `e07ddbbbd916e66579c8bab58bb38cccc6b6a8ff17712009c82e639328de75dc` |
| `jiejie-sing-box-android-arm64-v0.1.44.tar.gz` | 29,295,573 | `7cae2e28e2176d46045376fc6a0230ce3a0fda8735829ffe58b3b0d158486b07` |
| `jiejie-libbox-apple-0.1.44.tar.gz` | 72,575,994 | `d1c554f4c4f48d308913e43db60d1faffa2191e4bed96fed4f9ba36352a130b7` |
| `Jiejiebox-v0.1.44-windows-x64.exe` | 116,244,368 | `ac5abc0635bb33b4b7464d4de852ea2724c0e1e61cdb1766e386db9f8aebd607` |
| `jiejie-ios-selfsign-unsigned-0.1.44.ipa` | 44,802,342 | `f2ce611ba92646c1f6bf15ec02e30e9671ac21f0a755cf03d8ed8067f3e6011e` |
| `SHA256SUMS` | 746 | `02b6ee502e54d7c79564766a1ec985912defcab381701da15db519cc4c07354c` |

工作流 stage 与 publish 校验了同一批发布附件；本表的摘要是公开 API 元数据，不标成独立本地验包。

## Apple 与客户端来源

| 项 | 值 |
| --- | --- |
| Apple gitlink / 构建源码 | `e3283104c984d60cdf2a0250b47b7fc446e71ba6` |
| Apple Libbox 内部归档摘要 | `Libbox.xcframework.tar.gz`：`49808a53b3a56168295729e83ad4067410035a6d06b8c474716eafd39bd32e58`（运行日志） |
| Libbox slices | `ios-arm64`、`macos-arm64`（运行日志） |
| Android / Desktop gitlink | `c7837107dc0126fb704fdf08cf203e10b30c8a22` / `41722b030ea481523290d932a1d81177f8c13f32` |

TF 按内核 tag、完整 SHA 与公开 Apple Libbox 获取固定来源；不在 Mac 上重新解析移动的 Apple `main`。构建通过不代表签名后的真机 Network Extension 路由、reload 或本地签名回环已运行。

## Satelite 安卓客户端

| 项 | 值 |
| --- | --- |
| 版本 | `0.1.44`；`versionCode=55`（上一公开版为 54） |
| tag | `v0.1.44`（附注标签对象 `6f0e93d23f8d5df5c1e6c52318ab4e2abb8bfc61`） |
| tag 剥离后的客户端 SHA | `22ba907d62c23f09272117134a04e3bc96962520`，与 `main` 一致 |
| 固定内核 SHA | `3b6ec64f7145a2add99656d47caaa4f22a1cc478`，与内核正式 tag 一致 |
| Release | https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.44 |
| 运行 | [Release APK 42393794665](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/42393794665)，tag push 触发，attempt 1；构建与发布作业均成功 |
| 公开时间与状态 | `2026-11-07T17:16:03Z`（2026-11-08 01:16:03 Asia/Shanghai）；非 draft、非 prerelease |

| 资产 | 字节 | GitHub API 的 SHA256 |
| --- | ---: | --- |
| `satelite-one-0.1.44-arm64-v8a-signed-v0.1.44.apk` | 30,618,603 | `8eb20edb44080cb8eb05d611a065cad3ad8840fefc930a1669c51e73da9b38ee` |
| `satelite-one-0.1.44-x86_64-signed-v0.1.44.apk` | 32,594,522 | `fc4af6aef71319c6dc10acd69c7457e9698d4beb6d8592e955fa36c31bdb7047` |
| `SHA256SUMS.txt` | 227 | `b83357f4a7400d533f64948d478a2fafd9657d0f6f0b7302802ed35e0029e6dd` |
| `build-info.json` | 1,352 | `066b346ae441a0fb08bfce3099af06112f2324a5a78ad03d316044ace0fbe54a` |
| `apksigner-verify.txt` | 2,844 | `3394627b1680feb43d5ce6f6ba163850d8a558b0977996ead2504dbefd0aba7d` |

构建日志显示 `core_commit=3b6ec64f...`、`signature_state=signed-by-user`，`bundle OK: 2 APK(s)`；两个 APK 的清单校验均为 `OK`，各自打包的 ABI 引擎摘要与构建记录一致。签名和身份验证属于该 CI 运行的证据，本机未再次下载附件或调用 `apksigner`。至此内核与 Satelite 的正式附件都已验收；Mac TestFlight 仍是独立流程，真机行为仍未在本轮验证。
