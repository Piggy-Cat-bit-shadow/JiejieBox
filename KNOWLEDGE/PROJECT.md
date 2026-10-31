# Project delta

This knowledge base is a **fork-delta guide**, not a sing-box encyclopedia. Upstream's documentation
already answers "what is sing-box"; this repository answers *what this fork changed, why, which traps
are real, and how to continue without re-reading 1,834 commits*.

## What this fork is

A fork of `SagerNet/sing-box` with substantial hardening, evidence and feature work. The following
commit-subject census is historical, measured for `e4b7e692..a267e99`, and is not a current
classification of every commit's authorship or effect:

| | Count | Share |
|---|---|---|
| `fix` commits | 538 | 29.3% |
| `test` commits | 316 | 17.2% |
| `docs` commits | 282 | 15.4% |
| `ci` + `build` + `chore` + `style` + `bench` | 204 | 11.1% |
| **process/evidence subtotal** (`test` + `docs` + `ci`-group) | **802** | **43.7%** |
| `feat` commits | 76 | 4.1% |
| `perf` commits | 37 | 2.0% |
| other / untagged subjects | 381 | 20.8% |

The rows partition the 1,834 subjects; the process/evidence row is a subtotal of three rows, not a
fourth disjoint row. Four percent features against forty-four percent evidence and verification is the
shape of the project: the work went into making existing behaviour provable, bounded and reversible.

## What it adds — the load-bearing deltas

| Area | What the fork did | Module |
|---|---|---|
| **Start-time proof** | `common/physicalpath` + `common/routeflow`: report capability warnings and refuse fatal structural/promise failures without dialling, resolving, selecting or consuming rotation | `path-selection-validation.md` |
| **Runtime coordination** | `common/runtimecoord`: one monotonic network generation, identity-based rebuild leases, a coalescing window, and a foreground/automatic probe distinction | `core-lifecycle-runtime.md` |
| **Power / device axis** | `common/power`: platform **facts** in, policy out; three post-wake reuse bands over the pause/resume boundary | `power-mobile-lifecycle.md` |
| **Upload scheduler + traffic class** | `common/trafficsched`, `common/trafficclass`, `route/dial_governor.go`. `traffic_scheduler` and `traffic_class` **do not exist upstream** — a config using either is not loadable by official sing-box | `data-path-optimization.md` |
| **Direct Offload** | a provably-equivalent direct flow may leave userspace — with the honest limit that the verdict is a data-path bypass **only** in Linux `auto_redirect` | `data-path-optimization.md` |
| **MASQUE / CONNECT-IP** | substantial dataplane work, DNS assignment, bootstrap/inner resolver split and PREF64 on an upstream base; see the initial-size versus hard-ceiling distinction | `masque-connectip.md`, `mtu-and-geometry.md` |
| **Native Naive** | a NaiveProxy server inbound with UoT, padding, masquerade, pre-authentication limits and a target ACL; the tag-gated client outbound is integrated here and uses forked `cronet-go` | `naive-http-transport.md` |
| **HTTP transport** | `transport/http` is the fork's largest single addition: 97 `.go` files and 149 commits at `0067b03` (90 files / 137 commits at `a267e99`) | `naive-http-transport.md` |
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

* **No separate product-specific registry or capability allowlist** in the examined tree. Upstream
  changes still have to be incorporated, and target-specific tags determine what a binary contains.
* The old key/file census was made against historical revisions; it does not prove that every current
  upstream configuration loads or that no upstream source was deleted in later changes.
* No `quic-go/masque-go` or `quic-go/connect-ip-go` dependency in the main module at the examined
  target. `go.mod` does include Caddy's `certmagic` and `zerossl` modules, so "no Caddy dependency"
  would be incorrect.
  CONNECT-IP is self-implemented; Naive runs on `x/net/http2` + `h2c` + the fork's `quic-go`.
* **gVisor is retired as a stack, not removed from the module graph** — `go.mod` still carries it as
  `// indirect`, and the old `replace` is a comment on purpose.

## Two interface changes recorded in the historical comparison

| Interface | At `e4b7e692` | At `a267e99` | Commit | Why |
|---|---|---|---|---|
| `experimental/libbox.DeprecatedNote.Message()` / `MessageWithLink()` | returns `string` | returns `*StringBox` | `eef6bafff` | a bare `string` puts a Go pointer in gomobile's packed result frame → `fatal error: bulkBarrierPreWrite: unaligned arguments`, unrecoverable. Recorded in `docs/fork/libbox-abi-contract.tsv`. |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | the server-domain fact has to reach the adapter |

> Do not assume upstream libbox is a drop-in replacement. This historical pair is not an exhaustive
> inventory of later API changes; for example, the platform interface's `UseProcFS` method was later
> removed. Check the current interface and client binding for the target being changed.

## Historical provenance census

The old census counted 1,834 commits in `e4b7e692..a267e99`; reachability alone cannot establish
which author wrote a patch, especially after upstream rebases. Establish individual provenance using
the actual blobs and patch history, with confidence scoped to the evidence.

## Current release identity

The knowledge baseline is `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`, whose
`release/JIEJIE_VERSION` says `0.1.34` and whose annotated tag `v0.1.34` peels to it (tag object
`e2dad0bf040`; the tag was fetched from `origin` and verified for this review). The non-draft,
non-prerelease [v0.1.34 GitHub Release](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)
was published at 2026-10-29 05:34:42 UTC; v0.1.33 (`a0bbf666d`) is an ancestor. Recheck the branch,
tag and Release independently before making a current release claim; `release/JIEJIE_VERSION` alone is
not a release claim. The calibration ledger records the checks and evidence limitations.
