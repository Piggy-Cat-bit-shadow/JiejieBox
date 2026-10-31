# 知识校准 — 2026-10-29（Knowledge calibration）

## 目标与范围（Target and scope）

上一个生产基线 `cb71cf80d7224a51823b455f0be5959ad8556933` → 源码/CI 目标
`dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07`：125 个可达提交、76 个变更路径（含知识库历史）。
观测到 `testing` 分支头为 `8500bf9c7527cab65fd8612ceef638b3b6b2f928`；
经 `5468bf4dd74fbc47510b572fc2313299f9c3582e` 的后续提交只改 `docs/fork`，不移动生产基线。
已发布的 v0.1.33 peel 到 `a0bbf666dec8693450c0e8363ed42db3294ac64a`，是该目标的祖先。
远端 upstream/testing `e5d91fa79f6319d5de940d6c81d81f0fe53b9476` 是目标的 merge-base。

生产增量：MASQUE/WireGuard/Tailscale 的 endpoint-local 路由、TUN routed-port 发布、共享 flow logger；
测试增量：host verdict 与构造函数覆盖；依赖增量：sing-tun 与 uTLS 替换；构建/发布增量：源码冻结清单、
lock、工作流与测试；平台与 CI 增量：三个客户端 gitlink、ABI pin、Apple input/composite/single-runner
工作流与发布门禁。历史 v0.1.31 校准仍覆盖未变模块的范围；它的缺口与限制不会因为这次增量审查而消失。
注册表/索引、不变量、发布、MASQUE、构建、libbox、验收与提交导航已按受影响的边界更新。子仓库与 Go fork
变更已按固定对象和观测到的分支头单独复核；见 `modules/child-repositories.md`。模块映射已盘点，
未受影响模块的声明保留先前的校准，没有被说成刚刚重新证明。

## 检查与限制（Checks and limits）

* `go test ./adapter` 和聚焦的 `TestJudgeHostFlow` 在本地通过（Go 1.26.10）；范围更大的
  route/protocol 命令没有执行完就被中止，因此在完整套件层面记为 NOT_RUN。单元测试不能证明真实 TUN
  或端到端流量投递。
* `release-freeze.py verify --lock release/source-lock.json`：可用的 pin 为 PASS；Desktop 的嵌套
  dashboard 检查因 desktop 未检出而 SKIPPED。这项检查不证明分支头是最新的，也不证明产物内容正确或
  发布已就绪。
* 冻结 fixture 套件 35/35、工作流结构门禁 159/159、Apple input 套件 13/13；本地
  `check-libbox-abi.sh --pin-only` 对父仓的 Apple 与 Android gitlink 通过。完整客户端 ABI 扫描、
  Apple 构建、设备与产物检查都没有运行。
* 目标上既有的 Verify run 40765511913 已被取消；不要据此推断 CI 全绿。本次刷新没有派发新工作流。
* 父仓远端 ref 已 fetch 并快进，客户端工作树没有变化。Apple 与 Android 检出干净但仍停留在较早的
  revision；desktop 未初始化。因此父仓状态显示两个被修改的 gitlink 工作树。远端 submodule
  自动拉取报告 Android dev 与 desktop refs 不可用；这两者都不是父树损坏的证据。
* 先前校准中的 Windows TUN errno 限制、部分历史 Apple 扫描，以及其他未解决的源码/设备/平台限制
  继续适用，除非有独立证据取代它们。

重复校准的方法：盘点注册表与归属映射，比较 baseline 到 target 的路径，检查每个受影响归属的当前源码
与可执行测试，再检查索引链接/锚点，并独立核实发布事实。证据按 revision 和环境限定。

## 刷新状态（Refresh status）

```text
KNOWLEDGE_REFRESH = COMPLETE (source calibration; runtime/device limits above remain open)
OLD_PRODUCTION_BASELINE = cb71cf80d7224a51823b455f0be5959ad8556933
NEW_PRODUCTION_BASELINE = dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07
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
HISTORICAL_DOCS_RECONCILED = YES (preserved as historical)
CROSS_FILE_CONSISTENCY = PASS (link, path and section audits)
FUTURE_AI_INCREMENTAL_READ = PASS (entry points, baseline and child-repo routing)
```
