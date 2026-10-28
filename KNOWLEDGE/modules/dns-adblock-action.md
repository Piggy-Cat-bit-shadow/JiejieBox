# DNS Adblock Action

## Local verdicts and multi-family errors after the original review

`dns/verdict_marker.go` marks a local rule's rcode without changing its rcode matching contract.
`IsLocalVerdict` checks that **every leaf** of an aggregated error is a local verdict: a blocked
family mixed with a real failure must not hide the failure by matching only one branch. Upstream
NXDOMAIN caching is separate: `dns/client_negative.go` stores eligible NXDOMAIN with usable SOA,
but must not widen NODATA or transient errors to the whole name. These are `VERIFIED_SOURCE` at
`011f3fdcd99`; local DNS package tests passed in this refresh, not real-device validation.

> Status: `VERIFIED_SOURCE` (implementation, tests and schema verified in this worktree at the
> commit that introduced the feature; see `Provenance` below)
>
> The original feature review used branch `feat/dns-adblock-action` and base `c32c210b0db` at
> version 0.1.10. The current testing target `011f3fdcd99` contains the action; later changes and
> their narrower evidence are noted above. A historical feature branch is not the current release.
>
> This module documents the fork's **`adblock` DNS rule action**: a DNS-only terminal action that
> answers a name the rule-set has already classified as advertising with a locally generated,
> negative-cacheable NXDOMAIN, without issuing any upstream query. It is intended to answer "how do
> I block ad domains at the DNS layer with this fork?" without a source audit. If this document
> conflicts with current source or tests, **source and tests win**.

---

## 1. Purpose

```text
adblock 是本 fork 新增的 DNS rule action。
命中广告域名后直接在本地返回可 negative-cache 的 NXDOMAIN，
不访问 upstream DNS。
```

`adblock` is a **zero-configuration, DNS-only, terminal** action:

- the rule that carries it has already decided the name is advertising (`domain`, `domain_suffix`,
  `domain_keyword`, `domain_regex`, `rule_set`, …);
- the action does not ask an upstream server to confirm that decision;
- it answers with `NXDOMAIN` plus one SOA carrying a negative TTL, so the client can cache the
  verdict itself;
- it never drops the query silently and never returns `REFUSED`, `SERVFAIL` or a sinkhole address.

Primary source anchors:

```text
constant/rule.go                     RuleActionTypeAdblock
constant/dns.go                      DefaultDNSNegativeTTL
option/rule_action.go                _DNSRuleAction.Action enum, MarshalJSON,
                                     UnmarshalJSONContext, dnsActionUnion
route/rule/rule_action.go            NewDNSRuleAction, RuleActionAdblock, RuleActionAdblock.Response
route/rule/rule_dns.go               validateDNSRuleAction
route/reference_rule.go              collectDNSRuleReferences (terminal action)
dns/router.go                        matchDNS, walkDNSRules, sweepArmedDNSRules, exchangeLegacy,
                                     legacy Lookup
dns/router_adblock_test.go           end-to-end behaviour and upstream-exchange assertions
route/rule/rule_dns_adblock_test.go  action shape, DNS-only boundary, `race` refusal
option/rule_action_test.go           decode / round-trip / schema shape
```

---

## 2. Configuration syntax

The documented form is a DNS rule whose condition selects the advertising names and whose action is
`adblock`. Nothing else is required.

### 2.1 One rule-set

```json
{
  "dns": {
    "rules": [
      {
        "rule_set": "anti-ad",
        "action": "adblock"
      }
    ]
  }
}
```

### 2.2 Several rule-sets on one rule

```json
{
  "dns": {
    "rules": [
      {
        "rule_set": [
          "anti-ad",
          "AWAvenue-Ads-Rule"
        ],
        "action": "adblock"
      }
    ]
  }
}
```

### 2.3 Domain conditions directly

```json
{
  "dns": {
    "rules": [
      {
        "domain_suffix": [
          "doubleclick.net",
          "ads.example"
        ],
        "action": "adblock"
      }
    ]
  }
}
```

`domain`, `domain_suffix`, `domain_keyword`, `domain_regex` and `rule_set` are all ordinary DNS rule
conditions and all work with `adblock`; the action does not restrict which condition selected it.

### 2.4 Complete minimal configuration

```json
{
  "dns": {
    "servers": [
      {
        "type": "udp",
        "tag": "remote",
        "server": "8.8.8.8"
      }
    ],
    "rules": [
      {
        "rule_set": "anti-ad",
        "action": "adblock"
      }
    ]
  },
  "route": {
    "rule_set": [
      {
        "type": "remote",
        "tag": "anti-ad",
        "format": "binary",
        "url": "https://example.com/anti-ad.srs",
        "update_interval": "1d"
      }
    ]
  }
}
```

