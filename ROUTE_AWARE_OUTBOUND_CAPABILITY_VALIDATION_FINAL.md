# ROUTE-AWARE OUTBOUND CAPABILITY VALIDATION — FINAL

Branch `fix/route-aware-outbound-capability-validation`, commits `33eba5bf7` and the follow-up that
closes the adversarial audit's findings; base `jiejiebox/v0.1.6-hardening` @ `91ca1652d`.

---

## A0. WHAT THE ADVERSARIAL AUDIT CHANGED (read this first)

An independent adversary audited `33eba5bf7` and found **one measured regression and one
diagnostic dishonest in a second direction**. Both are fixed in this branch, and both are now pinned
by tests that go RED when the fix is reverted. The findings and their dispositions:

| finding | severity | disposition |
|---|---|---|
| **The terminal-action classification was wrong.** `routeflow` consumed a protocol on `adapter.IsFinalAction`, but `Router.matchRule` only stops for `route`, `reject`, `hijack-dns`, and `bypass` **when it names an outbound**. `route-options`, `direct` and an outbound-less `bypass` were treated as terminal by the analysis and NOT by the router. | **P1, measured regression** — three classes of configuration that `91ca1652d` refused now STARTED, and the udp flow reaches a TCP-only outbound and fails at dial. | **FIXED.** `terminates()` now mirrors `matchRule`, and cases 13-18 pin the whole dispatch table: `reject` and `hijack-dns` terminal, `route-options` / `direct` / outbound-less `bypass` not, `bypass` **with** an outbound terminal and delivering. |
| **The diagnostic claimed a route proof that was never made.** When nothing was proven the requirement came from `advertisedNetworks`, and the message still said "the routes prove tcp,udp reaches this path". Measured on a configuration whose only rule is domain-scoped: UNKNOWN for both networks, so no route proves anything. | **P2** | **FIXED.** `HopCheck.RequirementProven` carries the requirement's ORIGIN to the message, which now says "the routes prove nothing here, so this is what the path's own objects advertise: …". Pinned by `TestRouteAwareDiagnosticDoesNotClaimAProofTheRoutesNeverMade`. |
| `route.final` is not modelled: `{"final": "tcp-only", "rules": [{"network": "tcp", …}]}` starts although every unmatched udp flow reaches a tcp-only outbound. | P2, pre-existing by design | **NOT CHANGED, deliberately.** See section H — the fix would reverse a baseline contract the fork's own release-hardening suite pins. |
| An empty item list with `invert` — the auditor reported `NetworkScope()` returning `Never` while `Match` returns true. | P1 as reported | **NOT A DEFECT.** The report is inverted: `Match` special-cases the empty list and returns `true`, then the `invert` branch returns `metadata.DeferredIPCIDRMatchGroups != 0`, which is `false` for a rule with no items. The rule matches **nothing** at run time, so `ScopeNever` is right. The measured `REFUSED` verdict is the correct one (udp falls through to the tcp-only rule), not evidence of a false positive. The interaction is now spelled out in `NetworkScope`'s own comment. |
| `common/routeflow` has no test file of its own; some exported helpers are unused in production. | P2 | **Recorded.** The behaviour is covered where it is observable — at the box level (21 cases) and at the composition level (two unit suites). `Possible`, `Unclaimed`, `Reached`, `FinalOutbound` and `String` are carried for diagnostics and for the report a caller may build; `NetworksFor` is the documented accessor. |
| The rewritten dedup fixture no longer discriminates a leaf-keyed from a root-keyed dedup key. | P2 | **Accepted.** The old discriminating shape is not expressible once first-match-wins is modelled, and making both rules reachable requires the two failures to differ. The guard itself is still pinned by `TestOneRootWithTwoBrokenMembersIsReportedOnce`. |

The audit also confirmed independently: no user-config or protocol special case; first-match-wins
genuinely changes the verdict (case-01 STARTS vs case-03 REFUSED, same two rules); the group and
selector true positives survive; UNKNOWN is never silently reachable or unreachable; `Reach` is a
flat loop with no new recursion or cycle risk; the validator's roots are unchanged; and all three
test edits are **justified** — including the measured check that the OLD dedup fixture text against
the NEW code produces one line, confirming the rewrite follows the semantics rather than hiding a
regression.

---

## A. ROOT CAUSE

