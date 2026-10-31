# 分支知识库（Fork knowledge base）

**知识库 + Git 增量 = 当前 AI 上下文。** 这是一份分支增量（fork-delta）指南，不是 sing-box
百科。上游文档自己就回答了“sing-box 是什么”；本文回答的是 *这个分支改了什么、为什么改，
以及怎么在不重读 1,834 个提交的前提下继续*。

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

这份知识库描述的是已校准的源码基线，不一定与当前 `HEAD` 一致。完整的测量、发布谱系、来源检验和
标签陷阱都在 `BASELINE.md` 里。

**历史 2026-10-28 校准：** 最初的目标是 `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`，
比 v0.1.9 多 791 个提交；最终的生产目标比它晚四个提交。`v0.1.30` 已经发布过，也是最初目标的
祖先；它之后的修复没有进 v0.1.30。后来发布的
[v0.1.31 Release](https://github.com/Piggy-Cat-bit-shadow/sing-box/releases/tag/v0.1.31)
落在更晚的 `cb71cf80d72` 目标上。见 `BASELINE.md`、[校准台账](CALIBRATION-2026-10-28.md)
以及当前源码。

拿历史 v0.1.31 目标和检出的远端 `upstream/testing` 顶端
`e5d91fa79f6319d5de940d6c81d81f0fe53b9476` 求合并基，得到 `a6952ccf6907c18e18917fcb763a433e8e36937b`。
这只是某个时间点上的 Git 祖先关系观察，不能证明作者身份；引用一移动，就得重新测量。

**上一个基线（`a267e99e0`、`v0.1.6`）曾经悄无声息地过期了两个 release** —— 它的增量行
写着“没有任何 `.go` 源码发生变化”，实际上已经有 181 个提交、12 个生产文件落地。这次失败
记在 `BASELINE.md` 里，没有悄悄掩盖：教训在模型本身，不在某一行。

子仓库和它们各自独立的分支顶端也在范围内；见 `modules/child-repositories.md` 和
`REFRESH.md` §25A。

当前的增量和局限记录在 [2026-10-31 校准](CALIBRATION-2026-10-31.md)
和 [全面的第二轮审阅](CALIBRATION-2026-10-31-REVIEW.md) 里。

## 阅读顺序（Reading order）

```text
1. BASELINE.md            what SHA this describes, and how to compute the delta since
2. INDEX.md               route first: task / keyword / source path -> exact module section
3. PROJECT.md             what the fork is and what it deliberately did not change
4. ARCHITECTURE.md        the eleven boundaries worth preserving
5. modules/README.md      the module registry and ownership map
6. the 1-2 modules your task touches
7. INVARIANTS.md          when you are about to change something that looks load-bearing
8. ACCEPTANCE.md          the requirement -> evidence -> re-check index
9. COMMIT-INDEX.md        when you need to find the SHAs behind a behaviour
10. reports/README.md      what was audited, when, and against which tree — records, NOT current claims
```

> `reports/` 保存各次审计和收尾报告。先读其中的 `README.md`：每份报告只描述其审阅时的源码版本。
> 当前结论见 `BASELINE.md`、`INDEX.md` 和 `modules/`；报告与源码不一致时，以源码为准。
> 新增报告不等于刷新知识库，也不会改变生产基线。

**`INDEX.md` 与 `modules/README.md` 不可互换。** `modules/README.md` 是粗粒度的
登记表（哪个模块拥有什么）。`INDEX.md` 则是细粒度的路由器：精确的章节锚点、别名，以及
按源码路径和符号组织的反向索引。

**用不到的模块不要读。** 每个模块都自包含：源码锚点、来源、证据级别都在里面。

## 事实来源优先级（Source-of-truth order）

```text
current source and tests
    > git history and diffs
        > build and CI configuration
            > this knowledge base
                > any other document in the repository
```

本知识库和源码冲突时，**以源码为准**；冲突是要记录的缺陷，不是悄悄抹平的差异。

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

有两条规则，一旦忽视，就得在这个仓库里付出代价：

* **CI 任务被声明过，不等于它跑过。** 一条 `VERIFIED_CI` 主张需要一次具体、可被独立
  检查的运行及其 SHA；Windows 守护进程模块记录了这样一次发布运行。
* **`SKIP` 不等于 `PASS`。** 本分支自己的跳过项就写明了这一点（“NOT a pass”），还有个门禁
  按设计以退出码 0 打印 `PARTIAL`。

## 刷新策略（Refresh policy）

刷新（refresh）是一次显式的维护操作：重新审计源码、Git、测试和 CI，更新模块来源，然后移动
基线。**开发提交不需要修改文档。** 产品代码怎么动，本目录里都没有东西需要改——唯一告诉
读者该看什么的是 `BASELINE.md` 的“post-baseline delta”行。

刷新被 *显式* 请求时，流程走 `REFRESH.md`。那份文件是协议，本节只是策略；正常开发根本不会
动 `KNOWLEDGE/`。

**语言约定：** 正文使用简体中文；`file:line`、符号、测试名、命令、JSON 字段、SHA 和
`VERIFIED_*` 等证据标签保持英文。标题写成“中文（English original）”，既方便中文阅读，也保留
按原英文名路由的能力；fenced code block 不翻译。产品名、协议名和源码中的术语应保留原文；
通用技术词优先采用自然、准确的中文，可能产生歧义时保留英文。润色不得改变原有条件、
证据范围、命令或代码，也不要为了直译而牺牲可读性。

## 模块（Modules）

先用 `INDEX.md` 路由——它带精确的章节锚点和按源码路径组织的反向索引。
下面这张表是粗粒度的所有权地图。

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
| `modules/libbox-platform-contract.md` | 面向客户端的 ABI 表面与平台边界 |
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

**Windows 守护进程（`experimental/boxdd/**`）已经不在这个列表里。** 它曾是最后一个无主的
变更子系统，现在归 `modules/windows-daemon-security.md` 所有——同时还要记下一条同样属于
此处的限制：**它的信任边界没有文档化的端到端测试。** 一个 Windows 测试工作流
在 `.github/workflows/windows-boxdd-tests.yml` 中声明；只有声明并不证明运行过。拥有知识
不等于证明了行为，该模块的 §15 讲了两者的区别。
