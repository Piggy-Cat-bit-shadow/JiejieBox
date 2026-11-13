# Release 与 TestFlight 发布指南（调查稿）

**独立任务入口：[Release 构建指引](RELEASE-WINDOWS.md) / [TF 构建指引](TESTFLIGHT-MAC.md)。**
[0.1.40 逐产物遗漏复核](releases/RELEASE-0.1.40-READINESS-2026-11-07.md)是历史事故材料；当前操作请以本页下方的 0.1.45 增量和两份独立指引核对。

本文件供 AI 在每次发布前阅读。目标是尽早发现问题、减少重复构建和版本号消耗，并保证 **内核仓库正式 Release 的全部契约资产发布且回读验收完成后，才开始 TestFlight 发布**。本文件不授权执行发布；实际发布范围以当次用户任务为准。

## 0.1.45 候选发布链路增量（2026-11-13）

- `testing` 的 0.1.45 专题知识库固定于 `3032ca5`，其后已有合并和文档提交。每次从最新候选重新核对 `release/JIEJIE_VERSION`、source lock、依赖声明、父/嵌套 gitlink、工作流和产物契约；不要把知识库目标当作正式 tag SHA。
- `release/source-lock.json` 证明 pin 与源码一致；`release/pin-runs.json` 及 `check-pin-runs.py` 绑定真实成功运行和同一 pin 集合。`check-knowledge-facts.py` 对齐知识库入口、版本与锁。`kernel-release.yml` 的 freeze 严格检查 source lock、嵌套 gitlink、分支关系和 pin-runs，并上传 `release-pin-report`；Verify 和 Release contract 仍需最终候选同 SHA 的成功记录。
- pin 若前移，preparation 的自动冻结和旧 run 不能替代新 pin 的实际构建。先补真实运行和记录，再复核门禁；`[skip ci]` 的文档提交可避免 push 自动工作流，但正式候选必须显式取得同 SHA 门禁。
- `kernel-release.yml` 的正式运行负责 Linux/macOS CLI、Windows core/安装器、Android AAR、Apple Libbox 与 iOS/macOS/自签 IPA 的同批生产、stage 和 publish。公开资产清单以当前 `release-artifacts.sh` 的契约为准，不能沿用下方 0.1.40 调查表当作今天的事实。内核公开回读后，Satelite APK 和 Mac TF 各自独立继续；TF 从公开 Release 的 Libbox manifest 取固定 Apple 源码。

操作步骤分别见 [Windows Release 指引](RELEASE-WINDOWS.md)和[Mac TF 指引](TESTFLIGHT-MAC.md)；下面的 2026-11-07 表格与失败记录保留为历史调查证据。

## 1. 截止基线与使用方法

本页保存改造前调查证据。当前操作以 Release 和 TF 两份构建指引为准。

本次调查：**2026-11-07，Asia/Shanghai，约 09:30–09:42**。以下是当时观察，不是永久的“最新版本”。

| 对象 | 固定基线 / 本次观察 |
| --- | --- |
| 主仓 | `Piggy-Cat-bit-shadow/JiejieBox`，产品分支 `testing`；旧 `sing-box` URL 有历史记录 |
| 最近已公开正式 Release | `v0.1.39` → `fc7e400243d46a451b2dffbe68cb5a3147c66289`；API 回读非 draft、非 prerelease，8 个附件 |
| 本指南审阅的发布工具源码 | `ea5df162d965fd36d2ed5dc60268a7ef27bbe2d6`，包括冻结 Libbox 链接版本的修复 |
| 本次观察的远端候选 | `0de1c13dcb355773981c89a13158e532c75f61c3`，文件版本 `0.1.40`；相对工具基线只改版本文件和 source lock 的版本字段；当时 Apple 构建未完成，不能称为已发布 |
| 独立 Android App | `Piggy-Cat-bit-shadow/satelite-one`；观察到 main `bc4de2c23a3339ab6a2277a7a737b15b42508634`，最近公开版本 `v0.1.37`；`versionCode=53`，`coreCommit=ff47b2fe95c6adeb4b538f3d8c8589e839d85b5f` |
| TF 证据边界 | 本次未登录 App Store Connect、未在 Mac 签名上传；Apple CI 成功不能证明 TF 已上传或可供测试 |

每次使用先做以下增量调查，再执行后面的步骤：

