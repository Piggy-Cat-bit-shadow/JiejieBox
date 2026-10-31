# 分支知识库（Fork knowledge base）

**知识库 + Git 增量 = 当前 AI 上下文。** 这是一份分支增量（fork-delta）指南，而不是 sing-box
百科。上游自己的文档回答“sing-box 是什么”；本文回答的是 *这个分支改了什么、为什么改，以及如何在不重读 1,834 个提交的前提下继续*。

## 基线——使用其他任何内容之前先读这里（Baseline — read this before using anything else）

| | |
|---|---|
| 仓库 | `Piggy-Cat-bit-shadow/JiejieBox` |
| 产品分支 | `testing` |
| **生产基线** | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` —— 2026-10-31 校准的源码/CI 修订 |
| 发布基线 | **`v0.1.34`**，该标签剥离（peel）后指向生产基线 |
| **历史上游锚点** | `e4b7e692f4545890bfc9d43960328e1449fa803b` —— 供 v0.1.9 时期的对比使用，而非当前的上游合并基（merge-base） |
| 基线之后的生产增量 | 计算 `git diff --name-only 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28..HEAD`；见 `BASELINE.md` |
| 知识审阅时间 | 2026-10-31 |
| 知识修订 | 动态解析：`git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` |

本知识描述的是源码基线，并不自动描述当前的 `HEAD`。完整测量、发布谱系、来源检验以及标签陷阱都在 `BASELINE.md` 中。

**历史性的 2026-10-28 校准：** 最初的目标是 `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`，
比 v0.1.9 多 791 个提交；最终的生产目标比它晚四个提交。`v0.1.30` 是一个已发布的 release，也是
最初目标的祖先；之后的修复不在 v0.1.30 中。稍后一个 [v0.1.31 Release](https://github.com/Piggy-Cat-bit-shadow/sing-box/releases/tag/v0.1.31)
发布在更晚的 `cb71cf80d72` 目标上。见 `BASELINE.md`、[校准台账](CALIBRATION-2026-10-28.md)
以及当前源码。

在历史性的 v0.1.31 目标上，与被检出的远端 `upstream/testing` 顶端
`e5d91fa79f6319d5de940d6c81d81f0fe53b9476` 之间的合并基是 `a6952ccf6907c18e18917fcb763a433e8e36937b`。
这是一次时间点上的 Git 祖先关系观察，不是作者身份的证明；引用移动时需要重新测量。

**上一个基线（`a267e99e0`、`v0.1.6`）曾无声地过期了两个 release** —— 它的增量行
声称“没有任何 `.go` 源码发生变化”，而实际上已有 181 个提交和 12 个生产文件落地。这次失败记录在
`BASELINE.md` 中，而不是被悄悄改正，因为教训关乎模型本身，而不关乎某一行。

子仓库及其各自独立的分支顶端也属于本知识的范围；见
`modules/child-repositories.md` 和 `REFRESH.md` §25A。

当前的增量与局限记录在 [2026-10-31 校准](CALIBRATION-2026-10-31.md)
和 [全面的第二轮审阅](CALIBRATION-2026-10-31-REVIEW.md) 中。

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

> `reports/` 是时间点审计与收尾报告的存档。先读它的 `README.md`：一份报告描述的是
> 它自己日期对应的代码树，`BASELINE.md`/`INDEX.md`/`modules/` 拥有当前的主张，并且
> 当报告与源码不一致时以源码为准。新增一份报告不是一次刷新（refresh），也永远不会移动基线。

**`INDEX.md` 与 `modules/README.md` 不可互换。** `modules/README.md` 是粗粒度的
登记表（哪个模块拥有什么）。`INDEX.md` 是精细的路由器：精确的章节锚点、别名，以及
按源码路径和符号组织的反向索引。

**不要阅读你不需要的模块。** 每个模块都是自包含的：它自带源码锚点、来源与证据级别。

## 事实来源优先级（Source-of-truth order）

```text
current source and tests
    > git history and diffs
        > build and CI configuration
            > this knowledge base
                > any other document in the repository
