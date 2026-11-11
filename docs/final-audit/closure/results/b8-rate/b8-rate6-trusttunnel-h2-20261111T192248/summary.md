# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T192248`
- kind: e2e
- started: 2026-11-11T19:22:48+08:00
- commit: `c26a4ddce4bf` (docs(closure): state the archive and sidecar manifest contract)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 1 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.38 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 214.75 | 3.58 | 211.17 | 239.80 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 157.48 | 3.58 | 121.69 | 161.06 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.22 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 4658.85 | 497.75 | 4161.10 | 7430.68 | null | ms |
| latency_p95 | 1 | udp | 3 | 7510.63 | 1143.03 | 6367.59 | 9684.05 | null | ms |
| latency_p99 | 1 | udp | 3 | 7684.89 | 845.61 | 6839.28 | 9814.90 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 7.515 | 0.4 | 239.80 | 157.48 | 0.01 | 2.22 | 7430.68 | 9684.05 | 9814.90 |
| 1 | 1 | udp | 2.9 | 5.001 | 0.6 | 211.17 | 161.06 | 0.01 | 2.22 | 4658.85 | 7510.63 | 7684.89 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 214.75 | 121.69 | 0.01 | 2.22 | 4161.10 | 6367.59 | 6839.28 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.670 | 84.9 | 12 | 13 | 0.440 | 86.2 | 12 | 14 |
| 1 | 0.590 | 86.1 | 12 | 13 | 0.450 | 87.6 | 13 | 14 |
| 2 | 0.600 | 87.0 | 13 | 13 | 0.340 | 88.2 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
