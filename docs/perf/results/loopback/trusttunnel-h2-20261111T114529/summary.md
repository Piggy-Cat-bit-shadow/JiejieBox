# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T114529`
- kind: e2e
- started: 2026-11-11T11:45:29+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 176.07 | 2.41 | 173.15 | 189.93 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 9.85 | 0.12 | 9.24 | 9.97 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 9.13 | 0.03 | 8.50 | 9.31 | null | s/GiB |
| goodput | 4 | upload | 5 | 208.51 | 9.51 | 175.77 | 219.74 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 10.35 | 0.53 | 9.82 | 12.48 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 8.60 | 0.54 | 8.06 | 10.66 | null | s/GiB |
| goodput | 1 | download | 5 | 134.78 | 1.86 | 129.95 | 136.94 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 9.96 | 0.11 | 9.85 | 10.40 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 11.02 | 0.14 | 10.87 | 11.44 | null | s/GiB |
| goodput | 4 | download | 5 | 137.75 | 0.45 | 136.57 | 142.54 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 13.42 | 0.03 | 12.97 | 13.55 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 12.71 | 0.01 | 12.27 | 12.86 | null | s/GiB |
| goodput | 1 | udp | 5 | 9.59 | 1.23 | 7.44 | 11.35 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 5 | 217.19 | 31.38 | 185.81 | 286.82 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 5 | 244.53 | 27.15 | 217.38 | 319.58 | null | s/GiB |
| goodput | 4 | udp | 5 | 8.94 | 0.14 | 7.87 | 10.01 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 5 | 233.60 | 3.12 | 213.83 | 265.19 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 5 | 263.60 | 3.53 | 250.87 | 299.02 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 866.6 | 5.005 | 173.2 | 9.97 | 9.31 | null | null | null | null | null |
| 1 | 1 | upload | 889.7 | 5.004 | 177.8 | 9.85 | 9.13 | null | null | null | null | null |
| 2 | 1 | upload | 880.6 | 5.001 | 176.1 | 9.69 | 9.09 | null | null | null | null | null |
| 3 | 1 | upload | 949.8 | 5.001 | 189.9 | 9.24 | 8.50 | null | null | null | null | null |
| 4 | 1 | upload | 868.5 | 5.001 | 173.7 | 9.86 | 9.13 | null | null | null | null | null |
| 0 | 4 | upload | 1100.7 | 5.009 | 219.7 | 9.82 | 8.06 | null | null | null | null | null |
| 1 | 4 | upload | 1088.6 | 5.017 | 217.0 | 9.99 | 8.24 | null | null | null | null | null |
| 2 | 4 | upload | 1043.6 | 5.005 | 208.5 | 10.35 | 8.60 | null | null | null | null | null |
| 3 | 4 | upload | 881.8 | 5.017 | 175.8 | 12.48 | 10.66 | null | null | null | null | null |
| 4 | 4 | upload | 998.4 | 5.017 | 199.0 | 10.97 | 9.15 | null | null | null | null | null |
| 0 | 1 | download | 649.8 | 5.001 | 129.9 | 10.40 | 11.44 | null | null | null | null | null |
| 1 | 1 | download | 678.9 | 5.000 | 135.8 | 9.96 | 10.98 | null | null | null | null | null |
| 2 | 1 | download | 673.9 | 5.000 | 134.8 | 9.85 | 11.02 | null | null | null | null | null |
| 3 | 1 | download | 684.7 | 5.000 | 136.9 | 9.92 | 10.87 | null | null | null | null | null |
| 4 | 1 | download | 664.7 | 5.001 | 132.9 | 10.23 | 11.21 | null | null | null | null | null |
| 0 | 4 | download | 683.0 | 5.001 | 136.6 | 13.55 | 12.86 | null | null | null | null | null |
| 1 | 4 | download | 688.8 | 5.001 | 137.7 | 13.42 | 12.71 | null | null | null | null | null |
| 2 | 4 | download | 688.8 | 5.001 | 137.8 | 13.39 | 12.73 | null | null | null | null | null |
| 3 | 4 | download | 712.8 | 5.001 | 142.5 | 12.97 | 12.27 | null | null | null | null | null |
| 4 | 4 | download | 691.1 | 5.001 | 138.2 | 13.42 | 12.70 | null | null | null | null | null |
| 0 | 1 | udp | 47.9 | 5.000 | 9.6 | 217.19 | 244.53 | null | null | null | null | null |
| 1 | 1 | udp | 48.2 | 5.000 | 9.6 | 214.31 | 243.01 | null | null | null | null | null |
| 2 | 1 | udp | 56.8 | 5.000 | 11.4 | 185.81 | 217.38 | null | null | null | null | null |
| 3 | 1 | udp | 37.2 | 5.000 | 7.4 | 286.82 | 319.58 | null | null | null | null | null |
| 4 | 1 | udp | 41.8 | 5.000 | 8.4 | 249.50 | 279.61 | null | null | null | null | null |
| 0 | 4 | udp | 39.3 | 5.000 | 7.9 | 265.19 | 299.02 | null | null | null | null | null |
| 1 | 4 | udp | 44.7 | 5.000 | 8.9 | 233.60 | 263.60 | null | null | null | null | null |
| 2 | 4 | udp | 44.0 | 5.000 | 8.8 | 236.42 | 267.13 | null | null | null | null | null |
| 3 | 4 | udp | 44.9 | 5.000 | 9.0 | 230.48 | 261.49 | null | null | null | null | null |
| 4 | 4 | udp | 50.0 | 5.000 | 10.0 | 213.83 | 250.87 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.440 | 61.6 | 14 | 12 | 7.880 | 65.1 | 14 | 14 |
| 1 | 8.560 | 63.1 | 14 | 12 | 7.930 | 66.1 | 15 | 14 |
| 2 | 8.330 | 64.2 | 15 | 12 | 7.820 | 66.7 | 15 | 14 |
| 3 | 8.570 | 65.0 | 15 | 12 | 7.880 | 67.0 | 15 | 14 |
| 4 | 8.360 | 65.4 | 15 | 12 | 7.740 | 67.3 | 16 | 14 |
| 0 | 10.560 | 67.0 | 16 | 15 | 8.660 | 67.8 | 16 | 17 |
| 1 | 10.620 | 67.9 | 16 | 15 | 8.760 | 68.1 | 16 | 17 |
| 2 | 10.550 | 69.6 | 16 | 15 | 8.760 | 68.3 | 16 | 17 |
| 3 | 10.750 | 72.0 | 16 | 15 | 9.180 | 69.2 | 16 | 17 |
| 4 | 10.700 | 73.1 | 16 | 15 | 8.920 | 69.9 | 16 | 17 |
| 0 | 6.600 | 73.5 | 17 | 12 | 7.260 | 69.0 | 16 | 14 |
| 1 | 6.600 | 76.2 | 17 | 12 | 7.280 | 68.5 | 16 | 14 |
| 2 | 6.480 | 76.8 | 17 | 12 | 7.250 | 68.5 | 16 | 14 |
| 3 | 6.630 | 75.5 | 17 | 12 | 7.270 | 68.5 | 16 | 14 |
| 4 | 6.640 | 75.5 | 17 | 12 | 7.280 | 68.5 | 16 | 14 |
| 0 | 9.040 | 75.5 | 17 | 15 | 8.580 | 68.6 | 16 | 17 |
| 1 | 9.030 | 75.5 | 17 | 15 | 8.550 | 68.5 | 16 | 17 |
| 2 | 9.010 | 75.5 | 17 | 15 | 8.560 | 68.8 | 16 | 17 |
| 3 | 9.030 | 75.5 | 17 | 15 | 8.540 | 68.7 | 16 | 17 |
| 4 | 9.060 | 75.5 | 17 | 15 | 8.570 | 68.5 | 16 | 17 |
| 0 | 10.170 | 75.5 | 17 | 13 | 11.450 | 68.6 | 16 | 14 |
| 1 | 10.080 | 75.5 | 17 | 13 | 11.430 | 68.6 | 16 | 14 |
| 2 | 10.300 | 75.5 | 17 | 13 | 12.050 | 68.8 | 16 | 14 |
| 3 | 10.420 | 75.5 | 17 | 13 | 11.610 | 69.4 | 16 | 14 |
| 4 | 10.190 | 75.5 | 17 | 13 | 11.420 | 69.2 | 16 | 14 |
| 0 | 10.190 | 75.5 | 17 | 13 | 11.490 | 69.2 | 16 | 14 |
| 1 | 10.200 | 75.5 | 17 | 13 | 11.510 | 68.5 | 16 | 14 |
| 2 | 10.160 | 75.5 | 17 | 13 | 11.480 | 68.9 | 16 | 14 |
| 3 | 10.110 | 75.5 | 17 | 13 | 11.470 | 68.7 | 16 | 14 |
| 4 | 10.450 | 75.5 | 17 | 13 | 12.260 | 68.9 | 17 | 14 |
