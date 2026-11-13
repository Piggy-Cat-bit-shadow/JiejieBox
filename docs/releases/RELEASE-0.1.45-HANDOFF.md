# v0.1.45 正式发布交接记录（2026-11-13）

按 [Release 指引](../RELEASE-WINDOWS.md) 分别验收内核与 Satelite，整轮两个仓库的公开资产均已回读。附件摘要来自各自公开 GitHub Release API；Satelite 另取 `build-info.json` 核对内核来源与签名状态，本机未下载其它附件复算。此记录可供独立的 [TF 指引](../TESTFLIGHT-MAC.md) 使用，真机行为仍需设备验收。

## 内核发布身份

| 项 | 值 |
| --- | --- |
| 版本 / tag | `0.1.45` / `v0.1.45`（附注标签对象 `e12acbf22bc2ffe814cfc41567f0dbeab8bb2e92`） |
| tag 剥离后的完整 SHA | `97091ce3637e68533c060ebac4ff9e309e512f25` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.45 |
| 状态 | 公开、非 draft、非 prerelease；附件 8 项，自公开到回读未变 |
| 运行 | [Kernel release 43360468121](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43360468121)，attempt 1，`2026-11-13T03:39:57Z` → `2026-11-13T04:08:14Z`（stage `04:07:00Z`→`04:07:44Z`，publish `04:07:46Z`→`04:08:13Z`） |
| 公开时间 | `2026-11-13T04:08:10Z`（2026-11-13 12:08:10 Asia/Shanghai） |
| 发布前门禁 | [Verify 43358396443](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43358396443)、[Release contract 43358704997](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43358704997)、[preparation 43359945474](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43359945474) 均在最终 SHA `97091ce36` 成功 |

正式运行的 freeze、contract、Verify（含 race）、Linux、macOS CLI、Windows core、Windows 安装器、Android ARM64 AAR、Apple（Libbox + iOS + macOS + 自签 IPA）、stage 与 publish 全部成功。

## 公开正文

初次公开时正文为空（`Kernel release` 的既有行为）。本轮于 `2026-11-13T04:13Z` 前后把本版说明（3,316 字符）写入正文；随后并行的文档会话在 `04:23:55Z` 用同一版本的更完整日志（36,107 字符，提交 `4e6b0c5aa` "docs: publish full v0.1.45 changelog and align README"）替换了正文。当前正文非空、版本与附件未变；两次写入都是文字编辑，没有重新构建或重发资产。

## 公开资产（8 项）

| 资产 | 字节 | GitHub API 的 SHA256 |
| --- | ---: | --- |
| `jiejie-sing-box-linux-amd64-v0.1.45.tar.gz` | 28,924,941 | `27c0149ddfc80c6ec2a4b3acbb11fc2c35c388b4540a36828c64bc714e6d82e5` |
| `jiejie-sing-box-macos-arm64-v0.1.45.tar.gz` | 30,772,328 | `11b586c52d595afb0d4215c0a73f9a286210421af47e67874eb0e92953cdfbe5` |
| `jiejie-sing-box-windows-amd64-v0.1.45.tar.gz` | 29,256,491 | `4c6de2f07024b4ec858819675d3a0643c3bcca21506488678d38c62a9949a90c` |
| `jiejie-sing-box-android-arm64-v0.1.45.tar.gz` | 29,986,601 | `6deae6c0d30588389f1b3c163b7e1a9cc77d11452be2c69d2e4f634e109e8909` |
| `jiejie-libbox-apple-0.1.45.tar.gz` | 74,033,763 | `cbdf1b81ea70f941e4c77550862b5ae4b358806418e6d87a1fbec7410b8e37fe` |
| `Jiejiebox-v0.1.45-windows-x64.exe` | 116,616,664 | `2743bafedaea41f590b477751f765acbd35c4ef35555f7e3da00aaf7885c2bb5` |
| `jiejie-ios-selfsign-unsigned-0.1.45.ipa` | 45,794,606 | `7d57372a88ecc669474a82b9f8a210f7a21984b24395db070c0b35cee5c68704` |
| `SHA256SUMS` | 746 | `45db396bf5df9ad427ea3f39a332120f775d2a015c16a012e02f6e58d78dbdf4` |

工作流 stage 与 publish 校验了同一批发布附件；本表的摘要是公开 API 元数据，不标成独立本地验包。

## Apple 与客户端来源

| 项 | 值 |
| --- | --- |
| Apple gitlink / 构建源码 | `aa8e16aa21a7f28aefc944fac76086c144195e3b`（客户端 `main`） |
| Libbox 内部归档摘要 | `Libbox.xcframework.tar.gz`：`4d4adbe76c19f0703a3a6f43a3564a255335a07280bf61b59dc4a3200860bbfe`，74,879,400 字节（运行日志） |
| Libbox slices | `ios-arm64`、`macos-arm64`（运行日志） |
| 编译证据 | 同一 run 的 `apple / libbox`、`apple / ios`、`apple / macos`、`apple / ios-selfsign` 全部成功，三个产品消费同一份 Libbox |
| Android / Desktop gitlink | `9816e0068c82397e0ae48966cec982c0fd3dee18` / `a658231fcd976fbcd7658f0c49d8132a9fc4860a` |
| 嵌套 dashboard pin | `d9fca1b1f226e9555c63190cbad7b630eb3ae01d` |

TF 按内核 tag、完整 SHA 与公开 Apple Libbox 获取固定来源；不在 Mac 上重新解析移动的 Apple `main`。构建通过不代表签名后的真机 Network Extension 路由、reload 或本地签名回环已运行。

