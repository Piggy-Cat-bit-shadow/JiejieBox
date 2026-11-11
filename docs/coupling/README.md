# Runtime coupling audit

Stage 2 asks one question that a unit test cannot: do the eight protocols and the
shared runtime agree about who owns a resource, who admits a creation, and when a
generation ends. This directory records that audit: the ownership map, the
call graphs behind it, the defects it found, what was fixed, and what was
deliberately left alone.

## Inputs

| input | where |
|---|---|
| stage-1 laboratory, schema, baseline, failures | [`scripts/perf/README.md`](../../scripts/perf/README.md), [`docs/perf/`](../perf/) |
| stage-1 raw runs (25) | [`docs/perf/results/`](../perf/results/) |
| this branch | `work/coupling-audit-2026-11-11`, based on the stage-1 head `53a9f5dc8e2` |
| kernel code at audit time | unchanged from `4ec0decd9f2d`; the three remote commits ahead are docs/audit only (0 `.go`) |

## Method

1. Every claim starts from a call chain read in source (`path:line`), not from an
   interface name. The matrix in [OWNERSHIP-MATRIX.md](OWNERSHIP-MATRIX.md) names
   the concrete creator, owner and closer of each resource class.
2. Admission was mapped by searching for every consumer of
   `common/resourceguard` and every TLS seam, then asking of each creation
   whether it is expensive, whether it is admitted, and whether a *reuse* is
   charged as a creation.
3. Generation handling was mapped by listing every read of the network epoch and
   every registration with `common/runtimecoord`; a protocol that never reads a
   generation is recorded as such even when its interface suggests otherwise.
4. Findings were graded by consequence, and only defects with a source-level
   mechanism and a minimal fix were changed. Everything else is recorded with the
   test or experiment that would settle it.
5. Stage-1 benchmarks were re-run only for the paths a fix touched. No number in
   this stage is a re-used stage-1 number.

## Documents

| document | contents |
|---|---|
| [OWNERSHIP-MATRIX.md](OWNERSHIP-MATRIX.md) | eight protocols × resources, admission, generation, close, idle |
| [RESOURCEGUARD.md](RESOURCEGUARD.md) | admission units, every seam, the QUIC/H3 verdict |
| [ROUTE-LIFECYCLE.md](ROUTE-LIFECYCLE.md) | route/idle/on-demand, reclaim, memory pressure |
| [RUNTIME-GENERATIONS.md](RUNTIME-GENERATIONS.md) | epochs, rebind leases, and what each protocol does on network change |
| [ISSUES.md](ISSUES.md) | every finding with severity, evidence, status |
| [DECISIONS.md](DECISIONS.md) | what was deliberately not unified, and why |
| [STAGE-2-REPORT.md](STAGE-2-REPORT.md) | the stage report in the requested order |

## Raw results

[`results/`](results/) holds the six runs taken after the fixes (XHTTP H2/H3,
VLESS Vision, AnyTLS, Mieru, TrustTunnel H2): `run.json`, rendered configs and
gzipped logs, 2 MiB. They were produced by the `sing-box-stage2` binary built
from the fixed working tree before the fix commit, so each `run.json` records the
base commit plus `dirty: true`; the report says so explicitly rather than
presenting them as a clean-tree run. Method: loopback, 3 repetitions, 5 s,
streams 1 and 4.

