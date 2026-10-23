# Composite Fast Path — final closeout (2026-10-24)

**SCOPE:** `COMPOSITE_FAST_PATH_ONLY`. This is not a whole-kernel optimisation: it covers the
decision and execution of a shorter data path for a flow whose outbound has already been selected.
**BRANCH:** `testing`. **BASELINE_PINNED = NO** — the round started from the live tip, no SHA is a
checkout/reset target.

## 1. What is and is not "Fast Path" here

A Fast Path is: for a flow already routed to an outbound, identify the composite conditions (ingress,
network, routing result, both endpoints' executable capability, platform, non-skippable semantics,
ownership) and, **only when a semantically equivalent shorter execution path really exists**, use it
automatically; otherwise continue on the normal path. It is not `fast_path: true` (that is only the
group-intent switch for the implicit Direct Bypass attempt), not "all kernel optimisations", and not
"replace the proxy with direct".

## 2. Composite condition matrix

| ingress | network | outbound chain | executable shorter path found | decision | evidence |
|---|---|---|---|---|---|
| system proxy | TCP/UDP | direct | no TUN handover: source is not a `tun.GoConn`; normal proxy/ConnectionManager path stays | `NO_EXECUTABLE_SHORTCUT` (TUN-specific) | `e2e/tun_splice_dataplane_test.go::TestSystemProxyTCPSpliceIsRefusedAtTheSource` |
| system proxy | TCP/UDP | proxy | no TUN stage at all; normal protocol path | `NO_EXECUTABLE_SHORTCUT` | same; `canFastBypass` refuses non-TUN ingress (`BypassRefusedInboundType`) |
| TUN | TCP | direct | immediate `ActionBypass` is accept-equivalent; after the socket exists, splice is attempted automatically | control: `ELIGIBLE`; execution: `splice_rejected` on memory TUN, Copy fallback | `e2e/tun_fast_path_dataplane_test.go`, `e2e/tun_splice_dataplane_test.go` |
| TUN | UDP | direct | splice attempted after the NAT socket exists; otherwise UDP NAT copy | `ELIGIBLE`; accepted not verified here | `TestBypassVerdictStillDeliversUDP`, splice packet tests |
| TUN | TCP | transparent selector/urltest → direct | same as TUN→direct; `fast_path` tri-state only gates the implicit bypass attempt, not splice | `POLICY_ALLOWED` + `ELIGIBLE`; `TEST_CONFIRMED` that splice is independent | `TestSpliceAttemptIsIndependentOfFastPath` |
| TUN | TCP/UDP | proxy (VLESS/MASQUE/Mieru/socks) | no native direct bypass (outbound is not `direct`); splice only if the *target* really exposes a socket and the reader/writer are replaceable | `NO_EXECUTABLE_SHORTCUT` for bypass; splice is best-effort and fails safely for encrypted wrappers | `TestProxiedTUNFlowStillHasAGoConnSource`, `unwrapSpliceTargetWithReason` tests |
| TUN | any | loadbalance → direct | opaque group owns retry/accounting; not eligible | `NO_EXECUTABLE_SHORTCUT` | `TestDirectFastPathOpaqueGroupStillRefusesUnsetPolicy` |
| L0 route set | any | any | packet never enters TUN | OS-owned, outside this decision | source-confirmed in `JudgeFlow` |
| Linux nfqueue | any | direct | `NfRepeat` + mark is a real kernel bypass | `EXECUTABLE` (source); device not required by this task | resolved dispatcher |

## 3. TUN → proxy: the independent conclusion

A proxy outbound cannot be "accelerated" by pretending it is direct: the protocol handshake, TLS or
QUIC layer, and the user's chain must stay. The only shorter segment that could exist is the
post-handshake socket handover. `spliceConnection` accepts it only when the source is a
`*tun.GoConn`, the target unwraps to a `tun.SpliceSocket`, and reader/writer are the same owner.
`trackedConn` implements `SpliceSocket`, but its `SyscallConn` delegates only when the wrapped
connection itself is a `syscall.Conn`; an encrypted protocol wrapper is not, so the handover fails
safely instead of splicing plaintext into a TLS stream. No production gap was found here.

