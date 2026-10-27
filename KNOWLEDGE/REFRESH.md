# sing-box Fork Knowledge Refresh Protocol

> Repository: `Piggy-Cat-bit-shadow/sing-box`
>
> Purpose: define the **long-term, repeatable procedure** for refreshing this repository's AI knowledge base without turning documentation into a second source tree or requiring continuous maintenance after every commit.

---

# 1. What this document is

This repository uses a **snapshot + Git delta** knowledge model.

The knowledge base is not expected to describe the latest `HEAD` forever.

Instead:

```text
KNOWLEDGE
= verified project understanding up to a pinned Production Baseline

Current AI Context
= KNOWLEDGE
+ production-relevant Git delta after that baseline
```

A knowledge refresh is therefore **not normal development work**.

It is an explicit maintenance operation performed periodically, for example:

- after a major release,
- after a large subsystem rewrite,
- after a long development interval,
- before a major release freeze,
- after upstream history has been rewritten or significantly re-based,
- when too much `baseline..HEAD` delta has accumulated for efficient incremental reading.

Normal feature/fix commits **do not require** updating `KNOWLEDGE/`.

---

# 2. Source-of-truth hierarchy

Always use this order:

```text
1. Current source code and executable tests
2. Git history / diffs / tags / branch topology
3. CI / build / artifact / interop / device evidence
4. KNOWLEDGE/
5. Historical reports / closure notes / old docs
```

If `KNOWLEDGE/` conflicts with current source:

> Source wins.

If an old report conflicts with current source:

> Treat the report as historical evidence, not current truth.

Do not silently rewrite history to make documents agree with code.

Instead record:

```text
old claim
current reality
superseding commit
current evidence
```

---

# 3. Core rule: combine delta review with calibration

Use the delta to prioritize deep review, but do not assume an unchanged document is correct.
Inventory every current knowledge entry, sample its source anchors and cross-module claims, and
classify it as valid, update, rewrite, obsolete, or insufficient evidence. Expand the review when
sampling finds drift. Keep historical reports explicitly scoped to their original trees.

The refresh procedure is:

```text
read current knowledge baseline
↓
compute baseline..HEAD
↓
classify the delta
↓
map changed paths/commits to subsystems
↓
deep-read only affected subsystems
↓
calibrate existing claims and check coverage gaps even when the delta is empty
↓
update relevant module knowledge
↓
re-check cross-module invariants
↓
move the Production Baseline
```

The goal is to preserve the expensive-to-reconstruct understanding while avoiding repeated full-repository archaeology.

---

# 4. First step: identify the real Production Baseline

Discover the repository root, worktree status, local and remote branches, remote heads, tags and
release records first. Identify the active refresh protocol through the repository entry point;
do not assume another checkout or a branch called `main` is authoritative. Preserve pre-existing
changes. Record the target SHA separately from released tags and prove their ancestry. If network
or release data is unavailable, label it unknown. Never infer release status from a version file.

Read:

```text
KNOWLEDGE/BASELINE.md
```

Extract the current:

```text
Production Baseline
Release Baseline
Upstream Baseline
Knowledge reviewed date
```

Do not treat the knowledge-document commit as the production baseline.

These are different concepts:

## Production Baseline

The last production-code SHA fully described by the knowledge base.

This is the reference point for:

```bash
git log  --oneline <production-baseline>..HEAD
git diff --stat    <production-baseline>..HEAD
git diff --name-only <production-baseline>..HEAD
```

## Knowledge Revision

The commit that happens to contain the current Markdown files.

A docs-only commit does **not** move the Production Baseline.

Do not chase the knowledge base's own commit SHA.

---

# 5. Before reading diffs, classify the delta

First obtain a coarse view:

```bash
git log --oneline --decorate <baseline>..HEAD
git diff --stat <baseline>..HEAD
git diff --name-only <baseline>..HEAD
```

Do not immediately read every diff.

Classify changed files and commits into these high-level groups:

```text
PRODUCTION
TEST
CI
BUILD
RELEASE
PLATFORM
DOC
REPOSITORY-HYGIENE
VENDOR/SUBMODULE
```

Then classify production changes by subsystem.

A docs-only delta usually does **not** require a full knowledge refresh.

A CI-only delta may only require updates to:

```text
ACCEPTANCE.md
modules/ci-release.md
possibly build-pruning-release.md
```

A protocol implementation delta should not automatically force rewriting unrelated platform/release knowledge.

