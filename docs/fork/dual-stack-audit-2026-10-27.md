# Dual-stack end-to-end audit — 2026-10-27

**Baseline:** `6e6ec9a8b53` (`testing`; the commit this audit started from)
**Previous dual-stack audit:** `KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md`
(v0.1.21) and its closure. This round re-reads the current tree instead of inheriting that verdict.

## 1. Scope and method

The chain was followed end to end, not file by file:

| Stage | What was read |
|---|---|
| Configuration | `option/ipv6_entry.go`, `option/tun.go`, `option/route.go`, domain-strategy parsing |
| DNS | `dns/router.go`, `dns/router_dual_stack.go`, `dns/client.go` (`Lookup`, `collectFamiliesComplete`, `sortAddresses`), `dns/entry_ipv6.go`, `dns/transport/exchange_strategy.go` |
| Resolution / candidates | `common/dialer/resolve.go`, `dual_stack.go`, `dual_stack_recovery.go`, `admission.go`, `family_capability.go` |
| Scheduling | `dual_stack_scheduler.go`, `dual_stack_parallel_network.go`, `default_parallel_network.go`, `default_parallel_interface.go` |
| Socket factory | `common/dialer/default.go` (`DialContext`, `DialParallelInterface`, `ListenPacket`, `ListenSerialInterfacePacket`, `DialerForICMPDestination`) |
| TUN / entry | `protocol/tun/inbound.go`, `effective_ipv6.go`, `entry_ipv6.go`, `route/network_family_entry.go` |
| Platform observation | `common/netcap/netcap.go`, `entry.go`, `route/network_family_route_{linux,darwin,windows}.go`, `route/route.go` |
| Rules | `route/rule/rule_item_ipversion.go`, `route/route.go` (`IPVersion` and FakeIP packet matching) |
| Protocol bootstrap | `protocol/masque/bootstrap_race.go`, `bootstrap_cache.go` |

The earlier audit's `DS-01` (late local-family refusal) is fixed and its tests pass; `DS-03` joins are
present. `DS-02` (group outbounds do not forward the parallel candidate surface) and `DS-05` (entry
presentment latched until restart) are unchanged, deliberate boundaries, and are restated in §4.

## 2. Confirmed defects fixed this round

### DS-06 — the network entry point overrode the effective family preference

`resolveParallelNetworkDialer.schedule` derived the planner strategy from the **first candidate's
family**:

```go
plan := planCandidates(candidates, plannableOriginal,
    domainStrategyFor(len(candidates) > 0 && candidates[0].Is6()))
```

With a caller-supplied candidate list — TUN metadata, a cached answer, resolver order — this
overrode the operator:

- `prefer_ipv4` and a list leading with IPv6 raced IPv6 first;
- `prefer_ipv6` and a list leading with IPv4 raced IPv4 first;
- `AsIS` with an IPv6 original raced IPv4 first, although the planner's AsIS rule is
  "the application's original family leads".

**Fix:** use `admission.strategy` (the effective domain strategy, already resolved for `AsIS`).
For `AsIS` with no original, keep the list's leading family so an ordered caller answer is preserved.
The strict strategies were already filtered into one family; `planCandidates` receives them unchanged.

**Tests:** `TestNetworkEntryPointHonorsPreferIPv4OverTheListOrder`,
`TestNetworkEntryPointHonorsPreferIPv6OverTheListOrder`,
`TestNetworkEntryPointAsISKeepsTheOriginalFamily`.

### DS-07 — the per-attempt dialer ignored the candidate's family

`dialParallelInterfaceEpoch` chose the socket factory by transport only:

```go
if N.NetworkName(network) == N.NetworkTCP {
    dialer = d.dialer4.Dialer
} else {
    dialer = d.udpDialer4
}
```

