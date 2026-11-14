# v0.1.50 发布交接

日期：2026-11-15，Asia/Shanghai。

- 内核正式 tag：`v0.1.50`（annotated `097d67a689be1d6b9a6199030052d4a2b7e8a43f`），剥离提交 `934a290d4477b7dd7fe630dc5021a0e2d7bc2601`，`release/JIEJIE_VERSION` = `0.1.50`。
- [内核 Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.50)：2026-11-14T20:08:33Z 公开，非 draft、非 prerelease；正文与 8 项附件已回读（回读时间 2026-11-14T20:10Z 前后）。
- [Kernel release 43633810003](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43633810003)：run attempt 1，freeze、contract、Verify/race checks、linux、macos、windows core、desktop 安装器、android、apple（resolve / libbox / ios / macos / ios-selfsign）、stage、publish 全部成功，无重跑。
- Apple 固定 `09473938322bddfc6c30fc3886e747e808c3d526`；层 4 ABI 调用方扫描 483 文件 / 0 违规 / 3 个 interface 方法，编译证据为同批次 iOS、macOS 与自签 IPA 三个产物都从这一份 Libbox 构建成功。
- Libbox 身份绑定内核 `934a290d4`、Apple `0947393`、run `43633810003` / attempt 1；附件 `jiejie-libbox-apple-0.1.50.tar.gz`，摘要见同批 `SHA256SUMS`。
- 公开附件：`jiejie-sing-box-linux-amd64-v0.1.50.tar.gz`、`jiejie-sing-box-macos-arm64-v0.1.50.tar.gz`、`jiejie-sing-box-windows-amd64-v0.1.50.tar.gz`、`Jiejiebox-v0.1.50-windows-x64.exe`、`jiejie-sing-box-android-arm64-v0.1.50.tar.gz`、`jiejie-libbox-apple-0.1.50.tar.gz`、`jiejie-ios-selfsign-unsigned-0.1.50.ipa`、`SHA256SUMS`。
- 依赖与客户端最终 pin：`sing` `4abfaed`、`sing-tun` `24e6bfd`、`sing-quic` `08c6abd`、`quic-go` `a99a3c4`、`utls` `e14b8887`、`wireguard-go` `c308e20`、`sing-anytls` `8473492a`、`mieru` `b5934f7`、`cronet-go` `362e63b`（`all` `7ce9c49b`，native 仍 controlled）、apple `0947393`、android `9816e006`、desktop `1bd68313`、嵌套 dashboard `b6e3193`。
- 发布前准备：`Release preparation 43633318312`（`dry_run=false`、`create_tag=true`）生成冻结锁 `6735233ff` 并创建 tag；`pin-runs` 覆盖运行 `43631412925`（linux-kernel-verify）与 `43631416287`（Release contract verification）记录在 `release/pin-runs.json`。
- 上游审阅基线：`git merge -s ours` 记录上游 `3dd8aa48`，68 个上游独有提交逐条台账见 `docs/fork/UPSTREAM-BASELINE-2026-11-15.{md,json}`（45 已吸收 / 2 fork 等价 / 5 文档 / 16 待评审）。`behind = 0` 是审阅结论，不代表 68 个提交的代码已整体采纳。
- 根 `CHANGELOG.md` 与内核 Release 正文已补齐并回读，正文非空（3403 字符）；文档提交不移动已公开 tag。
- 已发布的 `v0.1.49`（tag `4e2b5d9f`）与更早版本未被改动；本轮未创建、未移动、未删除其他 tag。
- Satelite APK 发布（指引第 4 节）本次未执行：内核 Release 已完成，Satelite 需在其自己仓库按 `versionName=0.1.50`、`coreCommit=934a290d4…` 单独打 tag 并发布。
- TestFlight（`docs/TESTFLIGHT-MAC.md`）本次未执行；其输入是已公开的 `jiejie-libbox-apple-0.1.50.tar.gz`。
