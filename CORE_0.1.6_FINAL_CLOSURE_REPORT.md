# CORE 0.1.6 FINAL CLOSURE / INTEGRATION REPORT

## 0. Provenance

| field | value |
|---|---|
| `PARENT_CORE_BASE_BRANCH` | `jiejiebox/v0.1.6-hardening` @ `91ca1652d9de94eaf1bb8d1aaa09437b1974ac10` |
| `INTEGRATION_BRANCH` | `integration/0.1.6-final-freeze` |
| `INTEGRATION_BASE_SHA` | `140e3aca345c5c3c854714ba9b16123976f71339` |
| `INTEGRATION_HEAD_BEFORE_THIS_REPORT` | `fd6c44dcf` |
| `PUSHED` | `origin/integration/0.1.6-final-freeze` (this branch only) |
| `MAIN_TOUCHED` | NO — `origin/main` does not exist on this fork (see §6) |
| `TESTING_TOUCHED` | NO — `origin/testing` still `6cc45d3f49a3908ff150045d3a113f98920eaaea` |
| `APPLE_PIN` | `90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07` |
| `GO_VERSION` | go1.26.8 |
| `GOOS` / `GOARCH` | windows / amd64 |

Worktree `C:\Deepseek\内核\int1`. Build tags from `release/DEFAULT_BUILD_TAGS_OTHERS`.

## 1. Branch ancestry — what was merged and what must not be

```text
91ca1652d  jiejiebox/v0.1.6-hardening
├── audit/upstream-absorb-0.1.6-91ca1652d         c209ddd9e   5 unique commits
└── fix/route-aware-outbound-capability-validation 0dfedb382
       └── debug/static-validation-false-deny-20261012  140e3aca3  (+5)
```

### `ROUTE_AWARE_ABSORPTION = ALREADY_ABSORBED_IN_DEBUG`

Proven by ancestry, not by inspection:

```text
git merge-base --is-ancestor \
    origin/fix/route-aware-outbound-capability-validation \
    origin/debug/static-validation-false-deny-20261012   -> TRUE
git merge-base --is-ancestor 33eba5bf7 <debug>            -> TRUE
git merge-base --is-ancestor 0dfedb382 <debug>            -> TRUE
```

Both route-aware commits are ancestors of the debug tip, so the debug line contains the
route-aware validator in full. **The route-aware branch was therefore NOT merged separately** —
merging it would have been the duplicate-merge the prompt forbids.

Consequence for the prompt's target graph: the debug line is a strict SUPERSET of the route-aware
line, so it is the correct base for integration and the graph collapses to one line plus the
upstream-absorption line.

### The two real merges

| merge | parents | conflicts |
|---|---|---|
| `audit/upstream-absorb-0.1.6-91ca1652d` | `140e3aca3` + `c209ddd9e` → `ccae18ea0` | none |
| `jiejiebox/p1c-iphone-device-closure` | `ccae18ea0` + `e65d5d4f1` → `6eef514c4` | none (auto-resolved; `box.go` was the only shared file) |

`P1C_SHA_USED = e65d5d4f1d117390aa7b97515a4a169788637389` — the branch advanced during this session
(`645d96b4a..e65d5d4f1`, one new commit, `docs(fork): record the P1-C iPhone device closure
evidence`). The newer tip was used.

Pre-merge conflict analysis, run before merging rather than discovered during:

```text
files P1-C changes ∩ files debug line changes  = { box.go }        (1 file)
files P1-C changes ∩ files upA changes         = {}                (clean)
files P1-C changes ∩ validator/physicalpath/routeflow = {}         (disjoint)
```

The three files that end up in BOTH the P1-C line and the absorption line
(`protocol/openvpn/server.go`, `protocol/openvpn/client.go`, `protocol/openconnect/client.go`)
merged to a union of both sides' hunks, and the headroom wiring was verified present by
inspection after the merge, not assumed:

```text
server.go:115  packetFrontHeadroom, packetRearHeadroom := serverOptions.DataPacketHeadroom()
server.go:130  PacketFrontHeadroom: packetFrontHeadroom,
server.go:131  PacketRearHeadroom:  packetRearHeadroom,
server.go:137  Route: serverEndpoint.routeOutbound,
server.go:726  func (s *ServerEndpoint) applyDeviceWires(serverOptions *ovpn.ServerOptions)
server.go:730  serverOptions.NewOutboundQueue = s.newOutboundQueueFactory
server.go:737  func (s *ServerEndpoint) newOutboundQueueFactory(...) ovpn.OutboundQueue
```

## 2. Apple/UI pin unification

```text
APPLE_UI_MAIN_FROZEN       = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
PARENT_APPLE_PIN           = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
CLIENT_SOURCE_SELECTION    = PARENT GITLINK ONLY
PARENT_PIN_SOURCE_OF_TRUTH = clients/apple gitlink
```

### Authority analysis — which references actually decide

Only **two** places in this tree decide which Apple revision is consumed:

| site | before | after | why it is authoritative |
|---|---|---|---|
| `clients/apple` gitlink | `bb5309b4a` (from the P1-C merge) | `90a0e9aef` | `scripts/ci/apple-client-source.sh:91` reads it with `git ls-tree HEAD clients/apple`; `sha ios` and `sha macos` both return it and `assert-shared-source` fails if they differ |
| `VERIFIED_APPLE_SHA`, `scripts/ci/check-libbox-abi.sh:103` | `bb5309b4a` (from the P1-C merge) | `90a0e9aef` | `check_gitlink apple "$VERIFIED_APPLE_SHA" apple` (`:425`) compares it against that gitlink; the two must move together or the ABI gate fails |

`release/apple-client-refs.env` carries **no SHA**: `APPLE_CLIENT_REPOSITORY`,
`APPLE_CLIENT_BRANCH=main`, `HAKO_DESIGN_BASELINE_TAG`. The branch is already `main` and there is no
SHA field to update. The P1-C line had already done the repository/branch consolidation (its commit
`9f9eea55d`, `fix(apple): pin clients/apple to the consolidated Apple main`); this commit moves the
two revision sites onto the frozen commit.

### Deliberately NOT rewritten

`docs/fork/**` references to older revisions are **audit evidence** — they record which tree was
measured and when, and several are explicitly an observation that a pin was stale. Rewriting them
would falsify the record they exist to preserve. Left verbatim, including
`docs/fork/apple-screen-state-acceptance.md:148` ("`VERIFIED_APPLE_SHA` is still `5911580a`") and
`docs/fork/p1c-iphone-device-closure.md:26,48` (`BASE_SHA = bb5309b4…`).
`docs/fork/v016-next-round-baseline.json` is a record, not an input — nothing consumes it
programmatically (verified by grep).

### `APPLE_PIN_REACHABILITY = NOT_ESTABLISHED_ON_THIS_HOST`

The contract gate — `git rev-parse HEAD` inside `clients/apple` must equal the gitlink, and the
commit must be reachable from the reviewed branch — could **not** be discharged here:

- the submodule has never been initialised on this host (`git submodule status` → `-bb5309b4a…`,
  leading `-`; no `.git/modules/clients/apple`);
- the Apple repository is not reachable offline.

So the gitlink is asserted to be a well-formed commit SHA and to MATCH the gate constant; the
commit's existence and its ancestry on `main` are NOT established. This is the boundary the gate
itself documents at `scripts/ci/check-libbox-abi.sh:98-101`: a host that cannot check a client out
can establish that the constant and the gitlink agree "and nothing more, and must not report layer 4
as passed on that basis".

**Action required before release:** on a host with the submodule:

```bash
git submodule update --init clients/apple
git -C clients/apple rev-parse HEAD          # must equal 90a0e9aef…
git -C clients/apple merge-base --is-ancestor 90a0e9aef… origin/main
```

## 3. Static-validation tails — item by item

Statuses are `CLOSED` (done and pinned), `CLOSED_WITH_PROOF` (closed by evidence that the item is
not reachable/not a defect), `DEFERRED_WITH_EVIDENCE`, or `HOST_BLOCKED`.

