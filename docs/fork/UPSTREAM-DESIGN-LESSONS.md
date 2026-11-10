# Upstream design lessons

What the 2026-11-11 sing/sing-tun sync taught us; use it in the remaining stages.

## 1. Ownership flags are not ordering

`CachedConn` used `taken atomic.Bool` as a hand-over lock. The CAS made the
hand-over itself unique, but it never ordered `Read`'s use of `c.buffer`
against `Close`'s release. The field read/write pair was a data race and the
buffer could return to the pool while `Read` was still copying out of it.

Lesson: a flag that arbitrates "who owns the buffer" must also order every
consumer's access to that buffer, or it is not an ownership protocol. The
upstream mutex is the right shape; the fork's `CachedPacketConn` CAS is safe
because it has no unsynchronized field use. Keep the distinction, do not
mechanically replace every CAS.

## 2. A batch writer needs one error policy per datagram

Upstream's per-message `EMSGSIZE` skip (continue with the rest of the batch,
report the error once at the end) replaced the fork's abort-on-first-error
batch writer. The fork's `packet_batch` state machine now carries
`writeFunc/pending/writeErr` and a cursor preserved across poller wakeups.
Lesson: a batch API must state whether one bad datagram aborts or skips the
batch; tests must pin both.

## 3. One child context per parallel dial

The upstream parallel dial creates one child context for the whole race,
cancels it when the winner is chosen, and dials a single address directly.
The fork's per-attempt cancel slice kept the winner's dial context alive (a
leak) and the single-address path could recurse
`DialSerial -> ParallelDialer -> DialParallel`. Fused: one child context,
direct single-address dial, prompt `<-ctx.Done()` return, and an explicit
zero-address error.

Lesson: cancellation ownership belongs to the race, not to each attempt's
bookkeeping slice.

## 4. Fork budgets are the outer contract; upstream fixes run inside them

sing-tun's upstream fixes (ENOBUFS → dropped frame, auto-redirect close order,
GSO segment index, per-reply IPv4 ID) were applied inside the fork's
per-session/NAT/device byte budgets and waiter ownership. Where upstream
restructured (`NewForwardDispatcher` MTU, `portNAT.routed`, dispatcher return
lists), the fork's `RoutedPort`/`PortMTU`/staged-port design stayed; the
upstream *intent* was mapped onto it, not the upstream structure.

Lesson: do not adopt an upstream data structure just because it is newer; map
its invariant into the fork's state machine.

## 5. GSO split must be resumable

Upstream threads the segment index through `GSOSplit` so a frame with more
segments than the read batch continues across calls. A splitter that ignores
the segment index either drops the tail or loops forever; the new test
(`TestGSOSplitResumesAcrossBatches`) mutation-checks exactly that.

## 6. Synthesized replies own their header

Auto-redirect/system-stack UDP replies no longer copy the request's IP header;
the reply builds its own header and a per-reply IPv4 ID. Copying the request
header made every reply share ID 0; the new test mutation-checks `SetID`.

## 7. Merge shape

- One real merge commit per fork branch with both parents; upstream history is
  an ancestor, so GitHub Compare `ahead_by=0`.
- Conflicts resolved per file: keep the fork when it is a strict superset,
  take upstream when the fork only held the old snapshot, fuse when both carry
  invariants.
- API/semantic changes must be consumed by the kernel in the same round, not
  left for a later pin bump: `ReadRetry.Wait()` and
  `ErrTooManySegments` recoverability would otherwise break the read loop
  silently.
