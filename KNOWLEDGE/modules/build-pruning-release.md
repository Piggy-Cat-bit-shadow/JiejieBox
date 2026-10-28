# Build, pruning and release profiles

## Protocol registration and build gates at the testing target

**VERIFIED_SOURCE** at `011f3fdcd99`: `include/registry.go` registers TUN, redirect, SOCKS,
HTTP, Shadowsocks, Snell, VMess, Trojan, Naive inbound, VLESS, AnyTLS and Mieru; outbound
registration includes selectors, URLTest and load balance, plus those applicable protocols.
MASQUE is an endpoint. `include/quic.go` registers Hysteria, TUIC and Hysteria2 under `with_quic`;
`include/naive_outbound.go` registers Naive outbound only under `with_naive_outbound`. The tracked
`release/DEFAULT_BUILD_TAGS` includes that tag, while `DEFAULT_BUILD_TAGS_OTHERS` does not. Read
the current profile and stub files for a particular target: a registration or option type alone is
not proof a distributed binary contains the implementation, nor proof of interoperability.

The outbound registry also includes `bridge`: it implements L3 flow forwarding through a
platform backend, not ordinary `DialContext` or `ListenPacket` (both return an error). Its
pre-match rejects local destinations; the platform backends and unsupported-platform stub are in
`protocol/bridge`. Linux's backend reads the egress interface MTU, which is not proof of a whole
path MTU. The OpenVPN client/server and OpenConnect endpoints and their DNS transports are
registered via `with_openvpn` and `with_openconnect` respectively; opposite-tag stubs return an
actionable build error. Their presence in the registry does not prove a particular profile includes
them. Endpoint-owned DNS resolver retirement is discussed in `core-lifecycle-runtime.md`. These
are source boundaries, not live VPN or bridge interoperability evidence.

### TLS engines and ECH retry

`common/tls/client.go` selects Apple or Windows engines when configured, otherwise Go's engine,
Reality, or uTLS; `common/tls/utls_client.go` requires `with_utls`. The default TLS dialer closes
the failed connection and retries once with a server-provided ECH retry config only when the error
is `ECHRejectionError` with a retry list and the selected config supports ECH. The one-retry flag
prevents an unbounded retry loop. Reality and uTLS share the first-flight transform helper in
`common/tls/utls_client.go`; inspect the selected engine before assuming feature parity. These are
`VERIFIED_SOURCE` facts at `011f3fdcd99`, not a claim that every platform or server was exercised.

`NewUTLSClient` rejects ECH with a fingerprint outside `utlsFingerprintSupportsECH`'s explicit
allowlist, including the `random` spelling, before constructing the client. The historical full
audit's claim that these combinations silently proceed predates this gate. This is a configuration
guard, not wire-level evidence for every allowed preset or server.

The Go module's `replace` directives, not `require` versions alone, select the forked `sing`,
`sing-tun`, `sing-quic`, `quic-go`, `cronet-go` and `utls` source. Resolve the actual pseudo-version
in `go.mod` and compare the nested test module separately; Git submodule gitlinks for clients are
distinct from Go module pins and do not establish the contents of a published binary.

At `cb71cf80d72`, both root and `test/go.mod` replace `github.com/metacubex/utls` with the same
fork revision, and both replace `cronet-go/all` with `c9c0e6ec8845`. The independent
`REQUIRED_FORK_REPLACES` list in `scripts/ci/check-go-module-integrity.sh` names four modules plus
the Cronet prefix; uTLS is not named in that specific required list. Do not infer that the list is a
complete inventory of every fork replacement or that matching text proves built artifact contents.

**Scope:** `include/*` (the registry and the tag-polar pruning pairs), `release/*` (profiles, version,
pins), `cmd/internal/*` (builders and generators), `Makefile`, `.gitmodules`.

**Load this module when changing** a build tag, the protocol registry, a release profile, a generator,
or a dependency pin.

---

## 1. The registry is an explicit constructor — not `init()`-based

`include/registry.go` has **no global map and no `init()` registration**. `Context(ctx)` (`:50`)
installs six registries, each an explicit sequence of `Register*` calls:

