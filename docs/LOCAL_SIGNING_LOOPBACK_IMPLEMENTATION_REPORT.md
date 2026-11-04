# Local Signing Loopback — implementation report

Feature branch: `feat/ios-local-signing-loopback` (core), `feat/ios-local-signing-loopback`
(Apple client, `49be24188141276a3711ea411212e42e79663672`). Not merged, not released.

## 1. What was built, and what is not proven

The iOS/iPadOS client has one new switch — **More → Connection Behavior → Profile Override →
Local Signing Loopback** — off by default. When on, the tunnel answers for `10.7.0.1`:

| Layer | State |
|---|---|
| Preference, reset, persistence | implemented, unit-covered by construction |
| Start options → extension → libbox override | implemented; Go half unit-tested, Swift half **compiled by CI** (run `41754387032`, iOS job, green) |
| In-memory merge into the effective TUN | implemented, **8 Go tests** |
| libbox getters for the Apple bridge | implemented, **6 Go tests** |
| NE `includedRoutes` `/32` + `/128` | implemented and compiled by the same run; not observed on a device |
| Data plane: loopback reflection | the fork already reflected **TCP**; extended to **UDP and ICMP**, **4 fork tests** |
| SideStore end-to-end refresh | **NEEDS_DEVICE** |
| macOS/Windows/Android/tvOS | no switch, nothing injected, no behaviour change |

**No device has run this.** That is the boundary of every claim below.

## 2. Reference re-check (what was verified, and against what)

* Hako `ProjectClash/Clash` @ `5365826` (v1.19.33-clash.4), `Clash-Client` @ `e3e6472`.
  Their loopback is a *list* (`tun.loopback-address`), reflected by an IP-rewrite subsystem
  (`third_party/sing-tun/ip_rewrite.go`) that runs **before** the rewrite table and mirrors the
  packet back to the platform with the two addresses exchanged. The tests cover TCP, UDP and
  ICMPv4/6, and pin that a neighbouring address is not reflected.
* Our fork `Piggy-Cat-bit-shadow/sing-tun` already had the same *mechanism* for the default
  `go` stack: `goEngine.handleLoopbackHairpin` swaps the addresses and writes the frame back.
  It was restricted to TCP, which is the gap this change closes.
* Core: `option.TunInboundOptions.LoopbackAddress` (`json:"loopback_address"`) was already
  parsed and already reached `tun.Options.Inet4LoopbackAddress`/`Inet6LoopbackAddress`
  (`protocol/tun/inbound.go`), so no new configuration field was added.
* libbox exposed no getter for those addresses and had no way to add one at run time — both
  added here.

**Licence note.** The Hako repositories are GPL-3.0. Nothing was copied: their behaviour and
test intent were read, and the implementation here is written against our own fork's existing
`goEngine` hairpin, which predates this work. No Hako file, comment or test body was
transplanted.

## 3. The data flow, end to end

```
iPhone: HakoProfileOverrideView ─┐
iPad:   ProfileOverrideView ─────┤  SharedPreferences.localSigningLoopback (Bool, default false)
                                 │
                    ExtensionProfile.prepareStartOptions()
                                 │  options["localSigningLoopback"] = NSNumber(bool)
                                 ▼
                    ExtensionProvider.applyStartOptions()
                                 │  overridePreferences.localSigningLoopback
                                 ▼
                    ExtensionProvider.startService()
                                 │  LibboxOverrideOptions.loopbackAddress = ["10.7.0.1"]
                                 ▼
                    libbox CommandServer.StartOrReloadService
                                 │  daemon.OverrideOptions.LoopbackAddress
                                 ▼
                    daemon.applyTUNOverrides        ← the merge point (tested)
                                 │  first TUN only; user entries preserved; normalised; deduped
                                 ▼
             option.TunInboundOptions.LoopbackAddress → tun.Options.Inet4LoopbackAddress
                                 │
                    ┌────────────┴─────────────┐
                    ▼                          ▼
        goEngine.handleLoopbackHairpin   libbox.GetInet4LoopbackAddress
        (reflects TCP/UDP/ICMP)          (Apple: NEIPv4Route 10.7.0.1/32)
                    │                          │
                    └──────── device ──────────┘
```

