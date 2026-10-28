# TUN fast path: adversarial debug and acceptance

Scope: the group `fast_path` policy, the Direct Fast Path verdict, its data-plane execution and
fallback, and the route/TUN pieces they touch. This is the second document the brief requires; the
value review ([tun-fast-path-value-review.md](tun-fast-path-value-review.md)) is the first.

```text
OVERALL = PASS_WITH_LIMITATIONS
SCOPE = TUN_FAST_PATH_ONLY
REPO_BRANCH = Piggy-Cat-bit-shadow/sing-box/testing
BASE_SHA = 87c43f8ad23f2b7849f1287f0ec00cde5b2d40b3
FINAL_SHA = (the commit this file is part of; recorded in the round report)
SING_TUN_RESOLVED_VERSION = v0.0.0-20261020085711-109186a15f3b
D0_VALUE_VERDICT = NO_DISTINCT_FAST_PATH
```

## 1. What was audited, and what was not

Audited, with executable evidence: the tri-state `fast_path` policy and its precedence; eligibility
and its exact refusal reasons and ordering; the verdict's path through the real router, the real Go
stack and real bytes; the system-proxy comparison; splice on/off as far as this environment can
observe it; TCP payload integrity; shared leaves, concurrent policies and selector switching;
config validation and round-trip; the explicit `action: bypass` rule action.

Not audited: protocol cryptography, the DNS resolver architecture, the UI/API, release pipelines,
other platforms' device behaviour. Linux nfqueue and L0 need a Linux host with CAP_NET_ADMIN and a
real TUN; they are NOT_VERIFIED here, not assumed.

## 2. Real path matrix

| Path | Control verdict | Actual owner | Bytes move by | Evidence |
| --- | --- | --- | --- | --- |
| System proxy | refused at `InboundType != tun` | userspace ConnectionManager | userspace copy; splice refused at the source | e2e `TestSystemProxyTCPSpliceIsRefusedAtTheSource` |
| Ordinary TUN (go stack, memory device) | `ActionAccept` / `ActionBypass` | `*tun.GoConn` | userspace copy; splice decision made and rejected by the platform IO | e2e `TestFastPathVerdictDoesNotChangeTheTUNDataPath` |
| Same, proxied | same | `*tun.GoConn` | userspace copy; source still classified as GoConn | e2e `TestProxiedTUNFlowStillHasAGoConnSource` |
| Linux nfqueue L1 | `NfRepeat` + output mark | kernel routing | kernel | source only, NOT_VERIFIED |
| L0 route sets | OS routes before the TUN | kernel routing table | kernel | source only, NOT_VERIFIED |
| Flow / `tun.Port` | `ActionFlow` + `Port` | Port implementation | Port writes | ICMP only in production |
| TCP/UDP splice | post-handshake decision | kernel after acceptance | splice/sendfile | accepted path NOT_VERIFIED here; refusal path covered |
| Buffered copy | fallback | userspace copy loop | userspace buffers | e2e payload integrity + route tests |

### The new hard limit found this round

