# 3000 short-connection soak

Target client: `_mieru_phase3.exe`, local mixed inbound 11092 → Mieru low-mux TCP →
official-SDK server harness 18671. Each request is a new SOCKS5 connection with a 64-byte
echo (`mieru-bench-client -mode first-byte`). One process, sequential, no restart.

| Run | Requests | Success | Failures | P50 | P95 | P99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 3000 | 3000 | 0 | 1.018 ms | 1.061 ms | 1.534 ms |
| 2 | 3000 | 3000 | 0 | 1.023 ms | 1.070 ms | — |
| probe | 1 | 1 | 0 | 2.059 ms | — | — |

Raw client JSON: `soak.out`, `soak2.out`, `probe.out`.

## Resource behaviour

| Point | RSS | Handles | Established sockets to 18671 |
| --- | ---: | ---: | ---: |
| before run 1 | 31.55 MiB | 261 | 0 |
| right after run 1 | 68.50 MiB | 405 | 72 (after 45 s idle) |
| right after run 2 | 72.52 MiB | 436 | 106 |
| after ~4 min idle | 45.96 MiB | 329 | **1** |

The 72/106 intermediate state is **SDK idle-underlay retention**, not one socket per request:

- `Mux.cleanUnderlay` only closes an underlay when `SessionCount()==0` and the scheduler has
  been idle for `scheduleIdleTime = KeyRefreshInterval (2 min) + up to 61 s`.
- 6000 requests did not produce 6000 sockets; they produced a bounded pool that was reaped
  once the SDK idle window passed. After ~4 min only one established socket remained.
- `Outbound.Close`/`Mux.Close` closes the generation; the lifecycle tests cover the close
  path.

This is a **PASS for 3000 short connections** with the caveat that the SDK keeps idle
underlays for roughly 2-3 minutes after a burst. A multi-hour soak and a memory-profile
trend are still `NOT_TESTED`; they need a longer run than this round.
