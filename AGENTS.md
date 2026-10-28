# AI entry point

## Read this in order

1. **`KNOWLEDGE/README.md`** — the baseline, the reading order, the status labels.
2. **`KNOWLEDGE/BASELINE.md`** — the exact SHA this knowledge describes, and how to compute the delta
   since. **Do not assume the knowledge base describes `HEAD`.**
3. **`KNOWLEDGE/INDEX.md`** — route first. Task, keyword/alias, source path, symbol, and
   invariant/acceptance question → the exact module **section**. This is how you avoid reading modules
   you do not need.
4. **`KNOWLEDGE/modules/README.md`** — the module registry and ownership map. Then read **only** the
   one or two modules your task touches.

`INDEX.md` (fine router) and `modules/README.md` (coarse registry) are not interchangeable.

For a load-bearing change, also read `KNOWLEDGE/INVARIANTS.md` (is this rule an invariant or a
convention?) and the relevant rows of `KNOWLEDGE/ACCEPTANCE.md` (how do I re-check it?).

## The three things that most often go wrong here

1. **Provenance.** This fork's history contains **pre-rewrite upstream orphans**. Use
   `git rev-list --count <sha> --not --remotes=upstream` as an ancestry check only: zero means the
   commit is reachable from an upstream ref, while a nonzero count does not prove authorship or
   rule out rewritten upstream history. Compare patches, blobs and historical context. Never use
   `git describe` for provenance: it reports `v1.14.3-38-ge4b7e692f` for a commit that does not carry
   that tag. Never count upstream work by SHA — one logical upstream change appears up to **11 times**
   in the range. See `BASELINE.md` §provenance.
2. **Evidence.** A CI job declaration is not evidence that the job ran; a unit test is not interop; a
   static audit is not device behaviour; one platform's PASS is not all-platform PASS; `SKIP` is not
   `PASS`. A `VERIFIED_CI` claim requires a specific run and SHA; the Windows daemon module records
   one independently checked release run, not blanket verification of the current branch.
   And `verify-evidence/**` is stale by its own written rule: do not cite it for the baseline.
3. **Tags.** `release/DEFAULT_BUILD_TAGS` is the **Darwin** profile despite the name and cannot build
   on Windows. Use `DEFAULT_BUILD_TAGS_OTHERS` for anything general — including running the root
   package's tests, which otherwise fail on tag absence and look broken.

## Working rules

1. **Source and Git are facts.** `KNOWLEDGE/` preserves context that is expensive to reconstruct. When
   they conflict, the source wins, and the conflict is a defect to record — not to reconcile silently.
2. **Read the module's `AI cautions` before you edit.** Each one is a "reasonable but wrong" change
   that this codebase has already paid for.
3. **Read the module's `Provenance` table before you "fix" something.** Several current behaviours are
   the *second* or *fifth* attempt; the earlier ones are documented precisely so they are not repeated.
4. **Inspect `production-baseline..HEAD` before assuming a module is current:**

   ```sh
   git log --oneline v0.1.9..HEAD
   git diff --name-only v0.1.9..HEAD
   ```

   If that is empty or documentation-only, the modules still describe the tree. If not, map the changed
   paths with `modules/README.md` (or route with `INDEX.md`) and read only those modules.

   **Take the tag from `BASELINE.md`, never from this file or from memory.** A tag baked into a
   document goes stale the moment the baseline moves, and then the check silently passes while the
   modules drift — which is exactly what happened between `v0.1.6` and `v0.1.9`: this rule named
   `v0.1.6` while 181 commits and 12 production files landed, and the delta row still claimed nothing
   had changed.
5. **Do not refresh the knowledge base as part of normal development.** A refresh is an explicit
   maintenance operation: re-audit source, Git, tests and CI, update module provenance, then move the
   baseline. A development commit needs no documentation edit.
6. **Do not cite a finding from `KNOWLEDGE/` as if it were a fresh measurement.** Every claim carries
   its evidence level. If you need a higher level, run the re-check command the module gives you.
7. **Anything you cannot run is `BLOCKED_BY_ENVIRONMENT`, not `PASS`.** Say which command you could not
   run and what it would have proved.
8. **`KNOWLEDGE/REFRESH.md` is the refresh protocol, not a task mode.** Normal development does not
   update `KNOWLEDGE/` — rule 5 above. When a refresh has been explicitly requested, that document is
   the procedure: baseline and delta, then provenance, invariants, evidence levels, the cross-file
   consistency pass, and the refresh report. Its §§36-38 give the large- and small-delta workflows, and
   a small delta should not be escalated into the full multi-agent one.
