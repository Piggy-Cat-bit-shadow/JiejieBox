# Network generations and rebind

`common/runtimecoord` provides two things: a monotone **epoch** for the network,
and a **rebind lease** that lets one resource perform a controlled recreation
inside a transition. This document records who actually uses them.

## The mechanism

- `Coordinator.Advance` publishes a new epoch, ignoring non-increasing values
  (`coordinator.go:80-104`); the publisher is `route/network.go:1426-1432`, after
  the transition lock is released.
- `Coordinator.Register` creates a named `Registration` with a recovery window; a
  lease is granted once per window with one in flight (`BeginRebind`), carries a
  generation and a context, and ends with `Complete` or is invalidated by a
  generation change (`coordinator.go:225-465`).
- `Registration.Stale()`/`Acknowledge()` exist (`coordinator.go:499-536`) but
  have **no production caller**. The only consumer that validates a generation is
  WireGuard's rebind path; everything else is push-based (`InterfaceUpdated`).

## Who registers, who reads

| consumer | registration | generation read before reuse | effect of a change |
|---|---|---|---|
| WireGuard endpoint | yes (`transport/wireguard/endpoint.go:153-157`) | rebind lease carries the generation; family tolerance in `bind_family.go` | `UpdateBind` reopens the UDP socket; in-flight rebind not cancelled; the stack is kept |
| LoadBalance group | no registration | reads `Epoch()` for its penalty ledger (`protocol/group/loadbalance_failover.go:434-439`) | penalty window partition |
| URLTest probes | measurement context only | no | — |
| every protocol outbound | no | **no** | `InterfaceUpdated` fan-out; pools reset/drained by their own logic |
| Mieru | no | `generation` counter checked only on the fallback path (`outbound.go:590-611`) | runtime swap; non-fallback dial can publish across it |
| TrustTunnel | no | `pending.discarded` check at publish (`client.go:640-662`) | reset discards pending, closes session |
| XHTTP | no | pool-local `generation` for the reuse boundary (`xmux.go:426,583-586`) | `RetireSuspect` drains |
| AnyTLS | no | **none** in the external client | `Reset` closes sessions, but a creation crossing it publishes unconditionally |
| MASQUE / Naive | no | none | session restart / `CloseAllConnections` |

## The transition window

`route/network.go` publishes the new epoch, then runs listeners under a 6 s outer
bound (`:1715-1718,1727`) and abandons stragglers with a log (`:1802-1807`). New
traffic can be admitted during that window, and no protocol consults the epoch on
its dial path, so a dial can attach to a pooled session that has not been reset
yet. The dial-side guard is a different mechanism: a connection created before the
epoch change is closed at hand-over (`common/dialer/default.go:797-803`,
`errNetworkChanged`), but that covers the *socket being handed over*, not the
*pooled session reused by a new stream*.

Consequence: the correctness boundary is "the listener finishes within 6 s", not
"the generation is authoritative". That is a deliberate trade for platforms whose
interfaces do not report a settled state, and it is recorded as F-15 (Low-Medium)
with the test that would measure it.

## What a unified generation check would and would not buy

A per-protocol "epoch at pool entry, epoch at hand-out" check would close the
window above, and it is cheap: one atomic load and a comparison. It would not
replace `InterfaceUpdated`, because a generation change also has to *act* on the
resource (reset a bind, cancel a session), not merely refuse reuse. The audit's
recommendation is therefore narrow: read the epoch at pool hand-out in the
protocols that pool sessions (XHTTP, AnyTLS, Mieru, TrustTunnel), and leave the
action to the existing listeners. It is not done in this stage because none of
those pools currently has an epoch to compare against in its own type, and
threading one through every pool is a wider change than the evidence justifies
for a 6 s window (see [DECISIONS.md](DECISIONS.md), D-3).
