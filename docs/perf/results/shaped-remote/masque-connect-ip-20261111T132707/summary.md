# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T132707`
- kind: e2e
- started: 2026-11-11T13:27:07+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 16 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=remote delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 0.21 | 0.02 | 0.18 | 0.30 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 2 | 269.41 | 11.14 | 258.27 | 280.55 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 2 | 260.36 | 2.09 | 258.27 | 262.45 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 1.24 | 0.07 | 1.17 | 1.39 | null | ratio |
| goodput | 4 | upload | 3 | 0.32 | 0.02 | 0.14 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 2 | 291.79 | 7.11 | 284.68 | 298.90 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 2 | 328.34 | 0.14 | 328.20 | 328.47 | null | s/GiB |
| wire_gbit_per_s | 4 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 4 | upload | 3 | 1.21 | 0.04 | 1.17 | 1.30 | null | ratio |
| goodput | 1 | download | 3 | 0.04 | 0.02 | 0.02 | 0.09 | null | MiB/s |
| wire_gbit_per_s | 1 | download | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 3 | 13.06 | 6.76 | 6.30 | 20.33 | null | ratio |
| goodput | 4 | download | 3 | 0.03 | 0.01 | 0.02 | 0.07 | null | MiB/s |
| wire_gbit_per_s | 4 | download | 3 | 0.00 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 4 | download | 3 | 16.54 | 10.03 | 6.51 | 33.59 | null | ratio |
| goodput | 1 | udp | 1 | 0.03 | 0.00 | 0.03 | 0.03 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 1 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 1 | 26.01 | 0.00 | 26.01 | 26.01 | null | ratio |
| goodput | 4 | udp | 1 | 0.00 | 0.00 | 0.00 | 0.00 | null | MiB/s |
| wire_gbit_per_s | 4 | udp | 1 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 4 | udp | 1 | 244.42 | 0.00 | 244.42 | 244.42 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1.6 | 5.501 | 0.3 | 258.27 | 258.27 | 0.00 | 1.17 | null | null | null |
| 1 | 1 | upload | 1.0 | 5.501 | 0.2 | null | null | 0.00 | 1.39 | null | null | null |
| 2 | 1 | upload | 1.1 | 5.500 | 0.2 | 280.55 | 262.45 | 0.00 | 1.24 | null | null | null |
| 0 | 4 | upload | 0.8 | 5.501 | 0.1 | null | null | 0.00 | 1.30 | null | null | null |
| 1 | 4 | upload | 1.7 | 5.500 | 0.3 | 298.90 | 328.20 | 0.00 | 1.21 | null | null | null |
| 2 | 4 | upload | 1.9 | 5.501 | 0.3 | 284.68 | 328.47 | 0.00 | 1.17 | null | null | null |
| 0 | 1 | download | 0.1 | 5.043 | 0.0 | null | null | 0.00 | 20.33 | null | null | null |
| 1 | 1 | download | 0.5 | 5.251 | 0.1 | null | null | 0.00 | 6.30 | null | null | null |
| 2 | 1 | download | 0.2 | 5.320 | 0.0 | null | null | 0.00 | 13.06 | null | null | null |
| 0 | 4 | download | 0.4 | 5.501 | 0.1 | null | null | 0.00 | 6.51 | null | null | null |
| 1 | 4 | download | 0.2 | 5.501 | 0.0 | null | null | 0.00 | 16.54 | null | null | null |
| 2 | 4 | download | 0.1 | 5.500 | 0.0 | null | null | 0.01 | 33.59 | null | null | null |
| 0 | 1 | udp | 0.1 | 5.000 | 0.0 | null | null | 0.01 | 26.01 | null | null | null |
| 0 | 4 | udp | 0.0 | 5.000 | 0.0 | null | null | 0.01 | 244.42 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.410 | 67.6 | 14 | 14 | 0.410 | 66.4 | 15 | 14 |
| 1 | 0.300 | 69.1 | 16 | 16 | 0.320 | 67.6 | 15 | 16 |
| 2 | 0.310 | 70.0 | 16 | 18 | 0.290 | 67.8 | 15 | 18 |
| 0 | 0.220 | 71.6 | 16 | 26 | 0.220 | 68.1 | 16 | 26 |
| 1 | 0.510 | 75.4 | 16 | 34 | 0.560 | 69.0 | 16 | 34 |
| 2 | 0.520 | 79.2 | 16 | 42 | 0.600 | 70.0 | 16 | 42 |
| 0 | 0.260 | 80.3 | 17 | 43 | 0.310 | 70.3 | 16 | 44 |
| 1 | 0.440 | 81.6 | 18 | 43 | 0.570 | 73.7 | 16 | 46 |
| 2 | 0.400 | 82.8 | 18 | 43 | 0.520 | 74.5 | 17 | 48 |
| 0 | 0.490 | 84.7 | 18 | 46 | 0.590 | 77.2 | 17 | 55 |
| 1 | 0.360 | 84.9 | 18 | 50 | 0.440 | 79.3 | 17 | 62 |
| 2 | 0.530 | 85.4 | 18 | 51 | 0.560 | 81.6 | 17 | 70 |
| 0 | 3.890 | 85.7 | 19 | 44 | 0.590 | 85.3 | 17 | 69 |
| 0 | 3.910 | 87.1 | 20 | 44 | 0.570 | 86.2 | 18 | 74 |

## Failures

- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=257709
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=254301
- **measure** (no-payload): no datagrams returned at 4 stream(s): sent=255987
- **measure** (no-payload): no datagrams returned at 4 stream(s): sent=269737
