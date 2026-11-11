# Final audit (stage 3)

The last stage of the performance/coupling programme: attack the two earlier
stages, not restate them. It asks whether the laboratory is trustworthy, whether
the runtime's shared mechanisms have leaks/races/deadlocks, whether anything was
over-coupled, and whether the final HEAD regressed against the stage-1 baseline.

## State

| item | value |
|---|---|
| branch | `work/coupling-audit-2026-11-11` |
| HEAD | `f30e36a4dea` — merge of the current remote `testing` tip into the audit branch |
| stage-1 commits | `bf0e0bec560` … `53a9f5dc8e2` (6) |
| stage-2 commits | `992087a57bb`, `47288906810`, `cde28636176`, `55aca33e4a2` (4) |
| stage-3 commits | `11ff9575da0` (lab pin parser + sampler tests) and the commit that carries this directory |
| worktree | clean at commit time; the only uncommitted scratch is outside the repository |
| base | `4ec0decd9f2d`; the remote branch that hosted it was deleted during the work, `testing` advanced 15 commits; the merge brought those in |
| submodules | `clients/{apple,android,desktop}` pinned as in `testing`; not built here |

## Documents

| document | contents |
|---|---|
| [FINAL-REPORT.md](FINAL-REPORT.md) | the report in the requested order, with the verdict |
| [ISSUE-LEDGER.md](ISSUE-LEDGER.md) | every stage-1/2/3 issue with its final status |
| [DEBUG-REPORT.md](DEBUG-REPORT.md) | what was attacked, how, and what the evidence says |
| [OVER-COUPLING.md](OVER-COUPLING.md) | the 18 questions, each answered keep/shrink/split/delete |
| [PERFORMANCE.md](PERFORMANCE.md) | regression matrix, method and resource numbers |
| [evidence/](evidence/) | the B7 goroutine dump and peer-queue markers |
| [results/](results/) | the final runs, raw JSON + rendered configs + gzipped logs |

## Reproduce

```sh
# build the final binary (tags used by every number here)
cd scripts/perf && go build -o "$HOME/perf/bin/perflab" ./cmd/perflab
cd ../.. && CGO_ENABLED=0 go build -tags \
  "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),with_trusttunnel,with_awg,with_naive_outbound,with_purego" \
  -o "$HOME/perf/bin/sing-box-final" ./cmd/sing-box

# one failing B7 cell (expected: sent=0 on the second flow)
"$HOME/perf/bin/perflab" run -scenario scripts/perf/scenarios/wireguard-endpoint \
  -out /tmp/perf-b7 -bin-lab "$HOME/perf/bin/sing-box-final" \
  -pattern upload -streams 2 -reps 1 -duration 5s -warmup 1s

# the same cell with spaced starts (expected: passes, which is the diagnosis)
"$HOME/perf/bin/perflab" run -scenario scripts/perf/scenarios/wireguard-endpoint \
  -out /tmp/perf-b7 -bin-lab "$HOME/perf/bin/sing-box-final" \
  -pattern upload -streams 2 -reps 2 -duration 5s -warmup 1s -stagger 1s
```

A failed cell is evidence, not noise: the two runs above differ only in stream
spacing. The goroutine dump that explains it was taken by sending `SIGQUIT` to the
client while the cell was running; the relevant stacks are preserved in
`evidence/b7-wireguard-wedge.txt`.
