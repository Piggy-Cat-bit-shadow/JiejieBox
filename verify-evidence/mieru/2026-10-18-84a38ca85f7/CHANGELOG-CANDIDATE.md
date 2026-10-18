# Changelog candidate

Only claims with raw evidence in this directory. No WAN, device or battery claim is included.

## Added

- `type: "mieru"` outbound and inbound, built on the pinned
  `github.com/enfein/mieru/v3 v3.38.1` SDK.
- TCP and UDP (UDP associate / UoT) forwarding, TCP and UDP underlay,
  `default`/`no_wait` handshake, `off`/`low`/`middle`/`high` multiplexing,
  MTU, traffic patterns, multiple users, `user_hint_is_mandatory`, and port
  ranges.
- The fork's listener and dialer options (`bind_interface`, `routing_mark`,
  `reuse_addr`, `netns`, TCP fast-open/keepalive/multipath, UDP fragment) apply
  to Mieru through the shared `common/listener` and `common/dialer` paths.
- A UoT frame codec with a bounded address parse: 0 allocations on read, one
  pooled 64 B allocation on write, and no over-read on a truncated frame.

## Changed

- The startup network observation no longer stops and restarts a Mieru client
  that has never dialed. A network change after the first dial still rebuilds
  the client.
- `InterfaceUpdated` keeps the explicit `Stop()`/rebuild ownership; the SDK
  client is never restarted after `Stop`.

## Measured (loopback, official server SDK, 4 ABBA rounds, Windows 10)

- TCP download: +10.2% (tcp, low mux), +7.0% (tcp, high mux + no_wait), +3.2%
  (udp transport) vs the official 3.38.1 client.
- TCP upload: -6.1% (tcp, low mux), -1.2% (tcp, high mux + no_wait), +16.0%
  (udp transport). The low-mux upload gap is recorded as a regression.
- UDP echo: +5.8% / +7.6% / +47.4% (tcp-low / tcp-high / udp-low); P50 0.61x
  on the UDP underlay.
- TCP P95 latency: 0.98-1.01x. Startup to SOCKS-ready: unchanged within 2 ms.
- Mihomo v1.19.32 (SDK 3.38.0, product comparison): target/Mihomo 1.07x low-mux
  TCP download, 0.95x low-mux TCP upload, 0.85x high-mux TCP upload, 1.27x
  UDP-transport upload, 0.93-1.05x UDP echo.

## Not claimed

- No comparison with the real `mita` binary, mbox, extended or Hiddify on this
  host; those are `BLOCKED_BY_ENVIRONMENT` in `variants.json`.
- No WAN/last-mile, device sleep/wake, battery, iOS/Android or long-soak result.
- No claim that RSS is lower: the target process includes the sing-box core and
  a mixed inbound, so RSS is product context only.
- No claim that the target is uniformly fastest: Mihomo wins the high-mux TCP
  cells and the official client wins the low-mux TCP upload cell.
