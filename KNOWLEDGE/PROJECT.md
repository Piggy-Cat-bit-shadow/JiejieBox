# 项目增量（Project delta）

本知识库是一份**分支增量指南**，而不是 sing-box 百科。上游的文档
已经回答了“sing-box 是什么”；本仓库回答的是 *这个分支改了什么、为什么改、哪些陷阱
是真实的，以及如何在不重读 1,834 个提交的情况下继续*。

## 这个分支是什么（What this fork is）

这是 `SagerNet/sing-box` 的一个分支，包含大量加固、证据与特性工作。下面的
提交主题普查是历史性的，针对 `e4b7e692..a267e99` 测得，不是对当前每个提交
作者身份或影响的分类：

| | 数量 | 占比 |
|---|---|---|
| `fix` 提交 | 538 | 29.3% |
| `test` 提交 | 316 | 17.2% |
| `docs` 提交 | 282 | 15.4% |
| `ci` + `build` + `chore` + `style` + `bench` | 204 | 11.1% |
| **流程/证据小计**（`test` + `docs` + `ci` 组） | **802** | **43.7%** |
| `feat` 提交 | 76 | 4.1% |
| `perf` 提交 | 37 | 2.0% |
| 其他 / 无标签主题 | 381 | 20.8% |

各行划分了这 1,834 个主题；流程/证据行是其中三行的小计，不是第四条不相交的行。
百分之四的特性对应百分之四十四的证据与验证，这就是本项目的形态：工作
投入在让既有行为可证明、有边界且可回滚上。

## 它增加了什么——承重的增量（What it adds — the load-bearing deltas）

| 领域 | 本分支做了什么 | 模块 |
|---|---|---|
| **启动期证明** | `common/physicalpath` + `common/routeflow`：报告能力警告，并在不拨号、不解析、不选择、不消耗轮换的情况下拒绝致命的结构性/承诺失败 | `path-selection-validation.md` |
| **运行时协调** | `common/runtimecoord`：一个单调的网络代次、基于身份的重建租约、一个合并窗口，以及前台/自动探测的区分 | `core-lifecycle-runtime.md` |
| **电源 / 设备轴** | `common/power`：平台**事实**输入，策略输出；跨越暂停/恢复边界的三档唤醒后复用区间 | `power-mobile-lifecycle.md` |
| **上传调度器 + 流量类别** | `common/trafficsched`、`common/trafficclass`、`route/dial_governor.go`。`traffic_scheduler` 与 `traffic_class` **在上游不存在** —— 使用其中任一项的配置无法被官方 sing-box 加载 | `data-path-optimization.md` |
| **Direct Offload** | 可证明等价的直连流可以离开用户态 —— 但有一条诚实的限制：该判定**仅**在 Linux `auto_redirect` 中构成数据面绕过 | `data-path-optimization.md` |
| **MASQUE / CONNECT-IP** | 在上游基础上做了大量数据面工作：DNS 分配、bootstrap/inner resolver 拆分与 PREF64；注意初始大小与硬上限的区别 | `masque-connectip.md`, `mtu-and-geometry.md` |
| **Native Naive** | 一个带 UoT、padding、masquerade、预认证限制与目标 ACL 的 NaiveProxy 服务端入站；受构建标签门控的客户端出站已在此集成，并使用分叉的 `cronet-go` | `naive-http-transport.md` |
| **HTTP 传输** | `transport/http` 是本分支最大的单项新增：在 `0067b03` 时有 97 个 `.go` 文件和 149 个提交（在 `a267e99` 时为 90 个文件 / 137 个提交） | `naive-http-transport.md` |
| **负载均衡组 + 有界故障转移** | 分支独有的 `type: loadbalance`；故障转移需显式开启，**整个流跨嵌套只共享一份重试预算** | `data-path-optimization.md`, `path-selection-validation.md` |
| **缓冲区几何** | `common/bufgeom`：可池化与分配上限规则，附带一个 `//go:build race` 拆分 | `mtu-and-geometry.md`, `buffer-ownership.md` |
| **产品管线** | Apple 单一来源 pin + 品牌与身份拆分；Android AAR/ABI；Windows 核心产物；一个失败即拒绝的发布验收门禁，它重新校验字节而不是重新构建 | `libbox-platform-contract.md`, `build-pruning-release.md`, `ci-release.md` |