The two consumers of the same list — the core's matcher and the NE's host route — are what make
the feature work at all: without the route the packet never reaches the tunnel, and without the
reflection the tunnel proxies a packet that was meant for the device itself.

## 4. Files and why

**Core (Go)**

| File | Change |
|---|---|
| `daemon/instance.go` | `OverrideOptions.LoopbackAddress`; the TUN patch is now `applyTUNOverrides` + `mergeLoopbackAddresses`. The merge normalises (unmap, strip zone), de-duplicates, preserves the user's order and entries, and returns the configuration untouched when the platform adds nothing. |
| `daemon/instance_override_test.go` | 8 tests: inert when off, append when on, user entries preserved, mapped/zoned normalisation, no duplicate, no TUN, two TUNs (first only), other overrides unchanged. |
| `experimental/libbox/tun.go` | `GetInet4LoopbackAddress`/`GetInet6LoopbackAddress` on `TunOptions`, returning normalised strings; invalid addresses dropped. |
| `experimental/libbox/tun_loopback_test.go` | 6 tests, including that `::ffff:10.7.0.1` is reported as `10.7.0.1` (the spelling the core matches). |
| `experimental/libbox/command_server.go` | `OverrideOptions.LoopbackAddress StringIterator`; unparseable entries are skipped rather than failing the start. |
| `go.mod` / `test/go.mod` / `release/source-lock.json` | sing-tun pin `32134c2c3188` → `e3605a43aff2`. |

**sing-tun fork (`e3605a4`)**

| File | Change |
|---|---|
| `stack_go_engine.go` | `isLoopbackProtocol` (TCP, UDP, ICMPv4/v6) and `swapLoopbackAddresses` extracted; the hairpin now reflects all four. |
| `stack_go_loopback_test.go` | 4 tests: protocol coverage, IPv4 and IPv6 exchange-only-addresses, truncated frames ignored. |

**Apple client (`49be241`)**

| File | Change |
|---|---|
| `Library/Database/SharedPreferences.swift` | `localSigningLoopback`, default false; included in `resetProfileOverride()`. |
| `Library/Network/ExtensionProfile.swift` | sends the key in the existing start-options dictionary (iOS only). |
| `Library/Network/ExtensionProvider.swift` | reads it into `OverridePreferences`, exposes it in platform metadata, and sets `LibboxOverrideOptions.loopbackAddress` when on; the address constant lives here. |
| `Library/Network/ExtensionPlatformInterface.swift` | appends `/32` and `/128` host routes from the core's getters, de-duplicated against the configured routes; the IPv6 block is inside the `ipv6Address.isEmpty` guard so no route is installed for a family the tunnel cannot deliver. |
| `ApplicationLibrary/Views/HakoStyle/HakoProfileOverrideView.swift` | iPhone row, after Exclude APNs, before Reset. |
| `ApplicationLibrary/Views/Setting/ProfileOverrideView.swift` | iPad row, `#if os(iOS)` so macOS never shows it. |
| `Localizable.xcstrings` | `zh-Hans`/`zh-Hant` for the title, the Hako footnote and the iPad footnote. Other languages fall back to English rather than to a machine translation. |

## 5. Platform matrix

| Platform | Switch | Injection | Behaviour change |
|---|---|---|---|
| iPhone | yes (Hako UI) | only when on | none while off |
| iPad | yes (upstream UI) | only when on | none while off |
| macOS | **no** (`#if os(iOS)`) | never (key defaults false) | none |
| tvOS | no | never | none |
| Windows / Android | no | never | none |

