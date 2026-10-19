# Anywhere round two — research, construction and verification

**Date:** 2026-10-19 · **Branch:** `testing` · **Status:** current for the delivered code head; the
audit report itself describes the tree at `ccc2812e45d`.
**Evidence labels:** each file states its own; nothing here is `VERIFIED_CI` unless it names the
workflow run for the exact SHA.

This directory is the archive of the second Anywhere round: an independent audit of
`NodePassProject/Anywhere` against this fork, followed by construction, adversarial debugging,
dependency-fork updates and CI verification. It is an archive entry, not a knowledge-base refresh and
not a new baseline.

## Files

| file | what it is | evidence |
|---|---|---|
| [`anywhere-second-round-research.md`](anywhere-second-round-research.md) | round-two audit: DNS cache ownership, TUN→QUIC data paths, QUIC ACK/retransmission, queues/budgets, large rule-set loading, A/B/C/D matrix | `VERIFIED_SOURCE` + `MEASURED_LOCAL`; device items `NOT_RUN` |
| [`anywhere-round2-construction.md`](anywhere-round2-construction.md) | what was implemented, where, with which commit, measured before/after, rollback | `MEASURED_LOCAL` + `VERIFIED_SOURCE` |
| [`anywhere-round2-debug-audit.md`](anywhere-round2-debug-audit.md) | Red→Green proofs, adversarial cases, cross-component combinations, race/platform limits | as above |
| [`anywhere-round2-final-summary.md`](anywhere-round2-final-summary.md) | one-page delivery: commits, dependency pins, results, CI, residual risks | as above |

## What was delivered

| item | change | result |
|---|---|---|
| A0 | hijacked UDP DNS parsed once; the parsed message crosses the goroutine boundary, the borrowed TUN payload does not | correctness fix + one fewer `Unpack` per query |
| A1 | `varbin.ReadSliceCount` geometric growth with a bounded first chunk (in the `sing` fork) | 1M-domain load allocation 42.5 → 20.6 MiB; GC 10 → 5 |
| B3 | SRS decompressed-work budget with a real-EOF probe (not a synthetic `io.LimitReader` EOF) | decompression bombs fail bounded, checksum/truncation semantics kept |
| B2 | MASQUE owned batches chunked at the transport's 32-frame queue | 64-packet bursts keep the batch path; refusal only rolls back its chunk |
| B1 | `tun.PacketBufferPort` gives the forward dispatcher a one-copy owned path; the MASQUE endpoint consumes the buffers unconditionally | the packet is copied once instead of twice; refusal/close release exactly once |
| B6 | QUIC/H3 receive queues clear popped slots | delivered payloads are no longer kept reachable by the backing array |
| B4 | over-provisioned sorted STREAM frames are right-sized | a 128 B frame no longer pins a 1452 B pool buffer |
| B5 | Hysteria2/TUIC use `SendDatagramOwned` | one fewer copy/allocation per datagram, release exactly once |
| B7 | received-DATAGRAM drop counter on `quic.Conn` | a shedding tunnel is distinguishable from an idle one |
| B8 | UDP NAT drop counter (`UDPNatDroppedPackets`) | queue-full and closed-session drops are visible |

Not landed: **B9** (Android mmap needs device evidence; not enabled blind). It is a platform
constraint, not a task for the reader.

## Dependency pins used by the delivered tree

```
sing      v0.9.6-0.20261019051552-baed8c3b4a34   (branch fix/splice-setup-failure-fallback-v2)
quic-go   v0.61.1-0.20261019061042-7e08ecaba383 (branch jiejie-masque-mod9)
sing-quic v0.7.2-0.20261019060901-d0deb0359c06  (branch jiejie-packet-ceiling)
sing-tun  v0.0.0-20261019070836-a7bd7facc7cc    (branch fix/listenconfig-ipv6-only-selfheal)
```

`test/go.mod` carries the same four pins; the nested-module integrity gate in `Verify` compares them.

## Verification

`Verify` on the delivered code head passed, including the nested-module integrity check, tagged and
untagged builds, the build matrix and both race steps. Android Core ARM64 and Mobile contract passed
on the parent code SHA whose source is identical apart from the test-module pins. The exact run links
are in the final summary §4. Local `-race` is `BLOCKED_BY_ENVIRONMENT` (no cgo toolchain); no device
`phys_footprint` or Android mmap claim is made.
