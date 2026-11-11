# xhttp-h2 - vless+xhttp

- run_id: `xhttp-h2-20261111T145524`
- kind: e2e
- started: 2026-11-11T14:55:24+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 32.70 | 0.01 | 32.41 | 32.70 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 12.10 | 0.12 | 11.97 | 13.15 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 7.61 | 0.07 | 7.55 | 7.86 | null | s/GiB |
| goodput | 4 | upload | 3 | 126.32 | 0.27 | 126.05 | 127.32 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 14.16 | 0.11 | 14.04 | 14.49 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 10.49 | 0.31 | 10.18 | 11.53 | null | s/GiB |
| goodput | 1 | download | 3 | 89.63 | 0.14 | 89.49 | 91.22 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 15.01 | 0.00 | 14.53 | 15.01 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 17.42 | 0.04 | 17.13 | 17.46 | null | s/GiB |
| goodput | 4 | download | 3 | 368.58 | 17.68 | 222.15 | 386.26 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 10.48 | 0.42 | 10.06 | 15.02 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 12.09 | 0.54 | 11.55 | 16.15 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 2.27 | 0.00 | 2.25 | 2.27 | null | ms |
| latency_p95 | 1 | connect | 3 | 2.91 | 0.01 | 2.90 | 2.92 | null | ms |
| latency_p99 | 1 | connect | 3 | 3.51 | 0.03 | 3.47 | 3.55 | null | ms |
| latency_p50 | 4 | connect | 3 | 7.52 | 0.05 | 2.86 | 7.58 | null | ms |
| latency_p95 | 4 | connect | 3 | 8.32 | 0.04 | 7.67 | 8.36 | null | ms |
| latency_p99 | 4 | connect | 3 | 9.38 | 0.00 | 9.38 | 10.41 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 164.2 | 5.022 | 32.7 | 11.97 | 7.86 | null | null | null | null | null |
| 1 | 1 | upload | 164.2 | 5.021 | 32.7 | 12.10 | 7.55 | null | null | null | null | null |
| 2 | 1 | upload | 162.7 | 5.021 | 32.4 | 13.15 | 7.61 | null | null | null | null | null |
| 0 | 4 | upload | 638.7 | 5.056 | 126.3 | 14.04 | 11.53 | null | null | null | null | null |
| 1 | 4 | upload | 640.1 | 5.027 | 127.3 | 14.49 | 10.49 | null | null | null | null | null |
| 2 | 4 | upload | 633.5 | 5.026 | 126.1 | 14.16 | 10.18 | null | null | null | null | null |
| 0 | 1 | download | 456.1 | 5.000 | 91.2 | 14.53 | 17.13 | null | null | null | null | null |
| 1 | 1 | download | 447.5 | 5.000 | 89.5 | 15.01 | 17.42 | null | null | null | null | null |
| 2 | 1 | download | 448.2 | 5.000 | 89.6 | 15.01 | 17.46 | null | null | null | null | null |
| 0 | 4 | download | 1842.9 | 5.000 | 368.6 | 10.48 | 12.09 | null | null | null | null | null |
| 1 | 4 | download | 1931.6 | 5.001 | 386.3 | 10.06 | 11.55 | null | null | null | null | null |
| 2 | 4 | download | 1110.8 | 5.000 | 222.2 | 15.02 | 16.15 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.25 | 2.92 | 3.55 |
| 1 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.27 | 2.90 | 3.47 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.27 | 2.91 | 3.51 |
| 0 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 2.86 | 7.67 | 10.41 |
| 1 | 4 | connect | 0.0 | 5.006 | null | null | null | null | null | 7.52 | 8.36 | 9.38 |
| 2 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 7.58 | 8.32 | 9.38 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.920 | 86.7 | 19 | 14 | 1.260 | 50.8 | 17 | 13 |
| 1 | 1.940 | 90.1 | 19 | 14 | 1.210 | 52.0 | 17 | 13 |
| 2 | 2.090 | 90.1 | 19 | 14 | 1.210 | 51.9 | 17 | 13 |
| 0 | 8.760 | 108.0 | 20 | 20 | 7.190 | 70.3 | 18 | 19 |
| 1 | 9.060 | 108.7 | 20 | 21 | 6.560 | 73.4 | 19 | 22 |
| 2 | 8.760 | 110.9 | 20 | 20 | 6.300 | 73.4 | 19 | 21 |
| 0 | 6.470 | 110.9 | 20 | 13 | 7.630 | 73.4 | 19 | 15 |
| 1 | 6.560 | 110.9 | 20 | 13 | 7.610 | 73.4 | 19 | 11 |
| 2 | 6.570 | 110.9 | 20 | 13 | 7.640 | 73.4 | 19 | 11 |
| 0 | 18.860 | 110.9 | 20 | 16 | 21.760 | 73.4 | 19 | 14 |
| 1 | 18.980 | 110.9 | 20 | 16 | 21.790 | 73.4 | 19 | 14 |
| 2 | 16.290 | 110.9 | 20 | 16 | 17.520 | 73.4 | 19 | 14 |
| 0 | 5.150 | 110.9 | 20 | 13 | 5.760 | 80.5 | 21 | 443 |
| 1 | 5.140 | 110.9 | 20 | 13 | 5.900 | 86.9 | 21 | 438 |
| 2 | 5.090 | 110.9 | 20 | 13 | 6.000 | 96.2 | 21 | 438 |
| 0 | 11.800 | 110.9 | 21 | 17 | 15.940 | 180.5 | 21 | 1435 |
| 1 | 4.950 | 110.9 | 21 | 16 | 18.280 | 179.0 | 23 | 546 |
| 2 | 4.790 | 110.9 | 21 | 17 | 18.550 | 179.0 | 24 | 538 |
