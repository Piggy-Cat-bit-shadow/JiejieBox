# Prepared patch: a TUN NAT entry's eviction cancels the dial it launched

This directory holds the **prepared, unpinned** half of window A's R-4/R-5. It is committed so the next
round applies a reviewed change instead of re-deriving it, and it is deliberately **not** applied to the
release tree: the pin's release gate requires a CI run for the resulting `sing-tun` revision, which
cannot be produced from this environment.

## What it changes

`github.com/sagernet/sing-tun` at the pinned revision `v0.9.7-0.20261109145802-79ed0653fdc6`:

- `UDPNatConn` gains `cancelContext context.CancelFunc`, created per entry with
  `context.WithCancel(ctx)` inside `getOrCreate`;
- `getOrCreate` passes that derived context to `handler.NewPacketConnectionEx` instead of the caller's
  context (`newContext = ctx` before);
- `UDPNatConn.close()` calls it, so every path that ends the entry — the idle timer, an explicit
  `Close`, and `closeFromCache` eviction — cancels the outbound creation the handler is running.

Before this, an evicted entry's `NewPacketConnectionEx` kept dialling. The entry was gone, its `writer`
was closed, and the creation continued until the dial's own timeout: the exact "a resource created for a
flow that no longer exists" shape, and the amplifier the audit recorded (STUN-shaped bursts: many NAT
entries, each spawning a session creation that outlives its reason).

## Evidence attached to this patch — and what is missing

| Claim | Evidence |
| --- | --- |
| Applies cleanly to the pinned revision | `git apply --check -p1` exit 0 on a fresh copy of that revision |
| Compiles | `go build ./...` exit 0 in that copy after applying, and exit 0 before (so the patch is the only difference) |
| Formatted | `gofmt -l udp_nat.go` clean |
| **Behaviour** | **NOT verified.** A behavioural test needs sing-tun's own harness plus a context-aware dial double, and then a JiejieBox-side integration test. Sketches below |

## How the next round should verify it, then pin

1. **Fork-side test (sing-tun).** Build a `UDPNat` whose `Handler.NewPacketConnectionEx` blocks on
   `ctx.Done()` after recording the context. Create an entry, let the idle timer (or an explicit
   `Close`) end it, and assert the recorded context is cancelled **before** the timeout the handler
   would otherwise wait for. A `Handler` that ignores the context must keep the old behaviour, which is
   the control.
2. **Consumer-side test (JiejieBox).** An outbound whose `ListenPacket` blocks on `ctx.Done()`; a flow
   that is evicted while the dial is in flight; assert the dial returns with the cancellation and the
   outbound recorded no session. `route/conn.go`'s `NewPacketConnection` is the seam.
3. **Pin it.** Bump `go.mod` to the fork revision, regenerate `release/source-lock.json`,
   `python scripts/ci/release-freeze.py verify --lock release/source-lock.json`, and let CI run the new
   revision (the gate that blocked this during the freeze). Then delete this directory.
4. **The other half is in-repo and still open.** `route/conn.go`'s `NewPacketConnection` calls
   `this.ListenPacket` with no `resourceguard.AdmitCreation`. Wrapping it the way
   `transport/v2rayquic.createConnection` wraps QUIC construction would bound the creation rate at
   `Critical` — but it would also refuse **plain UDP** at Critical (DNS to a direct resolver, direct
   QUIC), which today still works under pressure. That is a policy decision, not a bug fix, and it is
   written up in `docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_RELEASE_READINESS.md`
   §5.
