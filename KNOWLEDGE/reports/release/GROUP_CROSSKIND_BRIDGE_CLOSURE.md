# GROUP FALLBACK / CROSS-KIND / BRIDGE CLOSURE

## 0. Baseline

| field | value |
|---|---|
| `BASE_BRANCH` | `integration/0.1.6-final-freeze` |
| `BASE_SHA` | `96a94f584d30691cadd44dab83b64d56fc7a1fec` |
| `WORK_BRANCH` | `closure/0.1.6-group-crosskind` |
| `WORKTREE_NAME` | `<WORKSPACE_ROOT>\内核\clB` |
| `GO_VERSION` / `GOOS` / `GOARCH` | go1.26.8 / windows / amd64 |
| branches merged | none (forbidden) |
| `MAIN_TESTING` | untouched |

## 1. §2 — the group contract, read from the three real implementations

| group | `Selected(network)` implementation | network-aware? | filters an incapable member? | can a switch pick an incapable member? |
|---|---|---|---|---|
| `selector` | `return s.selected.Load()` (`selector.go:216-218`) | **NO** — the argument is ignored | **NO** | **YES** — manual switch only |
| `urltest` | `if network == N.NetworkUDP { outbound = state.udp } else { outbound = state.tcp }` (`urltest.go:207`) | **YES** | **YES** — it caches a separate tcp/udp member | no (the cache is per network) |
| `loadbalance` | `return g.SelectForFlow(nil, network, false)` (`loadbalance.go:259-261`) | **YES** | **YES** — `isCandidate` refuses a member whose `Network()` lacks the network (`:463`) | no |

### Contracts, stated as the code behaves

```
selector      selection = the COMMITTED member, whatever the network
              network-aware? no
              member capability obligation = NONE is enforced at selection time
              when no demand is proven = the group's advertisement is the only thing that can
                                         prevent handing a udp flow to a tcp-only member, and a
                                         manual switch can select one anyway

urltest       selection = the cached tcp member for TCP, the cached udp member for UDP
              network-aware? yes
              member capability obligation = enforced by the cache each member was placed in
              when no demand is proven = no obligation is violated; an incapable member is simply
                                         never chosen for that network

loadbalance   selection = a flow-aware choice among the candidates that can carry the network
              network-aware? yes
              member capability obligation = enforced by isCandidate at selection time
              when no demand is proven = no obligation is violated
```

**The answer to the prompt's central question** ("when nothing proves demand, what does a
non-filtering group promise?") is: **a selector promises nothing about its members' capabilities** —
it returns whichever member is committed, for every network. That is why the union requirement is
not simply a validator artefact: for a selector it is the only thing standing between a committed
TCP-only member and a UDP flow. It is also why narrowing the requirement is not a local edit — see
§2.

### Verification that the exemption uses the IMMEDIATE parent

`nodeRequirementFor` (`dryrun.go:441-449`) exempts a node only when
`networkFilteringGroup(parentGroup(resolver, hop))`, and `parentGroup` takes the **last** element of
the control path — which, since a group is never emitted as a hop, is the group that actually handed
the flow over. So the exemption is per handoff edge, not per ancestor subtree. This satisfies the
prompt's "检查责任应落在真正 handoff 的 immediate edge".

## 2. The narrowing that is forbidden, and why it stays forbidden

The prompt forbids re-adopting "each member is only required to carry what it advertises". It was
implemented earlier and MEASURED to turn two true-positive tests green. Re-confirmed on this branch:

```
TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork          PASS  (adapter/outbound)
TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember          PASS  (root package)
```

`provenForRoot` and `selfRequirementFor` — the two dead helpers that experiment left behind — were
**removed** in this branch, and the reasoning they carried was moved into a comment at the site that
decides the requirement, so the next reader meets the measurement before the idea. That is the whole
of the change in this branch: no behaviour moved.

## 3. §3 — nested group adversarial matrix

