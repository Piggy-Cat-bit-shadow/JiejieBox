# TRIPLEWISE C — Execution evidence

Environment: Windows 10.0.19044 amd64 · Go `go1.27.4` · `testing` @ `7346eacb0`.

## 1. What was executed in this round

```
go test ./dns -run 'TestRemoteTransport' -count=1 -v
  --- PASS: TestRemoteTransportCacheDoesNotCrossNetworks
  --- PASS: TestRemoteTransportNegativeVerdictDoesNotCrossNetworks
  --- PASS: TestRemoteTransportPersistentAndRDRCNamespacesFollowTheNetwork
  --- PASS: TestRemoteTransportInFlightAnswerIsNotStoredOnTheNewNetwork
  ok   github.com/sagernet/sing-box/dns   0.094s
```

Plus, for the neighbouring rows of the catalog, the suites that already exist and were re-run in this
effort (not re-run again here, to avoid duplicating identical evidence):

| Suite | Where its transcript is |
| --- | --- |
| `go test ./dns/...` (8.7 s + 6.1 s) | `docs/audits/triplewise-b-2026-11-19/TRIPLEWISE_B_VERIFICATION.md` §1 |
| `go test ./common/dialer` (22.0 s), `./common/httpclient`, `./transport/trusttunnel` (33.4 s), `./daemon` (21.7 s) | same page |
| `go test -race ./common/runtimecoord ./common/dialer ./route` (1.2 s / 23.3 s / 461.8 s) | `docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_RELEASE_READINESS.md` §3.3 |

## 2. The cache × generation mechanism, as the tests observe it

`dns.Router.ResetNetwork` does two things under one lock: it advances the environment generation, and it
purges the cache. The tests above observe the consequences from the cache's own API rather than from the
router's log lines:

- an answer cached on network A is not returned on network B (`CacheDoesNotCrossNetworks`);
- a **name-wide negative** verdict from network A does not suppress the name on B
  (`NegativeVerdictDoesNotCrossNetworks`) — the case that would look like "DNS is broken after
  reconnecting";
- a persistent-cache namespace and the RDRC namespace follow the same rule
  (`PersistentAndRDRCNamespacesFollowTheNetwork`);
- an answer that was **in flight** across the reset is not stored on the new network
  (`InFlightAnswerIsNotStoredOnTheNewNetwork`).

That fourth one is the interesting one for a three-system reading: the in-flight answer's *owner* is the
old network, the *writer* is the new cache, and the *reader* would be a dial on the new generation. The
test asserts the writer is refused, which is the only place that can be refused deterministically.

## 3. What was not executed, and why

| Gap | Reason |
| --- | --- |
| an actual DNS query → fake-IP → TUN packet → routed outbound → reply cycle | needs a TUN device or a two-pass flow fixture (triple B's TB-6 records the same fixture) |
| a device measurement for any row (NE budget, Cronet native, `phys_footprint`) | no device; substituting Go heap or Linux RSS would be a false green |
| the `sing-tun` UDP NAT fix | cross-repo + pin evidence rule (release freeze) |
| the health-probe/selector family and the xhttp body lifecycle | session budget; both are named as the next round's first candidates |
| upstream Issue/PR reading | unreachable in this session (documented in the discovery catalog) |

## 4. Mutation controls carried by this round

**This page was written before the round had finished its code work, and the sentence that used to be
here — "this round adds no code" — is superseded.** The same round went on to make two production
changes, each with its own mutation control:

| Change | Control | Result |
| --- | --- | --- |
| C-T5: the QUIC pool's publish and generation acknowledgement became one decision (`transport/v2rayquic`) | restore publish-then-acknowledge | red: "a connection built for the previous generation must not be handed out" |
| The sixteenth fix: a closed `ManagedTransport` refuses to rebuild (`common/httpclient`) | disable only the `getEpoch` closed check | red on the request test, green on the reset test — the two guards are separately load-bearing |

The controls that back the *earlier* claims still belong to the rounds that made those fixes and are
listed in their verification pages: `MC-1` (A), `MC-B1…B6` (B), `MC-C1/C2` (C), `MC-T1a/b` (triple A).
