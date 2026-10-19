# Anywhere round two — final summary

**Date:** 2026-10-19 (Asia/Shanghai)
**Main repository:** `github.com/Piggy-Cat-bit-shadow/sing-box`, branch `testing` — no new branch was
created, nothing was force-pushed, no release/tag was made.
**Environment:** Windows 10 19044, Ryzen 7 4800H, Go 1.25.5 windows/amd64, `CGO_ENABLED=0`.
**Archive:** this file and the other round-two reports live in `KNOWLEDGE/reports/anywhere-round2/`.

---

## 1. Delivered commits and dependency versions

| Repository | Branch (existing) | Commit | Remote verified |
|---|---|---|---|
| `Piggy-Cat-bit-shadow/sing-box` | `testing` | see §1.1 (code head plus this archive commit) | `git ls-remote origin testing` |
| `Piggy-Cat-bit-shadow/sing` | `fix/splice-setup-failure-fallback-v2` | `baed8c3b4a3439360102ae2185a8a808ac841b6f` | `git ls-remote origin fix/splice-setup-failure-fallback-v2` |
| `Piggy-Cat-bit-shadow/quic-go` | `jiejie-masque-mod9` | `7e08ecab` (B4/B6/B7 on top of `1a730c584263`) | `git ls-remote origin jiejie-masque-mod9` |
| `Piggy-Cat-bit-shadow/sing-quic` | `jiejie-packet-ceiling` | `d0deb03` | `git ls-remote origin jiejie-packet-ceiling` |
| `Piggy-Cat-bit-shadow/sing-tun` | `fix/listenconfig-ipv6-only-selfheal` | `9c75994` | `git ls-remote origin fix/listenconfig-ipv6-only-selfheal` |

Effective dependency pins (all resolvable from the public module proxy):

```
replace github.com/sagernet/sing      => github.com/Piggy-Cat-bit-shadow/sing      v0.9.6-0.20261019051552-baed8c3b4a34
replace github.com/sagernet/quic-go   => github.com/Piggy-Cat-bit-shadow/quic-go   v0.61.1-0.20261019061042-7e08ecaba383
replace github.com/sagernet/sing-quic => github.com/Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261019060901-d0deb0359c06
replace github.com/sagernet/sing-tun  => github.com/Piggy-Cat-bit-shadow/sing-tun  v0.0.0-20261019061045-9c75994b2ef0
```

### 1.1 Main-repository commit sequence

1. `ea6e411c135` `fix(dns): preserve hijacked query ownership across async dispatch` (A0)
2. `e09a669067e` `fix(srs): bound decompressed work without weakening checksum verification` (B3)
3. `f1f9d28724f` `perf(masque): chunk owned datagram batches without ownership regression` (B2)
4. `4cb0f07e4ca` `perf(deps): pick up bounded varbin slice growth for large rule sets` (A1)
5. `7ecb24dfbed` `perf(deps): pick up datagram receive-slot release …` (B6)
6. `219f8cbf3fc` docs: construction/debug/audit evidence
7. `5c88bad7edf` `test(deps): keep the test module's fork pins in parity with the root`
8. `2a74686af57` `perf(deps): pick up owned datagram, frame right-sizing, drop counters and stream-frame retention fixes` (B4/B5/B7/B8)
9. this archive commit (reports moved to `KNOWLEDGE/reports/anywhere-round2/`)

Each is independently revertible; §10 of the construction record gives the order.

---

## 2. What changed

| Item | Result | Evidence |
|---|---|---|
| **A0** hijacked DNS ownership | Implemented; one parse; the parsed message, not the borrowed payload, crosses the goroutine boundary | Red→Green test; `route` suite (short 61.6 s, full 421.5 s) |
| **A1** counted-slice growth | Implemented in the `sing` fork; geometric growth with a 1024-element bounded first chunk | 1M-element probe: 6.0x → below 3x allocation; census below |
| **B3** decompression budget | Implemented; 128 MiB cap with a one-byte EOF probe and a dedicated error | Bomb/boundary/checksum tests; parser suite |
| **B2** owned-batch chunking | Implemented; >32-packet bursts keep the batch path in ≤32 chunks | 64-packet test, partial-refusal test, boundary table |
| **B6** QUIC receive-slot release | Implemented; popped payload slots are niiled | Retention tests fail before, pass after |
| **B4** small-frame right-sizing | Implemented; over-provisioned sorted frames are copied to exact size and the pooled buffer returned | Frame-sorter tests; MTU-sized frames still keep the pool buffer |
| **B5** Hysteria2/TUIC owned datagrams | Implemented; packed buffers are handed to `SendDatagramOwned`, released once by the transport | Ownership tests in both packages; fork build + main-repo protocol tests |
| **B7** received-DATAGRAM drop counter | Implemented; `Conn.DroppedDatagrams()` | Queue-full test |
| **B8** UDP NAT drop counter | Implemented; `UDPNatDroppedPackets()` counts queue-full and closed-session drops | Two counter tests in `sing-tun` |

