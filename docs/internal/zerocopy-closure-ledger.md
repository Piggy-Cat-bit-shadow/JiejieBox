# Zero-copy objective — closure ledger

Branch `jijiebox/v0.1.6-hardening`, 9 commits, all pushed. Tree clean.
Companion to `ZEROCOPY_AUDIT_CLOSED.md` (mechanism detail); this file is the item-by-item verdict.

```
589e2dcc1  docs(ktls): state the client-only RX splice restriction where a user reads it
3790afd45  fix(ktls): kernel_rx on a server is inert for splice, not merely slow
9560f30aa  record the fork-side silent splice stall
e5e8f160d  trojan: pin the ClientConn replaceability invariant
55f6d3e07  v2rayhttp: stop unwrapping past HTTPConn while held body bytes exist
786f74e90  http: refuse buffered IO the connection cannot honour
714e6cf48  pin the guard's WRITE-side bypass surface
c02d7cc8e  splice counters measure acceptance, not a kernel splice
1b0cedf40  device-axis, sniff and capability fixes
```

---

## Item-by-item

### 1. Copy-backend census (decision vs FINAL backend) — **BLOCKED_EXTERNAL**

Levels 1–2 shipped. Levels 3–4 are not observable from sing-box, and this is proved rather than believed.

| level | observable from | state |
|---|---|---|
| `splice_eligible` | sing-box | shipped — the userspace wrapper walks |
| `handover_accepted` | sing-box | shipped — `c02d7cc8e`, rendered `handover_accepted` |
| `copy_direct_selected` | **fork only** | `copyDirect`'s `handled`, consumed inside the fork |
| `actual_kernel_splice_observed` | **fork only** | `splice()`'s `n > 0`, never leaves the package |

The seam is four lines of fork control flow sing-box never sees, and a census built from `handed` would be
wrong in the direction that flatters the change — see `ZEROCOPY_AUDIT_CLOSED.md` §1a. Needs a fork change; the
fork is a read-only module cache on this host.

### 2. Kernel splice actually reaching `unix.Splice()` — **BLOCKED_EXTERNAL (no Linux execution)**

Mechanism verified from source: `splice_linux.go:32,39` call `unix.Splice` twice (socket→pipe→socket),
`SPLICE_F_MOVE` on the write side, `maxSpliceSize = 1<<20`, gated `//go:build linux`; `splice_stub.go` returns
`(false, 0)` off Linux, so on Windows and darwin this is structurally zero.

**Linux execution is confirmed unavailable on this host**, not assumed: `wsl --status` exits 50,
`wsl -e /bin/echo` fails, and no WSL distro package is installed. Cross-compilation works and is used for
build verification; running is not possible.

### 2a. Capability preservation on the splice path — verified

Three facts that decide whether the kernel splice can engage at all, each checked rather than assumed:

* **`copyDirect` is tried BEFORE `copyExtended`.** `CopyWithCounters` calls `copyDirect` first and only
  falls back when it returns `handed == false`. So a read waiter on either end does not preempt the syscall
  path — the ordering is the safe one.
* **`ktls.Conn` is the only implementer of `SyscallReader`/`SyscallWriter` in sing-box**, and the interface
  couples `SyscallConnForRead()` with `HandleSyscallReadError(err)` in ONE contract. That coupling is what
  makes the recoverable-error path possible: a type cannot hand out a raw fd without also saying how to
  handle the non-application-data record that arrives on it. No partial implementation is expressible.
* **A raw connection without kTLS falls back explicitly.** `splice` reaches
  `else if !notFirstTime && E.IsMulti(err, unix.EINVAL, unix.ENOSYS) { handed = false; return }`
  (`splice_linux.go:68-70`), so a `syscall.Conn`-only source that cannot splice yields to the userspace copy
  on the FIRST attempt. The nil-`sourceReader` case is guarded at `:55` before any method call.

`kTLSClientConfig.ClientHandshake` returns the kTLS conn directly and nothing wraps it afterwards, so the
capability is not lost between the TLS layer and the copy engine.

### 3. Wrapper capability preservation — **DONE**

Five defects fixed, each with a red-first control:

| where | defect | consequence |
|---|---|---|
| `route/conn.go` | batch creation not forwarded | batch-capable UDP written one datagram per syscall |
| `route/packet_destination_guard.go` | write side unpinned | one added method batches straight to the raw socket, skipping the check |
| `transport/http/stream_error.go` | `WriteBuffer`/`ReadBuffer` returned `nil` for a non-`ExtendedConn` | success reported for a buffer never written |
| `transport/v2rayhttp/conn.go` | `ReaderReplaceable` true while `responseCache` held bytes | **silent truncation** of the response head |
| `transport/trojan/protocol.go` | load-bearing invariant in a package with **no tests** | an inverted flag sends the payload with no handshake |

