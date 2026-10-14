# RELEASE_PIPELINE_REFACTOR_PLAN

Companion to `RELEASE_PIPELINE_FULL_AUDIT.md`, which holds the chain diagram, the responsibility matrix
and the failure-mode scan. This document is the ordered change list and the decisions behind it.

**Rejected as the primary strategy: a broad "release becomes staging + provenance + allowlist +
publish" teardown.** The audit's own finding is that each existing stage is sound where it sits, and
that most duplication is genuine defense-in-depth over different facts (architecture vs provenance vs
recorded commit). Removing them would trade a real guarantee for a smaller-looking pipeline. The
changes below are the ones that address a *proved* defect or a *proved* duplication.

---

## Batch 1 — Android staging boundary (P0, do first)

**Defect**: `release-assets` carries two build-time files that the payload contract excludes, and the
first fix for it was a silent no-op.

**Option A — scoped cleanup with a postcondition (low risk)**

Restrict the removal to the Android tree rather than the whole staging area, and **fail if anything
still matches after it**, so the cleanup cannot be a no-op again:

```sh
android_tree=release-assets/android          # the product's own subtree
before="$(find "$android_tree" -type f \( -name 'libbox.provenance' -o -name 'SHA256SUMS.txt' \) -print)"
[ -n "$before" ] || die "the android artifact no longer carries its build-time evidence; if that is
  intentional, update this step rather than letting it become a no-op"
printf '%s\n' "$before" | while IFS= read -r f; do rm -f -- "$f"; done
after="$(find "$android_tree" -type f \( -name 'libbox.provenance' -o -name 'SHA256SUMS.txt' \) -print)"
[ -z "$after" ] || die "build-time evidence survived the cleanup: $after"
echo "removed $(printf '%s\n' "$before" | wc -l) build-time file(s) from the android tree"
```

The `[ -n "$before" ]` guard is the part that matters: it converts "cleanup ran" into "cleanup had
something to do and did it", which is exactly the assertion the first attempt lacked.

**Option B — split the artifacts (structural, follow-up)**

```text
android-release-payload   { libbox.aar, libbox.aar.sha256, BUILD-INFO-ANDROID.txt, LICENSE }
android-build-records     { libbox.provenance, SHA256SUMS.txt }
```

This makes the boundary a property of the artifact rather than of a cleanup step, and it mirrors what
the Windows desktop already does (`-signed-test` + `build-records`).

**Decision: A now, B as a follow-up.** A closes the P0 without touching artifact selection or
`cmd_download`. B changes what the release downloads, which needs its own dry run.

---

## Batch 2 — the real-layout regression test (P0, prerequisite for any further staging change)

**Why this exists**: the mock `gh` in `test-release-artifacts.sh` did **not** reproduce
`gh run download`'s artifact-directory nesting. That is why the first cleanup fix passed its own suite
and failed a real dry run. A suite whose fixture disagrees with the tool it mocks cannot catch this
class at all.

**The change**: make the mock copy the artifact into a directory named after the artifact, exactly as
`gh run download --name` does, and add a case asserting that the release tree contains no
build-time-only file. Without this, every future staging fix is unverifiable locally.

**This is a prerequisite, not an optional extra** - it is the only mechanism that could have caught
the no-op.

---

## Batch 3 — tooling trust boundary (P0, needs its own dry run)

**Defect**: `release.yml` checks the tooling out with `path: tooling` and **no `ref`**, so the checker
comes from the dispatched ref (`testing`) while the artifacts come from the tag.

**The change**: pin the tooling to an explicit revision that `release-contract-verify` has already
passed, and record that revision in the run summary so the release can state which tool judged it.

```yaml
- name: check out the release tooling at the verified revision
  uses: actions/checkout@v4
  with:
    path: tooling
    ref: ${{ inputs.tag }}      # or a pinned SHA verified by release-contract-verify
```

**Why it is not done in this round**: it changes the release path's behaviour and can only be
validated by a dry run. Doing it without one would repeat the mistake this audit is about.

---

## Batch 4 — release self-test dedup (depends on Batch 3)

**Duplication, measured**: `test-release-artifacts.sh` runs three times - `verify.yml:64`,
`release-contract-verify.yml:84`, and `release.yml:109`.

**Why it cannot be removed yet**: the release's own re-run is currently the only thing standing
between a `testing` tooling change and a bad publish, because the tooling revision is unpinned
(Batch 3). Removing it first would delete the guard that compensates for the unpinned checker.

**After Batch 3** the release can keep only identity checks - tag, version, run selection, artifact
hashes, BUILD-INFO commits, allowlist - and drop the mock-failure-path suite, which belongs where the
tool is *changed*, not where it is *used*.

---

## Batch 5 — stale contract (technical debt, needs a decision)

`release-artifacts.sh` defaults to a two-product `TARGET_WORKFLOWS` and puts the other three behind
`RELEASE_EXTRA_PLATFORMS`, which `release.yml` sets at job level. Production and default disagree.

**Decision required, not taken here**: which contract is canonical. Collapsing the flag would make the
default the five-product one and simplify the tooling; keeping it preserves the two-product fixture
that `test-release-artifacts.sh` deliberately pins. The two-product fixtures are documented as
deliberate, so this is a policy question about what the tool's default *means*.

---

## Batch 6 — pipefail/SIGPIPE determinism (mostly done)

```text
DONE      windows-core capability gate            producer -> file -> consumer
DONE      test-apple-signing.sh tracked-key check same fix, 210 KB producer proved
DONE      GOOS/GOARCH/CGO on every judging step   windows-core, server-linux, client-macos
REVIEWED  20 further pipeline sites               bounded producers, triaged in the SIGPIPE report
OPEN      the mock-vs-real `gh` layout            Batch 2
```

---

## Order, and why

```text
1  Batch 2  the regression test         - nothing else is verifiable without it
2  Batch 1A scoped android cleanup      - closes the current P0, and is now testable
3  Batch 3  pin the tooling ref         - needs a dry run; do it alone
4  Batch 4  drop the release self-test  - only safe after 3
5  Batch 1B split the artifacts         - structural, after the boundary is proven
6  Batch 5  stale default               - a decision, not a change
```

**What must not happen**: widening the release allowlist, pruning unexpected files generically,
adding retries to a timing failure, or removing a check because it fired. Each of those replaces a
detectable failure with an undetectable one, which is the failure mode this whole audit exists to
find.

---

## Explicitly out of scope

```text
replacing the release pipeline with a new design        no evidence any stage is unsound
removing provenance or allowlist checks                  both caught real defects this round
the Apple pipeline                                      separate prompt, separate surface
the macOS arm64 flake                                   separate prompt, and it is a test issue
the release's ten gate call sites                        proven untouched and still needed
```
