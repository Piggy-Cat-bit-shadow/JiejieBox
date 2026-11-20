# Quadruple B — Fake-IP, capture routing, Provider and reload

Second window of the four-system round. The assigned rows were Q01 (fake-IP × TUN × router ×
`auto_redirect`), Q04 (provider × URLTest × selector × physical socket), Q05 (DNS cache × dual-stack ×
network epoch × pool) and Q06 (reload × fake-IP history × rule-set × live TUN flow).

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch, HEAD `3a1571efc`
- Platform: Windows amd64, Go **1.27.4**, `-race` with the bundled mingw64 gcc; no Administrator
  token, no Linux netns/nftables, no TUN device
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

One row was closed with real evidence, one candidate race was checked against the source and turned out to be already guarded, and two rows are stated as blocked. **Q05 — the chain of a real DNS
cache answer feeding the real dual-stack dialer across a network-generation change — had been recorded
as `NOT_COVERED` in every previous round**, because it cannot be tested from the dialer package (`dns`
imports `common/dialer`). It is now executed end to end in `dns`, against a real socket: two loopback
listeners on one port, the production client and the production dialer, a control arm that proves the
cache is actually in the chain, and a transition arm that only passes when the old-generation answer is
*not* reused. It passes, and the mutation that skips the environment refresh makes it fail. No
production defect was found in this window; the remaining rows need a Linux packet path (Q01), a
provider/selector harness that was not built (Q04), or a live TUN flow (Q06), and none of those is
dressed up as a result.

## 1. Q05 — the joint chain, executed (`7f90a8bea`)

**The question.** A cached answer produced on network A, a candidate plan built from it, a network
change, and a dial: does the socket the user gets belong to the new network?

**Why it had never been tested.** `dns` imports `common/dialer` (`dns/transport_dialer.go`), so a test
in `common/dialer` cannot import `dns`. The halves were covered: `dns` owns the cache namespace and the
answer stamp, and `common/dialer` owns the candidate plan with a *scripted* resolver. The seam — the
real cache behind the real dialer — had no harness.

**How it works now** (`dns/client_dial_candidate_test.go`):

1. Two TCP listeners on **one** port, on `127.0.0.1` and `127.0.0.2`. The destination is a domain plus
   a port, so the address the resolver returns decides which listener accepts — a socket-level witness.
2. The production `dns.Client`, started, with a scripted `adapter.DNSTransport` whose A answer is
   switchable and whose A queries are counted.
3. The production `dialer.NewWithOptions(..., RemoteIsDomain: true)`, with a thin `adapter.DNSRouter`
   that forwards into `Client.Lookup` (the real cache path) and no detour.
4. The environment: an `adapter.NetworkManager` fake answering `NetworkEnvironment()` with the
   generation — exactly what the client reads for its cache namespace.

**The two arms.**

| Arm | Action | Assertion | Why it is needed |
| --- | --- | --- | --- |
| control | dial twice, no generation change | one resolve, the second dial adds **no A query**, both land on the same listener | proves the cache is genuinely consulted; without it "the new address was used" would also hold for a chain with no cache |
| transition | change the answer, advance the generation, move the transport pins (`refreshTransportEnvironments`, what `Router.ResetNetwork` does) | the next dial lands on the **second** listener with a fresh A query; the first listener stays at two accepts | the actual question |

**Files.** `dns/client_dial_candidate_test.go` (220 lines). A-family queries are counted separately:
the dialer asks for both families and an empty AAAA answer is not cached, so counting every exchange
would grow by one per dial regardless of the A cache — the first version of this test made exactly that
mistake and reported a false "cache not in the chain".

**Evidence.**

| Stage | Evidence |
| --- | --- |
| green | `go test -count=1 -run 'TestTheRealCacheFeedsTheRealDialerAcrossANetworkChange' -v ./dns/` PASS |
| package | `./dns/` `ok 8.63 s`; `-race` `ok 9.91 s` |
| mutation | skipping the pin refresh fails with `the dial after the network change landed on the old address: a cached answer crossed the generation boundary` |

**Six-stage check.**

