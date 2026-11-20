# Triple-wise B — Provider / DNS / TUN / routing closed loop: matrix

Window B of the three-system round. Base: `testing` @ `dd50d93b4` (the state triple-wise A left).
Commits added: `test(dns): pin the complete lookup's cross-epoch refusal` plus these reports.

Verdict for this window: **no new production defect**. One safety property that the code already
enforces was found to have no test at all; a deterministic guard and its control were added. The prior
round's open rows are re-checked against HEAD rather than re-counted.

## 1. The rows, re-checked at HEAD

| Row | The three systems | Verdict at `dd50d93b4` | Evidence / why |
| --- | --- | --- | --- |
| TB-1 | fake-IP allocation · TUN fast path · router refusal | **NO_DEFECT** (unchanged; not re-claimed) | the three holes closed by B-3/B-4/B-5/C-2, with mutation controls; `pairwise-b` §2 |
| TB-2 | network transition · shared clients · fallback state | **NO_DEFECT** (unchanged) | `Router.ResetNetwork()` runs unconditionally in `performTransitionLocked`; the "silence" half is R-B3 (a reporting decision) |
| TB-3 | transition ordering · listeners · pools | **NO_DEFECT** (unchanged) | reset before listeners, by construction |
| **TB-4** | stale DNS cache · dual-stack candidate plan · new generation | **NO_DEFECT** for the boundary this window could execute; the **joint** remains `NOT_COVERED` | see §2: the DNS-side boundary is now pinned by a new test; the dialer side is tested against a *scripted* resolver, not the real cache |
| TB-5 | TUN UDP NAT · outbound packet session · admission | `POLICY_DECISION` / cross-repo (unchanged) | window A's R-4..R-7 verification: R-5 fixed in the fork, R-4/R-6/R-7 are product decisions |
| TB-6 | two-pass flow | `NOT_COVERED` | no fixture; unchanged |
| TB-7, TB-8 | nftables/TProxy, two capture paths | `NOT_OBSERVABLE` | no Linux netns, no Administrator token |
| TB-9 | URLTest interleaving | `NOT_COVERED` | in-package suites green; no new interleaving built |
| TB-10 | H3-only fallback | **NO_DEFECT** (policy honoured) | no fallback object to reach |
| TB-11 | reload consistency | **NO_DEFECT** (consistency) | close-then-build cannot expose a mixed graph; cost is R-B9 |
| TB-12 | selector node · physical connection · state display | `NOT_COVERED` | no per-connection node surface exists to disagree; the selector's reported node is the *current selection* by contract, so the question is a semantics note, not a socket-vs-report defect |

## 2. TB-4: which half is covered, and which is not

The triple needs three things at once: a **cached answer** produced under an old network, a **new
generation**, and a **dial decision** that consumes the answer as a candidate.

| Half | Where | Covered by |
| --- | --- | --- |
| the cache never crosses a namespace (environment fingerprint + transport pin) | `dns/client.go` | `environment_rebind_test.go`, `environment_transition_window_test.go`, `source_binding_test.go` |
| an answer selected under generation G keeps stamp G | `dns/client.go` | `source_binding_test.go` (C2) |
| a **complete** lookup cannot assemble A and AAAA across an epoch change | `dns/client.go` `lookupComplete` | **was untested** — this window adds `TestACompleteLookupSplitAcrossAnEpochChangeIsRefused` and its control, both executed |
| the dialer's candidate plan under a transition | `common/dialer` | `admission_transition_answer_interleaving_test.go` — but through a **scripted resolver gate** (`stallLookup`), not through `dns.Client` |
| the joint: real cache → real dialer candidate plan | — | **NOT_COVERED**. `dns` imports `common/dialer` (`dns/transport_dialer.go`), so a test in `common/dialer` cannot import `dns`; the joint needs a harness in `dns` or `route` that builds both. Recorded as the next step |

The new test closes the boundary that matters most for TB-4: it is the exact point where an old half
of an answer could enter the candidate set. If that refusal is removed, the caller receives an A+AAAA
union that was never simultaneously true, with no error - the "wrong target" shape the row is about.

## 3. Executed

| Command | Result |
| --- | --- |
| `go test -count=1 -run 'TestACompleteLookup…' -v ./dns/` | both PASS (guard + control) |
| `go test -count=1 ./dns/` | `ok` 8.626 s |
| mutation: the epoch comparison disabled in `lookupComplete` | the guard test fails (`An error is expected but got nil`) |
| `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./provider/... ./common/dialer/ ./protocol/group/ ./common/urltest/` | see §3.1 |

### 3.1 Suite results

Recorded in `REPORT.md` §3 with the timing; all green at this HEAD.

## 4. What this matrix does not say

- It does not claim TB-1/TB-2/TB-3/TB-10/TB-11 were re-attacked; they are inherited conclusions whose
  code and tests were checked for presence, not re-executed with new attacks.
- It does not claim the joint TB-4 chain is safe: the two halves are covered, the joint is not.
- It does not treat TB-5 as fixed: R-4/R-6/R-7 are product decisions recorded in `pairwise-a` §5.1.
