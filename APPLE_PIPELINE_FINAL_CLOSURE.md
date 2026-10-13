# APPLE_PIPELINE_FINAL_CLOSURE

```text
FINAL_SHA = e0af1beed25c87685faa63661bbe71c9f4c4f715   (testing)
APPLE_PIPELINE_FINAL_CLOSURE = INCOMPLETE
RELEASE_FREEZE = KEEP

FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
APP_STORE_CONNECT_UPLOAD = NO
```

Two of the five required items are done and verified here; three are not, and the reason in each case
is a capability this host does not have — **not** a decision left open. The honest per-item position
is §7, and §8 says exactly what one command would close each gap.

---

## A. Final authority

```text
DEFAULT_SOURCE              the tip of APPLE_CLIENT_BRANCH (main) on APPLE_CLIENT_REPOSITORY
RESOLVED_SHA                not recorded in this report: resolving it requires the network, and a
                            value written here would be a value I did not measure
WORKFLOW_WIDE_RESOLVE_ONCE  IMPLEMENTED and contract-tested; NOT yet observed on a runner (§D)
GITLINK_ROLE                developer convenience: the local default checkout, history, and the
                            `clients/apple` submodule. NOT the build authority. The difference is
                            reported and available as a hard gate via
                            APPLE_CLIENT_REQUIRE_GITLINK=1
OVERRIDE_BEHAVIOR           APPLE_CLIENT_REF / APPLE_CLIENT_SHA = 40-hex SHA | tag | branch, or the
                            repository variable of the same name; reported as
                            latest-main / explicit-ref / explicit-sha
```

### B1 — workflow-wide resolve-once: implemented

The frozen state file made resolve-once true within one shell, one runner, one workspace. It could
not make it true for the workflow, because `libbox`, `ios` and `macos` are separate jobs on separate
runners — so this remained possible:

```text
libbox resolves main -> A
someone pushes  main -> B
ios resolves    main -> B        <- one release, two Apple client commits
```

A new `resolve-apple-source` job is now the only place in `client-apple.yml` permitted to resolve the
default branch tip. It publishes five job outputs and writes the same values into the run summary.

```text
resolve-apple-source        (ubuntu-latest; resolving a SHA needs no macOS runner)
  |-- libbox   needs: resolve-apple-source
  |-- ios      needs: [resolve-apple-source, libbox]
  `-- macos    needs: [resolve-apple-source, libbox]
```

Both edges are real data dependencies. `ios` and `macos` remain siblings.

The consumers no longer call `resolve`. Each reads
`needs.resolve-apple-source.outputs.apple_client_sha` and **seeds** it into its own workspace's
`build/apple-client-source.env`, so the scripts that job then calls (`checkout ios|macos`) consume
the run-wide answer rather than asking the remote again. Seeded, never re-resolved.

`assert-shared-source` is kept in both GUI jobs, and it is now a stronger statement than before: it
no longer checks that two independent resolutions happened to agree, it checks that the single
run-wide value is what both platforms actually use.

Contract added — `check_apple_resolve_once()` in `scripts/ci/check-release-workflows.py` asserts the
resolver job exists and publishes `apple_client_sha`, that all three consumers `needs` it, that a
consumer reads the output, that **no** job calls `apple-client-source.sh resolve`, and that **only**
the resolver may consult the remote. That last pair is what stops a revert to per-job resolution.
**Checker: 89 passed, 0 failed.**

### B3 — provenance: implemented for BUILD-INFO

The Apple BUILD-INFO files carried `apple_repo`, `apple_branch`, `apple_sha` — but the label saying
where `apple_sha` came from read `parent clients/apple gitlink`, which stopped being true when the
authority moved. **A provenance line that names the wrong source is worse than no line, because it is
believed.** It now reads `resolved <mode> (Apple client repository, once per run)`, and the gitlink is
recorded separately as `apple_gitlink_sha` because the difference is real information.

The two sources are now named apart, with a comment saying why they are *expected* to differ:

```text
libbox_source_sha / libbox_parent_sha   = the CORE commit
apple_sha                               = the APPLE CLIENT commit
```

---

## C. Provenance fields

```text
CORE_SHA            parent_sha / libbox_source_sha / libbox_parent_sha   in BUILD-INFO-IOS.txt and
                                                                        BUILD-INFO-MACOS-APP.txt
APPLE_CLIENT_SHA    apple_sha                                            in both
APPLE_CLIENT_REPOSITORY / APPLE_CLIENT_BRANCH / APPLE_CLIENT_SOURCE_MODE  in both
LIBBOX_PARENT_SHA   yes, and documented as legitimately != APPLE_CLIENT_SHA
BUILD_NUMBER        APPLE_BUILD_NUMBER, already shared by both platforms
MARKETING_VERSION   version, from release/JIEJIE_VERSION
```

**Not wired, and recorded rather than guessed at:** the Libbox manifest deliberately records no
Apple revision (a contract test asserts `"apple_submodule_sha"` is absent, because Libbox keys on the
core SHA), and the local `publish-apple-beta.sh` / `release-apple.sh` summaries do not yet print the
Apple client SHA. Those two are the remaining half of Blocker 3.

---

## D. workflow evidence

**No CI run is claimed.** `STAGE_2_SOURCE_RESOLVER_CI = NOT RUN` and
`STAGE_3_LIBBOX_ONLY_CI = NOT RUN`: `client-apple.yml` now needs a `mode` input that does not exist
yet (Blocker 4), and dispatching the workflow as it stands would exercise the new resolver job but
not the libbox-only path this task is about. Rather than claim half of Stage 2, the report records
none of it.

What is verified about the graph is structural and reproducible here:

```text
client-apple.yml parses               PASS
resolve-apple-source                  needs=None
libbox                                needs=resolve-apple-source
ios                                   needs=[resolve-apple-source, libbox]
macos                                 needs=[resolve-apple-source, libbox]
resolve outputs                       apple_client_sha, apple_client_repository,
                                      apple_client_branch, apple_client_ref,
                                      apple_client_source_mode
