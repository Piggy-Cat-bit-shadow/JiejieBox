# Reports — audit, verification and close-out archive

This directory holds the point-in-time reports produced for this fork: architecture audits, adversarial
verification rounds, release close-outs and client-platform investigations. They are kept here — and not at
the repository root where most of them used to live — so that one index answers "what has been audited, when,
and against which tree".

## The rule that keeps this directory honest

> **A report is a RECORD of what was checked at a moment, not a claim about the current tree.**

- `BASELINE.md` owns "which SHA the knowledge base describes"; `INDEX.md` and `modules/` own the current
  claims. A report never overrides source, and it never overrides those files.
- When a report and the source disagree, **the source wins** and the disagreement is a defect to record —
  usually by committing a correction into the report itself (see `convergence/POST_REFACTOR_AUDIT_2026-10-17.md`,
  whose first line records that it corrected a construction report after finding a regression that report
  had introduced).
- Adding a report **does not move the baseline** and is **not** a knowledge-base refresh. It is an archive
  entry, nothing more.
- Each report states its own evidence level where it has one. **Where a report does not state one, treat it as
  no stronger than `VERIFIED_SOURCE`.** A workflow declaration is not evidence of execution. Later
  independently inspected CI runs are linked in the current CI and Windows daemon modules and are
  scoped to their actual commits; they do not upgrade every archived report or release artifact.

`-race`, device behaviour and cross-platform guarantees are recorded per report. Where a report says they were
not run, that is `BLOCKED_BY_ENVIRONMENT`, not `PASS`.

## Layout

| directory | what belongs there |
|---|---|
| `convergence/` | this branch's own work: architecture convergence, the adversarial audit of it, and the narrow-scope audits that followed |
| `core/` | audits of the core packages (root test hygiene, `experimental/boxdd`, CI/release configuration, knowledge-base delta mapping). Files are prefixed `_` where they are working notes rather than formal deliverables |
| `release/` | release phases, branch consolidation, handoffs and close-outs, in chronological order of the phase names |
| `windows-client/` | the Windows client's installer / uninstaller / diagnostic-channel and coexistence investigations |
| `anywhere-round2/` | the second Anywhere research round: audit, construction, adversarial debug and final verification for the DNS/rule-set/MASQUE/QUIC ownership work |
| `anywhere-round3/` | the third Anywhere round: an independent deep audit of four previously-favoured candidates (three falsified by local measurement), two new A-class candidates, two confirmed defects and the next-round blueprint, followed by the construction record and the debug handoff that turned the A-class items into code |
| `tun-fast-path/` | the TUN fast path round: the D0 value review and the adversarial acceptance, archived as produced |

## Index

Dates are the commit date for a report that was already tracked, and the file's own date for one archived
later. "Status" says whether the report still describes the tree or has been superseded.

### convergence/

| report | date | covers | status |
|---|---|---|---|
| `ARCHITECTURE_CONVERGENCE_2026-10-17.md` | 2026-10-17 | the 11-commit convergence round: dialer capability surfaces at construction time, one network observation per operation, bypass/userspace parity, L3 production-graph tests, bounded `NetworkManager` growth. **Corrected** after the audit: D10/D11 describe the final design, with the superseded approach kept for traceability | current, as corrected |
| `POST_REFACTOR_AUDIT_2026-10-17.md` | 2026-10-17 | adversarial audit of that round: one production regression the round introduced (a repo-wide capability protocol answered by an outbound, which moved a WireGuard bind), one repair that did not repair what it claimed, one guard tested only by setting a field | current |
| `POST_REFACTOR_AUDIT_ADDENDUM_2026-10-17.md` | 2026-10-17 | the terminal state of every coverage gap the audit opened, the fail-open / fail-closed inventory, and the `common/runtimecoord` boundary verdict | current |
| `RULE_SET_LIFECYCLE_AUDIT.md` | 2026-10-17 | narrow-scope audit of rule-set lifecycle: ownership map, reload teardown, updater/watcher/goroutine exit, GeoIP/GeoSite reachability. Conclusion: no defect, seven wiring tests added | current |

