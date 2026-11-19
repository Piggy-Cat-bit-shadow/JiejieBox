# TRIPLEWISE B — Wiring and leak evidence

For every triple this window examined, the chain that has to hold is:
**loaded → capture → DNS decision → route decision → physical egress → data returned / refused**. A
"break" means the control-plane decision did not reach the data plane, or two mechanisms disagreed.
Each section gives the chain with file:line and the negative control that proves a test would notice a
break.

## 1. TB-1 — fake-IP → TUN ghost guard → router refusal

| Stage | Where | What it guarantees |
| --- | --- | --- |
| loaded | `dns.transports[] type=fakeip`; `protocol/tun` resolves the store at `Start` (`inbound.go:585-598`) | without a fakeip transport there is no store, and the guard's second half (the durable record) is what still recognises a placeholder |
| capture | TUN `JudgeFlow` receives the flow before the router sees it | the fast path may bypass the router entirely, which is why the guard has to live here too |
| DNS decision | `fakeip.Store.Create` allocates, refuses out-of-range (`store.go`, window B's B-3), and resumes past the ledger on a reload (window C's C-2) | every issued address is inside the configured range and is not a re-issue |
| route decision | `route.prepareMatchMetadata` (`route/route.go:1368-1414`): `Lookup` → domain + `metadata.FakeIP = true`; a missing record is an error; an address this instance issued is refused; a retired range is refused only with issuance evidence | the restored domain is what the rule walk matches on |
| egress | the outbound dials the **domain** the store returned, and `newFakeIPNATPacketConn` maps replies back (`:545`) | the physical connection never carries the synthetic address |
| refusal | the three refusal reasons are distinct strings naming the interval | a report can tell "never ours" from "ours, retired" from "ours, unrecorded" |

**Negative controls already in the tree (window A/B, re-run green this window):** MC-B3 (disable the
range guard → issues `198.18.0.2`), MC-B4 (range-only guard → the router is never consulted), MC-B5
(remove the durable fallback → the refusal is lost), MC-C2 (remove the ledger seeding → a reload
re-issues a held address). Each fails on the *guard*, not on a mock of it.

**What a leak would look like and why it is now closed:** a synthetic address reaching `DialContext`.
The three places that could produce one — out-of-range allocation (fixed), a retired placeholder
bypassed by the route set (fixed), a placeholder with no live store to refuse it (fixed) — are the three
controls above.

## 2. TB-2/TB-3 — network transition → shared clients → fallback state

This is the row window B could only read. The chain is now verified end to end:

```
NetworkManager.performTransitionLocked
   ├── resetNetworkLocked(...)                     route/network.go:1685-1690
   │      └── r.router.ResetNetwork()              unconditional, "the DNS transports and their
   │             ├── httpClientManager.ResetNetwork()   environment pins move TOGETHER"
   │             │      └── ManagedTransport.Reset()  (managed_transport.go:174)
   │             │             epoch.Swap(nil) → next request rebuilds via factory()
   │             │             ⇒ a FRESH http3FallbackTransport + http2FallbackTransport:
   │             │                h3Broken ladder cleared, http2FallbackTransport.fallbackAuthority
   │             │                map empty
   │             └── dns.ResetNetwork()  (resolver pins)
   ├── commitTransition(token)                     settle BEFORE the listeners
   └── notifyNetworkChangeListeners                route/network.go:1751 (only caller)
```

Three facts this proves, which the pairwise round had only argued:

1. **The third system participates.** The H3 verdict ladder (`markH3Broken`, 5 min → 48 h) and the
   "this authority is HTTP/1.1 forever" pin (`fallbackAuthority`, `http2_fallback_transport.go:45-86`)
   are *not* epoch-keyed. Without the reset chain they would outlive the network that produced them; the
   chain is what makes a stale verdict impossible.
2. **It is ordered before the listeners.** `resetNetworkLocked` runs before `commitTransition`, and the
   listeners run after the settle — so no listener can observe a half-reset client chain.
3. **It is not gated on the pause.** The comment at `route/network.go:1684-1688` says why: gating it
   would let the DNS pin and the socket disagree.

**What remains true, and is not a wiring break:** the demotion is silent (window B's R-B3). A reader of a
report cannot tell "h3 broken, fell back" from "worked", and `markH2Fallback` has no window inside one
epoch. That is an observability decision, not a broken chain — and it is the honest residual of this row.

**Negative control used:** none can be written against the public surface yet, which is precisely R-B3's
complaint. The equivalent evidence is source-order plus the fact that `ResetNetwork` is unconditional; a
mutation (gating it on the pause) would be caught by the DNS-pin tests, which is the closest detector.

## 3. TB-10 — `H3 only` must not degrade

| Claim | Evidence |
| --- | --- |
| a configuration with `disable_version_fallback` builds **no** H2 path | `common/httpclient/client.go:134-135` builds `newHTTP3Transport` and the requirement becomes `AllOf UDP` (`transport_requirement.go:84-87`) — there is no fallback object to reach |
| naive refuses a fallback configuration it cannot honour | `protocol/naive/outbound.go:110` rejects `quic_fallback_to_h2` without `quic:true`, and builds the fallback engine only when opted in (`:261-279`) |
| a legal fallback excludes security-class failures | trusttunnel: cert/x509/hostname/unknown-CA refused (`client.go:502-517`), 407 and QUIC auth refused from retry (`:1164-1167`, `errors.go:125-137`); pinned by `client_fallback_test.go` |

So the policy is honoured where it is configured, and the one degradation that *is* silent (in-epoch
H3→H2→HTTP/1.1) is inside the policy's allowance. Recorded, not "fixed", because making it loud is a
reporting decision.

## 4. TB-11 — reload consistency

`go test ./daemon` (21.7 s) covers the reload/recovery matrix: candidate parse failure keeps the running
configuration, provider live-refresh path, interruption windows, and the close→build ordering. The
half-installed-state question is answered structurally: the daemon closes the old instance **before**
building the new one, so no request can observe a mixed object graph. The cost of that ordering is
window B's R-B9 (a semantic validation failure leaves nothing running until config recovery). This window
confirms the consistency half and does not re-litigate the ordering half.

## 5. Torrent summary

| Chain | Holds? | Evidence |
| --- | --- | --- |
| fake address → domain → physical egress | **yes** | §1, three independent guard tests + their mutation controls |
| transition → shared clients → fallback state | **yes** | §2, source-ordered chain + the unconditional call |
| H3-only policy → no degradation | **yes** | §3 |
| reload → no half-installed graph | **yes** | §4 |
| TUN UDP flow → NAT entry → packet session → admission | **no** | TB-5, cross-repo (window A's R-4/R-5) |
| platform capture paths (auto_redirect, system proxy) | **not measured** | TB-7/TB-8, no Linux/no two capture paths |
