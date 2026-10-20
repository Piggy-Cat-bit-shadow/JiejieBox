# Startup validation compatibility construction

**Date:** 2026-10-20 (Asia/Shanghai)
**Repository / branch:** `Piggy-Cat-bit-shadow/sing-box`, `testing` only. No branch, tag or release
was created; no workflow was dispatched; nothing was force-pushed.

| identity | value |
|---|---|
| `START_SHA` | `1cecadd9fe845b0c369743ee206600b76f0bf174` |
| upstream comparison baseline | `SagerNet/sing-box` `testing` = `dfb80349bf5779ec0a3f834c9ec2794345bff8f0` (read at construction time; no fetch/reset was performed against it) |
| `CONSTRUCTION_SHA` | `cd2316a704a199f8a1289836edfbf01cc2dc2b20` |

## 1. What the user hit, and what changed

The reported configuration refused to start with:

```
start outbound: unusable outbound path member(s):
  outbound/🐟 漏网之鱼 -> 🐟 漏网之鱼 -> 节点选择 -> 🎯 全球直连 -> 美国1|N|9929|1x倍率 -> hop #0 ...
  this outbound does not carry udp (it carries tcp) and cannot serve the udp flow routed through it
```

No UDP connection had failed. The fork's reachable-leaf dry run statically inferred that a UDP flow
*reaches* a TCP-only node, and refused the whole start — including every TCP flow the configuration
could serve. It now starts, and the same finding is emitted once at warn level through the normal
logger:

```
WARN[0000] the following outbound paths cannot serve some flows; the configuration starts and those
flows fail at run time exactly as before:
  outbound/leak-fish -> leak-fish -> node-select -> global-direct -> us1-tcp-only -> hop #0 ...
  this outbound does not carry udp (it carries tcp) and cannot serve the udp flow routed through it
```

The runtime is untouched: a UDP flow that reaches `us1-tcp-only` fails exactly as before, because no
dialer, group, route rule or capability advertisement was changed. The warning changes the START
verdict, not the forwarding behaviour.

## 2. The inventoried checks

Severity was decided at the site that produces the finding (`Failure.Kind`), not by matching message
text. `Report.Err()` returns only the fatal part; `Report.Warnings()` the capability part.

| # | check | file / function | trigger | classification | why |
|---|---|---|---|---|---|
| 1 | exit does not carry a routed network | `common/physicalpath/dryrun.go` `validateNode` | a rule delivers a network to a path whose node advertises fewer | `FailureCapability` → **WARN** | the other flows still run; the failing flow fails at run time as before |
| 2 | outbound reports no network at all | same | node advertises `{}` | `FailureCapability` → **WARN** | an absent advertisement is not a proven inability |
| 3 | group member cannot carry the delivered network | same | unselected/inner member narrower than the flow | `FailureCapability` → **WARN** | the switch may never happen; refusing the start also refuses the flows the group serves |
| 4 | group filters by network and no member carries it | `dryrun.go` `ValidateRoots` (group summary) | every member narrower than the delivered network | `FailureCapability` → **WARN** | a flow to that group fails at run time; the rest of the configuration is unaffected |
| 5 | declared member does not exist | `validateNode` (`hop.Outbound == nil`) and `truncatedRouteFailure` | a group names a tag nothing declares | `FailureStructure` → **ERROR** | the configuration cannot be constructed as written |
| 6 | tag claimed by both an outbound and an endpoint | `validateNode` | identity collision | `FailureStructure` → **ERROR** | one of the two objects can never be reached |
| 7 | route never reaches its far end | `truncatedRouteFailure` | no node of the route is an exit | `FailureStructure` → **ERROR** | nothing in it was proven, and the shape is not a capability statement |
| 8 | `destination_dns_ownership` on a type that cannot honour it | `validateNode` (four sentences) | declaration vs object capability | `FailurePromise` → **ERROR** | an explicit privacy/behaviour promise that starting would silently break |
| 9 | HTTP client detour exists but cannot serve the configured transport | `common/httpclient/manager.go` `Start` | `CheckDetourCapability` on an existing detour | **WARN** (new) | the affected requests fail at run time; an unused client must not block the start |
| 10 | HTTP client detour names nothing | `manager.go` reference check | missing tag | **ERROR** (unchanged) | missing reference is structural |
| 11 | DNS transport detour exists but cannot carry its queries | `dns/transport/{https,tcp,tls,udp}.go`, `dns/transport/quic/{quic,http3}.go` | `CheckDetourCapability` on an existing detour | **WARN** (new) | the affected lookups fail at run time; other lookups are unaffected; no DNS path is changed or leaked |
| 12 | DNS transport detour names nothing | same files, reference check | missing tag | **ERROR** (unchanged) | structural |
| 13 | start-order sort: missing dependency / cycle | `adapter/outbound/manager.go` `lintOutbounds` | declared graph | **ERROR** (unchanged, and runs before the dry run) | the sort's message is the precise report for a declared edge |
| 14 | duplicate tag / nil object | `lintOutbounds` | namespace collision | **ERROR** (unchanged) | a tag resolves to one object |
| 15 | outbound ↔ DNS transport cross-kind cycle | `adapter/outbound/cross_kind_cycle.go` | proven cycle | **ERROR** (unchanged) | would deadlock at run time |
| 16 | `fast_path` on a group that is not an eligible transparent group | `box.go` policy | fork-only field misuse | **ERROR** (unchanged) | explicit fork-only promise |
| 17 | duplicate outbound/endpoint tags at construction | `box.go` | namespace collision | **ERROR** (unchanged) | structural |

