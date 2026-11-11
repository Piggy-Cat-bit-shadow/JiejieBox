# naive-h2 - naive

- run_id: `naive-h2-20261111T145903`
- kind: e2e
- started: 2026-11-11T14:59:03+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 130.47 | 0.10 | 124.23 | 130.58 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 9.91 | 0.08 | 9.83 | 10.37 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 10.24 | 0.19 | 10.05 | 10.61 | null | s/GiB |
| goodput | 4 | upload | 3 | 238.15 | 1.91 | 235.13 | 240.06 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 9.39 | 0.03 | 9.36 | 9.46 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 9.29 | 0.03 | 9.25 | 9.53 | null | s/GiB |
| goodput | 1 | download | 3 | 234.64 | 0.02 | 226.68 | 234.66 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 6.19 | 0.02 | 6.17 | 6.45 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 5.18 | 0.02 | 5.17 | 5.36 | null | s/GiB |
| goodput | 4 | download | 3 | 371.63 | 4.19 | 367.45 | 379.19 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 6.09 | 0.07 | 6.00 | 6.17 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 4.34 | 0.00 | 4.24 | 4.34 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.74 | 0.01 | 3.72 | 3.79 | null | ms |
| latency_p95 | 1 | connect | 3 | 4.47 | 0.04 | 4.39 | 4.51 | null | ms |
| latency_p99 | 1 | connect | 3 | 4.82 | 0.10 | 4.72 | 5.46 | null | ms |
| latency_p50 | 4 | connect | 3 | 4.83 | 0.17 | 4.66 | 8.74 | null | ms |
| latency_p95 | 4 | connect | 3 | 8.14 | 1.86 | 5.60 | 10.00 | null | ms |
| latency_p99 | 4 | connect | 3 | 9.14 | 2.31 | 6.11 | 11.45 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 653.9 | 5.008 | 130.6 | 9.83 | 10.24 | null | null | null | null | null |
| 1 | 1 | upload | 621.4 | 5.002 | 124.2 | 10.37 | 10.61 | null | null | null | null | null |
| 2 | 1 | upload | 652.8 | 5.004 | 130.5 | 9.91 | 10.05 | null | null | null | null | null |
| 0 | 4 | upload | 1193.9 | 5.013 | 238.2 | 9.39 | 9.29 | null | null | null | null | null |
| 1 | 4 | upload | 1179.8 | 5.018 | 235.1 | 9.46 | 9.53 | null | null | null | null | null |
| 2 | 4 | upload | 1202.8 | 5.010 | 240.1 | 9.36 | 9.25 | null | null | null | null | null |
| 0 | 1 | download | 1173.4 | 5.000 | 234.7 | 6.17 | 5.17 | null | null | null | null | null |
| 1 | 1 | download | 1173.2 | 5.000 | 234.6 | 6.19 | 5.18 | null | null | null | null | null |
| 2 | 1 | download | 1133.5 | 5.000 | 226.7 | 6.45 | 5.36 | null | null | null | null | null |
| 0 | 4 | download | 1858.3 | 5.000 | 371.6 | 6.09 | 4.34 | null | null | null | null | null |
| 1 | 4 | download | 1896.9 | 5.003 | 379.2 | 6.00 | 4.24 | null | null | null | null | null |
| 2 | 4 | download | 1837.8 | 5.002 | 367.4 | 6.17 | 4.34 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.79 | 4.47 | 4.82 |
| 1 | 1 | connect | 0.0 | 5.005 | null | null | null | null | null | 3.74 | 4.51 | 5.46 |
| 2 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 3.72 | 4.39 | 4.72 |
| 0 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 4.66 | 5.60 | 6.11 |
| 1 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 4.83 | 8.14 | 9.14 |
| 2 | 4 | connect | 0.0 | 5.003 | null | null | null | null | null | 8.74 | 10.00 | 11.45 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 6.280 | 67.7 | 19 | 15 | 6.540 | 65.2 | 14 | 13 |
| 1 | 6.290 | 69.0 | 21 | 15 | 6.440 | 66.2 | 15 | 13 |
| 2 | 6.320 | 69.3 | 21 | 15 | 6.410 | 67.2 | 16 | 13 |
| 0 | 10.950 | 71.1 | 23 | 18 | 10.830 | 68.4 | 16 | 16 |
| 1 | 10.900 | 71.0 | 23 | 18 | 10.980 | 68.6 | 16 | 16 |
| 2 | 11.000 | 71.8 | 23 | 18 | 10.870 | 68.6 | 16 | 16 |
| 0 | 7.070 | 71.6 | 23 | 15 | 5.920 | 68.4 | 17 | 13 |
| 1 | 7.090 | 73.6 | 23 | 15 | 5.940 | 67.7 | 17 | 13 |
| 2 | 7.140 | 73.6 | 23 | 15 | 5.930 | 67.5 | 17 | 13 |
| 0 | 11.060 | 74.9 | 23 | 18 | 7.870 | 68.6 | 17 | 16 |
| 1 | 11.110 | 74.9 | 23 | 18 | 7.850 | 68.7 | 18 | 16 |
| 2 | 11.070 | 75.4 | 23 | 18 | 7.790 | 69.0 | 18 | 16 |
| 0 | 3.750 | 74.8 | 23 | 15 | 2.800 | 68.2 | 18 | 13 |
| 1 | 3.730 | 75.0 | 23 | 15 | 2.800 | 68.2 | 18 | 13 |
| 2 | 3.700 | 75.0 | 23 | 16 | 2.790 | 68.2 | 18 | 13 |
| 0 | 8.210 | 77.4 | 25 | 19 | 6.340 | 68.7 | 18 | 16 |
| 1 | 7.690 | 77.4 | 25 | 19 | 7.080 | 68.9 | 19 | 16 |
| 2 | 4.420 | 78.2 | 25 | 19 | 13.150 | 69.1 | 20 | 16 |
