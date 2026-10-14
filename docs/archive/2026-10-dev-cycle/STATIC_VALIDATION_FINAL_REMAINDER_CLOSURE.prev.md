# STATIC VALIDATION FINAL REMAINDER CLOSURE

## Baseline

```text
BASE_BRANCH                = integration/0.1.6-final-freeze
BASE_SHA                   = 96a94f584d30691cadd44dab83b64d56fc7a1fec
WORK_BRANCH                = closure/0.1.6-static-validation-final
WORK_HEAD                  = 4a7e43d1f  (pushed)
CONSUMER_DNS_SOURCE_SHA    = ba3cab01bd20d03f1d0248e4f563f4156dd1cfb6
GROUP_CROSSKIND_SOURCE_SHA = d95e256b7626e25b44df10faa70c25aa59efacb3
GO_VERSION / GOOS / GOARCH = go1.26.8 / windows / amd64
WORKTREE_NAME              = C:\Deepseek\内核\fin
WORKTREE_CLEAN             = YES (before and after)
```

## Merge status

```text
CONSUMER_DNS_BRANCH_INTEGRATED  = YES   (real merge 6e02ae2ff, --no-ff)
GROUP_CROSSKIND_BRANCH_INTEGRATED = YES (real merge 197b25dcc, --no-ff)
DUPLICATE_MERGE_AVOIDED         = YES
CHERRY_PICK_USED                = NO
```

Ancestry was proven before merging: neither branch was an ancestor of the integration tip
(`is-ancestor` false for both), and both had merge-base `96a94f584` — the integration tip itself —
so both were genuinely independent and neither was already contained.

The changed-file intersection was computed before merging: `common/physicalpath/dryrun.go` was the
only shared file, and git auto-resolved it. Both sides' semantics were then **verified present** in
the merged file rather than assumed:

| from | what | check |
|---|---|---|
| consumer-dns | the two split refusal sentences | both present at `:852` and `:858` |
| group-crosskind | `provenForRoot`/`selfRequirementFor` deleted | absent |
| group-crosskind | the reverted-narrowing contract comment | present at `:451` |

## Already-closed invariants

```text
ROUTE_DEMAND_MODEL_SOUND      = YES  (unchanged; matrix + mutations carried from the route-aware line)
ROUTE_FINAL_SOUND             = YES  (unchanged)
GROUP_FALLBACK_CONTRACT_SOUND = YES  (all five named true-positives re-run and PASS; see Regression)
BRIDGE_STATUS                 = HOST_BLOCKED_BUT_SOURCE_PROVEN   (not reopened; no Administrator host this round)
DNS_OWNERSHIP_PARITY          = YES  (six remaining local dialers carry the capability; three measured, three source-proven)
HTTP_CLIENT_GHOST_DETOUR      = CLOSED
```

## Consumer demand

```text
CONSUMER_DEMAND_MODEL_PRESENT        = YES
HTTP_TRANSPORT_VALIDATION_SOUND      = YES   (model + validation owner + 9 measured cases + M-C1 RED)
DNS_TRANSPORT_VALIDATION_SOUND       = YES   (model + validation owner + 9 measured cases + M-C2 RED)
NTP_VALIDATION_SOUND                 = YES   (proven UDP-only; its demand is Required:{UDP})
URLTEST_PROBE_SEPARATION_SOUND       = YES   (unchanged; probe TCP never becomes a member requirement)
FALSE_ALLOW_CONSUMER_MISMATCH_REMAINS = NO   (both owners refuse a provable mismatch at start)
```

### What was built

Two demand models, each derived from the code that selects the transport rather than from a label,
with the option enum expressed as `Required` / `Optional` — and, after them, the two validation
owners that READ them:

```text
common/httpclient/transport_requirement.go   HTTPClientTransportRequirement   (6 sub-cases)
dns/transport/requirement.go                 RequirementFor                   (6 + 10 unmodelled)
common/httpclient/transport_capability.go    CheckDetourCapability            (HTTP owner)
common/dialer/capability.go                  CheckDetourCapability            (DNS owner)
```

