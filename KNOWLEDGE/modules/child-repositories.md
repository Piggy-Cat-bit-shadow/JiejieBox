# Child repositories and forked dependencies

## Scope and how to read the revisions

Client repositories and forked Go dependencies are part of the kernel knowledge scope. The parent
source target is `dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07`; the pins below are what that tree
consumes, not a claim about every later child change or every release artifact. On 2026-10-29 the
observed Apple, Android, Desktop, Dashboard and uTLS `main` tips matched the respective pins below.
Record independent branch movement on every refresh even if the parent has no diff. `REFRESH.md` §25A
specifies discovery, comparison, evidence and safe inspection. Exact historical parent tree entries:
`git ls-tree <parent-sha> clients/apple clients/android clients/desktop`; Go pins are `replace`
directives, not `require`; Dashboard is a gitlink in Desktop, not a parent gitlink.

| Source | Previous pin at `cb71cf80d72` | Current parent/owner pin | Consumed as |
|---|---|---|---|
| [Apple](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-apple) | `3aedd7417488e33a69fe2d46dc34539780d76157` | `044e6d5447eb1285c950bd12cee10effab641f5a` | parent client gitlink; manual Apple build resolves its own source once |
| [Android](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-android) | `9d15717a85c5d995f4179457b48213b5c73b511d` | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | parent client gitlink; product branch now `main` |
| [Desktop](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-desktop) | `eb2dbf8ba7de0bb4f37e524dada0f30c38417282` | `6d4ccf8c5cc6dd0749cfee7822f8de45eab4f40b` | parent client gitlink |
| [Dashboard](https://github.com/Piggy-Cat-bit-shadow/sing-box-dashboard) | `d452c24` | `5e9271e516e437b001dbb51c4258441490b82318` | nested Desktop gitlink, reported not rewritten by parent freeze |
| [sing-tun](https://github.com/Piggy-Cat-bit-shadow/sing-tun) | `147ab164c5ae` | `8b9a380087059f759441c404e26061be218dfa64` | root and test Go replacements |
| [uTLS](https://github.com/Piggy-Cat-bit-shadow/utls) | `1bcb8e4d74d2` | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` | root and test Go replacements |

The rest of the actual dependency inventory is `release/dependencies.json` and `release/source-lock.json`:
`sing`, `sing-quic`, `quic-go`, Cronet root and aggregate plus 29 controlled native modules. A manifest
expresses desired branch/policy; the lock projects actual pins. It is not evidence that an artifact
contains those bytes. Look at Go modules and source lock rather than assuming every dependency is a
Git submodule. See `ci-release.md` and `build-pruning-release.md`.

## Apple: hotspot MTU, subscription body and build source

At `16cf87b4`, `Library/Network/ExtensionPlatformInterface.swift` sets TUN MTU only on the auto-route
path: a non-positive requested value becomes 1500; when `includeAllNetworks` is enabled, values over
1500 are clamped to 1500 for hotspot forwarding. After installing settings it logs the settings
summary. This is source behavior, not a real iOS hotspot measurement. `Library/Network/HTTPClient.swift`,
`RemoteProfileFetcher.swift` and `ApplicationLibrary/Views/Profile/NewProfileViewModel.swift` make
subscription-body import/update use `sing-box/1.15.0`; the general application UA is separate, as
are quota compatibility probes. Recheck actual call sites rather than replacing every UA.

Manual Apple workflow selection can resolve a different commit from the gitlink; exact input and
single-macOS-runner rules are in `ci-release.md`. Historical candidate fixes for symlink targets are
not evidence that the pinned `16cf87b4` tree includes them. A POSIX checkout and exact target blob
bytes are needed to verify links; Windows text materialization is insufficient. No Apple build or
physical hotspot test was performed in this refresh.

### Unconsumed Apple candidate — independent branch snapshot

The upstream integration is MERGED as of 2026-10-29: product `main`, the parent gitlink and
`VERIFIED_APPLE_SHA` are all `044e6d5447eb1285c950bd12cee10effab641f5a`, the candidate branch
`sync/upstream-2026-10-28` has been deleted, and the macOS client target compiled from that commit on
a real arm64 macOS runner (run 40776674407). The paragraph below describes what that merge carried. The actual tree diff has 21
paths. Its `CommandXPC.swift` routes endpoint registration into `UserServiceClient` and requests a
Wi-Fi refresh when required; `UserServiceXPC.swift` swaps connections under a lock and invalidates the
old connection, while its invalidation callback clears only the same connection. The publisher uses
`systemExtensionMachServiceName`; the old endpoint registry is removed. Other candidate changes
include the share extension, refreshed Tailscale peer views, update authorization and removal of a
duplicate startup alert in the upstream component. Inspect the candidate tree for exact platform
conditions; do not generalize those changes to the pinned client.

Two candidate commits correct `Tests/SubscriptionQuotaCaption/Sources/Caption/` symlink target
paths and then remove unintended CRLF bytes from the target blobs. Git tree mode is `120000`.
Check raw blob size and target bytes in a POSIX-capable checkout; an apparent Windows text-file
success is not symlink resolution. The candidate's source diff was inspected; its macOS build,
full ABI scan and device behavior were not verified here. Re-check its branch and parent gitlink
before calling any candidate change consumed or released.

## Android: service ordering, profile application and source convergence

The direct tree diff `9d15717a..c7837107` spans 43 files and includes merged development history;
ancestry is not an authorship count. In `app/src/main/java/io/nekohasekai/sfa/bg/BoxService.kt`,
startup rebuilds service mode, awaits `Application.application.setupLibbox()` before starting the
command server, and discards unfinished power-report drafts instead of promoting them. In
`compose/screen/dashboard/DashboardViewModel.kt`, `applySelectedProfile` reloads when the detected
service mode matches, otherwise stops, waits for stopped status with a bounded loop and requests a
start; a changed selected remote profile uses the same path. Subscription-body requests use
`HTTPClient.SUBSCRIPTION_USER_AGENT` (`sing-box/1.15.0`), separate from general app identity.
`version.properties` in the pinned client records 747 / `1.15.0-alpha.13` / `go1.26.10`; these are
client metadata, not the parent release version. Platform auto-redirect, package-list and network
changes also occur in this range: inspect the affected source for a task that touches those paths.
Pin-only ABI passed locally; no Android app build or device run was performed here.

## Desktop and nested Dashboard

Desktop `eb2dbf8..6d4ccf8` changes `src/main/profiles.ts`, `src/shared/subscription.ts`, the
subscription fetch test, and its `dashboard` gitlink. `remoteProfileRequest` uses the same stable
`sing-box/1.15.0` for subscription bodies; the separate app identity and quota probes remain
separate. GitHub's compare for Dashboard `d452c24..5e9271e` lists only
`.github/workflows/ci.yml` and `.github/workflows/deploy.yml`: this is a deployment workflow change,
not a source diff in the embedded web UI. Correct dependency update order is Dashboard → Desktop →
parent; changing a parent lock cannot write the nested gitlink. The Desktop checkout was absent;
remote compare and patches were inspected, but no Electron build/test was performed here.

## sing-tun routed port and uTLS

At `8b9a3800`, sing-tun `stack_go.go` implements `RoutedPort`: it captures the first configured
IPv4/IPv6 addresses, attaches/detaches the dispatcher return path and feeds packets into its running
Go stack. `stack_go_engine.go` invokes `ReturnParsed` after loopback hairpin, before the UDP socket
path. The fork adapts upstream's routed-port capability to its existing return machinery rather
than importing a second return implementation; `flow_dispatch_routed_port_test.go` is the child
regression anchor. The parent `JudgeHostFlow` boundary is in `masque-connectip.md`. Parent adapter
unit tests do not independently prove real device delivery or all child stack interleavings.

uTLS `1bcb8e4d..b63a2e9a` integrates a QUIC handshake build-error cleanup path, hashes the ECH
inner ClientHello for early-data transcript when ECH is active, and compresses ECH ALPN only for
standard ClientHello order (`outerExts == nil`). The child contains regression tests
`u_quic_build_error_test.go` and `u_ech_alpn_test.go`; this refresh inspected the child source diff,
not a fresh child test run. These changes are relevant to the core's pinned TLS behavior but do not
prove interoperability with every server or platform.

## Evidence and next refresh

Parent Gitlinks were read from the committed tree, not the older local Apple/Android checkouts; child
objects were fetched without changing those worktrees. Desktop and Dashboard comparisons used the
GitHub API; sing-tun and uTLS source patches were inspected there. Compare any new child source and
branch tip against the last observed pin and tip; classify unconsumed branch work separately and do
not move a parent baseline just because a child head moved. The previous calibration's platform and
ABI limits remain applicable. A checkout, `SKIPPED` scan, commit message or reported historical test
is not a current build or device result.
