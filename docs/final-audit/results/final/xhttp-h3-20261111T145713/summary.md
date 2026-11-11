# xhttp-h3 - vless+xhttp

- run_id: `xhttp-h3-20261111T145713`
- kind: e2e
- started: 2026-11-11T14:57:13+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 32.77 | 0.08 | 32.69 | 32.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 24.48 | 0.50 | 23.97 | 25.94 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 15.90 | 0.31 | 15.24 | 16.21 | null | s/GiB |
| goodput | 4 | upload | 3 | 127.44 | 0.28 | 126.50 | 127.72 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 25.04 | 0.45 | 23.57 | 25.48 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 17.84 | 0.51 | 17.32 | 18.38 | null | s/GiB |
| goodput | 1 | download | 3 | 119.79 | 1.94 | 115.48 | 121.73 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 16.94 | 0.02 | 16.92 | 17.31 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 18.17 | 0.09 | 18.09 | 18.55 | null | s/GiB |
| goodput | 4 | download | 3 | 203.88 | 1.77 | 202.11 | 227.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 16.54 | 0.18 | 16.02 | 16.73 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 16.73 | 0.27 | 15.13 | 17.00 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 2.85 | 0.03 | 2.83 | 2.91 | null | ms |
| latency_p95 | 1 | connect | 3 | 3.55 | 0.07 | 3.47 | 3.81 | null | ms |
| latency_p99 | 1 | connect | 3 | 4.29 | 0.08 | 4.21 | 4.77 | null | ms |
| latency_p50 | 4 | connect | 3 | 7.52 | 0.01 | 3.20 | 7.53 | null | ms |
| latency_p95 | 4 | connect | 3 | 8.75 | 0.61 | 4.68 | 9.36 | null | ms |
| latency_p99 | 4 | connect | 3 | 10.98 | 0.55 | 6.46 | 11.53 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 164.8 | 5.030 | 32.8 | 24.48 | 15.90 | null | null | null | null | null |
| 1 | 1 | upload | 165.3 | 5.031 | 32.9 | 23.97 | 15.24 | null | null | null | null | null |
| 2 | 1 | upload | 164.2 | 5.023 | 32.7 | 25.94 | 16.21 | null | null | null | null | null |
| 0 | 4 | upload | 640.7 | 5.028 | 127.4 | 23.57 | 17.32 | null | null | null | null | null |
| 1 | 4 | upload | 638.9 | 5.002 | 127.7 | 25.48 | 17.84 | null | null | null | null | null |
| 2 | 4 | upload | 638.0 | 5.043 | 126.5 | 25.04 | 18.38 | null | null | null | null | null |
| 0 | 1 | download | 599.0 | 5.000 | 119.8 | 16.92 | 18.17 | null | null | null | null | null |
| 1 | 1 | download | 577.5 | 5.001 | 115.5 | 17.31 | 18.55 | null | null | null | null | null |
| 2 | 1 | download | 608.7 | 5.000 | 121.7 | 16.94 | 18.09 | null | null | null | null | null |
| 0 | 4 | download | 1019.6 | 5.001 | 203.9 | 16.54 | 16.73 | null | null | null | null | null |
| 1 | 4 | download | 1139.4 | 5.000 | 227.9 | 16.02 | 15.13 | null | null | null | null | null |
| 2 | 4 | download | 1010.8 | 5.001 | 202.1 | 16.73 | 17.00 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 2.85 | 3.55 | 4.21 |
| 1 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.83 | 3.47 | 4.29 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.91 | 3.81 | 4.77 |
| 0 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.20 | 4.68 | 6.46 |
| 1 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 7.52 | 8.75 | 10.98 |
| 2 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 7.53 | 9.36 | 11.53 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 3.940 | 87.4 | 19 | 14 | 2.560 | 49.8 | 17 | 10 |
| 1 | 3.870 | 91.5 | 19 | 14 | 2.460 | 49.2 | 17 | 10 |
| 2 | 4.160 | 91.5 | 20 | 14 | 2.600 | 49.7 | 17 | 10 |
| 0 | 14.750 | 107.7 | 20 | 19 | 10.840 | 65.5 | 18 | 16 |
| 1 | 15.900 | 107.7 | 21 | 19 | 11.130 | 67.3 | 19 | 17 |
| 2 | 15.600 | 108.5 | 21 | 20 | 11.450 | 67.6 | 19 | 19 |
| 0 | 9.900 | 107.7 | 21 | 13 | 10.630 | 67.6 | 19 | 12 |
| 1 | 9.760 | 107.7 | 21 | 13 | 10.460 | 67.6 | 19 | 8 |
| 2 | 10.070 | 107.7 | 21 | 13 | 10.750 | 67.6 | 19 | 8 |
| 0 | 16.470 | 107.7 | 21 | 16 | 16.660 | 67.6 | 19 | 11 |
| 1 | 17.830 | 107.7 | 21 | 16 | 16.840 | 67.6 | 19 | 11 |
| 2 | 16.510 | 107.7 | 21 | 16 | 16.780 | 67.6 | 19 | 11 |
| 0 | 6.800 | 107.7 | 21 | 13 | 6.770 | 73.9 | 21 | 352 |
| 1 | 6.780 | 107.7 | 21 | 13 | 6.840 | 80.8 | 21 | 354 |
| 2 | 6.770 | 107.7 | 21 | 13 | 6.860 | 86.8 | 21 | 350 |
| 0 | 17.150 | 107.7 | 21 | 17 | 15.360 | 168.3 | 21 | 1199 |
| 1 | 7.850 | 107.7 | 21 | 17 | 19.440 | 171.9 | 24 | 1142 |
| 2 | 7.870 | 107.7 | 21 | 17 | 19.390 | 170.5 | 24 | 547 |
