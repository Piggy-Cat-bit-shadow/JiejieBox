# STATIC VALIDATION — FINAL WIRING CLOSURE

## Baseline

```text
BASE_BRANCH   = closure/0.1.6-static-validation-true-final
BASE_TIP      = d6a8a833d00990b4a266f5944844b87015947662
CODE_HEAD     = 5ad4c032b0a3a11fb48c2b666a53b4c168e56ba6
REPORT_COMMIT = self / docs-only successor of CODE_HEAD
WORK_BRANCH   = closure/0.1.6-static-validation-final-wire
WORKTREE_NAME = C:\Deepseek\内核\tf
WORKTREE_CLEAN = YES
GO_VERSION / GOOS / GOARCH = go1.26.8 / windows / amd64
```

`CODE_HEAD` is the commit whose code was tested, and every number below was produced on it. It is
NOT the branch tip: the tip is `CODE_HEAD` plus this docs-only commit. That is the provenance
convention this round adopts — a report that records the commit containing it can never converge,
because writing the SHA changes the SHA.

## The wiring closure

```text
ENDPOINT_ADAPTER_RESOLVER_PUBLISHED       = YES
ENDPOINT_ADAPTER_SERVER_DOMAIN_PUBLISHED  = YES
DEAD_RESOLVER_ACCESSOR_REMOVED            = YES
```

### What the gap was

`adapter/endpoint.Adapter` carried only type/tag/network/dependencies, and
`NewAdapterWithDialerOptions` preserved only `dialOptions.Detour`. The cross-kind graph walks
**adapter objects**, so an endpoint's own `domain_resolver` was invisible to it even though the field
is on every tunnel endpoint's options and the dial path reads it.

### The publication

Both adapters now publish the two facts, captured from the real `DialerOptions` at construction:

```go
DomainResolverReference() string   // the object's OWN explicit domain_resolver
ResolvesDomainServer()    bool      // whether its OWN remote address is a DOMAIN
```

They are **different facts** and neither is derived from the other:

```text
an object may name a resolver it never uses                  (domainResolver != "")
an object with no resolver may still read the network default (serverIsDomain)
```

`domainResolver != ""` is NOT a proxy for `serverIsDomain`, and a test pins that by asserting a
domain-server object naming a resolver that does **not** loop still starts.

### The fact is populated at every constructor site

`serverIsDomain` is variadic so a caller that has not established the fact reports `false` — the
conservative direction for a check whose job is to refuse. It is now supplied where the fact is
already computed for that object's own dialer, so the adapter and the dialer cannot disagree:

```text
openconnect   remoteIsDomain (client.go:147)          - the adapter assignment MOVED after it,
                                                        because the fact needs the parsed server URL
openvpn       openVPNClientRemoteIsDomain(options)     (endpoint.go:122)
masque        options.ServerIsDomain()
wireguard     the SAME expression its dialer uses: !M.ParseAddr(it.Address).IsValid()
direct        true   - its dialer sets RemoteIsDomain: true unconditionally
tailcat       true   - same
```

Thirteen outbound constructors additionally pass `options.ServerIsDomain()` for the default-resolver
edge. The `dns.TransportAdapter` publisher and the dead secondary accessor are **deleted**: one
derivation, on the adapter the runtime and the graph both walk.

## Measured

```text
ENDPOINT_DOMAIN_RESOLVER_CYCLE_REFUSED  = YES
DEFAULT_RESOLVER_DOMAIN_SERVER_REFUSED  = YES
DEFAULT_RESOLVER_LITERAL_SERVER_STARTS  = YES
```

| shape | verdict | note |
|---|---|---|
| endpoint explicit `domain_resolver` closes a cycle | **REFUSED** | was ACCEPTED — the dead accessor |
| … the same endpoint with no resolver reference | **STARTS** | the legal twin |
| default resolver on a **DOMAIN**-server endpoint closes a cycle | **REFUSED** | was ACCEPTED — the unpopulated fact |
| … the same endpoint with a **LITERAL** server | **STARTS** | the over-charge guard |
| the DOMAIN endpoint reached as a **selector member** | **REFUSED** | the fact survives the extra hop |
| domain server naming a resolver that does NOT loop | **STARTS** | the two facts are independent |
| outbound domain server + default resolver | **REFUSED** | `dns/remote -> outbound/proxy -> dns/remote` |
| outbound literal server + default resolver | **STARTS** | the corrected false deny |
| DNS detour cycle (control) | **REFUSED** | unchanged |

