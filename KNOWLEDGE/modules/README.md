# 模块地图 —— 哪个任务该读哪个文件（Module map — which file to read for which task）

**只**加载你的任务所触及的模块。每个模块都被写成可独立阅读的形式：它
自带源码锚点、溯源信息与证据级别。

## 任务 → 模块（Task → module）

| 如果你正在修改…… | 阅读 |
|---|---|
| 子仓库、客户端 gitlink、嵌套 dashboard 与 fork 依赖变更 | `child-repositories.md` |
| 端点本地流程、`JudgeHostFlow`、`HostPort` | `masque-connectip.md` |
| 发布源码冻结、源码锁定、Apple 单运行器 | `ci-release.md` |
| `experimental/boxdd/**` 中的任何内容、Windows 服务、每用户 worker、命名管道对端握手、对端/可执行文件认证、Authenticode 或签名者信任、更新路径 | `windows-daemon-security.md` |
| Box 启动/关闭顺序、`Scope`、拆卸、网络纪元、空闲退役、`route/reference.go` | `core-lifecycle-runtime.md` |
| 启动期校验、`detour`、selector/urltest/loadbalance、`PhysicalPath`/`ControlPath`、故障转移、环 | `path-selection-validation.md` |
| DNS 路由/缓存、FakeIP、`domain_resolver`、DNS Demand、DNS 泄漏、网络切换期的 DNS | `dns-demand.md` |
| `adblock` DNS 规则动作 —— 用本地 NXDOMAIN 拦截广告域名，为什么它不是 `reject`/`predefined`，以及为什么它不写入客户端否定缓存 | `dns-adblock-action.md` |
| 任何嗅探器、peek/read、嗅探超时、分片 QUIC、嗅探→路由元数据 | `sniff.md` |
| MASQUE/CONNECT-IP、capsule、会话、DATAGRAM 批处理、地址/路由/DNS/PREF64、PTB | `masque-connectip.md` |
| 原生 Naive、HTTP CONNECT 代理、`transport/http`、`http_clients`、目标 ACL、填充/伪装 | `naive-http-transport.md` |
| XHTTP / SplitHTTP / XMUX / stream-one / packet-up —— 编写 VLESS XHTTP 节点、模式选择、连接复用 | `xhttp-client-transport.md` |
| Mieru 入站/出站、TCP/UDP 承载、按需启用的回退与网络重建 | `mieru.md` |
| Direct Offload / native bypass、`traffic_class`、`traffic_scheduler`、拨号调速器 | `data-path-optimization.md` |
| selector 或 urltest 组上的 `fast_path` —— 授权某个组的流尝试 Direct Fast Path，或拒绝该请求 | `fast-path-group-policy.md` |
| 任何零拷贝、内存分配、缓冲区池、批处理所有权、splice 问题 | `buffer-ownership.md` |
| 睡眠/唤醒/息屏/锁屏、内存裁剪、设备轴、`ReuseSuspect`、电源策略 | `power-mobile-lifecycle.md` |
| 某个 libbox 导出、`SetupOptions`、CommandServer API、ABI 版本、Apple/Android 标签集 | `libbox-platform-contract.md` |
| 构建标签、协议裁剪、发布档位、生成器、固定版本 | `build-pruning-release.md` |
| CI 门禁、工作流所有权、发布产物与溯源 | `ci-release.md` |
| MTU、包大小边界、缓冲区几何、低内存档位 | `mtu-and-geometry.md` |

本图中的所有文件在知识版本处都存在；地图与模块是一起提交的。如果这里的某个链接无法解析，
说明知识库已被不一致地编辑过 ——
请把这一点当作缺陷，而不是缺失的文档。

## fork 自己的词汇，一览（The fork's own vocabulary, at a glance）

整个 fork 用一段话概括 —— 然后按你需要的部分去读对应模块。