no consumer calls resolve             PASS (asserted)
only the resolver uses ls-remote      PASS (asserted)
```

That the *jobs* consume one SHA is asserted. That *GitHub* runs them that way is not observed, and I
am not calling it observed.

---

## E. TF build count

```text
BEFORE = 5     Libbox(CI) 1, unsigned iOS(CI) 1, unsigned macOS(CI) 1, iOS TF archive 1, macOS TF archive 1
AFTER  = 5     unchanged by this round — Blocker 4 is not implemented
TARGET = 3
```

Ten of §32's seventeen criteria are not met, and they are all downstream of the two blockers below.
Reporting a 3 here would be inventing it.

---

## F. archive verification

```text
iOS archive-only     NOT RUN - requires macOS and Xcode
macOS archive-only   NOT RUN - requires macOS and Xcode
```

`BLOCKED_BY_ENVIRONMENT`. The iOS DerivedData fix from the previous round remains
**`VERIFIED_SOURCE` only** and its §17.1 A/B has still not been performed, so nothing about signing
behaviour is claimed.

---

## 7. §32 criteria, item by item

```text
 1 workflow run resolves one Apple SHA ......... MET (implemented + contract-tested; unobserved on a runner)
 2 libbox/ios/macos consume one job output ..... MET (same caveat)
 3 old gitlink contract suite rewritten ....... NOT MET - Blocker 2 untouched
 4 gitlink mismatch non-fatal by default ...... MET
 5 REQUIRE_GITLINK restores the old behaviour .. MET (all three gates)
 6 provenance records core SHA + UI SHA ....... MET for BUILD-INFO; NOT for the publish summaries
 7 TF publish no longer requires unsigned jobs . NOT MET - Blocker 4
 8 libbox-only workflow really dispatched ..... NOT MET - Blocker 4
 9 libbox-only builds no unsigned IPA/DMG ..... NOT MET - Blocker 4
10 publish consumes a libbox-only artifact .... NOT MET - Blocker 4
11 iOS fresh DD archive really passes ......... NOT MET - needs macOS
12 macOS archive-only really passes ........... NOT MET - needs macOS
13 nested submodules from the resolved commit . MET by source (both checkouts run
                                                `submodule update --init --recursive` AFTER detaching)
14 third hard-coded Apple SHA cleared ........ NOT MET - see §9
15 no signing / entitlement gate weakened ..... MET (no gate touched)
16 no App Store Connect upload ................ MET
17 no tag / release operation ................. MET
```

---

## 8. What one command closes each gap

```text
Blocker 2 (contract suite)   nothing but editor time on this host: the suite is bash, so it cannot
                             be RUN here, but it can be REWRITTEN here. ~22 assertions plus the
                             fixture premise at :333-336.
Blocker 4 (libbox-only)      a `mode` input on client-apple.yml, a mode argument on
                             apple-ci-job-gate.py, and the publish gate — then ONE real
                             `gh workflow run client-apple.yml -f mode=libbox-only`.
Blocker 5 (real archive)     one macOS machine with Xcode: `build-ios-testflight.sh --archive-only`
                             and `build-macos-testflight.sh --archive-only`. Must not upload.
Blocker 3 (rest of it)       print APPLE_CLIENT_SHA in the publish and release summaries.
```

None of these is a decision for the maintainer; they are work this host cannot finish. Blocker 5 is
the only one that needs different hardware rather than more time.

---

## 9. §18 — the third hard-coded Apple pin

`check-libbox-abi.sh:155` carries `VERIFIED_APPLE_SHA="40dd48795579718008518ac51af320b1d61377ac"` and
`:477` compares it against the gitlink. It has **no caller anywhere in the repository**, so it cannot
fail a build — but it is a third copy of the Apple revision, exactly the "second value that can
disagree with the authority" the refs-file rule exists to prevent, and it will mislead the next reader
who greps for the pin.

**NOT DONE.** §18 asks for this in the same round, and §18.2 says to remove only the hard-coded SHA
and not the script. It is a two-line change, but `check-libbox-abi.sh` is a bash script this host
cannot execute, so an edit here could not be verified even to parse, and the file is outside the
`client-apple`/TF surface this round touched. Recorded as the smallest remaining item.

---

## 10. Verification performed here

```text
client-apple.yml YAML parse              PASS
job graph + job outputs                  PASS (5 outputs, 3 consumers, all needs correct)
check-release-workflows.py               89 passed, 0 failed  (9 new Apple assertions)
dash -n apple-client-source.sh           PASS
dash -n build-ios-testflight.sh          PASS
dash -n publish-apple-beta.sh            NOT POSSIBLE (pre-existing bash here-string at :204)
real xcodebuild archive                  BLOCKED_BY_ENVIRONMENT
GitHub Actions dispatch                  NOT RUN
App Store Connect upload                 NOT PERFORMED
```

**No timing figures are reported**, per §30: there is no Mac here, and a guessed number would be
worse than none.

No tag was created or moved, no Release was created, no asset was published, and nothing was
uploaded to App Store Connect.
