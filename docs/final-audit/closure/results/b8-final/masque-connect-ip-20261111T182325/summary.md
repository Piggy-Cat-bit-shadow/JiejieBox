# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T182325`
- kind: e2e
- started: 2026-11-11T18:23:25+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.42 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 816.04 | 17.90 | 798.15 | 856.44 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 855.41 | 17.90 | 837.52 | 895.86 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.22 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.41 | 0.00 | 81.38 | 81.41 | null | ms |
| latency_p95 | 1 | udp | 3 | 82.08 | 0.17 | 81.91 | 82.25 | null | ms |
| latency_p99 | 1 | udp | 3 | 86.52 | 4.20 | 82.32 | 1862.12 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 816.04 | 855.41 | 0.01 | 2.22 | 81.38 | 81.91 | 82.32 |
| 1 | 1 | udp | 2.9 | 6.781 | 0.4 | 798.15 | 837.52 | 0.01 | 2.22 | 81.41 | 82.08 | 1862.12 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 856.44 | 895.86 | 0.01 | 2.22 | 81.41 | 82.25 | 86.52 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 2.280 | 68.2 | 17 | 13 | 2.390 | 69.5 | 17 | 13 |
| 1 | 2.230 | 67.2 | 17 | 13 | 2.340 | 69.1 | 17 | 15 |
| 2 | 2.390 | 67.2 | 17 | 13 | 2.500 | 69.8 | 17 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
