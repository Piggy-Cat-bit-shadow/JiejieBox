# vless-reality-vision - vless

- run_id: `vless-reality-vision-20261111T144328`
- kind: e2e
- started: 2026-11-11T14:43:28+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 261.93 | 3.36 | 258.57 | 268.23 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 4.09 | 0.05 | 3.95 | 4.13 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 2.87 | 0.00 | 2.75 | 2.87 | null | s/GiB |
| goodput | 4 | upload | 3 | 785.28 | 1.24 | 763.13 | 786.52 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 5.35 | 0.00 | 5.35 | 5.54 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 3.20 | 0.02 | 3.11 | 3.21 | null | s/GiB |
| goodput | 1 | download | 3 | 534.08 | 0.04 | 502.62 | 534.12 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 2.04 | 0.00 | 2.04 | 2.17 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1.07 | 0.00 | 1.07 | 1.09 | null | s/GiB |
| goodput | 4 | download | 3 | 1173.58 | 115.56 | 1058.01 | 1418.83 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 3.59 | 0.35 | 2.98 | 3.94 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 2.41 | 0.27 | 2.03 | 2.67 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 4.46 | 0.10 | 4.25 | 4.56 | null | ms |
| latency_p95 | 1 | connect | 3 | 5.79 | 0.24 | 5.23 | 6.03 | null | ms |
| latency_p99 | 1 | connect | 3 | 6.57 | 0.45 | 5.79 | 7.02 | null | ms |
| latency_p50 | 4 | connect | 3 | 6.44 | 0.27 | 6.15 | 6.70 | null | ms |
| latency_p95 | 4 | connect | 3 | 9.31 | 0.60 | 8.71 | 9.97 | null | ms |
| latency_p99 | 4 | connect | 3 | 12.00 | 1.05 | 10.95 | 13.56 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1310.2 | 5.002 | 261.9 | 4.09 | 2.87 | null | null | null | null | null |
| 1 | 1 | upload | 1293.2 | 5.001 | 258.6 | 4.13 | 2.87 | null | null | null | null | null |
| 2 | 1 | upload | 1342.2 | 5.004 | 268.2 | 3.95 | 2.75 | null | null | null | null | null |
| 0 | 4 | upload | 3937.0 | 5.006 | 786.5 | 5.35 | 3.11 | null | null | null | null | null |
| 1 | 4 | upload | 3818.2 | 5.003 | 763.1 | 5.54 | 3.21 | null | null | null | null | null |
| 2 | 4 | upload | 3930.9 | 5.006 | 785.3 | 5.35 | 3.20 | null | null | null | null | null |
| 0 | 1 | download | 2670.7 | 5.000 | 534.1 | 2.04 | 1.07 | null | null | null | null | null |
| 1 | 1 | download | 2670.9 | 5.001 | 534.1 | 2.04 | 1.09 | null | null | null | null | null |
| 2 | 1 | download | 2513.2 | 5.000 | 502.6 | 2.17 | 1.07 | null | null | null | null | null |
| 0 | 4 | download | 7094.3 | 5.000 | 1418.8 | 2.98 | 2.03 | null | null | null | null | null |
| 1 | 4 | download | 5291.1 | 5.001 | 1058.0 | 3.94 | 2.67 | null | null | null | null | null |
| 2 | 4 | download | 5868.2 | 5.000 | 1173.6 | 3.59 | 2.41 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 4.46 | 6.03 | 7.02 |
| 1 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 4.56 | 5.79 | 6.57 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 4.25 | 5.23 | 5.79 |
| 0 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 6.15 | 8.71 | 10.95 |
| 1 | 4 | connect | 0.0 | 5.003 | null | null | null | null | null | 6.70 | 9.31 | 13.56 |
| 2 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 6.44 | 9.97 | 12.00 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.230 | 61.5 | 14 | 11 | 3.670 | 62.5 | 15 | 11 |
| 1 | 5.220 | 62.7 | 14 | 11 | 3.620 | 62.0 | 15 | 11 |
| 2 | 5.180 | 63.0 | 15 | 11 | 3.610 | 62.0 | 16 | 11 |
| 0 | 20.580 | 66.2 | 16 | 17 | 11.940 | 64.9 | 17 | 17 |
| 1 | 20.640 | 66.4 | 17 | 17 | 11.980 | 64.6 | 18 | 17 |
| 2 | 20.540 | 66.2 | 17 | 17 | 12.270 | 65.3 | 18 | 17 |
| 0 | 5.330 | 66.2 | 17 | 11 | 2.800 | 64.9 | 18 | 11 |
| 1 | 5.320 | 65.3 | 18 | 11 | 2.840 | 64.3 | 18 | 11 |
| 2 | 5.330 | 65.3 | 18 | 11 | 2.630 | 64.3 | 18 | 11 |
| 0 | 20.650 | 67.2 | 18 | 17 | 14.090 | 65.6 | 18 | 17 |
| 1 | 20.360 | 66.6 | 18 | 17 | 13.810 | 66.1 | 18 | 17 |
| 2 | 20.560 | 67.6 | 18 | 17 | 13.790 | 66.4 | 18 | 17 |
| 0 | 3.910 | 66.0 | 18 | 11 | 4.050 | 65.0 | 18 | 11 |
| 1 | 3.910 | 66.0 | 18 | 11 | 4.070 | 65.1 | 18 | 11 |
| 2 | 3.900 | 66.4 | 18 | 11 | 4.030 | 65.5 | 18 | 11 |
| 0 | 11.620 | 68.9 | 19 | 17 | 11.310 | 67.2 | 18 | 19 |
| 1 | 11.570 | 69.2 | 19 | 18 | 11.280 | 68.3 | 19 | 19 |
| 2 | 10.940 | 69.7 | 19 | 18 | 11.800 | 68.4 | 19 | 17 |
