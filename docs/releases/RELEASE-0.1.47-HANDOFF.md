# v0.1.47 发布交接

## 固定身份

| 项 | 值 |
| --- | --- |
| 版本 | `0.1.47` |
| tag | `v0.1.47` |
| tag 剥离提交 | `c50336676eb90e03792c81284c736c1a152ff684` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.47 |
| Kernel release | [43519793788](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43519793788) |
| Apple 源码 pin | `aa8e16aa21a7f28aefc944fac76086c144195e3b` |
| Libbox | `jiejie-libbox-apple-0.1.47.tar.gz`，74,274,719 bytes |
| Libbox slices | `ios-arm64`, `macos-arm64` |
| Libbox build tags | `with_trusttunnel`, `with_awg` 均存在 |

Libbox manifest 的 `parent_sha` 为 `c50336676eb90e03792c81284c736c1a152ff684`，`jj_version` 为 `0.1.47`；构建标签包含 `with_quic,with_wireguard,with_utls,with_naive_outbound,with_xhttp,with_trusttunnel,with_awg,...`。

## 公开附件

共 8 项：

| 附件 | SHA256 |
| --- | --- |
| `Jiejiebox-v0.1.47-windows-x64.exe` | `255ae2f6b9f1e856528d46b3e120dbd313c655f0803ce45cfade9d0014baac16` |
| `jiejie-ios-selfsign-unsigned-0.1.47.ipa` | `db3d17da807d88af1f1e88ad8140e4ae937adbfdb22633e4eac570aa63bb060b` |
| `jiejie-libbox-apple-0.1.47.tar.gz` | `c46425f742357fb3f522ca5b3ccc9b6dc6070644c1a65d893463779957e204f8` |
| `jiejie-sing-box-android-arm64-v0.1.47.tar.gz` | `29597bf6b68ae81a24e8afa4321e25db414779b3f566533e0cd776bbe5ac2b31` |
| `jiejie-sing-box-linux-amd64-v0.1.47.tar.gz` | `ff68689509278379e0a4d14730d9fc6c57b1ac68539b88d3acf2b24d95135b2a` |
| `jiejie-sing-box-macos-arm64-v0.1.47.tar.gz` | `a580d1fd63c37ba9ace52d867c1246c8f88440c5e8bc48312992326d1925792e` |
| `jiejie-sing-box-windows-amd64-v0.1.47.tar.gz` | `e02d9ffb1b5e3611f4932f2d173cc527d55a596babfcdb70620d0fdfc5779dd8` |
| `SHA256SUMS` | 资产校验和清单，746 bytes |

`SHA256SUMS` 本身未在清单内自校验；上表各行来自该附件。

## Satelite Android

| 项 | 值 |
| --- | --- |
| 版本 / versionCode | `0.1.47` / `58` |
| tag | `v0.1.47` |
| 客户端提交 | `c33cd66846f3e09e4fe4b483232c2281591b8bb6` |
| coreCommit | `c50336676eb90e03792c81284c736c1a152ff684` |
| Release | https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.47 |
| Release APK run | [43526300534](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/43526300534) |

公开附件共 5 项：`arm64-v8a` 与 `x86_64` 两个签名 APK、`SHA256SUMS.txt`、`build-info.json`、`apksigner-verify.txt`。`build-info.json` 记录 `signature_state=signed-by-user`，`app_ref=v0.1.47`，`core_ref=pinned`；`apksigner verify` 报告两个 APK 均 SIGNED，v2 签名，证书 `CN=satelite-one`。

APK SHA256：

- `7c4e2f108aae66e126a6342053fe7b05efea3e80c2783e69e64a7b2b4f0d6b31` — `satelite-one-0.1.47-arm64-v8a-signed-v0.1.47.apk`
- `acc91aa943db391ff21b38a326aae1e1497e20b065745f837c255ffcf61a8aa0` — `satelite-one-0.1.47-x86_64-signed-v0.1.47.apk`

## 验证

`Kernel release 43519793788` 的以下作业全部成功：

- `freeze`
- `contract / release helper tests`
- `verify / verify`
- `verify / race checks`
- `linux / linux-amd64`
- `macos / macos-arm64`
- `windows / sing-box.exe (windows/amd64)`
- `desktop / unsigned-build / signed-test / install-lifecycle`
- `android / Android ARM64 core`
- `apple / resolve the Apple client source`
- `apple / libbox`
- `apple / ios`
- `apple / macos`
- `apple / ios-selfsign`
- `stage`
- `publish`

公开页面回读：非 draft、非 prerelease、8 项附件、正文已写入。

## 失败与恢复记录

1. 首次 `Kernel release 43471548360` 在 `macos-latest` 排队约 15 分钟后 `macos` 与 `apple / libbox` 被取消；无 runner 分配，未创建 tag 或 Release。
2. 第二次 `43475873935` 同样在 15 分钟后被取消。
3. 第三次 `43515885484` 成功拿到 macOS runner，但 macOS 单测失败：`TestHalfCloseRefusedConfigurationIsSkipped` 在 `newHalfCloseFixture` 启动 copy goroutine 后才设置 `configErr`，测试初始化与该异步路径竞态。
4. 修复 `c503366`：新增 `newHalfCloseFixtureWithConfigErr`，在 `manager.NewConnection` 之前设置配置错误，并断言 `Pending == 0`。随后 `43519793788` 全绿并发布。

## 未完成

- TestFlight 上传、签名、真机验收：未执行，按 [TF 构建指引](../TESTFLIGHT-MAC.md) 独立进行。
