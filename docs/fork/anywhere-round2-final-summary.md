# Anywhere round two — final summary

**Date:** 2026-10-19 (Asia/Shanghai)
**Main repository:** `github.com/Piggy-Cat-bit-shadow/sing-box`, branch `testing` — no new branch was
created, nothing was force-pushed, no release/tag was made.
**Environment:** Windows 10 19044, Ryzen 7 4800H, Go 1.25.5 windows/amd64, `CGO_ENABLED=0`.

---

## 1. Delivered commits and dependency versions

| Repository | Branch (existing) | Commit | Remote verified |
|---|---|---|---|
| `Piggy-Cat-bit-shadow/sing-box` | `testing` | `5c88bad7edf3201ddbb1d688c90dbf7dc4495784` (code) + this docs commit | `git ls-remote origin testing` |
| `Piggy-Cat-bit-shadow/sing` | `fix/splice-setup-failure-fallback-v2` | `baed8c3b4a3439360102ae2185a8a808ac841b6f` | `git ls-remote origin fix/splice-setup-failure-fallback-v2` |
| `Piggy-Cat-bit-shadow/quic-go` | `jiejie-masque-mod9` | `1a730c584263649495e9001811e70b97c8c64bcf` | `git ls-remote origin jiejie-masque-mod9` |

Effective dependency pins after this round (all resolvable from the public module proxy):

```
replace github.com/sagernet/sing => github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261019051552-baed8c3b4a34
replace github.com/sagernet/quic-go => github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261019051807-1a730c584263
```

`sing-tun` and `sing-quic` are unchanged from the round-two audit pins.

Main-repository commit sequence (oldest first; each is independently revertible):

1. `ea6e411c135` `fix(dns): preserve hijacked query ownership across async dispatch`
2. `e09a669067e` `fix(srs): bound decompressed work without weakening checksum verification`
3. `f1f9d28724f` `perf(masque): chunk owned datagram batches without ownership regression`
4. `4cb0f07e4ca` `perf(deps): pick up bounded varbin slice growth for large rule sets`
5. `7ecb24dfbed` `perf(deps): pick up datagram receive-slot release …`
6. `219f8cbf3fc` `docs: record the Anywhere round-two construction, debug audit and evidence`
7. `5c88bad7edf` `test(deps): keep the test module's fork pins in parity with the root` (Verify green)
8. `4b5e9150f82` `docs: record the round-two CI result and final verification`
9. this docs update (final record)

---

## 2. What changed, and the measured effect

| Item | Result | Evidence |
|---|---|---|
| **A0** hijacked DNS ownership | Implemented; one parse; the parsed message crosses the goroutine boundary; borrowed payload never does | Red→Green test; `route` suite (61.6 s short, full suite separately) |
| **A1** counted-slice growth | Implemented in the `sing` fork; geometric growth with a 1024-element bounded first chunk | 1 MiB-element probe: 6.0x → below 3x allocation; main-repo census below |
| **B3** decompression budget | Implemented; 128 MiB decompressed cap with a one-byte EOF probe and a dedicated error | Bomb/boundary/checksum tests; parser suite |
| **B2** owned-batch chunking | Implemented; >32-packet bursts keep the batch path in ≤32 chunks | 64-packet test (2 chunks), partial-refusal test, boundary table |
| **B6** QUIC receive-slot release | Implemented in the `quic-go` fork; popped payload slots are niiled | Retention tests fail before, pass after |

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

---

## 3. Debug result

| Area | Result |
|---|---|
| A0 red→green | Pre-fix data flow fails `the query ID must survive the payload being recycled`; fixed tree passes 5/5 hijack tests |
| B3 adversarial | bomb refused with the dedicated error; boundary enforced; corrupt checksum stays an integrity error; malformed counts still fail cheaply |
| B2 adversarial | 2/31/32/33/64/65/128 chunking; partial refusal transfers only the tail; one context ID per payload; no double release |
| B6 red→green | Pre-fix retention test fails on both queues; fixed tree passes |
| Package suites | dns 7.9 s, common/srs 0.1 s, route/rule 2.2 s, route -short 61.6 s, **route full 421.5 s (includes the six-minute kernel give-up test)**, `with_low_memory` route/rule+dns, `with_quic` masque 6.8 s + http 25.2 s — all PASS |
| `-race` | `BLOCKED_BY_ENVIRONMENT` locally (no cgo toolchain); delegated to CI |
| Apple tag / device / Android | `BLOCKED_BY_ENVIRONMENT` / not available; no device claims made |

Full detail: [`anywhere-round2-debug-audit.md`](anywhere-round2-debug-audit.md).

---

## 4. CI

| SHA | Workflow | Result | Link |
|---|---|---|---|
| `5c88bad7edf` (final code SHA) | **Verify** | **success** — gofmt, nested module integrity, tagged/untagged builds, build matrix, **race on the fork's own data paths**, race on the configuration surface | https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39205333743 |
| `219f8cb` (code, before the test-module parity fix) | Android Core ARM64 | success | https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39204268770 |
| `219f8cb` | Mobile contract verification (Apple tags + low-memory) | success | https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39204268783 |

The first Verify attempt on `219f8cb` failed at the **nested Go module integrity** step
(https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39204268737): the root `go.mod`
pinned the new fork commits but `test/go.mod` still pinned the previous ones. `5c88bad` changes
exactly those four lines (`test/go.mod` + `test/go.sum`); Verify is green on it, including both race
steps. Android and Mobile are not path-triggered by `test/go.mod`, so their green runs are on
`219f8cb`, whose code is identical to the final SHA apart from those test-module pins.

Local `-race` remains `BLOCKED_BY_ENVIRONMENT` (no cgo toolchain on this host); the remote race
steps above are what cover the pushed SHA. Device `phys_footprint`, energy and Android mmap remain
`NOT_RUN` (no devices).

---

## 5. Not implemented, with reasons

B1 (CONNECT-IP port-boundary ownership), B4 (QUIC small-frame pinning), B5 (Hysteria2/TUIC owned
datagrams), B7/B8 (drop observability) and B9 (Android mmap) were deferred. The reasons are specific:
B1 and B5 need cross-repository ownership contracts that cannot be validated without a release-
exactly-once test harness; B4 needs a lossy-network or frame-sorter harness; B7/B8 need a metrics
surface decision; B9 needs an Android device and the instruction explicitly forbids enabling a new
mapping path for all Android production traffic without evidence. None of them is claimed as done,
and none blocks the delivered items.

## 6. Residual unverified items

- A0's field trigger rate (the lifetime bug is proven, the frequency is not).
- B6's RSS effect on a live connection (reachability proven, delta not measured).
- B2's production trigger frequency (the deterministic loss above 32 is removed).
- A1 on a real subscription (synthetic census fixture; allocation, not device footprint).
- iOS/Android `phys_footprint`, energy, Android mmap — no devices in this environment.
- `-race` locally — delegated to CI.

## 7. Documents

- [`anywhere-second-round-research.md`](anywhere-second-round-research.md) — round-two audit (now
  committed as evidence; its historical conclusions are unchanged).
- [`anywhere-round2-construction.md`](anywhere-round2-construction.md) — construction matrix.
- [`anywhere-round2-debug-audit.md`](anywhere-round2-debug-audit.md) — adversarial/debug record.
- This summary.
