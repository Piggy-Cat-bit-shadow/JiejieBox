# Fork knowledge base

**Knowledge Base + Git Delta = Current AI Context.** This is a fork-delta guide, not a sing-box
encyclopedia. Upstream's own documentation answers "what is sing-box"; this answers *what this fork
changed, why, and how to continue without re-reading 1,834 commits*.

## Baseline — read this before using anything else

| | |
|---|---|
| Repository | `Piggy-Cat-bit-shadow/sing-box` |
| Product branch | `testing` |
| **Production baseline** | `a267e99e0bf1811dc975b013e2e8d7a15ab40d23` — the implementation every module describes |
| Release baseline | **`v0.1.6`**, the same SHA |
| **Upstream baseline** | `e4b7e692f4545890bfc9d43960328e1449fa803b` = `refs/remotes/upstream/testing` |
| Post-baseline production delta | **none** — `v0.1.6..testing` is one documentation commit |
| Knowledge reviewed at | 2026-10-12 |
| Knowledge revision | resolve dynamically: `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` |

**The knowledge base does not describe `HEAD`.** It describes `a267e99e`, and it says so. Full
measurements, the provenance test, and the two tag-scheme traps are in `BASELINE.md`.

## Reading order

```text
1. BASELINE.md            what SHA this describes, and how to compute the delta since
2. PROJECT.md             what the fork is and what it deliberately did not change
3. ARCHITECTURE.md        the eleven boundaries worth preserving
4. modules/README.md      task -> module map
5. the 1-2 modules your task touches
6. INVARIANTS.md          when you are about to change something that looks load-bearing
7. ACCEPTANCE.md          the requirement -> evidence -> re-check index
8. COMMIT-INDEX.md        when you need to find the SHAs behind a behaviour
```

**Do not read the modules you do not need.** Each is self-contained: it carries its own source
anchors, provenance and evidence level.

## Source-of-truth order

```text
current source and tests
    > git history and diffs
        > build and CI configuration
            > this knowledge base
                > any other document in the repository
```

When this knowledge base conflicts with the source, **the source wins** — and the conflict is a defect
to record, not to reconcile silently.

## The four status labels

| Label | Means |
|---|---|
| **VERIFIED** | direct source, history, test, CI-declaration or artifact evidence — and the specific level is always named |
| **INFERRED** | a reasoned reading with no direct anchor |
| **UNVERIFIED** | the project has no sufficient evidence |
| **UNKNOWN** | cannot be established here at all |

Evidence levels are always qualified: `VERIFIED_SOURCE`, `VERIFIED_GIT`, `VERIFIED_UNIT`,
`VERIFIED_INTEGRATION`, `VERIFIED_INTEROP`, `VERIFIED_CI`, `VERIFIED_ARTIFACT`, `VERIFIED_DEVICE`,
`INFERRED`, `UNVERIFIED`, `UNKNOWN`, `BLOCKED_BY_ENVIRONMENT`.

Two rules that this repository punishes if ignored:

* **A CI job declaration is not evidence that the job ran.** No workflow-run record exists in the tree,
  so **no claim here is `VERIFIED_CI`**.
* **`SKIP` is not `PASS`.** The fork's own skips say so explicitly ("NOT a pass"), and one gate prints
  `PARTIAL` with exit 0 by design.

## Refresh policy

A refresh is an explicit maintenance operation: re-audit source, Git, tests and CI; update module
provenance; then move the baseline. **Development commits do not require documentation edits.** Nothing
in this directory needs to change when product code moves — only `BASELINE.md`'s "post-baseline
delta" row tells a reader what to look at.

## Modules

| Module | Covers |
|---|---|
| `modules/core-lifecycle-runtime.md` | `adapter.Scope`, `Box` start/close order, `common/runtimecoord`, `route.ReferenceManager` |
| `modules/path-selection-validation.md` | `common/physicalpath`, `common/routeflow`, start-time validation, `detour`, groups, cycles, failover |
| `modules/dns-demand.md` | DNS transports, generations, cache commit, FakeIP, consumer demand |
| `modules/sniff.md` | sniffers, peek/read ownership, fragmented QUIC, the sniff→routing contract |
| `modules/masque-connectip.md` | MASQUE endpoint and dataplane, capsules, sessions, DNS roles, MTU/PTB |
| `modules/naive-http-transport.md` | Native Naive, `transport/http`, `http_clients`, the UoT target ACL |
| `modules/data-path-optimization.md` | Direct Offload, traffic class, the upload scheduler, the dial governor |
| `modules/buffer-ownership.md` | the buffer/zero-copy/splice rules that must not be optimised away |
| `modules/power-mobile-lifecycle.md` | device axis, sleeps, screen/lock, trims, reuse bands, the timer census |
| `modules/mtu-and-geometry.md` | the three MTU quantities, packet-size bounds, `common/bufgeom`, the low-memory profile |
| `modules/libbox-platform-contract.md` | the client-facing ABI surface and the platform boundary |
| `modules/build-pruning-release.md` | build tags, pruning, release profiles, generators, pins |
| `modules/ci-release.md` | workflows, gates, evidence, and what may not be claimed |

Fork-wide: `BASELINE.md`, `PROJECT.md`, `ARCHITECTURE.md`, `INVARIANTS.md`, `ACCEPTANCE.md`,
`COMMIT-INDEX.md`.

## What this knowledge base does not cover

`transport/v2rayxhttp` and `protocol/vless/encryption` (new fork packages with no recorded incident or
acceptance rule), individual upstream protocol wire formats, the Clash/V2Ray API internals, and the
Apple / Android / desktop **client** source — those are separate repositories, present as empty
submodules in a clean worktree. `modules/README.md` says so explicitly rather than leaving silence to
be mistaken for coverage.
