# Archive — `2026-10-dev-cycle`

Historical development reports from the `v0.1.6 → v0.1.9` window, moved out of the repository root so
the root describes the **current** project rather than the chronology of building it.

**These are historical evidence, not current truth.** `KNOWLEDGE/REFRESH.md` §2 puts them last in the
source-of-truth order:

```text
current source and tests  >  git history and diffs  >  build/CI config  >  KNOWLEDGE/  >  these reports
```

Read them to find out **how a decision was reached** — what was tried, what failed, what the
measurements were at the time. Do not read them to find out what the code does now; for that, route
through `KNOWLEDGE/INDEX.md` to the owning module, whose claims carry a re-check command.

## What is here

35 reports, all from the same development window:

| Group | Files |
|---|---|
| **Apple / iOS pipeline** | `APPLE_CLIENT_PIPELINE_AUDIT`, `APPLE_PIN_FINAL_VERIFICATION`, `APPLE_PIPELINE_FINAL_CLOSURE`, `APPLE_TF_PIPELINE_REFACTOR_REPORT` |
| **CI / release pipeline** | `CI_BUILD_PIPELINE_FINAL_CLOSURE`, `CI_BUILD_PIPELINE_REDUNDANCY_AUDIT`, `CI_PARALLEL_DAG_REFACTOR_REPORT`, `CI_PIPEFAIL_SIGPIPE_AUDIT`, `CI_RELEASE_POST_GREEN_OPTIMIZATION_REPORT`, `CI_RELEASE_UNIX_AUTHORITY_IMPLEMENTATION_REPORT`, `RELEASE_PIPELINE_*`, `V0.1.7_*` |
| **Apple supply chain / release trust** | `CI_RELEASE_APPLE_SUPPLY_CHAIN_REPORT` |
| **`fast_path` group policy** | `FAST_PATH_GROUP_POLICY_DEBUG_REPORT`, `FAST_PATH_GROUP_POLICY_IMPLEMENTATION_REPORT` |
| **L0 / Direct Offload** | `L0_DIRECT_COVERAGE_DEBUG_REPORT`, `L0_DIRECT_COVERAGE_IMPLEMENTATION_REPORT` |
| **Static validation** | `STATIC_VALIDATION_*` |
| **Stall / DNS / platform audits** | `ACTIVE_TCP_STALL_INVESTIGATION`, `SERVER_CHAIN_STALL_AUDIT`, `CONSUMER_DNS_PARITY_CLOSURE`, `ANDROID_ACTIVE_FLOW_RISK_AUDIT`, `MACOS_ARM64_FLAKY_GATE_AUDIT`, `WINDOWS_CORE_MASQUE_GATE_FIX`, `WINDOWS_RUNTIME_AUDIT_FINAL_CLOSURE` |
| **Upstream absorption** | `UPSTREAM_ABSORPTION_0.1.6_FINAL` |
| **Knowledge base's own build-out** | `KNOWLEDGE_STAGE1_REPORT`, `KNOWLEDGE_STAGE2_REINFORCEMENT_REPORT` |

## What is deliberately still in the repository root

Four reports stay at the root because `KNOWLEDGE/` cites them **with line-number ranges**, and moving
a file invalidates every `file:line` in a citation:

| File | Cited by | Why line numbers matter |
|---|---|---|
| `KNOWLEDGE/reports/release/APPLE-PARENT-FINALIZE-REPORT.md` | `modules/ci-release.md:133` | cited as `:198-203` for a gate-layer verdict |
| `KNOWLEDGE/reports/release/BRANCH_CONSOLIDATION_TO_TESTING_FINAL.md` | `modules/ci-release.md:130` | cited as `:452-458` for a missing stress result |
| `KNOWLEDGE/reports/release/CORE_0.1.6_FINAL_CLOSURE_REPORT.md` | `modules/ci-release.md:132,422` | cited as `:132-138` and `:18` |
| `KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` | `COMMIT-INDEX.md:70`, `modules/path-selection-validation.md:373,377` | cited by section, and `§1`'s table is still asserted correct |
| `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` | `COMMIT-INDEX.md:69`, `modules/path-selection-validation.md:181,371` | cited as `§B (:141-142)`, `§H (:350-367)` |

Moving these is a **separate, deliberate change** that must repair each `file:line` in the same commit.
`REFRESH.md` §29 says an uncertain move is a `KEEP`, so they were kept rather than moved with a guess.

`README.md` and `AGENTS.md` are entry points and are cited by name throughout; they are core root
files, not historical documents.

## Relationship to `docs/fork/`

`docs/fork/` is the **older** historical layer from before this window, and it is cited by `KNOWLEDGE/`
under stable lower-case names. This directory is the next layer, kept separate so a reader can tell
which cycle a report came from. Do not merge them without repairing the citations that name the
`docs/fork/` paths.
