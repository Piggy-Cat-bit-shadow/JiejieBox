# DNS

## Post-baseline family-probe logging (testing, 2026-10-28)

**VERIFIED_SOURCE** at `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`: `dns/router.go` uses
`QuietEmptyFamilyResult` to log an empty family probe at debug rather than as a lookup error;
the standalone unquiet case remains distinct. See `dns/empty_family_log_test.go` for positive
and negative controls. This is a logging distinction, not proof of all dual-stack recovery or
device behavior. Check the current implementation and test result for the exact target revision.

### Dual-stack lookup and candidate ownership

`dns/router_dual_stack.go`'s `LookupFamilies` reuses `Router.Lookup`, preserving routing, transport,
cache and response checks. Effective strict IPv4/IPv6 strategies issue only their selected family;
other strategies publish each family as it completes. `common/dialer` captures an immutable
`admissionPolicy` per flow, combining configured strategy with a local capability snapshot and its
ownership epoch. Unknown capability does not by itself refuse a family. The candidate scheduler
staggers attempts, advances on fast failure and closes losing connections, including late successes
(`dual_stack_scheduler.go`). Socket creation is not a completed QUIC handshake. Tests include
`dns/cross_generation_family_test.go`, `common/dialer/admission_policy_test.go` and
`common/dialer/dual_stack_scheduler_test.go`. These are `VERIFIED_SOURCE` observations at the
testing target; runtime and network-specific results require their own evidence.

The local-family gate applies only to sockets dialed here, including a proxy server's own address,
not a target handed to a remote proxy. A stale or transition-time capability snapshot cannot hard
deny a candidate; absent capability means no opinion. The strict configured family strategy remains
separate and can still exclude it. The observer prefers the combined family/generation/stability
snapshot; fallback to separate optional getters can produce two distinct readings. A batch gate
evaluates ownership once for its candidate list (`network_observation.go`, `family_capability.go`).

The scheduler's per-generation family health is softer than a hard capability refusal: a qualifying
path failure or sufficiently long losing opposite-family attempt can cause both families' first
candidates to start together on the next race. The entry expires after 15 seconds or clears on
success for that family, and the map is bounded. It does not blacklist that family or prove a QUIC
handshake succeeded. See `dual_stack_scheduler.go` and its family-health tests.

When the box has committed IPv4-only entry presentment, `dns/entry_ipv6.go` suppresses an
application-side TUN AAAA question before rules, cache, FakeIP or upstream work and answers NOERROR
with no data. It does not apply that gate to the dialer's `Lookup` or to DNS forwarded by a remote
endpoint: a remote proxy may have IPv6 even when the local entry does not. This is separate from
per-attempt family admission. See `dns/entry_ipv6_test.go` and the TUN presentment contract in
`libbox-platform-contract.md`.

IPv4-mapped IPv6 destinations must be classified as IPv4: the TUN canonicalizes them before DNS,
FakeIP and route-set comparisons (`protocol/tun/inbound.go`), and admission checks the unmapped
family (`common/dialer/admission.go`). See `protocol/tun/mapped_address_test.go` and
`common/dialer/dual_stack_mapped_asis_test.go`; the local TUN and dialer suites passed in this refresh.

### Private resolvers owned by endpoints

Tailscale, OpenVPN and OpenConnect DNS transports can own resolvers that are not themselves entries
in the global DNS manager. Their `CloseIdleConnections` methods snapshot those resolver references
under the read lock, release the lock and forward the idle-releaser action; `Reset` separately
forwards reset. See the three `protocol/*/dns_transport.go` implementations. Requiring the broader
`IdleConnectionKeeper` interface or walking only global transports can miss these private pools.
This is `VERIFIED_SOURCE` at `011f3fdcd99`; it is not device or external-network evidence.

For Tailscale, `tailnetDomains` in `protocol/tailscale/dns_transport.go` canonicalizes hosts, search
domains and route suffixes in one snapshot. A matching resolver-less local suffix takes precedence
over forwarded suffixes; otherwise forwarded suffixes are checked longest first. Single-label search
expansion is conditional and must find a known host or forwarded route, not merely any local suffix.
The endpoint and DNS transport use this model for their preferred-domain decisions. When platform
interfaces are used, `protocol/tailscale/system_binding.go` supplies Tailscale's default-route hook
from the network manager and registers cleanup of the shared interface getter. The tagged domain
and getter-cleanup tests passed locally; they do not establish real tailnet DNS or device handover.

