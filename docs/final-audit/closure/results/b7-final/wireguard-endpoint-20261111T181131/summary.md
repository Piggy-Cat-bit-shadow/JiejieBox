# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T181131`
- kind: e2e
- started: 2026-11-11T18:11:31+08:00
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
| goodput | 2 | upload | 3 | 55.38 | 3.08 | 50.53 | 58.46 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 56.06 | 2.88 | 53.18 | 61.06 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 59.12 | 2.63 | 56.49 | 63.19 | null | s/GiB |
| goodput | 4 | upload | 3 | 51.28 | 5.22 | 46.06 | 56.61 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 59.06 | 3.51 | 55.55 | 64.53 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 65.00 | 4.03 | 60.77 | 69.03 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 153.8 | 3.044 | 50.5 | 61.06 | 63.19 | null | null | null | null | null |
| 1 | 2 | upload | 167.7 | 3.028 | 55.4 | 56.06 | 59.12 | null | null | null | null | null |
| 2 | 2 | upload | 176.7 | 3.024 | 58.5 | 53.18 | 56.49 | null | null | null | null | null |
| 0 | 4 | upload | 182.5 | 3.224 | 56.6 | 55.55 | 60.77 | null | null | null | null | null |
| 1 | 4 | upload | 141.1 | 3.063 | 46.1 | 64.53 | 69.03 | null | null | null | null | null |
| 2 | 4 | upload | 162.3 | 3.164 | 51.3 | 59.06 | 65.00 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 9.170 | 100.3 | 19 | 17 | 9.490 | 87.1 | 16 | 16 |
| 1 | 9.180 | 105.1 | 19 | 17 | 9.680 | 101.9 | 17 | 16 |
| 2 | 9.180 | 105.8 | 19 | 16 | 9.750 | 102.6 | 17 | 15 |
| 0 | 9.900 | 116.6 | 19 | 19 | 10.830 | 107.7 | 17 | 18 |
| 1 | 8.890 | 118.1 | 19 | 18 | 9.510 | 108.8 | 18 | 17 |
| 2 | 9.360 | 122.7 | 19 | 19 | 10.300 | 122.7 | 18 | 18 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
