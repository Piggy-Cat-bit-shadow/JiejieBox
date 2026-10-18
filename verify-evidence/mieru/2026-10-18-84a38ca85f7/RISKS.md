# Risks for the next debug round

1. **No real mita / mbox / extended / Hiddify cell.** The server is the official
   SDK entry point, not the mita binary; the other three references have no
   scriptable Windows build on this host. A Linux host is required before any
   "faster than mita" claim.
2. **TCP upload, low mux, TCP underlay is 6% slower than the official client**
   and 4.7% slower than Mihomo. The CPU profile points at the shared
   `copyExtendedWithPool` path, not the Mieru adapter. Do not "fix" it by adding
   a Mieru-specific buffer until the copy ownership is understood.
3. **High-mux TCP is not the target's win.** Mihomo is 5.5% faster on download
   and 17% faster on upload at `high + no_wait` (SDK 3.38.0). Re-run on Linux
   with the same SDK before attributing the gap to the adapter.
4. **Loopback only.** No Wi-Fi/cellular hand-off, RTT, loss, MTU/PMTU or
   reordering. The `InterfaceUpdated` A/B used the startup observation, not a
   real path change.
5. **Product-boundary RSS/CPU.** The target process includes the sing-box core
   and a mixed inbound; the official client is a dedicated daemon. Do not turn
   the RSS column into an adapter-memory claim.
6. **Server harness is minimal.** It uses `io.Copy` for TCP and the SDK's
   `PacketOverStreamTunnel` for UDP; both clients share it, so it cancels in the
   client comparison, but it is not a mita data-path benchmark.
7. **No long soak / fd / goroutine leak run.** The lifecycle tests cover
   Start/Stop and the race suite; a multi-hour idle/reconnect run is still
   missing.
8. **Traffic pattern uses one seed only** (`20261018`). Pattern generation,
   low-entropy mode and rotation under a long flow were not stress-tested.
9. **Mihomo is SDK v3.38.0.** Its ratios are product-level; do not merge them
   into the v3.38.1 matrix.
10. **The bench harness itself is fixed-size and local.** 64 MiB per connection,
    5000 datagrams, 4 rounds; a larger/soak matrix may expose different
    bottlenecks.
