# PAIRWISE B — Effectiveness: did the mechanism really run?

Six layers per claim, as the window-B brief requires: build → load → wiring → decision → data →
cleanup, followed by the falsification controls. The two fixed findings are checked layer by layer;
the recorded findings are checked only for "the detector exists" (their proof is the reading in
`PAIRWISE_B_FINDINGS.md`).

---

## EC-B1 The TrustTunnel HTTP/2 session (B-1)

| Layer | Evidence |
| --- | --- |
| **Build** | `transport/trusttunnel` needs `with_trusttunnel` only for the outbound registration; the session and its pin compile untagged and run on a Windows host (`go test ./transport/trusttunnel`, 32.1 s). The `linux-kernel-verify` workflow builds it with the tag |
| **Load** | `newHTTP2Transport()` is the only session constructor; `connectHTTP2` calls it and immediately `NewClientConn` on the TLS conn (`client_http2.go:53-69`) |
| **Wiring** | The transport is built by `configureTransports` from `http.HTTP2Config`; the four Chrome values and now `MaxReadFrameSize` are in that literal (`client_http2.go:93-115`), which is the only place they exist |
| **Decision** | The parameter under test is in the client's first SETTINGS frame, i.e. the peer sees it before any request. `TestHTTP2Preface` reads it off a real TLS handshake and asserts the full ordered list plus the connection WINDOW_UPDATE |
| **Data** | Observed on the wire, not in a struct: `SETTINGS [... MAX_FRAME_SIZE = 16384 ...] WINDOW_UPDATE 15663105` after the fix; `1048576` before it |
| **Cleanup** | n/a (a preface has no lifetime); the session's own teardown is covered by the existing suite |

**Falsification control MC-B1** — the pre-fix condition reproduced by deleting the single
`MaxReadFrameSize` line: the pin reports `1048576` and fails. The control is the *same test binary*
against the same server, so the failure is attributable to the one line.

**Second member, same mechanism:** `TestAnIdleSessionSendsNoPing` — the "an idle session sends
nothing" contract that the constructor's comment states. The measurement is a frame reader on the
server side over 600 ms: 0 PING frames. **Positive control:** the same test builds a transport with
`ReadIdleTimeout = 50 ms` and requires at least one PING, so a zero cannot be an observer that cannot
see PINGs. This is the shape the window-B brief asks for ("至少一项隔离负控证明测试能够检出机制根本未执行") —
the observer is proven first, then the absence is measured.

---

## EC-B2 The pooled generation after a network change (B-2)

| Layer | Evidence |
| --- | --- |
| **Build** | `transport/v2rayhttp` (+ `v2raygrpclite`) are always built; no tag |
| **Load** | The pool is created in `NewClient` when the TLS config yields h2 (`client.go:113-116`); the registration is taken from the context's coordinator (`:118-120`) |
| **Wiring** | `protocol/vless/outbound.go:210-216` prefers `ResetContext`, then `Reset`, then `Close`. Before this window neither transport had either, so the wiring landed on `Close` — the bug. After it, the interface assertion matches `Reset` |
| **Decision** | `dialHTTP2` asks `generationStale()` at the hand-out and calls `retireGeneration()` when true; a successful response calls `acknowledgeGeneration()` |
| **Data** | Measured at the **server's** accept counter: 1 conn for the first demand, 1 more for the first demand after a reset, and **0 more** for the second demand after it. The same test asserts `generationStale()` is false afterwards — with the close-based reset it can never be |
| **Cleanup** | `TestResetDoesNotCutAStreamThatIsAlreadyRunning`: the reset retires the previous generation while the stream already using it finishes, and both halves read their bytes |

**Falsification control MC-B2** — `Reset()` implemented as `return c.Close()`: the reuse assertion
fails with `expected 2, actual 3`, i.e. the next stream opened the third connection. That is the
"one handshake per stream" shape, measured.

---

## EC-B3 The fake-IP placeholder guard (B-4, B-5)

| Layer | Evidence |
| --- | --- |
| **Build** | `protocol/tun` and `route` are always built; the guard has no tag |
| **Load** | `Inbound.fakeIPStore` is resolved once at `StartStateInitialize` from the DNS transport manager (`protocol/tun/inbound.go:585-598`); the retirement snapshot is resolved in the same block, from the store when there is one and from the cache file when there is not |
| **Wiring** | `JudgeFlow` asks `fakeIPPlaceholder` before the route-set bypasses (`:1248`); the router asks `retiredFakeIPAddress` inside `prepareMatchMetadata` (`route/route.go:1387-1414`), which now falls back to the cache file's record |
| **Decision** | Two booleans that decide between three outcomes: bypass to the platform, reach the router (and be refused there), or route normally. The tests assert **which component** produced the verdict (`router.preMatchCalls`) rather than only the verdict value, because a bypass and a refusal can look alike |
| **Data** | No socket is opened in these tests; the observable is whether the router was consulted. The refusal itself is the router's `E.New("refusing destination ...")`, whose text names the interval |
| **Cleanup** | n/a (per-flow decision); the snapshot is a value resolved at Start and dropped with the inbound |

**Falsification controls.** MC-B4 (restore the range-only expression in `JudgeFlow`) → red with
`preMatchCalls = 0`; MC-B5 (remove the durable fallback in `retiredFakeIPAddress`) → red with the
`Should be true` assertion. Both tests carry their own **positive controls**: an address in the same
range but past the issuance cursor must still bypass, and a real address must still cross. Without
those, a blanket block on every fake-IP-shaped address would pass.

## EC-B4 The recorded findings: does a detector exist?

A finding is only actionable if something can fail when it regresses. Status of each recorded item's
detector:

| Finding | Detector today | Verdict |
| --- | --- | --- |
| R-B1 double transition | `generation_gate_test.go` covers the *trusttunnel* shape only; nothing asserts idempotence for the other six owners | **detector missing** — a regression would be silent, which is why the fix needs its own test first |
| R-B2 non-atomic acknowledge | `AcknowledgeIfCurrent` exists and is used only by the gate; the four pools call `Acknowledge` | **detector missing** (needs the injected hook) |
| R-B3 silent demotion | internal state only (`http3_transport.go` verdict map), no log, no exported status | **detector cannot be written against the public surface yet** |
| R-B4 naive window not armed | `TestH3FallbackProbeExpiryIsAmbiguous` pins the *decision* (no arming) but measures no per-CONNECT cost | **detector pins the wrong half**: the decision is correct per branch, the cost is what is wrong |
| B-1b 1 MiB frame size elsewhere | none, and no contract claims a browser preface outside trusttunnel | **nothing to detect** — recorded so a later round decides deliberately |

---

## EC-B4 What this window did *not* prove, stated as such

- No device run: no iOS/macOS Network Extension, no Android root, no Linux netns. Every claim about
  auto_redirect (B03) is absent rather than assumed.
- No real network change: all generation work is driven by `coordinator.Advance`, which is exactly
  what the network manager does, but the Wi-Fi→cellular path around it (interface enumeration,
  route table rewrite, 6 s listener budget) was not exercised.
- No DNS latency/interleaving attack (B06). The fake-IP guard was read and its e2e rows are green,
  but the "old A/AAAA entry + family racing" interaction was not reproduced.
- The 58-test `e2e` package is green **on this host** and is executed by no workflow; mounting it is
  recommended with the caveat that only a Windows run exists (`PAIRWISE_B_VERIFICATION.md` §5).