**Not in this round, by rule:** new-protocol internals (MASQUE, NaiveProxy, Mieru), TLS/REALITY,
encryption/auth, transport stacks, `route.rules` evaluation at run time, and the packet destination
guard. None of them was read for compatibility changes, and none changed.

## 3. Files changed

| file | change |
|---|---|
| `common/physicalpath/dryrun.go` | `FailureKind` + `Kind` on `Failure`; kinds set at the five production sites; `Report.Fatal()/Warnings()/Err()/WarningErr()` split |
| `adapter/outbound/manager.go` | `validatePhysicalPaths` returns the report; `Start` refuses on fatal and logs capability findings once; `SetLogger` |
| `box.go` | injects `logFactory.NewLogger("outbound/manager")` |
| `common/httpclient/manager.go` | existing-detour capability mismatch → warn, missing reference still fatal |
| `dns/transport/{https,tcp,tls,udp}.go` | same rule at the four transport call sites |
| `dns/transport/quic/{quic,http3}.go` | same rule; `quic.Transport` gains a logger field set by its constructor |

No route rule, dialer, group selection, DNS path, capability advertisement or protocol implementation
was modified.

## 4. Tests

| suite | change |
|---|---|
| `box_static_validation_false_deny_test.go` | every positive twin now asserts **start succeeds + the warning names the reason**, keeping the detector provable |
| `box_physicalpath_*_test.go` (4 files) | capability refusals became warning assertions; diagnostic-wording assertions moved to the warning text |
| `box_physicalpath_networks_test.go` | matrix cases carry `warn: true`; fatal cases still assert the error; the two lifecycle tests now use a structural defect |
| `box_status_test.go` | the "status after a failed start" fixture uses a structural defect (capability no longer refuses) |
| `adapter/outbound/manager_physicalpath_test.go` | capability cases assert the logger's warning through the real factory; structural cases unchanged |
| `adapter/outbound/manager_reentrancy_test.go` | repeated-start stability now pinned on a structural defect |
| `box_static_validation_{dns_demand,consumer_demand,true_final}_test.go` | capability mismatches assert start + warning; a detour naming nothing still asserts refusal |
| **new** `box_startup_warning_capture_test.go` | real `box.New` + `Start` with `log.output` to a file, and the incident-shaped chain test |
| **new** `common/physicalpath/failure_kind_test.go` | the severity split itself, and that each production sentence is tagged at its source |

Commands and results (Windows, `-tags "$(release/DEFAULT_BUILD_TAGS_WINDOWS)"`, `CGO_ENABLED=0`):

| command | result |
|---|---|
| `go build ./...` | PASS |
| `go test . ./dns/... ./common/httpclient ./common/physicalpath ./adapter/outbound` | PASS (root 7.1 s; dns/transport 6.0 s; quic 0.1 s) |
| `go test -race . ./common/physicalpath ./common/httpclient ./adapter/outbound ./dns/transport ./dns/transport/quic` | PASS (root 8.5 s; no data races) |
| `gofmt -l` on the changed files | clean |
| `go vet` on the changed packages | clean |

Not run: Apple signing, device/WAN verification, real resolver behaviour. Those are `NOT_RUN`, not
PASS.

## 5. Residual risks

- A configuration that starts with a warning keeps a flow that will fail. That is the intended
  trade-off, and the warning names the flow; an operator who wants the old behaviour can read the
  warning, but there is no switch that turns the warning back into a refusal (deliberately: no new
  schema was added).
- The dry run's capability findings are still computed; only their verdict changed. The analysis,
  `UNKNOWN` handling and `Report` structure are untouched.
- A third-party outbound that lies about its `Network()` still produces a wrong verdict in either
  direction; that was already true and is out of scope.
- The DNS/HTTP warnings depend on the consumer having a logger; both production constructors provide
  one. A `nil` logger means "not reported", never "error".
- `dns/transport` `udp` keeps its existing rule that a UDP-only detour is accepted (the truncation
  retry is a capability gap rather than an unusable resolver); that was already the case and is
  unchanged.

## 6. Third-party note for the adversarial review

- The detector must still fire: every converted test asserts the warning text contains the root tag,
  the member/leaf and the missing network, and the fatal tests still assert the error.
- The incident shape is `TestTheReportedUDPCapabilityShapeStartsWithAWarning`.
- The severity split is data (`Failure.Kind`). `FailureStructure` is the ZERO value on purpose: a
  future check that reports a finding without classifying it is refused, not silently downgraded.
