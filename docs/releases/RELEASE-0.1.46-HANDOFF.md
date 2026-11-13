# v0.1.46 正式发布交接记录（2026-11-13）

按 [Release 指引](../RELEASE-WINDOWS.md) 分别验收内核与 Satelite，两个仓库的正式版及公开附件均已回读。校验和来自公开 GitHub Release API，产物校验与签名检查来自对应 Actions；本机仅下载冻结报告和构建元数据，未重复下载二进制验包，也未执行真机验证。最终汇总回读时间：`2026-11-13T18:44:27+08:00`（Asia/Shanghai）。

## 内核正式版

发布后文档补充：`2026-11-13T20:48:58+08:00` 将按既有日志结构展开的 0.1.46 说明同步至 GitHub Release，回读确认正文一致、发布身份和 8 项附件未变。同轮补齐中英文 TrustTunnel 回退配置、首页对应协议说明与[知识库专题](../../KNOWLEDGE/CALIBRATION-0.1.46.md)；0.1.45 / 0.1.46 的可前置检查、Apple 重跑条件和耗时记录要求写入 [Release 指引](../RELEASE-WINDOWS.md)与[复盘](../RELEASE-PLAYBOOK.md)。此次只更新文档，不重建产物或移动 tag。

| 项 | 值 |
| --- | --- |
| 版本 / tag | `0.1.46` / `v0.1.46`，附注标签对象 `c897632e3e4c0e48b113bd790c76fc980ee1fe48` |
| tag 剥离后的完整 SHA | `c795bb5cbbcd9b6a4d2d2f18e854b629915335f5` |
| Release | [内核 v0.1.46](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.46)，公开、非 draft、非 prerelease，8 项附件 |
| 正式运行 | [Kernel release 43404981823](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43404981823)，attempt 1，全部必需作业、stage 与 publish 成功 |
| 运行创建至完成 | `2026-11-13T17:54:07+08:00` → `2026-11-13T18:22:45+08:00` |
| 首次公开 | `2026-11-13T18:22:41+08:00` |
| 正文补写及首次回读 | `2026-11-13T18:23:44+08:00`，正文与本版 CHANGELOG 内容一致、附件保持不变 |
| 最终候选门禁 | [Verify 43403457223](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43403457223)、[Release contract 43403458276](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43403458276)、[Preparation 43403463500](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43403463500)，均为该 SHA 且成功 |

| 资产 | 字节 | GitHub API SHA256 |
| --- | ---: | --- |
| `jiejie-ios-selfsign-unsigned-0.1.46.ipa` | 45,796,841 | `2db280b14e7536c6dc09a4b595c01a34a37c900698149402be803e8283fbd511` |
| `jiejie-libbox-apple-0.1.46.tar.gz` | 74,031,983 | `c78221dc8db8adff6bb2f4fd91601c4b5a9b383052aec7a1d7c96766819885f7` |
| `jiejie-sing-box-android-arm64-v0.1.46.tar.gz` | 29,987,540 | `0e72fa7bb9068cf16319e1a503c2ba87c171bceae46c5da743bb8b23a4b01e19` |
| `jiejie-sing-box-linux-amd64-v0.1.46.tar.gz` | 28,929,154 | `32b397f64ecf4df3229a55a31636d34010b97fdcd31b15bda327609388221922` |
| `jiejie-sing-box-macos-arm64-v0.1.46.tar.gz` | 30,781,819 | `5001c68218b944c07738359dd00855fcc6ce4f28ab01eb2ee79234b594482f9f` |
| `jiejie-sing-box-windows-amd64-v0.1.46.tar.gz` | 29,264,633 | `f342bf4c8ffbc3cccc121964d9fcc2222e8dd15ef9be0c5208ae1e11ff57a615` |
| `Jiejiebox-v0.1.46-windows-x64.exe` | 116,621,704 | `fae1a23ea6a8424cccad13859367a7adae7ca206e0944905131db709de385640` |
| `SHA256SUMS` | 746 | `7fc03d12fd3e945d90d2057c8bfde5dc140005bf7f866b7966fd28e2d38b1072` |

## 正式运行时间与阶段等待

