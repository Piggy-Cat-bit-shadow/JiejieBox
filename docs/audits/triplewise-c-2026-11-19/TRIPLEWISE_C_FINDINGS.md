# TRIPLEWISE C — New combinations, and the findings

**C-T5 is this round's confirmed defect** (added after the first draft, which reported no new defect;
the finding came from applying P9 to the one pool with no atomic publish). Beyond it, the round's other
result is the shape of the answer: the three-system combinations that *were* real and broken in this
tree have all been found and fixed by this effort, and the ones that remain are blocked by a dependency
pin or a device.

## C-T5〔CONFIRMED → FIXED〕A publish and its generation acknowledgement were two steps

| | System | What it owns here |
| --- | --- | --- |
| ① | the completed creation's epoch token (`startGeneration`, read before the handshake) | "the generation this connection was built for" |
| ② | `Registration.Acknowledge` | records the pool as current for **whatever epoch the coordinator reports at call time** |
| ③ | the pool's publish decision (`connAccess`) | decides whether the connection is handed to later demands |

The dial's own re-confirmation closed the wide window (a change *during* the handshake), but the
acknowledgement ran **after** the publish lock. An `Advance` landing between the re-confirmation and the
acknowledgement made `Acknowledge()` stamp the **new** epoch onto a connection built on the **old**
network. After that `Stale()` is false, nothing retires the connection, and every later demand is handed
a session from the network that just ended — audit F-15, reached through a gap a comparison cannot
close. The same shape existed on the migration path, where the window is *wider* (a migration validates
a candidate for seconds).

Fix: the publish decision and the acknowledgement are one call inside `connAccess`
(`AcknowledgeIfCurrent(startGeneration)`); a refused acknowledgement becomes the retryable
stale-generation error and the connection is released instead of published, so the offer's waiters are
told rather than handed the stale session. The migration path acknowledges `AcknowledgeIfCurrent`
against the epoch its reset began under.

Evidence: `transport/v2rayquic/client_publish_generation_test.go` (the package's first test for this
path, driven with `testHookBeforePublish` inside the exact window). Mutation control: restoring
publish-then-acknowledge turns it red — "a connection built for the previous generation must not be
handed out". The existing migration suite is the positive control for the migration guard.

This closes R-B2's fourth site; `PAIRWISE_B_FINDINGS.md` §R-B2 now reads "4 of 4".

## The four genuine three-system defects this effort found and fixed

| ID | The three systems | The defect | Fixed in | Its proof |
| --- | --- | --- | --- | --- |
| **C-T1** | the admission gate's `resetEpoch` ledger × the six outbounds' listener entry × `route/network.go`'s walk | the push notification bypassed the gate, so a destructive transition ran twice per generation and the second pass destroyed the session the first had just built | pairwise C, commit `37b68ae7d` | six `TestInterfaceUpdatedRoutesThroughTheGenerationGate`, mutation: direct call → `expected 1, actual 0` |
| **C-T2** | `Client.Close` × the stale-generation predicate (a closed registration reports stale forever) × `GenerationPool` | a late dial installed a transport into a pool that had already released everything | triple A, commit `128222f20` | two tests + pool mutation: `expected 0x1, actual 0x2` |
| **C-T3** | the fake-IP allocator cursor × the cross-Box issuance ledger × a hot reload | a reload re-issued an address a client held for another domain, silently delivering the connection to the wrong destination | pairwise C, commit `08d2b2f6f` | two tests; mutation: "the reload re-issued 198.18.0.2" |
| **C-T4** | the fake-IP store's range × the TUN fast-path guard × the router's refusal | three separate holes: an out-of-range allocation, a retired-generation placeholder bypassed before the router, and a durable record with no reader | pairwise B, commits `2677d2134`, `2463ea3b5`, `00f711c5f` | three tests, each with its own mutation control |

Three of the four share one shape: **a shorter-lived state deciding from a longer-lived fact that had
already been invalidated** (the gate's ledger after a reset, the pool after a close, the cursor after a
reload). The fourth (C-T4) is the related "a guard that answers a narrower question than the one that
matters". Both are recorded as patterns P1/P2/P4 in
`docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_PATTERNS.md`.

## What this round examined and did not find a defect in

| Candidate | Evidence that it is genuinely coordinated |
| --- | --- |
| **DNS cache × network generation × the dialer** (the brief's shape 6) | `dns.Router.ResetNetwork` advances the epoch and purges the cache as one step under `dnsEnvironmentAccess`, with the exact interleaving it prevents documented (`dns/router.go:2597+`). Twelve tests name the cases, including `TestRemoteTransportNegativeVerdictDoesNotCrossNetworks` (an NXDOMAIN learned on one network must not suppress the name on the next) and `TestRemoteTransportInFlightAnswerIsNotStoredOnTheNewNetwork`. All executed green this round |
| **H3 fallback state × network transition × the shared HTTP client** (shape 10) | `performTransitionLocked` calls `Router.ResetNetwork()` unconditionally → every `ManagedTransport` rebuilds its inner transport (`epoch.Swap(nil)`), so the H3 verdict ladder and the HTTP/1.1 authority pin cannot survive a network change. Verified by source order; the remaining gap is that the demotion is silent (window B's R-B3) |
| **H3-only policy × the fallback decision × error classification** | the H3-only configuration builds no fallback object at all and raises its transport requirement to `AllOf UDP`; naive refuses a fallback flag it cannot honour; trusttunnel refuses cert/x509/407/QUIC-auth from the fallback eligibility set, with tests |

## The one open three-system item, and why it stays open

**TUN UDP NAT × the outbound packet session × the admission gate** (window A's R-4/R-5, triple B's
TB-5). The gate is not on the UDP path, and evicting a NAT entry does not cancel the dial it launched, so
the 4096-entry table is the only bound on the real per-flow cost. The fix needs the `sing-tun` fork **and**
a pin bump; the release gate for a new pin requires a CI run for the new revision, which cannot be
produced from this environment during a freeze. Recorded with deterministic test sketches.

## Not covered, by name

- health probe × selector × recovery state (window C's C-3): the largest untouched family, with a real
  queue and a user-visible failure mode.
- xhttp body/stream lifecycle × cancellation (window A's A09): needs a half-closed/bodiless-EOF server
  fixture.
- MASQUE NAT64/PREF64 × the DNS assignment × the QUIC path (triple B's TB-9): in-package suites are green;
  the interleaving was not attacked.
- anything requiring a device: iOS NE budget, Cronet native lifetime, `phys_footprint`, auto_redirect.
