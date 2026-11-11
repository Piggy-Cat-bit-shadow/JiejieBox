# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T190747`
- kind: e2e
- started: 2026-11-11T19:07:47+08:00
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
| goodput | 1 | udp | 3 | 0.28 | 0.00 | 0.28 | 0.38 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 586.62 | 22.12 | 494.74 | 608.75 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 423.26 | 2.13 | 388.34 | 425.39 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.46 | 0.00 | 2.28 | 2.47 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1764.97 | 215.90 | 1340.24 | 1980.87 | null | ms |
| latency_p95 | 1 | udp | 3 | 2127.75 | 65.28 | 2062.47 | 2494.32 | null | ms |
| latency_p99 | 1 | udp | 3 | 2216.04 | 84.73 | 2131.30 | 2624.00 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.9 | 5.000 | 0.4 | 494.74 | 388.34 | 0.01 | 2.46 | 1340.24 | 2062.47 | 2131.30 |
| 1 | 1 | udp | 1.4 | 5.001 | 0.3 | 586.62 | 423.26 | 0.01 | 2.47 | 1980.87 | 2494.32 | 2624.00 |
| 2 | 1 | udp | 1.4 | 5.000 | 0.3 | 608.75 | 425.39 | 0.01 | 2.28 | 1764.97 | 2127.75 | 2216.04 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.930 | 90.7 | 15 | 14 | 0.730 | 94.0 | 15 | 14 |
| 1 | 0.790 | 90.9 | 16 | 14 | 0.570 | 94.4 | 16 | 16 |
| 2 | 0.830 | 91.8 | 16 | 15 | 0.580 | 94.7 | 16 | 18 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
