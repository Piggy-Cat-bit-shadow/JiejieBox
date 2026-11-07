# 出站 Provider（Outbound provider）

本模块覆盖 `BASELINE.md` 中 2026-11-07 的 Provider 专题目标。用户字段见[配置参考](../../docs/configuration/provider/index.zh.md)，最小可用配置见[扩展示例](../../docs/FORK-CONFIGURATION.md#出站-provider)。v0.1.42 正式版早于该实现。

## 范围与当前设计（Scope and current design）

| 所有者 | 源码入口 | 职责 |
| --- | --- | --- |
| 配置、注册和 schema | `option/provider.go`、`option/options.go`、`include/registry.go` | 根级 `providers` 与三种类型，严格解码，检查唯一标签、斜杠和互斥字段。Box 与 Libbox 的配置入口都接入注册表。 |
| 内容输入 | `provider/remote/remote.go`、`provider/local/lcoal.go`、`provider/parser/parser.go` | HTTP 异步下载、文件变更重载、内联节点、格式探测及拨号覆盖。网络下载默认限时 30 秒，正文上限 10 MiB。 |
| 应用与生命周期 | `box.go`、`adapter/provider/update.go`、`adapter/outbound/manager.go` | 组先注册更新回调，Provider 发布节点后通知组。更新串行化，按依赖拓扑应用，先启动新对象再替换。依赖引用仍存在时拒绝移除。 |
| 组成员与探测 | `protocol/group/provider_members.go`、`selector.go`、`urltest.go` | 组保留静态成员，按 Provider 缓存成员。selector 保留仍可用的选择；URLTest 重建探测组并关闭旧组，共用历史存储。 |
| 持久化 | `experimental/cachefile/cache.go`、`adapter/experimental.go` | 订阅独立存储桶，SavedBinary v2 带 URL 摘要和 ETag。路径缓存通过同目录临时文件再重命名；恢复路径复用解析、过滤与覆盖逻辑。 |

## 不变量与现行约定（Invariants and conventions）

- 出站 Provider 只发布出站节点。订阅里的 DNS、route、inbounds 等不改写主配置。格式解析器可读取端点，Provider 发布层丢弃端点。
- 标签先处理来源再发布成 `provider/node`。remote 的正则作用于原始节点标签，组的正则作用于最终标签，`exclude` 优先。组的过滤不作用于静态成员。重复标签按出现顺序加后缀。
- 不识别、空输入、完全被过滤的更新不能清空有效成员。跨节点更新不是事务，单节点构造或启动失败保留旧对象。未变节点保留连接的前提是依赖也没有被替换。仍有外部依赖的旧节点保留管理器及所有权记录。
- remote/local 的文档级 detour 只保留可重写的同级节点引用。用户配置的 `override_dialer.detour` 可填补空值。inline 保留对主配置出站的引用。依赖缺失或成环时，拓扑检查在应用前拒绝。
- `use_all_providers` 启动时取全部 Provider，替代显式列表。纯 Provider 组可以从空成员启动。selector 显式 `default` 无匹配且没有可用的优先选择时会报错。
- 只有缓存写入成功后才前移下载 ETag。无已加载文档的 304 不改写路径缓存。解析失败保留现有集合；缓存写入失败时新节点可能已经生效，下次刷新重新取文档。

## 2026-11-07 出站图就绪与动态校验（Graph readiness and dynamic validation）

修复了两个在 v0.1.43 Windows 正式内核复现的缺陷，并补齐了它们暴露的发布顺序问题。以下均为当前源码行为。

### 缺陷与根因

1. **纯 Provider 组作为 detour 目标被误判为断链。** 启动期 dry run 在 `adapter/outbound.Manager.Start`（`StartStateStart`）里运行，早于 provider 组件的 Start；local/remote 的节点要到 provider 自己的 Start 才发布，所以组在 dry run 时是空的。`common/physicalpath/leaves.go` 对“作为依赖、materialise 不出成员”的组返回 `routeContinues`，`dryrun.go` 的 `truncatedRouteFailure` 再把它判成 `FailureStructure`（“this route never reached its far end”）。同一组作为根出站时产生零节点，被有意放过，因此“组单独作为根可以启动”而“作为 detour 不行”。
2. **动态节点闭合成环未被校验。** `lintOutbounds` 只读 `Dependencies()`（对象在选项里声明的标签）；组的已发布成员在 `All()` 里，而 Provider 在 Start 之后才发布。`a.detour = g`（经 `override_dialer.detour`）加上 `g.providers = ["p"]` 时，排序看到 `a → g` 和空组，`Replace` 安装 `a` 时组也还没有 `a`，随后组回调把 `a` 选为成员，形成 `g → a → g` 的无限递归拨号。

### 修复后的语义

| 交界面 | 现行行为 | 锚点 |
| --- | --- | --- |
| 空组判定 | 组实现 `adapter.ProviderBackedGroup`（`ProviderMembership()`）且当前无成员时，作为依赖的空组返回 `routeDeferred`；dry run 记为 `Report.Pending`，`Err()` 仍为 nil，`Reachable()` 为 false（没有已验证的叶子）。没有 provider 供应的空组仍按原逻辑致命。 | `common/physicalpath/leaves.go`、`dryrun.go` |
| 等待期拨号 | selector/urltest 的空组错误点名所等的 Provider（`... waiting for outbound provider <tag> to publish nodes`）。 | `protocol/group/selector.go`、`urltest.go` |
| 节点发布 | `Manager.Replace` 在 prepare-then-swap 的 swap 前，用当前图 + 候选对象校验；失败则释放新对象、保留旧对象。 | `adapter/outbound/graph.go`、`manager.go` |
| 组成员发布 | selector/urltest 的 Start 和 provider 回调都经 `adapter.GroupMembershipPublisher.UpdateGroupMembership`：先校验“把该组替换成新成员”后的图，再在同一把 `graphAccess` 锁内提交，所以两个组并发发布不会各自基于对方旧成员通过。 | `protocol/group/provider_members.go`、`adapter/outbound/graph.go` |
| 环检测图 | 出站侧边集 = `Dependencies() ∪ group.All() ∪ Referrer.References()`；跨 DNS/出站的 `crossKindGraph` 现在也读 `All()`，并在 `ValidateCrossKindCycles` 时保存 transports，供后续动态发布复用。静态排序仍拥有“依赖是否存在”和静态环的消息。 | `adapter/outbound/graph.go`、`cross_kind_cycle.go` |
| 节点移除 | `Remove` 的依赖扫描包含声明依赖、组已发布成员和 DNS transport 的 detour/resolver；组仍持有节点时拒绝移除，provider 记住并在组成员重建后重试。 | `adapter/outbound/manager.go`、`adapter/provider/adapter.go` |
| 关闭顺序 | provider 先于 outbound 组件关闭，组仍持有其节点；provider 关闭时走 `DynamicOutboundReleaser.Release`（跳过依赖守卫，仅用于拆除），因此正常关闭不会因守卫报错。 | 同上 |
| 下载引导环 | 未配置 `http_client` 时使用 Box 的隐式默认 HTTP 客户端，其 dialer 设 `DefaultOutbound=true`，即跟随 `route.final`；显式 `http_client.detour`/`download_detour` 跟随该出站。无缓存/`initial_path` 时，若这条路径只经过“仅由本 Provider 供应且当前无成员”的组，Provider 拒绝启动并点名组与路径。 | `adapter/provider/adapter.go` `CheckDownloadPath`、`provider/remote/remote.go` |
| 文档级外部 detour | remote/local 文档里指向主配置标签的 `detour` 仍被清空（`provider/parser/parser.go` 的既有设计）；要指定主配置出站必须用 `override_dialer.detour`。报告中的“a.detour=g”只有经 override 才进入图。 | `provider/parser/parser.go` |

### 证据与限制

- `VERIFIED_SOURCE`：上述锚点；`VERIFIED_UNIT`：`common/physicalpath`、`adapter/outbound`、`adapter/provider`、`protocol/group` 的新测试；`VERIFIED_INTEGRATION`：根包 `box.New` + `Start` 的 Provider 用例（含 httptest 订阅、真正的异步发布与前缀替换）。
- 复检命令（Windows 标签取自 `release/DEFAULT_BUILD_TAGS_WINDOWS`）：
  `go test -count=1 -tags "$windows_tags" ./common/physicalpath/... ./adapter/outbound/... ./adapter/provider/... ./protocol/group/... ./provider/...`，
  以及 `go test -count=1 -tags "$windows_tags" -run 'Provider|CrossKind' .`。
- race 复检需要 cgo；本次在 Windows 上用 MSYS2 mingw gcc（`CC=C:\msys64\mingw64\bin\gcc.exe`、`CGO_ENABLED=1`）跑过上述包与根包 Provider/CrossKind 子集。Swift 工具链的 clang 不支持 Go race 需要的 `-mthreads`，不能替代。
- **未覆盖**：仅由 DNS 解析形成的下载引导环（Provider 域名由某个 detour 到该组的 DNS 服务器解析）；Provider 节点在发布时不重跑完整能力 dry run，因此 TCP/UDP 不匹配的 Provider 成员不会产生启动期 capability 警告（静态成员会）；provider 节点作为 `fast_path` 组成员的行为未变。以上均未在真实设备或真实订阅上验证。
- Clash API 的 `/providers/proxies` 仍是桩实现（GET 返回空 map，具名一律 404）；普通 `/proxies` 能列出 Provider 节点并切换 selector，`box_clashapi_provider_test.go` 固定这一点。不能把前者当作手动刷新入口。

## 来源与演进（Provenance and evolution）

| 提交 | 分类 | 留下的行为 |
| --- | --- | --- |
| `10296629c` | EXTERNAL-ADAPTED | 提交记录标明移植自 reF1nd/testing 的 `17397d0` 设计，参考 nekolsd 等实现。复用本 fork 的 Scope、动态出站管理、选择状态与配置注册表。具体外部来源见[移植记录](../../docs/fork/outbound-provider.md)。 |
| `3d8c33580` | OWN / hardening | 修补畸形文档 panic、未识别输入的空成功、下载无界、URL 凭据暴露、缓存恢复和 Windows 路径监视。恢复允许静态组重复成员的原行为。 |
| `08bb7d67d` | OWN / hardening | 移除未实现的字段，修正 304、ETag 与缓存写入的顺序，序列化更新，保留被拒移除节点的所有权。 |

## 复验、限制与 AI 注意事项（Verification, limitations and AI cautions）

- `VERIFIED_SOURCE` 范围为本专题 SHA，复验命令见[验收索引](../ACCEPTANCE.md#出站-provider)。Box 集成和 schema 生成检查不证明设备行为或线上订阅的互操作性。
- `override_tls`、`override_anytls` 与 inline `endpoints` 曾声明但不生效，已删除。带有效值的旧键会被严格解码拒绝。deprecated `download_detour` 保留运行时兼容，schema 不再列出。
- Clash API 的普通 `/proxies` 可列出 Provider 节点并选择。独立 `/providers/proxies` 仍无 Provider 管理能力，不能用其当作手动刷新入口。
- Provider 健康检查调用兼容的 `StoreURLTestHistory`，把自定义 URL 的结果也记进默认目标的健康历史。失败使用按标签清理的兼容接口，范围包括该标签的其他探测目标。不能把普通 URLTest 的目标隔离结论套给 Provider，后续若修应测自定义 URL 和失败后的历史隔离。
- Provider 的添加没有改变所选提交的客户端 gitlink 或 native pin。客户端配置 UI 支持、跨平台运行与竞态检查需要独立证据。
