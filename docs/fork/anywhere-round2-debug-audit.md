# Anywhere round two — debug and adversarial audit

Companion to [`anywhere-round2-construction.md`](anywhere-round2-construction.md), which lists what
changed and where. This document records how it was attacked, what failed before the fix, what was
re-checked across components, and what remains untested.

**Environment:** Windows 10 19044, Ryzen 7 4800H, Go 1.25.5 windows/amd64, `CGO_ENABLED=0`, no
bash, no Apple SDK, no Android device. `-race` is not executable locally; it is delegated to the
repository's CI workflow and reported separately in
[`anywhere-round2-final-summary.md`](anywhere-round2-final-summary.md).

---

## 1. Test matrix (commands actually run)

| # | Command | Result |
|---|---|---|
| 1 | `go test ./dns ./common/srs ./route/rule -count=1 -timeout 15m` | **PASS** — dns 7.885 s, srs 0.098 s, route/rule 2.189 s |
| 2 | `go test ./route -count=1 -short -timeout 15m` | **PASS** — 61.550 s (`-short` skips the deliberate six-minute kernel give-up test) |
| 3 | `go test -tags with_low_memory ./route/rule ./dns -count=1` | **PASS** — 2.176 s / 7.891 s |
| 4 | `go test -tags with_quic ./transport/masque ./transport/http -count=1 -timeout 20m` | **PASS** — 6.786 s / 25.159 s |
| 5 | `go test ./route -run 'TestDNSPacketHijack*' -count=1 -v` | **PASS** — 5 tests, including the two new ownership tests |
| 6 | `go test -tags with_quic ./transport/masque -run 'TestWriteBatch|TestBatch' -count=1 -v` | **PASS** — 4 new chunk tests + 10 pre-existing batch tests |
| 7 | `go test ./common/srs -count=1 -v` | **PASS** — 3 new budget tests + all pre-existing parser tests |
| 8 | `go test ./common/varbin/... ./common/domain/...` (sing fork) | **PASS** — 0.042 s / 0.017 s |
| 9 | `go test . ./http3/... -run 'ClearsDelivered'` (quic-go fork) | **PASS** |
| 10 | `gofmt -l` + `go vet` on every touched package | clean |

Full log: `%TEMP%\round2_tests.log` from the run that produced this table (not committed).

---

## 2. A0 — adversarial ownership tests

### 2.1 The deterministic barrier

The dispatch goroutine is parked by a test-only hook (`testHookDNSPacketDispatch`, an
`atomic.Pointer[func()]`, nil in production) **before** the query reaches the DNS router. The test
then zeroes the caller's payload — exactly what a reused TUN read slot does — releases the hook, and
inspects what the router received.

- **Pre-fix data flow** (payload passed into the goroutine and re-parsed after the hook):
  the goroutine unpacks the zeroed buffer and the router sees a different query.
  `TestDNSPacketHijackOwnsTheQueryAcrossPayloadReuse` fails with
  `the query ID must survive the payload being recycled`.
- **Committed tree:** ID, question name, question type and EDNS0 UDP size all survive; the in-flight
  counter returns to zero.

This is a construction-level red proof, not a test that passes on both implementations.

### 2.2 Pool-reuse shape

`TestDNSPacketHijackParsesEachQueryBeforeTheNextReuse` reuses one `[]byte` for two different queries
(`first.example. A`, then `second.example. HTTPS`) and asserts both reach the router with their own
name and ID. With the parse on the caller's goroutine the two are independent by construction; with
the old code the outcome was scheduling-dependent. This is a regression test, not a red proof.

### 2.3 What did not change

- Malformed payload still never reaches the router (`TestDNSPacketHijackRejectsMalformedPayload`).
- The 256 in-flight cap still saturates and drops the excess
  (`TestDNSPacketHijackConcurrencyIsBounded`).
- The packet loop still returns before the exchange completes
  (`TestDNSPacketHijackDoesNotBlockPacketLoop`).
- The packet-loop path, the `PacketConn` path and every other `NewDNSPacket` caller now all go
  through the same synchronous parse; the borrowed payload never crosses the goroutine boundary.

### 2.4 Residual risk

The test proves the message is independent of the payload. It cannot prove that no *other* borrowed
view is retained by a transport implementation. The DNS router's own contract (`dns.Client` copies
on store and on read) was re-checked in the round-two audit and is unchanged.

---

## 3. B3 — decompression budget attacks

| Attack | Expected | Result |
|---|---|---|
| Valid rule list + 8 MiB of zeros in the same zlib stream, 1 MiB test budget | refuse with `ErrDecompressedTooLarge`, allocate < 4 MiB | PASS |
| Legal-shaped stream under the budget (8000 keywords) | parse | PASS |
| Legal-shaped stream past the budget (12000 keywords) | refuse with the dedicated error | PASS |
| Corrupt Adler-32 trailer | integrity error, **not** a budget error | PASS |
| Truncated stream (existing) | `unexpected EOF`, no publish | PASS |
| Trailing bytes after the zlib stream (existing) | accepted | PASS |
| Forged huge counts (`TestReadMalformed/huge_*`) | cheap failure, no large allocation | PASS |

The load-bearing detail is the one-byte probe at the limit: a plain `io.LimitReader` would return a
synthetic EOF, the trailer check would "succeed" on a file it never finished reading, and a truncated
file could be published. The probe keeps "budget exhausted" and "real end of stream" distinguishable,
and the dedicated error keeps them distinguishable to the caller.

---

## 4. B2 — batch ownership attacks

The chunk tests use a wrapper around the real owned sender that refuses any group larger than 32
(the quic-go queue capacity) and can refuse after N accepted chunks.