**A1 census (same fixture, same machine; `TotalAlloc` during load, `live` after two GCs):**

| size | before | after | delta |
|---|---:|---:|---:|
| 10k domains load | 537.0 KiB | 331.9 KiB | −38.2% |
| 100k domains load | 4.1 MiB | 2.9 MiB | −29.3% |
| 1M domains load | 42.5 MiB | 20.6 MiB | **−51.5%** |
| 1M domains GC cycles | 10 | 5 | −50% |
| 1M domains live | 8.3 MiB | 7.4 MiB | −10.8% |
| 100k domains live | 882.4 KiB | 986.3 KiB | +11.8% (capacity slack) |

The 1M target of ≤15 MiB was **not reached** (20.6 MiB); pre-sizing to the declared count would reach
it only by giving up the forged-count safety property, which was not done.

**A0 cost removed:** one `dns.Msg.Unpack` per hijacked DNS packet — 182–188 ns / 40 B / 2 allocs
(A query) or 358 ns / 152 B / 5 allocs (EDNS0 AAAA) with the pinned `miekg/dns` v1.1.72.

**B4 arithmetic:** a 128-byte STREAM frame parsed into a 1452-byte pooled buffer pinned ~11x its
payload; the sorter now keeps an exact-size copy only when `cap > 2*len`, so MTU-sized frames are
untouched.

---

## 3. Debug result

| Area | Result |
|---|---|
| A0 red→green | Pre-fix data flow fails `the query ID must survive the payload being recycled`; fixed tree passes 5/5 hijack tests |
| A1 red→green | One-element growth allocates 50,072,624 B (6.0x) for a 1M-element slice; geometric growth is below 3x |
| B2 red→green | Pre-chunking session fails `64 packets must be submitted as two chunks of 32`; fixed tree passes |
| B3 adversarial | Bomb refused with the dedicated error; boundary enforced; corrupt checksum stays an integrity error; malformed counts fail cheaply |
| B4/B5/B7/B8 | Ownership/retention tests and red proofs recorded in the debug audit §10 |
| Package suites | dns, common/srs, route/rule, route -short, **route full 421.5 s**, `with_low_memory`, `with_quic` masque+http, hysteria2/tuic — all PASS |
| `-race` | `BLOCKED_BY_ENVIRONMENT` locally; covered by the remote Verify race steps |

Full detail: [`anywhere-round2-debug-audit.md`](anywhere-round2-debug-audit.md).

---

## 4. CI

<!-- FINAL_CI -->

---

## 5. Not implemented

**B1 (CONNECT-IP port-boundary ownership)** — needs a new ownership-carrying capability on `tun.Port`
plus a dispatcher that allocates the final owned buffer; the refusal path must be proven exactly-once
before it is acceptable. A wrong ownership transfer is worse than the copy it removes, so it was not
landed.

**B9 (Android rule-set mmap)** — no Android device or emulator in this environment; the mapping path
was not enabled blind for all Android production traffic.

Both are recorded as design constraints, not as tasks for the reader.

## 6. Residual unverified items

- A0's field trigger rate (the lifetime bug is proven, the frequency is not).
- B6/B7/B8 effects on a live connection: reachability/counters are proven, RSS deltas are not.
- B2's production trigger frequency (the deterministic loss above 32 is removed).
- B4's benefit under a real lossy peer (retained capacity is proven, an RSS delta is not).
- B5's release-exactly-once at a live QUIC connection (helper boundary + quic-go contract are proven).
- A1 on a real subscription (synthetic census fixture; allocation, not device footprint).
- iOS/Android `phys_footprint`, energy, Android mmap — no devices.
- `-race` locally — delegated to CI.

## 7. Documents (archived in this directory)

- [`anywhere-second-round-research.md`](anywhere-second-round-research.md) — round-two audit.
- [`anywhere-round2-construction.md`](anywhere-round2-construction.md) — construction matrix.
- [`anywhere-round2-debug-audit.md`](anywhere-round2-debug-audit.md) — adversarial/debug record.
- This summary.
