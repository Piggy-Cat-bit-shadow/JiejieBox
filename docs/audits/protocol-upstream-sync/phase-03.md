# Phase 03A — Naive server / shared HTTP audit

| | |
|---|---|
| Phase | 03A (construction) |
| Date | 2026-11-02 |
| Branch | `testing` (default; no `main`) |
| Start base | `f15d4473` (clean, synced) |
| Code change | **none** — every listed upstream candidate is already covered in this tree, with source and tests below |
| Handoff | this file + `UPSTREAM_LEDGER.md` S-rows + `STATE.md` (`next=04`) |

## 1. Request path and where each candidate lands

| Stage | Local implementation | Existing pin |
|---|---|---|
| H1/H2/H3 ALPN | separate `tcpNextProtos()` / `quicNextProtos()` views, resolved before any listener starts (`inbound.go:165-205`) | `audit_alpn_isolation_test.go`, `TestAuditHTTP3ALPNConstantMatchesTheLibrary` |
| auth / CONNECT | parse → auth → tunnel; H1 hijack keeps buffered prologue bytes (`inbound.go:655-704`) | `auth_test.go`, `fuzz_authority_test.go` |
| H2/H3 tunnel | `naiveH2Conn` over `ResponseController.Flush`, failures propagate; routed **wrapped** for H2/H3 (`inbound.go:706-820`) | `inbound_conn_flush_test.go`, `bench_h2_test.go` |
| padding | frame-first budget, 0..255 range, `WriteZeroN`, frame counter after full write | `audit_write_contract_test.go`, `audit_padding_test.go`, `padding_test.go` |
| UoT v1/v2 | v2 Connect fixed destination; v1/non-Connect per-datagram destination; magic `.arpa` names | `test/jiejie` matrix (external module), unit codec tests |
| H3 availability | injected constructor, non-fatal when TCP exists, fatal when H3-only, failing stub | `inbound.go:207-234`, `quic/inbound_init.go` |
| rate / headers | accept-level limiter; `ReadHeaderTimeout` is request phase only; H2 windows validated | `server_limiter_test.go`, `http2_options_test.go` |
| close | `common.Close(n.listener, httpServer, h3Server, tlsConfig)`; scope cleanup | `TestOutbound...` counterparts, inbound close tests in `test/jiejie` |

## 2. Candidate verdicts

| Candidate | Verdict | Evidence |
|---|---|---|
| `forwardproxy` `e747854` authenticated-CONNECT idle (`read_body_idle`) | `ALREADY_COVERED` | JiejieBox is not Caddy. Header waiting is `net/http.Server.ReadHeaderTimeout` (`inbound.go:149-165`), which is request-phase only; H1 CONNECT is hijacked and H2/H3 CONNECT is a handler-held stream. No Caddy `read_body_idle` wrapper exists to break an authenticated tunnel. |
| `forwardproxy` `c096d6a` sing-quic BBRv1 for QUIC | `ALREADY_PRESENT` (opt-in) | `newNaiveCongestionControl` accepts `bbr` and builds `congestion_meta2.NewBbrSenderWithProfile(..., ProfileStandard)`; `quic_congestion_control` is plumbed and tested (`inbound_config_test.go`). Default remains the library's Cubic because the fork deliberately makes BBR an explicit production choice; changing the default is a product decision, not a missing patch. |
| `sing-box` `3c4a5a6ed` HTTP/2 wrapper | `ALREADY_COVERED` | H2/H3 path calls `RouteConnectionEx` with `v2rayhttp.NewHTTP2Wrapper(conn)`; the raw-conn branch is HTTP/1 only, where a wrapper would be wrong (`inbound.go:810-820`, `:701`). |
| `sing-box` `2f139af2d` inbound close | `ALREADY_COVERED` | `common.Close(n.listener, ...)` already passes the interface value, not `&n.listener`. |
| `sing-box` `d9b435fb6` padding bytes not zeroed | `ALREADY_COVERED` | both padding writers use `buffer.WriteZeroN(paddingSize)` (`inbound_conn.go:185`, `:256`); `audit_padding_test.go` covers the full 0..255 range. |
| `sing-box` `627912a78` QUIC ALPN race | `ALREADY_COVERED` (stronger) | ALPN is resolved into per-transport views before either listener starts; the H3 constructor refuses a TLS config without `h3` instead of mutating the shared config (`quic/inbound_init.go:189-192`). |
| `naiveproxy` long-tunnel / QUIC behaviour | `INAPPLICABLE` here | client/native; phase 02 covered the client side. |
| Half-close of the response direction | `INAPPLICABLE` | HTTP/2/3 expose no response-half-close API; the reference full-closes the stream the same way. The H1 path preserves `CloseWrite` through `hijackedConn` (`hijacked_conn_halfclose_test.go`). |

## 3. Deliberate divergences (not defects)

