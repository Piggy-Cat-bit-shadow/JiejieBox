# Anywhere round two — construction record

**Repository:** `github.com/Piggy-Cat-bit-shadow/sing-box`, branch `testing`, delivered directly on it.
**Round-two audit:** [`anywhere-second-round-research.md`](anywhere-second-round-research.md) (archived in this directory).
**Anywhere reference:** `89905e0e3641bcbd958a66fcb6f027a2bdecec90` (unchanged).
**Audit baseline:** `ccc2812e45d8f5fbbca3bc7af14b91bc0ba92a36`; external docs-only commits moved the
branch to `45078a78b1d` before construction started. Construction commits below are on top of that.
**Environment:** Windows 10 19044, Ryzen 7 4800H, Go 1.25.5 windows/amd64, `CGO_ENABLED=0`.

This record states, per item, what was implemented, where, with which commit, and what was measured.
Nothing is marked PASS without a command that ran in this environment or a link to a CI run for the
exact commit.

---

## 1. Construction matrix

| ID | Item | Status | Commit(s) |
|---|---|---|---|
| A0 | Hijacked DNS query ownership across async dispatch | **IMPLEMENTED / VALIDATED** | `ea6e411c135` |
| A1 | Bounded counted-slice growth for large rule sets | **IMPLEMENTED / VALIDATED** (dependency + bump) | `sing@baed8c3b4a34` → main `4cb0f07e4ca` |
| B3 | SRS decompressed-work budget | **IMPLEMENTED / VALIDATED** | `e09a669067e` |
| B2 | MASQUE owned-batch chunking above 32 datagrams | **IMPLEMENTED / VALIDATED** | `f1f9d28724f` |
| B6 | QUIC delivered-datagram slot release | **IMPLEMENTED / VALIDATED** (dependency + bump) | `quic-go@1a730c584263` → main `7ecb24dfbed` |
| B1 | CONNECT-IP second copy across the port boundary | **IMPLEMENTED / VALIDATED** | `sing-tun@a7bd7fa` → main `3eec16cc3b9` |
| B4 | QUIC receive small-frame pinning | **IMPLEMENTED / VALIDATED** | `quic-go@7e08ecab` → main `2a74686af57` |
| B5 | Hysteria2/TUIC owned datagram send | **IMPLEMENTED / VALIDATED** | `sing-quic@d0deb03` → main `2a74686af57` |
| B7 | QUIC received-DATAGRAM drop counter | **IMPLEMENTED / VALIDATED** | `quic-go@7e08ecab` → main `2a74686af57` |
| B8 | UDP NAT drop counter | **IMPLEMENTED / VALIDATED** | `sing-tun@9c75994` → main `2a74686af57` |
| B9 | Android rule-set mmap | **NOT IMPLEMENTED — no device; not enabled blind** | — |

`DEFERRED` here means the change was not started, not that it was rejected. Only B9 remains deferred.

---

## 2. A0 — hijacked DNS query ownership

**Problem.** `route/dns.go` parsed the packet on the caller's goroutine and then handed the borrowed
`payload` to a goroutine which parsed it again. The payload is a view into a sing-tun read slot that
is reset on the next burst (`sing-tun/stack_go_io_windows.go:291`). The function's own comment
described the correct lifetime rule; the body did not follow it.

**Change.** `HijackDNSPacket` parses once and passes `*mDNS.Msg` to `dispatchDNSPacket`; the second
`Unpack` is gone. A test-only barrier (`testHookDNSPacketDispatch`, an `atomic.Pointer[func()]`,
nil in production) lets a test park the dispatch goroutine, recycle the payload, and prove the parsed
message crossed the boundary.

**Files.** `route/dns.go`, `route/dns_hijack_ownership_test.go`.

**Red → Green.**

