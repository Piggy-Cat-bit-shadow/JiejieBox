# Anywhere round three — construction record

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` only. No branch was created, nothing was force-pushed, no tag or release was made.
**Construction start:** `d993c53e11d3b757196b5c3c0d69eef42bea3415`
**Dependency:** `github.com/Piggy-Cat-bit-shadow/sing-tun`, existing branch
`fix/listenconfig-ipv6-only-selfheal`, `a7bd7facc7cc` → `ff4fbed67c0d`; the main repository pins
`v0.0.0-20261020022904-ff4fbed67c0d` in both `go.mod` and `test/go.mod`.
**Status:** point-in-time construction record. It does not refresh `KNOWLEDGE/BASELINE.md`, does not
rewrite the round-three audit, and does not restate the audit's numbers as this round's results.

---

## 1. What was built, and what was deliberately not

| item | verdict | evidence |
|---|---|---|
| A3-1 `portReturn.ReturnPackets` local allocation | **IMPLEMENTED** | 236 ns / 72 B / 2 allocs → 156 ns / 0 B / 0 allocs at one packet |
| A3-2 MASQUE L4 CONNECT-UDP owned DATAGRAM (single packet) | **IMPLEMENTED** | 1 alloc and 1 payload copy removed per datagram; 19 unit tests + a live HTTP/3 end-to-end test |
| A3-2 batch-owned send (`SendDatagramsOwned`) | **REJECTED, measured** | on a real queue the copying and owned calls differ by ~10 % (10.6 vs 9.5 µs), and the queue dominates; batching would remove per-call queue overhead, not the queue |
| C Flush / close liveness | **ANALYSED, NOT FIXED** | the wait chain is proven and pinned by a deterministic test; the fix changes endpoint shutdown semantics and needs an end-to-end measurement first |
| P3 `equalFoldASCII` | **IMPLEMENTED** | one character, 20-case origin matrix proving the gate was not loosened |
| `stagePort` linear scan, `domain_keyword`, Fake-IP, XHTTP poll, TCP template | **NOT TOUCHED** | the round-three audit already falsified or deferred them; no new data contradicts it |

No second cache, no new lifecycle authority, no unified error decision centre, and no change to
CONNECT-IP, H2, plain TUN, Vision, AnyTLS, Naive, Mieru or XHTTP.

---

## 2. A3-1 — the return path's local allocation

### The measurement object

The audit's two numbers describe different things and the report says so: **236–250 ns / 72 B /
2 allocs** came from the real `ReturnPackets` with a real `portNAT`, while **166 ns / 168 B /
3 allocs** came from a replica of the old grouping structure. This round measured only the real
function, through the real dispatcher, with the real `portNAT`, and never mixed the two.

### The change

`ReturnPackets` used to build a `[]returnBatch` plus one packet slice per batch on every call. A port
serves one stage and therefore one writeback, so the grouping is normally unnecessary. The
implementation compacts the first writeback's packets **towards the front of the caller's own slice**
and builds the original grouping only when a second writeback appears.

Two refinements happened during the debug round, both recorded in their own commits:

1. The first version always wrote the kept packets back (each write landing on the same element).
   That widened the mutation contract from "only when a packet is passed back" to "always"; the race
   detector found it with a shared input slice. The compaction now writes **only when the index and
   the kept count disagree**, which happens only after a drop or a pass.
2. Therefore an all-write batch is left completely untouched, which is the contract the callers had
   before this round.

### Red → Green

Against the pre-change implementation (`a7bd7fa`), the new assertions fail with the real numbers:

```
--- FAIL: TestReturnPacketsAllocationStaysZeroAtScale
    size 1: expected 0 allocs, got 2.00
--- FAIL: TestReturnPacketsSingleWritebackKeepsOrderAndAllocatesNothing
    one packet through one writeback must not allocate, got 2.00 allocs/op
