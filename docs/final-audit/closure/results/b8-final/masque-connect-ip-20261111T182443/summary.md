# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T182443`
- kind: e2e
- started: 2026-11-11T18:24:43+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.29 | 0.01 | 0.22 | 0.30 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 606.41 | 9.88 | 596.52 | 707.34 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 436.99 | 25.98 | 411.01 | 558.80 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.50 | 0.12 | 2.38 | 2.86 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1728.13 | 162.10 | 1480.87 | 1890.23 | null | ms |
| latency_p95 | 1 | udp | 3 | 3170.87 | 510.19 | 2094.26 | 3681.06 | null | ms |
| latency_p99 | 1 | udp | 3 | 3314.24 | 473.60 | 2208.82 | 3787.84 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.5 | 6.777 | 0.2 | 606.41 | 411.01 | 0.00 | 2.38 | 1890.23 | 3681.06 | 3787.84 |
| 1 | 1 | udp | 1.4 | 5.000 | 0.3 | 707.34 | 558.80 | 0.01 | 2.86 | 1728.13 | 3170.87 | 3314.24 |
| 2 | 1 | udp | 1.5 | 5.000 | 0.3 | 596.52 | 436.99 | 0.01 | 2.50 | 1480.87 | 2094.26 | 2208.82 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.900 | 68.1 | 16 | 14 | 0.610 | 68.9 | 15 | 14 |
| 1 | 1.000 | 69.5 | 16 | 15 | 0.790 | 70.3 | 17 | 16 |
| 2 | 0.860 | 69.4 | 16 | 15 | 0.630 | 70.5 | 17 | 18 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