| Registry | Built by | Representative entries |
|---|---|---|
| Inbound | `InboundRegistry()` `:54` | `:57 tun.RegisterInbound`, `:70 naive.RegisterInbound` |
| Outbound | `OutboundRegistry()` `:83` | `:91-93 group.Register{Selector,URLTest,LoadBalance}`, `:101 registerNaiveOutbound` |
| Endpoint | `EndpointRegistry()` `:115` | `:121 masque.RegisterEndpoint` |
| DNS transport | `DNSTransportRegistry()` `:127` | `:130-139` |
| Service | `ServiceRegistry()` `:150` | `:153-162` |
| Certificate provider | `CertificateProviderRegistry()` `:167` | — |

**Consequence for a future AI:** registering a protocol can require changes beyond the registry.
`docs/BUILD-PROFILES.md:50-52` describes inheritance, but independent coverage lists and profile
tests may need changes. Review the following places for applicability; they are not nine mandatory
edits for every protocol (for example, a new source root or tag is conditional):

| # | Where | What |
|---|---|---|
| ① | `include/registry.go` | the right positional block, **plus** a tagged file **and** its matching stub if the capability is optional |
| ② | `constant/` | the type string |
| ③ | `option/` | the option struct |
| ④ | `.github/workflows/server-linux-amd64.yml:469-509` | **30 hard-coded required import paths** |
| ⑤ | `scripts/ci/verify-full-capabilities.sh:105-243` | **~55 capability cases** |
| ⑥ | `cmd/internal/build_libbox/tag_golden_test.go:43-99` | frozen resolved-tag strings for **8 platforms** |
| ⑦ | `cmd/internal/modlayout/layout.go:54-74` | `SourceRoots`, for a new top-level directory |
| ⑧ | `cmd/internal/{mobilebuildtags,applebuildtags}` + the profile files | if it is tag-gated |
| ⑨ | `include/registry.go:177/183` | a **removal** goes in the tombstone list, not a deletion |

`include/registry.go:75,108,141,157` calls the registry symbols **unconditionally**, which is the
structural reason both polarities must define them.

### The deliberate tombstones

`include/registry.go:177-190`, called at `:78` and `:110` **last**, so nothing shadows them:

| Tombstone | Message | Origin |
|---|---|---|
| ShadowsocksR (inbound + outbound) | *"deprecated and removed in sing-box 1.6.0"* | upstream |
| `C.TypeWireGuard` + `option.StubOptions` | *"WireGuard outbound is deprecated in sing-box 1.11.0 and removed in sing-box 1.13.0, use WireGuard endpoint instead"* | **fork-added** (absent at `e4b7e692`) |

They are **not dead code**. Removing them regresses a configuration that names `type: shadowsocksr` or
an outbound `wireguard` from a named, actionable error to the generic `unknown outbound type`.

> **Nothing guards them: there are zero `include/*_test.go` files.**

### Why a failing *stub* and not an absent symbol

For `quic_stub.go` specifically, there are three reasons, and they generalise:

1. **Absent symbols do not prune a capability — they fail to compile the module.** The call sites are
   unconditional and `include` is imported by every product entry point.
2. **The stub registers the option *type* with a failing constructor**, so the configuration still
   **decodes** and fails with an actionable hint instead of `unknown inbound type`:
   `quic_stub.go:38-41` registers `option.HysteriaInboundOptions` returning `C.ErrQUICNotIncluded`,
   whose text is *"QUIC is not included in this build, rebuild with -tags with_quic"*
   (`constant/err.go:7`).
3. **Two of the prunes are package-level hooks, not registry entries** —
   `quic_stub.go:27-36` installs a failing `v2ray.RegisterQUICConstructor`, and `:48-50` assigns
   `naive.ConfigureHTTP3ListenerFunc`, which `protocol/naive/inbound.go:226` calls behind the presence
   check at `:213`.

---

## 2. The pruning mechanism: tag-polar file pairs with `_stub` twins

`include/` holds 34 files. Each optional capability is a pair (`X.go` under a positive tag,
`X_stub.go` under its negation) or, for `with_xhttp`, a positive file with no stub.

