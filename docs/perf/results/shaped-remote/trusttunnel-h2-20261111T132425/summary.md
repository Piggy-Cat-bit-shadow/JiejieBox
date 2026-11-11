# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T132425`
- kind: e2e
- started: 2026-11-11T13:24:25+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 16 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=remote delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 0.27 | 0.05 | 0.22 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 24.83 | 2.44 | 22.07 | 27.27 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 34.09 | 0.98 | 33.11 | 41.38 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 1.00 | 0.03 | 0.87 | 1.03 | null | ratio |
| goodput | 4 | upload | 3 | 0.34 | 0.02 | 0.30 | 0.36 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 20.91 | 4.30 | 16.61 | 31.00 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 44.29 | 5.32 | 31.36 | 49.61 | null | s/GiB |
| wire_gbit_per_s | 4 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 4 | upload | 3 | 1.00 | 0.00 | 0.97 | 1.00 | null | ratio |
| goodput | 1 | download | 3 | 0.05 | 0.03 | 0.00 | 0.07 | null | MiB/s |
| wire_gbit_per_s | 1 | download | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 3 | 11.33 | 3.83 | 7.49 | 156.17 | null | ratio |
| goodput | 4 | download | 3 | 0.09 | 0.03 | 0.03 | 0.12 | null | MiB/s |
| wire_gbit_per_s | 4 | download | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 4 | download | 3 | 7.79 | 2.39 | 5.39 | 15.36 | null | ratio |
| goodput | 4 | udp | 1 | 0.04 | 0.00 | 0.04 | 0.04 | null | MiB/s |
| wire_gbit_per_s | 4 | udp | 1 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 4 | udp | 1 | 12.88 | 0.00 | 12.88 | 12.88 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1.9 | 5.501 | 0.3 | 22.07 | 33.11 | 0.00 | 0.87 | null | null | null |
| 1 | 1 | upload | 1.5 | 5.500 | 0.3 | 27.27 | 34.09 | 0.00 | 1.03 | null | null | null |
| 2 | 1 | upload | 1.2 | 5.501 | 0.2 | 24.83 | 41.38 | 0.00 | 1.00 | null | null | null |
| 0 | 4 | upload | 2.0 | 5.501 | 0.4 | 20.91 | 31.36 | 0.00 | 0.97 | null | null | null |
| 1 | 4 | upload | 1.7 | 5.501 | 0.3 | 31.00 | 49.61 | 0.00 | 1.00 | null | null | null |
| 2 | 4 | upload | 1.8 | 5.501 | 0.3 | 16.61 | 44.29 | 0.00 | 1.00 | null | null | null |
| 0 | 1 | download | 0.2 | 5.043 | 0.0 | null | null | 0.00 | 11.33 | null | null | null |
| 1 | 1 | download | 0.0 | 5.165 | 0.0 | null | null | 0.00 | 156.17 | null | null | null |
| 2 | 1 | download | 0.4 | 5.043 | 0.1 | null | null | 0.00 | 7.49 | null | null | null |
| 0 | 4 | download | 0.6 | 5.268 | 0.1 | null | null | 0.01 | 5.39 | null | null | null |
| 1 | 4 | download | 0.5 | 5.303 | 0.1 | null | null | 0.01 | 7.79 | null | null | null |
| 2 | 4 | download | 0.2 | 5.501 | 0.0 | null | null | 0.00 | 15.36 | null | null | null |
| 2 | 4 | udp | 0.2 | 5.000 | 0.0 | null | null | 0.00 | 12.88 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.040 | 59.5 | 14 | 13 | 0.060 | 60.6 | 13 | 15 |
| 1 | 0.040 | 59.6 | 14 | 13 | 0.050 | 60.8 | 13 | 16 |
| 2 | 0.030 | 59.8 | 14 | 13 | 0.050 | 60.8 | 13 | 16 |
| 0 | 0.040 | 60.3 | 15 | 16 | 0.060 | 61.1 | 13 | 19 |
| 1 | 0.050 | 61.1 | 15 | 20 | 0.080 | 62.1 | 13 | 22 |
| 2 | 0.030 | 61.6 | 15 | 23 | 0.080 | 62.4 | 13 | 26 |
| 0 | 0.050 | 62.0 | 15 | 24 | 0.060 | 62.8 | 13 | 26 |
| 1 | 0.060 | 62.4 | 16 | 24 | 0.060 | 63.3 | 14 | 26 |
| 2 | 0.050 | 62.8 | 16 | 24 | 0.080 | 63.6 | 14 | 26 |
| 0 | 0.060 | 63.5 | 16 | 27 | 0.080 | 63.7 | 14 | 29 |
| 1 | 0.070 | 63.6 | 16 | 27 | 0.070 | 64.0 | 14 | 29 |
| 2 | 0.050 | 63.8 | 16 | 27 | 0.050 | 64.3 | 14 | 29 |
| 2 | 0.110 | 64.4 | 16 | 15 | 0.110 | 65.3 | 14 | 18 |

## Failures

- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=456925
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=449202
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=469964
- **measure** (no-payload): no datagrams returned at 4 stream(s): sent=528662
- **measure** (no-payload): no datagrams returned at 4 stream(s): sent=552154
