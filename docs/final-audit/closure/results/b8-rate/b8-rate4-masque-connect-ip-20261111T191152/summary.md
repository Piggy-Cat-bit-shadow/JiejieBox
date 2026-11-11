# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T191152`
- kind: e2e
- started: 2026-11-11T19:11:52+08:00
- commit: `c26a4ddce4bf` (docs(closure): state the archive and sidecar manifest contract)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 1 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.28 | 0.00 | 0.21 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 539.64 | 6.40 | 486.86 | 546.04 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 444.12 | 37.19 | 376.21 | 481.30 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.21 | 0.00 | 2.20 | 2.54 | null | ratio |
| latency_p50 | 1 | udp | 3 | 338.78 | 9.90 | 328.88 | 850.35 | null | ms |
| latency_p95 | 1 | udp | 3 | 1072.17 | 1.71 | 1070.46 | 1377.50 | null | ms |
| latency_p99 | 1 | udp | 3 | 1400.69 | 314.76 | 1085.93 | 1857.95 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 486.86 | 376.21 | 0.01 | 2.54 | 850.35 | 1377.50 | 1400.69 |
| 1 | 1 | udp | 1.4 | 5.001 | 0.3 | 546.04 | 444.12 | 0.01 | 2.20 | 338.78 | 1070.46 | 1085.93 |
| 2 | 1 | udp | 1.4 | 6.722 | 0.2 | 539.64 | 481.30 | 0.00 | 2.21 | 328.88 | 1072.17 | 1857.95 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.660 | 90.4 | 16 | 14 | 0.510 | 94.3 | 16 | 14 |
| 1 | 0.750 | 89.8 | 16 | 13 | 0.610 | 94.3 | 16 | 15 |
| 2 | 0.740 | 89.7 | 16 | 13 | 0.660 | 93.9 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