1. 读取各仓 `AGENTS.md`、知识库基线；核对远端、分支和工作区状态。不要把本机 HEAD、远端分支头、已发布 tag 当成同一个东西。
2. 获取当前远端状态，计算本指南工具基线至目标的 `git log` / `git diff`。对新增或变更的 workflow、composite action、发布脚本、依赖声明、客户端 gitlink、版本和签名配置逐项复核。
3. 重新枚举全部发布出口：主仓 Release、独立 APK 仓库、Apple TF，以及此后新增的包仓库/镜像/安装器等。比较“源码声明的产物”“产者实际上传的产物”“发布端消费的产物”和“用户本轮要求”，不能只沿用旧清单。
4. 输出本轮产物表：产物、平台/架构、来源仓和 SHA、生产 workflow、输入、前置依赖、目标发布位置、校验方式、是否必需。新增、移除和暂不发布的项必须解释；缺失项不得静默略过。
5. 记录新的观察时间、目标 SHA、已核验范围与未知项。正文的基线不可仅因版本变了就整体前移；只对实际重新审阅的部分更新。

建议先生成一份本轮发布记录，后续构建和发布都消费它，避免聊天中多个 SHA、run ID 混用。

## 2. 已发现的产物与依赖关系

以下是基线清单，**不能取代每次重新枚举**：

| 产物 / 出口 | 基线生产路径 | 关键区别 |
| --- | --- | --- |
| Linux amd64 内核 | `server-linux-amd64.yml` | 主仓 Release tar.gz |
| macOS arm64 内核 | `client-macos.yml` | CLI 内核，不是 macOS GUI DMG |
| Windows amd64 内核 | `windows-core-amd64.yml` | 不等于 Windows 桌面安装器 |
| Android ARM64 AAR | `android-core-arm64.yml` | SDK，不是 APK；不能据此宣布 Android App 完成 |
| Apple Libbox | `client-apple.yml` | iOS/macOS 共用冻结来源；正式 Release 提供带 manifest 的 tar.gz |
| Windows x64 安装器 | `client-desktop-windows.yml` | 依赖 core SHA、desktop gitlink、嵌套 dashboard；正式模式要求 tag 身份 |
| iOS 自签 unsigned IPA | `experiment-ios-selfsign.yml` | 名称仍带 experiment，但已是正式发布产物；优先复用同 SHA 的合格 Libbox |
| 主仓校验和 | `release-artifacts.sh` → `SHA256SUMS` | 随完整公开资产集合生成与回读 |
| Satelite APK | 独立仓 `satelite-one/.github/workflows/release-apk.yml` | arm64-v8a / x86_64 签名 APK、build-info、签名验证和校验和；独立版本与 coreCommit |
| Apple GUI CI IPA/DMG | `client-apple.yml` | 构建验证产物；基线时不是主仓那 8 项附件中的独立 GUI 发布包 |
| iOS/macOS TF | Mac 上 `publish-apple-beta.sh` / `release-apple.sh` | 依赖正式版已完成、本机签名条件、精确版本和已验证 Libbox |

主仓公开清单唯一权威是目标提交的 `scripts/ci/release-artifacts.sh::public_asset_names()`，并结合 `release.yml` 的开关。基线启用 `RELEASE_EXTRA_PLATFORMS=true` 和 `RELEASE_SELF_SIGN_IPA=true`。在具有 Bash 的目标检出中可只读生成：

```bash
RELEASE_EXTRA_PLATFORMS=true RELEASE_SELF_SIGN_IPA=true \
  ./scripts/ci/release-artifacts.sh expected-assets --tag "v$VERSION" --version "$VERSION"
```

不要硬编码“必须永远是 8 个”，也不要只比数量；按名称、版本、来源和内容校验。上表是说明，不是给脚本复制的第二套资产契约。

Satelite 不在当前主仓 `release/dependencies.json` 的三个客户端 gitlink 里；其中的 `clients/android` 指向 `sing-box-for-android`，两者不可混同。Satelite 在主仓正式 Release 完成后独立发布，不能漏记后续任务，也不能将其未发布当成主仓不完整。

## 3. 主仓优先、后续任务独立

主仓 Release 完成后才开始后续 Satelite 或 Mac TF 发布。两者互不等待，也不需要为提前启动它们而增加主仓编排复杂度。

