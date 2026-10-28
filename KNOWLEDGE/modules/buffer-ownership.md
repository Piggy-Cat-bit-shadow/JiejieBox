# Buffer ownership and memory

**Dependency path scope:** `common/buf/*`, `common/bufio/*` and `common/cond.go` cited in this
module are paths within the pinned `github.com/sagernet/sing` replacement, not this repository root.
Resolve the active fork and version from `go.mod`; old line numbers and pool geometry require
rechecking against that exact pinned source before use.

## kTLS read-wait tail ownership after v0.1.9

**VERIFIED_SOURCE**, testing target `011f3fdcd99`: the Linux/Go 1.25/`badlinkname` gated
`common/ktls/ktls_read_wait.go` drains post-handshake messages before returning data. If an alert
or record error follows data already read, `deferReadErrorAfterData` returns the buffer first and
queues the error for the next read; releasing it on that branch would lose real tail bytes. See
`common/ktls/ktls_read_wait_test.go`. These Linux-only paths were not executed in the Windows
calibration; a Windows Go test cannot validate kernel TLS behavior.

**Scope:** cross-cutting. The rules live in `common/sniff`, `common/bufgeom`,
`transport/masque`, `route/conn*.go`, `route/splice.go`, `route/packet_destination_guard.go`,
`common/trafficsched/gate.go`, and the retirement path in `route/reference.go`.

**Provenance:** `common/bufgeom` is **fork-created**. The ownership documentation and the
census/bounded-copy tests are fork work. Many of the individual rules are upstream's structure,
defended by fork-added tests.

**Load this module BEFORE any zero-copy, allocation, buffer-pool, batch-ownership or splice change.**
This is the module whose whole purpose is to stop a well-intentioned optimisation.

---

## 1. The rules, each with what breaks

### 1.1 Buffer acquisition and geometry

| # | Rule | Anchor | What breaks |
|---|---|---|---|
| B1 | `buf.NewPacket()` = `Get(UDPBufferSize)` from one of **12 `sync.Pool`s**; `MaxPooledBufferSize = 73728` | `common/buf/alloc.go:11,19-36`; `buffer_standard.go:7` | see B1a/B1b — the failure is a **panic**, not a leak |
| B1a | `Put` accepts only `cap == MaxPooledBufferSize` **or a power of two in [64, 65536]** | `common/buf/alloc.go:89-97` | a non-poolable geometry **panics on the first release**: `Buffer.Release()` ends in `common.Must(Put(…))` (`buffer.go:300-309`) and `common.Must` panics (`common/cond.go:294-300`). A CI guard exists because this class shipped once as `panic: buffer overflow: capacity 16384, start 0, need 9` (`scripts/ci/test-low-memory.sh:16-25`) |
| B1b | Above `MaxPooledBufferSize`, `Get` returns **nil** | `common/buf/alloc.go:41-44` | a nil-slice panic inside `buffer.go:343-369` |
| B2 | `Buffer.Bytes()` is a **view** into the pooled array; `Release()` returns it to the pool **and zeroes it** | `common/buf/buffer.go:343-345`, `:300-309` | a retained slice reads recycled, zeroed memory. Note the consequence for diagnosis: **`Release()` on an already-zeroed struct is a no-op**, so an *extra* release is invisible at run time |
| B3 | `ReadOnceFrom` returns `io.ErrShortBuffer` when the buffer is full | `common/buf/buffer.go:137-144` | this is `PeekStream`'s byte ceiling — treating it as an error rather than a bound changes sniff behaviour |
| B4 | A pooled buffer must survive `Advance`/`Prepend` **without reallocating its payload** | `common/bufgeom/geometry_test.go:99 TestBufferAcquireNeverReallocatesThePayload`, `:63 TestGeometryBuffersAreExactlyPoolable` | an extra allocation per packet on the hot path |
| B5 | The allocation **ceiling** assertions are `//go:build !race`; under `-race` only the structural half runs | `common/bufgeom/race_detector_test.go` (`//go:build race`) vs `race_detector_stub_test.go` (`//go:build !race`) | under `-race` a pooled `buf.Get`/`Put` cycle **measured 8,251 bytes per acquire against 1.7 without it**; loosening the 1024-byte ceiling to accommodate that would erase the pooled-versus-reallocated distinction the assertion exists to make |

More detail on the geometry, including the measured 2× difference between the two profiles, is in
`mtu-and-geometry.md`.

### 1.2 Borrowed views and retained memory

