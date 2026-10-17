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
  no stronger than `VERIFIED_SOURCE`.** No report in this directory is `VERIFIED_CI` — this tree contains no
  workflow-run record, so a CI job declaration is not evidence that the job ran.

`-race`, device behaviour and cross-platform guarantees are recorded per report. Where a report says they were
not run, that is `BLOCKED_BY_ENVIRONMENT`, not `PASS`.

## Layout

| directory | what belongs there |
|---|---|
| `convergence/` | this branch's own work: architecture convergence, the adversarial audit of it, and the narrow-scope audits that followed |
| `core/` | audits of the core packages (root test hygiene, `experimental/boxdd`, CI/release configuration, knowledge-base delta mapping). Files are prefixed `_` where they are working notes rather than formal deliverables |
| `release/` | release phases, branch consolidation, handoffs and close-outs, in chronological order of the phase names |
| `windows-client/` | the Windows client's installer / uninstaller / diagnostic-channel and coexistence investigations |

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

## Adding a report

1. Put it in the directory that matches its subject, keeping a name that says **what** was audited and
   **when** — `SUBJECT_YYYY-MM-DD.md` for a formal report, `_subject.md` for working notes.
2. Add a row to the table above: report, date, what it covers, and whether it is current or superseded.
3. If the report contradicts a module, fix the module **or** record the conflict in the report — never leave
   both silently disagreeing (rule 1 of `AGENTS.md`).
4. Do not move `BASELINE.md` for a report. A report is not a refresh.