**Scope:** `dns/router.go`, `dns/client.go`, `dns/transport_manager.go`, `dns/transport/*` (including
`fakeip` and the fork-created `group`), the DNS-facing route actions, and
`adapter/fakeip_issuance.go`.

**Provenance:** 94 fork commits touch `dns/`. The fork-created package is **`dns/transport/group`**
(stable / fastest / parallel transport groups, `5b0a33c`). **Do not attribute the upstream DNS
behaviour to the fork**: upstream rebases `testing`, and 328 of the 343 `世界`-authored commits in the
range are pre-rewrite orphans whose content is already in the baseline (see `BASELINE.md`
§provenance). The test that decides a specific change is a **blob comparison** at `e4b7e692` versus
`a267e99`, not an author name.

**Load this module when changing:** DNS routing, cache or generations, FakeIP, `domain_resolver`,
resolver ownership, DNS leaks, or DNS behaviour across a network transition.

---

## 1. The router

`dns.Router` (`dns/router.go`, ~2,628 lines). `Start(stage, scope)` at `:237`.

**Rule evaluation is an explicit walk with suspension**, not a recursive match:

| Element | Anchor |
|---|---|
| the walk | `walkDNSRules` `:680` |
| exchange with rules | `exchangeWithRules` `:870` |
| resume after suspension | `resumeExchangeWithRules` `:880` |
| sweep armed rules | `sweepArmedDNSRules` `:912` |
| finish a pending exchange | `finishPendingExchange` `:1002` |
| the asynchronous `evaluate` action | `launchDNSEvaluate` / `dnsEvaluatedFuture` `:560-655` |

**Why it is a walk rather than a match:** an `evaluate` action may need to perform its own exchange
before the outer query can be resolved. The suspension is what lets the outer exchange be *resumed*
without re-running the rules that already fired. **Making the evaluate action synchronous would either
deadlock or re-enter the rule set**, which is why the `Future` machinery exists.

**Legacy / response-matching mode** is separate and validated up front:
`resolveLegacyDNSMode` `:2287`, `dnsRuleModeRequirements` `:2301`,
`validateLegacyDNSModeDisabledRules` `:2382`, `validateEvaluateFakeIPRules` `:2445`. These run at
start, purely.

---

## 2. Generations and the cache-commit invariant

### The invariant, in one sentence

> **Delivery and state mutation are separate.** An exchange started on a settled network may return
> its dated answer after a transition, but it must not commit that answer under the new generation or
> changed policy. An exchange started while the transition is pending is not deliverable as a stable
> result. See `responseDeliverable` and `stateMutationAllowed` in `dns/client.go`.

### The machinery

| Concern | Anchor |
|---|---|
| the DNS environment generation | `dnsGeneration` `:1318`, `dnsGenerationLocked` `:1329`, `observeDNSEnvironment` `:1339`, `observeDNSEnvironmentLocked` `:1370` |
| the environment's identity | `environmentHashEntry` `:1468`, `dnsEnvironmentFingerprintOnly` `:1506`, `dnsEnvironmentDescriptionFor` `:1548`, `dnsEnvironmentFingerprintNow` `:1586` |
| reverse mapping (FakeIP→domain) | `reverseMappingGenerationCurrent` `:1610`, `reverseMappingAnswersFrom` `:1629`, `recordReverseMappingFrom` `:1685`, `commitReverseMappingAnswers` `:1724` — **the commit is generation-checked** |
| the policy epoch | `policyStoreGuard` / `policyEpoch` `:2106-2162` |
| cache keys | `dnsCacheKey` `client.go:194`, `dnsExchangeKey` `:201`, `rdrcNamespace(transportTag, environment)` `:224`, `newCacheKey` `:244` |
| the checks | `captureGeneration` `:269`, `policyStillCurrent` `:291`, `generationStillCurrent` `:302`, `responseDeliverable` `:345`, `stateMutationAllowed` `:367` |
| the transition state | `errNetworkTransitioning` `:389`, `networkTransitionStable` `:407` |
| the commit | `finishCacheKey` `:434`, `transportEnvironment` `:473`, `commitCacheState` `:902` |
| reset | `ClearCache` `:2066`, `ResetNetwork` `:2184` |

