# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T214143`
- kind: e2e
- started: 2026-11-11T21:41:43+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.50 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 193.27 | 29.92 | 161.45 | 223.19 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 161.06 | 28.31 | 132.75 | 206.96 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.23 | 0.00 | 2.22 | 2.39 | null | ratio |
| latency_p50 | 1 | udp | 3 | 6786.18 | 505.72 | 6280.46 | 7639.74 | null | ms |
| latency_p95 | 1 | udp | 3 | 8690.53 | 202.74 | 8398.44 | 8893.27 | null | ms |
| latency_p99 | 1 | udp | 3 | 8863.16 | 269.48 | 8593.69 | 9330.16 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 161.45 | 132.75 | 0.01 | 2.22 | 6280.46 | 8893.27 | 9330.16 |
| 1 | 1 | udp | 2.5 | 5.000 | 0.5 | 223.19 | 206.96 | 0.01 | 2.39 | 7639.74 | 8690.53 | 8863.16 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 193.27 | 161.06 | 0.01 | 2.23 | 6786.18 | 8398.44 | 8593.69 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.450 | 85.6 | 12 | 12 | 0.370 | 85.7 | 12 | 13 |
| 1 | 0.550 | 86.1 | 13 | 12 | 0.510 | 86.9 | 12 | 13 |
| 2 | 0.540 | 87.1 | 13 | 12 | 0.450 | 87.7 | 13 | 13 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
