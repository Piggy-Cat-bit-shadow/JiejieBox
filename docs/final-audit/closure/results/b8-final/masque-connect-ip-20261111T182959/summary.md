# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T182959`
- kind: e2e
- started: 2026-11-11T18:29:59+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 6 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.34 | 0.01 | 0.30 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 563.54 | 16.70 | 528.11 | 580.24 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 391.73 | 1.65 | 390.08 | 427.54 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.20 | 0.00 | 2.20 | 2.54 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1643.03 | 192.93 | 1450.10 | 2112.62 | null | ms |
| latency_p95 | 1 | udp | 3 | 2529.08 | 480.67 | 2048.41 | 3239.70 | null | ms |
| latency_p99 | 1 | udp | 3 | 2777.05 | 498.05 | 2188.86 | 3275.10 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.7 | 5.000 | 0.3 | 580.24 | 427.54 | 0.01 | 2.54 | 2112.62 | 3239.70 | 3275.10 |
| 1 | 1 | udp | 1.7 | 5.000 | 0.3 | 528.11 | 390.08 | 0.01 | 2.20 | 1450.10 | 2529.08 | 2777.05 |
| 2 | 1 | udp | 1.5 | 5.000 | 0.3 | 563.54 | 391.73 | 0.01 | 2.20 | 1643.03 | 2048.41 | 2188.86 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.950 | 68.0 | 15 | 14 | 0.700 | 70.5 | 14 | 14 |
| 1 | 0.880 | 68.0 | 15 | 13 | 0.650 | 70.6 | 15 | 15 |
| 2 | 0.820 | 68.6 | 16 | 13 | 0.570 | 70.4 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
