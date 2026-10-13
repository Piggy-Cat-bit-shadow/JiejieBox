# FAST_PATH_GROUP_POLICY_IMPLEMENTATION_REPORT

## BASELINE

- branch: `testing`
- base SHA: `5d562d605cd10bd266c0f45502c860ca0684df49`
- final working SHA: `549dea7401f4e00fa8e94405e08e8a53a1b4fa3f` (pushed to `origin/testing`)
- worktree state: clean apart from the two pre-existing submodule states (` M clients/apple`,
  ` m clients/desktop`), which are not from this session and are untouched by it.

## SEMANTICS

**The user-visible meaning.** `"fast_path": true` on a `selector` or a `urltest` authorises the flows
that ENTER THROUGH THAT GROUP to *attempt* the existing Direct Fast Path. It does not make the group
a bypassable outbound, it does not make the group's selected leaf into a fast-path node, and it does
not skip a single condition of the existing eligibility gate.

**unset / true / false.** Three distinguishable states, carried as `*option.FastPathPolicy`:

| config | resolution |
|---|---|
| absent | fall through to automatic AI tag detection |
| `true` | authorise this group |
| `false` | refuse, **even if the tag looks like AI** |

`false` beating automatic detection is the load-bearing case, and it is tested directly
(`TestFastPathCaseExplicitFalseBeatsTheAIMatcher`). Without it the override would only ever work in
the permissive direction, and an operator could not switch the fast path off for an AI-named group.

**AI auto fallback.** Automatic detection runs ONLY when no element of the chain states a policy, and
it calls the existing `trafficclass.MatchTag` — the same matcher traffic classification uses. There
is no second keyword list and no `strings.Contains(tag, "ai")` anywhere: `MatchTag` does Unicode-aware
whole-token matching so that `NaiveProxy`, `mail`, `Thailand` and `Main` stay unmatched, plus a CJK
fragment rule for `人工智能`. The tests assert both directions of that matcher as a precondition, so a
future weakening of it fails here rather than silently authorising unrelated groups.

**Supported logical groups.** `selector` = YES, `urltest` = YES, `loadbalance` = NO.

The distinction is structural, not a type-name list. `adapter.FastPathAuthorizedGroup` is declared by
groups that assert their only effect on a flow is choosing the member that serves it. `LoadBalance`
deliberately does not declare it, because it implements `FailoverOutboundGroup` and may own the DIAL
of the flow it selected — bypassing it would silently discard the retry its configuration asked for.
I verified that `LoadBalance` is the only `FailoverOutboundGroup` implementor in the tree, so
`selector` and `urltest` genuinely have no dial ownership to skip.

**Using the field on a leaf.** A configuration error naming the outbound, never a silent ignore:

```
fast_path is not supported on outbound 美国-01 of type direct: it authorises the flows
entering a logical GROUP, not the node that serves them, and only a pure selection group
(selector, urltest) may carry it
```

Refusal rather than acceptance matters here: an operator who writes the key on a node believes the
node has been authorised, and nothing reads it there — so accepting it would leave a configuration
that looks effective while the data path never consults it.

## IMPLEMENTATION

### Where the policy is produced

`route/fast_path.go` — `resolveFastPathRequest(chain, policies) bool`.

It is computed **once per flow at setup**, from the flow's own logical outbound chain, inside
`preMatchFlow` (`route/route.go`), immediately before the gate that consumes it. It is not
recomputed per packet, per read or per write, and it is not memoised.

Explicit policies are collected once during box setup (`box.go`) into
`route.FastPathPolicies`, a `map[tag]option.FastPathPolicy` — keyed by the **group's** tag, never by
a leaf's. The same placement as `TrafficClassPolicies`, for the same structural reason: the field
lives on the configuration envelope, so a tag-keyed map leaves every outbound type untouched and
cannot interfere with the `outbound.(adapter.OutboundGroup)` assertions that group resolution
depends on.

### How the policy travels with the flow

```text
preMatchFlow
  └─ chain, err := resolveOutbound(outbound, metadata, network, false)
  └─ fastPathAuthorized := resolveFastPathRequest(chain, r.fastPathPolicies)
  └─ r.canFastBypass(metadata, packetDestination, chain, outbound, fastPathAuthorized)
```

It travels as a **function argument on the stack**. That is the whole design, and it is deliberate:

- nothing is written to the leaf outbound, the group, or any object reachable from either;
- nothing is keyed by the selected leaf's tag;
- there is no global map, no cache, and no lock on this path.

