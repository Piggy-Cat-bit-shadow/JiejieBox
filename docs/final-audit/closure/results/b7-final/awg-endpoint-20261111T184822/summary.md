# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T184822`
- kind: e2e
- started: 2026-11-11T18:48:22+08:00
- commit: `067cceb562e9` (docs(closure): finalize B7 evidence chain and index)
- branch: `work/closure-final-reconcile-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 55.92 | 0.11 | 54.67 | 56.02 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 52.93 | 0.11 | 52.82 | 53.69 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 56.11 | 0.29 | 55.70 | 56.40 | null | s/GiB |
| goodput | 4 | upload | 3 | 58.52 | 0.58 | 57.73 | 59.10 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 52.69 | 0.33 | 51.89 | 53.01 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 58.10 | 0.36 | 57.61 | 58.46 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 171.4 | 3.065 | 55.9 | 52.93 | 56.40 | null | null | null | null | null |
| 1 | 2 | upload | 168.0 | 3.074 | 54.7 | 53.69 | 55.70 | null | null | null | null | null |
| 2 | 2 | upload | 171.2 | 3.055 | 56.0 | 52.82 | 56.11 | null | null | null | null | null |
| 0 | 4 | upload | 180.8 | 3.059 | 59.1 | 51.89 | 57.61 | null | null | null | null | null |
| 1 | 4 | upload | 179.0 | 3.059 | 58.5 | 52.69 | 58.46 | null | null | null | null | null |
| 2 | 4 | upload | 179.1 | 3.102 | 57.7 | 53.01 | 58.10 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.860 | 124.1 | 17 | 17 | 9.440 | 116.5 | 17 | 16 |
| 1 | 8.810 | 129.3 | 19 | 17 | 9.140 | 130.8 | 17 | 16 |
| 2 | 8.830 | 135.0 | 20 | 16 | 9.380 | 131.1 | 18 | 15 |
| 0 | 9.160 | 138.4 | 20 | 19 | 10.170 | 131.1 | 18 | 18 |
| 1 | 9.210 | 147.4 | 20 | 19 | 10.220 | 168.1 | 18 | 18 |
| 2 | 9.270 | 147.5 | 20 | 18 | 10.160 | 167.0 | 18 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