| # | item | status | evidence |
|---|---|---|---|
| 1 | `http_clients` detour naming nothing | **CLOSED** | §3.1 |
| 2 | internal-consumer demand | **DEFERRED_WITH_EVIDENCE** | `fdC-consumer-demand.md`; the narrow fix was implemented and MEASURED to give up a true positive (§3.2) |
| 3 | `DestinationDNSOwnership` classification for local-dialer types | **PARTIAL** | `direct` CLOSED; five endpoints + `tailcat` deferred (§3.3) |
| 4 | `CanResolveDestination` single source of truth | **DEFERRED_WITH_EVIDENCE** | confirmed in code, not demonstrable in a configuration (§3.4) |
| 5 | group fallback contract | **DEFERRED, DELIBERATE** | the "narrow per member" reading was implemented and REVERSED: it turns two true-positive tests green (§3.2) |
| 6 | cross-kind graph identity | **DEFERRED_WITH_EVIDENCE** | `fdF`; embedder-reachable only, box-level guard fires first (§3.5) |
| 7 | `bridge` L4 capability claim | **HOST_BLOCKED** | cannot construct without Administrator (§3.6) |

### 3.1 `http_clients` detour — CLOSED

The measured asymmetry, four sites accepting a `detour`, three refusing a dangling one:

```text
dns transport   REFUSED  "outbound detour not found: ghost"
outbound        REFUSED  "dependency[ghost] not found for outbound[s]"
rule-set        REFUSED  "http_client not found: ghost"
http client     STARTED   <-- the defect
```

Cause: `dialer.NewDetour` resolves LAZILY (`common/dialer/detour.go`, `DetourDialer.init` asks the
outbound manager on first dial), and the http client manager never called the eager half
(`dialer.InitializeDetour`).

Fix: validate the REFERENCE at `Start`, by asking the outbound manager for the tag. Deliberately NOT
"resolve every defined client", which constructs a transport per client and would build clients
nothing references — a new failure mode for working configurations, i.e. the very class this audit
exists to remove.

Measured after:

```text
http_client detour "ghost"                  REFUSED  "no outbound or endpoint with that tag exists"
http_client detour naming an outbound       STARTS
http_client with no detour                  STARTS
rule-set naming that same client            REFUSED at the client, not later
```

Pinned by `box_static_validation_detour_test.go` (3 tests, positive controls included).

### 3.2 group fallback / internal consumers — DEFERRED, and why

Both trace to one mechanism: roots are only declared outbounds, `m.Default()` and endpoints, so a
consumer's demand never enters the analysis, and a non-filtering group's requirement is the UNION
over its exits, applied to every member.

`selector[tcp-only, dual]` with no rule and no consumer is refused member-by-member. The narrower
reading — ask each exit for what IT advertises — was **implemented and MEASURED**:

```text
after the narrowing:
  TestStartRefusesAMemberThatCannotCarryTheRoutedNetwork   FAILS (stops refusing)
  TestPhysicalPathDryRunRefusesAnUnusableUnselectedMember   FAILS (stops refusing)
```

Those two pin the runtime failure the dry run exists to prevent (a selector switched by hand or by a
health check handing a flow to an object that cannot carry it). The narrowing is therefore not a
local change and was reverted, with the reasoning recorded in
`common/physicalpath/dryrun.go` and in commit `133e6ed4f`.

### 3.3 local-dialer ownership — PARTIAL

`direct` CLOSED: `adapter.LocalDestinationDialer` added, `direct` implements it (its dial is
structural — `DirectOutbound: true` — and `detour` is refused outright, so no other hop can take the
request), and the dry run asks for it BEFORE the wire capability.

Also closed in the same check: the capability's VALUE is now read rather than only its presence, so
an object implementing `DestinationDNSOwner` and reporting `false` is no longer told it "does not
implement it".

Deferred: `tailcat`, `masque-client`, `openconnect`, `openvpn-client`, `tailscale`, `wireguard`
endpoint. Each needs its own dial path read before the same claim is made; asserting it from an
audit report is what this round deliberately avoided.

### 3.4 `CanResolveDestination` — DEFERRED_WITH_EVIDENCE

Confirmed in code: the validator models 2 resolver sources (explicit `domain_resolver`, or
`route.default_domain_resolver`) and `common/dialer/dialer.go:104-139` has 4. The two extra are
`dnsTransport.Default()` (gated on `len(Transports()) < 2`) and an empty policy the router still
resolves through.

