# CONSUMER / DNS / RESOLVER PARITY CLOSURE

## 0. Baseline

| field | value |
|---|---|
| `BASE_BRANCH` | `integration/0.1.6-final-freeze` |
| `BASE_SHA` | `96a94f584d30691cadd44dab83b64d56fc7a1fec` |
| `WORK_BRANCH` | `closure/0.1.6-consumer-dns-parity` |
| `WORK_HEAD` | `cf5a25bbe` (pushed) |
| `WORKTREE` | clean before and after; `gofmt` clean; `git diff --check` clean |
| `WORKTREE_NAME` | `<WORKSPACE_ROOT>\内核\clA` |
| `GO_VERSION` / `GOOS` / `GOARCH` | go1.26.8 / windows / amd64 |
| branches merged | none (forbidden by the prompt) |
| `MAIN_TESTING` | untouched |

## 1. The finding that governs this whole task

**No validator in this tree consumes consumer demand.** Verified by reading every validator call
chain and by grep:

```text
git grep -n "physicalpath\." -- '*.go'   (non-test)
  box.go:504   EnablePhysicalPathValidation(declarations, resolverFor, defaultDomainResolver)
  box.go:567   EnablePhysicalPathDelivery(physicalPathDelivery(router, routeOptions.Final))
  adapter/outbound/manager.go:279  ValidateRoots(...)
```

The dry run's inputs are (a) declarations = which tags DECLARED ownership, (b) resolverFor = the
tag's own `domain_resolver`, (c) defaultDomainResolver = a bool, (d) the route-proven delivery map.
There is no consumer input and no consumer-derived demand anywhere. `route/router.go:306`'s only
`httpClientManager` touch is `ResetNetwork()`.

Consequence, and it is the reason the H/D suits below are not "passing tests" in the usual sense:
the shapes `H2/H4` and `D1/D2` describe — a consumer whose required network exceeds its detour's
capability — are **not refusable today and are not refusals today**. They start. That is a
false-allow of the *shape*, but it is not a live false allow for any configuration, because nothing
reads the demand that would make it one.

This also means `M1` and `M2` (mutate forced-H3 demand to TCP, mutate DoQ demand to TCP) have
**nothing to mutate**: there is no demand derivation to damage, so the required "mutation must make
the test RED" cannot be satisfied honestly. They are recorded as `NOT_APPLICABLE` with this reason
rather than faked.

## 2. §2/§3 — consumer and HTTP transport demand, read from the code

HTTP client transport selection, `common/httpclient/client.go:89-128` (this package is local to the
tree, not vendored — read directly, not inferred):

| `version` | `disable_version_fallback` | transport built | line |
|---|---|---|---|
| `0` (default) → `2` | false | `newHTTP2FallbackTransport` | :92, :107 |
| `1` | — (ignored) | `newHTTP1Transport` | :102 |
| `2` | false | `newHTTP2FallbackTransport` | :107 |
| `2` | true | `newHTTP2Transport` | :105 |
| `3` | false | `newHTTP3FallbackTransport` (H3 + H2 fallback) | :124 |
| `3` | true | `newHTTP3Transport` (H3 only) | :117 |

Wire-level demand:

- **TCP**: `helpers.go:28` `rawDialer.DialContext(ctx, N.NetworkTCP, destination)` — the single TLS
  dial every TCP-based transport uses. `http1_transport.go:19` dials with the caller's network.
- **UDP**: only via QUIC/H3 in `http3_transport.go`.
- `version:3` **without** `disable_version_fallback` also routes non-`https` requests and
  websocket-upgrade requests to the H2 fallback (`http3_transport.go:132-134`), so it needs TCP
  *as well as* UDP.
- `version:3` **with** `disable_version_fallback` builds H3 only, so its demand is UDP alone.

### Suit results

| id | shape | what the code says | status |
|---|---|---|---|
| H1 | HTTP1/2 + TCP-capable detour | works | `NOT_REFUSED` (no demand validator exists) |
| H2 | HTTP1/2 + UDP-only detour | would fail at dial | `NOT_REFUSED` — shape is a false allow, unreachable as a verdict |
| H3 | forced H3 + UDP-capable | works | `NOT_REFUSED` |
| H4 | forced H3 + TCP-only | would fail | `NOT_REFUSED` — same |
| H5 | optional H3 with legal fallback | must not be refused | `NOT_REFUSED` (correct outcome, vacuous) |
| H6 | unused client stays lazy | not constructed | **PASS** — asserted by `TestAnUnreferencedHTTPClientIsNotBuiltAtStart` |
| — | `version:3 + disable_version_fallback` | demand is UDP-only, NOT "TCP or UDP" | recorded; a future demand validator must not write "HTTP client ⇒ TCP" |

