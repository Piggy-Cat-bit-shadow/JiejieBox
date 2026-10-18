# Performance decisions and data

All numbers are Windows 10 loopback, same host, same SDK 3.38.1. Raw cells are
under `performance/`; no WAN/GFW claim.

## Disabled old vs disabled new

Two independent 4-round pairs; old = phase-3 binary `f6daabb`, new = feature
binary `e1efe4c`. Same server harness, 4 TCP connections × 64 MiB, 2000 UDP
datagrams, 500 latency samples. `disabled-combined.csv` uses the mean of the two
pairs; individual pair summaries are retained.

| Scenario | TCP download | TCP upload | UDP echo |
| --- | ---: | ---: | ---: |
| tcp-low-default | 1.002 | 1.000 | 0.924 |
| tcp-high-nowait | 1.071 | 1.016 | 0.939 |
| udp-low-default | 1.002 | 1.128 | 0.950 |

There is no per-datagram change on the disabled path: one branch (`fallback ==
nil`), one runtime method and the same SDK `DialContext`. The UDP-echo cells are
0.92-0.95x in both pairs but the TCP direction moves the other way in the same
runs; this is recorded as run-to-run variance, not a claimed improvement or a
hidden regression. Startup and idle RSS are unchanged (241-321 ms, ~31.5 MiB).

## Enabled no-failure vs disabled

Real mita 3.38.1, both configs primary TCP + fallback UDP vs primary TCP only,
same mixed inbound, 4 rounds each, 4 TCP connections × 32 MiB, 2000 UDP datagrams,
500 first-byte requests. Raw: `performance/enabled-vs-disabled`.

| Mode | Disabled | Enabled | Ratio |
| --- | ---: | ---: | ---: |
| TCP download MiB/s | 377.08 | 386.08 | 1.024 |
| TCP upload MiB/s | 358.22 | 365.79 | 1.021 |
| UDP echo MiB/s | 2.23 | 2.35 | 1.053 |
| first-byte P50 ms | 1.034 | 1.034 | 1.000 |
| first-byte P95 ms | 1.570 | 1.581 | 1.007 |

This is the cost of an idle second SDK client and one selector call; it is
within run variance and does not add a per-packet path.

## Idle and selector cost

`performance/idle/metrics.tsv`: same binary, mixed inbound, no dials, 3 s after
start:

| Mode | RSS | Handles | Threads |
| --- | ---: | ---: | ---: |
| disabled | 31.36 MiB | 271 | 19 |
| enabled (both clients started) | 31.61 MiB | 265 | 18 |

No extra thread or socket was observed; the SDK Mux is control state until an
underlay is dialled. This makes lazy secondary start unnecessary for this
version.

`performance/selector-benchmark.txt`: `choose` + `report` is **26.6 ns/op,
0 B/op, 0 allocs/op** over three runs. It is a mutex-protected O(1) state change
once per new dial, not per packet.

## Failover and recovery

- TCP connection refused → UDP: dial returned in **1.5 ms** (E2E log).
- Primary recovery probe after cooldown: **0.5 ms** (E2E log).
- UDP blackhole → TCP and TCP accept-but-silent → UDP: **10.0 s** before
  fallback. This is the official SDK's fixed 10 s `PostDialHandshake` read
  timeout; the selector adds no second timeout. It is bounded, but it is the
  dominant failover latency for a silent primary and is explicitly not claimed
  as "instant".
- Cooldown: 30 s base; failed probe doubles to 60 s, 120 s, 240 s, capped at
  5 min. The first business dial in a new window is the only probe; concurrent
  requests stay on the secondary. No background timer.

## KEEP / REJECT / DEFER

- **KEEP** eager start of both SDK clients. Measured idle cost +0.25 MiB, no
  extra thread/socket; lazy start would add a second Start path inside
  `DialContext` and a lifecycle race for no measurable benefit.
- **KEEP** the O(1) selector with a local generation and single-flight probe.
  26.6 ns/dial is below noise; no global lock or worker.
- **KEEP** the disabled path unchanged; old clients do not enter the selector.
- **KEEP** 30 s base and capped exponential backoff. A fixed 30 s probe on a
  permanent outage would add a recurring 10 s tail.
- **REJECT** a background health-probe goroutine/timer. The product explicitly
  prefers business-driven recovery and no mobile wakeup source; the tests show
  the first request after cooldown is sufficient.
- **REJECT** a second DNS/power/epoch control plane. Both sides use the same
  fork dialer and DNS router; `InterfaceUpdated` is the network authority.
- **NO CHANGE** the official Mieru Mux/auth/crypto/underlay and wire format.
- **DEFER** reducing the 10 s silent-handshake wait. There is no public SDK API
  to shorten `PostDialHandshake`; a fork or custom session would be a protocol
  change, explicitly out of scope.
- **DEFER** auth-vs-blackhole classification. The SDK returns the same plain
  error; a typed error from upstream is the clean fix. One secondary attempt on
  wrong credentials remains a documented limitation.
- **NOT_TESTED** real mobile/TUN/long soak; see TEST-MATRIX.
