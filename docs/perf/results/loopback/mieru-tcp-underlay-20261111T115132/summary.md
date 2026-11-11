# mieru-tcp-underlay - mieru

- run_id: `mieru-tcp-underlay-20261111T115132`
- kind: e2e
- started: 2026-11-11T11:51:32+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 126.50 | 1.32 | 125.18 | 130.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 8.75 | 0.11 | 8.60 | 9.03 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 6.46 | 0.11 | 6.35 | 6.76 | null | s/GiB |
| goodput | 4 | upload | 5 | 438.46 | 3.41 | 421.32 | 442.72 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 10.79 | 0.06 | 10.70 | 11.40 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 7.15 | 0.11 | 7.00 | 7.74 | null | s/GiB |
| goodput | 1 | download | 5 | 124.50 | 8.34 | 112.92 | 132.84 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 7.07 | 0.74 | 6.33 | 8.93 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 6.64 | 0.58 | 6.06 | 8.25 | null | s/GiB |
| goodput | 4 | download | 5 | 570.86 | 34.15 | 510.17 | 615.37 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 7.55 | 0.79 | 6.57 | 8.41 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 7.25 | 0.20 | 6.91 | 8.01 | null | s/GiB |
| latency_p50 | 1 | connect | 5 | 2.43 | 0.08 | 2.33 | 2.52 | null | ms |
| latency_p95 | 1 | connect | 5 | 3.19 | 0.13 | 3.01 | 3.32 | null | ms |
| latency_p99 | 1 | connect | 5 | 3.61 | 0.22 | 3.38 | 3.93 | null | ms |
| latency_p50 | 4 | connect | 5 | 7.99 | 0.36 | 6.75 | 8.47 | null | ms |
| latency_p95 | 4 | connect | 5 | 10.79 | 0.79 | 8.66 | 15.44 | null | ms |
| latency_p99 | 4 | connect | 5 | 13.08 | 1.85 | 9.85 | 26.78 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 626.2 | 5.002 | 125.2 | 8.60 | 6.59 | null | null | null | null | null |
| 1 | 1 | upload | 633.0 | 5.003 | 126.5 | 9.03 | 6.76 | null | null | null | null | null |
| 2 | 1 | upload | 633.6 | 5.003 | 126.6 | 8.73 | 6.46 | null | null | null | null | null |
| 3 | 1 | upload | 626.2 | 5.002 | 125.2 | 8.86 | 6.46 | null | null | null | null | null |
| 4 | 1 | upload | 653.1 | 5.002 | 130.6 | 8.75 | 6.35 | null | null | null | null | null |
| 0 | 4 | upload | 2190.3 | 5.010 | 437.2 | 10.79 | 7.05 | null | null | null | null | null |
| 1 | 4 | upload | 2218.0 | 5.010 | 442.7 | 10.70 | 7.15 | null | null | null | null | null |
| 2 | 4 | upload | 2195.5 | 5.007 | 438.5 | 10.85 | 7.16 | null | null | null | null | null |
| 3 | 4 | upload | 2110.0 | 5.008 | 421.3 | 11.40 | 7.74 | null | null | null | null | null |
| 4 | 4 | upload | 2212.5 | 5.007 | 441.9 | 10.73 | 7.00 | null | null | null | null | null |
| 0 | 1 | download | 564.7 | 5.000 | 112.9 | 6.87 | 6.29 | null | null | null | null | null |
| 1 | 1 | download | 567.4 | 5.000 | 113.5 | 6.33 | 6.06 | null | null | null | null | null |
| 2 | 1 | download | 664.2 | 5.000 | 132.8 | 8.93 | 8.25 | null | null | null | null | null |
| 3 | 1 | download | 652.0 | 5.000 | 130.4 | 8.40 | 7.82 | null | null | null | null | null |
| 4 | 1 | download | 622.7 | 5.001 | 124.5 | 7.07 | 6.64 | null | null | null | null | null |
| 0 | 4 | download | 2854.8 | 5.001 | 570.9 | 8.34 | 7.43 | null | null | null | null | null |
| 1 | 4 | download | 2991.3 | 5.001 | 598.1 | 7.55 | 7.25 | null | null | null | null | null |
| 2 | 4 | download | 2685.4 | 5.003 | 536.7 | 8.41 | 8.01 | null | null | null | null | null |
| 3 | 4 | download | 3077.8 | 5.002 | 615.4 | 7.40 | 7.05 | null | null | null | null | null |
| 4 | 4 | download | 2551.1 | 5.001 | 510.2 | 6.57 | 6.91 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.33 | 3.02 | 3.42 |
| 1 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.43 | 3.19 | 3.61 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.35 | 3.01 | 3.38 |
| 3 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 2.52 | 3.32 | 3.93 |
| 4 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 2.46 | 3.27 | 3.83 |
| 0 | 4 | connect | 0.0 | 5.008 | null | null | null | null | null | 6.75 | 8.66 | 9.85 |
| 1 | 4 | connect | 0.0 | 5.008 | null | null | null | null | null | 7.63 | 10.00 | 11.22 |
| 2 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 8.32 | 10.79 | 13.08 |
| 3 | 4 | connect | 0.0 | 5.009 | null | null | null | null | null | 8.47 | 15.44 | 26.78 |
| 4 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 7.99 | 11.19 | 14.15 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.260 | 62.8 | 17 | 13 | 4.030 | 66.5 | 17 | 13 |
| 1 | 5.580 | 63.4 | 17 | 13 | 4.180 | 70.5 | 18 | 13 |
| 2 | 5.400 | 63.7 | 18 | 14 | 4.000 | 77.7 | 18 | 14 |
| 3 | 5.420 | 63.7 | 18 | 15 | 3.950 | 77.8 | 19 | 15 |
| 4 | 5.580 | 63.8 | 18 | 15 | 4.050 | 91.0 | 20 | 15 |
| 0 | 23.080 | 71.6 | 19 | 19 | 15.070 | 97.6 | 21 | 19 |
| 1 | 23.180 | 71.6 | 19 | 23 | 15.490 | 127.6 | 21 | 23 |
| 2 | 23.260 | 71.6 | 20 | 26 | 15.350 | 127.1 | 21 | 26 |
| 3 | 23.490 | 74.2 | 20 | 26 | 15.940 | 152.8 | 21 | 26 |
| 4 | 23.180 | 74.2 | 20 | 28 | 15.120 | 182.1 | 21 | 28 |
| 0 | 3.790 | 74.2 | 20 | 26 | 3.470 | 180.7 | 21 | 26 |
| 1 | 3.510 | 74.2 | 20 | 27 | 3.360 | 180.7 | 21 | 27 |
| 2 | 5.790 | 80.7 | 20 | 27 | 5.350 | 180.7 | 21 | 27 |
| 3 | 5.350 | 81.7 | 20 | 27 | 4.980 | 180.7 | 21 | 27 |
| 4 | 4.300 | 91.2 | 20 | 28 | 4.040 | 180.7 | 21 | 28 |
| 0 | 23.250 | 114.7 | 20 | 33 | 20.720 | 189.2 | 21 | 33 |
| 1 | 22.060 | 147.6 | 21 | 37 | 21.170 | 188.8 | 21 | 37 |
| 2 | 22.060 | 151.8 | 21 | 38 | 21.010 | 188.9 | 21 | 38 |
| 3 | 22.240 | 212.6 | 21 | 40 | 21.180 | 188.2 | 21 | 40 |
| 4 | 16.380 | 211.1 | 21 | 41 | 17.220 | 188.2 | 21 | 41 |
| 0 | 4.930 | 211.1 | 21 | 113 | 4.350 | 188.2 | 21 | 113 |
| 1 | 4.950 | 211.1 | 21 | 137 | 4.240 | 188.2 | 21 | 136 |
| 2 | 4.990 | 211.1 | 21 | 158 | 4.220 | 188.2 | 21 | 157 |
| 3 | 5.040 | 211.1 | 21 | 180 | 4.250 | 188.6 | 21 | 180 |
| 4 | 5.120 | 211.1 | 21 | 190 | 4.240 | 188.8 | 21 | 189 |
| 0 | 8.510 | 215.2 | 21 | 221 | 15.270 | 192.6 | 21 | 220 |
| 1 | 6.780 | 226.0 | 21 | 237 | 16.280 | 208.4 | 21 | 236 |
| 2 | 6.820 | 224.7 | 21 | 257 | 16.500 | 208.5 | 21 | 255 |
| 3 | 6.680 | 224.9 | 21 | 266 | 16.260 | 208.5 | 21 | 265 |
| 4 | 7.060 | 224.5 | 21 | 274 | 16.760 | 207.2 | 21 | 274 |
