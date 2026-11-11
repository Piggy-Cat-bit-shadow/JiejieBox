# Hidden proxy group

**Fork extension.** `hidden` does not exist in official sing-box. It is omitted when unset or false,
so a configuration that does not use it stays interchangeable; a configuration that does use it is
refused by the official client's decoder, as this fork's other extension keys are.

## What it is

A **display** property of a logical group. It removes the group's own card from the group list a
client renders. It removes nothing else: the group is created, registered, started, closed,
referenced by tag, usable as another group's member or as a detour, routable, and it keeps its
selection, its measurement history and its persisted state.

```json
{
  "outbounds": [
    {"type": "selector", "tag": "main", "outbounds": ["US-POOL", "HK-POOL", "JP-POOL"], "default": "US-POOL"},
    {"type": "selector", "tag": "US-POOL", "outbounds": ["US-01", "US-02"], "hidden": true},
    {"type": "urltest", "tag": "HK-POOL", "outbounds": ["HK-01", "HK-02"], "hidden": true},
    {"type": "selector", "tag": "JP-POOL", "outbounds": ["JP-01", "JP-02"]}
  ]
}
```

`main` and `JP-POOL` get cards. `US-POOL` and `HK-POOL` do not, but `main` still lists them as
members, `main` can select either of them, and a route rule or a `detour` can name them.

Supported on the three group types this fork registers: `selector`, `urltest`, `loadbalance`.

## Where the key is accepted, and where it is refused

The field is declared on the three group `Options` and **not** on the shared outbound envelope. A
leaf outbound that names it is refused at decode time with an error naming the key, rather than
accepting a setting the core would ignore:

```text
{"type": "direct", "tag": "node", "hidden": true}
  -> outbounds[0].hidden: json: unknown field "hidden"
```

That difference from `fast_path` (which rides the envelope because a flow policy has to exist for
every group type) is deliberate: a display flag does not.

## What it does not do

* It does not remove the group from the outbound manager, the outbound list (`SubscribeOutbounds`),
  the router, the provider path, or the persisted cache.
* It does not change `All()`, `Selected()`, the URLTest history read, or the `IsExpand` cache.
* It does not affect selection, probing, switching, balancing, health filtering or failover.
* It does not filter a parent's member list.
* It does not change the Clash API `/proxies` output. A third-party dashboard still sees the group;
  only this fork's own clients are affected, and they are affected because they all consume the
  filtered stream.
* It does not change the behaviour of any group that leaves the key out. In particular it does not
  reintroduce a member-count threshold: a single-member selector and an empty group that did not ask
  to be hidden are still listed.

## Where the filter is

One place: `daemon.StartedService.readGroups`, the single builder of the payload `SubscribeGroups`
emits. The group stream is the only group RPC the daemon proto declares, so every command client
(the Apple, Android and desktop UIs alike) sees the same list, and the filter cannot be bypassed by a
second consumer that recomputes it.

The check is a capability assertion, not a type switch:

```go
if hiddenGroup, isHidden := outboundGroup.(adapter.HiddenOutboundGroup); isHidden && hiddenGroup.Hidden() {
    continue
}
```

`adapter.HiddenOutboundGroup` is an OPTIONAL capability rather than a method on `adapter.OutboundGroup`.
`OutboundGroup`'s method set is exactly what a group-shaped test double provides, and this tree has a
dozen of them plus any implementation outside it; extending the base interface would force all of
them to state a display property they may have no opinion about. A group type that does not implement
the capability stays visible, which is what every group did before the key existed.

The value is fixed at construction from the configuration and immutable, so no lock is added and
nothing on the traffic path reads it. On the `urltest` wrapper it lives on the wrapper, not on the
published inner group, because a provider publication replaces the inner group.

## Verification

Measured on `go1.25.5 windows/amd64`, build tags `release/DEFAULT_BUILD_TAGS_OTHERS` (the profile
`verify.yml` uses for the schema gate), branch `feature/proxy-group-hidden`.

| Check | Result |
|---|---|
| `go build ./...` | pass (whole tree) |
| `go test ./option ./protocol/group ./daemon ./adapter ./experimental/clashapi ./common/physicalpath` | pass |
| `go test ./route` (full package) | pass (one Windows `TempDir` cleanup flake on the first run, `TestAnUnreferencedRuleSetDropsItsRulesAtStartup`; passes alone on this tree and on the pristine tree, and the package passes on re-run) |
| `go test -race` on the same packages | pass, no data race |
| `staticcheck` on the touched packages | 21 findings, byte-identical to the pristine tree except two pre-existing line-number shifts; no new finding |
| `go test -run TestTrackedSchemaMatchesTheGenerator ./cmd/sing-box` | pass; `docs/schema.json` regenerated, delta is `"hidden": {"type": "boolean"}` on the three group variants |
| Mutation: filter condition forced false | the four daemon tests that assert the omission fail |
| Mutation: capability *presence* used instead of its value | the tests that assert visible groups are listed fail |
| `"route": {"final": "does-not-exist"}` | refused at start (`default outbound not found`), which is what makes `final` naming a hidden group evidence that it resolved |

Not run here: `golangci-lint` (not installed on this host, and no workflow in this repository runs
it; the components that matter were covered with `staticcheck` and `gofumpt`, whose only differences
on the touched files are pre-existing at the baseline).

## Limits

* No client UI was changed. A hidden group cannot be revealed from a client at runtime; changing the
  flag is a configuration edit plus a reload, which rebuilds every group.
* A hidden group is still visible through the Clash API, to third-party dashboards and to the
  outbound list. That is the intended separation, not an oversight.
