# Quadruple B — Fake-IP, capture routing, Provider and reload

Second window of the four-system round. The assigned rows were Q01 (fake-IP × TUN × router ×
`auto_redirect`), Q04 (provider × URLTest × selector × physical socket), Q05 (DNS cache × dual-stack ×
network epoch × pool) and Q06 (reload × fake-IP history × rule-set × live TUN flow).

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch, HEAD `3a1571efc`
- Platform: Windows amd64, Go **1.27.4**, `-race` with the bundled mingw64 gcc; no Administrator
  token, no Linux netns/nftables, no TUN device
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

One row was closed with real evidence and three are stated as blocked. **Q05 — the chain of a real DNS
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

| Row | Verdict | Blocker |
| --- | --- | --- |
| Q01 | `NOT_OBSERVABLE` | the kernel capture/route decision needs a Linux netns with nftables and root; the fake-IP store's own guards were inherited, not re-claimed |
| Q04 | `NOT_COVERED` | needs two local servers, a provider replacement with the same tag, a selector switch and a real dial in one harness; the ownership rule it would attack was already fixed, so a genuinely new race has to be designed first |
| Q06 | `NOT_OBSERVABLE` | the live-TUN-flow half needs a TUN device; the reload/history half is the inherited issuance-ledger work |

## 3. New direction this window opens

The Q04 harness is the one worth building next, and this window's method says what it must observe:
two local servers whose accept counts are the witness, the provider's node UID as the authoritative
identity, and the selector's choice as the presentation label. The candidate race to design is a
URLTest result carrying the *new* node's UID arriving while the selector still holds the old node
object — the existing fix drops results by UID, and the open question is whether the selector's cached
choice can outlive the object it names.

## 4. Gates

`./dns/` green with and without `-race`; no production code changed, no dependency pin moved
(`release/source-lock.json`, `release/pin-runs.json` untouched).
