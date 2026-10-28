# fast_path — logical group policy

> Status: `VERIFIED_SOURCE`
>
> Reviewed against repository `Piggy-Cat-bit-shadow/sing-box`, branch `testing`.
>
> This module documents the fork's **`fast_path` configuration contract**: what the field means, where
> it may be set, how it interacts with transparent direct chains and with the router's
> eligibility gate, and which configurations are refused.
>
> If this document conflicts with current source or tests, **source and tests win**.

---

## 1. Scope

This module covers:

- what `fast_path` is, and what it is not;
- the exact JSON syntax, for `selector` and for `urltest`;
- the three-state semantics of the field;
- which outbound types may and may not carry it, and what happens when one that may not does;
- what `fast_path: true` does and does not authorise;
- precedence when groups are nested;
- why the authorisation is per-flow and is never written back to a node;
- how it relates to `urltest`'s node selection and transparent direct chains;
- common misconfigurations.

Primary source anchors:

```text
option/fast_path.go
option/outbound.go
option/schema.go
adapter/experimental.go
adapter/outbound.go
route/fast_path.go
route/route.go
protocol/group/selector.go
protocol/group/urltest.go
box.go
option/fast_path_test.go
route/fast_path_group_test.go
box_fast_path_group_policy_test.go
```

---

## 2. What `fast_path` is

`fast_path` is a **logical group policy** this fork adds. It is a property of a **group**, and it
answers one question:

> For a flow that this group selects, is the group authorised to let the flow *attempt* a Direct Fast
> Path?

It is **not** a node capability. Setting it does not mark a node as "fast", and it does not travel
with a node anywhere. A node that happens to be reachable through a `fast_path: true` group is not
thereby a fast-path node — see §7.

---

## 3. Configuration syntax

`fast_path` is a boolean field on the group's own object, beside `outbounds`.

### selector

```json
{
  "type": "selector",
  "tag": "🤖 AI",
  "outbounds": [
    "节点A",
    "节点B"
  ],
  "fast_path": true
}
```

### urltest

```json
{
  "type": "urltest",
  "tag": "🚀 海外高速",
  "outbounds": [
    "节点A",
    "节点B"
  ],
  "fast_path": true
}
```

The field is a sibling of `outbounds` and `type`, not a per-outbound or per-node entry.

---

## 4. Three-state semantics

### Three-state semantics

The field is **three-state**, and the distinction between the three is the whole point of the design:

| value | meaning |
|---|---|
| **not set** | No explicit decision. A chain of transparent selectors ending at a real `direct` leaf is intrinsically authorised to attempt bypass. |
| `"fast_path": true` | **Explicitly authorises** flows this group selects to attempt a Direct Fast Path. |
| `"fast_path": false` | **Explicitly forbids** the group authorisation, including the intrinsic direct-chain grant. |

`false` is a **decision**, not an absence. It overrides the intrinsic grant — see §8.

The three-state config type exists precisely so that "explicitly false" is *findable*: a plain
boolean could not distinguish "the operator wrote false" from "the operator wrote nothing", and those
two must behave differently.

### Two preconditions before any of this applies

Resolution returns `false` — with no group authorisation — in two cases that come
first, before the chain is examined at all:

```text
the chain has fewer than two elements
        -> there is no logical group to carry a policy, so there is nothing to authorise

the leaf cannot be bypassed at all
        -> the leaf must implement adapter.BypassableOutbound
```

The second is the reason a proxy leaf can never be bypassed: authorisation is about the *group*, but
the *leaf* still has to be something the platform could carry. When no explicit policy exists, a
real direct leaf behind only transparent groups supplies the intrinsic grant.

---

## 5. Which outbound types may carry it

Allowed:

```text
selector
urltest
```

Both are **transparent pure-selection groups**: they choose an outbound and hand the flow on, adding
no behaviour of their own. That is what makes it meaningful to speak of the group "authorising" a
flow — the group is not the thing that carries the traffic.

Refused:

```text
loadbalance
leaf outbounds — VLESS, Trojan, Hysteria2, TUIC, Shadowsocks, and any other
any other outbound that is not a FastPathAuthorizedGroup
```

**`loadbalance` is refused on purpose.** It is not a transparent selector: it may own dial, retry and
rebalance semantics, so a flow that skipped it through a Fast Path would skip those semantics too.
Being "a group" is not sufficient; being a *transparent* group is.

### Misconfiguration is an error, not a warning

Putting `fast_path` on an outbound that may not carry it makes **configuration loading fail**. It is
not ignored and not logged-and-continued: a configuration that asks for something the runtime will
not honour is refused, so the operator learns at load time rather than by observing behaviour that
silently differs from the file.

---

## 6. What `fast_path: true` authorises — and what it does not

It authorises **only** this:

```text
this logical group authorises this flow to ATTEMPT a Direct Fast Path
```

It is **not** a forced directive. After authorisation the flow still has to pass the `canFastBypass` /
`BypassVerdict` eligibility gate, which is where the real decision is made.

