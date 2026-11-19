# PAIRWISE C — Patterns

What the three windows (A: resources/lifecycle, B: network/DNS/TUN, C: autonomous) keep finding, stated
as scan rules rather than anecdotes. Each pattern names the shape, the windows that hit it, and the
question a reviewer should ask of any diff.

## P1 — The unrouted second path

**Shape.** A decision D exists, its owner applies it on one path, and a second path reaches the same
*action* without passing D. Nothing is broken when either path runs alone; together, D's purpose is
void.

**Instances.** The naive outbound's shed hook bypassed the gate's contract and cut live tunnels (A-1);
the TUN L0 route-set guard asked only the current-range question and bypassed the router's refusal
(B-4); six outbounds' listener phase bypassed the generation gate and ran the destructive action twice
(C-1); a cold xhttp pool gave one session two connections because the two halves are deliberately
unordered (A-4).

**Question for a diff.** *For every decision, list every path that reaches the action it guards. If a
path does not call the decision, say why in one sentence — or fix it.*

**Where to look next.** `InterfaceUpdated` implementations that also exist as a gate reset; any
`Close`/`Reset` pair where one is reachable from two interfaces; `JudgeFlow`-like fast paths in front of
a policy router.

## P2 — Short-lived state reuses an identity without the long-lived history

**Shape.** Module A owns a *history* (issuance ledger, retired intervals, generation counter) and module
B owns a *cursor* (next address, pool slot, session id). B restarts from zero while A still remembers,
and the identity B reuses already means something to a client or a peer.

**Instances.** A fake-IP reload re-issued an address a client held for another domain (C-2); a retired
placeholder was dialled as a literal because the refusal needed history the live store did not have
(B-5); a pool that treated "closed registration" as "stale generation" (B-2).

**Question for a diff.** *Which state dies with its owner and which survives it? Before the short-lived
one reuses an identity, does it consult the long-lived one?*

**Where to look next.** Port and address allocation, pool slot reuse, session ids, `generation` numbers
that are rebuilt from zero on reload.

## P3 — Compare-then-act leaves a window

**Shape.** A value is checked, then used or acted on, with a scheduling point in between: an epoch, a
`streams` count, a "closed" flag. The window is a handful of instructions, so only a hook or a
deliberate barrier can hit it — which is why it survives thousands of test runs.

**Instances.** The pool publish decision compared the epoch and *then* acknowledged it, so an `Advance`
in between stamped the new generation on an old-network connection (B, R-B2 — three of four pools
fixed, `v2rayquic` recorded because its ack is deliberately after the publish lock); trusttunnel's
`getSession` hands out a session without reserving it (A, R-1, recorded).

**Question for a diff.** *Is there an API that does this check-and-stamp atomically? If yes, use it. If
no, is the window reachable by a concurrent event that matters?*

**Where to look next.** Every `if x.Load() { ... }` followed by a write of the same state; every
`Stale()`-then-`Acknowledge()` pair.

## P4 — A terminal state reads as a recoverable one

**Shape.** One state bit means "the owner is gone" and another means "the owner is out of date". Code
that tests only the second treats the first as recoverable and keeps using, or re-creating, resources
whose owner has been torn down.

**Instances.** `runtimecoord.Registration.Stale()` returns true for a **closed** registration while
`Acknowledge()` silently refuses to clear it, so a pool closed by a network-change fallback stayed
stale forever and paid a handshake per stream (B-2); the TUN `closed`-carrier distinction (A-3) is the
same shape, fixed by giving the carrier an explicit shutdown state.

**Question for a diff.** *Can this state be reached after the owner is torn down? If yes, what should
happen then — and does the code distinguish it from the recoverable case?*

## P5 — The toolchain is part of the wire

**Shape.** A fingerprint, framing or API contract is half ours and half the standard library's. A
toolchain move changes what we send with no line of our code edited, and the diff that should trigger a
review does not touch the paths a CI filter watches.

