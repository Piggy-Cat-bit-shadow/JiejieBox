# PAIRWISE C — Execution evidence

Every claim in this window is backed by a command run in this session. Environment: Windows
10.0.19044 amd64, Go `go1.27.4` (the `toolchain` directive of `go.mod`), `CGO_ENABLED=1` with
`CC=C:\Deepseek\.tools\mingw64\bin\gcc.exe` when `-race` is used, branch `testing`, base
`86140a161` (the head window B left).

## 1. Commands and results

```
go build ./...                                            # OK (whole module, after both fixes)
go vet ./protocol/vless ./protocol/vmess ./protocol/trojan \
       ./protocol/hysteria2 ./protocol/tuic ./protocol/masque   # clean
gofmt -l <the touched packages>                           # no output

go test ./protocol/vless ./protocol/vmess ./protocol/trojan \
       ./protocol/hysteria2 ./protocol/tuic ./protocol/masque ./protocol/trusttunnel -count=1
        ok vless 0.435s · vmess 0.096s · trojan 0.112s · hysteria2 1.039s · tuic 0.950s
        ok masque 3.127s · trusttunnel 0.248s

go test ./protocol/{vless,vmess,trojan,hysteria2,tuic,masque} \
       -run TestInterfaceUpdatedRoutesThroughTheGenerationGate -count=1 -v
        6/6 PASS   (one per package, see §2)

go test ./dns/transport/fakeip ./dns/... -count=1
        ok fakeip · dns · dns/transport · group · hosts · quic    (full DNS suite after C-2)

go test ./dns/transport/fakeip -run 'TestAReloadWithoutACacheFile|TestConcurrentQueriesForOneDomain' -count=1 -v
        2/2 PASS
```

## 2. The new tests, and what each observes

| Test | Package | Observation (independent of the component's own logs) |
| --- | --- | --- |
| `TestInterfaceUpdatedRoutesThroughTheGenerationGate` ×6 | the six outbounds | a counting callback installed as the production `GenerationGate`'s reset; the listener must reach it once per generation and not twice |
| `TestAReloadWithoutACacheFileDoesNotReissueAddresses` | `dns/transport/fakeip` | the addresses two successive stores return, plus the ledger's own attribution of the first one |
| `TestConcurrentQueriesForOneDomainShareOneAddress` | `dns/transport/fakeip` | the two returned addresses and the mapping read back from the store |

## 3. Mutation controls (the "can the test see a disabled mechanism" requirement)

| Control | Mutation | Result |
| --- | --- | --- |
| **MC-C1** | in `protocol/vless/outbound.go`, replace the gateway wrapper's body with a direct `h.resetForNetworkChange(ctx)` | `TestInterfaceUpdatedRoutesThroughTheGenerationGate` FAILED: `expected 1, actual 0 — the listener must run the network-change action for the new generation` (the listener no longer reaches the gate at all) |
| **MC-C2** | in `dns/transport/fakeip/store.go`, remove the two `resumeAfterLedger` calls | `TestAReloadWithoutACacheFileDoesNotReissueAddresses` FAILED with the measured message: `the reload re-issued 198.18.0.2, which a client may still be holding for the previous domain` |

Both mutations were reverted immediately; the tree was confirmed clean (`git status --short` empty)
before each commit, and the restored suites re-ran green.

## 4. What was *not* executed, and why

- **No `-race` run for C-1.** The change adds no shared mutable state (it routes an existing call
  through an existing mutex-guarded ledger); the gate's own concurrency contract is covered by
  `common/runtimecoord`'s race-tested suite, which was run in window A. Reading that as "race-tested
  here" would be a false claim, so it is stated as absent.
- **No end-to-end network change.** Driving `coordinator.Advance` is exactly what the network manager
  does, but interface enumeration, route-table rewrite and the 6 s listener budget around it were not
  exercised. A real Wi-Fi→cellular switch needs a device.
- **No upstream Issue/PR scan.** One web search returned documentation pages only, and `git fetch`
  needed retries against connection resets; no issue or PR is cited anywhere in this window
  (`PAIRWISE_C_DISCOVERY_CATALOG.md` §1).
- **No load or soak test.** The fixes are on the control path (a listener call, an allocator's start),
  not on a data path; no benchmark or long run is claimed.

## 5. CI status of the new tests

- The six gate-routing tests live in packages that **no push-time job runs**: `verify.yml`'s build step
  compiles them (`go test -run '^$' ./...`) and `protocol/{vless,vmess,trojan,hysteria2,tuic}` are not
  in any test list; `protocol/masque` is raced only by `linux-masque-verify.yml`, a
  `workflow_dispatch` suite.
- The fakeip tests are executed by no workflow either, but `dns/...` is a small suite and the same call
  site is exercised indirectly by the `e2e` rows that also do not run (window B, §5).
- **Recommendation, not action:** the cheapest honest mount is a `verify.yml` step running
  `go test ./protocol/... ./dns/...` — measured cost in this session: `protocol/*` ≈ 4 s, `dns/...`
  ≈ 9 s, so ~15 s against a 30 m budget. It was not added here because the whole `protocol/` tree has
  never been run as a suite in CI, and introducing a 15 s step whose failure modes are unmeasured is a
  CI decision, not a debug decision. The tests exist and pass by hand; the gap is recorded.