本分支创建的包（已验证其在上游祖先 `e4b7e692` 处不存在）：`common/physicalpath`、
`common/routeflow`、`common/runtimecoord`、`common/power`、`common/trafficsched`、`common/trafficclass`、
`common/bufgeom`、`common/badh2`、`dns/transport/group`、`internal/memmetrics`、`transport/v2rayxhttp`、
`protocol/vless/encryption`，以及 `protocol/group/loadbalance*.go`、`route/failover.go`、
`route/bypass_verdict.go`、`common/dialer/profile.go`、`adapter/outbound/cross_kind_cycle.go`、
`adapter/fakeip_issuance.go`、`option/traffic_scheduler.go`、`option/v2ray_xhttp.go`、`scripts/ci/`、
`docs/fork/` 和 `test/jiejie/`。

## 它刻意不改变什么（What it deliberately does NOT change）

这一点与增量同样重要，因为它限制了未来的 AI 可以假设什么：

* 在所检查的代码树中，**没有单独的产品专用注册表或能力允许列表（capability allowlist）**。上游
  变更仍然必须被吸收，而目标专用的构建标签决定了一个二进制包含什么。
* 旧的键/文件普查是针对历史修订做的；它并不证明当前每一个
  上游配置都能加载，也不证明后续变更中没有删除任何上游源码。
* 在所检查的目标上，主模块中没有 `quic-go/masque-go` 或 `quic-go/connect-ip-go` 依赖。
  `go.mod` 确实包含 Caddy 的 `certmagic` 与 `zerossl` 模块，所以说“没有 Caddy 依赖”
  是不正确的。
  CONNECT-IP 是自行实现的；Naive 运行在 `x/net/http2` + `h2c` + 本分支的 `quic-go` 之上。
* **gVisor 作为协议栈已退役，但并未从模块图中移除** —— `go.mod` 仍以
  `// indirect` 携带它，而旧的 `replace` 是刻意保留的注释。

## 历史对比中记录的两处接口变更（Two interface changes recorded in the historical comparison）

| 接口 | 在 `e4b7e692` 时 | 在 `a267e99` 时 | 提交 | 原因 |
|---|---|---|---|---|
| `experimental/libbox.DeprecatedNote.Message()` / `MessageWithLink()` | 返回 `string` | 返回 `*StringBox` | `eef6bafff` | 裸 `string` 会把 Go 指针放进 gomobile 的打包结果帧 → `fatal error: bulkBarrierPreWrite: unaligned arguments`，且不可恢复。记录在 `docs/fork/libbox-abi-contract.tsv`。 |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | 服务器域名这一事实必须传到 adapter |

> 不要假设上游 libbox 是可直接替换的（drop-in replacement）。这一历史配对并不是后续 API 变更的
> 穷尽清单；例如平台接口的 `UseProcFS` 方法后来被移除了。在修改目标时，检查当前的接口与客户端绑定。

## 历史来源普查（Historical provenance census）

旧普查在 `e4b7e692..a267e99` 中计得 1,834 个提交；仅凭可达性无法确定
哪个作者写了某个补丁，尤其是在上游 rebase 之后。请使用
实际的 blob 与补丁历史来确定单个来源，并让可信度受证据范围约束。

## 当前发布身份（Current release identity）

知识基线是 `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`，其
`release/JIEJIE_VERSION` 为 `0.1.34`，而附注标签 `v0.1.34` 剥离后指向它（标签对象
`e2dad0bf040`；该标签从 `origin` 获取，并为本次审阅做过核验）。非 draft、
非 prerelease 的 [v0.1.34 GitHub Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)
发布于 2026-10-29 05:34:42 UTC；v0.1.33（`a0bbf666d`）是它的祖先。在做出当前发布声明之前，
请独立地重新核对分支、标签和 Release；仅凭 `release/JIEJIE_VERSION` 不构成发布声明。校准台账记录了
这些检查与证据局限。
