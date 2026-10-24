# Phase 04 — Cronet native matrix, Apple TLS leak, Android procfs and IPv6 hosts

```text
PHASE = 04/06
RESULT = PARTIAL_WITH_SPECIFIC_BLOCKER
CRONET_MAIN_SHA = 8aa139e9e140d5737f82deaa934b81b68054712b
CRONET_ABI_MATRIX = root/all 8aa139e; 29 lib modules 8c68ce8; 37 native assets byte-identical to the df0c319 build; no cgo/include change
MAIN_EFFECTIVE_NATIVE_PINS = root/all v0.0.1-143.0.7499.109-2.0.20261024085011-8aa139e9e140 / v0.0.0-20261024085011-8aa139e9e140; 29 libs v0.0.0-20260929202119-8c68ce89873c (root and test)
APPLE_TLS_LEAK = CLOSED_CODE_AND_MACOS: F_DUPFD_CLOEXEC, deferred anchor release, ObjC ownership rewrite; 30 success + 10 failure cycles keep fd count flat
ANDROID_PROCFS = CLOSED_CODE_AND_LINUX_UNIT: connection-owner fallback moved to connection_owner_android.go; procfs ranking/format tests pass; android/arm64 cross-compile passes
IPV6_HOST = CLOSED_CODE_AND_UNIT: DNS HTTPS/HTTP3, DERP mesh and v2ray host paths bracket IPv6 literals; domain/IPv4 and port cases covered
CI_FINAL_CODE_SHA_AND_PLATFORM_LIMITS = 3ddb0e866: Verify 40081589236, Android 40081589137, Mobile 40081589215, apple-tls-verify 40081595184, linux-platform-verify 40081597501 all SUCCESS; no iOS/legacy-Android device
NEXT_PHASE_READY = YES
```

## 1. Cronet atomic matrix

`cronet-go/main` was fast-forwarded from `049f290` to the fork's 13-commit feature tip `fd2f258`
(per-CONNECT naive transport, buffer pinning, early-copy growth, codec parity), then `8aa139e`
regenerated the `all` module against that root and the native lib generation.

| component | pin | why |
|---|---|---|
| root | `8aa139e` | includes all Go-only naive fixes |
| `all` | `8aa139e` | imports the 29 lib modules, go.mod points at root `8aa139e` and libs `8c68ce8` |
| 29 `lib/<os>_<arch>` | `8c68ce8` | the commit carrying the native archives |

Evidence that this is one ABI generation rather than a fake pin:

- 37 native `.a`/`.so`/`.dll` blobs are **byte-identical** between `df0c319` (the commit the archives
  were built from) and `8c68ce8`; zero mismatches.
- No file under `include/` and no cgo file changed between `df0c319` and `8aa139e`; the only
  differences are Go-only `naive_*` code/tests.
- Root and `test` replace all 29 lib paths; `check-go-module-integrity.sh` passes 36/36 root
  replaces and 31 cronet paths in parity.
- Real native binding: the Windows purego build with `libcronet.dll` from the `windows_amd64` lib
  module passes `TestNaiveSelf`, `TestNaiveSelfECH`, `TestNaiveSelfInsecureConcurrency` and
  `TestNaiveSelfQUIC` (HTTP/2 and HTTP/3). `TestNaiveSelfQUICCongestionControl` passes BBR, Cubic
  and Reno; BBR2 is rejected at configuration decode by the existing supported set, independent of
  Cronet.

Not done: the archives were not rebuilt from `8aa139e`. Rebuilding needs the Chromium/naiveproxy
toolchain and hours of native builds; the byte-identical proof is what the window can establish.
`cronet-go` Actions has no runs, so this is local evidence.

## 2. Apple TLS leak (`42cae14c6`)

Ported upstream's change:

- duplicate the socket with `unix.FcntlInt(fd, F_DUPFD_CLOEXEC, 0)` only after the anchor lookup,
  and defer the anchor release instead of releasing it on one path only;
- ObjC bridge: one `@autoreleasepool` around the whole create path, `NW_RETURNS_RETAINED` on the
  connection factory, release `anchors`/semaphore/queue/connection on every failure and cancel path.

The injected test keeps one listener and performs 30 successful and 10 failing handshakes; the
process fd count (counted with `F_GETFD` over the descriptor table) must return to its warm baseline.
Both tests pass on the macOS arm64 gate (run 40081595184), together with the pre-existing ALPN,
version-mismatch, server-name and recovery tests. `TestWindowsClient*TLS13` fails identically on the
phase-03 baseline; it is a pre-existing Windows environment issue.

Not verified: iOS device entitlements/NetworkExtension. macOS uses the same Network.framework code
path but is not a device.

## 3. Android legacy procfs (`40bc411e8`)

Ported the upstream split: `FindConnectionOwner` + `procfs.ResolveSocketByProcSearch` now live in
`connection_owner_android.go`; `service.go` no longer carries the lookup. The table search ranks an
exact local+remote row above a local-only UDP row and above the unspecified-address row, skips rows
with zero inode/uid, and handles the IPv4-in-IPv6 table fallback.

Tests: `procfs_test.go` pins the native-endian address + big-endian port spelling for IPv4/IPv6
loopback, unspecified and public addresses; the Linux platform gate runs it and cross-compiles the
package and `experimental/libbox` for android/arm64. The existing implementation does not scan all
users' sockets; it searches only the current `/proc/net` tables for the flow's tuple.

Not verified: a real legacy Android device, cross-user permissions and `app_process*` wrappers. Those
are listed as device gaps, not claimed.

## 4. IPv6 literal hosts (`42f487bd0`)

Ported the upstream change across DNS HTTPS/HTTP3, DERP mesh URLs, REALITY fallback, v2ray HTTP and
HTTP-upgrade. URL hosts now come from `M.ParseSocksaddrHostPort`; an IPv6 literal is bracketed when
no explicit non-default port is present, and domain/IPv4 spellings are untouched. HTTP/3 also sets
`NextProtoH3` on the TLS config and uses the same config for the QUIC dial instead of a nil copy.

Tests: `httpsDestinationHost`, `http3DestinationHost` and `derpMeshHost` tables cover domain, IPv4,
IPv6, zero/443/non-443 port; the Linux platform gate passes. A real IPv6-only resolver e2e was not
available on this host.

## 5. CI and remaining limits

- Main code `3ddb0e866`: Verify `40081589236`, Android Core ARM64 `40081589137`, Mobile contract
  verification `40081589215` all SUCCESS.
- `apple-tls-verify` `40081595184` SUCCESS (macOS arm64 + both-arch build).
- `linux-platform-verify` `40081597501` SUCCESS (procfs, IPv6 helpers, Android cross-compile).
- Blockers, all environmental and recorded rather than hidden:
  1. Cronet native archives were not rebuilt; ABI is proven by byte-identical blobs and unchanged
     cgo/include, not by a fresh native build.
  2. No iOS/legacy-Android device: those layers are `NOT_VERIFIED_ON_DEVICE`.
  3. `TestWindowsClient*TLS13` is a pre-existing Windows failure, reproduced on the phase-03
     baseline.

The next phase is `DEP-TAILSCALE-1`; no Tailscale production change was started here.
