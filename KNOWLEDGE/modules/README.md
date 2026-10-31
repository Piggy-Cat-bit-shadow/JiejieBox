# 模块地图 —— 哪个任务该读哪个文件（Module map — which file to read for which task）

本表将常见任务映射到对应模块。按需阅读即可；各模块记录了相关源码位置、来源和证据范围。

## 任务 → 模块（Task → module）

| 如果你正在修改…… | 阅读 |
|---|---|
| 子仓库、客户端 gitlink、嵌套 dashboard 与 fork 依赖变更 | `child-repositories.md` |
| 端点本地流量、`JudgeHostFlow`、`HostPort` | `masque-connectip.md` |
| 发布源码冻结、源码锁定、Apple 单运行器 | `ci-release.md` |
| `experimental/boxdd/**` 中的任何内容、Windows 服务、每用户 worker、命名管道对端握手、对端/可执行文件认证、Authenticode 或签名者信任、更新路径 | `windows-daemon-security.md` |
| Box 启动/关闭顺序、`Scope`、资源清理、网络 epoch、空闲资源退役、`route/reference.go` | `core-lifecycle-runtime.md` |
| 启动期校验、`detour`、selector/urltest/loadbalance、`PhysicalPath`/`ControlPath`、故障转移、环 | `path-selection-validation.md` |
| DNS 路由/缓存、FakeIP、`domain_resolver`、DNS Demand、DNS 泄漏、网络切换期的 DNS | `dns-demand.md` |
| `adblock` DNS 规则动作 —— 用本地 NXDOMAIN 拦截广告域名，它为什么不是 `reject`/`predefined`，以及为什么不写入客户端负缓存 | `dns-adblock-action.md` |
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

表中模块应与索引一起维护。如果路径失效，应检查知识库是否遗漏了相应更新。

## fork 自己的词汇，一览（The fork's own vocabulary, at a glance）

本项目保留上游的注册表、协议与构建档位，同时维护以下主要扩展：

- **启动期校验**：`common/physicalpath` 和 `common/routeflow` 报告能力警告，并在不拨号、不解析、不选择的前提下拒绝致命的结构性失败或承诺失败。
- **运行时协调**：`common/runtimecoord` 维护单调递增的网络 generation 和基于身份的重建租约；`common/power` 将平台状态转为睡眠、唤醒和连接复用策略。
- **流量优化**：`common/trafficsched` 和 `common/trafficclass` 管理上传调度与流量类别；**Direct Offload** 为可证明语义等价的直连流提供操作系统承载路径。
- **协议与传输**：`transport/masque` 实现 MASQUE/CONNECT-IP 数据面并区分内层解析器和引导解析器；Native Naive 入站支持 UoT、填充、伪装和预认证限制；`transport/http` 提供 HTTP/2 和 HTTP/3 传输。
- **客户端与发布**：构建管线覆盖 Apple 客户端源码解析及品牌身份、Android AAR/ABI 和 Windows 核心产物。独立的严格发布验收检查器会复核证据与字节，但发布路径不调用它；实际发布门禁及限制见 `ci-release.md`。

## fork 创建的包 —— fork 点之下并不存在的代码（Fork-created packages — the code that did not exist below the fork point）

用 `git ls-tree` 比对 `e4b7e692` 与 `a267e99` 确认，这些确实是全新的：

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

MASQUE 与 Naive **不是 fork 的发明**：`MASQUE support` 由 `b83089ec`（即 "Add MASQUE support", 世界, 2026-09-21）
引入，而 `transport/http`/`protocol/naive` 在 `e4b7e692` 时就已存在。
fork 在它之上新增了什么，按模块记录，并附溯源。

## 其他所有权与覆盖限制（Additional ownership and coverage limits）

