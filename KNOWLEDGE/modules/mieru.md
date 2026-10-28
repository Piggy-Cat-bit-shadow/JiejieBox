# Mieru protocol integration

**Scope and evidence.** At testing source target `011f3fdcd99`, `include/registry.go` registers
Mieru inbound and outbound; `protocol/mieru` adapts the `github.com/enfein/mieru/v3` SDK to
sing-box routing, lifecycle and dialer interfaces. The SDK owns its protocol and multiplexer;
the adapter owns configuration, listener/dialer bridges and their lifecycle. This is source-level
coverage of the integration, not a claim of universal wire compatibility. Selected fallback,
classification, option and lifecycle tests passed locally in this calibration; see `ACCEPTANCE.md`
for their scope.

## Configuration and data path

`option/mieru.go` and `protocol/mieru/config.go` accept TCP, UDP or both as the **Mieru underlay**;
omitted transport defaults to TCP. A primary port can be accompanied by port ranges. Password and
hashed password are alternatives, and Mieru's multiplexing is the SDK's own mechanism. The inbound
can listen on both underlays. UDP MTU defaults to the SDK value when omitted; validation and exact
range belong to `config.go`, not to the outer TUN MTU.

The outbound constructs a server dialer and resolver bridge using sing-box dialer options, so the
proxy-server path and the target carried through the protocol have different ownership. TCP target
connections use the SDK dial; UDP target operations use the Mieru stream through UoT adapters
(`outbound.go`, `uot.go`, `packetconn.go`). Inspect the exact `DialContext` versus `ListenPacket`
path before assuming a connected UDP flow and a per-datagram destination have identical semantics.

## Opt-in fallback

`fallback.enabled` is off by default. When enabled, the backup must explicitly use the other single
underlay (TCP versus UDP); primary or backup `both`, equal transports and `no_wait` handshake are
rejected. Backup server and port may inherit the primary; a primary configured only with port ranges
requires an explicit backup port. Credentials and other protocol settings are shared, not an
independent second account. `resolveFallbackOptions` validates this before building the runtimes.

The outbound holds independent primary and secondary SDK runtimes. Preference changes select a
transport for **new** sessions and do not themselves close existing sessions. The first cooldown is
30 seconds, increases after unsuccessful recovery probes, and is bounded at five minutes; recovery is
demand-driven, not a background polling loop. The selector permits one recovery probe for a generation
and discards stale-generation reports. The primary can retry the secondary once when the classified
failure permits it; a target SOCKS rejection, caller cancellation or ambiguous TCP EOF is not proof
that the transport is unhealthy. Endpoint address preparation failure can justify trying a distinct
backup endpoint without labeling the primary network path broken. UDP SOCKS-response EOF has its own
classification. The precise branches and typed socket observations are in `fallback.go` and
`dial_observation.go`; do not infer peer interoperability from classification tests.

## Lifecycle and network change

Both sides register cleanup with their scopes, and release the lifecycle lock before `scope.Add`
because a closing scope invokes late cleanup synchronously. Inbound start launches its accept loop
before registering close; a partial server start closes listeners created by the bridge. Inbound close
cancels, closes owned listeners, stops the SDK, closes tracked sessions and waits for the accept loop
and handlers only within its stated bounds (`inbound.go`). A timeout warning is not proof that every
handler has finished.

Outbound close blocks new dials, waits at most five seconds for in-flight setup, then stops the SDK
runtimes; a timeout is logged and does not prove the remaining setup finished. `InterfaceUpdated`
does nothing before first demand; otherwise it constructs and starts replacement runtimes before
swapping generation, resets fallback state, and stops the old runtimes. A failed replacement leaves
the old generation installed. The fallback path checks generation before handing out a resulting
connection; examine the separate non-fallback path rather than assuming every code path has the
same delivery check (`outbound.go`). Network switching can end existing Mieru sessions; a mere
fallback preference change is a different operation.

## Verification and maintenance

- `protocol/mieru/config_test.go`: fallback validation and option translation.
- `protocol/mieru/fallback_test.go`, `early_failure_test.go`: classification, rollback, cooldown,
  stale-generation and target-refusal behavior. Inspect the exact selected tests and environment.
- `protocol/mieru/inbound_test.go`, `outbound_test.go`, `e2e_test.go`: lifecycle and local SDK paths;
  the presence of these files alone does not establish execution or external peer compatibility.
- `KNOWLEDGE/modules/path-selection-validation.md`: how failure facts affect route selection;
  `KNOWLEDGE/modules/naive-http-transport.md`: the generic UoT per-datagram guard and its limits.

Before changing the SDK pin, transport selection, or network-rebuild path, recheck the option
validation, ownership of in-flight dials and sessions, fallback classification, and the exact SDK
behavior. Distinguish a local unit test from a physical network transition or independent peer test.
