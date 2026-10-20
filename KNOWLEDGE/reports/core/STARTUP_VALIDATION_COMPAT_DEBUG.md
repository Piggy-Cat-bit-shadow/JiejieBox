# Startup validation compatibility — adversarial debug

**Date:** 2026-10-21 (Asia/Shanghai)
**Repository / branch:** `Piggy-Cat-bit-shadow/sing-box`, `testing` only. No branch, tag or workflow
was created; nothing was force-pushed.

| identity | value |
|---|---|
| construction under test | `48ae8168a` (physicalpath split), `cd2316a70` (HTTP/DNS detours) |
| `START_SHA` of this debug | `1d3bb6e09e41235ab70cf92e7f11b0478e05e7c9` |
| debug code SHA | `39bd802a2d8f7be7bdfb9a57a10a9c28033b0ce3` (message fix + the four attack tests) |
| upstream baseline | `SagerNet/sing-box` `testing` = `dfb80349bf5779ec0a3f834c9ec2794345bff8f0` |
| environment | Windows 11 amd64, Go toolchain in the repo's stated location, `-tags "$(release/DEFAULT_BUILD_TAGS_WINDOWS)"`, `CGO_ENABLED=0` (and `CGO_ENABLED=1` + llvm-mingw for `-race`) |

**Verdict: `PASS_WITH_LIMITS`.** The split is correct on every axis this round could measure, one
real message defect was found and fixed, and the limits are runtime end-to-end verification (needs a
live inbound/outbound pair) and a slow-log-writer attack.

## 1. What was attacked, and what the code actually does

| axis | question | evidence |
|---|---|---|
| A. verdict | does Start succeed/refuse the right way? | `box_startup_warning_capture_test.go`, `box_startup_warning_debug_test.go`, `box_static_validation_*` |
| B. diagnostics | level, content, count, encoding | real `log.output` capture at warn level; count assertions; emoji/pipe/CJK test |
| C. runtime | is the traffic behaviour unchanged? | no runtime file changed (`git diff --stat`), and the existing capability tests still pass |

## 2. Attack matrix

`START` is the observed `Start` result; `WARN` is whether a warning was observed; `RUNTIME` is the
expected and unchanged traffic behaviour. Cases marked *existing* are covered by a test that predates
this round and was re-run.

### A — the original UDP shape and rule-order attacks

| id | configuration | START | WARN | RUNTIME | evidence |
|---|---|---|---|---|---|
| A1 | unconditional `udp` reject first, TCP route to a TCP-only leaf | OK | none | UDP is rejected by the rule; TCP runs | `fdMatrix` FD-A4 accepted twin; `TestRouteAwareTerminationRemovesTheProtocolFromTheDiagnostic` proves the clause says `tcp` alone |
| A2 | catch-all first, `udp` reject after | OK | **yes**, `cannot serve the udp flow` | UDP to the leaf fails on capability; TCP runs | FD-A4 warned twin |
| A3 | no rules, `final` = TCP-only | OK | **yes**, names the missing `udp` | UDP to `final` fails; TCP runs | `TestFinalReceivesTheResidualAndIsChargedWithIt`, matrix START-01-A3 |
| A4 | port-scoped / domain-scoped partial reject | OK | **yes** | the remaining UDP still reaches the leaf | FD-A5, FD-A6, FD-A8 |
| A5 | `domain`/`process`/`rule_set`, logical AND/OR, invert (UNKNOWN) | OK | only for the proven part | unchanged routing | FD-A9, FD-A10; `adapter/rule_network_scope.go` untouched |
| A6 | non-terminal actions (`sniff`, `resolve`, `route-options`) before a UDP route | OK | **yes**, the risk is not missed | unchanged | existing route-aware tests; action semantics untouched by this round |

### B — group selection, unselected members and detour position

