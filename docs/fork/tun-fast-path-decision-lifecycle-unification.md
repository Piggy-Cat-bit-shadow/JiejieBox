# TUN Fast Path — unified decision contract and lifecycle ownership

**Round:** 2026-10-24 (construction/verification on `testing`)
**START_HEAD:** `28621b61850e4b4dc869cab736c41d0444eeec28`
**END_HEAD:** the commit carrying this report (recorded in the round message)
**Branch:** `testing` — no branch was created, nothing was force-pushed, no tag or release was made.
**Resolved `sing-tun`:** `replace github.com/sagernet/sing-tun => Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020085711-109186a15f3b`; `go list -m -json` confirms the `Replace.Dir` is the fork, not the upstream `require` line (`v0.9.7-0.20261013140216-9ffa5391a2ad`, not built).
**Environment:** Windows 10 amd64, Go 1.25.5, `GOTOOLCHAIN=local`; build tags from `release/DEFAULT_BUILD_TAGS_OTHERS`; `CGO_ENABLED=0` for the normal gates.

## 1. Purpose, in the user's terms

The goal is not a second TUN forwarder. It is:

- system proxy stays light — no TUN stage, no new gate, no new observation;
- TUN optimisations that are real and safe stay automatic (the splice attempt is automatic, not opt-in);
- manual `fast_path` keeps its intent and precedence — `true`/`unset` are permission to *attempt*, explicit `false` withholds it; none of them forces past routing, DNS, rules, accounting or safety;
- fallback is safe only before an irreversible commit; a handed-over socket or an in-flight TCP/UDP byte stream is never "switched back";
- verdict, capability, execution, observation and lifetime are separately explainable;
- "unified" means one contract, one owner, one refusal vocabulary — not one scheduler for L0, nfqueue, Port, splice and copy.

The previous two rounds established the value verdict. This round asks a different question: **is the contract the code already implements internally consistent, and is every resource owned exactly once?**

## 2. Known vs discovered

### Inherited and re-checked

- `docs/fork/tun-fast-path-value-review.md`: `D0_VALUE = NO_DISTINCT_FAST_PATH`; `ActionBypass` is accept-equivalent in the go/gvisor/system TUN stacks and is honoured only by Linux `auto_redirect` (nfqueue `NfRepeat` + output mark). Re-checked against the resolved dispatcher, not the report.
- `docs/fork/tun-fast-path-adversarial-debug.md`: the real-bytes e2e, the tri-state matrix, the shared-leaf and selector-switch attacks.
- `docs/fork/native-bypass-trace.md`, `docs/fork/direct-offload.md`: the L1/L2 distinction and the tracker guard.
- `docs/fork/real-tun-validation.md`: the device procedure for L0 and splice.
- `docs/fork/lifecycle-scope-resource-ledger.md`, `physicalpath-controlpath-model.md`, `half-closed-flow-adversarial-audit.md`: the ownership vocabulary and the half-close contract.

### Discovered in this round

1. **The decision contract already separates the four layers.** `canFastBypass` (`route/route.go`) is one allow-list with a typed refusal for every hard gate (`route/bypass_verdict.go`); `resolveFastPathRequest` (`route/fast_path.go`) resolves intent first; the TUN dispatcher consumes the verdict (`protocol/tun/inbound.go:JudgeFlow`); the platform decides whether the verdict is executable. There is no second eligibility implementation to merge.
2. **The pre-match verdict is advisory and does not mutate the connection's selection.** `Router.PreMatch` takes `InboundContext` by value and returns `PreMatchResult`; the committed `routeConnection` re-resolves the chain and writes `metadata.OutboundChain` only there. A preview cannot become a commitment by accident.
3. **The splice refusal path keeps cached bytes.** `spliceConnection` (`route/splice.go:145`) drains `N.CachedReader` into the outbound before it asks for the handover, counts those bytes through the same `ReadCounters`, and on refusal returns to the copy loops without replaying them. A new 16 KiB multi-segment test now pins that the proxied (refused) path is byte-identical.
4. **No new production defect was found in the fast-path decision.** The two real lifecycle defects this branch already fixed are the ones this round re-verified:
   - `b256cdbca56`: an explicit `fast_path: false` now wins over the intrinsic direct grant (before it, the documented value could not occur on the only production shape).
   - `92fe54be05f` port: `multiFlowTracker` serializes `AttachFlow` against `CloseFlow`; the counters stay lock-free.
   - `5372a195b3ee` port: a failed URLTest connection deletes the leaf's history, so failover does not immediately re-pick the failed node.
