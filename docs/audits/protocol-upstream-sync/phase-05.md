# Phase 05A — XHTTP packet-up / XMUX data-path audit

| | |
|---|---|
| Phase | 05A (construction) |
| Date | 2026-11-02 |
| Branch | `testing` (default; no `main`) |
| Start base | `d059e086` (clean, synced) |
| Code change | **none** — the bounded-async pipeline was evaluated and rejected on evidence; no independent defect was found |
| Next | `06` — remaining protocols / convergence |

## 1. The write path, measured

`packetUplink` has four distinct stages: a write is queued (`packetConn.Write`), a batch is cut
from the queue, one POST is constructed (`newRequest` + `applyUplinkData`), and the POST is
completed (response drained, XMUX success noted). The current invariant is one POST in flight and a
monotonic seq, with a bounded queue and backpressure.

The existing probe measures the invariant at every RTT, not a claim:

```
round trip  0ms   posts=30  maxInflight=1
round trip 30ms   posts=30  maxInflight=1
round trip 50ms   posts=30  maxInflight=1
round trip 100ms  posts=15  maxInflight=1
round trip 200ms  posts=8   maxInflight=1
```

and the per-post cost is small relative to the batch it carries:

```
BenchmarkPacketUpPostConstruction   1789 ns/op   2203 B/op   15 allocs/op
BenchmarkPacketUpUplinkWrite/8KiB   11822 ns/op  44312 B/op   0 allocs/op
BenchmarkPacketUpUplinkWrite/64KiB  63713 ns/op  325010 B/op  1 alloc/op
```

At the default 1,000,000-byte post ceiling the construction cost is ~0.0002% of the payload, so
trimming individual allocations is not a meaningful data-path win; the RTT-bound cadence is the
dominant term.

## 2. LX bounded-async pipeline: evaluated, rejected

The lx.25 design starts the next POST when the previous request body has been written and drains
the response in the background, bounded at 16 in flight. It requires:

* a bounded in-flight set and a queue bound independent of `sc_max_each_post_bytes`;
* per-POST response accounting with seq ordering (the server reorders by seq) and an error
  priority that does not let a late success revive a failed stream;
* close drain semantics over multiple in-flight bodies, and H1 serial fallback;
* interaction with the XMUX breaker/slot accounting, which currently counts one request per POST.

The evidence in `KNOWLEDGE/modules/xhttp-client-transport.md` §12.3 is a controlled RTT sweep
against the reference: fork/reference upload ratios were 1.18x, 1.23x, 1.07x, 1.01x and 0.97x at
0/30/50/100/200 ms. That is not a clear win; the only point where the reference is ahead is 3%, and
pipelining changes failure and close semantics for every POST. The project's own probe shows the
current design already aggregates to `sc_max_each_post_bytes` and is RTT-bound, not
response-processing-bound.

**Decision: `NO CHANGE`.** No new local measurement shows the pipeline winning enough to justify a
second concurrency model in the transport. The LX design is recorded as a candidate if a future
workload (many small writes with high RTT and no `sc_min_posts_interval_ms` floor) is measured to
lose; the current tradeoff is documented in code and tests.

## 3. Independent items checked (no change)

| Item | Finding |
|---|---|
| `sc_max_concurrent_posts` | accepted and deliberately ignored (`option/v2ray_xhttp.go:135-142`); current Xray serialises, so honouring it would invent behaviour |
| `sc_max_each_post_bytes` / `sc_min_posts_interval_ms` | read per batch/post (`sendPacket`, `nextPostDelay`); no stale copy |
| queue memory | consumed prefix dropped and `pending` set to nil when empty; peak is bounded by the queue limit |
| backpressure | writers block on `notFull`; `TestPacketUpQueueAppliesBackpressure` |
| H1 | already serial; `sc_*` still applied; `TestHTTP1PacketUp` |
| H2/H3 XMUX accounting | breaker/slot/`h_max_request_times` unchanged by this phase |
| header/URL construction | per-POST clone/parse is measurable but ~1.8 µs vs a 1 MiB batch; not worth a shared-layer rewrite |

## 4. Verification

| Command | Result |
|---|---|
| `go test ./transport/v2rayxhttp/ -run 'TestPacketUp(PostCadenceTracksTheRoundTrip\|WriteBuffers\|Coalesces\|QueueAppliesBackpressure)' -count=1 -v` | `PASS`, cadence 9.247 s, `maxInflight=1` at every RTT |
| `go test ./transport/v2rayxhttp/ -run '^$' -bench 'BenchmarkPacketUp(PostConstruction\|UplinkWrite)' -benchmem -benchtime=200x` | numbers in §1 |
| `go test ./transport/v2rayxhttp/ -count=1` (04B run, same code) | `ok … 17.791s` |
| `go test -race ./transport/v2rayxhttp/ -count=1` (04B run) | `ok … 18.948s` |

No VPS/WAN test was used; the probe is loopback with a controlled fake round trip.

## 5. Handoff to phase 06

* XHTTP correctness (X-04) and the data-path evaluation are closed; do not add a pipeline without a
  new local measurement that shows a net win at the deployment's RTT.
* Phase 06 owns Hysteria2/TUIC/AnyTLS/VLESS/SS/Snell and MASQUE convergence. The `xhttp-box`
  independent tree (X-06) stays a comparison oracle; no behavioural delta was needed here.
* The `transport/v2rayxhttp` suite and `-race` remain the regression gate for any future change.

## 6. 05B queue-state review (2026-11-02)

Scope was the existing packet-up/XMUX data path (05 made no code change), not a protocol re-audit.
The attack list maps to tests that already exist and pin the same ownership rules:

| 05B attack | Existing evidence |
|---|---|
| concurrent writes, coalescing, max split, Close during write, fail after header | `TestUplinkConcurrentWritersLoseNothing`, `TestUplinkOrderingAndSeqContinuity`, `TestUplinkCloseAtEveryStage`, `TestPacketUpCoalescesWritesQueuedBehindAPost`, `TestPacketUpBatchRespectsMaxEachPostBytes` |
| out-of-order/early reply, 400/500/no body, late reply after Close | `TestUplinkNon200StopsTheStream`, `TestUplinkPostFailureSurfacesAndStopsAtTheFailure`, `TestUplinkFirstWriteAfterIdleIsPostedPromptly`; one POST in flight, so there is no reply reorder path |
| bounded backpressure / memory / goroutines | `TestUplinkBackpressureIsBoundedAndEndsInAnError`, `TestUplinkReleasesItsQueueOnClose`, `TestUplinkNoGoroutineLeakAcrossManySessions`, `TestPacketUpQueueAppliesBackpressure` |
| one stream's error vs shared XMUX | `TestTransportRedialIsInvisibleToXMUX`, `TestUplinkHidesAnRSTStreamError`; breaker tests; `xmux.roundTrip` ignores `context.Canceled` for the failure streak |
| H1 serial, H2/H3 behaviour | `TestHTTP1PacketUp`; H2/H3 use the same serial `packetUplink`, no pipeline was enabled |
| payload ownership / exactly-once cleanup | `TestUplinkPayloadIsNotAliasedByLaterWrites`, `TestUplinkTinyWritesArriveExactlyOnce`, `TestUplinkHandlesAGoawayWithoutConflictingReplay` |

Fresh run: `go test -race ./transport/v2rayxhttp/ -run 'TestPacketUp|TestSplitConn|TestXmux|TestXMUX|TestBreaker'`
→ **ok 11.158s**. No defect and no code change in 05B; the serial design's failure and close
semantics remain pinned by the tests above.