---

# 6. Fork-delta first

This knowledge base exists to describe:

> what this fork changed relative to upstream.

It is **not** a replacement for upstream sing-box documentation.

During refresh:

```text
fork-owned changes
fork-modified source
fork-added tests
fork-specific CI/build/release behavior
```

receive priority.

Only read upstream when necessary to answer one of these questions:

```text
What did the fork change from?
What contract did it preserve?
Was this fix inherited or independently implemented?
Was an upstream fix absorbed?
Did upstream rewrite history or rename the equivalent implementation?
```

Do not summarize unrelated upstream packages.

---

# 7. Upstream history is special in this repository

`upstream/testing` may be force-rewritten.

Therefore:

> SHA ancestry alone is not always enough to determine provenance.

Never assume that every commit not currently reachable from `upstream/*` is fork-authored.

When provenance matters, use multiple checks.

Recommended:

```bash
git rev-list --count <sha> --not --remotes=upstream
git show <sha>
git patch-id --stable
git diff <candidate-a> <candidate-b>
git hash-object <file>
```

Interpret carefully.

Use provenance categories:

```text
OWN
UPSTREAM-INHERITED
UPSTREAM-ABSORBED
EXTERNAL-ADAPTED
CORE-CLIENT-COORDINATED
SUPERSEDED
ARCHIVE-ONLY
UNKNOWN
```

Definitions:

## OWN

A surviving fork-authored change.

## UPSTREAM-INHERITED

Behavior currently present because the fork simply keeps upstream behavior.

Do not list every inherited upstream commit. Only record this when the inheritance is itself important to future decisions.

## UPSTREAM-ABSORBED

An upstream fix or behavior was intentionally brought into the fork, possibly under a different SHA or rewritten implementation.

## EXTERNAL-ADAPTED

The fork was materially derived from another implementation/project and then adapted.

Use only with actual provenance evidence. Never infer this from code similarity alone.

## CORE-CLIENT-COORDINATED

A change whose correctness depends on a corresponding Apple / Android / libbox client contract.

## SUPERSEDED

A historical fork commit whose behavior is no longer current.

Keep only when it explains:

- why the final design looks unusual,
- why a tempting approach is wrong,
- why a regression is easy to reintroduce.

## ARCHIVE-ONLY

Historical evidence that is intentionally not part of current behavior.

---

# 8. Commit classification taxonomy

Every important surviving change should be classified semantically.

Use one or more of:

```text
FEATURE
FIX
HARDENING
PERF
MEMORY
ZERO-COPY
COMPAT
LIFECYCLE
ROUTING
DNS
SNIFF
CHAIN-PROXY
MASQUE
NAIVE
PLATFORM
LIBBOX
BUILD
PRUNING
TEST
INTEROP
CI
RELEASE
DOC
REPOSITORY-HYGIENE
```

Do not force one commit into a single class.

Example:

```text
Commit: abc123
Subsystems:
- MASQUE
- buffer ownership

Types:
- FIX
- HARDENING
- ZERO-COPY

Provenance:
- OWN
```

---

# 9. Do not document commits as a flat list

A commit index is navigation only.

The real knowledge must be organized by **evolution theme**.

Bad:

```text
abc123 -> FIX
def456 -> FIX
ghi789 -> TEST
```

Good:

```md
## Upload scheduler backpressure

Initial problem:
...

Evolution:
- abc123 introduced ...
- def456 corrected ...
- ghi789 added the regression guard ...

Superseded approach:
- xyz999 ...

Final behavior:
...

Why this design:
...

Evidence:
...

AI caution:
...
```

The important object is the **final design theme**, not the individual commit.

---

# 10. Subsystem map

During each refresh, do not assume the current module map is permanently correct.

Start from the existing map, but re-evaluate whether changed code belongs to an existing subsystem or requires a split/merge.

Current major knowledge domains may include:

```text
Core lifecycle / runtime coordination
Path selection / physical-path validation
Routing
DNS / consumer-driven demand
Chain proxy / detour
Sniff
MASQUE / CONNECT-IP
Naive / HTTP transport
Network epoch / reuse
Data-path optimization
Traffic scheduling / classes
Buffer ownership / zero-copy / splice
MTU / buffer geometry
Power / mobile lifecycle
libbox / platform contract
Build tags / pruning
Apple / Android build linkage
CI / release / artifact provenance
Interop / adversarial verification
```

