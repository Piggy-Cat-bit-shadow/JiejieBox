# Adaptive IPv6 at the entry layer

**Scope:** the application-facing half of the address-family work. The dial-side gate
(`netcap.HardUndialable` through `common/dialer`) already exists and is unchanged; this document
describes the layer in front of it, which decides what the process *tells applications* about IPv6.

Status: implemented and unit-tested on `testing`. Platform clients (Apple, Android) were **not**
built or run - see [Limits](#limits-and-what-was-not-verified).

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
`Inet6RouteExcludeAddress`, `Inet6LoopbackAddress`, `Inet6Gateway` and any IPv6 `DNSAddress`. Two
refusals keep the configured options untouched and correct the decision back to "present":

1. no IPv4 address - an interface must keep at least one address;
2. an explicit `dns_address` list with no IPv4 entry - applications would have no resolver.

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

**Darwin (Network Extension, `ExtensionPlatformInterface.swift` `openTun0`): cannot, and is
therefore disabled for now.** At the pinned SHA (`clients/apple` = `c001d59`) the client creates
`NEIPv6Settings` and a default `::/0` route **unconditionally** inside `if options.getAutoRoute()`.
A core that stripped IPv6 addressing would hand it an IPv6 default route with no address - an
interface that claims the family and cannot carry it - or settings the system rejects. So
`NetworkManager.entryPresentmentDeliverable()` answers false on Darwin, nothing is committed, and the
previous behaviour is kept there.

The deliverability answer is a **positive list** - Windows and Linux (which `C.IsLinux` extends to
Android) - and not `!IsDarwin`: the Apple client family builds GOOS values that this tree's
`constant.IsDarwin` does not enumerate (tvOS and others), and an unknown client contract belongs on
the fail-open side. Anything not on the list keeps the previous behaviour.

The client-side fix is prepared and verified to apply against the pinned tree:
`docs/fork/ipv6-entry-apple.patch` wraps that block in `if !ipv6Address.isEmpty { ... }`. It is
deliberately **not** pushed to the client repository and the `clients/apple` gitlink is **not** moved:
that is a coordinated release action (client source authority, exact-SHA pinning, artifact
provenance), and no Apple build was run here. When the patched client is what ships, the flip is one
line: `entryPresentmentDeliverable()` returns true unconditionally (the platform allowlist at the
bottom of that method), and the Darwin entry decision becomes live.

## Limits and what was not verified

| item | status |
|---|---|
| `common/netcap`, `route`, `dns`, `protocol/tun`, `option` unit tests | `PASS` (see the commit message for the exact commands) |
| Windows/Linux route readers (reused, unchanged) | `PASS` as existing tests |
| Android `VpnService.Builder` receiving no IPv6 address/route | **VERIFIED_SOURCE** against `satelite-one-main/app/src/main/java/com/interstellar/proxy/bg/VPNService.kt` and `experimental/libbox/tun.go` + sing-tun `BuildAutoRouteRanges`; **no** Gradle/AAR/emulator run (`NOT_RUN_BY_REQUEST`) |
| Apple `NEIPv6Settings` with an empty address list | **VERIFIED_SOURCE** against `sing-box-for-apple` `c001d59` `Library/Network/ExtensionPlatformInterface.swift:122-161`: it is unconditional, so the core keeps IPv6 presented on Darwin and a ready patch ships in `docs/fork/ipv6-entry-apple.patch`; no Xcode/iOS/macOS build (`NOT_RUN_BY_REQUEST`) |
| an application with its own DoH or hard-coded IPv6 literal | not covered by design - the OS-level IPv4-only interface makes such attempts fail immediately, which is earlier than any DNS interception |
| a network change flipping the decision | not applied until restart, by design (see the trade above) |

## Files

| file | role |
|---|---|
| `common/netcap/entry.go` | address-shape classification, projection, presentment type |
| `adapter/network.go` | `IPv6EntryPresentmentReader`, `IPv6EntryPresenter` (optional interfaces) |
| `route/network_family_entry.go` | read-only observation, commit, reader, platform deliverability |
| `route/network.go` | the committed pointer and the demand flag |
| `option/ipv6_entry.go` | configuration demand summary |
| `protocol/tun/effective_ipv6.go` | effective-option surgery and the route-set append |
| `protocol/tun/entry_ipv6.go` | the TUN's consumption and its one correction |
| `dns/entry_ipv6.go` | the application-facing AAAA gate and its NODATA response |
| `docs/fork/ipv6-entry-apple.patch` | the Network Extension client's minimal condition, against `clients/apple` `c001d59` |

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
| the deliverability answer was `!IsDarwin` | the Apple client family builds GOOS values `constant.IsDarwin` does not enumerate (tvOS, ...) | the answer is now a positive allowlist: Windows and Linux/Android, which are the delivery contracts that were read and verified |

The tests added by the round were checked against mutations of the production code - the DNS gate
disabled, the route-set IPv6 prefix re-enabled, the commit-site deliverability guard dropped, the
projection forced to suppress, the loopback case removed, the proxy-only fast path disabled, the
demand predicate un-mapped - and each mutation turns at least one of them red.

### Open risks (not fixed, and why)

| risk | impact | mitigation today | shortest next step |
|---|---|---|---|
| the entry decision is latched at start | a box that starts on an IPv4-only network keeps an IPv4-only interface after moving to dual-stack, until restart | the decision is a property of the interface that was opened; the dial gate stays live, and the alternative would let DNS present an IPv6 the interface does not carry (I-05) | re-address/reopen the TUN through a platform update or an ordered inbound rebuild, then re-derive and re-commit; needs platform work this round cannot verify |
| the Apple client patch is not shipped | Darwin keeps the previous behaviour (feature inert there) | `entryPresentmentDeliverable` is false, so nothing can break | ship `docs/fork/ipv6-entry-apple.patch` with the client and drop the allowlist entry |
| non-Fake-IP proxied real-IPv6-only targets | an application can no longer reach them through the proxy when the entry suppressed AAAA | only fires when the machine has no non-link-local IPv6 at all; Fake-IP configurations keep the capability through the synthetic IPv4 address | per-route DNS decisions would need the route result at DNS time, which is the double evaluation this round forbids |
| applications with their own DoH / hard-coded IPv6 literals | not gated | the OS-level IPv4-only interface makes such attempts fail immediately | out of scope: MITM/DoH interception is explicitly not wanted |
| `dns_mode: native` / `disabled` TUNs | application DNS does not traverse the router, so the gate does not fire | the interface is still IPv4-only, so v6 attempts fail at the OS | documented boundary, not a defect |