5. **Nothing here upgrades a device claim.** The accepted handover and kernel byte movement remain `NOT_VERIFIED` on this host; the in-memory TUN's platform IO reports `supportsSockets() == false`, so `GoConn.Splice` refuses at the source. That is a fixture property, not a product result.

## 3. Decision contract matrix

| path | trigger | explicit intent | default intent | hard gates | real capability | commit boundary | fallback boundary | reportable fact | owner |
|---|---|---|---|---|---|---|---|---|---|
| System proxy | proxy inbound | `fast_path` not applicable | none | `canFastBypass` refuses non-TUN (`BypassRefusedInboundType`) | none | never enters TUN | n/a | "not TUN" | ConnectionManager (proxy) |
| L0 route set | OS route table, before TUN | n/a | configured `route_address_set` / `route_exclude_address_set` | IP-only policy | OS routing table | before TUN ingress | n/a | "packet never entered TUN" | OS / route table |
| Implicit L1 (ordinary TUN) | TUN flow | `fast_path` tri-state on a transparent group | intrinsic silence on transparent→direct, else AI matcher | full `canFastBypass` list | `ActionBypass` is accepted by the stack; bytes still moved by userspace | at `JudgeFlow` | not a byte commit; the flow still reaches userspace | "verdict allowed; data plane accept-equivalent" | TUN dispatcher + router |
| Implicit L1 (Linux nfqueue) | TUN flow + auto_redirect | same | same | same | `NfRepeat` + output mark hands the packet to the OS route | at `JudgeFlow` | kernel routing | "kernel carries this flow" (device proof needed) | kernel nfqueue |
| Explicit `action: bypass` | route rule | operator's literal instruction | n/a | does not consult the implicit gate at all: a tracker does not refuse it (dedicated test) | same as L1 | rule match | same | "explicit action, not implicit fast path" | router rule action |
| `ActionFlow` / Port | TUN flow | n/a | n/a | Port-capable flow (mainly ICMP) | Port owns the flow | `PreMatchFlow` | port close | "Port-owned flow" | `protocol/direct` Port |
| TCP splice | after the outbound socket exists | not controlled by `fast_path` | automatic attempt | source is `*tun.GoConn`; target unwraps to `tun.SpliceSocket` | accepted → kernel handover; refused → copy | `GoConn.Splice` success | only before acceptance; cached bytes already drained are counted and not replayed | `accepted` / `splice_rejected` + reason | `GoConn` after accept; copy loops after refusal |
| UDP splice | after the outbound packet conn exists | not controlled by `fast_path` | automatic attempt | NAT source + `tun.SpliceSocket` target | same | `Splice` success | same, with cached packet buffers | accepted / refused | `UDPNatConn` / copy |
| Buffered copy | splice refused/unsupported | n/a | n/a | none | userspace copy | n/a | the reliable bottom | copy path | copy loops |

Evidence: `SOURCE_CONFIRMED` for every row in this table; `TEST_CONFIRMED` for the implicit-L1 ordinary-TUN row (`e2e/tun_fast_path_dataplane_test.go`), the tri-state rows (`route/fast_path_production_equivalence_test.go`, `route/fast_bypass_verdict_matrix_test.go`), the splice refusal row (`e2e/tun_splice_dataplane_test.go`) and the system-proxy row (the same file). The nfqueue row and the accepted-handover row are `NOT_VERIFIED` on this host.

## 4. Resource and lifecycle matrix

