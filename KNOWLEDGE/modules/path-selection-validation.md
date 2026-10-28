# Path selection and start-time validation

## AnyTLS server resolution and failure attribution after v0.1.9

At `011f3fdcd99`, `protocol/anytls/outbound.go` asks its dialer to prefer IPv4 for the proxy
server name when the resolver strategy is otherwise `AsIS`. Explicit resolver or legacy strategy
still takes precedence; the preference is applied only where `NewWithOptions` constructs a local
resolve dialer, not universally to detoured lookups or target destinations. It is a preference,
not proof that IPv4 is the only possible path or that throughput improves on every network.

For TCP and UoT `DialContext`, an AnyTLS client error is wrapped with the outbound tag and server
while retaining its cause; this identifies a proxy reporting a remote resolution error without
fixing that resolver. `ListenPacket` directly returns its UoT client's result and does not pass it
through this naming wrapper. See `common/dialer/server_preference_test.go` and
`protocol/anytls/proxy_failure_naming_test.go`. Both packages passed their unfiltered local tests
in this refresh; this is not live proxy interoperability or performance evidence.

## Mieru transport failure boundaries after v0.1.9

**VERIFIED_SOURCE** at `011f3fdcd99`: `protocol/mieru/fallback.go` classifies caller cancellation,
local lifecycle, endpoint, transport, target and ambiguous failures separately. It preserves typed
socket observations before the SDK flattens error chains; a SOCKS rejection is target evidence and
must not mark the underlay transport unhealthy. In the SOCKS response-read classification, a bare
TCP EOF is ambiguous while the UDP case is classified as a transport failure; inspect the precise
branch rather than treating every EOF alike. The Mieru inbound and
outbound register close with their scopes (`protocol/mieru/inbound.go`, `outbound.go`); a close or
generation replacement must not be interpreted as a transport failure. This is not proof of peer
interoperability or every failure mode; consult the fallback and early-failure tests for their scope.

### Group forwarding of parallel candidates

`Selector`, `URLTest` and `LoadBalance` select a member before forwarding the destination candidate
list, network strategy, interface types and fallback delay through `DialParallelNetwork` and
`ListenSerialNetworkPacket`; nested capable groups forward these arguments rather than collapsing the
plan to one address. See `protocol/group/parallel_passthrough_test.go`. The unfiltered group package
tests passed locally at `cb71cf80d72`; this does not establish real network switching or all peer
behavior.

### Remote binary rule-set publication

`common/srs/binary.go` drains the zlib stream to its natural end to verify truncation and checksum
before returning a parsed rule set; decompressed work is bounded separately, with a distinct
`ErrDecompressedTooLarge`. `route/rule/rule_set_remote.go` uses the streaming path when there is no
cache file, drains the HTTP body before publication to catch framing errors, then builds and validates
the rules before publishing. `Close` marks the set closed and cancels its context; publication checks
the closed state under the lock. Do not equate completing rule parsing with verifying the enclosing
stream, or publish a late fetch after close. These are `VERIFIED_SOURCE` at `011f3fdcd99`; local
`./route/rule/...` tests passed in this refresh, not a live remote-server or device guarantee.

The remote rule-set HTTP client now has a five-minute timeout (`rule_set_remote.go`), so the old
audit's claim of no per-request timeout is stale. The updater still processes sets serially and
`Close` cancels its context without joining the loop; one slow request can delay later sets up to
the effective timeout. Publication checks closed state, but a callback already copied and past its
check is not universally recalled. TUN's `updateRouteAddressSet` and auto-redirect close serialize
through `autoRedirectAccess` and recheck closed before calling the redirect. The focused
`TestInFlightCallbackUnderCloseIsOrdered` passed on Windows in this refresh; that test is not a
Linux auto-redirect integration test or a proof for every observer.

**Scope:** `common/physicalpath`, `common/routeflow`, start-time validation in
`adapter/outbound/manager.go`, `detour` resolution, outbound groups, cycles, failover.

**Provenance:** `common/physicalpath` and `common/routeflow` are **fork-created** (absent at
`e4b7e692`); `protocol/group/loadbalance*.go` and `route/failover.go` are fork additions.
`common/physicalpath` is the largest fork-original subsystem: **35 commits, 8,543 Go lines
including 16 test files**.

