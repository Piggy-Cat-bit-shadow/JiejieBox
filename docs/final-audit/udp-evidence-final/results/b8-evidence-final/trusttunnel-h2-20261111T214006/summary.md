# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T214006`
- kind: e2e
- started: 2026-11-11T21:40:06+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.19 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 336.44 | 0.00 | 336.44 | 350.76 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 143.17 | 0.00 | 143.17 | 143.17 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.23 | 0.00 | 2.22 | 2.23 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1776.63 | 68.70 | 1539.43 | 1845.33 | null | ms |
| latency_p95 | 1 | udp | 3 | 3073.54 | 1012.04 | 2053.45 | 4085.57 | null | ms |
| latency_p99 | 1 | udp | 3 | 3137.78 | 1042.55 | 2095.07 | 4180.33 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 7.473 | 0.2 | 350.76 | 143.17 | 0.00 | 2.23 | 1776.63 | 4085.57 | 4180.33 |
| 1 | 1 | udp | 1.4 | 5.001 | 0.3 | 336.44 | 143.17 | 0.01 | 2.22 | 1539.43 | 2053.45 | 2095.07 |
| 2 | 1 | udp | 1.4 | 5.001 | 0.3 | 336.44 | 143.17 | 0.01 | 2.23 | 1845.33 | 3073.54 | 3137.78 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.490 | 84.7 | 14 | 12 | 0.200 | 86.0 | 13 | 13 |
| 1 | 0.470 | 84.9 | 14 | 12 | 0.200 | 86.2 | 13 | 13 |
| 2 | 0.470 | 84.3 | 14 | 12 | 0.200 | 85.1 | 13 | 13 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