### core/

| report | date | covers | status |
|---|---|---|---|
| `_root_test_audit.md` | 2026-10-14 | root-package test hygiene, read-only | historical |
| `_boxdd_audit.md` | 2026-10-14 | `experimental/boxdd` at `8a008c5d02d2` | historical |
| `_ci_reaudit.md` | 2026-10-14 | `ci-release` re-audit at `8a008c5d02d2` | historical |
| `_kb_delta_map.md` | 2026-10-14 | knowledge-base delta `a267e99e0 -> 5fc4f9366` | superseded by later baselines |
| `HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md` | 2026-10-17 | large-configuration cost at `42222237d`: the rule-set object-cost table to 1M domains (steady 8.3 MiB, load 42.5 MiB), the load-transient stage breakdown, the cold-node census (2 035 B and no thread each), the outbound static-vs-runtime and background-worker inventories, and the two changes they justified — the optimistic DNS refresh and the dashboard download now obey `common/power` | current |
| `HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` | 2026-10-17 | adversarial audit of that round at `e092ecec7`: the two sleep gates (every governor state including closed, the in-flight registry under refusal, the teardown of a parked loop, the factory wiring), one structural hardening (the DNS governor resolved at construction instead of in `Start`), five sensitivity mutations, an A/B showing no hot-path cost, and an explicit `BLOCKED_BY_ENVIRONMENT` on `-race` | current |
| `NETWORK_CAPABILITY_TRANSITION_PHASE01_2026-10-18.md` | 2026-10-18 | phase 01 of the four-phase network-capability-transition round at `f868a8fb4`: the existing transition event graph and its two asymmetric arrows, the single capability authority (`route.NetworkManager` → `netcap` → `adapter.NetworkFamilyCapabilitySnapshotter` → `common/dialer`), generation and stability semantics and why no second epoch is added, the full transition matrix (including the same-interface route case that deliberately does not bump the generation), flow capture, familyHealth's epoch-keyed scope, setup/recovery ordering, the reclaim and DNS-cache boundaries, hot-path benchmarks, **19 mutations of which 2 survived and are now killed by the two tests this round added**, and seven recorded findings including a medium Darwin risk. No production file changed | current |
| `CANDIDATE_ADMISSION_FLOW_LIFECYCLE_PHASE02_2026-10-18.md` | 2026-10-18 | phase 02 at `c449256be3`: the pre-existing decision+lifecycle graph (one literal flow took **five** readings of the network), the per-flow `admissionPolicy` (strategy + capability + one generation) and its single capture point per entry, the capture, lifetime and refusal-model rules, the five deliberately-retained stage gates, the **live defect it found and fixed** (the unconnected-packet domain arm applied no policy at all and created a UDP socket for a family the operator forbade, reported as success), the two generation counters that are never compared, **15 mutations — 11 killed, 3 proved equivalent, 1 invalid first form corrected**, hot-path benchmarks (`admits` 11–22 ns, zero allocations), and the PHASE 02 handoff incl. the dynamic questions reserved for 04 | current |
| `CANDIDATE_ADMISSION_FLOW_LIFECYCLE_PHASE03_DEBUG_2026-10-18.md` | 2026-10-18 | phase 03 (single-flow debug) at `4e86eae6df7`: the capture-count instrumentation that measures captures at the **owner seam** (the only seam that separates a capture from an ownership check), the **duplicate capture it found and fixed** — Phase 02's serial branch took a second `captureAdmissionPolicy()`, shadowed by an inner `:=`, invisible to 42 call-site migrations and a 15-mutation campaign — the corrected premise of §3 and the structural fact it exposed (**the epoch cannot move without a lifecycle cancel**, so only the capability term can drift under a live flow), the 200-pair × 6-stage cross-stage consistency matrix, 4 new mutations all killed, an honest table of which attack surfaces were **already** covered before this round, and the PHASE 03 handoff with five risks for 04 | current |
| `NETWORK_CAPABILITY_TRANSITION_PHASE04_FINAL_2026-10-18.md` | 2026-10-18 | phase 04 and the **four-phase acceptance** at `d385f5dff17`: an explicit table of what this environment can and cannot prove (deterministic producer+consumer phases yes; `-race`, real OS event ordering and 1000-flow stress no), the phase-driven A→B run through the real consumer (generation 1 IPv4-only → generation 2 dual-stack → a **capability-only route flap under an unchanged epoch**), Settling safety, familyHealth re-keying, 100 flaps with the goroutine count held, the brief's 23 sections mapped to the file or test that supports each, the one open item (a cancelled late candidate is still launched, without effect), four of the author's own assertions that were wrong and what each taught, and the **FOUR-PHASE FINAL VERDICT** with its condition of PASS stated as five things not claimed | current |
| `NAIVEPROXY_DEEP_AUDIT_2026-10-20.md` | 2026-10-20 | deep audit of the Cronet native stack and the Naive integration at `7e42ca38e`: the packaged native build is Chromium **154.0.8037.49** rather than the `143.0.7499.109` module string, protocol choice is per-CONNECT (`-force-quic`) rather than per-engine, forced H3 against an unreachable UDP path stops on Chromium's ~4.0 s budget, the idle engine-count costs (1 engine 15.9 MiB/17 threads vs 4 engines 16.4 MiB/23), the DoH bootstrap socket that bypasses the DNS policy, a byte-for-byte padding parity check against Caddy forwardproxy, and the `KEEP` verdict for the Go server; interop and WAN rows are recorded as `NOT_RUN` | current |
| `NAIVEPROXY_PERFORMANCE_INTEGRATION_IMPLEMENTATION_2026-10-20.md` | 2026-10-20 | construction round from `11c4c1b27` to `0ef38b8ff`: P1 makes the outbound finish CONNECT (`DialEarly → HandshakeContext`) and map narrow `FailureFacts`, which also fixed a defect bigger than the audit described (407 was never an error); P3 adds the opt-in per-CONNECT H3→H2 fallback; P9 adds the zero-padding regression; P7 corrects the documented congestion-control default; P2 is `BLOCKED_BY_NATIVE_API`; records the measured `+0.519 ms` loopback cost of the CONNECT handshake | current |
| `NAIVEPROXY_PERFORMANCE_INTEGRATION_DEBUG_2026-10-20.md` | 2026-10-20 | adversarial debug and acceptance of that round: the H1–H17 attack matrix for the fallback (per-CONNECT selection, ambiguous probe expiry, 407, out-of-order outcomes, 64-way concurrent failure, network-change reset), the two bugs it fixed (`h3ProbeContext` budget split so the H2 retry can succeed; the NetError-versus-teardown classification order), `-race` over the verdict, and a `READY_WITH_LIMITATIONS` verdict with WAN/macOS/Alt-Svc listed as unverified | current |
| `DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md` | 2026-10-20 | dual-stack and cross-component read-only audit at `b721b988f` (v0.1.21) plus a measured delta to the remote tip: acceptance of the admission-authority convergence, two production call graphs, the state-ownership table, DNS/Fake-IP/family-policy matrices, candidate discovery and scheduling, family health, the success definition per TCP/UDP/QUIC, the transition lifecycle, a census of 29 outbound/endpoint objects, and the Mihomo/Xray/upstream comparison. 1 `CONFIRMED_BUG` (the streaming hostname arm loses the local-family refusal and reports `no dial candidates`; reproduced with an out-of-tree probe), 2 `HIGH_CONFIDENCE_RISK` (group outbounds do not forward the candidate-planning surface; the recovery stream producer is not joined), 7 `ALREADY_FIXED`. `-race` became available on this host for the first time (MSYS2 mingw gcc 16.2.0) and passed | current |
| `DUAL_STACK_CROSS_COMPONENT_CLOSURE_2026-10-20.md` | 2026-10-20 | closure round: DS-01 re-verified against the audited baseline (not reproducible, already correct), DS-02 serial-dial cost measured and left unchanged with the reason, DS-03 producers already joined with the three-resolver matrix, the ssh unlocked-close bug fixed with a mutation-verified race test, all 17 InterfaceUpdated implementations tabulated, DS-04 attempt counts and DS-05 boundary | current for the delivered head |
| `DS03_RECOVERY_PRODUCER_CLOSURE_2026-10-20.md` | 2026-10-20 | DS-03 specialist closure: ownership tables for the literal and parallel-network recovery chains, the inner LookupFamilies goroutine measured under cooperative, slow-cooperative and cancel-ignoring resolvers, verdict NO_PRODUCTION_CHANGE with the contract boundary stated | current for the delivered head |
| `STARTUP_VALIDATION_COMPAT_CONSTRUCTION.md` | 2026-10-20 | start-time validation compatibility: capability findings (exit/group/detour cannot carry a routed network) warn and start, structural findings (missing member, identity collision, unfulfilled promise) still refuse; 17 checked surfaces, the incident shape, tests and limits | current for the delivered head |
| `STARTUP_VALIDATION_COMPAT_DEBUG.md` | 2026-10-21 | adversarial debug of the construction: the attack matrix with START/WARN/RUNTIME verdicts, the fatal cases that stayed fatal, the message defect found and fixed, and the runtime/CI limits | current for the delivered head |
| `NAIVEPROXY_PERFORMANCE_READONLY_AUDIT_2026-10-20.md` | 2026-10-20 | performance re-audit at `5b46c36d3` with a real native end-to-end harness (Chromium 154 client + Go server, loopback, 5 rounds each, payload hash verified): H2 457.7 MiB/s at 5.5 CPU·s/GiB versus H3 158.2 at 17.0; the box alone 868.7 and raw TCP 3285.7; write-path counters show 100 % `writeBufferWithPadding`, 523 writes **and** 523 flushes per 32 MiB (64 158 B each, zero slow-path calls); one H2 session serves six sequential CONNECTs; short requests 814 req/s (p50 1.02 ms); the engine sweep 1/2/4/8 puts the sweet spot at 2 (665 MiB/s) and reverses beyond it; the H3→H2 fallback costs one ~4.0 s cold CONNECT and nothing afterwards. One `CONFIRMED` test defect (`TestNaiveSelfInsecureConcurrency` counts a stale NetLog id 249 while the pinned build uses 267, so the pool-isolation invariant is not actually verified) and one documentation defect (stale historical numbers). Server verdict: `KEEP / STOP` | current |

