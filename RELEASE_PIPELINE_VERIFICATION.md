# RELEASE_PIPELINE_VERIFICATION

What was actually verified, at what level, and what is still not. The distinction the brief asks for is
between local verification, CI verification and production verification, so each claim carries which
one it has.

---

## 1. Level of every claim

| claim | level | evidence |
|---|---|---|
| Windows capability gate reads the target graph | **CI** | windows-core #37 (38445964597), #40 (38457146001) — success |
| the SIGPIPE pipeline could produce a false negative | **local + CI** | local: producer exit 0 alone, pattern present, **1214 packages, match on the LAST line**; CI: #36/#39 failed, #40 passed after the fix |
| the gate distinguishes absent / producer-failure / present | **local** | 4 negative tests executed; all three outcomes separated |
| `test-apple-signing.sh`'s tracked-key check had the same class | **local** | `git ls-files` = 209818 bytes ≫ 64 KiB buffer; rewritten check still PASSES clean and FAILS with a planted `.p12` |
| the trafficsched flake was a test bug | **local + CI** | CI: same SHA `6cfdc9ace` failed on two different tests in #232/#233. Local: counts measured high ~1000 / normal ~900, so the old 200-aggregate threshold is clearable by the high lane in well under a millisecond; 60 repetitions after the fix = 60 PASS, 0 FAIL |
| the Apple resolver's three bugs are fixed | **local** | `check-apple-resolver-posix.py`: 23 passed, 0 failed |
| workflow-wide resolve-once works on a runner | **CI** | Apple run #80: `resolve-apple-source` = **success** |
| GOOS/GOARCH/CGO explicit on every judging step | **local (static)** | step-scoped audit: 12 steps set their target, 2 documented exceptions (fuzz), 0 problems |
| the release tooling trust boundary is unpinned | **source** | `release.yml`: `path: tooling` with no `ref` |
| the Android staging cleanup is now correct | **CI (dry run)** | dry run #29 progressed past staging; it stopped later, at run selection, for an unrelated reason |
| **the release as a whole publishes correctly** | **NOT VERIFIED** | no Release has been created |
| **macOS arm64 is repeatably stable** | **NOT VERIFIED** | one flake source fixed; the wireguard census family is unresolved |
| **the Apple client chain reaches full SUCCESS** | **NOT VERIFIED** | run #80 stopped in `libbox` at a test fixture |

### What is deliberately absent

```text
no timing figures for any release stage      the prompt forbids them without a Mac, and there is none
no "3 consecutive same-SHA macOS runs"       not run - that is a repeated-dispatch exercise
no claim that the flake class is closed      it is not; one source is fixed, one is narrowed
no PRODUCTION_READY line                     a Release has never been created from this pipeline
```

---

## 2. Commands actually executed, and their results

```text
go list -deps -tags <windows tags> ./cmd/sing-box      exit 0, 1214 packages, masque on the last line
grep -Fx <masque> <graph file>                          exit 0
the gate's decision logic, 4 cases                      PASS / FAIL / FAIL(producer) / FAIL(producer)
git ls-files | wc -c / wc -l                            209818 bytes, 2918 lines
the rewritten tracked-key check, 2 cases                PASS on clean, FAIL on a planted .p12
go test ./common/trafficsched/                          ok 106.274s
the fairnes test, 60 repetitions                        60 RUN, 60 PASS, 0 FAIL
gofmt / go vet (trafficsched)                           clean / exit 0
check-apple-resolver-posix.py                           23 passed, 0 failed
check-release-workflows.py                              89 passed, 0 failed
YAML parse, every changed workflow                      pass
dash -n, every changed run block                        0 failures (PowerShell blocks N/A)
```

---

## 3. The one measurement that inverted my own conclusion

My first `grep` for masque on the Windows graph reported **no match**, which would have refuted the
SIGPIPE hypothesis. It was wrong: PowerShell 5.1 redirection writes **UTF-16LE**, and MinGit's `grep`
reads bytes. The file was 91352 bytes and plainly contained the line.

Recorded in the verification report because the failure mode — a false negative in the *measurement* —
is the same one this whole round was about, and it very nearly decided the question the wrong way.

---

## 4. What would move each open item

```text
macOS arm64 stability        one client-macos.yml dispatch, to read the EXISTING
                             transport/wireguard/census_diagnostic_test.go output. It is a report
                             and cannot fail on a census value, so it is safe as the next step.
                             Then 3 consecutive same-SHA runs, per §38.
Apple chain E2E              rewrite check-apple-source-selection.sh (~22 gitlink assertions) and fix
                             the overlay fixture, then one client-apple.yml dispatch.
release pipeline             apply REFACTOR_PLAN Batch 2 (mock gh real-layout), then Batch 1A,
                             then Batch 3 with its own dry run.
release itself               five product workflows green at ONE SHA, then a dry run, then publish.
```

---

## 5. Status lines, stated exactly

```text
CI_GATES_REMAIN_STRICT          YES   nothing was suppressed, widened or deleted
CI_GATES_ARE_DETERMINISTIC      YES for the gates fixed here (Windows capability, tracked-key)
                                NO  for macOS arm64 as a workflow (one flake source open)
PRODUCT_CODE_CHANGED            NO
MASQUE_CODE_CHANGED             NO
NAIVE_CODE_CHANGED              NO
APP_STORE_CONNECT_UPLOAD        NO
FORMAL_RELEASE_CREATED          NO
TAG_MOVED_THIS_ROUND            NO
```

The honest summary: **every defect found this round is fixed and has an executed test behind it**, and
the four things that are not fixed are named with the single action that would settle each. None of them
is blocked on a decision; two need a macOS runner, one needs a test rewrite, and one needs a dry run.