`DialParallelInterface` is called once per **scheduled address**. With `inet4_bind_address` set, an
IPv6 candidate was dialled from the IPv4 local address (and generally failed); with
`inet6_bind_address` set, the IPv6 bind was ignored on the IPv6 half. Without either bind both
dialers are copies of the same base, which is why the defect was invisible in the common case.

**Fix:** `parallelDialer(network, address)` selects `dialer4`/`dialer6` and
`udpDialer4`/`udpDialer6` from the candidate. The same family selector now governs the direct
`DialContext`, `ListenPacket`, `ListenSerialInterfacePacket` and `DialerForICMPDestination` paths.

**Test:** `TestParallelDialerFollowsTheCandidateFamily`.

### DS-08 — IPv4-mapped addresses were treated as IPv6 by family selectors

`netip.Addr.Is6()` is true for `::ffff:192.0.2.1`, while the candidate planner's `classifyAddress`
correctly treats it as IPv4. The divergence still existed in:

- `DefaultDialer.DialContext` / `ListenPacket` / `DialerForICMPDestination` (IPv6 bind chosen);
- `route.rule.IPVersionItem.Match` (`ip_version: 6` matched a mapped IPv4 destination);
- `route.Router` setting `metadata.IPVersion` from a mapped address;
- the FakeIP packet-destination family comparison in `preMatchFlow`.

**Fix:** a single `isIPv6DialAddress` helper (`classifyAddress == familyIPv6`) is used by the socket
selectors; `IPVersionItem`, the metadata assignment and the packet-destination comparison treat
`Is4In6` as IPv4.

**Test:** `TestIPVersionItemTreatsMappedAsIPv4`, plus the mapped cases in
`TestParallelDialerFollowsTheCandidateFamily`.

## 3. Verification

Local, with `release/DEFAULT_BUILD_TAGS_OTHERS`:

```
go build ./...                                              PASS
go test -run '^$' ./...                                     PASS (all test binaries compile/link)
go test ./common/dialer                                     PASS (17.9 s)
go test ./common/netcap ./protocol/tun ./protocol/masque     PASS (tun 4.8 s, masque 54.4 s)
go test ./dns/...                                           PASS
go test ./route/rule                                        PASS
go test ./route -short                                      PASS (80.9 s)
```

Targeted regressions:

```
TestNetworkEntryPointHonorsPreferIPv4OverTheListOrder       PASS
TestNetworkEntryPointHonorsPreferIPv6OverTheListOrder       PASS
TestNetworkEntryPointAsISKeepsTheOriginalFamily             PASS
TestParallelDialerFollowsTheCandidateFamily                 PASS
TestIPVersionItemTreatsMappedAsIPv4                         PASS
```

CI evidence is added after the run; no tag, no release, `release/JIEJIE_VERSION` remains `0.1.28`.

## 4. What this audit did NOT change

- **DS-02 — group outbounds serialise a resolved address list.** `selector`/`urltest`/`loadbalance`
  do not forward `ParallelNetworkDialer`, so `DialSerialNetwork` falls back to a per-address serial
  loop. The closure measured the difference (direct shape races, group shape starts the second
  family only after the first attempt ends and shares the same deadline) and chose not to add a
  delegated parallel surface because it would change member-selection/failover semantics. The risk
  remains; it is a latency/connection-budget risk, not a policy violation.
- **DS-05 — entry presentment is decided once, before the TUN opens, and holds until restart.**
  A box that starts on an IPv4-only network keeps an IPv4-only TUN and suppresses AAAA after the
  network becomes dual-stack. This is documented in `route/network_family_entry.go`; changing it
  means re-opening or re-addressing the TUN on a network change.
- **DS-04 — fallback amplification.** The layering is real but no production measurement was added
  in this round.
- **UDP semantics.** A connected UDP socket is still "success" at socket creation; the unconnected
  packet path remains serial because socket creation is not an outcome. No change.
- **Physical devices.** No real TUN/NetworkExtension/dual-stack network was exercised; the tests
  above are unit/production-graph tests, not device evidence.
