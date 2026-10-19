# Anywhere deep study: what the low-memory design actually is, and what it means for this fork

**Reference:** `github.com/NodePassProject/Anywhere`, `main` @ `89905e0e3641bcbd958a66fcb6f027a2bdecec90`
(2026-10-16). Cloned shallow in this environment and read directly; the repository was reachable over
git/HTTPS even though the web-fetch tool refuses non-public addresses.

**Method.** Source first, README last. Every claim below carries a `file:line` anchor from that commit.
Where the brief or the earlier analysis DOCX (a second-hand ChatGPT transcript, not source) disagrees
with the source, the source wins and the disagreement is recorded in §6. No Apple device was available,
so **no `phys_footprint`, energy or throughput number in this document is measured by us**; those are
explicitly marked as project/community claims or NOT_RUN.

**Files read for this report** (line anchors are from these): `TunnelConstants.swift`,
`TunnelLimits.swift`, `FlowBufferLedger.swift`, `BoundedAsyncQueue.swift`, `PacketFlowBridge.swift`,
`SegmentArena.swift`, `PacketTemplate.swift`, `BufferBudget.swift`, `PlaintextArena.swift`,
`TickScheduler.swift`, `FlatLabelTrie.swift`, `TieredRouteMatcher.swift`, `MultiplexerPool.swift`,
`TransportReclaim.swift`, `DNSLookupCache.swift`, `PacketDecoder.swift`, `MITMScriptWatchdog.swift`,
`TunnelStack+Lifecycle.swift`, plus targeted greps for `buildBulk`, `fatalError`, `fragment`,
`tcpMaxConnections`.

---

## 1. The three layers, with the actual numbers

`TunnelConstants` and `TunnelLimits` are the whole policy surface a reader needs before reading any
queue code. The values that matter (all source constants, not README claims):

| Quantity | Value | Anchor | What it is / is not |
|---|---:|---|---|
| TCP connection cap | 128 | `Shared/Networking/TunnelLimits.swift:11` | a state-table cap, not a buffer budget |
| UDP flow cap | 128 | same (`:12`) | same |
| UDP pending resolutions | 64 | same (`:13`) | bounds DNS-wait tasks |
| TCP global pending-buffer budget | 16 MiB | `TunnelConstants.swift:37` | **ledger budget for bytes registered by flows**, not RSS |
| UDP global pending-buffer budget | 16 MiB | `:46` | same |
| TCP buffer-growth budget | 6 MiB | `:41` | bound on *window expansion*, separate from the ledger |
| TCP max receive window | 768 KiB | `:39` | per-connection |
| TCP max send buffer | 512 KiB | `:40` | per-connection |
| Inbound TUN queue watermark | 2 MiB | `:27` | pause threshold, resume at half (see §3) |
| UDP pending-resolution bytes | 32 KiB | `:47` | per-resolution memory |
| Fake-IP pool | 16,384 | `:88` | bounded address space |
| Log retention | 50 entries / 300 s | `:55-56` | bounds UI/diagnostic retention |

Two corrections to the popular reading of these numbers. **16 MiB is not preallocated** and it is not a
ceiling on the TCP subsystem: it is the sum of the `bytes` fields each flow reports to
`FlowBufferLedger`, i.e. a *committed* figure. And **128 is a cap on connection state**, chosen for a
Network Extension; the brief is right that it must not be copied as a product limit.

---

## 2. FlowBufferLedger: the most transferable mechanism, and its real contract

`FlowBufferLedger.swift` is a generic byte ledger with one mutex, a budget, and two admission modes.

```
set(id, handle, bytes) -> [Victim]          // TCP: absolute sync
reserve(id, handle, bytes) -> Admission     // UDP: reserve or reject
release(id, bytes) / releaseAll(id)         // teardown
```

* **TCP (`set`, lines 49-68).** The flow reports its *current* committed bytes; the ledger updates
  `total` and then `while total > budget { evict largest }`. The victims are returned to the caller,
  which is responsible for closing them. There is a deliberate window in which `total` exceeds the
  budget (by the just-added bytes) before the eviction loop drains it. TCP cannot refuse bytes that
  already arrived, so eviction is the only lever.
* **UDP (`reserve`, 72-93).** The flow asks for bytes *before* accepting a datagram. If the request
  alone exceeds the budget it is `.dropped`; if evicting would hit the caller itself it returns
  `.selfEvicted(alsoEvicting:)`, so the caller does not keep a flow the ledger just killed. Only after
  that does it commit.