| id | configuration | START | WARN | RUNTIME | evidence |
|---|---|---|---|---|---|
| B1 | selector with TCP-only + dual member, currently dual, UDP proven to reach it | OK | **yes**, names the unselected member | switching to the TCP-only member fails that flow; dual keeps serving | `TestStartRefusesAGroupWhoseUnselectedMemberCannotCarryTheFlow` (renamed warning) |
| B2 | same selector, currently the TCP-only member | OK | **yes** | that flow fails | matrix START-01-G (warn) |
| B3 | urltest / loadbalance filter by network | OK, **no warning** | none | the group never hands UDP to a TCP-only member | START-01-B2, B3 accepted |
| B4 | nested groups, both orientations | OK | **yes**, attributed to the right root | unchanged | `TestOneRootWithTwoBrokenMembersIsReportedOnce` |
| B5 | UDP business flow over a TCP-only middle hop (UoT) | OK, no warning | none | legal conversion | START-01-D, D2 |
| B6 | one leaf reachable from two roots | OK | **yes**, one line per root | unchanged | `TestTwoIndependentBrokenRoutesAreBothReported` (both roots named, each once) |
| B7 | tags with emoji, a pipe character, spaces, CJK | OK | **yes**, tags intact | unchanged | `TestTheWarningCarriesTheReportedTagNames` (new) |

### C — structural errors must not be washed into warnings

All still `START ERR`, and each test asserts the error text rather than only the exit.

| id | case | result | evidence |
|---|---|---|---|
| C1 | selector member tag does not exist | ERR | matrix START-01-F1; `TestAFatalFindingWinsOverAWarningInTheSameConfiguration` (mixed) |
| C2 | outbound detour cycle / self-reference | ERR | `TestStartRefusesACycleInAnUnselectedBranch`, `lintOutbounds` |
| C3 | DNS transport ↔ outbound cross-kind cycle | ERR | `TestCrossKindDNSCycleIsRefusedAtStartup` |
| C3-control | acyclic cross-kind shape | OK | `TestCrossKindAcyclicConfigStillStarts` |
| C4 | duplicate outbound tags / outbound-endpoint collision | ERR, no panic | `TestTheTagClashRefusalIdentifiesBothObjects`, `TestAnEndpointTaggedDirectIsDiagnosedRatherThanCrashing` |
| C5 | `fast_path` on an ineligible group | ERR | `box_fast_path_group_policy_test.go` (untouched) |
| C6 | `destination_dns_ownership` on a type that cannot honour it | ERR | `TestStartRefusesDestinationDNSOwnershipOnAnIncapableType`; the local-direct exception still passes |
| C7 | undecodable schema / missing construction field | ERR at decode/construct | existing option tests, untouched |

### D — DNS and HTTP client consumers

| id | case | START | WARN | RUNTIME | evidence |
|---|---|---|---|---|---|
| D1 | DNS transport over an existing detour that cannot carry it | OK | **yes**, names the missing network | the query fails on the detour; no fallback is created | `TestADNSTransportDetourMustCarryTheQuery` (warned twins), `TestConsumerDemandContractIsProvableOnly` |
| D2 | `AllOf` / `AnyOf` / `Conditional` | unchanged | — | `Conditional` (truncation retry) is not a startup requirement | `dns/transport/requirement_test.go` |
| D3 | HTTP/3 client forced to a TCP-only detour | OK | **yes** | H3 fails, the fallback rule is untouched | `TestAnHTTPClientDetourMustCarryTheConfiguredTransports` warned twins |
| D4 | unused client with a bad detour vs a used one | OK + warn / ERR for a missing tag | **yes** | unused definitions do not block; a used one fails at request time | same file, both halves |
| D5 | `ResolveDialer` wrapping `DetourDialer` | unchanged | — | detour resolution is untouched | `common/dialer` detour tests |
| D6 | no DNS, direct-only, TCP-only, empty group | OK, **no warning** | none | unchanged | matrix accepted cases |

### E — the warning path itself

