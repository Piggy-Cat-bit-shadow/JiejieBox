# Fork knowledge base

**Knowledge Base + Git Delta = Current AI Context.** This is a fork-delta guide, not a sing-box
encyclopedia. Upstream's own documentation answers "what is sing-box"; this answers *what this fork
changed, why, and how to continue without re-reading 1,834 commits*.

## Baseline — read this before using anything else

| | |
|---|---|
| Repository | `Piggy-Cat-bit-shadow/JiejieBox` |
| Product branch | `testing` |
| **Production baseline** | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` — the source/CI revision calibrated on 2026-10-31 |
| Release baseline | **`v0.1.34`**, tag peels to the production baseline |
| **Historical upstream anchor** | `e4b7e692f4545890bfc9d43960328e1449fa803b` — used by the v0.1.9-era comparisons, not the current upstream merge-base |
| Post-baseline production delta | compute `git diff --name-only 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28..HEAD`; see `BASELINE.md` |
| Knowledge reviewed at | 2026-10-31 |
| Knowledge revision | resolve dynamically: `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` |

The knowledge describes the source baseline, not automatically the current `HEAD`. Full measurements,
the release lineage, the provenance test, and the tag traps are in `BASELINE.md`.

**Historical 2026-10-28 calibration:** the first target was `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`,
791 commits beyond v0.1.9; the final production target is four commits later. `v0.1.30` is a published release and an ancestor of the first target; subsequent
fixes are not in v0.1.30. A later [v0.1.31 Release](https://github.com/Piggy-Cat-bit-shadow/sing-box/releases/tag/v0.1.31)
was published at the later `cb71cf80d72` target. See `BASELINE.md`, [the calibration ledger](CALIBRATION-2026-10-28.md)
and the current source.

At the historical v0.1.31 target, the merge-base with the checked remote `upstream/testing` tip
`e5d91fa79f6319d5de940d6c81d81f0fe53b9476` is `a6952ccf6907c18e18917fcb763a433e8e36937b`.
This is a point-in-time Git ancestry observation, not proof of authorship; remeasure when the refs move.

**The previous baseline (`a267e99e0`, `v0.1.6`) went stale invisibly for two releases** — its delta row
claimed "no `.go` source changed" while 181 commits and 12 production files had landed. That failure is
recorded in `BASELINE.md` rather than quietly corrected, because the lesson is about the model, not
about one row.

Child repositories and their independent branch tips are part of this knowledge scope; see
`modules/child-repositories.md` and `REFRESH.md` §25A.

The current delta and limitations are recorded in [the 2026-10-31 calibration](CALIBRATION-2026-10-31.md).

## Reading order

```text
1. BASELINE.md            what SHA this describes, and how to compute the delta since
2. INDEX.md               route first: task / keyword / source path -> exact module section
3. PROJECT.md             what the fork is and what it deliberately did not change
4. ARCHITECTURE.md        the eleven boundaries worth preserving
5. modules/README.md      the module registry and ownership map
6. the 1-2 modules your task touches
7. INVARIANTS.md          when you are about to change something that looks load-bearing
8. ACCEPTANCE.md          the requirement -> evidence -> re-check index
9. COMMIT-INDEX.md        when you need to find the SHAs behind a behaviour
10. reports/README.md      what was audited, when, and against which tree — records, NOT current claims
```

> `reports/` is an archive of point-in-time audit and close-out reports. Read its `README.md` first: a
> report describes the tree at its own date, `BASELINE.md`/`INDEX.md`/`modules/` own the current claims, and
> where a report and the source disagree the source wins. Adding a report is not a refresh and never moves
> the baseline.

**`INDEX.md` and `modules/README.md` are not interchangeable.** `modules/README.md` is the coarse
registry (which module owns what). `INDEX.md` is the fine router: exact section anchors, aliases, and
a reverse index by source path and symbol.

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

* **A CI job declaration is not evidence that the job ran.** A `VERIFIED_CI` claim needs a specific
  independently inspectable run and SHA; the Windows daemon module records one such release run.
* **`SKIP` is not `PASS`.** The fork's own skips say so explicitly ("NOT a pass"), and one gate prints
  `PARTIAL` with exit 0 by design.

## Refresh policy

A refresh is an explicit maintenance operation: re-audit source, Git, tests and CI; update module
provenance; then move the baseline. **Development commits do not require documentation edits.** Nothing
in this directory needs to change when product code moves — only `BASELINE.md`'s "post-baseline
delta" row tells a reader what to look at.

The procedure to follow when a refresh *has* been explicitly requested is `REFRESH.md`. That file is
the protocol; this section is only the policy, and normal development does not update `KNOWLEDGE/` at
all.

## Modules

Route with `INDEX.md` first — it carries the exact section anchors and a reverse index by source path.
This table is the coarse ownership map.

| Module | Covers |
|---|---|
| `modules/windows-daemon-security.md` | `experimental/boxdd/**`: the daemon/service/worker roles, the named-pipe peer handshake, executable and signer identity, Authenticode semantics, the signer pin, service installation, the update path |
| `modules/core-lifecycle-runtime.md` | `adapter.Scope`, `Box` start/close order, `common/runtimecoord`, `route.ReferenceManager` |
| `modules/path-selection-validation.md` | `common/physicalpath`, `common/routeflow`, start-time validation, `detour`, groups, cycles, failover |
| `modules/dns-demand.md` | DNS transports, generations, cache commit, FakeIP, consumer demand |
| `modules/dns-adblock-action.md` | the DNS-only `adblock` rule action: local NXDOMAIN + SOA, zero upstream, and why it is not `reject` or a negative-cache entry |
| `modules/sniff.md` | sniffers, peek/read ownership, fragmented QUIC, the sniff→routing contract |
| `modules/masque-connectip.md` | MASQUE endpoint and dataplane, capsules, sessions, DNS roles, MTU/PTB |
| `modules/naive-http-transport.md` | Native Naive, `transport/http`, `http_clients`, the UoT target ACL |
| `modules/xhttp-client-transport.md` | XHTTP / SplitHTTP client contract: `transport/v2rayxhttp`, modes, XMUX reuse, X-Padding |
| `modules/fast-path-group-policy.md` | `fast_path`: the group-level policy field, its three states, and what it does not bypass |
| `modules/data-path-optimization.md` | Direct Offload, traffic class, the upload scheduler, the dial governor |
| `modules/buffer-ownership.md` | the buffer/zero-copy/splice rules that must not be optimised away |
| `modules/power-mobile-lifecycle.md` | device axis, sleeps, screen/lock, trims, reuse bands, the timer census |
| `modules/mtu-and-geometry.md` | the three MTU quantities, packet-size bounds, `common/bufgeom`, the low-memory profile |
| `modules/libbox-platform-contract.md` | the client-facing ABI surface and the platform boundary |
| `modules/build-pruning-release.md` | build tags, pruning, release profiles, generators, pins |
| `modules/ci-release.md` | workflows, gates, evidence, and what may not be claimed |

Fork-wide: `BASELINE.md`, `INDEX.md`, `PROJECT.md`, `ARCHITECTURE.md`, `INVARIANTS.md`,
`ACCEPTANCE.md`, `COMMIT-INDEX.md`, `REFRESH.md`.

## What this knowledge base does not cover

individual upstream protocol wire formats, the Clash/V2Ray API internals, and the Apple / Android /
desktop **client** source — those are separate repositories, present as empty submodules in a clean
worktree. `modules/README.md` says so explicitly rather than leaving silence to be mistaken for
coverage.

**The Windows daemon (`experimental/boxdd/**`) is no longer in this list.** It was the last unowned
changed subsystem and is now owned by `modules/windows-daemon-security.md` — with one honest limit that
belongs here too: **its trust boundary has no documented end-to-end test.** A Windows test workflow
is declared in `.github/workflows/windows-boxdd-tests.yml`; declaration alone does not prove a run. Owning
the knowledge is not the same as proving the behaviour, and the module's §15 says which is which.
