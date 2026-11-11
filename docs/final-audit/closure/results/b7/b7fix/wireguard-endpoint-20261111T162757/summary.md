# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T162757`
- kind: e2e
- started: 2026-11-11T16:27:57+08:00
- commit: `fa256e89afd8` (merge: start the closure round from the current testing tip)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 45.84 | 2.97 | 42.86 | 53.86 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 62.78 | 2.63 | 57.14 | 65.41 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 68.05 | 0.37 | 60.47 | 68.42 | null | s/GiB |
| goodput | 4 | upload | 3 | 53.68 | 1.10 | 49.83 | 54.79 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 60.87 | 3.05 | 57.82 | 65.31 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 67.14 | 2.27 | 64.87 | 73.19 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 132.6 | 3.094 | 42.9 | 65.41 | 68.42 | null | null | null | null | null |
| 1 | 2 | upload | 137.8 | 3.007 | 45.8 | 62.78 | 68.05 | null | null | null | null | null |
| 2 | 2 | upload | 163.1 | 3.028 | 53.9 | 57.14 | 60.47 | null | null | null | null | null |
| 0 | 4 | upload | 153.5 | 3.081 | 49.8 | 65.31 | 73.19 | null | null | null | null | null |
| 1 | 4 | upload | 164.9 | 3.071 | 53.7 | 60.87 | 67.14 | null | null | null | null | null |
| 2 | 4 | upload | 170.0 | 3.103 | 54.8 | 57.82 | 64.87 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.470 | 102.0 | 20 | 19 | 8.860 | 96.4 | 16 | 18 |
| 1 | 8.450 | 112.0 | 20 | 19 | 9.160 | 101.7 | 16 | 18 |
| 2 | 9.100 | 118.7 | 20 | 19 | 9.630 | 105.5 | 16 | 18 |
| 0 | 9.790 | 121.0 | 20 | 23 | 10.970 | 113.1 | 17 | 22 |
| 1 | 9.800 | 126.3 | 21 | 23 | 10.810 | 114.6 | 17 | 22 |
| 2 | 9.600 | 127.8 | 21 | 23 | 10.770 | 119.1 | 17 | 22 |
