# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T115737`
- kind: e2e
- started: 2026-11-11T11:57:37+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 64.20 | 0.35 | 63.21 | 65.02 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 37.46 | 0.18 | 37.29 | 38.27 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 40.65 | 0.12 | 40.53 | 41.67 | null | s/GiB |
| goodput | 4 | upload | 5 | 72.42 | 0.60 | 70.30 | 73.32 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 38.75 | 0.14 | 38.48 | 39.67 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 45.84 | 0.32 | 45.12 | 47.19 | null | s/GiB |
| goodput | 1 | download | 5 | 64.03 | 0.60 | 61.86 | 64.98 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 41.92 | 0.18 | 41.57 | 43.33 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 36.12 | 0.10 | 35.80 | 37.08 | null | s/GiB |
| goodput | 4 | download | 5 | 70.05 | 1.64 | 64.87 | 71.69 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 47.62 | 0.72 | 46.90 | 49.04 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 37.97 | 0.50 | 37.48 | 39.44 | null | s/GiB |
| goodput | 1 | udp | 5 | 45.63 | 2.06 | 42.75 | 47.69 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 5 | 93.71 | 3.11 | 90.61 | 99.99 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 5 | 86.13 | 3.13 | 82.36 | 90.36 | null | s/GiB |
| goodput | 4 | udp | 5 | 46.92 | 1.57 | 43.67 | 48.78 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 5 | 88.31 | 2.44 | 85.86 | 94.18 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 5 | 80.76 | 2.62 | 78.14 | 86.30 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 317.0 | 5.016 | 63.2 | 38.27 | 41.67 | null | null | null | null | null |
| 1 | 1 | upload | 321.2 | 5.003 | 64.2 | 37.43 | 40.65 | null | null | null | null | null |
| 2 | 1 | upload | 323.1 | 5.009 | 64.5 | 37.46 | 40.63 | null | null | null | null | null |
| 3 | 1 | upload | 319.7 | 5.006 | 63.9 | 38.12 | 41.35 | null | null | null | null | null |
| 4 | 1 | upload | 325.4 | 5.005 | 65.0 | 37.29 | 40.53 | null | null | null | null | null |
| 0 | 4 | upload | 359.8 | 5.118 | 70.3 | 39.67 | 47.19 | null | null | null | null | null |
| 1 | 4 | upload | 364.7 | 5.032 | 72.5 | 38.66 | 45.85 | null | null | null | null | null |
| 2 | 4 | upload | 362.9 | 5.053 | 71.8 | 38.89 | 45.52 | null | null | null | null | null |
| 3 | 4 | upload | 370.8 | 5.057 | 73.3 | 38.75 | 45.84 | null | null | null | null | null |
| 4 | 4 | upload | 365.6 | 5.049 | 72.4 | 38.48 | 45.12 | null | null | null | null | null |
| 0 | 1 | download | 309.3 | 5.000 | 61.9 | 43.33 | 37.08 | null | null | null | null | null |
| 1 | 1 | download | 323.2 | 5.000 | 64.6 | 41.92 | 36.12 | null | null | null | null | null |
| 2 | 1 | download | 319.8 | 5.000 | 64.0 | 41.94 | 36.02 | null | null | null | null | null |
| 3 | 1 | download | 320.2 | 5.000 | 64.0 | 41.74 | 36.14 | null | null | null | null | null |
| 4 | 1 | download | 324.9 | 5.000 | 65.0 | 41.57 | 35.80 | null | null | null | null | null |
| 0 | 4 | download | 350.3 | 5.000 | 70.1 | 47.62 | 37.97 | null | null | null | null | null |
| 1 | 4 | download | 358.5 | 5.001 | 71.7 | 46.90 | 37.48 | null | null | null | null | null |
| 2 | 4 | download | 341.6 | 5.267 | 64.9 | 49.04 | 39.44 | null | null | null | null | null |
| 3 | 4 | download | 350.9 | 5.000 | 70.2 | 47.33 | 37.93 | null | null | null | null | null |
| 4 | 4 | download | 337.3 | 5.001 | 67.5 | 48.84 | 39.19 | null | null | null | null | null |
| 0 | 1 | udp | 215.7 | 5.000 | 43.1 | 98.99 | 89.26 | null | null | null | null | null |
| 1 | 1 | udp | 213.7 | 5.000 | 42.7 | 99.99 | 90.36 | null | null | null | null | null |
| 2 | 1 | udp | 238.5 | 5.000 | 47.7 | 90.61 | 82.36 | null | null | null | null | null |
| 3 | 1 | udp | 235.6 | 5.000 | 47.1 | 91.12 | 83.25 | null | null | null | null | null |
| 4 | 1 | udp | 228.2 | 5.000 | 45.6 | 93.71 | 86.13 | null | null | null | null | null |
| 0 | 4 | udp | 243.9 | 5.000 | 48.8 | 85.86 | 78.14 | null | null | null | null | null |
| 1 | 4 | udp | 226.7 | 5.000 | 45.3 | 91.28 | 83.47 | null | null | null | null | null |
| 2 | 4 | udp | 236.2 | 5.000 | 47.2 | 87.78 | 80.41 | null | null | null | null | null |
| 3 | 4 | udp | 218.3 | 5.000 | 43.7 | 94.18 | 86.30 | null | null | null | null | null |
| 4 | 4 | udp | 234.6 | 5.000 | 46.9 | 88.31 | 80.76 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 11.850 | 69.1 | 17 | 12 | 12.900 | 69.6 | 17 | 12 |
| 1 | 11.740 | 68.9 | 17 | 12 | 12.750 | 70.4 | 17 | 12 |
| 2 | 11.820 | 69.1 | 18 | 12 | 12.820 | 70.7 | 18 | 12 |
| 3 | 11.900 | 69.1 | 18 | 12 | 12.910 | 71.1 | 18 | 12 |
| 4 | 11.850 | 69.6 | 18 | 12 | 12.880 | 70.7 | 18 | 12 |
| 0 | 13.940 | 73.5 | 18 | 18 | 16.580 | 72.9 | 18 | 18 |
| 1 | 13.770 | 74.5 | 18 | 17 | 16.330 | 72.3 | 18 | 17 |
| 2 | 13.780 | 74.5 | 18 | 19 | 16.130 | 73.0 | 20 | 19 |
| 3 | 14.030 | 75.0 | 18 | 19 | 16.600 | 72.8 | 20 | 19 |
| 4 | 13.740 | 75.0 | 18 | 19 | 16.110 | 73.7 | 20 | 19 |
| 0 | 13.090 | 75.0 | 18 | 12 | 11.200 | 72.9 | 20 | 13 |
| 1 | 13.230 | 75.0 | 18 | 12 | 11.400 | 72.7 | 20 | 15 |
| 2 | 13.100 | 75.0 | 18 | 12 | 11.250 | 73.2 | 20 | 14 |
| 3 | 13.050 | 75.0 | 18 | 12 | 11.300 | 72.7 | 20 | 14 |
| 4 | 13.190 | 75.0 | 18 | 12 | 11.360 | 72.8 | 20 | 14 |
| 0 | 16.290 | 75.0 | 21 | 15 | 12.990 | 77.9 | 20 | 19 |
| 1 | 16.420 | 75.0 | 21 | 15 | 13.120 | 80.1 | 20 | 24 |
| 2 | 16.360 | 75.0 | 21 | 15 | 13.160 | 78.8 | 20 | 27 |
| 3 | 16.220 | 75.0 | 21 | 15 | 13.000 | 79.9 | 20 | 25 |
| 4 | 16.090 | 75.0 | 21 | 15 | 12.910 | 80.3 | 20 | 27 |
| 0 | 20.850 | 75.4 | 21 | 14 | 18.800 | 82.3 | 20 | 23 |
| 1 | 20.870 | 75.0 | 21 | 14 | 18.860 | 82.8 | 20 | 23 |
| 2 | 21.100 | 75.1 | 21 | 14 | 19.180 | 81.5 | 20 | 22 |
| 3 | 20.960 | 75.5 | 21 | 14 | 19.150 | 81.5 | 20 | 23 |
| 4 | 20.880 | 75.1 | 21 | 14 | 19.190 | 81.5 | 20 | 24 |
| 0 | 20.450 | 75.1 | 21 | 14 | 18.610 | 81.5 | 20 | 25 |
| 1 | 20.210 | 75.0 | 21 | 14 | 18.480 | 81.5 | 20 | 25 |
| 2 | 20.250 | 75.0 | 21 | 14 | 18.550 | 81.5 | 20 | 27 |
| 3 | 20.080 | 75.4 | 21 | 14 | 18.400 | 81.5 | 20 | 29 |
| 4 | 20.230 | 75.1 | 21 | 14 | 18.500 | 81.5 | 20 | 31 |
