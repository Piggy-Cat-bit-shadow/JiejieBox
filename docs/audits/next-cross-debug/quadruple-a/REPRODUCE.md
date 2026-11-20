# Reproduce — quadruple A (Q02/Q03/Q07/Q08)

Windows PowerShell. This window made no production change; the commands below are what was executed and
what the blocked arms would need.

## 0. Environment

```powershell
$env:PATH = 'C:\Deepseek\.tools\go1.27.4\go\bin;' + $env:PATH
$env:GOFLAGS = '-mod=mod'
cd C:\Deepseek\JiejieBox
```

## 1. The suites that own the executable arms

```powershell
$tags = (Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
go test -count=1 -timeout 1200s -tags $tags `
  ./protocol/hysteria2/ ./protocol/tuic/ ./transport/masque/ `
  ./common/urltest/ ./experimental/clashapi/ ./common/resourceguard/ ./service/oomkiller/
# ok hysteria2 1.162s, tuic 1.101s, masque 9.424s, urltest 8.364s,
#    clashapi 0.578s, resourceguard 1.070s, oomkiller 0.877s
```

## 2. Q02 - the individual arms

```powershell
go test -count=1 -tags "$(Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()" -v `
  -run 'TestTheListenerTransitionIsNotReentrant|TestTheSecondNotificationIsStillDeduplicated|TestInterfaceUpdatedRoutesThroughTheGenerationGate|TestAShutdownCarrierNeverCreatesAClientAgain|TestANetworkChangeLeavesTheCarrierReusable' `
  ./protocol/hysteria2/
```

### 2.1 The in-flight arm (executed, `f306b2505`)

```powershell
go test -count=1 -tags "$(Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()" -v `
  -run 'TestANetworkChangeAbortsADialStuckOnTheDeadPath' ./protocol/hysteria2/
# --- PASS ... the in-flight dial returned 2.68ms after the network change: network changed
```

The fixture: a UDP socket bound on loopback that reads and never answers (the path is dead but refuses
nothing), the production hysteria2 outbound pointed at it, a `DialContext` started in a goroutine, the
first packet observed on the wire, then `coordinator.Advance(1)` **followed by**
`outbound.InterfaceUpdated(ctx)`. The dial returns in ~3 ms with the reset's own error.

The order is load-bearing: without `Advance(1)` the gate treats the notification as a duplicate and
runs no reset, and the dial then waits out the full handshake timeout - the exact shape that looks
like a defect. That is the gate doing its job (one epoch, one destructive action), not the arm failing.

### 2.2 The rebinding arm (not covered)

The fixture it needs:

1. a UDP proxy bound on loopback that forwards to a local hysteria2 server and, after N bytes, starts
   using a second upstream socket (the server then sees a new 4-tuple - rebinding);
2. the client outbound pointed at the proxy's address;
3. two flows: A held open (blocked peer) and B echoing small payloads;
4. `InterfaceUpdated` (the platform event) fired at the moment of the rebinding;
5. the assertions: the old pool/client is not resurrected after close; B is not killed by A's cancel;
   the client's physical socket actually moves; after everything closes the number of live QUIC
   sockets returns to zero.

None of that was built, so this half stays `NOT_COVERED`.

## 3. Q03 - why it is not observable here

The `sing-tun` NAT table only exists on a TUN device. `common/netcap`/`common/windivert` need an
Administrator token (they are `NOT_OBSERVABLE` in this session and in CI). The guard and OOM-killer
halves were run (above) but are not the four-way claim.

## 4. Q07 - the NAT64 fabric

`transport/masque` runs green but its tests do not synthesize PREF64 discovery, a delayed bootstrap
answer and an epoch change. A NAT64/PREF64 network is required; without one the row is
`NOT_OBSERVABLE`.

## 5. Q08 - the split

```powershell
go test -count=1 -tags "$(Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()" -v `
  -run 'TestClashDelayIsCancelledWithTheRequest|TestGenericGroupDelayIsCancelledWithTheRequest' `
  ./experimental/clashapi/
go test -count=1 -tags "$(Get-Content release/DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()" -v `
  -run 'TestCoordinatorSurvivesCancelAtTheGrantBoundary' ./common/urltest/
```

The device half (`phys_footprint`, Jetsam, native Cronet ownership) needs an iOS device/NE and is not
reproducible here.
