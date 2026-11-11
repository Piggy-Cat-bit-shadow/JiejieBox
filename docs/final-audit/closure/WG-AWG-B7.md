# WireGuard / AWG B7

## Root cause (proven)

The pinned fork (`wireguard-go a765d957fd81`) has exactly one call site of
`wakeOutboundWaiters`: `Peer.Stop`. `waitOutboundSpace` checks the counter
*before* registering its wake channel, and every normal decrement (staged flush
after the handshake, eviction, give-up drop, sequential-sender release) leaves
parked writers asleep. A writer that parks at the 1024-packet cap therefore never
returns after the backlog drains; `transport/wireguard/device_peer_router.go`
calls `Peer.WritePackets` from the peer's only outbound-drain goroutine, so all
TCP flows stall while keepalives (which bypass the staged path) keep the tunnel
looking alive. The counter is not leaking: 1024 is a legitimate full backlog of
128-element containers.

## Fix

Fork branch `fix/outbound-wake-2026-11-11` (`7ece3d66d79d`, pushed to
`Piggy-Cat-bit-shadow/wireguard-go`):

- `releaseOutboundPackets(n)` replaces every decrement and wakes waiters when the
  result is below the cap;
- `Peer.Start` wakes after `Store(0)`;
- `waitOutboundSpace` registers its channel before the final space check;
- `device/outbound_space_test.go`: park→release wakes; release above cap keeps
  parked; Stop still wakes.

Main repo pins `v0.0.0-20261111081811-7ece3d66d79d` in root and test `go.mod`,
`go.sum`, `release/source-lock.json`, `release/dependency-final-shas.json`.

## Two-process verification (WSL2, loopback, userspace endpoints)

`perflab run -pattern upload -streams 2,4 -reps 3 -duration 3s -warmup 1s
-stagger {0s,200ms,1s}` plus 1 s warmup:

| cell | result |
|---|---|
| wireguard 2 streams, 0 ms / 200 ms / 1 s | 9/9 pass, 42.9–53.9 MiB/s |
| wireguard 4 streams, 0 ms / 200 ms / 1 s | 9/9 pass, 46.5–60.5 MiB/s |
| awg 2 streams, 0 ms / 200 ms / 1 s | 9/9 pass, 38.6–55.9 MiB/s |
| awg 4 streams, 0 ms / 200 ms / 1 s | 9/9 pass, 15.4–56.2 MiB/s |

Before the fix, the same cell failed 2/2 at 0 ms and 200 ms spacing. The low AWG
4-stream/1 s figures (15–24 MiB/s) are throughput variance, not a stall; there is
no old-pin before/after for the fixed cells because the old pin cannot complete
them.

Raw runs: `results/b7/` (six runs, exported with logs). Fork commit and tests are
in the fork branch named above.