## Cross-kind graph

```text
GRAPH_MATCHES_RUNTIME_LOOKUP     = YES
OUTBOUND_FIRST_PRECEDENCE_PINNED = YES
SHADOWED_UNREACHABLE_NODE_NOT_WALKED = YES
CROSS_KIND_GRAPH_CLOSED          = YES
```

The node table is built through `Manager.Outbound(tag)` — outbound **first**, endpoint fallback —
rather than a two-pass map whose overwrite order defined the semantics. `describeOutbound` takes the
namespace from the manager rather than from an interface assertion, because MEASURED,
`*direct.Outbound` satisfies `adapter.Endpoint` and the refusal used to label both halves
`endpoint/…`. It now reads:

```text
two objects claim the tag direct: outbound/direct (direct, *direct.Outbound) and
endpoint/direct (wireguard, *wireguard.Endpoint); a tag resolves to one object, ...
```

The same-tag collision is refused at **decode** before any of this runs, so that state is
`PROVEN_UNREACHABLE` through `box.New` + `Start` and no parallel graph node is invented for it.

## Preserved invariants

```text
CONSUMER_POLICY_STILL_SOUND  = YES
GROUP_FALLBACK_STILL_SOUND   = YES
RESOLVER_PARITY_STILL_SOUND  = YES
DNS_OWNERSHIP_STILL_SOUND    = YES
```

Full focused suites green, including `case-08`, `case-10`, `case-10b`,
`TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork`,
`TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember`,
`TestAnAdvertisedOnlyMemberIsNotARefusal`, `TestConsumerDemandContractIsProvableOnly` and
`TestDestinationDNSOwnershipIsVacuousForEveryLocalDialer`. Nothing from the earlier rounds was
re-litigated or reverted.

## Mutations

```text
M-FINAL-1   endpoint DomainResolverReference returns ""        endpoint cycle test RED
M-FINAL-2   endpoint ResolvesDomainServer forced false         endpoint/domain-server pair RED
M-FINAL-2b  the fact dropped at the openconnect CONSTRUCTOR    same RED
M-FINAL-3   ResolvesDomainServer forced true                   literal-server twin RED
M-FINAL-4   endpoint-overwrites-outbound map lookup            lookup precedence RED
```

`M-FINAL-2b` is the adversarial reviewer's own controlled mutation, promoted to a permanent guard.
Every mutation is exact-string guarded and aborts before writing if the target text is absent, so a
no-op replacement fails loudly rather than being recorded as a success.

## Independent adversarial audit

A fresh reviewer that did not write the patch attacked all five claims.

```text
claim 1  endpoint adapter publishes the resolver reference    UPHELD (correct in form)
claim 2  the two facts are independent                        UPHELD
claim 3  one derivation, dead accessor gone                   UPHELD
claim 4  the default edge is charged from the runtime fact     BROKEN -> FIXED (this round)
claim 5  endpoint explicit resolver forms a real edge          UPHELD
```

It found **no false-deny counterexample** — the over-charge guard held in every graph it attacked,
including `socks -> direct`. What it found was an **under-charge**: `ResolvesDomainServer()` was
correct in form and constantly false in value because no constructor supplied it, and it reproduced a
genuinely recursive graph that was ACCEPTED. It proved the cause with a controlled mutation at the
openconnect constructor, which is now `M-FINAL-2b`. It also caught dead state left in
`dns/transport_adapter.go`, which is removed.

Not established by it: a run-time dial through an accepted loop (the reachable config is now refused,
and the endpoint/`direct` loops need a live handshake this host cannot do); wireguard domain-peer
shapes asserted structurally because wireguard endpoints fail at device setup on this host.

## Regression