The cases a label-based guess gets wrong, and which the tests pin:

| consumer | naive rule | actual, from the implementation |
|---|---|---|
| HTTP `version: 3` (fallback on) | TCP | **UDP + TCP** — H3, with an H2 fallback taken on a non-https URL, a websocket upgrade, and any H3 failure (`http3_transport.go` RoundTrip) |
| HTTP `version: 3` + `disable_version_fallback` | TCP | **UDP only** — H3 alone; the one place in this tree an HTTP client does not need TCP |
| HTTP `version: 1/2/0` | TCP | TCP — `helpers.go:28` is the single TLS dial |
| DNS `udp` | UDP | **UDP + TCP** — a truncated response is retried over TCP |
| DNS `https` | "TCP or maybe H3" | **TCP only** — `https_transport.go:37,:54,:61` dial `N.NetworkTCP` with no QUIC branch, and `h3` is a *separate* type (`constant.DNSTypeHTTP3`) |
| DNS `quic` / `h3` | — | UDP |
| DNS `local`/`hosts`/`fakeip`/`group`/`dhcp`/`mdns`/`tailscale`/`openconnect`/`openvpn` | — | **no requirement** — unmodelled rather than guessed |

The `https` row is a correction to my own first attempt: I modelled an optional H3 path for DoH and
the implementation refuted it. Modelling "DoH might use H3" would have been inventing a branch the
runtime does not have — exactly the failure mode the prompt's "model the configured runtime
selection, not the protocol label" rule exists to prevent.

### The two owners, and where each check lives

| consumer | owner | why there | check |
|---|---|---|---|
| HTTP client | `common/httpclient.Manager.Start` | it already resolved this client's detour reference, so the demand question is asked at the same moment by the same owner | detour tag → object → `Network()` vs `Required` |
| DNS transport | each transport's `Start`, next to `dialer.InitializeDetour` | the DNS packages form a chain (`dns` → `dns/transport` → `dns/transport/quic`), so the shared helper had to sit below all of them | same |

`common/dialer.CheckDetourCapability` is that shared helper. It reads the object the `DetourDialer`
**already resolved** (memoised behind a `sync.Once`) rather than looking the tag up again, so the
check judges exactly the object the traffic goes through. `DetourDialer.DetourName()` was added as a
read-only accessor because `Dialer()` is what triggers resolution, and a preflight must not perform
one as a side effect of asking.

**NTP**: strictly UDP — `common/ntp/ntp.go:14` in the replaced `sing` version is
`dialer.DialContext(ctx, N.NetworkUDP, serverAddress)`. Its demand is `Required: {UDP}`, and it is
the one consumer family whose requirement needed no derivation.

### What is NOT modelled, deliberately

An object that reports **no** networks is accepted, because "reports nothing" is not a claim and
treating it as "carries nothing" would refuse objects that simply do not implement `Network()`. A
**group** passes on its own advertisement and is left to the reachable-leaf dry run for its members —
duplicating a member walk here would be a second, weaker answer to a question `physicalpath` owns.
And the nine unmodelled DNS types are not constrained at all.


### The chosen owner, and why (the prompt's §3.2 question)

Per consumer family, read from where the consumer is constructed rather than assumed:

| family | constructed | detour resolved | exact network | phase enough info | already owns the runtime failure | should own static preflight |
|---|---|---|---|---|---|---|
| HTTP client | `common/httpclient.Manager` | lazily, `dialer.NewDetour` on first dial | version-dependent (above) | `Start` (`StartStateStart`) | the first request | **HTTP client manager** — it already validates the reference there |
| DNS transport | `dns.NewTransportManager` | eagerly, `dialer.InitializeDetour` | type-dependent (above) | `StartStateStart` | its own `Start` | **DNS transport initialization** — it already refuses a dangling detour |
| rule-set | router | via `http_client` | inherits the client's | after client init | fetch failure | not separately — it borrows the client's |
| NTP | `box.go:831` | `DialerOptions` | UDP only | `Start` | first probe | same owner as HTTP client if it grows one |
| URLTest probe | `protocol/group` | member dial | **TCP always** (`measure.go:244`) | n/a | probe failure | **nothing** — see the separation note |
| MASQUE / Tailscale / endpoint bootstrap | the endpoint | the endpoint's own dial | endpoint-specific | construction | the tunnel | not modelled |

