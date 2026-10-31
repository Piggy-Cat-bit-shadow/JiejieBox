# Knowledge calibration — 2026-10-31

## Scope

Previous production baseline `dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07` to target
`0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`: 12 reachable commits including docs. Target is the
peeled annotated tag of the published, non-prerelease [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34),
published 2026-10-29 05:34:42 UTC. Observed branch tip `a4cd8b1b12b2ff9dbecf7a17c2a1824053f061a4`
adds README, changelog and explanatory docs only after target. Current repository API identifies
`Piggy-Cat-bit-shadow/JiejieBox`; the existing remote URL is an old redirect. Upstream/testing
`e5d91fa79f6319d5de940d6c81d81f0fe53b9476` is the target merge-base, not proof of authorship.

Changed owners: Apple child gitlink `16cf87b4` → `044e6d5`, ABI pin and source lock; version and
release evidence; Apple shared-action topology/build/pack tests and structural guard. The previously
unconsumed Apple candidate is now merged into `main` and parent; stale candidate statements and
historical mixed-target acceptance rows were reconciled. Current ownership and cross-module boundaries
are in `modules/child-repositories.md`, `modules/libbox-platform-contract.md`, `modules/ci-release.md`
and `ACCEPTANCE.md`. No parent Go runtime source changed in this delta. Existing 2026-10-29 and
2026-10-28 ledgers remain historical with their own SHA and limits; unrelated source claims retain
prior calibration rather than being freshly re-proven.

## Child and dependency audit

At the observation, Apple `main` `044e6d5`, Android `main` `c7837107`, Desktop `main` `6d4ccf8`,
Dashboard `main` `5e9271e5`, sing `dev` `1729220c`, sing-tun `dev` `8b9a3800`, sing-quic `main`
`1ef2597c`, quic-go `sagernet` `2daa76f5`, uTLS `main` `b63a2e9a` and Cronet `main` `c9c0e6ec`
matched the recorded consumption pins. Controlled Cronet native archives remain separately pinned.
Apple's former sync branch is absent from the branch listing. Branch observations may move
independently after this review. The Apple integration source and symlink blob modes were inspected;
local client worktrees remain at their previous clean revisions and were not reset.

## Evidence and limits

* Local `check-release-workflows.py`: 164 passed; `release-freeze.py verify` passed for available pins,
  with Desktop/dashboard SKIPPED because the Desktop checkout was absent. The verifier's version
  mismatch is a note, not a failure.
* Local `check-libbox-abi.sh --pin-only` passed for the current Apple and Android gitlinks; this is
  not a full ABI scan. Local `check-apple-source-selection.sh`: 72 passed.
* Local `test-apple-beta-publish.sh`: 99 passed, 2 failed on Windows because macOS `ditto` was not
  available; the install/copy assertions are therefore BLOCKED_BY_ENVIRONMENT, not a local pass.
  The earlier macOS CI run is separate evidence, not a rerun of this Windows test.
* Prior [Verify 40819594893](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819594893)
  and [iOS Apple run 40819615363](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819615363)
  succeeded at `0067b03`, and [Release 40824078971](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40824078971)
  succeeded there. The Apple run used child `044e6d5`, but skipped its macOS job. The earlier
  [macOS run 40776674407](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40776674407)
  succeeded at parent `ed0f3dc`; it did not run iOS. Run success is scoped to those jobs and SHAs.
* This refresh did not perform real-device, interoperability, native archive or independent release
  artifact byte checks. Inherited Windows TUN errno and platform limits still apply.

## Refresh status

```text
KNOWLEDGE_REFRESH = COMPLETE (reviewed source and CI scope; device limits remain)
OLD_PRODUCTION_BASELINE = dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
DELTA_CLASSIFIED = YES
AFFECTED_SUBSYSTEMS_REAUDITED = YES
COMMIT_PROVENANCE_UPDATED = YES
INVARIANTS_REAUDITED = YES
ACCEPTANCE_UPDATED = YES
HISTORICAL_DOCS_RECONCILED = YES
CROSS_FILE_CONSISTENCY = PASS (link, path and section audits)
FUTURE_AI_INCREMENTAL_READ = PASS
```
