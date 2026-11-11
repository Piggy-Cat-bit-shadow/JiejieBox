# naive-h3 - naive

- run_id: `naive-h3-20261111T124233`
- kind: e2e
- started: 2026-11-11T12:42:33+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 10 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 53.09 | 0.64 | 52.45 | 56.74 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 21.37 | 0.28 | 19.91 | 21.64 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 22.71 | 0.33 | 21.46 | 23.05 | null | s/GiB |
| goodput | 4 | upload | 3 | 67.31 | 0.45 | 66.86 | 69.51 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 20.45 | 0.19 | 19.70 | 20.64 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 24.01 | 0.28 | 23.73 | 24.40 | null | s/GiB |
| goodput | 1 | download | 3 | 79.41 | 1.36 | 78.05 | 88.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 17.28 | 0.46 | 15.35 | 17.74 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 19.83 | 0.63 | 17.54 | 20.47 | null | s/GiB |
| goodput | 4 | download | 3 | 66.81 | 1.16 | 65.65 | 73.53 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 27.34 | 0.45 | 24.48 | 27.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 22.10 | 0.08 | 19.74 | 22.18 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.50 | 0.04 | 3.46 | 3.57 | null | ms |
| latency_p95 | 1 | connect | 3 | 4.30 | 0.01 | 4.29 | 4.51 | null | ms |
| latency_p99 | 1 | connect | 3 | 4.76 | 0.08 | 4.68 | 5.08 | null | ms |
| latency_p50 | 4 | connect | 3 | 4.85 | 0.25 | 4.60 | 8.93 | null | ms |
| latency_p95 | 4 | connect | 3 | 9.93 | 1.63 | 6.00 | 11.55 | null | ms |
| latency_p99 | 4 | connect | 3 | 11.71 | 2.48 | 6.75 | 14.18 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 284.5 | 5.013 | 56.7 | 19.91 | 21.46 | null | null | null | null | null |
| 1 | 1 | upload | 263.0 | 5.016 | 52.4 | 21.64 | 23.05 | null | null | null | null | null |
| 2 | 1 | upload | 266.0 | 5.010 | 53.1 | 21.37 | 22.71 | null | null | null | null | null |
| 0 | 4 | upload | 337.4 | 5.046 | 66.9 | 20.64 | 24.40 | null | null | null | null | null |
| 1 | 4 | upload | 350.8 | 5.047 | 69.5 | 19.70 | 23.73 | null | null | null | null | null |
| 2 | 4 | upload | 339.1 | 5.038 | 67.3 | 20.45 | 24.01 | null | null | null | null | null |
| 0 | 1 | download | 390.3 | 5.000 | 78.1 | 17.74 | 20.47 | null | null | null | null | null |
| 1 | 1 | download | 397.1 | 5.000 | 79.4 | 17.28 | 19.83 | null | null | null | null | null |
| 2 | 1 | download | 444.3 | 5.000 | 88.9 | 15.35 | 17.54 | null | null | null | null | null |
| 0 | 4 | download | 367.7 | 5.001 | 73.5 | 24.48 | 19.74 | null | null | null | null | null |
| 1 | 4 | download | 328.3 | 5.001 | 65.7 | 27.79 | 22.18 | null | null | null | null | null |
| 2 | 4 | download | 334.1 | 5.000 | 66.8 | 27.34 | 22.10 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 3.46 | 4.29 | 4.68 |
| 1 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.57 | 4.51 | 5.08 |
| 2 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.50 | 4.30 | 4.76 |
| 0 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 4.60 | 6.00 | 6.75 |
| 1 | 4 | connect | 0.0 | 5.008 | null | null | null | null | null | 4.85 | 9.93 | 11.71 |
| 2 | 4 | connect | 0.0 | 5.028 | null | null | null | null | null | 8.93 | 11.55 | 14.18 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.530 | 68.5 | 19 | 16 | 5.960 | 67.2 | 16 | 13 |
| 1 | 5.560 | 69.3 | 19 | 16 | 5.920 | 66.6 | 16 | 13 |
| 2 | 5.550 | 69.5 | 20 | 16 | 5.900 | 66.8 | 17 | 13 |
| 0 | 6.800 | 70.9 | 21 | 23 | 8.040 | 69.0 | 17 | 20 |
| 1 | 6.750 | 71.6 | 21 | 23 | 8.130 | 69.2 | 18 | 20 |
| 2 | 6.770 | 72.5 | 22 | 22 | 7.950 | 69.4 | 18 | 19 |
| 0 | 6.760 | 73.7 | 22 | 16 | 7.800 | 70.1 | 18 | 13 |
| 1 | 6.700 | 73.3 | 22 | 16 | 7.690 | 71.8 | 18 | 13 |
| 2 | 6.660 | 73.2 | 22 | 16 | 7.610 | 72.8 | 18 | 13 |
| 0 | 8.790 | 73.2 | 22 | 19 | 7.090 | 74.8 | 18 | 16 |
| 1 | 8.910 | 73.3 | 22 | 19 | 7.110 | 75.0 | 18 | 16 |
| 2 | 8.920 | 73.2 | 23 | 19 | 7.210 | 75.9 | 18 | 16 |
| 0 | 4.960 | 73.2 | 24 | 17 | 4.120 | 76.6 | 18 | 13 |
| 1 | 4.940 | 73.5 | 24 | 17 | 4.160 | 75.7 | 19 | 13 |
| 2 | 4.950 | 73.8 | 24 | 17 | 4.120 | 75.7 | 20 | 13 |
| 0 | 10.650 | 75.9 | 25 | 22 | 8.900 | 75.7 | 20 | 16 |
| 1 | 9.400 | 76.0 | 25 | 22 | 10.580 | 75.8 | 20 | 16 |
| 2 | 5.290 | 76.3 | 25 | 20 | 15.730 | 77.4 | 21 | 16 |
