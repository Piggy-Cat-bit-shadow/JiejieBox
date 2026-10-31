# 分支知识库（Fork knowledge base）

**知识库 + Git 增量 = 当前 AI 上下文。** 本知识库重点记录本项目相对于上游 sing-box 的改动、
设计原因和维护时容易踩的坑。它不代替上游文档，也不要求每次接手任务都重新梳理历史提交。

## 基线——使用其他任何内容之前先读这里（Baseline — read this before using anything else）

| | |
|---|---|
| 仓库 | `Piggy-Cat-bit-shadow/JiejieBox` |
| 产品分支 | `testing` |
| **生产基线** | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` —— 2026-10-31 校准的源码/CI 修订 |
| 发布基线 | **`v0.1.34`**，这个附注标签解析到的提交就是生产基线 |
| **历史上游锚点** | `e4b7e692f4545890bfc9d43960328e1449fa803b` —— 供 v0.1.9 时期的对比使用，而非当前的上游合并基（merge-base） |
| 基线之后的生产增量 | 计算 `git diff --name-only 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28..HEAD`；见 `BASELINE.md` |
| 知识审阅时间 | 2026-10-31 |
| 知识修订 | 动态解析：`git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` |

知识库描述的是上表中的源码基线，不一定与当前 `HEAD` 一致。使用前请先检查基线之后的 Git 增量。
发布历史、上游来源的核对方法和标签注意事项见 `BASELINE.md`。

**历史 2026-10-28 校准：** 最初的目标是 `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`，
比 v0.1.9 多 791 个提交；最终的生产目标比它晚四个提交。`v0.1.30` 已经发布过，也是最初目标的
祖先；它之后的修复没有进 v0.1.30。后来发布的
[v0.1.31 Release](https://github.com/Piggy-Cat-bit-shadow/sing-box/releases/tag/v0.1.31)
落在更晚的 `cb71cf80d72` 目标上。见 `BASELINE.md`、[校准台账](CALIBRATION-2026-10-28.md)
以及当前源码。

在当时的 v0.1.31 校准中，目标提交与远端 `upstream/testing` 的观察值
`e5d91fa79f6319d5de940d6c81d81f0fe53b9476` 的 merge-base 是 `a6952ccf6907c18e18917fcb763a433e8e36937b`。
这只反映当时的 Git 祖先关系，不能据此判断作者；分支更新后应重新计算。

此前的 `a267e99e0`（`v0.1.6`）基线曾连续两个版本没有更新。当时文档仍写着没有 `.go` 源码变更，
实际却已新增 181 个提交并修改了 12 个生产文件。`BASELINE.md` 保留了这次失效记录：
静态的增量描述会过期，使用时必须重新计算。

客户端和依赖子仓库及其独立分支也在维护范围内。如何区分父仓库固定版本与子仓库分支头，
见 `modules/child-repositories.md` 和 `REFRESH.md` §25A。

当前的增量和局限记录在 [2026-10-31 校准](CALIBRATION-2026-10-31.md)
和 [全面的第二轮审阅](CALIBRATION-2026-10-31-REVIEW.md) 里。

## 阅读顺序（Reading order）

1. [基线](BASELINE.md)：确认知识库描述的源码版本，并计算基线之后的改动。
2. [索引](INDEX.md)：按任务、关键词或源码路径找到对应章节。
3. [项目概览](PROJECT.md)：了解项目与上游的关系及改造范围。
4. [架构](ARCHITECTURE.md)：了解需要保留的核心边界。
5. [模块地图](modules/README.md)：按职责选择相关模块，再阅读当前任务涉及的文档。
6. [不变量](INVARIANTS.md)：修改关键行为之前，检查必须保持的约束。
7. [验收索引](ACCEPTANCE.md)：查找需求、证据和复检方法。
8. [提交索引](COMMIT-INDEX.md)：追溯行为背后的提交。
9. [历史报告](reports/README.md)：查看审计时间、对象和当时的结论。

> `reports/` 保存各次审计和收尾报告。先读其中的 `README.md`：每份报告只描述其审阅时的源码版本。
> 当前结论见 `BASELINE.md`、`INDEX.md` 和 `modules/`；报告与源码不一致时，以源码为准。
> 新增报告不等于刷新知识库，也不会改变生产基线。

`modules/README.md` 是模块总览，说明每个模块负责什么；`INDEX.md` 是详细索引，
可以按任务、源码路径或符号找到具体章节。根据当前任务选择需要的模块即可。

## 事实来源优先级（Source-of-truth order）

1. 当前源码与测试。
2. Git 历史和差异。
3. 构建与 CI 配置。
4. 本知识库。
5. 仓库中的其他文档。

本知识库和源码冲突时，**以源码为准**；发现冲突时应记录并核实原因，再更新知识库。

## 四种状态标签（The four status labels）

| 标签 | 含义 |
|---|---|
| **VERIFIED** | 直接的源码、历史、测试、CI 声明或产物证据——并且始终指明具体级别 |
| **INFERRED** | 有推理依据的解读，但没有直接锚点 |
| **UNVERIFIED** | 项目没有充分证据 |
| **UNKNOWN** | 在这里根本无法确定 |

证据级别始终带限定：`VERIFIED_SOURCE`、`VERIFIED_GIT`、`VERIFIED_UNIT`、
`VERIFIED_INTEGRATION`、`VERIFIED_INTEROP`、`VERIFIED_CI`、`VERIFIED_ARTIFACT`、`VERIFIED_DEVICE`、
`INFERRED`、`UNVERIFIED`、`UNKNOWN`、`BLOCKED_BY_ENVIRONMENT`。

使用证据时要注意两点：

* 工作流中声明了 CI 任务，不代表任务实际运行或通过。标记 `VERIFIED_CI` 时，应给出可核查的运行记录和 SHA。
* `SKIP` 不等于 `PASS`。部分检查即使退出码为 0，也可能输出 `PARTIAL`；应查看具体检查结果。

## 刷新策略（Refresh policy）

知识刷新是明确发起的维护工作：检查源码、Git、测试和 CI，更新受影响的知识，并在满足条件时
推进基线。普通开发提交不要求同步修改知识库；使用者应根据 `BASELINE.md` 计算基线后的增量。
需要刷新时，按 `REFRESH.md` 执行。

**语言约定：** 正文使用简体中文；`file:line`、符号、测试名、命令、JSON 字段、SHA 和
`VERIFIED_*` 等证据标签保持英文。标题写成“中文（English original）”，既方便中文阅读，也保留
按原英文名路由的能力；fenced code block 不翻译。产品名、协议名和源码中的术语应保留原文；
通用技术词优先采用自然、准确的中文，可能产生歧义时保留英文。润色不得改变原有条件、
证据范围、命令或代码，也不要为了直译而牺牲可读性。

## 模块（Modules）

先在 `INDEX.md` 中按任务或源码路径查找具体章节。下表列出各模块的大致范围。

| 模块 | 覆盖范围 |
|---|---|
| `modules/windows-daemon-security.md` | `experimental/boxdd/**`：守护进程/服务/worker 角色、命名管道对端握手、可执行文件与签名者身份、Authenticode 语义、签名者 pin、服务安装、更新路径 |
| `modules/core-lifecycle-runtime.md` | `adapter.Scope`、`Box` 启动/关闭顺序、`common/runtimecoord`、`route.ReferenceManager` |
| `modules/path-selection-validation.md` | `common/physicalpath`、`common/routeflow`、启动期校验（start-time validation）、`detour`、组、环、故障转移 |
| `modules/dns-demand.md` | DNS 传输、代次、缓存提交、FakeIP、消费者需求 |
| `modules/dns-adblock-action.md` | 只在 DNS 上生效的 `adblock` 规则动作：本地 NXDOMAIN + SOA、零上游，以及它为什么既不算 `reject`，也不算负缓存条目 |
| `modules/sniff.md` | sniffer、peek/read 所有权、分片 QUIC、sniff→routing 契约 |
| `modules/masque-connectip.md` | MASQUE 端点与数据面、capsule、会话、DNS 角色、MTU/PTB |
| `modules/naive-http-transport.md` | Native Naive、`transport/http`、`http_clients`、UoT 目标 ACL |
| `modules/xhttp-client-transport.md` | XHTTP / SplitHTTP 客户端契约：`transport/v2rayxhttp`、模式、XMUX 复用、X-Padding |
| `modules/fast-path-group-policy.md` | `fast_path`：组级策略字段、三种状态，以及它不绕过什么 |
| `modules/data-path-optimization.md` | Direct Offload、流量类别（traffic class）、上传调度器、拨号调速器（dial governor） |
| `modules/buffer-ownership.md` | 不能被优化掉的缓冲区/零拷贝/splice 规则 |
| `modules/power-mobile-lifecycle.md` | 设备轴、睡眠、屏幕/锁、裁剪、复用档位、定时器普查 |
| `modules/mtu-and-geometry.md` | 三个 MTU 量、包大小边界、`common/bufgeom`、低内存配置档 |
| `modules/libbox-platform-contract.md` | 面向客户端的 ABI 接口与平台边界 |
| `modules/child-repositories.md` | 客户端 gitlink、嵌套 dashboard、分叉的 Go 模块与受控的原生 pin、子分支头 |
| `modules/mieru.md` | Mieru 入站/出站、TCP/UDP 下层、需显式启用的回退与会话生命周期 |
| `modules/build-pruning-release.md` | 构建标签、裁剪、发布配置档、生成器、pin |
| `modules/ci-release.md` | 工作流、门禁、证据，以及哪些结论缺少证据 |

全分支范围：`BASELINE.md`、`INDEX.md`、`PROJECT.md`、`ARCHITECTURE.md`、`INVARIANTS.md`、
`ACCEPTANCE.md`、`PERFORMANCE-FACTORS.md`、`COMMIT-INDEX.md`、`REFRESH.md`。

## 本知识库不覆盖的内容（What this knowledge base does not cover）

本知识库不替代上游协议的线格式文档，也不完整覆盖 Clash/V2Ray API 的内部实现。
Apple、Android、桌面客户端及其他关联仓库的关键改动属于维护范围，见
`modules/child-repositories.md`；它们各自的完整源码仍以对应仓库为准。子模块可能尚未初始化，
检查时应区分本地检出、父仓库固定的 gitlink 和子仓库分支头。

Windows 守护进程 `experimental/boxdd/**` 由 `modules/windows-daemon-security.md` 覆盖。
该模块记录的限制仍然适用：其信任边界没有文档化的端到端测试。仓库声明了
`.github/workflows/windows-boxdd-tests.yml`，但工作流存在不代表它已经运行；详见模块 §15。
