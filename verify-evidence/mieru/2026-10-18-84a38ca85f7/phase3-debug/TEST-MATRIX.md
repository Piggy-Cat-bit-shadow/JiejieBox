# Mieru phase 3 — test matrix

Tree after code fix: `3f9303286e0` (the evidence/test-only commit follows it).
All commands were run from `C:\Deepseek\integration-release-next`.
Build tags for global work: `release/DEFAULT_BUILD_TAGS_OTHERS`.
`PASS` means the command was executed on this tree; `NOT_TESTED` and `BLOCKED` are not PASS.

| Area | What was exercised | Result | Evidence |
| --- | --- | --- | --- |
| Unit / protocol | `go test ./protocol/mieru ./option ./include -count=1` | PASS | 21.2 s; package output |
| Race | `CGO_ENABLED=1 go test -race ./protocol/mieru -count=1` | PASS | 90.1 s; includes concurrent UoT and lifecycle tests |
| Vet | `go vet ./protocol/mieru` | PASS | no output |
| Build | `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | PASS | full tree |
| Shared smoke | `go test -tags ... -timeout 20m . ./common/listener` | PASS | 5.9 s / 2.0 s |
| Config / schema | table tests: transport cases, MTU, mux, handshake, pattern, credentials, ports/ranges, duplicates | PASS | `config_test.go`; duplicate ranges documented as dedup, not rejected |
| TCP data | loopback TCP, FQDN destination, IPv6 `::1`, hashed password, multi-user, echo integrity | PASS | `e2e_test.go` |
| UDP over stream | 1B/64B/1400B-ish frames, truncation, oversized drain, bad suffix, sequence alignment, concurrent readers | PASS | `uot_test.go`; `-race` |
| UDP transport | UDP underlay loopback; TCP requests over UDP; UDP echo | PASS | `TestMieruUDPTransportLoopback`, phase-2 matrix |
| UDP underlay close | outbound socket registry, Close, generation reopen | PASS | BUG-003 tests |
| Auth | correct/wrong password, `no_wait` deferred failure, hashed server/client, multi-user | PASS | `TestMieruNoWaitWrongPasswordDoesNotReportSuccess`, `TestMieruHashedPasswordInterop`, `TestMieruMultiUser` |
| User hint | `user_hint_is_mandatory` with this client | PASS | `TestMieruUserHintMandatory` |
| Lifecycle | construct/close, close-before-start, double close, start twice, start after close, start on closing scope, failed start retry | PASS | `outbound_test.go`, `e2e_test.go` |
| Listener release | TCP, UDP, both, partial start, reload/rebind | PASS | BUG-001 tests |
| Active sessions | inbound Close with TCP and packet sessions; outbound Close with TCP and packet sessions; InterfaceUpdated with TCP and UDP generations | PASS | BUG-001/003 tests |
| Invalid request reply | unsupported command, invalid destination port | PASS | BUG-005 test |
| DNS / dual stack | server domain via fork DNS for TCP and UDP transport; fake resolver family filtering; IPv6 loopback | PASS | `TestMieruOutboundDomainServerUsesForkDNS`, `...UDPUsesForkDNS`, `TestMieruIPv6Loopback`, `TestResolverBridgeFamilyFiltering` |
| Interop | official `mieru.exe` 3.38.1 → this inbound: TCP + UDP PASS; this outbound → official `apis/server`: TCP + UDP PASS | PASS (rerun after fixes) | `interop-phase3/` logs |
| Real mita server | official `mita` 3.38.1 built from source, run in foreground on Windows; official client and target both reach it TCP+UDP | PASS | `mita/` |
| Performance vs real mita | 4-round official-vs-target matrix; target TCP download +14-15%, UDP echo +45%, first-byte P50 ~2x slower, UDP-transport upload 0.739x | PASS (negative results recorded) | `mita/comparison.csv` |
| Performance | 4-round A/B, same host/server, old vs new binary | PASS (no systematic regression; see below) | `performance/` |
| mbox / extended / Hiddify | no Windows build / no WSL / no Docker | BLOCKED | phase-2 `decisions.md` E6 |
| 3000 short connections | 6000 new SOCKS connections in two runs, 64-byte echo each; 0 failures; sockets/CPU/RSS settle after the SDK idle window (~4 min) | PASS (idle retention documented) | `soak/` |
| Long soak / loss / latency matrix | multi-hour trend, packet loss, jitter | NOT_TESTED | time and host; next round |
| TUN / iOS / Android device / Apple sleep | no device or TUN permission | NOT_TESTED | phase-2 limitations |
| Upstream SDK patching | not attempted; adapter fixes only | N/A | — |

## Performance A/B (old = phase 2 binary, new = phase 3 binary)

Same target, same official-SDK server, same host, 4 rounds, 4 TCP connections × 64 MiB,
2000 UDP datagrams, 500 latency requests. Values are medians; ratios are new/old.

| Scenario | TCP download | TCP upload | UDP echo | TCP p95 |
| --- | ---: | ---: | ---: | ---: |
| tcp-low-default | 1.010 | 0.983 | 0.946 | 0.514 vs 0.524 ms |
| tcp-high-nowait | 0.992 | 0.987 | 1.057 | 0.515 vs 0.514 ms |
| udp-low-default | 1.016 | 1.162 | 0.954 | 0.517 vs 0.514 ms |

- TCP cells are within ±2%; the UDP-echo cells are ±5% in opposite directions, which is the
  expected cost/noise of the read mutex plus a 4-round sample. No cell shows a stable
  regression; the read microbenchmark cost is +9 ns per datagram.
- First-byte p50 stays 0.52-0.54 ms; startup to SOCKS-ready stays 241-267 ms.
- The per-run mins/maxes and raw cells are in `performance/old|new/raw`.
- This is a loopback product comparison; it does not claim a WAN result.

## Real mita (supersedes the phase-2 "no Windows build" claim)

`mita` 3.38.1 builds from the official source and runs in foreground on Windows with
`MITA_INSECURE_UDS=1` (only for its Unix socket chown) and `C:\var\run\mita` for the socket
path. Official client and target both reach it TCP+UDP. Four-round target/official
throughput against real mita: TCP download 1.150 (low), 1.144 (high); TCP upload 1.020,
0.963; UDP-transport download 1.032; UDP echo 1.050 / 1.091 / 1.455; first-byte P50 1.97x
slower; UDP-transport upload 0.739x. Details in `mita/README.md` and `mita/comparison.csv`.

## Commands

```powershell
$env:PATH = 'C:\src\_toolchain\goroot\bin;' + $env:PATH
Set-Location C:\Deepseek\integration-release-next
go test ./protocol/mieru ./option ./include -count=1
CGO_ENABLED=1 go test -race ./protocol/mieru -count=1
go vet ./protocol/mieru
go build -tags (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim() ./...
go test -tags (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim() -timeout 20m . ./common/listener
```
