# Upstream merge, 2026-11-03 (round 03)

This is the record of the first upstream integration after the iOS resource governor landed. It
is written so round 04 can audit the shared paths that changed, and so nobody has to re-derive
which upstream commits are still outstanding.

## 1. The actual starting point

Measured at execution time, not from a tag or a document:

| | |
|---|---|
| repository / branch | `Piggy-Cat-bit-shadow/JiejieBox` / `testing` |
| start `HEAD` | `839209536c1` (round 02 delivered) |
| upstream remote | `https://github.com/SagerNet/sing-box.git` |
| upstream ref | `upstream/testing` = **`9f1cd1ef75b`** (2026-11-03 05:44 UTC), **force-updated** during this round |
| merge-base | `c005f35300c` (2026-10-23) |
| behind / ahead, raw | 53 / 3056 |
| behind, patch-aware (`--cherry-pick`) | **18** |

The raw 53 overstates the work by exactly the amount the fork's rewritten history hides: 35 of
them are patch-equivalent to commits this fork already carries under a different SHA. The 18 are
the real list. **The upstream branch was force-updated during this session** (`e5d91fa79f6` →
`9f1cd1ef75b`), which is why the merge-base is older than the fork's own predecessor work and why
counting commits is not a measure of outstanding work here.

## 2. Decision matrix

Each row is one upstream commit from the 18. "Contained" means the behaviour is already present
in this fork's source — established by reading the current file, not by the commit being an
ancestor.

| # | upstream | subject | decision | evidence |
|---|---|---|---|---|
| 1 | `9f1cd1ef75b` | Close UDP sockets when device addresses change | **absorbed** — `4d865d1a77f` | `transport/device/stack.go` now keeps one `interrupt.Group` per family, interrupts the changed one before publishing the new address; `TestDeviceAddressChangeInterruptsUDPSockets` covers changed / unchanged / no-op |
| 2 | `1a55e9ed23e` | Forward endpoint local flows to tun | **partial** — see §3 | `adapter.JudgeHostFlow`, `NetworkManager.RegisterHostPort/HostPort` and tests are already in this tree, but no endpoint calls them: `JudgeFlow` does not consult `JudgeHostFlow`, and the MASQUE/OpenVPN/OpenConnect `judgeFlow` entry points still have the pre-commit signature |
| 3 | `ed6c781b2d3` | Fix protocol input validation | **contained** | all three sites already had the fix: `common/srs/binary.go` returns errors instead of panicking and rejects empty domain items; `protocol/tailscale/tailcat_derp.go` is byte-identical to the commit's post-image; `transport/http/server_h2.go` already rejects any 1xx other than 101 |
| 4 | `a9dfdf4279d` | Fix AnyTLS splitting UDP packets | **contained by pin** | the commit is a `sing-anytls` bump to `20260924`; this tree requires `20260928`, the same version upstream reaches four commits later |
| 5 | `3c5862c6b0c` | Remove dependency on gVisor | **contained** | the core has **zero** `github.com/sagernet/gvisor` imports and **49** `sing-tun/gtcpip` uses; `adapter/host.go` already imports `gtcpip/header`. The 11 files the commit touches that do not exist here are the VPN device stacks, which this fork carries in the `sing-openconnect`/`sing-openvpn`/`sing-wireguard` modules instead |
| 6 | `ffb08ea1baa` | Improve idle connection management | **contained for endpoints, not applicable to urltest** | `adapter.OnDemandEndpoint`, `IdleConnectionKeeper`/`IdleConnectionReleaser` and per-outbound `SetKeepIdleConnections` already exist; the `common/urltest/urltest.go` hunk cannot apply because this fork rewrote URL testing (scoped history, two layers) and has no warm-session path at all |
| 7 | `469f672e12c` | Close idle connections of unreferenced outbounds and DNS servers | **contained** | `protocol/anytls/outbound.go` already implements `InterfaceUpdated`, `CloseIdleConnections` and `IdleConnectionKeeper`; the commit's only code file is that one |
| 8 | `20fd7be5c79` | Migrate anytls into our own library | **contained** | this tree imports `github.com/sagernet/sing-anytls` and has no `protocol/anytls/client_metadata.go`; the reflection-based metadata rewrite the commit deletes never existed here |
| 9 | `b7eb47b3089` | Fully functional auto redirect for Android | **not absorbed** — see §3 | the core side is inert without the Android client's `VpnService` wiring, which lives in a different repository and cannot be validated from this host |
| 10 | `8eb4395a391` | Update naiveproxy to v154.0.8037.49-2 | **not absorbed** — see §3 | `cronet-go` is one of this fork's four deliberately split module pins (root + `all` at one SHA, every `lib/<os>_<arch>` at another, ABI-checked). The upstream bump moves both halves together and would invalidate that pairing |
| 11 | `a62f8f1f508` | Fix AnyConnect CSD completion | **absorbed by pin** — `81548759f93` | `sing-openconnect` advanced to upstream's tip |
| 12 | `04b42a81231` | Fix DNS connection setup blocking TUN packet processing | **absorbed (adapted)** — `8360bb2a9a6` | upstream goroutine'd its async transport path; this fork removed that path with the pipelining probe, so the goroutine is created in `dispatch`, the single funnel. `TestExchangeAsyncDoesNotBlockOnConnectionSetup` |
| 13 | `a0bd909429b` | Fix concurrent DNS request packing mutating shared message | **absorbed** — `bbaff7bc3a8` | five sites changed from `exMessage := *message` to `message.Copy()` |
| 14 | `606ec5f0636` | Fix F5 VPN options parsing | **absorbed by pin** — `81548759f93` | `sing-openconnect` |
| 15 | `2d662a31053` | Fix strict route without kernel IPv6 support | **absorbed by fork sync** — `81548759f93` | cherry-picked `620991c` onto the `sing-tun` fork, new pin |
| 16 | `9474fb05f4f` | Fix TUN default route with RAS connections on Windows | **absorbed by fork sync** — `81548759f93` | cherry-picked `c6ca69e` onto the `sing-tun` fork, new pin |
| 17 | `a9c21da539d` | Close UDP sockets when endpoint addresses change | **obsolete** | the file it patches, `transport/openconnect/device_stack.go`, was deleted upstream by `3ddbc045045` ("Add MASQUE support") and by the same change in this fork (`3d0eb3d762b`); the endpoint device stacks now live in the `sing-openconnect`/`sing-openvpn` modules |
| 18 | `447cb47a374` | Reauthenticate when GPST returns HTTP 200 | **absorbed by pin** — `81548759f93` | `sing-openconnect` |