- [Release 构建指引](RELEASE-WINDOWS.md)：依赖冻结、完整产物构建、主仓发布、回读，最后生成交接记录并结束任务。
- [TF 构建指引](TESTFLIGHT-MAC.md)：另一次独立任务，读取交接记录并重新核验正式版，随后本机签名、归档、上传与验收。

两者有先后约束，但不是一个自动连续执行的流程。不得在 Windows 发布任务中等待或执行 TF，也不得在 Mac TF 任务中擅自补发正式版。GitHub 上的 macOS runner 构建仍可由 Windows 正式版任务组织。

## 5. 历史失败与跳号：证据和教训

证据来自 Git、历史报告和本次 GitHub API/Actions 回读。历史报告的操作建议不自动成为当前政策，尤其是旧报告中的强移 tag。

| 版本 / 事件 | 已核对原因与边界 | 应前移的检查 / 恢复方式 |
| --- | --- | --- |
| v0.1.7 未成为公开 Release | 历史正式报告记录首轮三平台 `BUILD-INFO` 的 `mobile_tags` 默认值问题，修复 `6cfdc9ace`；后续 macOS 测试失败阻断。报告对“flaky”的判断不能自动用于今天任何同名失败 | 构建记录生成先做跨平台测试；时序失败保留用例和日志，确认性质后重试/修复，不能跳过门禁 |
| v0.1.8 已公开但缺 Libbox | 三套资产清单互不一致，上传和回读都使用同一份错误的 6 项清单，因此 CI 绿色仍缺第 7 项；`3aa1577f4` 修复 | 单一资产权威、按名称比较、draft-first；不能把“CI 绿”视为完整性证明 |
| v0.1.11–15 无公开 Release | v0.1.11 Windows 静默安装挂起；v0.1.12 加诊断；v0.1.13 的 `/SD` 位置写错；v0.1.14/15 继续修安装和卸载。后续确认 MessageBox 没有静默默认按钮，以及卸载返回 0 但服务仍 RUNNING 的产品缺陷 | 发布 tag 前先诊断生命周期；安装、重装、卸载、卸载后重装都验；退出码不能代替状态检查 |
| v0.1.15 多次修复额外失败 | 报告明确记载 `/SD` 放在 flags 中、在顶层 MUI 宏插入只能在 Section/Function 内执行的语句、`${isUpdated}` 展开破坏参数个数；每次浪费约 15 分钟构建 | 先本地/短 CI 做 NSIS 编译和展开检查；诊断改动也要检查。旧报告里允许强移未公开 tag 是当时操作，不照搬到当前不可移动 tag 流程 |
| v0.1.29 缺席正式列表 | Release run `40520844453` 因 macOS 最新运行失败停止；macOS `40526061143`、Apple `40520183444` 的 job annotation 明确是 hosted runner 多次分配失败，伴 macOS arm64 容量提示 | 基础设施失败先原 SHA 重试，勿改版本求解排队问题。到 v0.1.30 之间还包含产品改动和 Android pin 变化；没有充分证据把整个升号决策归为唯一原因 |
| v0.1.38 → v0.1.39 | `42003187723` 在 flatten 报资产契约不匹配；新增自签 IPA 未被旧重复清单接纳；修复 `c7df0cf01`。`fc7e40024` 提交说明明确：旧 tag 已存在，工具修复要新 SHA，因此升至 0.1.39 | 新产物加入时测试实际开关组合和整个 staging 路径；尽量在 tag 前发现工具缺陷 |
| v0.1.38 修复树的另一场失败 | Linux `42004907269` 在 `TestSessionBurstWithoutGuardReproducesTheAmplification` 断言失败 | 与上面的资产缺陷分别记录；本次未证明该测试失败的根因，不能笼统归因网络或当作可忽略 |
| v0.1.39 后的 Apple 版本修复 | `ea5df162d` 让 Libbox 在 tag 尚不存在时也用冻结发布版本链接，并检查 `libbox.provenance`。此修复不在已发布 v0.1.39 源码中 | 检查产物内部版本；不要让 `git describe` 或旧 tag 替代冻结版本；本次只验证修复源码，未验证 v0.1.40 TF 结果 |

历史原文：

