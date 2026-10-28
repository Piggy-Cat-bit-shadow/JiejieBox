# Acceptance index

Every row is a **requirement**, the evidence that exists for it, its **evidence level**, and a
**command that re-checks it**. A row with no command is not verified; the honest entry is
`UNVERIFIED`, and several rows below are exactly that.

## How to read this file

Post-baseline checks run locally at target `011f3fdcd99` on 2026-10-28 (unit tests, not device or CI evidence):

| Requirement | Test | Result |
|---|---|---|
| TUN start/reset handoff and post-start updates | `go test ./protocol/tun -run TestInterfaceUpdated -count=1` | PASS |
| Dual-stack family empty answer logging | `go test ./dns -run 'TestFamilyProbeEmptyAnswerIsNotAnError\|TestUnquietEmptyAnswerStillLogsAnError' -count=1` | PASS |
| Known FakeIP domain / fragmented sniff | `go test ./route -run 'TestPacketSniffKnownFakeIPDomainDecision\|TestKnownFakeIPDestinationStopsFragmentedSniffOnFirstDatagram' -count=1` | PASS |
| Family lookup generation and stable transition ownership | `go test ./dns ./common/dialer -run 'TestLookupFamiliesDoesNotMixGenerations\|TestDialEpochPredicate\|TestDialPrefersTheAtomicSnapshotWhenOffered' -count=1` | PASS |
| Fast-path default and explicit false | `go test ./route -run 'TestFastPathUnsetAndTrueAreIndistinguishable\|TestFastPathFalseOnlySuppressesTheIntrinsicShape\|TestDirectFastPathSurvivesTransparentSelectorWithNoPolicy\|TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt' -count=1` | PASS |

The pipe escapes above are Markdown table escaping; use ordinary `|` in the shell regex.

On the same Windows host and target, unfiltered package tests passed for `./dns`,
`./dns/transport/...`, `./route/rule/...`, `./common/dialer`, `./common/physicalpath`,
`./protocol/tun`, `./common/sniff`, `./common/bufgeom`, `./common/runtimecoord`, `./common/power`,
`./protocol/masque`, `./transport/masque`, `./transport/http`, `./protocol/vless/...` and
`./protocol/anytls`. `./protocol/snell` reported no test files. These local package results do not
establish interoperability or behavior on another OS.

After the branch advanced to `cb71cf80d72` and changed the `cronet-go/all` pin, unfiltered
`go test ./protocol/group ./common/httpclient ./protocol/tuic -count=1` also passed. This does not
test the Cronet native client.

With `DEFAULT_BUILD_TAGS_OTHERS`, the root tests `TestADNSTransportDetourMustCarryTheQuery` and
`TestAnHTTPClientDetourMustCarryTheConfiguredTransports` passed at the newer branch state. Mieru
failure-classification and rollback tests also passed locally; they do not establish interop.
The unfiltered `go test ./transport/v2rayxhttp -count=1` also passed locally at the newer branch
state, including the packet-up tests available without extra build tags.
At the same branch state `go test ./option ./adapter/outbound ./common/trafficclass -count=1`
passed; this is local unit evidence, not a platform or deployment certification.
Three root `TestBoxClose*` tests for repeated/concurrent close and error preservation passed with
`DEFAULT_BUILD_TAGS_OTHERS`; this un-raced run does not prove every panic or concurrency interleaving.
Four selected `./route` tests for notification coalescing, confirmed offline recovery, short gaps
and cross-interface handover also passed locally; they do not replace a real-device transition test.
Selected `./protocol/shadowtls` first-record timeout, listener cleanup and handshake-target cleanup
tests passed locally; this is not external fallback-server interoperability evidence.
The endpoint-owned resolver retirement tests for Tailscale, OpenVPN and OpenConnect passed locally
with `with_tailscale`; they cover the targeted pool walk, not a real network transition.
Selected `common/srs` truncation, checksum and decompression-budget tests and `route/rule` remote
stream tests passed locally; they do not establish behavior against every live server.
`TestCommandServerCloseClosesTheBox` passed on this Windows host with `p2ios` and the other-platform
build profile; the test is not evidence of a mobile-device lifecycle run.

