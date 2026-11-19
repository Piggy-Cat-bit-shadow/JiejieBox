# PAIRWISE B — Matrix

Pairs from the window-B list, plus what was actually executed. `已确认` = source + execution;
`已确认（源码）` = confirmed by reading with file:line, no runtime repro; `已排除` = checked, no
defect; `无法观测` = needs a platform this environment does not have; `未覆盖` = not attacked.

## Executed in this window

| Command | Result |
| --- | --- |
| `go test ./dns/... ./common/dialer ./transport/... ./protocol/masque ./protocol/vless ./protocol/tun` | all ok except the one failure below |
| `go test ./transport/trusttunnel` | **FAIL** at HEAD (`TestHTTP2Preface`) → fixed → ok (32.1 s) |
| `go test ./e2e -count=1` | ok (35.4 s) — 58 tests, executed by **no workflow** (see `PAIRWISE_B_VERIFICATION.md` §5) |
| `go test ./transport/v2rayhttp ./transport/v2raygrpclite ./protocol/vless ./protocol/vmess ./protocol/trojan` | ok, after B-2 |
| `go test -race ./transport/v2rayhttp ./transport/trusttunnel` | ok (see §4 of the verification page) |

## The pairs

### B01〔P0〕IPv4/IPv6 racing × context cancel/deadline

| | |
| --- | --- |
| Shared boundary | candidate set, the dial context, and the network epoch a dial was started in |
| Executed | `go test ./common/dialer` (22.2 s) — the package's own dual-stack suite, whose files cover the interleavings the list asks for: winner/loser cancellation, late results, epoch capture (`dual_stack_*.go`, ~45 test files) |
| Conclusion | `已排除` for this round: no new defect found, and the mechanism is exercised by an existing suite that runs on a push path. This window adds no racing test of its own — the area is already the most densely tested in the tree, and a redundant test would be counted as coverage it is not |
| Evidence | `PAIRWISE_B_VERIFICATION.md` §2 |

### B02〔P0〕DNS / Fake-IP × TUN capture and routing

