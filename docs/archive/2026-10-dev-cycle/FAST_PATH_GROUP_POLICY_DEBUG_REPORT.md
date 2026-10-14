# FAST_PATH_GROUP_POLICY_DEBUG_REPORT

## BASELINE

- implementation base: `5d562d605cd10bd266c0f45502c860ca0684df49` (the commit the fast_path work was
  built on)
- debug starting SHA: `929790b90ddf9ca8bf24dd226746a44e30225091`
- final SHA: `c2660b3fd` (pushed to `origin/testing`)
- worktree: only the two pre-existing submodule states (` M clients/apple`, ` m clients/desktop`),
  which this pass did not touch. No untracked files, no stray build output, `go.sum` reverted.

## SCOPE

**Inspected (all read for this pass, production and test):**

| file | why |
|---|---|
| `route/fast_path.go` | the whole policy mechanism |
| `route/route.go` | `canFastBypass` + its single call site, line by line |
| `route/router.go` | the policy field and setter |
| `box.go` | load-time validation and policy collection |
| `option/outbound.go`, `option/fast_path.go`, `option/schema.go` | the field, its three states, the schema |
| `adapter/outbound.go`, `adapter/experimental.go` | `BypassableOutbound` vs the new capability |
| `protocol/group/selector.go`, `protocol/group/urltest.go` | the two carriers |
| `daemon/started_service.go` | whether a reload can mutate a live Router |
| all 8 modified/added test files | for tests that pass vacuously |

**Changed:** `route/fast_path_debug_test.go` (new, 374 lines). **No production file was changed.**

**Explicitly untouched:** MASQUE, Naive, Apple/Android/Windows UI, release pipeline, docs knowledge
base, L0, DNS, FakeIP, traffic scheduler, splice, unrelated route rules, unrelated flaky tests,
lint/style.

## CONTROL FLOW VERIFIED

Traced end to end from the real call graph, not from comments:

```text
option.Outbound.FastPath (*FastPathPolicy)          option/outbound.go:61
  ↓ box.go:786-797   group-only validation, refuses the key on anything that is not a carrier
option.FastPathPolicy.Enabled                       option/fast_path.go
  ↓ box.go:834-845   collected into route.FastPathPolicies keyed by GROUP tag
route.Router.fastPathPolicies                       route/router.go:68
  ↓ route/route.go:960
chain, err := resolveOutbound(outbound, metadata, network, false)      route/route.go:910
  ↓
fastPathAuthorized := resolveFastPathRequest(chain, r.fastPathPolicies)  route/route.go:960
  ↓  (a function argument on the stack - this is the entire propagation mechanism)
r.canFastBypass(metadata, packetDestination, chain, outbound, fastPathAuthorized)  route/route.go:961
  ↓  chain branch: route/route.go:898-902
  ↓  leaf capability: route/route.go:908-914  (unchanged)
BypassVerdict
  ↓  .BypassAllowed() at route/route.go:961
PreMatchResult{Action: PreMatchBypass} → adapter/tun FlowVerdict{Action: ActionBypass}
   or
PreMatchContinue → the existing userspace path
```

`canFastBypass` has exactly **one** non-test caller (`route/route.go:961`). `resolveFastPathRequest`
has exactly **one** caller (the same line). There is no second entry point.

## TRUTH TABLE

Asserted as a table (`TestFastPathTruthTable`), with the tag as a real dimension. The ACTION is
asserted, not only the authorisation, so a case that authorises and then fails to reach the bypass
fails here.

| explicit | tag | leaf capable | metadata | auth | action | observed |
|---|---|---|---|---|---|---|
| true | ordinary | yes | eligible | true | bypass | PASS |
| true | AI-like | yes | eligible | true | bypass | PASS |
| false | ordinary | yes | eligible | false | refuse | PASS |
| false | AI-like | yes | eligible | **false** | refuse | PASS |
| unset | ordinary | yes | eligible | false | refuse | PASS |
| unset | AI-like | yes | eligible | true | bypass | PASS |
| true | AI-like | **no** | eligible | true | refuse (leaf) | PASS |
| false | ordinary | yes | eligible | false | refuse | PASS |
| true | AI-like | yes | FakeIP | true | refuse (FakeIP) | PASS |
| true | AI-like | yes | sniffed domain | true | refuse (sniffed) | PASS |
| true | AI-like | yes | TLS fragment | true | refuse (TLS) | PASS |
| true | AI-like | yes | tracker attached | true | refuse (tracker) | PASS |
| true | AI-like | yes | non-TUN inbound | true | refuse (inbound) | PASS |

The key row — `false + AI-like` → **false** — holds. Automatic detection does not reopen an explicit
decision.

## CRITICAL FINDINGS

**None.** No defect was found in the production implementation.

## HIGH FINDINGS

**None in production.** Three defects were found in the *previous round's tests*; all three are
fixed in this pass. They are recorded as findings because a test that cannot fail is a defect in the
evidence base, and two of them were load-bearing.

### Test defect 1 — the truth table passed for the wrong reason