**Instances.** Go 1.27 moved `net/http`'s HTTP/2 `MAX_FRAME_SIZE` default from 16384 to 1 MiB and
TrustTunnel's Chrome-shaped preface silently stopped being Chrome's (B-1 — the pin existed, was red, and
no push-path job ran it). The repository already knew the class for TLS: `scripts/ci/clienthello-probe`
records that the default key-exchange group set follows the `go` directive.

**Question for a diff.** *Does this toolchain change any wire-visible default? Which pin would notice,
and does a job run it on a toolchain bump?*

**Where to look next.** HTTP/2 settings, TLS ClientHello, QUIC transport parameters, `net/http`
timeouts, `runtime` profile layouts (the `oomprofile` mirror was a real 0.1.52 crash), JSON/serialisation
defaults.

## P6 — A test that exists and runs nowhere

**Shape.** A test file, sometimes a whole package, is executed by no job. `go build ./...` and
`go test -run '^$' ./...` prove it *compiles*, which is routinely mistaken for coverage.

**Instances.** `transport/v2rayxhttp` (two red tests at HEAD, A-4); `./e2e` (58 green tests including the
fake-IP escape proofs cited by production comments, B); `protocol/{vless,vmess,trojan,hysteria2,tuic}`
(C, §5); the whole `protocol/` tree is not run as a suite anywhere.

**The census procedure** (three commands, no tooling needed):

```sh
# 1. packages that have tests
go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...

# 2. what the workflows actually execute
grep -rhoE 'go test[^|]*' .github/workflows/ | tr ' ' '\n' | grep -E '^\./'

# 3. compile-only gates are NOT coverage
grep -rn "go test -run '\^\\$'" .github/workflows/
```

**Question for a diff.** *Which job executes this test? If the answer is "verify only compiles it", the
test is documentation.*

## P7 — An assertion the platform never guaranteed

**Shape.** A test pins a property that the standard library or the OS does not promise, so it fails for
reasons the codebase does not own — and then either rots red or gets "fixed" by weakening it.

**Instance.** Two xhttp lifecycle tests asserted "one session = one TCP connection", which a cold Go
HTTP/2 pool does not guarantee when the two halves of the session are issued back-to-back (A-4). The
correction kept the invariant (a *warm* pool reuses; a redial happens only after the old connection
died) and recorded the measurement, with a mutation control proving the new assertion still catches a
re-dial.

**Question for a diff.** *If this test fails, whose bug is it? If the answer is "the standard library's
timing", the assertion is about the wrong thing.*

## P8 — One destructive action per event, not per observer

**Shape.** An event has several observers (a demand, a listener, a retry path). Each observer
independently decides the event needs its destructive action; the first one's work is destroyed by the
second's.

**Instance.** The generation gate's ledger exists precisely for this, and six of seven owners did not
use it (C-1). The gate's own comment records the earlier measurement: "one published generation plus a
burst of demands ran the action once per demand and closed the HTTP/3 sessions those demands were
opening".

**Question for a diff.** *How many observers can reach this action for one event? Is the "once" rule
implemented in the action, or assumed of the callers?*

## Recommended next-round order

1. **C-3 health probe × selector × recovery** (`protocol/group`, `common/urltest`): the largest
   untouched family, with a real queue (`RoundWaiterLimit`) and a real user-visible failure mode
   (permanent selection of a dead node).
2. **C-5/C-11 reload atomicity** (`daemon/started_service.go`, rule-set artifacts): B's R-B9 already
   documents stop-then-build; the rule-set side has the Windows temp-dir flake as a foothold.
3. **P4 sweep**: grep every `Stale()`/`Acknowledge()`/`closed` pair for "terminal read as recoverable"
   (`transport/v2rayquic` is the recorded outstanding member).
4. **P1 sweep**: every `InterfaceUpdated` implementer that is *not* gated (inbounds, endpoints,
   `transport/wireguard`), looking for the same double-action shape C-1 fixed for outbounds.
5. **P6 census as a CI gate** — a cheap script that fails when a package with tests is mentioned by no
   workflow. It would have caught three of this effort's nine defects before they shipped.