The safest owner for the first two is the manager that already resolves their detour, because that is
where the reference is already checked and where a refusal is already a start-time diagnostic. Adding
them to `physicalpath.ValidateRoots` would be the wrong abstraction: the dry run answers "is every
reachable LEAF usable", and a consumer is not a leaf.

`URLTEST_PROBE_SEPARATION_SOUND = YES` and unchanged: the probe is TCP (`measure.go:244`) while a
member is judged by its own `Network()`. The probe transport never becomes a member requirement, so
the conflation the prompt warns about does not exist here.

## Resolver parity

```text
RUNTIME_RESOLVER_SOURCE_OF_TRUTH   = common/dialer.ResolveAuthority (extracted from NewDNSQueryOptions)
VALIDATOR_RESOLVER_SOURCE_OF_TRUTH = the same function, via Manager.SetResolverAuthority -> Resolver.WithResolverAuthority
SHARED_SOURCE_OF_TRUTH             = YES
CONFIRMED_FALSE_DENY_FIXED         = YES
RESOLVER_MODEL_PARITY              = YES
```

### The four sources, and which are reachable

| # | source | owner | runtime lookup | validator visibility | reachable |
|---|---|---|---|---|---|
| 1 | explicit `domain_resolver` | the outbound | `dnsTransport.Transport(server)` | yes, via the shared function | yes |
| 2 | `route.default_domain_resolver` | network manager | `DefaultOptions().DomainResolver` | yes | yes |
| 3 | manager's own transport (`< 2` transports) | DNS transport manager | `dnsTransport.Default()` | yes | yes |
| 4 | empty policy, router default path | router | returns `{}`, no error | yes (`AuthorityNone` only when nothing exists) | yes |

### The fix

`common/dialer.ResolveAuthority` is extracted from `NewDNSQueryOptions` and returns which source
answers (`AuthorityNone` / `AuthorityExplicit` / `AuthorityNetworkDefault` /
`AuthorityTransportDefault`). `NewDNSQueryOptions` is rewritten to switch on it — **behaviour
unchanged, and the dialer suite does not move** — and the validator asks the same function.

The wiring obstacle the prompt flagged is real and was solved by late binding, not by widening the
guess: the seam is installed at `box.go:504` while the DNS manager is built at `:526`.
`Manager.SetResolverAuthority` is a second call made immediately after the manager is registered,
mirroring how `EnablePhysicalPathDelivery` is installed after the router compiles. A manager that
never calls it keeps the static behaviour, so no embedder changes. `physicalpath` keeps its
"imports nothing but adapter" property by taking a plain bool, so a walk still cannot dial or log.

### Tests

| id | case | result |
|---|---|---|
| R1 | one `udp` DNS server, declared ownership | **STARTS** (was REFUSED) |
| R1b | one `local` DNS server | **STARTS** (was REFUSED) |
| R2 | no `dns.servers` at all | **STARTS** — correct, see below |
| R3 | explicit `domain_resolver` | STARTS |
| R4 | `route.default_domain_resolver` | STARTS |
| R5 | control: no declaration | STARTS (the check only runs for a declared tag) |
| R6 | parity is by construction — one function, two callers | asserted by the shared `ResolveAuthority` |

`R2` needs a word, because my first expectation was wrong and the code corrected it. I labelled it
"truly no resolver → must REFUSE". It starts. The reason: Box installs a **`local` (system)
transport as the DNS fallback** (`box.go:809`), so a name resolves even with no `dns.servers`. The
label was wrong, not the behaviour — refusing it would have been a false deny, which is the very
class this item exists to remove.

