# vless-reality-vision - vless

- run_id: `vless-reality-vision-20261111T150735`
- kind: e2e
- started: 2026-11-11T15:07:35+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | download | 5 | 528.04 | 9.77 | 513.53 | 543.74 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 2.06 | 0.03 | 2.00 | 2.11 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 1.06 | 0.01 | 1.04 | 1.08 | null | s/GiB |
| goodput | 4 | download | 5 | 1455.46 | 14.30 | 1413.95 | 1475.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 2.92 | 0.04 | 2.87 | 2.99 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 1.98 | 0.06 | 1.87 | 2.04 | null | s/GiB |
| goodput | 1 | upload | 5 | 266.20 | 5.12 | 249.83 | 271.33 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 4.02 | 0.08 | 3.94 | 4.29 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 2.80 | 0.08 | 2.72 | 3.03 | null | s/GiB |
| goodput | 4 | upload | 5 | 814.20 | 4.72 | 789.77 | 818.92 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 5.16 | 0.01 | 5.15 | 5.33 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 3.06 | 0.03 | 3.02 | 3.17 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | download | 2689.1 | 5.000 | 537.8 | 2.03 | 1.06 | null | null | null | null | null |
| 1 | 1 | download | 2718.8 | 5.000 | 543.7 | 2.00 | 1.04 | null | null | null | null | null |
| 2 | 1 | download | 2567.7 | 5.000 | 513.5 | 2.11 | 1.04 | null | null | null | null | null |
| 3 | 1 | download | 2640.4 | 5.000 | 528.0 | 2.06 | 1.06 | null | null | null | null | null |
| 4 | 1 | download | 2608.5 | 5.000 | 521.7 | 2.09 | 1.08 | null | null | null | null | null |
| 0 | 4 | download | 7349.6 | 5.001 | 1469.8 | 2.88 | 1.90 | null | null | null | null | null |
| 1 | 4 | download | 7278.1 | 5.001 | 1455.5 | 2.92 | 2.04 | null | null | null | null | null |
| 2 | 4 | download | 7070.0 | 5.000 | 1414.0 | 2.99 | 1.98 | null | null | null | null | null |
| 3 | 4 | download | 7250.4 | 5.002 | 1449.6 | 2.93 | 1.98 | null | null | null | null | null |
| 4 | 4 | download | 7377.2 | 5.000 | 1475.3 | 2.87 | 1.87 | null | null | null | null | null |
| 0 | 1 | upload | 1356.9 | 5.001 | 271.3 | 3.94 | 2.79 | null | null | null | null | null |
| 1 | 1 | upload | 1296.2 | 5.004 | 259.0 | 4.13 | 2.90 | null | null | null | null | null |
| 2 | 1 | upload | 1331.2 | 5.001 | 266.2 | 4.02 | 2.72 | null | null | null | null | null |
| 3 | 1 | upload | 1336.7 | 5.002 | 267.2 | 3.99 | 2.80 | null | null | null | null | null |
| 4 | 1 | upload | 1249.7 | 5.002 | 249.8 | 4.29 | 3.03 | null | null | null | null | null |
| 0 | 4 | upload | 3952.9 | 5.005 | 789.8 | 5.33 | 3.17 | null | null | null | null | null |
| 1 | 4 | upload | 4075.8 | 5.006 | 814.2 | 5.16 | 3.06 | null | null | null | null | null |
| 2 | 4 | upload | 4009.0 | 5.005 | 801.0 | 5.26 | 3.09 | null | null | null | null | null |
| 3 | 4 | upload | 4100.0 | 5.007 | 818.9 | 5.15 | 3.02 | null | null | null | null | null |
| 4 | 4 | upload | 4093.0 | 5.006 | 817.5 | 5.15 | 3.04 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.320 | 62.9 | 17 | 11 | 2.790 | 61.5 | 13 | 11 |
| 1 | 5.310 | 62.6 | 17 | 11 | 2.760 | 61.8 | 14 | 11 |
| 2 | 5.290 | 63.1 | 17 | 11 | 2.620 | 62.4 | 15 | 11 |
| 3 | 5.310 | 63.2 | 17 | 11 | 2.730 | 61.8 | 15 | 11 |
| 4 | 5.320 | 62.8 | 17 | 11 | 2.760 | 62.2 | 15 | 11 |
| 0 | 20.690 | 65.7 | 17 | 17 | 13.670 | 65.1 | 15 | 17 |
| 1 | 20.750 | 66.0 | 17 | 17 | 14.500 | 65.7 | 16 | 17 |
| 2 | 20.620 | 66.1 | 18 | 17 | 13.650 | 65.3 | 16 | 17 |
| 3 | 20.720 | 66.5 | 18 | 17 | 14.030 | 64.8 | 16 | 17 |
| 4 | 20.660 | 66.3 | 18 | 17 | 13.440 | 65.4 | 17 | 17 |
| 0 | 5.220 | 65.7 | 18 | 11 | 3.700 | 64.3 | 17 | 11 |
| 1 | 5.230 | 65.3 | 18 | 11 | 3.670 | 64.3 | 17 | 11 |
| 2 | 5.230 | 65.3 | 18 | 11 | 3.530 | 64.3 | 17 | 11 |
| 3 | 5.210 | 65.3 | 18 | 11 | 3.650 | 64.3 | 17 | 11 |
| 4 | 5.240 | 65.3 | 18 | 11 | 3.700 | 64.3 | 17 | 11 |
| 0 | 20.560 | 66.2 | 18 | 17 | 12.220 | 65.7 | 17 | 17 |
| 1 | 20.520 | 65.9 | 18 | 17 | 12.160 | 66.5 | 19 | 17 |
| 2 | 20.610 | 65.8 | 18 | 17 | 12.080 | 66.3 | 19 | 17 |
| 3 | 20.600 | 67.3 | 18 | 17 | 12.110 | 65.3 | 19 | 17 |
| 4 | 20.580 | 66.5 | 18 | 17 | 12.160 | 66.4 | 19 | 17 |
