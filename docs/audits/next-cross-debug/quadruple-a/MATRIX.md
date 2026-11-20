# Quadruple A — QUIC · NE · OOM · NAT: matrix

Window A of the four-system round. Base: `testing` @ `5661fc9e5`. No commits added by this window
except these reports: no new production defect was found in the arms this host can execute, and the
report says exactly which arm of each four-way was executed and which is platform-blocked.

- Platform: Windows amd64, Go 1.27.4, **no Administrator token** (no WinDivert/TUN), no Linux
  netns/nftables, no NAT64/PREF64 network, no iOS device or Network Extension
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 1. The four rows

| Row | The four systems | Verdict | What was executed / what is blocked |
| --- | --- | --- | --- |
| **Q02** | platform interface event · `runtimecoord.GenerationGate` · QUIC carrier (`familyCarrier`/single client) · in-flight dial / stream | **`NO_DEFECT`** for the executable arms, including the in-flight dial (`f306b2505`); the NAT-rebinding arm stays `NOT_COVERED` | the gate's action, its once-per-generation dedupe, the teardown-vs-network-change distinction, post-reset reuse and the demand already inside the handshake are all pinned by tests run green here (see §2). The rebinding arm needs a controlled UDP proxy in front of a real hysteria2 server, which this window did not build |
| **Q03** | `service/oomkiller` pressure · `common/resourceguard` admission · `sing-tun` NAT table/queue · real UDP `PacketConn`/goroutines | **`NOT_OBSERVABLE`** for the NAT-table arm; the admission arms are `POLICY_DECISION` (already recorded, not re-claimed) | without TUN/admin there is no NAT table to drive; the 4096-entry cap and the "no admission on the UDP path" finding are window A's R-4 (pairwise), priced as a product decision, and are not re-counted here |
| **Q07** | MASQUE PREF64 discovery · DNS bootstrap/cache · QUIC path · network epoch | **`NOT_OBSERVABLE`** | needs a NAT64/PREF64 network and an IPv6-only bootstrap. `transport/masque` is green (9.4 s) but its tests do not synthesize PREF64 discovery × bootstrap × epoch, so a pass there is not this row |
| **Q08** | iOS NE budget · Cronet/native ownership · OOM/Guard pressure · H3 speed-test cancellation | desktop half **`NO_DEFECT`**; device half **`NOT_OBSERVABLE`** | the Go-side cancel chain is executed and green (§2); `phys_footprint`, Jetsam and native Cronet ownership need a device, and no Windows RSS number is offered as a substitute |

## 2. Executed in this window

| Command | Result |
| --- | --- |
| `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./protocol/hysteria2/ ./protocol/tuic/ ./transport/masque/ ./common/urltest/ ./experimental/clashapi/ ./common/resourceguard/ ./service/oomkiller/` | all `ok`: hysteria2 1.16 s, tuic 1.10 s, masque 9.42 s, urltest 8.36 s, clashapi 0.58 s, resourceguard 1.07 s, oomkiller 0.88 s |
| `go test -count=1 -race -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./protocol/hysteria2/ ./common/urltest/ ./common/resourceguard/ ./experimental/clashapi/` | all `ok` under `-race`: hysteria2 2.55 s, urltest 9.85 s, resourceguard 2.15 s, clashapi 1.69 s |

What those suites pin for the Q02 four-way (each is a prior round's fix, verified green at this HEAD,
not re-claimed as new):

- `gate_action_test.go` — the listener-phase notification is routed through the gate and the action is
  the reset, not the gated listener; the second notification for one generation is deduplicated.
- `gate_routing_test.go` — `InterfaceUpdated` reaches the gate at all (the push entry is not bypassed).
- `family_teardown_test.go` — a dial after the *Box* teardown is refused as `os.ErrClosed` and creates
  no client; a dial after a *network change* is allowed to build a fresh client (the two meanings of
  `CloseWithError` are not conflated).
- `family_concurrency_test.go`, `family_lifecycle_test.go` — two families under concurrent demand.
- `in_flight_reset_test.go` (`f306b2505`) — **the fourth arm, executed for the first time**: a dial
  already inside the QUIC handshake (the path is a UDP socket that reads and never answers) when the
  coordinator advances the generation and the listener notifies. The dial returns in ~3 ms with the
  reset's own `network changed` error, so the pinned sing-quic client's `CloseWithError` reaches
  pending offers and no in-flight creation outlives its owner. The test also records the harness trap
  it hit first: without the coordinator advance the gate (correctly) treats the event as a duplicate
  and runs no reset, which leaves the dial waiting out its timeout and looks like the defect itself.

For Q08's desktop half: `TestClashDelayIsCancelledWithTheRequest`,
`TestGenericGroupDelayIsCancelledWithTheRequest` (the handler returns when the request context is
cancelled, i.e. the measurement does not run on after the client disconnects) and
`TestCoordinatorSurvivesCancelAtTheGrantBoundary` / the coordinator lifecycle stress (cancel at the
grant boundary leaves every waiter with a slot or its own cancellation). All green.

## 3. Necessity: which fourth system is real, and which is decoration

| Row | Would three systems reproduce it? | The fourth's real contribution |
| --- | --- | --- |
| Q02 | the gate/teardown/dedupe behaviour is **two-system** (event × gate) with the carrier as the object; the in-flight-dial arm needs the fourth (the stream/dial) | the in-flight arm is the only one where the fourth system changes the observable (a dial that outlives the reset). Not attacked here |
| Q03 | the admission/permit behaviour is two-system (pressure × guard); the NAT table adds the *cost* dimension | the four-way claim needs the real NAT table, which is why it stays `NOT_OBSERVABLE` rather than being argued from unit tests |
| Q07 | the DNS half is two/three-system and was covered earlier; the fourth (the QUIC path's remoteAddr) is the whole point | without a NAT64 fabric the fourth cannot be observed, so no claim is made |
| Q08 | the Go cancel chain is two-system (request context × measurement); the fourth (native engine) is what the row is about | the device is required; the desktop half is honestly downgraded to "the Go half is clean" |

## 4. Not claimed

No row in this window is marked `FIXED`. The four-way interactions that *were* dangerous in this
area (gate re-entrancy, post-teardown client creation, one event executed twice) were fixed in the
2x2/3x3 rounds and are verified green here; the remaining arms need a Linux netns, a NAT64 network or
an iOS device, and each is named above rather than implied.