| Feature tag | On file | Off file |
|---|---|---|
| `with_acme` | `acme.go:1` | `acme_stub.go:1` (`!with_acme`) |
| `with_ccm && (!darwin \|\| cgo)` | `ccm.go:1` | `ccm_stub.go:1` + `ccm_stub_darwin.go:1` (`with_ccm && darwin && !cgo`) |
| `with_clash_api` | `clashapi.go:1` | `clashapi_stub.go:1` |
| `with_cloudflared` | `cloudflared.go:1` | `cloudflared_stub.go:1` |
| `with_dhcp` | `dhcp.go:1` | `dhcp_stub.go:1` |
| `with_naive_outbound` | `naive_outbound.go:1` | `naive_outbound_stub.go:1` |
| `with_ocm` | `ocm.go:1` | `ocm_stub.go:1` |
| `with_openconnect` | `openconnect.go:1` | `openconnect_stub.go:1` |
| `with_openvpn` | `openvpn.go:1` | `openvpn_stub.go:1` |
| `with_quic` | `quic.go:1` | `quic_stub.go:1` |
| `with_tailscale` | `tailscale.go:1` | `tailscale_stub.go:1` |
| `with_usbip && (linux \|\| (darwin && cgo) \|\| windows)` | `usbip.go:1` | `usbip_stub.go:1` |
| `with_v2ray_api` | `v2rayapi.go:1` | `v2rayapi_stub.go:1` |
| `with_wireguard` | `wireguard.go:1` | `wireguard_stub.go:1` |
| `with_xhttp` | `v2rayxhttp.go:1` | *(no stub — the symbol is simply absent)* |

Three files carry no build tag and are always compiled: `include/oom_killer.go`, `include/registry.go`,
`include/tz_ios.go`. `include/tz_android.go` is a vendored Go stdlib file with the Go copyright header.

### The polarity rule that matters — `quic_stub.go`

`include/quic_stub.go:48` installs a `naive.ConfigureHTTP3ListenerFunc` that **fails**, rather than
leaving the field nil. That is exactly what lets `protocol/naive/inbound.go:316 decideHTTP3Availability`
distinguish **"constructor absent"** from **"constructor present but H3 is broken"**, and therefore
choose fatal vs non-fatal for a missing optional transport.

> **A stub that leaves a field nil makes two different states indistinguishable.** When you add a
> tag-polar pair around a capability that has a *runtime* availability question, install a failing
> stub, not nothing.

### The one entry deliberately missing from the profile tag files

`tfogo_checklinkname0` is absent from `DEFAULT_BUILD_TAGS*` even though both products do pass
`-checklinkname=0`. The reason is structural: those files are also read by `go build ./...` and
`go test ./...` under those tags, where `cmd/go` cannot apply `-ldflags` to one package — and with the
tag set, `experimental/libbox`'s `//go:linkname` pulls of runtime internals stop linking. **The tag
travels with the flag instead:** `cmd/internal/mobilebuildtags` for the libbox artifacts and
`cmd/internal/build_boxdd` for the daemon. `TestProfileTagFilesDoNotClaimChecklinkname0` requires the
profile tag files to **stop** claiming it.

---

## 3. Release profiles

```text
release/DEFAULT_BUILD_TAGS         the DARWIN profile
release/DEFAULT_BUILD_TAGS_OTHERS  the same, MINUS with_naive_outbound
release/DEFAULT_BUILD_TAGS_WINDOWS the same as DEFAULT, PLUS with_purego
release/LDFLAGS                    -X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0
release/JIEJIE_VERSION             0.1.31 at cb71cf80d72; re-read for another target
```

Common contents: `with_quic, with_dhcp, with_wireguard, with_utls, with_acme, with_clash_api,
with_tailscale, with_ccm, with_ocm, with_cloudflared, with_usbip, with_openvpn, with_openconnect,
with_xhttp, badlinkname`.

> The Darwin and OTHERS tag files differ in `with_naive_outbound`; the separate Windows file adds
> `with_purego` to the Darwin list.
> `docs/BUILD-PROFILES.md:43-44` says the *client* profile "adds `with_utls`" — **that is false**;
> `with_utls` is in both files.