| Case | Expected | Result |
|---|---|---|
| 64 packets, capacity 32 | two batch calls 32/32, zero per-packet sends | PASS |
| Second chunk refused | first 32 transferred, last 32 per-packet, 64 payloads total, one context ID each | PASS |
| First chunk refused | nothing transferred, all 40 per-packet | PASS |
| Sizes 2/31/32/33/64/65/128 | minimum chunk count, zero fallback | PASS |
| Pre-existing refusal layout test | every buffer byte-identical after a refusal | PASS |
| Pre-existing double-release test | a second independent batch after an accepted one still works | PASS |
| Pre-existing PTB classification tests | oversize fallback still produces PTB / hard error | PASS |

The ownership invariant exercised here is the one the audit demanded: the accepted prefix is never
replayed, the refused chunk is restored byte-for-byte (context ID undone), and the per-packet loop
sees only the remainder.

**Red → Green.** With the pre-chunking `session.go`, the 64-packet test fails at
`64 packets must be submitted as two chunks of 32` because the whole group is refused and falls back.

---

## 5. B6 — delivered-payload retention

`TestDatagramQueueClearsDeliveredPayloadSlot` and
`TestStateTrackingStreamClearsDeliveredPayloadSlot` keep a view of the queue's backing array, deliver
one payload, and assert the popped slot is nil. Both fail on the pre-fix tree with
`the delivered datagram is still reachable through the queue's backing array`.

Ownership is unchanged: the payload returned by `Receive` is still the caller's, and the queue still
never reuses a delivered slice. Only the stale pointer in the array is cleared.

---

## 6. Cross-component combinations

| Combination | What was checked | Result |
|---|---|---|
| A0 × DNS generation / cache / singleflight / power | `go test ./dns` (7.9 s) and `./route -short` (61.6 s) after the change | PASS |
| A1 × B3 × SRS streaming | large census after the dependency bump + all parser/budget tests + `route/rule` (2.2 s) | PASS |
| A1 × low-memory geometry | `-tags with_low_memory ./route/rule ./dns` | PASS |
| B2 × existing batch ownership | 10 pre-existing masque batch tests + 4 new chunk tests, same package | PASS |
| B2 × HTTP/3 tunnel path | `-tags with_quic ./transport/http` (25.2 s) | PASS |
| B6 × masque owned path | `-tags with_quic ./transport/masque` after the quic-go bump | PASS |
| Dependency bumps × module graph | `go mod verify`; only the two `replace` lines and two `go.sum` pairs changed | PASS |
| All × `go vet` / `gofmt` | touched packages | clean |

The combinations that the plan listed but that were **not** exercised because the corresponding item
was deferred: B1 × B2 × QUIC owned, B1 × DNS/ICMP/PTB, B5 × B6 × quic-go queues, B4 × ACK/flow
control, B9 × rule-set hot update. They remain open risks for the next round, not verified paths.

---

## 7. Race, platform and CI

- **`-race` locally:** `BLOCKED_BY_ENVIRONMENT` — `CGO_ENABLED=0` and no mingw-w64 gcc on this host.
  The A0 test is designed to be race-detector-friendly (the hook serialises the two goroutines), and
  the new ownership tests would fail under `-race` on the old code because of the unsynchronised
  payload access.
- **Apple tag build:** `BLOCKED_BY_ENVIRONMENT` on Windows (the canonical Apple set carries
  `tfogo_checklinkname0`/`badlinkname`, which the linker rejects on a non-Apple host).
- **Android / iOS devices:** not available. No `phys_footprint`, energy or Android mmap claim is
  made anywhere in this round.
- **CI:** the main repository's workflow on the pushed SHA is tracked in the final summary. Where a
  path filter does not trigger a required job, that is recorded rather than papered over with an
  older green run.

---

## 8. Real defects found by this round beyond the audit list

**A0 itself is the round-two audit's finding, not a new one.** This round's additional observations
while implementing it and the other items:

1. **The pre-fix code contradicts its own doc comment and the introducing commit message.** The
   comment says the parsed message is what crosses the goroutine boundary; the body passes the
   payload. That is recorded in the construction record as the root cause, and it is why the fix is
   a deletion rather than new machinery.
2. **The second parse was invisible to the existing tests** because both handed the function a
   test-owned, stable `[]byte`. The new tests use a recycled buffer; that is the missing coverage.
3. **The 100k live-heap increase after A1 is a genuine trade-off**, not a measurement error:
   geometric growth leaves up to ~1.25x capacity in the retained slice. It is documented in the
   construction record rather than hidden; at 1M the live heap and the transient both fall.
4. **The CIDR fixture is unchanged** by A1, which shows the transient is domain-matcher specific
   (the IP-set reader is not the same counted-slice path). This is a useful boundary for the next
   round: do not generalise the result to "all rule sets got 50% cheaper".
5. **`srs.Read` never closed the zlib reader**; it now does. The close does not verify the checksum,
   so the trailer check is unaffected.

No new correctness defect was found in the MASQUE or QUIC code while changing it. The pre-existing
ownership rules (all-or-nothing chunks, context-ID rollback, one release per buffer) held under the
new chunking, and the tests that pin them were extended rather than replaced.

---

## 9. Unreproduced / not established

- A0's field trigger rate under real TUN load: the test proves the lifetime bug, not how often a
  recycled slot is read before the old goroutine parses it.
- B6's measured heap effect on a live connection: the bound is arithmetic (~128 + 32 payloads per
  connection) and the test proves reachability, not an RSS delta.
- B2's trigger frequency in production: the chunking removes the deterministic loss above 32, but
  how often a single flow receives >32 packets in one burst was not instrumented.
- A1's effect on a real 1M-entry subscription: the census fixture is synthetic (generated domains),
  and the number is allocation, not device footprint.
- The deferred items' risks are listed in the construction record §7.