* **BBR default.** The reference's Caddy patch installs BBRv1 unconditionally. JiejieBox keeps the library default unless the operator names `bbr`; this is recorded in `quic/inbound_init.go` and its tests. No wire behaviour changes either way; the choice is visible in config.
* **H3 idle timeout.** `nativeNaiveQUICConfig` leaves `MaxIdleTimeout` to quic-go. Checked against
  Caddy v2.11.7 `NetworkAddress.ListenQUIC`: its `&quic.Config` sets `InitialPacketSize`, `Allow0RTT`
  and `Tracer` only, so the library default applies there too. The local setting is therefore
  reference-like, not a missing knob; no option overrides it. (`Allow0RTT` deliberately differs:
  local is false because an early-data CONNECT is replayable against an authenticating proxy.)
* **No Caddy.** The server remains `x/net/http2` + the fork's quic-go; the upstream Caddy patches are behaviour oracles only.

## 4. Verification actually run

| Command | Result |
|---|---|
| `go test -race -tags with_quic ./protocol/naive/` | `ok … 1.923s` |
| `go test -run '^$' -fuzz '^FuzzNaivePaddingFrame$' -fuzztime=20s ./protocol/naive/` | PASS, 477k execs |
| `go test -run '^$' -fuzz '^FuzzNaiveConnectAuthority$' -fuzztime=15s ./protocol/naive/` | PASS, 892k execs |
| `go test -tags "$(DEFAULT_BUILD_TAGS_WINDOWS)" ./protocol/naive/` (phase 02 client build) | `ok … 0.471s` |
| CI on `f15d4473` | Verify, Android Core ARM64, Mobile contract, Release contract: success |

No production file was modified in 03A. 03B then found one real rejection-path defect (§6) and fixed it.

## 5. Handoff to phase 04

* The server candidate table is closed; do not re-open `e747854`, `c096d6a`, `3c4a5a6ed`, `2f139af2d`, `d9b435fb6` or `627912a78` without a new upstream revision.
* `UPSTREAM_LEDGER.md` S-rows carry the exact paths; `STATE.md` points at 04.
* Phase 04 owns XHTTP correctness, starting with X-04 (`dialStreamUp` non-200 upload status).

## 6. 03B hostile review (2026-11-02)

Scope: the Naive inbound, the shared HTTP framing it uses, and the direct tests. No 04/05 code
was touched.

### 6.1 Attacks and existing coverage

| Attack | Evidence |
|---|---|
| padding 0/255, exact/over-budget payload, 8-bit pad, consecutive frames, short read/write, writer failure after header | `audit_padding_test.go`, `audit_write_contract_test.go`, `padding_test.go`, `padding_zero_frame_test.go`; 20 s padding fuzz and 15 s authority fuzz passed in 03A |
| H1 without negotiated padding passes bytes through | `TestAuditUnpaddedNeverInsertsFrames`, `TestPaddingIsOptional` |
| H2/H3 CONNECT response flush failure, stream close, half-close | `inbound_conn_flush_test.go`, `audit_flush_contract_test.go`, `hijacked_conn_halfclose_test.go`; H2/H3 have no response-half-close API (documented) |
| unauth headers, many short connections, global/per-IP bounds, IPv4-mapped sharing | `auth_test.go`, `server_limiter_test.go`, `http2_options_test.go` |
| authority fallback, `X-Forwarded-For`, forged `-connect-authority` | handler uses `URL.Host` then `Host`; source is `RemoteAddr` (`inbound.go:629-650`); comments record the removed forged-header path |
| build tags / ALPN | `audit_alpn_isolation_test.go`; TCP-only and `with_quic` builds pass |

### 6.2 The defect found and fixed

The H2/H3 CONNECT branch that rejects `:scheme`/`:path` called only `n.badRequest` (a log
function) and returned. `net/http` then sent its **implicit 200**, so a request the server meant to
reject looked accepted to the client while no tunnel existed. The reference
(`klzgrad/forwardproxy`) returns 400 before authentication.

Commit `e8ec5c67` writes `http.StatusBadRequest` before the log. New tests:
`TestAuditHTTP2ConnectPseudoHeadersAreRejected` (red 200, green 400, H2 and H3) and
`TestAuditHTTP2ConnectWithoutPseudoHeadersStillChallenges` (control: well-formed unauth CONNECT
still gets 407, not 400).

### 6.3 Verification

| Command | Result |
|---|---|
| new tests before the fix | `FAIL … status = 200, want 400` |
| `go test ./protocol/naive/ -count=1` | `ok … 0.373s` |
| `go test -race -tags with_quic ./protocol/naive/ -count=1` | `ok … 1.908s` |
| `go build ./protocol/naive/` / `-tags with_quic` | exit 0 |

Remaining limitation: this is an `httptest` handler test, not a real H2/H3 server/peer; real
interop stays `BLOCKED_BY_ENVIRONMENT`. No other defect was found; the other attacks map to the
existing tests above.