**Failure mode if the mutation guard is broken:** a dated Wi-Fi answer can be stored and served as
if it belonged to the new cellular environment. Delivery of an already-started, settled-network
query is intentionally allowed; that is distinct from recording the answer as current.

### Which subsystems use the coordinator epoch, and which do not

This matters because an earlier summary over-claimed a single global "network epoch" for all state.

| Subsystem | Epoch-coupled? | Evidence |
|---|---|---|
| DNS cache and reverse mapping | **yes** — has its **own** generation (`dnsGeneration`) alongside the coordinator's | `:1318-1370` |
| DNS-only environment change | **yes, and it gets its own generation** even when no network interface changed | `ffec0df1` |
| the policy store | **yes** — a separate `policyEpoch` | `:2106-2162` |
| `runtimecoord.Coordinator.Epoch` | the shared counter the dial gate and the retirement walk read | `common/runtimecoord/coordinator.go` |
| `TrimMemory` | **does not advance the generation** | `1331e8567` |
| `ReleaseMemory` | **does** | same |

`common/runtimecoord/coordinator.go:72-79` states that the two counters describe the same transition —
they are two views, not two independent truths. **Do not describe the epoch as "the single source of
truth for all network state".** It is the shared identity for the subsystems that opted in.

---

## 3. FakeIP

| Element | Anchor |
|---|---|
| the store | `dns/transport/fakeip/store.go` — `Store` `:28`, `Start` `:98`, `recordRetired` `:160`, `Create` `:191`, `Lookup` `:256`, `Reset` `:260` |
| the issuance ledger (fork-added) | `adapter/fakeip_issuance.go` — `FakeIPIssuanceLedger` `:102`, `Advance` `:149`, `RecordSeed` `:170`, `RecordIssued` `:184`, `Issued` `:203`, `IssuedInterval` `:210`, `Clear` `:231`; consumed at `route/route.go:1131` (`issuedFakeIPAddress`), which returns `(false, zero)` for a nil ledger |
| the NAT rewrite | `route/fakeip_conn.go:15` `fakeIPNATPacketConn`, `:26` `rewriteReadDestination` |
| the transport manager | `dns/transport_manager.go` — `TransportManager` `:18`, `Start` `:43`, `startTransports` `:102`, `Default` `:169`, `FakeIP` `:175`, `Create` `:181`, `isDefaultCandidate` `:269` |

**The rule:** a FakeIP server **must not answer a destination lookup**, and `domain_resolver` could
name one. Fixed by `8fad3e9b`. A mapped FakeIP address is a synthetic placeholder whose domain the
router recovers. The TUN route-set guard falls through for the **current store range**; its scope is
narrower than the router's issuance and retired-range checks (`data-path-optimization.md` §A.5).

The current `route/route.go` refuses an address inside the configured current FakeIP range when
its store has no mapping. Outside that range it refuses an address covered by this process's
issuance ledger or by a retired range backed by installation issuance history; the latter is a
coarser cross-process record, not proof that each individual address was issued. Otherwise an
address is not rejected merely because it resembles a placeholder. See `adapter/fakeip_issuance.go`
and `route/route.go` for the exact ownership and persistence boundaries.

`experimental/cachefile/fakeip.go` returns the metadata flush error; `dns/transport/fakeip/store.go`
does not advance its reservation after that error. The old unconditional-success finding in the
Round 2 oracle is historical; `TestFakeIPSaveMetadataReportsAFailedFlush` covers the failure path.
This metadata guarantee should not be generalized to every cache write API.

---

## 4. Consumer demand

`dns/transport/requirement.go` gives each DNS type its transport requirement, in the same shape as the
HTTP client's (`common/httpclient/transport_requirement.go`):

```text
udp  ⇒  AllOf{udp}  +  Conditional{tcp}
```

**Why the TCP leg is `Conditional` and not `AllOf`:** the TCP retry only happens on a **truncated**
response. Requiring both would refuse a configuration that works perfectly well.