* **Eviction picks the largest flow** (`evictLargest`, 118-125) with a linear `max` scan under the
  mutex, removes it, and inserts its id into an `evicted` set so it cannot re-enter until
  `releaseAll`. That tombstone is the subtle part: without it, a flow that was evicted but has not yet
  observed its close could re-register and be evicted repeatedly.
* The public `totalBytes` is read-only and the budget is per-ledger (`TCPBufferLedger` /
  `UDPBufferLedger` typealiases, 11-12), so TCP and UDP cannot eat each other's budget.

**What this buys:** a hard, testable invariant — *the sum of registered pending bytes never stays above
the budget after `set`/`reserve` returns* — and an eviction policy that sacrifices the biggest consumer
first. **What it costs:** an O(n) scan per eviction (fine at 128 flows, not a general allocator), a
single lock across all flows, and a correctness obligation on every caller to register and release
exactly once. It also gives no guarantee about memory the caller did not register: crypto scratch,
matcher state, logger buffers and the runtime's own heap are outside the ledger. That is why the
README's number must not be read as an RSS cap.

For this fork, the transferable idea is **not** "add a global byte budget" (we have no evidence of an
unbounded data-path queue to put in it). It is the *asymmetry*: a receiver that can refuse before
committing (UDP) should reserve; a receiver that cannot (TCP) must account absolutely and hand the
victims to the owner that can actually close them. Any future budget in this tree must state which of
the two it is.

---

## 3. Inbound backpressure: stop pulling, do not drop

`InboundQueue` (`PacketFlowBridge.swift:67-154`) is a byte-counted queue between the Network
Extension's read callback and the stack:

* `push` (83-98) appends a batch and, when `pendingBytes >= limit` (2 MiB), sets `paused` and returns
  `false`. The `PacketReader` then **stops re-arming `readPacketsWithCompletionHandler:`** (`receive`,
  209-214). The kernel/NE side, not an internal queue, absorbs the excess.
* `didConsume` (113-122) subtracts the consumed bytes and resumes only when
  `pendingBytes <= limit / 2` — a 2:1 hysteresis, so the reader does not flap at the watermark.
* `next` (100-111) takes the whole pending slice with `removeAll(keepingCapacity: true)`; the capacity
  is retained for reuse (a small high-water cost, deliberately).
* `InboundBatch` (44-65) keeps the original `NSArray` storage alive and exposes
  `UnsafeRawBufferPointer`s borrowed from its `CFData`s. This is the "borrow the batch, copy only when
  crossing an async boundary" rule in code: the batch's lifetime is the queue entry, and the decoder
  copies what it must retain.

**Why this is better than a bounded ring with a drop policy:** TCP input cannot be dropped without
breaking the stream, and the NE already owns a queue. Pausing the pull propagates pressure to the only
place that can hold data without lying. The cost is latency when the stack is slow, not loss.

The 2 MiB is a *threshold on already-delivered batches*, not a hard cap: one `push` can overshoot by its
batch size, and the reader stops after that push. Anyone quoting it as "TUN memory is 2 MiB" is wrong.

---

## 4. Arenas: compact, but not magic

### SegmentArena (`Packages/AnywhereIP/.../TCP/SegmentArena.swift`)

* Chunks hold **4** `Segment.Storage` slots (`chunkSlots = 4`, line 9); allocation pops a free-list node,
  `recycle`/`release` pushes it back, `outstanding` counts live segments.
* `isReclaimable` = no outstanding **and** more than one chunk (15-17); `reclaim` frees all but the
  first chunk and re-threads it (53-61). So an idle connection can return to a one-chunk footprint
  without losing its arena object.
* The control block and payload are adjacent in `Segment.Storage`, which is what makes the chunk
  layout worth the trouble. This is a fixed-layout pool, not a general allocator; it works because
  `Segment` is not a general-purpose object.

### PlaintextArena (`AnywhereQUIC/.../Core/PlaintextArena.swift`)

* A chunk holds `packetCapacity * 4` bytes; `region(count:)` bump-allocates and, when the current
  reservation was turned into a `Data` slice, first advances by the reserved amount (`isSliced`).
* `slice(_:)` (40-50) returns a `Data` **view into the chunk** and sets `isSliced`. A caller that holds
  that `Data` keeps the whole chunk alive; the arena cannot reuse it until the next `region` call, and
  even then only after the slice's owner is done.
* This is the same hazard as a Go `[]byte` sub-slice: small retained view, large retained backing
  array. The arena reduces *allocation count* for short-lived plaintext, not the cost of a long-lived
  slice. The earlier DOCX flagged this correctly.

**Transfer decision:** Go's `buf.Buffer` pool + strict ownership already gives the short-lived case,
and this repository's `KNOWLEDGE/modules/buffer-ownership.md` forbids retaining a view past release.
Reimplementing an arena would add a second ownership model for no measured win.