```
# pre-fix data flow re-instated temporarily (payload passed to the goroutine, re-parsed after the hook)
--- FAIL: TestDNSPacketHijackOwnsTheQueryAcrossPayloadReuse
    the query ID must survive the payload being recycled

# committed tree
ok  github.com/sagernet/sing-box/route
--- PASS: TestDNSPacketHijackOwnsTheQueryAcrossPayloadReuse
--- PASS: TestDNSPacketHijackParsesEachQueryBeforeTheNextReuse
--- PASS: TestDNSPacketHijackDoesNotBlockPacketLoop
--- PASS: TestDNSPacketHijackRejectsMalformedPayload
--- PASS: TestDNSPacketHijackConcurrencyIsBounded
```

**Cost.** One `dns.Msg.Unpack` per hijacked packet saved: measured with the pinned `miekg/dns`
v1.1.72 at 182–188 ns / 40 B / 2 allocs for an A query, 358 ns / 152 B / 5 allocs for EDNS0 AAAA.
The first parse was already paid; the duplicate and the borrowed read are what changed.

**Invariants kept.** `dnsHijackConcurrency` 256, the CAS loop, context/metadata/writer, the
generation and cache semantics of the DNS router, and the response path (truncation still uses the
same `*dns.Msg`).

**Rollback.** Revert `ea6e411c135`. The old code is unsafe; a rollback would need a `bytes.Clone`
of the payload as a stop-gap, which was deliberately not chosen (extra allocation and still two
parses).

---

## 3. A1 — counted-slice growth

**Problem.** `sing/common/varbin/slices.go:ReadSliceCount` grew the result by ONE element per
iteration (`slices.Grow(result, 1)`). For a large declared count the slice is copied on every step;
measured 6.0x the final size in total allocation for a 1 MiB-element `uint64` slice.

**Change (dependency fork `Piggy-Cat-bit-shadow/sing`, branch `fix/splice-setup-failure-fallback-v2`).**
Geometric growth with a bounded first chunk:

```go
const maxInitialChunk = 1024
...
if len(result) == cap(result) {
    growth := uint64(cap(result))
    if growth < maxInitialChunk { growth = maxInitialChunk }
    growth = min(growth, count-uint64(len(result)))
    result = slices.Grow(result, int(growth))
}
```

The count is attacker-controlled, so the first allocation stays at 1024 elements; a forged count
fails on the first short read instead of pre-allocating the claimed size. The test pins both
properties.

**Red → Green (fork).**

```
# one-element growth
--- FAIL: TestReadSliceCountAllocatesGeometrically
    decoding 1048576 elements allocated 50072624 bytes (6.0x the final 8388608), want under 3x

# geometric growth
--- PASS: TestReadSliceCountAllocatesGeometrically   (below 3x)
--- PASS: TestReadSliceCountDoesNotPreallocateAForgedCount
--- PASS: TestReadSliceCountSmallCounts
ok  github.com/sagernet/sing/common/varbin
ok  github.com/sagernet/sing/common/domain
```

**Main-repository measurement (same fixture, same machine, before → after the dependency bump).**

| fixture | metric | before | after | change |
|---|---|---:|---:|---:|
| 1k domains | load allocation | 125.3 KiB | 106.5 KiB | −15.0% |
| 10k domains | live heap | 119.0 KiB | 105.0 KiB | −11.8% |
| 10k domains | load allocation | 537.0 KiB | 331.9 KiB | **−38.2%** |
| 10k domains | decode stage | 518.1 KiB | 313.1 KiB | **−39.6%** |
| 100k domains | live heap | 882.4 KiB | 986.3 KiB | +11.8% (capacity slack) |
| 100k domains | load allocation | 4.1 MiB | 2.9 MiB | **−29.3%** |
| 1M domains | live heap | 8.3 MiB | 7.4 MiB | −10.8% |
| 1M domains | load allocation | 42.5 MiB | 20.6 MiB | **−51.5%** |
| 1M domains | GC cycles during load | 10 | 5 | −50% |
| 1M domains | GC pause total | 691.1 µs | not reported (0) | — |
| 100k CIDR | load allocation | 7.8 MiB | 7.8 MiB | unchanged |

