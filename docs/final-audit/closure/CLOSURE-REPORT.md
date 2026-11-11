# Final evidence reconciliation report (2026-11-11)

## 1. Final state

`READY_WITH_EXPLICIT_BLOCKERS`. Every claim below is backed by a command, a raw
run, or a log in this directory. B8 and Mieru S3-9 remain `UNRESOLVED` with a
bounded locus and a next step; they are not counted as fixed.

## 2. Git

- Main repo branch: `work/closure-final-reconcile-2026-11-11`, started from
  `897553e81221761f2491b244cfb61332d61890e9`.
- Prior closure branch `work/closure-2026-11-11` is unchanged at `897553e...`;
  the audit branch `work/coupling-audit-2026-11-11` is unchanged at
  `2e0003012e1...`; `testing` was not merged and is unchanged.
- wireguard-go fork: default branch `awg3-inject` was fast-forwarded from
  `a765d957fd81...` to the fix commit `7ece3d66d79dcea5bbf872ebe76b1c7863b69ea6`
  (no merge commit), pushed to `Piggy-Cat-bit-shadow/wireguard-go`. Device tests
  and `-race` pass before and after the merge; the fix branch still points at the
  same commit.
- This round's commits are listed in `GIT-STATE.txt` (locks, provenance cache,
  drain/parameters, B8 evidence, this report, final B7 evidence).

## 3. Changes by commit

- `428c5555c15` release: regenerate `source-lock.json` with
  `release-freeze.py snapshot`; correct the pseudo-version in
  `dependency-final-shas.json`; `release-freeze.py verify` PASS.
- `96c20643048` perf(source): refresh the module view from `go.mod` on every
  cache use; `source_cache_modules_stale` warning; tests.
- `f03b1c72899` perf(udp): `drainCompleteFor` definition + tests; record
  `load_parameters` (patterns, streams, reps, duration, warmup, stagger, UDP
  rate/drain) in every run.
- `1318fa6ff5b` evidence(b8): final paced matrix, low-rate control,
  qdisc/socket diagnostics, manifest, recheck; Mieru brief; provenance/schema
  reconciliation.
- `c4e09f41b57` docs: reconcile provenance, UDP, B8 and Mieru with the final code.
- `067cceb562e` docs: finalize the B7 evidence chain and index.
- `7758798ba3b` evidence(b7): clean-tree matrix with binary, pin and parameters
  in every run; this report follows in the last docs commit.

## 4. B7: FIXED_AND_VERIFIED, full chain

- Source: fork `awg3-inject` = `7ece3d66d79d`; parent `a765d957fd81`; diff is
  only `device/send.go`, `device/peer.go`, `device/outbound_space_test.go`;
  device and `-race` suites pass.
- Pin: root and test `go.mod`, `go.sum`, `release/source-lock.json` and
  `release/dependency-final-shas.json` all name
  `v0.0.0-20261111081811-7ece3d66d79d`; `go mod verify` clean.
- Binary: rebuilt from the reconciled tree with the laboratory tags;
  sha256 `0157e713c8e2d43fc214e949cde40b9e3fc2dc812360c64cf41ea4b91f2bd3d6`,
  Go 1.25.5, CGO off, 19 tags, BuildInfo shows the wireguard-go pin above.
- Runs: 6 cells (WG and AWG 脳 stagger 0/200 ms/1 s) 脳 2/4 streams 脳 3 reps =
  36/36 pass, upload; every run records commit `067cceb562e`, dirty=false, binary SHA,
  `load_parameters` including stagger. Before the fix the 0/200 ms cells failed
  2/2; after the fork fix they pass. Raw runs: `results/b7-final/`.
- Honest caveat: the earlier `results/b7/` runs lacked binary SHA and stagger in
  their JSON; they are kept for history but the acceptance evidence is the new
  `b7-final` set.

## 5. Schema: FIXED_AND_VERIFIED

48 historical `run.json` under the published v1: 0 pass, 48 fail, 2,056 errors,
all `wire/latency/udp: null`. v1.1 allows exactly those three to be null and
restricts summary units; payload stays `perf-run/v1`. `validate_runs.py`
validates history (48/48) and fixtures (4 valid + 3 invalid, 0 wrong);
`run-baseline.sh` gates every matrix on it. Log: `evidence/final/schema.log`.

## 6. UDP instrument: FIXED_AND_VERIFIED; B8: UNRESOLVED

Instrument: paced mode, 20-byte sequence header, attempts/writes/scheduled/
skipped, unique/duplicate/out-of-order/foreign/stale, explicit drain definition,
target received/echoed, quiesce, run parameters; tests for pacer, integration,
runner derivation, drain edges.

Final B8 (`results/b8-final/`, 80 ms RTT, 500 pps, 1200 B, 5 s, 3 reps, drain
2 s with a 4 s MASQUE rerun where the 2 s tail was truncated):

