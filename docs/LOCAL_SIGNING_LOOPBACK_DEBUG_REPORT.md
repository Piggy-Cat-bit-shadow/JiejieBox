# Local Signing Loopback — debug report

Debug pass over the loopback system only. Baseline: the feature commit
`3aef9b341b4` (core), Apple `49be241`, sing-tun `e3605a4`. Debug head:
`4730e819be9` (core), Apple `36f36e7`, sing-tun `2bc6adb`. Branch:
`feat/ios-local-signing-loopback`.

Scope: the switch → preference → start options → override → TUN → libbox getters → NE routes →
reflection → off/reload/platform isolation chain. Nothing outside it was modified.

## 1. Findings

| # | Finding | Class | Where |
|---|---|---|---|
| F-1 | The reflection compared the packet's destination **as spelled**, so a packet carrying the IPv4-mapped form of a configured address (`::ffff:10.7.0.1`) or a zoned IPv6 destination did not match. The core's merge normalises those spellings, so the two halves disagreed about what "the same address" means. | **FIXED** | sing-tun `2bc6adb` (`isLoopbackDestination`) |
| F-2 | The NE host routes were installed **unconditionally**, and `ExtensionPlatformInterface` is shared by both Apple products. A macOS configuration that already listed `loopback_address` would have gained routes it never installed before - a behaviour change caused by a feature whose switch does not exist there. | **FIXED** | Apple `36f36e7` (`#if os(iOS)`) |
| F-3 | Suspected defect: reflection swaps addresses without touching checksums, so an IPv4 packet should be dropped by the receiver. **Not a defect** - the IPv4 header checksum and the TCP/UDP/ICMPv6 pseudo-headers are all sums over both addresses, so a swap is checksum-neutral. | VERIFIED | tests in sing-tun `2bc6adb` |
| F-4 | Suspected defect: toggling the switch could apply the core but not the NE routes (half-applied). `StartedService.StartOrReloadService` closes the old instance and builds a new one, so `openTun` runs again and recomputes `includedRoutes`. | VERIFIED (code) / NEEDS_DEVICE | `daemon/started_service.go:324` |
| F-5 | `gvisor`, `mixed` and `system` do not reflect loopback traffic. Pre-existing in the fork (gVisor only routes TCP to that destination into its own stack), **not introduced by this feature**. The feature commit added an iOS-only warning naming the stack. | REMAINS | `protocol/tun/inbound.go` warning |
| F-6 | Two loopback addresses exchanging traffic would reflect each other indefinitely (the swap makes the destination a loopback address again). Not reachable with the single address this feature injects, and Hako has no guard either. | REMAINS (theoretical) | sing-tun hairpin |
| F-7 | A reload failure leaves the toggle on while the service reports an error (the alert is shown). Same pattern as every other Profile Override row; not introduced here. | OUT_OF_SCOPE | shared UI pattern |

## 2. Reproductions

* **F-1**: unit test `TestIsLoopbackDestinationUnmapsAndStripsZones` fails on `2bc6adb`'s parent;
  `TestIsLoopbackDestinationRejectsNeighboursAndOtherLoopbacks` pins that `10.7.0.2`,
  `10.7.0.0`, `10.0.0.1`, `127.0.0.1`, `fdfe:7::2`, `0.0.0.0` and `255.255.255.255` are not
  captured, and `...KeepsTheFamiliesApart` pins that an IPv4 destination cannot match an IPv6
  entry.
* **F-3**: `TestSwapKeepsTheIPv4HeaderChecksumValid` and
  `TestSwapLeavesTheTransportChecksumValid` build valid packets, swap, and re-validate. Both
  pass, which is why no checksum code was added.
* **F-5**: `go test -run 'TestIsLoopbackProtocol'` covers which transports are reflected; the
  stack itself is not exercised on this host.

## 3. Test results

