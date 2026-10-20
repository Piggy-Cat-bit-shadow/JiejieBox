# Anywhere round three — independent deep audit

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` (no branch was created; nothing was force-pushed; no tag or release was made)
**Audit snapshot:** `5064f20a97880d3d5cfe1624c7f79da8d3b03b5c` (`Update README.md`), working tree clean apart from
the pre-existing submodule pointers `clients/apple` and `clients/desktop`, which this round did not touch.
**Status:** a point-in-time audit record. It is **not** a knowledge-base refresh and it does **not** move
`KNOWLEDGE/BASELINE.md`.

## Why this round exists

Rounds one and two studied `NodePassProject/Anywhere` and turned the findings into delivered code
(`KNOWLEDGE/reports/anywhere-round2/`). Round three asks a different question: **which of the mechanisms
that look attractive on paper actually survive contact with this fork's real data paths, and which ones
must be rejected before anyone writes code?**

The answer this round is deliberately lopsided. Of the four candidates the brief ranked highest, **three
are falsified by measurement on this host** and only their by-products are worth building. Most of the
value is in the negative results, in two C/D reclassifications of round-two assumptions, and in seven
findings that were not in the brief at all.

## Files

| file | what it is | evidence |
|---|---|---|
| [`anywhere-round3-independent-deep-audit.md`](anywhere-round3-independent-deep-audit.md) | the round-three audit: 18 sections, measured microbenchmarks, falsifications, A/B/C/D/R matrix, next-round blueprint and debug matrix | `MEASURED_LOCAL` (Go 1.25.5, Windows 10 19044, Ryzen 7 4800H) + `VERIFIED_SOURCE`; every device item `NOT_RUN` |

## The one-page result

| Verdict | Item | Why |
|---|---|---|
| **A — build next round** | `portReturn.ReturnPackets` allocates a grouping slice on every call, and `protocol/bridge` calls it **once per packet** | real-code harness: 2 allocs / 72 B for the one-packet shape (236 ns), 8 allocs / 3 480 B for 64 packets; both allocations are the grouping itself |
| **A — build next round** | MASQUE L4 CONNECT-UDP still copies every DATAGRAM payload, while CONNECT-IP has the owned path | `transport/http/capsule.go:572-586,978-1005` (copying `SendDatagram`) vs `client_h3.go:511-552`; the batch writer at `:954-971` already amortises the lock but not the copy, and `FrontHeadroom()` = 6 where owned needs 9 |
| **B — measure first** | `ForwardStage.ports` is append-only for the stage's lifetime, so the linear scan is O(cumulative outbound count), not O(per-batch) | measured 6.4 ns (K=1) → 31.5 ns (K=64) → 85 ns (K=256) per staged packet; production K unknown |
| **B — measure first** | `domain_keyword` is a bare `strings.Contains` loop | measured 9.9 ns at 1 keyword → 11.3 µs at 1 024; Aho–Corasick would be ~100× at K≥128 but typical K is unknown |
| **D — reject** | TCP `PacketTemplate` (precomputed header + partial checksums) | measured ceiling is 62.4 → 31.9 ns/segment; realistic partial template saves 8.7 ns, i.e. **0.55 % of one core at 1 Gbps full-MTU** |
| **D — reject** | Replacing XHTTP `packetUplink.gather`'s 50 µs poll with timers/events | `time.Sleep(50µs)` actually sleeps **518 µs** on this host, so the loop already runs 1–2 iterations; a timer cannot beat that and Go timers share the granularity |
| **FALSE PREMISE** | Hysteria2/TUIC "missing batch" | `writePackets` is a *fragmentation* helper that receives one message in the normal path; there is no burst source to amortise |
| **FALSE PREMISE** | TCP receive coalescing/delayed delivery | the Go stack already has direct-to-app-buffer delivery, receive-chain, 25 ms delayed ACK and 256 KiB ACK coalescing |

## What this round did not do

No production code, test, dependency, configuration, CI file or existing knowledge-base document was
modified. The only writes are this directory and the index row in `KNOWLEDGE/reports/README.md`. There was
no device, no real WAN, no iOS/Android target and no `cgo`, so **no claim here is `VERIFIED_DEVICE`**, and
the local `-race` runner stays `BLOCKED_BY_ENVIRONMENT` exactly as in round two.
