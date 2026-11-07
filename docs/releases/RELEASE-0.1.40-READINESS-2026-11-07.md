# 0.1.40 发布遗漏复核（2026-11-07）

本记录为调查时快照，不是实时状态。目标 `0de1c13dcb355773981c89a13158e532c75f61c3`；直接查询该 SHA 的 Actions runs，而非仅查仓库最近几条。后续执行前重新查询，避免与正在进行的发布任务重复派发。本次没有启动或修改任何发布。

## 范围澄清

用户后续明确：Satelite 和 TF 均可在内核仓库 Release 完成后独立发布，不着急，不阻塞主仓。下表中的 Satelite 状态仅是后续任务快照，不是 0.1.40 被打回的原因。主仓漏项是 Windows 安装器与自签 IPA。

## 本次直接失败

[Release 42326052206](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42326052206) 于北京时间 2026-11-07 09:49:32 在 `stage the Windows desktop installer` 失败：

```text
FAIL: no run of .github/workflows/client-desktop-windows.yml exists for
0de1c13dcb355773981c89a13158e532c75f61c3,
so there is no verified Windows installer to publish.
```

这是漏派发，不是安装器编译失败。Release 不是全平台构建编排器；它消费已有构建。当前 GitHub API 未找到该版本 Release 页面。

补建后，安装器运行 `42326450763` 成功。第二次 Release 运行于北京时间 10:02:06 在自签 IPA 阶段失败，日志确认该 SHA 没有成功的自签运行。 [第二次失败日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42327082763)

## 首次失败后的产物快照

| 应交付资产 / 出口 | 对应产者或生成步骤 | 调查时状态 |
| --- | --- | --- |
| `jiejie-sing-box-linux-amd64-v0.1.40.tar.gz` | `server-linux-amd64.yml` | run `42323578969` success；尚无正式 Release 回读 |
| `jiejie-sing-box-macos-arm64-v0.1.40.tar.gz` | `client-macos.yml` | run `42323582840` success；尚无正式 Release 回读 |
| `jiejie-sing-box-windows-amd64-v0.1.40.tar.gz` | `windows-core-amd64.yml` | run `42323586463` success；不是桌面安装器 |
| `jiejie-sing-box-android-arm64-v0.1.40.tar.gz` | `android-core-arm64.yml` | run `42323466600` success；不是 Satelite APK |
| `jiejie-libbox-apple-0.1.40.tar.gz` | `client-apple.yml` | run `42323575452` attempt 1 success；尚无正式 Release 回读 |
| `Jiejiebox-v0.1.40-windows-x64.exe` | `client-desktop-windows.yml` | 该 SHA 查询不到运行；本次 Release 的直接阻断 |
| `jiejie-ios-selfsign-unsigned-0.1.40.ipa` | `experiment-ios-selfsign.yml` | 该 SHA 查询不到运行；后续仍会缺失，不由普通 Apple client 自动替代 |
| `SHA256SUMS` | Release flatten | 完整集合尚未准备，发布未完成 |
| Satelite arm64-v8a 签名 APK | 独立仓 `release-apk.yml` | 最新运行仍为旧版本；未发现 0.1.40 发布证据 |
| Satelite x86_64 签名 APK | 同上 | 同上；不可遗漏第二个 ABI |
| Satelite `build-info.json`、`apksigner-verify.txt`、`SHA256SUMS.txt` | 同上 | 需与两个 APK 一起验收 |
| TF | Mac 独立任务 | 不在 Windows 正式版执行范围，正式版完成后交接 |

前五个 success 只表示对应运行成功，本调查没有逐个下载 0.1.40 artifact 复验其内容；不能将表格当成产物字节级验收报告。

Satelite 当前观察值：`main=bc4de2c23a3339ab6a2277a7a737b15b42508634`、`versionName=0.1.37`、`versionCode=53`、`coreCommit=ff47b2fe95c6adeb4b538f3d8c8589e839d85b5f`。主仓冻结文件不会替它完成版本或内核对齐，必须在独立仓处理并验证。

## 为什么会连着漏

源码结构把需求分散在三处：

1. `release-artifacts.sh::TARGET_WORKFLOWS` 列出五个核心产者。
2. 安装器由单独的 `cmd_desktop()` 消费；自签 IPA 由 `release.yml` 后面的单独步骤消费。只照五项表派发会同时漏掉这两个。
3. Satelite 在另一个仓库发布，主仓 Release 的通过条件完全不验证它。

首次运行没有到达自签步骤。补建安装器后的第二次运行实际触发了这道阻断。

## 本次应采用的恢复顺序

1. 重新查询当前运行与远端状态，避免另一个发布任务已补建而重复启动。
2. 核对 tag、版本、lock 和候选 SHA，不因本次遗漏直接升版本。
3. 满足身份条件后，Windows 安装器和自签 IPA 可以并行补建；已有合格 Apple Libbox 优先复用。Satelite 留到主仓发布之后独立处理。
4. 所有主仓必需产者及 artifact 完整后，重新进行 Release dry run，再正式发布和回读。复用合格的原 SHA 产物，不因漏派发把全部平台重新构建。
5. 主仓正式发布与回读完成后，Windows 内核发布任务生成交接记录并结束。Satelite 后续对齐依赖、版本和 coreCommit 再发布；Mac 另行接手 TF，两者互不阻塞。

这次证据不要求改源码或废弃 0.1.40；若补建发现新缺陷，再根据是否必须改变冻结源码决定后续版本。
