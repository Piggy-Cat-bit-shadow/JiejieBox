# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T181329`
- kind: e2e
- started: 2026-11-11T18:13:29+08:00
- commit: `897553e81221` (docs(closure): record the three self-check rounds and the final HEAD)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 54.13 | 0.98 | 53.15 | 55.12 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 53.45 | 0.39 | 52.04 | 53.84 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 55.14 | 0.79 | 54.12 | 55.93 | null | s/GiB |
| goodput | 4 | upload | 3 | 58.13 | 0.08 | 56.84 | 58.21 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 50.66 | 0.76 | 49.36 | 51.42 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 53.20 | 1.35 | 51.85 | 54.65 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 167.4 | 3.038 | 55.1 | 52.04 | 54.12 | null | null | null | null | null |
| 1 | 2 | upload | 163.8 | 3.026 | 54.1 | 53.45 | 55.14 | null | null | null | null | null |
| 2 | 2 | upload | 161.7 | 3.041 | 53.2 | 53.84 | 55.93 | null | null | null | null | null |
| 0 | 4 | upload | 177.2 | 3.118 | 56.8 | 51.42 | 54.65 | null | null | null | null | null |
| 1 | 4 | upload | 177.5 | 3.053 | 58.1 | 50.66 | 53.20 | null | null | null | null | null |
| 2 | 4 | upload | 176.6 | 3.033 | 58.2 | 49.36 | 51.85 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.510 | 100.3 | 17 | 16 | 8.850 | 92.6 | 15 | 15 |
| 1 | 8.550 | 108.5 | 19 | 16 | 8.820 | 95.6 | 17 | 15 |
| 2 | 8.500 | 113.3 | 20 | 16 | 8.830 | 100.5 | 17 | 15 |
| 0 | 8.900 | 119.7 | 20 | 17 | 9.460 | 121.3 | 17 | 16 |
| 1 | 8.780 | 121.8 | 20 | 17 | 9.220 | 120.1 | 17 | 16 |
| 2 | 8.510 | 121.4 | 20 | 17 | 8.940 | 120.1 | 17 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