> 本 fork 保留上游的注册表、协议与构建档位，并新增：一套**启动期
> 证明系统**（`common/physicalpath`、`common/routeflow`），它报告能力警告，并
> 在不拨号、不解析、不选择的前提下拒绝致命的结构性失败或承诺失败；一个
> **运行时协调器**（`common/runtimecoord`），带单一单调网络代次与
> 基于身份的重建租约；一个**电源/设备调速器**（`common/power`），把平台
> 事实转化为睡眠/唤醒/复用策略；一个**上传流量调度器**（`common/trafficsched`）与
> **流量类别**（`common/trafficclass`）；一个 **Direct Offload** 模型，让可证明
> 等价的直连流由操作系统承载；一套完整的 **MASQUE/CONNECT-IP 数据面**
> （`transport/masque`），外加内层/引导解析器拆分；一个**原生 Naive** 入站，带 UoT、
> 填充、伪装与预认证限制；一个重写的 **HTTP/2+HTTP/3 传输层**
> （`transport/http`）；以及面向 Apple（每次构建解析出一份客户端源码，品牌与身份
> 分离）、Android（AAR/ABI）与 Windows（仅核心产物）的**产品化管线**。独立的严格发布验收
> 检查器会重新核验证据与字节，但它并不由发布路径调用；实际的发布路径门禁及其限制见
> `ci-release.md`。

## fork 创建的包 —— fork 点之下并不存在的代码（Fork-created packages — the code that did not exist below the fork point）

以 `git ls-tree` 在 `e4b7e692` 与 `a267e99` 之间比对确认，因此这些确实是全新的：

| 包 | 规模（含测试的 Go 行数） | 模块 |
|---|---|---|
| `common/physicalpath` | 8,543 | `path-selection-validation.md` |
| `common/routeflow` | 340（无测试文件） | `path-selection-validation.md` |
| `common/runtimecoord` | 2,299 | `core-lifecycle-runtime.md` |
| `common/power` | 2,317 | `power-mobile-lifecycle.md` |
| `common/trafficsched` | 5,218 | `data-path-optimization.md` |
| `common/trafficclass` | 310 | `data-path-optimization.md` |
| `common/bufgeom` | 1,071 | `mtu-and-geometry.md`, `buffer-ownership.md` |
| `common/badh2` | 77 | `naive-http-transport.md` |
| `dns/transport/group` | 2,590 | `dns-demand.md` |
| `internal/memmetrics` | 877 | `power-mobile-lifecycle.md` |
| `transport/v2rayxhttp` | 7,181 | `xhttp-client-transport.md` |
| `protocol/vless/encryption` | 历史计数 1,771 | `xhttp-client-transport.md` —— 客户端加密边界 |

## 重度修改的包（Heavily modified packages (present below the fork point, substantially rewritten)）

| 路径 | 区间内新增文件数 | 模块 |
|---|---|---|
| `transport/http` | 共 90 个，fork 点处 20 个 | `naive-http-transport.md` |
| `transport/masque` | 共 57 个，fork 点处 7 个 | `masque-connectip.md` |
| `protocol/naive` | 共 31 个，fork 点处 4 个 | `naive-http-transport.md` |
| `protocol/masque` | 共 29 个，fork 点处 3 个 | `masque-connectip.md` |
| `route`、`adapter`、`dns` | 158 / 77 / 94 个提交 | 若干 |

MASQUE 与 Naive **不是 fork 的发明**：`MASQUE support` 随 `b83089ec`（即 "Add MASQUE support", 世界, 2026-09-21）
引入，而 `transport/http`/`protocol/naive` 在 `e4b7e692` 时就已经存在。
fork 在其之上新增的内容，按模块记录并附带溯源。

## 其他所有权与覆盖限制（Additional ownership and coverage limits）

