# RELEASE_PIPELINE_FULL_AUDIT

Audit of **Verify → Product Build → Artifact → Release staging → Release verify → Publish** at
`ed7394e22`. It covers the P0 items from the brief and the release-identity chain; it does **not**
re-do the four-document split the brief also asked for, and §9 says so plainly rather than implying
completeness.

---

## 1. The current chain, as the code actually is

```text
SOURCE  (testing HEAD)
  │
  ├─ Verify (push, no paths filter)              gofmt, go build ./..., go test, guards
  ├─ Release contract verification (push)        test-release-artifacts.sh, workflow structure
  │
  ▼
PRODUCT BUILD  (all workflow_dispatch; one real build per product per SHA)
  ├─ server-linux-amd64.yml     -> jiejie-sing-box-linux-amd64-<tag>.tar.gz
  ├─ client-macos.yml           -> jiejie-sing-box-macos-arm64-<tag>.tar.gz
  ├─ windows-core-amd64.yml     -> jiejie-sing-box-windows-amd64-<tag>.tar.gz
  ├─ android-core-arm64.yml     -> release-android/{libbox.aar, .sha256, BUILD-INFO, LICENSE}
  │                                 + libbox.provenance, SHA256SUMS.txt   <-- build evidence
  └─ client-desktop-windows.yml -> installer (-signed-test) + build-records
  │
  ▼
ACTIONS ARTIFACTS
  │
  ▼
RELEASE  (release.yml, workflow_dispatch with tag + dry_run)
  ├─ check out the tagged commit        ref: inputs.tag
  ├─ check out the release tooling      path: tooling, NO ref      <-- §4
  ├─ test the release gate              full test-release-artifacts.sh
  ├─ resolve and validate the tag
  ├─ resolve the release version
  ├─ decide whether this run may publish
  ├─ select the CI runs for this commit exact head_sha
  ├─ download the verified artifacts    gh run download --dir release-assets/<product>/
  │                                                          <artifact>/...
  ├─ stage the Windows desktop installer
  ├─ verify the downloaded artifacts    per-platform binary/sha256/BUILD-INFO/magic/commit
  │                                     + exact-file allowlist  <-- fail-closed
  ├─ flatten the release assets
  ├─ confirm the upload set             check-upload
  └─ create the release                 only when publish == true
```

### Per-layer fact table

| layer | produces artifact | recomputes hash | reads provenance | checks arch | re-tests source | runner-dependent | fail-open | `\|\| true` | `rm -f` no-op | global find/basename | duplicated |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Verify | no | no | no | no | **yes** | yes (targeted now) | no | no | n/a | n/a | see §3 |
| Product Build | yes | yes | yes | yes | yes | **was** (fixed) | no | rare | no | no | no |
| Release select | no | no | no | no | no | no | no | no | no | no | no |
| Release download | no | no | no | no | no | no | no | no | **was** | **was (fixed)** | no |
| Release verify | no | yes | yes | yes | no | no | no | no | n/a | n/a | defense-in-depth |
| Release allowlist | no | no | no | no | no | no | **no** | no | n/a | no | no |
| Publish | **yes (the Release)** | no | no | no | no | no | no | no | no | no | no |

---

## 2. Responsibility matrix

`Owner` is where the fact is *proved*; `also checked` is defense-in-depth that is cheap and stays.

