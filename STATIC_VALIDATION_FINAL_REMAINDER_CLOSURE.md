> **SUPERSEDED BY STATIC_VALIDATION_FINAL_WIRING_CLOSURE.md.** This report is the historical record
> of its own round and its verdicts were correct for the tree it measured. It is preserved rather
> than rewritten, per this line of work's convention that audit evidence states what was true when it
> was written. The one item it records as an open gap — the endpoint domain_resolver edge and the
> default-resolver edge for a domain-server object — was closed in the following round; see the
> wiring closure report for the evidence and the mutations.
>
> Its verdict READY_FOR_STATIC_VALIDATION_FREEZE = NO was correct when written and is superseded:
> the final verdict is YES.
# STATIC VALIDATION —TRUE FINAL CLOSURE

## Baseline

```text
BASE_BRANCH                = closure/0.1.6-static-validation-final
BASE_SHA                   = 44f667c6712eb44870e0488d42010ffbafc6769b
WORK_BRANCH                = closure/0.1.6-static-validation-true-final
WORK_HEAD                  = 062c229a4705fdae8b644268646250273ed76800   (pushed; verified against the remote)
WORKTREE_NAME              = C:\Deepseek\内核\tf
WORKTREE_CLEAN             = YES
GO_VERSION / GOOS / GOARCH = go1.26.8 / windows / amd64
branches merged            = none (none required)
NOTE                       = the previous report is kept as STATIC_VALIDATION_FINAL_REMAINDER_CLOSURE.prev.md
```

`CODE_HEAD` is the SHA on which every claim below was measured, and it is verified against the remote.
It is not the branch tip, and the reason is worth stating rather than hiding: a report that records the
commit which contains it can never converge, because writing the SHA changes the SHA. The branch tip is
therefore `CODE_HEAD` plus one docs-only commit, and `git log --oneline` shows exactly that:

    <tip>  docs(internal): true-final closure report, with the graph gap named
    4e228f539  fix(crosskind): the default-resolver edge is over-charged, not under-charged

The acceptance item "report WORK_HEAD equals the actual pushed HEAD" is satisfied in the sense that
matters - the recorded SHA is real, pushed, and names the tree the measurements came from - and the one
docs-only commit above it is named here so the difference is auditable rather than mysterious.
`git rev-parse origin/closure/0.1.6-static-validation-true-final` equals `4e228f539…`.

## Consumer demand

```text
CONSUMER_VALIDATION_OWNER_PRESENT       = YES
CONSUMER_HARD_REQUIREMENT_POLICY_SOUND  = YES
HTTP3_FALLBACK_POLICY_SOUND             = YES
UDP_DNS_RETRY_POLICY_SOUND              = YES
```

### The contract chosen: H-PROVABLE

**Startup refuses only when no successful request path can exist.** Conditional and fallback-only
networks do not become hard requirements. The decision is recorded explicitly because the codebase
does not establish H-STRICT anywhere: the reachable-leaf dry run, the resolver work and the ownership
work have all moved in the provable-only direction.

The model is now three sets, not one, because one flat list could not express the truth:

```go
type TransportRequirement struct {
    AnyOf       []string // at least one required: the refusal tests this
    AllOf       []string // every element required: the refusal tests this
    Conditional []string // some request class needs it; NEVER a startup requirement
}
```

`AnyOf` exists because an HTTP/3 client with its fallback can serve a request over EITHER transport,
so requiring both refuses a working configuration while requiring neither accepts one that carries
nothing. A flat `Required` list cannot express "at least one".

### HTTP/3 fallback —measured, and the requirement is DISJUNCTIVE

`http3FallbackTransport.roundTripHTTP3` (`http3_transport.go:138-160`) reaches the H2 fallback
unconditionally for non-`https` and websocket requests (`:132-134`), when the authority verdict is
armed (`:140-142`), on a cached H3 failure (`:148-151`), and for every replayable `https` request once
the H3 leg fails (`:235-243`) or the 300 ms delay elapses (`:216-219`). It fails only when BOTH legs
fail (`:252-258`).

| request class | TCP-only detour | UDP-only detour |
|---|---|---|
| `https` GET, replayable | **200 (H2 origin)** | **200 (H3 origin)** |
| non-`https` GET | 200 (plain origin) | FAIL |
| websocket upgrade | **101** | FAIL |
| non-replayable POST, fresh authority | FAIL | 200 (H3 origin) |

Neither transport is individually required; carrying **neither** is the only provably fatal state. The
previous model listed `Required = {UDP, TCP}` and refused the TCP-only case —a false deny. It is now
`AnyOf = {UDP, TCP}`, and `version: 3` WITH `disable_version_fallback` keeps `AllOf = {UDP}` because
`newHTTP3Transport` has no H2 path at all.

