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
| `golangci-lint` 2.14.0 with this repository's `.golangci.yml` | the finding set is byte-identical to the pristine tree, except two pre-existing `unused` findings whose line numbers moved because fields were inserted above them; no new finding |
| `go test -run TestTrackedSchemaMatchesTheGenerator ./cmd/sing-box` | pass; `docs/schema.json` regenerated, delta is `"hidden": {"type": "boolean"}` on the three group variants |
| Mutation: filter condition forced false | the four daemon tests that assert the omission fail |
| Mutation: capability *presence* used instead of its value | the tests that assert visible groups are listed fail |
| `"route": {"final": "does-not-exist"}` | refused at start (`default outbound not found`), which is what makes `final` naming a hidden group evidence that it resolved |

`gofumpt` would reformat `protocol/group/selector.go` and `protocol/group/urltest.go`, and does so on
the pristine tree as well; the only hunks are a blank line before `DialContext`, unrelated to this
change, so those files were left as they are. No workflow in this repository runs the linter or the
formatters, so none of the above is a gate; they were run because the Makefile exposes it and a
display-only feature should not arrive with formatting or dead-code debt.

## Adversarial acceptance (round 2)

A second, independent round tried to BREAK the separation. Everything below is a measurement on this
branch, and every assertion reads an output - the group list, the outbound list, the router's
decision, a loopback listener that did or did not receive a connection, the probe history - never a
configuration field.

| Attack | Result |
|---|---|
| A-H matrix in one Box (default, explicit false, three hidden types, single node, empty group) | the report contains exactly the non-hidden groups; no member-count or emptiness threshold re-entered |
| `MAIN -> REGION-US(hidden)`, `REGION-HK(visible)`, plus a hidden parent with a visible child | hiding does not propagate in either direction; `MAIN`'s member list still names `REGION-US` (read from the payload); `MAIN` selects it and the flow follows |
| `MAIN -> REGION(hidden) -> CITY(hidden urltest) -> LEAF`, with `route.rules[].outbound = REGION` | only `MAIN` is listed; the real router, driven with a connection whose destination matches the rule, reaches a LEAF listener; a `route.final` fallback in the same profile is never used, which is the negative control that proves the rule matched |
| `detour` pointing at a hidden group | `direct` refuses `detour` outright, so a `socks` outbound dials its server through the hidden group and the group's default member receives the connection |
| hidden URLTest | it measures its members at start with nothing touching it; `URLTest.Selected` is a pure preview and deliberately does not start the ticker, a real flow through it does, and the history timestamp then keeps advancing; the `URLTest` RPC resolves it by tag and re-measures |
| hidden LoadBalance | candidate set unchanged; six round-robin flows over three members reach all three |
| hidden LoadBalance with a health URL | with the probe target reachable through one member and refused through the other, every flow after the verdict lands on the healthy member; the control profile (same group, no `url`) hands a flow to the failed one, which is what makes the first result mean the filter ran |
| a group whose own `exclude` drops every provider node (a durable EMPTY group) | the visible one is listed with no members, the hidden one is not listed at all, and its parent still lists it. An empty provider DOCUMENT is refused by the platform ("an empty node set must not wipe the published nodes"), so this is the only way to build a group that stays empty |
| inline provider feeding a hidden selector | both nodes publish with `<provider>/<node>` tags and join the hidden group, are registered outbounds, and the parent's flow leaves through one of them; the group stays unlisted |
| expand cache | `SetGroupExpand` stores for a hidden group, and the value survives a reload |
| `hidden: null` | decodes as unset (visible) - the same as every non-pointer field in this tree |
| `"true"`, `1`, `{}`, `[true]` | refused with the field named (`cannot unmarshal ... into Go struct field ... of type bool`); no silent coercion |
| duplicate `"hidden"` key | last value wins, as `encoding/json` does for every struct field here |
| `hidden` on ten leaf families (`direct`, `block`, `socks`, `http`, `shadowsocks`, `trojan`, `vless`, `vmess`, `hysteria2`, `tuic`) | every one refused with `unknown field "hidden"` |
| `hidden` on an unknown outbound type | `unknown outbound type`, refused by the registry before any option applies |
| Mutation: the filter also drops hidden groups from a PARENT's member list | the nesting and three-level tests fail |
| Mutation: the filter also drops hidden groups from the complete outbound list | four tests fail |
| Mutation: the filter never fires / fires on capability presence instead of its value | the display tests fail |

Every production read of the property is one line: `daemon.StartedService.readGroups`. Nothing on
the selection, dial, probe, balance, provider or Clash API path reads it, and the clients do not
filter for themselves - all three take the stream as it arrives:

* Apple: `CommandClient.writeGroups` converts every received group, and `GroupListViewModel.setGroups`
  only reconciles pending selection/expand state - it removes nothing.
* Android: `compose/model/Groups.kt` `Group(item: OutboundGroup)` converts every group.
* Desktop: `GroupsView` renders `useStream(api.groups)` directly, fed by `subscribeGroups`.

## Limits

* No client UI was changed. A hidden group cannot be revealed from a client at runtime; changing the
  flag is a configuration edit plus a reload, which rebuilds every group.
* A hidden group is still visible through the Clash API, to third-party dashboards and to the
  outbound list. That is the intended separation, not an oversight.
* The three clients were read, not built: this host has no Xcode and no Android toolchain, and the
  client submodules are not checked out in the kernel worktree. What is established is that each one
  converts or renders the stream without removing entries, and that the daemon exposes exactly one
  group RPC - so there is no second place for the decision to be made or forgotten.
