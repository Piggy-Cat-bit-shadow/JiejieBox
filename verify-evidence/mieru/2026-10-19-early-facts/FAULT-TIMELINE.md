# Fault timeline — where the earliest reliable fact appears

Measured 2026-10-19 on the host in README.md, with the real official Mieru SDK
(`github.com/enfein/mieru/v3 v3.38.1`) over loopback, by a throw-away test that timed one
`transportRuntime.dial` (one transport, no fallback) or one full `Outbound.DialContext`
(fallback enabled). The scratch test is deleted; every number below is from that run
(`TestScratchTimeline`, total 60.03 s, 9 scenarios).

Three different constants are involved and must not be merged into one "15 s":

| Constant | Value | Where | What it bounds |
|---|---|---|---|
| `constant.TCPConnectTimeout` | 5 s | `constant/timeout.go`; `common/dialer/default.go` | one address family's connect attempt inside the fork dialer |
| `constant.TCPTimeout` | 15 s | `protocol/masque` (`transport/masque/client.go:330`) and other callers | the CALLER's budget for a whole tunnel/dial operation |
| Mieru SDK response read | 10 s | `apis/internal/handshake.go:47` `common.SetReadTimeout(conn, 10*time.Second)` | the SOCKS5 response after the underlay is up |
| quic-go handshake bound | 10 s | `config.go:17` `2 × HandshakeIdleTimeout`, default 5 s (`internal/protocol/params.go:103`) | the QUIC handshake for HTTP/3 |

## Measured timeline

| Scenario | Elapsed | Class after this round | Earliest reliable fact, and where it was lost before |
|---|---|---|---|
| TCP refused (closed loopback port) | **1 ms** | `socket` (definite) | The kernel's refusal at the dialer. Before: the SDK wrapped it with `%v`, so only text remained. The SDK's own English prefix still identified a transport failure, but the OS message is localised on a non-English Windows, so the failure could never be marked *definite* and its stage was lost. Now: `serverDialer` records the typed errno and `classifySocketError` reads it with `common/dialer`'s WSA/POSIX sets. |
| TCP accept, then silence | **10.002 s** | `transport` | Nothing earlier exists: the connect succeeded, the underlay is up, and the server simply never sends the SOCKS5 response. The first fact is the SDK's 10 s read deadline. |
| UDP blackhole (no listener) | **10.004 s** | `transport` | Same: a connected UDP socket is created instantly; only the missing response after 10 s proves anything on this platform (no ICMP refusal arrives). |
| Wrong password, live server | **10.002 s** | `transport` | Same 10 s deadline. It is *not* distinguishable from a blackhole through the public API, which is why two silent handshakes must not pin the secondary (see the ledger). |
| Target refused (through the proxy) | not reproducible locally | — | The loopback test inbound fabricates the destination, so no SOCKS error code is produced. Prior real-mita evidence: restricted user, SOCKS code 2, 3 ms, no transport switch (`verify-evidence/mieru/2026-10-18-fallback/`). Unit coverage: `dialTargetFailure` → `Target/Target`, no switch. |
| Fallback, TCP refused → UDP live | **1 ms total** | primary definite, secondary success | The refusal is the earliest fact anywhere in this table; the whole primary+secondary decision finished in about the time one socket takes. |
| Fallback, UDP blackhole → TCP live | **10.002 s** | primary transport, secondary success | The floor is the SDK read deadline; the fallback adds a healthy secondary handshake on top (<1 ms loopback). |
| Fallback, TCP accept-silent → UDP live | **10.002 s** | primary transport, secondary success | Same floor. |
| Caller budget 500 ms, primary UDP blackhole | **10.002 s** (measured before the reporting change below; the wait is unchanged by it), error now carries the caller's reason | caller | The caller's deadline is NOT a cancellation point inside the SDK handshake. The classification is correct (caller, no preference change), but the return is 9.5 s late. The adapter now returns the SDK error joined with `ctx.Err()`, so `errors.Is(err, ctx.Err())` is true and a user cancel is no longer reported as a protocol timeout (`TestCallerDeadlineIsReportedWhenTheSDKAnswersLate`). See §Residual. |

## Where "15 s" came from

For **Mieru**, the total worst case for one transport is connect-budget + response-budget:
5 s (per family, fork dialer) + 10 s (SDK response) when a TCP SYN is blackholed and the connect
times out; on an accepted-but-silent server it is the 10 s response budget alone. Fallback doubles the
second term only when the primary failed in a class that tries the secondary.

For **MASQUE CONNECT-IP**, the caller's budget is `C.TCPTimeout` = 15 s and the H3 attempt had no
window of its own, so a silent QUIC blackhole consumed the whole 15 s (quic-go's own handshake bound
is 10 s, below it). That is the "十几秒" shape on the MASQUE side, and it is the one this round
shortened to 3 s, leaving 12 s for HTTP/2.

## Propagation path of the earliest facts

```
fork dialer (admission / connect / socket errno)
  └─ serverDialer.DialContext | ListenPacket        [records typed error]
      └─ dialObservation (per SDK dial, context-scoped)
          └─ classifyDialError                      [class + definite/soft]
              ├─ fallbackSelector.report / reportBothFailed     [preference, cooldown]
              ├─ wrapFailureFacts → adapter.FailureFacts        [Dial/Path only when proven]
              └─ dialWithFallback → secondary attempt           [same dial, once]
```

The information that was previously available but not propagated: the typed socket error (destroyed by
the SDK's `%v`), and the distinction between "the socket layer proved this endpoint unusable" and "the
handshake was silent". Both are now propagated without a new interface.

## Residual

- The 10 s SDK response read is the hard floor for a silent remote peer while the public API is used.
  `PostDialHandshake` takes no context and sets the read deadline itself; the caller's cancellation
  cannot shorten it. A per-dial goroutine bridge was considered and rejected for this round (it moves
  connection ownership past the point where the caller left; see ARCHITECTURE-DECISIONS.md §Rejected).
- The measured target-refusal row is prior evidence, not a fresh measurement (see above).
- No real remote blackhole, no mobile radio, no TUN: all loopback.