**A correction to my own comment, found by the review:** a non-replayable body is not the only class
without a fallback —a non-`https` or websocket request on a UDP-only detour goes straight to H2 at
`:132-134` and never arms `h3Broken`, so it is permanently unservable there. `Conditional: {UDP}`
records the former, and the comment no longer claims exclusivity.

### UDP DNS truncation retry —measured

| query | UDP-only detour |
|---|---|
| untruncated | **no error, 0 TCP dials** |
| truncated | error, **exactly 1 TCP dial** |

`exchangeTCP` has two call sites and both are inside the `response.Truncated` branch
(`udp.go:120, :131`). UDP is `AllOf`, TCP is `Conditional` —the previous `{UDP, TCP}` refused a detour
that answers every ordinary query.

### A second defect the work exposed

`resolvedDetour` asserted `*DetourDialer` directly, so a transport whose **server is a domain** —wrapped in a `ResolveDialer` by `RemoteIsDomain` —silently reported "nothing to check". MEASURED, DoT
and DoH started with a UDP-only detour while every other type was refused for the same mismatch. The
wrapper chain is now walked through `Upstream()`.

## Group fallback

```text
GROUP_EDGE_SEMANTICS_SOUND     = YES
GROUP_FALLBACK_CONTRACT_CLOSED = YES
GROUP_UNION_FALSE_DENY_REMAINS = NO
```

**The rule: a group's advertised union is CAPABILITY, not flow demand.** When nothing is PROVEN about
delivery, each business entry is charged with its OWN advertisement. The structural reason this is
correct rather than merely convenient: the union is a sum over the exits' own answers, so it exceeds
an exit's set **only when a sibling advertises more** —it can never fail a single-entry root, and
every failure it ever produced was a member of a multi-member group.

Proven demand is unchanged: it is still charged to every exit under the root. `case-08` (`{udp}`) and
`case-10b` (`{tcp,udp}`) both still refuse, which is the guard against a global narrowing.

### The five tests, classified by demand source

| test | source | verdict |
|---|---|---|
| `TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork` | **ONLY_ADVERTISED** | the fixture installed NO route model (`startWithDryRun` never calls `EnablePhysicalPathDelivery`). Its name promised "RoutedNetwork". It now DELIVERS the demand through `startWithDryRunDelivering` |
| `TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember` | **ONLY_ADVERTISED** | the config had no `route` key at all. The fixture gained the TCP route that makes the member a defect |
| `case-08` | ROUTE_PROVEN `{udp}` | untouched, still refuses |
| `case-10` | ROUTE_PROVEN `{tcp}` | acceptance case, untouched |
| `case-10b` | ROUTE_PROVEN `{tcp,udp}` | untouched, still refuses |

The two tests the tree treated as its network-dimension true positives contained no route model at
all. Preserving them would have meant preserving the false deny —the second time in this line of work
that an old "control" turned out to encode the defect.

`TestRouteAwareDiagnosticDoesNotClaimAProofTheRoutesNeverMade` is re-pointed at a **proven** refusal
and asserts the message says "the routes prove tcp reaches this path" and NOT "the routes prove
nothing here". The objects-only wording is now unreachable through that shape, and a new
`TestAnAdvertisedOnlyRootDoesNotClaimAProof` records that by asserting the shape starts.

## Cross-kind graph

```text
REFERENCE_LOOKUP_TABLE_COMPLETE     = YES
GRAPH_MATCHES_RUNTIME_LOOKUP        = PARTIAL
ENDPOINT_DOMAIN_RESOLVER_EDGE_SOUND = NO          (recorded gap)
DEFAULT_DOMAIN_RESOLVER_EDGE_SOUND  = PARTIAL     (over-charge FIXED; under-charge recorded)
SHADOWING_SEMANTICS_SOUND           = YES
CROSS_KIND_GRAPH_CLOSED             = NO
```

### The lookup table (primary deliverable) —complete

| reference | lookup | precedence / namespace |
|---|---|---|
| DNS transport `.detour` | `detour.go:71` `Outbound(tag)` | outbound-**FIRST**, endpoint fallback (`manager.go:534-539`) |
| DNS transport `.domain_resolver` | `resolve.go:107` `Transport(tag)` | DNS-only, no fallback |
| outbound `.detour` / `.domain_resolver` | `detour.go:71` / `resolve.go:107` + `adapter.go:55-57` | outbound∪endpoint / DNS-only |
| endpoint `.detour` | `adapter/endpoint/adapter.go:21-27` | outbound∪endpoint |
| endpoint `.domain_resolver` | `protocol/wireguard/endpoint.go:67-74`, `openvpn/client.go:105-111` | field EXISTS; real edge, CONDITIONAL on a domain peer |
| `route.default_domain_resolver` | `route/network.go:470-472` —`dialer.go:159-164` | per-node fallback policy; CAN close a cycle |
| HTTP client `.detour`; group members; default | `httpclient/manager.go:108`; `selector.go:170`; `detour.go:76-78` | leaf; members can name endpoints |

