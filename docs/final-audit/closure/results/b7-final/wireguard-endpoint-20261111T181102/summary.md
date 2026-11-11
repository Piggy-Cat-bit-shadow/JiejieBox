# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T181102`
- kind: e2e
- started: 2026-11-11T18:11:02+08:00
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
| goodput | 2 | upload | 3 | 39.95 | 3.07 | 31.78 | 43.03 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 71.58 | 6.48 | 65.10 | 78.26 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 73.01 | 5.15 | 67.86 | 82.46 | null | s/GiB |
| goodput | 4 | upload | 3 | 48.63 | 2.66 | 45.97 | 57.38 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 59.94 | 1.68 | 56.10 | 61.62 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 66.40 | 0.97 | 63.19 | 67.37 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 122.0 | 3.054 | 40.0 | 71.58 | 73.01 | null | null | null | null | null |
| 1 | 2 | upload | 97.6 | 3.072 | 31.8 | 78.26 | 82.46 | null | null | null | null | null |
| 2 | 2 | upload | 130.1 | 3.023 | 43.0 | 65.10 | 67.86 | null | null | null | null | null |
| 0 | 4 | upload | 152.1 | 3.127 | 48.6 | 59.94 | 66.40 | null | null | null | null | null |
| 1 | 4 | upload | 140.6 | 3.058 | 46.0 | 61.62 | 67.37 | null | null | null | null | null |
| 2 | 4 | upload | 179.1 | 3.121 | 57.4 | 56.10 | 63.19 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.530 | 92.5 | 18 | 18 | 8.700 | 91.6 | 17 | 17 |
| 1 | 7.460 | 107.9 | 18 | 18 | 7.860 | 114.9 | 19 | 17 |
| 2 | 8.270 | 116.9 | 18 | 18 | 8.620 | 117.4 | 19 | 17 |
| 0 | 8.900 | 123.1 | 19 | 22 | 9.860 | 131.1 | 19 | 21 |
| 1 | 8.460 | 125.9 | 19 | 22 | 9.250 | 138.5 | 20 | 21 |
| 2 | 9.810 | 127.1 | 19 | 21 | 11.050 | 139.1 | 20 | 20 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