`go test -tags with_quic ./transport/masque -run 'TestWriteBatchChunksABurstAboveTheQueueCapacity|TestWriteBatchFallsBackOnlyForTheRefusedTail' -count=1 -v`
passed both named tests. An untagged run selects neither test, so the untagged package result above
is not evidence for chunk ownership.
The tagged HTTP/3 default tests `TestHTTP3MigrationEnabledByDefaultForClient`,
`TestMASQUEH3PathManagerDefaultsToEnabled`, and `TestParseBBRProfileUnsetKeepsTheLibraryDefault`
also passed locally; they do not prove migration on a real network.
Naive `TestH3Fallback` cases passed with `with_naive_outbound,with_purego` using a temporary modfile
that substituted the clean local Cronet checkout at the pinned `c9c0e6ec8845`. This is unit evidence
for the local source, not evidence that the downloaded module, native ABI or a live peer was tested.
The unfiltered Naive package suite also passed under that same local-substitution setup.

The unfiltered `go test ./route -count=1` on this Windows host **failed** after 570 seconds:
`TestTeardown_OneSidedCompletionNeverRunsOnClose` timed out after 120 seconds waiting for the peer
to receive the request. A subsequent isolated run of that test passed in 0.01 seconds. The suite
failure is not a pass and its cause is not established by the isolated rerun.

On Windows 10 Enterprise LTSC 2021 build 19044 with Go 1.25.5, the broader
`go test -tags with_utls ./common/tls -count=1` **failed**: `TestWindowsClientHandshakeTLS13`,
`TestWindowsClientRoundtripTLS13`, and `TestWindowsClientTLS13PostHandshakeConcurrentWrite`
returned `tls handshake: read handshake: unexpected EOF`; a filtered rerun reproduced all three.
The cause has not been established. The filtered uTLS fingerprint/ECH and first-flight transform
tests passed on the same host. Neither result establishes behavior on other Windows versions.

* Run everything from the repository root with the environment in `ci-release.md` §4.
* `$OTHERS` = the contents of `release/DEFAULT_BUILD_TAGS_OTHERS`. **Most commands need it**, and the
  root package's tests fail without it.
* `BLOCKED_BY_ENVIRONMENT` means *this repository has no environment in which the check has been run* —
  it is not a defect and not a pass.
* Historical rows below are not `VERIFIED_CI` merely because a workflow is declared. See the Windows
  daemon module for the separately checked release run and its exact SHA.

## The `.` in every command below is load-bearing

Several rows re-check with `go test -count=1 -tags "$OTHERS" -run <TestName> .`. The trailing `.` is
the **root package**, and these commands filter by **test function name**, not by file. Two consequences
that a repository-tidy pass must not miss:

1. **Renaming a root `*_test.go` file is invisible to every command here** — it changes no function
   name. Renames are safe, and no root test filename is referenced anywhere in `.github/`, the
   `Makefile` or `scripts/`, so nothing breaks.
2. **Moving one is not.** Relocating a test to a child package silently retargets its `-run` filter at a
   package that does not contain the test: the command still exits 0 and now proves nothing. A "tidy"
   move is therefore a silent loss of acceptance coverage, which is worse than an ugly filename.

**The 28 root test files must stay in the root**, and that was measured rather than assumed:

* **9 are `package box` (internal) and cannot move in principle.** They reach root-only identifiers:
  `&Box{ctx:, scope:, statusView:, statusResolver:}` composite literals over unexported fields, plus
  `newStatusSurface`, `releasePowerGovernor`, `applyPauseEvent`, `lifecycle{}`, `deviceAxis`,
  `reconcileScreenState`, `reconcileLockState`, `ensureURLTestServices`. No exported constructor
  produces those shapes, so these tests have no external form at all — changing the package clause is
  equally impossible.
* **19 are `package box_test` and are blocked mechanically, not semantically.** All nineteen call one
  root-only fixture, `newBoxFromConfig` (`box_cross_kind_cycle_test.go:91`). Its body uses only exported
  API, so the helper is portable — but every option (duplicate it per destination, extract a shared
  test-support package, or move all nineteen together) is a change to test **content**, which is out of
  scope for a hygiene pass. The host file has the largest blast radius and must move last if it ever
  does.

A clean `go vet -tags "$OTHERS" .` proves the second group touches no root internals: Go compiles `box`
and `box_test` as two packages in one directory and forbids the external one from naming unexported
identifiers. Verified, exit 0; and all nineteen contain no `reflect`, `unsafe` or `go:linkname`.