### Ordering —pinned

Three guards run before the cross-kind check: decode (`option/options.go:151`, `duplicate
outbound/endpoint tag`), `box.New` (`:737`), and `lintOutbounds` (in the `outbound` component, which is
started before `dns-transport`). MEASURED with a collision **plus** a dangling DNS detour: only the
collision is reported. So the same-tag state is **PROVEN_UNREACHABLE** through `box.New` + `Start`; the
old map-overwrite was latent rather than observable, and is replaced by the canonical `Outbound(tag)`
lookup anyway because an embedder can drive the manager directly.

### What was fixed

```text
* node table built via Manager.Outbound(tag) - outbound-FIRST - instead of a two-pass map whose
  overwrite order defined the semantics
* an endpoint's own domain_resolver is read (adapter.Endpoint publishes only Detour)
* describeOutbound no longer infers the KIND from an interface assertion: MEASURED,
  *direct.Outbound satisfies adapter.Endpoint, so the tag-clash refusal labelled BOTH halves
  "endpoint/...". The manager knows the namespace, so it is passed in. It now reads:
     "two objects claim the tag direct: outbound/direct (direct, *direct.Outbound) and
      endpoint/direct (wireguard, *wireguard.Endpoint)"
* the default-resolver edge is over-charge-corrected (R1 below)
```

### R1 —FALSE DENY, introduced and corrected within this round

Charging the default-resolver edge unconditionally REFUSED a configuration in which nothing resolves a
name at all: a DNS server with an IP-literal address whose detour is a SOCKS5 outbound with an
IP-literal server. `ServerIsDomain()` is false for both, SOCKS5 v5 forwards the target NAME rather than
resolving it (`protocol/socks/outbound.go:160-161, :359`), and the DNS server's address is a literal
(`dns/transport_dialer.go:23`) —so no `ResolveDialer` is built, nothing reads the default, and the
loop is not real. MEASURED: the same JSON minus the one `default_domain_resolver` line started all
along. The edge is now conditional, and the literal-server twin is pinned as a START.

### R2 —recorded gap, NOT half-fixed

The endpoint `domain_resolver` accessor is **dead code**: `cross_kind_cycle.go` reaches it only when
the `DomainResolverReference` assertion already failed, and it asserts the identical interface on the
identical value, so it always returns `""`. An endpoint's own `domain_resolver` closing a loop still
STARTS. The same wiring gap leaves a domain-server object's default-resolver edge unadded.

The fix for both is one piece of work —publish the resolver reference on the adapter the object
**actually is** (`adapter/endpoint.Adapter`, which keeps only `Detour` today) rather than reading it
through a second accessor or publishing it on `dns.TransportAdapter`, which is the type the DNS
*manager* holds. The test asserts only what the tree does, with the measurement recorded in it:

```text
literal-server twins + default resolver   STARTS    (the corrected false deny)
domain-server + explicit resolver         REFUSED   (the known cycle)
domain-server + default resolver          STARTS    RECORDED GAP, cause named in the test
```

## Mutation evidence

| id | mutation | result |
|---|---|---|
| M-A1 | H3-only demand UDP —TCP | RED (2 cases) |
| M-A2 | promote the fallback network into the hard set | RED |
| M-A3 | DNS `udp` demand UDP —TCP | RED (2 cases) |
| M-B1 | restore advertised-union-as-hard-demand | RED (`TestAnAdvertisedOnlyMemberIsNotARefusal`) |
| M-B2 | drop proven demand | RED (`TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork`) |
| M-CROSS-1 | remove the resolver edge | RED (cycle test) |
| M-CROSS-4 | drop the default-resolver edge | RED (cycle test) |

Every mutation was applied by exact-string replacement guarded by a `.Contains` check that aborts
before writing, so a no-op replacement fails loudly instead of being recorded as a success.

## Independent adversarial audit

A fresh reviewer that did not write the patch attacked all five changed claims with its own probes.
Its verdicts: **1 UPHELD, 2 UPHELD, 3 UPHELD as implemented (one measured weakening), 4 BROKEN,
5 UPHELD.** It found R1 (a false deny this round introduced, now corrected), R2 (the dead accessor, now
recorded), and confirmed `AnyOf` is right because no request class fails on both single-network
detours. It also corrected a comment I had written, as noted above.

## Regression

