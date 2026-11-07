# Outbound Provider

The first implementation and its hardening are on `testing` after v0.1.42. For the current
contract and verification, see [the provider knowledge module](../../KNOWLEDGE/modules/outbound-provider.md).
This page retains the original design and port history.

An outbound provider publishes nodes from a document outside the configuration - a subscription URL, a
local file, or the configuration itself - into the same outbound namespace the static `outbounds`
use. Groups name the provider instead of listing every node, and a refresh changes their membership
without a restart.

- Configuration reference: [Provider](../configuration/provider/)
- Ported from: `reF1nd/sing-box`, branch `reF1nd-testing` at `17397d0226` (2026-10-23), GPL-3.0-or-later.
- Additional references: `nekolsd/sing-box` at `0807e43e3e` (2026-09-14, same design lineage and
  provider tests); `ChuckPeng/sing-box-y` at `7b10b8186e` (v1.11.7, the earlier form of the same
  feature); `PuerNya/sing-box` branch `outbound-providers` at `ed89310d18` (2024) and
  `yelnoo/sing-box` at `73fc036b32` (1.13.11) for update/connection-drain and file-watch ideas. The
  XiReiki provider documentation has no public source and was used as documentation evidence only.

## Scope

Stage 1 ships:

- `remote`, `local` and `inline` providers;
- the full document parser set (`sing-box` JSON, Clash YAML, SIP008, raw/base64 links);
- cache via `path`/`initial_path` or the `cache_file` subscription bucket, with ETag revalidation;
- include/exclude filtering, duplicate-tag renaming, sibling-detour rewriting;
- the dialer override (`override_dialer`);
- optional health check writing into the shared URL-test history;
- `providers` / `use_all_providers` / `include` / `exclude` on selector and urltest.

Deliberately out of stage 1, and rejected rather than silently ignored:

- provider **endpoints** (WireGuard/Tailscale). The parsers still read them; the provider drops them
  because an endpoint owns a tunnel and a platform handover, which is a different lifecycle. The
  inline provider has no `endpoints` field.
- `override_tls` / `override_anytls`. The option types are not declared, so a configuration using
  them fails to decode with an unknown-field error.

## Architecture

| Layer | Files | What it owns |
| --- | --- | --- |
| Options | `option/provider.go` | Provider envelope decoded through `ProviderOptionsRegistry`; `remote`/`local`/`inline` options; `override_dialer`; schema definition. |
| Registry | `adapter/provider/registry.go` | `provider.Register[Options]`, option construction and provider construction. |
| Manager | `adapter/provider/manager.go` | Provider namespace: create/remove, per-stage `StartContext`, ordered close. Registered on the Box context as `adapter.ProviderManager`. |
| Shared adapter | `adapter/provider/adapter.go`, `update.go` | Owns the published node list, the update callbacks and the health check; `UpdateNodes` is the topological, prepare-then-swap apply. |
| Concrete providers | `provider/remote`, `provider/local` | Download + cache + refresh; local file + fswatch reload; inline nodes. |
| Parser | `provider/parser` | Format detection and node decoding through the outbound options registry. |
| Dynamic outbounds | `adapter/lifecycle_group.go`, `adapter/outbound/manager.go` | `LifecycleGroup` per-component scopes; `DynamicOutboundManager` with `Replace`/`Remove`; `Create` unchanged for static configuration. |
| Groups | `protocol/group/provider_members.go`, `selector.go`, `urltest.go` | Member collection (static + provider nodes, filtered), callback registration and rebuild. |
| Cache | `adapter/experimental.go`, `experimental/cachefile/cache.go` | `LoadSubscription`/`SaveSubscription` in their own `subscription` bucket. |
| Assembly | `box.go`, `include/registry.go` | Manager construction and context registration, provider construction from options, start/close order. |

### Lifecycle and start order

`provider` starts after `outbound` and `http-client` in the same stage. A provider therefore loads its
cache and publishes nodes while the outbound manager is already running, and the manager's
`Replace` starts each new node through every stage the manager has reached. Groups started before the
provider register a callback at their own `Start`, and the provider's first `UpdateGroups` is what
gives them their initial provider membership. Provider `PostStart` starts the refresh loop and, when
there was no cache, the first download on a background goroutine: a Box start never waits for a
subscription URL.

