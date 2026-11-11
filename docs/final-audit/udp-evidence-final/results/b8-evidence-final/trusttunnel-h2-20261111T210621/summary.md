# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T210621`
- kind: e2e
- started: 2026-11-11T21:06:21+08:00
- commit: `9ab0d01eeedd` (fix(perf): initialize scenario extras before recording effective parameters)
- branch: `work/udp-evidence-final-2026-11-11`
- **dirty worktree**: 6 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.57 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 354.62 | 28.92 | 325.70 | 390.28 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 175.45 | 17.97 | 157.48 | 193.43 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.23 | 0.00 | 2.22 | 2.23 | null | ratio |
| latency_p50 | 1 | udp | 3 | 4100.72 | 218.34 | 3444.96 | 4319.05 | null | ms |
| latency_p95 | 1 | udp | 3 | 7778.54 | 836.28 | 6746.54 | 8614.82 | null | ms |
| latency_p99 | 1 | udp | 3 | 8067.80 | 829.99 | 6977.63 | 8897.79 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 325.70 | 157.48 | 0.01 | 2.22 | 3444.96 | 6746.54 | 6977.63 |
| 1 | 1 | udp | 2.9 | 5.001 | 0.6 | 390.28 | 175.45 | 0.01 | 2.23 | 4319.05 | 8614.82 | 8897.79 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 354.62 | 193.43 | 0.01 | 2.23 | 4100.72 | 7778.54 | 8067.80 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.910 | 85.2 | 12 | 13 | 0.440 | 86.6 | 13 | 14 |
| 1 | 1.090 | 85.9 | 12 | 13 | 0.490 | 87.9 | 14 | 14 |
| 2 | 0.990 | 86.4 | 12 | 13 | 0.540 | 88.4 | 14 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