The `action` field is the only field `adblock` accepts. `ttl`, `method`, `drop`, `reply`, `ip_mode`,
`cache_size`, `fake_soa` and `strategy` do **not** exist for this action and are rejected as unknown
fields at parse time rather than silently ignored.

`race` is different, and the difference is worth knowing when reading a config error: it is a field
of the action *envelope* (`_DNSRuleAction.Race`), not of a variant, so the JSON decoder genuinely
does see it and accepts it. It is refused one layer up, by `validateDNSRuleAction` when the DNS rule
is built, with:

```text
`race` is not supported by the adblock action: it never consults an upstream lookup
```

Either way the configuration does not load, and the published schema's `adblock` variant declares
only `action`, so an editor validating against it flags `race` before the runtime sees it.

---

## 3. Behaviour

```text
domain / domain_suffix / rule_set
        ↓
      adblock
        ↓
local NXDOMAIN + SOA negative TTL
        ↓
   upstream = 0
```

The response is built for the request being answered:

| Field | Value |
|---|---|
| `ID` | the current request's ID |
| `Question` | the current request's single question, verbatim |
| `Rcode` | `NXDOMAIN` |
| `Answer` | empty |
| `Ns` | one SOA owned by the queried name, class from the question |
| SOA `TTL` / `MINIMUM` | `constant.DefaultDNSNegativeTTL` = `600` |
| flags | `QR`, `AA`, `RD`, `RA` set; `TC`, `CD`, `AD` clear |

`AA` mirrors what this tree's other locally answered responses do
(`RuleActionPredefined.Response`, `dns.FixedResponse*`): the resolver answered from its own
knowledge rather than from an upstream server.

Both the SOA's own TTL and its `MINIMUM` carry the same value because RFC 2308 §3 defines the
negative-cache lifetime as `min(SOA TTL, SOA MINIMUM)` and clients differ in which they honour.

---

## 4. Design goals

1. Advertising names must not produce **repeated** upstream DNS requests — not one per record type,
   not one per caller, not one on a cold start.
2. The network, CPU and wakeup cost an ad name causes must be as close to zero as the DNS layer can
   make it.
3. No silent drop: a dropped query makes the client retry or fall back, which costs more than the
   upstream query that was saved.
4. Not implemented through `reject`: a refusal is a failure a client is entitled to retry.
5. Client-side negative caching is encouraged, not assumed: the SOA is there so that conforming
   resolvers stop asking, but the guarantee this fork makes is about its own upstream traffic.

---

## 5. Difference from `reject`

```text
reject:
  Refuse the current DNS request.
  method=default -> REFUSED, method=drop -> no response at all.
  No negative-caching information, no statement about the name.
  A client is entitled to retry, re-ask another server, or fall back.

adblock:
  The user has already declared this name to be advertising.
  Produce a negative DNS verdict at the DNS layer, locally.
  The verdict is about the NAME, so it covers A / AAAA / HTTPS / SVCB alike.
  Goal: never enter upstream, and let the client negative-cache the result.
```

`reject` answers "I will not process this". `adblock` answers "this name does not exist", which is
the only answer that lets a client stop asking.

---

## 6. Difference from `predefined`

```text
predefined = a general custom DNS response builder: the operator chooses the rcode, the answer,
             the authority and the extra section, and therefore also chooses whether the result is
             negative-cacheable at all.

adblock    = a dedicated advertising semantic whose response is FIXED:
             NXDOMAIN, empty answer, one SOA carrying DefaultDNSNegativeTTL.
```

The two share a flag convention, not a configuration contract. `predefined` with
`"rcode": "NXDOMAIN"` and no `ns` entry is a valid configuration that produces an NXDOMAIN a
conforming client cannot negative-cache; `adblock` exists so that a large third-party advertising
rule-set can be attached to a correct negative answer without every operator re-deriving that shape.

---

## 7. Relationship to the existing NXDOMAIN optimisation

This is the part most easily mis-stated, so it is stated precisely:

> `adblock` reuses this fork's NXDOMAIN **negative-caching design principles and response
> semantics** — an NXDOMAIN about the name, with a real SOA and a non-zero negative TTL — but it is
> a **local policy verdict** that terminates at the DNS rule layer.
> `dns/client_negative.go` continues to own **upstream-learned genuine NXDOMAIN**, and nothing about
> its contract changes.

