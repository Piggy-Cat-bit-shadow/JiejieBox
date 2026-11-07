# 2026-11-07 出站 Provider 专题刷新

本次从 v0.1.39 校准目标粗扫到 `testing` 专题目标，共 24 个提交。详读出站 Provider 移植及两次加固提交 `10296629c`、`3d8c33580`、`08bb7d67d`，核对注册、配置、schema、订阅更新、动态出站、组集成与缓存。发布差异按统一编排、Apple pin 与 ABI 校验、Satelite 交接分组，实际发布结果以 [v0.1.42 交接记录](../docs/releases/RELEASE-0.1.42-HANDOFF.md)为准。

| 项 | 本轮记录 |
| --- | --- |
| 原广泛生产基线 | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`。保留，旧模块未在本轮全量复审。 |
| 前次增量目标 | v0.1.39，`fc7e400243d46a451b2dffbe68cb5a3147c66289`。 |
| 最新公开内核 Release | v0.1.42，远端附注标签剥离到 `9856b04964b5b137e83bfed4d4a3ebcadb934ac0`。 |
| 新专题源码目标 | `08bb7d67d3cefc0793058227b69e0481c30ae333`。Provider 功能已推送到 `testing`，未包含在 v0.1.42。 |
| 上游观测 | 远端 SagerNet `testing` 为 `9f1cd1ef75b7999bb118a9322a003f6edce2752d`。此值是分支头观测，历史上游锚点保留。移植来源采用提交与移植台账的记录，未作外部逐文件归因。 |
| 子仓与依赖 | 专题目标与 v0.1.42 的客户端 gitlink、source lock 相同。与 v0.1.39 相比 Apple 前移到 `0c7b10be`。远端 Apple、Android、Desktop、Dashboard 跟踪头与各自 pin 相符，sing、sing-tun、sing-quic、quic-go、uTLS 跟踪头也与锁定提交相符。Cronet 源码 `main` 观测为 `4c96192d73f67c48a91cba3bce358a3c91692444`，绑定包根模块、`all` 与 native 继续按发布锁定值解释。Provider 增量把 `gopkg.in/yaml.v3 v3.0.1` 从间接依赖改为直接依赖。 |
| 知识改动 | 新增 [Provider 模块](modules/outbound-provider.md)，同步索引、提交导航、架构、验收与不变量。CI、子仓模块增加本轮适用的修订说明。 |
| 配置改动 | 补根级示例，修正双语参考中互斥字段、种子加载、过滤阶段、默认成员和健康历史的说明。现有生成 schema 复验一致。 |
| 历史资料处置 | 保留原移植设计和 v0.1.39 校准，标明已推送后的专题入口。保留 v0.1.42 的发布交接作为该版本证据。 |

Windows amd64 本地用 Go 1.25.5、CGO 关闭复验 Provider、出站组、option、cachefile 和 Box/Clash API 集成测试。按 canonical server profile 验证生成 schema，并用实际构建的 CLI 检查三份根级示例。Android Core ARM64 在专题 SHA 的 [42342195901](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42342195901) 显示成功，其证据范围是 Android core 构建。[Verify 42342195845](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42342195845) 与 [Mobile contract 42342196001](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42342196001) 也在同一 SHA 上回读为成功，范围分别为其 CI 检查与移动构建契约。本轮未重跑跨平台、race、线上订阅或设备测试。Provider 自定义健康检查 URL 的历史归属限制已在模块中列出。

```text
KNOWLEDGE_REFRESH = COMPLETE (OUTBOUND_PROVIDER_SCOPED)
OLD_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_SCOPED_PROVIDER_TARGET = 08bb7d67d3cefc0793058227b69e0481c30ae333
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
DELTA_CLASSIFIED = YES
AFFECTED_SUBSYSTEMS_REAUDITED = YES (PROVIDER_SCOPE)
COMMIT_PROVENANCE_UPDATED = YES
INVARIANTS_REAUDITED = YES (PROVIDER_SCOPE)
ACCEPTANCE_UPDATED = YES
HISTORICAL_DOCS_RECONCILED = YES (PROVIDER_AND_RELEASE_SCOPE)
CROSS_FILE_CONSISTENCY = PASS (CHANGED_SCOPE)
FUTURE_AI_INCREMENTAL_READ = PASS
```
