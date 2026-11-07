# Outbound Provider

Status: implemented on `testing`, first stage. Local commit only; not pushed.

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
  upstream fixtures;
- `provider/remote`: URL-hash cache identity;
- `adapter/provider`: the hot-update contract - unchanged node and its live connection survive,
  changed node is replaced, failed replacement keeps the old node, removed node is unlinked, chain
  ordering and cycle rejection;
- `box_provider_test.go`: end-to-end - an inline provider publishes nodes, a selector and a urltest
  see them, the selector's configured default wins, and a group naming an undeclared provider refuses
  the start.

Not covered by a test: a live HTTP subscription download inside a Box (the remote provider is unit
tested, and the proxy manager path it uses is covered by the dynamic-update suite). Real device
traffic through a provider node is `BLOCKED_BY_ENVIRONMENT` here.

## Known limits

- A static outbound's `detour` that names a provider node is resolved once by the fork's
  `DetourDialer` cache; a provider refresh will not redirect it. Use providers through groups.
- Provider endpoints and TLS/AnyTLS overrides are not implemented (see scope).
- The Clash API lists provider nodes through the outbound manager and the groups; the
  provider-specific `/providers/proxies` endpoints are not part of this stage.
- A local provider is reloaded by the file watcher; a remote provider is refreshed on its interval.
  There is no manual refresh command.
