# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T120039`
- kind: e2e
- started: 2026-11-11T12:00:39+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 5 | 44.80 | 0.59 | 43.86 | 45.93 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 5 | 61.69 | 0.93 | 60.11 | 62.81 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 5 | 65.20 | 0.59 | 62.73 | 65.79 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 220.1 | 5.018 | 43.9 | 62.63 | 65.79 | null | null | null | null | null |
| 1 | 1 | upload | 224.4 | 5.009 | 44.8 | 61.69 | 65.20 | null | null | null | null | null |
| 2 | 1 | upload | 230.3 | 5.015 | 45.9 | 60.11 | 62.73 | null | null | null | null | null |
| 3 | 1 | upload | 221.4 | 5.008 | 44.2 | 62.81 | 65.40 | null | null | null | null | null |
| 4 | 1 | upload | 225.8 | 5.014 | 45.0 | 61.63 | 64.40 | null | null | null | null | null |
| 0 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 1 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 2 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 3 | 4 | upload | 0.0 | 10.002 | null | null | null | null | null | null | null | null |
| 4 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 0 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 1 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 2 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 3 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 4 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 0 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 1 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 2 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 3 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 4 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 0 | 1 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 1 | 1 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 2 | 1 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 3 | 1 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 4 | 1 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 0 | 4 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 1 | 4 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 2 | 4 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 3 | 4 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |
| 4 | 4 | udp | 0.0 | 5.000 | null | null | null | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 13.460 | 93.7 | 18 | 16 | 14.140 | 88.6 | 16 | 15 |
| 1 | 13.520 | 102.0 | 18 | 16 | 14.290 | 95.4 | 16 | 15 |
| 2 | 13.520 | 102.7 | 18 | 16 | 14.110 | 97.2 | 16 | 15 |
| 3 | 13.580 | 102.9 | 19 | 16 | 14.140 | 98.0 | 17 | 15 |
| 4 | 13.590 | 104.8 | 19 | 17 | 14.200 | 109.7 | 17 | 16 |
| 0 | 0.030 | 105.0 | 19 | 24 | 0.000 | 109.7 | 17 | 19 |
| 1 | 0.030 | 105.2 | 19 | 32 | 0.010 | 109.7 | 17 | 19 |
| 2 | 0.030 | 105.7 | 19 | 40 | 0.000 | 109.7 | 17 | 19 |
| 3 | 0.040 | 105.8 | 19 | 48 | 0.010 | 109.7 | 17 | 19 |
| 4 | 0.030 | 105.8 | 19 | 47 | 0.010 | 109.7 | 17 | 19 |
| 0 | 0.020 | 105.8 | 19 | 41 | 0.020 | 109.7 | 17 | 19 |
| 1 | 0.020 | 105.8 | 19 | 35 | 0.010 | 108.1 | 17 | 19 |
| 2 | 0.020 | 105.8 | 19 | 29 | 0.010 | 108.1 | 17 | 19 |
| 3 | 0.040 | 104.4 | 19 | 26 | 0.010 | 108.1 | 17 | 19 |
| 4 | 0.020 | 104.4 | 19 | 26 | 0.000 | 108.1 | 17 | 19 |
| 0 | 0.020 | 104.4 | 19 | 32 | 0.000 | 108.1 | 17 | 19 |
| 1 | 0.030 | 104.4 | 19 | 38 | 0.020 | 108.1 | 17 | 19 |
| 2 | 0.020 | 104.4 | 19 | 44 | 0.020 | 108.1 | 17 | 19 |
| 3 | 0.030 | 104.4 | 19 | 47 | 0.000 | 108.1 | 17 | 19 |
| 4 | 0.030 | 104.4 | 19 | 47 | 0.010 | 108.1 | 17 | 19 |
| 0 | 3.800 | 104.4 | 19 | 45 | 0.010 | 108.1 | 17 | 19 |
| 1 | 3.740 | 104.4 | 19 | 41 | 0.000 | 108.1 | 17 | 19 |
| 2 | 3.640 | 104.4 | 19 | 37 | 0.000 | 108.1 | 17 | 19 |
| 3 | 3.640 | 104.4 | 19 | 37 | 0.010 | 108.1 | 17 | 19 |
| 4 | 3.640 | 104.4 | 19 | 33 | 0.000 | 108.1 | 17 | 19 |
| 0 | 3.690 | 104.4 | 20 | 33 | 0.000 | 108.1 | 17 | 19 |
| 1 | 3.680 | 104.4 | 20 | 29 | 0.000 | 108.1 | 17 | 19 |
| 2 | 3.690 | 104.4 | 20 | 25 | 0.000 | 108.1 | 17 | 19 |
| 3 | 3.680 | 104.4 | 20 | 25 | 0.000 | 108.1 | 17 | 19 |
| 4 | 3.740 | 104.4 | 20 | 21 | 0.010 | 108.1 | 17 | 19 |