These are **ownership domains**, not mandatory file names.

---

# 11. When to split a module

Split a module if at least two of the following are true:

```text
different invariants
different commit evolution
different acceptance gates
different source ownership
different AI cautions
different likely future tasks
```

Example:

If a future Agent fixing `sniff` must load pages of unrelated MASQUE and DNS knowledge, the module is too broad.

---

# 12. When to merge modules

Merge two modules when:

```text
they always change together
they share the same ownership
they share the same invariants
they share the same verification path
their separation causes duplicated explanations
```

Do not split simply to make the directory look organized.

---

# 13. Required structure for every important module

Each maintained subsystem file should contain only sections that add real value, but the preferred model is:

```md
# <Subsystem>

## Scope

## Current design

## Why it is designed this way

## Relationship to upstream

### Inherited unchanged
### Modified
### Extended
### Upstream fixes absorbed
### Intentionally not modified

## Provenance

### Important commits
### Superseded attempts
### External/client coordination

## Hard invariants

## Current conventions

## Important evolution

## Verification

## Known limitations

## AI cautions

## Follow-up
```

Not every module needs every heading.

Do not add empty boilerplate sections.

---

# 14. Hard Invariants must be re-audited on every refresh

A previous invariant is not automatically permanent.

For each existing invariant ask:

```text
Is this still true?
Why must it be true?
What fails if it is violated?
What evidence supports it?
Has a newer implementation made it obsolete?
Is it actually only a convention?
```

Keep something as a **Hard Invariant** only if supported by one or more of:

```text
protocol correctness
memory / ownership safety
ABI contract
platform hard constraint
verified concurrency/lifecycle bug
release artifact integrity
explicit product ownership
repeatable test/interop evidence
```

Otherwise downgrade to:

```text
Current Convention
Optimization Strategy
Design Preference
Implementation Detail
```

Do not freeze current implementation accidentally.

---

# 15. Hard Invariant vs Current Convention

Use this distinction strictly.

## Hard Invariant

A rule future code must preserve unless the architecture itself intentionally changes.

Example patterns:

```text
A buffer cannot be retained after ownership transfer.
A snapshot must not be interpreted as a transition edge.
A route validation pass must not dial a real outbound.
A released artifact must correspond to the verified binary.
```

## Current Convention

A mature current design that can be replaced by a better implementation later.

Example patterns:

```text
Current scheduler data structure
Current cache organization
Current helper naming
Current build-script layout
```

---

# 16. Evidence classification

Every important claim should use a precise evidence level.

Allowed:

```text
VERIFIED_SOURCE
VERIFIED_GIT
VERIFIED_UNIT
VERIFIED_INTEGRATION
VERIFIED_INTEROP
VERIFIED_CI
VERIFIED_ARTIFACT
VERIFIED_DEVICE
INFERRED
UNVERIFIED
UNKNOWN
BLOCKED_BY_ENVIRONMENT
```

Never promote evidence.

Forbidden examples:

```text
unit test -> interop proof
CI declaration -> CI execution proof
build success -> runtime proof
static audit -> device behavior
one platform -> all platforms
SKIP -> PASS
```

---

# 17. Verification entries should be executable

For important acceptance rules, record:

```text
Requirement
Subsystem
Why
Evidence level
Current status
Relevant source/test
How to re-check
Required environment
```

Prefer commands over prose.

Example:

```text
Requirement:
Start-time physical-path validation performs no dial side effect.

Evidence:
VERIFIED_UNIT

Re-check:
go test ./common/physicalpath/... -run <test>
```

If the command cannot run in the current environment:

```text
BLOCKED_BY_ENVIRONMENT
```

Do not mark PASS.

---

# 18. Sniff refresh rules

When sniff-related files change, check whether the delta affects:

```text
peek/read behavior
buffer ownership
allocation
partial payload handling
timeout behavior
TCP/UDP semantics
TLS detection
HTTP detection
QUIC detection
fragmentation
metadata propagation
routing contract
chain-proxy interaction
false positive / false negative behavior
```

Classify changes carefully:

```text
PERF
FIX
HARDENING
BEHAVIOR CHANGE
TEST ONLY
```

Do not describe an allocation reduction as a protocol semantic change unless behavior actually changed.

---

# 19. Chain proxy / path-selection refresh rules

When route / selector / detour / chain logic changes, explicitly evaluate:

```text
ControlPath
PhysicalPath
group selection
detour
failover
cycle detection
start-time validation
runtime selection
DNS lookup side effects
dial side effects
state mutation
```

