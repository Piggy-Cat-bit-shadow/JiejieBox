# Phase 02A — Naive client / Cronet upstream absorption

| | |
|---|---|
| Phase | 02A (construction) |
| Date | 2026-11-02 |
| Branch | `testing` (default; no `main`) |
| Start base | `b6b03079` (working tree clean, synced) |
| Dependency commit | `Piggy-Cat-bit-shadow/cronet-go` `bd2b70516b927e788b2dab91c17984d3d3a2d052` (fork main) |
| JiejieBox commit | `f13f46e43dcd09dea17dcdfe3bcc08ac31aba949` — cronet pin bump + outbound comment |
| Next | `03` — Naive server / shared HTTP |

## 1. What was absorbed, and why it is a real fix

Upstream `SagerNet/cronet-go` `fa319c4f3b554bd7c647c6e0f18a7195ac1497a6` ("Let the dialer resolve the
server address", 2026-10-23, `dev`) was not an ancestor of the fork. It was ported onto the fork's
main line as `bd2b705`.

**Before** (fork `c9c0e6e`): Chromium resolved the Naive server name through the in-process DNS
bridge and handed the socket callback an **IP address**. JiejieBox configures its dialer with
`RemoteIsDomain: true` and `ResolverOnDetour: true`, but the dialer never saw the name, so
sing-box's own DNS policy and dual-stack scheduler could not decide how the *server* was reached;
Chromium's single answer did.

**After:** the DNS bridge answers the server name's A query with the synthetic address
`192.0.2.1`, and the TCP/UDP socket callbacks map that address back to the configured
`ServerAddress` before dialing. The dialer therefore receives the name (or the configured literal)
and performs the only real resolution. Chromium's fake answer is a routing marker, not a DNS result.

The fork's own material was kept:

* per-CONNECT H3/H2 transport choice (`fd2f258`) and its headers;
* DNS bridge SERVFAIL packing and short-write framing (`minimalSERVFAIL`, `writeFullDNS`);
* the Go-only module split: all `lib/<os>_<arch>` native modules stay at `8c68ce8`.

## 2. The three-layer audit (only client-side items are in scope)

| Candidate | Status | Evidence |
|---|---|---|
| `SagerNet/cronet-go` `617d38f` (Fix slow open), `172bb4e`, `f2dd023`, `fe74d92`, `2137940`, `795c32b`, `049f290` | `ALREADY_PRESENT` | every one is an ancestor of the fork head (`git merge-base --is-ancestor`, 2026-11-02) |
| `SagerNet/cronet-go` dev `fa319c4` (server resolution) | **`MERGED`** | not an ancestor; ported as `bd2b705`, with tests |
| `SagerNet/cronet-go` dev `b3c2ed8` (Generate all package) | `ALREADY_PRESENT` in the fork's build commits | the fork's `8aa139e`/`bd2b705` tree publishes `all`; the pin bump moves both modules together |
| `SagerNet/sing-box` `310206a88` ("Fix naive performance") | `ALREADY_COVERED` | its only source change is a cronet-go version bump; the dependency is now newer than that pin |
| `SagerNet/sing-box` `37da1712e` (receive window options published) | `ALREADY_PRESENT` | `buildCronetNaiveClientOptions` maps `ReceiveWindow` and `QUICSessionReceiveWindow`; `outbound_option_plumbing_test.go` and `receive_window_bounds_test.go` pin the mapping |
| `SagerNet/sing-box` `a349a0d76` (QUIC congestion), `3c4a5a6ed` (HTTP/2 inbound) | `INAPPLICABLE` here | both change `protocol/naive/inbound.go` / server options; they belong to phase 03, not the client |
| naiveproxy `v154.0.8037.49-3/-4/-5` (sleep/resume, QUIC window/BBR/cleanup, eMudhra block) | `BLOCKED_BY_ENVIRONMENT` (native) | see §3 |
| `ushan0v/sing-box-forkop`, `uskg8/sing-box-extended`, `flyzstu/xhttp-box` | no client improvement found in the checked window | fork commits reviewed in `FORK_CANDIDATES.md` |

No adapter code was re-implemented: H3 fallback, per-CONNECT transport selection, failure facts,
`CloseAllConnections` on network change, single-engine handling and the padding codec are unchanged.

## 3. Native gap: exact, not implied

The fork's `naiveproxy` submodule is pinned at `97ae698dede50ad27a9dd39b93de13da5a967340`
("Disable libc++ overridden function detection on MIPS O32", 2026-09-25). Its
`CHROMIUM_VERSION` is **`154.0.8037.49`**, i.e. the 154 base **before** klzgrad's
`v154.0.8037.49-3` (2026-10-06), `-4` (2026-10-07) and `-5` (2026-10-22).

The `-5` change is proven absent: `src/net/cert/cert_verify_proc_blocklist.inc` at the pinned
submodule does not contain the nine SPKI hashes added by klzgrad commit
`23953c90e3b9f08f8d0c6ede92ae35f357af6f73` (first new hash `{0x49, 0xdd, 0x35, 0x3d, ...}`).

The native archives are the controlled modules at `8c68ce8` (source lock
`cronet_native.shas = ["8c68ce89873c"]`), so adopting the `-5` blocklist, and the `-3`/`-4`
Chromium patches (sleep/resume, QUIC cleanup), requires rebuilding those archives with the Chromium
toolchain. That toolchain is not available here and the repository deliberately treats those
archives as controlled. **No Go-level certificate-name block was added to imitate the fix.** The
gap is deferred with this evidence, not papered over.

## 4. Verification

Environment: `go1.25.5 windows/amd64`; cronet modules fetched directly from the fork; the native
modules are only compiled, not loaded.

| Command | Result |
|---|---|
| cronet fork: `go build -tags with_purego ./...` | exit 0 |
| cronet fork: `go test -tags with_purego -count=1 .` | `ok` (includes the new fake-address and dial-mapping tests) |
| JiejieBox: `go test -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./protocol/naive/` | `ok … 0.471s` |
| JiejieBox: `go test -race -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./protocol/naive/` | `ok … 2.007s` |
| `go mod tidy -diff` (root and `test/`) | exit 0 |
| `go list -mod=readonly -m all` (root and `test/`) | exit 0 |
| `release-freeze.py verify --lock release/source-lock.json` | `PASS: every pin in the tree is the pin the lock records.` |
| `go build ./include/` (untagged server profile) | exit 0 |

The fork commit adds unit tests for the two new properties: the fake-address DNS wrapper answers A
for the server name without reaching the upstream resolver, and the dial callback maps the fake
address back to the configured server (including a domain).

Not run: Apple/Windows native runtime, macOS multi-engine behaviour, device tests. The push CI does
not link `with_naive_outbound` on Linux (`DEFAULT_BUILD_TAGS_OTHERS` omits it); `verify.yml`
"cronet provenance" checks the replacements and archive bytes, not runtime behaviour. Apple
verification remains `BLOCKED_BY_ENVIRONMENT`.

## 5. Risks and follow-ups

1. **Resolution plane changed on purpose.** The server name is now resolved by the fork dialer, not
   by Chromium. This is what makes the existing `ResolveDialer` requirement meaningful; a
   configuration that only works if Chromium resolves the server would be a regression. The
   outbound already requires a resolve dialer, and the tagged package tests pass.
2. **Native `-3`/`-4`/`-5` remain unavailable** until the controlled archives are rebuilt; the
   exact proof for `-5` is in §3. Do not describe the current native binary as carrying that fix.
3. **Apple runtime is unverified.** The change is Go-only and platform-independent, but only an
   Apple build/run can prove the engine still resolves/connects there.
4. **Phase 03 owns the server side**: `a349a0d76`, `3c4a5a6ed`, forwardproxy `e747854` and the
   authenticated-CONNECT idle semantic are not part of this phase.

## 6. Handoff to phase 03

Start from `UPSTREAM_LEDGER.md` §3 rows N-01…N-08, especially N-03 (authenticated CONNECT idle
semantic), N-04 (server-side QUIC BBR) and the new N-07/N-08. Do not re-open the client resolution
port unless an Apple runtime failure is observed.
