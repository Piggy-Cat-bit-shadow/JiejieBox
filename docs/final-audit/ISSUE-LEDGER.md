# Final issue ledger

Every issue the three stages raised, with one status from the permitted set:

- `FIXED_AND_VERIFIED` — changed, with a regression test that fails without the change;
- `CONFIRMED_NOT_A_BUG` — checked against source/measurement; the behaviour is intended or a property of the transport;
- `MITIGATED` — the blocking condition is worked around, the underlying gap remains;
- `DEFERRED` — root cause understood, fix belongs to a dependency/product decision or a later change;
- `BLOCKED_BY_ENVIRONMENT` — cannot be executed on this host; no substitute claim is made;
- `UNRESOLVED` — no root cause yet.

| id | finding | severity | status | evidence / next step |
|---|---|---|---|---|
| B1 | XHTTP has no in-fork server transport | Medium | MITIGATED | reference Xray 26.9.30 server; cells labelled `reference_server`; in-fork transport is a product change |
| B2 | Cronet cgo link fails on this host | Low | MITIGATED | purego + `libcronet.so` path measured; cgo path untested here |
| B3 | Go heap and native memory cannot be split from outside the process | Medium | BLOCKED_BY_ENVIRONMENT | no pprof endpoint in the production binary; `go_heap_peak_bytes` null in every run; RSS/VmHWM only |
| B4 | No privileged network operations | Medium | BLOCKED_BY_ENVIRONMENT | no root; `system=true` TUN/WireGuard/MASQUE and host capture untested |
| B5 | Independent tooling absent (iperf3, perf, tcpdump, pidstat) | Low | BLOCKED_BY_ENVIRONMENT | lab's own load generator and `/proc` sampler; no hardware-counter cross-check |
| B6 | Platform cells not run | Medium | BLOCKED_BY_ENVIRONMENT | Apple/Android/Windows runtime cells need their own hosts |
| B7 | Userspace WireGuard/AWG stalls after the first concurrent flows | High | DEFERRED | root cause found: `Peer.WritePackets` blocks forever in `waitOutboundSpace` when `queuedOutboundPackets` reaches 1024 while encryption/sender queues are empty (wireguard-go fork accounting/flush); goroutine dump in [`evidence/b7-wireguard-wedge.txt`](evidence/b7-wireguard-wedge.txt). Fix is a wireguard-go fork change |
| B8 | UDP over the tunnel collapses at 1 % loss | Low | CONFIRMED_NOT_A_BUG | DATAGRAM has no retransmission; a reliable stream carrying datagrams collapses under loss (TCP-in-TCP). Recorded as a transport-guarantee property, not a defect |
| F-01 | Device pause can suspend an on-demand tunnel carrying traffic | High | DEFERRED | policy decision: suspend only when idle, or accept the teardown; test proposal in `docs/coupling/ISSUES.md` |
| F-03 | XHTTP pool opens a connection after `Close` | Medium-High | FIXED_AND_VERIFIED | `xmuxManager.closed` + `getContext` error; `xmux_close_boundary_test.go` |
| F-04 | Fast-fail rate limit measured queue time as creation time | Medium | FIXED_AND_VERIFIED | clock starts after `Acquire`; `handshake_rate_window_test.go` |
| F-05 | MASQUE endpoint has no `Close`; a duplicate-tag loser leaks its HTTP client; teardown runs through two closers | Medium | DEFERRED | `protocol/masque/client.go:332,337,355`; needs an endpoint lifecycle interface or an optional closer in the endpoint manager |
| F-06 | AnyTLS charges a creation permit for a pure reuse | Medium | DEFERRED | pool lives in `sing-anytls`; needs a fork/upstream API; TrustTunnel's shared offer is the reference |
| F-07 | Mieru single-transport dial published across a generation change | Medium | FIXED_AND_VERIFIED | `acceptGeneratedConn` on both paths; `generation_boundary_test.go` |
| F-07b | Mieru has no creation single-flight; SDK cleaner can race `AddSession` | Medium | DEFERRED | inside the SDK; needs a burst measurement before changing connection-count semantics |
| F-08 | REALITY fallback ran on a connection the caller had closed | Medium | FIXED_AND_VERIFIED | `realityFallbackError` handoff + bounded fallback; `reality_fallback_ownership_test.go` |
| F-10 | QUIC/H3 creations are outside the admission budget | Medium | DEFERRED | design prerequisites in `docs/coupling/DECISIONS.md` D-2; must not collapse tunnel and per-request transports |
| F-11 | `retireIdleResources` reports owners asked, not connections closed | Low | CONFIRMED_NOT_A_BUG | the contract cannot know closure without a completion signal; wording should say "asked" |
| F-12 | Reclaim returns before disposal; close errors dropped | Low | CONFIRMED_NOT_A_BUG | `CloseAll` waits; the async path is documented in `route/conn_reclaim.go` |
| F-13 | `Registration.Stale`/`Acknowledge` have no production caller | Low | DEFERRED | remove or wire together with F-15 |
| F-14 | Reference graph recomputed only on events | Low | DEFERRED | provider/dynamic-graph case; needs an event source |
| F-15 | Dial during the 6 s listener phase can reuse a pre-transition session | Low | DEFERRED | no protocol reads the epoch at pool hand-out; D-3 |
| F-16 | DNS/dual-stack bypasses in MASQUE/Mieru/Cronet/WireGuard | Low | CONFIRMED_NOT_A_BUG | forced by native/QUIC lifecycles; recorded as a consistency gap, not a defect |
| S3-1 | Laboratory recorded the superseded `require` pin for replaced fork modules | Medium | FIXED_AND_VERIFIED | `modulePins` now records the replace target (single-line, block, local); `source_test.go` |
| S3-2 | Mieru FD count grows across connect repetitions | Medium | DEFERRED | lab client peaks 113→198 (s=1) and 222→285 (s=4) over five 1 s reps, RSS steps once to ~217 MiB. In-process probes: 20 sequential dials add ≤16 FDs and stay flat for 40 s idle; 200-dial bursts add diminishing amounts (55→73→83) and do not reclaim in 10 s idle. In-process counts include the test inbound, so the decisive two-process idle experiment is still missing; evidence/mieru-idle-retention.txt |
| S3-3 | `go_heap_peak_bytes` is always null | Low | BLOCKED_BY_ENVIRONMENT | same as B3; RSS-only attribution |
| S3-4 | Goodput denominator includes dial/handshake time | Low | CONFIRMED_NOT_A_BUG | 5 s windows on loopback make it <1 %; shaped 80 ms cells carry ~0.2 s, so cross-RTT comparisons use the same definition |
| S3-5 | Three different "generations" (network epoch, pool generation, connection-manager generation) share a word | Low | DEFERRED | naming/documentation change; no behavioural defect found |
| S3-7 | Fast-fail rate state is process-wide, not per destination | Medium | DEFERRED | `Guard` holds one `rateState`; one failing server delays creations aimed at every server (see OVER-COUPLING.md) |
| S3-8 | TrustTunnel QUIC redo loop is unbounded in source | Low | CONFIRMED_NOT_A_BUG | the loop only continues when `QUICDialHook` returns `redo`; production leaves the hook nil, tests use it deliberately |
| S3-9 | Mieru idle-underlay recovery race: a new dial can pick the underlay the server just closed and fail with SOCKS EOF | Medium | UNRESOLVED | `go test -race -count=3 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru` fails 1/3 with `failed to read socks5 connection response from the server: EOF`; passes 3/3 without `-race`. The test's expectation (a new dial must build a new underlay) is correct; the pick-to-dial window is in the SDK, so the in-fork adapter cannot choose a fresh underlay |

## State note (not an issue)

The remote deleted the branch that hosted this work's base
(`integration/0.1.45-final`) while the audit ran, and `testing` advanced 15
commits. The branch merges that tip (HEAD `f30e36a4dea`) rather than resetting or
rewriting anything; the merged files do not overlap the files this branch
changed, and `go.mod`/`go.sum` are unchanged across the merge. This is context,
not a defect, so it is not counted in the ledger.
