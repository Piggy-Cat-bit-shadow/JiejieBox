# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T210225`
- kind: e2e
- started: 2026-11-11T21:02:25+08:00
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
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.37 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 240.96 | 11.89 | 229.06 | 266.63 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 187.01 | 40.27 | 146.74 | 227.42 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.23 | 0.01 | 2.22 | 2.26 | null | ratio |
| latency_p50 | 1 | udp | 3 | 4201.00 | 769.33 | 3431.67 | 5448.17 | null | ms |
| latency_p95 | 1 | udp | 3 | 8163.28 | 663.11 | 7442.79 | 8826.40 | null | ms |
| latency_p99 | 1 | udp | 3 | 8393.27 | 520.17 | 7873.10 | 9082.39 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.6 | 6.969 | 0.4 | 266.63 | 227.42 | 0.01 | 2.26 | 5448.17 | 8163.28 | 8393.27 |
| 1 | 1 | udp | 2.9 | 5.000 | 0.6 | 229.06 | 146.74 | 0.01 | 2.22 | 3431.67 | 8826.40 | 9082.39 |
| 2 | 1 | udp | 2.8 | 5.000 | 0.6 | 240.96 | 187.01 | 0.01 | 2.23 | 4201.00 | 7442.79 | 7873.10 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.680 | 84.7 | 14 | 13 | 0.580 | 85.8 | 12 | 14 |
| 1 | 0.640 | 85.5 | 14 | 13 | 0.410 | 87.2 | 13 | 14 |
| 2 | 0.670 | 86.0 | 15 | 13 | 0.520 | 87.5 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