### Update semantics

`UpdateNodes` builds the dependency graph (detour and group members) and applies it in topological
order:

1. an unchanged node keeps its live object (to a group, a provider refresh that only reorders entries
   is a no-op);
2. a changed node is constructed and started before the previous object is unlinked, so a
   constructor or `Start` failure leaves the working node in service;
3. a node whose options did not change but which names a replaced node is rebuilt, because its
   `detour` dialer caches the object it resolved;
4. nodes the document no longer names are removed in retry passes, dependent first; the manager
   refuses a removal while a live node still declares the tag.

### Failure behaviour

- No cache and no reachable URL: the Box starts; the provider has zero nodes; a group that names it
  starts with no members and fails a dial with `outbound group <tag> has no available member yet`.
  The next refresh retries.
- Cache exists: the cached document is loaded before the Box finishes starting, and a later download
  failure keeps it.
- One node fails to construct: it is skipped, the rest of the document is applied, and the provider's
  published set keeps the previous object for that tag.

## Verification

Run with the project's tag profile (`release/DEFAULT_BUILD_TAGS_OTHERS`):

```bash
go build ./...
go vet ./adapter/... ./provider/... ./protocol/group/ ./option/ ./schema/ ./experimental/cachefile/
go test ./adapter/provider/... ./provider/... ./protocol/group/ ./option/ ./experimental/cachefile/
go test . ./cmd/...
```

Also regenerated and checked: `docs/schema.json` (`go run ./cmd/sing-box schema -o docs/schema.json`),
and the repository layout gate (`cmd/internal/modlayout`) after adding `provider` to `SourceRoots`.

Test coverage added or adapted:

- `provider/parser`: the full format matrix (Clash, link, SIP008, raw/base64, sing-box JSON) with the
  upstream fixtures; a hostile-document suite (null/non-object nodes, wrong types, invalid UTF-8,
  deep nesting) and `FuzzParseSubscription`, which must never return nodes together with an error;
- `provider/remote`: URL validation and redaction, request timeout, body-size bound, non-2xx handling,
  last-known-good preservation across parse/status failures, cache round-trip through the same
  pipeline as a download (filters, tag rename), corrupt-cache refusal, close during an in-flight
  download, and idempotent close;
- `provider/local`: reload on file change (including a forward-slash path on Windows), invalid edit
  and deleted file keep the published set, constructor validation;
- `adapter/provider`: the hot-update contract - unchanged node and its live connection survive,
  changed node is replaced, failed replacement keeps the old node, removed node is unlinked, chain
  ordering and cycle rejection, deterministic duplicate-tag renaming, empty-apply refusal, static-tag
  collision refusal, a 1500-node apply bound, and provider-manager lifecycle;
- `protocol/group`: selector/urltest membership follows provider updates, selection survives a
  refresh that keeps the member and falls back when it disappears, include/exclude on rendered tags,
  provider-only groups start empty, and a repeated member declaration stays legal (route resolution
  and loadbalance define what it means; a group constructor must not refuse it);
- `box_provider_test.go`: end-to-end - an inline provider publishes nodes, a selector and a urltest
  see them, the selector's configured default wins, a group naming an undeclared provider refuses the
  start, a subscription's `dns`/`route`/`inbounds` never reach the main configuration, and two
  providers with a colliding node tag stay distinct;
- `box_clashapi_provider_test.go`: `/proxies` lists provider nodes and group members and
  `PUT /proxies/{selector}` selects a provider node;
- `experimental/cachefile`: the subscription bucket round-trips nodes, ETag and URL hash across a
  reopen; `adapter.SavedBinary` refuses unknown record versions.

