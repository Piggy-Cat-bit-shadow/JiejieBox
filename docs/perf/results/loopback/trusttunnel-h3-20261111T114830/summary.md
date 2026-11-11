# trusttunnel-h3 - trusttunnel

- run_id: `trusttunnel-h3-20261111T114830`
- kind: e2e
- started: 2026-11-11T11:48:30+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 119.08 | 12.31 | 106.77 | 134.33 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 16.05 | 0.75 | 15.15 | 18.98 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 14.83 | 0.71 | 13.87 | 17.74 | null | s/GiB |
| goodput | 4 | upload | 5 | 196.50 | 13.94 | 176.11 | 210.44 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 14.83 | 0.70 | 14.02 | 16.16 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 17.46 | 0.94 | 16.35 | 19.21 | null | s/GiB |
| goodput | 1 | download | 5 | 195.03 | 9.33 | 176.46 | 204.37 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 11.04 | 0.42 | 10.54 | 11.54 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 11.11 | 0.39 | 10.56 | 11.50 | null | s/GiB |
| goodput | 4 | download | 5 | 202.18 | 11.18 | 163.02 | 217.92 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 17.65 | 0.61 | 16.76 | 18.66 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 12.02 | 0.48 | 11.29 | 12.65 | null | s/GiB |
| goodput | 1 | udp | 5 | 32.00 | 0.85 | 29.72 | 32.85 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 5 | 90.49 | 2.78 | 87.71 | 96.80 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 5 | 90.68 | 2.49 | 88.09 | 96.46 | null | s/GiB |
| goodput | 4 | udp | 5 | 28.39 | 0.79 | 24.83 | 31.30 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 5 | 100.54 | 1.98 | 92.72 | 114.82 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 5 | 101.12 | 2.06 | 93.11 | 115.81 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 671.9 | 5.001 | 134.3 | 15.15 | 13.87 | null | null | null | null | null |
| 1 | 1 | upload | 663.6 | 5.000 | 132.7 | 15.31 | 14.12 | null | null | null | null | null |
| 2 | 1 | upload | 596.4 | 5.008 | 119.1 | 16.05 | 14.83 | null | null | null | null | null |
| 3 | 1 | upload | 579.0 | 5.007 | 115.6 | 16.57 | 15.33 | null | null | null | null | null |
| 4 | 1 | upload | 534.6 | 5.007 | 106.8 | 18.98 | 17.74 | null | null | null | null | null |
| 0 | 4 | upload | 882.3 | 5.010 | 176.1 | 15.53 | 18.41 | null | null | null | null | null |
| 1 | 4 | upload | 913.1 | 5.022 | 181.8 | 16.16 | 19.21 | null | null | null | null | null |
| 2 | 4 | upload | 1001.5 | 5.014 | 199.7 | 14.82 | 17.46 | null | null | null | null | null |
| 3 | 4 | upload | 984.9 | 5.012 | 196.5 | 14.83 | 17.44 | null | null | null | null | null |
| 4 | 4 | upload | 1054.9 | 5.013 | 210.4 | 14.02 | 16.35 | null | null | null | null | null |
| 0 | 1 | download | 975.2 | 5.000 | 195.0 | 11.04 | 11.11 | null | null | null | null | null |
| 1 | 1 | download | 1022.0 | 5.001 | 204.4 | 10.61 | 10.56 | null | null | null | null | null |
| 2 | 1 | download | 882.3 | 5.000 | 176.5 | 11.54 | 11.50 | null | null | null | null | null |
| 3 | 1 | download | 899.0 | 5.000 | 179.8 | 11.36 | 11.30 | null | null | null | null | null |
| 4 | 1 | download | 991.7 | 5.000 | 198.3 | 10.54 | 10.60 | null | null | null | null | null |
| 0 | 4 | download | 815.2 | 5.000 | 163.0 | 17.65 | 12.02 | null | null | null | null | null |
| 1 | 4 | download | 1042.3 | 5.001 | 208.4 | 16.76 | 11.29 | null | null | null | null | null |
| 2 | 4 | download | 1089.6 | 5.000 | 217.9 | 17.04 | 11.54 | null | null | null | null | null |
| 3 | 4 | download | 1010.9 | 5.000 | 202.2 | 18.15 | 12.34 | null | null | null | null | null |
| 4 | 4 | download | 955.0 | 5.000 | 191.0 | 18.66 | 12.65 | null | null | null | null | null |
| 0 | 1 | udp | 160.0 | 5.000 | 32.0 | 90.49 | 90.68 | null | null | null | null | null |
| 1 | 1 | udp | 164.3 | 5.000 | 32.9 | 87.71 | 88.09 | null | null | null | null | null |
| 2 | 1 | udp | 148.6 | 5.000 | 29.7 | 96.80 | 96.46 | null | null | null | null | null |
| 3 | 1 | udp | 152.8 | 5.000 | 30.6 | 93.31 | 93.18 | null | null | null | null | null |
| 4 | 1 | udp | 162.3 | 5.000 | 32.5 | 88.67 | 88.23 | null | null | null | null | null |
| 0 | 4 | udp | 142.0 | 5.000 | 28.4 | 100.54 | 101.12 | null | null | null | null | null |
| 1 | 4 | udp | 138.0 | 5.000 | 27.6 | 102.24 | 102.68 | null | null | null | null | null |
| 2 | 4 | udp | 124.1 | 5.000 | 24.8 | 114.82 | 115.81 | null | null | null | null | null |
| 3 | 4 | udp | 145.4 | 5.000 | 29.1 | 98.57 | 99.06 | null | null | null | null | null |
| 4 | 4 | udp | 156.5 | 5.000 | 31.3 | 92.72 | 93.11 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 9.940 | 65.1 | 16 | 12 | 9.100 | 66.7 | 16 | 13 |
| 1 | 9.920 | 65.7 | 17 | 12 | 9.150 | 66.8 | 17 | 13 |
| 2 | 9.350 | 65.7 | 17 | 12 | 8.640 | 66.8 | 17 | 13 |
| 3 | 9.370 | 66.1 | 17 | 12 | 8.670 | 66.8 | 17 | 13 |
| 4 | 9.910 | 66.4 | 17 | 12 | 9.260 | 66.6 | 18 | 13 |
| 0 | 13.380 | 67.9 | 17 | 15 | 15.860 | 69.0 | 18 | 16 |
| 1 | 14.410 | 68.3 | 19 | 15 | 17.130 | 68.8 | 18 | 16 |
| 2 | 14.490 | 68.5 | 19 | 15 | 17.080 | 68.8 | 18 | 16 |
| 3 | 14.260 | 68.5 | 19 | 15 | 16.770 | 69.2 | 18 | 16 |
| 4 | 14.440 | 68.7 | 19 | 15 | 16.840 | 69.1 | 18 | 16 |
| 0 | 10.510 | 68.3 | 19 | 12 | 10.580 | 68.3 | 18 | 13 |
| 1 | 10.590 | 68.3 | 19 | 12 | 10.540 | 68.2 | 18 | 13 |
| 2 | 9.940 | 68.5 | 19 | 12 | 9.910 | 68.3 | 18 | 13 |
| 3 | 9.970 | 69.0 | 19 | 12 | 9.920 | 68.5 | 18 | 13 |
| 4 | 10.210 | 68.5 | 19 | 12 | 10.270 | 68.7 | 18 | 13 |
| 0 | 14.050 | 69.2 | 19 | 15 | 9.570 | 69.5 | 18 | 16 |
| 1 | 17.060 | 69.5 | 19 | 15 | 11.490 | 70.2 | 18 | 16 |
| 2 | 18.130 | 69.7 | 19 | 15 | 12.280 | 70.1 | 18 | 16 |
| 3 | 17.920 | 69.5 | 19 | 15 | 12.180 | 70.0 | 18 | 16 |
| 4 | 17.400 | 69.6 | 19 | 15 | 11.800 | 70.2 | 18 | 16 |
| 0 | 14.140 | 69.4 | 19 | 13 | 14.170 | 68.8 | 18 | 13 |
| 1 | 14.070 | 69.4 | 19 | 13 | 14.130 | 68.8 | 18 | 13 |
| 2 | 14.050 | 69.4 | 19 | 13 | 14.000 | 69.1 | 18 | 13 |
| 3 | 13.920 | 69.4 | 19 | 13 | 13.900 | 68.9 | 18 | 13 |
| 4 | 14.050 | 69.4 | 19 | 13 | 13.980 | 69.0 | 18 | 13 |
| 0 | 13.940 | 69.4 | 19 | 13 | 14.020 | 69.0 | 18 | 13 |
| 1 | 13.780 | 69.4 | 19 | 13 | 13.840 | 69.0 | 18 | 13 |
| 2 | 13.920 | 69.4 | 19 | 13 | 14.040 | 69.3 | 18 | 13 |
| 3 | 14.000 | 69.4 | 19 | 13 | 14.070 | 69.1 | 18 | 13 |
| 4 | 14.170 | 69.4 | 19 | 13 | 14.230 | 69.2 | 18 | 13 |
