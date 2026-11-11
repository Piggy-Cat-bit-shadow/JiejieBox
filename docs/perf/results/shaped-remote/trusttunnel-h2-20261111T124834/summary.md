# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T124834`
- kind: e2e
- started: 2026-11-11T12:48:34+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 14 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=remote delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 0.33 | 0.05 | 0.28 | 0.40 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 13.81 | 2.41 | 11.40 | 26.78 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 20.08 | 1.67 | 18.41 | 34.21 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 1.00 | 0.01 | 0.92 | 1.01 | null | ratio |
| goodput | 4 | upload | 3 | 0.26 | 0.01 | 0.26 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 14.54 | 0.37 | 14.17 | 24.42 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 29.09 | 5.10 | 21.26 | 34.19 | null | s/GiB |
| wire_gbit_per_s | 4 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 4 | upload | 3 | 0.95 | 0.04 | 0.91 | 1.01 | null | ratio |
| goodput | 1 | download | 1 | 0.00 | 0.00 | 0.00 | 0.00 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 1 | 3276.80 | 0.00 | 3276.80 | 3276.80 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 1 | 3932.16 | 0.00 | 3932.16 | 3932.16 | null | s/GiB |
| wire_gbit_per_s | 1 | download | 1 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 1 | 178.42 | 0.00 | 178.42 | 178.42 | null | ratio |
| goodput | 4 | download | 2 | 0.03 | 0.02 | 0.01 | 0.05 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 2 | 462.61 | 192.75 | 269.85 | 655.36 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 2 | 665.00 | 318.04 | 346.96 | 983.04 | null | s/GiB |
| wire_gbit_per_s | 4 | download | 2 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 4 | download | 2 | 35.29 | 18.51 | 16.78 | 53.79 | null | ratio |
| goodput | 1 | udp | 3 | 0.11 | 0.01 | 0.03 | 0.12 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 224.63 | 25.79 | 198.84 | 546.43 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 336.95 | 88.40 | 248.55 | 751.35 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 3.99 | 0.27 | 3.72 | 10.78 | null | ratio |
| goodput | 4 | udp | 3 | 0.15 | 0.00 | 0.09 | 0.15 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 181.22 | 2.25 | 140.03 | 183.47 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 282.27 | 12.22 | 196.04 | 294.49 | null | s/GiB |
| wire_gbit_per_s | 4 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 4 | udp | 3 | 3.55 | 0.04 | 3.20 | 3.58 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1.5 | 5.500 | 0.3 | 26.78 | 20.08 | 0.00 | 0.92 | null | null | null |
| 1 | 1 | upload | 1.8 | 5.501 | 0.3 | 11.40 | 34.21 | 0.00 | 1.00 | null | null | null |
| 2 | 1 | upload | 2.2 | 5.500 | 0.4 | 13.81 | 18.41 | 0.00 | 1.01 | null | null | null |
| 0 | 4 | upload | 1.4 | 5.501 | 0.3 | 14.54 | 29.09 | 0.00 | 0.95 | null | null | null |
| 1 | 4 | upload | 1.4 | 5.501 | 0.3 | 14.17 | 21.26 | 0.00 | 0.91 | null | null | null |
| 2 | 4 | upload | 2.1 | 7.392 | 0.3 | 24.42 | 34.19 | 0.00 | 1.01 | null | null | null |
| 0 | 1 | download | 0.0 | 5.403 | 0.0 | 3276.80 | 3932.16 | 0.00 | 178.42 | null | null | null |
| 0 | 4 | download | 0.3 | 5.501 | 0.0 | 269.85 | 346.96 | 0.01 | 16.78 | null | null | null |
| 1 | 4 | download | 0.1 | 5.501 | 0.0 | 655.36 | 983.04 | 0.01 | 53.79 | null | null | null |
| 0 | 1 | udp | 0.1 | 5.000 | 0.0 | 546.43 | 751.35 | 0.00 | 10.78 | null | null | null |
| 1 | 1 | udp | 0.5 | 5.000 | 0.1 | 224.63 | 336.95 | 0.00 | 3.72 | null | null | null |
| 2 | 1 | udp | 0.6 | 5.000 | 0.1 | 198.84 | 248.55 | 0.00 | 3.99 | null | null | null |
| 0 | 4 | udp | 0.7 | 5.000 | 0.1 | 183.47 | 282.27 | 0.00 | 3.55 | null | null | null |
| 1 | 4 | udp | 0.5 | 5.000 | 0.1 | 181.22 | 294.49 | 0.00 | 3.58 | null | null | null |
| 2 | 4 | udp | 0.7 | 5.000 | 0.1 | 140.03 | 196.04 | 0.00 | 3.20 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.040 | 58.9 | 14 | 14 | 0.030 | 60.6 | 11 | 16 |
| 1 | 0.020 | 59.0 | 14 | 16 | 0.060 | 61.2 | 11 | 17 |
| 2 | 0.030 | 59.2 | 14 | 16 | 0.040 | 61.4 | 11 | 18 |
| 0 | 0.020 | 60.2 | 15 | 19 | 0.040 | 61.7 | 11 | 20 |
| 1 | 0.020 | 60.7 | 15 | 20 | 0.030 | 62.0 | 11 | 22 |
| 2 | 0.050 | 61.2 | 15 | 26 | 0.070 | 62.5 | 13 | 25 |
| 0 | 0.050 | 62.6 | 15 | 23 | 0.060 | 62.9 | 13 | 24 |
| 0 | 0.070 | 67.0 | 15 | 24 | 0.090 | 63.7 | 13 | 26 |
| 1 | 0.040 | 68.9 | 15 | 24 | 0.060 | 64.4 | 14 | 24 |
| 0 | 0.080 | 70.0 | 15 | 16 | 0.110 | 65.1 | 14 | 19 |
| 1 | 0.120 | 70.8 | 15 | 13 | 0.180 | 65.2 | 14 | 14 |
| 2 | 0.120 | 71.5 | 15 | 13 | 0.150 | 65.5 | 14 | 14 |
| 0 | 0.130 | 71.6 | 15 | 13 | 0.200 | 66.0 | 14 | 14 |
| 1 | 0.080 | 71.9 | 15 | 13 | 0.130 | 66.4 | 14 | 14 |
| 2 | 0.100 | 72.1 | 15 | 13 | 0.140 | 67.0 | 14 | 14 |

## Failures

- **measure** (no-payload): no payload arrived for download at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for download at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for download at 4 stream(s): sent=0 target_received=0 client_received=0
