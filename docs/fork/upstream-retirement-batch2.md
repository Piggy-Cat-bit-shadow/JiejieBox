# Upstream retirement, batch 2: the lean maintained-patch index (audit Q47)

Batch 1 answered "which fork commits have an identical upstream patch" mechanically. Batch 2's object
was the other side: the 3562 commits upstream does **not** carry, "grouped into logical patches, each
with trigger/regression/consumer". That population is history, not a patch list: it includes two
months of merges, rebases and rewrites, most commits carry no maintenance marker, and a per-commit
trigger cannot be derived without adjudicating each one. Writing 3562 generated rows would look like
completion while saying nothing.

This batch therefore builds the **lean** index the audit's wording asks for: the logical patches the
fork still marks as its own. A patch is in scope when a source or documentation line carries a
`SPEC <id>` reference (the fork's own task identifier). The marker text is the trigger evidence, the
files are the consumers, and the tests in those packages are the available regression surface.

## Mechanical result

| Fact | Value |
| --- | --- |
| marker lines (`lx:` / `SPEC <id>`) in the tree | **162** |
| logical patches, grouped by SPEC id | **28** |
| patches with at least one blamed commit | 28 |
| patches with available package tests | 25 (3 are documentation-only) |
| patches with an upstream equivalent patch-id at the pinned tip | **0** |
| disposition | **HOLD** for all 28 |

The machine-readable index is [`upstream-retirement-batch2.tsv`](upstream-retirement-batch2.tsv):
`spec, commits, first_date, last_date, trigger_marker, consumer_files, regression_tests, disposition,
retirement_condition`.

## What each column means - and does not

- **trigger_marker** is the marker comment itself, with `file:line`. It is the source of the trigger
  claim for review; it is NOT a rewritten "user-visible symptom" paragraph. A row whose marker is a
  test name (for example `SPEC 016`) is a regression reference, not a full trigger.
- **consumer_files** is every file carrying that SPEC id. It is the patch's footprint, not a proven
  list of runtime call sites.
- **regression_tests** lists the test files present in the touched packages. That is the available
  regression surface; it does not claim each file exercises this patch. Documentation-only rows say
  `none in package`.
- **disposition** is HOLD for every row: none of these patches has an upstream patch-id at the pinned
  tip, and "no upstream equivalent" is not evidence that the fork no longer needs the change.
- **retirement_condition** is the same for every row: upstream ships an equivalent change (verify
  with `git show <blame-sha> | git patch-id --stable` and the same command on the upstream candidate),
  or the product drops the behaviour. Neither is true today.

## Reproduction

```sh
python scripts/ci/upstream-retirement-batch2.py --write   # regenerate the TSV (28 rows)
python scripts/ci/upstream-retirement-batch2.py --list    # spec, commits, dates
```

The tool blames each marker line to find the commit that introduced it. A marker copied by a later
commit attributes to the copy, which is why `commits` is the set of blamed commits, not a count of
every commit that ever mentioned the SPEC.

## Remaining batches (unchanged, and not claimed)

| batch | object | state |
| --- | --- | --- |
| B2 remainder | the unmarked fork-only commits (history without a maintenance marker) | NOT individually indexed. They are the 3562-commit population minus the 28 marked patches; indexing them needs per-topic adjudication, which this batch does not fabricate. `python scripts/ci/upstream-retirement-batch2.py --list` is the lean set. |
| B3 | the 218 retire-ready duplicate patches | still requires a rebase plan and approval: it rewrites published history. |
| B4 | dependency forks (sing, sing-tun, sing-quic, cronet-go) | Q42's pin/CI binding. |

Batch 2's contribution is that the maintained set is now small, traced to commits and files, and
checked for upstream equivalence; the unmarked history is named as unindexed rather than silently
counted as done.
