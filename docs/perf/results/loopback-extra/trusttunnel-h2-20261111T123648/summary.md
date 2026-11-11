# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T123648`
- kind: e2e
- started: 2026-11-11T12:36:48+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 10 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | duplex | 3 | 172.07 | 0.01 | 170.48 | 172.08 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | duplex | 3 | 10.15 | 0.10 | 10.05 | 10.25 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | duplex | 3 | 10.30 | 0.01 | 10.29 | 10.40 | null | s/GiB |
| goodput | 1 | pingpong | 3 | 1.33 | 0.04 | 1.30 | 1.38 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | pingpong | 3 | 467.62 | 2.97 | 459.65 | 470.60 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | pingpong | 3 | 565.72 | 2.78 | 562.93 | 584.30 | null | s/GiB |
| latency_p50 | 1 | pingpong | 3 | 0.70 | 0.02 | 0.68 | 0.72 | null | ms |
| latency_p95 | 1 | pingpong | 3 | 1.02 | 0.03 | 0.99 | 1.06 | null | ms |
| latency_p99 | 1 | pingpong | 3 | 1.22 | 0.03 | 1.17 | 1.25 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | duplex | 862.9 | 5.015 | 172.1 | 10.15 | 10.30 | null | null | null | null | null |
| 1 | 1 | duplex | 862.2 | 5.010 | 172.1 | 10.05 | 10.29 | null | null | null | null | null |
| 2 | 1 | duplex | 854.3 | 5.011 | 170.5 | 10.25 | 10.40 | null | null | null | null | null |
| 0 | 1 | pingpong | 6.7 | 5.000 | 1.3 | 459.65 | 565.72 | null | null | 0.70 | 1.02 | 1.22 |
| 1 | 1 | pingpong | 6.5 | 5.001 | 1.3 | 470.60 | 584.30 | null | null | 0.72 | 1.06 | 1.25 |
| 2 | 1 | pingpong | 6.9 | 5.000 | 1.4 | 467.62 | 562.93 | null | null | 0.68 | 0.99 | 1.17 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.550 | 63.6 | 13 | 12 | 8.680 | 67.1 | 15 | 14 |
| 1 | 8.460 | 65.3 | 15 | 12 | 8.660 | 68.1 | 16 | 14 |
| 2 | 8.550 | 65.8 | 15 | 12 | 8.680 | 68.5 | 17 | 14 |
| 0 | 2.990 | 66.3 | 15 | 12 | 3.680 | 67.7 | 17 | 14 |
| 1 | 2.980 | 66.3 | 15 | 12 | 3.700 | 67.5 | 17 | 14 |
| 2 | 3.140 | 66.3 | 15 | 12 | 3.780 | 67.5 | 17 | 14 |
