# Triple-wise B — the Provider / DNS / TUN / routing closed loop

Second window of the three-system round.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch
- Starting HEAD: `dd50d93b4` (the state triple-wise A left). Commits added: the DNS guard test plus
  these reports
- Platform: Windows amd64, Go **1.27.4**; no Linux netns/nftables, no Administrator token, no device;
  no PREF64/NAT64 network
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

**This window found no new production defect.** It did find a real hole in the evidence: the complete
lookup's cross-epoch refusal - the check that stops one address set from being assembled out of an A
half answered on the old network and an AAAA half answered on the new one - had **no test at all**, so
any refactor could delete it and every suite would stay green. A deterministic guard and its control
now pin it, with a mutation that turns the guard red by disabling exactly one comparison. The rest of
the matrix is inherited and re-checked against HEAD rather than re-counted: the fake-IP chain, the
transition ordering and the H3-only fallback still hold as recorded; TB-5 stays a cross-repo product
decision; TB-6/TB-9/TB-12 stay `NOT_COVERED` with the reason named; the Linux-only rows are
`NOT_OBSERVABLE` here.

## 1. The one thing added: a guard for an untested safety property

**The property.** `Client.Lookup` is the COMPLETE entry: A and AAAA are two exchanges, and the caller
receives ONE address set that routing, rule matching, the candidate list on metadata and diagnostics
all treat as a single claim about the name. Each exchange captures the generation at issue time, so a
network change between the two halves used to produce a set whose A half described the network that
had been left and whose AAAA half the one that was current. Both halves were individually well formed;
the set was never true on any network.

**The code.** `lookupComplete` (`dns/client.go:1339`) captures the epoch before dispatching either
family and refuses the result if the epoch moved, with a comment that reads like a fix write-up -
`Refusing is the minimal behaviour change: the caller's answer was never valid`. `grep` over every
`*_test.go` in the tree found no reference to the error, the function, or the split: the property was
enforced and unpinned.

**The test** (`dns/client_complete_epoch_test.go`), deterministic by construction:

1. a transport that answers A immediately and parks AAAA on a gate;
2. the complete lookup is started; the test waits until the A half has answered;
3. the test advances the generation - the network changes in the window between the halves - and
   releases AAAA;
4. the lookup must return an error naming `address families belong to different networks` and a **nil**
   address set.

The control runs the same harness without the advance and requires the union of both families, so the
refusal cannot be satisfied by breaking the normal path.

**Evidence.**

| Stage | Result |
| --- | --- |
| guard | `TestACompleteLookupSplitAcrossAnEpochChangeIsRefused` PASS |
| control | `TestACompleteLookupOnOneEpochReturnsBothFamilies` PASS (both addresses) |
| mutation | with `lookupHasEpoch && false && …` the guard fails: `An error is expected but got nil` |
| package | `go test -count=1 ./dns/` → `ok` 8.63 s |

**Six-stage check.**

1. *Loaded*: `dns` is in every build; `lookupWithSource` routes `complete` items to `lookupComplete`.
2. *Reached*: the test calls the same `lookupWithSource` entry the router uses, with the strategy that
   selects the complete item, and asserts `lookupItemFor(options).complete` before starting.
3. *Decided*: the decision is "may this set be published as one claim"; the epoch comparison is that
   decision, and the mutation shows it is the only thing that makes it.
4. *Acted*: the caller receives an error and a nil set instead of a half-stale union.
5. *Observed*: the returned error text is the guard's own, and the control proves the union is still
   produced when the epoch is stable.
6. *Settled*: both family exchanges complete; nothing is left parked (the gate is released in both
   tests).

## 2. The rest of the matrix, and why it is not re-counted

| Row | Verdict | Note |
| --- | --- | --- |
| TB-1 fake-IP chain | `NO_DEFECT` (inherited, code+tests checked) | the three holes are closed with mutation controls (`pairwise-b` §2); not re-attacked here |
| TB-2 transition / fallback | `NO_DEFECT` (inherited) | reset is unconditional and ordered; the missing *record* of a fallback is R-B3, a reporting decision |
| TB-3 ordering | `NO_DEFECT` (inherited) | by construction |
| TB-4 stale cache → candidate plan | **`NO_DEFECT` for the DNS boundary, joint `NOT_COVERED`** | the boundary is now pinned; the joint harness is described in `MATRIX.md` §2, including why `dns` importing `common/dialer` decides where it must live |
| TB-5 TUN UDP NAT × admission | `POLICY_DECISION` (cross-repo) | window A's R-4..R-7 verification; R-5 fixed in the fork, the rest are product calls |
| TB-6 two-pass flow | `NOT_COVERED` | no fixture |
| TB-7/TB-8 nftables/TProxy | `NOT_OBSERVABLE` | no Linux netns, no Administrator token |
| TB-9 URLTest interleaving | `NOT_COVERED` | suites green, no new interleaving built |
| TB-10 H3-only | `NO_DEFECT` (inherited) | policy honoured |
| TB-11 reload consistency | `NO_DEFECT` (inherited) | close-then-build; cost is R-B9 |
| TB-12 node vs socket reporting | `NOT_COVERED` | no per-connection node surface exists; the selector reports the *current selection*, which is the documented contract - the question is a semantics note |

## 3. Executed in this window

| Command | Result |
| --- | --- |
| `go test -count=1 -run 'TestACompleteLookup' -v ./dns/` | 2 PASS |
| `go test -count=1 ./dns/` | `ok` 8.63 s |
| `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./provider/... ./common/dialer/ ./protocol/group/ ./common/urltest/` | `ok` provider/local 0.85 s, provider/parser 0.16 s, provider/remote 0.63 s, common/dialer 21.98 s, protocol/group 7.30 s, common/urltest 8.41 s |
| mutation on `lookupComplete` | red, `An error is expected but got nil`; restored |

The suites were run with the general tag profile on this host; the timings in the table are from that
run.

## 4. Gates

The `dns` package is green with the new tests, no production code changed, no dependency pin moved
(`release/source-lock.json`, `release/pin-runs.json` untouched). The push gate status is in the
session's final report.

## 5. New direction this window opened

The joint TB-4 harness is the concrete next step and its shape is now known: it must live in `dns`
(which may import `common/dialer`) or in `route`, build a real `Client` over a scripted transport,
hand it to the dialer as the router, and assert that a candidate list produced after a transition
contains no address whose source generation predates it. The `dns` package already exposes the source
stamp and the generation option, so the fixture is a matter of wiring, not of new hooks.
