# Six-protocol efficiency: implementation round

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` (constructed in place; no branch was created, nothing was force-pushed)
**Entry SHA:** `d993c53e11d3b757196b5c3c0d69eef42bea3415`
**Scope:** the three approved candidates (A: MASQUE L4 owned DATAGRAM, B: `sing-tun`
`ReturnPackets`, C: `equalFoldASCII`), plus the verification-only items D/E.
**Status:** a point-in-time delivery record. It does not move `KNOWLEDGE/BASELINE.md`, does not
refresh `INDEX.md`, and leaves the six-protocol audit report untouched.

---

## 1. Verdicts

| item | verdict | where |
|---|---|---|
| **C** `equalFoldASCII` fold base | **DONE** | `protocol/masque/dns_assignment.go` |
| **A** MASQUE L4 CONNECT-UDP owned DATAGRAM | **DONE** | `transport/http/capsule.go` |
| **B** `ReturnPackets` grouping allocation | **DONE** (dependency pushed, pin updated) | `sing-tun` `flow_dispatch.go` + main `go.mod`/`go.sum` |
| **D** `ForwardStage.Flush` lock across a blocking port | **VERIFIED, NO CHANGE** | evidence below; next-round gate stated |
| **E** shared H3 DATAGRAM queue fairness | **ARCHIVED as next-round** | code evidence only; a real stand is outside this round's boundary |

No other protocol was touched. XHTTP, Vision, AnyTLS, Naive and Mieru received no production
change; they appear only as regression targets where the change was adjacent.

---

## 2. C — one-character fold fix

**Defect.** The second operand's fold started at `'B'`, so `equalFoldASCII("a", "A")` was false
while `equalFoldASCII("A", "a")` was true. Unreachable in production because `sameOrigin`
normalizes first — which is exactly why no caller-level test could see it.

**Change.** One character; the comparison is now symmetric. The comment records why it survived.

**Evidence.**
- New `TestEqualFoldASCIIFoldsBothOperands` (11 cases: both directions, whole runs, non-`A`
  capitals, length-first, empty, and a non-ASCII capital that must NOT fold).
- Red→green: with `'B'` restored the test fails on `a vs A`, `abc vs ABC` and
  `masque.example vs MASQUE.EXAMPLE`; with `'A'` it passes.
- `protocol/masque` passes in both the default and `with_quic` builds.
- **Pre-existing, not caused here:** `TestConcurrentEndpointConstructionIsIndependent` fails in
  the DEFAULT (no `with_quic`) build because it constructs an H3 endpoint that cannot exist
  without the tag. It fails identically with the fix reverted, and passes under `with_quic`.

---

## 3. A — MASQUE L4 CONNECT-UDP owned DATAGRAM

### What changed

`transport/http/capsule.go`:

1. `http3PacketConn` resolves `AsOwnedDatagramSender(stream)` **once at construction** and keeps it.
2. One primitive, `writeDatagram`, prefers the owned send and reports whether the transport took
   ownership; both send sites use it (`WritePacket` and `writeOnePacketLocked`).
3. `writeOnePacketLocked` now **consumes** its buffer on every path (transfer, copying release,
   capsule, error). The batch writer's blanket `defer Release` is gone; on a mid-batch fault it
   releases only the packets the loop never reached.
4. `FrontHeadroom()` reports `1 + 8` (Context ID + widest quarter-stream-ID varint) when the
   capability is present, and `CapsuleHeadroom` otherwise.

### Ownership contract (the risk this change had to not introduce)

| condition | send | owner after return | released by |
|---|---|---|---|
| owned, accepted | `SendDatagramOwned` | transport | quic-go, exactly once |
| owned, refused | `SendDatagramOwned` err | caller | this package (it consumed its input) |
| DATAGRAM unsupported | capsule | — | the capsule writer |
| too large | capsule | — | the capsule writer |
| copying success | `SendDatagram` | caller | this package |
| no capability | either | caller | this package |

Batch: each packet is settled inside `writeOnePacketLocked`; a fault releases only the remainder.
A refused owned batch leaves the buffer byte-identical (the HTTP/3 layer rolls its prefix back),
which is what lets the capsule fallback and the too-large reply see the original bytes.

### Tests (`transport/http/connect_udp_owned_test.go`, untagged)

| test | what it pins |
|---|---|
| `...IsTakenAndTransfersOwnership` | owned path selected; Context-ID framing; bytes valid at release time |
| `...DoesNotReleaseWhatItTransferred` | **double-release test**: the transport keeps ownership, so the buffer must still be alive after the call |
| `...FailureKeepsCallerOwnership` | transport fault returned unchanged, not downgraded to a capsule |
| `...UnsupportedFallsBackToCapsule` | capability refusal keeps capsule bytes |
| `...WithoutOwnedKeepsCopyingPath` | a plain stream keeps the old path |
| `...BatchConsumesEveryBuffer` | three packets, order and framing preserved |
| `...BatchCapsuleFallbackConsumesEveryBuffer` | all-refused batch produces one capsule each |
| `...BatchReleasesRemainderOnFault` | first transferred, second faulted, third released |
| `...FrontHeadroomDependsOnCapability` | 9 with the capability, `CapsuleHeadroom` without |

**Two mutations were run against these tests and both were killed:**
- disabling the owned path → "the owned capability must be used" fails;
- releasing a transferred buffer → "WritePacket must NOT release a buffer whose ownership it
  transferred" fails.

### Measured (this host, boundary only)

`BenchmarkConnectUDPWritePacket`, one `WritePacket` through `http3PacketConn`:

| payload | copying | owned | delta |
|---|---|---|---|
| 64 B | 145–156 ns, 144 B, 2 allocs | 74–102 ns, 64 B, 1 alloc | −1 alloc |
| 1 200 B | 409–428 ns, 1 345 B, 2 allocs | 83–85 ns, 64 B, 1 alloc | −1 alloc, −1 payload copy |
| 1 400 B | 448–482 ns, 1 473 B, 2 allocs | 82–96 ns, 64 B, 1 alloc | −1 alloc, −1 payload copy |
| 4 096 B | 992–1 069 ns, 4 933 B, 2 allocs | 100–119 ns, 64 B, 1 alloc | −1 alloc, −1 payload copy |

The owned arm's constant 64 B is the pooled `buf.Buffer` the harness allocates for the payload in
both arms; the delta is the frame the copying path builds. **This is the layer the change touches,
not tunnel throughput** — no WAN, no real peer, no device.

### Regressions

`transport/http` default 8.4 s pass, `with_quic` 25.1 s pass; `transport/masque` pass;
`protocol/http` default and `with_quic` pass; the full tagged profile
(`release/DEFAULT_BUILD_TAGS_OTHERS`) passes for `protocol/masque`, `transport/http`,
`transport/masque`, `protocol/http`.

---

## 4. B — `ReturnPackets` without the grouping container

### The dependency, and why not `dev`

The fork's `dev` was checked first, as instructed. It is **not an ancestor of the pinned branch and
the pinned branch is not an ancestor of it** (`7539c98` carries a different forward-NAT lineage),
so writing to `dev` would have required a merge or a force push — both forbidden. The change went
to `fix/listenconfig-ipv6-only-selfheal`, the branch this repository already pins, which
fast-forwarded `a7bd7fa..676ce1d`.

| | |
|---|---|
| dependency branch | `Piggy-Cat-bit-shadow/sing-tun` `fix/listenconfig-ipv6-only-selfheal` |
| dependency commit | `676ce1d56d347793f453a86535564dde610444ae` |
| previous pin | `a7bd7facc7ccb8148f1f8bcd97453cba92abc9a1` |
| new pin | `v0.0.0-20261020015203-676ce1d56d34` |

### What changed

`ReturnPackets` used to build a `[]returnBatch` plus one packet slice per batch on every call, even
though a port serves one stage and therefore one writeback. It now compacts the batch in place
while every packet shares one writeback, and builds the original grouping only on the first
disagreement — seeded with everything already classified, so the multi-writeback path keeps exactly
the batches, boundaries and order it had. No new state, no scratch pool, no queue, no goroutine.

The in-place compaction only ever writes at positions already read, which is why the append target
is always shorter than the loop index.

### Measured (real dispatcher, real `portNAT`, one writeback)

| packets | before | after |
|---|---|---|
| 1 | 237.9 ns, 72 B, **2 allocs** | 145.5 ns, 0 B, **0 allocs** |
| 2 | 432.6 ns, 120 B, 3 allocs | 288.3 ns, 0 B, 0 allocs |
| 8 | 1 493 ns, 408 B, 5 allocs | 1 109 ns, 0 B, 0 allocs |
| 32 | 5 342 ns, 1 688 B, 7 allocs | 4 355 ns, 0 B, 0 allocs |
| 64 | 10 392 ns, 3 480 B, 8 allocs | 8 672 ns, 0 B, 0 allocs |

Cross-check from the main repository against the new pin (external harness, real
`ForwardDispatcher`): 1 packet 159–160 ns / 0 allocs, 64 packets 9.5–9.8 µs / 0 allocs. This also
supersedes the audit's earlier 236–250 ns / 2 allocs figure, which was measured before the change.

### Tests (`sing-tun`, `flow_dispatch_return_test.go`)

one-writeback order plus a **zero-allocation assertion** at 1/2/8/32/64/128 packets; two-writeback
grouping, batch boundaries and per-batch order; unconsumed pass-through in order; closed dispatcher;
writeback error propagation.

### Regressions

`sing-tun` package suite passes with the two **pre-existing** failures excluded; both fail
identically on the old code and are unrelated to this path
(`TestGoKernelDeadline` — "I/O did not wait"; `TestGoKernelPacketBatchIdle` — "idle wait deadline
reached", a Windows timing test). Main repository: full `go build ./...`; `protocol/bridge` (the
per-packet caller), `transport/masque`, `protocol/tun` pass; `with_quic` and
`with_quic,with_low_memory` pass; the root package passes under the general tag profile.

---

## 5. D — `Flush` lock across a blocking port (verified, no change)

Re-ran the deterministic fixture against the **new** pin (real `ForwardDispatcher`, a
`PacketBufferPort` whose `WritePacketBuffers` blocks):

```
MEASURED: Close still blocked after 300ms while WritePacketBuffers blocks;
          Stage.access IS held across the port call
