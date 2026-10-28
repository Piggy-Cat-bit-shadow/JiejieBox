# MASQUE / CONNECT-IP

## Client lifecycle after v0.1.9

**VERIFIED_SOURCE**, testing target `011f3fdcd99`: `protocol/masque/client.go` registers both
the tunnel device and client close functions with the lifecycle scope during startup. Scope cleanup
therefore covers the tunnel client as well as the device; do not replace it with a close that handles
only the TUN. `transport/masque/client.go` owns the client close path. This does not establish
device, server interoperability or performance results.

**Scope:** `transport/masque/*` (the fork's dataplane), `protocol/masque/*` (the endpoint),
`protocol/http`'s CONNECT path, and the `option/masque.go` surface.

**Provenance — read this before attributing anything.** MASQUE arrived **upstream** in `b83089ec`
("Add MASQUE support", 世界, 2026-09-21), which is present *below* the fork baseline and is reachable
from upstream refs. At the upstream ancestor `e4b7e692`, `protocol/masque` had **3 files** and
`transport/masque` had **7**. At `a267e99` they have **29** and **57**. So:

```text
upstream inherited base   protocol/masque's endpoint skeleton, the capsule concepts, DATAGRAM
fork-added feature        the whole transport/masque dataplane, DNS assignment, inner resolver,
                          PREF64 plumbing, the MTU ceiling, ICMP error generation, the address pool
fork fixes / hardening    52 commits under protocol/masque, 70 under transport/masque
```

**There is no `protocol/connectip` package.** CONNECT-IP lives in `transport/masque` and
`protocol/masque`. The fork uses **no** `quic-go/connect-ip-go` and **no** `quic-go/masque-go` in the
main module — `grep` for `masque|connect-ip|connectip` over `go.mod`/`go.sum` returns exactly one
hit, a comment at `go.mod:288`. `connect-ip-go` appears only inside the separate `test/go.mod` as a
third-party **interop peer** (`test/jiejie/reference/connect_ip_go_interop_test.go:14,23`).
**The fork implements CONNECT-IP itself.**

**Load this module when changing:** any capsule, session lifetime, MTU/PTB, DNS assignment, the
inner/bootstrap resolver split, or the H3 client pool.

---

## 1. The dataplane — `transport/masque`

### Capsules

| Mechanism | Identifier | Anchor |
|---|---|---|
| Parse / serialise | `parseAddresses`, `parseVersion`, `parseRoutes`, `appendAddress`, `newCapsule`, `newAddressCapsule`, `newRouteCapsule`, `RangesFromPrefixes` | `capsule.go:45,80,109,211,220,228,239,249` |
| Address / route types | `AssignedAddress`, `AddressRange`, `Contains`, `OverlapsProtocol`, `protocolConflicts` | `capsule.go:30,35,41,94,105` |
| DNS capsules | `DNSConfiguration.validate`, `validateDomainName` | `capsule_dns.go:148,179` |
| DoH path templating | `validateTemplateExpression` | `capsule_dohpath.go:290` |
| SVCB parameters | `ValidateServiceParameters` | `capsule_svcb.go:150` |
| Request routing template | `expression`, `Template`, `Scope`, `ParseTemplate`, `Expand`, `Match`, `parseTarget` | `template.go:15,20,28,34,121,143,230` |

**Capsule hardening, with the measurements behind it:**

* **Protocol `0` means ALL protocols**, so cross-protocol overlap is real and must be checked
  (`protocolConflicts`). The overlap check is **linear** — the *correct* quadratic version measured
  **13.5 s vs 16 ms baseline = 800× CPU DoS** (`b656b062f`, superseding `969909bc1`).
* An **8192-entry capsule bound** (`a529b68c8`, an `EXTERNAL-ADAPTED` choice with a quoted source —
  `quic-go/connect-ip-go` uses the same constant). Before it: 149,796 entries / **17.65 MiB heap**.
* `Contains() == lookup()` on the server's own address (`e4d2f6c1a`).

**MTU / PTB capsules:** `handlePacketTooBig` (client `client.go:686`, server `server.go:579`),
`minimumLinkMTU` (`session.go:508-513`), ICMP generation in `icmp.go`
(`packetAddresses`, `isControlProtocol`, `decrementHopLimit`, `buildICMPError`, `buildICMPErrorTo` at
`:12,25,44,73,99`).

### Session

`session`, `newSession`, `run`, `loopDatagram`, `loopCapsule`, `readDatagramCapsule`,
`readControlCapsule`, `writeCapsule` (`session.go:80,103,133,162,245,277,302,350`).

**CONNECT-UDP is expressed as the RFC 9298/9297 datagram form** — `writePackets` prepends the context
ID (`session.go:481`, `PrependContextID`) — not as a separate capsule-per-packet type.

Client: `Client`, `clientSession`, `NewClient`, `Start`, `Close`, `Start`/`Suspend`/`Resume`/
`RestartSession`, `WritePacketBuffers`, `handleAddressAssign`, `handleDNSAssign`, `handlePREF64`,
`handleRouteAdvertisement`, `handlePacket`, `handlePacketTooBig`, `loop`, `connect`, `activeSession`,
`Ready`, `WaitReady` (`client.go:24,72,81,139,207,242,247,272,329,391,397,405,429,444,455,467,518,554,585,607,662,686`).

Server: `ServerHandler`, `Server`, `serverSession`, `NewServer`, `NewTunnelRequest`, `releaseSession`,
`lookup`, `route`, `RouteOutbound`, `Contains`, `WritePacketBuffers`, `receivedPacketHeadroom`,
`assignedAddresses` (`server.go:24,30,53,74,85,161,298,321,339,347,372,386,428,449`).

Address pool: `addressPool`, `usable`, `allocate`, `release` (`pool.go:10,30,37,59`).
Route matching: `routeMatcher`, `compileRouteMatcher`, `contains` (`route_matcher.go:49,69,98`).

### The ownership rules — the strictest in the tree

| # | Rule | Anchor | What breaks |
|---|---|---|---|
| M1 | Single-packet success **releases** the datagram — **except** on the owned-datagram capability, where **ownership transfers** and the release is skipped entirely | `session.go:485-489`, `:495`; `transport/http/client_h3.go:506` | double release / use-after-free |
| M2 | Every failure path releases **exactly the still-owned remainder**: `datagram.Release()`, `buf.ReleaseMulti(capsules)`, `buf.ReleaseMulti(buffers[i+1:])` | `session.go:518-520` | leak of pooled buffers |
| M3 | Each chunk is all-or-nothing for **ownership**; `writeBatchOwned` returns the transferred prefix length and the caller falls back only on the remainder | `session.go`, `writeBatchOwned` | replaying a transferred prefix double-sends or releases it; see `buffer-ownership.md` for partial queueing in HTTP/3's copy fallback |
| M4 | A `PrependContextID` **substitution releases the original**, so substitution refuses the affected chunk before send and updates the caller's slice in place | `session.go`, `writeOwnedBatchChunk` | stale pointers, double release or packet loss on fallback |
| M5 | A batch of **one** never takes the batch path | `session.go:467-472` | a measured regression (batch=1 is slower) |
| M6 | Packet headroom is reserved by the **producer** so the 1-byte context ID fits | `session.go:561-565`; `protocol/masque/client.go:557 FrontHeadroom`; `transport/masque/server.go:428 receivedPacketHeadroom` | every packet takes the substitution path (M4) |

**M4 is a real past bug, and the comment records it (`session.go:539-549`):** a refused batch kept
the context ID, the fallback prepended a second one, `packetAddresses` read the wrong version nibble,
and **every packet was silently dropped — no error, no PTB**.

### The H3 client pool

`transport/http/client_h3.go:105` builds `&http3.Transport{EnableDatagrams: true, DisableCompression: true}`;
the batching API is `SendDatagramOwned(payload http3.OwnedDatagramPayload)` (`:506`).

Pool behaviour (`b83695603` → `1511c1c6a`): the pool now rotates **within the tied-minimum set**;
before, **slot 0 served every tunnel**. CONNECT is gated on `HandshakeComplete` (`0271b145a`) to close
a 0-RTT replay window. `http3_fallback` is consumed but its defaults were kept at **5 s / 5 m**, not
the httpclient's 5 m / 48 h (`9491008a9` — a dead-config fix).

At the current target, `option/http.go` and `transport/http/server_h3.go` leave the HTTP/3 server
path manager enabled by default; disabling migration is the opt-in setting. The H3 client test also
checks that its `DisablePathManager` is false by default. Congestion-control options are separate
for client and server, and an unset client choice leaves the library default; do not infer BBR from
an unset option. Verify the particular constructor and pinned QUIC version before generalizing.

---

## 2. The endpoint — `protocol/masque`

`ClientEndpoint` (`client.go:50`), `NewClientEndpoint` (`:118`), `Start` (`:319`),
`UpdateConfiguration` (`:339`), `installAssignedDNS` (`:397`), `Pref64Prefixes` (`:449`),
`resolverCapability` (`:467`), `h3ConnectionState` (`:504`), `UpdateTunnelTransport` (`:527`),
`recompileAssignedDNS` (`:536`), `InterfaceUpdated` (`:561`), `OnDemand` (`:565`),
`SetKeepIdleConnections` (`:569`), `PreMatchFlow` (`:590`), `PortAddresses` (`:594`),
`PortMTU` (`:617`), `UpstreamPort` (`:621`), `AttachReturn`/`DetachReturn` (`:625,629`),
`JudgeFlow` (`:633`), `NewDNSPacket` (`:637`), `WritePackets` (`:641`), `DialContext` (`:671`),
`DialsDestinationLocally` (`:706`), `lookupInner` (`:745`), `dohExecutor` (`:807`),
`dialResolved` (`:837`), `ListenPacketWithDestination` (`:882`).

`PREF64` lives in `protocol/masque/pref64.go`, consumed via `ClientEndpoint.Pref64Prefixes()`
(`client.go:449`) and `clientSession.handlePREF64` (`transport/masque/client.go:585`); the server
answers at `transport/masque/server.go:495`.

---

## 3. The two resolvers — not interchangeable

| Role | What it resolves | Config field | Implementation |
|---|---|---|---|
| **Bootstrap (outer)** | the **server hostname** | `option/masque.go:34-40` `DialerOptions.DomainResolver` | `protocol/masque/bootstrap_cache.go` — `bootstrapResolution` `:65`, `bootstrapCache` `:89`, `newBootstrapCache` `:101`, `resolve` `:115`, `recordFresh` `:156`, `promote` `:187`, `hasRecovery` `:197`, `bootstrapDialer` `:213`, `resolveCandidates` `:233`, `DialContext` `:259`, `buildBootstrapResolution` `:311`, `bootstrapResolver` `:331`, `strategy` `:349`; plus `bootstrap_race.go` (`handshakeRacer` `:52`, `dialWithWinner` `:123`, `awaitHandshake` `:334`, `splitByPreference` `:357`, `interleaveCandidates` `:398`, `masqueConnDialer` `:445`, `preferIPv6FromStrategy` `:481`) |
| **Inner** | the traffic carried **inside** the tunnel | `option/masque.go:33-44` `InnerDomainResolver`, whose comment says exactly that; unset preserves prior behaviour (normal DNS router rules with empty query options) | `protocol/masque/dns_resolver.go` + `dns_assignment.go` (`dnsAssignmentSnapshot` `:41`, `dnsConfigurationSnapshot` `:54`, `dnsResolverSnapshot` `:73`, `decide` `:266`, `hasUsableResolver` `:298`, `resolversByPriority` `:311`, `compileDNSAssignment` `:390`, `compileResolver` `:449`, `compileCapabilities` `:519`, `resolverCapability.sameH3OriginAvailable` `:370`, `reachableForProtocol` `:668`) |

The two are structurally separated: the bootstrap dialer is built **before any tunnel exists**
(`bootstrap_cache.go:311`), while the inner path is only consulted from `lookupInner`
(`client.go:745`). **Swapping them sends the server's own hostname resolution through a tunnel that
does not exist yet**, or leaks the in-tunnel names to the outer resolver.

---

## 4. MTU and PTB

**Three separate quantities** — see `mtu-and-geometry.md` for the full treatment. In MASQUE:

* **outer link MTU** — what the platform path carries;
* **outer QUIC initial packet size** — clamped when detour capacity is known; the constructor does
  not establish a hard ceiling on later packets (see `mtu-and-geometry.md`);
* **inner tunnel MTU** — what the tunnel promises its payload.

The PTB path: a datagram send fails with `datagramErrorTooLarge` → `mtu := maxPayloadSize - 1`; if
`mtu >= minimumLinkMTU` the context ID is undone with `datagram.Advance(1)` and
`handlePacketTooBig(datagram, mtu)` is invoked (`session.go:505-513`).

Tests: `packet_too_big_test.go`, `packet_too_big_session_test.go`, `packet_size_bound_test.go`,
`outer_quic_mtu_test.go`, `port_mtu_lifecycle_test.go`, plus
`protocol/masque/outer_quic_mtu_test.go`.

---

## 5. Teardown

`Server.releaseSession` (`server.go:298`), `sessionErrorIsExpected` (`:613`).
Contract tests: `transport/masque/shutdown_test.go`, `close_boundary_test.go`,
`close_boundary_h3_test.go`, `session_datagram_error_test.go`, `session_error_class_test.go`;
`protocol/masque/close_boundary_h3_test.go`, `readiness_boundary_test.go`,
`lifecycle_generation_test.go`, `scope_ownership_test.go`.

Two documented teardown fixes worth knowing:

* `9235f90` — the server endpoint's teardown is given to **the `Scope` that closes it**.
* `c3b4f567` — releases resources acquired **after `Scope.Close` returned**.
* `a367bbe3` — closes the readiness window, serialises **every** DNS invalidation, and fixes three
  platform test defects.
* `a00a2fe6` — makes the HTTP/3 acquisition part of the **close decision**, so a tunnel cannot be
  reported ready while its H3 leg is being torn down.

`protocol/masque/server.go` also has the "acquire after the drain" fix: `StartStateInitialize`
registers `s.Close` at the device acquisition, but `StartStateStart` binds the listening socket
*later*. `common/listener` has no start-after-close guard, so `ServerEndpoint` publishes `closed` under
`startAccess` before releasing and re-checks it after the bind (`acquiredStillOwned`); if the endpoint
was closed meanwhile, `Start` releases the socket it just bound itself.

---

## 6. MASQUE DNS roles and the DNS-lock work (commit family `8f1ba42a9`…`1d87f0cf7`)

`e93e297aa` replaced the earlier mutable DNS model with **immutable snapshots** that reuse native DNS
transports, deleting `assigned_dns_model_test.go`, `assigned_dns_transport_test.go`,
`assigned_doh_test.go`, `dns_leak_cycle_test.go`, `resolver_precedence_test.go` and replacing them
with `dns_assignment_test.go`, `dns_capability_test.go`, `dns_endpoint_test.go`,
`dns_request_url_test.go`, `inner_resolver_test.go` — a refactor-with-retest, not a silent drop.
Any document referencing the deleted files is describing the pre-`e93e297aa` model.

---

## 7. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Capsule parse/serialise, address and route types | `VERIFIED_UNIT` | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...` |
| Per-chunk atomicity, transferred prefix and refusal fallback | `VERIFIED_UNIT` | `batch_chunk_test.go`, `batch_order_test.go`, `session_datagram_error_test.go` |
| Teardown ownership and close boundaries | `VERIFIED_UNIT` | `shutdown_test.go`, `close_boundary*.go`, `scope_ownership_test.go` |
| MTU ceiling / PTB | `VERIFIED_UNIT` | the five MTU test files |
| H3 pool rotation, 0-RTT gate, fallback schedule | `VERIFIED_UNIT` | `transport/http` pool tests |
| Long-run allocation behaviour | `VERIFIED_UNIT` (slow) | `transport/masque/longrun_alloc_test.go` |
| CONNECT-IP interoperability against an independent implementation | **declaration only** — `test/jiejie/reference/connect_ip_go_interop_test.go` lives in a separate module CI does not run on push | `run-jiejie-suite.sh` (needs bash) |
| macOS/H3 device behaviour | **`UNVERIFIED`** | needs a device |
| Race-freedom | `-race` is **blocked on this host**; inspect the exact package list and SHA in the CI run before claiming coverage of MASQUE | Linux runner |
| `protocol/masque` suite | untagged tests passed locally during the 2026-10-28 refresh; that does not select QUIC-tagged cases. The historical tagged result is scoped to its recorded baseline. | use the appropriate profile and inspect which tests ran |

---

## 8. Provenance

| Commit | Type | What it did |
|---|---|---|
| `b83089ec` | — | **UPSTREAM-INHERITED.** "Add MASQUE support", 世界, 2026-09-21. Present below `e4b7e692`; reachable from upstream refs. **Not a fork feature.** |
| `d53c867b7` | FEATURE | the fork's **first fork-authored commit**: inbound masquerade handler — which is what the MASQUE/HTTP inbound serves over-limit probes |
| `7e57fe8c7` | HARDENING | the limiter actually bounds |
| `b83695603` → `1511c1c6a` | FEATURE/FIX | H3 pool and its tie-break |
| `0271b145a` | HARDENING | CONNECT gated on `HandshakeComplete` (0-RTT) |
| `9491008a9` | FIX | `http3_fallback` was dead config |
| `e4f847ecf` | COMPAT | **EXTERNAL-ADAPTED** QUIC defaults, from `quic-go/masque-go` + `connect-ip-go` |
| `969909bc1` → `b656b062f` | HARDENING → PERF/SUPERSEDED | cross-protocol overlap; the quadratic version was an 800× CPU DoS |
| `a529b68c8` | HARDENING | the 8192-entry capsule bound |
| `e4d2f6c1a` | FIX | `Contains() == lookup()` on the server's own address |
| `66f0e9436`, `db94a76b2`, `c0475793f` | HARDENING | further capsule bounds |
| `6d94da584` → `b5a25edd8` | PERF/ZERO-COPY | no per-packet alloc; `errors.As(&tooLarge)` forced a heap escape **even when `err == nil`** |
| `864c0048b` | PERF | binary search in the route matcher |
| `1a7b3ebe5` | ZERO-COPY | owned DATAGRAM path |
| `8870623cf` | FEATURE | bootstrap cache |
| `8f1ba42a9` → `1d87f0cf7` | FEATURE/REFACTOR | DNS roles made explicit; immutable snapshots (`e93e297aa`) |
| `05fa73391`, `b1961033d`, `a6164dead`, `b81244bdf`, `907dbfb99` | REFACTOR | dead-code removal, incl. the PREF64 clear path and a dead selection rule |
| `2118ee40` | FEATURE | introduced detour-capacity handling; see the current initial-size qualification in §4 |
| `0d28c26d` | FIX | makes the H3 fallback reachable; lets the tunnel leg fragment |
| `a367bbe3` | FIX/HARDENING | readiness window, serialised DNS invalidation |
| `a00a2fe6` | FIX | HTTP/3 acquisition is part of the close decision |
| `9235f90` | FIX | server endpoint teardown goes to the `Scope` that closes it |
| `c3b4f567` | FIX | releases resources acquired after `Scope.Close` returned |

All except `b83089ec` are **OWN** or, where marked, `EXTERNAL-ADAPTED` with a quoted source.

---

## 9. AI cautions

1. **Do not call MASQUE a fork feature.** The endpoint skeleton and DATAGRAM concepts are upstream's;
   the dataplane is the fork's. Say which half you mean.
2. **Do not go looking for `protocol/connectip`.** It does not exist.
3. **Do not add a `quic-go/masque-go` or `connect-ip-go` dependency** to the main module — the fork
   implements this itself and uses `connect-ip-go` only as an interop peer in `test/`.
4. **Do not make the batch path handle a single packet.** Batch=1 is a measured regression.
5. **Do not release a datagram on the owned path.** Ownership transferred.
6. **Do not fix a `PrependContextID` substitution in place.** It releases the original and must refuse
   the affected chunk, updating the caller's slice; do not replay an already transferred prefix.
7. **Do not remove the producer-side headroom reservation.** Every packet would take the substitution
   path.
8. **Do not "correct" the linear overlap check into the obvious quadratic one.** It was measured at
   13.5 s vs 16 ms — an 800× CPU DoS.
9. **Do not raise the 8192-entry capsule bound** without re-measuring; 149,796 entries held 17.65 MiB.
10. **Do not swap the bootstrap and inner resolvers**, and do not give `InnerDomainResolver` a
    default that routes through the outer path.
11. **Do not cap the outer QUIC packet by a constant.** It is capped by a *proven* detour capacity.
12. **Do not re-enable `MaxIncomingStreams = 1<<60`** or make QUIC tuning non-opt-in.
13. **Do not gate CONNECT on anything weaker than `HandshakeComplete`.**
14. **Do not cite the deleted `assigned_dns_*` test files** — that model was replaced by `e93e297aa`.
15. **Do not run this package's tests untagged and conclude anything.** They need
    `DEFAULT_BUILD_TAGS_OTHERS`; untagged gives 19 tag-absence failures.
