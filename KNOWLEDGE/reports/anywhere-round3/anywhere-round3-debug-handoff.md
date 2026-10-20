# Anywhere round three — debug handoff

**For:** the independent adversarial debug pass (`02_Anywhere_Round3_专项Debug_DeepSeek.md`).
**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` only.

This is not a second report. It is the sheet the debug pass should work from: what changed, how to
run it, where the boundaries are, which paths are still suspicious, and which versions the claims
belong to.

---

## 1. What changed

| item | production change | tests |
|---|---|---|
| A3-1 | `sing-tun` `flow_dispatch.go` `ReturnPackets`: in-place compaction, gap-only writes, original grouping on the second writeback | `flow_dispatch_return_test.go`, `flow_dispatch_return_adversarial_test.go` |
| A3-2 | `transport/http/capsule.go`: owned DATAGRAM preference, consume-on-every-path, batch tail release, `FrontHeadroom` 9/6 | `connect_udp_owned_test.go`, `connect_udp_owned_adversarial_test.go`, `connect_udp_owned_e2e_test.go` (`with_quic`) |
| P3 | `protocol/masque/dns_assignment.go`: fold base `'A'` | `dns_assignment_test.go`, `dns_origin_adversarial_test.go` |
| C | **no production change**; a deterministic close-edge test was added to `sing-tun` (`flow_dispatch_close_test.go`) | same |
| batch-owned | **not implemented**, rejected with measurements | — |

Dependency: `Piggy-Cat-bit-shadow/sing-tun` `fix/listenconfig-ipv6-only-selfheal` `ff4fbed67c0d`,
pinned as `v0.0.0-20261020022904-ff4fbed67c0d` in `go.mod` **and** `test/go.mod`.

## 2. How to run it

```bash
# main repository, default tags
go test ./transport/http ./transport/masque ./protocol/http ./protocol/bridge ./protocol/tun -count=1
go test -tags with_quic ./transport/http ./transport/masque ./protocol/http ./protocol/tun -count=1
go test -tags 'with_quic,with_low_memory' ./transport/http ./transport/masque ./protocol/tun -count=1
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" . -count=1

# race (needs a working C toolchain; on this host set CC to the llvm-mingw clang)
go test -race ./transport/http -count=1
go test -race -tags with_quic ./transport/masque ./protocol/tun -count=1

# the dependency
cd <sing-tun checkout at ff4fbed67c0d>
go test . -count=1 -skip 'TestGoKernelDeadline|TestGoKernelPacketBatchIdle'
go test -race . -run 'TestReturnPackets|TestForwardStage|TestDispatcherCloseWaits' -count=3
```

`protocol/masque` in the **default** build has six pre-existing failures (they construct H3 endpoints
that only exist under `with_quic`). Run it with `-tags with_quic`, or filter by test name. Fixing that
is test hygiene, deliberately not folded into this round.

## 3. Where the edges are

1. **L4 ownership.** The dangerous branches are: owned accepted (transport owns, never touch again),
   owned refused (caller owns, buffer must be byte-identical because HTTP/3 rolls its prefix back),
   and mid-batch fault (release only what the loop never reached). The live e2e covers the accepted
   path; the adversarial file covers the rest against a scripted boundary.
2. **Headroom.** `FrontHeadroom()` is 9 only with the capability. A producer that hard-codes 6 will
   silently fall back to a copy when `Prepend` runs out of room — slow, not wrong.
3. **Return path borrowing.** `ReturnPackets` hands the caller's own subslices to the writeback, and
   the writebacks consume them synchronously. A future asynchronous writeback would need a copy
   contract; nothing in the tree does that today.
4. **Close ordering.** `Box.Close` → `Scope.Close` (reverse) → TUN inbound → `Go.Close` → wait for the
   engine → `dispatcher.Close`. The engine is inside `Flush` while it holds the stage lock across the
   port write. No cycle, but the releasing resource (the endpoint's QUIC connection) is torn down
   later, so the wait is bounded only by QUIC timers (measured worst single send: 1.762 s).

## 4. Suspicious paths not yet fully covered

| path | why it is suspicious | next experiment |
|---|---|---|
| real `Box.Close` with a TUN inbound + MASQUE endpoint and a dead path | the stall is proven from both ends separately, not end-to-end | build a box with the Go stack and a stalled peer, measure `Box.Close` wall time |
| endpoint shutdown semantics | candidate fix #2 (bind the QUIC client to the scope context) changes when the connection closes | measure the stall before/after, then decide |
| owned batch across 32 frames | rejected on measurement, but a queue-heavy profile could change the answer | profile a 1 000-packet burst on a healthy connection |
| `protocol/masque` untagged tests | they fail by build profile, not by product bug | add a capability skip, one file at a time |

## 5. Evidence levels

- `MEASURED_LOCAL`: every number in the construction report, on this host (Go 1.25.5, Windows 10
  19044, Ryzen 7 4800H).
- `VERIFIED_SOURCE`: the close graph, ownership matrix and headroom geometry, read from the current
  files.
- `NOT_TESTED`: WAN throughput, real devices, iOS/Android `phys_footprint`, capsule flow control under
  a real congested peer with a reading application.
- No claim here is `VERIFIED_DEVICE`.

## 6. CI

`Verify` (with two race steps), `Android Core ARM64` and `Mobile contract verification` run on every
push to `testing`. The runs for the final SHA and their conclusions are listed in the closing section
below, added after the runs finished — an earlier SHA's green is never reused.