The 100k live-heap increase is real capacity slack: geometric growth can leave up to ~1.25x the
length as capacity, and the census measures the retained slice after the load. It is a ~104 KiB
cost against a ~1.2 MiB transient reduction at the same size; the 1M case, which matters for the
memory-limited target, moves the other way.

The round-two target of ≤15 MiB for 1M was **not reached** (20.6 MiB). Reaching it would require
pre-sizing to the declared count, which trades the forged-count safety property for memory. That
trade is not made here.

**Rollback.** Revert the main-repo bump `4cb0f07e4ca` (the fork commit can stay; it is additive).

---

## 4. B3 — SRS decompressed-work budget

**Problem.** `srs.Read` must drain the decompressed stream to its natural end to make zlib verify
the Adler-32 trailer, but nothing bounded the work: 254.8 KiB of highly compressible bytes expanded
to 256 MiB and cost 143.6 ms of a core, and declared rule data allocates ~6.5x its decompressed size.

**Change.** A counting reader between zlib and `bufio`, a dedicated `ErrDecompressedTooLarge`, and a
one-byte probe at the limit so budget exhaustion cannot be mistaken for a natural EOF:

```go
var decompressedSizeLimit int64 = 128 << 20   // test-lowerable, never changed in production
type decompressedBudgetReader struct { reader io.Reader; remaining int64 }
// at remaining == 0: probe one byte; n == 0 is the real EOF, any byte is the refusal
```

`compressReader.Close()` is now deferred (it does not verify the checksum; only reaching the real
end does). The trailer check, the truncation check, the HTTP-body drain and the single publish point
are untouched. A budget refusal is returned before `publishRules`, so the old generation stays live.

**Why 128 MiB.** A one-million-domain `.srs` decompresses to about 7 MiB and its live matcher is
about 8 MiB, so 128 MiB leaves more than an order of magnitude of headroom for long-name and
IP-heavy profiles while bounding a bomb to ~75 ms instead of unbounded work.

**Tests.** `common/srs/decompression_budget_test.go`:

- `TestReadRejectsADecompressionBomb` — 8 MiB zero tail, 1 MiB test budget: `ErrDecompressedTooLarge`,
  not reported as a checksum failure, allocation below 4 MiB.
- `TestReadEnforcesTheDecompressedBudgetAtTheBoundary` — a legal stream under the budget parses; a
  legal-shaped stream past it is refused.
- `TestReadKeepsBudgetRefusalDistinctFromChecksumFailure` — corrupt Adler-32 stays an integrity
  error, not a budget error.
- Existing `TestReadRejectsTruncatedStream`, `TestReadRejectsCorruptChecksum`,
  `TestReadAllowsTrailingBytesAfterStream`, `TestReadMalformed/huge_*` still pass.

```
ok  github.com/sagernet/sing-box/common/srs   0.108s
ok  github.com/sagernet/sing-box/route/rule   2.175s
```

**Rollback.** Revert `e09a669067e`; the previous parser has no budget.

---

## 5. B2 — owned-batch chunking

**Problem.** A TUN read burst can be 64 packets, but the owned DATAGRAM queue accepts 32 per call
and rejects a larger group outright. `session.writeBatchOwned` submitted the whole burst, so every
burst above 32 lost the batch path and resent every packet through the per-packet loop.

**Change.** `writeBatchOwned` now returns the number of leading buffers transferred and submits
chunks of at most `maxOwnedDatagramBatch = 32`; `writePackets` continues at `buffers[consumed:]`.
Each chunk is all-or-nothing: a refusal only restores that chunk's context IDs, and the caller's
slice is updated in place if `PrependContextID` had to substitute a buffer.

**Tests.** `transport/masque/batch_chunk_test.go` (with a capacity-limiting wrapper around the real
owned sender):