- [v0.1.7 报告的固定修订](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/9f67392a27593bf1a74ee11faecc7d78cf883397/V0.1.7_FORMAL_RELEASE_REPORT.md)。
- [Windows gate 6 定位](../KNOWLEDGE/reports/release/PHASE5_GATE6_DIAGNOSIS.md)、[诊断被 tag 身份约束阻断](../KNOWLEDGE/reports/release/PHASE5_BISECT_BLOCKED.md)、[v0.1.15 失误与版本台账](../KNOWLEDGE/reports/release/PHASE5_V0.1.15_HANDOFF.md)、[卸载缺陷实测](../KNOWLEDGE/reports/release/PHASE5_V0.1.15_UNINSTALL_VERDICT.md)。
- [v0.1.29 Release 失败](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40520844453)、[macOS 容量失败](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40526061143)、[Apple 容量失败](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40520183444)。对应 check-run IDs：`122265867137`、`122255773571`；annotation 为 `The job was not acquired by Runner of type hosted even after multiple attempts`。
- [v0.1.38 发布失败](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42003187723)、[修复树 Linux 断言失败](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42004907269)、[v0.1.39 升号原因](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/fc7e400243d46a451b2dffbe68cb5a3147c66289)、[正式发布成功](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42017850417)。
- [Libbox 冻结版本修复](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/ea5df162d965fd36d2ed5dc60268a7ef27bbe2d6)。

## 6. 失败恢复与版本号决策

**失败不等于版本号立即作废；关键是是否需要改变已冻结、已打 tag 的源码或发布身份。**

| 情况 | 操作 |
| --- | --- |
| runner 容量、下载中断、临时服务故障，源码/输入未变 | 保存失败证据后，仅重跑失败任务/工作流；原 SHA 和版本可保留。反复失败需调查，不能无限重试直到绿 |
| 同一 SHA 出现测试断言失败 | 先分类并做有针对性的复现；未证明环境性前按阻塞处理；新 attempt 和旧证据关系写入记录 |
| 尚未打正式 tag，需要改源码/pin/工具 | 保留候选版本可行，但生成新 SHA；重新构建、验证受身份约束的正式产物，旧 SHA 只能作诊断证据 |
| 已存在不可移动的正式 tag，而修复需要新 SHA | 新版本、新冻结、新证据；不得强移 tag 或把新字节塞进旧版本以隐藏差异 |
| 发布上传中断或留下 draft | 先查询实际 Release 状态及已上传字节。确认工具是否支持恢复，避免盲目再 create、删资产或重复上传；若原字节未变，不因传输失败直接升号 |
| Apple run 被重跑 | attempt 属于身份；原跟随进程可能退出，重新核对 run/attempt 和产物。正式 Release 已固定某次库时，不能悄悄换成新 attempt |
| TF 服务端已接收但本地显示超时 | 查 App Store Connect 的版本/构建号/处理状态后决定，不盲目重传 |

节省版本号的主要方法是 **在必需前置检查完成后打 tag 并及时派发安装器、早验证发布工具、优先复用同一 SHA 的合格字节、局部重试**。不要通过删门禁、挪 tag、把 SKIP 当 PASS 达到表面效率。

## 7. 分别记录与交接

Windows 正式版记录与交接模板见 [Release 构建指引](RELEASE-WINDOWS.md)；Mac TF 的独立完成记录见 [TF 构建指引](TESTFLIGHT-MAC.md)。两份记录通过固定版本、SHA 和产物摘要关联，不合并成一个要求两台电脑连续执行的任务。

## 8. 后续可改进项（未在本次实现）

1. 为“内核仓库正式 Release 完成 → 后续 Satelite / Mac TF”提供固定来源的交接记录和前置验证；当前 follow-run 并不强制这个顺序。
2. 保持可在 tag 前运行的候选完整性检查，尤其是 Windows 安装器与新增资产端到端契约；不把诊断产物冒充正式产物。
3. 将产物依赖图、各 workflow 输入和验证方法逐渐机器化，减少 AI 手写清单漂移；保留单一公开资产契约。
4. 自动汇总每轮失败阶段、重试次数和排队/构建耗时，才能用实测安排关键路径。本次没有测出可承诺的总发布耗时。

本次仅调查和编写说明，没有触发构建、创建/移动 tag、发布 Release 或上传 TF，也没有将这些建议冒充已实现的门禁。