H6 is the one item in this table that is a real, measured pass: the detour validation added in the
previous round checks the tag and does not construct the transport.

## 3. §4 — DNS transport demand

| type | underlying transport | detour | network requirement |
|---|---|---|---|
| `udp` | UDP, TCP on truncation | yes | **UDP + TCP** (truncation retry) |
| `tcp` | TCP | yes | TCP |
| `tls` | TCP + TLS | yes | TCP |
| `https` | TCP (+ H3 when configured) | yes | TCP, +UDP if H3 enabled |
| `quic` (DoQ) | QUIC | yes | UDP |
| `h3` (DoH3) | HTTP/3 | yes | UDP |
| `local` | OS resolver, UDP or TCP + unix | n/a | host-dependent |
| `dhcp` / `mdns` | link-local UDP; mDNS bypasses the dialer | — | UDP, not via the outbound path |
| `hosts` / `fakeip` | none (no dial) | — | none |
| group | none (control node) | — | none |
| `resolved` (Linux) | DIRECT, no detour | no | n/a |

Read from `dns/transport/*.go` per type. Detour validation is eager for every transport that dials
(`dialer.InitializeDetour`), which is the contrast that produced the `http_clients` fix.

### Suit results

| id | shape | status |
|---|---|---|
| D1 | DoQ + TCP-only detour | `NOT_REFUSED` — same reason as H2; no demand validator |
| D2 | UDP DNS + TCP-only detour | `NOT_REFUSED` — same |
| D3 | DoH + TCP-only detour | `NOT_REFUSED` (correct outcome, vacuous) |
| D4 | H3 DNS + UDP-capable detour | `NOT_REFUSED` |
| D5 | fallback-enabled HTTPS not refused for "possible H3" | `NOT_REFUSED` (correct outcome, vacuous) |

`D5` is worth stating plainly: because nothing reads DNS transport demand, the false deny it guards
against cannot occur today. It is a constraint on a FUTURE demand validator, not a present bug.

## 4. §5 — NTP and URLTest

