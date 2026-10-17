# Phase 1 — addendum: final hashes, publication, and the CI caveat

Appended after the report was committed, so the numbers below are the **published** ones
rather than the ones known while writing §1's placeholder.

```text
INTEGRATION_BRANCH   = integration/release-next
INTEGRATION_BASE_SHA = c32c210b0dbb1a23fb9b19ef731d164cd8745d34   (== origin/testing)
INTEGRATION_HEAD_SHA = 66a2204c53b4539992b5dd1315da6347fa0e16bd
HEAD commit          docs(integration): Phase 1 topology audit, merge record and close-out report
PUSHED               yes  -> origin/integration/release-next, new branch, non-fast-forward not needed
LOCAL == REMOTE      yes  (both 66a2204c53b...)
ORIGIN/TESTING       c32c210b0dbb1a23fb9b19ef731d164cd8745d34   UNCHANGED
REMOTE BRANCHES      14 -> 14   (13 pre-existing + integration/release-next; none deleted)
NEW TAGS             none
RELEASE PUBLISHED    no
WORKTREE CLEAN       yes
```

The base relationship is proven, not asserted:

```text
git merge-base --is-ancestor c32c210b0dbb1a23fb9b19ef731d164cd8745d34 integration/release-next
  exit 0
```

## CI status — read this before treating the push as "verified by CI"

**No CI job runs on `integration/release-next`, and none was run.** This is the repository's own
configuration, not a failure:

```text
verify.yml               push: branches: [testing]
mobile-contract.yml      push: branches: [testing]
android-core-arm64.yml   push: branches: [testing]
release-contract-verify  push: branches: testing, fix/release-asset-contract, release/**
windows-boxdd-tests.yml  push: branches: testing, release/**   (+ path filters)
```

Every push-triggered workflow is branch-filtered to `testing` or `release/**`, so pushing this
branch started nothing. There is therefore **no GitHub Actions record for this integration
head**, and this report does not claim one — consistent with the fork's own rule that a CI job
declaration is not evidence that the job ran, and that `SKIP` is not `PASS`.

What stands in its place is the local verification in §9, run with the repository's own tag set:
the full tagged suite (only the 4 documented failures, each reproduced on a baseline comparator),
repo-wide gofmt, `go build ./...`, `go mod verify`, a tidy no-op check, the vet set proven
byte-identical to the baseline, an 18-package / 13-platform cross-build matrix with both
limitations proven pre-existing, the four discriminating regression tests, and the targeted
Windows/desktop regression (§9.5).

**To get a real CI run**, dispatch one manually — `verify` and `windows-boxdd-tests` both expose
`workflow_dispatch` — or move the branch under the `release/**` filter. That is an operator
decision about release process, so it was not made here.

## Two things a reader of §9 should know

**1. FreeBSD now gets further, which exposes a separate pre-existing gap.**
Fixing `interfaceNameByIndex` made `./route/` and the root package build on FreeBSD — both do.
But `experimental/boxdd`, the Windows daemon, imports `experimental/libbox`, and that package is
mobile-only: for BSD it is missing `OOMRecorderOptions`, `PowerReportOptions`,
`saveConfigSnapshot`, `copyConfigSnapshot`, `redirectStderr` and `savePlatformSnapshot`. That gap
is unrelated to this integration and is **pre-existing and identical on untouched
`origin/testing`** (reproduced there directly). It is not a regression and it is not the
`interfaceNameByIndex` failure the brief asked about; that one is fixed.

**2. One behaviour assertion was deliberately reversed, and it is the only one.**
`common/netcap.TestEvaluateIgnoresInvalidAddresses` asserted that an interface carrying only an
invalid prefix proved **both** families impossible. It now asserts `StateUnknown` for both. This
is recorded here as well as in the commit because it is the single place in this integration
where an existing test's expectation was inverted rather than extended. The justification is in
§5.2 and in the commit message: that assertion was the same defect the fix removes, seen from the
invalid-address side, and the replacement asserts the new gate in both directions, which is
strictly stronger than what it replaced. No test was deleted or weakened to obtain a green run.

## Phase 1 is complete; nothing is blocked

All nine acceptance criteria the brief enumerates hold, and no criterion is outstanding. The
items in §9.6 are host limitations (race, Docker, Apple gates, the Windows installer tests, the
release-artifact provenance gate), each recorded as `NOT RUN` rather than claimed, and none is a
prerequisite for Phase 2 starting.
