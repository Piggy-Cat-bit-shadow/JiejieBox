# TRIPLEWISE B — Verification

Environment: Windows 10.0.19044 amd64 · Go `go1.27.4` (the `toolchain` directive) · `testing` @
`649e29021` · `CGO_ENABLED` default for the plain runs. No platform-specific tag was needed for the
suites cited here; the trusttunnel package's `with_trusttunnel` tag adds only the outbound registration.

## 1. Commands executed in this window

```
go test ./transport/v2rayquic ./transport/v2rayhttp ./transport/trusttunnel \
        ./common/httpclient ./common/dialer ./dns/... ./daemon -count=1

  ?   transport/v2rayquic            [no test files]
  ok  transport/v2rayhttp            0.245s
  ok  transport/trusttunnel         33.422s
  ok  common/httpclient              0.127s
  ok  common/dialer                 22.037s
  ok  dns                            8.668s
  ok  dns/transport                  6.119s
  ok  dns/transport/dhcp             0.127s
  ok  dns/transport/fakeip           0.254s
  ok  dns/transport/group            0.960s
  ok  dns/transport/hosts            0.124s
  ok  dns/transport/quic             0.312s
  ok  daemon                        21.655s
```

Source reads are listed with file:line in `TRIPLEWISE_B_WIRING_AND_LEAKS.md`; each claim in that
document points at a line that was read in this window at this HEAD, not at a remembered one.

## 2. What is verified, and by what

| Claim | Kind of evidence |
| --- | --- |
| the transition resets the shared HTTP client chain unconditionally | source: `route/network.go:1685-1690` (`r.router.ResetNetwork()`) + `route/router.go:459-463` + `common/httpclient/managed_transport.go:174-187` |
| that reset rebuilds the inner transports (so the H3 verdict and the HTTP/1.1 pin cannot survive) | source: `epoch.Swap(nil)` and the `factory()` rebuild path, plus `common/httpclient/manager.go:214-221` iterating every managed transport |
| the reset precedes the listener phase | source: `performTransitionLocked` (`resetNetworkLocked` → `commitTransition` → `notifyNetworkChangeListeners`) |
| the fake-IP guard consults the issuance history | source + the six tests and four mutation controls listed in the wiring document §1 |
| reload cannot expose a half-installed graph | `go test ./daemon` (reload/recovery matrix, 21.7 s) |
| `H3 only` builds no fallback object | source: `common/httpclient/client.go:134-135`, `transport_requirement.go:84-87` |

## 3. What was not executed

| Gap | Reason |
| --- | --- |
| a full A+B+C data-plane run (DNS query → fake-IP → TUN packet → routed outbound → reply back through the NAT wrapper) | the harness would need a TUN device or a two-pass flow fixture; window B's TB-6 records the same missing fixture |
| auto_redirect / nftables / system-proxy × TUN (TB-7, TB-8) | Windows host, no netns, no nftables, no privilege |
| a stale-DNS-cache × new-generation candidate plan test (TB-4) | needs a scripted resolver plus the dialer's plan inspector; not built |
| the cross-repo UDP NAT fix (TB-5) | `sing-tun` fork change + a pin bump whose release gate requires a CI run for the new revision |
| `-race` on the cited suites in this window | they were raced in earlier windows (`common/dialer`, `route`, `common/runtimecoord` at 23 s / 462 s / 1.2 s) and the source here is unchanged by this window; no new race claim is made |
| upstream Issue/PR reading | the earlier attempt returned documentation pages only; no issue or PR is cited anywhere in this round |

## 4. Release position

Nothing in this window changed code, so the release evidence stands as recorded in
`docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_RELEASE_READINESS.md`: pin lock PASS,
knowledge-fact gate PASS, `go vet ./...` clean, the shipped Windows profile builds and its critical
packages pass, and `-race` on the three core state machines is green. The suites re-run for this window
are a subset of that and are green at the current HEAD.