**Coverage qualification, 2026-10-28:** `verify.yml` declares a root-package run filtered to
`TestNetLogEventCount`, as well as a compile-only `go test -run '^$' ./...`; its listed race packages
do not include the root. This does not establish that the other root acceptance tests execute in CI,
or that any specific workflow run passed. Inspect current workflow definitions and run records before
making either claim.

---

## 1. Core lifecycle and runtime coordination

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 1.1 | A cleanup registered on a closing Scope runs synchronously on the registering goroutine and is never dropped | `adapter/lifecycle.go:112-138`; `adapter/lifecycle*_test.go` | VERIFIED_UNIT + VERIFIED_SOURCE | `go test -count=1 ./adapter/...` | Go |
| 1.2 | `Scope.Close` does not wait for an in-flight `Start`, and does not fold in a late cleanup error | `adapter/lifecycle_close_boundary_test.go` (4 named tests) | VERIFIED_UNIT | `go test -count=1 -run 'TestScopeClose' ./adapter/` | Go |
| 1.3 | Epoch is monotone; a superseded lease cannot complete or release a newer one | `common/runtimecoord/coordinator.go:80-104`, `:282-310`; `coordinator_test.go`, `lease_test.go` | VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` | Go |
| 1.4 | The retirement walk reaches all four pools (outbounds, endpoints, DNS transports, HTTP client manager) | `route/reference.go:440-494`; `route/reference_coverage_stress_test.go` | VERIFIED_UNIT | `go test -count=1 ./route/...` | Go |
| 1.5 | Exactly one `ReuseSuspect` pool exists | `route/reusable_owner_inventory_test.go:49,96,117` | VERIFIED_UNIT | `go test -count=1 -run TestReusableOwnerInventory ./route/` | Go |
| 1.6 | Start/close ordering (`EstablishInitialNetworkEnvironment` last; governor before scope) | `box.go:1166-1168`, `:1210-1224`; `box_close_test.go` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -run TestClose .` | Go + tags |
| 1.7 | 100 sleep/wake cycles and 100 network transitions leak no goroutines; no resurrection after `Close` | `box_lifecycle_stress_test.go` | VERIFIED_UNIT (no `-race`) | `go test -count=1 -tags "$OTHERS" -v -run 'TestOneHundred\|TestNoResurrectionAfterClose' .` | Go + tags |
| 1.8 | Race-freedom of this subsystem | CI step `verify.yml:310` exists; **no run record in the tree** | **BLOCKED_BY_ENVIRONMENT** (`-race` needs cgo; no C compiler here) | `go test -race -count=1 ./adapter/... ./route/...` on Linux/macOS | cgo |