The previous matrix helper built `&transparentGroup{tag: "🤖 AI"}` unconditionally, while one of its
cases was named `unset + capable leaf -> NOT authorised`. Because the helper forced the AI tag, that
case ran the AI branch, and the previous round's code special-cased the assertion by case name to
compensate:

```go
if testCase.name == "unset + capable leaf -> NOT authorised" {
    require.True(t, authorised, "...")
}
```

A test whose expectation depends on its own name is not a test. The tag is now a table column and
every assertion is derived from the row.

### Test defect 2 — the fallback path was never asserted

Every prior test checked the `BypassVerdict`. None checked the thing §15 actually requires: that an
authorised-but-refused flow **survives** on the normal userspace path. A refusal that returned
`PreMatchReject` or `PreMatchDrop` would have satisfied every existing test while dropping traffic.

Fixed by `TestFastPathAuthorizedButRefusedFallsBackToTheNormalPath`, which asserts
`PreMatchContinue` — explicitly not `Reject`, not `Drop`, not `Bypass` — across five blockers, and
additionally asserts the refusal came from the *flow-specific* blocker rather than from the chain
gate, so the test cannot pass because the flow was refused for an unrelated reason.

### Test defect 3 — nested precedence was untested in one direction

`inner explicit false` vs `outer AI tag`, and `authorised outer group over an inner level that cannot
carry a policy`, were both uncovered. Fixed by `TestFastPathNestedPrecedence` (4 sub-cases,
including an inner group object reused by two outer groups, asserted in both orders).

## MEDIUM FINDINGS

**None.**

One candidate was investigated and dismissed with evidence rather than assumed away:

**`Router.fastPathPolicies` is a plain map written by `SetFastPathPolicies`.** A concurrent write
during live traffic would be a data race. Checked the reload path: `StartOrReloadService`
(`daemon/started_service.go:324-383`) builds a **new** instance and closes the old one *before*
starting the new one, so the setter only ever runs during construction of a Router that no flow has
seen. The map is effectively immutable after setup. Not a defect.

## LOW FINDINGS

**None.** No dead code, no unused helper, no new TODO.

## FIXES APPLIED

Production fixes: **none** — none were needed.

### Fix 1 (test)
- symptom: the truth-table case whose name asserted "NOT authorised" actually exercised the AI path,
  and only passed because the assertion was special-cased by case name
- root cause: the matrix helper hardcoded the AI tag, so the tag was not a table dimension
- patch: the helper takes the tag; the table carries it as a column; the per-name special case is gone
- test: `TestFastPathTruthTable`, 13 rows

### Fix 2 (test)
- symptom: no test proved an authorised-but-refused flow reaches the normal path
- root cause: every test asserted the verdict, and the verdict is not the outcome the brief specifies
- patch: new test asserting the `PreMatchResult` action, for five blockers, plus the blocker identity
- test: `TestFastPathAuthorizedButRefusedFallsBackToTheNormalPath`, 5 sub-cases

### Fix 3 (test)
- symptom: nested explicit-false vs outer-AI, and authorised-over-opaque, were uncovered
- root cause: the previous round tested nested precedence only outer-to-inner in the permissive direction
- patch: 4 nested sub-cases including shared inner group reuse in both orders
- test: `TestFastPathNestedPrecedence`

## STATE ISOLATION

```text
leaf mutated:                     NO
global map/cache:                 NO   (the only map is group-tag -> explicit policy, written once
                                       at box setup; the reader skips leaves by construction)
same leaf across groups isolated: PASS
concurrent flows isolated:        PASS
selector switch stale state:      PASS
urltest stale state:              PASS
```

Evidence for `leaf mutated: NO` — this is the red line the brief cares about most, so it was checked
mechanically rather than by reading intent. Every `fastPath`/`FastPath` reference in non-test code was
enumerated with `git grep` and classified: all 29 are a type declaration, a config field, a doc
comment, a capability declaration, a read, or a setup-time write. There is no assignment to any field
of an outbound, and no map whose key is a leaf tag.

`route.resolveFastPathRequest` additionally **enforces** this at read time: it skips any chain element
that does not itself declare the capability, so even a hand-built policy map keyed by a leaf's tag
authorises nothing. `TestFastPathPolicyIsNotCachedOnTheLeafTag` asserts exactly that.

Both production `BypassableOutbound` implementors are `protocol/direct` (verified by grep for
`CanBypass`); no proxy or group implements it. So an authorised chain that selects a proxy leaf is
refused by `BypassRefusedOutboundNotBypassable` — authorisation can never turn a proxy into a bypass.

## POLICY PRECEDENCE

```text
explicit true:   authorised                    PASS
explicit false:  refused                       PASS
unset AI:        authorised (AI matcher)       PASS
unset normal:    refused                       PASS
false + AI:      FALSE - detection does not reopen it   PASS
```

Precedence rule as implemented and as tested: **first explicit decision, outermost to inner; an
explicit decision anywhere suppresses automatic detection for the whole chain; only if nothing is
explicit does the AI matcher run, again outermost to inner.**

