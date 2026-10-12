# Routing, DNS, sniff and server protocols

## Scope and provenance

Fork work includes DNS epochs/groups (`5b0a33c`, `a123864b`, `ffec0df1`, `8fad3e9b`), route/dial behavior (`4ca74d69`, `0dfedb38`, `d017b5bb`), sniff hardening (`e053cc73`, `4dff25c`, `9167595`, `bfe72145`, `578663e0`), and validation closure (`fc7b0f17`, `04591f8`, `5ad4c032`, `ff004007`, `4e228f53`). All are **OWN**. MASQUE base support `b83089ec` and Naive dependency update `f4c0c412` are **UPSTREAM-INHERITED**, not fork inventions.

## Current design

DNS caches and reverse mappings use epoch-aware ownership; a DNS-only environment change has its own generation. Sniffed domains may aid destination-address planning but must not rewrite the original destination/rule semantics. Sniff input is hostile: QUIC parsing uses packet views and frame summaries, bounded fragmented rounds, and explicit ownership release.

Fork MASQUE lifecycle commits (`a367bbe3`, `a00a2fe6`, `0d28c26d`, `9235f90`, `c3b4f567`, `2118ee40`) make readiness/fallback/teardown/MTU bounds explicit. MASQUE bootstrap `DomainResolver` is outside the tunnel; `InnerDomainResolver` is inside it and they are not interchangeable.

## Invariants, evidence and cautions

Physical demand uses provable reachable flow types only. FakeIP cannot provide destination resolution. `ErrNeedMoreData` is the only fragmented-QUIC continuation signal. Outer link, outer QUIC packet and inner tunnel MTU are separate quantities.

Evidence: DNS transport/FakeIP/e2e tests, `route/action_resolve_test.go`, `resolve_uot_sentinel_test.go`, `common/sniff` boundary tests, `box_static_validation_dns_demand_test.go`, and MASQUE lifecycle/ownership/MTU tests. Multi-platform DNS and full macOS/H3 device behavior remain **UNVERIFIED**. `PhysicalPath.RequiredNetworks` has a history/comment tension; retain it as a **FOLLOW-UP**, not a new contract.
