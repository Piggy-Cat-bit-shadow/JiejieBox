# Window B — pair matrix (DNS / Selector / routing / real data plane)

Routing table for the rows this window attacked. `REPORT.md` has the narrative and the six-stage
evidence; `REPRODUCE.md` has the commands. Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` /
`NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch `testing`, start `72c11f522` (the state the
  previous window left; its audit reports and fixes are untouched here)
- Platform: Windows amd64, Go 1.27.4; `-race` with mingw64 gcc; no Linux netns/nftables, no
  iOS/macOS device, no Windows TProxy driver in this environment
- Commits added by this window: `016f6c729` (hysteria2), `48db6678c` (anytls), plus these reports

## Summary

| Row | Systems (repo) | Shared object | Conclusion | Commit |
| --- | --- | --- | --- | --- |
| B-P14a | `common/runtimecoord.GenerationGate` (JiejieBox) × `protocol/hysteria2` single-client outbound | the gate's transition lock (`resetAccess`) and the owner's network-change action | **FIXED** | `016f6c729` |
| B-P14b | `common/runtimecoord` gate (JiejieBox) × `protocol/anytls` session pool (JiejieBox + `sing-anytls`) | the coordinator's epoch vs the pool's live sessions, at the dial hand-out and at the listener notification | **FIXED** | `48db6678c` |
| B-P14c | the previous round's R-B1 ("one owner of seven routes the listener half through the gate") | — | **NO_DEFECT at HEAD** (already fixed; stale finding corrected) | — |
| B-P01 | `common/urltest` × `protocol/group` selector | per-node health evidence, timestamps, selection | **NOT_COVERED** (no new attack this round) | — |
| B-P07 | `dns` cache × IPv4/IPv6 candidate plan | cached answers, family candidates, epochs | **NOT_COVERED** (suite executed, no new attack) | — |
| B-P08 | rule-set update × DNS cache | rule verdict vs cached resolution | **NOT_COVERED** | — |
| B-P10 | TProxy UDP × flow close/timeout | NAT entries, sockets, conntrack | **NOT_OBSERVABLE** on this host | — |
| B-P13 | `auto_redirect` × TUN capture | nftables/route rules | **NOT_OBSERVABLE** on this host | — |
| B-P15 | DNS hijack × local forwarding | the flow verdict vs the DNS module | **NOT_COVERED** at the capture layer; the verdict layer is heavily tested already | — |

## B-P14a — the gate action must not re-enter the gate (FIXED)

