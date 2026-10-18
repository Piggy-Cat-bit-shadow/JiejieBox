# Lifecycle and ownership

## Per-generation ownership

| Resource | Owner | Created | Released |
| --- | --- | --- | --- |
| `option.MieruOutboundOptions` / resolved secondary | `Outbound` | `NewOutbound` | immutable |
| raw fork `N.Dialer` per side | `transportSpec` | `newTransportSpec` | GC; no sockets |
| `resolverBridge` per side | `transportSpec` | `newTransportSpec` | GC; DNS router is borrowed |
| `serverDialer` per side | `transportRuntime` | `newTransportRuntime` | `transportRuntime.stop` |
| UDP `PacketConn` registry (`serverDialer.packetConns`) | one `serverDialer` | `ListenPacket` | `shutdownPacketConns` on stop |
| official `Client` / `ClientConfig` | one `transportRuntime` | `newTransportRuntime` | `client.Stop()` in `transportRuntime.stop` |
| `fallbackSelector` | `Outbound` | `NewOutbound` only when enabled | `close()` under `Outbound.access` |
| in-flight dial counter | `Outbound` | `NewOutbound` | drained before stopping a generation |

Primary and secondary never share a `Client`, `ClientConfig`, `serverDialer`,
PacketConn registry or in-flight counter. The legacy `Outbound.client`,
`config` and `packetDialer` fields are aliases of the current primary and are
kept for existing lifecycle tests; production fallback code uses the runtimes.

## Start

1. `resolveFallbackOptions` validates the contract before any dialer/SDK object.
2. `transportSpec` for primary (and secondary if enabled) captures the fork
   dialer and DNS bridge. No socket.
3. `transportRuntime` creates `Client` + `Store(config)`. No goroutine/socket.
4. `StartStateStart`: start primary, then secondary; on secondary failure stop
   both and clear the runtimes so the next Start builds fresh Clients (SDK
   `Stop` is final).
5. Mark started, increment the local generation, reset the selector, register
   `Close` on the Scope **outside** `Outbound.access`.
6. Re-check `closed` after `Scope.Add`; a synchronous cleanup on a closing Scope
   reports failure instead of success.

## Close

1. Take the write lock, set `closed`, close the selector.
2. Drain in-flight dials with the existing 5 s bound (the caller holds the write
   lock, so no new dial can start).
3. Snapshot both runtimes, release the lock, stop primary then secondary and
   shut down each UDP registry. `closeOnce` makes repeat calls agree.
4. A dial that completes after the drain sees `closed` or a changed generation
   in `acceptGeneratedConn`, closes the SDK connection and returns an error; it
   is not handed to the route.

## InterfaceUpdated

1. Ignore before first dial or after close.
2. Drain in-flight dials, build and start fresh primary/secondary runtimes.
3. Only after both start, swap pointers, increment generation, reset selector,
   update aliases and stop the previous runtimes (closing their UDP registries).
4. If any next start fails, stop the new generation and leave the old one
   running. A late result from the old generation is rejected by generation in
   both `fallbackSelector.report` and `acceptGeneratedConn`.

## Network epoch, Scope, Power

- `InterfaceUpdated` is the existing network authority. The local generation is
  derived from its serialized swap; no second global epoch is created.
- Only the Outbound's one `Scope` cleanup owns both SDK clients.
- There is no background timer, heartbeat or recovery goroutine; recovery is
  triggered only by a business `DialContext` after cooldown. Therefore there is
  no Power Governor registration to add, and no new wakeup source.
- No Mieru-specific health data is written to `runtimecoord.Coordinator`; the
  selector is private, O(1) and dies with the outbound.

## Deadlock/ordering notes

- No external network operation, `client.Start/Stop`, DNS lookup or handshake is
  performed while `fallbackSelector.access` is held.
- `Outbound.access` is held across `client.Start/Stop` only on the existing
  lifecycle paths; these are non-blocking SDK bookkeeping operations. Dial
  handshakes happen after releasing the read lock.
- `acceptGeneratedConn` uses `TryRLock`: if Close/InterfaceUpdated already owns
  the write lock, the dial closes its connection instead of waiting for the
  teardown that is draining it. This prevents a 5 s Close stall on an in-flight
  handshake.
- `Scope.Add` is called without `Outbound.access`; `Close` is idempotent.
