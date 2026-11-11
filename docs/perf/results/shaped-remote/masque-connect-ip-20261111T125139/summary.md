# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T125139`
- kind: e2e
- started: 2026-11-11T12:51:39+08:00
- commit: `bf0e0bec5600` (perf(lab): add the stage-1 performance laboratory)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 14 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=remote delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 0.31 | 0.04 | 0.26 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 243.29 | 35.19 | 190.09 | 278.48 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 249.37 | 32.12 | 217.25 | 307.04 | null | s/GiB |
| wire_gbit_per_s | 1 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | upload | 3 | 1.14 | 0.08 | 1.07 | 1.35 | null | ratio |
| goodput | 4 | upload | 3 | 0.20 | 0.01 | 0.18 | 0.27 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 254.53 | 1.36 | 253.17 | 280.72 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 263.96 | 0.66 | 263.30 | 322.83 | null | s/GiB |
| wire_gbit_per_s | 4 | upload | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 4 | upload | 3 | 1.28 | 0.02 | 1.26 | 1.42 | null | ratio |
| goodput | 1 | download | 3 | 0.06 | 0.01 | 0.04 | 0.07 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 1225.45 | 228.52 | 996.94 | 2672.97 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1354.45 | 95.16 | 1259.29 | 3421.40 | null | s/GiB |
| wire_gbit_per_s | 1 | download | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | download | 3 | 6.67 | 0.09 | 6.58 | 15.24 | null | ratio |
| goodput | 4 | download | 3 | 0.06 | 0.00 | 0.05 | 0.10 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 1495.69 | 37.40 | 1007.21 | 1533.10 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 1866.38 | 11.93 | 1216.26 | 1878.31 | null | s/GiB |
| wire_gbit_per_s | 4 | download | 3 | 0.00 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 4 | download | 3 | 9.79 | 0.01 | 6.90 | 9.80 | null | ratio |
| goodput | 1 | udp | 2 | 0.03 | 0.01 | 0.02 | 0.04 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 2 | 33644.41 | 16022.63 | 17621.78 | 49667.04 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 2 | 4249.96 | 2104.31 | 2145.66 | 6354.27 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 2 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 2 | 24.02 | 11.52 | 12.50 | 35.54 | null | ratio |
| goodput | 4 | udp | 3 | 0.01 | 0.01 | 0.00 | 0.04 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 80530.64 | 57901.24 | 22629.40 | 1762726.16 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 11591.53 | 8705.13 | 2886.40 | 192378.74 | null | s/GiB |
| wire_gbit_per_s | 4 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 4 | udp | 3 | 64.97 | 48.97 | 16.00 | 1107.03 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1.7 | 5.500 | 0.3 | 243.29 | 249.37 | 0.00 | 1.07 | null | null | null |
| 1 | 1 | upload | 1.9 | 5.500 | 0.3 | 190.09 | 217.25 | 0.00 | 1.14 | null | null | null |
| 2 | 1 | upload | 1.4 | 5.501 | 0.3 | 278.48 | 307.04 | 0.00 | 1.35 | null | null | null |
| 0 | 4 | upload | 1.1 | 5.502 | 0.2 | 254.53 | 263.96 | 0.00 | 1.42 | null | null | null |
| 1 | 4 | upload | 1.0 | 5.501 | 0.2 | 253.17 | 263.30 | 0.00 | 1.26 | null | null | null |
| 2 | 4 | upload | 1.5 | 5.501 | 0.3 | 280.72 | 322.83 | 0.00 | 1.28 | null | null | null |
| 0 | 1 | download | 0.4 | 5.463 | 0.1 | 996.94 | 1259.29 | 0.00 | 6.58 | null | null | null |
| 1 | 1 | download | 0.3 | 5.397 | 0.1 | 1225.45 | 1354.45 | 0.00 | 6.67 | null | null | null |
| 2 | 1 | download | 0.2 | 5.380 | 0.0 | 2672.97 | 3421.40 | 0.00 | 15.24 | null | null | null |
| 0 | 4 | download | 0.3 | 5.501 | 0.1 | 1533.10 | 1866.38 | 0.00 | 9.80 | null | null | null |
| 1 | 4 | download | 0.3 | 5.501 | 0.1 | 1495.69 | 1878.31 | 0.00 | 9.79 | null | null | null |
| 2 | 4 | download | 0.5 | 5.501 | 0.1 | 1007.21 | 1216.26 | 0.01 | 6.90 | null | null | null |
| 0 | 1 | udp | 0.2 | 5.000 | 0.0 | 17621.78 | 2145.66 | 0.00 | 12.50 | null | null | null |
| 1 | 1 | udp | 0.1 | 5.000 | 0.0 | 49667.04 | 6354.27 | 0.00 | 35.54 | null | null | null |
| 0 | 4 | udp | 0.1 | 5.000 | 0.0 | 80530.64 | 11591.53 | 0.01 | 64.97 | null | null | null |
| 1 | 4 | udp | 0.0 | 5.000 | 0.0 | 1762726.16 | 192378.74 | 0.00 | 1107.03 | null | null | null |
| 2 | 4 | udp | 0.2 | 5.000 | 0.0 | 22629.40 | 2886.40 | 0.00 | 16.00 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.400 | 67.0 | 14 | 14 | 0.410 | 65.9 | 14 | 14 |
| 1 | 0.350 | 68.3 | 15 | 16 | 0.400 | 67.5 | 15 | 16 |
| 2 | 0.390 | 69.8 | 15 | 18 | 0.430 | 67.9 | 15 | 18 |
| 0 | 0.270 | 73.4 | 17 | 26 | 0.280 | 68.2 | 15 | 25 |
| 1 | 0.250 | 74.2 | 17 | 33 | 0.260 | 68.6 | 15 | 32 |
| 2 | 0.400 | 76.5 | 17 | 40 | 0.460 | 69.6 | 15 | 36 |
| 0 | 0.380 | 78.1 | 17 | 37 | 0.480 | 71.3 | 16 | 38 |
| 1 | 0.380 | 80.4 | 17 | 37 | 0.420 | 71.8 | 17 | 39 |
| 2 | 0.500 | 81.6 | 17 | 37 | 0.640 | 72.7 | 17 | 41 |
| 0 | 0.460 | 82.5 | 17 | 40 | 0.560 | 75.9 | 17 | 46 |
| 1 | 0.430 | 83.1 | 17 | 40 | 0.540 | 75.6 | 17 | 50 |
| 2 | 0.530 | 84.1 | 17 | 40 | 0.640 | 79.0 | 17 | 55 |
| 0 | 3.860 | 85.9 | 17 | 39 | 0.470 | 81.4 | 18 | 55 |
| 1 | 3.830 | 86.3 | 19 | 39 | 0.490 | 81.4 | 18 | 57 |
| 0 | 3.960 | 85.1 | 20 | 39 | 0.570 | 82.6 | 18 | 60 |
| 1 | 3.940 | 85.6 | 20 | 39 | 0.430 | 82.9 | 18 | 61 |
| 2 | 3.920 | 85.3 | 20 | 39 | 0.500 | 83.3 | 18 | 61 |

## Failures

- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=263316
