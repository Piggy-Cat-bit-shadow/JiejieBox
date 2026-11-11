# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T205202`
- kind: e2e
- started: 2026-11-11T20:52:02+08:00
- commit: `9ab0d01eeedd` (fix(perf): initialize scenario extras before recording effective parameters)
- branch: `work/udp-evidence-final-2026-11-11`
- **dirty worktree**: 2 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.29 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 780.25 | 35.79 | 744.46 | 837.52 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 773.09 | 35.79 | 737.30 | 830.36 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.22 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.28 | 0.04 | 81.24 | 81.38 | null | ms |
| latency_p95 | 1 | udp | 3 | 81.77 | 0.04 | 81.73 | 81.83 | null | ms |
| latency_p99 | 1 | udp | 3 | 82.00 | 0.07 | 81.92 | 82.07 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 837.52 | 830.36 | 0.01 | 2.22 | 81.38 | 81.83 | 82.07 |
| 1 | 1 | udp | 1.4 | 5.001 | 0.3 | 780.25 | 773.09 | 0.01 | 2.22 | 81.28 | 81.77 | 82.00 |
| 2 | 1 | udp | 1.4 | 5.001 | 0.3 | 744.46 | 737.30 | 0.01 | 2.22 | 81.24 | 81.73 | 81.92 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.170 | 88.2 | 15 | 13 | 1.160 | 92.8 | 16 | 13 |
| 1 | 1.090 | 88.5 | 15 | 13 | 1.080 | 92.8 | 16 | 15 |
| 2 | 1.040 | 87.9 | 16 | 13 | 1.030 | 92.8 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
