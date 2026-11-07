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
