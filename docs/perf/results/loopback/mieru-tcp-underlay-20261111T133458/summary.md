# mieru-tcp-underlay - mieru

- run_id: `mieru-tcp-underlay-20261111T133458`
- kind: e2e
- started: 2026-11-11T13:34:58+08:00
- commit: `3fb1c36233dd` (perf(baseline): add XHTTP reference cells, the tunnel limit and the UDP loss threshold)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 121.99 | 0.25 | 121.01 | 126.44 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 8.22 | 0.04 | 7.98 | 8.25 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 5.95 | 0.02 | 5.93 | 6.28 | null | s/GiB |
| goodput | 4 | upload | 5 | 429.74 | 10.48 | 407.07 | 461.22 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 10.61 | 0.34 | 10.10 | 10.95 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 6.66 | 0.19 | 6.35 | 6.95 | null | s/GiB |
| goodput | 1 | download | 5 | 105.05 | 0.47 | 102.79 | 108.77 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 5.55 | 0.13 | 5.42 | 6.19 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 5.48 | 0.04 | 5.43 | 5.80 | null | s/GiB |
| goodput | 4 | download | 5 | 701.32 | 14.52 | 660.04 | 715.84 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 6.94 | 0.11 | 6.83 | 7.16 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 6.46 | 0.08 | 6.29 | 6.57 | null | s/GiB |
| latency_p50 | 1 | connect | 5 | 2.34 | 0.01 | 2.27 | 2.35 | null | ms |
| latency_p95 | 1 | connect | 5 | 2.95 | 0.02 | 2.85 | 2.97 | null | ms |
| latency_p99 | 1 | connect | 5 | 3.42 | 0.07 | 3.34 | 3.49 | null | ms |
| latency_p50 | 4 | connect | 5 | 6.97 | 0.02 | 6.92 | 7.02 | null | ms |
| latency_p95 | 4 | connect | 5 | 8.23 | 0.06 | 8.16 | 9.23 | null | ms |
| latency_p99 | 4 | connect | 5 | 10.28 | 0.35 | 9.73 | 12.04 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 611.7 | 5.004 | 122.2 | 8.22 | 6.28 | null | null | null | null | null |
| 1 | 1 | upload | 610.5 | 5.005 | 122.0 | 8.25 | 6.09 | null | null | null | null | null |
| 2 | 1 | upload | 609.4 | 5.003 | 121.8 | 7.98 | 5.93 | null | null | null | null | null |
| 3 | 1 | upload | 605.7 | 5.005 | 121.0 | 8.06 | 5.93 | null | null | null | null | null |
| 4 | 1 | upload | 633.2 | 5.008 | 126.4 | 8.22 | 5.95 | null | null | null | null | null |
| 0 | 4 | upload | 2309.9 | 5.008 | 461.2 | 10.10 | 6.66 | null | null | null | null | null |
| 1 | 4 | upload | 2202.0 | 5.002 | 440.2 | 10.61 | 6.85 | null | null | null | null | null |
| 2 | 4 | upload | 2040.0 | 5.011 | 407.1 | 10.63 | 6.57 | null | null | null | null | null |
| 3 | 4 | upload | 2129.4 | 5.014 | 424.7 | 10.27 | 6.35 | null | null | null | null | null |
| 4 | 4 | upload | 2155.3 | 5.015 | 429.7 | 10.95 | 6.95 | null | null | null | null | null |
| 0 | 1 | download | 543.9 | 5.000 | 108.8 | 6.19 | 5.80 | null | null | null | null | null |
| 1 | 1 | download | 527.7 | 5.000 | 105.5 | 5.55 | 5.43 | null | null | null | null | null |
| 2 | 1 | download | 525.3 | 5.000 | 105.1 | 5.77 | 5.67 | null | null | null | null | null |
| 3 | 1 | download | 514.0 | 5.000 | 102.8 | 5.50 | 5.48 | null | null | null | null | null |
| 4 | 1 | download | 525.1 | 5.000 | 105.0 | 5.42 | 5.44 | null | null | null | null | null |
| 0 | 4 | download | 3395.7 | 5.002 | 678.9 | 6.94 | 6.29 | null | null | null | null | null |
| 1 | 4 | download | 3525.7 | 5.002 | 704.9 | 7.13 | 6.49 | null | null | null | null | null |
| 2 | 4 | download | 3507.7 | 5.002 | 701.3 | 7.16 | 6.46 | null | null | null | null | null |
| 3 | 4 | download | 3300.6 | 5.001 | 660.0 | 6.83 | 6.57 | null | null | null | null | null |
| 4 | 4 | download | 3581.2 | 5.003 | 715.8 | 6.88 | 6.39 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.27 | 2.90 | 3.34 |
| 1 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.28 | 2.85 | 3.35 |
| 2 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 2.34 | 2.97 | 3.49 |
| 3 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 2.35 | 2.95 | 3.42 |
| 4 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.35 | 2.96 | 3.44 |
| 0 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 6.98 | 9.23 | 12.04 |
| 1 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 6.92 | 8.23 | 10.28 |
| 2 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 7.02 | 8.41 | 10.63 |
| 3 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 6.95 | 8.18 | 10.09 |
| 4 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 6.97 | 8.16 | 9.73 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 4.910 | 61.9 | 17 | 14 | 3.750 | 67.1 | 16 | 14 |
| 1 | 4.920 | 62.2 | 17 | 15 | 3.630 | 70.8 | 18 | 15 |
| 2 | 4.750 | 62.4 | 17 | 15 | 3.530 | 78.0 | 18 | 15 |
| 3 | 4.770 | 62.6 | 17 | 15 | 3.510 | 78.4 | 19 | 15 |
| 4 | 5.080 | 62.8 | 18 | 15 | 3.680 | 90.3 | 20 | 15 |
| 0 | 22.790 | 70.5 | 19 | 20 | 15.030 | 98.1 | 20 | 20 |
| 1 | 22.810 | 70.5 | 20 | 23 | 14.740 | 127.5 | 21 | 23 |
| 2 | 21.180 | 70.9 | 20 | 23 | 13.080 | 126.6 | 21 | 23 |
| 3 | 21.350 | 70.9 | 20 | 24 | 13.210 | 146.6 | 21 | 24 |
| 4 | 23.050 | 71.0 | 20 | 27 | 14.620 | 181.3 | 22 | 27 |
| 0 | 3.290 | 70.9 | 20 | 24 | 3.080 | 180.8 | 22 | 24 |
| 1 | 2.860 | 72.5 | 20 | 24 | 2.800 | 180.9 | 22 | 24 |
| 2 | 2.960 | 79.0 | 20 | 24 | 2.910 | 180.9 | 22 | 24 |
| 3 | 2.760 | 79.9 | 20 | 25 | 2.750 | 181.0 | 22 | 25 |
| 4 | 2.780 | 80.0 | 20 | 25 | 2.790 | 181.0 | 22 | 25 |
| 0 | 23.030 | 121.0 | 20 | 31 | 20.850 | 188.4 | 22 | 31 |
| 1 | 24.550 | 149.9 | 21 | 36 | 22.330 | 190.8 | 22 | 36 |
| 2 | 24.510 | 206.7 | 22 | 37 | 22.140 | 189.2 | 22 | 37 |
| 3 | 22.020 | 206.5 | 22 | 40 | 21.180 | 189.2 | 22 | 40 |
| 4 | 24.070 | 218.0 | 22 | 43 | 22.330 | 189.2 | 22 | 43 |
| 0 | 4.920 | 216.7 | 22 | 113 | 4.380 | 190.2 | 22 | 112 |
| 1 | 4.910 | 216.7 | 22 | 137 | 4.150 | 191.7 | 22 | 137 |
| 2 | 4.970 | 216.7 | 22 | 159 | 4.180 | 192.1 | 22 | 159 |
| 3 | 4.980 | 216.7 | 22 | 179 | 4.150 | 192.1 | 22 | 178 |
| 4 | 4.980 | 216.7 | 22 | 198 | 4.120 | 192.2 | 22 | 197 |
| 0 | 7.400 | 216.7 | 22 | 222 | 16.070 | 195.6 | 22 | 220 |
| 1 | 6.300 | 220.9 | 22 | 237 | 15.830 | 204.6 | 22 | 235 |
| 2 | 6.620 | 221.0 | 22 | 255 | 16.220 | 204.7 | 22 | 255 |
| 3 | 6.570 | 221.7 | 22 | 275 | 16.150 | 204.8 | 22 | 273 |
| 4 | 6.670 | 222.8 | 22 | 285 | 16.330 | 204.8 | 22 | 284 |
