# Evidence index

| claim | evidence |
|---|---|
| B7 root cause + fix | fork branch `fix/outbound-wake-2026-11-11` (`7ece3d66d79d`), `device/outbound_space_test.go`; this branch's pin in root/test `go.mod`, `go.sum`, both release locks |
| B7 two-process result | `results/b7/` (6 runs × logs), summary table in `WG-AWG-B7.md`; command in that file |
| Schema is the defect | `PROVENANCE-AND-SCHEMA.md`; `scripts/perf/validate_runs.py` (48/48 pass); `scripts/perf/internal/schema/testdata/` (4 valid, 3 invalid, self-test 0 wrong) |
| Matrix generation is gated | `scripts/perf/run-baseline.sh` validation hook |
| S3-9 distribution | `MIERU-S3-9.md`; command and 19/20 + 20/20 results |
| UDP instrument cannot measure loss | `UDP-RETEST.md` (file:line audit + designed fix); no paced-mode code was landed |
| Provenance limitation | `PROVENANCE-AND-SCHEMA.md` (buildinfo fields, `-buildvcs` worktree limitation, sidecar requirement) |
| Release pin consistency | `git diff release/source-lock.json release/dependency-final-shas.json` in commit `699258bfa95` and the closure commit |
| Earlier fixes still pass | `protocol/mieru` generation test, `common/tls` handoff tests, `transport/v2rayxhttp` close tests, `run-baseline`/lab tests — rerun during this round |

## Commands (from the repository root unless noted)

```sh
# schema
python3 scripts/perf/validate_runs.py --self-test
python3 scripts/perf/validate_runs.py

# B7 (lab binary built with the lab tag set)
perflab run -scenario scripts/perf/scenarios/wireguard-endpoint -out "$HOME/perf/b7fix" \
  -pattern upload -streams 2,4 -reps 3 -duration 3s -warmup 1s -stagger 0s
# ... repeat with -stagger 200ms / 1s and scenario awg-endpoint

# S3-9
go test -race -count=20 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru

# fork tests
cd ../wireguard-go && go test -count=3 -run 'TestRelease|TestStop' -v ./device
```