下表为 Actions 回读的实际 job 起止时间；子作业依赖等待不冒充编译耗时。首次运行的 macOS hosted runner 排队失败约 15 分钟，见失败记录。最终成功运行从 freeze 开始至首次公开为 1710 秒；最后一个前置作业完成至 stage 开始等待 2 秒，stage 完成至 publish 开始等待 2 秒。

| 正式作业 | 开始（UTC） | 完成（UTC） | 用时（秒） |
| --- | --- | --- | ---: |
| freeze | 2026-11-13T09:54:11Z | 2026-11-13T09:54:42Z | 31 |
| windows / sing-box.exe (windows/amd64) | 2026-11-13T09:54:45Z | 2026-11-13T09:55:54Z | 69 |
| contract / release helper tests | 2026-11-13T09:54:45Z | 2026-11-13T09:55:47Z | 62 |
| desktop / unsigned-build / signed-test / install-lifecycle | 2026-11-13T09:54:45Z | 2026-11-13T10:01:08Z | 383 |
| apple / resolve the Apple client source | 2026-11-13T09:54:45Z | 2026-11-13T09:54:53Z | 8 |
| macos / macos-arm64 | 2026-11-13T09:54:45Z | 2026-11-13T10:12:11Z | 1046 |
| linux / linux-amd64 | 2026-11-13T09:54:46Z | 2026-11-13T10:05:34Z | 648 |
| android / Android ARM64 core | 2026-11-13T09:54:45Z | 2026-11-13T09:56:59Z | 134 |
| verify / verify | 2026-11-13T09:54:45Z | 2026-11-13T10:03:08Z | 503 |
| verify / race checks | 2026-11-13T09:54:45Z | 2026-11-13T10:06:44Z | 719 |
| apple / libbox | 2026-11-13T09:54:57Z | 2026-11-13T10:04:31Z | 574 |
| apple / macos | 2026-11-13T10:04:35Z | 2026-11-13T10:19:56Z | 921 |
| apple / ios-selfsign | 2026-11-13T10:04:35Z | 2026-11-13T10:21:27Z | 1012 |
| apple / ios | 2026-11-13T10:04:34Z | 2026-11-13T10:16:55Z | 741 |
| stage | 2026-11-13T10:21:29Z | 2026-11-13T10:22:17Z | 48 |
| publish | 2026-11-13T10:22:19Z | 2026-11-13T10:22:44Z | 25 |

## 冻结来源与 Apple 交接

- Apple 客户端：`aa8e16aa21a7f28aefc944fac76086c144195e3b`；Android gitlink：`9816e0068c82397e0ae48966cec982c0fd3dee18`；Desktop：`a658231fcd976fbcd7658f0c49d8132a9fc4860a`；嵌套 dashboard：`d9fca1b1f226e9555c63190cbad7b630eb3ae01d`。
- 干净检出完成递归子模块初始化，严格 source lock、ABI pin、知识库事实和 pin-runs 检查通过。正式运行的 `release-pin-report` 已下载回读：跟踪客户端、dashboard 和普通 Go fork pin 均一致；Cronet root/all 发布 pin 位于其源码分支 tip 之后，29 个 native 模块保持受控 pin，无分叉与待前移 update pin。
- Apple identity 为本次 run `43404981823` / attempt `1`，内核版本与 SHA 均匹配。iOS、macOS GUI 与自签 IPA 消费同一次 Libbox，三个作业均成功。
- 内部 `Libbox.xcframework.tar.gz`：74,878,716 字节，SHA256 `40eb3e9e14540c9d7d48dcfec79822193b4a17cc1c416afd589d78c0977f57ba`（Libbox 作业日志）；slices 为 `ios-arm64`、`macos-arm64`。公开 Apple 外层归档摘要见上表，两者是不同文件。
- Mac TestFlight 是独立后续任务：从本版公开 Libbox manifest 取得固定 Apple 来源。此次未进行 TF 上传或签名后的真机 Network Extension 验收。

## 失败调查与恢复

