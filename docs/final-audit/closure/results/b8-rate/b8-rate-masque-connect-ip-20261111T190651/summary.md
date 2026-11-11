# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T190651`
- kind: e2e
- started: 2026-11-11T19:06:51+08:00
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
| goodput | 1 | udp | 3 | 0.27 | 0.01 | 0.26 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 489.79 | 20.39 | 468.16 | 510.18 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 400.30 | 52.94 | 339.79 | 453.24 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.02 | 2.21 | 2.35 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1176.37 | 5.12 | 1162.86 | 1181.49 | null | ms |
| latency_p95 | 1 | udp | 3 | 2172.50 | 759.02 | 1413.48 | 3599.37 | null | ms |
| latency_p99 | 1 | udp | 3 | 2294.06 | 727.54 | 1566.52 | 3628.72 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.3 | 5.000 | 0.3 | 510.18 | 400.30 | 0.01 | 2.35 | 1162.86 | 2172.50 | 2294.06 |
| 1 | 1 | udp | 1.4 | 5.001 | 0.3 | 468.16 | 339.79 | 0.01 | 2.22 | 1181.49 | 1413.48 | 1566.52 |
| 2 | 1 | udp | 1.4 | 5.001 | 0.3 | 489.79 | 453.24 | 0.01 | 2.21 | 1176.37 | 3599.37 | 3628.72 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.650 | 91.1 | 15 | 14 | 0.510 | 94.7 | 16 | 14 |
| 1 | 0.620 | 90.7 | 15 | 15 | 0.450 | 95.5 | 16 | 16 |
| 2 | 0.670 | 90.3 | 16 | 14 | 0.620 | 95.5 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