| id | shape | verdict | evidence |
|---|---|---|---|
| G1 | `selector[tcp-only, dual]`, no proven route | REFUSED member-by-member | the known false deny, kept for the reason in §2 |
| G2 | `selector[tcp-only, udp-only]`, manual switch | REFUSED | `selector.go:216-218` ignores the network, so the switch can pick an incapable member |
| G3 | `urltest[tcp-only, dual]`, UDP business flow | correctly served | `urltest.go:207` picks the `udp` cache |
| G4 | `loadbalance[tcp-only, dual]`, UDP business flow | correctly served | `loadbalance.go:463` `isCandidate` |
| G5 | `loadbalance -> selector -> mixed` | ancestor filters, child does not; the CHILD's edge is checked | `parentGroup` = immediate parent |
| G6 | `selector -> loadbalance -> mixed` | child filters, so the leaf is exempt on that edge | same |
| G7 | `urltest -> selector -> mixed` | inner selector's members still charged | same |
| G8 | `selector -> urltest -> mixed` | inner urltest's edge exempts | same |
| G9 | network-aware ancestor + network-blind child | **the ancestor's filtering does NOT exempt the grandchild** | `nodeRequirementFor` reads only the immediate parent |
| G10 | network-blind ancestor + network-aware child | child's edge exempts its own leaves | same |

G9 is the case the prompt singled out, and it is already pinned end-to-end by the existing matrix:

```
case-10-nested-groups-carry-the-correct-requirement   wantStart: true
case-10b-nested-groups-keep-the-real-defect           wantStart: false,
                                                      wantErr: ["cannot serve the udp flow", "tcp-only-leaf"]
```

`case-10b` is the anti-blanket-pass assertion: the same nested shape WITHOUT the termination keeps
the defect, so nesting is not a free pass. Both PASS on this branch.

## 4. §4/§5 — cross-kind graph: NOT CLOSED, and deliberately not attempted blind

The prompt asks for node identity `(kind, tag)`, endpoint `domain_resolver` edges, and
`route.default_domain_resolver` edges, but also says: **"先读 runtime lookup semantics，不要凭概念设计."**

I did not complete that reading, so this section reports what is established and what is not:

| item | status |
|---|---|
| endpoints folded into the outbound map, so a shared tag overwrites the outbound's node | `REPORTED` (fdF, `cross_kind_cycle.go:99-111`) — not independently reproduced here |
| endpoint `domain_resolver` edge not modelled | `REPORTED` — and consistent with `adapter/endpoint/adapter.go:21-27` keeping only `Detour`, which I did read |
| `route.default_domain_resolver` edge not modelled | `REPORTED` |
| node identity `(kind, tag)` for DNS vs outbound | `CORRECT` for that pair (existing test pins it); `WRONG` for outbound vs endpoint on fdF's reading |

The runtime lookup semantics that decide whether `(kind, tag)` is even the right identity are the
same ones a previous audit listed as `CORRECT`: outbound-first with endpoint fallback in the manager
(`manager.go:432-440`), the dry run, `route/route.go:144`, and `common/dialer/detour.go:61`. If
lookup is outbound-first everywhere, then a shared tag is NOT two reachable nodes — the endpoint is
unreachable — which would make the `(kind, tag)` split the WRONG model and the tag guard the right
one. That is exactly the "don't design from concepts" trap the prompt warns about, and it is why I
stopped rather than landing a graph change on a report.

`CROSS_KIND_GRAPH_SOUND = NO` (unverified, not refuted). Required tests CK1–CK7 are `NOT_RUN`.
The CK7 mutation ("degrade node identity to tag-only") therefore has nothing to run against.

## 5. §6 — bridge capability: `HOST_BLOCKED_BUT_SOURCE_PROVEN`

### The source proof

```text
protocol/bridge/outbound.go:52
  outbound.NewAdapter(C.TypeBridge, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, nil)

protocol/bridge/outbound.go:126   func (o *Outbound) DialContext(...)
  return nil, E.New("only L3 traffic is supported by bridge")
protocol/bridge/outbound.go:130   func (o *Outbound) ListenPacket(...)
  return nil, E.New("only L3 traffic is supported by bridge")
```

So the object advertises `{tcp, udp, icmp}` while **both** L4 entry points unconditionally fail with
the same message. `protocol/bridge` tests pass locally (they are unit tests), but the endpoint cannot
be constructed on this host:

```text
start endpoint/bridge[br]: bridge: open injection handle (Administrator required):
  windivert: open SCM: Access is denied.
```

which is why the live repro is unavailable and the classification is source-proven rather than
measured.

### Why this is NOT simply "narrow Network() to icmp"

