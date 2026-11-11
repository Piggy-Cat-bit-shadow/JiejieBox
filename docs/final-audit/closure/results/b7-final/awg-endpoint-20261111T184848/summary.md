# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T184848`
- kind: e2e
- started: 2026-11-11T18:48:48+08:00
- commit: `067cceb562e9` (docs(closure): finalize B7 evidence chain and index)
- branch: `work/closure-final-reconcile-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 55.17 | 1.67 | 51.63 | 56.83 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 52.32 | 0.18 | 52.14 | 54.06 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 55.35 | 0.09 | 55.26 | 56.56 | null | s/GiB |
| goodput | 4 | upload | 3 | 56.53 | 0.63 | 55.27 | 57.16 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 52.16 | 0.72 | 51.44 | 53.07 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 55.51 | 0.50 | 54.27 | 56.01 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 155.7 | 3.016 | 51.6 | 54.06 | 56.56 | null | null | null | null | null |
| 1 | 2 | upload | 172.2 | 3.031 | 56.8 | 52.14 | 55.35 | null | null | null | null | null |
| 2 | 2 | upload | 167.3 | 3.033 | 55.2 | 52.32 | 55.26 | null | null | null | null | null |
| 0 | 4 | upload | 171.2 | 3.028 | 56.5 | 52.16 | 55.51 | null | null | null | null | null |
| 1 | 4 | upload | 167.5 | 3.030 | 55.3 | 53.07 | 56.01 | null | null | null | null | null |
| 2 | 4 | upload | 173.6 | 3.037 | 57.2 | 51.44 | 54.27 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.220 | 122.3 | 17 | 16 | 8.600 | 114.5 | 16 | 15 |
| 1 | 8.770 | 130.6 | 18 | 16 | 9.310 | 121.9 | 16 | 15 |
| 2 | 8.550 | 133.0 | 19 | 16 | 9.030 | 123.6 | 16 | 15 |
| 0 | 8.720 | 131.7 | 19 | 17 | 9.280 | 122.3 | 18 | 16 |
| 1 | 8.680 | 132.4 | 20 | 18 | 9.160 | 122.8 | 18 | 16 |
| 2 | 8.720 | 134.9 | 20 | 17 | 9.200 | 124.5 | 18 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