---

## 5. Timers, pools and DNS: three different authorities, deliberately

### TickScheduler (`Shared/Utilities/Timing/TickScheduler.swift`)

* One shared `TickScheduler.shared` (60-64) with a binary heap of `Entry{scheduledAt, tolerance,
  sequence, payload}` (78-87), a `wakeups` `AsyncInbox` (141), and one sleep loop that waits for the
  head deadline with a tolerance (268-294).
* `TimerPrecision` provides a tolerance/grid per deadline: `schedule` computes
  `deadline.precision.tolerance(forRemaining:)` (166, 180-181). This is the coalescing mechanism: the
  runtime may wake slightly late within tolerance, so nearby deadlines batch.
* Cancellation is **lazy**: `DeadlineToken.cancel` just claims a latch and calls `noteCancelled`
  (28-31); the heap entry stays until it reaches the head. Cheap cancel, but the wakeup at that
  deadline still happens. A Go `time.Timer.Stop` already removes the runtime timer and avoids the
  wakeup; copying this design into Go would trade a cheap cancel for an extra wakeup, the opposite of
  the goal.
* `DeadlineRegistration` (34-57) is the source-registration variant used by the mux pool; `poke`
  re-evaluates a source, `cancel` unregisters, `deinit` cancels. Fine for Swift, but in Go the
  lifecycle authority here is `adapter.Scope`, not a scheduler registration.

### MultiplexerPool (`Shared/Networking/Protocols/Core/MultiplexerPool.swift`)

* Idle eviction is a `.relaxed` `TickScheduler` deadline (`armIdleCheck`, 95-106), armed only when the
  pool is open and has at least one mux. `runIdleEviction` (114-145) sorts idle muxes by last activity,
  keeps `minIdleKeep`, evicts `softCapPerKey` surplus or anything older than `idleTimeout`, then closes
  them outside the lock.
* `minIdleKeep` is applied by index *after* sorting, and only for idle muxes; `hardCapPerKey` is a
  policy field, not enforced in this path. The DOCX's caution is right: a cap field is not a guarantee.
* `retire` (173-184) transitions to `.closed`, takes all muxes and the deadline under the lock, cancels
  the deadline, then closes outside. That split (state under lock, close outside) is the same rule as
  this fork's `Scope` cleanups.

### BoundedAsyncQueue: a different contract from InboundQueue

`Anywhere Network Extension/Network/BoundedAsyncQueue.swift` is the reliable-order queue: `put` blocks
the producer when `buffer.count == capacity` (`:44-59`), `take` blocks the consumer when empty
(`:61-87`), and `finish` wakes both (`:89-100`). There is **one** producer waiter and **one** consumer
waiter slot (`:24-35`), which is sufficient for its single-producer/single-consumer use and is not an
MPMC guarantee. The lesson is the pairing: a lossless stream gets a blocking bounded queue; TUN input,
which cannot be dropped, gets the deferred-pull gate above. A single "bounded queue" abstraction for
both would be wrong.

### PacketTemplate: fixed work at connection creation

`Packages/AnywhereIP/.../TCP/PacketTemplate.swift:19-59` precomputes the IP header and the
pseudo-header checksum once per connection (IPv4 and IPv6 in one value-typed struct), then
`encode` (`:61-...`) writes the header and completes the TCP checksum per packet with an incremental
sum. That is the "move everything that depends only on the 5-tuple out of the packet loop" rule in its
purest form. In this fork the equivalent lives below the Go boundary (`sing-tun`/gVisor writes the IP
header per packet), which is why the study records it as a design constraint rather than a patch.


### Two budgets, not one (`BufferBudget`)

`Packages/AnywhereIP/.../TCP/BufferBudget.swift` is the other half of the memory story: a lock-free
`Atomic<Int>` remaining-bytes counter with CAS `reserve`/`release` (7-31), wrapped in a `GrowthPolicy`
that also carries `maximumWindow` and `maximumSendBuffer` (33-37). It gates **window growth** (the 6 MiB
budget), while `FlowBufferLedger` gates **pending bytes** (the 16 MiB budget). They are separate on
purpose: a flow may be allowed to keep its current buffers but not to grow, and the global ledger may
evict a flow whose growth budget is fine. Any port of the idea must keep the two questions apart, or a
single number will silently mean two different things.

### Lifecycle (`TunnelStack+Lifecycle.swift`)

