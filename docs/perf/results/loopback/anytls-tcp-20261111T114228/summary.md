# anytls-tcp - anytls

- run_id: `anytls-tcp-20261111T114228`
- kind: e2e
- started: 2026-11-11T11:42:28+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 255.36 | 3.33 | 250.40 | 259.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 4.19 | 0.06 | 4.10 | 4.30 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 3.52 | 0.05 | 3.47 | 3.68 | null | s/GiB |
| goodput | 4 | upload | 5 | 750.25 | 3.17 | 737.65 | 756.14 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 5.60 | 0.03 | 5.57 | 5.73 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 3.80 | 0.03 | 3.74 | 3.83 | null | s/GiB |
| goodput | 1 | download | 5 | 557.17 | 6.64 | 549.34 | 728.71 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 2.82 | 0.07 | 2.34 | 2.91 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 1.91 | 0.02 | 1.24 | 1.93 | null | s/GiB |
| goodput | 4 | download | 5 | 1198.19 | 6.13 | 1167.28 | 1204.31 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 4.20 | 0.09 | 4.10 | 4.29 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 3.43 | 0.01 | 3.26 | 3.54 | null | s/GiB |
| goodput | 1 | udp | 5 | 23.88 | 0.29 | 23.09 | 24.17 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 5 | 102.08 | 1.48 | 100.60 | 105.65 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 5 | 99.93 | 1.20 | 98.73 | 103.43 | null | s/GiB |
| goodput | 4 | udp | 5 | 22.29 | 0.16 | 21.08 | 22.68 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 5 | 108.55 | 0.62 | 106.75 | 115.72 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 5 | 106.34 | 0.60 | 104.94 | 113.97 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1293.7 | 5.001 | 258.7 | 4.13 | 3.47 | null | null | null | null | null |
| 1 | 1 | upload | 1252.5 | 5.002 | 250.4 | 4.30 | 3.68 | null | null | null | null | null |
| 2 | 1 | upload | 1300.0 | 5.003 | 259.9 | 4.10 | 3.48 | null | null | null | null | null |
| 3 | 1 | upload | 1267.8 | 5.002 | 253.5 | 4.23 | 3.52 | null | null | null | null | null |
| 4 | 1 | upload | 1277.2 | 5.002 | 255.4 | 4.19 | 3.57 | null | null | null | null | null |
| 0 | 4 | upload | 3744.2 | 5.006 | 747.9 | 5.64 | 3.80 | null | null | null | null | null |
| 1 | 4 | upload | 3784.1 | 5.004 | 756.1 | 5.57 | 3.74 | null | null | null | null | null |
| 2 | 4 | upload | 3770.8 | 5.005 | 753.4 | 5.58 | 3.77 | null | null | null | null | null |
| 3 | 4 | upload | 3690.2 | 5.003 | 737.6 | 5.73 | 3.83 | null | null | null | null | null |
| 4 | 4 | upload | 3755.8 | 5.006 | 750.3 | 5.60 | 3.82 | null | null | null | null | null |
| 0 | 1 | download | 3643.9 | 5.000 | 728.7 | 2.34 | 1.24 | null | null | null | null | null |
| 1 | 1 | download | 2746.8 | 5.000 | 549.3 | 2.89 | 1.91 | null | null | null | null | null |
| 2 | 1 | download | 2819.1 | 5.000 | 563.8 | 2.80 | 1.93 | null | null | null | null | null |
| 3 | 1 | download | 2755.8 | 5.000 | 551.1 | 2.91 | 1.93 | null | null | null | null | null |
| 4 | 1 | download | 2786.0 | 5.000 | 557.2 | 2.82 | 1.91 | null | null | null | null | null |
| 0 | 4 | download | 6022.7 | 5.001 | 1204.3 | 4.29 | 3.26 | null | null | null | null | null |
| 1 | 4 | download | 5887.9 | 5.001 | 1177.4 | 4.25 | 3.42 | null | null | null | null | null |
| 2 | 4 | download | 5992.5 | 5.001 | 1198.2 | 4.10 | 3.43 | null | null | null | null | null |
| 3 | 4 | download | 5836.9 | 5.000 | 1167.3 | 4.20 | 3.54 | null | null | null | null | null |
| 4 | 4 | download | 6002.8 | 5.001 | 1200.3 | 4.10 | 3.43 | null | null | null | null | null |
| 0 | 1 | udp | 119.4 | 5.000 | 23.9 | 102.08 | 99.93 | null | null | null | null | null |
| 1 | 1 | udp | 120.4 | 5.000 | 24.1 | 100.95 | 99.25 | null | null | null | null | null |
| 2 | 1 | udp | 120.8 | 5.000 | 24.2 | 100.60 | 98.73 | null | null | null | null | null |
| 3 | 1 | udp | 115.4 | 5.000 | 23.1 | 105.65 | 103.43 | null | null | null | null | null |
| 4 | 1 | udp | 117.4 | 5.000 | 23.5 | 103.73 | 101.99 | null | null | null | null | null |
| 0 | 4 | udp | 111.2 | 5.000 | 22.2 | 108.55 | 106.34 | null | null | null | null | null |
| 1 | 4 | udp | 113.4 | 5.000 | 22.7 | 106.75 | 104.94 | null | null | null | null | null |
| 2 | 4 | udp | 112.2 | 5.000 | 22.4 | 107.93 | 105.74 | null | null | null | null | null |
| 3 | 4 | udp | 111.4 | 5.000 | 22.3 | 108.80 | 106.87 | null | null | null | null | null |
| 4 | 4 | udp | 105.4 | 5.000 | 21.1 | 115.72 | 113.97 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.220 | 60.1 | 12 | 13 | 4.390 | 61.9 | 13 | 14 |
| 1 | 5.260 | 61.0 | 12 | 13 | 4.500 | 62.2 | 13 | 14 |
| 2 | 5.210 | 61.8 | 13 | 13 | 4.420 | 62.9 | 14 | 14 |
| 3 | 5.240 | 62.0 | 14 | 13 | 4.360 | 63.2 | 15 | 14 |
| 4 | 5.230 | 62.5 | 15 | 13 | 4.450 | 63.0 | 15 | 14 |
| 0 | 20.620 | 63.3 | 16 | 22 | 13.880 | 65.9 | 17 | 23 |
| 1 | 20.600 | 64.5 | 17 | 22 | 13.820 | 65.8 | 17 | 23 |
| 2 | 20.560 | 64.9 | 18 | 22 | 13.890 | 67.4 | 20 | 23 |
| 3 | 20.640 | 64.8 | 18 | 22 | 13.820 | 67.1 | 20 | 23 |
| 4 | 20.550 | 64.9 | 18 | 22 | 14.020 | 68.0 | 20 | 23 |
| 0 | 8.310 | 65.7 | 18 | 19 | 4.420 | 66.6 | 20 | 20 |
| 1 | 7.760 | 64.4 | 18 | 19 | 5.110 | 66.6 | 20 | 20 |
| 2 | 7.720 | 64.6 | 18 | 19 | 5.310 | 66.6 | 20 | 20 |
| 3 | 7.840 | 64.6 | 18 | 19 | 5.190 | 66.6 | 20 | 20 |
| 4 | 7.670 | 64.8 | 18 | 19 | 5.200 | 66.6 | 20 | 20 |
| 0 | 25.230 | 67.6 | 18 | 19 | 19.180 | 66.6 | 20 | 20 |
| 1 | 24.450 | 68.1 | 18 | 19 | 19.680 | 66.6 | 20 | 20 |
| 2 | 24.010 | 68.0 | 18 | 19 | 20.090 | 66.6 | 20 | 20 |
| 3 | 23.960 | 68.1 | 18 | 19 | 20.200 | 66.6 | 20 | 20 |
| 4 | 24.040 | 68.5 | 18 | 19 | 20.130 | 66.6 | 20 | 20 |
| 0 | 11.900 | 67.2 | 18 | 16 | 11.650 | 66.6 | 20 | 16 |
| 1 | 11.870 | 67.2 | 18 | 16 | 11.670 | 66.6 | 20 | 16 |
| 2 | 11.870 | 67.2 | 18 | 16 | 11.650 | 66.6 | 20 | 16 |
| 3 | 11.910 | 67.2 | 18 | 16 | 11.660 | 66.6 | 20 | 16 |
| 4 | 11.890 | 67.2 | 18 | 16 | 11.690 | 66.6 | 20 | 16 |
| 0 | 11.790 | 67.2 | 18 | 15 | 11.550 | 66.6 | 20 | 15 |
| 1 | 11.820 | 67.2 | 18 | 15 | 11.620 | 66.6 | 20 | 15 |
| 2 | 11.830 | 67.2 | 18 | 15 | 11.590 | 66.6 | 20 | 15 |
| 3 | 11.840 | 67.2 | 18 | 15 | 11.630 | 66.6 | 20 | 15 |
| 4 | 11.910 | 67.2 | 18 | 15 | 11.730 | 66.6 | 20 | 15 |