## 2. Path selection and start-time validation

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 2.1 | Validation dials nothing, resolves nothing, changes no selection, consumes no rotation | `manager.go:235-237`; `manager_physicalpath_test.go:357-373` asserts committed == 0 **and** previewed == 0; `TestBuildNeverCallsADialingMethod` | VERIFIED_UNIT (+ mutation controls) | `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...` | Go |
| 2.2 | `Hops` is packet order (device-nearest first) and `ControlPath` is descent order, not reversed | `physicalpath.go:564-600`; `physicalpath_test.go:462-482` | VERIFIED_UNIT | `go test -count=1 -run TestControlPathIsShorterThanThePhysicalPath ./common/physicalpath/` | Go |
| 2.3 | A control group is never a physical hop, over the **real** groups | `route/physicalpath_route_test.go` | VERIFIED_UNIT | `go test -count=1 -run TestControlGroupIsNotAPhysicalHop ./route/` | Go |
| 2.4 | The validator does not over-refuse: 11 configurations each accepted with a refused twin | `box_static_validation_false_deny_test.go` (`TestFalseDenyMatrix`) | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run TestFalseDenyMatrix .` | Go + tags |
| 2.5 | Consumer demand is provable-only; the default resolver is a graph edge | `box_static_validation_true_final_test.go` | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run 'TestConsumerDemandContractIsProvableOnly\|TestTheDefaultDomainResolverIsAGraphEdge' .` | Go + tags |
| 2.6 | A cross-kind cycle (outbound ↔ DNS transport) is refused at start | `box_cross_kind_cycle_test.go`; `box_static_validation_final_wire_test.go` | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run TestCrossKind .` | Go + tags |
| 2.7 | `common/routeflow` demand semantics | **no test files in the package**; `common/routeflow/reach.go` only | VERIFIED_SOURCE | `go test -count=1 ./common/routeflow/` → reports `[no test files]` | Go |
| 2.8 | Cross-kind graph soundness **as a whole** | CK1–CK7 were never run; only the named gaps are closed | **UNKNOWN** | — | — |
| 2.9 | `PathNode.RequiredNetworks` semantics | **write-only in production**: 2 writers, 0 production readers, 5 test readers | VERIFIED_SOURCE — **FOLLOW-UP, not a contract** | `git grep -n RequiredNetworks` | git |

## 3. Sniff

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 3.1 | QUIC packet/datagram boundaries, coalesced packets, malformed headers, `Length < 4` | real tests + two fuzz targets | VERIFIED_UNIT | `go test -count=1 ./common/sniff/...` | Go |
| 3.2 | A retained QUIC fragment never aliases the caller's datagram | address-range + datagram-poisoning tests | VERIFIED_UNIT | `go test -count=1 -run TestQUICOwnership ./common/sniff/` | Go |
| 3.3 | TLS and HTTP gates never reject a prefix the parser would ask more data for | `sniff_gate_internal_test.go`; >500 prefixes | VERIFIED_UNIT | `go test -count=1 -run TestGate ./common/sniff/` | Go |
| 3.4 | WireGuard is sniffed; the uTP ambiguity is structural and deliberately not narrowed | `TestSniffWireGuardUTPAmbiguityIsStructural` | VERIFIED_UNIT | `go test -count=1 -run TestSniffWireGuard ./common/sniff/` | Go |
| 3.5 | Fragmented-QUIC rounds are capped at 64 and the buffer is released on abandon | `route/route.go:1211-1217`, `:1340-1347` | **VERIFIED_SOURCE only** — no test, no reproducible measurement | grep `maxSniffRounds` in `route/*_test.go` → nothing | — |
| 3.6 | The sniff goroutine census | green in one audit run; documented as **load-sensitive with a shifting failing test name** | VERIFIED_UNIT (single run) — **not proof of non-flakiness** | `go test -count=5 ./common/sniff/...` under load | Go |
| 3.7 | QUIC single-frame `ClientFirefox` false positive | measured and documented; fix out of scope | VERIFIED_UNIT that it exists | `quic_ownership_test.go:271-280` | Go |

## 4. DNS

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 4.1 | A dated answer from a settled-start query may be delivered but must not be committed under a changed generation; an unsettled-start result is not delivered as stable | `dns/client.go`; `dns/cross_generation_answer_contract_test.go` | VERIFIED_UNIT | `go test -count=1 ./dns/...` | Go |
| 4.2 | A FakeIP server cannot answer a destination lookup | `8fad3e9b`; DNS demand tests | VERIFIED_UNIT | `go test -count=1 -run FakeIP ./dns/...` | Go |
| 4.3 | DNS rule evaluation, cache, reverse mapping and FakeIP generations | `go test ./dns/...` passes | VERIFIED_UNIT | `go test -count=1 ./dns/...` | Go |
| 4.4 | The `systemd-resolved` goroutine leak is fixed | **it IS fixed**, by `6b495134c` (`signalLoopStop`/`signalLoopDone`/`signalLoopOnce`, a stop-channel `select`, and a **join** in `Close`) — `docs/RELEASE-CERTIFICATE.md:70-85` is **stale** | VERIFIED_SOURCE; `BLOCKED_BY_ENVIRONMENT` to execute | `git log -S'signalLoopStop' e4b7e692..a267e99`; then needs Linux + systemd-resolved | Linux |
| 4.5 | Multi-platform DNS environment matrix | never run | **UNVERIFIED** | — | multi-platform |

## 5. MASQUE / CONNECT-IP

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 5.1 | Capsule parse/serialise, address and route types, cross-protocol overlap | `transport/masque` capsule tests | VERIFIED_UNIT | `go test -count=1 -timeout 8m -tags "$OTHERS" ./transport/masque/...` | Go + tags |
| 5.2 | Each batch chunk is atomic; accepted prefix is not replayed; substitution refuses its chunk | `batch_chunk_test.go`, `batch_order_test.go` | VERIFIED_UNIT | same | Go + tags |
| 5.3 | Session/stream teardown and close boundaries | `shutdown_test.go`, `close_boundary*.go`, `scope_ownership_test.go` | VERIFIED_UNIT | same | Go + tags |
| 5.4 | PTB and the three MTU quantities | five MTU test files | VERIFIED_UNIT | same | Go + tags |
| 5.5 | CONNECT-IP interoperability against an independent implementation | `test/jiejie/reference/connect_ip_go_interop_test.go` — a **separate module** push CI does not run | **UNVERIFIED (declaration only)** | `bash scripts/ci/run-jiejie-suite.sh` | bash + network |
| 5.6 | macOS / H3 device behaviour | never run | **UNVERIFIED** | — | device |
| 5.7 | The package's own suite | **untagged it fails 19 tests** (tag absence). Tagged: `ok 318.4 s` at the release baseline | VERIFIED_UNIT (tagged) | `go test -count=1 -timeout 8m -tags "$OTHERS" ./protocol/masque/...` | Go + tags |

## 6. Native Naive and HTTP transport

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 6.1 | Write contract: no short write is silently truncated; the frame counter advances only after a whole frame | `audit_write_contract_test.go` | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" ./protocol/naive/...` | Go + tags |
| 6.2 | Padding is negotiated, first-8-frames, 0..255 never clamped | `padding_test.go`, `padding_segmentation_test.go`, `padding_zero_frame_test.go`, `fuzz_padding_test.go` | VERIFIED_UNIT | same | Go + tags |
| 6.3 | The target ACL is enforced, and DNS rebinding cannot bypass it | `TestJiejieTargetACLRebindingCannotBypass` | VERIFIED_UNIT | `cd test; go test -count=1 -run TestJiejieTargetACL ./jiejie/` | Go + network |
| 6.4 | The per-datagram UoT guard checks **every** destination and permits when no rule matches | five named guard tests | VERIFIED_UNIT | same | Go |
| 6.5 | The guard does not break server-internal backends | `TestJiejieTargetACLDoesNotBreakMasqueradeBackend` | VERIFIED_UNIT | same | Go |
| 6.6 | Interop with the official NaiveProxy client and Caddy | interop suites exist in `test/` | **UNVERIFIED here** — push CI deliberately does not run that module, and `run-jiejie-suite.sh` has no referrer | `bash scripts/ci/run-jiejie-suite.sh` (~20 min) | bash + reference binary |
| 6.7 | Real-network STUN, independent IPv6 UDP origin, Caddy performance comparison | recorded as not done | **UNVERIFIED** | — | network |
| 6.8 | The `host's own public address` is in the deny set | recorded as **not done** — the address is deliberately not guessed | **UNVERIFIED / FOLLOW-UP** | confirm on the host | the host |

## 7. Data path: offload, traffic class, scheduler

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 7.1 | Every `DialerOptions` field is classified; an unclassified one refuses a bypass | `TestEveryDialerOptionIsClassified` (reflection, both directions) | VERIFIED_UNIT | `go test -count=1 ./common/dialer/...` | Go |
| 7.2 | `ActionBypass` is honoured by no TUN stack; a tracker on one is never created | `protocol/tun/native_bypass_*_test.go` | VERIFIED_UNIT (behavioural trace) | `go test -count=1 ./protocol/tun/...` | Go |
| 7.3 | A bypass carrying a `Port` becomes an `ActionFlow` | `TestBypassWithAPortBecomesAFlow` | VERIFIED_UNIT | same | Go |
| 7.4 | The bypass decision asserts 0 allocs **as a test** | `route/direct_offload_bench_test.go`, `route/bypass_capability_cost_test.go` | VERIFIED_UNIT | `go test -count=1 -run Bypass ./route/` | Go |
| 7.5 | The scheduler shapes aggregate, admits no over-rate, and releases on transition | `common/trafficsched` gate tests (11) | VERIFIED_UNIT | `go test -count=1 ./common/trafficsched/...` | Go |
| 7.6 | The research controller cannot reach production | a test fails if it does | VERIFIED_UNIT | same | Go |
| 7.7 | Race-freedom of `trafficclass`/`trafficsched`/`dialer`/`direct`/`tun`/`route`/`adapter` | CI step `verify.yml:310` exists; no run record | **BLOCKED_BY_ENVIRONMENT** | `go test -race …` on Linux | cgo |
| 7.8 | Real-link shaped throughput and the AI-lane p95 claims | historical measurement recorded in `doc.go`; the harness is a manual tool | **UNVERIFIED here** | `go run ./common/trafficsched/harness` against a real link | two hosts |

## 8. Buffer ownership and memory

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 8.1 | Geometry buffers are exactly poolable and `Advance`/`Prepend` never reallocate | `geometry_test.go` | VERIFIED_UNIT | `go test -count=1 ./common/bufgeom/...` | Go |
| 8.2 | The allocation ceiling | asserted in the **`!race`** build only | VERIFIED_UNIT (`!race`); the `race` half needs cgo | same | Go |
| 8.3 | Batch capability survives a counter wrapper | 3 tests with a real red control | VERIFIED_UNIT | `go test -count=1 -run PacketBatch ./route/` | Go |
| 8.4 | The kernel splice actually reaches `unix.Splice()` | proved on Linux by the fork's `linux-splice-proof` workflow; **not here** | **BLOCKED_BY_ENVIRONMENT** | that workflow | Linux |
| 8.5 | A `Pipe2` setup failure leaves splice unhanded so the caller can fall back | pinned `sing` commit `1729220c46c4`, `common/bufio/splice_linux.go`; `splice_actual_linux_test.go` | VERIFIED_SOURCE at that pin; Linux execution not performed in this refresh | run the pinned sing Linux splice tests | Linux |
| 8.6 | kTLS read-path defects | fixed by mirroring the slow path; compared textually, **not executed** | VERIFIED_SOURCE | needs Linux | Linux |
| 8.7 | Zero-copy levels 3–5 | **structurally unobservable from sing-box** | **UNKNOWN by construction** | — | — |

## 9. Power and mobile lifecycle

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 9.1 | The reuse verdict is published **before** the level is released | `box_lifecycle_ordering_test.go`, with a vacuity guard | VERIFIED_UNIT | `go test -count=1 -tags "$OTHERS" -v -run TestTheReuseVerdict .` | Go + tags |
| 9.2 | The sleep edge is recorded **before** the level is paused | same file, `probingAxis` | VERIFIED_UNIT | `-run TestTheSleepEdge` | Go + tags |
| 9.3 | The reuse bands are the policy's, and an unknown sleep takes the strict verdict | `common/power` + `box` tests | VERIFIED_UNIT | `-run 'TestReuseBandsAreThePolicy\|TestTheBridgeUsesThePolicyBandsAndNothingElse'` | Go + tags |
| 9.4 | The suspect band drains nothing | `route.TestTheSuspectBandDrainsNothing` | VERIFIED_UNIT | `go test -count=1 -run TestTheSuspectBandDrainsNothing ./route/` | Go |
| 9.5 | The trim ladder maps every level, and an unknown level is ignored | `common/runtimecoord/platform_events.go:152-220` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` | Go |
| 9.6 | The Apple device axis on a real device | one **iPhone 17 Pro Max / iOS 26.4.2** run: 13 m 13 s lock → 1 boundary → `13m13.492448s` | **VERIFIED_DEVICE** for that device only | — | that device |
| 9.7 | iPad, macOS, other OS builds | `NOT_RUN` / `SIGNING_OUT_OF_SCOPE` | **UNVERIFIED** | — | — |
| 9.8 | Real TUN, real NetworkExtension, network transitions | zero executed evidence; a release blocker | **UNVERIFIED** | `docs/fork/real-tun-validation.md` §1–§7 | root + Linux |

## 10. MTU and geometry

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 10.1 | The three MTU quantities are computed and bounded separately | MASQUE + WireGuard + `common/bufgeom` sources | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/bufgeom/...` and the MASQUE suite | Go + tags |
| 10.2 | The MASQUE outer QUIC packet is capped by a proven detour capacity | `2118ee40`; `outer_quic_mtu_test.go`, `port_mtu_lifecycle_test.go` | VERIFIED_UNIT | MASQUE suite | Go + tags |
| 10.3 | The hysteria2 hard ceiling reaches the wire with ChromeParrot on | `protocol/hysteria2/hard_ceiling_wire_test.go`; the retraction banner at `docs/fork/v016-final-closure-ledger.md:4-13` | VERIFIED_UNIT | `go test -count=1 -run HardCeiling ./protocol/hysteria2/` | Go |
| 10.4 | The low-memory (16 KiB) geometry is selected by the same source the products use | `cmd/internal/appletags -mobile-only`; `verify.yml:252-258` fails if empty | VERIFIED_SOURCE | `go run ./cmd/internal/appletags -mobile-only` | Go |

## 11. libbox and platform contract

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 11.1 | The exported libbox surface and `SetupOptions` contract | `experimental/libbox/*`; package tests pass | VERIFIED_UNIT + VERIFIED_SOURCE | `go test -count=1 ./experimental/libbox/...` | Go |
| 11.2 | The core classifies platform levels; the client only reports them | `common/runtimecoord/platform_events.go:152-220`; `experimental/libbox/platform_events.go` | VERIFIED_SOURCE | read both | — |
| 11.3 | ABI version is served and consumed | `daemon/started_service.go:39` `APIVersion = 4`; `experimental/libbox/command_client.go:760` | VERIFIED_SOURCE | `git grep -n APIVersion` | git |
| 11.4 | The Apple client is one source for iOS and macOS | `assert-shared-source` in both jobs; `check-apple-source-selection.sh` **is** wired at `client-apple.yml:134` | VERIFIED_SOURCE (gate declaration) | `bash scripts/ci/check-apple-source-selection.sh` | bash + submodule |
| 11.5 | The Apple **pin** cannot move without objection | **no *wired* mechanism exists.** `VERIFIED_APPLE_SHA` and the gitlink now **agree** at `5cab4ec5…` (`ece0fb9d2` moved the constant), but nothing runs the comparison; the selection gate is self-referential to the gitlink by design | **KNOWN GAP, VERIFIED_GIT** | `git ls-tree HEAD clients/apple` and `git grep VERIFIED_APPLE_SHA= -- scripts/`, then compare the two by hand | git |
| 11.5b | A refs file that reinstates a two-source Apple pin is refused, and the refusal does not depend on the caller's environment | `apple-client-source.sh:88-105` greps the **file** before sourcing it; `resolve` (`:386-388`) and `release-apple.sh:181` legitimately export the same names, which the pre-fix environment test mistook for a file declaration | VERIFIED_SOURCE + VERIFIED_UNIT (`ece0fb9d2`) | `sed -n '88,105p' scripts/ci/apple-client-source.sh`; assert the guard greps `$refs_file` and is called before `. "$refs_file"` | sed |
| 11.6 | ABI call-site compatibility at the pinned revision | **Swift half VERIFIED_UNIT at `5cab4ec5`** — `probe-accessors.py` over a clean `clients/apple`: `scanned 670, violations [], interface_methods 3`. **Kotlin half not run** (no `clients/android` checkout here). For the **v0.1.6 release** revision it stays **UNVERIFIED** — the revision that shipped is older than the one checked | VERIFIED_UNIT (swift) / BLOCKED_BY_ENVIRONMENT (kotlin) | `python3 scripts/ci/probe-accessors.py --contract docs/fork/libbox-abi-contract.tsv --lang swift --root clients/apple` | python3 + submodule |
| 11.7 | Android AAR provenance and architecture | gates exist; dispatch-only | **UNVERIFIED (declaration only)** | `android-core-arm64.yml` | CI |

## 12. Build, CI and release

| # | Requirement | Evidence | Level | Re-check | Environment |
|---|---|---|---|---|---|
| 12.1 | `go build ./...` untagged | run in this audit | **VERIFIED_UNIT** | `go build ./...` | Go |
| 12.2 | The server profile builds | run in this audit | **VERIFIED_UNIT** | `go build -tags "$OTHERS" ./...` | Go |
| 12.3 | The Darwin profile **cannot** build on Windows | run in this audit | **VERIFIED_UNIT** of the failure | `go build -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./...` | Go |
| 12.4 | The root design-guarantee suite passes **with the right tags** | run in this audit: `ok 5.459s` | **VERIFIED_UNIT** | `go test -count=1 -tags "$OTHERS" .` | Go + tags |
| 12.5 | The root suite is **executed** by CI | **it is not** — the untagged step runs zero tests | **KNOWN GAP, VERIFIED_SOURCE** | `grep -n 'go test' .github/workflows/*.yml` | — |
| 12.6 | Tag composition and the `-checklinkname=0` contract | CI step `:168` | VERIFIED_UNIT | `go test -count=1 ./cmd/internal/...` | Go |
| 12.7 | Pinned-upstream assumption tripwires | CI step `:179`; the script is wired | **UNVERIFIED (declaration only)** — needs bash | `bash scripts/ci/verify-upstream-assumptions.sh` | bash + network |
| 12.8 | Nested modules resolve the same fork pins | CI step `:137` | **UNVERIFIED (declaration only)** | `bash scripts/ci/check-go-module-integrity.sh --require-checked test` | bash |
| 12.9 | Release re-verifies per-platform bytes rather than rebuilding | `release.yml:144-204` | VERIFIED_SOURCE; **no run record** | read the workflow | — |
| 12.10 | Release acceptance rejects bad evidence | `verify.yml:64`, `:87` run **self-tests against a mock `gh`** | VERIFIED_SOURCE (of the design) | `bash scripts/ci/verify-release-acceptance-test.sh` | bash |
| 12.11 | `verify-evidence/` describes the release tree | **it does not** — all 14 logs name `cfc77dc5`, 101 commits behind, and 15 covered files changed afterwards | **STALE — not evidence** | `head -3 verify-evidence/*.log` | git |
| 12.12 | The §7.4 stress results | an **empty `<!--STRESS_RESULTS-->` placeholder** | **EVIDENCE ABSENT** | — | — |
| 12.13 | Windows installer / service / signing gates | nine `.ps1` gates exist; no output archived | **UNVERIFIED (declaration only)** | `client-desktop-windows.yml` (dispatch) | Windows + Admin |
| 12.14 | Docker-backed fixtures | not run; `test/docker_test.go` has **no skip guard** | **BLOCKED_BY_ENVIRONMENT** | `docker info && go test ./test/...` | Docker |
| 12.15 | Xray reference interop | dispatch-only; QUICHE live interop tests were **deleted** | **UNVERIFIED (declaration only)** | dispatch `interop-xray.yml` | Linux + network |

---

## Known-failing and known-flaky set

These are historical observations, not proof that a current failure has the same cause. Reproduce
and compare the current target and environment before classifying a failure.

| Item | Why | Not a regression because |
|---|---|---|
| `common/tls` `TestWindowsClient*TLS13` | local test-server handshakes failed with `unexpected EOF` on Windows 10 build 19044 during this refresh | cause not established; see the current result above |
| `common/sniff` `TestAgentFFrameShape*`, `TestTheSniffCensusMovesForExactlyOneLeakedGoroutine` | **load-sensitive; the failing test name differs run to run** | 5/5 passes in isolation on both the fix and the base |
| `protocol/group` `TestRecheckSingleFlightCollapsesABurst` | load-sensitive | 20 consecutive isolated runs clean on both trees |
| `common/trafficsched` under `-count=10` | ~18 minutes of deliberate sleeps trips a timeout | every individual test passes |
| `protocol/masque` `TestMasqueRestartCyclesDoNotLeakLoops` | **330 s alone** — slow, not hung | measured |
| untagged root package | 12 tag-absence failures | use `$OTHERS` |
| untagged `protocol/masque` | historical tag-absence failures | unfiltered package tests passed at the 2026-10-28 target; verify required features and tags separately |
| `common/windivert`, `common/tlsspoof` | reports call them "3 pre-existing environmental failures" | **they pass here** (25 PASS / 9 PASS, 0 FAIL) — the claim was host-specific, not tree-specific |

---

## The fork's own standing limits

These are not acceptance gaps to be closed by a test; they are recorded limits.

* Device and privileged data-plane behavior requires evidence for the specific build and platform;
  this local Windows refresh did not exercise a real TUN, Linux kTLS or Apple NetworkExtension.
* Apple artifact claims require a matching workflow run and artifact provenance for the exact target.
* **`cronet-go/lib/linux_amd64` does not link under `-race` on ubuntu-latest.** No product is affected:
  the Linux product ships `DEFAULT_BUILD_TAGS_OTHERS`, which has no `with_naive_outbound`.
* **The `systemd-resolved` goroutine leak is FIXED** (`6b495134c`). `docs/RELEASE-CERTIFICATE.md:70-85`
  still says otherwise; that document is stale. The exclusion of the `test/` module's box-level tests
  from push CI was justified by this leak and can be revisited — a CI decision, not a knowledge one.
* The historical `Pipe2` false-handover defect is fixed in the pinned `sing` source; a separate
  general active-flow stall remains a design limitation, as described in `core-lifecycle-runtime.md`.
* The Apple pin has a declared `--pin-only` verifier in `verify.yml`; the full client check has a
  different environment and workflow. A declaration is not a run result.
* **`RequiredNetworks` is write-only.**