MEASURED: Close completed 304.0286ms after the port was released
```

So the coupling is unchanged and real: `Close()`, `Dispatch()` and `sweep()` on the same stage wait
for the whole downstream block. What is still missing is the **frequency**: a real QUIC congestion
stand (queue driven full, capsule flow control, cancel propagation) is a project of its own, not a
boundary refinement. Per the round's rule this stays a next-round gate:

- build the loopback stand with a real quic-go connection and a stalled peer;
- measure the p95/p99 of `WritePacketBuffers` and which `Close` actually blocks;
- only then design the lock-outside-write change, with exactly-once and ordering tests.

No production change was made for D.

---

## 6. E — shared H3 DATAGRAM queue (archived)

Code evidence is unchanged: one `quic.Conn` has one 32-frame DATAGRAM send queue
(`datagram_queue.go:15`), `Add`/`AddBatch` block when full (`:61-89`, `:113-153`), and every
CONNECT-IP session on that connection shares it. A two-session stand (one blasting, one
latency-sensitive) needs a real H3 server with two CONNECT-IP sessions; that exceeds this round's
boundary and is recorded as a next-round candidate rather than half-built here.

---

## 7. Git

| repository | branch | commit | push |
|---|---|---|---|
| `Piggy-Cat-bit-shadow/sing-tun` | `fix/listenconfig-ipv6-only-selfheal` (existing) | `676ce1d56d34` | fast-forward `a7bd7fa..676ce1d` |
| `Piggy-Cat-bit-shadow/sing-box` | `testing` (existing) | see the three commits below | fast-forward |

Main repository sequence, each independently revertible:

1. `d1ba155e3` `fix(masque): fold the second operand of equalFoldASCII from 'A'`
2. `91091b276` `perf(masque): send L4 CONNECT-UDP datagrams through the owned path`
3. `a8870b745` `perf(deps): pick up the allocation-free return path in sing-tun`
4. the archive commit for this report

Changed files: `protocol/masque/dns_assignment.go`, `protocol/masque/dns_assignment_test.go`,
`transport/http/capsule.go`, `transport/http/connect_udp_owned_test.go`, `go.mod`, `go.sum`,
`test/go.mod`, `test/go.sum`, and this report. Nothing else. The `clients/apple` and
`clients/desktop` submodule pointers were already dirty when the round started and were not staged.

---

## 8. Not run / limits

- `-race`: `BLOCKED_BY_ENVIRONMENT` (no cgo toolchain on this host). The new ownership tests are
  single-goroutine by design; the batch writer's locking is unchanged.
- Real WAN, real QUIC peer, device memory/energy: `NOT_TESTED`. The only numbers here are
  boundary measurements on one Windows host.
- The `dev` branch of the dependency was not written (divergent); see §4.
- CI was not triggered by this round; no workflow run is claimed.

---

## 9. What this round deliberately did not do

- No new abstraction, pool, queue, timer or lifecycle owner.
- No change to XHTTP batching, Vision, AnyTLS, Naive, Mieru, routing, Fake-IP, the TUN K lookup,
  or `domain_keyword` (upstream semantics).
- No batch-owned variant for L4: single-packet owned already removes the copy, and the batch writer
  already amortises its lock; a batched owned send is a candidate only if a measurement shows the
  per-frame queue cost matters.
- No lock redesign for D, no per-session queue for E.

## 10. Next-round candidates this round produced

1. **L4 batched owned send** (`SendDatagramsOwned` in the batch writer) — measured only if a real
   queue stand shows the per-frame enqueue cost matters.
2. **D's congestion stand**, then the lock-outside-write design if the red light is real.
3. **E's two-session fairness stand.**
4. The pre-existing untagged H3 test in `protocol/masque` should either gain the build tag or skip
   without it; it is a test-hygiene defect, not a product one.
