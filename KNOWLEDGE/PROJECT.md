# Project delta

This knowledge base is a **fork-delta guide**, not a sing-box encyclopedia. Upstream's documentation
already answers "what is sing-box"; this repository answers *what this fork changed, why, which traps
are real, and how to continue without re-reading 1,834 commits*.

## What this fork is

A **hardening-and-evidence fork** of `SagerNet/sing-box`, not a feature fork. Measured over the
fork's own history:

| | Count | Share |
|---|---|---|
| `fix` commits | 433 | 24% |
| `test` commits | 316 | 17% |
| `docs` commits | 282 | 16% |
| `ci` + `build` + `chore` + `style` + `bench` | 209 | 12% |
| **process/evidence total** | **807** | **44.7%** |
| `feat` commits | 76 | **4.2%** |
| `perf` commits | 37 | 2% |
| untagged subjects | 365 | 20% |

Four and a half percent features against forty-five percent evidence is the shape of the project: the
work went into making existing behaviour provable, bounded and reversible.

## What it adds — the load-bearing deltas

| Area | What the fork did | Module |
|---|---|---|
| **Start-time proof** | `common/physicalpath` + `common/routeflow`: report capability warnings and refuse fatal structural/promise failures without dialling, resolving, selecting or consuming rotation | `path-selection-validation.md` |
| **Runtime coordination** | `common/runtimecoord`: one monotonic network generation, identity-based rebuild leases, a coalescing window, and a foreground/automatic probe distinction | `core-lifecycle-runtime.md` |
| **Power / device axis** | `common/power`: platform **facts** in, policy out; three post-wake reuse bands over the pause/resume boundary | `power-mobile-lifecycle.md` |
| **Upload scheduler + traffic class** | `common/trafficsched`, `common/trafficclass`, `route/dial_governor.go`. `traffic_scheduler` and `traffic_class` **do not exist upstream** — a config using either is not loadable by official sing-box | `data-path-optimization.md` |
| **Direct Offload** | a provably-equivalent direct flow may leave userspace — with the honest limit that the verdict is a data-path bypass **only** in Linux `auto_redirect` | `data-path-optimization.md` |
| **MASQUE / CONNECT-IP** | the whole `transport/masque` dataplane, DNS assignment, the bootstrap/inner resolver split, PREF64, a proven MTU ceiling. The **endpoint skeleton** is upstream's | `masque-connectip.md` |
| **Native Naive** | a NaiveProxy server inbound with UoT, padding, masquerade, pre-authentication limits and a target ACL; the tag-gated client outbound is integrated here and uses forked `cronet-go` | `naive-http-transport.md` |
| **HTTP transport** | `transport/http` is the fork's largest single addition: 90 files, 137 commits | `naive-http-transport.md` |
| **Load-balance group + bounded failover** | fork-only `type: loadbalance`; failover is opt-in, with **one retry budget for the whole flow across nesting** | `data-path-optimization.md`, `path-selection-validation.md` |
| **Buffer geometry** | `common/bufgeom`: poolability and allocation-ceiling rules with a `//go:build race` split | `mtu-and-geometry.md`, `buffer-ownership.md` |
| **Product plumbing** | Apple single-source pin + brand-vs-identity split; Android AAR/ABI; Windows core artifact; a fail-closed release-acceptance gate that re-verifies bytes rather than rebuilding | `libbox-platform-contract.md`, `build-pruning-release.md`, `ci-release.md` |

Fork-created packages, verified absent at the upstream ancestor `e4b7e692`: `common/physicalpath`,
`common/routeflow`, `common/runtimecoord`, `common/power`, `common/trafficsched`, `common/trafficclass`,
`common/bufgeom`, `common/badh2`, `dns/transport/group`, `internal/memmetrics`, `transport/v2rayxhttp`,
`protocol/vless/encryption`, plus `protocol/group/loadbalance*.go`, `route/failover.go`,
`route/bypass_verdict.go`, `common/dialer/profile.go`, `adapter/outbound/cross_kind_cycle.go`,
`adapter/fakeip_issuance.go`, `option/traffic_scheduler.go`, `option/v2ray_xhttp.go`, `scripts/ci/`,
`docs/fork/` and `test/jiejie/`.

## What it deliberately does NOT change

This is as important as the delta, because it constrains what a future AI may assume:

* **No fork-specific registry and no capability allowlist.** Both products build from **upstream's own
  tag files** and registries, so a capability upstream adds is inherited automatically.
* **Zero upstream JSON config keys removed** — all 650 upstream keys are present; the fork adds 57.
  Every upstream configuration still loads here.
* **Zero upstream Go source files deleted.** The only upstream files absent at `a267e99` are six
  `.github/workflows/*` files.
* **No Caddy dependency**, no `quic-go/masque-go`, no `quic-go/connect-ip-go` in the main module.
  CONNECT-IP is self-implemented; Naive runs on `x/net/http2` + `h2c` + the fork's `quic-go`.
* **gVisor is retired as a stack, not removed from the module graph** — `go.mod` still carries it as
  `// indirect`, and the old `replace` is a comment on purpose.

## Two upstream interfaces WERE broken, deliberately

| Interface | At `e4b7e692` | At `a267e99` | Commit | Why |
|---|---|---|---|---|
| `experimental/libbox.DeprecatedNote.Message()` / `MessageWithLink()` | returns `string` | returns `*StringBox` | `eef6bafff` | a bare `string` puts a Go pointer in gomobile's packed result frame → `fatal error: bulkBarrierPreWrite: unaligned arguments`, unrecoverable. Recorded in `docs/fork/libbox-abi-contract.tsv`. |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | the server-domain fact has to reach the adapter |

> **Upstream's libbox is not drop-in replaceable in this fork, and any tooling that assumes it is will
> misbehave.** Everything else is additive: new capabilities are **optional interfaces embedding
> `Outbound`**, so upstream implementations still satisfy the originals.

## Provenance in one line

Of the 1,834 commits in `e4b7e692..a267e99`, **1,505 are the fork's own** and **329 are pre-rewrite
upstream orphans** (upstream rebases `testing`; the fork kept the old SHAs). See `BASELINE.md`
§provenance for the test and the traps.

## Current release identity

Tag **`v0.1.6`** = `a267e99e0bf1811dc975b013e2e8d7a15ab40d23` (2026-10-12), and `release/JIEJIE_VERSION`
is `0.1.6`. `v0.1.6..testing` contains exactly one commit — the Stage-1 documentation commit — so the
**production baseline equals the release baseline**. `v1.x` tags are **upstream's** and live in the same
namespace; `v0.1.0` is upstream's too. See `BASELINE.md` §release-identity.
