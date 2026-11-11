# WireGuard / AWG B7

## Root cause (proven)

The pinned fork (`wireguard-go a765d957fd81`) had exactly one call site of
`wakeOutboundWaiters`: `Peer.Stop`. `waitOutboundSpace` checked the counter before
registering its wake channel, and every normal decrement (staged flush after the
handshake, eviction, give-up drop, sequential-sender release) left parked writers
asleep. A writer that parks at the 1024-packet cap therefore never returns after
the backlog drains; `transport/wireguard/device_peer_router.go` calls
`Peer.WritePackets` from the peer's only outbound-drain goroutine, so all TCP
flows stall while keepalives (which bypass the staged path) keep the tunnel
looking alive. The counter is not leaking: 1024 is a legitimate full backlog of
128-element containers.

## Fix, merged into the fork default

`fix/outbound-wake-2026-11-11` (`7ece3d66d79d`) was reviewed (only
`device/send.go`, `device/peer.go`, `device/outbound_space_test.go`) and
fast-forwarded into `awg3-inject`, the fork's default branch. No merge commit, no
reset, no force-push. Device tests and `go test -race ./device` pass before and
after; `go test ./...` passes. Remote default is now
`7ece3d66d79dcea5bbf872ebe76b1c7863b69ea6`.

- `releaseOutboundPackets(n)` replaces every decrement and wakes waiters when the
  result is below the cap;
- `Peer.Start` wakes after `Store(0)`;
- `waitOutboundSpace` registers its channel before the final space check;
- tests: park→release wakes; release above the cap keeps parked; Stop wakes.

Main repo pins `v0.0.0-20261111081811-7ece3d66d79d` in root and test `go.mod`,
`go.sum`, `release/source-lock.json`, `release/dependency-final-shas.json`.

## Evidence chain

| link | value |
|---|---|
| fork default branch | `awg3-inject` = `7ece3d66d79d` (parent `a765d957fd81`) |
| module pin | `v0.0.0-20261111081811-7ece3d66d79d` |
| kernel binary | sha256 `0157e713c8e2d43fc214e949cde40b9e3fc2dc812360c64cf41ea4b91f2bd3d6` |
| build | Go 1.25.5 linux/amd64, CGO off, 19 lab tags, BuildInfo shows the pin |
| raw runs | `results/b7-final/` (6 runs × 6 reps), each with commit, dirty state, binary SHA, `load_parameters` |
| run parameters | upload, streams 2/4, 3 reps, 3 s, 1 s warmup, stagger 0/200/1000 ms |

The final matrix was rerun from a clean tree at the reconciliation HEAD with the
perflab that records `load_parameters`; the run JSON itself proves the matrix
shape (previously only the shell history did). The older `results/b7/` runs are
kept for history but are **SUPERSEDED** as acceptance evidence because they lack
binary SHA and stagger in their JSON.

## Result

`perflab run -pattern upload -streams 2,4 -reps 3 -duration 3s -warmup 1s
-stagger {0s,200ms,1s}` for `wireguard-endpoint` and `awg-endpoint`:

| cell | result |
|---|---|
| wireguard 2 streams, 0/200 ms/1 s | 9/9 pass |
| wireguard 4 streams, 0/200 ms/1 s | 9/9 pass |
| awg 2 streams, 0/200 ms/1 s | 9/9 pass |
| awg 4 streams, 0/200 ms/1 s | 9/9 pass |

36/36 upload reps pass. Before the fix the same cell failed 2/2 at 0 ms and
200 ms spacing. Goodput varies with host load (roughly 46-62 MiB/s in this run);
the claim is completion, not a throughput comparison. No download/UDP claim is
made for this userspace configuration.
