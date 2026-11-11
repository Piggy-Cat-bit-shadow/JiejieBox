# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T192028`
- kind: e2e
- started: 2026-11-11T19:20:28+08:00
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
| goodput | 1 | udp | 3 | 0.31 | 0.02 | 0.26 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 529.21 | 70.52 | 449.67 | 599.72 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 479.78 | 49.43 | 345.40 | 529.21 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.21 | 0.01 | 2.20 | 2.77 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1372.25 | 326.12 | 603.31 | 1698.37 | null | ms |
| latency_p95 | 1 | udp | 3 | 2076.67 | 522.74 | 1263.13 | 2599.41 | null | ms |
| latency_p99 | 1 | udp | 3 | 2123.42 | 558.84 | 1283.96 | 2682.26 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.3 | 5.000 | 0.3 | 599.72 | 479.78 | 0.01 | 2.77 | 1698.37 | 2076.67 | 2123.42 |
| 1 | 1 | udp | 1.6 | 5.000 | 0.3 | 449.67 | 345.40 | 0.01 | 2.20 | 1372.25 | 2599.41 | 2682.26 |
| 2 | 1 | udp | 1.7 | 5.000 | 0.3 | 529.21 | 529.21 | 0.01 | 2.21 | 603.31 | 1263.13 | 1283.96 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.750 | 91.1 | 16 | 14 | 0.600 | 91.5 | 14 | 14 |
| 1 | 0.690 | 90.4 | 16 | 13 | 0.530 | 91.6 | 14 | 15 |
| 2 | 0.870 | 89.9 | 16 | 13 | 0.870 | 92.2 | 15 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
