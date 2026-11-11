# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T162854`
- kind: e2e
- started: 2026-11-11T16:28:54+08:00
- commit: `fa256e89afd8` (merge: start the closure round from the current testing tip)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 46.57 | 1.46 | 44.68 | 48.03 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 63.88 | 0.57 | 60.88 | 64.44 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 66.37 | 0.02 | 63.11 | 66.39 | null | s/GiB |
| goodput | 4 | upload | 3 | 48.58 | 2.11 | 46.47 | 52.98 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 59.14 | 0.31 | 58.83 | 64.75 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 63.05 | 0.65 | 62.40 | 67.96 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 142.1 | 3.050 | 46.6 | 64.44 | 66.39 | null | null | null | null | null |
| 1 | 2 | upload | 147.2 | 3.064 | 48.0 | 60.88 | 63.11 | null | null | null | null | null |
| 2 | 2 | upload | 135.6 | 3.035 | 44.7 | 63.88 | 66.37 | null | null | null | null | null |
| 0 | 4 | upload | 149.0 | 3.067 | 48.6 | 58.83 | 62.40 | null | null | null | null | null |
| 1 | 4 | upload | 146.6 | 3.155 | 46.5 | 64.75 | 67.96 | null | null | null | null | null |
| 2 | 4 | upload | 162.4 | 3.066 | 53.0 | 59.14 | 63.05 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.940 | 94.4 | 17 | 17 | 9.210 | 91.7 | 16 | 16 |
| 1 | 8.750 | 102.9 | 17 | 17 | 9.070 | 116.7 | 16 | 16 |
| 2 | 8.460 | 105.2 | 17 | 17 | 8.790 | 117.5 | 16 | 16 |
| 0 | 8.560 | 112.5 | 17 | 18 | 9.080 | 117.8 | 17 | 17 |
| 1 | 9.270 | 115.8 | 18 | 18 | 9.730 | 116.2 | 18 | 17 |
| 2 | 9.380 | 116.1 | 19 | 18 | 10.000 | 130.4 | 18 | 17 |