Reported through `dns/transport/capability.go`; the per-type assertions live in
`dns/transport/{tcp,tls,udp,https}.go` and `dns/transport/quic/{quic,http3}.go`. These assert
interfaces at start time and **dial nothing**. An existing detour that lacks the required capability
is a warning in the current startup validation; a missing detour remains fatal. The affected runtime
flow can still fail. See `modules/path-selection-validation.md` for the taxonomy.

The provable-demand model itself is `common/routeflow/reach.go` — see
`path-selection-validation.md` §3 for the UNKNOWN-transparent policy, the `route.final` residual, and
why ICMP is deliberately out.

**`dns/transport/group`** is fork-created: `stable`, `fastest` and `parallel` transport groups
(`5b0a33c`). It selects **among** configured transports; it does not change what a transport is.

At the examined target, stable selection sticks to a clean member; parallel fans to clean members;
fastest keeps a live winner or starts a single election per generation when no live wins remain.
When none is clean, every mode makes one least-dirty attempt instead of fanning. Failure records
and wins expire lazily and are capped per member; a transport failure or SERVFAIL is a failure,
while NXDOMAIN and empty NOERROR are valid answers. Caller cancellation or group shutdown must not
be recorded as a member failure. The cache sees only the answer returned by the group. See
`group.go`, `fan.go` and the ownership/election tests; local `./dns/transport/...` tests passed in
this refresh, without claiming external DNS interoperability.

---

## 5. DNS leaks and the reach of DNS in the fork

Every mechanism that keeps DNS from escaping the intended path, and what each does:

| Mechanism | What it prevents | Anchor |
|---|---|---|
| per-transport **detour** capability check | warns at startup when an existing detour cannot carry the query; does not make that runtime path work | `dns/transport/capability.go` |
| **bootstrap vs in-tunnel** resolver separation (MASQUE) | resolving the server's own hostname through a tunnel that does not exist yet, or leaking in-tunnel names outward | `masque-connectip.md` §3 |
| `action_resolve` writing `metadata.DestinationAddresses` | a domain target bypassing an IP-based deny rule — **this is why `resolve` is required, not optional** | `naive-http-transport.md` §3 |
| the **packet destination guard** | a UoT non-Connect session sending a later datagram somewhere the session-level check never saw | `route/packet_destination_guard.go`; `naive-http-transport.md` §3 |
| `ui`/`route` DNS hijack ordering | a DNS query leaving sing-box entirely instead of reaching the DNS router | `route/route.go` (`dnsHijackByPort`); fixed by `497d37727` |
| FakeIP cannot be a destination resolver | a placeholder address being treated as a real answer | §3 above |
| the environment generation on cache commits | a stale answer being recorded as current after a network change | §2 above |

**`route/resolve_uot_sentinel_test.go`** pins the UoT sentinel contract. Note the path: the file is at
`route/`, not at the repository root.

`dns_server_address` and `dns_search_domain` rule items were absorbed from upstream
(`a2df2cdc1` family) — they are not fork work.

---

## 6. The `systemd-resolved` goroutine leak — fixed, and the document that says otherwise is stale

**`docs/RELEASE-CERTIFICATE.md:70-85` and `verify.yml:270-289` both record this leak as unfixed. They
are stale.** `git log -S'signalLoopStop' e4b7e692..a267e99` returns exactly one commit —
**`6b495134c`**, "fix(protocol,dns): early-data ownership, SOCKS identity, and network-scoped DNS
cache" (2026-10-09), fork-side and an ancestor of the release baseline.

What the fix did, verifiable at `a267e99`:

| Element | Anchor |
|---|---|
| `signalLoopStop chan struct{}` / `signalLoopDone chan struct{}` / `signalLoopOnce sync.Once` | `local_resolved_linux.go:80-82` |
| allocated in `Start` | `:206-207` |
| `Close` closes the stop channel once and **joins** the loop | `:239-241` (`close`, then `<-t.signalLoopDone`) |
| the loop signals its own exit | `:374` (`defer close(t.signalLoopDone)`) |
| the loop `select`s on the stop channel and deregisters with `RemoveSignal` | `:385`, `:380` |

**Blast radius, for the record:** Linux **with `systemd-resolved` present** only — the file is
`*_linux.go`, and `local_resolved_stub.go` carries `//go:build !linux`. It was unconditional on that
path, which is why running the `test/` module on a Linux runner for the first time surfaced it and why
the module's `TestMain` failed the run.