`start` claims a `startEpoch` and refuses to proceed if the phase moved (`:19-47`); the stack runs as a
`withDiscardingTaskGroup` tree (`:69-96`) with a deferred-restart generation; `stop` transitions and
awaits the root task (`:118-131`). This is the same shape as this fork's `adapter.Scope` +
`common/runtimecoord` epoch, and the same rule applies: a late callback from an old epoch must be
rejected, not reconciled.


### DNSLookupCache (`Shared/Networking/DNS/DNSLookupCache.swift`)

* `Entry{value, expiry}` + `inFlight: [Key: LookupBroadcast<Outcome>]` + `generation` (26-31).
* `join` (93-113) is single-flight: an existing in-flight broadcast is awaited; otherwise a `Task` is
  started with the **generation captured at scheduling time** (`scheduledGeneration`), and `store`
  (128-136) drops the result if the generation moved. That is the same "old network must not repopulate
  a new cache" rule this fork implements with DNS generation guards.
* `servesStaleOnFailure` and `waitLimit` are explicit policy knobs: a failed refresh can serve the
  previous value, and a waiter can time out rather than hold a flow forever.

**Takeaway:** Anywhere does not have one "resource manager". It has a ledger (bytes), a queue
(backpressure), a scheduler (time), a pool (reuse), and a cache (DNS), each with a single owner and a
small contract. That decomposition, not any single class, is the architecture worth learning.

---

## 6. Claims checked against source (and where the brief/DOCX is imprecise)

| Claim in the brief/DOCX | Source verdict | Evidence |
|---|---|---|
| TCP/UDP caps 128, pending resolutions 64 | **True** | `TunnelLimits.swift` |
| 16 MiB TCP + 16 MiB UDP budgets, 6 MiB growth budget | **True** | `TunnelConstants.swift:37,41,46` |
| Ledger evicts the flow with the most buffered bytes | **True**, with an `evicted` tombstone and victim hand-off | `FlowBufferLedger.swift:62-65,118-125` |
| Inbound queue pauses at 2 MiB, resumes at ~1 MiB | **True**; resume is exactly `limit/2` | `PacketFlowBridge.swift:90-93,118` |
| `PlaintextArena` shares a chunk and can retain it via a small slice | **True**; `isSliced` deferral | `PlaintextArena.swift:24-50` |
| `FlatLabelTrie` uses LOUDS, rank index, `Int16` payload ids, `buildBulk` | **True** | `FlatLabelTrie.swift:12-18,272`; `TieredRouteMatcher.swift:19-45` |
| `TickScheduler` has precision classes and coalescing | **True**; cancellation is lazy, unlike Go timers | `TickScheduler.swift:163-181,268-294` |
| `PacketDecoder` rejects IPv4 options/fragments | **True** — a real compatibility boundary | `PacketDecoder.swift:52` |
| MITM watchdog may terminate the process | **True** (`fatalError`) — do not imitate | `MITMScriptWatchdog.swift:52` |
| "Global buffer ledger bounds RSS" | **Imprecise**: it bounds registered pending bytes only | §2 |
| "16 MiB is preallocated / the TCP subsystem cannot exceed it" | **False** | constants + `set` semantics |
| Anywhere beats sing-box by a specific factor | **Unverified**; no same-device A/B exists in either repository | — |

---

## 7. What is genuinely new here (beyond the brief's list)

**A. The ledger's admission asymmetry is the reusable design, not its eviction policy.**
`reserve` (UDP) can say no before committing; `set` (TCP) cannot, so it evicts and hands victims back.
The code makes the difference explicit in the type (`Admission` vs `[Victim]`). A future budget in this
fork must answer "can this layer refuse before it commits?" first; if it cannot, eviction must be owned
by the layer that can close, or the budget becomes a number nobody enforces.

**B. Backpressure here is deferred *pulling*, and the high watermark is not a cap.**
The queue never drops; it stops calling `readPackets`. One batch can overshoot the threshold, and the
effective bound is "watermark + one batch". This is a better fit for TUN than a lossy ring, and it is
why the 2 MiB number should not be transplanted as a memory limit.

**C. Arena benefit is bounded by ownership, not by allocation count.**
`PlaintextArena.slice` pins a 4-packet chunk; `SegmentArena` can only reclaim when `outstanding == 0`.
Both are explicit that *no outstanding borrow* is the precondition for reuse. In Go, the same condition
is enforced by `buf.Buffer`'s release contract; an arena without that contract would be a second,
weaker ownership authority. That is why "port the arena" is not on the table.

**D. Lazy timer cancellation is a deliberate trade that Go should not copy.**
`DeadlineToken.cancel` is O(1) and leaves the heap entry; the wakeup still happens. A Go timer's
`Stop` participates in the runtime's timer wheel and can actually avoid it. Copying the lazy design
would add a goroutine and a heap for no measured wakeup reduction.