### release/

| report | date | covers | status |
|---|---|---|---|
| `APPLE-PARENT-FINALIZE-REPORT.md` | 2026-10-11 | Apple parent finalization | historical |
| `B_GIT_EVOLUTION_REPORT.md` | 2026-10-12 | git evolution / provenance of the Android fork | historical |
| `BRANCH_CONSOLIDATION_TO_TESTING_FINAL.md` | 2026-10-12 | branch consolidation onto `testing` | historical |
| `CORE_0.1.6_FINAL_CLOSURE_REPORT.md` | 2026-10-12 | 0.1.6 integration closure | historical |
| `GROUP_CROSSKIND_BRIDGE_CLOSURE.md` | 2026-10-12 | group fallback / cross-kind / bridge closure | historical |
| `ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` | 2026-10-12 | route-aware outbound capability validation | historical |
| `CI_RELEASE_POST_GREEN_OPTIMIZATION_REPORT.md` | 2026-10-14 | CI/release optimization after green | historical |
| `PHASE1_INTEGRATION_REPORT.md` | 2026-10-16 | phase 1 topology audit, merge and close-out | historical |
| `PHASE1_INTEGRATION_REPORT_ADDENDUM.md` | 2026-10-16 | final hashes, publication and the CI caveat | historical |
| `PHASE2_MERGED_STATE_AUDIT.md` | 2026-10-16 | phase 2 merged-state audit and regression | historical |
| `PHASE3_COMBINATION_DEBUG.md` | 2026-10-16 | phase 3 combination debugging of the integrated tree | historical |
| `PHASE4_ACTIONS_RELEASE_AUDIT.md` | 2026-10-16 | phase 4 CI/release differential audit since v0.1.10 | historical |
| `PHASE5_BISECT_BLOCKED.md` | 2026-10-16 | installer bisect blocked by a release-identity constraint | historical |
| `PHASE5_GATE6_DIAGNOSIS.md` | 2026-10-16 | gate 6 diagnosed as a product regression, isolated by control experiment | historical |
| `PHASE5_RELEASE_HELD.md` | 2026-10-16 | release held at the pre-publication gate | historical |
| `PHASE5_V0.1.15_CORE_FIX_STATUS.md` | 2026-10-16 | v0.1.15 core swallow-error fix in, gate still failing | historical |
| `PHASE5_V0.1.15_HANDOFF.md` | 2026-10-16 | v0.1.15 handoff | historical |
| `PHASE5_V0.1.15_STATUS.md` | 2026-10-16 | v0.1.15 status | historical |
| `PHASE5_V0.1.15_UNINSTALL_VERDICT.md` | 2026-10-16 | the uninstall settle measured as a product bug | historical |
| `PHASE5_FINISH_ASSESSMENT.md` | 2026-10-17 | release finish: the gate-6 comparison is confounded, and the hang is real | historical |
| `V018_RELEASE_HANDOFF.md` | 2026-10-14 | v0.1.8 release handoff | historical |
| `V0.1.16_BRANCH_CONSOLIDATION_RELEASE_CLOSEOUT.md` | 2026-10-17 | v0.1.16 consolidation and release close-out | historical |
| `LX_BUG_ORACLE_ROUND2_ADDENDUM.md` | 2026-10-15 | LX bug-oracle round 2 addendum | historical |