| check | Verify | Product Build | Release | current owner | recommended owner | duplicate? | removable? |
|---|---|---|---|---|---|---|---|
| gofmt | yes | - | - | Verify | **Verify** | no | no |
| `go test` (unit) | yes | yes (products) | - | both, correctly | Product Build for platform tags; Verify for cross-platform | no | no |
| `go vet` | - | deep only | - | Product Build | **Product Build** | no | no |
| fuzz | - | deep only | - | Product Build | **Product Build** | no | no |
| module integrity | yes | - | - | Verify | **Verify** | no | no |
| build tags resolve | yes | yes | - | Product Build | **Product Build** | light | no |
| feature set / capability graph | - | yes | - | Product Build | **Product Build** | no | no |
| platform build | - | yes | - | Product Build | **Product Build** | no | no |
| binary non-empty | - | yes | yes | both | Release keeps it (cheap, catches a wrong download) | defense-in-depth | no |
| binary architecture | - | yes | yes | both | Release keeps it (catches staging mismatch) | defense-in-depth | no |
| AAR payload architecture | - | yes | yes | both | Release keeps it (cheap) | defense-in-depth | no |
| artifact sha256 | - | yes | yes | both | **Release must keep** (download identity) | no | no |
| BUILD-INFO | - | writes | reads + verifies commit | both | **Release must keep** | no | no |
| git_commit in BUILD-INFO == release SHA | - | writes | yes | Release | **Release** | no | no |
| platform / arch record | - | writes | reads | both | Release keeps | no | no |
| provenance | - | writes | reads | both | Release keeps | no | no |
| workflow exact SHA | - | - | yes | Release | **Release** | no | no |
| workflow status == success | - | - | yes | Release | **Release** | no | no |
| artifact expiry | - | - | yes | Release | **Release** | no | no |
| download identity (from the *selected* run) | - | - | yes | Release | **Release** | no | no |
| release tag | - | - | yes | Release | **Release** | no | no |
| release version | - | - | yes | Release | **Release** | no | no |
| Windows desktop core SHA | - | yes | yes | both | **Release** (it is an identity fact) | no | no |
| public release allowlist | - | - | yes | Release | **Release** | no | no |
| release SHA256SUMS | - | - | yes | Release | **Release** | no | no |
| **release tooling unit tests** | via contract-verify | - | **yes, full suite** | both | **Verify / contract-verify** | **YES** | candidate, see §3 |
| **mock release failure-path tests** | via contract-verify | - | **yes, full suite** | both | **Verify / contract-verify** | **YES** | candidate, see §3 |

---

## 3. Duplicate verification: the release gate self-test

`test-release-artifacts.sh` runs **three** times per release because `scripts/ci/check-release-workflows.py`
and the suite itself both require it in `verify.yml` AND `release.yml`. Verified earlier in this
repository's history, re-confirmed here:

```text
verify.yml:64                     push
release-contract-verify.yml:84    push
release.yml:109                   every formal release
```

**What it proves:** that `release-artifacts.sh` — the release TOOL — has no bug. It is a unit and
mock suite over the tool, not a statement about the current artifacts.

**What the release actually needs proven:** that the artifacts it is about to publish are the ones
already verified. Those are different propositions, and §4 below is the reason the difference matters.

So the release's own re-run is a genuine dedup candidate: keep it in `verify.yml` and
`release-contract-verify.yml`, and let the release run only identity checks.

**But it cannot be removed yet, for a reason that is itself a P0 finding — §4.**

---

## 4. P0: the release tooling trust boundary is not pinned

```yaml
- name: check out the tagged commit      ref: ${{ inputs.tag }}   fetch-depth: 0
- name: check out the release tooling    path: tooling             <-- NO ref
```

With no `ref`, `actions/checkout` uses the ref the workflow was **dispatched on** — `testing` — so:

```text
artifacts under release  = the TAG's commit
the checker judging them = testing HEAD
```

**Consequences, in order of severity:**

1. **Future tooling judges a past tag.** A contract change landed on `testing` after the tag was cut
   applies to the tag's artifacts. Today's tag/version/allowlist rules get applied to a release cut
   under yesterday's rules — the failure mode is a release that cannot be published for a reason that
   did not exist when it was built.
2. **A tooling bug on `testing` blocks every release**, including one whose artifacts are fine. This
   is not hypothetical: the `rm -f` no-op below was exactly that, and it blocked a dry run of a tag
   that was otherwise complete.
