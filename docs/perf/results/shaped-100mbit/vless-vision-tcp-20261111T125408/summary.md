# vless-vision-tcp - vless

- run_id: `vless-vision-tcp-20261111T125408`
- kind: e2e
- started: 2026-11-11T12:54:08+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 14 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=normal delay=10.0ms loss=0.00% bandwidth=100Mbps mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 11.80 | 0.04 | 11.76 | 12.60 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 3.80 | 0.10 | 3.51 | 3.90 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 14.40 | 0.28 | 13.56 | 14.69 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.09 | 0.00 | 0.09 | 0.09 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 0.95 | 0.00 | 0.89 | 0.96 | null | ratio |
| goodput | 1 | download | 3 | 11.05 | 0.04 | 10.96 | 11.10 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 14.03 | 0.20 | 13.82 | 14.26 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1.31 | 0.02 | 1.29 | 1.48 | null | s/GiB |
| wire_gbit_per_s | 1 | download | 3 | 0.09 | 0.00 | 0.09 | 0.09 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 3 | 1.01 | 0.00 | 1.01 | 1.01 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 59.3 | 5.022 | 11.8 | 3.80 | 14.69 | 0.09 | 0.95 | null | null | null |
| 1 | 1 | upload | 64.2 | 5.093 | 12.6 | 3.51 | 13.56 | 0.09 | 0.89 | null | null | null |
| 2 | 1 | upload | 60.4 | 5.138 | 11.8 | 3.90 | 14.40 | 0.09 | 0.96 | null | null | null |
| 0 | 1 | download | 55.3 | 5.001 | 11.1 | 14.26 | 1.48 | 0.09 | 1.01 | null | null | null |
| 1 | 1 | download | 54.8 | 5.001 | 11.0 | 13.82 | 1.31 | 0.09 | 1.01 | null | null | null |
| 2 | 1 | download | 55.5 | 5.001 | 11.1 | 14.03 | 1.29 | 0.09 | 1.01 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.220 | 58.5 | 11 | 12 | 0.850 | 58.0 | 11 | 13 |
| 1 | 0.220 | 59.0 | 11 | 12 | 0.850 | 59.6 | 12 | 13 |
| 2 | 0.230 | 59.4 | 11 | 12 | 0.850 | 60.0 | 12 | 13 |
| 0 | 0.770 | 60.2 | 12 | 12 | 0.080 | 60.7 | 14 | 13 |
| 1 | 0.740 | 60.7 | 12 | 12 | 0.070 | 60.8 | 14 | 13 |
| 2 | 0.760 | 61.4 | 13 | 12 | 0.070 | 60.8 | 14 | 13 |