1. 初始候选 `829a4a45b` 的 [Maintenance ledger 43395448688](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43395448688) 失败：TrustTunnel 改动后账本引用的两处源码位置及回归文件计数过期。运行仓库生成器，提交 `e3bba7ed8`；本地完整账本检查和 [修复后 CI 43396840563](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43396840563) 通过。
2. [首个正式运行 43398479790](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43398479790) 的 attempt 1：两个 macOS job 等待约 15 分钟后均因 hosted runner 无法分配而取消，其他平台与门禁成功。按暂时故障重跑失败 job，保留 SHA 和版本。
3. 同 run 的 attempt 2：Libbox 编译及打包成功，但身份绑定失败，明确报 `Libbox run_attempt differs from the frozen release`。根因是成功的 Apple 身份解析作业未重跑，沿用 attempt 1 的 `apple-release.json`，而库为 attempt 2。未改写身份或放宽校验。之后因下面的测试修复生成新候选，整次正式构建从 attempt 1 重启，身份与库一致；相关恢复约束已补入 Release 指引。
4. 同 run 的 macOS 单测 `TestFastestWinExpiryTriggersReelection` 失败：发出全部首轮探测不等于 collector 已释放选举 token，测试在回收尚未完成时立即要求新 fan。未改动生产选举语义；本机以 `-count=1000 -cpu=1,2,8,32 -failfast` 复现同一超时，随后增加首轮选举完成屏障，保留精确三请求重选断言。修复后该命令共 4,000 次通过，DNS group 整包重复 20 次通过。提交 `c795bb5cb` 后重新取得同 SHA 门禁与 preparation；最终正式运行的 macOS 全套单测、生产构建和 headless 冒烟均通过。

上述失败发生时正式 tag 均不存在，因此修复后保留 `0.1.46`，重新冻结和完整构建。已发布 tag 未移动。

## Satelite 安卓客户端

| 项 | 值 |
| --- | --- |
| 版本 / 构建号 | `0.1.46` / `versionCode=57`；全历史版本文件中此前最高构建号为 56 |
| tag | `v0.1.46`，附注标签对象 `42eee41f352c2ac6f402b8e49e5cbcfb27486098` |
| tag 剥离后的客户端 SHA | `530c0812dd379644416608eee434bd96f17107ac` |
| 固定内核 SHA | `c795bb5cbbcd9b6a4d2d2f18e854b629915335f5`，与内核正式 tag 一致 |
| Release | [Satelite v0.1.46](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.46)，公开、非 draft、非 prerelease，5 项附件 |
| 正式运行 | [Release APK 43408906275](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/43408906275)，tag push 触发，attempt 1，构建、来源、单测、双 ABI 引擎、签名与发布校验成功 |
| 运行创建至完成 | `2026-11-13T18:25:06+08:00` → `2026-11-13T18:42:51+08:00` |
| 首次公开 | `2026-11-13T18:42:47+08:00` |
| 签名 | `signed-by-user`，由 `apksigner verify --verbose --print-certs` 实测 |
| AAR | 124,742,375 字节，SHA256 `5f5cd1fd683633ace80809c847d42b0bef4fb0916cb5c8fc3babc142f407c686`（本次 build-info） |

| 资产 | 字节 | GitHub API SHA256 |
| --- | ---: | --- |
| `apksigner-verify.txt` | 2,844 | `3394627b1680feb43d5ce6f6ba163850d8a558b0977996ead2504dbefd0aba7d` |
| `build-info.json` | 1,352 | `22da997e3521ac7a3e2fd7396e77f663d59311685912b97b9f420b26c5ac6716` |
| `satelite-one-0.1.46-arm64-v8a-signed-v0.1.46.apk` | 31,330,663 | `9a7f2095a058ef6532b02df7989ed4036684caf03900b47a93a5b8baa95764c5` |
| `satelite-one-0.1.46-x86_64-signed-v0.1.46.apk` | 33,334,330 | `66f39a3ed6e9b7c6ede4324404f405799516d44e251d1a88e393bf5ce1e352fd` |
| `SHA256SUMS.txt` | 227 | `6102cedd54bebf5e5b81a42e5519087e01546e2de0a8c6005176f7e07c2ea214` |

`build-info.json` 的 app/core SHA、版本、构建号、签名状态与上述身份一致，两个 APK 的摘要与公开 API 一致，AAR 的逐 ABI 引擎摘要覆盖四个内核 ABI。公开 APK 为 `arm64-v8a` 与 `x86_64`；它们与主仓 Android ARM64 AAR 分别验收。

整轮内核与 Satelite 正式发布完成。CHANGELOG 与公开内核 Release 正文已同步，发布后文档提交不改变冻结的正式 tag。
