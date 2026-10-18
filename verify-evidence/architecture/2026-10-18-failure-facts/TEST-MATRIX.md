# Test matrix 闁?cross-protocol failure facts

| Area | Check | Result | Evidence |
| --- | --- | --- | --- |
| Fact encoding | `WrapFailure` preserves `errors.Is/As`; zero/Unknown is not attached; nested facts resolve nearest | PASS | `adapter/failure_test.go` |
| Fact on aggregate | `errors.Join` + outer path fact preserves inner EOF and reports path | PASS | `TestFailureFactsOnAggregate` |
| Group retry | Target fact on ECONNREFUSED does not retry; Path dial fact does; Proxy handshake retries; Caller no; Data/EOF no | PASS | `protocol/group/loadbalance_failure_facts_test.go` fact rows |
| Group penalty | Target/Caller/Local/Proxy do not penalize; Path dial/handshake penalizes only when non-timeout; timeout stays unpenalized; Data no | PASS | same table |\n| Adversarial: timeout fact | A path/handshake fact on a timeout is retried but not penalized; a wrong-credential/blackhole timeout cannot demote a member | PASS | `TestLoadBalanceFailoverPathFactTimeoutDoesNotPenalise` |\n| Adversarial: cancellation fact | A Path fact attached to context.Canceled or net.ErrClosed cannot penalize a member | PASS | fact-table rows `path fact on cancellation`, `path fact on own teardown` |\n| Group end-to-end | Same ECONNREFUSED: target fact does not retry or penalize; path fact retries and penalizes | PASS | `TestLoadBalanceFailoverTargetFactSuppressesRetry`, `TestLoadBalanceFailoverPathFactStillRetries` |
| Existing errno policy | All pre-existing fact rows unchanged when no facts | PASS | `TestFailureFacts...` |
| URLTest health | Target/Local facts leave health history; Path fact deletes it | PASS | `TestATargetFactLeavesTheHealthEvidenceAlone`, `TestATargetFactIsNotAPathFact` |\n| Mieru producer | target reply 闁?Target/Target; both transport timeouts 闁?Path/Handshake; ambiguous TCP EOF fact-less | PASS | `TestMieruFailureFactsReachGroupConsumers` |
| Mieru disabled path | single-client dial wraps same facts; `ErrClientIsNotRunning` chain preserved | PASS | full mieru package |
| Mieru fallback behavior | target refusal, both failures, ambiguous EOF still behave as before | PASS | full mieru package + prior evidence |
| Adapter package | `go test ./adapter` | PASS | package run |
| Group package | `go test ./protocol/group` | PASS (3.9 s) | package run |
| Mieru package | `go test ./protocol/mieru` | PASS | package run |
| Race | `go test -race ./adapter ./protocol/group ./protocol/mieru` | PASS locally; final Verify CI success at 7569f0e | local race + CI 39141645579 |
| Error-path cost | plain 75 ns/op, wrapped 180 ns/op, 16 B/op, 0 success-path calls | PASS | `failure-facts-benchmark.txt` |
| Default fast path | `FailureFactsOf` only called on error in `RetryThisFlow`/`PenalizeMemberGlobally`; no scheduler/lock/goroutine | PASS by source | `adapter/failure.go`, group code |

## Commands

```powershell
go test ./adapter ./protocol/group ./protocol/mieru -count=1
go test ./adapter -run '^$' -bench BenchmarkFailureFactsOf -benchmem -count=3
go test -race ./adapter ./protocol/group ./protocol/mieru -count=1
```

## Not tested

- Real target-refusal behind every protocol: only Mieru can prove the fact;
  other protocols keep errno behavior and were not claimed.
- UI/API display of failure facts: intentionally not added.
- Mobile/TUN/long soak: environment boundary.