NOT demonstrable as a defect: the manager always registers a fallback transport
(`dns/transport_manager.go:50-58`), so with any user DNS server the count is 2 and that branch is
unreachable. Measured: one `local` server → REFUSED; one `udp` server → REFUSED; explicit
`domain_resolver` → STARTS. The models agree in every configuration constructible here, so a change
would be a guess. Proper fix: propagate the dial path's own answer instead of re-deriving it.

### 3.5 cross-kind identity — DEFERRED_WITH_EVIDENCE

`fdF` reports endpoints folded into the outbound map in `cross_kind_cycle.go:99-111` (a shared tag
makes one node whose value is the endpoint), and an endpoint's `domain_resolver` edge unmodelled.
Both are embedder-reachable on that report; the box-level guard at `box.go:702-704` refuses declared
outbound clashes first. Not independently reproduced here.

### 3.6 `bridge` — HOST_BLOCKED, claim downgraded

`fdG` reported `bridge` advertising `{tcp,udp,icmp}` while its L4 entry points fail. Not
reproducible on this host at all:

```text
{"outbounds":[{"type":"bridge","tag":"br"}]}
-> start endpoint/bridge[br]: bridge: open injection handle (Administrator required):
   windivert: open SCM: Access is denied.
```

The bridge fails CONSTRUCTION before any routing decision, so the shape is unreachable without
Administrator on Windows. The claim stays a source-level observation.

## 4. Verification

### Combined final regression — whole tree

```text
go test -tags "<tags>" -count=1 ./...
  packages ok = 82    FAIL = 3    no test files = 87
```

The 3 failures are **pre-existing and environmental**, proven by running the same three packages on
the pre-integration debug tip `140e3aca3` in a separate worktree — identical failures before and
after:

| package | cause | classification |
|---|---|---|
| `common/windivert` | `windivert: open SCM: Access is denied.` — needs Administrator | HOST_BLOCKED |
| `common/tlsspoof` | same WinDivert injection boundary | HOST_BLOCKED |
| `common/tls` | `TestWindowsClient*TLS13`: `tls handshake: read handshake: unexpected EOF` — a real handshake against a remote endpoint | NETWORK_DEPENDENT |

No package that passed before the integration fails after it.

### Targeted suites, all green on the integration tip

```text
.                        ok        common/httpclient        ok
common/physicalpath      ok        adapter                  ok
adapter/outbound         ok        protocol/group           ok
route                    ok        protocol/openvpn         ok
protocol/openconnect     ok        protocol/tailscale       ok
protocol/tun             ok        transport/device         ok
test (self-to-self)      ok        common/tls (pre-existing failures)  FAIL (environmental)
```

`test/TestOpenVPNSelfToSelf` passes on the merged tree — the production self-to-self fixture that
exercises the absorbed OpenVPN/OpenConnect work end to end.

### Retained invariants — re-asserted after integration

```text
TestFalseDenyMatrix                                11/11 PASS (each with a refused twin)
box_static_validation_no_crash_test.go              4/4 PASS
box_static_validation_ownership_test.go             2/2 PASS
box_static_validation_detour_test.go                3/3 PASS
TestFinalReceivesTheResidualAndIsChargedWithIt      PASS
routeaware mutation matrix A-F                      all RED (carried from the route-aware line)
```

### Build hygiene

```text
go build -tags "<tags>" ./...          ok
go vet (touched packages)              ok
gofmt -l (touched files)               clean
```

### Mutation evidence

| id | mutation | result |
|---|---|---|
| M1–M5 | route-demand model (entry capability, UNKNOWN, descendant exemption, parent exemption, dependency hop) | RED (carried from the route-aware line) |
| M6 | delete the duplicate-tag guard | the P0 panic returns |
| M7 | revert the `route.final` residual charge | `TestFinalReceivesTheResidualAndIsChargedWithIt` RED |

### HOST_BLOCKED verification, stated rather than claimed

