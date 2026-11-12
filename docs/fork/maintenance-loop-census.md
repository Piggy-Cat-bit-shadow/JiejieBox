# Maintenance-loop census (audit Q46)

This is the full-range census the audit asked for: every periodic maintenance loop reachable from the
product tree, with its owner, what wakes it, its cadence, how it exits, and whether it is kept,
merged, or excluded (test tooling).

The previous round listed the eleven loops it had touched. That subset was correct but not a census:
the audit's minimum is "list the loops this round touches and their actual owner/wake frequency/exit,
then choose a small number of net-benefit changes or give a data/contract-backed retention decision".
This file does that for the whole tree, and `scripts/ci/maintenance-loop-census.py --check` fails when
a ticker exists that is not listed here, so the census cannot silently fall behind the code.

No loop was merged into a single scheduler. The reason is the same for every retention row and is
stated once: these loops have different owners, different wake conditions (wall clock, pressure,
request/stream lifetime, pause state) and different exit signals. A single scheduler would move every
cadence's ownership into one place; the audit's own rule is that a merge needs evidence that it is
cheaper than the wakes it removes, and no such measurement exists. The one loop with a real
net-benefit change in the prior round (the provider eligibility loop) is event-driven now and has a
test showing twelve commits collapse to one pending event; it has no entry below because it no
longer owns a timer.

## Product loops (retention decisions)

| # | Site | Owner | Wakes because | Cadence | Exit | Decision |
|---|---|---|---|---|---|---|
| 1 | `adapter/provider/adapter.go:115` | provider adapter | provider health/expiry check | `interval` from provider options (default 10 min, floor 1 min) | `Close` stops the ticker and its goroutine | keep: provider state is its owner's fact, not a global cadence |
| 2 | `common/networkquality/networkquality.go:439` | network-quality prober | one probe per direction | `probeInterval()` (configured probe cadence) | runner context cancellation | keep: it is the only producer of the quality samples |
| 3 | `common/networkquality/networkquality.go:987` | network-quality sampler | sample window | `sampleInterval` | measurement context done | keep: different quantity from #2 (sampling vs probing) |
| 4 | `common/networkquality/networkquality.go:989` | network-quality stability | stability window | `stabilityInterval` | measurement context done | keep: stability is a moving-window summary, not a probe |
| 5 | `common/networkquality/networkquality.go:991` | network-quality progress | progress reporting | `progressInterval` | measurement context done | keep: request-scoped reporting |
| 6 | `common/suspendwatch/watcher.go:95` | suspend watcher | wall-clock gap detection after wake | `w.interval` | stop channel and `done` | keep: it is the mechanism that detects a suspend gap |
| 7 | `daemon/started_service.go:939` | daemon status stream | gRPC subscriber asked for updates | subscriber `Interval` (default 1 s) | stream end (`defer ticker.Stop`) | keep, request-scoped: it exists only while a client streams |
| 8 | `daemon/started_service.go:1449` | daemon connections stream | gRPC subscriber asked for connections | subscriber `Interval` (default 1 s) | stream end | keep, request-scoped |
| 9 | `daemon/started_service_tailscale_ssh.go:265` | Tailscale SSH pump | session output polling | 30 s | SSH session context | keep: Tailscale's API is poll-only here |
| 10 | `experimental/cachefile/dns_cache.go:99` | DNS cache file | cleanup sweep | configured cleanup `interval` | context cancellation | keep: cache expiry is storage-owned |
| 11 | `experimental/clashapi/api_meta.go:64` | Clash API meta stream | one streaming HTTP response | 1 s | request end | keep, request-scoped |
| 12 | `experimental/clashapi/connections.go:147` | Clash API connections stream | one streaming HTTP response | request `interval` | request end | keep, request-scoped |
| 13 | `experimental/clashapi/server.go:254` | Clash API traffic stream | one streaming HTTP response | 1 s | request end | keep, request-scoped |
| 14 | `internal/memmetrics/sampler.go:94` | memory sampler | continuous diagnostic sampling | `s.interval` | `Close`/done | keep: the OOM report's only continuous source |
| 15 | `protocol/group/urltest.go:1122` | URL-test group | group check schedule; may register with the pause gate | group `interval` | group close; pause registration stops the ticker | keep: pause-aware by construction; no second scheduler needed |
| 16 | `protocol/tailscale/ping.go:24` | Tailscale ping | one ping operation | 1 s while the operation runs | operation context | keep, operation-scoped |
| 17 | `provider/remote/remote.go:391` | remote provider | provider update schedule | `updateInterval` | provider close/workers | keep: config-driven, same owner as #1 |
| 18 | `service/api/web_bridge_websocket.go:97` | web bridge | WebSocket keepalive | `webSocketPingInterval` | connection context | keep, connection-scoped |
| 19 | `service/ssmapi/server.go:109` | SSM API | persist traffic counters | 1 min | service scope `Add` stops it and saves once | keep: disk durability cadence |
| 20 | `transport/trusttunnel/client.go:690` | TrustTunnel client | session idle teardown | `max(idleTimeout/4, 1 s)` | session context | keep: TRIM path, never dials |
| 21 | `transport/trusttunnel/service_udp.go:223` | TrustTunnel UDP service | association expiry sweep | `timeout/4`, clamped 10 ms..15 s | service `done` | keep: the only path returning an association slot |

## Recurring `AfterFunc` maintenance (self-rescheduling)

| Site | Owner | Wakes because | Cadence | Exit | Decision |
|---|---|---|---|---|---|
| `common/power/governor.go:639` | power governor | next policy boundary | policy deadline (`longest`) | close stops the timer | keep: one timer for all policy steps |
| `common/power/governor.go:765` | power governor | deep-idle transition | `DeepIdleAfter` | close stops the timer | keep: a distinct policy edge, not a poll |
| `route/conn_reclaim.go:930` | connection reclaimer | sweep with backoff | `sweepDelayLocked()` (bounded backoff) | manager close | keep: adaptive, already backs off |
| `service/oomkiller/timer.go:336` | OOM killer | pressure poll | `minInterval`, reschedules itself | service close | keep: the pressure fact's only source |

## Test/benchmark tooling (excluded from retention)

These are real tickers but not product maintenance loops. They are listed so the inventory is
complete, and the check script still requires their sites in this file.

| Site | Scope | Why excluded |
|---|---|---|
| `common/trafficsched/harness/main.go:323` | harness | benchmark driver progress print |
| `common/trafficsched/harness/main.go:437` | harness | benchmark workload pacing |
| `scripts/perf/internal/sampler/sampler.go:95` | tool | performance sampler process |
| `test/interop/client.go:195` | tool | interop driver poll |
| `test/interop/reference.go:151` | tool | reference-server poll |
| `test/interop/reference.go:189` | tool | reference-server poll |
| `test/xhttpbench/cmd/xnettool/udpshim.go:303` | tool | benchmark reporting |

## Re-check

```sh
python scripts/ci/maintenance-loop-census.py --check   # every product/harness ticker is listed
python scripts/ci/maintenance-loop-census.py --list    # the machine-readable inventory
```

The check is the anti-drift guard: adding a ticker without adding a row fails it. The line numbers
are part of the key, so moving a loop requires updating its row - that is intentional, because the
row's owner and exit statement must be re-read when the loop moves.
