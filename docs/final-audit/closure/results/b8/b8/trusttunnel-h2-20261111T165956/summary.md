# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T165956`
- kind: e2e
- started: 2026-11-11T16:59:56+08:00
- commit: `227777920b8e` (feat(perf): paced UDP with sequence and drain, and binary provenance)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 8 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.57 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 483.18 | 0.00 | 483.18 | 493.92 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 540.45 | 3.58 | 536.87 | 561.92 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.34 | 0.00 | 2.34 | 2.34 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.02 | 0.00 | 81.01 | 81.04 | null | ms |
| latency_p95 | 1 | udp | 3 | 178.60 | 5.11 | 157.76 | 183.71 | null | ms |
| latency_p99 | 1 | udp | 3 | 251.54 | 6.53 | 242.36 | 258.07 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.000 | 0.6 | 493.92 | 561.92 | 0.01 | 2.34 | 81.04 | 157.76 | 242.36 |
| 1 | 1 | udp | 2.9 | 5.000 | 0.6 | 483.18 | 540.45 | 0.01 | 2.34 | 81.02 | 183.71 | 251.54 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 483.18 | 536.87 | 0.01 | 2.34 | 81.01 | 178.60 | 258.07 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.380 | 85.3 | 13 | 13 | 1.570 | 87.4 | 14 | 14 |
| 1 | 1.350 | 86.3 | 13 | 13 | 1.510 | 88.2 | 14 | 14 |
| 2 | 1.350 | 86.8 | 14 | 13 | 1.500 | 88.4 | 14 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