```text
FOCUSED_TESTS = PASS
  .  ./adapter/  ./adapter/outbound/  ./adapter/endpoint/  ./common/physicalpath/
  ./common/dialer/  ./common/httpclient/...  ./dns/...  ./route/  ./route/rule/  ./protocol/group/

STRESS_TESTS  = PASS
  ./adapter/outbound/ -count=20   ok        ./adapter/endpoint/ -count=20   no test files
  ./protocol/group/ -count=20     ok  78.5s
  ./common/physicalpath/ -count=10 ok       ./common/dialer/ -count=10      ok  131.1s
  ./dns/... -count=10             ok  (dns 108.3s, transport 61.9s)

GO_BUILD_ALL = PASS
GO_VET_ALL   = PRE_EXISTING_ONLY (3 unsafe.Pointer findings)
  daemon/managed_service.go:75 · experimental/libbox/debug.go:11 ·
  experimental/boxdd/authenticode_windows.go:95
  -> reproduced IDENTICALLY on the untouched comparison worktree at 96a94f584
GO_TEST_ALL  = PRE_EXISTING_ONLY
  common/tls (real handshake) · common/tlsspoof · common/windivert · common/sniff (load-sensitive)
  -> each reproduced on the untouched comparison worktree before being classified
RACE         = NOT_RUN_HOST_BLOCKED (needs cgo; no C compiler on this host)
DOCKER       = NOT_RUN_HOST_BLOCKED (no daemon)
```

## Final verdict

```text
BASE_BRANCH = closure/0.1.6-static-validation-true-final
BASE_TIP    = d6a8a833d00990b4a266f5944844b87015947662
CODE_HEAD   = 5ad4c032b0a3a11fb48c2b666a53b4c168e56ba6
REPORT_COMMIT = self / docs-only successor

ENDPOINT_ADAPTER_RESOLVER_PUBLISHED       = YES
ENDPOINT_ADAPTER_SERVER_DOMAIN_PUBLISHED  = YES
DEAD_RESOLVER_ACCESSOR_REMOVED            = YES

ENDPOINT_DOMAIN_RESOLVER_CYCLE_REFUSED    = YES
DEFAULT_RESOLVER_DOMAIN_SERVER_REFUSED    = YES
DEFAULT_RESOLVER_LITERAL_SERVER_STARTS    = YES

GRAPH_MATCHES_RUNTIME_LOOKUP              = YES
OUTBOUND_FIRST_PRECEDENCE_PINNED          = YES
SHADOWED_UNREACHABLE_NODE_NOT_WALKED      = YES
CROSS_KIND_GRAPH_CLOSED                   = YES

CONSUMER_POLICY_STILL_SOUND               = YES
GROUP_FALLBACK_STILL_SOUND                = YES
RESOLVER_PARITY_STILL_SOUND               = YES
DNS_OWNERSHIP_STILL_SOUND                 = YES

MUTATIONS_RED                             = 5/5
FOCUSED_TESTS                             = PASS
STRESS_TESTS                              = PASS
GO_BUILD_ALL                              = PASS
GO_VET_ALL                                = PRE_EXISTING_ONLY
GO_TEST_ALL                               = PRE_EXISTING_ONLY
RACE                                      = NOT_RUN_HOST_BLOCKED
DOCKER                                    = NOT_RUN_HOST_BLOCKED

MAIN_UNTOUCHED                            = YES
TESTING_UNTOUCHED                         = YES
WORK_BRANCH_PUSHED                        = YES

STATIC_VALIDATION_FALSE_DENY_CLOSED       = YES
STATIC_VALIDATION_FALSE_ALLOW_CLOSED      = YES
RELEASE_BLOCKER_REMAINS                   = NO
READY_FOR_STATIC_VALIDATION_FREEZE        = YES
```

### Acceptance gate

```text
[x] endpoint.Adapter publishes explicit resolver reference
[x] endpoint.Adapter publishes server-domain fact
[x] dead duplicate resolver accessor is gone
[x] endpoint explicit-resolver cycle is refused
[x] default-resolver domain-server cycle is refused
[x] literal-server default-resolver twin starts
[x] graph uses runtime outbound-first lookup
[x] no phantom edge is built to a runtime-unreachable object
[x] all final mutations go RED
[x] H-PROVABLE consumer semantics remain green
[x] group union false deny remains fixed
[x] resolver parity remains one-source-of-truth
[x] DNS ownership parity remains green
[x] no new regression appears
[x] main/testing remain untouched
[x] report provenance is unambiguous
```

## Final principle

> The graph may only reason about facts published by the same runtime objects whose references it
> walks.

No raw-config shadow model. No second lookup policy. No protocol-type guess. No dead accessor. The
one thing this round had to learn twice — first that publishing an accessor is not the same as
populating it, and then that a fact computed at a construction site has to be handed to the object
the graph walks — is now pinned by `M-FINAL-2b`, which drops the fact at the constructor and turns the
test red.
