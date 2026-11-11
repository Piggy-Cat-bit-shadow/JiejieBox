# naive-h3 - naive

- run_id: `naive-h3-20261111T150052`
- kind: e2e
- started: 2026-11-11T15:00:52+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 58.51 | 3.89 | 53.23 | 62.40 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 19.41 | 1.39 | 18.02 | 21.34 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 20.36 | 1.15 | 19.20 | 22.69 | null | s/GiB |
| goodput | 4 | upload | 3 | 78.06 | 0.14 | 76.18 | 78.20 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 17.72 | 0.05 | 17.68 | 18.15 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 20.60 | 0.04 | 20.56 | 21.54 | null | s/GiB |
| goodput | 1 | download | 3 | 91.49 | 0.03 | 91.17 | 91.52 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 15.02 | 0.01 | 14.95 | 15.03 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 17.23 | 0.07 | 17.16 | 17.34 | null | s/GiB |
| goodput | 4 | download | 3 | 76.45 | 0.17 | 75.90 | 76.62 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 23.66 | 0.03 | 23.63 | 23.74 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 19.17 | 0.07 | 19.10 | 19.34 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.00 | 0.04 | 2.95 | 3.04 | null | ms |
| latency_p95 | 1 | connect | 3 | 3.67 | 0.11 | 3.55 | 3.79 | null | ms |
| latency_p99 | 1 | connect | 3 | 3.94 | 0.13 | 3.81 | 4.74 | null | ms |
| latency_p50 | 4 | connect | 3 | 6.80 | 0.60 | 4.02 | 7.39 | null | ms |
| latency_p95 | 4 | connect | 3 | 8.28 | 0.19 | 5.45 | 8.48 | null | ms |
| latency_p99 | 4 | connect | 3 | 8.94 | 0.15 | 6.88 | 9.10 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 266.3 | 5.003 | 53.2 | 21.34 | 22.69 | null | null | null | null | null |
| 1 | 1 | upload | 312.5 | 5.007 | 62.4 | 18.02 | 19.20 | null | null | null | null | null |
| 2 | 1 | upload | 292.8 | 5.003 | 58.5 | 19.41 | 20.36 | null | null | null | null | null |
| 0 | 4 | upload | 383.2 | 5.030 | 76.2 | 18.15 | 21.54 | null | null | null | null | null |
| 1 | 4 | upload | 393.9 | 5.046 | 78.1 | 17.68 | 20.56 | null | null | null | null | null |
| 2 | 4 | upload | 394.6 | 5.047 | 78.2 | 17.72 | 20.60 | null | null | null | null | null |
| 0 | 1 | download | 455.9 | 5.000 | 91.2 | 15.03 | 17.34 | null | null | null | null | null |
| 1 | 1 | download | 457.5 | 5.000 | 91.5 | 15.02 | 17.23 | null | null | null | null | null |
| 2 | 1 | download | 457.7 | 5.001 | 91.5 | 14.95 | 17.16 | null | null | null | null | null |
| 0 | 4 | download | 383.1 | 5.000 | 76.6 | 23.66 | 19.17 | null | null | null | null | null |
| 1 | 4 | download | 379.5 | 5.001 | 75.9 | 23.74 | 19.34 | null | null | null | null | null |
| 2 | 4 | download | 382.3 | 5.001 | 76.4 | 23.63 | 19.10 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.04 | 3.79 | 4.74 |
| 1 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.00 | 3.67 | 3.94 |
| 2 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 2.95 | 3.55 | 3.81 |
| 0 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 4.02 | 5.45 | 6.88 |
| 1 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 6.80 | 8.28 | 8.94 |
| 2 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 7.39 | 8.48 | 9.10 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.550 | 68.0 | 19 | 15 | 5.900 | 66.2 | 16 | 12 |
| 1 | 5.500 | 68.5 | 19 | 15 | 5.860 | 66.0 | 17 | 12 |
| 2 | 5.550 | 69.6 | 20 | 15 | 5.820 | 66.2 | 18 | 12 |
| 0 | 6.790 | 70.9 | 21 | 22 | 8.060 | 67.9 | 18 | 19 |
| 1 | 6.800 | 71.7 | 21 | 22 | 7.910 | 67.9 | 18 | 19 |
| 2 | 6.830 | 72.3 | 21 | 22 | 7.940 | 68.2 | 18 | 19 |
| 0 | 6.690 | 72.7 | 22 | 15 | 7.720 | 69.6 | 18 | 12 |
| 1 | 6.710 | 72.0 | 22 | 15 | 7.700 | 71.1 | 18 | 12 |
| 2 | 6.680 | 72.0 | 22 | 15 | 7.670 | 72.5 | 18 | 12 |
| 0 | 8.850 | 72.2 | 23 | 18 | 7.170 | 74.4 | 18 | 15 |
| 1 | 8.800 | 72.6 | 23 | 18 | 7.170 | 75.8 | 19 | 15 |
| 2 | 8.820 | 73.2 | 23 | 18 | 7.130 | 75.7 | 19 | 15 |
| 0 | 4.910 | 72.6 | 23 | 16 | 4.110 | 76.1 | 19 | 12 |
| 1 | 4.940 | 73.0 | 23 | 16 | 4.110 | 75.0 | 19 | 12 |
| 2 | 4.930 | 73.2 | 24 | 16 | 4.110 | 75.0 | 19 | 12 |
| 0 | 10.630 | 75.2 | 24 | 21 | 8.760 | 75.0 | 20 | 15 |
| 1 | 7.220 | 75.7 | 24 | 19 | 13.580 | 75.8 | 20 | 15 |
| 2 | 5.550 | 76.0 | 24 | 22 | 15.460 | 77.3 | 20 | 15 |