**NTP is strictly UDP — CONFIRMED.** `common/ntp/ntp.go:13-14` in the replaced `sing` version
(`v0.9.6-0.20261008194531-3af46fe99d3b`, the fork, per `go.mod`'s replace directive):

```go
func Exchange(ctx context.Context, dialer N.Dialer, serverAddress M.Socksaddr) (*Response, error) {
	conn, err := dialer.DialContext(ctx, N.NetworkUDP, serverAddress)
```

`box.go:831` builds `ntp.NewService(...)` and `box.go:599` registers it as `ntp.TimeService` for the
TLS layer. A detour declared for NTP therefore needs UDP.

**URLTest probe transport — CONFIRMED TCP, and separate from member capability.**
`common/urltest/measure.go:244`: `detour.DialContext(ctx, "tcp", target.Destination)`.

So a URLTest group's own health check is always TCP (`measure.go:244`), while its members are judged
by their own `Network()`. These are two different questions and the validator does **not** conflate
them: the only place URLTest appears in the dry run is `networkFilteringGroup`, where it is an
exemption for a *filtering* group, and the probe transport never becomes a member requirement.

`URLTEST_PROBE_SEPARATION_SOUND = YES` — by the absence of any conflation, with the two facts
(file:line) as evidence.

## 5. §6 — DestinationDNSOwnership parity: CLOSED

All six remaining local dialers were read first and then given
`adapter.LocalDestinationDialer`, with the file:line evidence in each method's doc comment:

```
wireguard endpoint   dnsRouter.Lookup(:489)  -> N.DialSerial -> endpoint.DialContext(:497)
tailscale endpoint   dnsRouter.Lookup(:777)  -> N.DialSerial -> systemDialer/stack(:784/:796/:802)
tailcat              dnsRouter.Lookup(:252)  -> N.DialSerial(:256)
masque-client        lookupInner(:683)       -> dialResolved(:687) -> device.DialContext(:692)
openconnect          dnsRouter.Lookup(:640)  -> N.DialSerial -> device.DialContext(:649)
openvpn-client       dnsRouter.Lookup(:863/:502) -> N.DialSerial -> device.DialContext(:872)
```

In every one, a domain destination is resolved locally and only the resulting ADDRESSES are dialled.
None has a proxy peer that could receive the name, which is what the declaration is about.

| type | measured through Start | evidence |
|---|---|---|
| `direct` | **STARTS** | closed in an earlier round |
| wireguard endpoint | **STARTS** | measured |
| openconnect endpoint | **STARTS** | measured |
| tailscale endpoint | reached the validator, passed it, then `post-start … tsnet: creating state directory: Access is denied` | host-blocked AFTER validation |
| openvpn-client | construct: `missing \`tls\` options` | source-proven only |
| masque-client | construct: `TLS required` | source-proven only |
| tailcat | construct: `parse server_public_key: unexpected key length: 0` | source-proven only |
| **control: socks, no resolver** | **REFUSED** | the true positive is intact |

`UNVERIFIED_TYPES = 3` (openvpn-client, masque-client, tailcat) — present in the capability matrix
and asserted never to be refused *for the ownership declaration*, but not driven through Start.

## 6. §7 — capability value semantics: CLOSED

The refusals are now four distinct sentences:

```
capability absent   -> "does not implement the capability"
capability false    -> "reports that it does NOT resolve the destination itself"
resolver missing    -> "neither the outbound nor the network configures a domain resolver"
```

The first two were one sentence until the value was read, which meant an object that implements
`DestinationDNSOwner` and reports `false` was told it "does not implement it" — untrue of it. No
shipped type reports false, so the branch was unreachable without a fixture: `refusingOwner` in
`common/physicalpath/physicalpath_test.go` is that fixture. The test asserts the two wordings are
DISTINCT (`Contains` the false-value sentence **and** `NotContains` the absent-interface one),
because a change that collapsed them would still refuse and only the wording catches it.

## 7. §8 — resolver parity: `WORKAROUND_REQUIRED`, recorded not guessed

### The real precedence, `common/dialer/dialer.go:104-139`

```
#1  explicit domain_resolver.Server                      -> transport, return
#2  networkManager.DefaultOptions().DomainResolver       -> transport, return   (route.default_domain_resolver)
#3  len(dnsTransport.Transports()) < 2                   -> Transport: dnsTransport.Default()
#4  otherwise (newDialer=false, the target-resolution path) -> EMPTY policy, no error
```

### The validator models #1 and #2 only

`common/physicalpath/physicalpath.go:181-192`. It never asks the dial path.

### #4 IS reachable — and my earlier `PROVE_UNREACHABLE` conclusion was WRONG

I previously recorded source #3 as unreachable because "the manager always registers a fallback
transport, so the count is 2". That reasoning was **faulty**, and reading the stages fixes it:

- `dns/transport_manager.go:50-58` appends the fallback at **`StartStateInitialize`**, so the count
  reaches 2 *after Initialize completes*.
- The outbound dry run and `Manager.Start` run at **`StartStateStart`** (`box.go:953/1026/1042`),
  which is **after** Initialize.

So at the moment the validator asks, a configuration with one user DNS server has
`len(Transports()) == 2` → #3 is skipped → #4 returns an EMPTY policy with **no error**, and
`resolveDialer.DialContext` (`resolve.go:115,141`) passes that empty policy straight to
`router.Lookup`. The resolution is completed by the router's own default path — which is exactly what
`TargetQueryOptions`' own doc comment states as intended ("the router falls back to its own default
path", `dialer.go:182-189`).

`TargetQueryOptions` calls `NewDNSQueryOptions(ctx, options.DomainResolver, newDialer=false)`, i.e.
the same function on the same branch. **The runtime does not fail this configuration and the
validator does.** That is a real false deny, and it is only reachable when the user configured at
least one DNS server.

Measured, and this is the asymmetry:

| configuration | runtime `TargetQueryOptions` | validator |
|---|---|---|
| no `dns.servers` | empty policy (**fails**: count is 1) | REFUSED — agrees |
| one `dns.servers` (`local`, or `udp`) | empty policy, **usable** (count is 2) | REFUSED — **disagrees** |
| explicit `domain_resolver` | transport | STARTS — agrees |
| `route.default_domain_resolver` | transport | STARTS — agrees |

### Why the fix is not in this commit

The honest repair is to make the validator ask the dial path's own question instead of re-deriving
it — a shared helper or a capability on the DNS transport manager, per the prompt's
"抽取共享 helper / capability". Two concrete obstacles, both structural rather than lazy:

1. The DNS transport manager is constructed at `box.go:526`, **after**
   `EnablePhysicalPathValidation` at `box.go:504`, so the seam's closure cannot capture it as
   written. The wiring has to move or be deferred.
2. `CanResolveDestination`'s current contract is a pure question about the configuration. #3/#4 are
   questions about the transport manager's state, so the seam needs that dependency threaded in.

Recording it with the measurement, the corrected reasoning, and the two obstacles is worth more than
a guessed patch in a release-closure branch. `RESOLVER_MODEL_PARITY = NO`.

## 8. §9 — mutation evidence

| id | mutation | result |
|---|---|---|
| M3a | delete `DialsDestinationLocally` from the wireguard endpoint | **RED** — `…/wireguard_endpoint` fails: "outbound type wireguard does not implement the capability" |
| M3b | collapse the two refusals back to one condition and one wording | **RED** — the test asserts the false-value sentence is present and the absent-interface one is NOT; the mutated message fails on `does not contain "reports that it does NOT resolve the destination itself"` |
| M1 | mutate forced-H3 demand to TCP | `NOT_APPLICABLE` — no demand derivation exists to mutate (§1) |
| M2 | mutate DoQ demand to TCP | `NOT_APPLICABLE` — same |

Mutations were applied by exact-string replacement guarded by a `.Contains` check that aborts before
writing if the target text is absent, so a no-op replacement fails loudly instead of recording a
false success (this is the lesson from an earlier round where a replacement silently fell through).
The first M3b attempt produced a syntax error rather than a semantic change; it was redone as a real
reversion and is the result reported above.

## 9. §10 — verification

```
gofmt -l <changed>                                     clean
git diff --check                                       clean
go build -tags "<tags>" ./...                          ok
go vet (touched packages)                              ok
go test -count=1 ./common/physicalpath/ ./adapter/ ./adapter/outbound/ .   ok
go test -tags "<tags>" -count=1 ./...                  81 packages ok, 4 environmental
```

The 4 environmental failures, all proven pre-existing on the untouched base
(`integration/0.1.6-final-freeze`) and none in a package this branch touches:

| package | cause |
|---|---|
| `common/windivert`, `common/tlsspoof` | Administrator required (`windivert: open SCM: Access is denied`) |
| `common/tls` | `TestWindowsClient*TLS13`: real handshake, `unexpected EOF` |
| `common/sniff` | flaky under full-tree load; **passes 2/2 in isolation on both this branch and the base** |

Flakes attributed rather than hidden: `common/sniff` (`TestAgentFFrameShape*`) and
`protocol/group` (`TestRecheckSingleFlightCollapsesABurst`) each failed once under parallel
full-tree load and pass in isolation on both trees. Neither package is touched by this diff.

`-race` remains `NOT_RUN` (needs cgo; no C compiler). Bash-based `scripts/ci/*.sh` gates remain
`HOST_BLOCKED` (no bash on this host).

## 10. §11 — verdicts

```
INTERNAL_CONSUMER_DEMAND_SOUND   = NO   (no validator consumes consumer demand; that is the finding)
HTTP_CLIENT_TRANSPORT_PARITY     = NO   (demand table verified from code; nothing enforces it)
DNS_TRANSPORT_PARITY             = NO   (demand table verified from code; nothing enforces it)
NTP_DEMAND_SOUND                 = YES  (strictly UDP, sing common/ntp/ntp.go:14; detour via DialerOptions)
URLTEST_PROBE_SEPARATION_SOUND    = YES  (probe is TCP at common/urltest/measure.go:244; never becomes a member requirement)
DNS_OWNERSHIP_PARITY             = YES  (7 local dialers carry the capability; 3 measured through Start, control still refused)
RESOLVER_MODEL_PARITY            = NO   (4 runtime sources vs 2 modelled; #4 proven REACHABLE, fix needs rewiring)
UNVERIFIED_TYPES                 = 3    (openvpn-client, masque-client, tailcat — source-proven, not driven through Start)
RELEASE_BLOCKER_REMAINS          = NO   (nothing here breaks a ship; the resolver gap is a false DENY, i.e. a working configuration is refused)
```

`RESOLVER_MODEL_PARITY = NO` is the item to carry forward, and its severity is worth stating
precisely: the divergence refuses a configuration whose runtime path works. It is a false deny, not
a silent bad start, so it is not a release blocker in the safety sense — but it is exactly the class
this whole audit line exists to remove, and it is now documented with the corrected reachability
proof rather than the wrong one I recorded earlier.