The implicit gate is consulted only where the entry consumes a bypass verdict: the nfqueue
auto-redirect consumer. A plain TUN does not use the verdict to bypass the userspace path; this is
an entry-mode distinction, not a blanket operating-system distinction. Explicit bypass actions are
separate. See `route/fast_path.go`, `route/route.go`, and `adapter/inbound.go`.

**The gate is refined by this field, not left unchanged.** `canFastBypass` takes a
`fastPathAuthorized bool` argument, and exactly **one** blocker is relaxed because of it:

```text
BypassRefusedOutboundChain
```

and only when **every** element in front of the leaf is a transparent pure-selection group
(`fastPathChainReachesLeaf`) **and** the leaf still passes the unchanged `BypassableOutbound` /
`CanBypass` check. Without that one branch the field would be inert on every group configuration,
because a flow routed into a group necessarily has `len(chain) >= 2` — so the refinement is what makes
the feature exist rather than an extra permission it grants.

An **unauthorised** group flow still gets `BypassRefusedOutboundChain`, unchanged. `loadbalance` still
refuses, because it does not declare the capability (it may own the dial and retry of the flow it
selected, so it is not transparent).

**These blocking conditions are untouched by `fast_path: true`**, and this list is the reason the field
is safe to expose:

```text
FakeIP destination
domain destination
sniffed domain
resolved candidates
destination rewrite
route original destination
UoT
UDPConnect
domain unmapping
custom UDP timeout
network options
TLS options
tracker
active power-report recorder
unproven process metadata
```

So the complete meaning is: *"if this flow is otherwise eligible, this group does not stand in its
way — and a chain of transparent groups is no longer treated as a chain."* It cannot make an otherwise
ineligible flow eligible.

---

## 7. Per-flow, never written back

Fast Path authorisation is resolved **per flow**. It is not stored on the node, and it does not use
shared mutable state across flows.

That matters concretely. One node may be referenced by several groups at once:

```text
节点A   <-  "🤖 AI"          group with fast_path: true
节点A   <-  "🌍 普通国外流量"  group with no fast_path
```

The two flows are evaluated independently; depending on their leaf and chain they may have the same
answer. Authorisation belongs to the chain for that flow, not to a node permanently.

This is the property that keeps the feature a policy rather than a mode: without it, a single
selection would permanently change how a node is treated.

---

## 8. Nested groups: precedence

When groups are nested, the chain is scanned **from the outermost group inward, towards the leaf**,
and the **first explicit `fast_path` decides**.

```text
outer group        fast_path: false   <-- decided here; inner values not consulted
  └─ middle group  fast_path: true
       └─ leaf
```

Three consequences:

1. The first explicit value wins; later ones in the chain are not consulted.
2. **`fast_path: false` is an explicit decision**, so it stops the scan and overrides the intrinsic grant.
3. With no explicit policy, an all-transparent chain to a real direct leaf gets the intrinsic grant;
   other chains do not. The former AI-tag fallback was removed (`c78edf21084`); tags do not authorise.

---

## 9. `fast_path` and `urltest` are different questions

They are easy to conflate because both appear on the same kinds of group. They answer different
questions:

```text
urltest     decides WHICH NODE is selected
fast_path   decides whether the flow, having been selected, may ATTEMPT a Direct Fast Path
```

`urltest` measures and picks. `fast_path` does not pick anything and does not measure anything. A
`urltest` group with no `fast_path` still picks nodes; a transparent chain to direct may receive the
intrinsic authorisation described above.

---

## 10. Common misconfigurations

```text
1. Putting fast_path on a leaf outbound (VLESS / Trojan / ...).       -> load-time error
2. Putting fast_path on a loadbalance group.                        -> load-time error
3. Expecting fast_path: true to FORCE a Direct Fast Path.            -> it only permits an attempt;
                                                                        the eligibility gate still decides
4. Expecting fast_path: true to bypass FakeIP / sniffing / tracker
   / process / TLS or network options.                              -> none of them is bypassed
5. Setting fast_path: false expecting "no opinion".                  -> it is an explicit DENY, and it
                                                                        overrides the intrinsic grant
6. Setting fast_path on an outer group while expecting an inner
   group's true to win.                                             -> the OUTER value is seen first
7. Assuming a node selected once by a fast_path group becomes a
   fast node for every later flow.                                  -> authorisation is per-flow only
```

---

## 11. Where the behaviour is pinned

The tests that make these statements checkable rather than aspirational:

| property | test |
|---|---|
| the field is three-state and group-only | `option/fast_path_test.go` |
| group policy resolution and precedence | `route/fast_path_group_test.go` |
| a non-carrier group setting `fast_path` is refused at load | `box_fast_path_group_policy_test.go` |
| the debug/truth-table behaviour of the policy | `route/fast_path_debug_test.go` |

The implementing code lives in `route/fast_path.go` (policy resolution) and `route/route.go`
(`canFastBypass`), with the marker interface in `adapter/experimental.go`.
