# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T184622`
- kind: e2e
- started: 2026-11-11T18:46:22+08:00
- commit: `067cceb562e9` (docs(closure): finalize B7 evidence chain and index)
- branch: `work/closure-final-reconcile-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 50.01 | 1.25 | 48.55 | 51.26 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 58.23 | 0.22 | 56.38 | 58.45 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 61.29 | 0.17 | 61.12 | 62.97 | null | s/GiB |
| goodput | 4 | upload | 3 | 55.30 | 1.30 | 54.00 | 56.64 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 55.92 | 0.05 | 55.86 | 56.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 62.38 | 0.40 | 61.99 | 63.36 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 147.2 | 3.031 | 48.6 | 58.45 | 62.97 | null | null | null | null | null |
| 1 | 2 | upload | 156.4 | 3.051 | 51.3 | 56.38 | 61.29 | null | null | null | null | null |
| 2 | 2 | upload | 152.5 | 3.049 | 50.0 | 58.23 | 61.12 | null | null | null | null | null |
| 0 | 4 | upload | 165.3 | 3.062 | 54.0 | 56.79 | 63.36 | null | null | null | null | null |
| 1 | 4 | upload | 172.1 | 3.113 | 55.3 | 55.92 | 61.99 | null | null | null | null | null |
| 2 | 4 | upload | 176.0 | 3.107 | 56.6 | 55.86 | 62.38 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.400 | 128.9 | 17 | 18 | 9.050 | 126.6 | 16 | 17 |
| 1 | 8.610 | 136.2 | 17 | 18 | 9.360 | 133.8 | 17 | 17 |
| 2 | 8.670 | 140.6 | 19 | 17 | 9.100 | 132.7 | 17 | 16 |
| 0 | 9.170 | 141.0 | 19 | 22 | 10.230 | 133.3 | 17 | 21 |
| 1 | 9.400 | 151.7 | 19 | 22 | 10.420 | 136.7 | 18 | 21 |
| 2 | 9.600 | 152.1 | 20 | 22 | 10.720 | 137.2 | 18 | 21 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
