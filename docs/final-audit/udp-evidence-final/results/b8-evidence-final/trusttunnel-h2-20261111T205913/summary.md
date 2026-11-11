# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T205913`
- kind: e2e
- started: 2026-11-11T20:59:13+08:00
- commit: `9ab0d01eeedd` (fix(perf): initialize scenario extras before recording effective parameters)
- branch: `work/udp-evidence-final-2026-11-11`
- **dirty worktree**: 6 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.20 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 522.55 | 7.16 | 515.40 | 558.35 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 594.14 | 14.32 | 579.82 | 629.93 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.37 | 0.00 | 2.37 | 2.37 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.10 | 0.02 | 81.08 | 81.20 | null | ms |
| latency_p95 | 1 | udp | 3 | 126.88 | 7.68 | 119.20 | 150.33 | null | ms |
| latency_p99 | 1 | udp | 3 | 210.21 | 8.24 | 201.97 | 2070.66 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.001 | 0.3 | 515.40 | 579.82 | 0.01 | 2.37 | 81.10 | 126.88 | 210.21 |
| 1 | 1 | udp | 1.4 | 5.000 | 0.3 | 522.55 | 594.14 | 0.01 | 2.37 | 81.08 | 119.20 | 201.97 |
| 2 | 1 | udp | 1.4 | 6.990 | 0.2 | 558.35 | 629.93 | 0.00 | 2.37 | 81.20 | 150.33 | 2070.66 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.720 | 84.2 | 12 | 13 | 0.810 | 85.9 | 13 | 14 |
| 1 | 0.730 | 84.6 | 12 | 13 | 0.830 | 86.9 | 13 | 14 |
| 2 | 0.780 | 85.1 | 12 | 13 | 0.880 | 88.0 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
