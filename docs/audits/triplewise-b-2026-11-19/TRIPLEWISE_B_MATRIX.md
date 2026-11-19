# TRIPLEWISE B — Matrix

Window B of the three-system round: DNS / TUN / routing × dual-stack × protocol and network state. The
rows below are this window's grouping of the brief's themes; each names the three systems, the shared
state, why the third one is not optional, and what was actually executed.

Base: `testing` @ `649e29021`. Verdicts: `CONFIRMED` / `NOT_TRIGGERED` / `NOT_OBSERVABLE` /
`NOT_COVERED` / `NO_DEFECT` (attacked, coordination proven).

| ID | The three systems | Shared state / object | Why the third is not optional | Executed | Verdict |
| --- | --- | --- | --- | --- | --- |
| **TB-1** | ① fake-IP allocation & issuance history (`dns/transport/fakeip`) ② the TUN fast-path guard (`protocol/tun` `JudgeFlow`) ③ the router's restore/refusal (`route/route.go` `prepareMatchMetadata`) | the address→domain mapping and the issuance ledger | ② runs *before* ③ and decides whether ③ is consulted at all; ① is the only thing that can prove the address is ours | source read of all three + `go test ./protocol/tun ./route ./dns/...` (green) + the six fixes/tests listed in `TRIPLEWISE_B_WIRING_AND_LEAKS.md` §1 | **NO_DEFECT after three fixes** (windows A/B closed the real holes: out-of-range allocation, retired-generation placeholder, missing durable record) |
| **TB-2** | ① network transition (`NetworkManager.performTransitionLocked`) ② the shared HTTP client chain (`common/httpclient` `ManagedTransport`) ③ the protocol fallback state (H3 verdict ladder, H2→HTTP/1.1 pin) | the transport object and the authority-keyed verdict maps | ② is rebuilt only because ① calls `Router.ResetNetwork`; ③ would otherwise outlive the network that produced it | source read of the chain + `go test ./common/httpclient ./transport/trusttunnel ./transport/v2rayhttp` (green) | **NO_DEFECT** — the chain clears both verdicts; the residual is *silence*, recorded as window B's R-B3 |
| **TB-3** | ① DNS transport pinning (`Router.ResetNetwork` → `dns.Router.ResetNetwork`) ② the environment pin the transition publishes ③ a dial that resolves while the transition is in flight | the pinned resolver + the network the pin describes | ① and ② must move together; ③ is what reads them | source: the unconditional call is documented as the reason a pause must not gate it (`route/network.go:1684-1690`); `go test ./dns/... ./route` (green) | **NO_DEFECT** (by construction: the reset is unconditional and ordered with the epoch publication) |
| **TB-4** | ① cached A/AAAA answers ② the dual-stack candidate plan (`common/dialer`) ③ the generation a dial belongs to | the candidate set built from cached addresses | ③ is what refuses a plan built for the previous network; ①② alone would dial it | `go test ./common/dialer` (22 s, its own ~45-file dual-stack suite) | **NOT_COVERED as a triple** — the pair-wise behaviour is heavily covered, but a test that feeds a *stale cache* into a *new generation* plan was not built |
| **TB-5** | ① TUN UDP NAT entries (`sing-tun`) ② the outbound packet session (`route/conn.go`) ③ the admission gate | one UDP flow's chain of objects | ③ is the only process-wide budget; ①② are the cost | window A's audit (R-4/R-5) | **CONFIRMED, cross-repo, unfixed** — admission is absent on the path and eviction does not cancel the dial; fix needs the `sing-tun` fork + a pin whose evidence is a CI run |
| **TB-6** | ① rule matching on a restored domain ② the fake-IP NAT wrapper (`newFakeIPNATPacketConn`) ③ the flow's return path | the metadata's `OriginDestination` vs the packet's address | ③ is what must map replies of a rewritten destination back to the client | **five new contract tests** (`route/fakeip_conn_test.go`, the file had none): upload reported as the domain, then as the resolved address after `UpdateDestination`; download delivered from the fake address; a foreign reply normalised to the fake address; an address the app addressed itself stays direct (the full-cone case) | **NO_DEFECT — contract pinned** (this was `NOT_COVERED`; the round's real attack) |
| **TB-7** | ① auto_redirect (Linux nftables) ② TUN capture ③ the route-set/bypass decision | kernel rule ownership vs the flow verdict | ③ decides whether the platform sees the flow at all | — | **NOT_OBSERVABLE** — no Linux, no nftables, no privilege |
| **TB-8** | ① system proxy inbound ② TUN capture ③ the strict-route semantics | the same destination reachable by two capture paths | ③ is the policy that must not silently degrade to direct | — | **NOT_OBSERVABLE** (needs two live capture paths and a platform) |
| **TB-9** | ① MASQUE NAT64/PREF64 ② the DNS assignment/bootstrap ③ the QUIC path | the discovered prefix feeding address synthesis | ③ is what the synthesised address must actually reach | `go test ./protocol/masque ./transport/masque` (green) | **NOT_COVERED as a triple** — the in-package suites are green; the interleaving was not attacked |
| **TB-10** | ① `H3 only` policy ② the fallback decision ③ the error classification | the configured promise vs the demotion path | ③ decides whether ② may fire at all | window B's reading: `disable_version_fallback` builds no H2 path at all and the requirement becomes `AllOf UDP`; naive rejects `quic_fallback_to_h2` without `quic:true` | **NO_DEFECT (policy honoured)** — the *observability* of a legal demotion is R-B3 |
| **TB-11** | ① reload (close-then-build) ② the route/rule version ③ live connections | the Box object graph | ③ is what a reload destroys and what must not observe a half-installed graph | `go test ./daemon` (21.7 s green, incl. the reload/recovery tests) | **NO_DEFECT for consistency; the ordering cost is window B's R-B9** |
| **TB-12** | ① the rule walk ② the selector's real socket ③ the reported path status | the displayed node vs the socket actually used | ③ is what a user is told; ①② are what happens | window A's note on `physicalpath` (a detached view) | **NOT_COVERED** |

## Priorities, and what the priorities are based on

| Priority | Rows | Why |
| --- | --- | --- |
| P0 | TB-1, TB-5 | they sit on the production path for every TUN user, and both have a *silent* wrong outcome (wrong destination / an unbounded real cost) |
| P1 | TB-2, TB-3, TB-10 | coordination of the transition; a wrong answer is a degraded or failing transport after a network change |
| P2 | TB-4, TB-6, TB-11, TB-12 | real but self-correcting or diagnosable |
| P3 | TB-7, TB-8, TB-9 | platform- or harness-bound; nothing claimed |

## The colour of this window's result, stated plainly

No new defect was found in triple-B. The three P0 rows had already been closed by windows A/B — two of
them (TB-1's out-of-range allocation and retired-placeholder bypass) *were* genuine three-system
defects, found and fixed there, and this window verified that the assembled chain now holds. The one
P0 item still open (TB-5) is a dependency-layer fix blocked by the release-freeze pin evidence rule.
What this window adds is the wiring proof for TB-2/TB-3 (§2 of the wiring document), which was the
unverified half of window B's R-B3: the transition *does* reach the fallback state; it is the absence of
a diagnostic that remains.