## 本轮的三次阻断与恢复

1. **首轮正式运行在 macOS 单测失败**（[43340503796](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43340503796)，候选 `d03600ed4`）：`TestRaceSimultaneousCompletionsDeliverExactlyOneResponse` 断言 `len(transport.broken) == 0`，但该测试允许任一线路获胜，而 HTTP/3 获胜时 `clearH3Broken` 依设计保留条目并只撤销窗口。强制 H3 获胜可复现证据：`entries=1 found=true backoff=0s untilZero=true h3Broken=false`。判定为测试断言缺陷（非产品缺陷），改为断言"未设置判定"并校验条目已撤销，提交 `b7a5a4500`。
2. **Apple 客户端从未编译**（`3221000` 只做过语法解析）：四处编译阻断（配置结果分支未穷尽、探针与摘要类型错配、可选回执、三处 Libbox 方法调用点使用 Go 名称）与三处调用点在客户端修复并合入 `main`（`aa8e16a`，修复分支已合并后删除）。内核同轮把 gitlink、`release/source-lock.json`、`check-libbox-abi.sh` 的已验证 Apple SHA 与 `pin-runs.json` 的观测一起前移；[Apple client 43348165279](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43348165279) 用显式输入 `aa8e16a` 构建同一份 Libbox 并通过 iOS、macOS、自签 IPA 三个目标。新 pin 的执行证据由 `efe37e13` 上的 [Android Core ARM64 43350914802](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43350914802) 与 [Release contract 43350914799](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43350914799) 提供，随后 `5020c7de4` 记录进 `release/pin-runs.json`。
3. **第二轮的 macOS headless 冒烟在 `SubscribeConnections` 失败**（[43354180514](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43354180514)，候选 `5020c7de4`）：服务端返回 `Grpc-Status: 9` / `Grpc-Message: service is not running`。根因是独立 `run` 的 Native API 用 `NewAttachedService` 直接写 `s.instance`，绕过 `setInstanceLocked`，实例身份恒为空，`connectionStreamBinding` 因而拒绝连接事件订阅（状态、日志、出站不受影响）。用 v0.1.44 公开二进制与本地构建 A/B 证明（`frames=1` vs `frames=0`），修复后 `frames=1`。提交 `97091ce36`：构造函数改走唯一写入点，新增结构门禁测试（负向对照：还原旧构造函数即失败），并让冒烟脚本输出 HTTP 状态、gRPC 状态/消息与异常，不再吞掉错误。该作业在最终正式运行中通过。

## Satelite 安卓客户端

| 项 | 值 |
| --- | --- |
| 版本 | `0.1.45`；`versionCode=56`（上一公开版为 55） |
| tag | `v0.1.45`（附注标签对象 `f7e5e913bd0cc698f5f08d1ab4592045d77e699f`） |
| tag 剥离后的客户端 SHA | `030093fe49f5ad1a8bafaa629abaa7f7544acd16`，与 `main` 一致 |
| 固定内核 SHA | `97091ce3637e68533c060ebac4ff9e309e512f25`，与内核正式 tag 一致（`build-info.json` 的 `core_commit`） |
| Release | https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.45 |
| 运行 | [Release APK 43363780921](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/43363780921)，tag push 触发，attempt 1，`04:10:36Z` → `04:37:23Z`；构建与发布作业均成功 |
| 公开时间与状态 | `2026-11-13T04:37:19Z`（2026-11-13 12:37:19 Asia/Shanghai）；非 draft、非 prerelease |
| 签名 | `apksigner verify` 实测 `signed-by-user`；内核 AAR `e736153485d2129b52b07df65021de275f4c9a0b79be76f8edf587327379d68e`（124,741,579 字节） |

| 资产 | 字节 | GitHub API 的 SHA256 |
| --- | ---: | --- |
| `satelite-one-0.1.45-arm64-v8a-signed-v0.1.45.apk` | 31,330,475 | `b7f856c6ae667321460cf9e864a71aa059dba1a4d8890b73121949c6e933ef5d` |
| `satelite-one-0.1.45-x86_64-signed-v0.1.45.apk` | 33,331,922 | `4f80b4fa5e97f635408f5a538ae16f2303b28f370dfa16f0eff3cd0c77d9fe56` |
| `SHA256SUMS.txt` | 227 | `4e5ebea051fca806c87d9be01adfb32aa5ceb785c8bb927bcb071de1ea1ea49e` |
| `build-info.json` | 1,352 | `ffe3b9240eaac3a765660fa9f4633e4f028d57e0e05175303b411ba2169c0e75` |
| `apksigner-verify.txt` | 2,844 | `3394627b1680feb43d5ce6f6ba163850d8a558b0977996ead2504dbefd0aba7d` |

`build-info.json` 记录 `core_commit=97091ce3637e68533c060ebac4ff9e309e512f25`、`signature_state=signed-by-user`（由 `apksigner verify --verbose --print-certs` 实测），两个 APK 的摘要与附件一致，`core_engine_sha256` 覆盖 arm64-v8a、armeabi-v7a、x86、x86_64 四个引擎。签名与身份验证属于该 CI 运行的证据，本机未再次下载附件或调用 `apksigner`。

## 结论

内核与 Satelite 的公开资产均已验收，整轮发布完成。Mac TestFlight 仍是独立流程，从公开 Release 的 Libbox manifest 取固定 Apple 源码；真机 Network Extension、锁屏与回环行为未在本轮验证。
