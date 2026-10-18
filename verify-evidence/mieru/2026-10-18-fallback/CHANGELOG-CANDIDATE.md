# Changelog candidate

## Added

- Mieru outbound optional `fallback`: one tag can declare a primary TCP or UDP
  transport and the opposite transport as backup, with optional different
  server/port. Disabled by default; legacy `transport: "tcp"`, `"udp"`, `"both"`
  and `handshake_mode: "no_wait"` behavior is unchanged when disabled.
- `fallback` rejects `both` and `no_wait`, inherits server/port when
  unambiguous, and rejects a `server_ports`-only primary with an omitted backup
  port rather than guessing.
- Two independent official Mieru SDK generations with separate client, config,
  dialer, DNS bridge and UDP socket registry. A small selector changes only new
  sessions, never stops an active client, and uses one business-driven recovery
  probe with capped backoff.
- Tests for config/JSON, pure selector states, real loopback TCP↔UDP failure
  modes, UDP associate both directions, target refusal, active-session
  preservation, concurrent failover, single-flight recovery, Close,
  InterfaceUpdated and socket ownership.
- Schema, English docs and a Chinese summary; real mita 3.38.1 interop evidence.

## Fixed

- The SDK returns a live connection together with a post-dial handshake error;
  the old single-client path dropped it and leaked a session. It is now closed.
- A wrong-credential/two-silent-handshake path no longer leaves the outbound
  permanently preferring the backup; it fails closed and returns to primary.
- A silent recovery probe now backs off instead of allowing a probe on every
  request.
- A successful connection from a closed/changed generation is rejected and
  closed instead of being handed to the route.
- Linux reports an unreachable UDP peer as a bare EOF; a UDP-primary EOF is now
  classified as transport (TCP-primary EOF stays ambiguous), so the UDP
  blackhole fallback works on Linux. This was caught by the Linux race CI.
- Secondary Start failure rolls back the already-started primary and clears the
  runtimes for a fresh next Start.

## Performance note

No claim of universal speedup. Disabled old/new was 1.00-1.07x on TCP download,
1.00-1.02x on TCP upload, 0.92-0.95x on UDP echo (recorded as variance; no
per-datagram change). Enabled with a healthy primary vs disabled was 1.024x TCP
download / 1.021x upload / 1.053x UDP echo / identical first-byte. Idle overhead
+0.25 MiB, no extra thread/socket; selector 26.6 ns/op, 0 allocs. Silent primary
failover costs the SDK's 10 s handshake wait; connection-refused failover is
1.5 ms.

## Not included

Active-session migration, per-side credentials/Mux/MTU/pattern, background
probes, `no_wait` fallback, `both` fallback, and an SDK auth-vs-blackhole typed
error. Mobile/TUN/real network switching and multi-hour soak are not claimed.
