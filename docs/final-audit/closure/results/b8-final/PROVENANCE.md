# B8-final provenance reconciliation

## What the run records say

All nine `results/b8-final/*/run.json` were produced at commit
`f03b1c7289934dcc93db3b6886ce4d8547f836fe` with `source.dirty=true`. The dirty
list contains `M scripts/perf/internal/runner/runner.go` plus documentation files.
This means the **measured kernel binary SHA (`0157e713c8e2...`) is known, but the
exact runner source at run time is not bound by a hash**. That is an executor
provenance gap, separate from the kernel binary.

## What was recovered

The next commit, `1318fa6ff5b`, captured the working-tree runner file. The
recovered diff is stored as `runner-diff-recovered.patch` (SHA256
`1a05df5fd780ace5b2fe4713ebfb39ca9c3620a9e4a9c987d11c442f753a6a71`).
It contains only the `load_parameters` map alignment changes (the same keys and
values); the file blob at `1318fa6` is
`1fb4bc236484de5f7c66caec83ccbf37d403e3f3`.

A reviewer can therefore see what the uncommitted file *most likely* was. But
**no run-time hash of that file was recorded**, so this reconstruction cannot be
cryptographically bound to the nine runs. It is presented as recovered content,
not as proof.

## Status

- `results/b8-final/`: **PROVENANCE_INCOMPLETE / SUPERSEDED** for acceptance.
  The raw data stays on disk unchanged; the kernel SHA, qdisc/socket diagnostics
  and observed behaviour remain useful context.
- `results/b8-rate/`: runner code was committed (`c26a4dd`); the only dirty entry
  was the untracked `MIERU-UPSTREAM-ISSUE.md`. These runs have clean runner
  provenance, but they predate the split drain semantics and are treated as
  **legacy observations**, not as current-semantics loss.
- `results/b8-evidence-final/` (new): produced from a clean tree after the
  observation/epoch/provenance changes, with runner executable SHA, tree commit,
  effective parameters, pre/post binary hashes and per-epoch target counters.
  These are the acceptance data.

## Why not simply rerun b8-final as if it were clean

A fresh run with the new semantics is not comparable to the old one: the drain
state now distinguishes deadline misses from estimable loss, the target counter is
epoch-isolated, and the pacer reports completion rates. The old rows are kept as
history and labelled; the new set is the evidence.