Two concurrent flows whose chains share a leaf but enter through different groups therefore resolve
different values by construction rather than by discipline. `TestFastPathPolicyFollowsTheGroupNotTheLeaf`
asserts the two chains hold the *same leaf object* (`require.Same`) while producing opposite answers,
and `TestFastPathPolicyIsNotCachedOnTheLeafTag` additionally stores a policy under the LEAF's tag and
proves it authorises nothing.

### How the selected leaf is resolved

Unchanged. `resolveOutbound` (`route/route.go`) walks `Selected(network)` / `SelectForFlow(...)`
until it reaches a non-group leaf, building `chain = [outermost … leaf]`. For a selector the chain
is `["🤖 AI", "美国-01"]`; the last element is the leaf that carries the flow. My code adds no new
resolution mechanism, and the `OutboundChain` published to trackers is byte-for-byte what it was —
the existing e2e assertion `require.Equal(t, []string{"🤖 AI", "remote"}, flow.OutboundChain)` is
unaffected.

### Where the existing gate consumes it

`route/route.go` — `Router.canFastBypass(..., fastPathAuthorized bool)`. The chain condition is
**refined, not removed**:

```go
if len(chain) != 1 {
    if !(fastPathAuthorized && len(chain) >= 2 && fastPathChainReachesLeaf(chain)) {
        return BypassRefusedOutboundChain
    }
}
```

The branch is narrow on three independent conditions, and each is tested:

1. `fastPathAuthorized` — the caller resolved an authorisation for THIS flow;
2. `fastPathChainReachesLeaf(chain)` — every element in front of the leaf is a transparent
   pure-selection group, so a chain containing `loadbalance` still refuses;
3. the leaf is then checked by the **same** `adapter.BypassableOutbound` assertion and `CanBypass`
   call as any other flow, so an incapable leaf still refuses with its own verdict.

An unauthorised group flow still gets `BypassRefusedOutboundChain`, unchanged.
`TestFastBypassOpaqueGroupRefusesEvenWhenAuthorized` proves authorisation alone is never enough.

**No other blocker is touched.** FakeIP, domain destination, sniffed domain, resolved candidates,
destination rewrite, route original destination, UoT datagram destinations, UDPConnect, domain
unmapping, custom UDP timeout, network options, TLS options, tracker and process metadata are all
unchanged, in the same order, on the same conditions. The eleven existing refusal tests
(`TestFastBypassFakeIPIsIneligible`, `...DomainDestinationIsIneligible`, `...SniffedDomainIsIneligible`,
`...ResolvedCandidatesAreIneligible`, `...DestinationOverrideIsIneligible`, `...TrackerIsIneligible`,
`...UDPConnectIsIneligible`, `...CustomUDPTimeoutIsIneligible`, `...NetworkOptionsAreIneligible`,
`...TLSOptionsAreIneligible`, `...NonTunInboundIsIneligible`) all still pass unchanged, and
`TestFastBypassAuthorizedChainStillRefusesWhenLeafCannotBypass` adds the authorised case: with
authorisation granted the leaf's refusal still applies, reported as `BypassRefusedOutboundSemantics`
rather than being overridden.

**`fast_path: true` never yields `BypassAllowed` by itself.** Every gate after the chain check still
runs; the branch only removes the mechanical refusal of "there is a group here".

### Nested group rule

**The first explicit policy, scanning outermost to inner, wins; an explicit decision anywhere
suppresses automatic detection for the whole chain; otherwise the first AI-looking level, outermost
to inner; otherwise not authorised.**

Identical to `resolveTrafficClass`, including the reasoning: the intent belongs to the group the flow
was routed INTO, not to the node that happens to serve it. All three sub-cases are tested in
`TestFastPathCaseOutermostExplicitDecisionWins` — outer `false` suppresses an inner AI group, outer
`true` authorises, and no-policy-anywhere falls back to the first matching level.

### A design error I made and fixed

`adapter.FastPathAuthorizedGroup` first shipped as an **empty interface** embedding `OutboundGroup`.
That was wrong, and it was wrong in a way that silently disabled the safety property: `OutboundGroup`'s
method set is exactly what any group-shaped type provides, so `anyType.(FastPathAuthorizedGroup)`
succeeded for the test double that meant the *opposite* of transparency. The compiler confirmed it —
`opaqueGroup` and `transparentGroup` had identical method sets (9 methods each) and were
indistinguishable by assertion. The safety test that existed to catch an opaque group was passing for
the wrong reason.