```

Against the change they pass, and `go test -race` on the return path passes with `-count=3`.

### A/B (real dispatcher, real `portNAT`, one writeback)

| packets | before | after | after the gap-only refinement |
|---|---|---|---|
| 1 | 237.9 ns, 72 B, 2 allocs | 145.5 ns, 0 B, 0 allocs | 156 ns, 0 B, 0 allocs |
| 2 | 432.6 ns, 120 B, 3 allocs | 288.3 ns, 0 B, 0 allocs | 304 ns, 0 B, 0 allocs |
| 8 | 1 493 ns, 408 B, 5 allocs | 1 109 ns, 0 B, 0 allocs | 1 157 ns, 0 B, 0 allocs |
| 32 | 5 342 ns, 1 688 B, 7 allocs | 4 355 ns, 0 B, 0 allocs | 4 500 ns, 0 B, 0 allocs |
| 64 | 10 392 ns, 3 480 B, 8 allocs | 8 672 ns, 0 B, 0 allocs | 8 919 ns, 0 B, 0 allocs |

The refinement costs ~7 % of the win and buys back the input-immutability contract; that is the
trade recorded here, not hidden.

### Why not an inline array

The audit's candidate was `[2]returnBatch` / inline packet slices. Escape analysis plus a replica
microbenchmark showed an inline array escaping through the interface-typed writeback, and the replica
measured 3 allocs where the real function measured 2 — exactly the measurement-object mismatch this
round was told to avoid. The in-place shape has no array to escape and keeps the call allocation-free
for every batch size measured (1, 64, 256, 1 024 packets).

### Scope of the claim

`transport/device` calls the return path once per batch; `protocol/bridge` calls it once per packet.
Those are the two shapes in the tree. Protocols that never return through a TUN port are not covered.

---

## 3. A3-2 — L4 CONNECT-UDP owned DATAGRAM

### The change

`http3PacketConn` resolves `AsOwnedDatagramSender(stream)` once at construction. One primitive,
`writeDatagram`, prefers the owned send and reports whether the transport took ownership; both send
sites use it. `writeOnePacketLocked` consumes its buffer on every path; the batch writer's blanket
`defer Release` is gone and a mid-batch fault releases only the packets the loop never reached.
`FrontHeadroom()` reports `1 + 8` when the capability exists and `CapsuleHeadroom` (6) otherwise, so
an H2 or non-owned path pays nothing extra.

### Ownership matrix (implemented and tested)

| state | owner | caller may | exit requirement |
|---|---|---|---|
| owned accepted | transport | nothing | transport releases once |
| owned refused | caller | rollback → capsule / error / release | no leak, no duplicate |
| copying send | caller | release itself | this package releases |
| batch accepted | transport, per packet | nothing | each packet consumed once |
| batch fault | caller for the rest | settle the tail | accepted prefix never replayed |
| capsule fallback | caller until the synchronous write returns | must not claim success | old framing and error semantics |

### Evidence

- 19 unit tests against the production functions (`connect_udp_owned_test.go`, `connect_udp_owned_adversarial_test.go`).
- Two mutations were run and both were killed: disabling the owned path, and releasing a buffer the
  transport had taken.
- A live HTTP/3 end-to-end test (`connect_udp_owned_e2e_test.go`) opens a real CONNECT-UDP tunnel
  through `Client.listenPacket`, asserts the resolved capability and `FrontHeadroom() == 9`, sends 32
  single packets and a real 16-packet `WriteConnectedPacketBatch`, and requires the server to receive
  exactly one datagram per packet, in order, with Context ID 0 and the exact payload. Also passes
  under `-race -count=2`.
- **Corrected assumption:** `DatagramTooLargeError` unwraps to `ErrDatagramUnsupported`, so the
  single-packet path carries a too-large datagram as a capsule exactly like the batch path. The
  matrix pins the measured behaviour.

### Performance (boundary only)

| payload | copying | owned |
|---|---|---|
| 64 B | 145–156 ns, 144 B, 2 allocs | 74–102 ns, 64 B, 1 alloc |
| 1 400 B | 448–482 ns, 1 473 B, 2 allocs | 82–96 ns, 64 B, 1 alloc |
| 4 096 B | 992–1 069 ns, 4 933 B, 2 allocs | 100–119 ns, 64 B, 1 alloc |

On a real quic-go connection: copying 10.6 µs/datagram, owned 9.5 µs — the queue dominates at these
sizes, which is why the claim is written as an allocation delta.

### Why batch-owned was rejected

A batch call would save the per-call queue lock and signal, not the queue itself: both arms above are
queue-bound, and their difference is already only ~10 %. Adding `SendDatagramsOwned` to the batch
writer would introduce a second ownership path across 32 frames for a fraction of that 10 %. Rejected
with the numbers, not with an opinion. This is the "B 类不施工" the round asked to record.

---

## 4. C — Flush and the close path

### The dependency graph (verified source)

Start order in `box.go:1123-1133`: … outbound, **endpoint**, certificate-provider, **inbound**,
service. `Scope.Close` runs the cleanups with `slices.Backward` (`adapter/lifecycle.go`), so the
close order is the reverse: service → **inbound** → certificate-provider → **endpoint** → …

Inside that, the chain is:

```
Box.Close
 └─ Scope.Close (reverse order)
     └─ tun.Inbound.Close                       protocol/tun/inbound.go
         └─ tunStack.Close()                    sing-tun stack_go.go:389
             ├─ post closeMessage to engines
             └─ <-engine.exitSignal             stack_go.go:412   ← WAITS
                 └─ engine goroutine: Flush()   stack_go_engine.go:244,300
                     └─ s.access.Lock()         flow_dispatch.go:775
                         └─ port.WritePacketBuffers   flow_dispatch.go:779
                             └─ MASQUE client    protocol/masque/client.go:675
                                 └─ quic-go datagram queue Add   ← BLOCKS on a dead path
             (dispatcher.Close() at stack_go.go:415 is never reached)
     └─ endpoint Close → the QUIC connection closes   ← this would release the Add, but runs later