> `DEFAULT_BUILD_TAGS` is the Darwin profile despite the name. At the current Cronet fork pin,
> `cronet-go/all` also has Linux files and Windows files; its Windows amd64/arm64 files require
> `with_purego`. The Windows profile explicitly adds that tag. Use the target's actual profile and
> toolchain; OTHERS omits the Naive outbound. Locally substituted exact-pinned Cronet source passed
> Naive purego unit tests on Windows, which does not prove native library loading or a release build.

### A stale comment that claims a shipped capability was deleted

`scripts/ci/build-macos-client.sh:19-22` says:

> *"The single deviation from upstream's tag file is the removal of `with_clash_api`, which no longer
> names any file: the Clash API was deleted from this fork as a control-plane decision."*

**All three clauses are false at the release baseline.** The script reads
`release/DEFAULT_BUILD_TAGS` verbatim (`:44`, `:49`) and `with_clash_api` **is** in that file;
`include/clashapi.go:1` and `include/clashapi_stub.go:1` are tag-polar files that name it; and
`server-linux-amd64.yml:350-449` probes the Clash API live.

The identical claim in `build-server.sh:16-29` was **explicitly retracted in place** with a
four-bullet refutation; the macOS half of the same change was never corrected. **This is the most
dangerous stale comment in the subsystem, because it tells a reader that a shipped capability is
gone.**

The Clash API's real history is a removal followed by a **restore**: `bbfc31582` (−1908 lines) →
`968395304` (+2254 lines) → `76cd68eaf`, `332e20194`. It is present, and `with_clash_api` is in every
profile.

Both tag files' contents are **upstream's**, not fork-invented; the fork's additions are the
`badlinkname` handling, `JIEJIE_VERSION`, and the Apple pin metadata.

### The capability boundary is upstream's

The current tree uses the common `include` registry rather than a separate product-specific
allowlist. Registered capabilities still depend on build tags, platform constraints and the
actual upstream changes incorporated into this fork. The practical Darwin/OTHERS tag difference is:

* the **server** profile omits `with_naive_outbound` because the Cronet outbound is a client
  capability. **The Naive inbound is present in both** — the production configuration declares one.
* the **client** profile adds `with_naive_outbound`; `with_utls` is in both. It does **not** add `with_gvisor`:
  upstream retired that tag when it moved to the in-process Go TUN stack.
  `scripts/ci/build-macos-client.sh` asserts this **in both directions** — required tags present,
  retired tags not reappearing.

`with_clash_api` is enabled in both profiles. The Clash API and the Native `api` service are two
management surfaces over **one** instance: they share the traffic manager and the routing-mode manager,
so a mode set through the Native API's `SetClashMode` is the mode the Clash API reports.

### Registry changes and upstream synchronization

An upstream addition becomes available here only after the relevant upstream changes are incorporated
and the selected target profile builds and registers it. No separate product allowlist implies no
automatic synchronization or universal artifact coverage. `verify-full-capabilities.sh` exercises
its own named configuration cases through a built binary; inspect the script and exact run for the
actual coverage before claiming a particular artifact supports a capability.

### A trap in the version string

`cmd/internal/build_shared.ReadTag()`'s `currentTagRev[1:]` mis-slices a tag that does not start with
`v`: `tooling-v0.1.5` becomes `ooling-v0.1.5`, and the version string degrades to `0.0.0-v0.1-<sha>`.
**`0.0.0-v0.1-<sha>` means "built from that sha", not "a wrong Core is installed".**
`ReadTag()` reads `SING_BOX_BUILD_VERSION` first.

---

## 4. Generators and builders — `cmd/internal/*`

| Kind | Entries |
|---|---|
| **Generators that write into the tree** | `format_docs`, `protogen` (regenerates `*.pb.go`), `update_android_version`, `update_apple_version`, `update_desktop_version`, `update_certificates`, `read_tag`, `schema` (via `schema/generate.go`) |
| **Build drivers** | `build`, `build_boxdd`, `build_libbox`, `merge_aar`, `merge_apple_xcframework`, `app_store_connect` |
| **Tag definitions — shared source of truth, NO `main.go`** | `applebuildtags`, `mobilebuildtags`, `modlayout`, `build_shared` |

