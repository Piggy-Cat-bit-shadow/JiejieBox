# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T184651`
- kind: e2e
- started: 2026-11-11T18:46:51+08:00
- commit: `067cceb562e9` (docs(closure): finalize B7 evidence chain and index)
- branch: `work/closure-final-reconcile-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 51.18 | 3.71 | 47.31 | 54.89 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 56.44 | 2.46 | 53.99 | 61.30 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 59.88 | 2.74 | 57.14 | 63.38 | null | s/GiB |
| goodput | 4 | upload | 3 | 55.04 | 1.53 | 53.51 | 56.82 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 55.70 | 0.69 | 54.45 | 56.39 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 60.97 | 0.80 | 59.98 | 61.77 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 143.0 | 3.022 | 47.3 | 61.30 | 63.38 | null | null | null | null | null |
| 1 | 2 | upload | 155.1 | 3.031 | 51.2 | 56.44 | 59.88 | null | null | null | null | null |
| 2 | 2 | upload | 165.8 | 3.020 | 54.9 | 53.99 | 57.14 | null | null | null | null | null |
| 0 | 4 | upload | 174.2 | 3.255 | 53.5 | 56.39 | 60.97 | null | null | null | null | null |
| 1 | 4 | upload | 171.9 | 3.123 | 55.0 | 55.70 | 61.77 | null | null | null | null | null |
| 2 | 4 | upload | 181.5 | 3.194 | 56.8 | 54.45 | 59.98 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.560 | 125.3 | 17 | 17 | 8.850 | 116.4 | 17 | 16 |
| 1 | 8.550 | 128.6 | 17 | 17 | 9.070 | 123.1 | 17 | 16 |
| 2 | 8.740 | 133.3 | 19 | 16 | 9.250 | 124.8 | 17 | 15 |
| 0 | 9.590 | 139.2 | 19 | 18 | 10.370 | 129.6 | 17 | 17 |
| 1 | 9.350 | 148.7 | 19 | 20 | 10.370 | 140.5 | 18 | 18 |
| 2 | 9.650 | 149.1 | 19 | 18 | 10.630 | 139.2 | 18 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
