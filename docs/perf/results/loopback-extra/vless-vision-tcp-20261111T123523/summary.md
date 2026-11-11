# vless-vision-tcp - vless

- run_id: `vless-vision-tcp-20261111T123523`
- kind: e2e
- started: 2026-11-11T12:35:23+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 10 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | duplex | 3 | 446.12 | 5.06 | 441.06 | 452.18 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | duplex | 3 | 3.93 | 0.04 | 3.87 | 3.98 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | duplex | 3 | 2.48 | 0.01 | 2.43 | 2.50 | null | s/GiB |
| goodput | 1 | pingpong | 3 | 2.13 | 0.00 | 2.10 | 2.14 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | pingpong | 3 | 220.59 | 1.04 | 216.66 | 221.63 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | pingpong | 3 | 223.37 | 4.05 | 217.79 | 227.42 | null | s/GiB |
| latency_p50 | 1 | pingpong | 3 | 0.43 | 0.00 | 0.42 | 0.43 | null | ms |
| latency_p95 | 1 | pingpong | 3 | 0.67 | 0.02 | 0.65 | 0.69 | null | ms |
| latency_p99 | 1 | pingpong | 3 | 0.86 | 0.04 | 0.78 | 0.89 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | duplex | 2233.9 | 5.007 | 446.1 | 3.93 | 2.50 | null | null | null | null | null |
| 1 | 1 | duplex | 2209.5 | 5.010 | 441.1 | 3.98 | 2.48 | null | null | null | null | null |
| 2 | 1 | duplex | 2262.8 | 5.004 | 452.2 | 3.87 | 2.43 | null | null | null | null | null |
| 0 | 1 | pingpong | 10.7 | 5.000 | 2.1 | 216.66 | 223.37 | null | null | 0.43 | 0.65 | 0.78 |
| 1 | 1 | pingpong | 10.5 | 5.000 | 2.1 | 220.59 | 227.42 | null | null | 0.43 | 0.69 | 0.89 |
| 2 | 1 | pingpong | 10.7 | 5.000 | 2.1 | 221.63 | 217.79 | null | null | 0.42 | 0.67 | 0.86 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.580 | 62.1 | 16 | 12 | 5.450 | 60.8 | 16 | 13 |
| 1 | 8.580 | 61.8 | 16 | 12 | 5.360 | 61.3 | 18 | 13 |
| 2 | 8.560 | 62.4 | 16 | 12 | 5.360 | 61.5 | 18 | 13 |
| 0 | 2.260 | 62.2 | 16 | 12 | 2.330 | 61.1 | 18 | 13 |
| 1 | 2.260 | 62.2 | 16 | 12 | 2.330 | 61.3 | 18 | 13 |
| 2 | 2.310 | 62.3 | 16 | 12 | 2.270 | 61.3 | 18 | 13 |