- 64 packets → exactly two batch calls of 32/32, no per-packet sends, one context ID per packet.
- Refusal of the second chunk → first 32 stay transferred, only the remaining 32 fall back, all 64
  payloads have the IPv4 version nibble at offset 1 (no double prefix).
- Boundary sizes 2/31/32/33/64/65/128 → minimum chunk count, no fallback.
- First-chunk refusal → whole group falls back, nothing counted as accepted.

**Red → Green.**

```
# pre-chunking session.go, expect FAIL
    Messages: 64 packets must be submitted as two chunks of 32
--- FAIL: TestWriteBatchChunksABurstAboveTheQueueCapacity

# committed tree
ok  github.com/sagernet/sing-box/transport/masque
```

All pre-existing batch tests (refusal layout, double release, PTB classification, byte order) pass
unchanged.

**Rollback.** Revert `f1f9d28724f`.

---

## 6. B6 — delivered DATAGRAM slot release

**Problem.** `quic-go/datagram_queue.go` and `http3/state_tracking_stream.go` advanced the receive
queue with `queue = queue[1:]` without clearing the popped slot. The slice header moves, but the
array still holds the pointer and the GC scans the whole array, so every delivered payload stayed
reachable until reallocation. Bounded (128 and 32 entries), but it makes a drained connection's heap
look like a full one.

**Change (dependency fork `Piggy-Cat-bit-shadow/quic-go`, branch `jiejie-masque-mod9`).**
`queue[0] = nil` before the reslice, in both call sites.

**Tests.** `datagram_queue_retention_test.go`, `http3/state_tracking_stream_retention_test.go`: keep
a view of the original array, deliver one datagram, assert the popped slot is nil.

**Red → Green.**

```
# pre-fix
--- FAIL: TestDatagramQueueClearsDeliveredPayloadSlot
    the delivered datagram is still reachable through the queue's backing array
--- FAIL: TestStateTrackingStreamClearsDeliveredPayloadSlot

# committed tree
ok  github.com/sagernet/quic-go
ok  github.com/sagernet/quic-go/http3
```

**Rollback.** Revert the main-repo bump `7ecb24dfbed`.

---

## 7. Follow-up pass: B1, B4, B5, B7, B8

These five were delivered after the first push, in the same round.

**B1 — owned packets across the port boundary (`sing-tun@a7bd7fa` + main `3eec16cc3b9`).**
`tun.PacketBufferPort` is an optional `Port` capability: `PacketHeadroom() int` plus
`WritePacketBuffers([]*buf.Buffer) error`, whose contract is that ownership moves **unconditionally**
before it returns. The forward stage allocates one headroomed buffer per packet and stages it, so the
packet is copied once instead of once into the stage and again into the endpoint. `Flush` hands the
buffers over and never touches them again; `Close` releases anything still staged. The release and
the port flush run under the same stage lock, so a close cannot free a batch a flush is handing over
(`a7bd7fa`). Tests in
`sing-tun` cover owned staging with headroom and byte equality, refusal (the port consumes and
releases exactly once), the unchanged borrowed path for a plain port, and close-time release. The
main repository asserts `ClientEndpoint` implements `tun.PacketBufferPort` at compile time.

**B4 — right-size over-provisioned sorted frames (`quic-go@7e08ecab`).** `frameSorter.push` now copies
a frame to an exact-size slice when `cap(Data) > 2*len(Data)`, and releases the pooled frame through
its `DoneCb`; MTU-sized frames keep their pooled buffer and pay nothing. Tests:
`TestFrameSorterRightSizesOverProvisionedFrames` (128 B frame in a 1452 B buffer → cap == len, bytes
intact, `DoneCb` once) and `TestFrameSorterKeepsThePooledBufferForAFullSizeFrame`. Red proof: with the
pre-fix sorter the first test fails because the queued frame keeps cap 1452.

