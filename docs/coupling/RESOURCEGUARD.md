# ResourceGuard and admission

`common/resourceguard` is a process-wide budget for expensive *creations*: a
gate of N in-flight slots with an interactive reserve, a queue for waiters, and a
fast-fail rate window. On a platform without a budget (`resourceguard.FromContext`
returns nil — everywhere except an iOS Network Extension, `service/oomkiller/
service.go:79-85`) every seam is a nil-check no-op.

## The admitted unit, by seam

| seam | protocol / layer | admitted unit | before or after the expensive work | reuse charged |
|---|---|---|---|---|
| `common/tls/client.go:178-205` | every TLS outbound (VLESS/REALITY, Trojan, VMess, XHTTP H1/H2, Hysteria fallback, …) | one TLS handshake | **before** dial (so it covers DNS + TCP + handshake) | n/a |
| `protocol/anytls/admission.go:42-72` | AnyTLS | one stream dial | **before** the pool decision | **yes** |
| `protocol/trusttunnel/outbound.go:155-177` | TrustTunnel | one shared session creation | **before** dial, once per offer | no (waiters join free) |
| `protocol/mieru/dialer.go:85-120` | Mieru | one real underlay socket | **before** connect | no |
| `route/conn.go:587` (`GrowthBudget`) | copy path | one buffer-growth direction | at copy start | n/a (held for the copy) |
| `common/buildgate/buildgate.go:111` | provider/rule-set build | one build | around the build | n/a |

Everything else is unadmitted: MASQUE H3, XHTTP H3, TrustTunnel's QUIC handshake
(the session is admitted, the handshake is inside it), v2rayquic, gRPC TLS, Naive
Cronet engines, WireGuard/AWG devices, and REALITY's server-side handshake.

## What the seams mean

- **A permit bounds concurrent creations, not live streams or bytes.** The Permit
  attaches to no socket; releasing it at dial return is correct and is what every
  seam does. Holding it for a connection's lifetime would be a different,
  bigger decision.
- **One logical dial holds one permit.** `resourceguard.WithHeldPermit` marks the
  context so a chain (outbound → detour → TLS) does not charge the same logical
  creation twice. The marker is broad: one permit can cover several handshakes in
  a chain. That is the intended trade — the alternative deadlocks (outer waits
  for inner, inner waits for outer).
- **The clock is part of the policy.** The fast-fail rate window classifies a
  failure by how long the *creation* took. Taking that timestamp before
  `Acquire` counted queue time as creation time, so a queued instant refusal
  looked slow and never armed the limiter. **Fixed in this stage** (see
  [ISSUES.md](ISSUES.md), F-04); the four seams now start the clock after the
  permit.
- **Queue refusal is local.** `IsLocalRejection` keeps a `ErrQueueFull` from
  arming the rate limiter or being classified as a node failure
  (`resourceguard/rate.go:143-150`).

## Reuse versus creation: the one seam that charges the wrong thing

AnyTLS takes the permit **before** asking `sing-anytls` whether an idle session
can be reused (`admission.go:59` before `dep/client.go:99`). The comment states
the reason: the seam cannot see the pool decision, and the expensive path is
behind it. The consequence is the one the stage-2 question predicts: under a
saturated gate, a stream that would create nothing can wait or be refused with
`ErrQueueFull`, and a healthy AnyTLS outbound's reuse queues behind another
outbound's stalled handshakes.

TrustTunnel shows the alternative: `AdmitCreation` runs once per shared creation,
waiters join the offer and are not charged, and a refusal is published to all
waiters without dialing (`transport/trusttunnel/client.go:601-625,631-671`).

A pool-aware fix needs the pool to answer "would this dial reuse?" before the
permit, which lives in the external `sing-anytls` module (no replace in
`go.mod`). The audit therefore records it as a design debt with a proposed patch
([ISSUES.md](ISSUES.md), F-06) rather than adding a fork of the module in this
stage.

## QUIC / H3 verdict

QUIC paths do **not** reach `handshakeWithAdmission`; they do their own TLS 1.3
handshake inside sing-quic:

| creation | code | admitted |
|---|---|---|
| MASQUE H3 UDP socket + QUIC handshake | `transport/http/client_h3.go:424,428` | no |
| MASQUE bootstrap race (per candidate) | `protocol/masque/bootstrap_race.go:445` | no |
| XHTTP H3 UDP + QUIC handshake | `transport/v2rayxhttp/http3.go:153-157` | no |
| TrustTunnel QUIC handshake | `transport/trusttunnel/client_quic.go:90-118` | inside the session permit |
| v2rayquic | `transport/v2rayquic/client.go:399` | no |
| gRPC over TLS | `transport/v2raygrpc/tls_credentials.go:37` | no |

Stage-2 decision: **do not add a generic QUIC admission now.** The candidates do
not share one resource class — a MASQUE session is a long-lived tunnel with one
CONNECT-IP stream, an XHTTP H3 connection is a pooled transport, a TrustTunnel
QUIC connection is one shared session with waiters — and a single budget over
"a QUIC connection" would charge a stream-carrying tunnel the same as a
per-request transport. What a future design would need, and why it is not this
stage's change, is recorded in [DECISIONS.md](DECISIONS.md), D-2.

What stage 2 does record is the *observable* asymmetry: with the guard saturated,
an H2 XHTTP creation is refused while an H3 creation proceeds. That is a real
coverage gap for a platform budget whose purpose is to bound creation storms; it
is listed as F-10 with the experiment that demonstrates it.
