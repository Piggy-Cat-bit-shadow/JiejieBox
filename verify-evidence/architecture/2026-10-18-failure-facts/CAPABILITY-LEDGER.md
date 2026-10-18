# Cross-protocol capability audit

Date: 2026-10-18. Tree: `testing` including the Mieru fallback work.
Evidence level: `VERIFIED_SOURCE` (methods read) plus package tests; no device
or reference-client claim.

| Protocol / component | Owner | Public dial/DNS | Network generation / rebind | Idle / reuse | Failure facts | Status |
| --- | --- | --- | --- | --- | --- | --- |
| VLESS | `protocol/vless.Outbound` | fork dialer + resolver | `InterfaceUpdated` | `SetKeepIdleConnections`, `CloseIdleConnections`, `RetireSuspect` | no producer | Existing owner is the richest; no change. |
| AnyTLS | `protocol/anytls.Outbound` | fork dialer | `InterfaceUpdated` | `SetKeepIdleConnections`, `CloseIdleConnections` | no producer | Existing; no `RetireSuspect` because its pool has no multiplexed stream-drain state. |
| Naive | `protocol/naive.Outbound` | own HTTP/2/H3 client over fork dialer | `InterfaceUpdated` | not exposed as idle keeper | no producer | Its transport owns the live connections; exposing a fake idle method would be a false capability. |
| MASQUE | `protocol/masque.ClientEndpoint` | own QUIC/H2/H3 client | `InterfaceUpdated`, `SetKeepIdleConnections` | no `CloseIdleConnections` on the endpoint | no producer | Resume/suspend is the transport's contract, not an idle-pool release. |
| Mieru | `protocol/mieru.Outbound` | fork dialer + DNS bridge for both fallback runtimes | `InterfaceUpdated`, explicit Stop/drain | SDK Mux owns idle underlays; no adapter idle keeper | **producer added this round** | Target/path/caller facts exported; bare TCP EOF remains Unknown. |
| Shadowsocks | `protocol/shadowsocks.Outbound` | fork dialer | `InterfaceUpdated` | `SetKeepIdleConnections`, `CloseIdleConnections` | no producer | Existing; no suspect pool. |
| Hysteria2 | `protocol/hysteria2.Outbound` | own QUIC | `InterfaceUpdated` | idle keeper | no producer | Existing. |
| TUIC | `protocol/tuic.Outbound` | own QUIC | `InterfaceUpdated` | idle keeper | no producer | Existing. |
| WireGuard | `protocol/wireguard.Endpoint` | own device | `InterfaceUpdated`, `SetKeepIdleConnections` | no generic idle keeper | no producer | Device wake/suspend is endpoint-owned. |
| Groups | `protocol/group` | route resolves leaf | leaf owns generation | leaf owns idle | **consumer added this round** | Group policy uses facts without owning resources. |

## What the audit actually found

1. **No missing idle/reuse method is a defect by itself.** The interface
   method must be backed by a real owner; Naive/MASQUE/WireGuard do not expose
   `CloseIdleConnections` because their live transport is the owner. Adding a
   no-op would be a false capability, which the architecture forbids.
2. **Group wrappers do not own leaf pools.** `route/reference.go` walks the
   outbound manager, so a leaf referenced by several groups is visited once;
   the group itself is not an idle owner. The existing tests for group
   references already pin the ownership.
3. **The real cross-protocol gap was failure attribution, not another
   interface.** Every proxy group only had errno; a target refusal and a dead
   proxy endpoint could arrive as the same `ECONNREFUSED`. Mieru already had
   the classification internally, so the minimal fix was to export it through
   a narrow optional error fact and let the existing group policy consume it.
4. **The failover shim's eight hidden optional capabilities are intentional.**
   `route/failover.go` documents them as unreachable from the dialer argument
   today (packet path passes the leaf); forwarding them would be no-op churn.
   No code change was made there.

## Not changed

- No protocol wire/handshake/crypto/MUX change.
- No new capability manager, idle pool, DNS plane, timer, goroutine or lock.
- No facts for protocols whose error path cannot prove a stage; their existing
  errno policy remains.
- No API/UI field; the fact is internal until a client contract exists.
