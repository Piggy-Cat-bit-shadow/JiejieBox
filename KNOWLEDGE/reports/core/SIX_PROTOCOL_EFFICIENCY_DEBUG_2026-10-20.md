# Six-protocol efficiency: adversarial debug and acceptance

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` (debugged in place; no branch was created, nothing was force-pushed)
**Implementation round:** start `d993c53e1`, code commits `d1ba155e3` / `91091b276` / `a8870b745`,
delivery report `7d4ffc39a` (updated by `11bf41492`, `a5a1b9ce6`)
**Debug round:** this file, plus the dependency fix `2cb91e96c01a`; the re-pin commit and this
report's own commit are recorded in `git log` and in the final reply (§8 lists the ones fixed here)
**Status:** point-in-time debug record. It does not move `KNOWLEDGE/BASELINE.md`, does not rewrite
the audit, and does not touch any protocol outside the change set.

---

## 1. Change attribution (what is actually in scope)

| repository | before | after |
|---|---|---|
| `sing-box` `testing` | `d993c53e1` | see §8 |
| `sing-tun` pin | `v0.0.0-20261019070836-a7bd7facc7cc` | `v0.0.0-20261020021541-2cb91e96c01a` |

Touched files, and nothing else:

| file | why | owner of the risk |
|---|---|---|
| `transport/http/capsule.go` | L4 owned send, consumption discipline, headroom | buffer ownership |
| `transport/http/connect_udp_owned*.go` (2 files) | 19 tests | every branch of that ownership |
| `protocol/masque/dns_assignment.go` | fold base `'B'`→`'A'` | origin gate |
| `protocol/masque/dns_assignment_test.go`, `dns_origin_adversarial_test.go` | 12 + 20 cases | origin gate |
| `go.mod`/`go.sum`/`test/go.mod`/`test/go.sum` | dependency pin | build identity |
| `sing-tun` `flow_dispatch.go` | return-path grouping | borrowing, lifetime, order |
| `sing-tun` `flow_dispatch_return*.go` (2 files) | 15 tests | same |

D and E were **not** changed in the implementation round, so they are re-checked, not re-debugged
(§6). The pre-existing submodule pointers `clients/apple`, `clients/desktop` were dirty before this
round started and were never staged.

---

## 2. A — MASQUE L4 owned DATAGRAM

### 2.1 Ownership truth table, as implemented and as tested

| state | owner | caller may | exit requirement | test |
|---|---|---|---|---|
| buffer without a Context ID | caller | read/write/Release | exactly one branch releases | `...HeadroomMatrix` |
| `PrependContextID` in place | caller | send/fallback | prefix is content or undone | `...CapsuleCarriesTheOriginalBytes` |
| `PrependContextID` replaced the buffer | **new buffer**, old already released | must not touch the old pointer | caller's handle is empty | `...HeadroomMatrix` (headroom 0) |
| `SendDatagramOwned` accepted | transport | **nothing** | transport releases once | `...DoesNotReleaseWhatItTransferred` (the transport keeps it and the buffer must still be alive) |
| owned refused | caller | rollback → capsule / error / release | no leak, no duplicate | `...RefusalDoesNotLeakOrDuplicate`, `...ErrorClassMatrix` |
| copying send accepted | caller | release itself | released by this package | `...WithoutOwnedKeepsCopyingPath` |
| batch accepted | transport | nothing, per packet | each packet consumed once | `...BatchSizes` (1..65) |
| batch refused mid-way | caller for the remainder | settle the tail | no replay of the accepted prefix | `...LostTailIsReleased`, `...RefusalInTheMiddle...` |
| capsule fallback | caller until the synchronous write returns | must not claim success | old framing and error semantics | `...UnsupportedFallsBackToCapsule`, `...CapsuleCarriesTheOriginalBytes` |

The batch writer's blanket `defer Release` was removed in the implementation round precisely because
a successful owned send moves ownership; the debug round confirmed the replacement holds on every
path, including the one where the transport *keeps* the buffer past the call.

### 2.2 Attack matrix and result

| attack | cases | result |
|---|---|---|
| headroom geometry | 0, 1, 5, 6, 8, 9, 16 | all deliver context ID + payload; caller's handle empty; one transport release |
| batch sizes | 1, 2, 4, 8, 16, 31, 32, 33, 64, 65 | one datagram per packet, order kept, every buffer consumed once |
| refusal in the middle | oversized packet between two normal ones | the two datagrams keep their positions, the oversized one becomes a capsule **in place** |
| error classes | `ErrDatagramUnsupported`, `DatagramTooLargeError`, `net.ErrClosed`, `context.Canceled`, stream reset | see correction below |
| peer without datagrams | `DatagramsEnabled()==false`, owned capable | two capsules, zero datagrams |
| lost tail | fault after 2 of 8 | exactly 2 sent, all 8 consumed |
| capsule bytes | refusal path | type byte, varint length, Context ID, payload byte-identical |
| concurrent writers | 8 writers × 16 packets | 128 datagrams, 128 transport releases, no double consume |

**Correction found by the matrix** (worth recording, not a code defect): `DatagramTooLargeError`
implements `Unwrap() error { return ErrDatagramUnsupported }`, so a too-large datagram matches
`errors.Is(err, ErrDatagramUnsupported)` and the **single-packet** path also falls back to a capsule.
The implementation report assumed it returned an error; the code is in fact consistent with the
batch path, and the test now pins the measured behaviour instead of the assumption.

### 2.3 Buffer copy census (old vs new, boundary only)

| payload | copying | owned |
|---|---|---|
| 64 B | 145–156 ns, 144 B, 2 allocs | 74–102 ns, 64 B, 1 alloc |
| 1 400 B | 448–482 ns, 1 473 B, 2 allocs | 82–96 ns, 64 B, 1 alloc |
| 4 096 B | 992–1 069 ns, 4 933 B, 2 allocs | 100–119 ns, 64 B, 1 alloc |

On a real quic-go connection (5 000 sends, 1 000 B): copying 10.6 µs/datagram, owned 9.5 µs. The
frame allocation is the part that disappears unconditionally; the transport-side saving is small at
these sizes, which is why the report states the delta as an allocation delta and not a throughput
claim.

Headroom: `FrontHeadroom()` is 9 only when the owned capability exists; a stream without it keeps
`CapsuleHeadroom` (6), so an H2 or non-owned path does **not** pay 3 extra bytes per packet.

---

## 3. B — `sing-tun` return path

### 3.1 What the debug round changed

The first implementation always wrote the kept packets back into the caller's slice. Every write
landed on the identical element, so behaviour and allocations were right — but the mutation surface
widened from "only when a packet is passed back" to "always". `-race` proved it matters (§3.2).

**Fix (`sing-tun` `2cb91e9`)**: the compaction writes only when the index and the kept count
disagree, i.e. only after a drop or a pass, and always to an element already read. An all-write
batch is now left completely untouched, which is the contract the callers had before.

Cost of the added branch: n=1 145.5 → 156 ns, still 0 allocs, still below the 237.9 ns it replaced.

### 3.2 The race that found it

```
WARNING: DATA RACE
Write at ... by goroutine 10:  flow_dispatch.go:985 (the compaction write)
Previous read ... by goroutine 13: flow_dispatch.go:975
```

Four goroutines shared one input slice in my own test — a test bug, not a product path — but it
demonstrated that the function had become a writer on every call. With the fix, the same test (each
goroutine now owning its batch, as production does) passes under `-race -count=3`.

### 3.3 Attack matrix and result

| attack | cases | result |
|---|---|---|
| sizes | 0, 1, 2, 3, 4, 8, 32, 64, 128, 1 024 | one batch, order kept, pointer identity kept |
| writebacks | 1, 2, 3, 8, 64 | one batch per writeback per call, own packets only |
| interleaving | P1,P2,P1,P3,P2 | each gets its own order, one batch each (old grouping reproduced) |
| duplicates / nil | same packet three times; nil input | delivered three times; nil returns nil and calls nobody |
| bytes | no rewrite rule | byte-identical before/after |
| downstream copy | 32 packets copied at call time | correct content |
| concurrent close | 4 callers vs `dispatcher.Close()` | no panic, no hang, no double release |
| allocations | 1, 64, 256, 1 024 packets | **0 allocs** at every size |
| contract | all-write batch | input slice not modified (new test) |
| gap | matched, unmatched, matched | two packets compacted around the gap, in order |

Performance (real dispatcher, one writeback, old → new):

| packets | old | new |
|---|---|---|
| 1 | 237.9 ns, 72 B, 2 allocs | 156 ns, 0 B, **0 allocs** |
| 8 | 1 493 ns, 408 B, 5 allocs | 1 157 ns, 0 B, 0 allocs |
| 64 | 10 392 ns, 3 480 B, 8 allocs | 8 919 ns, 0 B, 0 allocs |

### 3.4 Borrowing and lifetime

`ReturnPackets` passes subslices of the caller's batch to the writeback and the writebacks consume
them synchronously (`goWriteback` writes each packet, `systemWriteback` loops `Tun.Write`,
`systemWritebackLinux` hands the slice to `BatchWrite`). The debug round checked all five
implementations rather than assuming, and the test `...DownstreamCopiesDuringTheCall` copies inside
the call to prove the bytes are valid at that point. No inline array is used, so there is no stack
array that a hypothetical asynchronous consumer could dangle from — the storage is the caller's own
slice, which outlives the call by construction.

---

## 4. C — ASCII origin gate

`sameOrigin` is the only caller and it normalizes both sides first, so the fold fix changes no
production path. The debug round added 20 cases that would fail if the gate were loosened:
capital `A` at start/middle/end, both directions, ports (default, non-default, two different),
IPv6 bracketed hex (including a capital `A` in the address — the exact letter the old fold missed),
punycode, trailing root dots, empty authorities, subdomain vs parent in both directions, and a
non-ASCII pair whose case is settled by `normalizeHostName` before the ASCII comparison runs.

`TestSameConnectionDoHStillRequiresEverything` states the load-bearing half directly: the
authenticated origin in any spelling matches, a suffix attack does not, a shorter name does not, a
different port does not. **Verdict: preventive correctness fix; not a reproducible DNS leak, and not
a performance change.**

---

## 5. Cross-component regression (only "did this round change it")

| boundary | check | result |
|---|---|---|
| `transport/http` H1/H2/H3 | full suite, default and `with_quic` | PASS (8.5 s / 25.1 s) |
| `transport/http` under `-race` | full suite | PASS (13.6 s) |
| `transport/masque` (CONNECT-IP owned/batch untouched) | default, `with_quic`, `with_quic,with_low_memory` | PASS (5.5 / 7.1 / 7.1 s) |
| `transport/masque` under `-race` | `with_quic` | PASS (68.8 s) |
| `protocol/http`, `protocol/bridge`, `protocol/tun` | default and `with_quic` | PASS |
| `protocol/tun` under `-race` | `with_quic` | PASS (4.2 s) |
| `protocol/masque` | `with_quic` full suite | PASS (66 s) |
| `protocol/masque` (origin tests) | `-race`, filtered | PASS |
| root package | `release/DEFAULT_BUILD_TAGS_OTHERS` | PASS (6.6 s) |
| `sing-tun` | full package minus two pre-existing Windows timing tests | PASS (335 s) |
| `sing-tun` under `-race` | return path + forward stage | PASS (`-count=3`) |

**`-race` is no longer `BLOCKED_BY_ENVIRONMENT` on this host.** The earlier rounds recorded it as
blocked because `CGO_ENABLED=0`; with the llvm-mingw clang that ships with the local toolchain
(`CC=...\x86_64-w64-mingw32-clang.exe CGO_ENABLED=1`) the race detector builds and runs, and every
touched package was raced above. The Go standard `gcc` named in `go env CC` does not exist on PATH,
which is what made it look impossible.

Pre-existing failures, verified identical on the pre-change code and therefore **not** this round's:

| failure | where | why |
|---|---|---|
| `TestGoKernelDeadline` | `sing-tun`, Windows | "I/O did not wait" — timing assertion |
| `TestGoKernelPacketBatchIdle` | `sing-tun`, Windows | idle deadline with pooled buffers retained |
| six untagged H3 tests | `protocol/masque`, default build | they construct H3 endpoints that only exist with `-tags with_quic` |

---

## 6. D and E: re-checked, not changed

D was re-measured on the new pin: with a real quic-go queue behind a silent path, a datagram send is
still blocked for up to **1.76 s** (mean 21.6 ms), and the stage lock is held across it, so a
`Close()` on that stage waits the same. No production change; the lock redesign needs its own round.

E was re-measured: the DATAGRAM queue is per connection, so a quiet sender goes from 8.9 µs to a
mean of 3.45 s behind a flooding neighbour on the same connection. No production change.

Both are `NOT_CHANGED`; the code paths are identical to the pre-debug tree.

---

## 7. Findings

| id | severity | finding | status |
|---|---|---|---|
| D1 | — | `ReturnPackets` wrote into the caller's batch on every call, widening the mutation contract; caught by `-race` with a shared-slice test | **FIXED** in `sing-tun` `2cb91e9` + re-pin |
| D2 | — | the implementation report and one test assumed the single-packet path returned too-large as an error; it actually matches `ErrDatagramUnsupported` and falls back to a capsule | **CORRECTED** (test pins the measured behaviour) |
| D3 | info | `-race` was thought unavailable on this host; it works with the local llvm-mingw clang | recorded, evidence added |
| — | — | no double release, no use-after-release, no lost tail, no reordering, no origin loosening found | — |

Nothing was reverted: the two production changes held under every adversarial case.

---

## 8. Git

| repository | branch | commit | push |
|---|---|---|---|
| `sing-tun` | `fix/listenconfig-ipv6-only-selfheal` | `676ce1d56d34` (implementation) → `2cb91e96c01a` (debug fix) | fast-forward both times |
| `sing-box` | `testing` | `d1ba155e3`, `91091b276`, `a8870b745`, then the debug round's two commits (adversarial tests, re-pin to `2cb91e9`) and this report | fast-forward |

Pin after the debug round: `v0.0.0-20261020021541-2cb91e96c01a`, carried by both `go.mod` and
`test/go.mod`, with both `go.sum` files updated; `go list -m` resolves to that directory, and the
full `go build ./...` passes in read-only module mode.

---

## 9. The fourteen acceptance answers

1. **Exactly one release after a successful owned send?** Yes. The buffer is not touched after the
   send; `...DoesNotReleaseWhatItTransferred` holds the buffer in the transport and asserts it is
   still alive and unmodified after `WritePacket` returns. A failure leaves the buffer with the
   caller, byte-identical, and no duplicate send (`...RefusalDoesNotLeakOrDuplicate`).
2. **Regression for connections without owned, or H2?** None observed. `...WithoutOwnedKeepsCopyingPath`
   pins the copying path; `FrontHeadroom()` stays 6 without the capability, so no extra reservation.
3. **Batch 1/32/33/64, queue full, partial failure — no replay?** Batch sizes 1..65 are each one
   datagram per packet in order; a mid-batch fault sends exactly the accepted prefix and releases the
   rest (`...LostTailIsReleased`). The batch writer never retries an accepted packet.
4. **Does the headroom change introduce copies/allocation/encoding differences?** No. 9 is reported
   only when the owned capability exists, the wire bytes are unchanged (Context ID + payload, or the
   capsule framing), and headroom 0/1/5/6/8/9/16 all produce identical payloads.
5. **How much does `ReturnPackets` actually save, and for which callers?** 2→0 allocs at one packet
   (237.9→156 ns) and 8→0 at 64 packets (10.4→8.9 µs), measured on the real dispatcher and `portNAT`.
   It applies to every caller of the return path: `transport/device` (batch) and `protocol/bridge`
   (per packet) are the two shapes in tree; protocols that never return packets through a TUN port
   are not covered by this claim.
6. **Is the dependency actually pushed and pinned?** Yes: `2cb91e96c01a` on the existing branch,
   `ls-remote` matches, and `go list -m -json` resolves the pin to the module cache directory rather
   than a local replace.
7. **Does the `'A'` fix keep the origin gate strict?** Yes; 20 cases plus the capability-level test
   show different hosts, suffixes, ports and address families still do not match.
8. **Are H3 stream-level errors and connection faults classified as before?** Unchanged — no code in
   the H3 verdict or error classifier was touched, and the `with_quic` suites pass.
9. **Close / cancel / network change / low memory: leaks, hangs, races, over/under-send?**
   `-race` passes on every touched package; concurrent callers against a closing dispatcher do not
   panic or hang; `with_low_memory` suites pass; the batch accounting test shows sent + released =
   input for every size.
10. **Are the other four protocols unaffected?** The shared surfaces they use were exercised:
    `protocol/http` (H1/H2/H3 client) and the root package under the general tags pass. XHTTP,
    Vision, AnyTLS, Naive and Mieru were **not** individually exercised beyond those suites — that
    is the one honest gap in this table, and it is a test-coverage gap rather than a behaviour change,
    because no code on their paths changed.
11. **Net gain, local cost, rollback?** Gain: one allocation and one payload copy per L4 datagram;
    zero allocations in the return path. Cost: one branch in the compaction (+10 ns at one packet,
    still −35 % against the old code). Nothing was rolled back.
12. **Why no new unified framework/queue/decision centre?** None was needed: both changes are local
    ownership/grouping fixes inside code paths that already existed, and every attempt to generalise
    them (batch-owned L4, per-session queue, lock-outside-write) would add a lifetime contract this
    round has no evidence to justify.
13. **Is everything confined to the change set?** Yes. The touched files are listed in §1; the six
    pre-existing failures were reproduced on the old code and left alone.
14. **Committed and pushed to the correct existing branches?** Yes, on `testing` and on the fork's
    existing branch, both by fast-forward, no force, no new branch, no tag.
