# mieru-udp-underlay - mieru

- run_id: `mieru-udp-underlay-20261111T133759`
- kind: e2e
- started: 2026-11-11T13:37:59+08:00
- commit: `3fb1c36233dd` (perf(baseline): add XHTTP reference cells, the tunnel limit and the UDP loss threshold)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 30.55 | 0.27 | 29.85 | 30.85 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 50.22 | 0.26 | 49.57 | 51.38 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 55.10 | 0.26 | 53.21 | 55.36 | null | s/GiB |
| goodput | 4 | upload | 5 | 73.65 | 1.89 | 71.75 | 77.46 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 43.08 | 2.16 | 40.92 | 45.99 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 39.13 | 0.74 | 35.87 | 39.87 | null | s/GiB |
| goodput | 1 | download | 5 | 31.48 | 0.10 | 31.20 | 31.68 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 53.03 | 0.30 | 52.73 | 54.67 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 50.33 | 0.28 | 47.56 | 50.61 | null | s/GiB |
| goodput | 4 | download | 5 | 39.70 | 0.13 | 39.40 | 40.11 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 53.68 | 1.11 | 51.81 | 54.80 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 50.36 | 0.38 | 49.56 | 50.98 | null | s/GiB |
| latency_p50 | 1 | connect | 5 | 2.48 | 0.01 | 2.44 | 2.49 | null | ms |
| latency_p95 | 1 | connect | 5 | 3.30 | 0.10 | 3.11 | 3.42 | null | ms |
| latency_p99 | 1 | connect | 5 | 4.00 | 0.07 | 3.62 | 4.30 | null | ms |
| latency_p50 | 4 | connect | 5 | 7.32 | 0.05 | 6.91 | 7.37 | null | ms |
| latency_p95 | 4 | connect | 5 | 8.85 | 0.09 | 8.53 | 9.04 | null | ms |
| latency_p99 | 4 | connect | 5 | 11.50 | 0.81 | 9.98 | 12.37 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 149.3 | 5.001 | 29.9 | 51.38 | 55.36 | null | null | null | null | null |
| 1 | 1 | upload | 154.5 | 5.014 | 30.8 | 49.57 | 53.21 | null | null | null | null | null |
| 2 | 1 | upload | 152.6 | 5.024 | 30.4 | 50.41 | 55.24 | null | null | null | null | null |
| 3 | 1 | upload | 153.5 | 5.025 | 30.5 | 49.96 | 53.76 | null | null | null | null | null |
| 4 | 1 | upload | 155.2 | 5.029 | 30.9 | 50.22 | 55.10 | null | null | null | null | null |
| 0 | 4 | upload | 405.1 | 5.501 | 73.6 | 40.92 | 35.87 | null | null | null | null | null |
| 1 | 4 | upload | 370.4 | 5.162 | 71.8 | 45.58 | 39.75 | null | null | null | null | null |
| 2 | 4 | upload | 392.2 | 5.090 | 77.0 | 42.51 | 36.11 | null | null | null | null | null |
| 3 | 4 | upload | 368.5 | 5.122 | 72.0 | 45.99 | 39.87 | null | null | null | null | null |
| 4 | 4 | upload | 391.5 | 5.054 | 77.5 | 43.08 | 39.13 | null | null | null | null | null |
| 0 | 1 | download | 157.4 | 5.000 | 31.5 | 53.03 | 50.56 | null | null | null | null | null |
| 1 | 1 | download | 158.4 | 5.000 | 31.7 | 52.75 | 49.51 | null | null | null | null | null |
| 2 | 1 | download | 157.9 | 5.001 | 31.6 | 52.73 | 50.33 | null | null | null | null | null |
| 3 | 1 | download | 156.0 | 5.000 | 31.2 | 53.63 | 50.61 | null | null | null | null | null |
| 4 | 1 | download | 156.9 | 5.000 | 31.4 | 54.67 | 47.56 | null | null | null | null | null |
| 0 | 4 | download | 198.2 | 5.001 | 39.6 | 51.81 | 50.36 | null | null | null | null | null |
| 1 | 4 | download | 200.6 | 5.000 | 40.1 | 51.92 | 49.98 | null | null | null | null | null |
| 2 | 4 | download | 197.0 | 5.001 | 39.4 | 53.68 | 50.98 | null | null | null | null | null |
| 3 | 4 | download | 199.2 | 5.001 | 39.8 | 54.80 | 50.38 | null | null | null | null | null |
| 4 | 4 | download | 198.6 | 5.001 | 39.7 | 53.89 | 49.56 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.44 | 3.42 | 4.30 |
| 1 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 2.48 | 3.39 | 4.00 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.49 | 3.30 | 3.98 |
| 3 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.45 | 3.11 | 3.62 |
| 4 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.49 | 3.24 | 4.07 |
| 0 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 6.91 | 8.53 | 9.98 |
| 1 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 7.25 | 8.76 | 11.50 |
| 2 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 7.37 | 9.04 | 12.31 |
| 3 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 7.37 | 8.91 | 10.79 |
| 4 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 7.32 | 8.85 | 12.37 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 7.490 | 63.6 | 17 | 13 | 8.070 | 83.9 | 21 | 11 |
| 1 | 7.480 | 64.7 | 17 | 14 | 8.030 | 102.4 | 21 | 11 |
| 2 | 7.510 | 65.5 | 17 | 15 | 8.230 | 129.1 | 21 | 11 |
| 3 | 7.490 | 66.5 | 18 | 15 | 8.060 | 139.6 | 21 | 11 |
| 4 | 7.610 | 66.9 | 18 | 15 | 8.350 | 143.8 | 21 | 11 |
| 0 | 16.190 | 76.0 | 19 | 18 | 14.190 | 218.0 | 22 | 14 |
| 1 | 16.490 | 77.6 | 21 | 23 | 14.380 | 246.6 | 22 | 18 |
| 2 | 16.280 | 82.0 | 21 | 20 | 13.830 | 358.5 | 22 | 15 |
| 3 | 16.550 | 81.8 | 21 | 22 | 14.350 | 373.4 | 22 | 17 |
| 4 | 16.470 | 82.6 | 21 | 20 | 14.960 | 384.9 | 22 | 14 |
| 0 | 8.150 | 101.9 | 21 | 17 | 7.770 | 385.0 | 22 | 11 |
| 1 | 8.160 | 107.5 | 21 | 18 | 7.660 | 385.0 | 22 | 11 |
| 2 | 8.130 | 140.2 | 21 | 19 | 7.760 | 388.3 | 22 | 11 |
| 3 | 8.170 | 145.8 | 21 | 19 | 7.710 | 389.3 | 22 | 11 |
| 4 | 8.380 | 179.3 | 21 | 20 | 7.290 | 389.3 | 22 | 11 |
| 0 | 10.030 | 214.6 | 21 | 25 | 9.750 | 391.2 | 22 | 15 |
| 1 | 10.170 | 222.0 | 21 | 26 | 9.790 | 392.4 | 22 | 15 |
| 2 | 10.330 | 228.8 | 21 | 28 | 9.810 | 392.4 | 22 | 16 |
| 3 | 10.660 | 251.8 | 21 | 28 | 9.800 | 393.1 | 22 | 15 |
| 4 | 10.450 | 344.5 | 21 | 28 | 9.610 | 393.6 | 22 | 14 |
| 0 | 5.340 | 345.2 | 21 | 86 | 5.540 | 394.2 | 22 | 12 |
| 1 | 5.620 | 348.3 | 21 | 115 | 4.970 | 394.4 | 22 | 12 |
| 2 | 5.560 | 354.0 | 22 | 138 | 5.400 | 394.6 | 22 | 12 |
| 3 | 5.280 | 354.0 | 22 | 158 | 4.940 | 394.7 | 22 | 12 |
| 4 | 5.540 | 357.6 | 22 | 168 | 5.370 | 408.3 | 22 | 12 |
| 0 | 9.940 | 361.9 | 22 | 200 | 16.630 | 408.7 | 22 | 15 |
| 1 | 7.750 | 372.9 | 22 | 218 | 17.820 | 411.1 | 24 | 16 |
| 2 | 8.110 | 377.5 | 22 | 231 | 18.090 | 411.3 | 24 | 15 |
| 3 | 8.210 | 377.6 | 22 | 246 | 17.790 | 411.4 | 24 | 14 |
| 4 | 8.160 | 380.9 | 22 | 262 | 18.180 | 420.0 | 24 | 14 |
