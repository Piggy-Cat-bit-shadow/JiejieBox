# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T162826`
- kind: e2e
- started: 2026-11-11T16:28:26+08:00
- commit: `fa256e89afd8` (merge: start the closure round from the current testing tip)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 48.88 | 1.33 | 47.54 | 51.83 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 57.94 | 1.92 | 55.86 | 59.85 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 60.48 | 1.22 | 59.27 | 61.77 | null | s/GiB |
| goodput | 4 | upload | 3 | 55.78 | 1.15 | 54.63 | 60.49 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 57.40 | 1.71 | 54.27 | 59.11 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 62.32 | 2.10 | 59.26 | 64.41 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 156.5 | 3.020 | 51.8 | 55.86 | 59.27 | null | null | null | null | null |
| 1 | 2 | upload | 148.8 | 3.045 | 48.9 | 57.94 | 60.48 | null | null | null | null | null |
| 2 | 2 | upload | 144.4 | 3.037 | 47.5 | 59.85 | 61.77 | null | null | null | null | null |
| 0 | 4 | upload | 166.1 | 3.041 | 54.6 | 59.11 | 64.41 | null | null | null | null | null |
| 1 | 4 | upload | 170.7 | 3.061 | 55.8 | 57.40 | 62.32 | null | null | null | null | null |
| 2 | 4 | upload | 189.0 | 3.125 | 60.5 | 54.27 | 59.26 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.540 | 101.5 | 17 | 18 | 9.060 | 92.8 | 16 | 16 |
| 1 | 8.420 | 111.8 | 18 | 18 | 8.790 | 94.7 | 16 | 17 |
| 2 | 8.440 | 113.3 | 20 | 18 | 8.710 | 105.5 | 17 | 17 |
| 0 | 9.590 | 115.0 | 20 | 19 | 10.450 | 126.8 | 17 | 18 |
| 1 | 9.570 | 120.6 | 20 | 20 | 10.390 | 127.6 | 18 | 19 |
| 2 | 10.020 | 120.8 | 20 | 19 | 10.940 | 127.5 | 18 | 18 |
