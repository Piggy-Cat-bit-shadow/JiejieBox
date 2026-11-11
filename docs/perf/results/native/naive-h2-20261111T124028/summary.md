# naive-h2 - naive

- run_id: `naive-h2-20261111T124028`
- kind: e2e
- started: 2026-11-11T12:40:28+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 10 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 119.36 | 2.59 | 115.91 | 121.95 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 10.79 | 0.26 | 10.53 | 11.15 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 10.93 | 0.01 | 10.91 | 11.31 | null | s/GiB |
| goodput | 4 | upload | 3 | 224.67 | 2.02 | 219.56 | 226.68 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 9.99 | 0.03 | 9.96 | 10.10 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 9.97 | 0.04 | 9.93 | 10.15 | null | s/GiB |
| goodput | 1 | download | 3 | 217.64 | 0.39 | 217.25 | 220.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 6.74 | 0.02 | 6.65 | 6.76 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 5.57 | 0.02 | 5.51 | 5.59 | null | s/GiB |
| goodput | 4 | download | 3 | 342.45 | 2.08 | 333.56 | 344.53 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 6.64 | 0.04 | 6.60 | 6.84 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 4.55 | 0.05 | 4.50 | 4.67 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 4.06 | 0.13 | 3.79 | 4.18 | null | ms |
| latency_p95 | 1 | connect | 3 | 5.00 | 0.20 | 4.61 | 5.20 | null | ms |
| latency_p99 | 1 | connect | 3 | 5.33 | 0.28 | 4.91 | 5.60 | null | ms |
| latency_p50 | 4 | connect | 3 | 4.89 | 0.17 | 4.72 | 9.88 | null | ms |
| latency_p95 | 4 | connect | 3 | 6.03 | 0.32 | 5.71 | 11.65 | null | ms |
| latency_p99 | 4 | connect | 3 | 9.32 | 3.20 | 6.12 | 14.40 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 610.0 | 5.002 | 122.0 | 10.53 | 10.91 | null | null | null | null | null |
| 1 | 1 | upload | 597.0 | 5.002 | 119.4 | 10.79 | 10.93 | null | null | null | null | null |
| 2 | 1 | upload | 580.5 | 5.008 | 115.9 | 11.15 | 11.31 | null | null | null | null | null |
| 0 | 4 | upload | 1126.7 | 5.015 | 224.7 | 9.99 | 9.97 | null | null | null | null | null |
| 1 | 4 | upload | 1136.2 | 5.012 | 226.7 | 9.96 | 9.93 | null | null | null | null | null |
| 2 | 4 | upload | 1101.2 | 5.015 | 219.6 | 10.10 | 10.15 | null | null | null | null | null |
| 0 | 1 | download | 1086.3 | 5.000 | 217.2 | 6.76 | 5.59 | null | null | null | null | null |
| 1 | 1 | download | 1101.5 | 5.000 | 220.3 | 6.65 | 5.51 | null | null | null | null | null |
| 2 | 1 | download | 1088.2 | 5.000 | 217.6 | 6.74 | 5.57 | null | null | null | null | null |
| 0 | 4 | download | 1712.4 | 5.000 | 342.5 | 6.64 | 4.55 | null | null | null | null | null |
| 1 | 4 | download | 1723.0 | 5.001 | 344.5 | 6.60 | 4.50 | null | null | null | null | null |
| 2 | 4 | download | 1668.2 | 5.001 | 333.6 | 6.84 | 4.67 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 4.18 | 5.20 | 5.60 |
| 1 | 1 | connect | 0.0 | 5.004 | null | null | null | null | null | 4.06 | 5.00 | 5.33 |
| 2 | 1 | connect | 0.0 | 5.004 | null | null | null | null | null | 3.79 | 4.61 | 4.91 |
| 0 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 4.72 | 5.71 | 6.12 |
| 1 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 4.89 | 6.03 | 9.32 |
| 2 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 9.88 | 11.65 | 14.40 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 6.270 | 67.7 | 19 | 16 | 6.500 | 65.6 | 15 | 14 |
| 1 | 6.290 | 68.6 | 20 | 16 | 6.370 | 66.4 | 15 | 14 |
| 2 | 6.320 | 68.9 | 22 | 16 | 6.410 | 66.1 | 15 | 14 |
| 0 | 10.990 | 70.8 | 22 | 19 | 10.970 | 70.0 | 17 | 17 |
| 1 | 11.050 | 71.7 | 22 | 19 | 11.020 | 68.7 | 17 | 17 |
| 2 | 10.860 | 71.7 | 23 | 19 | 10.920 | 69.3 | 17 | 17 |
| 0 | 7.170 | 72.2 | 23 | 16 | 5.930 | 68.6 | 17 | 14 |
| 1 | 7.150 | 71.7 | 23 | 16 | 5.930 | 68.6 | 17 | 14 |
| 2 | 7.160 | 71.7 | 23 | 16 | 5.920 | 68.6 | 17 | 14 |
| 0 | 11.100 | 72.5 | 23 | 19 | 7.610 | 68.6 | 18 | 17 |
| 1 | 11.110 | 72.4 | 25 | 19 | 7.580 | 69.1 | 18 | 17 |
| 2 | 11.150 | 72.3 | 25 | 19 | 7.600 | 68.7 | 18 | 17 |
| 0 | 3.860 | 72.5 | 25 | 17 | 2.900 | 68.6 | 18 | 14 |
| 1 | 3.800 | 72.6 | 25 | 17 | 2.910 | 68.6 | 18 | 14 |
| 2 | 3.730 | 72.9 | 25 | 16 | 2.840 | 68.6 | 18 | 14 |
| 0 | 8.250 | 74.8 | 25 | 20 | 6.470 | 68.8 | 18 | 17 |
| 1 | 8.180 | 75.2 | 26 | 20 | 6.740 | 69.2 | 18 | 17 |
| 2 | 4.060 | 75.3 | 26 | 22 | 13.750 | 69.0 | 18 | 17 |
