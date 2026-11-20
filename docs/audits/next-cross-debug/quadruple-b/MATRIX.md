# Quadruple B — Fake-IP · capture routing · Provider · reload: matrix

Window B of the four-system round. Base: `testing` @ `3a1571efc`. Commits added: `7f90a8bea` plus
these reports.

- Platform: Windows amd64, Go 1.27.4; no Administrator token, no Linux netns/nftables, no device
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 1. The four rows

| Row | The four systems | Verdict | What was executed / what is blocked |
| --- | --- | --- | --- |
| **Q01** | fake-IP ledger/allocator · TUN fast path · router restore/refusal · `auto_redirect` kernel rules | `NOT_OBSERVABLE` | the kernel packet path needs Linux netns + nftables + root. The three fake-IP holes and the reload cursor fix are inherited and were not re-claimed |
| **Q04** | provider identity · URLTest result ownership · selector choice · physical socket | `NOT_COVERED` | the harness needs two local servers, a provider replacement with the same tag and a real dial through the selector; not built in this window. The prior round's ownership fix is the baseline, so only a *new* race would count |
| **Q05** | DNS cache · dual-stack candidate plan · network epoch · pool publish | **`NO_DEFECT`** for the joint chain (`7f90a8bea`) | **executed end to end for the first time**: real `dns.Client` cache → real `dialer.NewWithOptions` → two loopback listeners on one port. §2 |
| **Q06** | config reload · fake-IP issuance/retirement history · rule-set revision · pre-existing TUN flow | `NOT_OBSERVABLE` for the live-TUN half; the reload/history half is inherited | a live TUN flow cannot be created without a TUN device; the issuance-ledger seeding and the retired-generation refusal were closed in earlier rounds and are not re-counted |

## 2. Q05 — the chain that had never been run as a chain

The three-system rounds recorded "stale DNS cache × dual-stack dialing × new network generation" as
`NOT_COVERED` three times. The reason was structural: `dns` imports `common/dialer`, so a test in the
dialer package cannot import the DNS client, and nobody had built it in `dns`.

`dns/client_dial_candidate_test.go` (`7f90a8bea`) now does:

| Element | How it is real |
| --- | --- |
| the cache | the production `dns.Client`, `Start()`ed, with a scripted `adapter.DNSTransport` |
| the router seam | a thin `adapter.DNSRouter` that forwards into `Client.Lookup` (the real cache path) |
| the environment | an `adapter.NetworkManager` fake whose `NetworkEnvironment()` is the generation, which is exactly what the client reads for its cache namespace |
| the dialer | `dialer.NewWithOptions(..., RemoteIsDomain: true)` with no detour - the production candidate plan |
| the socket | two TCP listeners on **one port**, on 127.0.0.1 and 127.0.0.2; the resolver decides which one accepts |
| the transition | the answer changes, the generation advances, and `Client.refreshTransportEnvironments()` moves the transport pins the way `Router.ResetNetwork` does |

Two arms, and both are load-bearing:

- **Control**: with no generation change the second dial adds **no A query** and lands on the same
  listener. Without this arm, "the post-change dial used the new address" would also be true of a chain
  whose cache is never consulted.
- **Transition**: after the change the dial lands on the **second** listener with a fresh A query. The
  old listener stays at two accepts.

**Mutation**: commenting out the pin refresh leaves the dial on the old address and fails the
assertion that names it (`the dial after the network change landed on the old address: a cached answer
crossed the generation boundary`). That is what makes the test discriminating rather than decorative.

The in-flight half of the same question was already pinned by `cross_generation_answer_test.go` (B1:
an exchange answers from the network it was asked on; B3: an async delivery does not present a
superseded answer as success), so this window covered the sequential chain, not a re-run of those.

## 3. Why Q04 and Q06 are not claimed

- **Q04** is the most attractive remaining row: two local servers, a provider that replaces a node
  with the same tag, a selector that switches, and a real dial. It needs the provider manager, the
  group stack and the outbound registry in one harness, and the prior round already fixed the
  ownership rule it would attack. A new race has to be *designed* first (candidate: a URLTest result
  for the new UID arriving while the selector still points at the old node object), and this window
  did not design one that was not a re-run of the fixed rule.
- **Q06**'s live-flow half needs a TUN device; the config/history half is the inherited C-2 ledger
  work, and re-counting it would be a duplicate.

## 4. Executed

| Command | Result |
| --- | --- |
| `go test -count=1 -run 'TestTheRealCacheFeedsTheRealDialerAcrossANetworkChange' -v ./dns/` | PASS |
| `go test -count=1 ./dns/` | `ok` 8.63 s |
| `go test -count=1 -race ./dns/` | `ok` 9.91 s |
| mutation: skip the pin refresh | red with the generation-boundary message; restored |
