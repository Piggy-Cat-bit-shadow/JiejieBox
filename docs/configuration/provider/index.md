# Provider

This feature is available on JiejieBox `testing`. It requires a build containing the outbound-provider implementation. The published v0.1.42 predates it.

Outbound providers publish nodes from a document that is external to the configuration: a
subscription URL, a local file, or the configuration itself. The nodes join the same outbound
namespace the static `outbounds` use, and a [selector](/configuration/outbound/selector/) or
[urltest](/configuration/outbound/urltest/) names the provider to include them.

A provider only supplies **outbounds**. It never replaces the configuration's DNS, route, inbounds or
experimental settings: a subscription that also contains those keys has them ignored, and only its
`outbounds` are read.

### Structure

```json
{
  "providers": [
    {
      "type": "remote",
      "tag": "airport",
      "url": "https://example.com/sing-box-subscription",
      "update_interval": "6h"
    }
  ],
  "outbounds": [
    {
      "type": "selector",
      "tag": "proxy",
      "outbounds": [],
      "providers": ["airport"]
    }
  ]
}
```

### Fields

| Key         | Format                  |
|-------------|-------------------------|
| `type`      | `remote`, `local` or `inline` |
| `tag`       | Provider tag            |

The provider tag is required to be unique among providers, and it must not contain `/` (the slash is
reserved as the provider/node separator). It is also the prefix of every node tag: a node named
`hk-01` inside provider `airport` is published as `airport/hk-01`. Use `providers: ["airport"]` on a
group to include every node of that provider.

### Remote

Downloads the document from `url`, caches the last known good copy, and refreshes it on
`update_interval`.

```json
{
  "type": "remote",
  "tag": "airport",
  "url": "https://example.com/sing-box-subscription",
  "path": "provider/airport.json",
  "user_agent": "sing-box",
  "update_interval": "6h",
  "include": "hk|sg|jp",
  "exclude": "expired"
}
```

| Field             | Description                                                                    |
|-------------------|--------------------------------------------------------------------------------|
| `url`             | Required. Absolute `http` or `https` URL.                                     |
| `path`            | Cache file for the downloaded document. Mutually exclusive with `initial_path`. |
| `initial_path`    | Seed document loaded at startup when no usable cache exists. Mutually exclusive with `path`.         |
| `user_agent`      | `User-Agent` sent with the request. Conflicts with a non-empty `http_client`. Set User-Agent through the HTTP client headers when using one.       |
| `http_client`     | [HTTP client](/configuration/shared/http-client/) used to download.            |
| `download_detour` | Deprecated. Outbound tag used to download. Use `http_client` instead.           |
| `update_interval` | Refresh interval, minimum `1h`. Defaults to `24h`.                              |
| `include`         | Regexp: only nodes whose **source node tag** matches are published.                |
| `exclude`         | Regexp: nodes whose **source node tag** matches are skipped. `exclude` wins.       |
| `override_dialer` | Dialer fields applied to every published node: `detour`, bind, timeouts, resolver. |
| `health_check`    | See below.                                                                      |

`http_client` accepts a top-level `http_clients` tag or an inline HTTP client object. It is
mutually exclusive with the deprecated `download_detour`.

This stage does not implement provider **endpoints**, `override_tls` or `override_anytls`, and those
keys are not part of the schema: a document that sets one fails with an unknown-field error naming
the key, instead of accepting it and ignoring it. `override_dialer` is supported on remote and local
providers.

The URL is parsed and its scheme checked while the configuration is loaded: a typo such as `ftp://`
or a relative path fails the start with a message naming the provider instead of being discovered by
a background worker later. Query parameters are treated as credentials: the URL in logs is printed
without its query string, userinfo or fragment.

Without `path`, the cache lives in the configured [cache file](/configuration/experimental/cache-file/)
and disappears when that is disabled. With `path`, the cache is replaced by writing a temporary file
in the same directory and renaming it, so an interrupted write leaves the previous document intact.

A provider that has no cache and cannot reach its URL at start starts with zero nodes; its groups
report a wait that names the provider on the dial path
(`... has no available member yet: it is waiting for outbound provider <tag> to publish nodes`) until
a download succeeds, and the first download is retried on the update interval.

The download path itself must not depend on nodes that cannot exist before it. With no
`http_client`, the provider uses the implicit default HTTP client the Box creates, whose dialer
follows `route.final`; an explicit `http_client.detour` or the deprecated `download_detour` follows
that outbound instead. If the path reaches only a group fed by this provider and that group has no
member, the first download waits on its own nodes: the provider refuses the start with an error
naming the group and the path, instead of leaving a group that stays empty forever and fails every
refresh in the log. A static member, another provider's nodes, or a restored cache/`initial_path`
removes the loop. A bootstrap closed only through DNS resolution (the provider's hostname resolved by
a DNS server whose detour enters that group) is not detected yet.

The download is asynchronous: `sing-box` never blocks the start of the Box on a subscription URL.
One request is bounded by a 30 second timeout (including reading the body) and a 10 MiB document
limit; a server that stalls or an endless body fails that refresh without stopping the provider.

