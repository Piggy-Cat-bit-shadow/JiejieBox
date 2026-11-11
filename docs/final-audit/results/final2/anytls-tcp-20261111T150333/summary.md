# anytls-tcp - anytls

- run_id: `anytls-tcp-20261111T150333`
- kind: e2e
- started: 2026-11-11T15:03:33+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 255.91 | 1.54 | 253.89 | 262.47 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 4.17 | 0.04 | 4.06 | 4.21 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 3.49 | 0.01 | 3.47 | 3.50 | null | s/GiB |
| goodput | 4 | upload | 5 | 769.43 | 14.71 | 745.20 | 788.90 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 5.47 | 0.12 | 5.34 | 5.65 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 3.65 | 0.10 | 3.50 | 3.76 | null | s/GiB |
| goodput | 1 | download | 5 | 538.90 | 11.24 | 527.66 | 782.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 2.86 | 0.02 | 2.24 | 2.88 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 2.01 | 0.06 | 1.16 | 2.07 | null | s/GiB |
| goodput | 4 | download | 5 | 1234.27 | 27.95 | 1197.58 | 1262.23 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 4.11 | 0.07 | 3.95 | 4.25 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 3.26 | 0.01 | 3.14 | 3.33 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1313.1 | 5.003 | 262.5 | 4.06 | 3.47 | null | null | null | null | null |
| 1 | 1 | upload | 1269.5 | 5.000 | 253.9 | 4.21 | 3.49 | null | null | null | null | null |
| 2 | 1 | upload | 1288.0 | 5.003 | 257.4 | 4.15 | 3.50 | null | null | null | null | null |
| 3 | 1 | upload | 1276.1 | 5.002 | 255.1 | 4.21 | 3.47 | null | null | null | null | null |
| 4 | 1 | upload | 1280.2 | 5.003 | 255.9 | 4.17 | 3.50 | null | null | null | null | null |
| 0 | 4 | upload | 3735.1 | 5.012 | 745.2 | 5.65 | 3.76 | null | null | null | null | null |
| 1 | 4 | upload | 3948.4 | 5.005 | 788.9 | 5.34 | 3.50 | null | null | null | null | null |
| 2 | 4 | upload | 3776.8 | 5.004 | 754.7 | 5.59 | 3.65 | null | null | null | null | null |
| 3 | 4 | upload | 3852.0 | 5.006 | 769.4 | 5.47 | 3.65 | null | null | null | null | null |
| 4 | 4 | upload | 3895.5 | 5.006 | 778.2 | 5.42 | 3.55 | null | null | null | null | null |
| 0 | 1 | download | 3911.7 | 5.000 | 782.3 | 2.24 | 1.16 | null | null | null | null | null |
| 1 | 1 | download | 3378.5 | 5.000 | 675.7 | 2.47 | 1.42 | null | null | null | null | null |
| 2 | 1 | download | 2638.5 | 5.000 | 527.7 | 2.88 | 2.07 | null | null | null | null | null |
| 3 | 1 | download | 2685.0 | 5.000 | 537.0 | 2.86 | 2.03 | null | null | null | null | null |
| 4 | 1 | download | 2694.7 | 5.000 | 538.9 | 2.88 | 2.01 | null | null | null | null | null |
| 0 | 4 | download | 6312.9 | 5.001 | 1262.2 | 4.18 | 3.14 | null | null | null | null | null |
| 1 | 4 | download | 5989.5 | 5.001 | 1197.6 | 4.25 | 3.24 | null | null | null | null | null |
| 2 | 4 | download | 6173.4 | 5.002 | 1234.3 | 4.07 | 3.26 | null | null | null | null | null |
| 3 | 4 | download | 6027.8 | 5.001 | 1205.3 | 4.11 | 3.33 | null | null | null | null | null |
| 4 | 4 | download | 6230.1 | 5.001 | 1245.7 | 3.95 | 3.27 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.200 | 59.8 | 14 | 12 | 4.450 | 62.1 | 14 | 13 |
| 1 | 5.220 | 61.0 | 15 | 12 | 4.330 | 63.0 | 15 | 13 |
| 2 | 5.220 | 61.3 | 15 | 12 | 4.400 | 63.4 | 15 | 13 |
| 3 | 5.250 | 61.5 | 16 | 12 | 4.330 | 63.8 | 15 | 13 |
| 4 | 5.210 | 61.9 | 16 | 12 | 4.370 | 63.6 | 15 | 13 |
| 0 | 20.600 | 63.5 | 16 | 21 | 13.710 | 67.0 | 18 | 22 |
| 1 | 20.580 | 64.6 | 16 | 21 | 13.490 | 66.9 | 20 | 22 |
| 2 | 20.620 | 64.2 | 17 | 21 | 13.480 | 67.4 | 20 | 22 |
| 3 | 20.590 | 64.7 | 17 | 21 | 13.740 | 67.7 | 20 | 22 |
| 4 | 20.610 | 64.8 | 17 | 21 | 13.520 | 67.2 | 20 | 22 |
| 0 | 8.560 | 65.5 | 17 | 18 | 4.430 | 66.7 | 20 | 19 |
| 1 | 8.140 | 64.6 | 17 | 18 | 4.670 | 66.7 | 20 | 19 |
| 2 | 7.410 | 64.4 | 17 | 18 | 5.340 | 66.7 | 20 | 19 |
| 3 | 7.500 | 64.2 | 17 | 18 | 5.310 | 66.7 | 20 | 19 |
| 4 | 7.570 | 64.5 | 17 | 18 | 5.300 | 66.7 | 20 | 19 |
| 0 | 25.780 | 67.9 | 17 | 18 | 19.350 | 66.7 | 20 | 19 |
| 1 | 24.880 | 67.7 | 17 | 18 | 18.960 | 66.7 | 20 | 19 |
| 2 | 24.550 | 67.6 | 17 | 18 | 19.630 | 66.7 | 20 | 19 |
| 3 | 24.180 | 67.7 | 17 | 18 | 19.620 | 66.7 | 20 | 19 |
| 4 | 24.020 | 67.9 | 17 | 18 | 19.900 | 66.7 | 20 | 19 |
