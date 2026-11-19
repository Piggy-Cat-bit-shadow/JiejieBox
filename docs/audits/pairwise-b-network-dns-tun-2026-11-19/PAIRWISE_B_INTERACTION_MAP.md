# PAIRWISE B — Interaction map: DNS, TUN, address families, network state

Window B of the JiejieBox cross-system pairwise debug. This file is the map every other B document
cites: who owns which state, which call chain reaches it in production, and which platform/mode/build
entry actually enables it. Nothing here is copied from a historical report; every row was read in the
current source (`testing` @ `d3b0e8190` + this window's commits) and the file:line is given.

## 1. Ownership

| State / resource | Owner (authority) | Where |
| --- | --- | --- |
| Network generation (epoch) | `common/runtimecoord.Coordinator` — monotone, one publication per reset | `common/runtimecoord/coordinator.go:36-108` |
| Per-resource generation view, rebind lease, coalescing | `Coordinator.Registration` | `coordinator.go:156-640` |
| Whether a dial may publish its result | `GenerationGate.BeforeDial/AfterDial` | `common/runtimecoord/generation_gate.go:113-183` |
| Network transition token + "stable" flag | `route.NetworkManager` (`transitionOwner`, `transitionAccess`) | `route/network.go:1390-1440` |
| Idle trim / aggressive release | `NetworkManager.TrimMemory/ReleaseMemory` → `ReferenceManager.retireIdleResources` | `route/network.go:1835-1900`, `route/reference.go:601-625` |
| Fake-IP allocation, persistence, retired intervals | `dns/transport/fakeip.Store` + `adapter.FakeIPIssuanceLedger` | `dns/transport/fakeip/store.go:98-364`, `adapter/fakeip_issuance.go` |
| Fake-IP → domain restore, and refusal of unattributable placeholder | `route.Router.prepareMatchMetadata` | `route/route.go:1326-1428` |
| DNS rule walk → transport → cache | `dns.Router` | `dns/router.go:476-1272` |
| Address-family strategy, candidates, racing, per-flow admission | `common/dialer.DefaultDialer` + `dualStackScheduler` | `common/dialer/default.go`, `dual_stack_scheduler.go`, `admission.go` |
| Physical pools | per transport (`transport/v2rayxhttp` xmux, `transport/trusttunnel` session, QUIC family carriers) | `transport/*` |
| Reload / replace | Box lifecycle | `box.go`, `box_lifecycle.go` |

Two authorities that look duplicated and are not: `Coordinator` owns the *epoch*; `NetworkManager`
owns the *transition*. The publication site reads the manager's token inside the defer that releases
`transitionAccess`, so the epoch the resources see is the one the transition claimed
(`route/network.go:1423-1431`), and a reader outside the lock cannot see a new token with a settled
state (`:1433-1440`).

## 2. Data path (one TUN flow)

```
packet ──▶ protocol/tun inbound
        ──▶ Router.PreMatch ──────────────────────────────────────────────┐
              • prepareMatchMetadata (route/route.go:1326)                │
                  · fake address? Store.Lookup → domain  (:1368-1381)     │
                  · no record and issued by us → REFUSE  (:1382-1386)     │
                  · retired range, no record → REFUSE    (:1387-1414)     │
                  · otherwise reverse mapping / sniff / IP version        │
              • rule walk (route/route.go) → outbound                     │
        ──▶ outbound.DialContext / ListenPacket                          │
              • common/dialer: family strategy + candidates + racing     │
              • per-flow admission policy (common/dialer/admission.go)    │
              • generation capture → GenerationGate.BeforeDial            │
        ──▶ transport pool (xhttp xmux / trusttunnel session / QUIC)      │
        ──▶ socket ──▶ back through the same flow tracker ────────────────┘
```

The two facts this path is built to guarantee: a fake address that this installation minted is never
forwarded as a literal, and a dial that started in an older generation cannot publish into the
current one (see §3).

## 3. Control path (network change)

```
interface event / reset / release
  ──▶ NetworkManager.beginTransition            route/network.go:1390+
        · transitionAccess.Lock()
        · unstable = true                        (:1433)
        · transitionOwner++                      (the new token)
        · defer: unlock transitionAccess, THEN
                 Coordinator.Advance(claimed)    (:1423-1431)
  ──▶ Coordinator.Advance(epoch)                 coordinator.go:80-108
        · epoch = max(epoch, claimed); closed the change channel (fan-out)
  ──▶ every Registration reacts                   coordinator.go:388-440
        · observeEpoch → invalidated; ScheduleRebind/BeginRebind coalesce the burst
  ──▶ transport boundary action (per protocol)
        · xhttp: RetireSuspect (drain, keep live streams)
        · trusttunnel/hy2/tuic: CloseWithError (session generation bump)
        · anytls: CloseIdleConnections (idle-only)
  ──▶ next dial: GenerationGate.BeforeDial        generation_gate.go:113
        · a result whose generation moved is discarded, not published
```

`Advance` is monotone and idempotent (`epoch <= c.epoch` is a no-op), so a duplicate event cannot
re-sweep the resources (`coordinator.go:80-90`, pinned by `TestAdvancePublishesMonotoneGenerations`).

## 4. Effective entry: platform / mode / build tag

| Mechanism | Real entry | Enabled when | Not available when |
| --- | --- | --- | --- |
| TUN capture | `protocol/tun` inbound | `inbounds[].type=tun` + a `with_gvisor`/system stack for the platform | no tun build tag, no permission |
| fake-IP | `dns.transports[].type=fakeip`; carry-over with `experimental.cache_file.store_fakeip` | a DNS rule routes A/AAAA to the fakeip transport | no fakeip transport ⇒ `prepareMatchMetadata` skips the restore/refusal branch entirely (`route/route.go:1368`) |
| auto_redirect | `sing-tun` platform, Linux only | Linux + nftables/iptables + capability | every other platform; no privilege |
| TrustTunnel H2 session | `transport/trusttunnel` | `with_trusttunnel` | build without the tag |
| TrustTunnel H3→H2 | `quicFallback` verdict + `h3Cordoned` | operator option | no QUIC build |
| naive H2/H3 CONNECT | `protocol/naive` | `with_naive_outbound` (Darwin profile only) | server profile (no such tag) |
| MASQUE endpoint | `protocol/masque` | `with_quic` | no QUIC build |
| XHTTP/XMUX | `transport/v2rayxhttp` | `with_xhttp` | no xhttp tag |
| IPv4/IPv6 racing | `common/dialer` | whenever a family strategy or a second candidate exists | single-family destination/strategy ⇒ one candidate, no race |

## 5. Junctions this window attacks (the B pairs)

| Pair | Shared state | Where the two meet |
| --- | --- | --- |
| B01 families × cancel | candidate set, dial context, generation | `dual_stack_scheduler.go`, `common/dialer/admission.go` |
| B02 DNS/fake-IP × TUN/route | fake-IP mapping + flow metadata | `route/route.go:1326-1428`, `dns/transport/fakeip/store.go` |
| B03 auto_redirect × TUN/routes | kernel routes/nft sets vs the TUN device | `sing-tun` platform, `protocol/tun` |
| B04 H3/QUIC pool × generation | epoch + pooled physical connections | `transport/*/generation.go`, `route/network.go:1430` |
| B05 fallback × error class/ALPN | error classification + configured transport promise | `protocol/naive/h3_fallback.go`, `transport/trusttunnel` (`quicFallback`, `h3Cordoned`) |
| B06 DNS cache × family racing | cached A/AAAA + candidate plan | `dns/router.go` cache path, `common/dialer` candidates |
| B07 reload × live state | old/new Box object graphs | `box.go`, `box_lifecycle.go` |
| B08 rules × outbound switch | rule set + selector + real socket | `route/route.go` rule walk, `protocol/group/*` |
| B09 H2/H3 body × cancel/close | request/response body vs stream lifetime | `transport/trusttunnel`, `transport/v2rayxhttp`, `protocol/naive` |
| B10 MASQUE × NAT64/PREF64/DNS | discovered prefix + QUIC path | `protocol/masque/pref64.go`, `dns_assignment.go` |
| B11 UDP session × TUN route change | NAT entry + outbound session | `sing-tun` `UDPNat`, `route/conn.go` |
| B12 free | — | this window: the toolchain as a participant (§ `PAIRWISE_B_FINDINGS.md` B-1) |

## 6. What "enabled" does NOT imply

Recorded because this window's first finding is exactly this shape:

- A build tag enables a transport; it does not enable the *test* that pins its wire behaviour. See
  `PAIRWISE_B_MATRIX.md` for which suites on a push path execute each junction.
- A configuration key enables a mechanism; the mechanism still reads defaults from the standard
  library. TrustTunnel's HTTP/2 preface is half configuration, half `net/http` defaults, and a
  toolchain move changes the second half with no line of this repository edited (B-1).