### Local

Reads the same document shape from a local file and reloads it when the file changes.

```json
{
  "type": "local",
  "tag": "local-nodes",
  "path": "provider/local.json"
}
```

The file must exist and be readable when the Box starts; unlike a remote provider there is no cache
to fall back to, so a missing file fails the start with a message naming the provider. The path is
resolved to an absolute path before the watch is installed, which is also what makes a path written
with forward slashes work on Windows. An edit that cannot be parsed, and a deleted file, leave the
last published node set in place; the next valid edit replaces it.

### Inline

Declares the nodes in the configuration itself. Useful for testing a node set without a subscription,
and for a configuration that is generated by an external tool.

```json
{
  "type": "inline",
  "tag": "inline-nodes",
  "outbounds": [
    {
      "type": "socks",
      "tag": "local-socks",
      "server": "127.0.0.1",
      "server_port": 1080
    }
  ]
}
```

### Health check

All three provider types support an optional check. It probes published nodes and records results
in shared URLTest history. The current storage call records results under the default target scope
even for a custom probe URL. Use the URLTest group's own probes when target isolation matters.

```json
{
  "health_check": {
    "enabled": true,
    "url": "https://www.gstatic.com/generate_204",
    "interval": "10m",
    "timeout": "3s"
  }
}
```

| Field      | Description                                             |
|------------|---------------------------------------------------------|
| `enabled`  | Enable the periodic check.                               |
| `url`      | URL to request.                                          |
| `interval` | Check interval, minimum `1m`. Defaults to `10m`.         |
| `timeout`  | Per-node timeout. Defaults to `3s`.                      |

### Node document

Supported inputs are sing-box JSON (only `outbounds` are imported), Clash YAML, SIP008 and
plain or Base64-encoded share links. Clash and share-link conversion covers the protocols and
options implemented by those readers. The resulting outbound type must also be registered in the build.

Nodes are filtered in this order:

1. `selector`, `urltest`, `direct`, `block` and `dns` entries in native sing-box JSON documents are
   skipped.
2. `exclude` and `include` of the provider apply only to remote providers and match the source node tag before namespacing.
3. A tag repeated inside one document is renamed deterministically: the first occurrence keeps the
   name, later ones become `name (2)`, `name (3)`.

For remote/local documents, sibling detours are prefixed and external detours are cleared.
`override_dialer.detour` can fill in a main-configuration outbound for nodes with no surviving
sibling detour. Inline providers rewrite sibling references and preserve main-configuration references.

### Failure handling

- A document that cannot be parsed - including an empty body, an HTML login page, or a JSON document
  with no usable nodes - is a **failed refresh**. The published node set is left untouched. An empty
  document is never read as "this provider now has no nodes"; remove the provider from the
  configuration to remove its nodes.
- A single node that fails to construct (an option the build does not support, an invalid server
  address) is skipped; the rest of the document is still applied. If that tag already had a node, the
  previous object stays in service for that tag.
- A failed download, a non-2xx status, a redirect loop, a timeout and an oversized body all take the
  same path: the refresh fails, the last known good set remains, and the next interval tries again.

### Updates

An update is a diff. A node whose options are unchanged keeps its live object and therefore its
connections; a changed node is prepared and started before the previous object is unlinked; a node
whose replacement fails keeps the previous object in service. A subscription refresh that only
reorders or reformats entries does not disturb traffic.

A replaced node's previous object is closed after the swap, so connections that were using it are
interrupted; only unchanged nodes keep their connections across a refresh. A node dropped from the
document is closed when it is removed, for the same reason: there is no object left for a new
connection to resolve. If a group still publishes that node as a member, it stays alive until the
group's membership rebuild completes and is closed only then - never under a group that can still
dial it. When the membership rebuild is refused by the graph validation, the node stays alive and
keeps serving the previous valid selection.

`update_interval` is the minimum spacing between downloads. A manual refresh is not exposed through
the Clash API in this version; the provider refreshes on its own schedule.

### Group integration

- A group may name several providers. Group filters match the published `provider/node` tag and leave static `outbounds` alone.
- `use_all_providers` includes every configured provider; the set is resolved at start.
- A provider node is addressed by its rendered tag, so `default` and the Clash API see
  `airport/hk-01` exactly as the configuration does.
- A selector tries a valid saved selection, a surviving current selection on refresh, the explicit default, then the first member. A missing explicit default is an error. For a provider-only group that starts empty, omit the default or add a static default member.
- A group with no members yet and no missing explicit default starts successfully - including when it
  is another outbound's `detour` target: the start-time physical-path validation reports the route as
  pending rather than as a route that never reaches its far end. The dial fails with a message naming
  the group and the provider it waits for, rather than silently selecting an unconfigured node.
- A dynamic node publication or a group membership change is validated against the final graph
  before anything becomes selectable. An update that would close a cycle is refused, and the previous
  members and selection stay in place.
