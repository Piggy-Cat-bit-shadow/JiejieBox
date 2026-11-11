# Final report

> Stage 3 of the performance/coupling programme. The report follows the requested
> order; the evidence lives in [`DEBUG-REPORT.md`](DEBUG-REPORT.md),
> [`ISSUE-LEDGER.md`](ISSUE-LEDGER.md),
> [`OVER-COUPLING.md`](OVER-COUPLING.md),
> [`PERFORMANCE.md`](PERFORMANCE.md) and [`evidence/`](evidence/).

## 1. Latest HEAD, branch and worktree

- Branch `work/coupling-audit-2026-11-11`.
- HEAD `f30e36a4dea` — `merge: bring the coupling-audit branch onto the current
  testing tip`. The merge was necessary: the remote deleted
  `integration/0.1.45-final` during the work and `testing` advanced 15 commits
  (hidden-group feature, Apple submodule pin, release locks, docs). Audit and
  measurements were re-run on the merged tree.
- The worktree is clean at commit time. No reset, no force-push, no branch
  deletion; the stage-1/2 commits are intact.

## 2. Earlier stages and their commits

| stage | commits | head |
|---|---|---|
| 1 — laboratory + baseline | `bf0e0bec560`, `88833270fec`, `67b2238ffe8`, `49679c0e927`, `3fb1c36233d`, `53a9f5dc8e2` | 25 runs, matrix, failures |
| 2 — runtime coupling audit | `992087a57bb`, `47288906810`, `cde28636176`, `55aca33e4a2` | ownership matrix, 17 findings, 4 fixes |
| 3 — this audit | see §13 | final report, evidence, parser fix |

## 3. Debug scope

Attacked: the laboratory's own reliability (provenance, sampling, definitions);
ResourceGuard accounting under storms and cancellation; the four session pools;
RuntimeCoordinator generations and leases; OOM/MemoryPressure; DNS/dual stack;
TrustTunnel UDP2 budgets; MASQUE datagram boundaries; WireGuard/AWG lifecycle and
the stage-1 stall (B7); Mieru/Naive/Cronet lifecycles; and the reverse-coupling
question for every abstraction the programme added. Not attacked because the host
cannot: real NE/Android/Windows runtimes, privileged TUN, native memory
attribution, qlog.

## 4. Issue counts and severity

31 ledger entries:

| status | count |
|---|---|
| FIXED_AND_VERIFIED | 5 |
| CONFIRMED_NOT_A_BUG | 6 |
| MITIGATED | 2 |
| DEFERRED | 12 |
| BLOCKED_BY_ENVIRONMENT | 5 |
| UNRESOLVED | 1 |

Severity: two High (B7, F-01), the rest Medium/Low/Info. The two High items are
both `DEFERRED`: B7's fix belongs to the wireguard-go fork, and F-01 is a product
decision about device-pause semantics. Neither is presented as done. The one
`UNRESOLVED` item is the Mieru idle-underlay recovery race found by this stage.

## 5. The eight protocols

| protocol | conclusion | status |
|---|---|---|
| VLESS / REALITY / Vision | Admission through the shared TLS seam; REALITY's camouflage fallback now owns its socket; no throughput or lifecycle defect found | F-08 fixed |
| AnyTLS | Healthy pool; the remaining defect is that a pure reuse is charged a creation permit while the gate is saturated | F-06 deferred |
| Mieru | Generation recheck now covers the single-transport path; FD growth under connect churn is reuse retention, not proven leak; a new idle-underlay recovery race appears under `-race` | F-07 fixed, S3-2 deferred, S3-9 unresolved |
| TrustTunnel | The reference shared-offer implementation; UDP2 budgets and close/idle invariants pass under race | no defect |
| XHTTP | Pool close race fixed; H3 still has its own QUIC/HTTP3 ladder rather than the shared one | F-03 fixed, duplication documented |
| MASQUE | Datagram/MTU/close boundaries covered; the client endpoint still has no `Close` and the duplicate-tag loser leaks its HTTP client | F-05 deferred |
| Naive / Cronet | Engine per outbound, not rebuilt on network change; native footprint measured (25–26 threads, 22–23 FDs, +~9 MiB RSS); cgo path blocked | B2 mitigated, limits recorded |
| AWG / WireGuard | Lifecycle/rebind/pause tests pass; the concurrent-flow stall's root cause is a send-path accounting deadlock in the wireguard-go fork | B7 deferred |

