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


## 8. ACK-only is not a send

Pacing accounts for bytes that compete for the congestion window. An ACK-only
packet carries no retransmittable data, so calling `pacer.SentPacket` before the
`isRetransmittable` check charges it against the budget and delays real data.
The fix must cover every sender: CUBIC, BBR and Brutal had the same ordering.
The regression test observes `pacer.lastSentTime` and budget directly because a
concrete pacer gives no injectable fake.

## 9. A runt obfuscated datagram is not payload

Salamander returns `(0, addr, nil)` for a datagram no longer than the salt.
A bare `return` after a successful underlying read reports the salt length as if
it were plaintext, so the caller sees the salt as payload. Test the boundary
around the salt, not just the happy path.

## 10. Transport parameters must be written into the marshalled extension

`SetTransportParameters` writes `quic.transportParams`, but a preset ClientHello
marshals the extension cloned by `ApplyPreset`; the two are different objects.
The fork's marshal-time `syncQUICTransportParameters` parses and writes the
cloned extension (and covers SetTransportParameters-before-ApplyPreset, when no
extension exists yet), so it subsumes upstream's direct `marshalResult` write.
Keeping the stronger helper is correct; document the upstream finding instead of
copying a weaker write.


## 11. A grafted branding line can be a content superset

The dashboard fork has no common ancestor with upstream, but its tree contains
every upstream feature plus branding. A real two-parent merge is still correct:
resolve the conflict hunks, verify upstream has no unique feature/field/key, and
record the ancestry link. The merge tree may equal the fork tree; that is not a
fake merge, it is a content-subset merge. Do not use `-s ours` — prove the subset
per hunk and keep the commit honest.

## 12. Parallel generation lines are not "behind"

Cronet has a source line (`main`) and a generated line (`go`). The fork's `go`
generation is built from `4c96192`, which already contains upstream's source
`73349ff`; upstream's two newer `go` commits are a parallel generation from the
older source. Ordinary branch comparison says "behind 2", but merging would
replace the newer fork native artifacts with an older generation. Check the
generation source ancestry, not only the branch tip.

## 13. Check the consumer before writing a fix

The XHTTP stream-up non-200 candidate was already fixed and covered by a
red-first test (`96bd665`, `669fa275`). Read the current code and tests before
turning an audit candidate into a change; otherwise the same fix is written
twice and the test matrix is duplicated.


## 14. The main merge is an audit, not a checkout

Absorbing 85 upstream commits into a 3,700-commit fork produced 108 conflicts
but only 42 files with a real content delta: most upstream behaviour was already
present under a different implementation. Resolve by reading both sides, keep
the fork's lifecycle/budget model, and port only the upstream boundary that is
actually missing. A real two-parent merge with every conflict inspected is the
honest shape; `-s ours` would have hidden the 42 real deltas.

## 15. A generated artifact must be regenerated after conflict resolution

`docs/schema.json` is generated and tracked. A hand-resolved conflict produced a
schema that was semantically plausible but did not match the generator, and the
tracked-schema test caught it. Resolve generated files by running their
generator, never by hand.

## 16. Release hardening is a product decision

Upstream's root service switch is not automatically acceptable: the fork's
packaged unit keeps `User=sing-box` and the capability bounding set, and its test
pins that. A merge that takes the upstream file wholesale silently widens the
deployment surface. Restore the fork's policy and record the rejection.
