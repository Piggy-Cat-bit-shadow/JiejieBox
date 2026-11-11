# Self-check (UDP evidence final closure)

Statuses: PASS = test/evidence ran; FAIL = defect found; BLOCKED = environment;
NOT_RUN = not executed; DEFERRED = out of scope; SUPERSEDED = old evidence.

## Round A 鈥?function and normal path

| ID | check | evidence | status |
|---|---|---|---|
| A01 | drain not decided by 50 ms alone | `drainStateFor` + `drain_test.go` 5 cases; `loadgen.go` state split | PASS |
| A02 | observation vs quiesce separated | `observation_*`, `quiet_period_ms`, `tail_active`, `drain_status`; `QuiesceResult` | PASS |
| A03 | loss null when not estimable | `udp_record_test.go` tail-active/truncated; new TT 8 s rows show null loss | PASS |
| A04 | target epoch isolates warmup | `target/epoch_test.go`; new runs `target_epoch_isolated=true`, per-epoch count | PASS |
| A05 | quiesce timeout explicit | `quiesce_test.go`: growing counter times out at ~2 s with status timeout; stable counter confirms; missing handle unavailable | PASS |
| A06 | pacer split counters | new run: configured 250, scheduled 249.99, attempt 249.99, completion 249.99; fields in JSON | PASS |
| A07 | achieved PPS uses completion time | `write_completion_pps` from last write end; `send_phase_ms`; `pacer_slow_test.go` | PASS |
| A08 | effective parameters | `load_parameters` resolved; `params_test.go`; new runs show duration 5/reps 3/drain 8000 | PASS |
| A09 | runner fingerprint separate | new `source.runner.sha256=6d9904...`, tree commit, dirty list; kernel SHA separate | PASS |
| A10 | pre/post binary SHA | `provenance_test.go`; new runs before=after=`0157e713...`, `binary_changed=false` | PASS |
| A11 | cache module/dirty | `cache_test.go` module refresh + dirty-from-cache warning | PASS |
| A12 | sequence truncation degrades | `drain_test.go` truncation; `sequence_tracking_complete=false`, loss null | PASS |
| A13 | schema covers new state | v1.2; 48/48 historical; 4 valid + 3 invalid fixtures; 27 new reps validate | PASS |
| A14 | B8 report from raw JSON | `report-b8.py`; tables generated for both sets; deterministic check | PASS |

## Round B 鈥?negative / edge / fault injection

| ID | check | evidence | status |
|---|---|---|---|
| B01 | no replies must not fake network loss | `drainStateFor` no-reply case returns complete_quiet; loss is labelled deadline-based, not network | PASS |
| B02 | replies at 2/4/8/12 s | TT 500: 8 s tail-active for 2 reps, 12 s complete with 7-9 s last reply | PASS |
| B03 | reply at window edge | `drain_test.go` tail-at-edge 鈫?`window_elapsed_tail_active` | PASS |
| B04 | cancel/target timeout degrade | cancel unit; quiesce timeout status; target unavailable 鈫?null | PASS |
| B05 | delayed warmup cannot pollute | epoch map test; measured epoch read only | PASS |
| B06 | mismatched epoch isolated | `epoch_test.go` (42/43; headerless not epoch 0) | PASS |
| B07 | blocking write lowers completion | `pacer_slow_test.go` slow callback skips slots, invariant holds | PASS |
| B08 | counter invariants | generator validates all 27 new + 45 legacy reps; no failure | PASS |
| B09 | defaults/partial/full params | `params_test.go`; new run JSON | PASS |
| B10 | dirty runner not claimed clean | b8-final marked `PROVENANCE_INCOMPLETE`; recovered diff stored; no clean claim | PASS |
| B11 | binary replaced mid-run detected | `provenance_test.go` changed=true | PASS |
| B12 | sequence over limit no false loss | truncation test; loss null | PASS |
| B13 | invalid JSON blocked | schema fixtures; validator gate; report refuses on schema error | PASS |
| B14 | report cannot disagree with JSON | generator output; old TT 2 s row now legacy_incomplete, no 77.2 | PASS |

## Round C 鈥?independent acceptance and scope

| ID | check | evidence | status |
|---|---|---|---|
| C01 | b8-final diff recovered? | `runner-diff-recovered.patch` + blob `1fb4bc23...`; runtime hash absent, stated | PASS (recovered, not bound) |
| C02 | unrecoverable old data downgraded | `PROVENANCE_INCOMPLETE`/`SUPERSEDED` in `PROVENANCE.md` | PASS |
| C03 | new set provenance | `source.runner`, pre/post SHA, effective params, dirty = untracked docs only | PASS |
| C04 | comparable data only | new set + labelled legacy; TT 12 s separate | PASS |
| C05 | 2/4/8/12 s unified semantics | regenerated legacy table; new table; no old loss reused | PASS |
| C06 | TrustTunnel 500 summary fixed | generated row: 2 s `legacy_incomplete`, not_returned 933/1134/1484, no loss | PASS |
| C07 | no unrelated re-runs | B7/other protocols not run; only 9 B8 cells | PASS |
| C08 | B7/schema/release/fork unchanged | read-only checks in final gate log | PASS |
| C09 | Mieru still unresolved | no retry code changed | PASS |
| C10 | platform NOT_RUN | unchanged | PASS |
| C11 | HEAD consistency | README/GIT-STATE/ZIP built at final HEAD | PASS (after packaging) |
| C12 | clean worktree/background | final `git status`, `list_agents`, `job_list` | PASS |
| C13 | new issue found | nil-map panic in TrustTunnel parameter recording, fixed in `9ab0d01`; first run set discarded | PASS |
| C14 | stop condition | measurement semantics fixed; root cause left explicitly UNRESOLVED | PASS |

## Gate commands

```
cd scripts/perf && gofmt -l . && go vet ./... && go test ./...
python3 scripts/perf/validate_runs.py --self-test
python3 scripts/perf/validate_runs.py
python3 scripts/perf/report-b8.py <runs> --out table.md
go test -tags "$TAGS" ./protocol/wireguard ./transport/wireguard
python3 scripts/ci/release-freeze.py verify --lock release/source-lock.json
go mod verify
```
