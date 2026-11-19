# PAIRWISE B — Verification

Commands, results and limits. Raw output where it is short enough to be evidence; long runs are cited
by their summary line.

## 1. Environment

| Field | Value |
| --- | --- |
| Repository / branch | `Piggy-Cat-bit-shadow/JiejieBox` / `testing` |
| Window A's head (this window's base) | `d3b0e8190` |
| OS | Windows 10.0.19044, amd64 |
| Go | `go1.27.4 windows/amd64` (`toolchain` directive of `go.mod`); this is the toolchain the whole window is about |
| cgo | `CGO_ENABLED=1` with `CC=C:\Deepseek\.tools\mingw64\bin\gcc.exe` (gcc 16.2.0) for `-race` |
| Tags | none, except where noted (`with_trusttunnel` is needed only for the outbound registration) |

## 2. Suites executed

```
go test ./dns/...                                                        # all ok (dns 8.7s, transport 6.1s, fakeip 0.2s, quic 0.2s)
go test ./common/dialer                                                  # ok (22.2s) — the dual-stack suite
go test ./transport/...                                                  # one FAIL: transport/trusttunnel TestHTTP2Preface (B-1)
go test ./protocol/masque ./protocol/vless ./protocol/tun                # ok
go test ./e2e -count=1                                                   # ok (35.4s) — 58 tests; no workflow ran them AT THAT TIME.
                                                                         # Since mounted: a Linux run (Go 1.27.4, CGO off) passed 58/58,
                                                                         # and verify.yml now runs ./e2e — see the release readiness page §7.
go test ./transport/v2rayhttp ./transport/v2raygrpclite ./protocol/vless ./protocol/vmess ./protocol/trojan
                                                                          # ok after B-2 (vmess has no test files)
go test ./dns/transport/fakeip ./dns/...                                  # ok after B-3
go test ./protocol/tun                                                   # ok (4.5s) after B-4
go test ./route -run 'FakeIP|ARetiredPlaceholder|NoFakeIPHistory'        # ok; see the Windows flake in §6
go test ./transport/trusttunnel ./protocol/trusttunnel                    # ok (32.1s / 0.2s) after B-1
go test -run 'TestHTTP2Preface|TestAnIdleSessionSendsNoPing|TestTheSessionPrefersOneConnection' ./transport/trusttunnel
                                                                          # ok (1.3s) — the new push-gate step
go test -race ./transport/v2rayhttp ./transport/v2raygrpclite ./protocol/vless
                                                                          # ok (see §4)
go test -race ./transport/trusttunnel -run 'TestAnIdleSessionSendsNoPing|TestTheSessionPrefersOneConnection|TestHTTP2Preface'
                                                                          # ok (2.4s) — the new tests are race-clean
```

Per-package result of the first broad run (all `ok` unless noted): `dns`, `dns/transport`,
`dns/transport/dhcp`, `dns/transport/fakeip`, `dns/transport/group`, `dns/transport/hosts`,
`dns/transport/quic`, `common/dialer`, `transport/device`, `transport/http`, `transport/masque`,
`transport/trojan`, `transport/trusttunnel` (**FAIL**, B-1), `transport/v2raygrpc`,
`transport/v2raygrpclite`, `transport/v2rayhttp`, `transport/v2rayxhttp`, `transport/wireguard`,
`protocol/masque`, `protocol/vless`, `protocol/tun`; `transport/openconnect`, `transport/openvpn`,
`transport/simple-obfs`, `transport/sip003`, `transport/v2ray`, `transport/v2rayquic`,
`transport/v2raywebsocket` have no test files.

## 3. Tests added