## 4. Fact levels reached

| claim | level |
|---|---|
| implicit `ActionBypass` is accept-equivalent on ordinary TUN | `BYTES_PROVEN` (e2e same handler/bytes) |
| splice is attempted independently of `fast_path` | `TEST_CONFIRMED` |
| splice refusal falls back to Copy with intact cached bytes | `BYTES_PROVEN` (16 KiB / 1200-byte segments) |
| cached-write failure is terminal and does not consume the next buffer | `TEST_CONFIRMED` (F11, third buffer) |
| accepted handover → kernel bytes | `NOT_EXECUTABLE` on this host (`supportsSockets()==false`) |
| Linux nfqueue/mark, L0 route installation | `SOURCE_CONFIRMED`, device not required by this task |

## 5. F01–F15 coverage

| ID | status |
|---|---|
| F01 | PASS — `TestSpliceAttemptIsIndependentOfFastPath` |
| F02 | PASS — nested precedence + shared-leaf tests |
| F03 | PASS — opaque chain and proxy leaf tests |
| F04 | PASS — e2e same bytes under `ActionAccept`/`ActionBypass` |
| F05 | SOURCE_CONFIRMED (no device required) |
| F06 | PASS — system-proxy source refusal; no TUN handover |
| F07 | PASS — direct TUN splice attempted, refusal → Copy |
| F08 | PASS — proxy TUN has a `GoConn` source, no direct bypass; splice refuses safely |
| F09 | PASS — eligibility matrix (FakeIP/sniff/rewrite/TLS/tracker/bind/mark) |
| F10 | PASS — 16 KiB multi-segment refused handover, byte-identical |
| F11 | PASS — third buffer never written after the failing one |
| F12 | SOURCE_CONFIRMED + packet-path tests; no new device claim |
| F13 | NOT_EXECUTABLE on memory TUN; simulated acceptance is not claimed |
| F14 | PASS — setup generation/reclaim/half-close tests |
| F15 | PASS — every refusal keeps the original route/connection |

## 6. Changes in this round

1. **Documentation correction** in `tun-fast-path-decision-lifecycle-unification.md`: the explicit
   `action: bypass` row wrongly said it "still refuses on a tracker". The production code does not
   consult `canFastBypass` for that action, and `TestAnExplicitBypassRuleIgnoresTheFastPathGate` pins
   exactly that asymmetry. The code was not changed; the document now matches it.
2. **F11 test strengthened**: `TestCachedDrainStopsAtTheFirstWriteFailure` now has a third buffer, so
   it proves the loop stops after the failing write instead of merely stopping because no buffer was
   left.

No production code changed; no new scheduler, gate or state machine.

## 7. Result

```text
SCOPE = COMPOSITE_FAST_PATH_ONLY
BRANCH = testing
BASELINE_PINNED = NO
DECISION_COVERAGE = PASS
EXECUTABLE_SHORTCUT_AUDIT = NO_EXECUTABLE_SHORTCUT
TUN_DIRECT = SPLICE_ATTEMPTED / COPY_FALLBACK (accepted handover not executable here)
TUN_PROXY = NO_NATIVE_BYPASS / SAFE_SPLICE_REFUSAL
SYSTEM_PROXY = LIGHT_PATH_UNCHANGED
FALLBACK_AND_OWNERSHIP = PASS
PRODUCTION_FIXES = 0 (no production gap found; documentation corrected)
TEST_ADDITIONS = 1 strengthened (third buffer)
REGRESSIONS = PASS
REAL_DEVICE_TEST_REQUIRED_FOR_THIS_TASK = NO
UNRELATED_CHANGES = NONE
FINAL_SHA = the commit carrying this report
CI_VERIFY = <run in the round message>
CI_ANDROID = NOT_TRIGGERED (docs/test-only)
CI_MOBILE = NOT_TRIGGERED (docs/test-only)
OVERALL = DONE
```
