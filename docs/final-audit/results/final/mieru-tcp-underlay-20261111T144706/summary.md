# mieru-tcp-underlay - mieru

- run_id: `mieru-tcp-underlay-20261111T144706`
- kind: e2e
- started: 2026-11-11T14:47:06+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 128.86 | 2.73 | 126.13 | 132.31 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 8.66 | 0.13 | 8.53 | 9.02 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 6.47 | 0.22 | 6.24 | 6.95 | null | s/GiB |
| goodput | 4 | upload | 3 | 326.78 | 31.08 | 255.41 | 357.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 13.74 | 1.74 | 12.00 | 16.91 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 8.86 | 1.26 | 7.60 | 10.17 | null | s/GiB |
| goodput | 1 | download | 3 | 115.18 | 6.73 | 108.45 | 143.08 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 6.54 | 0.48 | 6.06 | 8.61 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 6.24 | 0.20 | 6.04 | 7.47 | null | s/GiB |
| goodput | 4 | download | 3 | 616.16 | 7.48 | 608.68 | 644.13 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 7.66 | 0.09 | 7.48 | 7.75 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 7.03 | 0.01 | 7.02 | 7.27 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 2.52 | 0.02 | 2.50 | 2.55 | null | ms |
| latency_p95 | 1 | connect | 3 | 3.15 | 0.04 | 3.11 | 3.65 | null | ms |
| latency_p99 | 1 | connect | 3 | 3.47 | 0.07 | 3.40 | 4.30 | null | ms |
| latency_p50 | 4 | connect | 3 | 7.54 | 0.07 | 3.52 | 7.62 | null | ms |
| latency_p95 | 4 | connect | 3 | 8.98 | 0.63 | 4.99 | 9.62 | null | ms |
| latency_p99 | 4 | connect | 3 | 9.78 | 1.68 | 6.88 | 11.47 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 662.0 | 5.004 | 132.3 | 9.02 | 6.95 | null | null | null | null | null |
| 1 | 1 | upload | 644.4 | 5.000 | 128.9 | 8.66 | 6.47 | null | null | null | null | null |
| 2 | 1 | upload | 631.4 | 5.006 | 126.1 | 8.53 | 6.24 | null | null | null | null | null |
| 0 | 4 | upload | 1288.1 | 5.043 | 255.4 | 16.91 | 10.17 | null | null | null | null | null |
| 1 | 4 | upload | 1639.6 | 5.018 | 326.8 | 13.74 | 8.86 | null | null | null | null | null |
| 2 | 4 | upload | 1793.2 | 5.011 | 357.9 | 12.00 | 7.60 | null | null | null | null | null |
| 0 | 1 | download | 715.6 | 5.001 | 143.1 | 8.61 | 7.47 | null | null | null | null | null |
| 1 | 1 | download | 575.9 | 5.000 | 115.2 | 6.54 | 6.24 | null | null | null | null | null |
| 2 | 1 | download | 542.4 | 5.001 | 108.5 | 6.06 | 6.04 | null | null | null | null | null |
| 0 | 4 | download | 3044.1 | 5.001 | 608.7 | 7.48 | 7.02 | null | null | null | null | null |
| 1 | 4 | download | 3223.6 | 5.004 | 644.1 | 7.66 | 7.03 | null | null | null | null | null |
| 2 | 4 | download | 3081.3 | 5.001 | 616.2 | 7.75 | 7.27 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 2.55 | 3.65 | 4.30 |
| 1 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.50 | 3.11 | 3.40 |
| 2 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 2.52 | 3.15 | 3.47 |
| 0 | 4 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.52 | 4.99 | 6.88 |
| 1 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 7.54 | 8.98 | 9.78 |
| 2 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 7.62 | 9.62 | 11.47 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.830 | 62.6 | 18 | 12 | 4.490 | 65.9 | 17 | 12 |
| 1 | 5.450 | 62.9 | 18 | 12 | 4.070 | 69.6 | 18 | 12 |
| 2 | 5.260 | 63.1 | 18 | 14 | 3.850 | 76.6 | 19 | 14 |
| 0 | 21.270 | 80.7 | 19 | 19 | 12.790 | 101.9 | 20 | 19 |
| 1 | 22.000 | 79.6 | 19 | 20 | 14.180 | 103.0 | 21 | 20 |
| 2 | 21.010 | 79.6 | 19 | 20 | 13.310 | 124.2 | 21 | 20 |
| 0 | 6.020 | 79.6 | 19 | 17 | 5.220 | 123.2 | 21 | 17 |
| 1 | 3.680 | 79.6 | 19 | 18 | 3.510 | 123.2 | 21 | 18 |
| 2 | 3.210 | 79.6 | 19 | 19 | 3.200 | 123.2 | 21 | 19 |
| 0 | 22.230 | 113.0 | 19 | 23 | 20.860 | 129.7 | 21 | 23 |
| 1 | 24.120 | 147.0 | 20 | 28 | 22.130 | 130.5 | 21 | 28 |
| 2 | 23.320 | 149.5 | 20 | 28 | 21.880 | 133.5 | 21 | 28 |
| 0 | 5.050 | 147.8 | 21 | 91 | 4.340 | 132.1 | 21 | 90 |
| 1 | 5.060 | 147.8 | 21 | 114 | 4.350 | 133.3 | 21 | 113 |
| 2 | 5.050 | 151.7 | 21 | 136 | 4.320 | 134.0 | 21 | 135 |
| 0 | 14.300 | 206.5 | 21 | 189 | 10.130 | 158.3 | 21 | 187 |
| 1 | 7.150 | 215.5 | 21 | 211 | 16.520 | 163.5 | 21 | 211 |
| 2 | 6.920 | 216.6 | 21 | 233 | 16.580 | 161.8 | 23 | 230 |
