# Over-coupling reverse audit

The question is not "is there an abstraction" but "does it earn its keep, and does
it make one protocol's failure another protocol's problem". Each answer ends in
**keep**, **shrink**, **split** or **delete**, with the evidence that decided it.

## The 18 questions

1. **Does the benchmark runner know protocol internals?**
   No. Scenarios are data (JSON templates + metadata: protocol, transport,
   security, targets, reference-server flag); the runner switches on target
   *kind* (sink/source/echo/udp-echo) and load *pattern*, not on protocol. The
   one protocol-shaped rule is `run-baseline.sh` skipping XHTTP cells without
   `--xray`, which is expressed by the scenario's reference-server metadata.
   **Keep.**

2. **Does the JSON lose protocol-specific metrics?**
   Yes for QUIC (no PTO/loss/cwnd/qlog field), Cronet (no native heap), and
   WireGuard (no handshake time or queue depth). This is a known measurement
   gap, not a reason to widen the schema with per-protocol blobs: the generic
   fields are comparable across protocols, which is what the matrix is for.
   Protocol-specific evidence belongs in the debug captures (as the B7 goroutine
   dump does). **Keep, with the gap recorded.**

3. **Does the sampler force expensive diagnostics into the production hot path?**
   No. It reads `/proc` from outside the child; the binary exposes no hooks, no
   counters and no profile endpoint. The price is B3: Go heap and native memory
   cannot be split. **Keep.**

4. **Did collection logic enter a production hot path?**
   No production code was changed for measurement in any stage. `scripts/perf`
   is a separate Go module that imports none of the runtime packages. **Keep.**

5. **Does ResourceGuard own lifecycle it should not?**
   No. A permit bounds a creation slot and is released at dial return; it is
   attached to no socket, stream or session, and it does not close anything. The
   one component that *does* own buffer growth is `GrowthBudget`, and it lives in
   route's copy path, not in the guard. The measured cost matches the claim:
   an uncontended admit/release is 270.7 ns/op with 48 B and one allocation; even
   the synthetic one-slot/16-goroutine contention case is 1438 ns/op. **Keep.**

6. **Is RuntimeCoordinator a central big lock?**
   No. Its lock guards the epoch, the registration set and the change channel
   only; callbacks run after unlocking (`common/runtimecoord/coordinator.go:80-104`),
   and each registration has its own lock. `Close` follows the same discipline.
   **Keep.**