**File**: `box.go`
**Function**: `deliveredNetworksFromRoutes` (removed by this commit)
**Downstream**: `adapter/outbound/manager.go` → `physicalpath.ValidateRoots`
**Old data flow**:

```
config JSON ──scan──▶ per-tag network set ──▶ ValidateRoots(tag) ──▶ every reachable node
                       (union of rules that                          must carry that set
                        NAME the tag and have
                        an explicit `network:`)
```

Route filtering was lost because the requirement was derived from the configuration **text** and
then applied as `EntryCapability ⊆ Capability(every descendant)` — an inequality about the entry
point's theoretical capability, not about what the path can receive. Concretely, the scan could not
express four things, and each one is a way a protocol reaches a path it provably should not:

1. **No rule order.** `union` over rules that name a tag. `{network: udp, action: reject}` followed
   by a catch-all and the same two rules in the opposite order produced the *same* set, so a reject
   placed after a delivery was read as though it had filtered it.
2. **No terminal actions that are not `route`.** `reject`, `hijack-dns` and `direct` were skipped by
   `if action.Action != C.RuleActionTypeRoute { continue }`, so the protocols they provably consume
   stayed in the requirement.
3. **No logical rules.** `if rule.Type != C.RuleTypeDefault { continue }` — an entire subtree
   contributed nothing rather than the AND/OR of its conditions.
4. **No `invert`.** An inverted rule was indistinguishable from its opposite.

A fifth, subtler failure: the scan only ever *added* to a tag, and a tag named by one rule that
constrained the network plus another that did not was deleted entirely (`unproven`), so the answer
was all-or-nothing per tag.

## B. ARCHITECTURE FIX

```
                  ┌──────────────────────┐
                  │ compiled route rules │   router.Rules() — the objects the router evaluates
                  └──────────┬───────────┘
                             │  per network, ask: which rule does the router STOP at?
                             ▼
                  ┌──────────────────────┐
                  │  common/routeflow    │   order + first-match-wins + terminal actions
                  │  Reach(rules, final) │   + invert + logical AND/OR
                  └──────────┬───────────┘
                             │  Proven[tag] = networks provably delivered to tag
                             ▼
                  ┌──────────────────────┐
                  │ box.physicalPath     │   installed on the manager right after
                  │ Delivery             │   router.Initialize — the first moment it exists
                  └──────────┬───────────┘
                             ▼
                  ┌──────────────────────┐
                  │ physicalpath         │   unchanged per-leaf contract: existence, endpoint
                  │ ValidateRoots        │   ownership, DNS ownership, group membership
                  └──────────────────────┘
```

`ReachableDemand(path edge) ⊆ Capability(path member)` replaces
`EntryCapability ⊆ Capability(every descendant)`.

Three new pieces, each with one job:

| piece | file | job |
|---|---|---|
| `Reach` | `common/routeflow/reach.go` | walk the compiled rule list per network; produce `Proven` / `Possible` / `Unclaimed` |
| `RuleNetworkScope` | `adapter/rule_network_scope.go` | the three-state predicate summary: `Never` / `Unknown` / `Always`, with `And` / `Or` / `Not` |
| `NetworkScope()` | `route/rule/rule_network_scope.go` | compose a compiled rule's **own** item list into that summary, so the summary cannot describe a different predicate than the one that runs |

**No second routing engine.** Every fact comes from the compiled rule. The one thing added to the
compiled rule is a method that reads the same item slice `Match` iterates:
`abstractDefaultRule.NetworkScope()` folds `allItems` — the complete list, not a subset — and
`abstractLogicalRule.NetworkScope()` recurses into `rules` with the same mode the `Match` switch
uses. A future commit that adds an item type or changes the composition therefore changes both the
runtime predicate and its summary in the same file.

### The UNKNOWN policy, stated explicitly

`UNKNOWN` is a first-class third state, not a synonym for either neighbour:

* `NEVER` — the predicate provably rejects **every** flow of this network. Only `network: [...]` can
  prove this, because it is the only condition in the tree whose answer depends on nothing but
  `metadata.Network`.
* `ALWAYS` — the predicate provably accepts every flow of this network. A rule with no items at all
  is this (a catch-all), as is an unconditional `network:` naming the network.
* `UNKNOWN` — everything else: a domain, port, process, rule-set or interface condition, and any
  rule that pairs a network with one of them.

