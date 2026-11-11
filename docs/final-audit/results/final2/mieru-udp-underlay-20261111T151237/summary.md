# mieru-udp-underlay - mieru

- run_id: `mieru-udp-underlay-20261111T151237`
- kind: e2e
- started: 2026-11-11T15:12:37+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 30.41 | 0.23 | 30.14 | 31.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 50.15 | 0.46 | 49.69 | 51.02 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 54.16 | 0.27 | 53.90 | 55.55 | null | s/GiB |
| goodput | 4 | upload | 5 | 75.95 | 5.43 | 69.59 | 81.38 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 5 | 42.44 | 0.47 | 41.33 | 43.36 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 5 | 35.48 | 1.36 | 34.12 | 38.25 | null | s/GiB |
| goodput | 1 | download | 5 | 31.38 | 0.29 | 30.83 | 32.20 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 5 | 53.86 | 0.87 | 52.28 | 54.88 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 5 | 49.08 | 2.21 | 46.81 | 51.84 | null | s/GiB |
| goodput | 4 | download | 5 | 39.85 | 0.64 | 39.19 | 40.53 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 5 | 51.42 | 0.95 | 50.47 | 54.52 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 5 | 49.84 | 1.03 | 48.10 | 52.77 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 151.5 | 5.018 | 30.2 | 50.77 | 54.63 | null | null | null | null | null |
| 1 | 1 | upload | 153.1 | 5.000 | 30.6 | 50.08 | 53.90 | null | null | null | null | null |
| 2 | 1 | upload | 157.0 | 5.021 | 31.3 | 49.69 | 53.93 | null | null | null | null | null |
| 3 | 1 | upload | 152.9 | 5.029 | 30.4 | 50.15 | 54.16 | null | null | null | null | null |
| 4 | 1 | upload | 151.3 | 5.021 | 30.1 | 51.02 | 55.55 | null | null | null | null | null |
| 0 | 4 | upload | 382.8 | 5.500 | 69.6 | 42.91 | 34.16 | null | null | null | null | null |
| 1 | 4 | upload | 385.8 | 5.079 | 76.0 | 42.44 | 38.04 | null | null | null | null | null |
| 2 | 4 | upload | 390.1 | 5.100 | 76.5 | 42.26 | 34.12 | null | null | null | null | null |
| 3 | 4 | upload | 411.5 | 5.057 | 81.4 | 41.33 | 35.48 | null | null | null | null | null |
| 4 | 4 | upload | 385.2 | 5.501 | 70.0 | 43.36 | 38.25 | null | null | null | null | null |
| 0 | 1 | download | 161.0 | 5.000 | 32.2 | 52.28 | 46.81 | null | null | null | null | null |
| 1 | 1 | download | 158.4 | 5.000 | 31.7 | 53.08 | 47.45 | null | null | null | null | null |
| 2 | 1 | download | 154.2 | 5.000 | 30.8 | 53.86 | 49.08 | null | null | null | null | null |
| 3 | 1 | download | 155.8 | 5.000 | 31.2 | 54.73 | 51.84 | null | null | null | null | null |
| 4 | 1 | download | 156.9 | 5.000 | 31.4 | 54.88 | 51.29 | null | null | null | null | null |
| 0 | 4 | download | 202.7 | 5.001 | 40.5 | 50.47 | 48.96 | null | null | null | null | null |
| 1 | 4 | download | 199.3 | 5.001 | 39.9 | 51.28 | 49.84 | null | null | null | null | null |
| 2 | 4 | download | 200.7 | 5.001 | 40.1 | 51.42 | 48.10 | null | null | null | null | null |
| 3 | 4 | download | 196.1 | 5.001 | 39.2 | 54.52 | 50.87 | null | null | null | null | null |
| 4 | 4 | download | 196.0 | 5.001 | 39.2 | 53.61 | 52.77 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 7.510 | 63.3 | 17 | 11 | 8.080 | 83.2 | 21 | 10 |
| 1 | 7.490 | 64.8 | 17 | 13 | 8.060 | 101.8 | 21 | 10 |
| 2 | 7.620 | 65.8 | 17 | 13 | 8.270 | 130.1 | 21 | 10 |
| 3 | 7.490 | 66.1 | 18 | 13 | 8.090 | 139.7 | 21 | 10 |
| 4 | 7.540 | 66.3 | 18 | 14 | 8.210 | 143.2 | 21 | 10 |
| 0 | 16.040 | 77.8 | 20 | 20 | 12.770 | 223.2 | 21 | 14 |
| 1 | 15.990 | 76.7 | 20 | 22 | 14.330 | 286.0 | 21 | 14 |
| 2 | 16.100 | 81.1 | 21 | 22 | 13.000 | 360.0 | 21 | 13 |
| 3 | 16.610 | 81.9 | 21 | 25 | 14.260 | 376.1 | 21 | 16 |
| 4 | 16.310 | 83.4 | 21 | 22 | 14.390 | 389.1 | 21 | 14 |
| 0 | 8.220 | 101.3 | 21 | 20 | 7.360 | 389.2 | 21 | 10 |
| 1 | 8.210 | 108.5 | 21 | 20 | 7.340 | 389.2 | 21 | 10 |
| 2 | 8.110 | 140.1 | 21 | 20 | 7.390 | 394.2 | 21 | 10 |
| 3 | 8.330 | 144.4 | 21 | 20 | 7.890 | 397.7 | 21 | 10 |
| 4 | 8.410 | 175.0 | 21 | 20 | 7.860 | 398.8 | 21 | 10 |
| 0 | 9.990 | 211.9 | 21 | 24 | 9.690 | 402.6 | 21 | 13 |
| 1 | 9.980 | 220.4 | 21 | 24 | 9.700 | 404.8 | 21 | 13 |
| 2 | 10.080 | 226.9 | 21 | 26 | 9.430 | 408.4 | 21 | 15 |
| 3 | 10.440 | 244.0 | 21 | 26 | 9.740 | 416.5 | 21 | 14 |
| 4 | 10.260 | 337.8 | 21 | 26 | 10.100 | 423.5 | 21 | 15 |
