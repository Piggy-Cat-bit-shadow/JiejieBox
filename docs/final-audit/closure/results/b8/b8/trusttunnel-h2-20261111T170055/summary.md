# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T170055`
- kind: e2e
- started: 2026-11-11T17:00:55+08:00
- commit: `227777920b8e` (feat(perf): paced UDP with sequence and drain, and binary provenance)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 8 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.24 | 0.00 | 0.24 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 423.02 | 17.18 | 394.25 | 440.20 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 194.70 | 56.24 | 137.13 | 250.94 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.56 | 0.06 | 2.24 | 2.62 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1617.80 | 390.89 | 1190.63 | 2008.68 | null | ms |
| latency_p95 | 1 | udp | 3 | 3123.47 | 193.24 | 2896.99 | 3316.72 | null | ms |
| latency_p99 | 1 | udp | 3 | 3322.54 | 80.65 | 3007.83 | 3403.20 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.001 | 0.3 | 423.02 | 250.94 | 0.01 | 2.56 | 1190.63 | 2896.99 | 3007.83 |
| 1 | 1 | udp | 1.2 | 5.000 | 0.2 | 394.25 | 137.13 | 0.01 | 2.62 | 2008.68 | 3316.72 | 3403.20 |
| 2 | 1 | udp | 1.2 | 5.001 | 0.2 | 440.20 | 194.70 | 0.00 | 2.24 | 1617.80 | 3123.47 | 3322.54 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.590 | 84.6 | 13 | 13 | 0.350 | 86.1 | 14 | 14 |
| 1 | 0.460 | 85.4 | 13 | 13 | 0.160 | 87.0 | 14 | 14 |
| 2 | 0.520 | 85.8 | 13 | 13 | 0.230 | 87.8 | 14 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