Concretely:

| | upstream-learned NXDOMAIN (`dns/client_negative.go`) | `adblock` |
|---|---|---|
| Who produced the verdict | an authoritative server, proved by a response | the operator's rule |
| Where it is stored | `nxdomainCache` (`dnsCacheKey` minus Qtype, in-memory LRU) | nowhere — re-matched per query |
| Lifetime | the SOA's negative TTL, counted down on each hit | the rule's lifetime; only the downstream client caches |
| Namespace | transport tag + client subnet + environment | the rule itself |
| Survives `ClearCache` semantics | yes, it is purged by them | not applicable |
| `disable_cache` / `disable_expire` | govern it | do not apply |

**`adblock` deliberately does not write into `nxdomainCache`.** That cache's contract is "an
upstream server proved this name does not exist", and a rule match is not that proof. Recording a
policy verdict there would make the two indistinguishable, and would leave a stale verdict behind
after a rule-set update — the exact failure the placement below avoids.

Container-scoped invariants of `client_negative.go` are unchanged: only genuine `NXDOMAIN` widens,
single question only, SOA required, authoritative negative TTL non-zero, empty answer required,
NODATA/SERVFAIL/REFUSED/FORMERR/timeouts never widen, `rewrite_ttl` cannot promote a zero negative
TTL into a widening verdict.

---

## 8. Where it takes effect, and why that is the whole point

`adblock` is handled inside the **DNS rule walk**, which runs before the DNS client and therefore
before its exact cache, its QNAME-wide negative cache, its singleflight and every transport:

```text
Router.Exchange / ExchangeAsync
  prepareExchange
    walkDNSRules  ← adblock returns here
      match rule
      action == adblock -> RuleActionAdblock.Response(message) -> return
  (never reached) client.Exchange
                    exact cache
                    nxdomainCache
                    singleflight
                    transport exchange
```

The consequences, each covered by a test in `dns/router_adblock_test.go`:

1. **First query, upstream = 0.** Nothing has to be learned before the verdict is available.
2. **Cross-QTYPE, upstream = 0.** The rule is re-evaluated per query, so A/AAAA/HTTPS/SVCB all cost
   a match and nothing else.
3. **An existing positive exact-cache entry cannot shadow the policy.** The walk is upstream of the
   cache, so the rule wins over an answer that was cached before the rule existed.
4. **Removing the rule takes effect immediately.** No policy-derived verdict was written into any
   cache, so the next query follows the new rules. (A downstream client that already cached the
   NXDOMAIN may legitimately keep it until its own negative TTL expires — that is DNS working as
   specified, not a stale verdict inside sing-box.)
5. **Non-advertising names are untouched.** No new lock, map, goroutine, timer or allocation
   manager was added to the DNS hot path; an unmatched name takes exactly the path it took before.

Legacy DNS mode (`exchangeLegacy` and the legacy `Lookup` loop) is handled as well, because legacy
mode is selected by an address-filter condition rather than by the action, so an `adblock` rule can
land in either path. In the `Lookup` path there is no message to answer, so the verdict surfaces as
the rcode error the caller already understands for "this name has no address".

---

## 9. Scope

```text
Currently only for DNS domain rules.
```

Works with: `domain`, `domain_suffix`, `domain_keyword`, `domain_regex`, `rule_set`, logical rules,
`invert`, and any other DNS rule condition — the action is applied after a match and does not care
what produced it.

### A pinned resolver bypasses the rule walk entirely

**This action does not apply to a lookup whose transport is pinned by the caller.** It is the
pre-existing contract for *every* DNS rule action, not something specific to `adblock`, and it is
recorded here because it is surprising: a name that the rules block will still resolve when
something asks for it through a pinned server.

```go
dns/router.go
    if options.Transport != nil {
        ...                                  // the rule walk is never entered
    } else if !legacyDNSMode {
        responseAddrs, err = r.lookupWithRules(...)
```

The pin comes from configuration, so this is reachable in production rather than theoretical:

```text
domain_resolver on an outbound       common/dialer/dialer.go  dnsQueryOptions.Transport, _ = dnsTransport.Transport(...)
route.default_domain_resolver        the same path
```

MEASURED, with an `adblock` rule for the name in place:

```text
Lookup("ads.example.", Transport = pinned)   addresses = [203.0.113.9]   err = <nil>   queryCount = 2
Lookup("ads.example.", Transport = nil)      addresses = []              err = NXDOMAIN  queryCount = 0
```

