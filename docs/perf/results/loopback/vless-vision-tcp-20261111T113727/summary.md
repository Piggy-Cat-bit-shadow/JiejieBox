# vless-vision-tcp - vless

- run_id: `vless-vision-tcp-20261111T113727`
- kind: e2e
- started: 2026-11-11T11:37:27+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 243.67 | 4.80 | 166.24 | 248.47 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 4.40 | 0.07 | 4.33 | 6.54 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 3.18 | 0.10 | 3.08 | 4.97 | null | s/GiB |
| goodput | 4 | upload | 5 | 608.64 | 87.17 | 497.69 | 700.83 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 6.97 | 0.90 | 6.04 | 8.52 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 4.35 | 0.63 | 3.65 | 5.59 | null | s/GiB |
| goodput | 1 | download | 5 | 532.48 | 17.34 | 436.32 | 572.45 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 2.05 | 0.06 | 1.89 | 2.81 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 1.13 | 0.02 | 1.10 | 2.47 | null | s/GiB |
| goodput | 4 | download | 5 | 1372.79 | 9.86 | 1362.71 | 1399.02 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 3.10 | 0.01 | 3.03 | 3.11 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 2.13 | 0.04 | 2.07 | 2.35 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1218.7 | 5.001 | 243.7 | 4.40 | 3.18 | null | null | null | null | null |
| 1 | 1 | upload | 1232.3 | 5.004 | 246.3 | 4.34 | 3.18 | null | null | null | null | null |
| 2 | 1 | upload | 1134.9 | 5.001 | 226.9 | 4.75 | 3.43 | null | null | null | null | null |
| 3 | 1 | upload | 1243.6 | 5.005 | 248.5 | 4.33 | 3.08 | null | null | null | null | null |
| 4 | 1 | upload | 832.5 | 5.008 | 166.2 | 6.54 | 4.97 | null | null | null | null | null |
| 0 | 4 | upload | 2493.4 | 5.010 | 497.7 | 8.52 | 5.59 | null | null | null | null | null |
| 1 | 4 | upload | 2913.6 | 5.008 | 581.8 | 7.26 | 4.67 | null | null | null | null | null |
| 2 | 4 | upload | 3046.3 | 5.005 | 608.6 | 6.97 | 4.35 | null | null | null | null | null |
| 3 | 4 | upload | 3510.0 | 5.008 | 700.8 | 6.04 | 3.65 | null | null | null | null | null |
| 4 | 4 | upload | 3483.6 | 5.007 | 695.8 | 6.06 | 3.72 | null | null | null | null | null |
| 0 | 1 | download | 2685.8 | 5.000 | 537.2 | 2.03 | 1.15 | null | null | null | null | null |
| 1 | 1 | download | 2862.4 | 5.000 | 572.5 | 1.89 | 1.10 | null | null | null | null | null |
| 2 | 1 | download | 2575.8 | 5.000 | 515.1 | 2.11 | 1.11 | null | null | null | null | null |
| 3 | 1 | download | 2181.7 | 5.000 | 436.3 | 2.81 | 2.47 | null | null | null | null | null |
| 4 | 1 | download | 2662.4 | 5.000 | 532.5 | 2.05 | 1.13 | null | null | null | null | null |
| 0 | 4 | download | 6813.8 | 5.000 | 1362.7 | 3.11 | 2.17 | null | null | null | null | null |
| 1 | 4 | download | 6995.4 | 5.000 | 1399.0 | 3.03 | 2.07 | null | null | null | null | null |
| 2 | 4 | download | 6815.0 | 5.000 | 1362.9 | 3.10 | 2.13 | null | null | null | null | null |
| 3 | 4 | download | 6865.9 | 5.001 | 1372.8 | 3.10 | 2.35 | null | null | null | null | null |
| 4 | 4 | download | 6871.9 | 5.000 | 1374.3 | 3.08 | 2.12 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.240 | 60.5 | 13 | 12 | 3.790 | 61.6 | 15 | 13 |
| 1 | 5.220 | 62.1 | 14 | 12 | 3.830 | 61.7 | 15 | 13 |
| 2 | 5.270 | 62.6 | 15 | 12 | 3.800 | 62.2 | 16 | 13 |
| 3 | 5.260 | 62.3 | 15 | 12 | 3.740 | 62.6 | 16 | 13 |
| 4 | 5.320 | 63.0 | 16 | 12 | 4.040 | 62.7 | 16 | 13 |
| 0 | 20.750 | 64.5 | 17 | 18 | 13.600 | 64.1 | 16 | 19 |
| 1 | 20.670 | 64.3 | 17 | 18 | 13.280 | 64.4 | 18 | 19 |
| 2 | 20.720 | 65.5 | 18 | 18 | 12.950 | 66.2 | 18 | 19 |
| 3 | 20.710 | 65.8 | 18 | 18 | 12.520 | 64.9 | 18 | 19 |
| 4 | 20.620 | 65.4 | 19 | 18 | 12.650 | 65.5 | 18 | 19 |
| 0 | 5.330 | 65.4 | 19 | 12 | 3.020 | 64.8 | 18 | 13 |
| 1 | 5.280 | 65.4 | 19 | 12 | 3.070 | 64.7 | 18 | 13 |
| 2 | 5.310 | 65.4 | 19 | 12 | 2.800 | 64.7 | 18 | 13 |
| 3 | 5.980 | 65.4 | 19 | 12 | 5.260 | 64.7 | 18 | 13 |
| 4 | 5.320 | 65.4 | 19 | 12 | 2.950 | 64.7 | 18 | 13 |
| 0 | 20.710 | 65.4 | 19 | 18 | 14.410 | 65.6 | 18 | 19 |
| 1 | 20.680 | 66.5 | 19 | 18 | 14.120 | 66.3 | 18 | 19 |
| 2 | 20.660 | 66.4 | 19 | 18 | 14.150 | 66.8 | 18 | 19 |
| 3 | 20.800 | 65.6 | 19 | 18 | 15.750 | 66.6 | 18 | 19 |
| 4 | 20.660 | 66.1 | 19 | 18 | 14.230 | 66.6 | 18 | 19 |
