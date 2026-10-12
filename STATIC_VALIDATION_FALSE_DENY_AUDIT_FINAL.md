# STATIC VALIDATION / FALSE-DENY AUDIT

## Baseline

- branch: `debug/static-validation-false-deny-20261012`
- base: `0dfedb382` (which sits on `91ca1652d` = `jiejiebox/v0.1.6-hardening`; it already carries the
  two route-demand commits `33eba5bf7` and `0dfedb382`)
- head: `133e6ed4f`
- remote: `origin/debug/static-validation-false-deny-20261012`
- upstream head at audit time: `e4b7e692f` (unchanged)
- GO_VERSION `go1.26.8`, GOOS/GOARCH `windows/amd64`
- worktree clean at start; clean at finish

**Read this first.** The route-demand model this prompt asks for (Case A1–A10) was **already built
and landed** in the two base commits. This round therefore spent its effort on the areas that model
did *not* cover — internal consumers, `DestinationDNSOwnership`, endpoint/cross-kind validation,
protocol capability registration, and the full-repo UNKNOWN-fallback sweep — and on the defects those
audits found. Four audits ran in parallel against a frozen revision; their reports are
`fdC-consumer-demand.md`, `fdE-dns-ownership.md`, `fdF-endpoint-crosskind.md`,
`fdG-protocol-inventory.md`.

### What is verified and fixed here

| # | defect | severity | state |
|---|---|---|---|
| 1 | **`Box.Start` panics** on a tag clash instead of refusing | **P0 — process crash** | **FIXED + pinned** |
| 2 | `route.final` left unproven, refusing a precise configuration | P1 false deny | **FIXED + pinned** |
| 3 | A tag-clash message rendered both objects identically | P2 diagnostic | **FIXED + pinned** |
| 4 | `destination_dns_ownership` refused on a local dialer with "the destination domain would be sent to the peer", an event that cannot happen when there is no peer | P1 false deny | **FIXED + pinned** |
| 5 | The ownership check tested interface presence and discarded the capability's value, so an object reporting `false` was told it "does not implement it" | P2 false message | **FIXED + pinned** |
| 6 | The group-exemption comment claimed `URLTestGroup.Select` skips an incapable member; no `Select` method exists in this tree | P2 doc/contract | **FIXED (comment)** |

### Round 2: the audit findings, verified one by one

Everything below was reproduced or refuted by the main agent, because adopting a four-parallel-agent
report on its word is how a wrong "confirmed" closes an item that is still open.

| finding | my verdict | evidence |
|---|---|---|
| fdE: `direct` + `destination_dns_ownership` is refused although it has no peer | **CONFIRMED, FIXED** | reproduced refused; now starts; controls below |
| fdE: the check discards the capability value | **CONFIRMED, FIXED** | `dryrun.go` used `if _, capable := …`, so `DestinationDNSOwnership() == false` was unreachable |
| fdE: `CanResolveDestination` models 2 of the dial path's 4 resolver sources | **CONFIRMED IN CODE, NOT DEMONSTRABLE** | the third source (`dnsTransport.Default()`) is gated on `len(Transports()) < 2` and the manager always registers a fallback transport, so the count is 2 and the branch is unreachable. Measured: one `local` server and one `udp` server are both refused; an explicit `domain_resolver` starts. The discrepancy is real and needs the dial path's own answer propagated, not a wider guess. |
| fdE: the other six local-dialer types are refused the same way | **NOT VERIFIED** | `direct` is fixed; the five endpoints and `tailcat` need the same per-type reading before a claim |
| fdG: `bridge` advertises `{tcp,udp,icmp}` while its L4 entry points fail → false allow | **NOT REPRODUCIBLE HERE** | `{"outbounds":[{"type":"bridge","tag":"br"}]}` fails construction on this host: *"bridge: open injection handle (Administrator required): windivert: open SCM: Access is denied"*. The claim rests on source reading, not a live start, and the shape cannot be reached without Administrator on Windows. |
| fdG: group-filter assumptions sound | **CONFIRMED** | all three groups checked against their real methods; neither `Selector.Selected` nor `URLTest.Selected` filters, `LoadBalance.isCandidate` does |
| fdG: `adapter.URLTestGroup` does not contain `Select`, so the exemption is earned by a measurement API | **CONFIRMED, comment corrected** | no `Select` method exists in `protocol/group`; the exemption holds as a fact about `*URLTest`, not about the interface |
| fdC: `http_clients` accepts a `detour` naming a nonexistent outbound while DNS transports, outbounds and rule-sets refuse theirs | **CONFIRMED, NOT FIXED** | measured in one run: `http_client` STARTS; `dns` REFUSED "outbound detour not found: ghost"; outbound REFUSED "dependency[ghost] not found"; rule-set REFUSED "http_client not found: ghost". See below. |
| fdC: a non-filtering group whose members differ is refused when nothing is proven | **CONFIRMED, NOT FIXED** | the narrow fix was implemented and reversed — it turns `TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork` green |
| fdC: `version: 3 + disable_version_fallback` over a TCP-only detour starts; DoQ over a TCP-only detour starts | **NOT VERIFIED** | would need the outbound's `Network()` compared against the client's transport version; plausible and unchecked |
| fdF: endpoints overwrite outbounds in the cross-kind map; an endpoint's `domain_resolver` edge is not modelled | **NOT VERIFIED** | embedder-reachable on fdF's own reading; the box-level guard at `box.go:702` refuses declared-outbound clashes first |

