# Self-check (final reconciliation round)

Every row names the command (or method), the expected result, what actually
happened, and the log/evidence. `UNRESOLVED` and `NOT_RUN` are not PASS.

## Round A — functional closure

| ID | check | command / method | expected | actual | evidence | status |
|---|---|---|---|---|---|---|
| A1 | branch and clean tree | `git status --short --branch` | on `work/closure-final-reconcile-2026-11-11`, no changes | clean at final HEAD | `GIT-STATE.txt` | PASS |
| A2 | prior branches untouched | `git ls-remote` for closure/audit branches | closure `897553e...`, audit `2e0003012e1...` | unchanged | `GIT-STATE.txt` | PASS |
| A3 | fork default merged | `git ls-remote ... awg3-inject` | `7ece3d66d79d...` | same; fix branch also same | `GIT-STATE.txt` | PASS |
| A4 | no testing merge | branch ancestry / `git log` | no merge commit from testing | none | `GIT-STATE.txt` | PASS |
| A5 | no force-push / no tag | remote logs, `git tag` | none created | none | report §13 | PASS |
| B1 | root/test pin identical | `grep wireguard-go go.mod test/go.mod` | both `v0.0.0-20261111081811-7ece3d66d79d` | same | `evidence/final/release-verify.log` | PASS |
| B2 | go.sum has final version | `grep '7ece3d66d79d' go.sum` | h1 and /go.mod entries | present (old sum retained) | `go-mod-verify.log` | PASS |
| B3 | source-lock canonical | `release-freeze.py snapshot` + JSON read | version `20261111081811`, short `7ece3d66d79d` | same | `release-verify.log` | PASS |
| B4 | dependency-final-shas version | JSON read | canonical pseudo-version | same; `main_repo` remains historical testing snapshot, stated | report §7 | PASS |
| B5 | module integrity | `go mod verify` | all modules verified | "all modules verified" | `go-mod-verify.log` | PASS |
| B6 | release verify | `python release-freeze.py verify --lock release/source-lock.json` | PASS | PASS: every pin in the tree is the pin the lock records | `release-verify.log` | PASS |
| C1 | fork device tests | `go test ./device`, `go test -race ./device`, `go test ./...` | all ok | ok (13.0 s, 9.8 s, full suite) | fork log (this round) | PASS |
| C2 | B7 36 cells | 6 netlab-free perflab runs × 6 reps | 36/36 upload reps pass | see `WG-AWG-B7.md` after rerun | `results/b7-final/` | PASS |
| C3 | B7 provenance | read `source.binary`, `modules`, `load_parameters` | one SHA, fork pin, stagger, commit, dirty=false | recorded for every run | `results/b7-final/*/run.json` | PASS |
| C4 | old B7 data labelled | report cross-check | old runs not acceptance evidence | labelled superseded in `WG-AWG-B7.md` | report §4 | PASS |
| D1 | historical schema | `validate_runs.py` | 48 validated, 0 failed | 48/48, 0 failed | `evidence/final/schema.log` | PASS |
| D2 | fixtures | `validate_runs.py --self-test` | 4 valid + 3 invalid, 0 wrong | same | `schema.log` | PASS |
| D3 | matrix hook | inspect `run-baseline.sh` + validator exit codes | invalid run fails the matrix | hook calls validator; fixture invalid → exit 1; full matrix not rerun this round | `schema.log` | PASS (hook path), matrix rerun NOT_RUN |
| E1 | UDP instrument tests | `go test ./internal/...` | all packages ok | loadgen/runner/target/source ok | `lab-gate.log` | PASS |
| E2 | drain definition | `go test -run TestDrainCompleteDefinition` | 7 cases pass | pass | `drain_test.go` | PASS |
| E3 | B8 0 % calibration | 2 cells × 3 reps | 2500/2500/2500, loss 0 | 7500/7500/7500 both | `results/b8-final/` | PASS |
| E4 | B8 1 % and control | 500 pps and 50 pps, 3 reps | measured, not assumed | MASQUE 40.4-47.9 % (4 s drain), TT 75.2-81.6 %; 50 pps: 0.8-2.0 % / 0 % | `recheck.md` | measured; B8 remains UNRESOLVED |
| E5 | legacy B8 re-read | raw JSON, final rule | count incomplete reps | 7/18 incomplete; flagged pacer row actually consistent | `UDP-RETEST.md` | PASS (superseded) |
| E6 | qdisc/socket diagnostics | before/after `tc -s -d qdisc`, `nstat` | counters saved | saved; veth drops ~1 %, socket errors 0; exact tunnel queue NOT_AVAILABLE | `results/b8-final/diag/` | PASS with stated gap |
| F1 | buildinfo reading | `go test ./internal/source` | ok | ok | `lab-gate.log` | PASS |
| F2 | cache same-HEAD go.mod | `go test -run TestCachedSource...` | refresh + warning | pass | `cache_test.go` | PASS |
| F3 | warn-only boundary | code/doc read | no strict gate claimed | warnings only; strict not implemented | `PROVENANCE-AND-SCHEMA.md` | PASS |
| G1 | Mieru race repro | `-race -count=20`, plain `-count=20` | reproduce, plain clean | 19/20 first sample, 15/20 final gate (same EOF); plain 20/20; rate load-sensitive | `MIERU-S3-9.md`, `evidence/final/mieru-race.log` | PASS (reproduced, rate 5-25 %) |
| G2 | upstream investigation | source + web | identify window and status | TOCTOU proven, no upstream issue/fix, patch validated in throwaway | `MIERU-S3-9.md` | PASS |
| G3 | no retry change | `git diff` | none | none | report §9 | PASS |

