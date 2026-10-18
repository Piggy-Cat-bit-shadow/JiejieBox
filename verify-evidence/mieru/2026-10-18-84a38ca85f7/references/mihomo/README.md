# REF-E: Mihomo v1.19.32

This is a **product** comparison, not a unified-SDK one. Mihomo ships
`github.com/enfein/mieru/v3 v3.38.0`; the official client and the target use
v3.38.1. Do not add these ratios to the unified-SDK matrix.

| Item | Value |
| --- | --- |
| Binary | `mihomo-windows-amd64-compatible-v1.19.32.exe` |
| Version | Mihomo Meta v1.19.32, windows/amd64, go1.26.8 |
| SHA-256 | `04F8D7FC2B314771E1ECBF15951D59F0E1CB7914503C88CFB6972FF6A824E314` |
| Mieru SDK | v3.38.0 (`go version -m`) |
| Entry | `mixed-port: 11093`, SOCKS5, `udp: true` |
| Upstream | `adapter/outbound/mieru.go` at v1.19.32; UDP uses `UDPAssociateWrapper(NewPacketOverStreamTunnel)` |
| Server | the same official-SDK harness on 127.0.0.1:18671 |
| Rounds | 4 per scenario |

Mihomo starts its Mieru client lazily on the first dial. Each round is a fresh
Mihomo process; a 1 MiB warmup dial starts the client before the measured cells.
`startup_ms` is therefore app-ready, not Mieru-client-ready.

## Results

Target/Mihomo throughput ratios (from `comparison.csv`):

| Scenario | TCP download | TCP upload | UDP echo |
| --- | ---: | ---: | ---: |
| tcp-low-default | 1.066 | 0.953 | 1.018 |
| udp-low-default | 1.026 | 1.274 | 1.049 |
| tcp-high-nowait | 0.945 | 0.851 | 0.933 |

- Mihomo is faster on the high-mux TCP cells (up to 17% on upload).
- The target is faster on low-mux TCP download (6.6%) and UDP-transport upload
  (27.4%).
- The target's TCP-upload weakness is confirmed against two independent
  references: official 435 vs target 408 MiB/s, Mihomo 428 vs target 408 MiB/s
  on `tcp-low-default`.
- UDP echo: target is 1.8% ahead on the TCP underlay and 4.9% ahead on the UDP
  underlay; both are behind the target's unified-SDK comparison because Mihomo
  uses the older SDK and its own packet wrapper.

## Files

| File | Contents |
| --- | --- |
| `summary.csv` | Mihomo medians/min/max per scenario x mode, 4 rounds |
| `comparison.csv` | official / target / Mihomo side by side, plus target-vs-Mihomo ratios |
| `raw/` | one JSON per cell with CPU/RSS/startup, plus Mihomo/client/server logs |
| `mihomo-*.yaml` | the exact YAML used for each scenario |
