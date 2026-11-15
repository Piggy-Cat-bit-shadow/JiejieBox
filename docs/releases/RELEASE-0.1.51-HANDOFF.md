# v0.1.51 发布交接

日期：2026-11-15，Asia/Shanghai。

- 内核正式 tag：`v0.1.51`（annotated `ecd477631f0bb3a5a5db4107a6f7e3fb5617e33f`），剥离提交 `1bf3c91613daffa465da5c2fd6011efca39b222a`，`release/JIEJIE_VERSION` = `0.1.51`。
- [内核 Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.51)：2026-11-15T14:26:13Z 公开，非 draft、非 prerelease；正文与 8 项附件已回读。
- [Kernel release 43721350987](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43721350987)：run attempt 1；freeze、Verify/race checks、Release contract、Linux、macOS、Windows core、Desktop 安装器、Android AAR、Apple（resolve / libbox / ios / macos / ios-selfsign）、stage、publish 全部成功，无重跑。
- Apple 固定 `2e5b71fa59809b17faff1e24ab2e56caee52612e`。该 revision 是 v0.1.50 Apple build manifest 实际记录的源码；v0.1.50 的 source lock 曾冻结落后一 commit 的 `0947393`，v0.1.51 已对齐 gitlink、source lock、ABI verified SHA 并重新完成 iOS/macOS 编译（run 43717278645）。
- 最终候选 `1bf3c9161` 的 Verify（含 race checks）为 run `43717257806`，Release contract 为 `43717274783`；Release preparation run `43719950462`（`dry_run=false, create_tag=false`）确认无 pin 移动并取得同 SHA 证据，随后 run `43720667598` 创建 tag。
- `release/pin-runs.json` 的 Apple pin 覆盖引用 commit `f512b8e8b`（Android run `43713121151`）。集成分支 `release/0.1.51-apple-pin` 已在发布后删除；该 commit 现由非发布 tag `archive/0.1.51/jiejiebox/apple-pin-anchor` 保留，浅克隆 tag v0.1.51 上执行 `check-pin-runs.py --fetch-missing` 已实测通过。
- 公开附件（均排除 `SHA256SUMS` 自身）：`jiejie-sing-box-linux-amd64-v0.1.51.tar.gz`、`jiejie-sing-box-macos-arm64-v0.1.51.tar.gz`、`jiejie-sing-box-windows-amd64-v0.1.51.tar.gz`、`Jiejiebox-v0.1.51-windows-x64.exe`、`jiejie-sing-box-android-arm64-v0.1.51.tar.gz`、`jiejie-libbox-apple-0.1.51.tar.gz`、`jiejie-ios-selfsign-unsigned-0.1.51.ipa`、`SHA256SUMS`。
- [Satelite v0.1.51](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.51)：2026-11-15T14:50:14Z 公开，非 draft、非 prerelease；客户端提交 `17eb94cecaa45678a93210820a4ec1e2c5a8fb69`，`versionName=0.1.51`、`versionCode=63`、`coreCommit=1bf3c91613daffa465da5c2fd6011efca39b222a`。
- [Satelite run 43724418843](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/43724418843)：tag push 触发，attempt 1；build 与 publish 两个 job 成功；附件 `satelite-one-0.1.51-arm64-v8a-signed-v0.1.51.apk`、`satelite-one-0.1.51-x86_64-signed-v0.1.51.apk`、`SHA256SUMS.txt`、`build-info.json`、`apksigner-verify.txt` 已回读。
- `build-info.json` 自证：`app_version=0.1.51`、`app_version_code=63`、`core_commit=1bf3c9161…`、`core_ref=pinned`、`signature_state=signed-by-user`（由 `apksigner verify --verbose --print-certs` 实测）；两个 APK 与 AAR 的 SHA-256 均在该文件中。
- TestFlight（`docs/TESTFLIGHT-MAC.md`）本次未执行；其输入是已公开的 `jiejie-libbox-apple-0.1.51.tar.gz`，需在已认证 Mac 上另行完成签名、上传和 App Store Connect 回读。
- 发布后文档提交（`CHANGELOG.md` 与本文）不移动已冻结 tag。
