# Pairwise/Triplewise — index of this effort

Four rounds were executed on `testing`, each continuing from the previous head. This page is the map;
each round's own README is the entry point for its evidence.

| Round | Focus | Fixed | Recorded, not fixed | Head after the round |
| --- | --- | --- | --- | --- |
| [pairwise A](pairwise-a-resource-lifecycle-2026-11-19/README.md) | resources, memory, connection lifecycle | 4 | 8 | `b80156f81` → `2fa8895ed` |
| [pairwise B](pairwise-b-network-dns-tun-2026-11-19/README.md) | network, DNS, TUN, protocol state | 5 | 8 | `54736ec28` |
| [pairwise C](pairwise-c-autonomous-redteam-2026-11-19/README.md) | autonomous red team (patterns, cross-cutting) | 2 | — | `afd523415` |
| [pre-release evidence](pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_RELEASE_READINESS.md) | the gates that were actually run for 0.1.53 | — | — | `413e115b3` |
| [triplewise A](triplewise-a-2026-11-19/TRIPLEWISE_A_MATRIX.md) | three-system chains: teardown × generation × pool | 1 | — | `649e29021` |
| [triplewise B](triplewise-b-2026-11-19/TRIPLEWISE_B_MATRIX.md) | three-system chains: DNS/TUN/generation wiring | 0 (no new defect) | 1 cross-repo (TB-5) | `7346eacb0` |
| [triplewise C](triplewise-c-2026-11-19/TRIPLEWISE_C_FINDINGS.md) | autonomous three-system discovery, shapes → tree | 1 (QUIC publish/migration acknowledgement) | the cross-repo item + the device-bound rows | `e882ef559` |
| [release windows](pr-issue-windows-2026-11-19/RELEASE_WINDOW_STATUS.md) | the PR/Issue set: core control plane, QUIC transports, expanded search | 1 (provider health history) | named per group | `29289af38` |

## The fixes, in one list

| # | Fix | Round |
| --- | --- | --- |
| 1 | the naive outbound no longer offers a pressure hook that terminates live tunnels | A |
| 2 | the admission gate hands recovered capacity to callers already queued | A |
| 3 | hy2/tuic carriers have a shut-down state, so a post-teardown dial cannot install a client | A |
| 4 | the two xhttp pool-lifecycle tests measure reuse as a delta (and the package is now gated) | A |
| 5 | the TrustTunnel HTTP/2 preface is pinned to Chrome's under Go 1.27 | B |
| 6 | a network change no longer unregisters the HTTP pool (Reset on v2rayhttp/grpclite) | B |
| 7 | fake-IP never issues an address outside its configured range | B |
| 8 | the TUN guard recognises a placeholder from a retired generation | B |
| 9 | the durable fake-IP retirement record is read with no fakeip server configured | B |
| 10 | the generation publish decision is one atomic call (3 of 4 pools) | B |
| 11 | every gated outbound routes its listener through the generation gate | C |
| 12 | a reload no longer re-issues the addresses its predecessor handed out | C |
| 13 | a generation offered to a closed pool is released, not adopted | triple A |
| 14 | a replaced or removed node loses its predecessor's URL-test measurement | release windows A |
| 15 | a publish and its generation acknowledgement are one decision, on the QUIC pool and its migration path | triple C |
| 16 | a shared HTTP transport refuses to rebuild itself after Close (request and reset paths) | triple C |

Every one has a test that fails when the fix is removed; the mutation controls are listed in each
round's verification page (`MC-1`, `MC-B1…B6`, `MC-C1/C2`, `MC-T1a/b`).

## What remains open, and where it is written down

| Item | Risk | Why it is open |
| --- | --- | --- |
| `sing-tun` UDP NAT × admission (A's R-4…R-7, triple B's TB-5) | the largest remaining | cross-repo fix + a pin bump whose release gate needs a CI run for the new revision |
| reload is stop-then-build (B's R-B9) | a validation failure leaves nothing running until config recovery | lifecycle redesign; needs a device to judge a capture gap |
| the H3→H2→HTTP/1.1 demotion is silent (B's R-B3) | diagnosability | a reporting decision (which field, which surface) |
| naive's fallback window can stay unarmed (B's R-B4) | extra probes on short deadlines | changes what "ambiguous" means for a probe |
| `v2rayquic`'s publish site is not atomic (B's R-B2) | a narrow race | its acknowledgement is deliberately after the publish lock; needs that section restructured |
| trusttunnel hands out a session without reserving it (A's R-1) | one wasted session during a trim | a reservation protocol across four call sites |
| anytls has no `ReuseSuspect`; mieru no idle releaser (A's R-2/R-3) | a pre-sleep session can take new work; mieru's pool is untrimmable for ~5 s | needs SDK methods that do not exist at the current pins |
| report writing can complete a transition (A's R-8) | by design | a documented decision about who may advance the pressure state machine |

## The CI gap, now guarded and closed except for device-bound packages

The audit's recurring finding was a defect behind a suite nobody ran. That is measured and ratcheted:
`scripts/ci/check-package-execution.py` found **35 of 109 packages with tests that no workflow runs**;
the 27 pure-logic ones are now mounted in `verify.yml` (green on Windows in one batch before mounting),
**`e2e` is mounted too** after a real Linux run (Go 1.27.4, CGO off, 57 PASS / 1 gated SKIP / 0 FAIL,
16.0 s; transcript in `pr-issue-windows-2026-11-19/e2e-linux-run.txt`), and the
remaining **7** are in `scripts/ci/uncovered-packages.txt` with the reason each needs a device or a
privilege. The gate fails only when a **new** package gains tests without a runner. Its self-test
(`check-package-execution.test.py`) has two mutation controls, both red.

## Dependencies, after the network returned

`govulncheck ./...` found **13 vulnerabilities in 5 modules this code calls**. Five were bumped away
without touching the module's `go` line — `grpc v1.79.3 → v1.83.2`, `brotli → v1.2.7`,
`klauspost/compress → v1.20.2` — and the rescan reports **8 remaining** (six in `golang.org/x/net`, two in
`golang.org/x/crypto`), whose fixed releases require **`go ≥ 1.26`**. Raising that line changes the TLS
ClientHello (measured with the repository's own probe: it adds `SecP256r1MLKEM768` and
`SecP384r1MLKEM1024`), so it is a product decision, not a debugging step. The full record, with the
command that would perform the bump, is
`pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_DEPENDENCY_VULNERABILITIES.md`.

## Unexecuted prompt files

The three windows in `JiejieBox_PR_Issue_Debug_…` (`01_窗口A_核心控制面_TUN_DNS`, `02_窗口B_协议传输_QUIC`,
`03_窗口C_扩大搜索`) were **not read or executed**. The three-system prompts are complete: triplewise A,
B and C were all read and executed in this effort. The PR/Issue set is recorded here so the next session
starts from a known state rather than assuming it was covered.
