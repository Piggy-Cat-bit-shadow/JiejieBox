# Lifecycle, zero-copy and protocol/system coupling audit — 2026-10-27

**Baseline:** `a6ee0c0d012` (`testing`; the dual-stack closeout).

This round looked at three cross-cutting questions, not one component: who owns a lifecycle, where
buffer ownership crosses a copy boundary, and how a protocol reaches the rest of the system.

## 1. Lifecycle

### LC-01 — `CommandServer.Close` released subscribers but left the Box running

`experimental/libbox/command_server.go` `Close` stopped gRPC, closed the listener, then called
`StartedService.Close`, the OOM recorder and the power manager. `StartedService.Close` only closes
the observable subscribers; it does **not** close the running `Instance`/Box. The ordered shutdown is
the separate `CloseService` RPC. An embedder that tears down the transport (or a process path that
does not issue the last RPC) therefore left the Box — TUN, DNS, goroutines, network monitor — running
behind a closed command server.

`CloseService` is `closeOnce`-fenced at the subscriber level and serialized by `lifecycleAccess`, so
calling it from `Close` is idempotent and cannot race the ordered path. `Close` now calls it before
`StartedService.Close`. Regression: `TestCommandServerCloseClosesTheBox` (p2ios build; it starts a
real Box through `StartOrReloadService` and asserts `Instance()` is nil after `Close`).

The rest of the lifecycle inventory was re-read: `adapter.Scope` close-boundary rules, `Box.Close`
ordering (governor before scope), reload (`StartOrReloadService`), device-axis mirror, the macOS
resume path and the five nested-scope owners. No new ownership defect was found; the existing tests
(`adapter/lifecycle_close_boundary_test.go`, `daemon/device_axis_reload_test.go`,
`route/reference_coverage_stress_test.go`) still pass.

## 2. Zero-copy

### ZC-01 — kTLS `WaitReadBuffer` dropped data that arrived with close-notify

The kTLS fast path reads the application data into a pooled buffer, then, when the next record is a
close-notify alert, calls `readRecord` to make the current read return `(n, EOF)` like the slow
`Read`. `WaitReadBuffer` released the buffer and returned `nil, io.EOF` when that call returned EOF:
the n bytes were never handed to the caller. `Read` returns `n, io.EOF`; the waiter lost the tail.
The consumer (`sing/common/bufio/copy_direct.go`) discards a buffer whenever `err != nil`, so the
fix has to be on the waiter side.

`ktls_read_wait.go` now records `pendingReadEOF` and returns the data with `nil`; the next call
returns `io.EOF`. The stream ends one read later rather than losing its last bytes. Linux
cross-build with `badlinkname` passes.

### ZC-02 — badtls wait path had an unsatisfiable close-notify condition

`common/badtls/read_wait.go` tested:

```go
if n != 0 && c.rawConn.Input.Len() == 0 && c.rawConn.Input.Len() > 0 &&
    c.rawConn.RawInput.Bytes()[0] == 21 {
```

The second `Input.Len()` term made the branch impossible, so the fast path never consumed the alert
and never observed the EOF on the same read. The index was also unguarded in principle. The condition
now tests `RawInput.Len() > 0` (the same condition as `Read` and the kTLS waiter); the alert is
consumed, data is returned now, EOF follows on the next call.

### Verified as already correct

- The splice silent stall is fixed in the pinned sing source (`cebf939` sets `handed=false` on the
  `Pipe2` setup failure); the pin `v0.9.6-0.20261024214131-f601163d18d1` carries it.
- The buffer-ownership rules (pool geometry, sniff borrowed views, MASQUE all-or-nothing batch,
  owned-datagram transfer, substitution refusal, pool retirement) pass their suites:
  `common/bufgeom`, `common/sniff`, `transport/masque`.
- No new packet-clone path was found; the kTLS/badtls wait paths are the two copy-boundary defects.

## 3. Protocol/system coupling

No new production defect was found in this round, but the coupling boundaries are real and are
recorded here rather than implied:

- **Service locator assumption.** Protocol constructors reach for core services through
  `service.FromContext`: `adapter.NetworkManager` (direct, bridge, masque endpoint, openvpn/openconnect),
  `adapter.DNSRouter` (HTTP, naive, mieru, masque, DNS outbound), `adapter.HTTPClientManager`
  (hysteria2), `adapter.EndpointManager` (openvpn/openconnect DNS transports),
  `*runtimecoord.Coordinator` (loadbalance), `pause.Manager` (urltest). `box.New` registers all of
  them; a protocol constructed outside a Box (a test or a tool) is responsible for doing the same.
  `direct.Outbound.fetchMyAddresses` in particular assumes `NetworkManager` and `InterfaceFinder`
  are present. This is a deliberate service-locator architecture, not a hidden global, but it is the
  main coupling cost in the protocol layer.
- **Capability loss through wrappers.** The failover shim (`route/failover.go`) embeds the narrow
  `adapter.Outbound` and forwards only the surfaces the route layer reads. It hides
  `LocalFamilyDialer`, `PacketDialerWithDestination`, `BypassableOutbound`, `ResolveDialer`,
  `FlowOutbound`, the idle-connection interfaces, `OutboundWithPreferredRoutes` and
  `OutboundWithMultiplex`. The KB records why each is unreachable on the current packet path; this
  round found no new consumer that reads one of them through the shim. The one known latency boundary
  remains DS-02: a group outbound does not forward the parallel candidate surface, so a resolved
  address list is walked serially behind `selector`/`urltest`/`loadbalance`.
- **`InterfaceUpdated` implementations.** The current tree has 27. The earlier audit checked the 17
  that existed at v0.1.21 and fixed the SSH unlocked-close race. The newer ones read this round —
  `naive` (fallback reset + close all), `masque` (`RestartSession`), `hysteria2` inbound
  (`service.Reset`), `redirect/tproxy` (`udpNat.Purge`) — perform a single state-owning reset and do
  not close a value another path can replace. No new unlocked read/write was found.
- **`Upstream`/`DialChain`.** No outbound gained a repo-wide `Upstream` that would let
  `common.Cast` find a dialer below a detour; the known WireGuard bind regression remains the reason
  `DialChain` is the narrow hop protocol.
- **Protocol-specific knowledge in the route layer** (`C.TypeTun`, `C.PortProtocols`, fake-IP family
  matching) is intentionally central; this round found no new protocol switch outside those tables.

## 4. Evidence

| Check | Result |
|---|---|
| `go build -tags "$OTHERS" ./...` | PASS |
| `go test ./common/bufgeom ./common/sniff ./transport/masque` | PASS |
| `go test -run '^$' ./common/badtls ./common/ktls -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)"` | PASS (compile) |
| Linux cross-build of `common/badtls ./common/ktls ./experimental/libbox` with `GOOS=linux` | PASS (the kTLS files are Linux-only) |
| `go test -tags p2ios -run TestCommandServerCloseClosesTheBox ./experimental/libbox` | PASS |
| `go test ./route -short` (from the dual-stack closeout) | PASS |
| `-race` on the changed packages | **BLOCKED_BY_ENVIRONMENT** on this host (no C compiler); CI runs it |

`common/ktls` and `common/badtls` have no runtime test for the wait path; the two fixes are verified
by source inspection and by a Linux cross-build, not by a close-notify fixture. The KB's recommended
table test driving the same records through `Read` and `WaitReadBuffer` remains the missing check.

No tag, no release, `release/JIEJIE_VERSION` remains `0.1.28`.