Net: **5 absorbed by source change, 4 by dependency pin, 2 obsolete-or-moved, 6 already
contained, 2 deliberately not absorbed, 1 partial.** No upstream commit is unaccounted for.

## 3. The three decisions that are not "done"

### 3.1 Endpoint host flows (`1a55e9ed23e`) — partial, and the gap is real

Three of the four pieces are already in this tree: `adapter.JudgeHostFlow` with its own tests,
`NetworkManager.RegisterHostPort`/`HostPort`, and the `tun.RoutedPort` plumbing behind them. What
is missing is the wiring: `adapter.JudgeFlow` never consults `JudgeHostFlow`, and the MASQUE,
OpenVPN and OpenConnect endpoints still call their `judgeFlow` without the context the helper
needs.

So the helper is currently reachable only from its own test. That is not a behavioural regression
— nothing regressed, the feature was never finished here — but it is dead production code, and
finishing it changes which flows an endpoint accepts locally. That decision belongs to round 04,
which audits the endpoint data path with the same tests.

### 3.2 Android auto redirect (`b7eb47b3089`) — not absorbed

The commit is 21 files: `common/srs` (which this fork has modified), `daemon/instance.go`,
`experimental/libbox/{config,platform,redirect}.go` and the Android-facing platform switches. Its
entire effect depends on an Android client that passes the redirect mode down and installs the
`VpnService`-level redirect; that client is a separate repository and a device-only test surface.
Importing the core half would add a configuration field that nothing sets, and the fork's own
`common/srs` changes would have to be reconciled at the same time. Recorded for the round that
actually ships Android.

### 3.3 naiveproxy bump (`8eb4395a391`) — not absorbed

`cronet-go` is pinned as **four** modules in this fork: the Go binding (`cronet-go` and
`cronet-go/all`) at one SHA, and every native `lib/<os>_<arch>` archive at another, because the
Go-only change and the native generation were validated separately. Upstream's bump moves the
whole family together. Absorbing it would silently pair a new binding with the old archives — the
exact failure the pin comment in `go.mod` exists to prevent. Advancing it needs a native rebuild,
which is not reproducible from a Windows host.

## 4. Dependency pins after this round

| module | before | after | why |
|---|---|---|---|
| `sing-tun` (fork) | `8b9a38008705` | **`32134c2c3188`** | two upstream TUN fixes cherry-picked onto the fork's `dev`; the fork's routed-port work is preserved |
| `sing-openconnect` | `20260929151226` | **`20261103042440`** | three upstream protocol fixes; module is not forked here |
| `sing-anytls` | `20260928104022` | unchanged | already equal to upstream's tip |
| `sing`, `sing-quic`, `quic-go`, `utls`, `cronet-go` | forks | unchanged | each carries fork-only work; upstream has not moved them in a way this round can validate |
| `sing-openvpn` | `20261020061844` | unchanged | already equal to upstream's tip |

The `sing-tun` change is a fork-repo push (`Piggy-Cat-bit-shadow/sing-tun`, `dev`,
`8b9a380..32134c2`, fast-forward) plus a real pseudo-version in `go.mod` — no local `replace`
pointing at a working copy.

## 5. What was verified

See the round report for the full gate output. In this document's scope:

* `go build ./...` and `go vet` on the touched packages.
* Cross-compile of the touched packages for darwin/arm64, ios/arm64, android/arm64, linux/amd64
  (CGO off) and of the whole tree for windows/amd64.
* `go test ./dns/... ./transport/... ./protocol/openconnect ./protocol/openvpn ./common/resourceguard
  ./protocol/anytls ./service/oomkiller`.
* The `sing-tun` fork cross-builds for windows/linux/darwin/android. Its Go-kernel integration
  suite fails on this Windows host **identically at the pre-cherry-pick commit** (`8b9a380`), so
  that is an environment limitation, not a regression; the suite belongs to upstream's Linux/macOS
  CI.

## 6. Handoff to round 04

1. **Finish or delete `adapter/host.go`.** The helper and the `NetworkManager` API exist and the
   endpoints do not call them; decide with the endpoint data path in front of you.
2. **The DNS change is on the packet path.** `dispatch` now spawns a goroutine per async query and
   the DNS client's operation context owns its lifetime. Watch for a goroutine leak if a caller
   ever cancels the context before the callback (round 03 verified `dns.Client` does not).
3. **`transport/device` changed shape.** `bindAddressLocked` must be called under `stateAccess`;
   the address and the interrupt group are one observation. Any new caller must take the lock, or
   a socket can be created for a family whose group was already interrupted.
4. **`sing-tun` moved.** The two absorbed fixes touch Windows default-route selection and Linux
   strict-route rules. The iOS governor does not depend on either, but the TUN lifecycle suites
   in round 04 should run against the new pin.
5. **Do not "finish" the cronet bump** without a native rebuild; the four-module pin is a
   correctness constraint, not a convenience.