### windows-client/

| report | date | covers | status |
|---|---|---|---|
| `WINDOWS_AUDIT_STATUS.md` | 2026-10-16 | Windows client audit, status at handoff | historical |
| `WINDOWS_AUDIT_P0_STATUS.md` | 2026-10-16 | P0-1 … P0-4 status, one confirmed real bug | historical |
| `WINDOWS_AUDIT_P04_STATUS.md` | 2026-10-16 | P0-4 hardening implemented, blocked by a stricter patch parser | historical |
| `WINDOWS_AUDIT_P04_COMPLETE.md` | 2026-10-16 | P0-4 complete, verified in the generated NSIS | historical |
| `WINDOWS_AUDIT_DIAGNOSTIC_CHANNEL.md` | 2026-10-16 | the uninstaller writes nothing and the file never grows | historical |
| `WINDOWS_AUDIT_CHANNEL_FIXED.md` | 2026-10-16 | diagnostic channel fixed; the uninstaller still silent | historical |
| `WINDOWS_AUDIT_FIRST_UNINSTALL.md` | 2026-10-16 | first-uninstall trace: option passed, `customUnInit` invoked, nothing written | historical |
| `WINDOWS_AUDIT_UNINSTALLER_PROVENANCE.md` | 2026-10-16 | uninstaller provenance capture, hypothesis untested | historical |
| `WINDOWS_COEXISTENCE_SUBSCRIPTION_DEBUG_REPORT.md` | 2026-10-15 | Windows coexistence + subscription traffic: targeted debug | historical |
| `WINDOWS_COEXISTENCE_SUBSCRIPTION_FINAL_REPORT.md` | 2026-10-15 | Windows coexistence + subscription traffic: delivery report | historical |
| `WINDOWS_COEXISTENCE_FINAL_CLOSEOUT_REPORT.md` | 2026-10-15 | Windows coexistence close-out | historical |

