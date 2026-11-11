# mieru-tcp-underlay - mieru

- run_id: `mieru-tcp-underlay-20261111T150534`
- kind: e2e
- started: 2026-11-11T15:05:34+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 122.57 | 0.63 | 121.55 | 124.88 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 8.08 | 0.10 | 7.98 | 8.30 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 5.90 | 0.02 | 5.83 | 6.35 | null | s/GiB |
| goodput | 4 | upload | 5 | 483.07 | 1.17 | 471.51 | 490.23 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 9.63 | 0.01 | 9.51 | 9.83 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 6.32 | 0.15 | 6.07 | 6.68 | null | s/GiB |
| goodput | 1 | download | 5 | 103.45 | 0.33 | 102.04 | 103.78 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 5.64 | 0.20 | 5.44 | 5.99 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 5.59 | 0.07 | 5.48 | 5.68 | null | s/GiB |
| goodput | 4 | download | 5 | 689.43 | 16.16 | 602.46 | 707.30 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 7.12 | 0.24 | 5.82 | 7.35 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 6.41 | 0.05 | 5.96 | 6.55 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 624.4 | 5.000 | 124.9 | 8.30 | 6.35 | null | null | null | null | null |
| 1 | 1 | upload | 608.6 | 5.007 | 121.6 | 7.99 | 5.91 | null | null | null | null | null |
| 2 | 1 | upload | 616.3 | 5.002 | 123.2 | 7.98 | 5.83 | null | null | null | null | null |
| 3 | 1 | upload | 612.9 | 5.001 | 122.6 | 8.19 | 5.88 | null | null | null | null | null |
| 4 | 1 | upload | 610.8 | 5.006 | 122.0 | 8.08 | 5.90 | null | null | null | null | null |
| 0 | 4 | upload | 2362.0 | 5.010 | 471.5 | 9.83 | 6.68 | null | null | null | null | null |
| 1 | 4 | upload | 2416.2 | 5.008 | 482.4 | 9.63 | 6.38 | null | null | null | null | null |
| 2 | 4 | upload | 2419.8 | 5.009 | 483.1 | 9.64 | 6.17 | null | null | null | null | null |
| 3 | 4 | upload | 2453.9 | 5.006 | 490.2 | 9.51 | 6.32 | null | null | null | null | null |
| 4 | 4 | upload | 2424.3 | 5.006 | 484.2 | 9.63 | 6.07 | null | null | null | null | null |
| 0 | 1 | download | 518.0 | 5.000 | 103.6 | 5.99 | 5.63 | null | null | null | null | null |
| 1 | 1 | download | 518.9 | 5.000 | 103.8 | 5.64 | 5.53 | null | null | null | null | null |
| 2 | 1 | download | 517.4 | 5.001 | 103.5 | 5.84 | 5.68 | null | null | null | null | null |
| 3 | 1 | download | 510.3 | 5.000 | 102.0 | 5.44 | 5.48 | null | null | null | null | null |
| 4 | 1 | download | 510.9 | 5.000 | 102.2 | 5.49 | 5.59 | null | null | null | null | null |
| 0 | 4 | download | 3528.7 | 5.001 | 705.6 | 7.33 | 6.41 | null | null | null | null | null |
| 1 | 4 | download | 3536.9 | 5.001 | 707.3 | 7.12 | 6.46 | null | null | null | null | null |
| 2 | 4 | download | 3448.2 | 5.002 | 689.4 | 7.35 | 6.55 | null | null | null | null | null |
| 3 | 4 | download | 3404.2 | 5.001 | 680.7 | 6.69 | 6.38 | null | null | null | null | null |
| 4 | 4 | download | 3012.9 | 5.001 | 602.5 | 5.82 | 5.96 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.060 | 62.4 | 17 | 12 | 3.870 | 66.1 | 17 | 12 |
| 1 | 4.750 | 62.5 | 17 | 14 | 3.510 | 69.8 | 18 | 14 |
| 2 | 4.800 | 62.9 | 17 | 14 | 3.510 | 76.7 | 19 | 14 |
| 3 | 4.900 | 62.8 | 17 | 15 | 3.520 | 77.3 | 20 | 15 |
| 4 | 4.820 | 63.1 | 18 | 16 | 3.520 | 89.0 | 20 | 16 |
| 0 | 22.670 | 69.7 | 19 | 20 | 15.420 | 98.3 | 20 | 20 |
| 1 | 22.720 | 71.6 | 20 | 22 | 15.060 | 125.7 | 21 | 22 |
| 2 | 22.780 | 72.1 | 20 | 23 | 14.580 | 125.1 | 21 | 23 |
| 3 | 22.790 | 74.1 | 20 | 27 | 15.140 | 177.9 | 22 | 27 |
| 4 | 22.790 | 74.1 | 20 | 27 | 14.380 | 182.9 | 22 | 27 |
| 0 | 3.030 | 74.1 | 20 | 25 | 2.850 | 181.5 | 22 | 25 |
| 1 | 2.860 | 74.1 | 20 | 25 | 2.800 | 181.5 | 22 | 25 |
| 2 | 2.950 | 78.4 | 20 | 27 | 2.870 | 181.5 | 22 | 27 |
| 3 | 2.710 | 79.7 | 20 | 27 | 2.730 | 181.5 | 22 | 27 |
| 4 | 2.740 | 80.1 | 20 | 27 | 2.790 | 181.5 | 22 | 27 |
| 0 | 25.270 | 128.3 | 20 | 32 | 22.090 | 189.6 | 23 | 32 |
| 1 | 24.580 | 144.2 | 21 | 35 | 22.330 | 188.7 | 23 | 35 |
| 2 | 24.760 | 201.2 | 21 | 36 | 22.040 | 188.1 | 23 | 36 |
| 3 | 22.230 | 199.8 | 21 | 36 | 21.210 | 188.1 | 23 | 36 |
| 4 | 17.120 | 205.8 | 21 | 40 | 17.530 | 188.1 | 23 | 40 |
