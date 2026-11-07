# v0.1.44 正式发布交接记录（2026-11-08）

按 [Release 指引](../RELEASE-WINDOWS.md) 分别验收内核与 Satelite。下列内核摘要来自公开 GitHub Release API；工作流已校验产物与清单，本机未重复下载附件复算。此记录可供独立的 [TF 指引](../TESTFLIGHT-MAC.md) 使用，真机行为仍需设备验收。

## 内核发布身份

| 项 | 值 |
| --- | --- |
| 版本 / tag | `0.1.44` / `v0.1.44`（附注标签对象 `e6a718553ca10b907478bc7457d618505c1bad4d`） |
| tag 剥离后的完整 SHA | `3b6ec64f7145a2add99656d47caaa4f22a1cc478` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.44 |
| 状态 | 公开、非 draft、非 prerelease；正文为空 |
| 运行 | [Kernel release 42391992312](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391992312)，attempt 1，`2026-11-07T16:34:41Z` → `2026-11-07T16:54:39Z` |
| 公开时间 | `2026-11-07T16:54:35Z`（2026-11-08 00:54:35 Asia/Shanghai） |
| 发布前门禁 | [Verify 42391024793](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391024793)、[Release contract 42391024692](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391024692)、[preparation 42391062041](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391062041) 均在最终 SHA 成功 |

正式运行的 freeze、contract、各平台构建、Verify（含 race）、stage 和 publish 全部成功。首轮在旧提交 `58fb5d293` 上的正式运行 `42390257395` 因契约测试 SIGPIPE 误报而取消，未进入 stage/publish，也未创建本版 tag；修复检查器后重新冻结和完整构建。另见 [CI/Release 模块](../../KNOWLEDGE/modules/ci-release.md)。

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

客户端 `version.properties` 已设置 `versionName=0.1.44`、`versionCode=55`、`coreCommit=3b6ec64f7145a2add99656d47caaa4f22a1cc478`；客户端提交 `22ba907d62c23f09272117134a04e3bc96962520` 与 `v0.1.44` tag 已推送。[Release APK 42393794665](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/42393794665) 是该 tag push 触发。签名 APK、公开附件及整轮完成状态须在该运行结束后回读。
