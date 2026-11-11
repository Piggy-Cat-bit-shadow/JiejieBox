# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T191704`
- kind: e2e
- started: 2026-11-11T19:17:04+08:00
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
| goodput | 1 | udp | 3 | 0.30 | 0.01 | 0.28 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 529.58 | 96.19 | 427.81 | 625.77 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 392.03 | 82.70 | 301.27 | 474.73 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.27 | 0.07 | 2.20 | 2.67 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1435.28 | 508.16 | 927.12 | 2471.92 | null | ms |
| latency_p95 | 1 | udp | 3 | 2694.81 | 132.00 | 1917.60 | 2826.81 | null | ms |
| latency_p99 | 1 | udp | 3 | 2770.96 | 188.43 | 2051.08 | 2959.39 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 625.77 | 474.73 | 0.01 | 2.67 | 1435.28 | 1917.60 | 2051.08 |
| 1 | 1 | udp | 1.5 | 5.001 | 0.3 | 529.58 | 392.03 | 0.01 | 2.27 | 2471.92 | 2694.81 | 2770.96 |
| 2 | 1 | udp | 1.7 | 5.000 | 0.3 | 427.81 | 301.27 | 0.01 | 2.20 | 927.12 | 2826.81 | 2959.39 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.870 | 91.2 | 16 | 14 | 0.660 | 91.4 | 16 | 14 |
| 1 | 0.770 | 90.9 | 16 | 13 | 0.570 | 92.2 | 16 | 15 |
| 2 | 0.710 | 90.3 | 16 | 13 | 0.500 | 91.9 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
