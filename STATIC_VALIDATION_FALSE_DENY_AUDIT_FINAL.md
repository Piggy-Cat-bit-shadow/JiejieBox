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

### What the audits found and this round did NOT fix — deliberately

| finding | why not fixed here |
|---|---|
| A non-filtering group whose members differ is refused member-by-member when **nothing** is proven about delivery (`selector[tcp-only, dual]`, no rule, no consumer). The union over its exits is required of every member, which `tcp-only` cannot satisfy. | The narrower reading (ask each exit for what *it* advertises) was **implemented and MEASURED to give up a true positive**: `TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork` and `TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember` both stop refusing, because a selector's *unselected* member is then not held to the group's advertised set at all. Those two pin the runtime failure this dry run exists to prevent. Not a local change: the group-membership contract must be decided first. |
| `DestinationDNSOwnership` marker parity — 7 types that resolve the destination locally are refused; the validator never calls the capability method; the resolver half models 2 of 4 resolver sources | Reported by `fdE`, **not yet independently verified by me**. High impact (5 of the 7 were measured refused through `Box.Start`), so it must be confirmed before any code changes. |
| `bridge` `Network()` advertises `{tcp,udp,icmp}` while both L4 entry points unconditionally fail → false allow | Reported by `fdG`, **not yet independently verified**. |
| Cross-kind: endpoints are folded into the outbound map, so a shared tag overwrites the outbound's edges (false cycle); an endpoint's `domain_resolver` edge is not modelled (false allow of the fork's own documented hang shape); `route.default_domain_resolver` edge not modelled | Reported by `fdF`, **not yet independently verified**. |
| `http_clients` detour naming a nonexistent outbound starts (lazy dialer) while the identical DNS-transport detour is refused | Reported by `fdC`, **not yet independently verified**. |

I am stating plainly that five audit findings are **unverified by the main agent**. They are the
input to the next round, not conclusions.

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
FALSE_DENY_AUDIT_COMPLETE      = NO   (5 audit findings unverified by the main agent; 1 false deny recorded and not fixed)
FALSE_ALLOW_AUDIT_COMPLETE     = NO   (4 audit-reported shapes unverified; SV-NIL diagnosed but not resolved)
ROUTE_DEMAND_MODEL_SOUND       = YES  (A1-A10 asserted, both halves each; 8 mutations RED)
INTERNAL_CONSUMER_DEMAND_SOUND = NO   (fdC; no consumer is a root)
GROUP_EDGE_SEMANTICS_SOUND     = YES  (fdG verified all three groups against their real methods)
CUSTOM_PROTOCOL_CAPABILITY_PARITY = NO (fdG found `bridge`; unverified by me)
RELEASE_BLOCKER_REMAINS        = YES  (the unverified DNS-ownership false denies, if confirmed)
```

The objective was fewer false denials with no new false allows. This round removed a process crash, a
false deny, and a misleading diagnostic, and added 15 new assertions each paired with a refused twin.
It did **not** claim the audit is complete: the honest state is three confirmed-and-fixed defects,
one confirmed-and-recorded false deny, and five high-impact audit findings that a second pass must
verify before anything is changed on their word.
