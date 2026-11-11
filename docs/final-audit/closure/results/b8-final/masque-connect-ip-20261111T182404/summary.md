# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T182404`
- kind: e2e
- started: 2026-11-11T18:24:04+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0.1 delay=40.0ms loss=0.10% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.57 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 767.47 | 120.87 | 620.18 | 888.34 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 814.09 | 110.06 | 684.71 | 924.16 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.21 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.49 | 0.11 | 81.38 | 106.69 | null | ms |
| latency_p95 | 1 | udp | 3 | 105.51 | 23.10 | 82.41 | 191.78 | null | ms |
| latency_p99 | 1 | udp | 3 | 122.97 | 11.58 | 111.39 | 207.61 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 767.47 | 814.09 | 0.01 | 2.22 | 81.38 | 105.51 | 111.39 |
| 1 | 1 | udp | 2.9 | 5.000 | 0.6 | 888.34 | 924.16 | 0.01 | 2.22 | 81.49 | 82.41 | 122.97 |
| 2 | 1 | udp | 2.9 | 5.001 | 0.6 | 620.18 | 684.71 | 0.01 | 2.21 | 106.69 | 191.78 | 207.61 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 2.140 | 68.7 | 16 | 13 | 2.270 | 69.9 | 17 | 13 |
| 1 | 2.480 | 67.6 | 16 | 13 | 2.580 | 69.1 | 17 | 15 |
| 2 | 1.730 | 67.6 | 16 | 13 | 1.910 | 70.0 | 17 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