The most important question:

> Does validation merely inspect topology, or does it accidentally execute selection/dial/resolution?

If behavior changes, update:

```text
path-selection module
routing module
DNS module if relevant
ACCEPTANCE
INVARIANTS
COMMIT-INDEX
```

---

# 20. DNS / routing refresh rules

Check whether changes affect:

```text
consumer demand
DNS transport activation
cache behavior
FakeIP
rule-set demand
route lookup
resolver detour
network epoch
stale state
network switch
leak prevention
```

Do not document DNS and routing separately if the new behavior is explicitly cross-cutting.

But do not collapse them into one module merely because one commit touched both.

---

# 21. MASQUE refresh rules

MASQUE is a high-value fork domain.

Any MASQUE-related refresh should inspect:

```text
CONNECT-IP
CONNECT-UDP
HTTP/2
HTTP/3
DATAGRAM
capsules
fallback
session lifetime
stream lifetime
buffer ownership
batching
PREF64
PTB
MTU
DNS role
error handling
cleanup
interop
```

Always distinguish:

```text
upstream inherited base
fork-added feature
fork fix
fork hardening
interop evidence
```

If a change only modifies one transport path, do not claim all MASQUE paths changed.

---

# 22. Naive refresh rules

When Naive / HTTP transport changes, inspect:

```text
client
server
CONNECT behavior
auth
headers
TLS
transport reuse
shutdown
error mapping
dependency compatibility
interop
```

Do not infer "Native Naive parity" from dependency changes alone.

---

# 23. Buffer ownership / zero-copy refresh rules

This area requires especially careful documentation because future "optimizations" can silently break correctness.

For changed hot paths answer:

```text
Who owns the buffer?
When is ownership transferred?
When can it be reused?
When must it be copied?
Can the callee retain it?
Can it return to a pool?
Can a later async path still reference it?
```

Record:

```text
ownership invariant
copy boundary
pool/release rule
relevant tests
known unsafe tempting optimization
```

Do not write only:

```text
"zero-copy optimized"
```

---

# 24. Network epoch / reuse refresh rules

If network change, DNS invalidation, route invalidation, connection reuse, interface switching or resource retirement changes, determine:

```text
who produces the epoch
who consumes it
whether the epoch is global or partial
what becomes stale
when stale resources are retired
whether rebuild is eager or lazy
what happens to live connections
```

Never generalize a subsystem-local generation counter into a fork-wide invariant without proof.

---

# 25. Mobile lifecycle / platform contract refresh rules

When core/client lifecycle APIs change, inspect the contract with:

```text
sing-box-for-apple
satelite-one
```

only as needed.

Distinguish:

```text
raw platform fact
level
edge
snapshot
event
core policy
client policy
```

Preferred ownership model should be verified, not assumed:

```text
client reports platform facts
core interprets policy
```

If an API changes, update:

```text
libbox-platform-contract
power/mobile lifecycle
relevant acceptance rows
client coordination provenance
```

Do not copy the clients' own full knowledge bases into the core repository.

---

# 26. Build / pruning refresh rules

When build tags, protocol registration, generators or release profiles change, determine:

```text
which binaries/platforms are affected
which protocols are included/excluded
whether config validation changes
whether dependency closure changes
whether binary size or memory is the goal
whether a client/server build profile differs
```

Never convert a temporary release profile into a permanent invariant without evidence.

---

# 27. Apple / Android build-linkage refresh rules

If either client pin or libbox build contract changes, verify:

```text
core SHA
client SHA
ABI/API compatibility
build scripts
provenance checks
artifact content
```

A metadata value alone is not binary provenance.

Prefer:

```text
binary inspection
artifact hash
embedded build info
ABI gate
```

where the repository already provides them.

---

# 28. CI / release refresh rules

When workflow or release code changes, separately classify:

```text
workflow declared
workflow actually ran
artifact produced
artifact verified
artifact released
```

Do not conflate them.

If a workflow trigger changes, record:

```text
branches
tags
paths filter
manual dispatch
automatic gate scope
```

If docs-only pushes trigger expensive verification, that is a workflow property, not a production-code fact.

---

# 29. Historical documents

Historical reports are useful for:

```text
why a fix exists
failed approaches
device evidence
interop evidence
branch consolidation
release incidents
```

But they are dangerous as current truth.

On every refresh, classify significant documents:

