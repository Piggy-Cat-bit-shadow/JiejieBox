# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T181200`
- kind: e2e
- started: 2026-11-11T18:12:00+08:00
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
| goodput | 2 | upload | 3 | 50.97 | 0.17 | 48.04 | 51.14 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 59.23 | 0.42 | 58.81 | 62.35 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 62.10 | 0.81 | 61.29 | 65.36 | null | s/GiB |
| goodput | 4 | upload | 3 | 51.90 | 0.73 | 51.02 | 52.63 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 54.49 | 1.18 | 53.31 | 58.25 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 57.37 | 1.23 | 56.14 | 61.21 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 155.5 | 3.041 | 51.1 | 58.81 | 62.10 | null | null | null | null | null |
| 1 | 2 | upload | 146.0 | 3.040 | 48.0 | 62.35 | 65.36 | null | null | null | null | null |
| 2 | 2 | upload | 153.7 | 3.016 | 51.0 | 59.23 | 61.29 | null | null | null | null | null |
| 0 | 4 | upload | 159.1 | 3.065 | 51.9 | 58.25 | 61.21 | null | null | null | null | null |
| 1 | 4 | upload | 162.9 | 3.095 | 52.6 | 53.31 | 56.14 | null | null | null | null | null |
| 2 | 4 | upload | 156.4 | 3.065 | 51.0 | 54.49 | 57.37 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.930 | 95.5 | 19 | 16 | 9.430 | 94.6 | 17 | 15 |
| 1 | 8.890 | 98.1 | 19 | 16 | 9.320 | 98.6 | 17 | 15 |
| 2 | 8.890 | 111.5 | 19 | 16 | 9.200 | 104.8 | 17 | 15 |
| 0 | 9.050 | 112.7 | 20 | 17 | 9.510 | 104.1 | 17 | 16 |
| 1 | 8.480 | 113.9 | 20 | 17 | 8.930 | 103.2 | 17 | 16 |
| 2 | 8.320 | 114.9 | 20 | 17 | 8.760 | 103.1 | 17 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