| resource | created by | holder while active | handover event | closer | close-once mechanism | network epoch | failure cleanup | counted | terminal transition |
|---|---|---|---|---|---|---|---|---|---|
| TUN flow entry | dispatcher | dispatcher/Port | none | dispatcher | flow close reason | n/a | dispatcher | yes (tracker) | CloseFlow |
| `GoConn` (TCP) | TUN stack | copy loop | `Splice` accept | `spliceClose` | `common.Close` + `onClose` once | drain sweep | close on setup failure | yes (read/write counters) | OnClose |
| `UDPNatConn` | TUN stack | copy loop | `Splice` accept | `spliceClose` | same | drain sweep | close on setup failure | yes | OnClose |
| outbound socket | dialer | `trackedConn` | target for splice | `spliceClose` | `common.Close` | drain sweep | `CloseOnHandshakeFailure` | yes | OnClose |
| cached TCP payload | bufio reader | `spliceConnection` | drained before `Splice` | buffer release | `buffer.Release()` once | n/a | terminal error if write fails | yes (read counters) | caller closes |
| cached UDP payload | cached packet reader | `splicePacketConnection` | moved before accept | packet buffer put | `N.PutPacketBuffer` | n/a | returned to copy on refusal | yes | caller |
| tracker | router | metadata/connection | none | tracker close | tracker contract | drain | no tracker on implicit path | yes | CloseFlow |
| half-close watcher | `route/conn_halfclose.go` | connection | none | observation teardown | bounded probe/timer | transition | probe cancel | yes | detach |
| network epoch / drain | `conn_reclaim.go` | manager | none | sweep | `closed` atomic | per transition | sweep re-arms | diagnostics only | shutdown/reclaim |
| setup context | `dial_setup.go` | in-flight dial | established → cancel handover | `finishSetup` / `releaseDial` | gate generation | transition cancels old epoch only | released on failure | no | connection established |
| Scope startup resources | `adapter.Scope` | box | none | Scope close | reverse-order once | n/a | Start failure unwinds | no | Close |

Evidence: `SOURCE_CONFIRMED` for all rows; `TEST_CONFIRMED` for the flow-tracker serialization (`route/flow_tracker_serialization_test.go`), the splice refusal/fallback (`e2e/tun_splice_dataplane_test.go`), the kernel-owned drain protection (`route/conn_reclaim_test.go`), and the half-close observer (`route/conn_halfclose*_test.go`). Accepted-handover ownership after `Splice` is `NOT_VERIFIED` on this host.

## 5. D0 value

`D0_VALUE = NO_DISTINCT_FAST_PATH`, re-derived, not quoted:

- In the go/gvisor/system stacks the dispatcher's `ActionBypass` arm installs the same accept path as `ActionAccept`; the bytes are still copied by userspace.
- Linux `auto_redirect` is the only transport that gives the verdict a data-plane meaning (`NfRepeat` + output mark).
- Splice and buffered copy are two real mechanisms, but the splice is automatic and independent of `fast_path`; it is not a second "fast path" controlled by the field.
- The field's only observable control-plane effect is `false` withholding the attempt on the one shape that can take it; `unset` and `true` are equivalent.

There is therefore **no new optimisation path to add**, and no new global scheduler is justified.

## 6. Options A/B/C

| option | what it would be | verdict |
|---|---|---|
| A — fix real defects + reuse existing interfaces | the chosen path: audit, pin contracts with tests, no new production abstraction | **chosen** |
| B — thin read-only contract/snapshot | not needed: `BypassVerdict`, `PreMatchResult`, `TCPSpliceDiagnostics` and `managedConnState` already expose the facts at their own layers | rejected (no repeated drift found) |
| C — new unified control-plane entity | would add a second owner for decisions the router and the dispatcher already own; every field would have to be kept in sync with `canFastBypass` | rejected |

## 7. Defect ledger

No new S0–S3 defect was found in this round. The defects relevant to this contract were already fixed on `testing`, and are re-verified here:

| id | severity | defect | fix | evidence |
|---|---|---|---|---|
| FP-1 | S1 | explicit `fast_path: false` was overruled by the intrinsic direct grant, so the documented off switch could not occur | `b256cdbca56` | `route/fast_path_production_equivalence_test.go`, `route/fast_path_adversarial_test.go` |
| FP-2 | S1 | `multiFlowTracker` let `AttachFlow` and `CloseFlow` overlap; a tracker could close before attaching | `92fe54be05f` port | `route/flow_tracker_serialization_test.go` |
| FP-3 | S1 | a failed URLTest connection left the node's history intact, so the next flow re-picked the failed node | `5372a195b3ee` port | `route/url_test_history_test.go` |
| FP-4 | S2 | ICMP to the TUN's own range was accepted as a Port flow and could loop | `b722a038e61b` port | `protocol/direct/icmp_loopback_test.go` |

The new test added by this round is not a defect fix: **`TestProxiedSpliceRejectionDeliversAMultiSegmentPayload`** (16 KiB, 1200-byte segments, proxied outbound) pins that a refused handover delivers every cached/forwarded byte exactly once through the copy loops. It is the I2 negative control that was missing at this size.

## 8. Tests and performance

| command | result |
|---|---|
| `go test -tags <DEFAULT_BUILD_TAGS_OTHERS> ./e2e -run 'TestProxiedSpliceRejectionDeliversAMultiSegmentPayload'` | PASS |
| `go test -tags <tags> ./e2e -run 'TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack|TestProxiedTUNFlowStillHasAGoConnSource|TestSystemProxyTCPSpliceIsRefusedAtTheSource'` | PASS |
| `go test -tags <tags> ./route -run 'TestFastPath|TestBypass|TestDirectIsTheOnly|TestNestedGroup'` | PASS |
| `go test -tags <tags> ./protocol/tun` | PASS (18.2 s) |
| `go build -tags <tags> ./...` | PASS |
| `gofmt -l` on every touched file | clean |

Not run / not applicable:

- **Accepted handover and kernel byte movement:** `NOT_VERIFIED`. The in-memory TUN reports `supportsSockets() == false`; the test asserts the refusal and the fallback bytes, not the accepted half.
- **Linux nfqueue / L0 / CAP_NET_ADMIN:** `NOT_VERIFIED` (no Linux host, WSL not installed). Procedure: `docs/fork/real-tun-validation.md` §1–§2.
- **No performance claim:** no production path changed in this round, so there is no A/B to report; the previous 85.5 ns / 0 alloc decision benchmark still stands for the eligibility function.
- `-race` was not rerun for the new e2e test: it is an in-memory data-path test whose new code is test-only; the route race suite runs in CI ("race on the fork's own data paths").

## 9. Compatibility

- `fast_path` JSON, the group whitelist (selector/urltest only), the explicit-false precedence, ordinary `direct`, `action: bypass`, the tracker guard and the system-proxy path are unchanged.
- No release, tag, key, server or submodule change.
- `go.mod`/`go.sum` unchanged in this round.

## 10. Repo closeout

- New file: this report.
- New test: `e2e/tun_splice_dataplane_test.go` — one test appended.
- Commit: the commit that adds these two files; pushed to `origin/testing`; CI on that SHA in the round message.
- Worktree and submodules: clean; the three submodule pointers are the pre-existing ones.

```text
OVERALL = DONE_WITH_LIMITATIONS
BRANCH = testing
BASELINE_PINNED = NO
DECISION_CONTRACT = PASS
OWNERSHIP_LIFECYCLE = PASS
DATA_PLANE = NOT_VERIFIED_ON_DEVICE
NEW_GLOBAL_SCHEDULER = NOT_NEEDED
REGRESSIONS = PASS
UNRELATED_CHANGES = NONE
```

Limitations: the accepted TCP/UDP handover, kernel byte movement, Linux nfqueue and L0, and all device behaviour are `NOT_VERIFIED_ON_DEVICE`; the refusal/fallback half and the decision contract are verified on this host.