**E. `buildBulk`'s win is already realized differently here.**
Anywhere sorts and bulk-builds the trie to avoid a transient dictionary; this fork's `.srs` v3+ format
stores the succinct matcher and `srs.Read` builds it directly off the decompressed stream (see
`route/rule/rule_set_scale_census_test.go:32-52`), so there is no `[]string` intermediate to remove.
That census is also the evidence that further rule-set "optimization" has little left to win.

**F. The two projects share the same failure mode, not the same fix: caches must be generation-fenced.**
Anywhere captures `generation` when scheduling a DNS lookup and drops the result if it moved; this fork
does the equivalent in `dns/client.go` with a generation guard and tests. Anywhere's extra `waitLimit`
and `servesStaleOnFailure` knobs are worth studying as *policy*, but implementing a second cache would
violate this fork's single-authority rule.

---

## 8. What this fork already has (verified in this tree, not assumed)

| Capability | Where | Status |
|---|---|---|
| Resource ownership/cleanup authority | `adapter/lifecycle.go` (`Scope`), `box_lifecycle.go` | existing; not duplicated by this round |
| Network generation / rebind | `common/runtimecoord/coordinator.go` | existing |
| Power/sleep policy | `common/power/governor.go` | existing; rule-set refreshes already gated (`route/rule/rule_set_updater.go`) |
| Buffer geometry + low-memory profile | `common/bufgeom/doc.go`, `scripts/ci/test-low-memory.sh`, `with_low_memory` | existing; this round ran `route/rule` under the tag |
| DNS cache bounds + generation guard | `dns/client.go:185,673` (`freelru`, capacity ≥1024), `cache_generation_guard_test.go` | existing |
| Rule-set mmap + succinct matcher | `route/rule/rule_set_mmap_ios.go`, `common/srs/mmap.go`, `rule_set_scale_census_test.go` | existing |
| Sniff bounded copy / cached-buffer ownership | `common/sniff/sniff_bounded_copy_internal_test.go`, `route/conn_cached_buffer_test.go` | existing |
| Splice `handed` defect recorded in KNOWLEDGE | `KNOWLEDGE/modules/buffer-ownership.md:76-100` | **stale: fixed in the pinned dependency** (see Report 2 §3) |
| Runtime memory diagnostics | `experimental/libbox/runtime_memory_diagnostics.go` | existing |

---

## 9. Limits of this study, and what to research next

* **No device measurements.** `phys_footprint`, energy and NE memory limits are not measured here; the
  50 MiB limit is a community/Apple-forum figure, not a repository fact.
* **Not every file was read.** The reading list is §0; `AnywhereQUIC` recovery/pacing, the TLS/VLESS
  builders and the MITM engine were only spot-checked. Claims about those are deliberately absent.
* **The DOCX is second-hand.** It was useful for navigation, but two of its framings ("16 MiB is the
  TCP subsystem", "zero-copy everywhere") do not survive source reading.
* **Next, in order:** (1) `ConnectionCore+Send`/`Pacer` against this fork's hysteria2/QUIC send path,
  measuring wakeups and bytes per burst; (2) `TieredRouteMatcher`'s keyword automaton vs this fork's
  domain matcher on a 100k-domain set (the census already gives the baseline); (3) `MultiplexerPool`'s
  `minIdleKeep` policy as a *parameter study* for `route/reference.go`, not as a new pool.

**Bottom line:** Anywhere's advantage is a set of narrow, owned contracts — a byte ledger that only
counts what callers register, a pull-based backpressure gate, compact matchers built without
intermediates, and a shared deadline scheduler — not Swift, and not a single "low memory mode". The
most useful thing this fork can take is the **discipline of asking which layer can refuse, which layer
must evict, and which layer owns the close**, which is exactly the question the construction in Report 2
applies to rule-set loading.

## 10. Findings by action class (for the construction round)

* **A — shipped:** remote binary rule-set streaming (no cache): removes the download copy from the
  load peak; Report 2 §3.1.
* **B — promising, not shipped:** packed-DNS cache to avoid `dns.Msg.Copy`; a pooled per-datagram
  buffer in the `sing` dependency (`buf.NewSize` in `CopyPacket`); a policy study of
  `MultiplexerPool`'s `minIdleKeep` against `route/reference.go`. Each needs an ownership/measurement
  decision that this round did not make.
* **C — deliberately not ported:** the global ledger without an enforcing queue, the arenas, the
  custom timer scheduler, the 128-connection caps, and Anywhere's IPv4 fragment/options rejection
  (a compatibility boundary, not a win).