| | |
| --- | --- |
| Shared boundary | the fake-IP mapping store and the TUN flow's metadata |
| Executed | `go test ./dns/...` (ok, incl. `dns/transport/fakeip`), `go test ./e2e` (ok, 58 tests), plus a source read of the restore/refusal branch |
| Conclusion | The chain is wired and its guards are proof-based: a missing record is an error, an address this instance minted is refused, and a retired range is refused only with issuance evidence (`route/route.go:1368-1414`), with positive controls that forbid a blanket range guard. Three defects fixed here: **B-3** (a degenerate `/31`/`/32` range issued addresses OUTSIDE itself), **B-4** (the TUN L0 guard asked only the current-range question, so a placeholder from a retired generation was bypassed to the platform before the router's refusal could run), **B-5** (with no fakeip server configured, the durable retirement record in the cache file had no reader at all). One recorded, not fixed: **R-B8** (a reload without `store_fakeip` restarts the cursor and can silently remap a client-held address to a different domain) |
| Evidence | `PAIRWISE_B_FINDINGS.md` B-3, B-4, B-5, R-B8; verification §5 |

### B03〔P1〕Linux auto_redirect × TUN / routes / nftables

`无法观测`. Windows host, no netns/nftables capability, and no privilege in this session. Nothing was
claimed. The static half (who owns the rules, whether install/rollback is paired) was **not**
attacked either, so this pair is `未覆盖` rather than "checked".

### B04〔P1〕H3/QUIC pool × network change / generation

| | |
| --- | --- |
| Shared boundary | the epoch and the pooled physical connections of four transports |
| Executed | the `transport/...` suites; **B-2 fixed and pinned**; two further defects recorded from reading |
| Conclusion | **B-2 `已确认` + fixed**: after one network change, vless/vmess/trojan over HTTP/2 or gRPC-lite paid a fresh TCP+TLS+H2 handshake per stream because the outbound's reset fell back to `Close`, which permanently marks the pool stale. Also **fixed (R-B2, 3 of 4 pool sites)**: the publish decision compared the epoch and then acknowledged it in two steps, so an `Advance` landing between them stamped the new generation on a connection built on the old network. Still recorded, not fixed: **R-B1** (only 1 of 7 owners routes the listener half through the generation gate, so the boundary runs twice and the second run kills the session the first one built) and the fourth pool site (`transport/v2rayquic`, whose acknowledgement is deliberately separated by its publish lock) |
| Evidence | `PAIRWISE_B_FINDINGS.md` B-2, R-B1, R-B2 |

### B05〔P1〕Protocol fallback × timeout / error class / ALPN

| | |
| --- | --- |
| Shared boundary | the error classification and the transport promise the operator configured |
| Executed | **B-1 fixed and pinned**; trusttunnel's eligibility classification and budgets were audited by reading; `common/naive`'s fallback window read |
| Conclusion | **B-1 `已确认` + fixed**: the Go 1.27 move changed TrustTunnel's HTTP/2 preface from Chrome's `MAX_FRAME_SIZE = 16384` to 1 MiB; the pin caught it, nothing ran it. `已排除`: trusttunnel excludes certificate/x509/hostname/unknown-CA and 407 from fallback, honours `disable_version_fallback` unconditionally, and bounds its budgets (`min(3 s, remaining)` + bounded retries). Recorded, not fixed: **R-B3** (the H3→H2→HTTP/1.1 demotion in `common/httpclient` is completely silent) and **R-B4** (naive's fallback window never arms when its own probe budget expires first) |
| Evidence | `PAIRWISE_B_FINDINGS.md` B-1, R-B3, R-B4 |

### B06〔P1〕DNS cache / negative cache × family racing

| | |
| --- | --- |
| Executed | `go test ./dns/...` (ok); the cache path read only far enough to confirm the fake-IP/cache ownership split |
| Conclusion | `未覆盖` as an attack. No claim is made about cache-driven family bias; a test would need a scripted DNS server plus the dialer's candidate planner, which this round did not build |

### B07〔P1〕Config hot reload × live connections

`未覆盖` as an attack. Reload identity is covered by the repository's own daemon gate
(`verify.yml` "daemon configuration recovery"), which was not modified or challenged here.

### B08〔P2〕Rule routing × outbound switch / strict route

`未覆盖`. The A window's `route` suite run (451 s, ok) exercises the rule walk, but no new attack was
designed for "selected node vs real socket".

### B09〔P1〕H2/H3 request lifecycle × XHTTP / naive / TrustTunnel

| | |
| --- | --- |
| Shared boundary | request/response body lifetime vs the pooled session |
| Executed | `transport/http`, `transport/masque`, `transport/trusttunnel`, `transport/v2rayxhttp`, `protocol/masque` suites (all ok); two new pins on the TrustTunnel session: no PING while idle (with a positive control) and one connection at the stream limit |
| Conclusion | `已确认` as verified contracts (R-B5): an idle session sends nothing, and the strictness setting keeps one connection. The H2 preface fix (B-1) is in this pair too. Body/stream cancellation across H2/H3 was not attacked beyond these |

### B10〔P1〕MASQUE dual-stack / NAT64 / PREF64 × DNS/QUIC

| | |
| --- | --- |
| Executed | `go test ./protocol/masque ./transport/masque` (ok, 3.2 s + 8.1 s) |
| Conclusion | `已排除` for this round at the suite level; the PREF64/AAAA-only/DNS-bootstrap interleavings were not attacked. The silent H3→H2 demotion that MASQUE consumes is R-B3 |

### B11〔P2〕UDP session × TUN route change / reclaim

`未覆盖` here by design: the TUN UDP path was the A window's A05, where four findings were recorded
(`docs/audits/pairwise-a-resource-lifecycle-2026-11-19/PAIRWISE_A_FINDINGS.md` R-4…R-7). This window
adds nothing to avoid duplicating that work.

### B12〔free〕The toolchain as a participant in the wire format

| | |
| --- | --- |
| Shared boundary | the standard library's defaults and this repository's wire fingerprints |
| Executed | B-1 (HTTP/2 preface), the toolchain census in `scripts/ci/clienthello-probe`, the new idle-PING pin |
| Conclusion | This is the highest-value *new* pair this window found, and it is not on the list: a toolchain move changes wire-visible behaviour with no path filter matching, and the repository's toolchain story ("the toolchain directive is the toolchain that runs") covered only *which* toolchain runs, not what its defaults emit. Two of the three members found are fixed/gated; one (`common/httpclient`'s 1 MiB frame size, xhttp's preface) is recorded with no contract to break |
