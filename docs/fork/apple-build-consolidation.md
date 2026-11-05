# Apple build consolidation — one Libbox producer, many consumers

> **历史阶段记录。** 本文的分支、pin、未合并/未发布或构建阻塞描述只适用于写作时版本。v0.1.39 已集成本地回环、自签修复与后续 Cronet 五目标发布；当前固定版本、已知限制和取代关系见 [2026-11-05 增量校准](../../KNOWLEDGE/CALIBRATION-2026-11-05.md)。保留原文中的设备未验项和失败过程，不把构建通过外推为真机通过。

Phase 2. The scope is the *build chain*, not the core and not the UI: the same Core SHA must
compile Libbox once, and every consumer must prove it is the same Libbox before using it.

Branch: `experiment/ios-selfsign-compat`. Nothing here is merged into `testing`, and no
release, tag or TestFlight upload is involved.

## 1. The graph, before and after

**Before.** `client-apple.yml` already had the right shape — `resolve-apple-source -> libbox ->
ios / macos` — and both GUI jobs downloaded the one artifact. But the phase-1 experiment
workflow compiled Libbox itself:

```
client-apple.yml:   resolve -> libbox (compile) -> ios / macos
experiment-ios-selfsign.yml: (compile Libbox again) -> IPA
```

Same Go bytes, two compilations, and nothing tied the two together. A commit built by both
workflows cost two gomobile builds of the entire core.

**After.** The experiment consumes the artifact the official pipeline produces for that commit:

```
client-apple.yml:  resolve -> libbox (compile once) -> artifact jiejiebox-libbox-<core sha>
                                   |                        │
                                   |                        ├── ios / macos (existing GUI jobs)
                                   |                        └── experiment-ios-selfsign (IPA)
```

The experiment job no longer contains a mandatory Go build. It looks for the artifact by name,
downloads it, and accepts it only if `apple-libbox-artifact.sh validate` agrees with the manifest
about the parent commit and the release version. With no artifact it still builds one — a
consumer may not be blocked by a missing producer — and the workflow records which of the two
happened in `BUILD-INFO.txt` and in the run summary.

## 2. Why the name is not the evidence

The artifact name is chosen by whoever uploads it, so a name match only decides *what to
download*. Usage is decided by the manifest:

* `validate --parent-sha <sha>` proves the framework was compiled from this commit;
* `--jj-version <release/JIEJIE_VERSION>` proves it belongs to this release;
* the manifest carries the framework's own sha256, and `install` is only called after both hold.

A stale, foreign, or tampered artifact therefore fails closed rather than silently linking a
different core. `libbox: required` turns "we reused it" from a description into a test: with no
validated artifact the run fails instead of falling back to a compile.

## 3. Version governance

The unsigned iOS build never passed `MARKETING_VERSION`/`CURRENT_PROJECT_VERSION`, so Xcode's
project defaults won: the shipped IPA said `1.14.8 (10)` while its file name and
`release/JIEJIE_VERSION` said `0.1.37`. The phase-1 audit measured this in the uploaded package;
the package's own `BUILD-INFO-IOS.txt` recorded both values side by side.

`scripts/ci/build-ios-ipa.sh` now derives both values from `release/JIEJIE_VERSION`
(`0.1.37 -> CFBundleVersion 137`) and asserts the *packed* app against them before writing the
zip, so the defect fails the build rather than reaching a user. The TestFlight path is untouched:
it already passed both values, and this makes the two paths agree instead of adding a second
source of truth.

## 4. What deliberately did not change

* **No `libbox`-only mode for `client-apple.yml`.** The idea is sound — the module gate alone
  would answer "does the core still build for Apple" without the GUI cost — but the current
  contract encodes the opposite: `check-release-workflows.py` asserts that with neither product
  selected *nothing* runs on a macOS runner, and the resolve job fails with
  `apple-release: no Apple platform selected`. Making libbox-only real means a new input and
  editing that contract (164 checks), which is a release-gate change, not a cleanup. The
  measured duplication did not justify it: the official pipeline already compiles Libbox once
  per run, and the only duplicate producer was the experiment.