- 0 %: both cells 7500/7500/7500 over three reps, zero loss.
- 0.1 % per direction: 0.08-0.16 % reported, matching the ~0.2 % expected.
- 1 % per direction at 500 pps: MASQUE 40.4-47.9 % loss (4 s drain, complete);
  TrustTunnel H2 75.2-81.6 % (complete).
- 1 % at 50 pps: MASQUE 0.8-2.0 %, TrustTunnel 0 %; both complete.

Diagnostics: the veth qdisc drops only the configured ~1 % of wire packets (60-96
per cell), no overlimits/requeues, and every UDP socket error counter is zero in
both namespaces. The client qdisc sees 40-54 % fewer packets at 1 % than at 0 %
(MASQUE 15.8 k 鈫?9.2 k; TrustTunnel 17.5 k 鈫?8.0 k), so the missing datagrams are
dropped inside the client/tunnel queue path before the veth. MASQUE target 鈮?unique means upstream loss dominates; TrustTunnel target > unique means both
directions are queue-limited. The exact queue (device queue vs transport send
buffer) is `NOT_AVAILABLE` without in-process instrumentation. No production
transport code was changed. Legacy B8 data (7/18 reps failing the final drain
rule) is `SUPERSEDED`; the archived legacy `b8` runs are not acceptance evidence.

## 7. Release and dependency consistency

Fork default = fix commit; `source-lock.json` was regenerated by the project's
own `release-freeze.py snapshot`, not hand-edited; its version is
`v0.0.0-20261111081811-7ece3d66d79d` with short SHA `7ece3d66d79d`.
`dependency-final-shas.json` has no generator, so its version field was corrected
to match; its `main_repo` fields remain the earlier testing snapshot and are
explicitly historical. `release-freeze.py verify --lock release/source-lock.json`
prints PASS; `go mod verify` prints "all modules verified". Logs:
`evidence/final/release-verify.log`, `go-mod-verify.log`.

## 8. Provenance

Binary SHA256, Go version, GOOS/GOARCH, CGO, tags with source, main module, and
deps/replace are read from `debug/buildinfo` and stored per run. Drift, tag/sidecar
mismatch, changed binary and stale cache produce warnings, not failures. The
source cache now recomputes the module view from `go.mod` even when HEAD is
unchanged, with a `source_cache_modules_stale` warning. The linked worktree
cannot carry `vcs.revision` (Go's `buildvcs` requires a `.git` directory), so the
chain is binary SHA 鈫?BuildInfo 鈫?build command 鈫?commit; that limitation is
recorded, not papered over. Strict mode is not implemented.

## 9. Mieru S3-9: UNRESOLVED, root cause located upstream

Pinned SDK v3.39.0 has a pick-to-attach TOCTOU: `Mux.DialContext` picks an
underlay, then `AddSession` has no liveness/`done` guard, and `done` closes only
after sessions are closed and awaited 鈥?which is exactly what unblocks the app
read with `io.EOF`. Deterministic probe: `AddSession` after `Close()` succeeds.
Upstream has no issue/tests for this; a throwaway P1+P2 patch gave 42/42 clean
`-race` runs. The adapter retry is rejected (cannot force a fresh underlay,
duplicate-target risk, contradicts the fork's ambiguous-EOF policy). Status stays
`UNRESOLVED`; next step is an upstream issue/PR and a `-race` quarantine job.
Reproduction: 1 failure in 20 `-race` runs (2/48 aggregate), 20/20 without race.

## 10. NOT_RUN / BLOCKED / DEFERRED

- Platform runtime: Apple, Android, Windows real devices and privileged TUN are
  `NOT_RUN` (environment).
- B8 root cause: `UNRESOLVED`, not `BLOCKED`; exact queue needs instrumentation.
- Mieru retry: `DEFERRED` pending product decision/upstream.
- F-01/F-05/F-06/F-07b/F-10/F-13/F-14/F-15/S3-7: unchanged `DEFERRED` decisions.
- The stage-1/2 performance matrices were not rerun: the only measured data-path
  change is the wireguard-go pin, which B7 covers; no claim of global
  no-regression is made.

## 11. Three self-check rounds

`SELF-CHECK.md` records A (functional), B (negative/race/edge), C (independent
acceptance), with command, expected result, actual result, log path and status
for each row. Unresolved items stay `UNRESOLVED`; `NOT_RUN` is not PASS.

## 12. Delivery ZIP

`JiejieBox_Final_Evidence_Reconciliation_2026-11-11.zip` (Desktop), containing
this report, self-check, GIT-STATE, commit list, closure docs, B7/B8 raw runs and
diagnostics, schema/fixtures/validator, release logs, Mieru repro, and the file
manifest. Size/sha256/file count are printed by the packaging command and stored
in `ZIP-MANIFEST.json`; the README and GIT-STATE point at the same final HEAD.

## 13. Stop confirmation

All subagents are inactive, no background jobs or test processes remain, and the
worktree is clean at the final HEAD. No merge to `testing`, no force-push, no
release tag; prior branches and the old ZIP were not modified.

