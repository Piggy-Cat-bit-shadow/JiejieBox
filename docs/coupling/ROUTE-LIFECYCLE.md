# Route, idle cleanup, on-demand tunnels, memory pressure

This is the layer that decides when a resource owner is asked to release, and it
is where "close" acquires three different meanings.

## The three actions

| action | interface | contract | who calls it |
|---|---|---|---|
| release idle | `adapter.IdleConnectionReleaser.CloseIdleConnections()` | close only what carries no user traffic; must not dial | `route/reference.go:504-514` (trim + `Box.CloseIdleConnections`), `route/network.go:1867,1875` |
| refuse new work, drain the rest | `adapter.ReuseSuspect.RetireSuspect()` | close empty, keep live streams on their connection, hand nothing new out | `route/reference.go:571-577` (reuse boundary only) |
| suspend the tunnel | `adapter.IdleConnectionKeeper.SetKeepIdleConnections(false)` | device-level suspend/resume, not an idle action | eligibility pass `route/reference.go:316-358` |

`IdleConnectionKeeper` is `Releaser + SetKeepIdleConnections`; the on-demand
tunnels (WireGuard, MASQUE, OpenVPN, OpenConnect, Tailscale) implement the
setter but deliberately **not** `CloseIdleConnections`. `route/reusable_owner_
inventory_test.go` pins both directions: they must remain reachable by the
eligibility pass and must not become reachable by the release walks.

## What the walk can and cannot promise

`retireIdleResources` returns a count of owners **asked**, not of connections
closed (`route/reference.go:501-513`). `Box.CloseIdleConnections` discards that
count, and no owner reports completion. Every pool legitimately keeps a
connection that carries a stream (`xmux.go:500-510`,
`transport/trusttunnel/client.go:371-381`, `v2rayquic/client.go:449-460`,
`httpclient/managed_transport.go:166-171`).

So "the runtime released everything reusable" is true only as "every owner that
implements the release capability was asked". That is the honest contract, and it
is a documentation gap in `adapter.IdleConnectionReleaser` (F-11), not a bug: the
alternative — waiting for every pool to confirm — would require a completion
signal no owner has.

Reclaim (`route/conn_reclaim.go`) goes one step further and returns after
*staging* disposal; the socket closes on the disposer goroutine, errors from
`Close` are dropped (`route/conn.go:246`). `CloseAll` does wait
(`conn_reclaim.go:850`). The difference matters to a caller that assumes
"Reclaim returned = sockets closed" (F-12).

## On-demand tunnels and the device pause

The eligibility pass computes `keep = referencedOutbounds[tag] && !devicePaused`
for on-demand endpoints (`route/reference.go:339`). A device pause therefore
suspends a referenced tunnel:

- MASQUE `Suspend()` cancels the live session and resets HTTP connections
  (`transport/masque/client.go:429-442`) with no stream-count guard;
- WireGuard `SetKeepIdleConnections(false)` calls `Down()` on the device
  (`protocol/wireguard/endpoint.go:698-714`);
- TrustTunnel is an outbound, and its close paths guard on `streams>0`
  (`protocol/trusttunnel/outbound.go:266-275`).

This is the one place where a lifecycle action can cut live traffic: the
stage-1 comment that protects tunnels from the idle walks describes the Apple
hotspot case (a tethered client keeps using a tunnel whose owner's screen went
off). The audit could not find a guard that refuses a suspend while a tunnel
carries flows, and the policy test that would prove the data loss does not exist
today. It is recorded as F-01 (High) with the experiment that settles it; the fix
is a product decision (suspend only when idle, or accept the teardown), not a
mechanical change, so it was not made.

## Memory pressure

`service/oomkiller` publishes the level and the guard reads it; the reclaim
ladder is L1 caches → L2 idle pools → L3 GC, and crisis adds a full network
reset (`service/oomkiller/timer.go:818-867,983-986`). Shed hooks exist exactly
twice (AnyTLS and TrustTunnel, both idle-only, edge-triggered once per entry into
Critical). Active streams cannot be closed by the contract, and recovery
creation is throttled (Critical plan: limit/4, queue/8, 100 ms wait), not
blocked. That asymmetry is correct for an extension whose failure mode is a
memory kill: refusing all recovery would turn a slow moment into a dead tunnel.

The gap is coverage, not semantics: eight protocols, two shed hooks, no
coordinated reclaim for MASQUE's userspace stack, WireGuard's device, or Cronet's
native memory. Those are the three owners whose memory is dominated by native
allocations invisible to the Go heap — and none of them can be trimmed by a hook
that only drops idle Go connections.