**An unknown predicate neither becomes a requirement for its tag nor removes its network from the
remaining set.** Both halves are deliberate:

* it is **not** a requirement, because a rule that merely might match would demand a capability of
  an outbound the flow may never reach — `{network: tcp, domain: [...]}` is UNKNOWN for TCP, and
  charging every such tag with TCP refuses configurations that have always started;
* it does **not** remove the network, because "I cannot prove this rule rejects the flow" is not a
  proof that it accepts it, and a later rule may still be reached.

The ability lost is only the ability to **prove** a requirement *through* such a rule, and the
validator's pre-existing fallback covers that case: a tag with nothing proven is checked against
what its own reachable objects advertise between them (`common/physicalpath`'s `advertisedNetworks`).
Nothing is skipped silently — de-risked by three facts:

1. the runtime dial path still refuses a flow the selected outbound cannot carry;
2. the dry run still validates existence, endpoint ownership, DNS ownership and group membership for
   every node, whatever the requirement;
3. the fallback is the *same* place the question was already being answered before this change, so
   UNKNOWN cannot be worse than the previous behaviour for any tag.

`route.final` is likewise left unproven: it receives every flow no rule matched, so it proves
nothing about *which* networks arrive. Its tag is carried on the result for diagnostics only.

## C. WHY THIS IS NOT A CONFIG-SPECIFIC PATCH

* **No tag special case.** No fixture tag, node name, group name or protocol name appears in any
  decision. Searched: `grep` for the reported symptom's business strings returns nothing in the
  production code or the tests.
* **No protocol special case.** The analysis never asks a type name; capability comes from the
  object's own `Network()` (existing behaviour, unchanged).
* **No protocol-name whitelist**, no vendor string, no language-specific match.
* **Fake capability, not real protocols, where it matters.** The two unit-level suites
  (`adapter/rule_network_scope_test.go`, `route/rule/rule_network_scope_test.go`) use hand-built
  items — `neutralItem`, `narrowingItemProvable`, `unprovableItem` — so the properties are pinned
  without binding to any protocol implementation.
* **TCP/UDP mirror.** Case 1 (UDP terminated, TCP-only target) and Case 5 (TCP terminated, UDP-only
  target) are the same test with the two networks swapped; a UDP-only special case fails one of them.
* **Rule order.** Case 1 and Case 3 are the *same two rules in opposite orders* and must reach
  **opposite** verdicts. No order-blind model can satisfy both.
* **Partial matcher.** Case 4 (`network: udp` + `domain_suffix` + reject, then a UDP route to a
  TCP-only leaf) must still FAIL: a partial termination does not exhaust the protocol.
* **Nested group.** Case 10 (requirement flows down through two group levels) and Case 10b (the same
  shape without the termination keeps the defect).
* **Renaming proves independence.** Case 7 changes every tag, the group kind (`selector` →
  `loadbalance`) and the leaf type (`socks` → `vless`) and must reach Case 1's verdict.
* **Non-inbound root.** The validator is installed by `box.New`, not by the TUN inbound, so its
  roots are the declared outbounds, the default outbound and the endpoints — every configuration,
  not only packet-tunnel. `EnablePhysicalPathDelivery` is called from **exactly one place**
  (`box.go`), and `"does not carry"` is constructed in **exactly one place**
  (`common/physicalpath/dryrun.go`). There is therefore no other consumer that reuses an
  "entry supports TCP+UDP" requirement: HTTP client detours, DNS transports and rule-set downloaders
  are **not** validator roots and are not charged anything. Case 12c exercises a non-route terminal
  action (`direct`), and the `final`-only and no-route fixtures prove the "nothing proven" path.

## D. TRUE POSITIVES PRESERVED

These still fail, and each is asserted by a case in the new matrix or by an existing test:

| scenario | verdict | asserted by |
|---|---|---|
| catch-all delivers UDP to a TCP-only leaf | **FAIL** | `case-02` |
| termination placed *after* the delivery | **FAIL** | `case-03` |
| partial (domain-scoped) UDP termination then a UDP route to a TCP-only leaf | **FAIL** | `case-04` |
| UDP reaches a network-blind selector with a TCP-only member | **FAIL** | `case-08` |
| the same nested through two group levels | **FAIL** | `case-10b` |
| a group no member of which can carry the delivered network | **FAIL** | `START-01-G2` (existing) |
| a declared member that does not exist | **FAIL** (start-order sort's own message) | `START-01-F1` (existing) |
| a declared cycle | **FAIL** (sort's own cycle error, untouched) | `START-01-F2` (existing) |
| a proven network reaching an incapable member | **FAIL**, naming the member | `TestAProvenNetworkIsRefusedDefinitelyAndNotLeftUnverified` (existing) |
| a truncated route (declared detour that does not exist) | **FAIL** | `dryrun_truncated_route_test.go` (existing) |
| two independent broken routes | **FAIL**, both roots named | `TestTwoIndependentBrokenRoutesAreBothReported` |
| one root with two broken members | **FAIL**, one line | `TestOneRootWithTwoBrokenMembersIsReportedOnce` |

The group-member check was **not** weakened: `case-08` and `START-01-G2` are exactly the tests that
go RED under mutation D.

## E. TEST EVIDENCE

All commands were run in the worktree with
`$TAGS = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()`.

```
go build -tags "$TAGS" ./...                                              ok
go vet   -tags "$TAGS" ./adapter/ ./common/routeflow/ ./route/rule/ \
                       ./common/physicalpath/ ./adapter/outbound/ .       ok
gofmt -l <touched files>                                                  (empty)

go test -tags "$TAGS" -count=1 .                                          ok  6.451s
go test -tags "$TAGS" -count=1 ./adapter/                                 ok  2.065s
go test -tags "$TAGS" -count=1 ./route/rule/                              ok  2.410s
go test -tags "$TAGS" -count=1 ./common/physicalpath/                     ok  1.157s
go test -tags "$TAGS" -count=1 ./adapter/outbound/                        ok  1.056s
go test -tags "$TAGS" -count=1 ./protocol/group/                          ok  5.523s
go test -tags "$TAGS" -count=1 ./route/                                   ok 12.486s
go test -tags "$TAGS" -count=1 ./common/routeflow/                        (no test files — pure unit tests live in adapter and route/rule)
```

Stress (`-race` is **NOT RUN** on this host: `-race requires cgo` and no `gcc` is installed, so the
race detector is unavailable — recorded rather than skipped silently):

```
go test -tags "$TAGS" -count=20 -run TestRouteAware .                     ok  4.586s
go test -tags "$TAGS" -count=20 ./adapter/                                ok 18.532s
go test -tags "$TAGS" -count=20 ./route/rule/                             ok 21.315s
go test -tags "$TAGS" -count=10 ./adapter/outbound/                       ok  0.860s
go test -tags "$TAGS" -count=5  ./common/physicalpath/                    ok  0.964s
```

`go test -tags "$TAGS" -count=1 ./...` over the whole repository was **not** run to completion;
the directly modified packages, every caller of the validator, and the root package were.

### The regression matrix (15 cases)

`box_physicalpath_route_aware_test.go`, driven through the production loader with `Start()` as the
assertion — the claim under test is "a configuration a user can write does or does not start".

| id | verdict |
|---|---|
| `case-01-udp-terminated-before-the-path` | PASS (the reported symptom) |
| `case-02-unterminated-catch-all-is-a-real-defect` | FAIL |
| `case-03-termination-placed-after-delivery-does-not-count` | FAIL |
| `case-04-partial-termination-does-not-exhaust-the-protocol` | FAIL |
| `case-05-tcp-terminated-before-a-udp-only-path` | PASS (the mirror) |
| `case-06-dual-capable-leaf` | PASS |
| `case-07-renamed-tags-group-kind-and-leaf-type-change-nothing` | PASS |
| `case-08-group-really-delivers-udp-to-a-tcp-only-member` | FAIL |
| `case-09-each-protocol-to-its-own-outbound` | PASS |
| `case-10-nested-groups-carry-the-correct-requirement` | PASS |
| `case-10b-nested-groups-keep-the-real-defect` | FAIL |
| `case-11-unknown-matcher-follows-the-stated-policy` | PASS |
| `case-11b-never-still-skips-the-rule` | PASS |
| `case-12-logical-rule-that-does-narrow-the-network` | PASS |
| `case-12b-inverted-rule-is-composed-not-ignored` | PASS |
| `case-12c-a-route-with-no-outbound-consumes-without-attributing` | PASS |
| `case-13-unconditional-reject-is-terminal` | PASS |
| `case-14-route-options-does-not-stop-the-router` | **FAIL** (found by the adversarial audit) |
| `case-15-bypass-without-an-outbound-does-not-stop-the-router` | **FAIL** (found by the adversarial audit) |
| `case-16-direct-does-not-stop-the-router` | **FAIL** (found by the adversarial audit) |
| `case-17-hijack-dns-is-terminal` | PASS |
| `case-18-bypass-with-an-outbound-terminates-and-delivers` | **FAIL** |

Plus two diagnostic tests asserting the message states reachability and cannot claim a protocol the
routes terminated.

## F. MUTATION EVIDENCE

Eight mutations, applied to the working tree, run, and reverted (`mutate_routeaware.ps1`). **All
eight go RED as required**; the baseline is GREEN before and after each.

| # | mutation | file | test that caught it | verdict |
|---|---|---|---|---|
| A | requirement back to the entry point's theoretical capability (`physicalPathDelivery` returns a fixed `{tcp,udp}` map) | `box.go` | `case-01` | **RED** |
| B | first-match-wins removed (a terminal action no longer consumes its protocol) | `common/routeflow/reach.go` | `case-01` | **RED** |
| C | a partial match read as exhaustive | `route/rule/rule_network_scope.go` | `case-04` | **RED** |
| C2 | a condition that is not about the network read as neutral (Always) | `route/rule/rule_network_scope.go` | composition test | **RED** |
| C3 | the same, exercised through a logical rule | `route/rule/rule_network_scope.go` | `TestALogicalRuleProvesNothingAboutDelivery` | **RED** |
| D | group member capability check removed | `common/physicalpath/dryrun.go` | `case-08` | **RED** |
| E | `Never` read as `Unknown` | `adapter/rule_network_scope.go` | scope algebra test | **RED** |
| F | **every** action consumes its protocol (the reverse of mutation B, and the pre-fix classification) | `common/routeflow/reach.go` | `case-13` / `case-14`(no) / `case-17` | **RED** |

### Two findings the mutation work produced, which are part of the result

1. **A first version of mutation C2 was NOT caught**, and that was a real weakness in the *test*,
   not in the fix: the same mutation is invisible end to end, because a wrong extra charge and a
   missing charge can leave a small graph with the same verdict. The composition is where the claim
   lives, so `route/rule/rule_network_scope_test.go` now tests it directly with hand-built items.
2. **Two mutations were mis-specified and had to be rebuilt**, and both mistakes are instructive:
   * the first F replaced `!terminates(action)` with a type test for `direct`/`route-options`/`bypass`.
     That is NOT the pre-fix logic and is behaviourally identical to the fix (all three fall through
     either way), so it was a no-op that "passed". The real pre-fix logic —
     `if !adapter.IsFinalAction(action) {` — was then applied by hand and **did** turn cases 14, 15
     and 16 RED, which is the direct evidence that the audit's P1 was real and that the fix closes
     it. F is now "every action consumes", which `case-13` and `case-17` catch.
   * the first C2/F pair were also checked for the trivial trap of a replacement that does not
     apply; `Invoke-Mutation` throws if its pattern is absent, so a silent no-op cannot be recorded
     as a caught mutation.
3. **One mutation is deliberately NOT caught, and the reason is a property of the model rather than a
   gap.** Making the UNKNOWN branch consume the protocol (`live[network] = false`) is unobservable:
   keeping a network live after a non-consuming rule is both the sound answer *and* the one that
   follows from "no later rule can be proven unreachable". The `Unknown` and `Never` branches differ
   only in that flag, and after a non-consuming rule the flag must stay set either way. Recorded
   rather than papered over with a test that would pin an implementation detail.

## G. FILES / COMMITS

```
changed files (10):
  adapter/rule_network_scope.go              (new)
  adapter/rule_network_scope_test.go         (new)
  box.go                                     (modified)
  box_physicalpath_networks_test.go          (modified — one fixture, justified below)
  box_physicalpath_route_aware_test.go       (new)
  box_physicalpath_route_dedup_test.go       (modified — two fixtures, justified below)
  common/physicalpath/dryrun.go              (modified — diagnostic only)
  common/routeflow/reach.go                  (new)
  route/rule/rule_network_scope.go           (new)
  route/rule/rule_network_scope_test.go      (new)

commit SHA:     33eba5bf70e21aa8a4a79a54d354296d8fb5d105
branch:         fix/route-aware-outbound-capability-validation
remote branch:  origin/fix/route-aware-outbound-capability-validation
```

### Why the two pre-existing fixtures were edited

Both were **passing for reasons the old, order-blind model created**, and neither encoded a true
positive of the validator. Each is now unambiguous, and the property each test is *named* for is
still asserted.

**`box_physicalpath_networks_test.go` case A3** asserted that a rule with no match items "proves
nothing about delivery" and required it to START. `abstractDefaultRule.Match` special-cases the
empty item list and returns `true`, so a bare `{outbound: x}` rule is a **catch-all**: it really does
deliver both networks, and a TCP-only outbound below it cannot serve the UDP half. The fixture now
asserts the refusal. The property the old note was actually protecting — *a rule that constrains
something other than the network must not be read as delivering every network* — is preserved and
pinned instead by `case-11`, whose rule names a domain, and by the composition unit tests.

**`box_physicalpath_route_dedup_test.go`** used two rules that both said `network: udp`. The runtime
router can only ever send a UDP flow to the **first** of them, so once order is modelled the second
is correctly uncharged and stops failing — the fixture was asserting a second failure that the route
model does not produce. Both fixtures now give each rule its own protocol and each route its own
member, so both rules are genuinely reachable and the tests assert what their names say. The
second test was additionally rewritten to pin what its name claims — the manager's root-keyed
`reported` guard, which reports one line for one root however many of its members are broken — after
measurement showed the original shape could not hold together with that guard even on the unmodified
tree.

## H. REMAINING UNKNOWN

* **`route.final` is still unmodelled, and this is a measured residual rather than an oversight.**
  `{"route": {"final": "tcp-only", "rules": [{"network": "tcp", "outbound": "tcp-capable"}]}}` starts,
  although every unmatched udp flow is handed to a tcp-only outbound and fails at dial. The tempting
  fix — treat `final` as a catch-all for whatever no rule claimed — is **rejected**, and the reason is
  visible in the change itself:
  - `route.final` IS a declared outbound, so it is already a validator root, and
    `TestFinalProvesNothingAboutDelivery` (a pre-existing fixture in the fork's release-hardening
    suite) pins that a TCP-only `final` must START. Charging unclaimed networks to `final` reverses
    that verdict, so the fix would have to rewrite an existing baseline contract rather than add to
    it.
  - when `final` is unset the router falls back to a `direct` outbound created at start, whose tag is
    not known at `box.New` time, so only the `final`-is-set half is even expressible.
  - the reported symptom does not involve `final`, and the prompt's own rule is to decide rather than
    extend: "the requirement is a fact about this root — what the routes proved reaches it, or, when
    nothing is proven, what its own reachable objects advertise".

  Recorded with a reproducer so the next round can decide it deliberately. It is the one place where
  this validator can still report a false READY.
* **The race detector was not run.** `-race` requires cgo and this host has no C compiler. The
  concurrency-relevant paths (a Box built and started concurrently in the parallel matrix, repeated
  20×) were exercised without it.
* **`go test ./...` over the whole repository was not completed.** The directly modified packages,
  every caller of the validator, and the root package were run.
* **No live UDP session was driven end to end.** The runtime half of the terminal-action finding is
  `matchRule` at unit level plus the dial-time network check read from code; the auditor recorded the
  same limitation.
* **`common/sniff/dns.go` is byte-identical to upstream**, so the fork has no hardening of its four
  residuals (compression-pointer handling relies on miekg/dns's own bound; per-check `len(header) > N`
  guards; a valid header can stall the sniffer up to 65535 bytes; no per-RR validation). Out of scope
  here and recorded so the "sniff hardening" claim is not made on this file.
* **A latent nil-logger path** exists in `adapter/lifecycle.go`'s `Scope.Start`: `LogElapsed`
  dereferences the logger inside a `time.AfterFunc`, so a nil logger would panic in the timer
  goroutine about a second later. Not reachable — all 13 `NewScope` call sites pass a real logger and
  none passes `nil` — and not introduced by this change.

```
REMAINING_UNKNOWN = one measured residual (route.final, section H) plus the unrun checks above;
                    no unresolved correctness question about the fix itself
```
