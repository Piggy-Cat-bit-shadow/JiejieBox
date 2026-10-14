# Stage 1 knowledge-base report

## Result

- Release/baseline: `v0.1.6` = `a267e99e0bf1811dc975b013e2e8d7a15ab40d23`.
- Upstream baseline: `upstream/testing` = `e4b7e692f4545890bfc9d43960328e1449fa803b` (the locally determinable upstream ancestor; `upstream/main` is unavailable).
- Fork commit range scanned: 1,834 commits (`e4b7e692..a267e99e`). `v0.1.6..HEAD`: 0 commits.
- Knowledge-indexed commits: 58 high-value commit entries/pairs across 3 subsystems. The remaining history was treated as upstream history, process/noise, or superseded closure work unless needed to explain a trap.
- Subsystems: core/lifecycle/validation; routing/DNS/sniff/protocols; platform/build/release.
- New files: `AGENTS.md`, 7 root `KNOWLEDGE` files, 3 modules, and this report.

## Documentation disposition

- **KEEP:** user/developer documents such as `README.md`, active `docs/index*`, build/profile, migration, support, MASQUE/Naive, Apple release/signing, Windows status/acceptance and upstream-sync guides.
- **ABSORB-INTO-KNOWLEDGE:** root closure/validation reports, `docs/RELEASE-CERTIFICATE.md`, and fork platform/ABI/runtime acceptance reports.
- **ARCHIVE:** phase/round/handoff/sweep/debug/closure ledgers, including `STATIC_VALIDATION_FINAL_REMAINDER_CLOSURE.prev.md`; preserve them as SHA evidence, not current facts.
- **DELETE:** none. No legacy document was safely removable without a separate archival decision.

## Limits and follow-up

UNVERIFIED product behavior is listed in `KNOWLEDGE/ACCEPTANCE.md`. Follow-ups are: reconcile the `PhysicalPath.RequiredNetworks` historical/comment tension; verify real-device Apple/Android, Windows loader, TUN/routing and multi-platform DNS behavior. No production change was made to address them.

```
KNOWLEDGE_STAGE1 = COMPLETE
PRODUCTION_CODE_CHANGED = NO
GIT_HISTORY_REWRITTEN = NO
BASELINE_PINNED = YES
COMMIT_PROVENANCE_BUILT = YES
READY_FOR_INDEPENDENT_REVIEW = YES
```