| # | Rule | Anchor | What breaks |
|---|---|---|---|
| B6 | A `payloadReader` cursor borrows segments and **must not outlive the `PeekStream` call that created it** | `common/sniff/sniff.go:88-91` | a sniffer retaining the cursor observes a recycled pool buffer |
| B7 | `takePacketBytes` returns a **view into the caller's datagram**, valid only while the datagram lives | `common/sniff/quic.go:23-35` | a retained DCID/PN sample reads recycled memory |
| B8 | QUIC's working copy is a **fresh packet-sized `make`, deliberately not pooled**; the decrypted buffer aliases it | `common/sniff/quic.go:183-184`, `:226` | pooling it makes a fragment outlive its owner |
| B9 | Retained `qCryptoFragment.payload` aliases the **working copy**, never the caller's datagram; the reassembly buffer is released by `defer` | `common/sniff/quic.go:305-306`, `:341-342`, `:472-476` | a retained fragment survives the datagram's `Release` |
| B10 | Header protection writes **4 bytes in place**, so a `Length` that cannot reach them is rejected | `common/sniff/quic.go:125-131`, `:190-192` | out-of-bounds write into the datagram tail |

B6–B10 are proved, not asserted: an **address-range** test (`quic_ownership_test.go:314-350`),
**datagram-poisoning** equivalence tests (`:206-281`, `quic_boundary_test.go:427-456`), and a `cap`
bound that does not grow with a coalesced tail (`sniff_bounded_copy_internal_test.go:120-156`). See
`sniff.md` §2.

### 1.3 Ownership transfer and batch

| # | Rule | Anchor | What breaks |
|---|---|---|---|
| B11 | Each batch **chunk** is all-or-nothing; the accepted prefix is transferred and only the refused remainder stays with the caller | `transport/masque/session.go`, `writeBatchOwned` | replaying an accepted prefix double-sends or releases transferred buffers |
| B12 | On the **owned-datagram** path, ownership **transfers on success**, so the release is skipped entirely | `transport/masque/session.go:484-489`, `:495`; `transport/http/client_h3.go:506` | **double release / use-after-free** |
| B13 | A `PrependContextID` **substitution releases the original**, so it must refuse the affected chunk before it is attempted and update the caller's slice in place | `transport/masque/session.go`, `writeOwnedBatchChunk` | stale pointers, double release or packet loss on fallback |
| B14 | Every failure path in `writePackets` releases **exactly the still-owned remainder** | `transport/masque/session.go:518-520` | a leak of pooled buffers |
| B15 | A batch of **one** never takes the batch path | `transport/masque/session.go:467-472` | a measured regression |
| B16 | Packet headroom is reserved by the **producer** so the 1-byte context ID fits | `transport/masque/session.go:561-565`; `protocol/masque/client.go:557 FrontHeadroom`; `transport/masque/server.go:428 receivedPacketHeadroom` | every packet takes the B13 substitution path |
| B17 | `matchRule`'s packet buffers belong to the caller and must be released on **all five** error paths | `route/route.go:394-400`, `:412`, `:422`, `:426`, `:440`; comment `:396-399` records the measurement: **1,817 bytes released vs 18,255 unreleased** | a 16 KiB pooled allocation never recycled |
| B18 | Release sites are explicit, not inferred: `buf.ReleaseMulti` (`route/route.go:146,156,160,184`), `N.ReleaseMultiPacketBuffer` (`route/route.go:400,412,422,426,440`; `route/dns.go:44`), `route/fakeip_conn.go:205`, `protocol/masque/{client,server}.go`, `transport/masque/client.go`, `common/listener/listener_udp.go`, `common/iponly/conn.go`, `common/trafficsched/gate.go:135,353,376`, `transport/device/*`, `transport/wireguard/device_peer_router.go` | as cited | leaks |
| B19 | Double-release guards exist and must be preserved: `route/conn_reclaim.go` (`managedConnState`, `markKernelOwned:114`, `reclaimState:118`, `touch:125`, `countBytes:136`) and `route/splice.go` (`spliceSource.takeCached:145`) | as cited | a spliced connection's cached packets released twice |

### 1.4 Lifetime, pools and retirement

