# Fallback test matrix

Code SHA: `e1efe4cafdc`. All commands from
`C:\Deepseek\integration-release-next`; Go 1.26.8, Windows 10, loopback.

| Area | Coverage | Result | Evidence |
| --- | --- | --- | --- |
| Config/JSON | fallback absent/false; tcp鈫抲dp and udp鈫抰cp; server/port inheritance; explicit different endpoint; server_ports-only ambiguity; both/no_wait/same-transport rejection; JSON round-trip | PASS | `config_test.go`; `tests/fallback-tests.txt` |
| Schema/docs | `docs/schema.json` regenerated with `MieruFallbackOptions`; English + Chinese contract | PASS | `git diff` in code commit; `docs/configuration/outbound/mieru.md` |
| Selector state | primary healthy; target failure; transport cooldown; secondary preference; single-flight probe; probe target/success/cancel; ambiguous-probe backoff; generation reset; close | PASS | `fallback_test.go` |
| Error classification | target reply, DNS, permission/cancel, dial refused, write failure, timeout, reset, bare EOF, unknown | PASS | `TestClassifyDialError` |
| SDK connection ownership | `Client.DialContext` returns live conn + handshake error; runtime closes it | PASS | `TestTransportRuntimeClosesConnectionReturnedWithHandshakeError`; mutation `repro/bug1.txt` |
| Primary healthy | no secondary UDP socket, both clients running, no switch | PASS | `TestMieruFallbackPrimaryHealthyStaysPrimary` |
| TCP refused 鈫?UDP | fast fallback; second session secondary; cooldown; primary recovery; old secondary session alive; close | PASS | `TestMieruFallbackTCPRefusedUsesUDPAndRecovers` |
| UDP blackhole 鈫?TCP | 10 s SDK handshake timeout, then real handshake; no socket-success false positive | PASS | `TestMieruFallbackUDPBlackholeUsesTCP` |
| TCP accept/silent 鈫?UDP | TCP connect/session succeeds, handshake does not; fallback | PASS | `TestMieruFallbackTCPHandshakeBlackholeUsesUDP` |
| UDP Associate | fallback for `ListenPacket` in both directions (TCP鈫扷DP and UDP鈫扵CP) | PASS | `TestMieruFallbackUDPAssociateUsesSecondary`, `...UDPBlackholeUDPAssociateUsesTCP` |
| Target refusal | valid SOCKS error does not switch; real mita restricted user code 2 | PASS | `TestFallbackTargetRefusalDoesNotTrySecondary`; `interop/mita/target-restricted.log` |
| Wrong credentials | both transports silent; no success; durable preference returns to primary; one secondary attempt remains | PASS (limitation) | `TestMieruFallbackWrongPasswordDoesNotSwitch`; mutation `repro/bug2.txt` |
| Existing sessions | established primary survives fallback caused by new-connection proxy failure; established secondary survives primary recovery | PASS | `TestMieruFallbackDoesNotStopExistingPrimarySession`; TCP-refused test |
| InterfaceUpdated | both generations replaced, old sockets closed, selector reset, next dial fallback | PASS | `TestMieruFallbackInterfaceUpdateRebuildsBothGenerations` |
| Close | both clients/sockets stopped, active secondary session terminated, in-flight dial cannot report success, <7 s | PASS | `TestMieruFallbackCloseStopsBothRuntimes`, `...CloseWinsAgainstInFlightDial` |
| Concurrency | 50 concurrent new sessions during primary failure; one recovery probe per window | PASS | `...ConcurrentNewSessions`, `...ConcurrentRecoveryProbeSingleFlight` |
| Start rollback | primary started, secondary Start fails; both cleared/stopped; no ghost | PASS | `TestOutboundStartRollsBackBothRuntimesOnSecondaryFailure` |
| Disabled path | no selector, no secondary runtime; existing tcp/udp/both/no_wait tests | PASS | `TestMieruFallbackDisabledCreatesNoSecondaryRuntime` + full package |
| Unit/package | `go test ./protocol/mieru ./option ./include` | PASS, 81.7 s | `tests/fallback-tests.txt` |
| Race | `CGO_ENABLED=1 go test -race ./protocol/mieru` | PASS, 85.9 s | `tests/race.txt` |
| Build/vet | `go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...`, `go vet ./protocol/mieru ./option`, gofmt | PASS | command history |
| Real mita 3.38.1 | healthy; TCP-refused鈫扷DP; UDP-blackhole鈫扵CP; restricted target refusal | PASS | `interop/mita/` |
| Performance disabled | two 4-round old/new pairs; TCP 1.00-1.07x, upload 1.00-1.02x, UDP echo 0.92-0.95x | PASS (negative recorded) | `performance/disabled-*`, `disabled-combined.csv` |
| Performance enabled | healthy primary vs disabled; 4 rounds real mita | PASS | `performance/enabled-vs-disabled` |
| Idle/selector | +0.25 MiB, no extra thread/socket; 26.6 ns/op 0 allocs | PASS | `performance/idle`, `selector-benchmark.txt` |
| Fallback soak | 3000 short sessions via primary-refused→UDP; 0 failures; idle UDP endpoints settle 77→0 after 4 min | PASS | `performance/fallback-soak/` |
| Real network/mobile/TUN | no iOS/Android device, no TUN, no Wi-Fi↔cellular handoff, no multi-hour soak | NOT_TESTED | listed below |

## NOT_TESTED / BLOCKED

- iOS NetworkExtension sleep/wake and Android background behavior: no device/signing.
- Real Wi-Fi鈫攃ellular handoff: not reproducible on this desktop; `InterfaceUpdated`
  is covered with a deterministic call, not a hardware path change.
- TUN path: no privileged TUN runner.
- Multi-hour soak and packet-loss/latency matrix: only loopback and short
  concurrency; no long trend.
- Real GFW behavior: explicitly not claimed.
- mbox / extended / Hiddify: no Windows build/WSL/Docker (phase-2 limitation);
  real mita did run and is in this matrix.

## Commands

```powershell
$env:PATH = 'C:\src\_toolchain\goroot\bin;' + $env:PATH
Set-Location C:\Deepseek\integration-release-next
go test ./protocol/mieru ./option ./include -count=1
CGO_ENABLED=1 go test -race ./protocol/mieru -count=1
go vet ./protocol/mieru ./option
go build -tags (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim() ./...
```