So a dialer whose `domain_resolver` names a server resolves a blocked name through that server and
connects. The action is not broken — it is never consulted — and changing that would mean changing
what a pinned resolver means for every DNS action, which is out of scope for this feature.

**Operator consequence:** if a name must not resolve for a dialer, the rule has to be enforced
where the dialer's resolution happens. Either do not pin a `domain_resolver` for the name, pin one
that enforces the block itself, or block at the route level (`reject`/`drop` on the destination)
rather than at DNS.

---

## 10. Not handled

```text
IP / CIDR                         — not a condition of this action and not implemented by it
TCP route                          — no route-level adblock
UDP route
IP -> domain reverse lookup
transport reject/drop              — adblock is not an alias for reject and shares no code with it
Fake-IP reverse mapping
a dedicated adblock cache/LRU/timer/goroutine
disk persistence of advertising verdicts
a lookup through a PINNED resolver — see §9: the rule walk is not entered at all
```

An advertising rule whose `ip_cidr` list is non-empty still only blocks the **DNS** names that match
it. A direct-to-IP advertising connection that never issues a DNS query is out of scope for this
action and is not affected by it.

---

## 11. `race` is refused

`race` means "start the real lookup before this response-bound rule decides". `adblock` never
consults a lookup — its verdict comes from the match that selected it — so the combination cannot be
honoured, and accepting it would mean silently launching the very upstream query the action exists
to prevent.

Both halves are enforced:

- the published schema's `adblock` variant declares **only** `action`; there is no `race` property
  (`option/rule_action.go` → `dnsActionUnion`);
- `validateDNSRuleAction` rejects `race` on an `adblock` rule at build time
  (`route/rule/rule_dns.go`), which covers a rule constructed in Go rather than decoded from JSON.

This is the same treatment the DNS-only boundary gets on the other side: `adblock` does not appear
in the non-DNS rule action union, and a route rule that names it fails to decode.

One consequence is deliberate and is easy to misread as a leak: a rule list where a `race` rule
appears BEFORE an adblock rule still sends that `race` rule's query, because the lookup is already
armed by the time adblock is reached, and the armed rule has to be judged against the response it
was waiting for. That query belongs to whoever wrote `race`. What must never happen is the converse,
and it does not: a rule listed AFTER a matched adblock rule starts nothing, not even a lookup that
would be cancelled later (`TestAdblockCannotBeMadeToLaunchAResolverByANeighbouringRule` measures both
directions, with the no-adblock list as the control).

---

## 12. Logging

`adblock` emits no per-query log line of its own. It is an ordinary match: the existing DNS rule-hit
debug logging prints the matched rule and its action (whose `String()` is `adblock`), which is
visible when debug logging is enabled and costs nothing when it is not. Adding an `INFO`/`WARN` line
per blocked query would make log I/O the new cost centre for exactly the traffic this feature exists
to make cheap.

---

## 13. TTL choice

`constant.DefaultDNSNegativeTTL` is `600` seconds, aliased to the repository's existing
`DefaultDNSTTL`. The reasoning, both sides:

- **Too short** moves the cost back onto the resolver: the client asks again sooner, and each ask is
  a rule match (cheap) that this fork accepts, but the point of the feature is that the client stops
  asking.
- **Too long** means a downstream OS or application may hold an NXDOMAIN after the operator removes
  the rule. The response advertises a TTL, but this code cannot guarantee how a downstream client
  caches or honors it. The action itself writes no sing-box cache entry.

600 seconds is the repository's own DNS default (it is what every locally answered record here
reports), the middle of the range used by filtering resolvers, and the shorter end of what the
well-known ones advertise (AdGuard Home and Blocky default to 3600). It is deliberately not
configurable.

---

## 14. Evidence and acceptance

