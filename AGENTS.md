# AI entry point

1. Read `KNOWLEDGE/README.md` first.
2. Record its Knowledge Baseline SHA. Do **not** assume the knowledge base describes `HEAD`.
3. Inspect `baseline..HEAD` with `git log` and `git diff --stat`; map changed paths to the listed modules.
4. Deep-read only changed modules and their cited source/tests. Reuse unchanged knowledge.
5. Source and Git are facts; this knowledge base preserves costly-to-reconstruct context, not implementation detail.
6. Normal development must not refresh this knowledge base. Refresh it only as an explicit Knowledge Refresh, then repin the baseline.
