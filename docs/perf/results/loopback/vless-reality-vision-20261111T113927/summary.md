# vless-reality-vision - vless

- run_id: `vless-reality-vision-20261111T113927`
- kind: e2e
- started: 2026-11-11T11:39:27+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 255.20 | 2.89 | 252.31 | 271.84 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 4.21 | 0.05 | 3.92 | 4.26 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 2.98 | 0.08 | 2.71 | 3.05 | null | s/GiB |
| goodput | 4 | upload | 5 | 782.79 | 5.51 | 777.29 | 800.89 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 5.38 | 0.04 | 5.27 | 5.42 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 3.19 | 0.01 | 3.12 | 3.24 | null | s/GiB |
| goodput | 1 | download | 5 | 542.79 | 14.92 | 527.87 | 565.99 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 2.00 | 0.05 | 1.93 | 2.06 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 1.05 | 0.01 | 1.04 | 1.08 | null | s/GiB |
| goodput | 4 | download | 5 | 1365.09 | 10.38 | 1354.71 | 1400.00 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 3.09 | 0.03 | 3.03 | 3.12 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 2.07 | 0.03 | 2.00 | 2.10 | null | s/GiB |
| latency_p50 | 1 | connect | 5 | 4.09 | 0.02 | 4.07 | 4.16 | null | ms |
| latency_p95 | 1 | connect | 5 | 4.88 | 0.05 | 4.83 | 5.18 | null | ms |
| latency_p99 | 1 | connect | 5 | 5.27 | 0.03 | 5.24 | 6.21 | null | ms |
| latency_p50 | 4 | connect | 5 | 9.99 | 0.91 | 5.91 | 10.90 | null | ms |
| latency_p95 | 4 | connect | 5 | 11.43 | 1.60 | 7.42 | 13.03 | null | ms |
| latency_p99 | 4 | connect | 5 | 13.13 | 1.01 | 8.05 | 14.26 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1261.7 | 5.001 | 252.3 | 4.26 | 3.04 | null | null | null | null | null |
| 1 | 1 | upload | 1271.6 | 5.000 | 254.3 | 4.23 | 3.05 | null | null | null | null | null |
| 2 | 1 | upload | 1276.4 | 5.002 | 255.2 | 4.21 | 2.98 | null | null | null | null | null |
| 3 | 1 | upload | 1359.5 | 5.001 | 271.8 | 3.92 | 2.71 | null | null | null | null | null |
| 4 | 1 | upload | 1316.9 | 5.001 | 263.3 | 4.06 | 2.77 | null | null | null | null | null |
| 0 | 4 | upload | 4010.0 | 5.007 | 800.9 | 5.27 | 3.12 | null | null | null | null | null |
| 1 | 4 | upload | 3917.1 | 5.004 | 782.8 | 5.38 | 3.19 | null | null | null | null | null |
| 2 | 4 | upload | 3890.6 | 5.005 | 777.3 | 5.42 | 3.24 | null | null | null | null | null |
| 3 | 4 | upload | 3961.5 | 5.005 | 791.5 | 5.32 | 3.18 | null | null | null | null | null |
| 4 | 4 | upload | 3904.6 | 5.004 | 780.4 | 5.42 | 3.19 | null | null | null | null | null |
| 0 | 1 | download | 2815.6 | 5.000 | 563.1 | 1.94 | 1.04 | null | null | null | null | null |
| 1 | 1 | download | 2692.2 | 5.000 | 538.4 | 2.02 | 1.05 | null | null | null | null | null |
| 2 | 1 | download | 2830.0 | 5.000 | 566.0 | 1.93 | 1.05 | null | null | null | null | null |
| 3 | 1 | download | 2714.0 | 5.000 | 542.8 | 2.00 | 1.04 | null | null | null | null | null |
| 4 | 1 | download | 2639.4 | 5.000 | 527.9 | 2.06 | 1.08 | null | null | null | null | null |
| 0 | 4 | download | 6775.3 | 5.001 | 1354.7 | 3.12 | 2.10 | null | null | null | null | null |
| 1 | 4 | download | 6782.9 | 5.001 | 1356.4 | 3.12 | 2.10 | null | null | null | null | null |
| 2 | 4 | download | 7000.2 | 5.000 | 1400.0 | 3.03 | 2.00 | null | null | null | null | null |
| 3 | 4 | download | 6826.5 | 5.001 | 1365.1 | 3.09 | 2.07 | null | null | null | null | null |
| 4 | 4 | download | 6926.3 | 5.000 | 1385.2 | 3.07 | 2.03 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 4.16 | 5.00 | 5.37 |
| 1 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 4.09 | 4.83 | 5.27 |
| 2 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 4.16 | 5.18 | 6.21 |
| 3 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 4.09 | 4.88 | 5.25 |
| 4 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 4.07 | 4.84 | 5.24 |
| 0 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 5.91 | 7.42 | 8.05 |
| 1 | 4 | connect | 0.0 | 5.012 | null | null | null | null | null | 5.97 | 8.80 | 13.13 |
| 2 | 4 | connect | 0.0 | 5.010 | null | null | null | null | null | 10.90 | 13.03 | 14.26 |
| 3 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 9.99 | 11.43 | 12.12 |
| 4 | 4 | connect | 0.0 | 5.008 | null | null | null | null | null | 10.21 | 12.07 | 13.13 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.250 | 62.1 | 13 | 12 | 3.750 | 62.3 | 15 | 12 |
| 1 | 5.250 | 62.5 | 14 | 12 | 3.790 | 62.4 | 16 | 12 |
| 2 | 5.250 | 62.7 | 14 | 12 | 3.710 | 63.0 | 16 | 12 |
| 3 | 5.200 | 62.9 | 14 | 12 | 3.600 | 63.0 | 16 | 12 |
| 4 | 5.220 | 63.0 | 15 | 12 | 3.560 | 62.6 | 16 | 12 |
| 0 | 20.630 | 66.8 | 16 | 18 | 12.210 | 66.2 | 18 | 18 |
| 1 | 20.580 | 67.4 | 17 | 18 | 12.190 | 65.5 | 18 | 18 |
| 2 | 20.600 | 65.9 | 17 | 18 | 12.320 | 65.2 | 18 | 18 |
| 3 | 20.590 | 66.5 | 17 | 18 | 12.300 | 66.2 | 19 | 18 |
| 4 | 20.650 | 67.1 | 18 | 18 | 12.160 | 65.1 | 19 | 18 |
| 0 | 5.340 | 65.9 | 18 | 12 | 2.870 | 65.1 | 19 | 12 |
| 1 | 5.300 | 65.9 | 18 | 12 | 2.760 | 65.1 | 19 | 12 |
| 2 | 5.330 | 65.9 | 18 | 12 | 2.900 | 65.1 | 19 | 12 |
| 3 | 5.310 | 65.9 | 18 | 12 | 2.760 | 65.1 | 19 | 12 |
| 4 | 5.300 | 65.9 | 18 | 12 | 2.780 | 65.1 | 19 | 12 |
| 0 | 20.670 | 67.1 | 18 | 18 | 13.900 | 66.9 | 19 | 18 |
| 1 | 20.680 | 67.0 | 18 | 18 | 13.940 | 67.3 | 19 | 18 |
| 2 | 20.720 | 67.7 | 18 | 18 | 13.650 | 66.6 | 19 | 18 |
| 3 | 20.620 | 67.9 | 18 | 18 | 13.810 | 66.0 | 19 | 18 |
| 4 | 20.740 | 67.3 | 18 | 18 | 13.750 | 66.6 | 19 | 18 |
| 0 | 3.900 | 66.5 | 18 | 12 | 3.990 | 65.8 | 19 | 12 |
| 1 | 3.880 | 66.5 | 18 | 12 | 3.950 | 65.8 | 19 | 12 |
| 2 | 3.870 | 66.5 | 18 | 12 | 3.990 | 66.0 | 19 | 12 |
| 3 | 3.870 | 67.0 | 18 | 12 | 3.960 | 66.0 | 19 | 12 |
| 4 | 3.870 | 66.9 | 18 | 12 | 3.960 | 66.1 | 19 | 12 |
| 0 | 11.660 | 69.4 | 18 | 18 | 11.300 | 68.2 | 20 | 18 |
| 1 | 11.070 | 69.7 | 19 | 18 | 11.690 | 68.3 | 20 | 18 |
| 2 | 6.670 | 69.9 | 19 | 18 | 15.330 | 68.7 | 20 | 18 |
| 3 | 6.590 | 70.0 | 19 | 18 | 15.110 | 68.7 | 20 | 18 |
| 4 | 6.680 | 70.4 | 19 | 18 | 15.280 | 68.8 | 20 | 18 |
