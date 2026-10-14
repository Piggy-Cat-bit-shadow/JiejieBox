# CI_PIPEFAIL_SIGPIPE_AUDIT

## ROOT_CAUSE

```text
go list -deps -tags "$BUILD_TAGS" ./cmd/sing-box | grep -qx '.../protocol/masque'
```

under `set -euo pipefail`.

`grep -q` exits the moment it matches, which closes the read end of the pipe. `go list` is still
writing, takes **SIGPIPE**, and dies with **141**. `pipefail` then reports the whole pipeline as
failed **even though grep succeeded**. MASQUE is present; the gate said it was not.

## PROOF

Every number below was measured on this host, not read from a comment. Run with the workflow's own
environment: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` and the real
`release/DEFAULT_BUILD_TAGS_WINDOWS`.

```text
GO_LIST_EXIT ............... 0            (the producer, uncontended, into a file)
packages ................... 1214
GREP_EXIT on the file ...... 0
MASQUE_FOUND ............... TRUE
exact match ................ github.com/sagernet/sing-box/protocol/masque
its position ............... line 1214 — THE LAST LINE
protocol/* in the graph .... 32 packages, masque among them
```

**The last-line position is what makes the race the typical case rather than an edge.** `grep -q`
cannot match any earlier than the final line, so it exits while `go list` still has output queued.
The producer is therefore almost always the one killed, which is why this failed most of the time -
but not always.

## A CORRECTION TO MY OWN FIRST MEASUREMENT

My first `grep` on this host reported **no match at all**, which would have "disproved" the
hypothesis outright. That was an artefact: PowerShell 5.1 redirection writes **UTF-16LE**, and
MinGit's `grep` reads bytes, so it saw `\xff\xfe` and NUL-riddled lines. The file was 91352 bytes and
plainly contained the line. Recorded because a false negative in the *measurement* is what nearly
sent this the wrong way, and it is the same failure mode as the bug itself.

Also measured, for the same reason: `git ls-files` at this repository's HEAD emits **209818 bytes /
2918 lines**, which is what makes the second instance below real rather than theoretical.

## AFFECTED_LOCATION

```text
.github/workflows/windows-core-amd64.yml
  step: "Verify the custom features are compiled in, from the build constraints"
  the MASQUE graph check
```

## WHY PREVIOUS RUNS COULD RANDOMLY PASS

The outcome depends on whether `go list` had already flushed its output when `grep` exited. That is a
scheduling race, not a property of the code:

```text
6737404d1  windows-core  success
#34        success
#35        success
#36        failure   <- same code
#39        failure   <- same code, after an unrelated release.yml change
```

The fix commit for the *host-graph* defect (`6737404d1`) did make `go list` query the right target,
and that was a real and separate bug. It did not remove this one, because the pipeline shape survived
the fix intact. Both bugs produced the identical error message, which is why they were easy to
conflate - and why the previous round's "verified by #37 success" was true but incomplete.

## FIX

The producer and the consumer are separated: the graph is written to a file, and the consumer reads
that file. The consumer is no longer inside the producer's lifetime, so it cannot kill it.

```sh
deps_file="$(mktemp)"
trap 'rm -f "$deps_file"' EXIT
if ! go list -deps -tags "$BUILD_TAGS" ./cmd/sing-box > "$deps_file"; then
  echo "FAIL: 'go list -deps' itself failed ..." >&2
  exit 1
fi
echo "package graph: $(wc -l < "$deps_file") packages for $GOOS/$GOARCH"
if ! grep -Fxq 'github.com/sagernet/sing-box/protocol/masque' "$deps_file"; then
  echo "FAIL: protocol/masque is not in the $GOOS/$GOARCH build's package graph" >&2
  ... prints the protocol packages actually present ...
  exit 1
fi
```

Constraints honoured, each deliberately:

```text
set -euo pipefail retained ......... yes, unchanged
set +o pipefail .................... NOT used
|| true ............................ NOT used
retry added ........................ NO
gate deleted ....................... NO
MASQUE / Naive / protocol code ..... UNTOUCHED
```

The three outcomes are now distinguishable, which was the other half of the requirement: a producer
failure is reported **as a producer failure**, not as an absent package.

## NEGATIVE_TEST

The gate's exact decision logic was executed against real and synthetic graphs:

```text
Case 1  real Windows graph, MASQUE present  -> PASS   (1214 packages)
Case 2  MASQUE genuinely absent             -> FAIL   "is not in the graph"
Case 3  producer itself fails               -> FAIL   "'go list -deps' itself failed"
Case 4  producer fails mid-stream           -> FAIL   "'go list -deps' itself failed"
```

真存在 → PASS, 真不存在 → FAIL, producer 出错 → FAIL. All three distinguished.

**`set -o pipefail` could not be replayed on this host**: MinGit's dash has no such option, so the
pipeline's own exit status is not reproducible here. Stated rather than glossed. What establishes the
bug is the combination of measurements above plus the CI failure itself; the mechanism is OS-level
and not shell-specific.

## SIMILAR_PATTERNS_FOUND

A scan of `.github/workflows/**`, `scripts/**` and `release/**` for
`large-producer | early-exiting-consumer` returned **22 hits**. Hits are not bugs - each was triaged on
whether the producer can still be writing when the consumer exits, which is a question of output size
against the **64 KiB** Linux pipe buffer.

### CONFIRMED_BUG — 1

```text
.github/workflows/windows-core-amd64.yml   go list -deps | grep -qx      (the gate above)
```

### POTENTIAL_RISK_FIXED — 1

```text
scripts/ci/test-apple-signing.sh:303-304
    git ls-files | grep -qiE "\.(p12|pfx|p8|cer|mobileprovision|provisionprofile)$"
```

**Why it is real:** `git ls-files` emits **209818 bytes** at this HEAD - three times the pipe buffer -
so `grep -qiE` can exit while git is still writing, and the pipeline fails under pipefail. The
consequence is worse than a nuisance: the check is negated, so a false pipeline failure reports
**"signing material was tracked" on a clean tree**. In `test-apple-signing.sh`, which
`client-apple.yml` runs, that is a spurious failure of a security-shaped assertion.

**Fixed** the same way - listing to a file, then grep the file - and **re-verified the discrimination
is unchanged**: PASSES on a clean listing, FAILS on one with a planted `.p12`.

### REVIEWED_SAFE — 20

Grouped, with the reason each cannot race:

```text
go version -m "$gobin/$tool" | grep -q ...        android-core-arm64.yml:236
                                                  scripts/ci/gomobile-toolchain.sh:146
    go version -m emits a few dozen lines - far under 64 KiB - and the pattern it looks for appears
    near the TOP, so the producer finishes long before the consumer can exit. Bounded producer.

find <dir> -name X | head -1                      10 sites (android 171/292, build-ios-ipa 68/173,
    Bounded by the directory's contents: one     build-ios-testflight 200, build-macos-dmg 204/228,
    match, or a handful, matching a specific      build-macos-testflight 142, verify-apple-signed-
    name. No large output to be interrupted.     artifact 104/118/134)

find ... | head -N (diagnostics only)             3 sites; output not consumed, status not used

command -v curl && curl --version | grep -q       jiejie-masque-vps.sh:321 - a version string, tens
                                                  of bytes

grep -n ... | head -1 | cut   (test assertions)   test-apple-beta-publish.sh:381 - reads its OWN
                                                  small file, and the pipeline is inside `bash -c`
                                                  where the exit status is the assertion itself
```

Verified by hand for each: **no producer with a potentially large, slow or unbounded output is piped
into a consumer that exits early in a pipeline whose status matters.**

## SIMILAR_PATTERNS_FIXED

```text
windows-core-amd64.yml   MASQUE graph gate            CONFIRMED_BUG
test-apple-signing.sh    no-signing-material check   POTENTIAL_RISK_FIXED
```

## SIMILAR_PATTERNS_REVIEWED_BUT_SAFE

20 sites, enumerated above. **Not included in the fixed count** - reporting 22 findings as 22 bugs
would be exactly the inflation the brief forbids.

## FILES_CHANGED

```text
.github/workflows/windows-core-amd64.yml   the gate: producer and consumer separated
scripts/ci/test-apple-signing.sh           the same fix for the tracked-key listing check
```

## PRODUCT_CODE_CHANGED

```text
NO
```

## RUNTIME_BEHAVIOR_CHANGED

```text
NO.  Only two CI shell gates changed; both keep their strictness and their fail-closed behaviour.
```

## WINDOWS_CORE_RERUN

```text
WINDOWS_CORE_RERUN = success   (run 38457146001, #40, at 857a4ed38)
```

Dispatched **alone**, as instructed - not as part of a full release re-run.

## FINAL_STATUS

```text
CURRENT_WINDOWS_FALSE_NEGATIVE = FIXED
PIPEFAIL_SIGPIPE_AUDIT         = COMPLETE
PRODUCT_CODE_CHANGED           = NO
MASQUE_CODE_CHANGED            = NO
NAIVE_CODE_CHANGED             = NO
CI_GATES_REMAIN_STRICT         = YES
CI_GATES_ARE_DETERMINISTIC     = YES for the two fixed gates

VERIFIED:  LOCAL (measurements, negative tests) + REAL GITHUB RUN (#40 success)
NOT VERIFIED: the SIGPIPE exit status itself could not be replayed locally (no pipefail in MinGit's
              dash); the conclusion rests on the producer's own exit 0, the pattern's presence, the
              last-line position, and the CI failure.
```

One thing this audit does **not** claim: that no other pipeline of this shape exists anywhere. The
scan covered the CI and release surface the brief named, and every hit was triaged. A producer added
later with unbounded output would need the same treatment, which is why the fixed gate now prints the
graph size - so the next reader can see how close a pipeline would come to the buffer limit.
