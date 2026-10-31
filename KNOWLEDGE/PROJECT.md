# 项目增量（Project delta）

本知识库是**分支增量指南**，不是 sing-box 百科。上游文档已经回答了“sing-box 是什么”；
本仓库回答的是 *这个分支改了什么、为什么改、哪些陷阱是真的，以及怎么在不重读
1,834 个提交的前提下继续*。

## 这个分支是什么（What this fork is）

这是 `SagerNet/sing-box` 的一个分支，做了大量加固、证据和特性工作。下面的提交主题
普查是历史数据，测自 `e4b7e692..a267e99`；它不是对当前每个提交的作者身份或影响的归类：

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

上面各行划分了这 1,834 个主题；流程/证据那一行是其中三行的小计，不是第四条互不相交的行。
百分之四的特性，对应百分之四十四的证据与验证，这就是本项目的形态：力气花在让既有行为
可证明、有边界、可回滚上。

## 它增加了什么——承重的增量（What it adds — the load-bearing deltas）

| 领域 | 本分支做了什么 | 模块 |
|---|---|---|
| **启动期证明** | `common/physicalpath` + `common/routeflow`：报告能力警告；遇到致命的结构性/承诺失败直接拒绝，过程里不拨号、不解析、不改变选择、不消耗轮换 | `path-selection-validation.md` |
| **运行时协调** | `common/runtimecoord`：一个单调的网络 generation、基于身份的重建租约、一个合并窗口，以及前台/自动探测的区分 | `core-lifecycle-runtime.md` |
| **电源 / 设备轴** | `common/power`：平台**事实**输入，策略输出；跨越暂停/恢复边界的三档唤醒后复用区间 | `power-mobile-lifecycle.md` |
| **上传调度器 + 流量类别** | `common/trafficsched`、`common/trafficclass`、`route/dial_governor.go`。`traffic_scheduler` 与 `traffic_class` **上游没有** —— 配置里用了其中任何一个，官方 sing-box 都加载不了 | `data-path-optimization.md` |
| **Direct Offload** | 可证明等价的直连流可以离开用户态 —— 但有一条要说明的限制：这个判定**仅**在 Linux `auto_redirect` 中才构成数据面绕过 | `data-path-optimization.md` |
| **MASQUE / CONNECT-IP** | 在上游基础上做了大量数据面工作：DNS 分配、bootstrap/inner resolver 拆分与 PREF64；注意初始大小与硬上限的区别 | `masque-connectip.md`, `mtu-and-geometry.md` |
| **Native Naive** | 一个 NaiveProxy 服务端入站，带 UoT、padding、masquerade、预认证限制和目标 ACL；受构建标签门控的客户端出站也集成在这里，用的是分叉的 `cronet-go` | `naive-http-transport.md` |
| **HTTP 传输** | `transport/http` 是本分支最大的一项新增：`0067b03` 时有 97 个 `.go` 文件和 149 个提交（`a267e99` 时是 90 个文件 / 137 个提交） | `naive-http-transport.md` |
| **负载均衡组 + 有界故障转移** | 分支独有的 `type: loadbalance`；故障转移要显式开启，**整个流跨嵌套只共享一份重试预算** | `data-path-optimization.md`, `path-selection-validation.md` |
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

这一点和增量一样重要，因为它限定了未来的 AI 可以假设什么：

* 在所检查的代码树里，**没有单独的产品专用注册表，也没有能力允许列表（capability allowlist）**。上游
  变更仍然必须吸收，而一个二进制里包含什么，由目标专用的构建标签决定。
* 旧的键/文件普查针对的是历史修订；它既证明不了当前每一个
  上游配置都能加载，也证明不了后续变更没有删掉任何上游源码。
* 在所检查的目标上，主模块中没有 `quic-go/masque-go` 或 `quic-go/connect-ip-go` 依赖。
  `go.mod` 确实带了 Caddy 的 `certmagic` 和 `zerossl` 模块，所以说“没有 Caddy 依赖”
  是错的。
  CONNECT-IP 是自己实现的；Naive 跑在 `x/net/http2` + `h2c` + 本分支的 `quic-go` 上。
* **gVisor 作为协议栈已经退役，但没有从模块图里移除** —— `go.mod` 仍以
  `// indirect` 带着它，旧的 `replace` 是刻意留下的注释。

## 历史对比中记录的两处接口变更（Two interface changes recorded in the historical comparison）

| 接口 | 在 `e4b7e692` 时 | 在 `a267e99` 时 | 提交 | 原因 |
|---|---|---|---|---|
| `experimental/libbox.DeprecatedNote.Message()` / `MessageWithLink()` | 返回 `string` | 返回 `*StringBox` | `eef6bafff` | 裸 `string` 会把 Go 指针塞进 gomobile 的打包结果帧 → `fatal error: bulkBarrierPreWrite: unaligned arguments`，而且无法恢复。记录在 `docs/fork/libbox-abi-contract.tsv`。 |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | 服务器是不是域名这个事实，必须传到 adapter |

> 别以为上游 libbox 可以直接替换（drop-in replacement）。这组历史配对不是后续 API 变更的
> 完整清单；比如平台接口的 `UseProcFS` 方法后来就被移除了。改目标时，先看当前接口和客户端绑定。

## 历史来源普查（Historical provenance census）

旧普查在 `e4b7e692..a267e99` 里数出 1,834 个提交；光看可达性，定不了某个补丁是谁写的，
上游 rebase 之后尤其如此。要定单个来源，请看实际的 blob 和补丁历史，并让可信度受
证据范围约束。

## 当前发布身份（Current release identity）

知识基线是 `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`，它的
`release/JIEJIE_VERSION` 是 `0.1.34`，附注标签 `v0.1.34` 剥离后指向它（标签对象
`e2dad0bf040`；这个标签从 `origin` 取回，并为本次审阅核对过）。非 draft、
非 prerelease 的 [v0.1.34 GitHub Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)
发布于 2026-10-29 05:34:42 UTC；v0.1.33（`a0bbf666d`）是它的祖先。在做出当前发布声明之前，
请自行重新核对分支、标签和 Release；光凭 `release/JIEJIE_VERSION` 不算发布声明。校准台账
记下了这些检查和证据局限。
