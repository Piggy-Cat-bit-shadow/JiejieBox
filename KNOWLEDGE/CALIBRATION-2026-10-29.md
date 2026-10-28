# Knowledge calibration — 2026-10-29

## Target and scope

Previous production baseline `cb71cf80d7224a51823b455f0be5959ad8556933` → source/CI target
`dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07`: 125 reachable commits, 76 changed paths including
knowledge history. The observed `testing` tip was `8500bf9c7527cab65fd8612ceef638b3b6b2f928`;
later commits through `5468bf4dd74fbc47510b572fc2313299f9c3582e` only changed `docs/fork` and do not move the production baseline.
Published v0.1.33 peels to `a0bbf666dec8693450c0e8363ed42db3294ac64a`, an ancestor of this target.
Remote upstream/testing `e5d91fa79f6319d5de940d6c81d81f0fe53b9476` is its merge-base.

Production delta: endpoint-local routing for MASQUE/WireGuard/Tailscale, TUN routed-port publication,
shared flow logger; test delta: host verdict and constructor coverage; dependency delta: sing-tun and
uTLS replacements; build/release delta: source freeze manifest, lock, workflow and tests; platform
and CI delta: three client gitlinks, ABI pins, Apple input/composite/single-runner workflow and
release gates. Historical v0.1.31 calibration remains the scope of unchanged modules; its gaps and
limitations are not erased by this delta review. Registry/index, invariants, release, MASQUE, build,
libbox, acceptance and commit navigation were updated for affected boundaries. Child repositories and Go fork changes were reviewed separately against pinned objects and observed
branch heads; see `modules/child-repositories.md`. The modules map was
inventoried and unaffected module claims retain their prior calibration rather than being represented
as newly re-proven.

## Checks and limits

* `go test ./adapter` and focused `TestJudgeHostFlow` passed locally (Go 1.26.10); the broader
  route/protocol command did not finish and was stopped, so it is NOT_RUN as a completed suite. Unit tests do not establish real TUN or end-to-end traffic delivery.
* `release-freeze.py verify --lock release/source-lock.json`: PASS for available pins; nested Desktop
  dashboard check SKIPPED because desktop was not checked out. This check does not prove branch tips
  are current, artifact contents or release readiness.
* Freeze fixture suite 35/35, workflow structural gate 159/159, Apple input suite 13/13; local
  `check-libbox-abi.sh --pin-only` passed for parent Apple and Android gitlinks. Full client ABI scan,
  Apple build, devices and artifact checks were not run.
* Existing Verify run 40765511913 at the target was cancelled; do not infer green CI. No new workflow
  was dispatched for this refresh.
* The parent's remote ref was fetched and fast-forwarded without changing client worktrees. Apple and
  Android checkouts are clean but remain at their earlier revisions; desktop is uninitialized. Parent
  status therefore shows two modified gitlink worktrees. Remote submodule auto-fetch reported unavailable
  Android dev and desktop refs; neither is evidence of a corrupted parent tree.
* The earlier calibration's Windows TUN errno limitation, partial historical Apple scan and other
  unresolved source/device/platform limits continue to apply unless independently superseded.

Repeat calibration: inventory the registry and ownership map, compare baseline-to-target paths, inspect
current source and executable tests for every affected owner, then check index links/anchors and verify
release facts independently. Evidence is revision- and environment-scoped.

## Refresh status

```text
KNOWLEDGE_REFRESH = COMPLETE (source calibration; runtime/device limits above remain open)
OLD_PRODUCTION_BASELINE = cb71cf80d7224a51823b455f0be5959ad8556933
NEW_PRODUCTION_BASELINE = dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07
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
HISTORICAL_DOCS_RECONCILED = YES (preserved as historical)
CROSS_FILE_CONSISTENCY = PASS (link, path and section audits)
FUTURE_AI_INCREMENTAL_READ = PASS (entry points, baseline and child-repo routing)
```
