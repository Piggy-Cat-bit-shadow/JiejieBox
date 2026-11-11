# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T125505`
- kind: e2e
- started: 2026-11-11T12:55:05+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 14 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=normal delay=10.0ms loss=0.00% bandwidth=100Mbps mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 11.79 | 0.02 | 11.38 | 11.81 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 13.71 | 0.56 | 13.15 | 22.46 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 21.17 | 0.41 | 20.76 | 36.48 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.10 | 0.00 | 0.10 | 0.10 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 0.97 | 0.00 | 0.97 | 1.02 | null | ratio |
| goodput | 1 | download | 3 | 11.23 | 0.00 | 11.23 | 11.23 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 17.87 | 0.54 | 17.14 | 18.41 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 16.23 | 0.18 | 16.04 | 16.77 | null | s/GiB |
| wire_gbit_per_s | 1 | download | 3 | 0.10 | 0.00 | 0.10 | 0.10 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 3 | 1.02 | 0.00 | 1.02 | 1.02 | null | ratio |
| goodput | 1 | udp | 3 | 10.15 | 0.03 | 10.10 | 10.18 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 139.34 | 7.55 | 129.97 | 146.89 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 170.58 | 9.22 | 161.36 | 184.82 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.19 | 0.00 | 0.19 | 0.20 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.29 | 0.00 | 2.28 | 2.29 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 57.0 | 5.007 | 11.4 | 22.46 | 36.48 | 0.10 | 1.02 | null | null | null |
| 1 | 1 | upload | 59.2 | 5.012 | 11.8 | 13.15 | 20.76 | 0.10 | 0.97 | null | null | null |
| 2 | 1 | upload | 59.0 | 5.006 | 11.8 | 13.71 | 21.17 | 0.10 | 0.97 | null | null | null |
| 0 | 1 | download | 56.2 | 5.001 | 11.2 | 18.41 | 16.77 | 0.10 | 1.02 | null | null | null |
| 1 | 1 | download | 56.2 | 5.000 | 11.2 | 17.87 | 16.23 | 0.10 | 1.02 | null | null | null |
| 2 | 1 | download | 56.2 | 5.000 | 11.2 | 17.14 | 16.04 | 0.10 | 1.02 | null | null | null |
| 0 | 1 | udp | 50.9 | 5.000 | 10.2 | 129.97 | 161.36 | 0.19 | 2.28 | null | null | null |
| 1 | 1 | udp | 50.5 | 5.000 | 10.1 | 139.34 | 170.58 | 0.19 | 2.29 | null | null | null |
| 2 | 1 | udp | 50.7 | 5.000 | 10.1 | 146.89 | 184.82 | 0.20 | 2.29 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.250 | 60.0 | 13 | 12 | 2.030 | 62.1 | 13 | 14 |
| 1 | 0.760 | 60.5 | 13 | 12 | 1.200 | 62.9 | 13 | 14 |
| 2 | 0.790 | 60.8 | 13 | 12 | 1.220 | 63.5 | 14 | 14 |
| 0 | 1.010 | 61.6 | 14 | 12 | 0.920 | 63.7 | 14 | 14 |
| 1 | 0.980 | 61.8 | 14 | 12 | 0.890 | 63.9 | 14 | 14 |
| 2 | 0.940 | 62.0 | 14 | 12 | 0.880 | 64.0 | 14 | 14 |
| 0 | 6.460 | 65.8 | 16 | 13 | 8.020 | 66.7 | 16 | 14 |
| 1 | 6.870 | 64.9 | 16 | 13 | 8.410 | 67.6 | 16 | 14 |
| 2 | 7.280 | 65.4 | 16 | 13 | 9.160 | 67.5 | 17 | 14 |
