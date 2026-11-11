# xhttp-h3 - vless+xhttp

- run_id: `xhttp-h3-20261111T132150`
- kind: e2e
- started: 2026-11-11T13:21:50+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 12 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 32.62 | 0.15 | 32.43 | 32.77 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 34.31 | 2.19 | 29.82 | 36.50 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 22.58 | 1.24 | 19.53 | 23.81 | null | s/GiB |
| goodput | 4 | upload | 3 | 121.86 | 0.02 | 121.84 | 122.41 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 37.77 | 1.08 | 35.52 | 38.85 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 25.86 | 0.63 | 24.23 | 26.49 | null | s/GiB |
| goodput | 1 | download | 3 | 83.35 | 0.68 | 82.67 | 90.62 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 24.08 | 0.05 | 22.03 | 24.13 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 25.23 | 0.21 | 23.34 | 25.44 | null | s/GiB |
| goodput | 4 | download | 3 | 169.32 | 0.86 | 146.65 | 170.18 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 22.20 | 0.48 | 21.72 | 24.87 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 20.57 | 0.98 | 19.59 | 21.57 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.57 | 0.13 | 3.42 | 3.70 | null | ms |
| latency_p95 | 1 | connect | 3 | 4.77 | 0.19 | 4.55 | 4.96 | null | ms |
| latency_p99 | 1 | connect | 3 | 5.82 | 0.40 | 5.42 | 6.52 | null | ms |
| latency_p50 | 4 | connect | 3 | 4.13 | 0.29 | 3.84 | 8.20 | null | ms |
| latency_p95 | 4 | connect | 3 | 9.73 | 1.11 | 6.32 | 10.84 | null | ms |
| latency_p99 | 4 | connect | 3 | 12.90 | 1.94 | 9.47 | 14.85 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 164.1 | 5.031 | 32.6 | 29.82 | 19.53 | null | null | null | null | null |
| 1 | 1 | upload | 163.3 | 5.034 | 32.4 | 34.31 | 22.58 | null | null | null | null | null |
| 2 | 1 | upload | 164.7 | 5.025 | 32.8 | 36.50 | 23.81 | null | null | null | null | null |
| 0 | 4 | upload | 614.9 | 5.047 | 121.8 | 35.52 | 24.23 | null | null | null | null | null |
| 1 | 4 | upload | 618.2 | 5.050 | 122.4 | 37.77 | 25.86 | null | null | null | null | null |
| 2 | 4 | upload | 616.3 | 5.057 | 121.9 | 38.85 | 26.49 | null | null | null | null | null |
| 0 | 1 | download | 416.7 | 5.000 | 83.3 | 24.08 | 25.23 | null | null | null | null | null |
| 1 | 1 | download | 453.2 | 5.001 | 90.6 | 22.03 | 23.34 | null | null | null | null | null |
| 2 | 1 | download | 413.4 | 5.000 | 82.7 | 24.13 | 25.44 | null | null | null | null | null |
| 0 | 4 | download | 733.3 | 5.000 | 146.6 | 24.87 | 21.57 | null | null | null | null | null |
| 1 | 4 | download | 851.0 | 5.001 | 170.2 | 22.20 | 19.59 | null | null | null | null | null |
| 2 | 4 | download | 846.8 | 5.001 | 169.3 | 21.72 | 20.57 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 3.42 | 4.55 | 5.42 |
| 1 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 3.70 | 4.96 | 6.52 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.57 | 4.77 | 5.82 |
| 0 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.84 | 6.32 | 9.47 |
| 1 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 4.13 | 9.73 | 12.90 |
| 2 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 8.20 | 10.84 | 14.85 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 4.780 | 87.9 | 19 | 15 | 3.130 | 48.6 | 17 | 11 |
| 1 | 5.470 | 91.7 | 19 | 15 | 3.600 | 51.2 | 17 | 11 |
| 2 | 5.870 | 91.7 | 19 | 15 | 3.830 | 50.1 | 17 | 11 |
| 0 | 21.330 | 121.6 | 20 | 21 | 14.550 | 67.5 | 18 | 17 |
| 1 | 22.800 | 121.6 | 21 | 21 | 15.610 | 67.7 | 19 | 19 |
| 2 | 23.380 | 121.6 | 21 | 20 | 15.940 | 74.4 | 19 | 20 |
| 0 | 9.800 | 121.6 | 21 | 14 | 10.270 | 73.5 | 19 | 12 |
| 1 | 9.750 | 121.6 | 21 | 14 | 10.330 | 73.5 | 19 | 9 |
| 2 | 9.740 | 121.6 | 21 | 14 | 10.270 | 73.5 | 19 | 9 |
| 0 | 17.810 | 121.6 | 21 | 17 | 15.450 | 73.5 | 19 | 12 |
| 1 | 18.450 | 121.6 | 21 | 17 | 16.280 | 73.5 | 19 | 12 |
| 2 | 17.960 | 121.6 | 21 | 17 | 17.010 | 73.5 | 19 | 12 |
| 0 | 6.990 | 121.6 | 21 | 15 | 6.600 | 73.5 | 21 | 303 |
| 1 | 7.110 | 121.6 | 21 | 14 | 6.720 | 73.6 | 21 | 289 |
| 2 | 7.050 | 121.6 | 21 | 14 | 6.770 | 79.3 | 21 | 296 |
| 0 | 17.550 | 121.6 | 21 | 18 | 15.090 | 143.5 | 21 | 996 |
| 1 | 13.270 | 121.6 | 22 | 17 | 17.320 | 162.5 | 21 | 1052 |
| 2 | 8.090 | 121.6 | 22 | 17 | 19.490 | 161.3 | 23 | 514 |
