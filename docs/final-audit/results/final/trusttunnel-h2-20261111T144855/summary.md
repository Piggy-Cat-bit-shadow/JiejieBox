# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T144855`
- kind: e2e
- started: 2026-11-11T14:48:55+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 172.55 | 3.56 | 168.99 | 179.90 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 9.84 | 0.26 | 9.58 | 10.26 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 9.21 | 0.25 | 8.90 | 9.46 | null | s/GiB |
| goodput | 4 | upload | 3 | 213.46 | 0.72 | 212.74 | 217.48 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 10.12 | 0.01 | 9.94 | 10.13 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 8.32 | 0.02 | 8.16 | 8.34 | null | s/GiB |
| goodput | 1 | download | 3 | 140.26 | 0.09 | 139.74 | 140.35 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 9.64 | 0.04 | 9.56 | 9.68 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 10.56 | 0.01 | 10.56 | 10.60 | null | s/GiB |
| goodput | 4 | download | 3 | 140.74 | 0.05 | 140.69 | 140.85 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 13.10 | 0.03 | 13.07 | 13.17 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 12.43 | 0.01 | 12.40 | 12.44 | null | s/GiB |
| goodput | 1 | udp | 3 | 10.27 | 0.33 | 9.94 | 12.16 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 200.45 | 7.50 | 172.44 | 207.95 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 226.17 | 7.74 | 202.07 | 233.92 | null | s/GiB |
| goodput | 4 | udp | 3 | 10.01 | 0.02 | 9.99 | 10.18 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 206.10 | 0.23 | 202.61 | 206.33 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 232.73 | 0.85 | 231.19 | 233.58 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 845.0 | 5.001 | 169.0 | 10.26 | 9.46 | null | null | null | null | null |
| 1 | 1 | upload | 863.4 | 5.004 | 172.5 | 9.84 | 9.21 | null | null | null | null | null |
| 2 | 1 | upload | 899.7 | 5.001 | 179.9 | 9.58 | 8.90 | null | null | null | null | null |
| 0 | 4 | upload | 1090.4 | 5.014 | 217.5 | 9.94 | 8.16 | null | null | null | null | null |
| 1 | 4 | upload | 1070.8 | 5.017 | 213.5 | 10.12 | 8.34 | null | null | null | null | null |
| 2 | 4 | upload | 1066.7 | 5.014 | 212.7 | 10.13 | 8.32 | null | null | null | null | null |
| 0 | 1 | download | 701.8 | 5.000 | 140.4 | 9.56 | 10.56 | null | null | null | null | null |
| 1 | 1 | download | 701.3 | 5.000 | 140.3 | 9.68 | 10.56 | null | null | null | null | null |
| 2 | 1 | download | 698.7 | 5.000 | 139.7 | 9.64 | 10.60 | null | null | null | null | null |
| 0 | 4 | download | 703.6 | 5.001 | 140.7 | 13.07 | 12.43 | null | null | null | null | null |
| 1 | 4 | download | 704.3 | 5.001 | 140.9 | 13.10 | 12.40 | null | null | null | null | null |
| 2 | 4 | download | 703.8 | 5.001 | 140.7 | 13.17 | 12.44 | null | null | null | null | null |
| 0 | 1 | udp | 49.7 | 5.000 | 9.9 | 207.95 | 233.92 | null | null | null | null | null |
| 1 | 1 | udp | 60.8 | 5.000 | 12.2 | 172.44 | 202.07 | null | null | null | null | null |
| 2 | 1 | udp | 51.3 | 5.000 | 10.3 | 200.45 | 226.17 | null | null | null | null | null |
| 0 | 4 | udp | 50.0 | 5.000 | 10.0 | 206.33 | 232.73 | null | null | null | null | null |
| 1 | 4 | udp | 49.9 | 5.000 | 10.0 | 206.10 | 233.58 | null | null | null | null | null |
| 2 | 4 | udp | 50.9 | 5.000 | 10.2 | 202.61 | 231.19 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.470 | 61.4 | 13 | 11 | 7.810 | 64.7 | 14 | 13 |
| 1 | 8.300 | 62.8 | 13 | 11 | 7.770 | 65.7 | 14 | 13 |
| 2 | 8.420 | 63.6 | 13 | 11 | 7.820 | 67.0 | 15 | 13 |
| 0 | 10.580 | 65.1 | 14 | 14 | 8.690 | 68.0 | 16 | 16 |
| 1 | 10.580 | 66.0 | 14 | 14 | 8.720 | 68.1 | 16 | 16 |
| 2 | 10.550 | 68.4 | 14 | 14 | 8.670 | 68.2 | 17 | 16 |
| 0 | 6.550 | 72.2 | 15 | 11 | 7.240 | 68.4 | 17 | 13 |
| 1 | 6.630 | 74.4 | 16 | 11 | 7.230 | 67.1 | 17 | 13 |
| 2 | 6.580 | 75.8 | 16 | 11 | 7.230 | 67.1 | 17 | 13 |
| 0 | 8.980 | 74.3 | 16 | 14 | 8.540 | 67.6 | 17 | 16 |
| 1 | 9.010 | 74.3 | 17 | 14 | 8.530 | 68.0 | 17 | 16 |
| 2 | 9.050 | 74.3 | 17 | 14 | 8.550 | 68.0 | 17 | 16 |
| 0 | 10.090 | 74.4 | 17 | 12 | 11.350 | 68.2 | 17 | 13 |
| 1 | 10.240 | 74.3 | 17 | 12 | 12.000 | 67.9 | 17 | 13 |
| 2 | 10.050 | 74.3 | 17 | 12 | 11.340 | 68.0 | 17 | 13 |
| 0 | 10.080 | 74.3 | 17 | 12 | 11.370 | 67.2 | 17 | 13 |
| 1 | 10.050 | 74.3 | 17 | 12 | 11.390 | 67.8 | 17 | 13 |
| 2 | 10.070 | 74.3 | 17 | 12 | 11.490 | 67.8 | 17 | 13 |
