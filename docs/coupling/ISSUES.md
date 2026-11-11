# Stage-2 issues

Severity is about consequence for a running kernel, not about how interesting the
code is. **Status** is one of:

- `fixed` — changed in this stage, with a regression test;
- `tested` — no change; a test now pins the behaviour;
- `documented` — recorded with evidence and a proposed fix/experiment;
- `risk` — a design boundary that needs a product decision.

## Fixed

### F-03 — XHTTP pool opens a connection after Close (Medium-High)
`xmuxManager.Close` emptied the pool and closed the transport, but a dial that
raced or followed it found an empty pool and opened a replacement
(`transport/v2rayxhttp/xmux.go:get`). Nothing owned that connection afterwards:
the lifecycle had already run. The window is not theoretical for a network change
that closes the outbound while a stream is being admitted.

Fix: `xmuxManager` now has a `closed` flag set under the same lock as the pool
swap; `get` refuses to open when closed, and `getContext` maps the empty result to
an error instead of waiting out the caller's deadline.
Tests: `transport/v2rayxhttp/xmux_close_boundary_test.go` (after-close and
close-racing-dials, the latter asserting every opened connection is closed
exactly once).

### F-04 — Fast-fail rate limit measured queue time as creation time (Medium)

All four admission seams took `started := time.Now()` **before** `Acquire`, and
`observeOutcome` classifies a failure as fast only if it returned within 250 ms.
A dial that waited 300 ms at the gate and then failed in a microsecond was
therefore classified as slow: the streak reset, and the rate limit stayed
disarmed exactly while the gate was saturated — the state in which a failing far
side produces the storm the limiter exists to stop.

Fix: the clock starts after the permit in `common/tls/client.go`,
`protocol/anytls/admission.go`, `protocol/trusttunnel/outbound.go` and
`protocol/mieru/dialer.go`.
Test: `common/tls/handshake_rate_window_test.go` — four dials queue longer than
the fast-fail window and then fail instantly; the test requires
`Rate.FastFailures >= 4` and a rate window to arm.

### F-08 — REALITY fallback ran on a connection the caller had closed (Medium)

