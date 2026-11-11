# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T205559`
- kind: e2e
- started: 2026-11-11T20:55:59+08:00
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
| goodput | 1 | udp | 3 | 0.31 | 0.07 | 0.18 | 0.38 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 543.31 | 24.99 | 518.32 | 622.46 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 387.38 | 31.88 | 355.50 | 420.16 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.33 | 0.00 | 2.20 | 2.33 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1662.74 | 110.71 | 1552.03 | 2111.25 | null | ms |
| latency_p95 | 1 | udp | 3 | 2890.96 | 665.71 | 2225.24 | 4609.97 | null | ms |
| latency_p99 | 1 | udp | 3 | 2947.63 | 570.39 | 2377.24 | 4716.89 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.3 | 7.161 | 0.2 | 622.46 | 420.16 | 0.00 | 2.33 | 2111.25 | 4609.97 | 4716.89 |
| 1 | 1 | udp | 1.5 | 5.001 | 0.3 | 543.31 | 355.50 | 0.01 | 2.20 | 1662.74 | 2225.24 | 2377.24 |
| 2 | 1 | udp | 1.9 | 5.001 | 0.4 | 518.32 | 387.38 | 0.01 | 2.33 | 1552.03 | 2890.96 | 2947.63 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.800 | 90.9 | 16 | 14 | 0.540 | 94.1 | 16 | 14 |
| 1 | 0.810 | 90.2 | 16 | 13 | 0.530 | 94.0 | 16 | 15 |
| 2 | 0.950 | 90.3 | 16 | 13 | 0.710 | 94.8 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
