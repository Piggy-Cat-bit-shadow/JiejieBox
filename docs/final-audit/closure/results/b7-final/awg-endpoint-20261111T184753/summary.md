# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T184753`
- kind: e2e
- started: 2026-11-11T18:47:53+08:00
- commit: `067cceb562e9` (docs(closure): finalize B7 evidence chain and index)
- branch: `work/closure-final-reconcile-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 53.34 | 0.05 | 53.29 | 57.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 54.45 | 0.18 | 52.60 | 54.63 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 58.63 | 0.53 | 56.68 | 59.16 | null | s/GiB |
| goodput | 4 | upload | 3 | 57.73 | 1.60 | 56.14 | 59.81 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 53.06 | 0.70 | 51.17 | 53.75 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 58.94 | 0.99 | 57.29 | 59.93 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 173.3 | 3.024 | 57.3 | 52.60 | 56.68 | null | null | null | null | null |
| 1 | 2 | upload | 160.6 | 3.014 | 53.3 | 54.63 | 59.16 | null | null | null | null | null |
| 2 | 2 | upload | 161.6 | 3.029 | 53.3 | 54.45 | 58.63 | null | null | null | null | null |
| 0 | 4 | upload | 179.3 | 3.106 | 57.7 | 53.06 | 58.94 | null | null | null | null | null |
| 1 | 4 | upload | 174.1 | 3.102 | 56.1 | 53.75 | 59.93 | null | null | null | null | null |
| 2 | 4 | upload | 182.3 | 3.048 | 59.8 | 51.17 | 57.29 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.900 | 126.5 | 18 | 18 | 9.590 | 116.1 | 17 | 17 |
| 1 | 8.570 | 130.1 | 19 | 18 | 9.280 | 124.0 | 17 | 17 |
| 2 | 8.590 | 131.1 | 19 | 18 | 9.250 | 126.5 | 17 | 17 |
| 0 | 9.290 | 138.3 | 19 | 22 | 10.320 | 131.3 | 17 | 21 |
| 1 | 9.140 | 145.2 | 19 | 21 | 10.190 | 131.2 | 17 | 20 |
| 2 | 9.110 | 146.8 | 19 | 22 | 10.200 | 132.2 | 18 | 21 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
