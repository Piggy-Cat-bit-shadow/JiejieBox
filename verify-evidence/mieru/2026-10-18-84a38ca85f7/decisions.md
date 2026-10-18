# Mieru phase 2: measured decisions

Date: 2026-10-18
Tree: `testing` at `84a38ca85f7a46b3910147148dd349da20bb5c17`
Matrix: `summary.csv`, `comparison.csv`, raw cells in `raw/`.
Server for every cell: the official `apis/server` harness (`scripts/mieru-bench/server`), not the mita binary (no Windows build).
Both clients entered through a local SOCKS5 port and used the same echo targets.

## What the matrix says

ABBA order, 4 rounds per cell, 4 TCP connections x 64 MiB, 5000 UDP datagrams of 1400 B, 1000 request/response samples.
Ratios are target / official; > 1 is better for throughput, < 1 is better for latency and CPU.

| Scenario | TCP download | TCP upload | UDP echo | TCP p95 lat. | CPU/GiB down | CPU/GiB up |
| --- | --- | --- | --- | --- | --- | --- |
| tcp-low-default | **1.102** | 0.939 | **1.058** | 0.979 | 0.979 | **0.681** |
| udp-low-default | **1.032** | **1.160** | **1.474** | 0.996 | **0.894** | **0.824** |
| tcp-high-nowait | **1.070** | 0.988 | **1.076** | 1.011 | 1.021 | **0.740** |

- TCP download is 3-10% faster than the official client on this host; TCP upload is 6% slower in
  `tcp-low-default`, 1% slower in `tcp-high-nowait`, and 16% faster over the UDP transport.
- UDP echo is 6-8% faster over the TCP underlay and 47% faster over the UDP underlay. Both clients
  talk to the same server, so the difference is in the client adapter / local entry, not the server.
- P50/P95 latency is unchanged within 2%; the first-byte cell has one 1.49x P50 outlier
  (`tcp-high-nowait`) but its P95 is 1.02x, so it is not a stable regression.
- CPU/GiB is generally lower for the target, except `tcp-high-nowait` download (1.02x, inside noise).
- RSS is **not** an adapter comparison: the target process also contains the sing-box core and a mixed
  inbound (40-62 MiB here), while the official process is a dedicated client (19-39 MiB). It is
  recorded in `summary.csv` as product-level context only.

The 6% TCP upload gap is a real negative result, not hidden: it is consistent across the four
`tcp-low-default` rounds (target 398-415 MiB/s, official 422-453 MiB/s) and is the main open
performance item for the next debug round. CPU per GiB is 32% lower on that same cell, so it is a
throughput trade-off rather than a uniform regression.

## Experiments and decisions

### E1. UoT framing instead of the SDK's `PacketOverStreamTunnel` — KEEP

The adapter's frame codec was written in phase 1 and is measured here for the first time end to end.
Microbench (1200 B payload, `protocol/mieru/bench_test.go`, Windows/amd64): write 118.8 ns, 64 B,
1 alloc vs SDK 666.1 ns, 1280 B, 1 alloc; read 79.83 ns, 0 B, 0 alloc vs SDK 86.93 ns, 4 B, 2 alloc.
The UDP-echo cell over the UDP underlay is 1.474x the official client with the same server; the
TCP-underlay cells are 1.06-1.08x. This is an adapter-layer win, not a claim about Mieru's cipher.

### E2. Do not rebuild the SDK client before its first dial — KEEP, but not as a speedup

`InterfaceUpdated` is called once at startup, before any traffic. The old path stopped and started a
client that had no underlay or session. `Outbound.dialed` now records the first dial after each
(re)start, and the pre-dial update returns without touching the client; a network change after a
dial still rebuilds it.

A/B with 12 cold starts per side: startup medians 249-267 ms (baseline) vs 251-269 ms (after), first-byte
P50/P95 unchanged, idle CPU 0.086 s vs 0.085 s. There is **no measurable performance claim** here.
The change is kept because it removes a guaranteed no-op Start/Stop, removes a misleading
"restarted after network change" log, and closes the only window in which a first dial could wait on
a rebuild. It does not change the data path.

### E3. Per-packet scratch buffer instead of `buf.NewSize` — REJECT

`writeUOTFrame` is 118 ns and one 64 B allocation per datagram. The SDK's `Session.Write` copies the
payload synchronously into segment buffers, so a per-connection scratch buffer would be safe only if
writes were serialized; `net.PacketConn` permits concurrent writes, so a shared scratch would need a
lock. A lock per datagram costs more than the 64 B pooled allocation, and the profile does not show
the allocator as the UDP bottleneck. No code change.

### E4. Synthetic SDK resolver to skip its UDP pre-resolution — REJECT

The SDK calls `Resolver.ResolveUDPAddr` before opening the outer UDP socket and validates the source
against the returned address. The adapter intentionally ignores that address and dials the configured
name through the fork's dialer. Returning a synthetic address would remove a cached DNS lookup but
would fabricate a source address the SDK then treats as real; that is exactly the fake-DNS pattern
this fork's dialer design forbids. The fork's DNS cache already absorbs the lookup. Keep the honest
bridge.