That is the obvious fix and it would be wrong. The bridge's real data path is L3: it receives packets
from WinDivert, not through `DialContext`/`ListenPacket`. `Network()` in this tree is read by
`adapter.Outbound.Network()` for two distinct purposes — what the object can CARRY (the dry run) and
what the runtime guard admits before dialling (`route/route.go:269-271`). A bridge is an L3 hop, so
narrowing its advertisement to `{icmp}` would misdescribe L3 forwarding just as badly as the current
`{tcp,udp,icmp}` misdescribes its L4 entry points.

So this is a real modelling gap — `Network()` cannot express "this object carries L3 while its L4
entry points are stubs" — and it is recorded as such rather than patched. It is low severity: it
needs Windows **and** Administrator, and the shape it enables is "a route rule sends TCP/UDP at a
bridge", which fails at the first connection rather than silently misrouting.

## 6. §7 — similar-problem sweep

Performed narrowly, since the prompt forbids widening it into a group rewrite. The
`Network()`-vs-reality question was asked of the registry and the only unconditional-L4-failure shape
found is `bridge` (§5). The group capability question is answered in §1: the exemption is keyed on
`FlowAwareOutboundGroup` (a real statement of the property — `SelectForFlow` takes the network and
`isCandidate` filters) and on `URLTestGroup` (NOT a statement of it — it adds a measurement API and
inherits `Selected`). That weakness was documented in an earlier round and is unchanged here.

No new fix was landed from this sweep, because no other shape was both reproduced and safe to change.

## 7. §8 — regression protection: ALL PASS

```
TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork            PASS
TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember            PASS
case-08-group-really-delivers-udp-to-a-tcp-only-member             PASS (matrix)
case-10-nested-groups-carry-the-correct-requirement                PASS (matrix)
case-10b-nested-groups-keep-the-real-defect                        PASS (matrix)
```

None of them moved. This is the required check that a group "fix" did not succeed by turning a true
positive green.

## 8. §9 — verification

```
gofmt -l <changed>            clean
git diff --check              clean
go build -tags "<tags>" ./... ok
go vet ./common/physicalpath/ ok
go test -count=1 ./protocol/group/ ./adapter/outbound/ ./common/physicalpath/ ./route/ .   ok
go test -count=20 ./protocol/group/          ok   (79.6s)
go test -count=10 ./adapter/outbound/        ok
go test -count=10 ./common/physicalpath/     ok
```

The `protocol/group` stress run is worth noting: `TestRecheckSingleFlightCollapsesABurst` failed
once under full-tree parallel load in an earlier round, and **20 consecutive runs of the whole
package did not reproduce it**. The flake is load-sensitive rather than order-dependent, and it is
not a regression from this work.

## 9. §10 — verdicts

```
GROUP_FALLBACK_CONTRACT_SOUND  = YES  (three groups read from their real Selected implementations; contracts stated in §1)
GROUP_FALSE_DENY_REMAINS       = YES  (selector[tcp-only, dual] with no proven demand — kept, with the measurement that forbids the narrow fix)
GROUP_FALSE_ALLOW_REMAINS      = YES  (bridge L4 stubs behind a {tcp,udp,icmp} advertisement; source-proven, host-blocked)
CROSS_KIND_GRAPH_SOUND         = NO   (unverified, not refuted; CK1-CK7 NOT_RUN, no graph change landed)
ENDPOINT_RESOLVER_EDGE_SOUND   = NO   (reported edge absent, consistent with endpoint.Adapter:21-27; not reproduced)
DEFAULT_RESOLVER_EDGE_SOUND    = NO   (reported edge absent; not reproduced)
BRIDGE_CAPABILITY_STATUS       = HOST_BLOCKED_BUT_SOURCE_PROVEN
RELEASE_BLOCKER_REMAINS        = NO   (bridge needs Windows+Administrator and fails at first connection; nothing here silently misroutes)
```

### The one change this branch makes

No behaviour changed. Two dead helpers from the reverted narrowing experiment were deleted and their
reasoning was moved to the decision site, so the next person to consider narrowing the group
requirement meets the two true-positive tests and the selector's `Selected` implementation before
they meet the idea. Everything else in this report is measurement, and the three items marked `NO`
are marked that way because I could not finish reading the runtime lookup semantics that would make a
design safe — which the prompt explicitly told me not to guess at.