It now has a named method, `FastPathTransparent() bool`, which cannot be satisfied by accident, and
`TestFastPathGroupMarkerIsNotSatisfiedByAccident` is the tripwire that fails if the method is ever
removed. Recorded here because the failure mode is invisible in review and only a test that asserts
the *negative* exposes it.

### urltest probe traffic

Verified, not assumed: urltest's delay probes call the member outbound's `DialContext` directly
(`protocol/group/urltest.go`) and never pass through `Router.PreMatch`, so they cannot inherit a
business flow's policy. The probes are not business flows at all and need no policy.

## SAFETY

```text
leaf state mutation:                     NO   — the authorisation is a call argument; nothing is
                                                written to any outbound object
global tag policy cache:                 NO   — the only map is group-tag -> explicit policy, built
                                                once at box setup and never written at flow time
canFastBypass still authoritative:       YES  — it is the only thing that decides eligibility
BypassVerdict changed:                   NO   — no verdict added, removed, renamed or reordered
L0 changed:                              NO   — dnsHijackAddress / dnsHijackByPort /
                                                routeAddressSet / routeExcludeAddressSet /
                                                Router.PreMatch order untouched
DNS/FakeIP ordering changed:             NO   — FakeIP still refuses at its existing check, before
                                                anything this change touches
traffic_class coupling introduced:       NO   — separate field, separate resolver, no derivation
                                                either way (tested both directions)
sibling-group contamination:             NO   — tested with require.Same on the shared leaf
```

## FILES CHANGED

**Production**

| path | purpose |
|---|---|
| `option/fast_path.go` | new. `FastPathPolicy`, the three-state config type, JSON and schema |
| `option/outbound.go` | new `FastPath *FastPathPolicy` on the shared envelope |
| `option/schema.go` | documents the key on the polymorphic base |
| `adapter/experimental.go` | new `FastPathAuthorizedGroup` capability with `FastPathTransparent()` |
| `protocol/group/selector.go` | declares the capability; also removes a duplicated declaration block |
| `protocol/group/urltest.go` | declares the capability |
| `route/fast_path.go` | new. Policy resolution, transparency walk, carrier predicate |
| `route/router.go` | `fastPathPolicies` field + `SetFastPathPolicies` |
| `route/route.go` | one argument on `canFastBypass`, the narrow chain branch, the resolution at the call site |
| `box.go` | group-only validation and policy collection at load time |

**Tests**

| path | purpose |
|---|---|
| `route/fast_path_group_test.go` | the gate's two invariants, the narrowness proofs, the precedence matrix, the per-flow separation and concurrency tests |
| `option/fast_path_test.go` | the three-state decode contract and round-trip |
| `box_fast_path_group_policy_test.go` | case F: refusal on a leaf, on a proxy leaf and on loadbalance; acceptance on selector and urltest |
| `route/fast_bypass_test.go` | the existing chain test, re-commented as the UNAUTHORISED half |
| `route/bypass_capability_cost_test.go`, `direct_offload_bench_test.go`, `direct_offload_hits_test.go`, `fakeip_bypass_boundary_test.go`, `process_transient_miss_test.go` | the new fifth argument; every site is a single-leaf chain, so `false` is the faithful translation |

## TESTS

| test | result |
|---|---|
| `go build ./...` | PASS |
| `go vet` on every touched package | PASS |
| `gofmt -l cmd include option protocol route service transport common dns adapter test/contract test/jiejie` | clean |
| `go test ./route/...` | PASS (58.3 s) |
| `go test ./option/...` | PASS |
| `go test ./protocol/group/` | PASS |
| `go test ./adapter/...` | PASS |
| `go test ./common/trafficclass/` | PASS |
| `go test .` (root, with the feature tags the endpoint tests need) | PASS |
| `TestFastBypass\|TestFastPath` — `-count=20` | PASS (no order dependence) |
| `TestFastPathPolicyConcurrentFlowsDoNotShareState` — `-count=50` | PASS |

**NOT RUN, and therefore not claimed:**

- **`-race`.** Blocked by this host, after three genuine attempts rather than one. The toolchain has
  no usable C compiler for Go's Windows race build: `gcc`/`cc` are absent, and the only `clang` is
  Swift's, which hands the link to MSVC's `link.exe` and dies on
  `LNK1143: invalid or corrupt file: no symbol for COMDAT 0x6`. Redirecting it to the LLVM linker
  that ships beside it (`-fuse-ld=lld`, `lld-link.exe` present) gets further and then fails on the
  linker script Go injects: `lld-link: warning: ignoring unknown argument '-T'` followed by
  `error: ...fix_debug_gdb_scripts.ld: unknown file type`. Go's race build on Windows expects MSVC's
  linker, so this is an environment limit and not something the change can work around. The
  concurrency tests therefore ran without the detector, at `-count=50` (and 20× for the whole set).
  The design has no shared mutable state for a race to exist on — the authorisation is a stack
  argument — but that is an argument, not a measurement, and `client-macos.yml`'s deep-check race
  step is where it should be confirmed.
