# vless-vision-tcp - vless

- run_id: `vless-vision-tcp-20261111T124649`
- kind: e2e
- started: 2026-11-11T12:46:49+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 14 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=remote delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 1.49 | 0.08 | 1.41 | 3.21 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 5.00 | 0.27 | 4.05 | 5.27 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 14.50 | 3.00 | 5.79 | 17.51 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.01 | 0.00 | 0.01 | 0.03 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 0.99 | 0.00 | 0.98 | 0.99 | null | ratio |
| goodput | 4 | upload | 3 | 5.60 | 0.54 | 5.07 | 6.32 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 4.71 | 0.06 | 4.65 | 5.51 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 14.33 | 0.95 | 12.66 | 15.28 | null | s/GiB |
| wire_gbit_per_s | 4 | upload | 3 | 0.04 | 0.00 | 0.04 | 0.05 | null | Gbit/s |
| wire_overhead_ratio | 4 | upload | 3 | 0.96 | 0.02 | 0.91 | 0.98 | null | ratio |
| goodput | 1 | download | 3 | 0.59 | 0.12 | 0.48 | 1.00 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 25.50 | 5.29 | 10.11 | 30.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 63.76 | 45.72 | 10.11 | 109.48 | null | s/GiB |
| wire_gbit_per_s | 1 | download | 3 | 0.02 | 0.00 | 0.01 | 0.03 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 3 | 4.14 | 2.66 | 1.40 | 6.80 | null | ratio |
| goodput | 4 | download | 3 | 2.56 | 0.36 | 2.20 | 3.07 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 12.50 | 0.13 | 9.91 | 12.62 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 3.61 | 0.30 | 3.30 | 3.91 | null | s/GiB |
| wire_gbit_per_s | 4 | download | 3 | 0.02 | 0.00 | 0.02 | 0.03 | null | Gbit/s |
| wire_overhead_ratio | 4 | download | 3 | 1.05 | 0.00 | 1.03 | 1.05 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 17.7 | 5.501 | 3.2 | 4.05 | 5.79 | 0.03 | 0.99 | null | null | null |
| 1 | 1 | upload | 7.8 | 5.501 | 1.4 | 5.27 | 14.50 | 0.01 | 0.98 | null | null | null |
| 2 | 1 | upload | 8.2 | 5.501 | 1.5 | 5.00 | 17.51 | 0.01 | 0.99 | null | null | null |
| 0 | 4 | upload | 27.9 | 5.501 | 5.1 | 5.51 | 14.33 | 0.04 | 0.98 | null | null | null |
| 1 | 4 | upload | 34.8 | 5.501 | 6.3 | 4.71 | 12.66 | 0.05 | 0.96 | null | null | null |
| 2 | 4 | upload | 30.8 | 5.501 | 5.6 | 4.65 | 15.28 | 0.04 | 0.91 | null | null | null |
| 0 | 1 | download | 3.0 | 5.031 | 0.6 | 30.79 | 109.48 | 0.03 | 6.80 | null | null | null |
| 1 | 1 | download | 2.4 | 5.032 | 0.5 | 25.50 | 63.76 | 0.02 | 4.14 | null | null | null |
| 2 | 1 | download | 5.1 | 5.075 | 1.0 | 10.11 | 10.11 | 0.01 | 1.40 | null | null | null |
| 0 | 4 | download | 15.5 | 5.055 | 3.1 | 9.91 | 3.30 | 0.03 | 1.03 | null | null | null |
| 1 | 4 | download | 13.1 | 5.116 | 2.6 | 12.50 | 3.91 | 0.02 | 1.05 | null | null | null |
| 2 | 4 | download | 11.4 | 5.153 | 2.2 | 12.62 | 3.61 | 0.02 | 1.05 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.070 | 58.1 | 11 | 14 | 0.100 | 57.3 | 12 | 15 |
| 1 | 0.040 | 58.5 | 11 | 16 | 0.110 | 58.3 | 13 | 19 |
| 2 | 0.040 | 59.4 | 12 | 20 | 0.140 | 58.7 | 13 | 21 |
| 0 | 0.150 | 61.7 | 13 | 30 | 0.390 | 61.0 | 14 | 35 |
| 1 | 0.160 | 62.6 | 13 | 36 | 0.430 | 62.9 | 14 | 41 |
| 2 | 0.140 | 64.1 | 14 | 38 | 0.460 | 64.1 | 15 | 45 |
| 0 | 0.090 | 64.2 | 14 | 34 | 0.320 | 64.4 | 15 | 37 |
| 1 | 0.060 | 64.4 | 14 | 20 | 0.150 | 64.5 | 15 | 29 |
| 2 | 0.050 | 64.6 | 14 | 14 | 0.050 | 64.5 | 15 | 17 |
| 0 | 0.150 | 65.8 | 15 | 18 | 0.050 | 64.7 | 15 | 19 |
| 1 | 0.160 | 66.1 | 15 | 18 | 0.050 | 64.8 | 15 | 19 |
| 2 | 0.140 | 66.2 | 15 | 18 | 0.040 | 65.1 | 15 | 19 |
