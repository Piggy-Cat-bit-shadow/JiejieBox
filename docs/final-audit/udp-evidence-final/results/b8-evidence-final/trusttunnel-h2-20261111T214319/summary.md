# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T214319`
- kind: e2e
- started: 2026-11-11T21:43:19+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.06 | 0.00 | 0.06 | 0.06 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.37 | 0.00 | 2.35 | 2.37 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.22 | 0.01 | 81.22 | 81.36 | null | ms |
| latency_p95 | 1 | udp | 3 | 146.60 | 3.88 | 142.72 | 361.25 | null | ms |
| latency_p99 | 1 | udp | 3 | 202.87 | 20.79 | 182.08 | 500.49 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.37 | 81.36 | 146.60 | 182.08 |
| 1 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.37 | 81.22 | 142.72 | 202.87 |
| 2 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.35 | 81.22 | 361.25 | 500.49 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.160 | 84.0 | 13 | 12 | 0.170 | 84.6 | 12 | 13 |
| 1 | 0.140 | 84.6 | 13 | 12 | 0.170 | 85.0 | 12 | 13 |
| 2 | 0.130 | 84.7 | 13 | 12 | 0.150 | 85.4 | 13 | 13 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