The cross-platform `loopback_address` configuration field is untouched: a user who writes it in
their own profile still gets exactly what they wrote, on every platform.

## 6. Tests actually run

| Command | Result |
|---|---|
| `go test ./daemon/` | ok (includes the 8 new override tests) |
| `go test ./experimental/libbox/` | ok (includes the 6 new getter tests) |
| `go test ./protocol/tun/ ./option/` | ok |
| `go build -tags <windows profile> ./...` | ok |
| `python scripts/ci/release-freeze.py verify` | PASS — every pin matches the lock |
| `check-libbox-abi.sh --pin-only` | passes with the updated verified SHA (see §8) |
| sing-tun fork `go test -run 'TestIsLoopbackProtocol|TestSwapLoopback'` | ok (4 tests) |
| iOS client build (Swift compile + unsigned IPA) | **PASS** — [run 41754387032](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/41754387032), artifact `jiejiebox-ios-arm64-0.1.37-73cf43e84-unsigned.ipa`, apple `49be241` |
| **UI tests / simulator** | **NOT RUN** |

## 7. Prompt requirements this design deliberately does not satisfy (honest gaps)

1. **The data plane covers the default `go` stack only.** `mixed` routes TCP through the `go`
   engine and ICMP through gVisor; the gVisor path (`stack_gvisor_filter.go`) sends TCP to a
   loopback destination into the gVisor stack instead of reflecting it, and has no UDP/ICMP
   handling. The `system` stack has no loopback handling at all. A configuration that selects
   `stack: gvisor` or `system` will not reflect, so on iOS `protocol/tun` now logs a warning
   naming the stack when the address is configured on one of them - the alternative is a switch
   that looks enabled and does nothing. Closing the gap itself needs a hook in each stack and
   cannot be validated without a device, so it is not done blind here.
2. **The Swift is compiled, not exercised.** CI built the client at `49be241` (both UIs, the overlay, the ABI gate), which proves it compiles and links against the new libbox; no screen of it has been shown and no switch of it has been thrown.
3. **No device run.** The SideStore refresh scenario, the NE route actually appearing in
   `scutil`/`ifconfig` on iOS, and the reflection actually satisfying the local service are all
   `NEEDS_DEVICE`.
4. **hako-equivalence is partial.** TCP/UDP/ICMP reflection on the default stack matches their
   behaviour; the multi-stack coverage and their IP-rewrite interaction do not.

## 8. Version governance and gates touched

* libbox gained two interface methods. `VERIFIED_APPLE_SHA` moves to the Apple feature commit,
  which is the documented procedure when the client moves deliberately; the migrated-method
  contract (`docs/fork/libbox-abi-contract.tsv`) is unaffected because the new getters return
  iterators, not `StringBox`.
* The sing-tun pin moved in three places (root, `test/`, source lock) and the freeze verifier
  passes.

## 9. Rollback

| Change | Undo |
|---|---|
| Core | revert the branch; the pin returns to `32134c2c3188` |
| Fork | `32134c2` still exists on `dev`'s history; repin to it |
| Apple | branch `feat/ios-local-signing-loopback`; the gitlink returns to `9f445fb` |

## 10. Handoff to the next debug round

* **Entry points**: `daemon.applyTUNOverrides` (merge), `goEngine.handleLoopbackHairpin`
  (reflection), `ExtensionPlatformInterface.openTun0` (host routes).
* **Most likely to bite**: the interaction between the loopback address and
  `route_exclude_address`/`excludeDefaultRoute`; a user profile that already lists the address;
  a reload that races the switch; and the `mixed`/`gvisor` stacks, which are not covered.
* **Reproduce**: enable the switch, `ifconfig`/`scutil` should show `10.7.0.1/32` on the utun;
  a TCP, UDP and ICMP probe to `10.7.0.1` must be answered locally and a probe to `10.7.0.2`
  must not be reflected.