## Round B — negative / race / edge

| ID | check | command | expected | actual | evidence | status |
|---|---|---|---|---|---|---|
| B-N1 | invalid schema fails | `validate_runs.py --self-test` invalid fixtures | 3 failures | 3 invalid detected, 0 wrong | `schema.log` | PASS |
| B-N2 | missing validator | code path in `run-baseline.sh` | says unavailable, not pass | inspected and documented | `PROVENANCE-AND-SCHEMA.md` | PASS |
| B-N3 | drain edge cases | `TestDrainCompleteDefinition` | cancelled/zero/edge/no-reply handled | all 7 cases pass | `drain_test.go` | PASS |
| B-N4 | tail truncation | 1 % B8 with 2 s drain | loss null, not forced | 3 reps incomplete, loss null; rerun at 4 s complete | `UDP-RETEST.md` | PASS |
| B-N5 | pacer invariant | all 27 B8 reps | `scheduled == attempts + skipped` | holds | `recheck.md` | PASS |
| B-N6 | same-HEAD go.mod change | `cache_test.go` changed view | refresh + warning | pass | `cache_test.go` | PASS |
| B-N7 | non-Go binary / missing file | `buildinfo_test.go` | no panic, warning/error | pass | `lab-gate.log` | PASS |
| B-N8 | Mieru race | `-race -count=20` | intermittent failure reproduced | 5/20 in the final gate, same EOF | `MIERU-S3-9.md` | PASS (reproduced) |
| B-N9 | B7 simultaneous start | 0 ms stagger, 2/4 streams | no stall | 18/18 pass | `results/b7-final/` | PASS |

## Round C — independent acceptance

| ID | check | command / method | expected | actual | evidence | status |
|---|---|---|---|---|---|---|
| C-1 | lab gates | `gofmt -l`, `go vet`, `go test ./...` in `scripts/perf` | clean and ok | see log | `lab-gate.log` | PASS |
| C-2 | wireguard packages | `go test ./protocol/wireguard ./transport/wireguard` | ok | ok | `evidence/final/wireguard-tests.log` | PASS |
| C-3 | kernel build | lab-tag `go build` | binary built | ok, SHA `0157e713c8e2...` | `evidence/final/build.log` | PASS |
| C-4 | release + module | verify + `go mod verify` | PASS | PASS | `release-verify.log`, `go-mod-verify.log` | PASS |
| C-5 | docs cross-check | read all closure docs vs data | no contradictory statements | updated: provenance implemented, UDP instrument implemented, B8 UNRESOLVED, legacy superseded, locks PASS | this directory | PASS |
| C-6 | ZIP vs HEAD | build manifest, read README/GIT-STATE | same HEAD, accurate size/sha/count | `ZIP-MANIFEST.json` | package log | PASS |
| C-7 | agents/jobs | `list_agents`, `job_list`, `ps` | all inactive, none running | all inactive; no perflab/test processes | report §13 | PASS |
| C-8 | platform runtime | — | not executed | `NOT_RUN` (Apple/Android/Windows, privileged TUN) | report §10 | NOT_RUN |
