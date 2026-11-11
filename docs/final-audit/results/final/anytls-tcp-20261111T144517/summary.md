# anytls-tcp - anytls

- run_id: `anytls-tcp-20261111T144517`
- kind: e2e
- started: 2026-11-11T14:45:17+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 240.85 | 1.84 | 239.01 | 254.40 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 4.46 | 0.02 | 4.16 | 4.49 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 3.78 | 0.01 | 3.57 | 3.79 | null | s/GiB |
| goodput | 4 | upload | 3 | 529.61 | 41.24 | 488.37 | 712.91 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 8.08 | 0.57 | 5.93 | 8.65 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 5.27 | 0.16 | 3.98 | 5.44 | null | s/GiB |
| goodput | 1 | download | 3 | 674.17 | 98.11 | 546.67 | 772.27 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 2.55 | 0.25 | 2.29 | 2.93 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1.35 | 0.19 | 1.16 | 1.91 | null | s/GiB |
| goodput | 4 | download | 3 | 1176.34 | 10.13 | 1159.01 | 1186.47 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 4.25 | 0.00 | 4.15 | 4.25 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 3.39 | 0.01 | 3.37 | 3.50 | null | s/GiB |
| goodput | 1 | udp | 3 | 23.64 | 0.04 | 23.54 | 23.68 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 102.50 | 0.26 | 102.24 | 103.29 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 100.42 | 0.25 | 99.82 | 100.68 | null | s/GiB |
| goodput | 4 | udp | 3 | 21.97 | 0.12 | 21.85 | 22.43 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 109.99 | 0.70 | 107.54 | 110.69 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 107.38 | 0.87 | 105.35 | 108.25 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1204.7 | 5.002 | 240.8 | 4.46 | 3.79 | null | null | null | null | null |
| 1 | 1 | upload | 1195.8 | 5.003 | 239.0 | 4.49 | 3.78 | null | null | null | null | null |
| 2 | 1 | upload | 1273.2 | 5.005 | 254.4 | 4.16 | 3.57 | null | null | null | null | null |
| 0 | 4 | upload | 3569.2 | 5.006 | 712.9 | 5.93 | 3.98 | null | null | null | null | null |
| 1 | 4 | upload | 2457.8 | 5.033 | 488.4 | 8.65 | 5.27 | null | null | null | null | null |
| 2 | 4 | upload | 2652.3 | 5.008 | 529.6 | 8.08 | 5.44 | null | null | null | null | null |
| 0 | 1 | download | 3371.0 | 5.000 | 674.2 | 2.55 | 1.35 | null | null | null | null | null |
| 1 | 1 | download | 3861.5 | 5.000 | 772.3 | 2.29 | 1.16 | null | null | null | null | null |
| 2 | 1 | download | 2733.4 | 5.000 | 546.7 | 2.93 | 1.91 | null | null | null | null | null |
| 0 | 4 | download | 5883.0 | 5.001 | 1176.3 | 4.25 | 3.37 | null | null | null | null | null |
| 1 | 4 | download | 5935.9 | 5.003 | 1186.5 | 4.15 | 3.39 | null | null | null | null | null |
| 2 | 4 | download | 5796.5 | 5.001 | 1159.0 | 4.25 | 3.50 | null | null | null | null | null |
| 0 | 1 | udp | 117.7 | 5.000 | 23.5 | 103.29 | 100.68 | null | null | null | null | null |
| 1 | 1 | udp | 118.2 | 5.000 | 23.6 | 102.50 | 100.42 | null | null | null | null | null |
| 2 | 1 | udp | 118.4 | 5.000 | 23.7 | 102.24 | 99.82 | null | null | null | null | null |
| 0 | 4 | udp | 112.2 | 5.000 | 22.4 | 107.54 | 105.35 | null | null | null | null | null |
| 1 | 4 | udp | 109.9 | 5.000 | 22.0 | 109.99 | 107.38 | null | null | null | null | null |
| 2 | 4 | udp | 109.3 | 5.000 | 21.9 | 110.69 | 108.25 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.250 | 59.9 | 13 | 12 | 4.460 | 61.5 | 13 | 13 |
| 1 | 5.240 | 60.4 | 13 | 12 | 4.410 | 62.2 | 14 | 13 |
| 2 | 5.170 | 61.4 | 14 | 12 | 4.440 | 62.8 | 14 | 13 |
| 0 | 20.660 | 63.9 | 16 | 21 | 13.870 | 66.9 | 20 | 22 |
| 1 | 20.760 | 64.7 | 16 | 21 | 12.660 | 67.0 | 20 | 22 |
| 2 | 20.930 | 65.1 | 18 | 21 | 14.080 | 67.7 | 20 | 22 |
| 0 | 8.380 | 66.6 | 18 | 18 | 4.440 | 67.9 | 20 | 19 |
| 1 | 8.650 | 66.6 | 18 | 18 | 4.380 | 66.9 | 20 | 19 |
| 2 | 7.830 | 64.9 | 18 | 18 | 5.090 | 66.7 | 20 | 19 |
| 0 | 24.420 | 67.2 | 18 | 21 | 19.380 | 66.7 | 20 | 22 |
| 1 | 24.050 | 67.5 | 18 | 21 | 19.640 | 66.7 | 20 | 22 |
| 2 | 24.080 | 68.7 | 18 | 21 | 19.830 | 66.7 | 20 | 22 |
| 0 | 11.870 | 67.3 | 18 | 19 | 11.570 | 67.0 | 20 | 19 |
| 1 | 11.830 | 67.3 | 18 | 19 | 11.590 | 66.7 | 20 | 19 |
| 2 | 11.820 | 67.3 | 18 | 19 | 11.540 | 66.7 | 20 | 19 |
| 0 | 11.780 | 67.3 | 18 | 15 | 11.540 | 66.7 | 20 | 15 |
| 1 | 11.800 | 67.3 | 18 | 15 | 11.520 | 66.7 | 20 | 15 |
| 2 | 11.810 | 67.3 | 18 | 15 | 11.550 | 66.7 | 20 | 15 |