| id | attack | result | evidence |
|---|---|---|---|
| E1 | warning must not be a failed start | the outbounds are started, the scope cleanups run, `Close` is clean and idempotent, status is populated (not `disconnected`) | `TestAWarnedStartClosesCleanly` (new) |
| E2 | a fatal and a warning in one configuration | **FATAL wins**; the error names the missing member and does not carry the capability sentence; no warning is logged for that start | `TestAFatalFindingWinsOverAWarningInTheSameConfiguration` (new) |
| E3 | `log.disabled` (NOP logger) | start succeeds, no panic | `TestAWarningWithLoggingDisabledNeitherPanicsNorRefuses` (new) |
| E4 | flood / sensitive data | one aggregated warning per start, one line per defect (count-asserted); messages contain tags and networks only, no credentials | `TestTheReportedUDPCapabilityShapeStartsWithAWarning` |
| E5 | concurrency | `-race` on the root package, `common/physicalpath`, `common/httpclient`, `adapter/outbound`, `dns/transport`, `dns/transport/quic` | see §4 |
| E6 | upstream comparison | upstream `manager.go` at the recorded SHA has no dry run at all; the three representative shapes now start where the fork refused | `gh api` read of the upstream file, recorded in the construction report |

## 3. Defect found and fixed

**The warning claimed the start would succeed.** `Report.WarningErr()` read *"…the configuration starts
and those flows fail at run time…"*, but the manager emits it **before** `startOutbounds`, so a later
stage can still fail for its own reason. In exactly the case an operator is debugging, the log would
have promised a start the code had not performed.

Fixed in `common/physicalpath/dryrun.go`: the sentence is now *"the following outbound paths cannot
serve some flows; those flows fail at run time exactly as before"*. Covered indirectly by every
warning test (they assert the stable prefix and the per-defect lines) and by
`TestAFatalFindingWinsOverAWarningInTheSameConfiguration`, which proves that a fatal start logs no
warning at all.

No other bug was found. In particular, no production consumer re-escalates a warning: the only
production reader of the report is `adapter/outbound/manager.go`, which calls `Err()` (fatal-only).
`Report.Reachable()` still means "no findings at all" and is used by tests and by a dot-directory
scratch package that Go does not build.

## 4. What was run

| command | result |
|---|---|
| `go test . ./common/physicalpath ./common/httpclient ./adapter/outbound ./dns/transport ./dns/transport/quic` (release tags) | PASS |
| `go test -race . ./common/physicalpath ./common/httpclient ./adapter/outbound ./dns/transport ./dns/transport/quic` | PASS |
| `go build ./...` (release tags), untagged `go build`/`go vet` on the changed packages | PASS |
| `gofmt -l` on changed files, `git diff --check` | clean |
| CI on the pushed SHA | recorded in §6 |

`NOT_RUN`, and not claimed:

- runtime end-to-end through a live inbound (SOCKS/UDP associate) against a TCP-only outbound. The
  runtime capability path is untouched by this round — no file under `protocol/`, `route/` or
  `common/dialer` changed — and `git diff` is the evidence; a live harness is the next step if a
  device is available.
- slow-log-writer and reload-storm behaviour.
- parallel `Start`/`Close` under `-race` beyond the existing lifecycle tests.

## 5. Fatal protections confirmed still fatal

Missing member · outbound dependency cycle · DNS ↔ outbound cross-kind cycle · duplicate tag ·
outbound/endpoint identity collision · `fast_path` misuse · `destination_dns_ownership` that cannot
be honoured · undecodable schema. Each has a test that asserts the error text, and none of them was
converted by the construction round.

## 6. CI and limits

The pushed SHA's Verify run is linked from the final answer; `NOT_RUN` items above are the only
coverage gap. The residual risk is unchanged from the construction report: a configuration that
starts with a warning keeps one flow that will fail, which is the intended trade-off, and no switch
turns the warning back into a refusal.