At the time of the port: not covered by a test was a live HTTP subscription download inside a Box
(the remote provider is unit tested, and the proxy manager path it uses is covered by the
dynamic-update suite). Real device traffic through a provider node is `BLOCKED_BY_ENVIRONMENT` here.
The race detector was also `BLOCKED_BY_ENVIRONMENT`: this host had no C toolchain, so `go test
-race` could not run; concurrency was covered by targeted interleaving tests (close during download,
repeated close, update versus close) and by the absence of shared mutable state between providers.
The 2026-11-07 record below supersedes both statements.

## 2026-11-07 修复记录（graph readiness and dynamic validation）

v0.1.43 正式内核上复现并修复了两个跨模块缺陷，并补齐了它们暴露的发布顺序问题。详细语义、证据级别
与限制以 [KNOWLEDGE/modules/outbound-provider.md](../../KNOWLEDGE/modules/outbound-provider.md)
的 2026-11-07 一节为准；这里只记录改了什么。

- **纯 Provider 组作为 detour 目标不再被判成断链。** `common/physicalpath` 新增
  `ProviderBackedGroup` 能力与 `routeDeferred`/`Report.Pending`：试运行早于 Provider Start，
  因此“Provider 还没发布”是 pending，不是 `FailureStructure`；没有 Provider 供应的空组仍然致命。
  等待期拨号点名所等的 Provider。
- **动态发布/组成员变更先校验最终图。** `adapter/outbound` 新增 `graph.go`：`Replace`、
  组发布的 `UpdateGroupMembership`、`Remove` 共用同一套环检查（声明依赖 ∪ 已发布成员 ∪
  references，外加跨 DNS/出站图）。拒绝时保留上一份有效成员与选择；并发发布由 `graphAccess`
  串行化。
- **移除不再关闭组仍在使用的节点。** `Remove` 的依赖扫描包含组已发布成员和 DNS transport 引用；
  provider 记住被拒的移除，在组成员重建后重试。组件拆除期走 `DynamicOutboundReleaser.Release`，
  因为 provider 先于组关闭。顺带修复了 `Outbounds()` 暴露可变切片导致的 teardown 数据竞争
  （改为 copy-on-write 发布）。
- **下载引导环给出诊断。** 未配置 `http_client` 时，隐式默认 HTTP 客户端跟随 `route.final`；
  显式 detour 跟随该出站。无缓存/`initial_path` 且路径只经过“仅由本 Provider 供应且无成员”的组时，
  启动拒绝并点名组与路径。
- **修正一处误导性测试。** `TestGroupWithProviderOnlyStartsWithoutMembers` 更名为
  `TestGroupNamingAnUndeclaredProviderIsRefused`（它断言的是未声明 Provider 的启动失败）；合法
  空组的用例移到 `box_provider_lifecycle_test.go`。

上面两段“未覆盖/环境阻塞”的描述在该轮已更新：Box 内的 HTTP 订阅下载现在由 `httptest` 集成测试
覆盖（本机回环，不触外网）；race 检测在 Windows 上使用 MSYS2 mingw gcc 运行通过（Swift clang
不支持 race 需要的 `-mthreads`）。仍未覆盖：DNS 解析形成的引导环、Provider 成员发布时的 TCP/UDP
能力试运行、真实设备/真实订阅。

## Known limits

- A static outbound's `detour` that names a provider node is resolved once by the fork's
  `DetourDialer` cache; a provider refresh will not redirect it. Use providers through groups.
- Provider endpoints and TLS/AnyTLS overrides are not implemented (see scope).
- The Clash API lists provider nodes through the outbound manager and the groups; the
  provider-specific `/providers/proxies` endpoints are not part of this stage.
- A local provider is reloaded by the file watcher; a remote provider is refreshed on its interval.
  There is no manual refresh command.
- A changed node's previous object is closed after the swap, so its live connections are
  interrupted. Only unchanged nodes keep connections across a refresh. Deferring the close until
  connections drain would need per-node reference tracking; the reference implementations close
  immediately as well.
- An empty or unrecognised document is a failed refresh, never "remove all nodes". The safe default
  means a provider that legitimately becomes empty keeps its previous nodes until it is removed from
  the configuration.
- The download limits (30 s, 10 MiB) are constants, not options: they are safety bounds for
  untrusted input, not a tuning surface.