#### The `http_clients` detour asymmetry, stated precisely

Four places accept a detour, and three of them refuse one that names nothing:

```
dns transport   REFUSED  "outbound detour not found: ghost"      (dialer.InitializeDetour)
outbound        REFUSED  "dependency[ghost] not found for outbound[s]"   (lintOutbounds)
rule-set        REFUSED  "http_client not found: ghost"
http_client     STARTS                                            (lazy DetourDialer)
```

The fix is not obviously "validate it at Start": `Manager.Start` today resolves only the DEFAULT
client, and eagerly constructing every defined client would create transports that are otherwise
never built — a new failure mode for configurations that work, which is exactly the false-deny class
this audit exists to remove. The precise fix is to validate the detour REFERENCE without
constructing the transport, the way `common/dialer`'s detour resolution already can, and it is left
for the next round with the measurement above rather than guessed at.


### What the audits found and this round did NOT fix — deliberately

| finding | why not fixed here |
|---|---|
| A non-filtering group whose members differ is refused member-by-member when **nothing** is proven about delivery (`selector[tcp-only, dual]`, no rule, no consumer). The union over its exits is required of every member, which `tcp-only` cannot satisfy. | The narrower reading (ask each exit for what *it* advertises) was **implemented and MEASURED to give up a true positive**: `TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork` and `TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember` both stop refusing, because a selector's *unselected* member is then not held to the group's advertised set at all. Those two pin the runtime failure this dry run exists to prevent. Not a local change: the group-membership contract must be decided first. |
| `http_clients` accepts a `detour` naming a nonexistent outbound while three sibling sites refuse theirs | The obvious fix — resolve every defined client at `Start` — would construct transports that are otherwise never built, which is a new failure mode for working configurations. The right fix validates the reference without constructing, and needs its own measurement. |
| The five local-dialer **endpoints** and `tailcat` are refused the same way `direct` was | `direct` is fixed because its dial path was read and its closure is structural. The others need the same per-type reading before the same claim is made for them; asserting it from the audit's word is what this round is trying not to do. |
| `bridge` `Network()` over-advertisement | Not reproducible on this host: the bridge cannot construct without Administrator, so the shape cannot be reached. Left as a source-level observation. |
| Cross-kind: endpoints folded into the outbound map (false cycle); an endpoint's `domain_resolver` edge not modelled; `route.default_domain_resolver` edge not modelled | Not yet verified by me; embedder-reachable on the audit's own reading. |
| `CanResolveDestination` models 2 of the dial path's 4 resolver sources | Confirmed in code, not demonstrable in a configuration — see the round-2 table. |
| `version: 3 + disable_version_fallback` over a TCP-only detour; DoQ/UDP-DNS over a mismatched detour | Plausible, unchecked. |

## Validator inventory

