# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T181234`
- kind: e2e
- started: 2026-11-11T18:12:34+08:00
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
| goodput | 2 | upload | 3 | 52.81 | 0.88 | 48.86 | 53.68 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 57.01 | 1.66 | 55.35 | 58.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 60.38 | 1.33 | 57.59 | 61.70 | null | s/GiB |
| goodput | 4 | upload | 3 | 46.83 | 4.31 | 42.52 | 51.75 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 65.33 | 3.36 | 57.50 | 68.69 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 72.09 | 1.81 | 63.46 | 73.90 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 164.5 | 3.065 | 53.7 | 57.01 | 60.38 | null | null | null | null | null |
| 1 | 2 | upload | 147.7 | 3.023 | 48.9 | 58.79 | 61.70 | null | null | null | null | null |
| 2 | 2 | upload | 160.0 | 3.030 | 52.8 | 55.35 | 57.59 | null | null | null | null | null |
| 0 | 4 | upload | 159.9 | 3.090 | 51.7 | 57.50 | 63.46 | null | null | null | null | null |
| 1 | 4 | upload | 145.5 | 3.106 | 46.8 | 65.33 | 72.09 | null | null | null | null | null |
| 2 | 4 | upload | 135.7 | 3.191 | 42.5 | 68.69 | 73.90 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 9.160 | 105.4 | 16 | 18 | 9.700 | 99.0 | 16 | 16 |
| 1 | 8.480 | 112.5 | 17 | 18 | 8.900 | 119.5 | 16 | 17 |
| 2 | 8.650 | 114.7 | 17 | 18 | 9.000 | 120.3 | 16 | 17 |
| 0 | 8.980 | 118.4 | 17 | 22 | 9.910 | 132.9 | 16 | 21 |
| 1 | 9.280 | 121.1 | 17 | 22 | 10.240 | 132.4 | 16 | 21 |
| 2 | 9.100 | 121.5 | 19 | 22 | 9.790 | 132.4 | 17 | 21 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
