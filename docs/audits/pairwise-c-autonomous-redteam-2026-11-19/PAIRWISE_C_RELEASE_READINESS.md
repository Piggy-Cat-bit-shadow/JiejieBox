# Release readiness — evidence gathered before the 0.1.53 line ships

This page is the "is the tree safe to release" answer, as measured in this environment at
`afd523415` (plus this round's commits). It is deliberately separate from the window audits: those
report *what was found*; this reports *what the release gates did when they were run*.

Everything below was executed; nothing is inferred from a CI declaration.

## 1. The repository's own release gates

| Gate | Command | Result |
| --- | --- | --- |
| Pin lock | `python scripts/ci/release-freeze.py verify --lock release/source-lock.json` | **PASS** — "every pin in the tree is the pin the lock records" (version `0.1.53`, 10 go modules, 3 clients) |
| Knowledge entry points | `python scripts/ci/check-knowledge-facts.py` | **PASS** — entry points agree on one dated target, report index covers its directories, 55-item matrix intact |
| Static vet | `go vet ./...` | **exit 0**, whole module |
| Formatting | `gofmt -l .` | 4 files, all **pre-existing and outside the CI gate** (§4) |

## 2. Build matrix (the profiles the release actually ships)

`CGO_ENABLED=0`, `cmd/sing-box`:

| Target | Tags | Result |
| --- | --- | --- |
| windows/amd64 | `release/DEFAULT_BUILD_TAGS_OTHERS` | **ok** |
| windows/amd64 | `release/DEFAULT_BUILD_TAGS_WINDOWS` (the shipped profile: `with_naive_outbound`, `with_purego`, `http2legacy`, …) | **ok** |
| linux/amd64 | `DEFAULT_BUILD_TAGS_OTHERS` | **ok** |
| linux/arm64 | `DEFAULT_BUILD_TAGS_OTHERS` | **ok** |
| darwin/arm64 | `DEFAULT_BUILD_TAGS` | **BLOCKED_BY_ENVIRONMENT** — `cronet-go/lib/darwin_arm64: build constraints exclude all Go files`; the Darwin slice is a cgo/native archive that cannot be built from this host. The Darwin CI job is the only honest place to judge it |

## 3. Tests

### 3.1 Full sweep, and what the failures turned out to be

`go test ./... -count=1` (untagged) reported four failing packages. Each was classified, and **none is
a code defect**:

| Package | Failure | Classification and proof |
| --- | --- | --- |
| `github.com/sagernet/sing-box` (root) | 13 tests, e.g. `TestADistinctEndpointTagStarts`: `WireGuard is not included in this build, rebuild with -tags with_wireguard` | **build tags, by design.** The whole root package is green under the release profile: `go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" .` → `ok 13.9s` |
| `protocol/naive/quic` | `TestRealHTTP3ListenerIsRegistered` | **build tags.** Green under the release profile (`ok 0.1s`) |
| `common/tlsspoof` | 3 `TestIntegration…` | **environment.** `tls_spoof: open WinDivert: windivert: open SCM: Access is denied.` — WinDivert's service control manager needs Administrator |
| `common/windivert` | 5 `TestIntegration…` | **environment.** same `open SCM: Access is denied` |

### 3.2 The suites this effort touched, under the **shipped Windows profile** (`http2legacy` included)

```
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" . ./daemon/ ./common/runtimecoord \
        ./common/resourceguard ./service/oomkiller ./transport/v2rayhttp \
        ./dns/transport/fakeip ./protocol/vless ./protocol/tuic
```

Result: **all nine `ok`** — including the root package and the daemon, which are the two the release
actually ships:

```
ok github.com/sagernet/sing-box              14.599s
ok github.com/sagernet/sing-box/daemon       23.269s
ok .../common/runtimecoord                    0.139s
ok .../common/resourceguard                   1.073s
ok .../service/oomkiller                      0.900s
ok .../transport/v2rayhttp                    0.205s
ok .../dns/transport/fakeip                   0.230s
ok .../protocol/vless                         2.754s
ok .../protocol/tuic                          0.936s
```

The same profile was used for the wire pins:

```
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" -run \
  'TestHTTP2Preface|TestAnIdleSessionSendsNoPing|TestTheSessionPrefersOneConnection' ./transport/trusttunnel
  → ok 1.309s
```

That matters because the Windows profile carries `http2legacy`: the TrustTunnel preface fix (B-1) was
verified **in the profile that ships**, not only in the default one.

### 3.3 Race detector on the core state machines

```
go test -race ./common/runtimecoord ./common/dialer ./route
  ok common/runtimecoord   1.167s
  ok common/dialer        23.274s
  ok route               461.764s
```

`route` at 7.7 minutes under the race detector is the longest single piece of evidence in this
release; it exercises the transition machinery, the listener phase, the fake-IP decision and the rule
walk.

## 4. Two things that are *not* clean, and are not blockers

**4.1 Four files are not `gofmt`-clean, and the CI gate cannot see them.**
`daemon/started_service.go`, `daemon/connection_stream_compat_test.go`,
`daemon/reload_request_identity_test.go`, `experimental/libbox/command_server_dns_history.go`. All four
are unmodified in the working tree, and the `verify.yml` gofmt step only covers
`cmd include option protocol route service transport common dns adapter` — so this is a **pre-existing
gap in the gate**, not a regression. It was left alone deliberately during a release freeze: reformatting
`daemon/started_service.go` would add alignment-only noise to the most release-critical file in the
tree. Recommended after the release: `gofmt -w daemon experimental && gofmt -l ./...` as the gate, in
one commit of its own.

**4.2 `go test ./...` without tags looks broken.**
The 13 root failures above are tag-gated by design, but the untagged command is what a new contributor
runs first. The repository's own answer is `verify.yml`'s `go test -run '^$' ./...` (compile only) plus
profile-tagged package lists. Recorded, not changed.

## 5. What this round fixed, and what it accepts

The eleven fixes across the three windows are in `docs/audits/pairwise-{a,b,c}-*`; the ones with
release-relevant effect:

| Fix | Release-relevant failure it removes |
| --- | --- |
| naive no longer registers `CloseAllConnections` as a pressure hook (A) | every live naive tunnel being cut on the first memory-pressure event |
| the admission gate hands recovered capacity to its queue (A) | dials timing out after pressure subsides |
| hy2/tuic carriers have a shut-down state (A) | a QUIC connection created after teardown that nothing owns |
| the TrustTunnel preface is pinned to Chrome's (B) | the H2 camouflage drifting with the toolchain |
| `Reset` on v2rayhttp/grpclite (B) | a fresh TLS+H2 handshake per stream after any network change |
| fake-IP refuses out-of-range allocations (B) | a synthetic address dialled as a literal |
| the TUN guard recognises a retired generation's placeholder (B) | the same leak, one generation later |
| the durable fake-IP record is read with no fakeip server (B) | a stale placeholder dialled after a config change |
| the publish decision is atomic (B) | a connection from the old network stamped current |
| every gated outbound routes its listener through the gate (C) | the double transition that killed the session built a moment earlier |
| the fake-IP cursor resumes past the ledger (C) | a client's held address remapped to a different domain after a reload |
| the QUIC pool's publish and its acknowledgement are one decision (C) | a connection built on the old network stamped as current, then handed to every later demand |
| a replaced or removed node loses its predecessor's URL-test measurement (release windows A) | a never-probed node selected on evidence about a server that is gone |

**Accepted, unfixed, and why** (each has a file:line and a settling test in the window audits):

| Item | Release risk | Why not now |
| --- | --- | --- |
| trusttunnel hands out a session without reserving it (A, R-1) | P2 — one wasted session during a trim | needs a reservation protocol across four call sites in the transport's most delicate path |
| anytls has no `ReuseSuspect`; mieru no idle releaser (A, R-2/R-3) | P2 — a pre-sleep session can take new work; mieru's pool is not trim-reachable for ~5 s | requires SDK methods that do not exist at the current pins |
| `sing-tun` UDP NAT: no admission on the path, eviction does not cancel the dial it launched, download-only flows expire, byte budget × instances (A, R-4…R-7) | P1/P2 — the 4096-entry table is the only bound, and a burst can outrun it | cross-repo fix plus a pin bump whose evidence is a CI run; needs a product decision on cancelling a UDP flow's dial |
| `v2rayquic`'s publish site is not atomic (B, R-B2) | P3 — a narrow race | its acknowledgement is deliberately after the publish lock; the atomic form needs that section restructured |
| H3→H2→HTTP/1.1 demotion is silent; naive's fallback window can stay unarmed (B, R-B3/R-B4) | P2 — diagnosability, and extra probes on short deadlines | both are observability/policy decisions (what must be visible, what "ambiguous" means) rather than bugs |
| reload is stop-then-build (B, R-B9) | P2 — a semantic validation failure leaves nothing running until config recovery | lifecycle redesign; needs a device to judge whether a capture gap beats a failed reload |
| report writing can complete a transition and run reclaim (A, R-8) | P3 — by design, pinned by a test | documented decision about who may advance the pressure state machine |
| `e2e` (58 tests) and `protocol/` suites run by no push-time job | P2 — regressions there are not caught before a push | **measured and now guarded** (§7): a script reports the exact set and a ratchet fails only when a new gap appears; mounting the 35 known gaps is a CI-cost decision, measured 35 s for `e2e` |

## 6. The network-change listener sweep: 30 implementers, no terminal-Close-as-reset

The single most dangerous shape for a release is "a network change permanently breaks an outbound"
(a `Close()` used as a reset on a client that cannot be rebuilt). Every `InterfaceUpdated`
implementation in the tree was enumerated and classified:

| Group | Count | What it does | Verdict |
| --- | --- | --- | --- |
| Gated outbounds (C-1) | 7 | `gate.NetworkChanged` → the owner's reset action | correct after this round |
| Outbounds with a reset/retire action | 8 | `Reset()`, `CloseIdleConnections()`, `RetireSuspect()`, `CloseWithError()` | correct: none of them terminates a client it cannot rebuild |
| Outbounds that swap or re-derive state | 6 | mieru swaps the SDK client under a write lock (its SDK cannot restart after Stop); direct re-fetches local addresses and closes its ICMP port; naive resets its fallback window and closes pooled sockets, keeping the engine; openconnect/openvpn/tailscale re-derive | correct |
| Inbounds / endpoints | 6 | rebind the listener or device under their own lock | correct |
| DNS transports / misc | 3 | re-read the system configuration | correct |

Detailed checks: `protocol/direct/outbound.go` closes only its ICMP port (recreated on demand);
`protocol/naive/outbound.go` closes pooled connections while the Cronet engine stays alive;
`protocol/mieru/outbound.go` holds the write lock across the client swap precisely so no dial can
observe a half-replaced client. No implementation uses a **terminal** `Close()` on a resource it needs
afterwards — which is the shape that turned into a permanent-staleness bug in the v2ray HTTP pools
(B-2) and was fixed there.

## 7. The package-execution gate: the gap, measured, and a ratchet

The audit's most repeated finding was that a defect hid behind a suite **nobody ran**. That is now
measured instead of suspected:

```
python3 scripts/ci/check-package-execution.py --list
packages with tests that no workflow runs: 35 of 109
```

The count is honest about its own method in two ways that matter: `go test -run '^$' ./...` is compile
only and does **not** count as execution (that is the line that made the gap invisible), and shell
continuations are joined before a package list is read (a line-based reader misses every package on the
continuation of `go test ... \` — it reported 64 gaps before that fix, which would have been a false
alarm).

`scripts/ci/uncovered-packages.txt` freezes what is left, with the reason next to each line. The 27
pure-logic packages that were in it have since been **mounted**: they ran green on Windows in one
27-package batch (~26 s wall) before being added to `verify.yml` as "the pure-logic packages no other
job runs". What remains is 8 — `e2e` (needs one Linux run first) and 7 that need a device, a privilege
or a platform (WinDivert/capture, power events, schannel). `verify.yml` runs the census script with
`--baseline`, so the step **fails only when a package gains tests and no workflow runs them** — a
ratchet, not a coverage mandate.

The gate has a self-test with its own mutation controls: forcing `covers()` to answer true, or removing
the continuation join, both make the self-test exit 1. That is what distinguishes it from the
`-run '^$'` line it replaces.

## 8. Transcripts
```
release-freeze: PASS: every pin in the tree is the pin the lock records.
knowledge-facts: PASS: the entry points agree on one dated target, the report index covers its
                 directories, and the 55-item matrix is intact.
vet ./...       : exit 0
windows/amd64 (OTHERS, WINDOWS)  : ok
linux/amd64, linux/arm64 (OTHERS) : ok
darwin/arm64 (Darwin)            : BLOCKED (cronet darwin archive unavailable on this host)
root package (OTHERS)            : ok 13.895s
protocol/naive/quic (OTHERS)     : ok 0.101s
critical packages (WINDOWS)      : root 14.599s · daemon 23.269s · runtimecoord 0.139s ·
                                   resourceguard 1.073s · oomkiller 0.900s · v2rayhttp 0.205s ·
                                   fakeip 0.230s · vless 2.754s · tuic 0.936s
-race runtimecoord / dialer / route : ok 1.167s / 23.274s / 461.764s
trusttunnel wire pins (WINDOWS profile) : ok 1.309s
```