Three audit leads **disproved rather than fixed**: `v2raygrpc`'s `syscall.Conn` (byte-identical to
`grpc@v1.79.1`), `normalizingConn`'s advertised capabilities (the silent `nil` was the defect, not the
advertisement), and `trackedConn`'s activity tracking (anticipated by `conn_reclaim.go:109` and handled).

**Class swept, not just instances:** every `WriteBuffer`/`ReadBuffer` that can `return nil` without writing →
none; every struct embedding `N.ExtendedConn` (an interface, which promotes dynamic methods) exposing
`Upstream()` → four, all correct; every narrow-interface wrapper with capabilities but no `Upstream()` → one,
a deliberate write-only shim. The objective's own exemplar — a scheduler gate observing `Write` — is
implemented and covered by **11 passing tests** in `common/trafficsched` (`TestPacketGate*`,
`TestGatePreservesTheExtendedWriterPath`, `TestGateMTUIsAbsentWhenTheChainHasNone`,
`TestGateDropsVectorisedTruthfullyWhenTheUpstreamHasNone`, and the handshake-visibility pair).

The batch-loss fix is covered by **3 tests** in `route/udp_batch_capability_test.go`
(`TestProductionPacketGateForwardsGeometry`, `TestCounterWrapperMustNotEatPacketBatchWriter`,
`TestCounterWrapperKeepsTheDestinationCounters`), and the red control is real: reintroducing the original
defect — `CreatePacketBatchWriter` returning `nil, false` — fails
`TestCounterWrapperMustNotEatPacketBatchWriter` with *"losing it degrades every UDP session of a
counter-carrying destination from one batched syscall to one syscall per datagram; the data still arrives,
so no functional test notices."*

> **The counts in this ledger were wrong in an earlier revision and are corrected here.** It claimed "8
> regression tests" for the batch fix (actual: 3), and the round reports before it claimed "nine passing
> tests" for the gate (actual: 11). Both were recalled rather than counted — which is the same failure mode
> as the `scidLen` marker and the "kernel_rx has no effect" sentence: a plausible number asserted from
> memory. Every count here was re-derived by running `go test -v` and counting `--- PASS` lines, and the
> test names are listed so the counts can be checked without re-running anything.

### 4. Shadowsocks steady-state copy — **DONE (measured keep)**

The read-side copy is real, sits in a dependency, is allocation-free, and its cost is within noise. Closing it
needs a non-power-of-two capacity that `buf.Put` refuses — verified from source (`alloc.go`'s switch pools
only powers of two; the in-place arm needs `length+Overhead`). The trade would be a benign memcpy for ~32 KiB
of unpooled garbage per read. Kept, with the measurement recorded.

### 5. UDP packet fast path — **DONE in sing-box; one fork-side gap**

Fixed: the batch-loss through the counter wrapper, and kTLS RX reporting an inert splice as merely slow.
Open but **fork-side**: the canceler forwards only *connected* batch capabilities, so an unconnected batch
writer behind a canceler-wrapped conn is invisible (`route/conn.go:718` then falls back per-packet at `:722`)
— cost, not breakage.

**"packet clone" is vacuous**: no packet clone path exists in the repository. Every `Clone()` is
config/TLS/buffer cloning. The ownership half is real and is the buffer-release evidence already collected.

---

## Two defects recorded for whoever can act on them

1. **Fork splice silent stall** — `docs/internal/fork-splice-silent-stall.md`. `handed = true` is set before
   anything is attempted, so a `Pipe2` failure (EMFILE/ENFILE/ENOMEM — exactly when the process is busiest)
   returns `handed=true, n=0` with no error, the copy loop terminates, and `copyExtended` is never reached.
   **The connection stops carrying data with zero bytes copied and no data-loss error.** One-line fix; the
   fork's only splice test asserts the flag on the success path, which is why it survived.
2. **quic-go fork ceiling fix** — `out/quic-go.config.fixed.go`, to be pushed to `jijie-masque-mod9` @
   `9c94b1e9`. Until then `protocol/hysteria2/outbound.go` is inert by design and says so in its own comments.

## Environment facts, so they are not re-derived

| fact | evidence |
|---|---|
| No Linux execution | `wsl --status` → 50; `wsl -e /bin/echo` → fail; no distro package |
| `-race` unavailable | no C compiler; MSVC `cl.exe` rejected by `runtime/cgo` |
| `common/tls` 3 failures | `BLOCKED_ENV`, already in `docs/fork/v016-final-closure-ledger.md:298` — this host's Schannel has no TLS-1.3-over-TCP |
| `with_naive_outbound` cannot link | `cronet-go/lib/linux_amd64` excluded by build constraints |
| `libbox/oomprofile` link error | pre-existing under Go 1.26 linkname validation; reproduces with changes stashed |
| `badlinkname` **is** shipped | present in `release/DEFAULT_BUILD_TAGS`, so `common/ktls` is live on Linux |
