# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T214454`
- kind: e2e
- started: 2026-11-11T21:44:54+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.41 | 0.00 | 0.40 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 236.22 | 7.16 | 229.06 | 264.86 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 136.01 | 17.90 | 114.53 | 153.90 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.23 | 0.00 | 2.22 | 2.24 | null | ratio |
| latency_p50 | 1 | udp | 3 | 3215.25 | 50.23 | 3165.02 | 7081.21 | null | ms |
| latency_p95 | 1 | udp | 3 | 7891.71 | 1880.65 | 5168.50 | 9772.36 | null | ms |
| latency_p99 | 1 | udp | 3 | 8149.36 | 1798.37 | 5488.20 | 9947.73 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 7.065 | 0.4 | 264.86 | 136.01 | 0.01 | 2.24 | 3215.25 | 5168.50 | 5488.20 |
| 1 | 1 | udp | 2.9 | 6.995 | 0.4 | 236.22 | 153.90 | 0.01 | 2.22 | 7081.21 | 9772.36 | 9947.73 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 229.06 | 114.53 | 0.01 | 2.23 | 3165.02 | 7891.71 | 8149.36 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.740 | 84.6 | 13 | 12 | 0.380 | 86.1 | 13 | 13 |
| 1 | 0.660 | 85.5 | 13 | 12 | 0.430 | 87.6 | 13 | 13 |
| 2 | 0.640 | 86.1 | 14 | 12 | 0.320 | 88.0 | 14 | 13 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