**Load this module when changing:** start-time validation, `detour`, selectors/urltest/loadbalance,
`destination_dns_ownership`, `PathNode.RequiredNetworks`, or failover.

---

## 1. The three things that are not the same

```text
CONTROL PATH   who SELECTS what — the routing rule's outbound root, then every group entered,
               in descent order. A group is NOT a physical hop. Selection groups can still wrap
               the returned connection for interruption/lifecycle management; see selector.go and
               urltest.go. Physical-path representation must not erase that separate contract.

PHYSICAL PATH  the hops the bytes actually traverse, in PACKET order:
               Hops[0] = nearest this device, Hops[len-1] = the exit.
```

A diagnostic that reports a group as a hop, or reports the hop list backwards, **describes a path the
traffic does not take** — worse than no diagnostic.

### Naming — get this right, it is a documented trap

| Type | Field | Order | Anchor |
|---|---|---|---|
| `Path` (a `Build` result) | `ControlPath []string` | **descent** (root → innermost group) | `physicalpath.go:316` |
| `Path` (a `Build` result) | `Hops []Hop` | **packet** (device-nearest first) | `physicalpath.go:320` |
| `PathNode` (the walk's node) | `PhysicalPath []string` | packet | `leaves.go:56` |

**There is no `PhysicalPath` type and no `Path.PhysicalPath` field.** `PhysicalPath` is a field of
`PathNode`, produced by `Resolver.Hops()`/`Leaves()` (`leaves.go:215`, `:257`).

### Why `ControlPath` is NOT reversed — and the document that says otherwise

`reversePacketOrder` (`physicalpath.go:564-600`) reverses `Hops` and renumbers `Position`, then
deliberately **leaves `ControlPath` in descent order** (`:571-587`), remapping `Unknown.Position`
through the same permutation (`:592-599`).

* The fix that established this is `d46717dbd` — "fix(path): ControlPath is the decision order, not
  the packet order", 2026-10-10 12:54:53, an ancestor of the release baseline.
* **`docs/fork/physicalpath-controlpath-model.md` is stale here.** It is explicit that the fix
  "also reverses `ControlPath`", but it was last touched by `e201b5add` (10:03:43), **51 minutes
  before** `d46717dbd`. Read that one paragraph as superseded; the code is the authority.
* Pinned by `common/physicalpath/physicalpath_test.go:462-482` (`TestControlPathIsShorterThanThePhysicalPath`) —
  note the test function's own doc comment still carries the older name
  `TestControlPathOrderIsNotPacketOrder`; the assertion is `ControlPath == ["outer","inner"]` (descent)
  while `hopTags(path) == ["exit","member"]` (packet), i.e. the two lists differ in **membership and
  order**.

### The direction proof, and how it was decided

A configured `detour` is a **dependency** edge: `X.detour = Y` means X consumes Y. A proxy outbound
sends the user's *target* inside the protocol and uses its dialer only to reach **its own server**:

```go
// sing/protocol/socks/client.go
:162  tcpConn, err := c.dialer.DialContext(ctx, N.NetworkTCP, c.serverAddr)   // the SERVER
:198  response, err = ClientHandshake5(tcpConn, command, address, ...)        // the TARGET, in the request
```

So `exit.detour = entry` makes the device enter **entry first, exit second**. The conclusion rests on
a wire observation, not on reasoning about those lines: `common/dialer/detour_wire_order_test.go`
builds the dialer with the real `NewDetour`, substitutes a recording peer, and records which hop is
entered and **the address it was asked for** — the address alone cannot distinguish "the entry was
asked to carry a connection to the exit's server" from "the device dialled the exit directly".

Tests that pin it: `TestDetourTwoHopReachesTheDetourFirst`, `TestDetourThreeHopKeepsTheOrder`,
`TestOutboundToEndpointReachesTheEndpointFirst`, `TestHopZeroIsTheNearestEntryForTheWholeMatrix`.
Two of these were **renamed** because they asserted the opposite of their names — the earlier reading
shipped and was corrected by `524ea41e`.

**Honest limit, stated in the source** (`physicalpath.go:551-554`): the ordering is derived from the
dial edge each hop declares. It is exact for a chain of forwarding proxies, which is what `detour`
expresses. It does not model a hop that carries traffic for a server other than the one its
dependency names, and it cannot see a route the configuration did not declare.

---

## 2. Start-time validation — what is actually checked, and in what order

`adapter/outbound/manager.go` `Start` (`:153-231`) runs, in this order:

1. `lintOutbounds` (`:191`) — the dependency-exists check and the acyclic sort.
2. `validatePhysicalPaths` (`:211-216`) — the reachable-leaf dry run.
3. `startOutbounds` (`:217`).

The order matters: the dry run runs **after** the sort so it cannot replace the sort's more precise
message, and the sort's computed order is passed to `startOutbounds` rather than recomputed.

### The read-only contract, stated by the code

`manager.go:235-237`: *"It is read-only: it dials nothing, resolves nothing, changes no selection and
consumes no rotation."* Provable from the object graph: `Resolver` (`physicalpath.go`) holds a
registry lookup, a `Snapshot` and a network name — **no dialer, no logger, no context, no clock** —
so a walk cannot dial, log or time anything. `Build` never clones: `TestTheReportedLeafIsTheSelectedLeaf`
asserts `require.Same` on the registry's object.

Rotation is pinned separately: `TestTheDryRunConsumesNoRotationOnARealStart`
(`adapter/outbound/manager_physicalpath_test.go:357-373`) asserts committed == 0 **and** previewed == 0.
A caller that will not accept even a preview sets `Snapshot.Selections`, or passes `""` to declare the
member **NOT KNOWABLE** (`TestASnapshotCanSayTheLeafIsNotKnowable`).

Where the leaf is not knowable the walk appends `Unknown{Node, Position, Reason}` and **stops**;
`Path.Exit()` reports `ok=false` while any unknown is present, so a partly-verified path cannot be
mistaken for a verified one (`TestAnUnresolvableDependencyIsUnknownAndNotInvented`).

### But validation is not side-effect-free — the honest correction

Two real side effects exist on the validation path. Neither dials; both are recorded because the
blanket claim "read-only" is stronger than the code:

1. `common/dialer/dialer.go:187` calls `deprecated.Report(ctx, OptionMissingDomainResolver)` — a
   **deprecation log** emitted during start-time validation (reachable: `box.go:555` passes
   `newDialer=false`).
2. `common/dialer/capability.go:100` calls `detourDialer.Dialer()`, which **commits a memoised detour
   resolution** into a `sync.Once` (`common/dialer/detour.go:63`). It resolves a tag → object; it does
   not dial.

Separately, `Box.Status` (`box.go:1318`) **deliberately** takes a live `Selected()` preview. That is a
status read, not validation.

### What each check catches

| Check | Anchor | Catches |
|---|---|---|
| member exists | the pre-existing sort | a group whose declared member names nothing |
| member can carry every network its root routes to it | dry run | a UDP-only member in a chain a TCP flow takes |
| endpoint participation | dry run | a tag claimed by two different objects |
| declared `destination_dns_ownership` is implemented | dry run | a promise about what the peer receives that the object cannot keep |
| declared ownership has a resolver | dry run | the fail-closed path that would break every named destination |

Every node of a detour chain is checked **at its own position**, not only the exit: a middle hop that
cannot carry that flow even when the exit can. This capability finding now warns at start; the
affected flow may still fail at runtime. See the severity table immediately below.

The dry run adds one deliberate new refusal: `destination_dns_ownership` on an outbound type that does
not implement it. That field is fork-only and added in the same cycle, so **no configuration
predating it can contain it**.

### Severity: capability findings warn, structural findings refuse (2026-10-20)

`Failure` carries a `Kind`, decided where the finding is produced rather than by matching the
message:

| kind | examples | start verdict |
|---|---|---|
| `FailureCapability` | an exit that does not carry a routed network; an unselected member that cannot serve the flow; a group whose members cannot carry the delivered network | **WARN**, start proceeds |
| `FailureStructure` | a declared member that does not exist; a tag claimed by both an outbound and an endpoint; a route that never reaches its far end | **ERROR**, start refused |
| `FailurePromise` | `destination_dns_ownership` declared on a type that cannot honour it | **ERROR**, start refused |

`Report.Err()` returns only the fatal part, `Report.Warnings()` the capability part, and the outbound
manager logs the latter once through its injected logger (`SetLogger`, wired from `box.go`). The
runtime is unchanged: a flow that reaches an object which cannot carry it still fails exactly as
before. The distinction exists because a capability finding is a statement about a FLOW THAT MIGHT
ARRIVE, while a structural one is a statement about whether the configuration can be built.

The same split applies to the two consumer checks: an HTTP client whose detour exists but cannot
serve every configured transport, and a DNS transport whose detour exists but cannot carry its
queries, are warnings; a detour tag that names nothing stays fatal.

### A latent panic it replaced

The start-order sort marked a node started **by tag** and stopped when the number of started tags
equalled the number of **nodes**. Two objects under one tag satisfied that count with one unvisited,
and the code that then reported a dependency problem dereferenced the nil result — measured as
`invalid memory address or nil pointer dereference` inside `lintOutbounds`, not an error.
`TestDuplicateMemberTagIsRefusedRatherThanCrashingTheSort` pins the replacement.

### The one construction side effect

`adapter/outbound/manager.go:156-173`: if `route.final` is empty, a `direct` fallback object is
**created**. That is the only construction on this path.

---

## 3. `common/routeflow` — provable demand

`common/routeflow/reach.go` (340 lines, **no test files**).

`Reach(rules, finalOutbound) Demand` (`:95`). `Demand` (`:278-302`) carries `Proven`, `Possible`,
`Unclaimed`, `Reached`. `Demand.NetworksFor` returns only **`Proven`** (`:302-307`) — never
`Possible`.

The **UNKNOWN policy** is stated at `:47-70` and is the load-bearing rule:

> An unknown rule is **transparent in both directions** — it neither charges a tag nor removes its
> network.

Consequences worth knowing:

* **`route.final` IS charged with the residual, as PROVEN demand** (`:72-89`, `:177-183`). An earlier
  version treated final as "proves nothing" and pinned a test that a TCP-only final must start; that
  was a **measured false deny**, reversed by `133e6ed4f`. The test it pinned
  (`TestFinalProvesNothingAboutDelivery`) **no longer exists** — if you find it cited in
  `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` §B/§H, that document is superseded there.
* Provability means scope `Always` **and** `terminates()` (`:214-228`), which mirrors `matchRule`
  (`route/route.go:1191-1206`).
* **ICMP is deliberately out** (`:338-347`).

### The three per-class capability checks

| Consumer class | Check | Anchor |
|---|---|---|
| dialer / detour | `CheckDetourCapability` | `common/dialer/capability.go:40` |
| HTTP client | `HTTPClientTransportRequirement` | `common/httpclient/transport_capability.go:30`, called at `common/httpclient/manager.go:122` |
| DNS transport | requirement per DNS type | `dns/transport/capability.go:34` |

**There is no `Consumer`/`ConsumerProvider` type anywhere in the tree** (`grep -i ConsumerProvider`
→ zero hits). Earlier wording that called this design "consumer-first" was naming the **dial-chain
construction order** (`route/route.go:248`, `:267`), not the validation model.

---

## 4. Groups: selection is a preview until it is committed

| Group | `Selected(network)` behaviour | Anchor |
|---|---|---|
| `Selector` | Returns the loaded atomic; **ignores the network argument**; `Network()` reports `{tcp,udp}` while unselected (the "blanket") | `protocol/group/selector.go:216-218`, `:159-161` |
| `URLTest` | Filters per network, reads the history store, **writes nothing back** | `protocol/group/urltest.go:207-224` |
| `LoadBalance` | Documented as "a pure preview"; delegates to `SelectForFlow(…, commit=false)`; the round-robin branch takes the cursor with `Load()` and only advances with `Add(1)` when `commit` is true | `protocol/group/loadbalance.go:259-261`, `:337-341` |

**The `commit` flag is the control-plane / physical-plane separation at run time**
(`route/route.go:218-246`). A preview must not consume a slot: a diagnostic that spent one would
hand the next flow a different member than the one it just reported.

Pinned over the **real** groups, not fixtures: `route/physicalpath_route_test.go`
(`TestPhysicalPathTakesNoSelectionPreviewThroughRealGroups`,
`TestTwoFlowsAdvanceTheCursorExactlyTwiceWithAPreviewInBetween`), validated by a reverse-break that
mutated `loadbalance.go`'s `Selected` to pass `commit=true` and watched
`CommittedSelections()` change.

`TestControlGroupIsNotAPhysicalHopThroughRealGroups` asserts one hop with two control nodes.

### Runtime dial ownership and failover

`route/failover.go`:

* `routeDialOwner` (`:45-54`) requires `FailoverEnabled()`.
* `routeDialDecision` (`:74-93`): **more than one destination address ⇒ no owner** (`:76-78`); a
  `ConnectionHandler` leaf ⇒ re-resolve with `commit=true` (`:83-91`).
* `failoverDialer` (`:117-141`) embeds the leaf for tag/network/copy tuning but dials through
  `group.DialWithFailover` (`:124-126`).

The failover budget belongs to the **user flow**, not the group call: a chain `outer → inner` spends
**one** alternate in total. A per-group bound would multiply.

---

## 5. `PathNode.RequiredNetworks` — an implementation detail, NOT a contract

`git grep -n RequiredNetworks` over the tree gives 15 hits:

* **writers (production, 2):** `leaves.go:359`, `leaves.go:446`
* **readers (tests, 5):** `diamond_routes_test.go:105,107,134,169`, `p1_contract_diagnostic_test.go:54`
* **readers (production, 0)**

The validator does **not** read it. It reads `HopCheck.Requirement` (`dryrun.go:71`), computed from a
**function parameter** via `nodeRequirementFor`/`selfRequirementFor`.

The fork's own commit `fc4bdd416` says so: *"physicalpath.RequiredNetworks is still write-only. It is
populated by leaves.go and read nowhere … should be wired or removed by the file's owner."*
`docs/fork/v016-correction-round-ledger.md:249-251` adds that its doc comment *"is actively wrong
until the owner wires or deletes it."*

**Two requirement models coexist in the package, and they disagree.** `RequiredNetworks` charges
**dependency** hops the business network (`leaves.go:480` passes `required` down unchanged); the
validator **exempts** them (`nodeRequirementFor` returns nil unless `businessEntry`,
`dryrun.go:460-468`, `:623-625`).

Treat this as a **FOLLOW-UP for the file's owner**, never as a contract. Do not cite it as evidence
that dependency hops are charged a network.

### A second, dated contradiction inside `dryrun.go`

`dryrun.go:470-493` is an **orphan comment block** — a blank line at `:469` separates it from
`nodeRequirementFor`'s closing brace at `:468` — stating that the narrow reading was "tried and
REVERSED … The union therefore STAYS". The code at `:313-317` implements **exactly** that narrowing
when `!requirementProven` (`effective = selfRequirementFor(node)`), and `selfRequirementFor`'s own doc
at `:511-516` says the opposite of the orphan comment.

Proved by commit ordering: `d95e256b7` (2026-10-12 04:49:42) **deletes** `selfRequirementFor` and adds
the comment; `31f250ff1` (06:08:04) **re-adds** the helper and keeps the comment
(`git merge-base --is-ancestor d95e256b7 31f250ff1` → yes). **The comment is dead; the code is
settled. Never re-instate the union.**

### A diagnostic branch that can never fire

`requirementClause`'s advertised branch (`dryrun.go:947-948`) is **unreachable**:
`selfRequirementFor` = `decidedNetworks(networksOf(node.Outbound))` (`:517-519`) and
`decidedNetworks` returns a **subset** (`:765-777`), so when `!requirementProven` the requirement is a
subset of the node's own `Network()`, and `validateNode`'s step-2 failure (`:814-845`) — the only
caller (`:819`, `:844`) — cannot fire. Corollary: `HopCheck.RequirementProven` is always true wherever
a message is built. `box_physicalpath_route_aware_test.go:766` therefore asserts `NotContains` on a
string that can never appear, and that file's comment at `:733-738` contradicts its own fixture at
`:749-754` and its own assertions at `:763-768`.

---

## 6. Cycle detection — three implementations, two read-only guards

| Site | Anchor |
|---|---|
| outbound dependency sort | `adapter/outbound/manager.go:477-479` |
| cross-kind (outbound ↔ DNS transport) | `adapter/outbound/cross_kind_cycle.go:98` `ValidateCrossKindCycles`, graph `validate()` `:318-380` |
| route walk | `route/route.go:264-266` |
| physical-path walk | `physicalpath.go:700-702` |
| leaf enumeration | `leaves.go:385-387`, `:470-476` |

Cycle detection reuses the **existing** graph (declared membership ∪ `adapter.Referrer.References`) —
the same edge set `cross_kind_cycle.go` validates at start. The walk's own descent *is* the cycle
record, the shape `route.resolveOutbound` already uses. **No second graph was introduced**; keep it
that way.

`default_domain_resolver` is modelled as a graph edge (`box.go:616-618`, `SetDefaultDomainResolver`
installs a **closure**, so the answer stays live). Charging it unconditionally was a **measured false
deny** (`cross_kind_cycle.go:288-294`); omitting it accepted a cycle that hangs on the first dial
(`box.go:606-612`). Both directions have been wrong once.

---

## 7. Seams — one production call site each

| Seam | Consumer | Anchor |
|---|---|---|
| `EnablePhysicalPathValidation` | `box.New` | `manager.go:86` ← `box.go:504` |
| `SetResolverAuthority` | `box.New` | `manager.go:112` ← `box.go:541` |
| `EnablePhysicalPathDelivery` | **only** after `router.Initialize` succeeds | `manager.go:134` ← `box.go:601` |
| `SetDefaultDomainResolver` | `box.New` | `manager.go:50` ← `box.go:616` |
| `ValidateRoots` | `Manager.Start` | `dryrun.go:186` ← `manager.go:333` |

`box.go:593-601` states that `EnablePhysicalPathDelivery` is called only after `router.Initialize`
succeeds, because that is the only point where route-proved delivery is derivable from the
**compiled** rules.

**The declarations are opt-in.** A `Manager` that was never given them runs exactly as before;
`EnablePhysicalPathValidation` is what turns the dry run on and only `box.New` calls it
(`TestNoDryRunWithoutTheDeclarationsKeepsTheManagerUnchanged`).

---

## 8. Evidence, provenance, cautions

### Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Dry run dials nothing, resolves nothing, consumes no rotation | `VERIFIED_UNIT` (+ mutation controls) | `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...` |
| `ControlPath` is descent order and `Hops` is packet order | `VERIFIED_UNIT` | `physicalpath_test.go:462-482` |
| A control group is never a hop | `VERIFIED_UNIT`, over real groups | `route/physicalpath_route_test.go` |
| `route.final` is charged the residual as proven | `VERIFIED_UNIT` | `box_physicalpath_unconstrained_test.go:62` |
| False-deny matrix (11 accepted/refused twins) | `VERIFIED_UNIT` (needs tags) | `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestFalseDenyMatrix .` |
| Cross-kind cycle refused at start | `VERIFIED_UNIT` | `box_cross_kind_cycle_test.go`, `box_static_validation_final_wire_test.go` |
| `RequiredNetworks` semantics | **`VERIFIED_SOURCE` only** — write-only, no production reader, no contract | `git grep -n RequiredNetworks` |
| Cross-kind graph soundness **as a whole** | **`UNKNOWN`** — CK1–CK7 were never run; only the named gaps are closed | — |

### Provenance

| Commit | Type | What it did |
|---|---|---|
| `01262510` | FEATURE | implements the `PhysicalPath`/`ControlPath` model and the start-time reachable-leaf dry run |
| `524ea41e` | FIX | makes `Hops` packet order, decided by the dial path and not by the walk. **Nine tests went red**; two were renamed because they asserted the opposite of their names. |
| `d46717dbd` | FIX | `ControlPath` is the decision order, not the packet order |
| `e3399187` | FIX | per-walk resolver state; a diamond is validated per route |
| `fc4bdd416` | FIX | the three requirement facts; records that `RequiredNetworks` is write-only |
| `8299f30e` | FIX | the exported dry run refuses a truncated route instead of leaving it to a lint |
| `8ae8f10d` | FIX | a truncated route cannot hide behind a sibling |
| `91f595906` | FIX | `destination_dns_ownership` is vacuous for a local dialer |
| `cf5a25bbe` | FIX | ownership parity for every proven local dialer; refusals split |
| `33eba5bf7` | FIX | validate outbound capability against **reachable flow types**, not entry capability |
| `0dfedb382` | FIX | consume a protocol only where the runtime router actually stops |
| `04591f8` | FIX | one resolver source of truth, and the false deny it removes |
| `4e228f53` | FIX | **the default-resolver edge is over-charged, not under-charged** — a reversal of an earlier decision |
| `31f250ff` | FIX | settles the three contracts: provable-only demand, group union, graph edges |
| `ff004007` | FIX | publish resolver facts on the adapters the graph actually walks |
| `5ad4c032` | FIX | populate the server-domain fact at every constructor site |
| `d95e256b7` | REFACTOR | drops two dead helpers — **and adds the orphan comment §5 describes** |
| `d2b34f1cf`…`386e95a70` | FEATURE/FIX/DOC | the Direct Offload chain — see `data-path-optimization.md` |

All of the above are **OWN**. `d2b34f1cf`… are shared with `data-path-optimization.md`.

### Superseded documents — do not cite them as current

| Document | Superseded where | By |
|---|---|---|
| `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` §B (`:141-142`), §H (`:350-367`) | `route.final` proven/unproven; its `TestFinalProvesNothingAboutDelivery` **no longer exists** | `133e6ed4f`; `reach.go:177-183`; `box_physicalpath_unconstrained_test.go:62` |
| same doc §C (`:169-170`) | "'does not carry' constructed in exactly one place" is tree-wide false | also `common/dialer/capability.go:59`, `dns/transport/capability.go:45`, `common/httpclient/transport_capability.go:44` |
| `KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §4, §9 | `CROSS_KIND_GRAPH_SOUND=NO`, `ENDPOINT_RESOLVER_EDGE_SOUND=NO`, `DEFAULT_RESOLVER_EDGE_SOUND=NO`, `GROUP_FALSE_DENY_REMAINS=YES` | `ff0040079`, `4e228f539`, `31f250ff1`; `manager_physicalpath_test.go:249-271` |
| `docs/fork/v016-five-workstream-final-report.md:565-587`, `docs/fork/v016-overnight-implementation-report.md:479` | declare J-1/J-2/J-3 NOT IMPLEMENTED | all three exist (`01262510`, `common/physicalpath/status.go`, `box.go:1318`) |
| `docs/fork/v016-correction-round-ledger.md:252-254` | "`Selector.References()` lies before Start" | fixed by `96e6dc419` (`selector.go:269-274`) |

`KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §1's group table is **still correct**.

### Stale citations inside the code itself

`common/physicalpath/physicalpath.go`'s package doc cites `common/dialer/detour.go` at `:57/:61/:77/:80/:85`
(actual `:67/:71/:87/:90/:95`) and `route/route.go` at `:247/:266/:899/:956` (actual
`:248/:267/:914/:971`). Verify by reading, not by following the comment.

### AI cautions

1. **A preview must never consume a rotation slot.** Pass `commit=false`; assert `CommittedSelections()`
   is unchanged.
2. **A group is not a hop.** `Hop.IsGroup` is never set (`physicalpath.go:261-264`); groups contribute
   to `ControlPath` only.
3. **Do not reverse `ControlPath`.** Two adjacent fields describing the same path in opposite
   directions is exactly what the current code avoids.
4. **Do not "improve" the dry run by resolving a tag it cannot know.** Append an `Unknown` and stop;
   `Exit()` must report `ok=false`.
5. **Do not make validation dial, resolve or select** to answer a question it cannot decide offline.
   The two documented side effects (a deprecation log, a memoised tag→object resolution) are the
   whole list; adding a third is the bug.
6. **Do not treat `RequiredNetworks` as a contract.** It is write-only.
7. **Do not re-instate the union reading in `dryrun.go`.** The orphan comment at `:470-493` says
   otherwise and is dead.
8. **Do not charge the `default_domain_resolver` edge unconditionally** and do not omit it — both
   directions were measured wrong once.
9. **Do not teach the validator to preview-select through `Selector`.** Its `Selected` ignores the
   network argument; the "blanket" `{tcp,udp}` is intentional and pinned by
   `protocol/group/selector_edge_test.go:11-30`.
10. **Do not treat `<tags>`-less test runs as evidence for this subsystem.** The root contract tests
    need `DEFAULT_BUILD_TAGS_OTHERS`; see `ci-release.md`.
11. **Do not introduce a second graph** for cycle detection. Reuse declared membership ∪
    `adapter.Referrer.References`.
12. **Do not cite `docs/fork/physicalpath-controlpath-model.md` for `ControlPath` ordering** without
    applying the `d46717dbd` correction.