```text
KEEP
ABSORB
ARCHIVE
DELETE
```

## KEEP

Still operationally useful and current.

## ABSORB

Contains lasting knowledge that belongs in `KNOWLEDGE/`.

## ARCHIVE

Useful historical evidence but not current truth.

## DELETE

Duplicated, stale, or fully superseded with no remaining evidentiary value.

When in doubt:

```text
ARCHIVE
```

---

# 30. AI cautions

Each subsystem should preserve only high-value cautions.

A useful AI caution describes a change that:

```text
looks reasonable locally
but violates a non-local design or proven constraint
```

Examples:

```text
Do not preview a route by actually selecting/dialing it.
Do not merge snapshot and event semantics.
Do not retain a transferred pooled buffer.
Do not assume a metadata pin proves binary provenance.
Do not rebuild all stale resources merely because an epoch changed.
Do not duplicate policy interpretation in mobile clients.
```

Avoid generic warnings such as:

```text
"be careful"
"run tests"
```

---

# 31. Cross-file consistency pass

Before moving the baseline, compare:

```text
AGENTS.md
KNOWLEDGE/README.md
KNOWLEDGE/BASELINE.md
KNOWLEDGE/PROJECT.md
KNOWLEDGE/ARCHITECTURE.md
KNOWLEDGE/INVARIANTS.md
KNOWLEDGE/ACCEPTANCE.md
KNOWLEDGE/COMMIT-INDEX.md
KNOWLEDGE/modules/*
README.md
important historical reports
```

Look for:

```text
same SHA described differently
same subsystem with conflicting ownership
same claim with different evidence status
fixed issue still listed as active
superseded commit described as current
upstream inherited behavior mislabeled OWN
fork feature mislabeled upstream
release baseline confused with knowledge revision
```

Resolve before finishing.

Check local Markdown links and source paths/symbols, using symbol searches rather than assuming
historical line numbers still apply. Review TUN/network changes, routing/DNS/fast-path authority,
dual-stack dial and fallback, protocol/TLS/QUIC lifecycle, platform callbacks and fork dependency
pins together. Inventory real registered protocols, platform build gates and tests rather than
assuming the list in this protocol is exhaustive. For each key conclusion record its scope,
source anchor and evidence level; distinguish source, tests actually run, CI runs, interoperability,
devices and measurements. Do not claim a quantitative performance result without a matching benchmark.

If interrupted, preserve a concise ledger of reviewed entries, target SHA, unresolved contradictions
and checks remaining. Resume from that ledger after verifying HEAD and worktree; never advance the
baseline past unresolved load-bearing gaps. Do not modify runtime code, dependencies, build or
release configuration as part of a knowledge refresh. Do not recursively refresh this protocol.

---

# 32. Updating COMMIT-INDEX.md

`COMMIT-INDEX.md` is a navigation layer.

Add only commits that help explain the current product.

A commit belongs there if it is:

```text
the origin of a surviving feature
a key bug fix
a hardening step
the final replacement of a superseded approach
the test/interop gate that protects a major invariant
a release/build change with lasting consequence
```

Do not index:

```text
every docs commit
every typo
every intermediate retry
every merge with no unique semantic effect
```

---

# 33. Updating BASELINE.md

After all changed subsystems are reconciled, update:

```text
Production Baseline
Release Baseline if relevant
Upstream Baseline if relevant
Knowledge reviewed date
Post-baseline production delta
```

Do not set Production Baseline to a docs-only commit.

The new Production Baseline should be:

> the newest production implementation SHA that the refreshed knowledge fully describes.

---

# 34. Updating README.md / AGENTS.md

Only change these if the refresh procedure or module map changed.

Do not update them just because baseline SHA changed unless they duplicate that SHA.

Prefer dynamic references:

```text
Read KNOWLEDGE/BASELINE.md
```

over hardcoded baseline values in multiple files.

The fewer duplicated SHAs, the less future staleness.

---

# 35. Knowledge refresh report

Each explicit refresh should produce one concise report:

```text
KNOWLEDGE_REFRESH_<DATE_OR_RELEASE>.md
```

or update the repository's chosen refresh-report convention.

It should contain:

```text
Old Production Baseline
New Production Baseline
Release/tag context
Upstream baseline change
Commits coarse-scanned
Commits deep-read
Subsystems changed
Modules updated
Invariants added/removed/downgraded
Acceptance rows changed
Important provenance changes
Historical docs disposition
Unverified/blocked findings
Production code changed by refresh? NO
Git history rewritten? NO
```

