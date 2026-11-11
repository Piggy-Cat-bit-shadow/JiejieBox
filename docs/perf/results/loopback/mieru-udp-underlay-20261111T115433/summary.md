# mieru-udp-underlay - mieru

- run_id: `mieru-udp-underlay-20261111T115433`
- kind: e2e
- started: 2026-11-11T11:54:33+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 28.96 | 0.18 | 28.78 | 30.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 53.62 | 0.32 | 49.67 | 53.94 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 58.33 | 0.70 | 52.65 | 59.57 | null | s/GiB |
| goodput | 4 | upload | 5 | 71.38 | 10.15 | 61.23 | 85.95 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 46.68 | 5.19 | 38.04 | 51.87 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 40.48 | 2.14 | 37.54 | 45.48 | null | s/GiB |
| goodput | 1 | download | 5 | 28.49 | 0.26 | 26.87 | 28.81 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 60.06 | 0.52 | 59.55 | 64.08 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 56.53 | 2.79 | 52.50 | 60.42 | null | s/GiB |
| goodput | 4 | download | 5 | 34.33 | 0.74 | 33.02 | 37.74 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 64.00 | 2.70 | 54.58 | 66.70 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 58.10 | 1.00 | 52.63 | 63.51 | null | s/GiB |
| latency_p50 | 1 | connect | 5 | 2.80 | 0.08 | 2.54 | 3.00 | null | ms |
| latency_p95 | 1 | connect | 5 | 3.67 | 0.06 | 3.30 | 4.04 | null | ms |
| latency_p99 | 1 | connect | 5 | 4.19 | 0.10 | 3.82 | 4.75 | null | ms |
| latency_p50 | 4 | connect | 5 | 8.69 | 0.08 | 4.21 | 8.95 | null | ms |
| latency_p95 | 4 | connect | 5 | 10.68 | 0.47 | 9.55 | 11.90 | null | ms |
| latency_p99 | 4 | connect | 5 | 12.18 | 1.05 | 10.95 | 16.07 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 144.7 | 5.027 | 28.8 | 53.94 | 58.33 | null | null | null | null | null |
| 1 | 1 | upload | 154.8 | 5.017 | 30.9 | 49.67 | 52.65 | null | null | null | null | null |
| 2 | 1 | upload | 148.6 | 5.021 | 29.6 | 52.35 | 58.07 | null | null | null | null | null |
| 3 | 1 | upload | 145.7 | 5.031 | 29.0 | 53.62 | 59.03 | null | null | null | null | null |
| 4 | 1 | upload | 144.6 | 5.010 | 28.9 | 53.91 | 59.57 | null | null | null | null | null |
| 0 | 4 | upload | 416.5 | 5.035 | 82.7 | 41.38 | 38.63 | null | null | null | null | null |
| 1 | 4 | upload | 434.0 | 5.049 | 85.9 | 38.04 | 37.54 | null | null | null | null | null |
| 2 | 4 | upload | 359.7 | 5.040 | 71.4 | 46.68 | 40.48 | null | null | null | null | null |
| 3 | 4 | upload | 314.5 | 5.136 | 61.2 | 51.87 | 42.62 | null | null | null | null | null |
| 4 | 4 | upload | 328.3 | 5.059 | 64.9 | 49.16 | 45.48 | null | null | null | null | null |
| 0 | 1 | download | 144.1 | 5.000 | 28.8 | 60.06 | 53.74 | null | null | null | null | null |
| 1 | 1 | download | 142.4 | 5.000 | 28.5 | 59.88 | 57.58 | null | null | null | null | null |
| 2 | 1 | download | 141.3 | 5.003 | 28.2 | 61.82 | 56.53 | null | null | null | null | null |
| 3 | 1 | download | 143.8 | 5.000 | 28.8 | 59.55 | 52.50 | null | null | null | null | null |
| 4 | 1 | download | 134.4 | 5.001 | 26.9 | 64.08 | 60.42 | null | null | null | null | null |
| 0 | 4 | download | 165.1 | 5.001 | 33.0 | 66.05 | 63.51 | null | null | null | null | null |
| 1 | 4 | download | 172.8 | 5.001 | 34.5 | 60.70 | 59.10 | null | null | null | null | null |
| 2 | 4 | download | 188.7 | 5.001 | 37.7 | 54.58 | 52.63 | null | null | null | null | null |
| 3 | 4 | download | 171.7 | 5.001 | 34.3 | 64.00 | 58.10 | null | null | null | null | null |
| 4 | 4 | download | 168.0 | 5.001 | 33.6 | 66.70 | 57.86 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 2.54 | 3.30 | 3.82 |
| 1 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 2.75 | 3.61 | 4.14 |
| 2 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 2.80 | 3.67 | 4.29 |
| 3 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.00 | 4.04 | 4.75 |
| 4 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.88 | 3.72 | 4.19 |
| 0 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 4.21 | 9.55 | 10.95 |
| 1 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 8.95 | 10.99 | 12.49 |
| 2 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 8.71 | 10.68 | 12.18 |
| 3 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 8.61 | 10.21 | 11.13 |
| 4 | 4 | connect | 0.0 | 5.011 | null | null | null | null | null | 8.69 | 11.90 | 16.07 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 7.620 | 63.9 | 17 | 13 | 8.240 | 83.2 | 20 | 11 |
| 1 | 7.510 | 64.4 | 17 | 14 | 7.960 | 101.0 | 21 | 11 |
| 2 | 7.600 | 65.9 | 17 | 15 | 8.430 | 126.9 | 21 | 11 |
| 3 | 7.630 | 66.1 | 17 | 15 | 8.400 | 138.7 | 21 | 11 |
| 4 | 7.610 | 67.1 | 17 | 16 | 8.410 | 143.3 | 21 | 11 |
| 0 | 16.830 | 75.8 | 18 | 20 | 15.710 | 215.9 | 21 | 15 |
| 1 | 16.120 | 77.4 | 20 | 20 | 15.910 | 229.6 | 21 | 14 |
| 2 | 16.400 | 81.2 | 21 | 21 | 14.220 | 353.8 | 21 | 15 |
| 3 | 15.930 | 82.3 | 21 | 24 | 13.090 | 365.7 | 21 | 16 |
| 4 | 15.760 | 81.8 | 21 | 22 | 14.580 | 377.8 | 21 | 14 |
| 0 | 8.450 | 100.5 | 21 | 19 | 7.560 | 377.9 | 21 | 12 |
| 1 | 8.330 | 107.1 | 21 | 19 | 8.010 | 378.1 | 21 | 12 |
| 2 | 8.530 | 138.0 | 21 | 19 | 7.800 | 379.9 | 21 | 11 |
| 3 | 8.360 | 143.7 | 21 | 19 | 7.370 | 381.2 | 21 | 11 |
| 4 | 8.410 | 147.0 | 21 | 19 | 7.930 | 382.7 | 21 | 11 |
| 0 | 10.650 | 204.4 | 21 | 23 | 10.240 | 385.3 | 21 | 14 |
| 1 | 10.240 | 215.1 | 21 | 24 | 9.970 | 385.3 | 21 | 15 |
| 2 | 10.060 | 222.8 | 21 | 28 | 9.700 | 385.8 | 21 | 16 |
| 3 | 10.730 | 228.0 | 21 | 29 | 9.740 | 387.0 | 21 | 15 |
| 4 | 10.940 | 239.4 | 21 | 30 | 9.490 | 387.0 | 21 | 15 |
| 0 | 5.630 | 242.7 | 21 | 88 | 5.090 | 387.4 | 21 | 12 |
| 1 | 5.780 | 257.9 | 21 | 100 | 5.090 | 387.5 | 21 | 12 |
| 2 | 5.710 | 265.2 | 21 | 125 | 5.580 | 387.7 | 21 | 12 |
| 3 | 5.770 | 279.4 | 21 | 141 | 5.170 | 388.0 | 21 | 12 |
| 4 | 5.750 | 289.8 | 21 | 157 | 5.080 | 388.2 | 21 | 12 |
| 0 | 12.220 | 323.5 | 21 | 187 | 14.500 | 394.3 | 21 | 14 |
| 1 | 7.360 | 338.4 | 21 | 203 | 18.450 | 399.7 | 23 | 14 |
| 2 | 7.620 | 345.5 | 21 | 214 | 17.750 | 399.8 | 23 | 15 |
| 3 | 7.650 | 348.5 | 21 | 230 | 17.810 | 399.9 | 23 | 14 |
| 4 | 7.650 | 356.5 | 21 | 247 | 18.050 | 401.2 | 23 | 15 |
