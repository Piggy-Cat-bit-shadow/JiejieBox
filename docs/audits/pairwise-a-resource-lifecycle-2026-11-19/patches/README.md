# Prepared patch: a TUN NAT entry's eviction cancels the dial it launched

**Behaviour is now verified in both directions**, as the audit asked. The patch is still **not pinned**:
the pin needs a CI run for the resulting `sing-tun` revision, which this environment cannot produce.

## What it changes

`github.com/sagernet/sing-tun` at the pinned revision `v0.9.7-0.20261109145802-79ed0653fdc6`:

- `UDPNatConn` gains `cancelContext context.CancelFunc`, created per entry with
  `context.WithCancel(ctx)` inside `getOrCreate`;
- `getOrCreate` passes that derived context to `handler.NewPacketConnectionEx` instead of the caller's
  context (`newContext = ctx` before);
- `UDPNatConn.close()` calls it, so every path that ends the entry — the idle timer, an explicit
  `Close`, and `closeFromCache` eviction — cancels the outbound creation the handler is running.

## The behaviour evidence

Two test files, each run against the pinned revision and against the patched one (the `.txt` suffix keeps
them out of the Go package graph — copy them into the sing-tun copy as `.go` to run).

`udp_nat_eviction_cancel_test.go.txt` covers the **timeout sweep**: a `UDPNat` with a 50 ms timeout and a
`Prepare` that returns an observable context; one packet creates the entry and the handler runs on the
context derived from it; the entry expires, `PurgeExpired()` forces the sweep, and the context must be
cancelled.

`udp_nat_close_paths_test.go.txt` covers **all three close paths independently**, because the audit was
right that one sweep is not the whole story: the timeout sweep, an explicit `conn.Close()`, and a capacity
eviction (`MaxSize: 1`, a second five-tuple arrives).

| Path | Pinned (unpatched) | Patched |
| --- | --- | --- |
| timeout sweep | **FAIL** — "the handler's context was NOT cancelled after its entry was evicted" | **PASS** (0.20 s) |
| explicit `Close()` | **FAIL** — "closing the entry did not cancel the handler's context" | **PASS** (0.00 s) |
| capacity eviction | **FAIL** — "the capacity eviction did not cancel the evicted entry's context" | **PASS** (0.00 s) |

Transcripts: `sing-tun-eviction-cancel-{unpatched,patched}.txt` (the sweep) and
`sing-tun-close-paths-{unpatched,patched}.txt` (all three). Run either the same way:

```
# in a copy of the pinned module, with the test file beside udp_nat.go
go test -run 'TestAnEvictedEntryCancels|TestAnExplicitCloseCancels|TestACapacityEvictionCancels' \
        -count=1 -v -timeout 60s .
```

What the tests do **not** claim: they observe the cancellation, not fd/goroutine/buffer counts after it,
and they do not exercise the consumer's blocking `ListenPacket`. Both are the next step below.

## Still open

1. **The consumer side.** JiejieBox's outbound `ListenPacket` already takes the caller's context, and
   `route/conn.go` passes the one it was given, so a cancelled creation should return — but there is no
   test that asserts it end to end. The shape: an outbound whose `ListenPacket` blocks on `ctx.Done()`,
   a flow evicted while the dial is in flight, and an assertion that the dial returned and nothing was
   recorded.
2. **The pin.** Bump `go.mod` to the fork revision, regenerate `release/source-lock.json`, run
   `python scripts/ci/release-freeze.py verify --lock release/source-lock.json`, and let CI run the new
   revision — the gate that blocked this during the freeze. Then delete this directory.
3. **The admission policy is separate.** Gating `route/conn.go`'s `ListenPacket` with
   `resourceguard.AdmitCreation` would refuse **plain UDP** at `Critical` (DNS to a direct resolver,
   direct QUIC), which today still works under pressure. The audit's point stands: cancelling an unowned
   dial is a lifecycle fix and should be pinned on its own; the budget policy is a separate design that
   must keep normal UDP usable. See
   `docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_RELEASE_READINESS.md` §5.
