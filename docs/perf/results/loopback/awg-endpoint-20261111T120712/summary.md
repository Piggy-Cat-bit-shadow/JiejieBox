# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T120712`
- kind: e2e
- started: 2026-11-11T12:07:12+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 2 | 49.78 | 1.39 | 48.39 | 51.17 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 2 | 55.34 | 1.57 | 53.77 | 56.91 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 2 | 58.42 | 1.65 | 56.77 | 60.08 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 242.5 | 5.012 | 48.4 | 56.91 | 60.08 | null | null | null | null | null |
| 1 | 1 | upload | 256.1 | 5.006 | 51.2 | 53.77 | 56.77 | null | null | null | null | null |
| 2 | 1 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 3 | 1 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 4 | 1 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 0 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 1 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 2 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 3 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 4 | 4 | upload | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 0 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 1 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 2 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 3 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 4 | 1 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 0 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 1 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 2 | 4 | download | 0.0 | 10.002 | null | null | null | null | null | null | null | null |
| 3 | 4 | download | 0.0 | 10.001 | null | null | null | null | null | null | null | null |
| 4 | 4 | download | 0.0 | 10.002 | null | null | null | null | null | null | null | null |
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
| 0 | 13.480 | 98.4 | 19 | 16 | 14.230 | 88.0 | 17 | 15 |
| 1 | 13.450 | 99.8 | 19 | 16 | 14.200 | 97.6 | 18 | 15 |
| 2 | 0.010 | 100.4 | 19 | 17 | 0.010 | 97.7 | 18 | 15 |
| 3 | 0.020 | 100.4 | 19 | 19 | 0.000 | 97.7 | 18 | 15 |
| 4 | 0.000 | 100.4 | 19 | 21 | 0.010 | 97.7 | 18 | 15 |
| 0 | 0.010 | 100.5 | 19 | 29 | 0.010 | 97.7 | 18 | 15 |
| 1 | 0.020 | 100.6 | 19 | 34 | 0.000 | 97.7 | 18 | 15 |
| 2 | 0.020 | 101.2 | 19 | 40 | 0.010 | 97.8 | 18 | 15 |
| 3 | 0.020 | 101.3 | 19 | 43 | 0.000 | 96.5 | 18 | 15 |
| 4 | 0.000 | 101.4 | 19 | 43 | 0.010 | 96.5 | 18 | 15 |
| 0 | 0.020 | 101.4 | 19 | 37 | 0.000 | 96.5 | 18 | 15 |
| 1 | 0.010 | 101.4 | 19 | 31 | 0.010 | 96.5 | 18 | 15 |
| 2 | 0.010 | 101.4 | 19 | 25 | 0.010 | 96.5 | 18 | 15 |
| 3 | 0.030 | 101.4 | 19 | 22 | 0.010 | 96.5 | 18 | 15 |
| 4 | 0.010 | 100.0 | 19 | 22 | 0.010 | 96.5 | 18 | 15 |
| 0 | 0.010 | 100.0 | 19 | 28 | 0.000 | 96.5 | 18 | 15 |
| 1 | 0.010 | 100.0 | 19 | 34 | 0.010 | 96.5 | 18 | 15 |
| 2 | 0.020 | 100.0 | 19 | 40 | 0.010 | 96.5 | 18 | 15 |
| 3 | 0.010 | 100.0 | 19 | 43 | 0.020 | 96.5 | 18 | 15 |
| 4 | 0.030 | 100.0 | 19 | 43 | 0.020 | 96.5 | 18 | 15 |
| 0 | 2.520 | 100.0 | 19 | 41 | 0.030 | 96.5 | 18 | 15 |
| 1 | 2.870 | 100.0 | 19 | 37 | 0.000 | 96.5 | 18 | 15 |
| 2 | 2.430 | 100.0 | 19 | 33 | 0.000 | 96.5 | 18 | 15 |
| 3 | 2.590 | 100.0 | 19 | 33 | 0.010 | 96.5 | 18 | 15 |
| 4 | 2.530 | 100.0 | 19 | 29 | 0.010 | 96.5 | 18 | 15 |
| 0 | 3.710 | 100.0 | 19 | 29 | 0.000 | 96.5 | 18 | 15 |
| 1 | 3.930 | 100.0 | 19 | 25 | 0.000 | 96.5 | 18 | 15 |
| 2 | 3.870 | 100.0 | 19 | 21 | 0.010 | 96.5 | 18 | 15 |
| 3 | 3.720 | 100.0 | 19 | 21 | 0.000 | 96.5 | 18 | 15 |
| 4 | 3.760 | 100.0 | 19 | 17 | 0.000 | 96.5 | 18 | 15 |
