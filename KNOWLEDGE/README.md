# Fork knowledge base

**Knowledge Base + Git Delta = Current AI Context.** This is a fork-delta guide, not a sing-box encyclopedia.

- Knowledge generated at: 2026-10-12
- Knowledge implementation baseline: `a267e99e0bf1811dc975b013e2e8d7a15ab40d23` (`v0.1.6`)
- Release baseline: `v0.1.6` / `a267e99e0bf1811dc975b013e2e8d7a15ab40d23`
- Upstream: `https://github.com/SagerNet/sing-box.git`; confirmed baseline: `e4b7e692f4545890bfc9d43960328e1449fa803b` (`upstream/testing`)

## Reading and refresh

Read `BASELINE.md`, `PROJECT.md`, then the modules matching `git diff --name-only <baseline>..HEAD`. Do not re-audit unchanged modules. A refresh is an explicit maintenance operation: re-audit source, Git, tests and CI, update the module provenance, then move the baseline. Development commits do not require documentation edits.

Source-of-truth order: current source and tests; Git history/diffs; build and CI configuration; this knowledge base; old reports.

## Modules

- `modules/core-lifecycle.md` — lifecycle ownership, epochs, memory/reuse, validation.
- `modules/routing-protocols.md` — route/DNS/sniff, MASQUE and Naive boundaries.
- `modules/platform-release.md` — Apple, Android, Windows, build/release and acceptance.

Status labels: **VERIFIED** has direct source/history/test/CI evidence; **INFERRED** is a reasoned reading; **UNVERIFIED** lacks sufficient project evidence; **UNKNOWN** cannot be established.
