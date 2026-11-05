# v0.1.39 文档与知识增量校准（2026-11-05）

## 目标、基线与范围（Target, baseline and scope）

| 项目 | 本次记录 |
| --- | --- |
| 仓库 / 唯一产品主线 | `Piggy-Cat-bit-shadow/JiejieBox` / `testing` |
| 本次固定生产目标 | `fc7e400243d46a451b2dffbe68cb5a3147c66289`，即 `v0.1.39^{commit}` |
| 既有广泛知识基线 | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` / v0.1.34 |
| 旧基线到本次目标 | 275 个可达提交，含大量文档；不作为作者身份或独立功能计数 |
| 上一正式产品版本 | v0.1.37；至 v0.1.39 共 157 提交、108 文件、+10,574/−421 行 |
| 当前 Release 状态 | API 核对 v0.1.39 为公开、非 prerelease，8 项附件；v0.1.33 为公开、非 prerelease，7 项附件 |
| 本轮性质 | 显式文档整理、增量源码校准、历史报告吸收；不修改生产源码、依赖 pin、gitlink、工作流或发布产物 |

本次按 `REFRESH.md` 的基线/增量、来源、证据与跨文件检查流程处理受影响主题。**保留旧广泛生产基线**：本轮不是对所有旧模块逐条重新审计后作出的全库基线推进。新增模块和现有模块前置的 v0.1.39 章节明确标注自己的目标，使用者应先读本记录，再按路径计算尚未覆盖的增量。不能再说“旧基线后只有文档”。

## 上游基线应怎样描述（Upstream context）

常规配置参考已包含的 `v1.15.0-alpha.13`；2026-11-03 报告固定观察 upstream/testing `9f1cd1ef75b`，按补丁语义分为已包含、适配吸收、依赖吸收、废弃与暂不吸收。那次 force-update 令简单 behind/ahead 夸大实际增量，不能把 merge-base 或不可达提交当作作者分类。

本次没有声称已完整同步执行时最新上游；首页以“配置参考 + 后续选择性修复”描述。Android auto-redirect 仍受客户端联动限制。报告中“尚不升级 Cronet”已由后续 [patch5 与 Linux DNS 消费记录](reports/release/cronet-patch5-linux-dns-2026-11-05.md) 取代。

## 增量分类与所有权（Delta classification and ownership）

本轮粗扫旧基线到目标的提交主题与变更路径，深读相关语义聚类、当前源码与测试，并独立查询 Release / CI API。没有逐个将 275 个提交称为完整代码评审。

| 分类 / 子系统 | 核对的主要路径或材料 | 当前知识所有者 |
| --- | --- | --- |
| PRODUCTION / TLS、AnyTLS、资源准入 | `common/resourceguard`、`common/tls/client.go`、`protocol/anytls/admission.go`、`route/conn.go`、`trafficclass/context.go` | [resource-admission](modules/resource-admission.md) |
| PRODUCTION / OOM、内存指标 | `service/oomkiller`、`internal/memmetrics`，结合已有资源治理报告 | resource-admission；[power/mobile](modules/power-mobile-lifecycle.md) |
| PRODUCTION / DNS 与设备变化 | `dns/transport` 的连接建立及 message.Copy、`transport/device/stack.go`，OpenVPN/OpenConnect HostPort | [dns-demand](modules/dns-demand.md)、[core lifecycle](modules/core-lifecycle-runtime.md) |
| PRODUCTION / 监听安全 | `common/listener`、`common/netx`、API/debug/pprof/boxdd 调用点 | [listener-security](modules/listener-security.md) |
| PRODUCTION / 协议修复 | Naive CONNECT 验证、服务器解析 pin、XHTTP stream-up 状态与取消、Vision 依赖边界 | [naive-http](modules/naive-http-transport.md)、[xhttp](modules/xhttp-client-transport.md)、[mieru](modules/mieru.md) |
| PLATFORM / 本地回环与命令通道 | `daemon/instance.go`、libbox TUN getters、command socket 解析、TUN warning | [apple-selfsign-loopback](modules/apple-selfsign-loopback.md)、[libbox](modules/libbox-platform-contract.md) |
| VENDOR/SUBMODULE | source-lock、Go replacements、Apple/sing-tun/Cronet 固定源码与独立观察分支头 | [child repositories](modules/child-repositories.md) |
| CI / BUILD / RELEASE | 自签 IPA、单一 Libbox、版本与 source manifest、pseudo-version 校验、附件 flatten 契约 | [ci-release](modules/ci-release.md)、[build](modules/build-pruning-release.md) |
| TEST | 准入账本、压力发布、取消与多跳、Darwin bind、DNS 异步退出、依赖 socketpair 与 cgo/purego | [ACCEPTANCE](ACCEPTANCE.md) 与各模块复查命令 |
| DOC / HYGIENE | 首页、性能表、v0.1.39 与 v0.1.33、历史材料状态与文档入口 | 本记录与 [报告目录](reports/README.md) |

不受本轮生产增量影响的旧模块，沿用旧基线与既有证据限定，不暗中改成当前版本全量认证。

## 子仓库与依赖状态（Pinned children and separately observed tips）

| 对象 | 父仓固定版本 | 2026-11-05 的额外观察 / 本次阅读 |
| --- | --- | --- |
| Apple | `ab575dace8190a48a9d7cf666ef009f803e6ca62` | main 相同；读取 FilePath、ExtensionProvider、HakoProfileOverrideView，验证实际授权发现与回环覆盖 |
| Android | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | 本轮 gitlink 未变；不重新追认全部 Android 设备行为 |
| Desktop | `41722b030ea481523290d932a1d81177f8c13f32` | 本轮 gitlink 未变；nested dashboard `0da341722a8a0046fa5f5b602a19f9b4e1efccc8` 来自 source-lock |
| sing-tun | `8f65451175c085a055a552898758f8594f988239` | dev 相同；读取固定 loopback 测试，保留 UDP NAT stress 的独立层级 |
| cronet-go | root/libs `362e63b14ce2`，all `7ce9c49b464a` | main `4c96192d...`、go `7ce9c49b...`；读取消费提交的 DNS 回复实现；Go 发布与源码分支分别记录 |
| Mieru | v3.39.0 | 历史性能表多为 v3.38.1，Mihomo 产品对照为 v3.38.0；不将旧表包装成 v3.39.0 复测 |

其他未改变的 Go fork 仍以 `release/source-lock.json` 固定值为准；本轮未宣称独立检查其所有分支头。重要固定版本已核对，不使用本地任意子仓 HEAD 替代 gitlink。

## 历史 Markdown 如何吸收与整理（Document disposition）

用户所指的上传材料为仓库内已归档 Markdown，本轮以这些材料为主要补充来源；外部桌面施工提示词不作为完成证据，也不执行其中嵌入的开发指令。

| 文档组 | 处理 | 被吸收的持久知识 / 被取代状态 |
| --- | --- | --- |
| `reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md` | KEEP + ABSORB | 架构、历史工作量及性能条件；原文保留，新增性能导航，不把旧数字改成新测量 |
| `reports/anywhere-round2/`、`anywhere-round3/`、`tun-fast-path/` | KEEP + ABSORB | 规则集扩容、流式完整性、包所有权；进入性能专页并保留负面结果 |
| `docs/fork/mieru-integration.md` | KEEP + ABSORB | 官方/Mihomo/mita 比较的正负数据与旧 SDK 条件；当前 v3.39.0 另行说明 |
| `docs/LOCAL_SIGNING_LOOPBACK_*_REPORT.md` | KEEP + ABSORB | 开关、覆盖与反射契约；“未合并”更新为历史注记，设备未验范围保留 |
| `reports/ios-selfsign-compat-2026-11-04.md` 与 `docs/fork/ios-command-socket-path-fix.md` | KEEP + ABSORB | 三轮路径预算错误、共享容器授权、可观测性与设备反馈；进入新 Apple 模块 |
| `docs/fork/ios-resource-guard.md`、`resourceguard-protocol-coverage.md`、`resourceguard-adversarial-debug.md` | KEEP + ABSORB | 从单 AnyTLS 到共享 TLS、类别接线、多跳许可和陈旧发布修复；报告早期覆盖矩阵标明后续状态 |
| `reports/fork-audit-2026-11-04/` | KEEP | 依赖来源、API 对比缺陷和自签审计证据；不能用当时的分支与旧 pin 替代当前值 |
| `docs/fork/dependency-forks-sync-status.md`、`upstream-merge-2026-11-03.md` | KEEP + SUPERSEDED 注记 | 保留原阶段决策；四平台 Cronet 与 publisher 阻塞现已被五目标发布和新 pin 取代 |
| 旧 Release 正文归档 | KEEP | v0.1.33 原文仍保留，新增详细补写说明；不改旧标签或附件 |

没有为“整洁”删除有复现、失败方案或设备反馈价值的历史文档；通过目录与状态入口消除重复权威。

## 证据、约束与更正（Evidence and corrections）

- `VERIFIED_SOURCE/GIT`：资源准入、默认 MPTCP 策略、Apple 与 Cronet 固定源码、31 模块锁、v0.1.33 仅版本变动。
- `VERIFIED_CI`：本次 API 查询具体 SHA 下运行结论；例：最终标签 [Verify 42007622263](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42007622263)（attempt 2）和 [Release 42017850417](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42017850417) 成功。结论不外推为所有作业、所有设备或全部压力场景通过。
- `VERIFIED_ARTIFACT` 的范围：本次独立核对公开资产名称、大小、存在性与发布状态；没有重新下载全部二进制做字节复验。先前 provenance/哈希测试保留其原始证据层级。
- 历史测量单列在 [性能专页](../docs/PERFORMANCE-COMPARISONS.md)。Mieru TCP 上传、Mihomo 高复用、真实 mita 首字节等负面结果没有隐藏。
- MPTCP 安全背景查一手 Go 与 Xray 资料；明确是应用层缓解，底层 TCP UAF 仍需系统修补。它从 v0.1.37 继承，不列为 v0.1.39 首次新增。
- 无完整 DNS NetLog，不将 fallback 具体内部过程与某个 IP:443 的用途写成完全抓取证实。
- 本地回环的其他栈、SideStore 完整刷新、签名权限组合、iOS footprint/Jetsam 保持设备与实现边界；不为首页宣传抹去限制。

不变量新增了有明确故障依据的准入许可、状态发布与平台覆盖约束；默认额度、路径候选顺序等归类为当前策略，不冻结为协议常量。验收索引补上源码、测试位置、环境和复查入口。

## 如何继续增量阅读（Next incremental reading）

```sh
git diff --name-only 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28..HEAD
# 先扣除本记录已覆盖的语义与文档，不把未覆盖领域当作最新认证
git diff --name-only fc7e400243d46a451b2dffbe68cb5a3147c66289..HEAD
git log --oneline fc7e400243d46a451b2dffbe68cb5a3147c66289..HEAD
```

本记录所在的文档提交不会移动生产目标，也不会改变已发布标签。后续全库刷新要按 REFRESH.md 重新完成旧模块与子仓库的广泛校准后再推进生产基线。

## 本轮文档校验（Documentation checks）

本地检查覆盖 42 个变更 Markdown 文件、306 个可解析的本地文件与章节链接，未发现缺失路径或失效章节；修正了首页原有的英文旧锚点。v0.1.39/v0.1.33 详细页与 CHANGELOG 正文一致，当前 Release 模板与 v0.1.39 详细页一致（CHANGELOG 标题按层级缩进）。`git diff --check` 通过；生产源码、依赖、工作流及 release 配置没有本轮改动。

本轮是文档维护，没有重新执行 Go 套件、原生编译、性能实验或设备验收。发布前验收的历史结果与本次链接/一致性校验分开记载。

```text
TARGETED_CALIBRATION = COMPLETE
TARGET = fc7e400243d46a451b2dffbe68cb5a3147c66289
BROAD_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28 (retained)
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
HISTORICAL_DOCS_RECONCILED = YES
```
