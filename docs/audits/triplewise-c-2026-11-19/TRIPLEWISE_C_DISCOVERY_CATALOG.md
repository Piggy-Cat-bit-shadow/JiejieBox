# TRIPLEWISE C — Discovery catalog

The brief's own §3.2 lists ten "key shapes" — search patterns rather than module names. This catalog
takes each shape, finds what it actually is in this tree, and records whether it has been attacked, is
covered by an executed test, or remains open. Sources: the current source at `7346eacb0`, the tests that
exist for each shape, and the A/B/triple-A rounds of this effort.

**The Issue/PR circle is BLOCKED and no claim is made about it.** One web search for upstream
sing-box issues returned documentation pages only
([migration.zh.md](https://github.com/sagernet/sing-box/blob/1ac1a339cb1223e9c70eae14c44411c75033c02d/docs/migration.zh.md),
[rule_action.zh.md](https://github.com/sagernet/sing-box/blob/0b8995879f29a9b98ee027bc17b75e101445b238/docs/configuration/dns/rule_action.zh.md),
[changelog.md](https://github.com/sagernet/sing-box/blob/253b41936ecd6ae17948d49d9c510d7100830927/docs/changelog.md)),
and `git fetch`/`push` to the origin repeatedly failed with connection resets. No issue, PR or fork is
cited anywhere in this round.

| # | Shape (the brief's words) | Where it is in this tree | Status |
| --- | --- | --- | --- |
| 1 | `lock → callback → another manager → lock` | the transition's `transitionAccess` vs `Coordinator.Advance`'s fan-out; the ABBA reasoning is written out at `route/network.go:1405-1420` and was a real deadlock before it was fixed | **already hardened**, with the measurement in the comment; `go test ./route` and `-race ./route` (462 s) green this effort |
| 2 | `observe state → schedule async work → apply state` | `GenerationGate.applyTransition` (once per generation), the pool publish decision (now atomic, window B's R-B2 fix), `observe→apply` versioning in `Guard` | **fixed** where it was broken (atomic publish), **covered** by the gate's own suite |
| 3 | `pool lookup → gate → physical dial → retry` | `common/dialer` dual-stack + `resourceguard` admission; the naive native seam charges one permit per physical socket | **covered** by ~45 dual-stack test files + window A's A03 read; the one gap is the TUN UDP path (TB-5, cross-repo) |
| 4 | `capture packet → fake-IP lookup → choose outbound → resolve` | `protocol/tun` `JudgeFlow` → `route.prepareMatchMetadata` → rule walk | **three defects found and fixed** in this effort (B-3, B-4, B-5), each with a mutation control |
| 5 | `config reload → close old outbound → late completion → publish` | `v2rayhttp`/`v2raygrpclite` pools + the daemon's close-then-build reload | **fixed** in triple A: a generation offered to a closed pool is released, not adopted (C-T2 below) |
| 6 | `network change → DNS cache invalidation → retry transport` | `dns.Router.ResetNetwork`: the epoch advance and the cache purge are one step under `dnsEnvironmentAccess`, with the interleaving it prevents written out at `dns/router.go:2597+` | **already hardened**; 12 named tests cover it (`cache_generation_guard_test.go`, `cache_remote_environment_test.go`, `async_generation_isolation_test.go`), executed green in §4 of the evidence page — including `TestRemoteTransportNegativeVerdictDoesNotCrossNetworks` and `TestRemoteTransportInFlightAnswerIsNotStoredOnTheNewNetwork`, which are exactly this shape |
| 7 | `native callback → Go owner released → pressure reclaim` | `protocol/naive/native_creation.go` (the Cronet dialer callback) + `resourceguard` + `service/oomkiller` | **partially**: window A fixed the one Go-side violation (a shed hook that terminated live tunnels); the native side needs a device |
| 8 | `diagnostic sample → memory allocation → OOM callback` | `service/oomkiller/recorder*` and the extra-metadata producers | **recorded only** (window A's R-8: the report path can complete a transition); the allocation cost needs a device |
| 9 | `system proxy → local proxy listener → TUN redirect` | inbounds × TUN × platform routing | **NOT_OBSERVABLE** — needs two live capture paths and a platform |
| 10 | `fallback decision → policy enforcement → actual socket` | `common/httpclient` H3→H2→HTTP/1.1, naive's H3 probe, trusttunnel's H3 verdict | **policy verified** (H3-only builds no fallback object; security-class failures excluded from retry); the *silence* of a legal demotion is window B's R-B3 |

## What this catalog says, plainly

- Six of the ten shapes already have a hardened mechanism **and** an executed test. The two shapes this
  effort broke open (4 and 5) are fixed with mutation controls.
- Two shapes are open for reasons that are structural, not sloppy: TB-5 (cross-repo + pin evidence) and
  the device-bound ones (7, 8, 9).
- The most valuable *new* information in this window is the negative-but-measured result on shape 6: the
  DNS cache × generation × dialer triple is not only implemented, it has 12 tests naming the exact
  interleavings, including the NXDOMAIN case. A future round should treat that family as a model for how
  the others should be written, not as an area to re-attack.