On a REALITY verification failure the client launches the camouflage exchange on
the same connection after reporting the error, but the shared TLS dialer closed
that connection immediately (`common/tls/client.go`'s error path), so the request
raced a dead socket. A prober saw an abrupt close — the signature the fallback
exists to avoid — and two goroutines could use one socket.

Fix: the failure is now a `realityFallbackError` implementing a handoff marker
(`ConnectionHandedOff`); the dialer leaves the connection open when the error
carries it, and the fallback owns the close. The fallback also runs under
`C.TCPTimeout` and binds the request context, so a stalled camouflage site cannot
hold the socket or the goroutine forever.
Tests: `common/tls/reality_fallback_ownership_test.go` — the handoff connection is
not closed by the dialer, an ordinary handshake failure is closed exactly once,
and the marker survives error wrapping.

### F-07 (generation half) — Mieru single-transport dial published across a generation change (Medium)

`InterfaceUpdated` drains in-flight dials before stopping a generation, but the
drain is bounded; a dial that outlives it could return a connection created for
the network that was just left. The fallback path already re-checked the
generation, the single-transport path returned the connection unchanged.

Fix: the single-transport path goes through the same `acceptGeneratedConn` check
(`protocol/mieru/outbound.go`), closing the stale connection and returning
`errFallbackStale`.
Tests: `protocol/mieru/generation_boundary_test.go` — a generation change during
the dial closes the connection exactly once and fails; an unchanged generation
returns the connection untouched.

The other half of F-07 (no creation single-flight for Mieru underlays; the SDK's
idle cleaner racing `AddSession`) lives inside the SDK and is recorded below
unchanged.

## Documented — defects with a concrete next step

### F-01 — An on-demand tunnel can be suspended while it carries traffic (High, risk)

The eligibility pass suspends a referenced on-demand endpoint whenever the device
is paused (`route/reference.go:339`). MASQUE's suspend cancels the live session
unconditionally (`transport/masque/client.go:429-442`); WireGuard's downs the
device (`protocol/wireguard/endpoint.go:698-714`). The stage-1 test comment
describes the exact user-visible case — a tethered client through a phone tunnel
whose owner's screen goes off — and no guard refuses the suspend while flows are
active.

Next step: a policy test with a fake pause manager and a real endpoint carrying a
stream, asserting whether the stream survives. If it does not, the decision is
whether `SetKeepIdleConnections(false)` should be deferred until the tunnel is
idle, or whether "device paused" must not reach a tunnel with live flows. Both
are product decisions; the audit did not pick one.

### F-06 — AnyTLS charges a creation permit for a pure reuse (Medium, documented)

`dialWithAdmission` takes the permit before `sing-anytls` is asked whether an idle
session exists (`protocol/anytls/admission.go:59` before the external
`Client.DialContext`). Under a saturated gate, a stream that would create nothing
waits or is refused with `ErrQueueFull`, and the gate is process-wide, so one
outbound's stalled handshakes can starve another's healthy reuse.

Next step: a pool-aware check needs the external module to expose "would this
dial reuse"; `sing-anytls` is not replaced in `go.mod`, so this is a fork patch or
an upstream API request, not an in-tree edit. TrustTunnel's shared-offer
admission is the reference semantics.

### F-07 (remaining) — Mieru has no creation single-flight and can lose a dial on an idle-clean race (Medium, documented)

Every concurrent cold dial calls the SDK's `newUnderlay`; there is no waiter join,
so a burst creates N sockets up to the admission limit. In the SDK, the idle
cleaner can close an underlay between `Mux.DialContext` picking it and
`AddSession`, and the fork's dial returns that failure without a retry. Both
live inside the SDK; the generation half of this finding is fixed above.

Next step: a single-flight wrapper around `newUnderlay` and a retry on
`AddSession` failure are local to the fork's adapter but change the SDK's
connection-count semantics, so they need a measurement of the burst they are
meant to damp first.

### F-10 — QUIC/H3 creations are outside the admission budget (Medium, documented)

MASQUE H3, XHTTP H3, v2rayquic and gRPC handshakes do not reach
`handshakeWithAdmission`; TrustTunnel admits its session but not the QUIC
handshake as a separate unit. On the platform the guard exists for, an H3 storm
is unbounded while an H2 storm is bounded.

Next step: the experiment is one saturated guard and two dials, H2 and H3; the
H2 must fail and the H3 must succeed. The design (if any) must distinguish
socket, QUIC transport, HTTP/3 client and logical session, must not charge a
stream, and must not collapse a tunnel session and a per-request transport into
one class ([DECISIONS.md](DECISIONS.md), D-2).

### F-13 — `Registration.Stale`/`Acknowledge` are dead code (Low, documented)

They encode a pull-side generation contract that no production caller uses; only
WireGuard's push/lease path is live. Either a consumer appears (F-15) or the API
should be removed so the next reader does not assume a check that never runs.

## Documented — contracts that are honest but easy to misread

- **F-11** — `retireIdleResources` returns owners asked, not connections closed;
  `Box.CloseIdleConnections` discards the count. Every pool legitimately keeps
  busy resources. The interface comment should say "asked", not "released".
- **F-12** — reclaim returns after staging disposal; `Close` errors are dropped;
  only `CloseAll` waits.
- **F-15** — a dial during the 6 s listener phase can reuse a pre-transition
  pooled session; no protocol reads the epoch at pool hand-out.
- **F-14** — the reference graph is recomputed only on history/clash/pause
  events, so an outbound that becomes referenced later can stay `keep=false`.

## Tested boundaries (no code change)

- Stage-1 B7 (userspace WireGuard/AWG two-process concurrent flows) is **not** a
  stack limitation: the in-repo dataplane test passes six concurrent flows, and
  `test/wireguard_concurrent_flows_test.go` drives four concurrent flows through
  the laboratory's SOCKS → route → endpoint topology in one process. The failure
  requires the two-process shape. The stagger experiment narrows it further: two
  streams started together fail (0 and 200 ms spacing, 2/2 each), while 1 s
  spacing passes (2/2) — the trigger is *simultaneous handshakes*, not the
  transfer path. `perflab -stagger` is the diagnostic that produced this.
  Instrumenting the pinned sing-tun drop points is the next step; it must use the
  module version `go.mod` pins, not the newest in the module cache.
- `TestWireGuardEndpointDataPlane/system=true` fails unprivileged by design
  (TUNSETIFF); this is an environment failure, not a regression.
