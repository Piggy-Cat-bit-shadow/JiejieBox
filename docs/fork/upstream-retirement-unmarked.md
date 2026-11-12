# Fork-only history: the full factual index (audit Q47 B2 remainder)

`upstream-retirement-batch2.tsv` indexes the 28 patches this fork explicitly marks (SPEC references).
This page and [`upstream-retirement-unmarked.tsv`](upstream-retirement-unmarked.tsv) cover the rest:
every non-merge commit in `HEAD` that is not in the pinned `upstream/testing` tip.

## What the index contains

| fact | value |
| --- | --- |
| non-merge commits in `HEAD --not upstream/testing` | **3790** |
| normalized-subject groups | **3361** |
| rows with test files in the same commit | available per row |
| rows with a non-test consumer file | available per row |
| disposition | **REVIEW** for every row |

Columns: `group, commit, date, subject, trigger, regression_tests, consumer_files, disposition`.

- **trigger** is the first paragraph of the commit body (the author's own statement), or the subject
  when the body is empty. It is source material for review, not a rewritten symptom statement.
- **regression_tests** / **consumer_files** are the test and non-test files the commit touched. A
  commit is not proof that those tests exercise the change, and a touched file is not a proven runtime
  call site.
- **group** is a hash of the normalized subject (conventional prefix and trailing `(#n)` removed), so
  commits that describe the same logical change can be reviewed together. It is a reading aid, not an
  automatic merge decision.
- **disposition** stays REVIEW: this tool does not decide that a fork-only change is still wanted.
  That is the per-topic decision the audit asks for, and the user approved it as a review task.

## Relationship to batch 1 and B3

Batch 1 (patch-id) classified the history into 2947 same-SHA upstream commits, 218 duplicate
patch-ids (DROP-ready) and 3561 with no upstream equivalent. This index does not recompute patch-ids;
it is the complete factual list for the remainder, including commits that later changed the same
files.

B3 (the 218 duplicate drops) is a history-rewrite decision. The user approved it; the execution plan
and its state are recorded in [`upstream-retirement-b3.md`](upstream-retirement-b3.md). The index in
this page is not the B3 list.

## Reproduction

```sh
python scripts/ci/upstream-retirement-unmarked.py --write
python scripts/ci/upstream-retirement-unmarked.py --summary
```

The index is intentionally large (about 1.2 MB): it is the complete review input, not a summary. The
lean 28-row ledger remains the place to start for the patches the fork still marks.