The in-memory TUN cannot exercise an ACCEPTED TCP handover, and the reason is a fixture property,
not a product one: sing-tun's `goMemoryIO.supportsSockets()` returns false, and `GoConn.Splice`
refuses on that first check. Both a direct TUN flow and a proxied TUN flow therefore record
`splice_rejected` (the target walk succeeds because the dialer wraps every outbound conn in the
route's `trackedConn`, which is a `tun.SpliceSocket`). A system-proxy flow records
`source_not_go_conn`. The accepted branch and any kernel byte movement after it are
**NOT_VERIFIED**; a real device (Linux, Darwin, Windows Wintun) is required.

This corrects a sentence that could be read too strongly in the value review: splice is attempted
automatically for every TUN connection, but on this test harness it is always refused at the
platform check, and the fallback is what carries the bytes. The mechanism is real; its accepted half
is unproven here.

## 3. The principled answer

Ordinary TUN has no second byte path: `ActionBypass` is an accept entry in go/gvisor/system and is
honoured only by Linux nfqueue. The group field controls the ATTEMPT, not the data path: an explicit
`false` withholds the intrinsic grant on transparent-direct chains, `unset`/`true` are equivalent,
and no value can grant a bypass the shape forbids. The automatic optimisation that does exist is the
post-handshake splice attempt, and it is not gated by the field.

## 4. Behaviour table

| Input | Result |
| --- | --- |
| `direct` leaf alone | bypass; no group can carry the field |
| `selector`/`urltest`, nested, `-> direct`, unset or true | bypass |
| same, explicit false (on any carrier, outermost wins) | attempt withheld, normal direct connection |
| `selector -> loadbalance -> direct` | refused by the chain gate for every value |
| proxy leaf | refused before any policy is read |
| AI-looking tag, no explicit policy | authorises only shapes that are not the intrinsic one; those chains are then refused by the gate, so no production verdict changes |
| same leaf through an authorised and a refusing group | per-flow: the authorised flow bypasses, the refusing one does not, concurrently and in either order |
| selector switch direct -> proxy -> direct | the refusal survives the switch; an unset group bypasses on direct and is refused on proxy |

## 5. Bug and finding ledger

| ID | Severity | Status | Evidence |
| --- | --- | --- | --- |
| SPLICE-ON-HALF | environment limit, not a defect | NOT_VERIFIED | `supportsSockets()==false` on the memory IO; both TUN shapes record `splice_rejected`, bytes still delivered |
| EXPLICIT-BYPASS-ACTION | design asymmetry, documented | ACCEPTED | `action: bypass` bypasses the fast-path gate by design (`TestAnExplicitBypassRuleIgnoresTheFastPathGate`); in nfqueue mode those flows are an explicit operator choice to leave userspace |
| ROUTE-RULE-FLAKE | S3, pre-existing, unrelated | FIXED | the updater fetch loop is sequential, so at most ONE request may complete after `Close`; the assertion now tolerates that one and still fails a loop that outlives the close (it would add ~30 hits in the 150ms window). 20x normal, 20x `-race`, and the whole package 3x under `-race` pass |
| TUN-VERIFY-01 | S2, fixed earlier in this campaign | FIXED | pinned-source helper now resolves the replaced module |
| TUN-POLICY-01 | S2, fixed earlier in this campaign | FIXED | explicit false now withholds the intrinsic grant |

No S0 or S1 was found in this round.

### Mutation controls

The brief asks for deliberate inversions. Two are real controls rather than source patches, which is
stronger: the verdict is flipped by the product's own decision path in the e2e case matrix
(`fast_path: false` and an attached tracker both produce `ActionAccept` with byte-identical
delivery), and the splice OFF path is produced by the platform refusal and by a proxied target. The
dispatcher and stack traces compare `ActionBypass` against `ActionAccept` directly. No source
mutation was left in the tree; the only code change is the production precedence fix.

## 6. Fallback, TCP and UDP

- A withheld attempt returns `PreMatchContinue` and the flow takes the normal direct connection; no
  reject, no drop, no redirect (`TestExplicitFastPathFalseSuppressesTheDirectChain`, e2e `false`).
- A refused splice falls back to the copy loops with byte integrity in both directions
  (`TestTCPSpliceDiagnostics_FallbackKeepsCountersAndOwnership`), and the product-level attempt is
  visible in the diagnostics with the exact reason.
- An 8 KiB multi-segment TCP payload through a bypassed flow is byte-identical and ACKed segment by
  segment (`TestFastPathMultiSegmentPayloadIsIntact`); a stateful collector was required because the
  existing helper returns the first segment each call and would have "proved" a one-segment transfer.
- UDP: a bypassed datagram returns through the userspace NAT (`TestBypassVerdictStillDeliversUDP`);
  the route UDP NAT matrix and half-close suites are unchanged and pass.
- Close/teardown: no production change to teardown in this round; the existing half-close and
  liveness suites pass, and the e2e fixture fails if a handler delegation outlives the stack.

## 7. Performance

No new A/B claim is made. The decision costs ~85 ns and zero allocations per flow; the verdict is
discarded on ordinary TUN, so there is no throughput delta to measure there. The splice A/B remains
device-level NOT_VERIFIED; the local loopback numbers are historical and inconclusive by
construction. See the value review §5.2.

## 8. Linux L0 and nfqueue

Kept separate from everything above. Both are source-verified and device-unverified. The L0 verdict
is an IP-only pre-router decision; the nfqueue verdict is the only one that leaves userspace. Do not
add their effects to any ordinary-TUN number.

## 9. Tests run

| Gate | Result |
| --- | --- |
| full `./route ./protocol/tun ./option ./e2e` | PASS |
| new eligibility/order/adversarial route tests, `-count=10` | PASS |
| new e2e controls, `-count=3` | PASS |
| new route and e2e controls under `-race -count=2` | PASS |
| box-level configuration tests (`go test ./ -run TestFastPath`) | PASS: `fast_path` refused on a leaf and on loadbalance, accepted on selector/urltest, ordinary routing unchanged |
| `go build`, `go vet`, untagged test-binary compile, `gofmt` | PASS / clean |
| full `-race` sweep of `./route/... ./protocol/tun/... ./option/...` | PASS (route 451.1s, route/rule 11.4s, protocol/tun 5.4s, option 1.2s) |
| CI `Verify` on the pushed SHA | PASS (run id in the round report) |
| `Mobile contract verification` on the same SHA | PASS on re-run. The first attempt hit the documented `transport/http` flake, `TestActiveInboundStopsBecauseThePeerIsStarved` (3 s floor, measured 2.973 s) - its third recorded occurrence, in a package this work does not touch |

The mobile-contract failure is the pre-existing runner-scheduling flake recorded in
[half-closed-flow-release.md](../../../docs/fork/half-closed-flow-release.md): the assertion measures inbound arrival
times against a 3 s floor and a loaded runner landed 27 ms under it. The job passed on re-run, as it
did for both earlier occurrences, and the assertion was not relaxed for this work. Recorded here
rather than silently dropped.

The `route/rule` flake that the earlier full sweep hit is fixed in this round: the updater's fetch
loop is sequential, so at most the one request already in flight when `Close` returns may complete;
the assertion now bounds that and still fails a loop that outlives the close (which would add ~30
hits in the window). The full sweep above is the gate that used to fail, now green.

## 10. Unverified limits

Linux nfqueue, L0 route installation, accepted TCP/UDP handovers and kernel movement, real NIC
throughput/CPU, macOS/iOS/Android/Windows device behaviour, the Android platform auto-redirect
implementation. Each needs hardware or privileges this environment does not have. The procedure for
the first three already exists in [real-tun-validation.md](../../../docs/fork/real-tun-validation.md) §1 (L0) and §2
(splice), including what would falsify each claim; what is missing is the host, not the plan.

## 11. Gap to the original goal, and the smallest next step

The value question is answered: there is no distinct fast data plane to build for ordinary TUN, and
the field now has an honest off switch. The remaining gap is evidence, not code: one Linux host with
CAP_NET_ADMIN for nfqueue/L0, and one real device for an accepted handover and a power measurement.
Until then the mechanism stays labelled as it is.

## 12. Reproduce

```powershell
$env:GOROOT='C:\Deepseek\_gotool\go'; $env:GOPATH='C:\Deepseek\_gotool\gopath'
$env:PATH="$env:GOROOT\bin;$env:PATH"; $env:GOTOOLCHAIN='local'
cd C:\Deepseek\integration-release-next
$tags = (Get-Content .\release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()

go test -count=10 -tags $tags ./route -run 'TestEveryBypassConditionRefuses|TestBypassChainAndLeafConditions|TestTheBypassConditionsAreOrdered|TestSharedDirectLeaf|TestFastPathFalseSurvivesASelectorSwitch|TestAnExplicitBypassRuleIgnores'
go test -count=3 -tags $tags ./e2e -run 'TestDirectTUNFlowTakesTheSplice|TestProxiedTUNFlowStill|TestSystemProxyTCPSplice|TestFastPathMultiSegment'
go test -count=1 -tags $tags ./route ./protocol/tun ./option ./e2e
```

`-race` needs a cgo toolchain on this host (`CGO_ENABLED=1 CC=<mingw gcc>`); the CI runner supplies
its own.

## Appendix: finding records

```text
BUG_ID: SPLICE-ON-HALF
SEVERITY: S3 (environment limit, not a product defect)
SCOPE: data plane / observation
CURRENT_BASE_SHA: 87c43f8ad23f2b7849f1287f0ec00cde5b2d40b3
SING_TUN_RESOLVED_COMMIT: 109186a15f3b2b57df7e8634818873ab5b927c46
REPRO_COMMAND: go test -count=1 -tags <others> ./e2e -run TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack
TEST_NAME: TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack, TestProxiedTUNFlowStillHasAGoConnSource, TestSystemProxyTCPSpliceIsRefusedAtTheSource
FIXTURE / CONFIG / NETWORK / PLATFORM: in-memory TUN, real Go stack, real loopback peer, Windows/amd64
EXPECTED_BEHAVIOUR: the decision is made for every TUN TCP flow; on this fixture the platform refuses it and the copy loops carry the bytes
OBSERVED_BEHAVIOUR: Attempts=1, Accepted=0, Reasons={splice_rejected:1}, payload delivered
ACTUAL_BYTES_ROUTE: userspace copy loops (kernel splice not reached)
NEGATIVE_CONTROL: system proxy -> Reasons={source_not_go_conn:1}; proxied TUN -> no source_not_go_conn
ROOT_CAUSE: sing-tun stack_go_io_memory.go supportsSockets() == false; GoConn.Splice returns false at its first check
WHY_EXISTING_TESTS_MISSED_IT: the accepted path was only unit-tested against fakes; no test read the manager's diagnostics from a product-level flow
MINIMAL_FIX: none possible in this environment; a real device IO is required, and the acceptance branch is correctly refused rather than faked
BEFORE_FAIL / AFTER_PASS: the new tests pass by documenting the refusal; they fail if the fixture gains socket support without the expectation being revisited
REGRESSION_AND_RACE_RESULT: -count=3 and -race -count=2 pass
COMPATIBILITY_RISK: none (no production change)
PERFORMANCE_AB_IF_RELEVANT: none claimed
STATUS: ENVIRONMENT_BLOCKED (accepted half), VERIFIED (refusal and fallback halves)
```

```text
BUG_ID: EXPLICIT-BYPASS-ACTION
SEVERITY: S3 (design asymmetry, documented rather than changed)
SCOPE: policy / data plane (Linux redirect mode only)
CURRENT_BASE_SHA: 87c43f8ad23f2b7849f1287f0ec00cde5b2d40b3
SING_TUN_RESOLVED_COMMIT: 109186a15f3b2b57df7e8634818873ab5b927c46
REPRO_COMMAND: go test -count=1 -tags <others> ./route -run TestAnExplicitBypassRuleIgnoresTheFastPathGate
TEST_NAME: TestAnExplicitBypassRuleIgnoresTheFastPathGate
FIXTURE / CONFIG / NETWORK / PLATFORM: real Router rules, no device
EXPECTED_BEHAVIOUR: an explicit `action: bypass` rule is an operator instruction and returns the bypass verdict regardless of the fast-path gate
OBSERVED_BEHAVIOUR: PreMatchBypass with a tracker attached; the implicit direct path is refused by the same tracker
ACTUAL_BYTES_ROUTE: ordinary TUN - unchanged (the verdict is an accept entry); Linux redirect - the flow leaves userspace by explicit choice
NEGATIVE_CONTROL: implicit direct flow -> BypassRefusedTracker
ROOT_CAUSE: route/route.go RuleActionBypass returns PreMatchBypass without calling canFastBypass
WHY_EXISTING_TESTS_MISSED_IT: the action was tested for its no-Port shape, not for its relationship to the eligibility gate
MINIMAL_FIX: none - the action's contract is "let the platform carry this"; changing it would be a separate product decision
BEFORE_FAIL / AFTER_PASS: new test pins both sides
REGRESSION_AND_RACE_RESULT: -count=10 and -race pass
COMPATIBILITY_RISK: none (documented, unchanged)
PERFORMANCE_AB_IF_RELEVANT: none
STATUS: VERIFIED_NO_BUG / DESIGN_RECORDED
```

## 13. Repository state

Worktree clean at the time of writing; the commits and the final SHA are recorded in the round
report. No submodule, dependency or release change was made. The `sing-tun` fork pin is unchanged.
