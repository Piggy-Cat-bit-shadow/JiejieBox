# 项目增量（Project delta）

本知识库重点说明本项目相对于上游 sing-box 的改动：改了什么、为什么改，以及维护时有哪些
容易出错的边界。通用的 sing-box 功能请同时参考上游文档。

## 这个分支是什么（What this fork is）

本项目基于 `SagerNet/sing-box`，增加了功能、加固和验证。下表统计的是历史区间
`e4b7e692..a267e99` 的提交标题，不代表当前每个提交的作者或实际影响：

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

这 1,834 个标题按上表类别统计；“流程/证据”是其中测试、文档和 CI 等类别的小计，不能再
作为独立类别相加。这组历史数据说明项目投入了较多测试和验证工作，但不能替代对具体改动的
源码审查。

## 主要改造（What it adds — the load-bearing deltas）

| 领域 | 本分支做了什么 | 模块 |
|---|---|---|
| **启动期证明** | `common/physicalpath` + `common/routeflow`：报告能力警告；遇到致命的结构性错误或无法满足的要求时直接拒绝，过程里不拨号、不解析、不改变选择、不消耗轮换 | `path-selection-validation.md` |
| **运行时协调** | `common/runtimecoord`：一个单调的网络 generation、基于身份的重建租约、一个合并窗口，以及前台/自动探测的区分 | `core-lifecycle-runtime.md` |
| **电源 / 设备轴** | `common/power`：平台**事实**输入，策略输出；跨越暂停/恢复边界的三档唤醒后复用区间 | `power-mobile-lifecycle.md` |
| **上传调度器 + 流量类别** | `common/trafficsched`、`common/trafficclass`、`route/dial_governor.go`。`traffic_scheduler` 与 `traffic_class` **上游没有** —— 配置里用了其中任何一个，官方 sing-box 都加载不了 | `data-path-optimization.md` |
| **Direct Offload** | 可证明等价的直连流可以离开用户态 —— 但有一条要说明的限制：这个判定**仅**在 Linux `auto_redirect` 中才构成数据面绕过 | `data-path-optimization.md` |
| **MASQUE / CONNECT-IP** | 在上游基础上做了大量数据面工作：DNS 分配、bootstrap/inner resolver 拆分与 PREF64；注意初始大小与硬上限的区别 | `masque-connectip.md`, `mtu-and-geometry.md` |
| **Native Naive** | 一个 NaiveProxy 服务端入站，带 UoT、padding、masquerade、预认证限制和目标 ACL；受构建标签门控的客户端出站也集成在这里，用的是分叉的 `cronet-go` | `naive-http-transport.md` |
| **HTTP 传输** | `transport/http` 是本分支改动较多的模块之一：`0067b03` 时有 97 个 `.go` 文件和 149 个提交（`a267e99` 时是 90 个文件 / 137 个提交） | `naive-http-transport.md` |
| **负载均衡组 + 有界故障转移** | 分支独有的 `type: loadbalance`；故障转移要显式开启，**整个流跨嵌套只共享一份重试预算** | `data-path-optimization.md`, `path-selection-validation.md` |
| **缓冲区几何** | `common/bufgeom`：可池化与分配上限规则，附带一个 `//go:build race` 拆分 | `mtu-and-geometry.md`, `buffer-ownership.md` |
| **产品管线** | Apple 客户端固定版本及品牌与身份拆分；Android AAR/ABI；Windows 核心产物。发布路径会核验已有产物；独立的严格验收检查器是否接入发布路径，须按 `ci-release.md` 区分 | `libbox-platform-contract.md`, `build-pruning-release.md`, `ci-release.md` |

本分支创建的包（已验证其在上游祖先 `e4b7e692` 处不存在）：`common/physicalpath`、
`common/routeflow`、`common/runtimecoord`、`common/power`、`common/trafficsched`、`common/trafficclass`、
`common/bufgeom`、`common/badh2`、`dns/transport/group`、`internal/memmetrics`、`transport/v2rayxhttp`、
`protocol/vless/encryption`，以及 `protocol/group/loadbalance*.go`、`route/failover.go`、
`route/bypass_verdict.go`、`common/dialer/profile.go`、`adapter/outbound/cross_kind_cycle.go`、
`adapter/fakeip_issuance.go`、`option/traffic_scheduler.go`、`option/v2ray_xhttp.go`、`scripts/ci/`、
`docs/fork/` 和 `test/jiejie/`。

## 它刻意不改变什么（What it deliberately does NOT change）

了解哪些内容没有改变，同样有助于判断本项目与上游的边界：

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

> 不要假设上游 libbox 可以直接替换本项目的版本。上述历史对照并未列出后续全部 API 变更，例如平台接口的 `UseProcFS` 后来已移除。更换目标版本前，应核对当前接口及客户端绑定。

## 历史来源普查（Historical provenance census）

历史普查在 `e4b7e692..a267e99` 中统计了 1,834 个提交。Git 可达性不能单独证明补丁的
作者，尤其是在上游重写历史后。需要确定来源时，应结合实际 blob、补丁和可信的历史记录。

## 当前发布身份（Current release identity）

知识基线是 `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`，它的
`release/JIEJIE_VERSION` 是 `0.1.34`，附注标签 `v0.1.34` 剥离后指向它（标签对象
`e2dad0bf040`；这个标签从 `origin` 取回，并为本次审阅核对过）。非 draft、
非 prerelease 的 [v0.1.34 GitHub Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)
发布于 2026-10-29 05:34:42 UTC；v0.1.33（`a0bbf666d`）是它的祖先。在做出当前发布声明之前，
请自行重新核对分支、标签和 Release；光凭 `release/JIEJIE_VERSION` 不算发布声明。校准台账
记下了这些检查和证据局限。
