# Change boundary and call-chain audit

## Change boundary table

| Class | Path | Why it is in this audit |
|---|---|---|
| Changed by round 1 (production) | `protocol/mieru/dial_observation.go` (new) | typed socket evidence per SDK dial |
| | `protocol/mieru/dialer.go` | bridge records socket results |
| | `protocol/mieru/fallback.go` | classes, facts, selector, aggregate |
| | `protocol/mieru/outbound.go` | fallback branch, aggregate consumer |
| | `protocol/mieru/inbound.go` | listener-first shutdown ordering |
| | `transport/http/tunnel_client.go` | CONNECT-IP H3 window |
| Changed by round 1 (tests/docs) | `protocol/mieru/{early_failure_test,fallback_test,e2e_test}.go`, `transport/http/client_h3_tunnel_window_test.go`, `docs/fork/cross-protocol-failure-facts.md`, `verify-evidence/mieru/2026-10-19-early-facts/**` | evidence for the above |
| Called, not changed | `common/dialer/{default,admission,family_capability,network_observation,windows_socket_errors}.go` | the dialer whose typed errors the bridge records; errno sets consumed |
| | `adapter/failure.go` | fact type and wrapper consumed |
| | `protocol/group/{loadbalance_failover,urltest}.go` | existing fact consumers |
| | `transport/http/{client,client_h3,client_h3_request}.go` | existing H3 machinery the tunnel window wraps |
| | `protocol/masque/client.go`, `transport/masque/client.go` | caller of `OpenTunnelWithInfo`; session lifecycle |
| | `constant/timeout.go` | `TCPConnectTimeout`/`TCPTimeout`, unchanged |
| Frozen | DNS/Fake-IP/rule compiler, sniff, client UI, installers, Android/Apple packaging, MASQUE server features, WireGuard, other protocols, CI/release | explicitly out of scope |

Round-2 changes stay inside the same set: `protocol/mieru/{fallback,outbound}.go` and
`protocol/mieru/debug_audit_test.go`, `protocol/group/loadbalance_failure_facts_test.go`,
`transport/http/client_h3_tunnel_window_test.go` (one more case).

## Call chains (who owns what)

### Mieru new session

```text
Outbound.DialContext
  └─ adapter.ExtendContext (metadata)
  └─ Outbound.dial                         read lock; closed/started gate; inFlight.begin()
       ├─ no fallback: primary.dial
       └─ dialWithFallback
            ├─ fallbackSelector.choose(generation)      mutex; generation gate; probe latch
            ├─ transportRuntime.dial(ctx, dest)
            │    ├─ ctx.Err() pre-check                  caller already gone -> no SDK call
            │    ├─ withDialObservation(ctx)             one context wrapper + ledger
            │    ├─ client.DialContext (official SDK)
            │    │    └─ mux.DialContext -> serverDialer.DialContext/ListenPacket
            │    │         └─ fork dialer (admission, family gate, socket, dual-stack)
            │    │              └─ observeSocketDial(error|success)
            │    ├─ (on error) close a conn the SDK returned with the error   [ownership]
            │    └─ classifyDialError(ctx, err, transport, ledger)
            ├─ fallbackSelector.report(generation, choice, probe, class)
            ├─ (transport class) secondary.dial once; report; reportBothFailed
            ├─ aggregate: E.Errors(primary, secondary)
            │    └─ aggregateFailureClass(primaryClass, secondaryClass)
            └─ wrapFailureFacts -> adapter.WrapFailure
```

Ownership: the selector owns preference/cooldown; the runtime owns its SDK client and packet
conns; the outbound owns the generation and the in-flight counter. `Close`/`InterfaceUpdated` take
the write lock, drain `inFlight` (5 s bound), then stop the generation. A late success is closed by
`acceptGeneratedConn` when the generation or the closed flag changed.

### MASQUE CONNECT-IP setup

