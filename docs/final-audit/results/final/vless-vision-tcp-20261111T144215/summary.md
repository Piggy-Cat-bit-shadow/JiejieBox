# vless-vision-tcp - vless

- run_id: `vless-vision-tcp-20261111T144215`
- kind: e2e
- started: 2026-11-11T14:42:15+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 264.69 | 1.91 | 262.51 | 266.59 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 4.04 | 0.04 | 4.00 | 4.08 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 2.85 | 0.01 | 2.82 | 2.86 | null | s/GiB |
| goodput | 4 | upload | 3 | 809.14 | 19.76 | 760.03 | 828.90 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 5.21 | 0.15 | 5.07 | 5.54 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 3.07 | 0.06 | 3.01 | 3.27 | null | s/GiB |
| goodput | 1 | download | 3 | 542.26 | 36.81 | 505.45 | 586.67 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 2.01 | 0.14 | 1.86 | 2.16 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1.12 | 0.02 | 1.10 | 1.14 | null | s/GiB |
| goodput | 4 | download | 3 | 1408.96 | 15.19 | 1374.24 | 1424.15 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 3.00 | 0.03 | 2.98 | 3.09 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 2.24 | 0.10 | 2.07 | 2.34 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1334.0 | 5.004 | 266.6 | 4.00 | 2.82 | null | null | null | null | null |
| 1 | 1 | upload | 1324.1 | 5.002 | 264.7 | 4.04 | 2.86 | null | null | null | null | null |
| 2 | 1 | upload | 1312.8 | 5.001 | 262.5 | 4.08 | 2.85 | null | null | null | null | null |
| 0 | 4 | upload | 4051.5 | 5.007 | 809.1 | 5.21 | 3.07 | null | null | null | null | null |
| 1 | 4 | upload | 4150.1 | 5.007 | 828.9 | 5.07 | 3.01 | null | null | null | null | null |
| 2 | 4 | upload | 3803.7 | 5.005 | 760.0 | 5.54 | 3.27 | null | null | null | null | null |
| 0 | 1 | download | 2711.5 | 5.000 | 542.3 | 2.01 | 1.14 | null | null | null | null | null |
| 1 | 1 | download | 2527.5 | 5.000 | 505.5 | 2.16 | 1.12 | null | null | null | null | null |
| 2 | 1 | download | 2933.6 | 5.000 | 586.7 | 1.86 | 1.10 | null | null | null | null | null |
| 0 | 4 | download | 7045.8 | 5.001 | 1409.0 | 3.00 | 2.07 | null | null | null | null | null |
| 1 | 4 | download | 6871.7 | 5.000 | 1374.2 | 3.09 | 2.34 | null | null | null | null | null |
| 2 | 4 | download | 7121.1 | 5.000 | 1424.2 | 2.98 | 2.24 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.210 | 60.5 | 13 | 11 | 3.680 | 61.1 | 14 | 12 |
| 1 | 5.220 | 61.5 | 14 | 11 | 3.700 | 61.0 | 15 | 12 |
| 2 | 5.230 | 62.1 | 15 | 11 | 3.660 | 61.0 | 16 | 12 |
| 0 | 20.630 | 64.7 | 17 | 17 | 12.160 | 63.5 | 18 | 18 |
| 1 | 20.540 | 64.7 | 18 | 17 | 12.200 | 64.4 | 18 | 18 |
| 2 | 20.590 | 64.5 | 18 | 17 | 12.150 | 64.5 | 18 | 18 |
| 0 | 5.330 | 63.8 | 18 | 11 | 3.030 | 63.8 | 18 | 12 |
| 1 | 5.320 | 63.8 | 18 | 11 | 2.760 | 63.8 | 18 | 12 |
| 2 | 5.320 | 63.8 | 18 | 11 | 3.140 | 63.1 | 18 | 12 |
| 0 | 20.650 | 65.2 | 18 | 17 | 14.210 | 64.9 | 18 | 18 |
| 1 | 20.730 | 65.1 | 18 | 17 | 15.690 | 64.9 | 18 | 18 |
| 2 | 20.690 | 66.0 | 18 | 17 | 15.550 | 65.3 | 18 | 18 |