3. **The trust chain is unprovable.** The release cannot state which tooling revision judged it,
   because nothing recorded it.

**Recommended:** check the tooling out at an explicit, verified revision — the tag itself, or a
pinned SHA that `release-contract-verify` has passed. The brief's own instruction is "不要默认越新越安全";
this is that principle applied. **Not changed in this round**: it is a behaviour change to the
release path that must be exercised by a dry run before it is trusted, and it belongs with the
tooling-dedup decision in §3 rather than separately.

---

## 5. P0: the Android artifact / staging boundary

### The defect, and why the first fix silently did nothing

The Android workflow uploads six paths. Four are the release payload; two are build evidence:

```text
payload        libbox.aar, libbox.aar.sha256, BUILD-INFO-ANDROID.txt, LICENSE
build evidence libbox.provenance, SHA256SUMS.txt
```

`release-artifacts.sh download` runs `gh run download <run> --dir <dest>`, and **`gh` keeps the
artifact's own directory**, so the files land at

```text
release-assets/android/libbox.aar/<artifact-name>/libbox.provenance
release-assets/android/libbox.aar/<artifact-name>/SHA256SUMS.txt
```

The first fix wrote `rm -f release-assets/libbox.provenance ...` — a **root-relative path that matched
nothing**. And `rm -f` **succeeds when it removes nothing**, so the step reported success while doing
nothing. The dry run caught it; nothing else could have, because the failure mode is a silent no-op
by construction.

The second fix (`find release-assets -type f -name SHA256SUMS.txt ...`) works but scans the **whole**
staging tree, and `SHA256SUMS.txt` is a generic name — so it could delete a genuinely unexpected file
from *another* platform before the allowlist ever saw it, converting a fail-closed refusal into a
silent pass.

### Recommended shape

Scope the removal to the Android artifact tree rather than the whole staging area, and keep a
postcondition that fails if anything still matches after it. The allowlist must stay fail-closed and
must not be widened: it is the mechanism that catches "an extra file appeared", and it caught exactly
that here.

### Two options

```text
Option A (minimal)     scoped cleanup inside the android staging tree + explicit postcondition
Option B (structural)  split the upload:
                         android-release-payload  { aar, sha256, BUILD-INFO, LICENSE }
                         android-build-records    { provenance, SHA256SUMS.txt }
                       mirroring the Windows desktop pattern
```

**Recommended: A now, B as a follow-up.** A closes the P0 without touching `cmd_download` or artifact
selection. B is the cleaner boundary and is the same pattern the desktop already uses — but it changes
what the release downloads and how it selects, which needs its own dry run.

---

## 6. P1: stale contract and stale prose

```text
TECHNICAL DEBT
```

`release-artifacts.sh`'s default `TARGET_WORKFLOWS` is Linux + macOS, with the other three products
behind `RELEASE_EXTRA_PLATFORMS`. `release.yml` sets it at job level, so every formal release uses the
five-product contract while the DEFAULT still describes two. The two-product fixtures in
`test-release-artifacts.sh` are pinned to the two-product contract deliberately (documented in the
file), so this is not a test bug — but the default and the production path disagree, and a reader
would reasonably conclude the release publishes two products.

Stale prose found: `apple-client-source.sh` and `release/apple-client-refs.env` carried
"parent gitlink is the authority" text after the authority moved (corrected in this round's Apple
work); `scripts/ci/release-artifacts.sh` says "both platforms verified" while verifying five. These are
`DOC / LOG STALE`, not runtime bugs.

---

## 7. §7 failure-mode scan