```

当本知识库与源码冲突时，**以源码为准**——而且冲突是一个需要记录的缺陷，不是要被悄悄调和的差异。

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

有两条规则，一旦忽视，这个仓库会让你付出代价：

* **一个 CI 任务的声明，不是该任务运行过的证据。** 一个 `VERIFIED_CI` 主张需要一次具体、
  可被独立检查的运行及其 SHA；Windows 守护进程模块记录了一次这样的发布运行。
* **`SKIP` 不等于 `PASS`。** 本分支自己的跳过项明确说明了这一点（“NOT a pass”），并且有一个门禁
  按设计以退出码 0 打印 `PARTIAL`。

## 刷新策略（Refresh policy）

刷新（refresh）是一次显式的维护操作：重新审计源码、Git、测试与 CI；更新模块
来源；然后移动基线。**开发提交不需要修改文档。** 产品代码移动时，本目录中没有任何内容
需要改变——只有 `BASELINE.md` 的“post-baseline delta”行告诉读者该看什么。

当刷新被 *显式* 请求时所要遵循的流程是 `REFRESH.md`。那份文件是协议；本节只是策略，正常开发
根本不会更新 `KNOWLEDGE/`。

## 模块（Modules）

先用 `INDEX.md` 路由——它带有精确的章节锚点和按源码路径组织的反向索引。
这张表是粗粒度的所有权地图。

| 模块 | 覆盖范围 |
|---|---|
| `modules/windows-daemon-security.md` | `experimental/boxdd/**`：守护进程/服务/worker 角色、命名管道对端握手、可执行文件与签名者身份、Authenticode 语义、签名者 pin、服务安装、更新路径 |
| `modules/core-lifecycle-runtime.md` | `adapter.Scope`、`Box` 启动/关闭顺序、`common/runtimecoord`、`route.ReferenceManager` |
| `modules/path-selection-validation.md` | `common/physicalpath`、`common/routeflow`、启动期校验（start-time validation）、`detour`、组、环、故障转移 |
| `modules/dns-demand.md` | DNS 传输、代次、缓存提交、FakeIP、消费者需求 |
| `modules/dns-adblock-action.md` | 仅 DNS 的 `adblock` 规则动作：本地 NXDOMAIN + SOA、零上游，以及为什么它既不是 `reject` 也不是负缓存条目 |
| `modules/sniff.md` | sniffer、peek/read 所有权、分片 QUIC、sniff→routing 契约 |
| `modules/masque-connectip.md` | MASQUE 端点与数据面、capsule、会话、DNS 角色、MTU/PTB |
| `modules/naive-http-transport.md` | Native Naive、`transport/http`、`http_clients`、UoT 目标 ACL |
| `modules/xhttp-client-transport.md` | XHTTP / SplitHTTP 客户端契约：`transport/v2rayxhttp`、模式、XMUX 复用、X-Padding |
| `modules/fast-path-group-policy.md` | `fast_path`：组级策略字段、它的三种状态，以及它不绕过什么 |
| `modules/data-path-optimization.md` | Direct Offload、流量类别（traffic class）、上传调度器、拨号调速器（dial governor） |
| `modules/buffer-ownership.md` | 不得被优化掉的缓冲区/零拷贝/splice 规则 |
| `modules/power-mobile-lifecycle.md` | 设备轴、睡眠、屏幕/锁、裁剪、复用档位、定时器普查 |
| `modules/mtu-and-geometry.md` | 三个 MTU 量、包大小边界、`common/bufgeom`、低内存配置档 |
| `modules/libbox-platform-contract.md` | 面向客户端的 ABI 表面与平台边界 |
| `modules/child-repositories.md` | 客户端 gitlink、嵌套 dashboard、分叉的 Go 模块与受控的原生 pin、子分支头 |
| `modules/mieru.md` | Mieru 入站/出站、TCP/UDP 下层、需显式启用的回退与会话生命周期 |
| `modules/build-pruning-release.md` | 构建标签、裁剪、发布配置档、生成器、pin |
| `modules/ci-release.md` | 工作流、门禁、证据，以及不得主张的内容 |

全分支范围：`BASELINE.md`、`INDEX.md`、`PROJECT.md`、`ARCHITECTURE.md`、`INVARIANTS.md`、
`ACCEPTANCE.md`、`PERFORMANCE-FACTORS.md`、`COMMIT-INDEX.md`、`REFRESH.md`。

## 本知识库不覆盖的内容（What this knowledge base does not cover）

上游各协议自身的线格式（wire format）、Clash/V2Ray API 内部实现，以及 Apple / Android /
桌面**客户端**源码——那些是独立的仓库，在干净工作树中表现为空的子模块（submodule）。`modules/README.md` 明确说明了这一点，
而不是留下沉默让人误以为已有覆盖。

**Windows 守护进程（`experimental/boxdd/**`）已不再在这个列表里。** 它曾是最后一个无主的
变更子系统，现在由 `modules/windows-daemon-security.md` 拥有——同时还有一条同样属于此处的诚实限制：
**它的信任边界没有文档化的端到端测试。** 一个 Windows 测试工作流
在 `.github/workflows/windows-boxdd-tests.yml` 中声明；仅有声明并不证明运行过。拥有
知识并不等于证明了行为，该模块的 §15 说明了二者的区别。