## 6. ResourceGuard

No permit leak, double release or deadlock found. The invariant
`Inflight == Admitted - Released` is fuzzed on every `go test`; repeated pressure
cycles recover the limit and shed exactly once; the stage-2 rate-window fix is
pinned by a regression test. **New finding S3-7**: the fast-fail rate state is
process-wide, so four fast failures against one dead server delay creations aimed
at every server; that is a scope defect in an otherwise correct mechanism.

## 7. RuntimeCoordinator

The lock discipline is correct: bookkeeping under the lock, callbacks outside,
the change channel closed-and-replaced (one transition per reader). Leases are
single-flight per window, cancelled by generation change or Close. The dead
`Stale`/`Acknowledge` API has no consumer (F-13). The only live gap is the 6 s
listener window (F-15): a dial inside it can reuse a pool that has not been reset,
and no protocol reads the epoch at hand-out.

## 8. Route, pools and on-demand tunnels

The three close semantics remain correctly separated: release idle, refuse new
work and drain, suspend a tunnel. The dangerous direction is pinned by the
inventory test (tunnels must not become idle-releasable). F-01 remains the one
place where a lifecycle action can cut live traffic (device pause suspending a
busy tunnel); it needs a product decision. Reclaim returns after staging disposal
and drops close errors, which is documented and does not affect `CloseAll`.

## 9. DNS and dual stack

The shared dial layer plans candidates, resolves within the dial, and rejects a
socket that crossed a network generation. MASQUE/Mieru/Cronet/WireGuard bypass
the shared planner for reasons forced by their native/QUIC lifecycles; the cost is
consistency, not a defect found in the shared path (F-16).

## 10. OOM, native resources and Cronet

