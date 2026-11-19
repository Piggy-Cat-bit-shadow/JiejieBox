# TRIPLEWISE A — Verification

Environment: Windows 10.0.19044 amd64 · Go `go1.27.4` (the `toolchain` directive) · `testing` branch ·
base `413e115b3` (the head the pairwise C round left). `CGO_ENABLED=1` with the bundled mingw gcc where
`-race` is used; `CGO_ENABLED=0` for cross-builds.

## 1. Commands and results

```
go build ./...                                        # OK, whole module after the fix
go vet ./transport/v2rayhttp ./transport/v2raygrpclite  # clean
gofmt -l transport/v2rayhttp transport/v2raygrpclite    # no output

go test ./transport/v2rayhttp ./transport/v2raygrpclite ./transport/v2rayxhttp -count=1
    ok v2rayhttp 0.187s · grpclite 0.135s · xhttp 21.905s

go test ./transport/v2rayhttp -run \
  'TestALateDialAfterCloseInstallsNoGeneration|TestARetireOnAClosedPoolReleasesWhatItIsGiven' -count=1 -v
    PASS both (before the fix: both FAIL, see §3)

go test ./transport/v2rayhttp -run 'TestResetKeepsThePoolReusable|TestResetDoesNotCut|TestAnEpochChange|TestThePublishDecision' -count=1
    ok    # the pool's other generation contracts still hold after the change

go test -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" -count=1 ./transport/v2rayhttp ./transport/v2raygrpclite
    ok    # the shipped Windows profile, which the release builds with
```

## 2. Tests added

| Test | Package | Pins |
| --- | --- | --- |
| `TestALateDialAfterCloseInstallsNoGeneration` | `transport/v2rayhttp` | T1: a dial after Close builds no transport and leaves no generation in the closed pool |
| `TestARetireOnAClosedPoolReleasesWhatItIsGiven` | `transport/v2rayhttp` | T1 backstop: a closed pool releases an offered transport instead of adopting it |

Both use the production `Client`, the production `GenerationPool` and a real local TLS+HTTP/2 server; the
observations are a counting transport factory, the pool's own ledger and the server's accept counter.

## 3. Mutation controls (the evidence that the tests can fail)

| Control | Mutation | Result |
| --- | --- | --- |
| MC-T1a | the whole fix, i.e. the pre-fix tree | `TestALateDialAfterCloseInstallsNoGeneration`: `Should be zero, but was 1` (a transport was installed into the closed pool). `TestARetireOnAClosedPoolReleasesWhatItIsGiven`: `expected 0x1, actual 0x2` (the pool adopted the generation) |
| MC-T1b | `if false && p.closed` in `GenerationPool.Retire`, client guard kept | the pool test red again with the same `sequence` mismatch; the client-side test stayed green, which is what makes the two controls distinguish the two layers |

Both reverted. `git status` was clean before each commit, and the restored suites re-ran green.

## 4. What was not executed, and why

| Gap | Reason |
| --- | --- |
| the Critical × transition × real pool-rotation cross-product | needs a harness that holds the pressure source mid-transition and rotates a real pool deterministically; not approximated with a sleep |
| any iOS NE / native measurement (A02, A09, A14) | no device and no NE process; Go heap or Linux RSS substituted for `phys_footprint` would be a false green |
| the `sing-tun` UDP NAT triple (A03) | cross-repo fix plus a pin bump whose release gate requires a CI run for the new revision — not producible during a release freeze |
| a concurrent barrier test for the Close↔dial window | the flags close the window, but the test drives it sequentially; recorded in the effectiveness page as an evidence limit |
| `-race` on this round's packages | the change adds two `atomic.Bool` reads and one mutex-guarded branch, no new shared mutable state; the suites were run without `-race` and that is what is claimed |

## 5. Release position

The fix was verified in the **shipped Windows tag profile** as well as the default one, because the
release builds with `release/DEFAULT_BUILD_TAGS_WINDOWS`. The repository's release gates were re-run in
this session and pass: `release-freeze.py verify` (pins match the lock), `check-knowledge-facts.py`,
`go vet ./...`, and the full `go test ./...` sweep whose only failures are the four classified
build-tag/administrator cases documented in
`docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_RELEASE_READINESS.md`.