```

**It is a wait, not a cycle.** No edge points back from the endpoint to the inbound; the teardown is
sequential. But the resource that would release the blocked send is torn down *after* the component
that waits on it, so the wait is bounded only by QUIC's own timers.

### The two measurements that bound it

| measurement | result |
|---|---|
| deterministic fake port (committed as a regression test, `sing-tun` `ff4fbed`) | `Flush` holds the stage lock across `WritePacketBuffers`; `Close` stayed blocked ≥ 200 ms and completed only after the port was released |
| real quic-go queue, silent path (relay forwards the handshake then stops delivering) | a datagram send blocks; 162 datagrams accepted in 2 s, mean 21.6 ms, **worst 1.762 s**, heap delta 0.0 MiB |

So a `Close` that lands while the stage is inside a dead-path send waits for at least that send, and
the number is the queue's, not the lock's.

### What was not done, and why

No production change. The candidate fixes each change a contract this round cannot validate:

1. **Close the endpoint before the inbounds** — a global scope order change; inbounds legitimately
   need endpoints while starting, and the reverse order is load-bearing for other component types.
2. **Bind the endpoint's QUIC client to the scope context** so `Scope.Close`'s `s.cancel()` closes the
   connection immediately — plausible and local, but it changes endpoint shutdown semantics and needs
   a real-box measurement of the stall before and after; that is a round of its own.
3. **A bounded wait in `Go.Close`** — unsafe: the engine would keep using the platform IO that the
   close path frees next.

Classification: **RISK / NEEDS_FIELD_DATA**, with the wait chain and the bounded numbers recorded, and
the regression test committed so any future change to the lock scope fails a test instead of a
production shutdown.

---

## 5. P3 — `equalFoldASCII`

The second operand's fold started at `'B'`, so `("a", "A")` was false while `("A", "a")` was true.
Unreachable in production because `sameOrigin` normalizes first, which is why no caller-level test
could see it. Fixed to `'A'`; 20 adversarial cases now pin the gate: capital `A` in every position,
both directions, ports, bracketed IPv6 hex (including the `A` the old fold missed), punycode, root
dots, subdomain versus parent, and the unicode case that `normalizeHostName` settles before the ASCII
comparison. **Verdict: preventive correctness, not a reproducible DNS leak, and not a performance
change.**

---

## 6. Git

| repository | branch | commits |
|---|---|---|
| `sing-tun` | `fix/listenconfig-ipv6-only-selfheal` | `676ce1d5` (A3-1), `2cb91e96` (gap-only refinement), `ff4fbed6` (close-edge regression test) |
| `sing-box` | `testing` | `d1ba155e3` (P3), `91091b276` (A3-2), `a8870b745` + `611fcf366` + `d7b78e07a` (dependency pins), `705dae22c` (adversarial matrix), `fdcc81af7` (live L4 e2e), plus the delivery and debug reports |

Each item is independently revertible. The `clients/apple` and `clients/desktop` submodule pointers
were dirty before this round started and were never staged.

## 7. Tests run (this host)

| command | result |
|---|---|
| `go test ./transport/http ./transport/masque ./protocol/http ./protocol/bridge ./protocol/tun` | PASS |
| `go test -tags with_quic ./transport/http ./transport/masque ./protocol/http ./protocol/tun` | PASS |
| `go test -tags 'with_quic,with_low_memory' ./transport/http ./transport/masque ./protocol/tun` | PASS |
| `go test -tags <release/DEFAULT_BUILD_TAGS_OTHERS> .` | PASS |
| `go test -race ./transport/http` | PASS |
| `go test -race -tags with_quic ./transport/masque ./protocol/tun` | PASS |
| `go test -race -tags with_quic ./transport/http -run 'TestConnectUDPOwned\|TestHTTP3ConnectUDP\|TestOwnedDatagram' -count=2` | PASS |
| `sing-tun`: full package minus two pre-existing Windows timing tests | PASS (335 s) |
| `sing-tun`: `-race` on the return path, forward stage and close edge, `-count=3` | PASS |

`-race` is available on this host through the local llvm-mingw clang
(`CC=…\x86_64-w64-mingw32-clang.exe CGO_ENABLED=1`); the earlier rounds' `BLOCKED_BY_ENVIRONMENT`
was a toolchain-PATH mistake, now corrected in the debug report.

Pre-existing failures, reproduced on the pre-change code and **not** this round's: `TestGoKernelDeadline`
and `TestGoKernelPacketBatchIdle` (Windows timing), and six untagged H3 tests in `protocol/masque`
that need `-tags with_quic`.

## 8. CI

All three gates ran on **`8323753656c14d7ad0acb8c6808cda6215306db0`**, the SHA that contains every
code commit, the `sing-tun` pin and the go.sum cleanup:

| workflow | run | conclusion |
|---|---|---|
| Verify — nested module integrity, `go test -run '^$' ./...`, race on the fork's data paths | [39359862219](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39359862219) | **success** |
| Android Core ARM64 | [39359862343](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39359862343) | **success** |
| Mobile contract verification | [39359862096](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39359862096) | **success** |

An earlier SHA's green is never counted as a run on this one, and the `Verify` gate is what caught a
real defect in this round's own delivery: each pin bump had left the previous `sing-tun` hash in
`go.sum`, and `go mod tidy -diff` reports it. The fix is its own commit (`8323753`), and the gate is
clean afterwards.

## 9. Final state

- `git rev-parse HEAD` = `git ls-remote origin testing` = **`8323753656c14d7ad0acb8c6808cda6215306db0`**
  at the time the runs above were taken; the only later commits are the documentation amendments
  that record these results.
- Working tree: clean apart from `clients/apple` and `clients/desktop`, the pre-existing submodule
  pointers that were dirty before the round started and were never staged.
- Both modules pin `v0.0.0-20261020022904-ff4fbed67c0d`; `go mod tidy -diff` is clean in both.