| Command | Result |
|---|---|
| sing-tun `go test -run 'TestIsLoopbackDestination\|TestSwap\|TestIsLoopbackProtocol'` | **12 tests PASS** |
| sing-tun full `go test ./...` | many `TestGoKernel*` FAIL - **pre-existing and environmental**: the same test fails identically at the feature base `32134c2` in 0.00 s (it needs a real TUN/privileges this host does not have) |
| core `go test ./daemon/ ./experimental/libbox/ ./protocol/tun/` | PASS (8 + 6 + 3 feature tests included) |
| core `go build -tags <windows profile> ./...` | PASS |
| full core suite on the debug pin, `-tags <windows profile>` | 96 packages ok; the same three environment failures as the baseline (`common/tls` TLS 1.3 prerequisites, `common/tlsspoof`, `common/windivert` driver) |
| `release-freeze.py verify` | PASS (sing-tun `2bc6adb`, clients/apple `36f36e7`) |
| `check-libbox-abi.sh --pin-only` | PASS |
| iOS client build (Swift compile + full ABI gate) | PASS twice: run `41754387032` at Apple `49be241` (the feature), and run `41762575779` at Apple `36f36e7` (the debug head, including the `#if os(iOS)` route gate) |
| device behaviour | **NOT RUN — NEEDS_DEVICE** |

## 4. Hako comparison

| Aspect | Hako | Here | Status |
|---|---|---|---|
| Address set | user-editable list | one switch → `10.7.0.1`, core still supports a list | equivalent for the product decision |
| Normalisation | `Unmap()` on the destination before matching | now the same, plus zone stripping | matched (F-1) |
| Precedence | reflection before IP-rewrite rules | hairpin runs before `dispatchStage.ReturnParsed` | matched |
| Transports | TCP/UDP/ICMPv4/ICMPv6 | same, on the `go` stack | matched for that stack |
| Stacks | System / Mixed / gVisor | `go` only | **not matched** (F-5, pre-existing) |
| Checksum handling | address swap only | address swap only, now proven checksum-neutral | matched (F-3) |
| Apple routes | merged into included routes | merged on iOS, `/32` and `/128`, de-duplicated | matched, platform-gated (F-2) |

## 5. Default-OFF and platform isolation evidence

* `TestLoopbackOverrideIsInertWhenThePlatformAsksForNothing`: with no address requested, the
  parsed configuration is returned unchanged, including a user's own entries.
* `TestLoopbackOverridePreservesTheUsersOwnEntries`: a user's `10.9.0.1`, `fd00::9` survive and
  keep their order; the platform address is appended after them.
* `TestLoopbackOverrideDoesNotDuplicateAnAddressTheUserAlreadyHas`: the mapped and plain
  spellings collapse to one entry.
* The preference defaults to `false` and is deleted by `Reset Profile Override`.
* macOS: the toggle is inside `#if os(iOS)` (both the state and the row), and the route
  injection is inside `#if os(iOS)`. Windows/Android/tvOS never read the preference; the core
  merge stays a no-op when nothing is requested.
* The user's profile file is never rewritten: the address lives in the run-time override only.

## 6. Lifecycle

* Toggle → `reloadService()` → `StartOrReloadService` closes the instance and builds a new one
  (`daemon/started_service.go:324`), so the TUN inbound and therefore
  `ExtensionPlatformInterface.openTun0` run again with the current override: core and NE are
  re-derived from one source, not patched separately. Code-verified; the actual
  `setTunnelNetworkSettings` behaviour is **NEEDS_DEVICE**.
* On Demand / extension restart: the persisted start-options snapshot carries the key; a
  snapshot without the key (older build, corrupt file, empty options) reads as `false` — the
  conservative direction.
* Rapid toggling follows the existing row pattern (F-7).

## 7. What is still unknown (NEEDS_DEVICE)

1. `10.7.0.1/32` actually appearing on the utun after enabling the switch.
2. A TCP, UDP and ICMP probe to `10.7.0.1` actually being answered locally, and `10.7.0.2` not
   being reflected.
3. The SideStore Wi-Fi refresh handshake itself.
4. `setTunnelNetworkSettings` re-application on reload.
5. iPad layout with the new row, dark mode, and dynamic type at large sizes.

## 8. OUT_OF_SCOPE (seen, not modified)

* `gvisor`/`mixed`/`system` reflection (F-5) — pre-existing fork limitation, larger than a fix.
* The reload-failure toggle state (F-7) — shared with every other Profile Override row.
* The fork's `TestGoKernel*` suite failing on a Windows host — environmental, identical at the
  feature base.
* Hako's `tun.loopback-address` list UI — this product deliberately ships a single switch.

## 9. Classification

**FIXED**: F-1, F-2.
**VERIFIED**: F-3 (checksum neutrality), F-4 (reload rebuilds the instance), default-OFF
inertness, user-address preservation, platform isolation.
**REMAINS**: F-5 (non-`go` stacks, warned), F-6 (theoretical two-address loop).
**NEEDS_DEVICE**: every item in §7.
