### Structure

```json
{
  "type": "urltest",
  "tag": "auto",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "providers": ["airport"],
  "use_all_providers": false,
  "include": "",
  "exclude": "",
  "url": "",
  "expected_status": "",
  "interval": "",
  "tolerance": 0,
  "idle_timeout": "",
  "hidden": false,
  "interrupt_exist_connections": false
}
```

### Fields

#### outbounds

==Required==

List of outbound tags to test. It may be empty when `providers` or `use_all_providers` is set.

#### providers

List of outbound provider tags whose nodes join this group. See [Provider](/configuration/provider/).

A provider-only group can wait for its first download before any member is available to probe.
During that wait, dials through the group return an error naming the provider. If the download
path depends on this group, give the download and subscription host's DNS lookup an independent
outbound path; see the [proxy chain example](../../FORK-CONFIGURATION.md#远程订阅与链式代理).

#### use_all_providers

When true, use every provider configured at startup in place of the explicit `providers` list.

#### include

Regexp: only provider nodes whose rendered tag matches are included.

#### exclude

Regexp: provider nodes whose rendered tag matches are skipped. `exclude` wins over `include`. Neither filter affects static `outbounds`.

A provider refresh recreates the probe group: the new member set is measured from scratch, while the
shared URL-test history keeps the results that are still relevant.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### expected_status

The HTTP status codes that count as reachable. Empty accepts any status.

Accepted syntax:

```text
*                 any status
204               exactly 204
200-299           a range
200/204/301-399   several, separated by /
200,204,301-399   several, separated by ,
```

Ranges may be reversed (`299-200` means `200-299`), and the list may contain at most 28 entries.

This is part of the group's measurement identity: a group requiring `204` is asking a different
question from one accepting any status, so the two do not share history even for the same URL.

#### interval

The test interval. `3m` will be used if empty.

#### tolerance

The test tolerance in milliseconds. `50` will be used if empty.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### failover

Optional. Turns on the stable/failover probing policy for this group.

Absent, the group keeps its historical behaviour: every member is probed every round. Naming the
block opts in, even with no fields, and then every non-positive threshold takes its conservative
default.

```json
"failover": {
  "widen_after": 2,
  "settle_after": 3,
  "recover_after": 2,
  "tolerance": 50
}
```

- `widen_after` — consecutive rounds the stable member may fail (or be missing) before the group
  widens the probe set again. While it is healthy, a periodic round probes only that member.
- `settle_after` — consecutive rounds a challenger must beat the stable member by more than
  `tolerance` before it replaces it.
- `recover_after` — consecutive healthy rounds the stable member needs, while the set is widened,
  before the group narrows back to it.
- `tolerance` — the delay margin in milliseconds that counts as a degradation.

The policy filters the group's own rounds; it does not add a second scheduler. Selection, history
and measurement identity are unchanged, so a configuration without the block behaves exactly as
before.

#### hidden

Optional, default `false`. Affects presentation in supporting clients only. The group still participates in routing, group references, probes, and dialing. Applies to `selector`, `urltest`, and `loadbalance` only.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.

### Unified delay

A measurement makes exactly two HTTP requests over one connection:

```text
dial (not timed)
  ↓
HEAD #1  warm-up: proxy path, TLS, HTTP and mux setup
  ↓
HEAD #2  timed: the delay shown and compared
```

The first request carries the one-off costs, so the reported delay reflects the path rather than the
setup. If the second request fails in a way that does not indicate a broken node - the server closed
a reused connection, say - the node is still considered reachable and the reported delay is the
whole attempt.

Only one outbound connection is made per measurement, and it is handed to the HTTP client at most
once.

### History

Two layers are kept, and they are independent:

- **Display** - one entry per node: the last successful measurement, whatever URL produced it. This
  is what a node list or a Clash proxy entry shows. A manual delay probe updates it.
- **Health** - one entry per (node, URL, status set). This is what selection, tolerance and interval
  skipping read, and only the group's own automatic checks write it.

A manual probe therefore never affects which node a group selects, and testing an arbitrary URL does
not accumulate state. A failed check removes only its own entry and never erases a node's last
successful measurement.
