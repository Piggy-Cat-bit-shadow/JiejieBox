# Implementation report

## Commits

- `b5a1e77ba40` fix(perf): split UDP observation state, isolate target epochs and
  record completion. Files: loadgen (state machine, pacer metrics, sequence
  window), target (per-epoch counters), runner (effective parameters, quiesce
  result, runner/binary provenance, record derivation), schema v1.2, validator,
  report generator, tests.
- `9ab0d01eeed` fix(perf): initialize scenario extras before recording effective
  parameters (TrustTunnel panicked on a nil map; found by the first new run).
- The evidence commit(s) for this report and the regenerated tables follow.

Final pass: `a64719f7839` monotonic timing, independent observation completeness and unknown binary provenance; `7cc152fc059` timing calibration evidence. `quiesce_test.go` closes A05. The final gate and race logs are in `evidence/`.

## What each fix addresses

| review item | fix | test |
|---|---|---|
| 1.3 drain 50 ms | `drainStateFor` + split fields; loss only when estimable | `drain_test.go` 5 cases; `udp_record_test.go` |
| 1.4 warmup/quiesce | target per-epoch maps; structured `QuiesceResult`; epoch selected in runner | `target/epoch_test.go`; `udp_record_test.go` |
| 1.5 B8-final dirty runner | recovered diff + blob hash stored; data marked `PROVENANCE_INCOMPLETE`/`SUPERSEDED`; new clean set | `results/b8-final/PROVENANCE.md` |
| 1.6 pacer | configured/scheduled/attempt/write-completion + errors + phase/overrun + last timestamps | `pacer_test.go`, `params_test.go`, `udp_record_test.go` |
| 1.7 effective params | `loadParameters` after defaults, requested vs effective | `params_test.go` |
| 1.8 provenance | runner SHA + tree state, pre/post binary SHA, binary_changed, cache dirty disclosure | `provenance_test.go`, `cache_test.go` |
| 1.9 sequence window | `add` returns exact; truncation → unattributed, loss null | `drain_test.go`, `udp_test.go` |
| 1.10 report vs JSON | `report-b8.py` derives tables, checks invariants; old curve regenerated | generator output; deterministic test in self-check |

## Verification commands

```
cd scripts/perf && gofmt -l . && go vet ./... && go test ./...
python3 scripts/perf/validate_runs.py --self-test
python3 scripts/perf/validate_runs.py
python3 scripts/perf/report-b8.py <runs> --out table.md
cd ../wireguard-go && go test ./device && go test -race ./device
```

The exact logs and exit codes are in `SELF-CHECK.md` and the final gate log.

## Scope control

No production protocol code changed. No B7 matrix was rerun. No historical run
was edited. Mieru retry semantics were not touched.