| # | Rule | Anchor | What breaks |
|---|---|---|---|
| B20 | `IdleConnectionReleaser` may only close a resource with **no active user traffic**, and the walk can **never dial** | `adapter/outbound.go:214-219`; `route/reference.go:420-440` | a trim becomes a reconnect trigger — the exact shape the memory path must not have (`route/reference.go:394-411`) |
| B21 | `ReuseSuspect.RetireSuspect()` runs **before** `CloseIdleConnections`; a pool that can only drop idle gets the old behaviour | `route/reference.go:496-511`, `:547-556` | a new stream attaches to a session nobody trusts |
| B22 | The retirement walk asserts `IdleConnectionReleaser`, **not** the bundled `IdleConnectionKeeper` | `route/reference.go:479-484` | asserting the bundled interface silently failed at run time on **every** walk, leaving the HTTP client pools unreachable through four review passes (commits `93823c3d`, `8735b1d`) |
| B23 | `route/reusable_owner_inventory_test.go:49,96,117` **forbids a second `ReuseSuspect` pool** | as cited | "add a new reusable pool" is not an available move without changing that test |
| B24 | Scope cleanups never run while the scope lock is held | `adapter/lifecycle.go:124-125`, `:133-134` | reentrancy deadlock |
| B25 | `RebindLease.Complete()` **always cancels**, even for a superseded lease | `common/runtimecoord/coordinator.go:307-309` | a cancel func left registered against a live parent |
| B26 | Pool introspection is a **badlinkname** mirror of the pinned toolchain's `sync.Pool` layout, with a size assertion that fails loudly on layout change | `experimental/libbox/internal/runtimeinfo/pool_badlinkname.go:14,44-47` | a silent misread of pool internals |

---

## 2. The splice path — corrected setup handover

`docs/internal/fork-splice-silent-stall.md` is irreplaceable: it is the sole record of this defect and
its fix.

* The historical defect reported handover before `Pipe2` succeeded, so a setup failure prevented
  userspace fallback and could stop the copy without moving data.
* In the pinned `sing` revision `1729220c46c4`, `common/bufio/splice_linux.go` returns
  `handed=false` when `splicePipe2` fails and sets `handed=true` only after the pipe exists.
  `splice_actual_linux_test.go` includes a forced-failure regression. This is source-verified at
  that pin; Linux execution was not performed in this Windows refresh.
* The splice counters measure **acceptance**, not a kernel splice (`c02d7cc8e` renamed them to
  `spliceReasonHandoverAccepted` / `Accepted` / `AcceptanceRatio()`), and a source-level test forbids
  the old `Successes` / `successes=` / `"splice success"` / `"splice ratio"` names.

Two facts that decide whether the kernel splice can engage at all, each checked rather than assumed:

* **`copyDirect` is tried BEFORE `copyExtended`.** `CopyWithCounters` calls `copyDirect` first and only
  falls back when it returns `handed == false`, so a read waiter on either end does not preempt the
  syscall path.
* **`ktls.Conn` is the only implementer of `SyscallReader`/`SyscallWriter`**, and the interface couples
  `SyscallConnForRead()` with `HandleSyscallReadError(err)` in **one** contract — so a type cannot hand
  out a raw fd without also saying how to handle the non-application-data record that arrives on it. No
  partial implementation is expressible.
* A raw connection without kTLS **falls back explicitly**: `splice` reaches
  `else if !notFirstTime && E.IsMulti(err, unix.EINVAL, unix.ENOSYS) { handed = false; return }`
  (`splice_linux.go:68-70`), so a `syscall.Conn`-only source that cannot splice yields to the userspace
  copy on the **first** attempt.

`common/ktls` is gated by `linux && go1.25 && badlinkname`. It now has regression tests including
`common/ktls/ktls_read_wait_test.go`; those tests require the Linux/tagged environment and were not
executed in this Windows refresh.

**Because it cannot be run here, two kTLS read-path defects were found by comparing the fast path
against its slow path and fixed by mirroring rather than by executing:**

| Where | Defect | Consequence |
|---|---|---|
| `ktls_read_wait.go:35` | `Input.Len() == 0 && Input.Len() > 0` — unsatisfiable | the close-notify cleanup never ran, so a read returned `(n, nil)` instead of `(n, EOF)` |
| `ktls_read_wait.go:22-27` | no `Hand` drain after `readRecord` | a TLS 1.3 KeyUpdate was never applied, so the kernel kept the old RX key while the peer moved on |

**The root cause was the absence of the comparison, not either typo.** `Conn.Read` and
`Conn.WaitReadBuffer` are two implementations of one contract with **no test asserting they agree**, in
a package with zero tests. The concrete recommendation on record is a table test driving the same
record sequence through both paths and asserting the same `(n, err)`.

---

## 3. The zero-copy objective — what is observable and what is not

`docs/internal/zerocopy-closure-ledger.md`.

