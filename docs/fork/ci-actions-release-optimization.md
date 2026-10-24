# Actions / Release closeout — 2026-10-24

Base: `testing` @ `9b1f95e40a4bbc25a34362557da3363dd30ffb02`.
Work branch: `ci/actions-release-closeout-20261024` @ `0d5dd2f3210a0e1921700f65bbc0034768d6c045` (final code); this
documentation commit. No `main`, `testing`, tag, Release or asset was modified.

## 1. What the workflows actually do

| workflow | triggers | role | release blocker |
|---|---|---|---|
| Verify | push/PR `testing`, dispatch | source assumptions, builds, race checks | **yes** |
| Release contract verification | push/PR release-tooling paths, dispatch | release helper/tooling tests | **yes** |
| server-linux-amd64 | dispatch | Linux artifact | yes (artifact source) |
| client-macos | dispatch | macOS artifact | yes (artifact source) |
| windows-core-amd64 | dispatch | Windows artifact | yes (artifact source) |
| android-core-arm64 | push path + dispatch | Android libbox artifact | yes (artifact source) |
| client-apple | dispatch | Apple libbox artifact | yes (artifact source) |
| client-desktop-windows | dispatch | desktop installer | staged separately |
| Release | dispatch only | downloads built artifacts, drafts, publishes | publisher |
| Mobile contract | push path + dispatch | Apple tag/low-memory data-path contract | no (diagnostic gate) |
| Runtime audit | `workflow_run` + dispatch | Windows runtime diagnosis | no |
| Windows boxdd, Apple TLS, Linux netns/MASQUE/platform/DNS | dispatch | focused platform checks | no |
| interop Xray | dispatch | reference interop | release acceptance can require it |
| client-desktop-windows-runtime-audit | workflow_run | diagnostics only | no |

The release gate remains the same authority: `release.yml` is the only writer; it checks tag →
commit → `release/JIEJIE_VERSION`, reads exact-SHA evidence from `verify.yml` and
`release-contract-verify.yml`, selects the newest run per workflow id/sha (a newer failure cannot be
masked by an older success), downloads the already-built artifacts, verifies each binary, checksum,
BUILD-INFO, platform/commit and the public asset set, creates a Draft, and only then publishes. No
release/tag logic was changed.

## 2. P0 — mobile manual dispatch is isolated

The observed policy was:

```yaml
group: mobile-contract-${{ github.ref }}
cancel-in-progress: true
```

Two real manual runs at `ed0686ca`/`80b42c24` (`40052951978`, `40053082031`) were cancelled. A manual
candidate freeze could therefore be killed by a later push, and two manual runs shared the single
pending slot.

The fix gives each dispatch its own group and keeps pushes ref-keyed:

```yaml
group: mobile-contract-${{ github.event_name == 'workflow_dispatch' && github.run_id || github.ref }}
cancel-in-progress: ${{ github.event_name != 'workflow_dispatch' }}
```

Live validation at `549a5f80be6`:

- `40095339516` and `40095360873` are two simultaneous `workflow_dispatch` runs; both completed and
  both ultimately report success. The first exposed two existing timing-sensitive tests
  (`TestActiveInboundStopsBecauseThePeerIsStarved` at 2.916s vs >3s and
  `TestAdblockSteadyStateAllocatesNothing` 39 vs 38 goroutines); its failed jobs were rerun and
  passed. Neither was cancelled by the other.
- `check-release-workflows.py` now evaluates the real expressions: two pushes share a group and
  cancel, two dispatches have distinct groups, a dispatch is not in a push group, and dispatch
  cancellation is false.

## 3. Verify cost

Measured before (`40091508491`):

| | |
|---|---|
| workflow wall clock | 17m23s |
| non-race steps | ~7m13s |
| race data paths | 8m38s |
| race configuration | 1m32s |

The two race steps were serial after all build/assumption steps. They now run in a second job in the
same `verify.yml`; a workflow run is still the conjunction of both jobs, so release evidence is
unchanged and no test is skipped.

After (`40097435132`, final code SHA):

| | |
|---|---|
| workflow wall clock | **10m50s** |
| verify job | 8m09s |
| race job | 10m50s (checkout/setup included) |
| runner-job minutes | 18m59s vs 17m23s (~+1m36s) |

Push feedback improves by about **6m33s (38%)** at a small runner-minute cost. The header now states
the race job and the release-evidence rationale.

## 4. Same-SHA evidence / Release

`release-artifacts.sh` already selects runs by numeric workflow id, exact `head_sha`, newest run per
workflow, and requires completed/success with the expected job/artifact set. A newer failure or
cancellation is not hidden by an older success; a run with no artifact is not accepted. The release
workflow imports artifacts instead of rebuilding.

Therefore no automatic “reuse an old green run” selector was added: the existing selector already
reuses a completed exact-SHA success when it is the newest run, and adding a second authority would
weaken fail-closed semantics. `Mobile contract` remains diagnostic (not `REQUIRED_EVIDENCE`):
its push trigger is path-filtered, so making it a hard release prerequisite without a guaranteed
same-SHA dispatch would create a release deadlock. That is recorded instead of silently changing the
contract. `release.yml` was not modified.

## 5. Validation

- `scripts/ci/check-release-workflows.py`: **133 passed, 0 failed** on the branch.
- All 18 workflow YAML files parse with PyYAML.
- Release contract run `40097437803` (final branch SHA): success, including
  `test-release-artifacts.sh` **all 107 cases** and the 92-case helper suite; this is the Linux
  environment the suite requires.
- Local Windows attempt of `test-release-artifacts.sh` reached all cases but reported four
  platform-magic failures (Android `libbox.aar` and executable-magic checks use Linux/ELF tooling);
  the Linux CI result above is the authoritative one.
- Verify `40097435132` (final branch SHA): success.
- Two manual Mobile runs `40095339516` / `40095360873` at `549a5f80be6`: no cancellation; the first's unrelated
  timing-test failures passed on rerun. A third run at the final code SHA, `40098352189`, also succeeded.
- No release was published; no tag or asset was touched.

## 6. Remaining human action

Only review/merge this branch (or open a PR). CI does not auto-merge, and no release should be
dispatched from this work.
