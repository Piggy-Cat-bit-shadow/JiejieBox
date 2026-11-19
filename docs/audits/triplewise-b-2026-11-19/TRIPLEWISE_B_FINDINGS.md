# TRIPLEWISE B — Findings

**This window found no new defect.** That is not "everything passed": three of the twelve rows were
attacked and are `NO_DEFECT` with evidence, one is a confirmed cross-repo item that cannot be fixed
during a release freeze, and the rest are `NOT_COVERED` or `NOT_OBSERVABLE` with the reason named. The
brief asks for attack attempts and "no finding yet" evidence when existing protection is effective; this
is that document.

## Why there is no new defect in the fake-IP chain (TB-1)

The chain *was* broken, in three places, and all three were fixed in this effort before this window
started. Re-stating them here is not a claim of new work; it is the evidence that the assembled triple now
holds:

| Was | Where | Fixed in |
| --- | --- | --- |
| a `/31`/`/32` range issued an address **outside itself**, which the router then dialled as a literal | `dns/transport/fakeip/store.go` `nextAddress` | window B, B-3 (`issuableAddress`) |
| a placeholder from a **retired generation** was bypassed by the route set *before* the router could refuse it | `protocol/tun/inbound.go:1248` | window B, B-4 (`fakeIPPlaceholder`) |
| with **no fakeip server** configured, the durable retirement record had no reader at all | `route/route.go` `retiredFakeIPAddress` | window B, B-5 (cache-file fallback) |
| a **reload** re-issued an address a client held for another domain | `fakeip.Store.Start` | window C, C-2 (ledger seeding) |

Each has a test and a mutation control (`TRIPLEWISE_B_WIRING_AND_LEAKS.md` §1). Any one of them alone is
enough to make the triple wrong; that is what made this row P0, and why it was closed first.

## Why the transition/fallback triple (TB-2) is coordinated but still worth a note

The generation genuinely reaches the fallback state: `performTransitionLocked` calls
`Router.ResetNetwork()` **unconditionally**, which rebuilds every managed transport (`epoch.Swap(nil)`),
so the H3 verdict ladder and the HTTP/1.1 authority pin cannot survive a network change. This was the
unverified half of window B's R-B3.

What remains is *silence*, not a broken chain: `markH3Broken` and `markH2Fallback` log nothing and expose
nothing, so a report cannot distinguish "fell back" from "worked", and the HTTP/1.1 pin has no window
inside one epoch. That is a reporting decision (which field, which surface, what a user should see) and
was deliberately not taken in a release round; it stays recorded in
`docs/audits/pairwise-b-network-dns-tun-2026-11-19/PAIRWISE_B_FINDINGS.md` R-B3.

## The one confirmed open item (TB-5), unchanged

TUN UDP NAT × outbound packet session × admission: the gate is not on the UDP path at all, and evicting a
NAT entry does not cancel the dial it launched, so the 4096-entry table is the only bound on the real
per-flow cost. Fixing it requires the `sing-tun` fork **and** a pin bump whose release gate demands a CI
run for the new revision — not producible during a freeze. The deterministic test designs are in window
A's findings, R-4…R-7. This window adds no new evidence and makes no new claim.

## What a three-system defect would have had to look like, and did not appear

For this window the test is: *a control-plane decision (DNS restore, route verdict, generation reset)
that the data plane does not honour, or two mechanisms that disagree about one flow.* Two were searched
for explicitly:

1. **A route-set bypass of the fake-IP guard.** Present once (B-4), now closed; the guard asks the
   retired-generation question as well as the range question.
2. **A fallback state that outlives its network.** Structurally impossible after the reset chain; what is
   missing is only the record of it.

Neither produced a new defect this round. The remaining risk concentration is `TB-5` (a dependency-layer
amplification) and `TB-4`/`TB-6` (stale cache and the two-pass flow, both unbuilt harnesses).

## Honest classification table

| Row | Verdict | One-line reason |
| --- | --- | --- |
| TB-1 | `NO_DEFECT` (after B-3/B-4/B-5/C-2) | the three holes are closed, with controls |
| TB-2 | `NO_DEFECT` | the reset chain clears the verdicts; the silence is R-B3 |
| TB-3 | `NO_DEFECT` | the reset is unconditional and ordered before the listeners, by construction |
| TB-4 | `NOT_COVERED` | no test feeds a stale cache into a new generation's candidate plan |
| TB-5 | `CONFIRMED` (cross-repo, unfixed) | window A's R-4/R-5 |
| TB-6 | `NOT_COVERED` | needs a two-pass flow fixture |
| TB-7, TB-8 | `NOT_OBSERVABLE` | no Linux/nftables, no two live capture paths |
| TB-9 | `NOT_COVERED` | in-package suites green, interleaving not attacked |
| TB-10 | `NO_DEFECT` (policy honoured) | the H3-only path has no fallback object to reach |
| TB-11 | `NO_DEFECT` (consistency) | close-then-build cannot expose a mixed graph; the cost is R-B9 |
| TB-12 | `NOT_COVERED` | node-vs-socket reporting not attacked |