| Level | Observable from | State |
|---|---|---|
| `splice_eligible` | sing-box | shipped — the userspace wrapper walks |
| `handover_accepted` | sing-box | shipped (`c02d7cc8e`) |
| `copy_direct_selected` | **fork only** — `copyDirect`'s `handled`, consumed inside the fork | not observable from this repository |
| `actual_kernel_splice_observed` | **fork only** — `splice()`'s `n > 0`, never leaves the package | not observable from this repository |

> **Zero-copy levels 3–5 are structurally unobservable from sing-box**, and a census built from
> `handed` "would be wrong in the direction that flatters the change". Do not build one.

**"Packet clone" is vacuous.** No packet-clone path exists in the repository; every `Clone()` is
config/TLS/buffer cloning.

**Shadowsocks steady-state copy is a measured KEEP, not an oversight.** The read-side copy is real,
sits in a dependency, is allocation-free, and its cost is within noise. Closing it needs a
non-power-of-two capacity that `buf.Put` refuses (`alloc.go`'s switch pools only powers of two; the
in-place arm needs `length + Overhead`). The trade would be a benign memcpy for ~32 KiB of unpooled
garbage per read.

---

## 4. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Geometry poolability and no-realloc-after-Advance | `VERIFIED_UNIT` | `go test -count=1 ./common/bufgeom/...` |
| The allocation ceiling | `VERIFIED_UNIT` **in the `!race` build only** | same; the `race` half needs a cgo-capable host |
| Sniff ownership (borrowed cursor, retained cap, in-place unmask) | `VERIFIED_UNIT` + fuzz | `go test -count=1 ./common/sniff/...` |
| MASQUE atomic chunks, transferred prefix and substitution refusal | `VERIFIED_UNIT` | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...` |
| Batch-capability loss through a counter wrapper | `VERIFIED_UNIT` with a real red control | `route/udp_batch_capability_test.go` (3 tests) |
| The retirement walk reaches all four pools | `VERIFIED_UNIT` | `route/reference_coverage_stress_test.go`, `route/reusable_owner_inventory_test.go` |
| Race-freedom | **`BLOCKED_BY_ENVIRONMENT`** — `-race` needs cgo and there is no C compiler | Linux/macOS or Windows+gcc |
| The kTLS read-path fixes | **`VERIFIED_SOURCE`** — textually identical to the reference, and the package cannot be compiled here | needs Linux |
| `unix.Splice()` actually running | **`UNVERIFIED` here** — proved on Linux by the fork's `linux-splice-proof` workflow on real loopback sockets, not in this environment | that workflow |
| The splice silent stall | **FIXED in the sing pin** — the current root replacement resolves to `1729220c46c4`, whose `splice_linux.go` returns unhanded on `Pipe2` failure. `docs/internal/fork-splice-silent-stall.md` is the historical record. | inspect the exact pinned `common/bufio/splice_linux.go` and run its Linux regression tests |

---

## 5. AI cautions

1. **A borrowed cursor must not outlive `PeekStream`.** Do not cache a `payloadReader`.
2. **Do not pool the QUIC working copy.** It is a fresh per-call `make` for a reason.
3. **Do not retain a view into the caller's datagram past its release.**
4. **Do not drop the `Length < hdrLen+4` guard.**
5. **Do not unmask inside the caller's datagram.**
6. **Before removing any copy, find the rule that requires it.** The MASQUE substitution and the
   `takePacketBytes` view are two examples where the copy/aliasing boundary is the correctness
   condition, not an optimisation target.
7. **Do not touch the owned-datagram release semantics.** Ownership transferred; a release there is a
   double free.
8. **Do not "fix" a `PrependContextID` substitution in place.**
9. **Do not remove the producer-side headroom reservation.**
10. **Do not raise the sniff buffer size to "avoid" `io.ErrShortBuffer`.** That error is the bound.
11. **Do not assert `IdleConnectionKeeper` where only `IdleConnectionReleaser` is implemented.**
12. **Do not add a second `ReuseSuspect` pool.**
13. **Do not make a trim dial, wake suspended work, reset the network or terminate an active flow.**
14. **Do not build a census from `handed`.** It measures "the syscall path was selected".
15. **Do not close the Shadowsocks steady-state copy** without solving the non-power-of-two pool
    constraint; it is a deliberate measured keep.
16. **Do not assume `-race` covers the allocation ceilings.** They are `!race`-gated by design.
17. **Do not treat a platform-gated package as tested by an untagged run.** `common/ktls` is a
    data-plane path and its Linux/tagged tests need the right environment.
18. **Preserve the splice handover contract:** a failure before consuming payload must permit fallback;
    an already handed-over stream must not be replayed.
