# 2026-11-08 v0.1.44 发布后专题刷新

本次按 `REFRESH.md` 将已发布的 Provider、路径选择、Apple/TUN 与 CI/Release 增量并入对应模块。它是专题刷新；2026-10-31 的 v0.1.34 广泛生产基线不移动，未逐条重审的旧模块继续按旧基线加 Git 增量使用。

## 固定目标与增量

| 项 | 已核对的值 |
| --- | --- |
| 旧专题目标 | Provider 初始目标 `08bb7d67d3cefc0793058227b69e0481c30ae333`；上一个公开版 v0.1.43 剥离到 `0d63a7a16dd488d952b4d899ebc1a2542cfb7532`。 |
| 新专题目标 | `v0.1.44^{commit}` = `3b6ec64f7145a2add99656d47caaa4f22a1cc478`，与本轮 `testing` 候选一致。 |
| 旧／新广泛生产基线 | 均为 `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`；其余旧模块没有全量复审。 |
| 发布证据 | [Kernel release 42391992312](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391992312) 全部必需作业成功；[公开内核 Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.44) 非 draft、非 prerelease，8 项附件经 API 回读。自动发布时正文为空，发布后补写本版更新说明，当前正文非空。独立的 [Satelite Release APK 42393794665](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/42393794665) 构建与发布成功；[公开客户端 Release](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.44) 5 项附件经 API 回读。 |
| 子仓固定值 | Apple `e3283104c984d60cdf2a0250b47b7fc446e71ba6`，Android `c7837107dc0126fb704fdf08cf203e10b30c8a22`，Desktop `41722b030ea481523290d932a1d81177f8c13f32`；`release-freeze.py verify` 与父仓 gitlink 一致。 |

`git log v0.1.43..v0.1.44` 粗扫 17 个提交、55 个变更路径。分类：PRODUCTION（Provider 更新、出站图、组、route、TUN、QUIC 初始化）、TEST（确定性交错、race、根包、回环）、BUILD/RELEASE（Apple ABI pin、版本、source lock、契约检查）、VENDOR/SUBMODULE（Apple gitlink）、DOC（配置、指南、知识）。重点详读 `3a2e5f54c`、`d38bfc3e5`、`15814e7d1`、`5291924af`、`8c1e804ea`、`3b6ec64f7` 及 Apple `e3283104` 的相关 diff；未逐个深读纯文档提交。没有把这些提交归为新上游合并，历史上游基线未改。

## 本次校准的结论

| 子系统 | 当前结论与证据 | 模块 |
| --- | --- | --- |
| Provider 与路径选择 | 只有确实等待 Provider 发布的空组可在启动期标记 pending；动态节点和组成员发布前检查当前图、跨 DNS/出站环；订阅下载不得依赖自身尚未发布的节点。`VERIFIED_SOURCE`、针对性交错与 Box 测试、正式运行的 `VERIFIED_CI`。 | [Provider](modules/outbound-provider.md)、[路径选择](modules/path-selection-validation.md) |
| 替换与拨号所有权 | 同标签旧节点等组重建与最后一个租约释放后关闭；`AcquireOutbound` 在同一临界区确认对象仍可租用并加计数。失败轮次保留旧对象；selector/urltest 有界重读，route 明确拒绝已失效叶子。`VERIFIED_SOURCE`、确定性交错测试、正式 race 作业。 | [Provider](modules/outbound-provider.md)、[不变量](INVARIANTS.md) |
| Apple/TUN | TUN 边界将 IPv4-mapped 回环地址按真实族拆分；Apple 新会话重置接管标志，等待不持服务生命周期锁，stop/新 start 使旧等待失效。`VERIFIED_SOURCE`、Go/Swift 测试、正式 Apple 构建；真机仍为 `NEEDS_DEVICE`。 | [Apple 自签与回环](modules/apple-selfsign-loopback.md)、[子仓库](modules/child-repositories.md) |
| CI/Release | preparation、Verify、Release contract 在候选 SHA 上成功；正式构建、stage、publish 成功。首次 preparation 的 Apple ABI pin 阻断、GITHUB_TOKEN 推送后缺少自动门禁、Verify 信号丢失测试及 `grep -v | grep -q` 的 SIGPIPE 误判，均按新提交重新冻结与构建处理。公开元数据已回读，未额外下载逐字节复算。 | [CI 与发布](modules/ci-release.md)、[验收索引](ACCEPTANCE.md) |
| Satelite Android | 客户端 tag 剥离到 `22ba907d62c23f09272117134a04e3bc96962520`，`versionCode=55`，`coreCommit` 等于内核正式 SHA；tag push 的运行 `42393794665` 报告 `signed-by-user`、双 ABI bundle OK，公开 5 项附件经 API 回读。`VERIFIED_CI`，没有本机验包或设备安装。 | [CI 与发布](modules/ci-release.md)、[发布交接](../docs/releases/RELEASE-0.1.44-HANDOFF.md) |

旧的 v0.1.39、v0.1.42 交接及 2026-11-07 Provider 专题仍保留各自的时间与源码范围；本轮在 `BASELINE.md`、`README.md` 和模块顶部标明后续目标，不重写历史。验收索引原称 DNS 下载引导环未检测，与后续源码矛盾，已纠正。`COMMIT-INDEX.md` 只加入解释当前行为的关键修复。与现行 `AGENTS.md`、索引、source lock、配置文档和 Release 指引核对后，相关路径与目标 SHA 一致。

## 未覆盖边界

- 没有访问真实订阅、启动本机客户端或做设备测试；Provider 的线上互操作、iPhone/iPad 的 NE route/reload、SideStore 续签仍需各自实测。
- Satelite Android APK 已在独立仓库验收；内核 Android AAR 成功本身不构成其发布证据，客户端运行与设备安装仍未实测。
- 广泛旧基线后的其他子系统未在本专题逐条核对，因此不声称全分支知识完全刷新，也不移动全局生产基线。

```text
KNOWLEDGE_REFRESH = COMPLETE (V0.1.44_SCOPED)
OLD_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_SCOPED_TARGET = 3b6ec64f7145a2add99656d47caaa4f22a1cc478
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
DELTA_CLASSIFIED = YES
AFFECTED_SUBSYSTEMS_REAUDITED = YES (SCOPED)
COMMIT_PROVENANCE_UPDATED = YES (SCOPED)
INVARIANTS_REAUDITED = YES (SCOPED)
ACCEPTANCE_UPDATED = YES
HISTORICAL_DOCS_RECONCILED = YES (SCOPED)
CROSS_FILE_CONSISTENCY = PASS (SCOPED)
FUTURE_AI_INCREMENTAL_READ = PASS (SCOPED)
```