`appletags` **does** have a `main.go`, and its doc comment states why: the low-memory CI gate and the
memory benchmark previously read `DEFAULT_BUILD_TAGS_OTHERS`, which *"does not match what libbox
builds"*, so those checks *"were testing a configuration no Apple binary ships"*. The geometry tag is
now read from one place: `go run ./cmd/internal/appletags -mobile-only`.

`applebuildtags/tags.go:177` keeps an unused `bindTarget` parameter "for signature stability".

`cmd/internal/build_libbox/main.go:145-146,303` emits a capability manifest so CI can assert
"capability implies matching tag"; `TestNoShippedVariantShipsGVisor` refuses a gVisor-carrying variant.

**Regeneration commands:** `Makefile:57 proto` (daemon protobuf) and `Makefile:35 schema`
(`docs/schema.json`). `//go:generate` directives are rare — only five across the tree, of which the
material one is `constant/goos/goos.go:12` (`go run gengoos.go` → `constant/goos/zgoos_*.go`, 20 files).

`build_libbox` must set `Dir` explicitly: `gomobile` writes relative to the process CWD with **no flag
and no environment override** (`gomobile@v0.1.12 cmd/gomobile/bind_iosapp.go:106`,
`bind_androidapp.go:373`).

---

## 5. The pinned dependency tree — a prefix problem, not a list problem

`.gitmodules` declares three submodules:

| Path | Repository |
|---|---|
| `clients/apple` | `Piggy-Cat-bit-shadow/sing-box-for-apple` |
| `clients/android` | `Piggy-Cat-bit-shadow/sing-box-for-android` |
| `clients/desktop` | **`SagerNet/sing-box-for-desktop`** (upstream's) |

Their gitlinks at `a267e99`: apple `d0524912…`, android `ec61030d…`, desktop `32f915ba…`. Their
contents are **not** in a clean worktree, which is why `Makefile:94` shells into
`../sing-box-for-android` and the Android client is not buildable from this worktree.

**`cronet-go` is a tree of modules, not one module.** A `replace` directive rewrites **one module
path**, so pinning the root leaves every nested module — `/all` and 30 per-platform `lib/<os>_<arch>`
paths — resolving to upstream: a pairing nothing built or tested, and on linux/amd64 one that does not
link. `scripts/ci/check-go-module-integrity.sh`'s parity rule is therefore a **PREFIX rule** derived
from the root `go.mod` rather than from a static list, so a new platform path demands a pin instead of
being missed. `REQUIRED_FORK_REPLACES` (`:105-115`) demands four: `sing`, `sing-tun`, `quic-go`,
`sing-quic`.

> **`docs/FORK-DIFF.md:26` lists only three pinned dependencies while the guard demands four.** The
> guard is authoritative; the document is a summary.

The same script's history is worth knowing: `check-go-module-integrity.sh:60-66` documents that the
`test` module once silently linked **upstream sing-tun**, and *"the old code could not fail on it, by
construction"*. **There are three Go modules in this repository** — the root, `test/`, and
`test/jiejie/reference/` — and `go test ./...` from the root covers only the first.

At the current testing target, `sing-anytls`, `tailscale` and `sing-cloudflared` have no fork
replacement in `go.mod`, while `sing-tun` **does** resolve through the fork replacement. Always
inspect the actual `replace` directives, rather than inferring from the `require` block. `gVisor`
remains in `go.mod` as `// indirect` at the historical release baseline, and
`protocol/tun/inbound.go`, `cmd/internal/build_libbox` and `route/dns.go` still reference it under build
tags.

> **"gVisor retired" means the Go TUN stack is the default and forced-gVisor Android was removed — NOT
> that gVisor left the module graph.** The old `replace` is a comment **on purpose**
> (`go.mod:337-356`).

---

## 6. Packaging surfaces

`Dockerfile`, `Dockerfile.binary`, `.fpm_systemd`, `.fpm_openwrt`, `.fpm_pacman`,
`.github/build_{alpine_apk,openwrt_apk}.sh`, `.github/deb2ipk.sh`, `.github/detect_track.sh`.

Two Windows workflows exist at the release baseline and they are **different products**:
`windows-core-amd64.yml` ("Windows amd64 core") builds `sing-box.exe` for an external loader, while
`client-desktop-windows.yml` ("Windows desktop installer") builds and signs the NSIS installer with a
daemon SCM service lifecycle. The desktop baseline was **restored verbatim** at `7a46125ded0`
("restore(windows): recover the retired desktop installer baseline verbatim", 2026-10-10). **Do not
describe the fork as "core-only, not the desktop installer" without naming which workflow you mean.**

Windows signing is a **hard install gate**: `WinVerifyTrustEx` plus `secureWindowsInstallation` refuses
service registration on a mismatch. Exactly **two** i386 PE exceptions are allowed
(`Jiejiebox-v*-windows-x64.exe`, `resources/elevate.exe`).

---

## 7. Evidence, provenance, cautions

### Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| `go build ./...` untagged | **`VERIFIED_UNIT`** (run here) | `go build ./...` |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | **`VERIFIED_UNIT`** (run here) | as written |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./...` | historical Windows failure at the older pin/profile; not a current-platform verdict | check the actual platform profile and Cronet pin; Windows Cronet files now require `with_purego` |
| Tag composition and the linkname contract | `VERIFIED_UNIT` | `go test -count=1 ./cmd/internal/...` |
| Nested modules' replacement parity | `check-go-module-integrity.sh --require-checked test` passed offline in this calibration under MSYS2 bash for the three discovered modules; no `go mod verify` or artifact provenance | that script |
| `verify-full-capabilities.sh` passes for a shipped binary | **declaration only** | needs a built artifact + bash |
| Reproducibility (build twice, compare sha256) | **declaration only**, and only under `deep_checks` on a Linux runner | `server-linux-amd64.yml` deep path |
| Windows/Android/Apple built artifact contents | not established by these local checks; separately observed workflow runs are SHA-scoped in `ci-release.md` | inspect the exact workflow, run and artifact |

### Provenance

`badlinkname`, `with_xhttp`, `with_purego`, `tfogo_checklinkname0` handling and the profile files are
upstream's structure; the fork's additions are `JIEJIE_VERSION`, `apple-client-refs.env`,
`jiejie-production-topology.json` (a full production config fixture: six inbounds — `masque-h2`,
`masque-h3`, `anytls`, `naive`, `shadowtls`, `ss2022` — and eight route rules), `cronet-provenance.txt`
(five `libcronet.a` archives by size and sha256), and the reachability/idempotence work in
`check-go-module-integrity.sh`.

A **retired branch line** worth knowing: an earlier "pruning architecture" with a fork-specific
registry and a capability allowlist was retired by `3bab501db`, `dcd8b10cd`, `4c39a835b`, `b175f04dd`
and `523db47e4`. **No fork-specific registry or capability allowlist remains** — do not resurrect the
idea from an older document.

### AI cautions

1. **Do not add a protocol by editing a map.** There is no map; edit the registry function.
2. **Do not remove the registry tombstones.** They carry a specific deprecation message.
3. **Do not make a stub leave a capability nil where "absent" and "broken" must differ.**
4. **Do not put `tfogo_checklinkname0` back into the profile tag files.** It broke bare `go build`.
5. **Do not assume the Darwin tag file selects the correct profile for another platform.**
6. **Do not assume `DEFAULT_BUILD_TAGS` is the general default** despite its name.
7. **Do not add `with_gvisor`.** It is retired and the build script asserts its absence.
8. **Do not pin one `cronet-go` module path and call it pinned.** It is a prefix tree.
9. **Do not trust a summary list of pinned dependencies over `check-go-module-integrity.sh`'s
   `REQUIRED_FORK_REPLACES`.**
10. **Do not forget there are three Go modules.** `go test ./...` from the root covers one.
11. **Do not write a build script that relies on the process CWD for `gomobile` output.**
12. **Do not read `0.0.0-v0.1-<sha>` as a wrong version.** It means "built from that sha".
13. **Do not merge the two Windows products in your description.**
14. **Do not diagnose the Windows desktop installer as a file drop yet.** The current installer
    work lives on a separate branch; at the release baseline, verify before using any diff.