### E5. CPU profile during TCP download — no adapter hotspot, NO CHANGE

`profiles/cpu-tcp-download.pprof` is a 6 s profile of the target during an 8 GiB download
(4 x 2 GiB, 398.8 MiB/s). Flat/cumulative: SDK `StreamUnderlay.readOneSegment` 36.4% (of which
Windows socket syscalls 31.5%), route `connectionCopy`/`copyExtendedWithPool` 23.1%, GC 24.3%,
ChaCha20-Poly1305 12.1%. The adapter's own code does not appear above the 0.09 s sample threshold.
The remaining work is in the SDK's segment loop and the shared copy pipeline; changing either is not
a Mieru adapter change. No speculative pool/goroutine work.

`profiles/heap-tcp-download.pprof` is the in-use heap after a 4 GiB / 10 s download at 408.3 MiB/s:
`StreamUnderlay.readDataAckSegment` 35.6%, SDK replay cache 20.3%, ChaCha20 buffers 10.2%, session
input loop 9.9%, config decoding 9.9%. Again no adapter allocation site; the largest single buffer is
the SDK's replay cache. This is why E3 (a scratch buffer) would not move the heap or the UDP pps.

### E6. Reference implementations — one of five ran, four are BLOCKED_BY_ENVIRONMENT

**Phase-3 correction:** the phase-2 claim that mita has no Windows build was wrong. The
official `cmd/mita` v3.38.1 source builds on Windows and runs in foreground with
`MITA_INSECURE_UDS=1` and `C:\var\run\mita`; official client and target both reached it
TCP+UDP. The 4-round real-mita matrix and the negative first-byte/upload results are in
[`phase3-debug/mita`](phase3-debug/mita/README.md). The historical E6 table below is kept to
show what the phase-2 run could not do; do not cite it as the current blocker list.

REF-E, Mihomo v1.19.32 (Mieru SDK **v3.38.0**, product-version comparison only), ran on the same
official server and the same harness: `references/mihomo/summary.csv`, `comparison.csv`, 4 rounds per
cell. Target/Mihomo throughput ratios:

| Scenario | TCP download | TCP upload | UDP echo |
| --- | ---: | ---: | ---: |
| tcp-low-default | 1.066 | 0.953 | 1.018 |
| udp-low-default | 1.026 | 1.274 | 1.049 |
| tcp-high-nowait | 0.945 | 0.851 | 0.933 |

The target is not uniformly fastest: Mihomo wins the high-mux TCP cells (especially upload), the
target wins the low-mux TCP download and the UDP-transport upload, and both are close on UDP echo.
Mihomo ships a different SDK (v3.38.0), so this is a product result and cannot be folded into the
unified-SDK ratios above. It independently confirms the target's TCP-upload weakness: the official
client and Mihomo both beat it on `tcp-low-default` upload.

| Ref | Reason |
| --- | --- |
| REF-A official mieru -> official mita | mita has no Windows build; this host has no WSL distribution and no Docker, so a real Linux mita could not run. |
| REF-B mbox `1c5b3971` | Android application, no Windows CLI. |
| REF-C extended `5956780b` | full fork, no tested Windows Mieru CLI artifact here. |
| REF-D Hiddify `3bbbd61e` | GUI application; no scriptable Mieru-only Windows entry without its UI/core packaging. |

These are not PASS and are not folded into the ratios above. The official client (REF-A client half),
the official server SDK (the server half mita uses) and Mihomo (REF-E) are the real reference points
that could run here. A Linux host with the real mita binary and the remaining apps is the next step.

## Architecture feedback

- **Nothing Mieru-specific was pushed into the shared core.** The only shared
  change in this round is the phase-1 `common/listener` option-path extraction;
  the routing, DNS, dialer, power and copy layers are untouched.
- **The TCP slow path is shared, not Mieru.** The profile puts 23% in
  `route.connectionCopy` / `copyExtendedWithPool`; that is the same path every
  stream outbound uses. Adding a Mieru-only copy buffer would create a second
  ownership model for no measured gain, so it was rejected.
- **The UDP pre-resolution is an SDK constraint, not a fork abstraction.** The
  SDK requires a resolver answer before it opens the outer UDP socket and
  validates the source against it. Generalising that into a public
  "pre-resolve" hook would invite fake DNS answers; the adapter-local bridge is
  the narrowest correct boundary.
- **InterfaceUpdated ownership is the outbound's, not the route's.** The route
  broadcasts a network change; the Mieru outbound decides whether it has state
  to rebuild. Moving that decision into the route would make every outbound pay
  for Mieru's non-restartable client.
- **No metrics abstraction.** The shared connection manager already owns
  counters; a Mieru metrics surface would duplicate it. The SDK's internal
  metrics stay internal.

## Reproduction

`reproduction.md` has the exact build and script commands; `environment.md` has the host facts.
The raw cells are the primary evidence; `summary.csv` is a convenience projection, not a replacement.