| 领域 | 覆盖与限制 |
|---|---|
| **`experimental/boxdd/**` —— Windows 守护进程**（55 个 `.go` 文件 —— 51 个非测试 —— 外加一个 `.proto`） | **OWNED —— `windows-daemon-security.md`。** 此前这里把它登记为“最大的缺口”。它覆盖守护进程/服务/worker/对端认证这一面：命名管道握手、可执行文件路径与同一文件解析、签名者字节相等固定、Authenticode 允许清单、服务安装与更新器。它**不是**上游代码，也**不会**被 `libbox-platform-contract.md` 顺带覆盖 —— 后者记录的是面向客户端的 ABI，不是守护进程的授权模型。**跨模块：** `daemon/**`（进程内核心 API）→ `libbox-platform-contract.md`；`cmd/internal/build_boxdd` 与打包 → `build-pruning-release.md` + `ci-release.md` §5；`clients/desktop/**` 是独立仓库。 |
| `option/schema.go` → `docs/schema.json`（生成的产物） | 链条是：`option/schema.go` 与各选项结构体 → `schema.Generate`（`cmd/sing-box/cmd_schema.go:36`） → `make schema`（`Makefile:35-36`） → 受跟踪的 `docs/schema.json`。它漂移过一次 —— 不在 `fast_path` 上（该键出现 83 次），而在 `User` 上：产物宣告的是 `Username`/`Password`，选项结构体携带的却是 `json:"username"` / `json:"password"`，于是按已发布 schema 写配置的消费方，写进去的是解析器不接受的键。修复方式是重新生成该产物，并新增 `cmd/sing-box/cmd_schema_artifact_test.go`，它把 `schema.Generate` 的输出与受跟踪文件精确比对；`verify.yml` 在规范标签下运行它。生成契约现在可以执行了：某个选项结构体的改动一旦改变 schema，测试就会失败，不会一直留到有人手工执行 `make schema`。消费方仍在仓库之外（`docs/configuration/schema.md`、`docs/JIEJIE-SERVER.md`、一个编辑器 schema URL），这正是该门禁比对字节、而不是信任文档的原因。证据：`docs/schema.json`、`cmd/sing-box/cmd_schema_artifact_test.go`、`.github/workflows/verify.yml`；复检 `go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestTrackedSchemaMatchesTheGenerator ./cmd/sing-box`。 |
| `protocol/vless/encryption` | 客户端边界已在 `xhttp-client-transport.md` 里覆盖；线级互操作仍单独取证。部分 ABI 检查带 `//go:build !race` 拆分。 |
| 各协议的具体线格式（shadowsocks、trojan、tuic、hysteria2……） | 属于上游。fork 为特定行为打了补丁；这些补丁在负责该行为的模块内部标出，不按协议逐个记录。 |
| Apple / Android / 桌面**客户端**源码 | 独立的 Git 子模块，初始化情况各不相同。本次校准期间 Apple 与 Android 都在各自的 gitlink 处存在，桌面端未初始化。`libbox-platform-contract.md` 界定了客户端相关发现的范围，以及 Windows 符号链接限制。 |
| Clash API / V2Ray API 内部实现 | 属于上游，fork 特有的共存说明见 `build-pruning-release.md`。 |

## 如何判断一个模块是否仍然准确（How to decide whether a module is still accurate）

```sh
git log --oneline <baseline>..HEAD -- <paths the module cites>
```

结果为空，只说明这些路径在该区间内没有变化；它证明不了原始论断正确，也证明不了
固定版本依赖和跨模块行为没变。要针对该论断检查当前的源码与测试。
增量非空时，还要读该模块的 `AI cautions`，把发生变化的路径映射出来，
再决定是否依赖旧描述。

**行号只是建议，符号名才是定位器。** 信任一个 `file:line` 之前，
先重新解析它（用 `git grep -n` 找符号定义，不要搜旧行号）。
2026-10-31 的评审发现，有些表的行号已经整片漂移，
但每个符号名仍然有效 —— 符号仍是可靠的定位图，行号不是。
不要用统一的偏移量去“修正”行号：漂移随文件和插入点各不相同。

**不要**凭记忆或某份报告把旧标签抄进这条命令。标签属于
当前生产基线，记在 `BASELINE.md` 里，而且**会移动**；这里放一个过期的标签，
这项检查就变成了空操作，模块漂移时照样静默通过。（`v0.1.6` 与 `v0.1.9`
之间就是这样：命令里写的是 `v0.1.6`，基线那一行又说什么都没变，
于是没人重跑它。那一行和这条命令必须一起移动。）