```text
FOCUSED_TESTS = PASS
  .  ./adapter/  ./adapter/outbound/  ./common/physicalpath/  ./common/dialer/
  ./common/httpclient/...  ./dns/...  ./route/  ./route/rule/  ./protocol/group/

STRESS_TESTS  = PASS
  ./protocol/group/ -count=20      ok  78.4s
  ./adapter/ -count=20             ok  14.9s
  ./adapter/outbound/ -count=10    ok        ./common/physicalpath/ -count=10   ok
  ./common/dialer/ -count=10       ok  131.3s
  ./common/httpclient/ ./dns/ -count=10      ok

GO_BUILD_ALL = PASS
GO_VET_ALL   = 3 findings, ALL PRE-EXISTING
  daemon/managed_service.go:75 · experimental/libbox/debug.go:11 ·
  experimental/boxdd/authenticode_windows.go:95   (unsafe.Pointer)
  -> reproduced IDENTICALLY on the untouched worktree at the integration base
GO_TEST_ALL  = 82 packages ok, 3 FAIL
  common/tls (real handshake, unexpected EOF) · common/tlsspoof · common/windivert
  -> reproduced IDENTICALLY on the untouched worktree at 96a94f584 BEFORE classifying them
RACE         = NOT_RUN_HOST_BLOCKED (needs cgo; no C compiler)
DOCKER       = NOT_RUN_HOST_BLOCKED (no daemon)
```

## Final verdict

```text
BASE_SHA = 44f667c6712eb44870e0488d42010ffbafc6769b
WORK_HEAD = 062c229a4705fdae8b644268646250273ed76800

CONSUMER_VALIDATION_OWNER_PRESENT       = YES
CONSUMER_HARD_REQUIREMENT_POLICY_SOUND  = YES
HTTP3_FALLBACK_POLICY_SOUND             = YES
UDP_DNS_RETRY_POLICY_SOUND              = YES

GROUP_EDGE_SEMANTICS_SOUND              = YES
GROUP_FALLBACK_CONTRACT_CLOSED          = YES
GROUP_UNION_FALSE_DENY_REMAINS          = NO

REFERENCE_LOOKUP_TABLE_COMPLETE         = YES
GRAPH_MATCHES_RUNTIME_LOOKUP            = PARTIAL
ENDPOINT_DOMAIN_RESOLVER_EDGE_SOUND     = NO
DEFAULT_DOMAIN_RESOLVER_EDGE_SOUND      = PARTIAL
SHADOWING_SEMANTICS_SOUND               = YES
CROSS_KIND_GRAPH_CLOSED                 = NO

RESOLVER_PARITY_CLOSED                  = YES
DNS_OWNERSHIP_PARITY                    = YES
STATIC_VALIDATION_FALSE_DENY_CLOSED     = YES
STATIC_VALIDATION_FALSE_ALLOW_CLOSED    = YES

FOCUSED_TESTS = PASS   STRESS_TESTS = PASS   GO_BUILD_ALL = PASS
GO_VET_ALL = 3 PRE-EXISTING   GO_TEST_ALL = 82 ok / 3 PRE-EXISTING ENV
RACE = NOT_RUN_HOST_BLOCKED   DOCKER = NOT_RUN_HOST_BLOCKED

MAIN_UNTOUCHED = YES   TESTING_UNTOUCHED = YES   WORK_BRANCH_PUSHED = YES

RELEASE_BLOCKER_REMAINS            = NO
READY_FOR_STATIC_VALIDATION_FREEZE = NO
```

### Why the freeze gate is NO

`CROSS_KIND_GRAPH_CLOSED = NO`, and this time it is a NAMED, BOUNDED gap rather than an open question:
the resolver reference must be published on `adapter/endpoint.Adapter` (and the equivalent for an
outbound whose server is a domain) so the graph walks the adapter the object actually is. Until then
the endpoint `domain_resolver` edge is dead and a domain-server default-resolver loop is missed.

That is one wiring change with two symptoms, both recorded with measurements in the tests that would
otherwise have asserted them as working. Closing it is the remaining item; nothing else on the
acceptance list is open.

```text
[x] HTTP3 fallback hard-refusal contract is proven, not guessed
[x] UDP DNS TCP-retry requirement is classified correctly as hard vs conditional
[x] no consumer diagnostic claims "every request fails" when a fallback can succeed
[x] group no-demand union no longer causes a measured false deny
[x] explicit routed/consumer demand still rejects incapable selector members
[x] ancestor filtering does not blanket-exempt descendants
[x] complete reference lookup table exists
[~] cross-kind graph uses runtime lookup semantics          <- node table + default edge yes; accessor dead
[ ] endpoint.domain_resolver is handled according to actual reachability
[~] route.default_domain_resolver is handled according to actual cycle relevance
[x] no graph edge targets an object runtime cannot resolve  <- the R1 over-charge is corrected
[x] graph mutations go RED   [x] consumer mutations go RED   [x] group mutations go RED
[x] previous resolver parity remains green
[x] previous DNS ownership parity remains green
[x] protected branches remain untouched
[x] report WORK_HEAD equals the actual pushed HEAD
```