Recovery creation is throttled, never blocked; no active stream can be closed by
the memory path. The gap is that the three owners with mostly native memory
(MASQUE's stack, WireGuard's device, Cronet's engines) have no shed hook, and the
host cannot attribute their memory: `go_heap_peak_bytes` is null because the
production binary has no pprof endpoint, and PSS/cgroup accounting is not
available in this container. B3/S3-3 stay `BLOCKED_BY_ENVIRONMENT`.

## 11. Over-coupling

Eighteen questions answered with keep/shrink/split/delete in
[`OVER-COUPLING.md`](OVER-COUPLING.md). Nothing the programme added was found to
force an unnecessary abstraction; the laboratory is a separate module and the
stage-2 fixes are smaller than their bugs. Three items should shrink rather than
grow: the process-wide rate ledger (S3-7), the dead registration-staleness API
(F-13), and the three different counters called "generation" (S3-5).

## 12. Performance regression

**No regression.** The 3-repetition round produced a few apparent drops, and all of
them moved back to the baseline when the cell was re-run with 5 repetitions:
AnyTLS 4-stream upload 529.6 → 769.4 (baseline 750.3), Mieru 4-stream download
616.2 → 689.4 (baseline 701.3), REALITY 4-stream download 1173.6 → 1455.5
(baseline 1365.1). Those were host spread on a 3-repetition median, not code.

Several cells are faster than the stage-1 baseline beyond that spread (XHTTP H2/H3
download, Naive H2/H3, TrustTunnel H3 download, MASQUE). No commit in this branch
touches those data paths, so the conservative reading is host-state drift between
the baseline day and this run; the report claims no improvement. Full table,
CPU analysis and the reasons CPU-per-GiB is not comparable where payload rate
moved: [`PERFORMANCE.md`](PERFORMANCE.md). Raw runs:
[`results/final/`](results/final/) and [`results/final2/`](results/final2/).

## 13. Fixes and commits

- `11ff9575da0` — `fix(perf): record the replaced dependency pins and test the
  sampler` (S3-1 provenance fix + sampler invariants; two test files).
- this directory's commit — the final audit report, ledger, debug report,
  over-coupling verdict, performance report, evidence and exported runs, plus
  the `docs/perf/ENVIRONMENT.md` pin correction.

No kernel source was changed in stage 3. The stage-2 fixes remain the only
production changes on the branch, and every one is pinned by a regression test.

## 14. Race, stress, integration, cross-platform

**Race / stress.** `common/resourceguard` passes `-race -count=3`; its fuzz
target runs 36 650 executions in 25 s; `common/runtimecoord`,
`transport/trusttunnel`, `protocol/masque`, `transport/wireguard` pass under
`-race`. **One failure:** `protocol/mieru` fails
`TestMieruClientObservesServerClosingAnIdleUnderlay` 1/3 under `-race` (passes
3/3 without), recorded as S3-9 / `UNRESOLVED` with the capture in
[`evidence/mieru-idle-underlay-race.txt`](evidence/mieru-idle-underlay-race.txt).

**Profiles.** ResourceGuard: 270.7 ns/op uncontended (48 B, 1 alloc), 1438 ns/op
contended (6 allocs); CPU/mutex profiles show the queue's own handoff, not a
hidden lock ([`evidence/resourceguard-profile.txt`](evidence/resourceguard-profile.txt)).

**Integration.** 12-scenario matrix + 5-scenario re-run (17 runs) exported under
[`results/`](results/); B7 cell reproduced and the `-stagger 1s` control passes.

**Cross-platform builds.** linux/amd64, linux/arm64, windows/amd64, darwin/arm64
PASS with the laboratory tag set; android/arm64 blocked by the missing NDK
(`purego` needs cgo `dlopen` there). Runtime cells on those platforms remain
blocked (B6).

## 15. Unresolved

- **B7** — root cause known (wireguard-go fork `queuedOutboundPackets` accounting /
  staged-packet flush), fix not applied because it is a dependency change; the
  in-repo workaround would hide loss. `DEFERRED`.
- **F-01** — device-pause semantics need a product decision. `DEFERRED`.
- **S3-9** — Mieru idle-underlay recovery race: the new dial can pick the
  underlay the server just closed; reproduced under `-race` (1/3), product-level
  retry semantics needed. `UNRESOLVED`.
- **F-05, F-06, F-07b, F-10, F-13, F-14, F-15, S3-2, S3-5, S3-7** — each has a
  root cause and a next step in the ledger; none has a source fix in this branch.
- **B3/B4/B5/B6/S3-3** — host limits: no pprof/native attribution, no root, no
  external tools, no Apple/Android/Windows runtime.

## 16. Suitability for final review

The branch is suitable for review as a **measurement and audit** deliverable and
as a **small, tested set of lifecycle fixes**. It is not a claim that the runtime
is defect-free: two High-severity items remain deferred for reasons outside this
repository (a dependency's send-path accounting and a product decision about
device pause). Every other open item is classified, evidenced and assigned a next
step. The laboratory can reproduce every number and every failure in this report.

## 17. Next steps

1. Patch the wireguard-go fork: flush staged containers on keypair install and
   audit the counter; re-run the B7 cell and the AWG variant.
2. Decide F-01 (defer suspend until the tunnel is idle, or accept the teardown)
   and add the policy test either way.
3. Scope the rate ledger per destination (S3-7) or document the process-wide
   choice as intentional.
4. Delete or wire `Registration.Stale`/`Acknowledge` (F-13) together with the
   epoch-at-pool-hand-out change (F-15).
5. Add a pprof endpoint behind an option, or accept RSS-only attribution forever;
   without it B3 cannot be closed.
6. Run the Apple/Android/Windows cells on their own hosts; nothing in this report
   substitutes for them.