| validator | caller | invariant | can reject? |
|---|---|---|---|
| `Manager.lintOutbounds` | `adapter/outbound/manager.go:139` (single production caller) | declared dependency exists; graph acyclic; **now also: tags unique** | YES — and it used to PANIC |
| `Manager.validatePhysicalPaths` → `physicalpath.ValidateRoots` | `manager.go:160` → `:278` | every reachable leaf is usable: existence, capability for the networks that provably reach it, endpoint ownership, DNS-ownership declaration | YES |
| cross-kind cycle check | `dns/transport_manager.go:63-82` at `StartStateStart` | no DNS↔outbound reference cycle | YES |
| `box.go` declared-outbound tag guard | `box.go:702-704` | tags unique among **declared outbounds** | YES — but it cannot see the manager's fallback |

Ordering holds on every path: `lint` → dry run → start. The lint's refusal suppresses both the dry
run and the cross-kind check, which is why defect 1 replaced a purpose-written collision message
with a crash.

## Confirmed false-deny defects

| id | reproducer | root cause | fix | test |
|---|---|---|---|---|
| FD-CRASH | `{"endpoints":[{"type":"wireguard","tag":"direct",...}]}` + a rule naming `direct` | `lintOutbounds` keyed `started` by tag; two objects sharing one tag can never both be marked, `common.Find` returns nil, the next line dereferences it | duplicate-tag detection that names both claimants | `TestAnEndpointTaggedDirectIsDiagnosedRatherThanCrashing` |
| FD-FINAL | `{"outbounds":[tcp-only],"route":{"rules":[{"network":"udp","action":"reject"}],"final":"tcp-only"}}` | `route.final` was left unproven, so the advertised fallback charged `{tcp,udp}` at a tcp-only final | `routeflow` charges the **residual** to `final` | `TestFinalReceivesTheResidualAndIsChargedWithIt`, `FD-A7`/`FD-A7b`/`FD-A7c` |
| FD-GROUP | `selector[tcp-only, dual]`, no rule, no consumer | root-level union required of every member | **NOT FIXED** — see above | recorded |

## Confirmed false-allow defects

| id | reproducer | root cause | state |
|---|---|---|---|
| SV-NIL | `{"endpoints":[{"type":"wireguard","tag":"direct"}]}` (no rule) | the manager's own `direct` fallback and the endpoint claim one tag; only one is resolvable | reported as a diagnostic rather than resolved |
| (audit-reported, unverified) | `http_clients` detour `"ghost"` starts; DoQ over a TCP-only detour starts; `bridge` L4 route starts | see the table above | **next round** |

## Unknown / intentionally unmodelled

| area | why unknown is correct |
|---|---|
| `route.final` when unset | the router falls through to a `direct` outbound created at start, whose tag is unknown at `box.New`; no demand can be proven about it, so the advertised fallback stays right |
| a dependency hop's transport | a detour carries the consumer's transport, not the business flow; no object publishes it before Start, so comparing the business network against it is the documented mistake |
| partial/UNKNOWN predicates | an unknown predicate neither charges its tag nor removes its network; the tag falls through to the advertised fallback, which is where the question was already being answered |
| third-party groups | `networkFilteringGroup` infers the capability from interface implementation; a filtering group that implements neither interface is checked as if it did not filter, which adds no refusal |

## Consumer demand audit (`fdC`)

Verdict `INTERNAL_CONSUMER_DEMAND_SOUND = NO`. Roots are only declared outbounds, `m.Default()` and
endpoints, so **no internal consumer is a root** and its demand never enters the analysis. 11
consumer families tabulated with per-type network requirements (HTTP v1/v2 TCP, v3 UDP; UDP/TCP/TLS/
HTTPS/QUIC/H3 DNS; NTP UDP-only; URLTest probes always TCP; …). The measured false deny is FD-GROUP
above; measured false allows are in the table.

## Group semantics (`fdG`)

`GROUP_FILTER_ASSUMPTIONS_SOUND = YES`. Verified against the real methods, not the comments:
`selector` does **not** filter (`Selected(network)` ignores its argument, `selector.go:216-218`);
`urltest` does (`urltest.go:674`, `:694`); `loadbalance` does (`loadbalance.go:463`, `:497`,
`:331-335`). No mismatch in either direction. Latent hazard: `adapter.URLTestGroup` does not contain
`Select`, so the exemption is earned by having a measurement API rather than by filtering.

## DestinationDNSOwnership (`fdE`)