| gate | why blocked |
|---|---|
| `-race` | requires cgo; no C compiler on this host |
| `scripts/ci/*.sh` (apple source selection, libbox ABI, release acceptance) | **no bash on this host** — `C:\src\MinGit` ships without `usr/bin/bash.exe`, and no other bash was found |
| Apple submodule reachability | submodule never initialised; Apple repo not reachable offline |
| `bridge` / `windivert` / `tlsspoof` | Administrator required |
| Docker-backed fixtures | no daemon |
| Apple / Android / Windows cross-builds | toolchains absent |

The shell-based gates are the significant gap: the Apple pin unification is verified by inspection
and by the constant/gitlink agreement, NOT by running the gate that enforces it.

## 5. Deliberate non-actions

- **Route-aware branch not merged** — proven already an ancestor; merging it would duplicate work.
- **`main` not touched, `testing` not touched** — and `origin/main` does not exist on this fork at
  all, so "protect main" is satisfied vacuously; no branch was created, merged, pushed or rebased
  for it. The working branch `jiejiebox/v0.1.6-hardening` remains at `91ca1652d`, untouched.
- **Apple/UI repository not touched** — no source change, no branch recreation, no history rewrite;
  the parent adapted to the frozen UI, not the reverse.
- **No source branch deleted.**
- **`docs/fork/**` evidence not rewritten** (§2).

## 6. Residual risks and open items

| risk | severity | note |
|---|---|---|
| Apple pin reachability unverified (§2) | **release-gating** | the gitlink is a syntactically valid SHA matching the gate constant, but its existence and its ancestry on `main` are not established on this host |
| The shell CI gates were not executed | high | no bash; the pin change is inspected, not gate-tested |
| Local-dialer ownership for 5 endpoints + `tailcat` | medium | same false deny `direct` had, unclosed because each dial path needs its own reading |
| group fallback union false deny | medium | needs the group-membership contract decided first |
| `CanResolveDestination` two-model divergence | low | real in code, unreachable in configuration |
| cross-kind identity / endpoint resolver edge | low | embedder-reachable only |
| `bridge` L4 over-advertisement | low | Windows + Administrator only; unverified either way |
| `protocol/group` flake | low | `TestRecheckSingleFlightCollapsesABurst` failed once under parallel package load, passes 10/10 in isolation on BOTH the integration tree and the untouched base |

## 7. Final verdict

```text
APPLE_UI_REPOSITORY          = UNTOUCHED (frozen; no write of any kind)
APPLE_UI_MAIN                = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
PARENT_APPLE_PIN             = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
PARENT_APPLE_PIN_REACHABLE   = NOT_ESTABLISHED_ON_THIS_HOST
UPSTREAM_ABSORPTION          = INTEGRATED (real merge ccae18ea0; endpoint-headroom + OpenVPN queue + OpenConnect wiring + Tailscale netmon + tun docs)
ROUTE_AWARE_VALIDATION       = INTEGRATED VIA LATER DEBUG BRANCH (ancestry-proven, not re-merged)
CORE_P1C                     = ABSORBED (real merge 6eef514c4)
STATIC_VALIDATION_TAILS      = PARTIALLY_CLOSED (1 closed, 1 partial, 4 deferred with evidence, 1 host-blocked)
FINAL_COMBINED_REGRESSION    = COMPLETE for 82 packages; 3 packages HOST_BLOCKED/NETWORK_DEPENDENT and proven pre-existing
VALIDATOR_STRENGTH_PRESERVED = YES (no safety check removed; the one narrowing that would have removed one was reverted)
MAIN_TESTING                 = UNTOUCHED
FINAL_INTEGRATION_BRANCH     = PUSHED (origin/integration/0.1.6-final-freeze)
```

`STATIC_VALIDATION_TAILS = PARTIALLY_CLOSED` is the honest reading. The prompt's desired end state
asks for `STATIC VALIDATION TAILS = CLOSED`; one item is closed, one is half closed, four are
deferred with a measurement explaining why closing them is not a local change, and one cannot be
reached on this host. Reporting that as `CLOSED` would be a false statement about four items.

The single highest-value remaining action is not code: **run the Apple submodule reachability check
on a host that can**, because that is the one gate this integration can fail at release time.
