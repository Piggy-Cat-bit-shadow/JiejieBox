# Adaptive IPv6 at the entry layer

**Scope:** the application-facing half of the address-family work. The dial-side gate
(`netcap.HardUndialable` through `common/dialer`) already exists and is unchanged; this document
describes the layer in front of it, which decides what the process *tells applications* about IPv6.

Status: implemented and unit-tested on `testing`. The Apple client change is landed in
`sing-box-for-apple` `6e559a4` and pinned by `clients/apple`. No Android/iOS/macOS build, simulator or
device run was performed - see [Limits](#limits-and-what-was-not-verified).
## The problem it solves

A machine whose physical network carries no usable IPv6 still usually has *some* IPv6 structure: a
link-local `fe80::` address on every adapter, a TUN carrying `fdfe:...` and `::/0`, a Fake-IP range
with `inet6_range`, a hosts table with AAAA entries, a resolver that answers AAAA because nobody set
a strategy. Applications then choose IPv6 - repeatedly, per connection - and every such attempt is
either refused by the dial gate (one wasted candidate) or fails after the OS gives up.

The dial gate cannot prevent the *choice*: it only refuses an attempt after the application has
already made one. An entry can prevent it, because an entry is what the application's address
selection is based on.

## The layers

```text
typed configuration
    │  option.ResolveIPv6EntryDemand          (at box construction; O(inbounds + DNS servers))
    ▼
demand: does anything here present IPv6 to an application?
    │  no  → nothing below runs: no observation, no route read, no decision, no consumers
    │  yes
    ▼
read-only network observation                  (NetworkManager.Start, StartStateStart)
    │  interfaces (platform list or finder) + MyInterfaces() + global IPv6 route
    │  netcap.Evaluate + netcap.ClassifyIPv6Addressing
    ▼
netcap.ProjectIPv6Presentment                 (the decision, committed once)
    │
    ├─ TUN: applyIPv4OnlyPresentment          (effective options, before the interface is opened)
    └─ DNS: NODATA for application AAAA       (dns.Router.prepareExchange, before rules/transports)
```

### 1. Demand (`option/ipv6_entry.go`)

`ResolveIPv6EntryDemand` reads the assembled typed options - not the configuration text - and reports
whether any application-facing surface can present IPv6:

| surface | input | why |
|---|---|---|
| TUN | `address`, `route_address`, `route_exclude_address`, `dns_address`, `loopback_address`, `route_address_set` / `route_exclude_address_set` | an IPv6 address, route or hijack DNS address reaches the platform and the stack. `auto_route` alone does **not**: every stack in this tree derives the automatic IPv6 range from the address list |
| Fake-IP | `inet6_range` | an AAAA query can be answered with a synthetic IPv6 address |
| Hosts | a predefined IPv6 address, or any configured hosts file | proving a file has no AAAA would mean reading and scanning it at configuration time |
| DNS | any configured server | `strategy: ipv4_only` narrows what a *lookup* asks for; an application that asks AAAA itself is still answered, so it is not a demand suppressor |

`Required() == false` is the fast path: the whole system is inert and the process behaves exactly as
before.

### 2. Observation (`route/network_family_entry.go`)

`observeIPv6EntryPresentment` runs in `NetworkManager.Start(StartStateStart)`, which is **before any
inbound exists** and long before `Box.start()` calls `EstablishInitialNetworkEnvironment()` at the
end. It is read-only:

- prefers the interface list the manager already published (the platform's filtered view on clients
  that supply one), otherwise reads the platform list, otherwise refreshes the finder once;
- excludes this process's own interfaces (`MyInterfaces`);
- reads the machine-level global IPv6 route through the existing platform readers;
- calls `netcap.Evaluate` and `netcap.ClassifyIPv6Addressing` on the same slice;
- **does not** publish the list, move the environment fingerprint, claim a transition, reset DNS or
  transports, or touch `NetworkFamilyCapabilitySnapshot`.

A failure at any step is "no opinion": nothing is committed and every consumer behaves as before.

### 3. The decision (`common/netcap/entry.go`)

`ProjectIPv6Presentment(capability, addressing)` suppresses only on facts the machine can prove:

| evidence | verdict |
|---|---|
| `IPv6 == Unavailable` (usable interfaces exist, none carries IPv6) | suppress |
| link-local addressing only | suppress |
| global addressing, `GlobalIPv6Egress == Unavailable` | suppress |
| unique-local (ULA) addressing | **present** - LAN IPv6 works even when the public path does not |
| route table unreadable / network unknown / no interface observed | present (fail open) |
| interfaces up but not addressed yet (handover) | present (the shape alone never suppresses) |

### 4. Consumers

**TUN** (`protocol/tun/entry_ipv6.go`, `protocol/tun/effective_ipv6.go`). When the committed
decision is "suppress", the effective `tun.Options` lose `Inet6Address`, `Inet6RouteAddress`,
`Inet6RouteExcludeAddress`, `Inet6LoopbackAddress`, `Inet6Gateway` and any IPv6 `DNSAddress`.

#### Multiple TUNs: the box decides once, and only if every interface can honour it

A box may configure more than one `tun` inbound. The entry decision is one value for the whole box,
so `box.New` pre-checks the typed configuration with
`option.AllTunInboundsReducibleToIPv4Only` - the same predicate the reduction itself uses - and the
manager commits "suppress" only when every interface is reducible. If one is not, the box commits
"present" and says so in the log; every interface then keeps its configured IPv6.

The two refusals that make a TUN irreducible are the same rule the reduction applies, and they are
shared rather than duplicated (`option.TunReducibleToIPv4Only`):

1. no IPv4 address - an interface must keep at least one address;
2. an explicit `dns_address` list with no IPv4 entry - applications would have no resolver.

An earlier revision let each TUN correct the shared value when its own reduction was refused. That
was order-dependent and produced exactly the split it was meant to prevent: the first interface
stripped, the second rolling the decision back to "present" for everyone - so DNS presented an IPv6
one interface was no longer carrying. The correction API is gone; `commitIPv6EntryPresentment` has
one writer (the manager's observation), and a TUN that cannot apply a committed suppression logs an
Error and leaves the shared value as the box committed it, which is the safe direction.

The same decision is applied to the IPv6 prefixes a `route_address_set` contributes at start
(`appendRouteSetPrefixes`), which is the second place IPv6 can enter the options and the one a
leading-fields-only implementation leaves open. Clearing `Inet6Address` is what makes the rest
coherent: sing-tun derives the platform addressing, the automatic IPv6 route range, the IPv6 DNS
address and the Windows firewall exception from it, and the platform clients condition their own
default `::/0` branch on it.

**DNS** (`dns/entry_ipv6.go`). In `Router.prepareExchange` - shared by `Exchange` and `ExchangeAsync`,
so UDP and TCP behave identically - an AAAA question whose source is the local TUN is answered with
NOERROR/NODATA before rule matching. Consequences:

- the Hosts transport is never consulted for that AAAA, so a hosts entry with A and AAAA keeps
  serving A and the AAAA never costs a lookup; the hosts data itself is untouched;
- Fake-IP never allocates an IPv6 mapping for an application query; `inet6_range` and the store are
  untouched, and mappings persisted from earlier sessions keep resolving. `Store.Create` has exactly
  one production caller (`dns/transport/fakeip/fakeip.go:75`, the transport's own `Exchange`), so the
  unified entry covers every creation path - there is no side channel that could hand out a v6
  placeholder;
- no upstream resolver is dialled, no cache entry is written, no reverse mapping is recorded;
- A, PTR, TXT, HTTPS/SVCB, EDNS and DNSSEC are untouched; HTTPS/SVCB hints are deliberately not
  rewritten (no new protocol surface).

`Lookup` is **not** gated: it is how a dialer resolves a destination of its own choosing, including a
proxy's own domain and a target a proxy will reach on this machine's behalf.

Other entries are deliberately not gated:

| source | why not |
|---|---|
| a WireGuard / MASQUE / OpenVPN / Tailscale endpoint | it forwards DNS for a *remote* peer, whose connectivity is not this machine's |
| a `hijack_dns` rule reached from a proxy inbound | the application chose to send DNS into a proxy, and the address it gets back is one that proxy can carry |
| internal exchanges (ECH, resolver discovery, a transport) | no inbound metadata; they are not an application being told what this machine can do |
| `service/resolved`, `clash_api` | a LAN server and a diagnostic API, not the local application entry |

## Lifecycle

- **One owner.** The network manager owns the facts, the observation and the published value. No
  new epoch, lock, goroutine, timer or subscription is added, and no component registers a second
  network callback.
- **Start.** Demand → observation → commit → the TUN consumes it when its own `StartStateStart`
  runs. No observation is made on a network event.
- **Network change.** The decision is a latch: the interface is already open, and no supported
  platform offers a cheap in-place update of its addressing. A transition therefore does **not**
  reconfigure the TUN, reallocate the Fake-IP store or reset DNS for this feature. The dial gate
  (which *is* live, per generation) continues to refuse what it can prove.
- **Reload.** A new box re-runs demand and observation from the new configuration; no old decision
  can reach it.
- **Close.** Nothing is registered and nothing is spawned, so teardown is unchanged.

### The trade, stated plainly

A box that starts on a network proving no local IPv6 keeps its IPv4-only interface until it is
restarted, even if the device later moves to a dual-stack network. The reverse is also true: a box
that started dual-stack keeps presenting IPv6 after moving to an IPv4-only network, and the dial
gate refuses the attempts.

This is deliberate. A live projection would let the DNS entry approve AAAA for an interface that is
not carrying IPv6 - the split state this pipeline exists to prevent - and reconfiguring the interface
on every transition is the "frequent TUN rebuild" the round forbids. The direction of the residual
error is always "IPv6 presented but refused at the dial", never "IPv4 broken" and never "DNS and
interface disagree".

### The trade for proxied IPv6

With Fake-IP, the application never connects to a real address: it connects to a synthetic one that
sing-box maps back to a domain, and the proxy dials the domain. Suppressing AAAA therefore does not
cost a proxied IPv6-only destination anything - the proxy's own reachability is what decides.

Without Fake-IP, an application that receives a real AAAA and then connects is on its own: if the
route is direct, the dial gate refuses it immediately (that is the win); if the route is proxied, the
application can no longer reach that IPv6-only target through the proxy from this machine. That is
the accepted cost of a single, global entry decision - a per-destination decision would require
evaluating the route rules at DNS time, which is exactly the double rule evaluation the round
forbids. Configurations that need proxied real-IPv6 keep presenting IPv6 when the machine has any
unique-local or global IPv6 path; a machine with no IPv6 at all is the case where the entry
suppresses.

## Platform delivery contracts

The decision is only published where the platform layer can actually express "this interface has no
IPv6". That is a property of the CLIENT's tunnel-settings code, not of the build tag, and it was
checked against the pinned clients rather than assumed.

**Android (`satelite-one`, `VPNService.kt`): can, and is enabled.** Every IPv6 fact is derived from
the options that the core reduces:

- `while (inet6Address.hasNext()) builder.addAddress(...)` - no address, nothing added;
- API 33+: `if (inet6RouteAddress.hasNext()) ... else if (options.inet6Address.hasNext()) addRoute("::", 0)`
  - no explicit route and no address, no default route;
- pre-33: routes come from `options.inet6RouteRange`, which the core builds with
  `BuildAutoRouteRanges`, whose IPv6 block is gated on `len(Inet6Address) > 0` - again empty;
- DNS servers come from `options.dnsServerAddress`, which `tun.Options.DNSServerAddress()` derives
  from the remaining (IPv4) address.

**Darwin, iOS and the tvOS flavour (Network Extension, `ExtensionPlatformInterface.swift`
`openTun0`): can, and is enabled.** The pinned client used to create `NEIPv6Settings` and a default
`::/0` route **unconditionally** inside `if options.getAutoRoute()`, so a core that stripped IPv6
addressing would have handed it an IPv6 default route with no address - an interface that claims the
family and cannot carry it - or settings the system would reject. That is fixed in
`sing-box-for-apple` commit `6e559a4` (pushed to `main`; `git diff -w c001d59 6e559a4` is the four
comment lines, `if !ipv6Address.isEmpty {` and its closing brace, plus re-indentation):

```swift
if !ipv6Address.isEmpty {
    let ipv6Settings = NEIPv6Settings(addresses: ipv6Address, networkPrefixLengths: ipv6Prefixes)
    ...
    settings.ipv6Settings = ipv6Settings
}
```

Every dual-stack configuration keeps exactly the previous code path (routes, sub-range default,
excluded routes, `excludeDefaultRoute`). The other `setTunnelNetworkSettings` call sites re-apply the
same stored `networkSettings` object, so no second writer can reintroduce the family. The core pins
that revision (`clients/apple` = `6e559a4`) and `scripts/ci/check-libbox-abi.sh` records the move:
the range touches no contract method and no call site, layer 4 could not be re-run on this host (no
Python interpreter), and CI performs it against the moved tree.

The deliverability answer is a **positive list** - Windows, Linux (which `C.IsLinux` extends to
Android), `C.IsDarwin` (macOS and iOS) and `C.IsTvOS` (spelled out because the tvOS flavour's
GOOS/tag combination is not guaranteed to satisfy `IsDarwin`) - and not a negative one: any platform
without a verified client contract keeps the previous behaviour. `route/network_family_entry_test.go`
pins that table on one runner.

## Limits and what was not verified

| item | status |
|---|---|
| `common/netcap`, `route`, `dns`, `protocol/tun`, `option` unit tests | `PASS` (see the commit message for the exact commands) |
| Windows/Linux route readers (reused, unchanged) | `PASS` as existing tests |
| Android `VpnService.Builder` receiving no IPv6 address/route | **VERIFIED_SOURCE** against `satelite-one-main/app/src/main/java/com/interstellar/proxy/bg/VPNService.kt` and `experimental/libbox/tun.go` + sing-tun `BuildAutoRouteRanges`; **no** Gradle/AAR/emulator run (`NOT_RUN_BY_REQUEST`) |
| Apple `NEIPv6Settings` with an empty address list | **VERIFIED_SOURCE** against `sing-box-for-apple` `6e559a4` (the `!ipv6Address.isEmpty` guard) and `swiftc -parse` on the patched file (exit 0). The tunnel-settings change is not exercised by an Xcode build, simulator or device (`NOT_RUN_BY_REQUEST`); the pin is `clients/apple` and the ABI gate records the move |
| an application with its own DoH or hard-coded IPv6 literal | not covered by design - the OS-level IPv4-only interface makes such attempts fail immediately, which is earlier than any DNS interception |
| a network change flipping the decision | not applied until restart, by design; see Open risks for why re-deriving it without re-addressing the interface would be worse |

## Files

| file | role |
|---|---|
| `common/netcap/entry.go` | address-shape classification, projection, presentment type |
| `adapter/network.go` | `IPv6EntryPresentmentReader` (the optional reader interface) |
| `route/network_family_entry.go` | read-only observation, commit, reader, platform deliverability |
| `route/network.go` | the committed pointer and the demand flag |
| `option/ipv6_entry.go` | configuration demand summary |
| `protocol/tun/effective_ipv6.go` | effective-option surgery and the route-set append |
| `protocol/tun/entry_ipv6.go` | the TUN's read and application of the decision |
| `dns/entry_ipv6.go` | the application-facing AAAA gate and its NODATA response |
| `docs/fork/ipv6-entry-apple-applied.patch` | the Network Extension client change, archived after it landed as `6e559a4` |

## Debug round 02: what the attacks changed

The Debug pass exercised this system against its own invariants and fixed what broke. The defects
are worth recording because each one was reachable by a configuration, not by a code review:

| finding | trigger | fix |
|---|---|---|
| a proxy-only box paid for the observation and could never consume it | no `tun` inbound, but DNS servers (and possibly a Fake-IP IPv6 range) | `ResolveIPv6EntryDemand` returns the empty summary when no TUN is configured; the DNS gate is keyed on the TUN as the query source, so nothing could consume the decision |
| a bare hosts server (or `predefined`-only) was reported as "no AAAA" | `{"type":"hosts"}` with no `path` | the transport always loads a file - the system hosts file when `path` is empty - so the answer is "may"; this also removed the summary's only table scan |
| a four-in-six address was classified as IPv4 demand | `::ffff:172.19.0.1/120` in `address` / `dns_address` | the demand now uses `Is6`, the same predicate the TUN splits these lists with, so it cannot miss an address the interface installs |
| loopback was classified as global addressing | a `::1` address on a non-loopback interface set | `ClassifyIPv6Addressing` ignores loopback; the projection still presents (unproven shape), which the test states explicitly |
| the entry-demand hand-off was a plain bool across a constructor/Start boundary | a client that constructs the box on one goroutine and starts it on another | `entryDemand` is an `atomic.Bool` |
| the deliverability answer was `!IsDarwin` | the Apple client family builds GOOS values `constant.IsDarwin` does not enumerate (tvOS, ...) | the answer is a positive allowlist; Apple joined it only after its client revision made the delivery expressible (see Platform delivery contracts) |

The tests added by the round were checked against mutations of the production code - the DNS gate
disabled, the route-set IPv6 prefix re-enabled, the commit-site deliverability guard dropped, the
projection forced to suppress, the loopback case removed, the proxy-only fast path disabled, the
demand predicate un-mapped - and each mutation turns at least one of them red.

Three more things were closed by the same pass: the TUN-side demand helper that the box-level
summary had already replaced is deleted (a second copy of the same predicates is a second place for
them to drift); the startup tests now supply the platform delivery answer through the existing test
seam, because they would otherwise have passed only on Windows/Linux runners while the repo also
builds Apple artifacts; and a 300-query concurrent AAAA burst asserts that the gate stays local under
load. The new exported type in `option` was also checked against the generated-schema contract:
regenerating the schema produces a byte-identical `docs/schema.json`, and the demand summary is not
part of the reachable option graph, so no regeneration is owed.

## Closeout round 03: the two cross-cutting gaps, closed

**Apple is delivered, pinned and enabled.** The tunnel-settings defect was fixed in the client
repository and pushed: `sing-box-for-apple` `6e559a4` on `main`, `git diff -w c001d59 6e559a4` being
six added lines and re-indentation, `swiftc -parse` exit 0. The core's `clients/apple` gitlink now
points at that revision, `scripts/ci/check-libbox-abi.sh` records the move and its `VERIFIED_APPLE_SHA`
matches, and `entryPresentmentDeliverable()` admits macOS, iOS and the tvOS flavour through the same
positive allowlist that already covered Windows and Linux/Android. Every dual-stack configuration is
untouched: the guard only fires when the core sends no IPv6 address at all.

**Multiple TUNs share one decision, and no interface can roll it back.** `box.New` pre-checks every
TUN with the same predicate the reduction uses (`option.TunReducibleToIPv4Only`), the manager commits
"suppress" only if all of them pass, and the TUN-side correction API is gone: the manager is the only
writer, and an interface that cannot apply a committed suppression logs an error and leaves the
shared value alone. The order-dependent split the round-02 report carried as a risk - first interface
stripped, second rolling the decision back to "present" - can no longer be produced, and
`TestMultipleTunInboundsShareOneDecision` runs both orders over eight configurations while
`TestTheBoxPredicateAndTheReductionAgree` pins the shared predicate against the reduction.
### Open risks (not fixed, and why)

| risk | impact | mitigation today | shortest next step |
|---|---|---|---|
| the entry decision is latched at start | a box that starts on an IPv4-only network keeps an IPv4-only interface after moving to dual-stack, until restart | the decision is a property of the interface that was opened. Re-deriving it WITHOUT re-addressing that interface is not a safe fix: the TUN would carry no IPv6 while the DNS answer presented AAAA, and those connections would leave through the physical interface - a bypass of the proxy, not merely a slower failure. The reverse direction is harmless: a TUN that still carries IPv6 captures the traffic, the dial gate refuses direct destinations fast, and proxied IPv6 keeps working | a platform update or an ordered inbound rebuild that re-addresses the interface, then re-derive and re-commit. No platform in this tree exposes one, and this environment cannot verify a TUN re-open on any device |
| non-Fake-IP proxied real-IPv6-only targets | an application can no longer reach them through the proxy when the entry suppressed AAAA | only fires when the machine has no non-link-local IPv6 at all; Fake-IP configurations keep the capability through the synthetic IPv4 address | per-route DNS decisions would need the route result at DNS time, which is the double evaluation this round forbids |
| applications with their own DoH / hard-coded IPv6 literals | not gated | the OS-level IPv4-only interface makes such attempts fail immediately | out of scope: MITM/DoH interception is explicitly not wanted |
| `dns_mode: native` / `disabled` TUNs | application DNS does not traverse the router, so the gate does not fire | the interface is still IPv4-only, so v6 attempts fail at the OS | documented boundary, not a defect |
| another Box's TUN in the same process | a new Box's startup observation can count that interface as underlay IPv6, because its monitor does not own it | the error can only cause "present", never a suppressed interface, so it cannot strip a working family; this is the same limitation the dial capability has always had | a process-level interface-ownership registry; deliberately not added for an edge that a client restart already resolves |