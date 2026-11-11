# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T205424`
- kind: e2e
- started: 2026-11-11T20:54:24+08:00
- commit: `9ab0d01eeedd` (fix(perf): initialize scenario extras before recording effective parameters)
- branch: `work/udp-evidence-final-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.28 | 0.00 | 0.20 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 494.68 | 26.05 | 468.63 | 534.09 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 407.38 | 68.18 | 329.50 | 475.56 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.20 | 0.00 | 2.20 | 2.21 | null | ratio |
| latency_p50 | 1 | udp | 3 | 781.79 | 70.46 | 246.09 | 852.25 | null | ms |
| latency_p95 | 1 | udp | 3 | 1810.83 | 832.95 | 977.88 | 2825.56 | null | ms |
| latency_p99 | 1 | udp | 3 | 1843.81 | 848.24 | 995.57 | 2843.94 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 494.68 | 407.38 | 0.01 | 2.20 | 781.79 | 977.88 | 995.57 |
| 1 | 1 | udp | 1.4 | 6.930 | 0.2 | 534.09 | 475.56 | 0.00 | 2.21 | 246.09 | 2825.56 | 2843.94 |
| 2 | 1 | udp | 1.4 | 5.000 | 0.3 | 468.63 | 329.50 | 0.01 | 2.20 | 852.25 | 1810.83 | 1843.81 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.680 | 89.7 | 16 | 13 | 0.560 | 93.6 | 16 | 13 |
| 1 | 0.730 | 89.5 | 16 | 13 | 0.650 | 94.1 | 16 | 15 |
| 2 | 0.640 | 89.6 | 16 | 13 | 0.450 | 94.0 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