1. *Loaded*: `dns.Client`, `common/dialer` and `adapter.NetworkManager` are the production types; no
   reimplementation was substituted.
2. *Reached*: the dial goes through `dialerInstance.DialContext` with a domain destination, i.e. the
   same entry an outbound uses.
3. *Decided*: the cache decides which answer, the dialer decides which candidate, the generation
   decides which cache namespace.
4. *Acted*: the dial opens a real TCP connection to the resolved address.
5. *Observed*: which loopback listener accepted, and how many A queries the resolver saw.
6. *Settled*: both listeners are closed by cleanup, the client's cache holds the second entry, and no
   goroutine is left parked.

## 2. The rows that could not be executed

### 2.1 Q04 — the candidate race was checked against the code and does not exist

The race I designed for Q04 was: **a provider republication replaces the outbound object behind a tag,
and the selector's cached choice keeps pointing at the old object** — the user-visible shape being "the
UI shows B, the dial goes to A". Reading the selector at this HEAD, that shape is blocked four times
over, and the code says why:

| Guard | Where | What it prevents |
| --- | --- | --- |
| the selection is an atomic **object**, not an index or a tag cursor | `selector.go:68` (`selected common.TypedValue[adapter.Outbound]`) | the P2 shape (a short-lived index reusing long-lived history) cannot occur |
| a provider republication re-resolves the selection **by tag** against the new member map | `selector.go:363-366`, `selector.go:398-420` | the selection follows the tag, so a replaced object is adopted rather than left behind |
| replacing the object behind the same tag is explicitly "a subscription refresh, not a switch" | `selector.go:414-417` | the in-flight dial's lease keeps the old object open; the revision does not advance as if the user switched |
| `dialMember` rejects a member whose revision moved during setup | `selector.go:767-773` | the dial cannot commit to a member that the update already replaced |

The presentation side is a named contract too: `SelectionStatus`/`SelectionSnapshot`/`Revision`
(`selector.go:542-609`) distinguish "configured default", "persisted preference", "committed" and
"moved", and `common/physicalpath` consumes exactly those to label `PathNode.IsCurrent` and
`Decision` (`selector.go:477-491`). That is the mechanism the prompt asks for — authoritative identity
separate from the presentation label — and it exists.

So Q04 is `SOURCE_ONLY` rather than `NOT_COVERED`: the harness was not built **because the race it was
meant to attack is already prevented by construction**, and building it would have been a re-run of
`protocol/group`'s own tests. `protocol/group` 7.24 s, `common/physicalpath` 0.10 s and
`common/urltest` 8.50 s are green here.

What is *not* claimed: a real two-server dial through a provider-backed selector was not executed, so a
defect in a path these guards do not cover (for example the provider's own publish ordering) would not
have been seen by this window.

### 2.2 Q01 and Q06

| Row | Verdict | Blocker |
| --- | --- | --- |
| Q01 | `NOT_OBSERVABLE` | the kernel capture/route decision needs a Linux netns with nftables and root; the fake-IP store's own guards were inherited, not re-claimed |
| Q04 | `SOURCE_ONLY` | the candidate race was checked against the code and is already guarded (§2.1), so the two-server harness was not built |
| Q06 | `NOT_OBSERVABLE` | the live-TUN-flow half needs a TUN device; the reload/history half is the inherited issuance-ledger work |

## 3. New direction this window opens

The Q04 source check moved the open question: the selector side is guarded, so the remaining risk in
that row is the **provider's publish ordering** - whether a provider can publish a member list whose
objects are already closed, or publish two lists out of order under a rebuild. That is a different
harness (no selector switch needed, two servers plus the provider's callback path), and it is the one
to build next.

The second direction is the one this window proved works: the Q05 style - a real owner behind a real
consumer, two loopback listeners on one port as the witness, and a control arm that proves the cache or
pool participates. The same shape applies to the HTTP/QUIC pool epoch questions that are still open.

## 4. Gates

`./dns/` green with and without `-race`; no production code changed, no dependency pin moved
(`release/source-lock.json`, `release/pin-runs.json` untouched).
