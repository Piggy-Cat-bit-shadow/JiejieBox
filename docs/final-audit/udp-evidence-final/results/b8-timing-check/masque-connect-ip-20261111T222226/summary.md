# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T222226`
- kind: e2e
- started: 2026-11-11T22:22:26+08:00
- commit: `a64719f78392` (fix(perf): monotonic timing, independent observation completeness, unknown binary provenance)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.28 | 0.00 | 0.28 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 605.27 | 37.39 | 567.89 | 745.05 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 525.06 | 95.50 | 429.55 | 716.12 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.02 | 2.20 | 2.76 | null | ratio |
| latency_p50 | 1 | udp | 3 | 460.85 | 370.36 | 90.49 | 839.28 | null | ms |
| latency_p95 | 1 | udp | 3 | 699.98 | 521.52 | 178.47 | 1350.41 | null | ms |
| latency_p99 | 1 | udp | 3 | 725.09 | 533.19 | 191.91 | 1376.96 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.001 | 0.3 | 605.27 | 525.06 | 0.01 | 2.76 | 839.28 | 1350.41 | 1376.96 |
| 1 | 1 | udp | 1.4 | 5.000 | 0.3 | 567.89 | 429.55 | 0.01 | 2.20 | 460.85 | 699.98 | 725.09 |
| 2 | 1 | udp | 1.4 | 5.000 | 0.3 | 745.05 | 716.12 | 0.01 | 2.22 | 90.49 | 178.47 | 191.91 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.830 | 91.3 | 16 | 13 | 0.720 | 95.0 | 15 | 13 |
| 1 | 0.780 | 91.0 | 16 | 12 | 0.590 | 95.2 | 15 | 14 |
| 2 | 1.030 | 90.7 | 16 | 12 | 0.990 | 95.2 | 15 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
