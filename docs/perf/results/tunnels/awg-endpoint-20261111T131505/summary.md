# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T131505`
- kind: e2e
- started: 2026-11-11T13:15:05+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 11 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 42.20 | 4.97 | 4.88 | 47.17 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 58.74 | 0.45 | 58.29 | 64.28 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 61.79 | 0.25 | 61.54 | 67.96 | null | s/GiB |
| goodput | 1 | download | 1 | 1.78 | 0.00 | 1.78 | 1.78 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 1 | 111.95 | 0.00 | 111.95 | 111.95 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 1 | 116.13 | 0.00 | 116.13 | 116.13 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 211.4 | 5.009 | 42.2 | 64.28 | 67.96 | null | null | null | null | null |
| 1 | 1 | upload | 236.5 | 5.013 | 47.2 | 58.29 | 61.54 | null | null | null | null | null |
| 2 | 1 | upload | 26.8 | 5.500 | 4.9 | 58.74 | 61.79 | null | null | null | null | null |
| 0 | 1 | download | 9.8 | 5.500 | 1.8 | 111.95 | 116.13 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 13.270 | 92.9 | 18 | 16 | 14.030 | 100.5 | 17 | 15 |
| 1 | 13.460 | 97.5 | 19 | 16 | 14.210 | 125.4 | 17 | 15 |
| 2 | 1.540 | 99.0 | 19 | 16 | 1.620 | 125.4 | 17 | 15 |
| 0 | 1.070 | 80.5 | 16 | 16 | 1.110 | 84.2 | 18 | 16 |

## Failures

- **measure** (no-payload): no payload arrived for download at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for download at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=278188
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=277188
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=270403