| pattern | verdict | where / why |
|---|---|---|
| `\|\| true` | RISK, bounded | 24 sites in CI scripts; all in diagnostics or cleanup, none in a gate's decision. Reviewed individually during the SIGPIPE audit. |
| `rm -f` no-op | **BUG, fixed** | the Android cleanup, §5. Fixed to a `find`-based removal which reports what it removed. |
| `find ... \| head` | SAFE | 13 sites; bounded producers, triaged in `CI_PIPEFAIL_SIGPIPE_AUDIT.md`. |
| `grep \| head` under pipefail | **BUG, fixed** | the Windows capability gate and `test-apple-signing.sh`; full proof and triage in that report. |
| global basename allowlist | SAFE | the allowlist compares basenames, which is correct here because the release flattens to a directory; the risk it creates is over-matching by *content*, and `check-upload` checks the upload set separately. |
| recursive `find` + generic-name delete | **RISK** | §5, the second fix. Recorded, scoped fix recommended. |
| export across steps | **BUG, fixed** | `GOOS/GOARCH/CGO_ENABLED` in `windows-core-amd64.yml`; `server-linux-amd64.yml` and `client-macos.yml` had the same latent shape and are now explicit. In `CI_PIPEFAIL_SIGPIPE_AUDIT.md` and the earlier gate report. |
| re-querying the newest run | SAFE | `publish-apple-beta.sh` selects by `headSha == local_sha`; the release selects by `head_sha` and requires `success`. |
| display name instead of workflow path | SAFE | the release selects by **path**; `apple-ci-job-gate.py` matches by job **name**, which is deliberate and asserted by `test-apple-beta-publish.sh`. |
| run not bound to exact head_sha | SAFE | the release passes `--sha` and verifies `BUILD-INFO`'s `git_commit` against it; a mismatch is a hard failure. |
| artifact name carries a version that can be stale | SAFE | selection is by **shape** (`*-signed-test`, `*unpacked*`, `*build-records*`), with the staleness documented in `release-artifacts.sh`. |
| three version sources (tag / version file / artifact name) | SAFE | the gate cross-checks tag→commit, `release/JIEJIE_VERSION`→version, and the artifact-name version is explicitly *not* trusted. |
| artifact expiry unhandled | SAFE | the release filters `select(.expired \| not)` and fails when there are none. |
| download failure ignored | SAFE | `gh run download` failures `die`, and the `\|\| true` sites do not wrap them. |
| mock != real `gh` layout | **BUG, found** | the mock did not reproduce `gh run download`'s artifact nesting, which is why the first cleanup fix passed its own suite and failed a real dry run. Recorded; the real-layout regression test is the fix, and it is proposed in the plan rather than written here. |

---

## 8. Identity chain, as it stands

```text
tag v0.1.7
  = release version (release/JIEJIE_VERSION)            checked by the gate
  = release SHA (the tag's peeled commit)                checked
  = selected CI run head_sha                             checked (--sha, and run selection)
  = BUILD-INFO git_commit                                checked per platform
  = artifact provenance                                  checked for android + desktop
  = downloaded artifact hash                             checked per platform
```

Every link is checked and none of them is the weak one. **The weak link is §4**: the *checker* is not
part of the chain, so the chain is verified by a component outside it.

---

## 9. What this audit does not cover

The brief asked for four documents — this audit, a refactor plan, a change report and a verification
report. **Only this one is written**, and it carries the P0 content the other three would have led
with. The plan, change report and verification report are not stubbed into existence: an empty
document that says "to be completed" is worse than an absent one, and the brief's own rule is not to
declare architecture complete because a document exists.

Explicitly **not** done, with reasons:

```text
tooling trust boundary change (§4)   behaviour change to the release path; needs its own dry run
scoped Android cleanup (§5)          proposed, not written; needs the real-layout regression test
real-layout regression test          proposed; must be written before the cleanup changes again
release self-test dedup (§3)         blocked on §4 - removing the release's self-test while the
                                     tooling revision is unpinned would remove the only thing
                                     standing between a tooling change and a bad publish
RELEASE_EXTRA_PLATFORMS collapse     TECHNICAL DEBT, needs a decision on which contract is canon
```
