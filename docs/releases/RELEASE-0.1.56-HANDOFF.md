# Release 0.1.56 交接

- 版本：`0.1.56`（`release/JIEJIE_VERSION`）
- tag：`v0.1.56`（附注 tag），剥离后提交 **`b3c3606b767d4ce39d970b0c29d5067b6db533ab`**
- Release：https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.56
- 公开时间（UTC）：2026-11-28T07:08:22Z
- 回读时间（Asia/Shanghai）：2026-11-28 15:10
- 状态：`isDraft=false`、`isPrerelease=false`，正文非空（与 `CHANGELOG.md` 的 v0.1.56 条目同源）

## 公开资产（8 个）

| 资产 | SHA-256 |
| --- | --- |
| `Jiejiebox-v0.1.56-windows-x64.exe` | `4663b207c687564f93cb0e9fae7040df0dfbfe7cdceb418030ab0e3764bf3baa` |
| `jiejie-ios-selfsign-unsigned-0.1.56.ipa` | `52ca36cb79b37786ec60f63b93e7888e3d0e131c21501296a8621094de72aabb` |
| `jiejie-libbox-apple-0.1.56.tar.gz` | `d1143b31f96cafadc4b53f77004cde5c69e8fc8aa62c15641198a72faf85431b` |
| `jiejie-sing-box-android-arm64-v0.1.56.tar.gz` | `87095f08add349a12f1a68fb442dfcb37667d3e489acfc7359be3a0565b9e1a9` |
| `jiejie-sing-box-linux-amd64-v0.1.56.tar.gz` | `1c86ca4eab776d4135f0e2e904061051a80c10f3537536e32999949d88c15b32` |
| `jiejie-sing-box-macos-arm64-v0.1.56.tar.gz` | `2579d703625f63c0aa0e870fffd489ae430f4ca383f41b3fb2464a8e6a9f4f24` |
| `jiejie-sing-box-windows-amd64-v0.1.56.tar.gz` | `b26f3a993751faf5673f9ddd092b92f7dd45578f5518c045adf08aa06ae4d552` |
| `SHA256SUMS` | 汇总文件本身，746 B |

## 冻结来源

- `release/source-lock.json`（tag 处，`release_version=0.1.56`）：
  - sing `987a8527862b`、sing-tun `f23ebd654a75`、sing-quic `08d3812472b6`、quic-go `61b6144fd83e`、
    utls `87a09712c441`、wireguard-go `7f8be415c871`、sing-anytls `460710636c7f`、
    mieru `543072ab559b`、cronet-go `b9f445ddb1f4`（29 个 native 模块，controlled）
  - clients/apple `cb56592ece39c520cc06cb1b870b29bf002ef65f`、clients/android `9816e0068c82`、
    clients/desktop `cbd27f73d7ad`（report 策略保留）、nested dashboard `63a68d8cb2a7`
- Apple 构建所用的客户端 SHA：`cb56592e`（与 gitlink、main tip 一致）。

## 构建与证据

- 正式构建：[`Kernel release` run 46108342068](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/46108342068)（attempt 1 全绿；`publish` 于 attempt 2 完成）
- 失败与恢复：attempt 1 的 `publish` 在 `git push refs/tags/v0.1.56` 被 GitHub 拒绝，原因是以 GitHub App 身份推送 tag 时该提交包含 workflow 文件变更而 App 无 `workflows` 权限；已用维护者凭据在同一 SHA 创建并推送附注 tag，然后 **仅重跑 publish**（attempt 2），复用 attempt 1 已验证的 bundle，未重新构建。详见 `docs/releases/RELEASE-0.1.56-HANDOFF` 的下一节。
- `freeze`：source lock、嵌套 gitlink、分支关系、`check-pin-runs` 全部通过（pin 运行证据：`Release contract verification` run 46103991806 与最终候选 Verify run 46105636827）。
- `verify`（含 race checks）、`contract`、`linux-amd64`、`macos-arm64`（CLI）、`windows` core、`desktop` 安装器、`android` AAR、`apple`（Libbox / iOS / macOS / 自签 IPA）均 success。
- 重跑 Apple 的一次同 SHA 记录（发布前 Blocker Clearance 阶段）：run 46095153769（6 个 job 全绿）；正式发布内的 Apple 证据见上。

## 版本说明

- 仓库：`CHANGELOG.md` 的 `v0.1.56` 条目（随发布后提交 `47698a88f`，`[skip ci]`）。
- Release 正文：由同一份内容生成（去掉 changelog 的版本标题），`gh release edit --notes-file` 写入后回读非空。
- 两者内容同源；发布后仅补文档，不移动已冻结 tag。

## 后续（不在本次 Release 范围）

- Satelite Android 客户端 `0.1.56`：独立仓库发布（`versionName=0.1.56`、`coreCommit=b3c3606b7…`、`versionCode=68`），由该仓库的 `Release APK` 工作流产出签名 APK。
- TestFlight / App Store Connect：使用已发布的 Libbox manifest 与 Apple SHA，在 Mac 上按 `docs/TESTFLIGHT-MAC.md` 执行；Actions 成功不等于测试者可用。
- Windows 安装器的公开消费、真实设备 NE footprint / Jetsam / 功耗仍未取证。