* **No change to TestFlight, release gates, tags or secrets.** `publish-apple-beta.sh`,
  `apple-release.py follow` and `APPLE_USE_PREBUILT_LIBBOX=1` already consume a verified Libbox
  from a selected CI run; this work reuses them rather than adding a second upload path.
* **The self-sign IPA is still an independent, revocable experiment.** Deleting
  `.github/workflows/experiment-ios-selfsign.yml` (and `scripts/experiment/`) removes it entirely;
  nothing in the release path references it.

## 5. Rollback

| Change | How to undo |
|---|---|
| experiment workflow + scripts | delete `.github/workflows/experiment-ios-selfsign.yml`, `scripts/experiment/` |
| version governance in the unsigned path | revert `scripts/ci/build-ios-ipa.sh` to the previous commit |
| Apple client launch fixes | the Apple branch `experiment/selfsign-compat` is separate; the core branch only moves the `clients/apple` gitlink |

## 6. Evidence

Runs that actually executed (branch `experiment/ios-selfsign-compat`):

| run | what it proves | wall clock | Libbox job |
|---|---|---|---|
| `41733637543` (a2a91621) | producer: builds both slices, packs, uploads `jiejiebox-libbox-<sha>` | 23.4 min | **4.4 min** (warm Go cache) |
| `41738314328` (a2a91621) | consumer with `libbox=required`: build/publish/upload all **skipped**, IPA still produced | 18.1 min | **0 min** |
| `41740879398` (0e27832) | producer with the provenance line in `BUILD-INFO.txt` | 22 min | built |
| `41746464813` (5b561e7) | two products, one Libbox: the iOS job produced it (3.4 min) and the **macOS** job fetched, validated and installed the same artifact, then compiled `JiejieBox.app` | 20.4 + 11.1 min | **0 min in the macOS job** |

The 5.3-minute difference is the duplicated gomobile build the experiment used to pay on every
run; it is now paid once per commit. A cold module cache makes the producer's Libbox step much
longer than 4.4 minutes (the first phase-1 build with an empty cache took over 12), so the ratio
matters more than the absolute number.

`check-release-workflows.py` passes (164 checks) locally and in CI. TestFlight was **not** run:
no upload, no notarisation, no App Store Connect call.

---

## Where this stands after the branch merge (2026-11-04)

The consolidation ran on a branch that has since been merged into the product line and deleted, so
two things had to change for it to remain true rather than merely documented:

* **The push trigger is gone.** It named `experiment/ios-selfsign-compat`; a trigger on a branch
  nobody can push to is a workflow that looks armed and is not. `workflow_dispatch` is the entry
  point (`gh workflow run experiment-ios-selfsign.yml -R … --ref testing`).
* **The Apple pin is the integrated client**, `4773b5f`, which is also the parent repository's
  gitlink: launch-path fixes, the loopback feature and upstream's four commits at one revision. The
  experiment now builds the client the product line ships.

### The reuse rule, measured end to end

Two dispatches against the same core commit (`bd722f8`):

| run | input | what the build did | BUILD-INFO |
|---|---|---|---|
| `41798935420` | `libbox=auto` | no validated artifact existed → built one and uploaded it as `jiejiebox-libbox-bd722f8…` | `libbox_source: built in this run (no validated artifact existed)` |
| `41803033272` | `libbox=required` | refused to build; consumed the previous run's artifact | `libbox_source: reused artifact 13307156543 (validated against manifest)` |

`required` is what makes this a test rather than a hope: had the artifact been missing or its
manifest not matched `--parent-sha`, the second run would have failed before compiling anything.

### What is still not claimed

`TestFlight` and the release path are untouched by this; no signed artifact was produced or
uploaded. The reuse rule is proven for the experiment build, which is the one that duplicated
Libbox work.