## SAFETY GATE

```text
group true bypasses canFastBypass:   NO
BypassVerdict remains authoritative: YES
FakeIP:                              refused, authorised or not   PASS
domain/sniff:                        refused                     PASS
TLS rewrite:                         refused                     PASS
UDP special (UDPConnect):            refused                     PASS
outbound semantic blocker:           refused (leaf's own answer) PASS
tracker:                             refused                     PASS
non-TUN inbound:                     refused                     PASS
resolved candidates:                 refused                     PASS
```

Verified two ways. By diff: the only change to `canFastBypass` is the addition of one parameter and a
nested `if` around the existing `BypassRefusedOutboundChain` return — no blocker was removed,
reordered, or had its condition altered, and `BypassVerdict` itself is byte-identical. By execution:
all eleven pre-existing refusal tests still pass unchanged, and seven new authorised-flow cases
confirm that granting authorisation does not change any of their answers.

## ARCHITECTURAL BOUNDARY

```text
L0 changed:                  NO
DNS ordering changed:        NO
FakeIP boundary changed:     NO
traffic_class coupled:       NO
```

The diff touches none of `protocol/tun`'s L0 files, no DNS file, and no traffic-class file. The two
resolvers share `trafficclass.MatchTag` and nothing else — asserted in both directions by
`TestFastPathIsIndependentOfTrafficClass` (setting one never populates the other).

## PERFORMANCE CHECK

```text
per-packet tag scan:      NO
new hot-path lock:        NO
per-packet log/counter:   NO
```

`resolveFastPathRequest` runs once per flow inside `preMatchFlow`, which is itself a setup-time path.
It does at most one map lookup per chain element plus, only when nothing is explicit, one
`MatchTag` call per carrier element. Nothing was added to any read, write or packet path.
`BenchmarkBypassDecision` (single-leaf, the common case) is unchanged and still passes.

## TESTS RUN

```text
go build ./...                                             PASS
go vet <all touched packages>                              PASS
gofmt -l cmd include option protocol route service
        transport common dns adapter test/contract
        test/jiejie                                         clean
go test -count=10 -run 'TestFastBypass|TestFastPath' ./route/   PASS
go test -short <touched packages + root>  (13 feature tags)     PASS
  route 58.3s / route/rule / option / protocol/group /
  adapter / adapter/outbound / common/trafficclass / root
python3 scripts/ci/check-release-workflows.py             37 passed, 0 failed
```

New this pass: 13 truth-table rows, 5 fallback cases, 4 nested-precedence cases, 11 matcher
false-positive cases.

**Not run:** `-race`. Blocked by this host's C toolchain, established with three attempts in the
previous round; CI's race steps do not cover `./option/` or `./protocol/group/`, and the step that
would cover `./route/` including these tests (`client-macos.yml` deep-check) is dispatch-only behind
`deep_checks`. The concurrency tests ran without the detector, at `-count=50` and `-count=10`.

## OUT_OF_SCOPE_NOTES

- `server-linux-amd64.yml:731/:751/:777` count `SKIP` as "ran", so an all-SKIP run satisfies those
  vacuity guards. Carried over from the earlier CI audit; not touched here.
- `route/route.go:899`'s `len(chain) >= 2` is provably redundant (the branch is only reached when
  `len(chain) != 1`). Kept deliberately as a guard against future refactoring. Not a defect.

## RESIDUAL RISKS

1. **`-race` has not observed the concurrency tests.** The design has no shared mutable state for a
   race to exist on — the authorisation is a stack argument, and the only map is written once before
   any flow exists — but that remains an argument. To confirm: dispatch `client-macos.yml` with
   `deep_checks: true`.
2. **The urltest probe-traffic claim is verified by reading `protocol/group/urltest.go`** (probes dial
   the member directly, never through `Router.PreMatch`) and by the fact that probes cannot reach
   `preMatchFlow`, rather than by a test that runs a probe. A test would require a live urltest
   group.
3. **`loadbalance` exclusion rests on it not declaring `FastPathTransparent`.** A future group type
   that declares it while actually owning a dial would be a silent regression; the guard is that
   writing the method is an explicit assertion, not that the type system verifies it.

## FINAL VERDICT

```text
FAST_PATH_GROUP_POLICY_DEBUG = PASS
READY_FOR_NEXT_STAGE = YES
```

Answering §26's single essential question: **yes** — when one real node is reused by several logical
groups, `fast_path` remains strictly the policy of the group the current flow entered, and the flow
is still subject to every existing Direct Fast Path condition. The evidence is that no state was
written anywhere (checked by exhaustive grep and by the read-time carrier filter, with a test that
tries to cache a policy on a leaf's tag and proves it authorises nothing), and that authorisation
alone changes no verdict — including that of a leaf that cannot bypass, which still refuses with its
own reason.

**No production code was changed by this pass**, because no production defect was found. Three
test defects were found and fixed; the production change under review is identical to the one that
passed CI at `f08a61a0f`.