| Column | Value |
| --- | --- |
| Systems and repos | ① `common/runtimecoord.GenerationGate` (JiejieBox) ② `protocol/hysteria2`'s single-client outbound (JiejieBox) |
| Owners | ① owns when the transition runs and the lock that serialises it; ② owns what the transition does |
| Shared object | `resetAccess` (held across the action) and the action itself |
| Failure timeline | T1 `coordinator.Advance(N+1)` → T2 the listener phase calls `InterfaceUpdated` → T3 `NetworkChanged` → `applyTransition` takes `resetAccess` → T4 the action is `InterfaceUpdated` itself → `NetworkChanged` → `applyTransition` → the same goroutine blocks on `resetAccess` forever. The reset never runs; every later `BeforeDial` blocks behind the same mutex |
| Why exactly 2 systems | the gate decides when, the owner decides what; the defect is that the owner's "what" is the gate's entry point. Neither side alone can see it |
| Product path executed | `InterfaceUpdated` on the real outbound constructed by `NewOutbound` with a coordinator (the path `route/network.go`'s listener phase drives), plus the `BeforeDial` a demand calls first |
| Evidence before | `the listener phase (InterfaceUpdated) did not return within 2s: it is blocked on the generation gate's transition lock`; stack shows `applyTransition → NetworkChanged → InterfaceUpdated → applyTransition` |
| Evidence after | both tests green; the reset ran exactly once (`carrier.generation == 1`) and a following `BeforeDial` returns; full package `ok` 0.98 s, `-race` 2.32 s |
| Mutation control | restoring `created.InterfaceUpdated` as the gate action turns both tests red again |
| Uncovered boundary | whether any *future* owner wires a gated method as its action; the fix records the rule at the constructor |
| Commit | `016f6c729` |

## B-P14b — the AnyTLS pool was never migrated to the gate (FIXED)

| Column | Value |
| --- | --- |
| Systems and repos | ① `common/runtimecoord` epoch (JiejieBox daemon) ② `protocol/anytls` outbound (JiejieBox) ③ `sing-anytls` session pool (fork) |
| Owners | ① owns the generation; ② owns the listener and dial entry; ③ owns the sessions that must not be lent across generations |
| Shared object | the epoch at the hand-out, and the pool's live session set |
| Failure timeline | T1 `Advance(N+1)` → T2 a demand arrives before the listener phase → T3 `getSession`/`takeIdleSession` hands it the session built on the previous network → T4 `cmdSYN` goes into a path that is gone; TLS over TCP has no keepalive, so the failure surfaces only when the next read times out |
| Why exactly 2 systems | the epoch belongs to the daemon's coordinator; the session set belongs to the protocol pool. Only the owner can apply the transition to its own resource, and only the coordinator can say a transition happened |
| Product path executed | real outbound from `NewOutbound` with a coordinator in the constructor context, real pinned AnyTLS service on loopback TLS; sessions counted at the server by client source port |
| Evidence before (red) | dial without the gate: `the demand inside the publish-to-listener window reused the session built on the previous network` (one source port). listener without the gate: `the late listener notification cut the session the in-window demand built` |
| Evidence after | both green: the in-window demand opens a second session (two source ports) and the pre-change session refuses a write (the transition ran); the late listener leaves the new session carrying bytes. Full package `ok` 7.41 s, `-race` 8.58 s |
| Mutation controls | bypassing the gate in the dial half turns the first test red; bypassing it in the listener half turns the second red |
| Uncovered boundary | the UoT `ListenPacket` half is gated symmetrically but has no UoT end-to-end attack here; the other ungated class-A owners are listed in `REPORT.md` §5 |
| Commit | `48db6678c` |

## B-P14c — R-B1 is already fixed at HEAD (stale finding corrected)

| Column | Value |
| --- | --- |
| Claim checked | the previous round recorded that only `protocol/trusttunnel` routed the listener half through the gate, and that the other six owners took the listener call directly |
| Current source | all seven owners call `generation.NetworkChanged(ctx)` from `InterfaceUpdated`: trusttunnel, vless, vmess, trojan, hysteria2 (both paths), tuic, masque. `NetworkChanged` has no other call site in the tree |
| Conclusion | `NO_DEFECT` at HEAD; the finding was fixed by a later round ("six outbounds bypassed the GenerationGate push phase"). Not re-claimed as this window's work |
| Evidence | source inventory (`grep -n 'NetworkChanged('` across `protocol/`), plus the hysteria2 and anytls tests above |

## B-P01 — URLTest × Selector (NOT_COVERED as a new attack)

| Column | Value |
| --- | --- |
| Systems and repos | `common/urltest` (measurement, evidence, failover policy) × `protocol/group` (selector, loadbalance) |
| Shared object | a node's health evidence and the selection derived from it |
| What exists already | `common/urltest` has ~25 test files (evidence provenance, recheck handoff, stale evidence, round convergence, failover policy) and `protocol/group` has ~30, including `selector_pending_dial_test.go` and `physicalpath_edge_test.go` |
| Why it is not covered | a new attack needs a controlled probe server whose answers can be parked per node, plus assertions on which node the *socket* went to; this round spent its budget on the generation-gate pair instead |
| Next step | two scripted outbounds behind a gate that can be paused; drive "A selected → A probe times out → demand arrives" and require the demand's socket to reach B, with the late A result unable to overwrite B's win |

## B-P07 / B-P08 — DNS cache × family racing / rule update (NOT_COVERED)

| Column | Value |
| --- | --- |
| Existing coverage | `dns` has the epoch-bound cache (`source_binding_test.go` pins "an answer selected under generation 1 must not be relabelled by a later advance"), reverse-mapping atomicity, and the policy-epoch store guard |
| Why it is not covered | the row asks for a scripted DNS server feeding a *real* dial plan; the DNS side is exercised in-process but the candidate set was not pushed through `common/dialer` this round |
| Witness it would need | the real QNAME/server of the query, the candidate set the planner produced, and the SYN the dialer actually sent |

## B-P10 / B-P13 — TProxy, auto_redirect (NOT_OBSERVABLE)

Windows host: no netns, no nftables, no TProxy driver, no privilege to install rules. Nothing is
claimed about them; the static pairing (rule install/rollback owner) was not attacked either.

## B-P15 — DNS hijack × local forwarding (NOT_COVERED at the capture layer)

The verdict layer (which flow is hijacked, at which address/port, ahead of the direct fast path) is
densely pinned already (`direct_fast_path_dns_test.go`, `judge_flow_truth_table_test.go`,
`l0_authoritative_vs_reverse_mapping_test.go`), and that is why this row is not a source-only guess:
the remaining gap is the *capture* half (system resolver, DoH/DoT, loopback), which needs a platform
this host does not have.
