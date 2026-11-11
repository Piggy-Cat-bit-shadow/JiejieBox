# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T182523`
- kind: e2e
- started: 2026-11-11T18:25:23+08:00
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
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.57 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 468.87 | 17.90 | 440.23 | 486.76 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 511.82 | 25.05 | 486.76 | 536.87 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.34 | 0.00 | 2.33 | 2.34 | null | ratio |
| latency_p50 | 1 | udp | 3 | 80.98 | 0.06 | 80.93 | 81.05 | null | ms |
| latency_p95 | 1 | udp | 3 | 176.05 | 3.58 | 172.48 | 184.92 | null | ms |
| latency_p99 | 1 | udp | 3 | 253.85 | 4.15 | 242.73 | 258.00 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 468.87 | 511.82 | 0.01 | 2.34 | 80.98 | 172.48 | 242.73 |
| 1 | 1 | udp | 2.9 | 5.000 | 0.6 | 486.76 | 536.87 | 0.01 | 2.33 | 81.05 | 176.05 | 253.85 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 440.23 | 486.76 | 0.01 | 2.34 | 80.93 | 184.92 | 258.00 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.310 | 60.2 | 13 | 13 | 1.430 | 64.7 | 14 | 14 |
| 1 | 1.360 | 61.3 | 13 | 13 | 1.500 | 65.2 | 14 | 14 |
| 2 | 1.230 | 62.4 | 14 | 13 | 1.360 | 65.7 | 15 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