> **Two consequences for a future AI:**
>
> 1. **Do not re-report this as an open defect.** Correct the document instead. The `test/` module's
>    box-level tests — including the loadbalance ones — were kept **out of push CI** on the strength of
>    this leak; that exclusion can now be revisited, which is a CI decision, not a knowledge one.
> 2. **Do not apply the fix the document proposes.** Closing the dbus *signal* channel would **panic**
>    here, because dbus is the sender. The shipped fix is a separate stop channel plus a join. That is
>    the shape to preserve.

**Evidence level:** `VERIFIED_SOURCE` (the symbols exist at the release baseline and the introducing
commit is confirmed). **`BLOCKED_BY_ENVIRONMENT` to execute** — the path needs Linux with
`systemd-resolved`.

---

## 7. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| DNS rule walk, cache, reverse mapping and FakeIP generations | `VERIFIED_UNIT` | `go test -count=1 ./dns/...` |
| DNS transports' requirement model | `VERIFIED_UNIT` | `go test -count=1 ./dns/transport/...` |
| FakeIP cannot answer a destination lookup | `VERIFIED_UNIT` | `go test -count=1 -run FakeIP ./dns/...` |
| Route-proved DNS demand at start | `VERIFIED_UNIT` (needs tags) | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -v -run 'TestADNSTransportDetourMustCarryTheQuery\|TestTheDefaultDomainResolverIsAGraphEdge' .` |
| The resolver-authority parity check | `VERIFIED_UNIT` (needs tags) | `-run TestResolverAuthorityParity .` |
| The `systemd-resolved` goroutine leak | **it IS fixed**, by `6b495134c` | `git log -S'signalLoopStop' e4b7e692..a267e99`; the symbols at `local_resolved_linux.go:80-82`, `:239-241`, `:374`, `:385` |
| Multi-platform DNS environment matrix | never run | **`UNVERIFIED`** |
| DNS and FakeIP on a real device | no real TUN exercised in this Windows refresh | **`UNVERIFIED` here**; check device evidence for the specific build |
| Race-freedom | `-race` needs cgo | **`BLOCKED_BY_ENVIRONMENT`** |

Measured here: `go test -count=1 ./dns/...` → **all ok** (`dns` 9.5 s, `dns/transport` 6.4 s,
`dns/transport/fakeip` 8.7 s, `dns/transport/group` 0.7 s, `dns/transport/hosts` 0.4 s,
`dns/transport/quic` 0.4 s).

---

## 8. AI cautions

1. **Do not make the DNS rule walk synchronous.** The suspension exists so an `evaluate` action can run
   its own exchange and then resume the outer one.
2. **Do not commit a response under a changed generation or policy.** Preserve the distinct
   delivery contract for a query that started on a settled network; a query started during a pending
   transition is not deliverable as a stable result.
3. **Do not remove the reverse-mapping generation check.** A FakeIP→domain mapping committed under an
   old generation is a wrong domain for an address that has been reassigned.
4. **Do not describe the epoch as global.** DNS has its own generation beside the coordinator's, and
   the policy store has its own epoch.
5. **Do not make `TrimMemory` advance the generation**, and do not make `ReleaseMemory` stop doing so.
6. **Do not let a FakeIP server answer a destination lookup**, and do not let `domain_resolver` name
   one.
7. **Do not require both UDP and TCP for a DNS transport.** The TCP leg is `Conditional` because the
   retry only fires on truncation.
8. **Do not re-report the `systemd-resolved` leak as open**, and **do not apply the fix its document
   proposes** — closing the dbus *signal* channel panics here, because dbus is the sender. The shipped
   fix is a separate stop channel plus a join.
9. **Do not attribute upstream DNS behaviour to the fork.** Compare blobs at `e4b7e692` and `a267e99`.
10. **Do not cite the `assigned_dns_*` test files** for the MASQUE-adjacent DNS model — `e93e297aa`
    replaced that model with immutable snapshots and deleted them.
11. **Do not route a DNS query past the DNS hijack** to make the Direct Offload fast path fire; the
    hijack ordering is what keeps DNS-based filtering working.
12. **Do not treat "Domain is set" on a connection as "a sniffer set it"** — FakeIP reverse mapping
    also sets it.