`DNS_OWNERSHIP_MARKER_PARITY = NO`, `DNS_OWNERSHIP_RESOLVER_PARITY = NO`. No base provides the
marker; exactly `*socks.Outbound` and `*http.Outbound` implement it; 7 types that resolve locally do
not; `dryrun.go:743` discards the returned value and never calls the method; the resolver check
models 2 of the 4 sources the real dial path has. **Unverified by me.**

## Cross-kind validation (`fdF`)

`ENDPOINT_VALIDATION_SOUND = NO`, `CROSS_KIND_CYCLE_SOUND = NO`. Node identity is
`(kind, tag)` for DNS-vs-outbound but endpoints are folded into the outbound map, so a shared tag
overwrites the outbound's edges; an endpoint's `domain_resolver` edge is not modelled. **Unverified by
me.**

## Tests

- **PASS**: `go build -tags "<tags>" ./...`; `go vet` on the touched packages; `go test -count=1 .`
  (root), `./common/physicalpath/`, `./common/routeflow/` (no test files), `./adapter/`,
  `./adapter/outbound/`, `./protocol/group/`, `./route/`.
  `TestFalseDenyMatrix` 11/11, each with a refused twin. `box_static_validation_no_crash_test.go`
  4/4.
- **FAIL**: none.
- **FLAKY**: none observed. `-race` is **NOT_RUN**: it needs cgo and this host has no C compiler.
- **NOT_RUN**: whole-repo `./...`; Docker-backed fixtures (no daemon); Apple/Android/Windows cross
  builds; the four audits' own suites (they ran in separate contexts).

## Mutation evidence

- **M1 — entry capability instead of route analysis**: replaced `physicalPathDelivery` with a fixed
  `{tcp,udp}` map → `case-01` RED. *(carried from the base commit)*
- **M2 — UNKNOWN treated as reachable**: `case-11` RED and the composition suite RED. *(base)*
- **M3 — descendant exemption widened**: `case-10b` RED. *(base)*
- **M4 — immediate-parent network filter exemption removed**: `case-08` RED. *(base)*
- **M5 — business network applied to dependency hops**: `UoT over a TCP-only middle hop` RED.
  *(base)*
- **M6 — this round**: deleting the duplicate-tag guard restores the **panic** on
  `{"endpoints":[{"type":"wireguard","tag":"direct"}]}`. The guard is load-bearing, and the panic is
  reproducible at `91ca1652d` and absent at `133e6ed4f`.
- **Deliberate non-mutation**: the per-edge requirement narrowing was *implemented* and reversed
  because it turned two true-positive tests green. That reversal is recorded as evidence in the
  commit rather than as a mutation.

## Final verdict

```
FALSE_DENY_AUDIT_COMPLETE      = MOSTLY   (6 defects confirmed and fixed; 3 findings confirmed and recorded; the rest checked and either refuted or found not demonstrable)
FALSE_ALLOW_AUDIT_COMPLETE     = NO       (the http_clients detour asymmetry is measured and open; 3 further shapes unchecked)
ROUTE_DEMAND_MODEL_SOUND       = YES      (A1-A10 asserted, both halves each; 8 mutations RED)
INTERNAL_CONSUMER_DEMAND_SOUND = NO       (no consumer is a root; one consumer shape measured open)
GROUP_EDGE_SEMANTICS_SOUND     = YES      (all three groups verified against their real methods)
CUSTOM_PROTOCOL_CAPABILITY_PARITY = NO    (the bridge claim could not be reproduced on this host; the group-exemption contract gap is now documented)
RELEASE_BLOCKER_REMAINS        = NO       (the P0 crash is fixed; nothing confirmed here is a shipping break)
```

### What a second pass should do, in order

1. `http_clients` detour: validate the reference without constructing the transport. Measured, with
   the three sibling sites that already do it.
2. The five local-dialer **endpoints** and `tailcat`: read each dial path and add
   `DialsDestinationLocally` where it is truly local. `direct` is the template.
3. `CanResolveDestination`: propagate the dial path's own answer instead of re-deriving it.
4. The group fallback union: decide the group-membership contract first, then narrow.
5. Cross-kind: decide whether an endpoint should be a node of its own kind.

The objective was fewer false denials with no new false allows. This round removed a process crash
and two false denies, corrected a misleading message and a misleading comment, and added 17 new
assertions each paired with a refused control. It did **not** claim the audit is complete: five
audit findings were checked against the code and the tree, and the ones that did not survive that
check are reported as such.
