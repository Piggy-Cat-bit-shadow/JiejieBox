# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T145043`
- kind: e2e
- started: 2026-11-11T14:50:43+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 70.08 | 0.00 | 70.07 | 72.16 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 34.01 | 0.18 | 33.31 | 34.19 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 36.67 | 0.06 | 35.76 | 36.73 | null | s/GiB |
| goodput | 4 | upload | 3 | 80.53 | 0.34 | 78.23 | 80.87 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 33.86 | 0.07 | 33.79 | 34.07 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 39.68 | 0.34 | 39.35 | 40.17 | null | s/GiB |
| goodput | 1 | download | 3 | 72.30 | 0.12 | 70.90 | 72.43 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 36.73 | 0.11 | 36.62 | 37.14 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 31.61 | 0.06 | 31.55 | 32.06 | null | s/GiB |
| goodput | 4 | download | 3 | 79.19 | 0.28 | 78.69 | 79.47 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 40.89 | 0.04 | 40.85 | 41.40 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 32.85 | 0.09 | 32.76 | 33.23 | null | s/GiB |
| goodput | 1 | udp | 3 | 49.50 | 0.34 | 49.16 | 51.33 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 86.56 | 0.48 | 84.62 | 87.03 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 79.33 | 0.07 | 77.20 | 79.40 | null | s/GiB |
| goodput | 4 | udp | 3 | 53.51 | 0.06 | 53.44 | 53.90 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 78.33 | 0.21 | 77.74 | 78.54 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 72.31 | 0.12 | 72.19 | 72.58 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 351.0 | 5.009 | 70.1 | 34.01 | 36.67 | null | null | null | null | null |
| 1 | 1 | upload | 350.7 | 5.005 | 70.1 | 34.19 | 36.73 | null | null | null | null | null |
| 2 | 1 | upload | 360.9 | 5.000 | 72.2 | 33.31 | 35.76 | null | null | null | null | null |
| 0 | 4 | upload | 406.1 | 5.042 | 80.5 | 34.07 | 40.17 | null | null | null | null | null |
| 1 | 4 | upload | 410.9 | 5.253 | 78.2 | 33.79 | 39.35 | null | null | null | null | null |
| 2 | 4 | upload | 408.2 | 5.048 | 80.9 | 33.86 | 39.68 | null | null | null | null | null |
| 0 | 1 | download | 354.6 | 5.001 | 70.9 | 37.14 | 32.06 | null | null | null | null | null |
| 1 | 1 | download | 361.5 | 5.000 | 72.3 | 36.62 | 31.55 | null | null | null | null | null |
| 2 | 1 | download | 362.1 | 5.000 | 72.4 | 36.73 | 31.61 | null | null | null | null | null |
| 0 | 4 | download | 393.5 | 5.001 | 78.7 | 41.40 | 33.23 | null | null | null | null | null |
| 1 | 4 | download | 397.4 | 5.001 | 79.5 | 40.89 | 32.85 | null | null | null | null | null |
| 2 | 4 | download | 396.0 | 5.001 | 79.2 | 40.85 | 32.76 | null | null | null | null | null |
| 0 | 1 | udp | 256.7 | 5.000 | 51.3 | 84.62 | 77.20 | null | null | null | null | null |
| 1 | 1 | udp | 245.8 | 5.000 | 49.2 | 87.03 | 79.33 | null | null | null | null | null |
| 2 | 1 | udp | 247.5 | 5.000 | 49.5 | 86.56 | 79.40 | null | null | null | null | null |
| 0 | 4 | udp | 269.5 | 5.000 | 53.9 | 77.74 | 72.31 | null | null | null | null | null |
| 1 | 4 | udp | 267.5 | 5.000 | 53.5 | 78.54 | 72.19 | null | null | null | null | null |
| 2 | 4 | udp | 267.2 | 5.000 | 53.4 | 78.33 | 72.58 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 11.660 | 70.3 | 17 | 11 | 12.570 | 70.3 | 17 | 11 |
| 1 | 11.710 | 69.8 | 17 | 11 | 12.580 | 70.1 | 17 | 11 |
| 2 | 11.740 | 70.0 | 17 | 11 | 12.600 | 70.5 | 17 | 11 |
| 0 | 13.510 | 75.8 | 18 | 16 | 15.930 | 72.3 | 19 | 16 |
| 1 | 13.560 | 74.5 | 18 | 15 | 15.790 | 72.4 | 19 | 15 |
| 2 | 13.500 | 74.9 | 18 | 18 | 15.820 | 73.4 | 19 | 18 |
| 0 | 12.860 | 74.5 | 18 | 11 | 11.100 | 72.5 | 19 | 12 |
| 1 | 12.930 | 74.5 | 18 | 11 | 11.140 | 72.6 | 19 | 12 |
| 2 | 12.990 | 74.5 | 18 | 11 | 11.180 | 72.9 | 19 | 13 |
| 0 | 15.910 | 74.5 | 19 | 14 | 12.770 | 78.2 | 19 | 20 |
| 1 | 15.870 | 74.5 | 19 | 14 | 12.750 | 79.1 | 19 | 22 |
| 2 | 15.800 | 74.5 | 19 | 14 | 12.670 | 79.2 | 19 | 23 |
| 0 | 21.210 | 75.5 | 19 | 13 | 19.350 | 80.7 | 19 | 18 |
| 1 | 20.890 | 75.2 | 19 | 13 | 19.040 | 79.9 | 20 | 20 |
| 2 | 20.920 | 75.4 | 19 | 13 | 19.190 | 79.9 | 20 | 18 |
| 0 | 20.460 | 75.4 | 19 | 13 | 19.030 | 79.9 | 20 | 18 |
| 1 | 20.520 | 75.2 | 19 | 13 | 18.860 | 79.9 | 20 | 20 |
| 2 | 20.440 | 75.1 | 19 | 13 | 18.940 | 79.9 | 20 | 22 |
