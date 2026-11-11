# xhttp-h2 - vless+xhttp

- run_id: `xhttp-h2-20261111T132001`
- kind: e2e
- started: 2026-11-11T13:20:01+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 12 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 32.60 | 0.08 | 32.51 | 32.72 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 12.96 | 0.29 | 12.67 | 14.28 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 8.09 | 0.24 | 7.85 | 8.64 | null | s/GiB |
| goodput | 4 | upload | 3 | 125.62 | 0.24 | 125.37 | 126.52 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 15.68 | 0.17 | 15.52 | 18.03 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 12.53 | 0.03 | 12.51 | 12.62 | null | s/GiB |
| goodput | 1 | download | 3 | 83.84 | 0.81 | 79.16 | 84.65 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 16.02 | 0.01 | 16.02 | 16.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 18.69 | 0.23 | 18.46 | 19.71 | null | s/GiB |
| goodput | 4 | download | 3 | 213.38 | 42.78 | 170.60 | 336.30 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 15.01 | 3.42 | 11.52 | 18.43 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 15.93 | 2.97 | 12.96 | 19.69 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.12 | 0.22 | 2.67 | 3.34 | null | ms |
| latency_p95 | 1 | connect | 3 | 4.08 | 0.38 | 3.55 | 4.45 | null | ms |
| latency_p99 | 1 | connect | 3 | 4.85 | 0.45 | 4.15 | 5.30 | null | ms |
| latency_p50 | 4 | connect | 3 | 8.12 | 0.21 | 3.26 | 8.33 | null | ms |
| latency_p95 | 4 | connect | 3 | 10.39 | 0.52 | 5.09 | 10.91 | null | ms |
| latency_p99 | 4 | connect | 3 | 13.12 | 0.39 | 6.98 | 13.50 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 164.4 | 5.024 | 32.7 | 12.96 | 7.85 | null | null | null | null | null |
| 1 | 1 | upload | 163.3 | 5.022 | 32.5 | 12.67 | 8.09 | null | null | null | null | null |
| 2 | 1 | upload | 163.5 | 5.016 | 32.6 | 14.28 | 8.64 | null | null | null | null | null |
| 0 | 4 | upload | 639.5 | 5.055 | 126.5 | 15.52 | 12.51 | null | null | null | null | null |
| 1 | 4 | upload | 633.7 | 5.044 | 125.6 | 18.03 | 12.62 | null | null | null | null | null |
| 2 | 4 | upload | 633.3 | 5.051 | 125.4 | 15.68 | 12.53 | null | null | null | null | null |
| 0 | 1 | download | 419.2 | 5.000 | 83.8 | 16.02 | 18.69 | null | null | null | null | null |
| 1 | 1 | download | 395.8 | 5.000 | 79.2 | 16.79 | 19.71 | null | null | null | null | null |
| 2 | 1 | download | 423.3 | 5.000 | 84.6 | 16.02 | 18.46 | null | null | null | null | null |
| 0 | 4 | download | 1067.0 | 5.001 | 213.4 | 15.01 | 15.93 | null | null | null | null | null |
| 1 | 4 | download | 1681.6 | 5.000 | 336.3 | 11.52 | 12.96 | null | null | null | null | null |
| 2 | 4 | download | 853.1 | 5.001 | 170.6 | 18.43 | 19.69 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 3.34 | 4.45 | 5.30 |
| 1 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.12 | 4.08 | 4.85 |
| 2 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 2.67 | 3.55 | 4.15 |
| 0 | 4 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.26 | 5.09 | 6.98 |
| 1 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 8.12 | 10.39 | 13.12 |
| 2 | 4 | connect | 0.0 | 5.007 | null | null | null | null | null | 8.33 | 10.91 | 13.50 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 2.080 | 89.6 | 18 | 15 | 1.260 | 50.6 | 16 | 14 |
| 1 | 2.020 | 90.7 | 18 | 15 | 1.290 | 50.6 | 17 | 14 |
| 2 | 2.280 | 91.9 | 19 | 15 | 1.380 | 52.5 | 17 | 14 |
| 0 | 9.690 | 108.9 | 21 | 21 | 7.810 | 69.3 | 19 | 20 |
| 1 | 11.160 | 125.7 | 21 | 21 | 7.810 | 72.1 | 19 | 21 |
| 2 | 9.700 | 125.7 | 21 | 21 | 7.750 | 72.1 | 19 | 21 |
| 0 | 6.560 | 125.7 | 21 | 14 | 7.650 | 72.1 | 19 | 16 |
| 1 | 6.490 | 125.7 | 21 | 14 | 7.620 | 72.1 | 19 | 12 |
| 2 | 6.620 | 125.7 | 21 | 14 | 7.630 | 72.1 | 19 | 12 |
| 0 | 15.640 | 125.7 | 21 | 17 | 16.600 | 72.1 | 19 | 15 |
| 1 | 18.910 | 125.7 | 21 | 17 | 21.280 | 72.1 | 19 | 15 |
| 2 | 15.350 | 125.7 | 21 | 17 | 16.400 | 72.1 | 19 | 15 |
| 0 | 5.330 | 125.7 | 21 | 14 | 5.650 | 72.1 | 21 | 322 |
| 1 | 5.290 | 125.7 | 21 | 15 | 5.790 | 76.3 | 21 | 352 |
| 2 | 5.270 | 125.7 | 21 | 14 | 5.840 | 86.9 | 22 | 385 |
| 0 | 13.790 | 125.7 | 21 | 18 | 14.780 | 160.8 | 22 | 1230 |
| 1 | 6.480 | 125.7 | 21 | 19 | 18.310 | 170.9 | 22 | 1162 |
| 2 | 5.300 | 125.7 | 21 | 18 | 18.720 | 169.8 | 22 | 503 |
