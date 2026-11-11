# trusttunnel-h3 - trusttunnel

- run_id: `trusttunnel-h3-20261111T150936`
- kind: e2e
- started: 2026-11-11T15:09:36+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 145.72 | 1.24 | 136.43 | 148.52 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 14.01 | 0.04 | 13.87 | 14.59 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 12.73 | 0.06 | 12.51 | 13.29 | null | s/GiB |
| goodput | 4 | upload | 5 | 202.24 | 7.73 | 172.85 | 221.72 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 13.60 | 0.08 | 13.15 | 13.85 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 15.95 | 0.10 | 15.25 | 16.70 | null | s/GiB |
| goodput | 1 | download | 5 | 194.83 | 2.30 | 192.53 | 209.36 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 10.42 | 0.18 | 10.07 | 10.59 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 10.52 | 0.19 | 10.18 | 10.71 | null | s/GiB |
| goodput | 4 | download | 5 | 233.01 | 8.64 | 209.58 | 244.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 15.49 | 0.30 | 15.18 | 15.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 10.62 | 0.16 | 10.38 | 10.78 | null | s/GiB |
| goodput | 1 | udp | 5 | 34.32 | 0.07 | 33.80 | 34.70 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 5 | 83.66 | 0.39 | 83.28 | 85.56 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 5 | 83.63 | 0.36 | 83.28 | 85.62 | null | s/GiB |
| goodput | 4 | udp | 5 | 31.78 | 0.44 | 31.27 | 32.62 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 5 | 90.54 | 1.28 | 88.72 | 91.84 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 5 | 91.20 | 0.97 | 89.23 | 92.21 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 732.9 | 5.004 | 146.4 | 14.06 | 12.73 | null | null | null | null | null |
| 1 | 1 | upload | 723.3 | 5.006 | 144.5 | 14.00 | 12.73 | null | null | null | null | null |
| 2 | 1 | upload | 682.8 | 5.005 | 136.4 | 14.59 | 13.29 | null | null | null | null | null |
| 3 | 1 | upload | 743.5 | 5.006 | 148.5 | 13.87 | 12.51 | null | null | null | null | null |
| 4 | 1 | upload | 729.4 | 5.005 | 145.7 | 14.01 | 12.66 | null | null | null | null | null |
| 0 | 4 | upload | 867.7 | 5.020 | 172.9 | 13.85 | 16.70 | null | null | null | null | null |
| 1 | 4 | upload | 1014.6 | 5.017 | 202.2 | 13.51 | 15.93 | null | null | null | null | null |
| 2 | 4 | upload | 1112.3 | 5.017 | 221.7 | 13.15 | 15.25 | null | null | null | null | null |
| 3 | 4 | upload | 1040.7 | 5.015 | 207.5 | 13.62 | 15.95 | null | null | null | null | null |
| 4 | 4 | upload | 976.8 | 5.022 | 194.5 | 13.60 | 16.05 | null | null | null | null | null |
| 0 | 1 | download | 1046.8 | 5.000 | 209.4 | 10.07 | 10.18 | null | null | null | null | null |
| 1 | 1 | download | 962.7 | 5.000 | 192.5 | 10.59 | 10.71 | null | null | null | null | null |
| 2 | 1 | download | 1014.3 | 5.000 | 202.8 | 10.15 | 10.27 | null | null | null | null | null |
| 3 | 1 | download | 974.2 | 5.000 | 194.8 | 10.42 | 10.52 | null | null | null | null | null |
| 4 | 1 | download | 966.8 | 5.000 | 193.4 | 10.58 | 10.61 | null | null | null | null | null |
| 0 | 4 | download | 1048.0 | 5.000 | 209.6 | 15.54 | 10.78 | null | null | null | null | null |
| 1 | 4 | download | 1121.9 | 5.000 | 224.4 | 15.49 | 10.62 | null | null | null | null | null |
| 2 | 4 | download | 1165.1 | 5.000 | 233.0 | 15.79 | 10.74 | null | null | null | null | null |
| 3 | 4 | download | 1222.9 | 5.000 | 244.6 | 15.18 | 10.38 | null | null | null | null | null |
| 4 | 4 | download | 1194.1 | 5.001 | 238.8 | 15.18 | 10.45 | null | null | null | null | null |
| 0 | 1 | udp | 173.5 | 5.000 | 34.7 | 83.28 | 83.63 | null | null | null | null | null |
| 1 | 1 | udp | 171.8 | 5.000 | 34.4 | 83.28 | 83.28 | null | null | null | null | null |
| 2 | 1 | udp | 171.3 | 5.000 | 34.3 | 84.07 | 84.31 | null | null | null | null | null |
| 3 | 1 | udp | 171.6 | 5.000 | 34.3 | 83.66 | 83.61 | null | null | null | null | null |
| 4 | 1 | udp | 169.0 | 5.000 | 33.8 | 85.56 | 85.62 | null | null | null | null | null |
| 0 | 4 | udp | 163.1 | 5.000 | 32.6 | 88.72 | 89.23 | null | null | null | null | null |
| 1 | 4 | udp | 156.3 | 5.000 | 31.3 | 91.84 | 92.16 | null | null | null | null | null |
| 2 | 4 | udp | 158.9 | 5.000 | 31.8 | 90.49 | 91.20 | null | null | null | null | null |
| 3 | 4 | udp | 158.9 | 5.000 | 31.8 | 90.54 | 90.54 | null | null | null | null | null |
| 4 | 4 | udp | 156.7 | 5.000 | 31.3 | 91.82 | 92.21 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 10.060 | 63.9 | 16 | 11 | 9.110 | 66.4 | 17 | 12 |
| 1 | 9.890 | 64.6 | 17 | 11 | 8.990 | 66.4 | 17 | 12 |
| 2 | 9.730 | 64.9 | 17 | 11 | 8.860 | 66.4 | 17 | 12 |
| 3 | 10.070 | 65.0 | 17 | 11 | 9.080 | 67.2 | 18 | 12 |
| 4 | 9.980 | 64.9 | 17 | 11 | 9.020 | 67.0 | 18 | 12 |
| 0 | 11.740 | 66.4 | 18 | 15 | 14.150 | 68.6 | 18 | 16 |
| 1 | 13.390 | 67.2 | 18 | 14 | 15.780 | 68.7 | 20 | 15 |
| 2 | 14.280 | 67.9 | 18 | 14 | 16.560 | 68.9 | 20 | 15 |
| 3 | 13.840 | 67.2 | 18 | 14 | 16.210 | 68.9 | 20 | 15 |
| 4 | 12.970 | 67.2 | 18 | 14 | 15.310 | 69.3 | 20 | 15 |
| 0 | 10.290 | 66.7 | 18 | 11 | 10.410 | 68.8 | 20 | 12 |
| 1 | 9.960 | 67.0 | 18 | 11 | 10.070 | 68.4 | 20 | 12 |
| 2 | 10.050 | 67.0 | 18 | 11 | 10.170 | 68.2 | 20 | 12 |
| 3 | 9.910 | 67.1 | 18 | 11 | 10.010 | 68.3 | 20 | 12 |
| 4 | 9.990 | 67.2 | 18 | 11 | 10.020 | 68.7 | 20 | 12 |
| 0 | 15.900 | 68.0 | 18 | 14 | 11.030 | 69.5 | 20 | 15 |
| 1 | 16.970 | 68.3 | 18 | 14 | 11.630 | 70.4 | 20 | 15 |
| 2 | 17.970 | 68.3 | 18 | 14 | 12.220 | 70.4 | 20 | 15 |
| 3 | 18.130 | 68.5 | 18 | 14 | 12.400 | 70.2 | 20 | 15 |
| 4 | 17.700 | 68.2 | 19 | 14 | 12.190 | 70.6 | 20 | 15 |
| 0 | 14.110 | 67.4 | 19 | 12 | 14.170 | 69.1 | 20 | 12 |
| 1 | 13.970 | 67.4 | 19 | 12 | 13.970 | 69.1 | 20 | 12 |
| 2 | 14.060 | 67.6 | 19 | 12 | 14.100 | 69.1 | 20 | 12 |
| 3 | 14.020 | 67.4 | 19 | 12 | 14.010 | 69.1 | 20 | 12 |
| 4 | 14.120 | 67.5 | 19 | 12 | 14.130 | 69.1 | 20 | 12 |
| 0 | 14.130 | 67.4 | 19 | 12 | 14.210 | 69.1 | 20 | 12 |
| 1 | 14.020 | 67.4 | 19 | 12 | 14.070 | 69.2 | 20 | 12 |
| 2 | 14.040 | 67.4 | 19 | 12 | 14.150 | 69.1 | 20 | 12 |
| 3 | 14.050 | 67.6 | 19 | 12 | 14.050 | 69.3 | 20 | 12 |
| 4 | 14.050 | 67.4 | 19 | 12 | 14.110 | 69.2 | 20 | 12 |
