# Early-failure signal matrix

The purpose of this table is to keep every layer inside the evidence it actually owns. It is the
acceptance rule for this round: a layer may only *refuse* or *switch* on what it can prove, and
"Unknown" stays Unknown.

| Layer | What it can prove | What it cannot prove | This round |
|---|---|---|---|
| `NetworkManager` / epoch / family capability (`common/dialer/network_observation.go`, `family_capability.go`) | One consistent snapshot: network generation, stable state, local IPv4/IPv6 capability. That a destination family is hard-undialable **on the current network**. | Anything about a remote Mieru/MASQUE endpoint; that a family will stay unavailable (the next generation may differ). | Reused, not changed. Snapshots stay single-read; no three-getter split. |
| Admission / strategy (`common/dialer/admission.go`, `default.go`) | That a candidate address is excluded by the operator's family strategy or by local capability, **before a socket is created**. | That the proxy endpoint is reachable; nothing about UDP vs TCP preference. | Reached for free: Mieru sets `BypassDialerDNS=true`, so the fork dialer resolves and admits the *actual* address, and a refusal returns in microseconds instead of waiting for the SDK. |
| Fork dialer socket (`common/dialer/default.go`, `windows_socket_errors.go`) | Connect refusal; kernel unreachable / network down; local interface failures (`EADDRNOTAVAIL`, `ENETDOWN`); connect timeout. Both POSIX and raw Winsock spellings. | That a connected UDP socket can reach the peer; that a timeout means the endpoint is down. | Its typed errors are now recorded by the Mieru bridge instead of being lost. |
| Mieru SDK underlay (`pkg/protocol/mux.go`, `underlay_*.go`) | That the underlay/session could not be created. That the SOCKS5 response did not arrive within 10 s. | Which of auth failure / server close / dead underlay caused a silent handshake. | Classified through the bridge; the 10 s floor remains (public API has no cancellation point). |
| `adapter.FailureFacts` | The stage and origin of **one attempt**, when a producer can prove it. | Health, reachability, or the future. Unknown is not a verdict. | New producer rows: definite socket → Dial/Path; socket timeout → no fact; local interface error → Dial/Caller. |
| Mieru `fallbackSelector` | Which transport this outbound prefers for *new sessions*, per generation, with cooldown/probe. | That the remote endpoint is globally down; anything about other outbounds. | Only a **definite** socket failure on both transports keeps the secondary preference; silent handshakes still reset. |
| `transport/http` H3/H2 (`client.go`, `client_h3.go`, `tunnel_client.go`) | That the HTTP/3 attempt did not produce a usable connection within its window; that the server refused the CONNECT with a status. | That UDP works in general; that a status error means the transport is broken. | CONNECT-IP setup now has its own window; `ErrHTTP3Unavailable` (not every error) drives version fallback; status errors never do. |
| MASQUE session (`protocol/masque`) | That a CONNECT-IP session exists, the transport it was established over, capsules/addresses/routes. | That a fresh H3 attempt would work; that the path to the proxy is healthy. | Unchanged; it now actually receives the HTTP/2 tunnel in the blackhole case instead of the caller's timeout. |

## Scenario coverage: proven early vs only after a timeout

| Requested scenario | Earliest proof | Measured | Verdict |
|---|---|---|---|
| Mieru TCP normal | — (success) | — | Fallback never engaged; no extra work. |
| Mieru TCP immediate RST / refused | kernel refusal at connect | 1 ms | **Early**; now definite → Dial/Path. |
| Mieru TCP SYN blackhole | kernel connect timeout (5 s per family, fork dialer) | ≤5 s | **Early-ish** (bounded by `TCPConnectTimeout`), now classified `socket-timeout` and can fall back; fact-less on purpose. |
| Mieru TCP accept-silent | SDK 10 s response read | 10.002 s | **Only after timeout**; nothing earlier is evidence. |
| Mieru wrong password | SDK 10 s response read | 10.002 s | **Only after timeout**; indistinguishable from blackhole. |
| Mieru target SOCKS refusal | SOCKS reply | prior: 3 ms | **Early**, and never a transport verdict. |
| Mieru UDP normal | — | — | no extra work. |
| Mieru local no-UDP-route / family refusal | Admission / kernel before any packet | unit-tested | **Early**, local origin, no preference change, no global penalty. |
| Mieru IPv6 hard unreachable | family capability snapshot / kernel unreachable | unit-tested | **Early**; `socket`; fact says Dial/Path only when the kernel says unreachable. |
| Mieru UDP remote silent blackhole | SDK 10 s read | 10.004 s | **Only after timeout**; honest limit. |
| Mieru UDP underlay stopped | SDK error after close | unit/lifecycle tests | Not a health signal; `Close` wins. |
| Mieru both transports fail | two attempt results | tests | Definite+definite keeps secondary; anything else resets to primary. |
| MASQUE H3 normal | — | 3.4 ms setup (loopback) | no extra work. |
| MASQUE UDP system unreachable | fork dialer / quic-go error | unit | H3 attempt fails fast, H2 is attempted (window not reached). |
| MASQUE H3 silent blackhole | per-attempt window (3 s) | before: >3 s blocked, H2 unreachable | **Fix**: 3 s to the H2 attempt, inside the 15 s caller budget. |
| MASQUE TLS failure | TLS handshake error | unit | Not an H3-unavailable verdict; returned as-is. |
| MASQUE CONNECT-IP 4xx / unsupported | HTTP status from a working H3 connection | test | Not a version verdict; H2 is not attempted. |
| MASQUE H3→H2 success | tunnel transport recorded | tests | `TunnelTransportHTTP2` reported as before. |
| MASQUE H2 fails too | H2 error | unchanged | Caller sees both; no invented health state. |
| Old-generation request interrupted by network switch | epoch/generation guards | existing tests | Stale results are dropped; `Close` wins. |
| Device pause / idle | power governor / SDK | not tested on device | No new background work was added, so nothing new to pause. |
| Caller cancel | caller context | 10.002 s with a 500 ms budget | Classification correct; **return is late** (SDK has no cancellation point). See residual. |

## The 80/20 rule, stated honestly

The first layer is allowed to remove only what it can prove: a family with no local path, an operator
strategy exclusion, a kernel refusal, a kernel unreachable. That is cheap (no packet, no timer) and has
a near-zero false-positive rate, but it is **not** a measured 80 % detection rate. The remaining
silent-blackhole cases are explicitly left to the deeper layer (the SDK's 10 s response read), because
no local observation can distinguish "the remote is dropping UDP" from "the remote is slow" or "the
credentials are wrong".
