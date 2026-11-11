# Evidence index

| claim | evidence |
|---|---|
| B7 root cause + fix | fork branch `fix/outbound-wake-2026-11-11` (`7ece3d66d79d`), `device/outbound_space_test.go`; pin in root/test `go.mod`, `go.sum`, both release locks |
| B7 two-process result | `results/b7/` (6 runs × logs), summary in `WG-AWG-B7.md` |
| Schema is the defect | `PROVENANCE-AND-SCHEMA.md`; `scripts/perf/validate_runs.py` (48/48 pass); `internal/schema/testdata/` (4 valid, 3 invalid, 0 wrong) |
| Matrix generation is gated | `scripts/perf/run-baseline.sh` validation hook |
| Binary provenance | `source.binary` in new run.json; `internal/source/buildinfo_test.go`; `perflab env -binary ...` output |
| UDP instrument | `internal/loadgen` paced mode; `udp_test.go`; smoke cell (400/400 returned at 200 pps); B8 runs under `results/b8/` and `results/b8-low/` |
| B8 answer | `UDP-RETEST.md` table (fixed offered load + low-rate control) |
| S3-9 distribution | `MIERU-S3-9.md`; 19/20 race, 20/20 normal |
| Earlier fixes still pass | `protocol/mieru` generation test, `common/tls` handoff tests, `transport/v2rayxhttp` close tests, lab tests |
| Pin consistency | `git diff release/source-lock.json release/dependency-final-shas.json` in the closure commits |

## Commands (repository root unless noted)

```sh
# schema
python3 scripts/perf/validate_runs.py --self-test
python3 scripts/perf/validate_runs.py

# B7 (lab binary built with the lab tag set)
perflab run -scenario scripts/perf/scenarios/wireguard-endpoint -out "$HOME/perf/b7fix" \
  -pattern upload -streams 2,4 -reps 3 -duration 3s -warmup 1s -stagger 0s
# repeat with -stagger 200ms / 1s and scenario awg-endpoint

# UDP paced cell
perflab run -scenario scripts/perf/scenarios/masque-connect-ip -out /tmp/udp \
  -bin-lab "$BIN" -pattern udp -streams 1 -reps 1 -duration 2s -udp-rate 200 -udp-drain 500ms
# B8 (80 ms, fixed offered load)
scripts/perf/netlab/netlab.sh --rtt-ms 80 --loss-pct 0.1 -- perflab run \
  -scenario scripts/perf/scenarios/masque-connect-ip -out /tmp/b8 ... -udp-rate 500 -udp-drain 500ms

# S3-9
go test -race -count=20 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru

# fork tests
cd ../wireguard-go && go test -count=3 -run 'TestRelease|TestStop' -v ./device
```