| Test | Package | Pins |
| --- | --- | --- |
| `TestHTTP2Preface` (existing, was red) | `transport/trusttunnel` | B-1: the HTTP/2 preface is Chrome's, `MAX_FRAME_SIZE` included |
| `TestAnIdleSessionSendsNoPing` | `transport/trusttunnel` | an idle session sends nothing, with a positive control that proves the observer sees PINGs |
| `TestTheSessionPrefersOneConnection` | `transport/trusttunnel` | `StrictMaxConcurrentStreams` on, `ReadIdleTimeout` 0 |
| `TestResetKeepsThePoolReusableAcrossAGenerationChange` | `transport/v2rayhttp` | B-2: a reset keeps the pool reusable (observed at the server's accept counter) |
| `TestResetDoesNotCutAStreamThatIsAlreadyRunning` | `transport/v2rayhttp` | a reset drains rather than cuts |
| `TestADegenerateRangeIssuesNothingOutsideItself` | `dns/transport/fakeip` | B-3: `/31`, `/32` refuse instead of issuing out-of-range addresses |
| `TestEveryIssuedAddressIsInsideItsRange` | `dns/transport/fakeip` | every issued address is a host address of its own range |
| `TestARetiredPlaceholderIsNotBypassedByTheRouteSet` | `protocol/tun` | B-4: a placeholder from a retired generation reaches the router instead of being bypassed, with a control that an address past the cursor still bypasses |
| `TestARetiredPlaceholderIsRecognizedWithoutAFakeIPTransport` | `protocol/tun` | B-4/B-5: the durable record alone recognises a placeholder when no store exists |
| `TestARetiredPlaceholderIsRefusedWithoutAFakeIPStore` | `route` | B-5: the refusal works with no fakeip server configured, and stays proof-based |
| `TestNoFakeIPHistoryMeansNoRefusal` | `route` | B-5: a machine that never configured fake-IP refuses nothing |
| `TestAnEpochChangeDuringThePublishDecisionDiscardsTheConnection` | `transport/v2rayhttp` | R-B2: an epoch change inside the publish decision discards the connection and leaves the pool stale |
| `TestThePublishDecisionIsAPlainAcknowledgementWithoutANetworkChange` | `transport/v2rayhttp` | R-B2 guard-rail: a healthy publish still publishes and reuses |

## 4. Mutation controls

| Control | Mutation | Result |
| --- | --- | --- |
| MC-B1 | delete `MaxReadFrameSize: chromeMaxFrameSize` | `TestHTTP2Preface` FAILED with `[MAX_FRAME_SIZE = 1048576]`, i.e. the pre-fix toolchain default |
| MC-B2 | implement `Reset()` as `return c.Close()` | `TestResetKeepsThePoolReusable…` FAILED with `expected 2, actual 3` — one extra handshake for the second stream |
| MC-B3 | disable the `issuableAddress` guard | the range tests FAILED with `must refuse instead of issuing 198.18.0.9` (`/32`) and `198.18.0.2` (`/31`) |
| MC-B4 | restore the range-only expression in `JudgeFlow` | `TestARetiredPlaceholderIsNotBypassedByTheRouteSet` FAILED with `router preMatchCalls = 0` |
| MC-B5 | remove the durable fallback in `retiredFakeIPAddress` | `TestARetiredPlaceholderIsRefusedWithoutAFakeIPStore` FAILED (`Should be true`) |
| MC-B6 | restore compare-then-acknowledge (hook between the two) | `TestAnEpochChangeDuringThePublishDecisionDiscardsTheConnection` FAILED — the connection was published with the new epoch stamped on it |

The idle-PING test carries its own in-test control: a transport configured to ping must be seen to
ping (it is), and only then is the production transport's zero meaningful.

## 5. The package that proves the DNS/TUN pairs and is executed by nothing

`./e2e` holds 58 tests, including the fake-IP issuance/refusal rows that `route/route.go`'s comments
cite as the proof of its own guards (`TestFIPLiteralWithoutIssuanceStaysReachable`,
`TestFIPLiteralBeyondTheCursorStaysReachableAfterARangeMove`,
`TestFIPAddressFromThePreviousGenerationDoesNotEscape`).

- **No workflow mentions `e2e`.** `verify.yml` only compiles it (`go test -run '^$' ./...`), which
  proves it builds, not that it runs.
- Executed here for the first time in this window: `ok github.com/sagernet/sing-box/e2e 35.407s`.
- Not mounted by this window, deliberately: the only run that exists is on Windows, and adding a
  58-test integration package to the push gate on the strength of a Windows run could turn the gate
  red for an environment difference nobody here can debug. The recommendation is one Linux run, then
  a step in `verify.yml` next to the daemon gate — 35 s is affordable in a gate whose budget is
  dominated by `route` and the artifact jobs.

## 6. Limits — stated, not glossed

1. **B03 (auto_redirect × TUN/nftables) is `无法观测` here.** Windows host, no netns, no nftables, no
   privilege. Nothing about kernel rule installation or rollback is claimed.
2. **No device, no real network change.** Every generation claim is driven by
   `coordinator.Advance`, which is the network manager's own publication point, but interface
   enumeration, route rewrites and the 6 s listener budget around it were not exercised.
3. **`-race` on the whole `transport/trusttunnel` package does not complete here.** The package alone
   timed out after 600 s, and a verbose `-race` run with a 240 s cap named the test that hangs:
   `TestUDPBadFrameReleasesAssociations` (`service_udp_limit_test.go:91`, reached from `:412` via
   `service_test.go:243`/`:122`) — a UDP-association test, **not** one of this window's tests. The
   three new wire tests pass under `-race` in 2.4 s, and `transport/v2rayhttp`,
   `transport/v2raygrpclite` and `protocol/vless` pass under `-race`. Recorded, not fixed: whether the
   association release genuinely deadlocks under the race detector's timing or the test merely waits
   without a bound is exactly what the next round should settle; the package's own CI job
   (`linux-kernel-verify.yml`) runs it without `-race`, so nothing current would notice either way.
4. **No performance numbers.** The two transport changes remove work (no extra handshake per stream,
   no PING) rather than add it; no benchmark was run, and none is claimed.
5. **CI was not executed.** The new `verify.yml` step was validated by parsing the workflow and by
   running its exact command locally; a workflow edit is not evidence that a job ran.
6. **One `route` test flakes on Windows under full-suite load.**
   `TestAnUnreferencedRuleSetDropsItsRulesAtStartup` failed once with `TempDir RemoveAll cleanup: …
   The directory is not empty` — a Windows open-handle race in the rule-set artifact directory, not an
   assertion: the same test passes 3/3 in isolation. It is unrelated to this window's changes
   (rule-set artifacts, not fake-IP) and is recorded rather than worked around.