| 领域 | 覆盖与限制 |
|---|---|
| **`experimental/boxdd/**` —— Windows 守护进程**（55 个 `.go` 文件 —— 51 个非测试 —— 外加一个 `.proto`） | **OWNED —— `windows-daemon-security.md`。** 此前在这里被登记为“最大的缺口”。它是守护进程/服务/worker/对端认证这一面：命名管道握手、可执行文件路径与同一文件解析、签名者字节相等固定、Authenticode 允许清单、服务安装与更新器。它**不是**上游代码，也**不会**被 `libbox-platform-contract.md` 顺带覆盖 —— 后者记录面向客户端的 ABI，而不是守护进程的授权模型。**跨模块：** `daemon/**`（进程内核心 API）→ `libbox-platform-contract.md`；`cmd/internal/build_boxdd` 与打包 → `build-pruning-release.md` + `ci-release.md` §5；`clients/desktop/**` 是独立仓库。 |
| `option/schema.go` → `docs/schema.json`（生成的产物） | 链条是：`option/schema.go` 与各选项结构体 → `schema.Generate`（`cmd/sing-box/cmd_schema.go:36`） → `make schema`（`Makefile:35-36`） → 受跟踪的 `docs/schema.json`。它曾经发生漂移 —— 不是在 `fast_path` 上（该键出现 83 次），而是在 `User` 上：产物宣告的是 `Username`/`Password`，而选项结构体携带的是 `json:"username"` / `json:"password"`，于是按已发布 schema 编写的消费方写入了解析器并不接受的键。修复方式是重新生成该产物，并新增 `cmd/sing-box/cmd_schema_artifact_test.go`，它把 `schema.Generate` 的输出与受跟踪文件精确比对；`verify.yml` 在规范标签下运行它。生成契约现在已经是可执行的：某个选项结构体的改动若改变了 schema，测试就会失败，而不会一直存活到有人手工执行 `make schema`。消费方仍在仓库之外（`docs/configuration/schema.md`、`docs/JIEJIE-SERVER.md`、一个编辑器 schema URL），这正是该门禁比对字节、而不是信任文档的原因。证据：`docs/schema.json`、`cmd/sing-box/cmd_schema_artifact_test.go`、`.github/workflows/verify.yml`；复检 `go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestTrackedSchemaMatchesTheGenerator ./cmd/sing-box`。 |
| `protocol/vless/encryption` | 客户端边界已在 `xhttp-client-transport.md` 中覆盖；线级互操作仍单独取证。部分 ABI 检查带有 `//go:build !race` 拆分。 |
| 各协议的具体线格式（shadowsocks、trojan、tuic、hysteria2……） | 属于上游。fork 针对特定行为打了补丁；这些在负责该行为的模块内部标出，而不是按协议逐个记录。 |
| Apple / Android / 桌面**客户端**源码 | 独立的 Git 子模块；初始化情况各不相同。本次校准期间 Apple 与 Android 都在其 gitlink 处存在，桌面端未初始化。`libbox-platform-contract.md` 界定了客户端相关发现的范围以及 Windows 符号链接限制。 |
| Clash API / V2Ray API 内部实现 | 属于上游，fork 特有的共存说明见 `build-pruning-release.md`。 |

## 如何判断一个模块是否仍然准确（How to decide whether a module is still accurate）

```sh
git log --oneline <baseline>..HEAD -- <paths the module cites>
```

如果它为空，只能说明这些路径在该区间内没有变化；它并不能证明原始论断是正确的，
也不能证明依赖固定版本与跨模块行为没有变化。要针对该论断检查当前的源码与测试。
如果增量非空，还要读该模块的 `AI cautions`，并映射出发生变化的路径，
然后再依赖旧描述。

**行号只是建议性的；符号名才是定位器。** 在信任一个 `file:line` 之前，
先重新解析它（用 `git grep -n` 定位符号定义，而不是去搜索旧的行号）。
2026-10-31 的评审发现，有些整张表的行号已经漂移，
而每个符号名仍然有效 —— 符号仍然是一张正确的定位图，行号却不是。
不要用统一的偏移量去“修正”行号：漂移在不同文件
与不同插入点之间各不相同。

**不要**凭记忆或凭某份报告把旧标签抄进这条命令。标签属于
当前生产基线，它记录在 `BASELINE.md` 中并且**会移动**；此处一个过期的标签会把
这项检查变成一个空操作，在模块漂移时静默通过。（这正是
`v0.1.6` 与 `v0.1.9` 之间发生过的事：这条命令写的是 `v0.1.6`，而基线那一行声称什么都没
变，于是没有人重跑它。那一行与这条命令必须一起移动。）