The consequence is worth stating precisely: `AuthorityNone` is **not reachable through any
configuration that starts at all**. Every path that could produce it either fails construction
earlier (a `domain_resolver` naming a transport that does not exist) or leaves the `local` fallback
behind. So the ownership-resolver refusal is now a **defensive** branch: it fires only for an
embedder-level state, not for a configuration an operator can write. That is the honest reading of
"one semantic source of truth" here — it does not make the check fire more, it makes it stop firing
when the runtime would work.

### Mutation

```text
M-R1  made the seam inert (`if false {`), restoring the two-source model
      -> RED on exactly accepted/one_udp_dns_server, accepted/one_local_dns_server,
         accepted/no_dns_server_at_all.  Reverted; green again.
M-R2  "force the validator to claim a resolver when the runtime says no"
      -> NOT_APPLICABLE: the seam IS the runtime helper, so there is no second place to force it.
         That absence is the fix, not a gap.
```

## Cross-kind graph

```text
REFERENCE_LOOKUP_TABLE_COMPLETE      = NO
GRAPH_MATCHES_RUNTIME_LOOKUP         = NO
ENDPOINT_DOMAIN_RESOLVER_EDGE_SOUND  = NO
DEFAULT_DOMAIN_RESOLVER_EDGE_SOUND   = NO
SHADOWING_SEMANTICS_SOUND            = NO
CROSS_KIND_GRAPH_SOUND               = NO
```

`CROSS_KIND_GRAPH_CLOSED = NO`, and I did not attempt it. The prompt's §11 requires the reference
lookup table to be built from the *actual* lookup functions before any graph change, and its §10.1
records why: lookup is outbound-first, so a shared tag is not two equally reachable nodes, and
`(kind, tag)` would model a graph the runtime does not execute. Finishing that table needs each
reference-bearing field's lookup function read and its precedence established — `DNS transport.detour`,
`outbound.detour`, `endpoint.domain_resolver`, `route.default_domain_resolver`, `HTTP client.detour`,
group members. I established only that outbound-first is the manager's order
(`manager.go:432-440`) and that `adapter/endpoint.Adapter` keeps only `Detour`
(`adapter/endpoint/adapter.go:21-27`), which is consistent with the reported missing endpoint edge but
does not prove the graph's correct shape.

CK1–CK7 are therefore `NOT_RUN`, and per §14 the mutation must attack the real design — with no
design change there is nothing to mutate. The prompt's own rule applies: no fabricated closure.

## Mutation evidence

```text
consumer mutations   M-C1  forced-H3 demand UDP -> TCP      -> 2 cases RED   VERIFIED
                     M-C2  DoQ demand UDP -> TCP            -> 2 cases RED   VERIFIED
resolver mutation    M-R1  seam made inert                  -> resolver parity RED (3 cases)  VERIFIED
                     M-R2  NOT_APPLICABLE (the seam IS the runtime helper, so there is no second
                           place to force an answer)
graph mutations      NONE — no graph change landed
earlier, preserved   M3a delete wireguard capability        -> ownership parity RED   VERIFIED
                     M3b collapse the two refusals           -> ownership diagnostic RED   VERIFIED
                     M6  delete the tag-clash guard          -> the P0 panic returns   VERIFIED
```

Every mutation was applied by exact-string replacement guarded by a `.Contains` check that aborts
before writing if the target text is absent, so a no-op replacement fails loudly rather than being
recorded as a success.

## Regression

```text
FOCUSED_TESTS = PASS
  .  ./adapter/  ./adapter/outbound/  ./common/physicalpath/  ./common/routeflow/ (no test files)
  ./route/  ./route/rule/  ./protocol/group/  ./dns/...  ./common/httpclient/...  ./common/dialer/

STRESS_TESTS  = PASS
  ./protocol/group/ -count=20    ok  78.4s
  ./adapter/ -count=20           ok  14.8s
  ./adapter/outbound/ -count=10  ok
  ./common/physicalpath/ -count=10  ok

GO_BUILD_ALL  = PASS
GO_VET_ALL    = FAIL (3 pre-existing unsafe.Pointer findings)
  daemon/managed_service.go:75 · experimental/libbox/debug.go:11 ·
  experimental/boxdd/authenticode_windows.go:95
  -> reproduced IDENTICALLY on the untouched integration base, and none of the three files is
     touched by this branch (diffed the file list to confirm)
GO_TEST_ALL   = 81 packages ok, 4 FAIL
  common/windivert · common/tlsspoof  -> Administrator required (windivert: open SCM: Access is denied)
  common/tls                          -> TestWindowsClient*TLS13, real handshake, unexpected EOF
  common/sniff                        -> TestAgentFFrameShape*, flaky under full-tree load;
                                         passes in isolation on this branch AND the base
  -> reproduced on the untouched comparison worktree before being classified; none is in a package
     this branch touches
RACE          = NOT_RUN_HOST_BLOCKED (needs cgo; no C compiler on this host)
DOCKER        = NOT_RUN_HOST_BLOCKED (no daemon)
```

### One regression the resolver fix caused, and how it was resolved

Two ownership tests failed after the resolver fix. Their control asserted that a SOCKS proxy
declaring ownership with no NAMED resolver must be REFUSED. **That control was the false deny.**
Verified cause: the `local` fallback transport resolves it. Both controls were replaced with the
refusal that is genuinely impossible — an outbound type with no capability (`trojan`), which "accept
every declaration" would still have to refuse. Net effect: the ownership check is narrower, not
weaker, and the true positive it was written for is now stated with a case that is actually
impossible rather than one that was merely unusual.

## Final verdict

```text
INTERNAL_CONSUMER_DEMAND_CLOSED   = YES
RESOLVER_PARITY_CLOSED            = YES
CROSS_KIND_GRAPH_CLOSED           = NO

GROUP_SEMANTICS_STILL_SOUND        = YES
BRIDGE_CLASSIFICATION_STILL_VALID  = YES
DNS_OWNERSHIP_PARITY_STILL_SOUND  = YES

STATIC_VALIDATION_FALSE_DENY_CLOSED = NO   (resolver false deny FIXED; the group union false deny remains,
                                            deliberately, pending the group-membership contract)
STATIC_VALIDATION_FALSE_ALLOW_CLOSED = YES (both consumer owners now refuse a provable mismatch at start,
                                            each with a mutation proving the refusal is load-bearing)

RELEASE_BLOCKER_REMAINS            = NO
READY_FOR_STATIC_VALIDATION_FREEZE = NO

MAIN_UNTOUCHED                     = YES
TESTING_UNTOUCHED                  = YES
WORK_BRANCH_PUSHED                 = YES
```

### Why the freeze gate is still NO

Two of the three semantic items are closed. The gate is held by the cross-kind graph, whose lookup
table the prompt requires before any change:

```text
[x] the two prior closure branches are integrated exactly once
[x] internal consumer demand has an actual production validation owner
[x] forced H3 mismatch is pinned
[x] DoQ / UDP-DNS mismatch is pinned where runtime proves invalid
[x] fallback-capable cases are not falsely refused
[x] resolver false deny is actually fixed, not merely documented
[x] runtime and validator use one resolver semantic source of truth
[ ] cross-kind graph is derived from real runtime lookup semantics
[ ] endpoint/default resolver edges are handled according to actual reachability
[ ] no graph edge targets a shadowed/unreachable object
[x] all previous group true-positives remain intact
[x] bridge is not "fixed" by lying about L3 capability
[x] mutation tests prove the new logic is load-bearing   <- M-C1, M-C2, M-R1, M3a, M3b, M6 RED
[x] no protected branch was modified
```

### The one remaining item, and its next step

**Cross-kind graph.** Build the §11 reference lookup table first — one row per reference-bearing
field (`DNS transport.detour`, `outbound.detour`, `endpoint.domain_resolver`,
`route.default_domain_resolver`, `HTTP client.detour`, group members), each with its lookup
function, precedence, legal target namespaces and shadowing behaviour — and only then decide whether
`(kind, tag)` is the right identity. Outbound-first lookup already suggests it is NOT, which is why
landing it without the table would model a graph the runtime does not execute. The prompt's own
instruction applies: edges come from actual runtime lookup, not theoretical namespaces.