Do not create one report per subsystem.

---

# 36. Recommended refresh workflow with Harness / multi-Agent systems

For a large delta, parallel agents may be used when the active execution instructions allow them;
otherwise perform the same independent reviews sequentially.

## Phase 1 — coarse map

Master Agent:

```text
read baseline
compute delta
classify paths
cluster commits
identify affected subsystems
```

## Phase 2 — subsystem agents

One agent per affected subsystem.

Each gets:

```text
old module knowledge
changed commits
changed source
relevant tests
necessary upstream context
```

Each returns:

```text
CONFIRMED
CHANGED
REMOVED
NEW
SUPERSEDED
UNVERIFIED
```

## Phase 3 — provenance agent

Independently audits:

```text
OWN
UPSTREAM-INHERITED
UPSTREAM-ABSORBED
EXTERNAL-ADAPTED
```

especially when upstream history was rewritten.

## Phase 4 — verification agent

Independently checks:

```text
tests
CI
interop
artifact
device evidence
```

## Phase 5 — master convergence

The master:

```text
deduplicates
resolves contradictions
re-reads disputed source
updates knowledge
runs cross-file consistency
moves baseline
```

Do not concatenate sub-agent reports directly.

---

# 37. Small-delta refresh workflow

If only a few commits changed one subsystem:

```text
1. Read BASELINE
2. Inspect baseline..HEAD
3. Read the affected module
4. Read changed source + relevant diffs
5. Run/review relevant tests
6. Update that module
7. Update ACCEPTANCE / COMMIT-INDEX only if needed
8. Check INVARIANTS
9. Move Production Baseline
10. Update reviewed date
```

Do not invoke a full multi-agent audit for a trivial change.

---

# 38. Large-delta refresh workflow

Use a full, independently checked refresh when:

```text
> several hundred relevant commits
multiple subsystem rewrites
release architecture changed
upstream history rewritten
platform ABI changed
major protocol feature added
knowledge baseline is months old
```

In a large refresh:

```text
coarse scan all relevant commits
deep-read only semantic clusters
do not deep-read every commit
```

---

# 39. When NOT to move the baseline

Do not move Production Baseline if:

```text
important changed subsystem remains UNVERIFIED
knowledge still contradicts current source
commit provenance remains unresolved for a load-bearing change
a major module is knowingly missing
cross-file consistency fails
```

In that case:

```text
REFRESH = INCOMPLETE
```

Keep the old baseline.

---

# 40. Final refresh acceptance checklist

A refresh is complete only if a new AI can:

```text
1. Read AGENTS.md
2. Read KNOWLEDGE/README.md
3. Identify the Production Baseline
4. Inspect baseline..HEAD
5. Map changed paths to modules
6. Avoid reading unrelated modules
7. Distinguish fork behavior from upstream
8. Trace important behavior to commits
9. See why the design exists
10. Re-run the relevant acceptance checks
11. Recognize superseded approaches
12. Avoid known reasonable-but-wrong changes
```

If any important subsystem still requires full repository archaeology:

> the refresh is not finished.

---

# 41. Final status block

Every refresh report should end with:

```text
KNOWLEDGE_REFRESH = COMPLETE / INCOMPLETE

OLD_PRODUCTION_BASELINE = <sha>
NEW_PRODUCTION_BASELINE = <sha>

PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO

DELTA_CLASSIFIED = YES / NO
AFFECTED_SUBSYSTEMS_REAUDITED = YES / NO
COMMIT_PROVENANCE_UPDATED = YES / NO
INVARIANTS_REAUDITED = YES / NO
ACCEPTANCE_UPDATED = YES / NO
HISTORICAL_DOCS_RECONCILED = YES / NO
CROSS_FILE_CONSISTENCY = PASS / FAIL
FUTURE_AI_INCREMENTAL_READ = PASS / FAIL
```

---

# 42. The principle to preserve

The knowledge system succeeds when:

```text
source stays authoritative
Git stays authoritative for change history
knowledge stays compact
knowledge is allowed to become stale
staleness is explicit
refreshes are periodic
future AI reads only the necessary delta
```

Do not turn `KNOWLEDGE/` into a continuously synchronized shadow copy of the codebase.

The purpose of this system is:

> **to preserve the context that is expensive to reconstruct, while leaving implementation facts in the source code and change facts in Git.**