| Requirement | Evidence | Re-check |
|---|---|---|
| first query for an ad name reaches no transport | `dns/router_adblock_test.go` `TestAdblockFirstQueryNeverReachesTheTransport` | `go test ./dns/ -run TestAdblock -count=1` |
| A / AAAA / HTTPS / SVCB each cost zero upstream exchanges | `TestAdblockAnswersEveryRecordTypeLocally` | same |
| a transport configured to fail is never entered | `TestAdblockNeverReachesASabotagedTransport` | same |
| 128 concurrent cold-start callers, mixed record types, zero exchanges | `TestAdblockSurvivesConcurrentColdStart` | same |
| nothing after a matched adblock rule launches a lookup | `TestAdblockCannotBeMadeToLaunchAResolverByANeighbouringRule` (with the no-adblock control) | same |
| an entry cached before the rule existed cannot shadow it | `TestAdblockPolicyBeatsAnExistingPositiveCache` | same |
| removing the rule takes effect on the next query | `TestAdblockPolicyRemovalIsImmediate` | same |
| rule ordering is preserved in both directions | `TestAdblockObeysRuleOrder` | same |
| the legacy dispatch path terminates the same way | `TestAdblockTerminatesTheLegacyExchangePath`, `TestAdblockLegacyAndModernAnswersAreIdentical` | same |
| non-advertising names keep their own rule and transport | `TestAdblockLeavesOtherNamesOnTheirOriginalPath` | same |
| async entry point answers locally, without suspending | `TestAdblockAnswersTheAsyncExchangeFromTheCallerGoroutine` | same |
| a real compiled rule-set drives the action | `TestAdblockThroughACompiledRuleSet` | same |
| `disable_cache` does not change where the verdict is produced | `TestAdblockIsUnaffectedByDisableCache` | same |
| 2000 blocked queries stay within the test's retained-heap and goroutine tolerances | `TestAdblockSteadyStateAllocatesNothing` (less than 128 KiB extra retained heap and at most four extra goroutines; responses do allocate) | same |
| the question comes back as asked, in any case spelling | `TestAdblockAnswersCaseVariantsWithTheAskedSpelling` | same |
| malformed requests are rejected above the action, not inside it | `TestAdblockAgreesWithTheRouterOnMalformedRequests` | same |
| response shape, flags, SOA, ID, question | `route/rule/rule_dns_adblock_test.go` `TestAdblockResponseShape`, `dns/router_adblock_test.go` `requireLocalNXDomain` | `go test ./route/rule/ -run Adblock -count=1` |
| adblock is terminal, stateless, and needs no logger | `TestAdblockIsATerminalActionThatStopsTheWalk`, `TestAdblockResponseNeedsNoLogger`, `TestAdblockRuleSurvivesNilAuthorizedRules` | same |
| `adblock` and `predefined` differ only where the design says they do | `TestAdblockDoesNotChangeThePredefinedAnswerForTheSameShape` | same |
| `race` refused; DNS-only boundary; decode; round-trip; schema shape | `TestDNSRuleActionAdblockRejectsRace`, `option/rule_action_test.go` | `go test ./route/rule/ ./option/ -run Adblock -count=1` |
| the action costs the same order of allocations as a local `predefined` answer | `BenchmarkAdblockResponseConstruction` vs `BenchmarkPredefinedResponseConstruction` (3 allocs / 136 B vs 1 / 144 B per answer) | `go test ./route/rule/ -run XXX -bench ResponseConstruction -benchtime 200000x` |
| upstream-learned NXDOMAIN behaviour unchanged | pre-existing `dns/client_negative_test.go`, `dns/client_singleflight_test.go` | `go test ./dns/... -count=1` |

The evidence table records source and historical local unit runs, not a specific CI run. The current
workflow declarations and independently inspected run SHAs are recorded in `ci-release.md` and
`ACCEPTANCE.md`; those records do not automatically establish each individual row above on HEAD.

**One claim in this module is weaker than the rest, and it is named rather than implied.** The
concurrent tests drive 128 simultaneous callers, but `go test -race` cannot run here (`-race requires
cgo`, and no C compiler is installed on the audit machine). So "concurrent answers do not race" is
supported by construction — every answer is built fresh from the request, the action holds no state,
and nothing is shared between calls — and is **not** supported by the race detector. It is recorded
as the honest limit of the evidence above rather than folded into it.

---

## 15. Provenance

| Fact | Where it comes from |
|---|---|
| `RuleActionTypePredefined = "predefined"` was the closest existing terminal action | `constant/rule.go`, `route/rule/rule_action.go` |
| locally answered DNS responses set `QR/AA/RD/RA` | `RuleActionPredefined.Response`, `dns.FixedResponseStatus` |
| RFC 2308 negative TTL is `min(SOA TTL, SOA MINIMUM)` | `dns/client.go` `extractNegativeTTL` |
| the QNAME-wide negative cache is upstream-learned only | `dns/client_negative.go` header and `storeNXDomain` guards |
| the DNS rule walk runs before the client | `dns/router.go` `walkDNSRules` vs `finishPendingExchange` |

There is no earlier attempt at this feature in the fork's history: `git grep -i adblock` is empty at
the base commit.

---

## 16. Maintenance rule

> Any new or modified user-visible JSON field, action, method or protocol parameter in this fork must
> be reflected in the knowledge base in the same change. **A configuration ABI that exists only in Go
> structs and source is not documented.**