**B5 — Hysteria2/TUIC owned datagrams (`sing-quic@d0deb03`).** Both `writePacket` paths now hand the
packed buffer to `SendDatagramOwned`; the buffer is the `DatagramOwner`, so the transport releases it
exactly once after serialisation, and the helper releases only on a refused send. Tests:
`TestSendOwnedDatagramTransfersOwnershipOnSuccess` (no release by the helper) and
`TestSendOwnedDatagramReleasesOnError` (exactly one release) in both packages. The fork builds against
the fork quic-go; the main-repo `protocol/hysteria2` and `protocol/tuic` tests pass with the bump.

**B7 — received-DATAGRAM drop counter (`quic-go@7e08ecab`).** `datagramQueue` increments an atomic
counter only on the drop path, and `Conn.DroppedDatagrams()` exposes it. Test:
`TestDatagramQueueCountsReceiveDrops` fills the 128-entry queue and asserts one counted drop while
the queue stays at its bound.

**B8 — UDP NAT drop counter (`sing-tun@9c75994`).** `enqueue` counts both the queue-full and the
closed-session drop in `udpNatDroppedPackets`, exposed as `UDPNatDroppedPackets()`. Tests:
`TestUDPNatCountsQueueFullDrop` and `TestUDPNatCountsClosedSessionDrop`.

`go test -tags with_quic ./protocol/masque` (60.3 s) passes with the B1 endpoint and the new
sing-tun; `go test ./route -short` (61.8 s) and the transport suites pass unchanged.

## 8. Item not implemented, with reason

**B9 (Android mmap).** No Android device or emulator in this environment. The instruction forbids
silently enabling a new mapping path for all Android production traffic without evidence, and
`GOOS=android` compilation alone does not prove temp-directory, freeze/thaw or page-fault
behaviour.

---

## 9. Cross-cutting checks performed

- `gofmt -l` clean on every touched file; `go vet` on the touched packages.
- `go mod verify` → all modules verified; every dependency bump changed only its `replace` line and
  its `go.sum` pair (root and `test/` kept in parity).
- The fork commits were pushed **before** the main repository was updated, and the main repository
  resolves them through the public module proxy (`go list -m` returned the pseudo-versions quoted
  above), so a clean machine can build the bumped tree.
- `test/go.mod` is kept in parity with the root's fork pins, because the nested module integrity gate
  compares them. The first CI run on `219f8cb` failed exactly there; `5c88bad` updates the four
  `test/go.mod`/`test/go.sum` lines and Verify is green on it.
- `go test ./dns ./common/srs ./route/rule`, `go test ./route -short`, `go test ./route` (full),
  `go test -tags with_low_memory ./route/rule ./dns`,
  `go test -tags with_quic ./transport/masque ./transport/http`,
  `go test -tags with_quic ./protocol/masque` (60.3 s),
  `go test ./protocol/hysteria2/... ./protocol/tuic/...` — results in
  [`anywhere-round2-debug-audit.md`](anywhere-round2-debug-audit.md).
- Low-memory tag: the changed code paths are not geometry-specific (DNS dispatch, SRS parser,
  MASQUE batching, QUIC queue clearing), and the package tests are run under `with_low_memory`
  where the package is geometry-sensitive.

## 10. Rollback order

1. `3eec16cc3b9` (B1 endpoint + sing-tun bump) — independent.
2. `2a74686af57` (B4/B5/B7/B8 dependency bump) — independent.
3. `7ecb24dfbed` (B6 quic-go bump) — independent.
4. `4cb0f07e4ca` (A1 sing bump) — independent.
5. `f1f9d28724f` (B2) — independent.
6. `e09a669067e` (B3) — independent.
7. `ea6e411c135` (A0) — revert only with a `bytes.Clone` stop-gap, because the old code reads
   recycled memory.

Each commit builds on its own; no commit depends on a later one. The fork commits are additive and
can be reverted by restoring the previous `replace` pins.
