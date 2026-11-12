# Decisions

Every decision here says what was **not** unified and what evidence would change
it. A stage whose conclusion is "no change" still has to record the alternative
it rejected.

## D-1 — Admission stays per-seam; no `Admitter` interface

The eight protocols create four different kinds of expensive thing: a TLS
handshake, a QUIC session, a tunnel session with waiters, and a native engine. A
common interface would have to name one unit, and every seam would pass a
different thing through it. The principle recorded instead is *admit the
expensive unit, at the layer that owns it*:

- TLS handshake → the shared TLS dialer (covers every H1/H2/TLS outbound);
- shared tunnel session → the client that owns the singleflight (TrustTunnel);
- per-stream dial that may create a session → the protocol (AnyTLS);
- underlay socket → the adapter that replaces the SDK dialer (Mieru).

Changing this needs evidence that two seams with *the same* unit are
inconsistent. The audit found one: AnyTLS charges a reuse (F-06), which is a
placement defect, not a missing abstraction.

## D-2 — No generic QUIC admission in this stage

Requirements if it is ever built:

1. name the unit: UDP socket, QUIC transport, HTTP/3 client, or logical session;
2. prove at least two consumers share that unit's cost and lifetime — MASQUE's
   one long-lived tunnel, XHTTP's pooled per-request transport and TrustTunnel's
   shared session do not;
3. never charge a QUIC stream;
4. never charge a session that waiters join (singleflight belongs with the
   session, as TrustTunnel already does);
5. cancellation and reset need the same discard-at-publish rule TrustTunnel uses.

Until then the gap is documented (F-10) with an experiment that demonstrates it,
rather than papered over with an interface that would mischarge the one tunnel
that dominates a mobile budget.

## D-3 — No epoch check threaded into every pool

The transition window is bounded at 6 s and the reuse boundary already drains
pools (`RetireSuspect`). Adding a per-protocol epoch read at pool hand-out is a
small correctness win and a wide change: each pool would need the coordinator,
and the pools live partly in external modules (sing-anytls, Mieru SDK). Recorded
as F-15 with the test that would measure the window; revisit together with D-2,
because a QUIC session admission would need the same generation plumbing.

## D-4 — On-demand suspend: a device pause keeps a tunnel that is carrying traffic

**Decided (Q07, option a).** The two candidate policies were:

- **(a)** a device pause suspends only IDLE on-demand tunnels; a tunnel that is
  carrying traffic keeps its transport;
- **(b)** the pause keeps tearing every referenced tunnel down, and the published
  promise is narrowed to say a lock screen may end a transfer.

(b) was rejected as a user-visible downgrade: for a VPN/proxy product, "we may
cut your download when you lock the screen" is worse than the battery the
exemption costs, and it would have required editing the platform contract in
three places to say something weaker than the code already claims.

The implementation is `power.Policy.OnDemandIdleAfter` (the activity window, 60 s
by default) plus the ledger in `route/traffic_activity.go`, read by the eligibility
pass in `route/reference.go`. Three properties are part of the decision rather
than tuning:

1. **Activity is payload that moved, not a reference and not a connection count.**
   A referenced tag is the routing graph, not business; a stream count cannot tell
   a silent banking session from a download. Both were rejected by name.
2. **The exemption carries its own expiry.** Nothing emits an event when a
   transfer stops, so the pass arms a deadline at `last payload + window`; when it
   fires, an idle tunnel is suspended exactly as before. Without this the
   exemption would be permanent, which is the battery cost D-4 originally warned
   about.
3. **The verdict still comes from the pause manager's callbacks.** The withdrawn
   candidate in `route/reference_pause_test.go` (re-reading `IsDevicePaused()` on
   every pass) stays withdrawn: it does not repair a missed wake, and it breaks
   `experimental/libbox`'s reload-window contract.

**Not proven here, and not claimed:** battery cost and business continuity under a
real lock screen / sleep. That is a device round: the window (60 s) and the
one-wake-per-window cost of a kept transfer are provisional tuning until it is
measured, and the acceptance procedure is `docs/fork/apple-screen-state-acceptance.md`
§6. The exemption also does not cover a tunnel used as another outbound's
`detour`, because that traffic does not pass through the router's chain walk;
`route/traffic_activity.go` records that limit where a future per-endpoint counter
would replace it.

## D-5 — No shared session-pool abstraction

AnyTLS (external idle list), Mieru (SDK scheduler), TrustTunnel (one session with
waiters) and XHTTP (xmux usage counters) have genuinely different reuse rules:
"last stream released", "scheduler idle", "single session", "usage below
concurrency". A common pool would need a least-common-denominator policy and
would move correctness out of the code that can test it. What *is* worth sharing
is the creation primitive (singleflight + waiter-isolated cancel + discard on
reset), which only TrustTunnel has; recorded as a recommendation, not built.

## D-6 — The release contract stays "asked", not "confirmed"

`CloseIdleConnections` cannot report completion without every owner growing a
channel, and owners legitimately keep busy resources. The interface comment should
say "asked to release what is idle"; the walk's count should not be read as
"connections closed". Fixing the documentation is cheaper than inventing a
completion protocol nobody needs (F-11).