### anywhere-round2/

| report | date | covers | status |
|---|---|---|---|
| `README.md` | 2026-10-19 | the round's executive index, dependency pins and what was delivered | current |
| `anywhere-second-round-research.md` | 2026-10-19 | independent audit: DNS cache ownership, TUN→QUIC data paths, QUIC ACK/retransmission, queues/budgets, large rule-set loading, A/B/C/D matrix | historical (describes `ccc2812e45d`) |
| `anywhere-round2-construction.md` | 2026-10-19 | construction matrix: A0/A1/B2–B8, commits, before/after measurements, rollback | current for the delivered head |
| `anywhere-round2-debug-audit.md` | 2026-10-19 | Red→Green proofs, adversarial and cross-component tests, race/platform limits | current for the delivered head |
| `anywhere-round2-final-summary.md` | 2026-10-19 | commits, dependency pins, measured results, CI links, residual risks | current for the delivered head |

### anywhere-round3/

| report | date | covers | status |
|---|---|---|---|
| `README.md` | 2026-10-20 | the round's executive index: what was falsified, what is worth building, and what was not run | current |
| `anywhere-round3-independent-deep-audit.md` | 2026-10-20 | independent audit at `5064f20a9`: the four brief candidates measured (TCP `PacketTemplate`, XHTTP gather polling, HY2/TUIC batching and the TUN batch grouping), the two rejected premises, the two new A-class candidates (`portReturn.ReturnPackets` per-call allocation, L4 CONNECT-UDP missing the owned DATAGRAM path), two confirmed defects (`equalFoldASCII` fold base, `domain_keyword` not lowercased), the `ForwardStage.Flush` lock-across-blocking-enqueue liveness risk, seven autonomous findings, the measured microbenchmark log and the next-round blueprint | current for `5064f20a9` — **not** a claim about later HEADs |
| `anywhere-round3-construction.md` | 2026-10-20 | the construction record: A3-1 `ReturnPackets` (2 → 0 allocs, with the Red→Green against the pre-change code), A3-2 L4 owned DATAGRAM plus the live HTTP/3 end-to-end test, the close-path dependency graph and the two numbers that bound it, P3, and the rejected batch-owned candidate with its measurements | current for the delivered head |
| `anywhere-round3-debug-handoff.md` | 2026-10-20 | the sheet for the independent adversarial pass: change list, commands, dangerous edges, suspicious paths, dependency versions, CI links and what is still open | current for the delivered head |
| `anywhere-round3-close-liveness-final.md` | 2026-10-20 | the close-liveness counterexample attempt: idle-disabled peer plus a one-way broken path, measured timeline, why inbound traffic cannot be sustained without our ACKs, and the committed mechanism test | current for the delivered head |
| `anywhere-round3-debug-report.md` | 2026-10-20 | the acceptance pass: the close wait measured as bounded, the operations that queue behind a blocked port, the L4 ownership matrix re-verified, and the exit condition for the residual risk | current for the delivered head |

### tun-fast-path/

| report | date | covers | status |
|---|---|---|---|
| `README.md` | 2026-10-24 | the round's index: snapshot, files, verdict and limits | current for `32c37219c59` |
| `tun-fast-path-value-review.md` | 2026-10-24 | D0 value review, path matrix, `fast_path` precedence and decision | current for `32c37219c59` |
| `tun-fast-path-adversarial-debug.md` | 2026-10-24 | adversarial acceptance, exact verdicts and finding records | current for `32c37219c59` |

## Adding a report

1. Put it in the directory that matches its subject, keeping a name that says **what** was audited and
   **when** — `SUBJECT_YYYY-MM-DD.md` for a formal report, `_subject.md` for working notes.
2. Add a row to the table above: report, date, what it covers, and whether it is current or superseded.
3. If the report contradicts a module, fix the module **or** record the conflict in the report — never leave
   both silently disagreeing (rule 1 of `AGENTS.md`).
4. Do not move `BASELINE.md` for a report. A report is not a refresh.