- **CI.** The push-triggered workflows were dispatched at the end of this session; their result is
  reported in the session summary, not in this document.

## KEY CASES

| case | result |
|---|---|
| **same leaf / fast group vs normal group** | PASS — `require.Same` on the leaf object; authorised chain → `BypassAllowed`, ordinary chain → `BypassRefusedOutboundChain`; reverse order asserted; 200 concurrent iterations per side |
| **explicit false + AI tag** | PASS — `false` wins over the matcher, and the flow is really refused at the gate |
| **unsupported leaf** | PASS — configuration error naming the outbound, for `direct`, a `socks` proxy and `loadbalance`; the key never becomes a node property |
| **FakeIP** | PASS unchanged (`TestFastBypassFakeIPIsIneligible`) |
| **domain / sniff** | PASS unchanged (`...DomainDestinationIsIneligible`, `...SniffedDomainIsIneligible`) |
| **TLS rewrite** | PASS unchanged (`...TLSOptionsAreIneligible`, 3 sub-cases) |
| **selector switch** | PASS — `TestFastPathSurvivesASelectorSwitch` (A→B→A, verdict stays `BypassAllowed`, served leaf follows, neither leaf mutated) and `TestFastPathSelectorSwitchConcurrentWithFlows` (4 readers × 500 resolutions against a switcher, authorisation constant) |
| **urltest** | PASS — `TestFastBypassUrltestGroupIsEligibleToo`; probe traffic shown to bypass the router entirely |

## DIFF SIZE

```text
production:  278 added lines across 10 files (55 of them comment lines in the two new files;
             net tracked diff +518 / -9)
tests:       659 added lines across 8 files
total:       ~940 lines
```

Above the brief's "200–500 lines total" guidance, and the reason is comment volume rather than logic:
the two new production files are 214 lines of which 117 are documentation, and the non-comment code
across all three new source files is roughly 87 lines. The implementation itself adds no new
subsystem — one flag field, one resolver function, one boolean argument and one narrow branch. No
Router rewrite, no L0 change, no DNS/FakeIP work, no traffic-class rework, no policy engine.

## RESIDUAL RISKS

1. **`urltest` selection updates under concurrency.** The authorisation is read once per flow from
   the chain `resolveOutbound` produced, so it is consistent with the member that will actually serve
   the flow, and the selector-switch concurrency test covers the same interleaving shape. If the
   group re-selects between resolution and the bypass decision, the flow still takes the member it
   resolved — which is the pre-existing behaviour, not something this change introduces — but that
   specific interleaving has no dedicated test.

2. **`-race` unverified here.** See TESTS. Three attempts, all blocked by the host's C toolchain. The
   no-shared-state claim is architectural and backed by a 50× concurrency test, but the detector has
   not seen it.

3. **The chain gate is now richer.** `fastPathChainReachesLeaf` walks every element in front of the
   leaf on every authorised flow. It is O(chain length) at flow setup, off the packet path, and it
   runs only after the cheap authorisation check — but it is new work on a path that previously did
   one integer comparison. No allocation change was observed in the refusal cases;
   `BenchmarkBypassDecision` still covers the single-leaf path unchanged, and the authorised case was
   not benchmarked.

4. **`loadbalance` is excluded by omission, not by an explicit denial.** A future group type that
   forgets to NOT declare `FastPathTransparent` would silently become bypassable. What guards it is
   that a type must actively write the method that asserts it adds no flow semantics — the guard is
   honesty, not the type system.

5. **`route/route.go` gained an argument, not a field.** Every future caller of `canFastBypass` must
   decide what authorisation to pass. Passing `true` without a resolved chain would be wrong; the
   parameter name and the doc comment say so, but nothing enforces it.

## RESULT

```text
FAST_PATH_GROUP_POLICY = COMPLETE
```

against the scope agreed in this session: selector + urltest, explicit-or-AI authorisation, per-flow
propagation, and a refined `BypassRefusedOutboundChain` with every other blocker untouched.

Every case in the brief's §14 matrix has a passing test, including §14.3 (selector switching) and
§14.4 (urltest, plus the probe-traffic check the brief asks to verify specifically). The one thing
this report does not claim is a `-race` run, for the environment reason given above.
