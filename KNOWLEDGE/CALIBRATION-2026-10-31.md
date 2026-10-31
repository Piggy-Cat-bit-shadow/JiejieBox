# 知识校准 — 2026-10-31（Knowledge calibration）

> 同一天还有一次完整的二次盘点，记录在
> [`CALIBRATION-2026-10-31-REVIEW.md`](CALIBRATION-2026-10-31-REVIEW.md)。它不移动基线，
> 只修正过时的锚点/计数，并记录一个未决的 NXDOMAIN/`rewrite_ttl` 问题。

## 范围（Scope）

上一个生产基线 `dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07` 到目标
`0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`：12 个可达提交（含 docs）。目标正是已发布的
非草稿、非预发布 [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)
注解 tag 的 peel 结果，发布于 2026-10-29 05:34:42 UTC。观测到的分支头
`a4cd8b1b12b2ff9dbecf7a17c2a1824053f061a4` 在目标之后只增加了 README、changelog 和说明文档。
当前仓库 API 识别为 `Piggy-Cat-bit-shadow/JiejieBox`；现有远端 URL 是旧地址的重定向。
Upstream/testing `e5d91fa79f6319d5de940d6c81d81f0fe53b9476` 是目标的 merge-base，不是作者身份证明。

本次增量包括 Apple 子仓库 gitlink 从 `16cf87b4` 更新到 `044e6d5`、ABI pin 和 source lock 变更、版本与发布证据，以及 Apple 共享 action 的拓扑、构建、打包测试和结构检查。此前尚未纳入的 Apple 候选已合入 `main` 和父仓库，相关候选说明与历史验收记录也已更新。

涉及的职责和跨模块边界见 `modules/child-repositories.md`、`modules/libbox-platform-contract.md`、`modules/ci-release.md` 和 `ACCEPTANCE.md`。本次增量没有父仓库 Go 运行时源码变更；2026-10-29 与 2026-10-28 校准记录保留各自的 SHA 和限制，未受影响的源码结论沿用先前证据。

## 子仓与依赖审计（Child and dependency audit）

观测时，Apple `main` `044e6d5`、Android `main` `c7837107`、Desktop `main` `6d4ccf8`、
Dashboard `main` `5e9271e5`、sing `dev` `1729220c`、sing-tun `dev` `8b9a3800`、sing-quic `main`
`1ef2597c`、quic-go `sagernet` `2daa76f5`、uTLS `main` `b63a2e9a` 与 Cronet `main` `c9c0e6ec`，
全部匹配已记录的消费 pin。受控的 Cronet 原生归档仍单独 pin。Apple 先前的 sync 分支已不在分支列表中。
分支观测可能在此审查之后独立移动。Apple 集成源码与 symlink blob mode 已检查；本地客户端工作树
保持先前的干净 revision，没有被 reset。

## 证据与限制（Evidence and limits）

* 本地 `check-release-workflows.py`：164 passed；`release-freeze.py verify` 对可用的 pin 通过，
  由于 Desktop checkout 缺失，Desktop/dashboard 为 SKIPPED。验证器的版本不匹配属于 note，不是 failure。
* 本地 `check-libbox-abi.sh --pin-only` 对当前的 Apple 与 Android gitlink 通过；这不是完整的 ABI 扫描。
  本地 `check-apple-source-selection.sh`：72 passed。
* 本地 `test-apple-beta-publish.sh`：99 passed，2 failed（Windows 上没有 macOS 的 `ditto`）；因此
  install/copy 断言属于 BLOCKED_BY_ENVIRONMENT，不是本地通过。先前的 macOS CI run 是独立证据，
  不是重新执行这次 Windows 测试。
* 先前的 [Verify 40819594893](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819594893)
  和 [iOS Apple run 40819615363](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819615363)
  在 `0067b03` 成功，[Release 40824078971](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40824078971)
  也在该 SHA 成功。Apple run 使用子仓 `044e6d5`，但跳过了 macOS job。更早的
  [macOS run 40776674407](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40776674407)
  在父仓 `ed0f3dc` 成功；它没有运行 iOS。run 成功只限定于那些 job 和 SHA。
* 本次刷新没有执行真实设备、互操作、原生归档或独立的发布产物字节检查。继承下来的 Windows TUN errno
  与平台限制继续适用。

## 刷新状态（Refresh status）

```text
KNOWLEDGE_REFRESH = COMPLETE (reviewed source and CI scope; device limits remain)
OLD_PRODUCTION_BASELINE = dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
DELTA_CLASSIFIED = YES
AFFECTED_SUBSYSTEMS_REAUDITED = YES
COMMIT_PROVENANCE_UPDATED = YES
INVARIANTS_REAUDITED = YES
ACCEPTANCE_UPDATED = YES
HISTORICAL_DOCS_RECONCILED = YES
CROSS_FILE_CONSISTENCY = PASS (link, path and section audits)
FUTURE_AI_INCREMENTAL_READ = PASS
```
