# v0.1.55 发布交接

- 版本：`0.1.55`
- 分支：`testing`
- 候选提交：`70e9109e91660c8f04991d10f0ea66252aa85300`
- tag：`v0.1.55`（注释 tag 对象 `8b8150532cee9946e7c464ab7f7248621f64dd44`，剥离后指向候选提交）
- 公开时间：2026-11-20T16:39:29Z（Asia/Shanghai 2026-11-21 00:39）
- Release：<https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.55>
- Release 正文：已按 `CHANGELOG.md` 的 v0.1.55 节写入并回读（非空，5735 字符），正文补写与初次公开同一次操作完成。

## 门禁证据（同一候选 SHA）

| 工作流 | run | 结果 |
|---|---|---|
| Verify（含 pin-runs、知识库事实、ABI pin、e2e、build matrix、cronet provenance、race checks） | 44756301538 | success |
| Release contract verification（含 desktop provenance 自测） | 44756301793 | success |
| Release preparation（`dry_run=false`、`create_tag=false`） | 44759379531 | success，`CHANGED: false`、`nothing to push`，exact-SHA evidence 通过 |
| Kernel release | 44760335832 | success（freeze、contract、Linux、macOS CLI、Windows core、Windows 安装器、Android AAR、Apple Libbox + iOS + macOS + 自签 IPA、stage、publish） |

说明：Kernel release 的 `apple / macos` 与 `apple / ios-selfsign` 在 attempt 1 因 GitHub 托管 macOS runner 容量（排队 15 分钟未分配）被取消；`apple / ios-selfsign` 在 attempt 2 因 runner 到 artifact 服务的 `ECONNRESET` 失败；两次均在原 run 重跑失败 job 后通过（attempt 3），Apple 身份文件与 Libbox 均来自 attempt 1，`run_attempt` 一致，未重建 Libbox。

## 公开附件（8 项，回读 `SHA256SUMS`）

```
b367b5f27070bba18d2ffa8770409783b9f3267c35d8c08bed042ff7cd207cba  Jiejiebox-v0.1.55-windows-x64.exe
f867af391a52917d14ab531b32051f5d9d7c364105ed2d2a1ef3894bad65841b  jiejie-ios-selfsign-unsigned-0.1.55.ipa
be8065c0e6966304df94aa3d3731405e121f60e6e3885efddb1a100347defd75  jiejie-libbox-apple-0.1.55.tar.gz
6f714402de1fa0b45368b070df1679a0bfa016f05404d2ebe44ef91a01a0540b  jiejie-sing-box-android-arm64-v0.1.55.tar.gz
0abd705d56f787798955626813ed68ed531c4fc725dae51eaeb90c09c2e969c9  jiejie-sing-box-linux-amd64-v0.1.55.tar.gz
f2356c7ec7076be8bf3fdc85f81b33113cd8d55de78575b09bf32cede4ebe99a  jiejie-sing-box-macos-arm64-v0.1.55.tar.gz
b641f836bb79bc369fe851be0eb1100e9b3fef0f6803ee95de471280762ce700  jiejie-sing-box-windows-amd64-v0.1.55.tar.gz
```

## 客户端与工具链

- Apple 客户端：`5998e5e8362d888c1ffe20ea0dee89041f066472`（`clients/apple` gitlink，与 `release/source-lock.json` 一致）。
- Libbox：来自 run 44760335832 / attempt 1，`jiejie-libbox-apple-0.1.55.tar.gz`，摘要见上。
- Windows 桌面：`clients/desktop` pin `1a1c98e590db6ae2b38bd60f492db93ca395b30f`，`version.json` 的 `go_version=go1.27.4`；安装器作业的 provenance 门禁记录 `built by go go1.27.4 (== version.json go_version)`，daemon 自报 `jiejiebox-daemon version 0.1.55`、`core api version 4`。
- 依赖 pin：`sing-anytls` `b74ada19da50`（换网清理在途创建），其余 9 个 Go 模块与 Android/Desktop/嵌套 dashboard 与各自跟踪分支头一致；cronet 29 个原生模块保持 controlled。

## 卫星（Satelite）交接

- 内核已发布并验收；Satelite 需要 `versionName=0.1.55`、`coreCommit=70e9109e91660c8f04991d10f0ea66252aa85300`、`versionCode=67`，提交后打 `v0.1.55` 并推送，触发 `Release APK`。
- Satelite 状态：**待完成**（发布后回填 Release URL、tag、客户端 SHA、run ID 与 APK 清单）。