```text
protocol/masque client connect()
  └─ transport/masque connect(): dialCtx = WithTimeout(c.ctx, C.TCPTimeout = 15s)
       └─ transport/http.Client.OpenTunnelWithInfo(ctx, protocol, path)
            └─ openTunnel
                 ├─ c.closed gate                        Close wins before any attempt
                 ├─ http3Available()                     broken-memory gate
                 ├─ beginHTTP3Attempt()                  sequence stamped BEFORE the attempt
                 ├─ probeCtx = WithTimeout(ctx, 3s)      tunnel-only window
                 ├─ http3.OpenTunnel(probeCtx, request)
                 │    └─ acquire -> UDP dial + QUIC handshake   (quic-go handshake bound 10s)
                 │    └─ openConnectStream -> CONNECT headers + response
                 ├─ caller ctx done -> return caller error, arm nothing, no H2
                 ├─ strict -> return (window named), arm nothing, no H2
                 ├─ !expired && !ErrHTTP3Unavailable -> return error, arm nothing (app refusal)
                 └─ markHTTP3Broken(attempt) -> H2 extended CONNECT -> H1
       └─ MASQUE session: 200 starts the long-lived tunnel; UpdateTunnelTransport reports the
          actual transport; Suspend/Close cancel the session context
```

Ownership: the HTTP client owns the H3 connection and the broken memory; the attempt sequence
orders outcomes; MASQUE owns the session and must never infer the tunnel transport from "an H3
connection object exists".

## Fact production → propagation → consumption

| Production point | Fact | Propagation | Consumers verified |
|---|---|---|---|
| `classifyDialError` target reply | Target/Target | `wrapFailureFacts` → `adapter.failureError` → outbound error | group retry=false, penalty=false, URLTest health preserved (tests) |
| definite socket errno (bridge ledger) | Dial/Path | same | group retry=true, penalty=true (non-timeout) (tests) |
| handshake timeout/reset | Handshake/Path | same | group retry=true, penalty=false while the error is a timeout |
| local DNS/permission/config/lifecycle | Dial/Local | same | group retry=false, penalty=false; URLTest neutral |
| caller ctx | Dial/Caller | same | group retry=false, penalty=false (cancellation checked first) |
| socket connect timeout/reset | none | error unchanged | group's conservative default; no fact invented |
| aggregate | precedence rule above | same | matrix test + group table |
| HTTP/MASQUE | none | — | no guessed facts; group sees the plain HTTP error |

`errors.Is/As` chain: `adapter.WrapFailure` keeps `Unwrap`; `E.Cause1(SDK, ctxErr)` uses
`Unwrap() []error`, so both the SDK error and the caller reason survive; `FailureFactsOf` uses
`errors.As` (nearest fact) and is tested through `E.Cause`, `errors.Join` and the aggregate.

## Uncovered-path map (what was NOT exercised this round)

```text
NetworkManager snapshot ──> Dialer admission ──> socket/underlay
        │                          │
        │                          ├── Mieru SDK handshake ──> FailureFacts ──> selector
        │                          └── HTTP3/QUIC CONNECT ──> H2 fallback ──> MASQUE session
        │
        └── no production change here; existing package tests were run

fallback/group consumers/URLTest health   — group tests run; no code change
real GFW / real cellular / TUN / iOS      — NOT TESTED (no environment)
client-side PacketUnderlay shutdown       — measured 75 iterations, no stall, no change
```

## Who may overwrite a previous result

| State | Writer | Guard |
|---|---|---|
| preference / cooldown | `fallbackSelector.report` | `closed`, `generation`, mutex; definite rule in `reportBothFailed` |
| probe latch | `choose` / `report` | same mutex; one probe per cooldown |
| H3 broken memory | `markHTTP3Broken` / `clearHTTP3Broken` | `claimHTTP3Outcome` (attempt order) + verdict mutex; `ResetConnections` supersedes in one critical section |
| tunnel transport fact | `openTunnel` return value | only the successful branch returns a transport |
| listener registry | `Inbound.Close` | `closeOnce`; registry refuses adds after `closeAll` |
| in-flight dials | `Outbound.Close` / `InterfaceUpdated` | write lock + drain; late results closed by `acceptGeneratedConn` |