7. **Does the route manager know too many protocol details?**
   No type switches on protocol packages. It asserts capabilities
   (`IdleConnectionReleaser`, `ReuseSuspect`, `IdleConnectionKeeper`,
   `OnDemandEndpoint`) and, in the copy path, probes *buffer geometry* interfaces
   (`route/conn.go` comments about Naive's padded writer). The latter is
   transport-shape knowledge, not protocol knowledge, and replacing it with a
   per-protocol branch would be worse. **Keep**, and treat any new concrete type
   switch in route as a review failure.

8. **Are there duplicate session managers?**
   Four pools with genuinely different reuse rules: AnyTLS (external idle list),
   Mieru (SDK scheduler), TrustTunnel (one session + waiters), XHTTP (xmux usage
   counters). A common pool would need a least-common-denominator policy and
   would move correctness out of the code that can test it. **Keep separate**
   (stage-2 D-5); share only the creation primitive, which only TrustTunnel has.

9. **Are there duplicate retries or timeouts?**
   No stacked generic retry: TLS retries once for ECH, TrustTunnel's QUIC redo
   loop is driven by a hook that production never sets, Mieru's fallback is one
   deliberate second transport. Timeouts are layered (route dial deadline, TLS
   handshake timeout, QUIC handshake) but each layer has a distinct job and the
   caller's context is the outer bound. **Keep.**

10. **Are generations maintained in several layers?**
    Three counters use the word: the network epoch (`runtimecoord`), the XHTTP
    pool generation, and the connection manager's drain generation. They measure
    different things (network, pool boundary, tracked socket). The defect is
    naming, not behaviour: a reader can believe a protocol checks a generation it
    never reads. **Shrink by renaming/documenting** (S3-5), not by merging.

11. **Are there public interfaces with a single consumer?**
    `ReuseSuspect`: one consumer (the boundary walk), two implementations
    (XHTTP, TrustTunnel) — justified. `Registration.Stale`/`Acknowledge`: zero
    production consumers — **delete or wire** (F-13). `CacheMemoryReleaser`: one
    consumer (OOM killer) and several cache owners — justified.

12. **Are there no-op implementations?**
    Yes, and each is deliberate: `SetKeepIdleConnections(true)` on XHTTP has
    nothing to pre-warm; `SetMark` on the connected client bind has no socket
    option to set; `ClearSrc`/`SrcToString` are wireguard-go endpoint API that a
    connected socket cannot honour. No-op is acceptable when the contract says
    "may do nothing"; it is a defect when the caller reads it as "done". The
    stage-2 test pins the dangerous direction (tunnels must not implement the
    idle releaser). **Keep.**

13. **Does the runtime confuse native engines, QUIC sessions, TCP pools and tunnels?**
    The ownership matrix separates them. ResourceGuard treats them uniformly only
    as "one expensive creation"; that is a slot, not a lifetime, so the
    uniformity is sound. The place where the distinction matters — who may be
    suspended or idle-closed — is pinned by the capability inventory test.
    **Keep.**

14. **Is there cross-protocol failure propagation?**
    Yes, one instance: the fast-fail rate state lives in the single process-wide
    `Guard` (`common/resourceguard/guard.go:189`), and `ObserveOutcome` has no
    destination or outbound scope. Four fast failures from one dead server arm a
    delay for **every** admitted creation of the process, not just that server.
    On a phone that means one broken node can slow handshakes to working nodes.
    **Shrink**: key the rate state by seam+destination, or document the
    process-wide choice as intentional. Recorded as S3-7, not changed in this
    stage because it alters a policy the stage-1 measured failure was designed
    around.

15. **Did build tags get more complex?**
    No. The laboratory is a separate module with its own tag-free build; the
    production tag sets are untouched. **Keep.**

16. **Do tests introduce unclosable overhead?**
    No. The lab's tests run in its own module; production tests gained three
    regression tests and one concurrency test, all in-process and bounded.
    **Keep.**

17. **Did diagnostic events become per-packet locks or allocations?**
    No production diagnostics were added. The B7 instrumentation lived in a local
    copy of sing-tun and was not committed. **Keep.**

18. **Are metric labels high-cardinality?**
    The lab writes one JSON document per run; there is no long-lived metric
    registry, so no cardinality risk. **Keep.**

## Reverse audit of what the three stages added

| addition | what it couples | verdict |
|---|---|---|
| `scripts/perf` module | reads repo metadata, builds nothing into the binary | keep; separate module is the reason it cannot affect production |
| scenario templates | protocol knowledge lives in data, not code | keep |
| stage-2 fixes (xmux `closed`, rate clock, REALITY handoff, Mieru recheck) | one bool, one timestamp move, one error marker, one `TryRLock` | keep; each is smaller than the bug it fixes |
| `perflab -stagger` | laboratory only | keep; it is the diagnostic that turned B7 from a symptom into a location |
| three generations named alike | reader confusion | shrink (rename/document) |
| `Stale`/`Acknowledge` | dead contract | delete or wire |
| process-wide rate ledger | one node's failures slow every node | shrink (scope the ledger) |

Nothing in the reverse audit justified deleting a protection that stage 2 found
load-bearing. The two "delete" candidates are an API with no consumer and a
policy whose scope is too wide; neither is a lifecycle guard.

## New finding from this audit

**S3-7 — the fast-fail rate limit is process-wide, not per destination.**
`Guard` holds one `rateState`; `ObserveOutcome` records every seam's result into
it; `Acquire` applies `rate.delay` to every non-interactive creation. Four fast
failures against one server therefore delay TLS, AnyTLS, TrustTunnel and Mieru
creations aimed at other servers. The stage-1 failure this policy was built from
(201 concurrent handshakes to *one* failing destination) does not require global
scope. Status: `DEFERRED` — the fix is a keyed ledger plus a policy decision
about whether cross-destination damping is wanted on a memory-constrained
extension.
