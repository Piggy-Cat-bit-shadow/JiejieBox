# v0.1.42 内核正式发布交接记录（2026-11-07）

本记录由 Windows 正式版任务生成，供 [TF 构建指引](../TESTFLIGHT-MAC.md) 与 Satelite 后续任务消费。
表中的摘要来自公开 Release 实际字节的本地回读，不是构建日志的转述。

## 发布身份

| 项 | 值 |
| --- | --- |
| 版本 | `0.1.42` |
| tag | `v0.1.42`（附注标签对象 `dc2e436ba0dcec76e8e749bf299e9266e542a01a`） |
| tag 剥离后的完整 SHA | `9856b04964b5b137e83bfed4d4a3ebcadb934ac0` |
| Release | https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.42 |
| 状态 | 公开、非 draft、非 prerelease；正文为空（0 字节） |
| 运行 | Kernel release `42331566733` attempt 1，`2026-11-07T02:55:28Z` → `2026-11-07T03:17:48Z`（22 分 20 秒） |
| 公开时间 | `2026-11-07T03:17:44Z`（11:17:44 Asia/Shanghai） |
| 回读时间 | `2026-11-07T03:24:53Z`（11:24:53 Asia/Shanghai） |

`v0.1.42` 是 0.1.39 之后第一个公开正式版；`v0.1.40` 只有 tag（指向 `0de1c13dcb3`，无公开
Release），0.1.41 未被使用。

## 源码链

| 提交 | 内容 |
| --- | --- |
| `26326d022` | 发布编排与 Release/TF 指引基线 |
| `ca51b7811` | `release(0.1.42): cut 0.1.42`（版本文件、source lock、空 release notes） |
| `5c3266048` | `release: freeze the dependency sources for v0.1.42`（source lock） |
| `dc7892964` | `release: pin the Apple client to the frozen source`（gitlink → `0c7b10be`） |
| `4cc04c612` | `build(apple): move the ABI-verified client pin to 0c7b10be` |
| `9856b0496` | `ci(apple): follow the per-platform TF modes and the third Libbox consumer`（tag 目标） |

## 公开资产（8 项）

全部 8 个附件已下载并复核：7 个二进制逐个用 SHA256 与清单比对，全部一致；清单恰好 7 条，
无缺失或多余条目；`SHA256SUMS` 自身摘要与 Release 记录一致。

| 资产 | 字节 | SHA256 |
| --- | --- | --- |
| `jiejie-sing-box-linux-amd64-v0.1.42.tar.gz` | 27,960,764 | `7704649058d08ee0de8528b0186f393d1366600a2bce02aed940c637f87e0284` |
| `jiejie-sing-box-macos-arm64-v0.1.42.tar.gz` | 29,905,201 | `f3e1aaae20156dbe4008aea653b8ca846dd1b7c58eceee37980297543a58a809` |
| `jiejie-sing-box-windows-amd64-v0.1.42.tar.gz` | 28,305,261 | `7524e0a2464be546da05c3a2862418aa7a415949014826a6de6b5ce4d3e83a51` |
| `jiejie-sing-box-android-arm64-v0.1.42.tar.gz` | 28,992,638 | `c6cebd15f25b5012c81ecbe92c63845b15a9e7009c5b32018af58b2e9958252d` |
| `jiejie-libbox-apple-0.1.42.tar.gz` | 71,940,025 | `5684312c3c19d92af9614fca84e4cf72e197dde8030ab7eb74b79079eb2be299` |
| `Jiejiebox-v0.1.42-windows-x64.exe` | 115,976,640 | `1d7670eb83bd9afeaae8ff764c9b3aa513ff5961565d8a0050524e6e9be09e3a` |
| `jiejie-ios-selfsign-unsigned-0.1.42.ipa` | 44,503,660 | `9437e6ea6991474806a54b3f65f7875a9bb3af005bad5c02dc6ccc873ac7b3fe` |
| `SHA256SUMS` | 746 | `ba4f9d0699284ec07e44dc88b88b7f51f5fd96b7983b144d47c9c75e4a10af25` |

## Apple 交接

| 项 | 值 |
| --- | --- |
| Apple 客户端 SHA | `0c7b10be05811cdd3f31a48a7cbe49f061852084`（`sing-box-for-apple` 的 `main`） |
| Libbox 版本 | `0.1.42+jiejie.9856b04` |
| Libbox manifest `artifact_sha256` | `d34c910a0b24d89819d40d5f02baec902603bae78c78dda05b77ec666db7b9ce` |
| slices | `ios-arm64`、`macos-arm64` |
| 构建环境 | Xcode 26.6、Go 1.25.5；run `42331566733` attempt 1，`2026-11-07T03:03:42Z` |
| 公开载体 | `jiejie-libbox-apple-0.1.42.tar.gz`（含 `Libbox.xcframework.tar.gz`、`manifest.json`、`SHA256SUMS-LIBBOX.txt`、`BUILD-INFO-LIBBOX.txt`） |

`Libbox.xcframework.tar.gz` 已从发布资产解出，本地复核摘要等于 manifest 的
`artifact_sha256`。TF 任务按 [TF 构建指引](../TESTFLIGHT-MAC.md) §1 用该 tag、完整 SHA 和已发布
Libbox；Apple 源从 Manifest 读取，不要在 Mac 重新解析 `main`。

## 本轮发布工具缺陷（记录）

1. **`release-prepare` 的提交步骤会撤销 pin 移动。** `release-freeze.py apply` 只改 index
   （`git update-index`），随后工作流的提交步骤执行 `git add -A`，会把 submodule 按工作区
   checkout 的旧提交重新暂存。结果：`5c3266048` 的 `release/source-lock.json` 写着
   `0c7b10be`，而 `clients/apple` 的 gitlink 仍是 `ab575dac`。用
   `python scripts/ci/release-freeze.py verify --lock release/source-lock.json` 当场可复现
   FAIL。处置：不重跑 prepare（会再次产生同一状态），直接以 `git update-index` 提交
   gitlink（`dc7892964`）。建议修复：apply 后不要用 `git add -A` 覆盖 gitlink，例如
   `git add -A -- . ':(exclude)clients'`，或在 commit 前再执行一次 apply 重新断言 index，
   并为该路径补测试。
2. **ABI pin 常量没有随 gitlink 移动。** `check-libbox-abi.sh` 的 `VERIFIED_APPLE_SHA`
   仍指向 `ab575dac`，verify 作业的 "client gitlinks match the ABI contract pins" 立刻拦下。
   已对新源重跑 layer-4 caller sweep（424 个 Swift 文件、3 个接口方法、0 违反），再更新常量
   （`4cc04c612`）。
3. **`89a16b578` 留下的过期断言。** 该提交改了 `scripts/publish-apple-beta.sh`（改为
   `release-apple.sh "$testflight_mode"`）和 `client-apple.yml`（新增第三个 Libbox 消费者），
   却没有更新 `test-apple-beta-publish.sh`，导致 Apple 作业的回归套件连续两次红。已按新设计
   更新两处断言（`9856b0496`）。

## 后续任务

- **Mac TF**：按 [TF 构建指引](../TESTFLIGHT-MAC.md) 独立执行；开始条件是本记录及上面的
  回读结果。
- **Satelite**：独立发布。按已公开 core SHA `9856b0496` 对齐依赖、`versionName`、递增
  `versionCode`、`coreCommit`，核对双 ABI APK、签名和校验和；它不在主仓 Release 的附件里。
