# Phase 10 — sing code closeout

```text
RESULT = DONE
SING_SHA = 0dc63cd57a6d7e4259ac7e3a7ba7973ee8b5ab7b
SING_BOX_SHA = 912fdca21a0977056258687584a74f76cadf4d05 (docs; code 73ade67668c)
WINDOWS_VECTORISED = PRE_EXISTING_RACE_CHECKPTR; NATIVE_TEST_PASS; SYSCALL_TCP_PANIC_UNREACHABLE
UDP_BATCH_OWNERSHIP = NO_PRODUCTION_DEFECT; TWO REGRESSION TESTS ADDED
SOCKS_BATCH = NO_DEFECT_FOUND; SOURCE/TEST AUDIT
PARALLEL_DIAL = FIXED (parent context cancellation)
BUGS_FIXED = 1 (parallel dial cancellation latency/hang)
TESTS_AND_CI = PASS (code CI 40132223175/40132223238/40132223179; docs 40133339649)
PERFORMANCE = NOT_MEASURED
UPSTREAM_TARGET_ANCESTOR = YES
TARGET_BEHIND = 0
ROOT_TEST_PIN_MATCH = YES
PROTECTED_BRANCHES_UNCHANGED = YES
WORKTREES = _p01_main clean; _sing10 temporary integration worktree
```

## 1. Windows Vectorised / checkptr

- `TestWriteVectorised` (TCP) passes natively on Windows: the factory route is
  `CreateVectorisedWriter -> *net.TCPConn -> NetVectorisedWriterWrapper`; it never constructs
  `SyscallVectorisedWriter`.
- `SyscallVectorisedWriter` is constructed only in the non-Windows `else` branch of
  `CreateVectorisedWriter` (`vectorised.go:49/51`). Its `panic("not implemented")` is therefore not
  reachable on Windows from the production factory. No new implementation was written.
- `TestWriteVectorisedPacket` does use `SyscallVectorisedPacketWriter`. It passes without race and
  panics under `-race` with `checkptr: converted pointer straddles multiple allocations` at the
  `RawSockaddrAny` conversion (`vectorised_windows.go:83`). The same failure occurs on the
  pre-merge fork commit `4200a04`, so it is pre-existing, not a phase-09/10 regression.
- It was not added to a permanent skip list. The normal Windows test remains enabled; the race
  probe in this report is the only place it is excluded.

## 2. UDP packet batch / ownership

- Audited fallback and connected batch writers plus the syscall backends.
- The phase-09 merge ported upstream's per-packet `errMessageTooLong` handling: the failed buffer is
  handed to the writer (`Leak`, which is `Release` outside Debug), the next packet is still attempted,
  and the last message-too-long error is returned. Non-message-too-long errors stop and release all
  remaining buffers.
- No production defect was found; the existing error/deadline/closed-socket tests already prove
  remaining-buffer release. Added two direct regression tests in `486e7d4`:
  - `TestPacketBatchWriterMessageTooLongContinues`
  - `TestConnectedPacketBatchWriterMessageTooLongContinues`
- No scratch/offload/mmsg/Darwin batch code was rewritten.

## 3. SOCKS UDP batch / lazy handshake

- Audited `protocol/socks/packet_batch.go` and `packet.go`:
  - batch writer encodes each destination, releases the whole batch if encoding fails;
  - lazy writer releases the batch on handshake failure;
  - the batch reader releases the complete buffer set when a header cannot be decoded;
  - empty batches are accepted without underflow;
  - lower-level batch errors are returned without being converted into a successful close.
- No production defect was found. Existing `protocol/socks` tests pass; no code change was made for
  this item.

## 4. Parallel Dial

- Found a real cancellation gap in `common/network/multi.go`: the select only handled the fallback
  timer and dial results. A parent context cancellation could wait up to `fallbackDelay`, and with a
  child dialer that ignored its context, could wait indefinitely.
- Minimal fix `0dc63cd`: add `case <-ctx.Done(): return nil, ctx.Err()`; the existing defer cancels
  remaining attempts and closes late connections.
- Deterministic test `TestDialParallelReturnsOnContextCancellation` uses a dialer that ignores its
  per-attempt context and a one-hour fallback delay, proving the parent cancellation returns
  promptly. It passes normally and under `-race`.

## Verification

- `go test ./...` in `sing`: PASS.
- `go test -race` on `common/bufio`, `common/network`, `protocol/socks`: PASS;
  `TestWriteVectorisedPacket` excluded only from the local race probe (pre-existing checkptr).
- Cross builds Windows amd64, Linux amd64, Darwin arm64: PASS.
- `sing-box` regressions: SOCKS, WireGuard, TUN, direct, DNS, MASQUE/MASQUE transport: PASS.
- Root/test `sing` effective pin: `v0.9.6-0.20261024205802-0dc63cd57a6d`, identical in both.
- Upstream target `03a6cce6` remains an ancestor of `sing/dev`; `TARGET_BEHIND=0`.
- No tags/releases/assets changed; no force push; protected
  `pending/device-axis-reload-window-tests` untouched.

## Performance

No reliable same-host before/after benchmark was run for the batch/SOCKS/parallel paths; recorded
`NOT_MEASURED`. The only behavior change is the context cancellation path, which is covered by a
timing-bound regression test rather than a throughput claim.
