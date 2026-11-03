# iOS self-sign compatibility — phase 1

Experiment branch: `experiment/ios-selfsign-compat` (core), `experiment/selfsign-compat`
(Apple client). Neither is merged, and no official workflow, release gate, TestFlight entry
point or key changed.

This document records what the uploaded packages actually contain, what the launch failure's
mechanism is, and what the experimental build does differently. It deliberately separates
**measured static facts** from **hypotheses that need a device**.

## 1. What the four uploaded packages contain

Measured with `scripts/experiment/audit-ipa.py` (stdlib only, no macOS needed):

| package | app id | version | minOS | extensions | embedded frameworks |
|---|---|---|---|---|---|
| `singer-0.1.0-bare.ipa` | `app.singer.ios` | 0.1.0 (202609200907) | 15.0 | 1 — Packet Tunnel | **none** |
| `wloc-代理工具` | `com.yu9191.wloc` | 1.1.2 (27) | 18.0 | 1 — Live Activity | **none** |
| `wloc-自带隧道` | `com.yu9191.wloc` | 1.1.2 (27) | 27.0 | 2 — Live Activity + Packet Tunnel | **none** |
| `jiejiebox-…-unsigned-LC.ipa` | `io.nekohasekai.sfamt` | **1.14.8 (10)** | 16.0 | **5** — Packet Tunnel, Action, Share, FileProvider, Widget | **`Library.framework`, 86 MB** |

Two things follow directly, and both matter more than the signature itself.

**The identity is upstream's, and the build's own metadata says so.** The artifact's file name
and `release/JIEJIE_VERSION` say 0.1.37, while the app says 1.14.8 (10) and
`io.nekohasekai.sfamt`. The package's own `BUILD-INFO-IOS.txt` records both facts side by
side: `version: 0.1.37`, `base_bundle_id: io.nekohasekai.sfamt`,
`app_group: group.io.nekohasekai.sfamt`, `xcode: Xcode 26.6`, `signing_mode: unsigned`.

The intermediate `-LC` package is structurally identical to the original: same identifiers,
same versions, same five extensions, same framework, still unsigned. Whatever that step did,
it did not change the identity or the version. The cause is in the
unsigned build path: `scripts/ci/build-ios-ipa.sh` passes neither `MARKETING_VERSION` nor
`BASE_PACKAGE_IDENTIFIER` nor `CURRENT_PROJECT_VERSION` in unsigned mode, so Xcode's
project defaults win. The TestFlight path does pass them. Every extension also carries its
own version (1.0 / 1), so "the app is 0.1.37" was not true per bundle even after the
branding overlay set `CFBundleDisplayName` to JiejieBox.

**This is the only one of the four with an embedded dynamic framework.** A re-signing tool
must sign the app, five extensions and an 86 MB framework; the two packages that demonstrably
work on the user's device have nothing nested at all.

## 2. Why a re-signed build cannot open — mechanism, not guess

`Library/Shared/FilePath.swift` resolved the shared container as:

```swift
private static let defaultSharedDirectory: URL! =
    FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: groupName)
public static let sharedDirectory = defaultSharedDirectory!   // iOS
```

`containerURL(forSecurityApplicationGroupIdentifier:)` returns a URL **only** when the
process's signature carries an `application-groups` entitlement containing exactly
`groupName`. A third-party signing service signs with its own profile; the App Group it
grants is its own, or absent. The call then returns `nil`, the force unwrap traps, and the
first access happens during launch — opening the profile database in
`Library/Database/Database.swift` — so the process dies before the first frame. To the user
this looks like "it installs and will not open", which is precisely the reported symptom.

`AppConfiguration` had the same shape for its two Info.plist keys (`fatalError` when
`BasePackageIdentifier`/`AppGroupIdentifier` are missing), and `FileProviderExtension` had
both a `fatalError` and the same force unwrap.

This is a **high-confidence cause**, not a proven one: no `.ips` from the failing launch is
available. It is consistent with every observation, and it is the only startup path in the
client that is unconditional on the signature.

A second, quieter defect follows from the same root: `AppConfiguration.extensionBundleID`
was `packageName + ".extension"` with `packageName` baked into Info.plist. A renamer rewrites
bundle identifiers but not custom Info.plist keys, so the app asked for a tunnel provider
that no longer existed and the VPN could not start while the GUI looked healthy.

## 3. What the experiment changes

Apple client (`1fa26ca`, branch `experiment/selfsign-compat`):

1. `FilePath` no longer force-unwraps. When the App Group is unavailable it falls back to a
   **process-local** container, logs the degradation, and sets `isUsingProcessLocalDirectory`
   so a diagnostic surface can say so. The GUI, settings and profile management do not need
   the group; only the tunnel does.
2. `AppConfiguration` reports missing Info.plist keys instead of terminating, and
   `extensionBundleID` is **discovered** by enumerating the embedded PlugIns for the
   extension whose `NSExtensionPointIdentifier` is `com.apple.networkextension.packet-tunnel`.
   A renamer can therefore rename everything and the app still finds its tunnel.
3. `FileProviderExtension` serves its own container when the group is missing.

Core (`experiment/ios-selfsign-compat`):

4. `.github/workflows/experiment-ios-selfsign.yml` builds the IPA from source at the pinned
   Apple SHA, on the experiment branch only. It produces a full artifact and a **diagnostic**
   artifact that keeps only the Packet Tunnel extension, so a launch failure can be
   attributed to signing surface instead of guessed at.
5. `scripts/experiment/build-ios-selfsign-ipa.sh` passes the version and identity the
   unsigned path never passed (`MARKETING_VERSION`, `CURRENT_PROJECT_VERSION`,
   `BASE_PACKAGE_IDENTIFIER`, `APP_GROUP_IDENTIFIER`), so the artifact's name, its
   `Info.plist` and `release/JIEJIE_VERSION` are one number.
6. `scripts/experiment/verify-selfsign-ipa.py` refuses to upload an artifact whose version,
   identity family, extension versions, tunnel presence or signature residue is wrong.

The identity default for the experiment is `io.jiejiebox.app` / `group.io.jiejiebox.app`.
It is a **base**, not a claim: whatever profile the tester signs with decides the final
identifiers, which is why the family is built from one setting and verified as a family.

## 4. What this cannot do

- It cannot grant an entitlement the provisioning profile does not have. Without a profile
  that grants `packet-tunnel-provider`, the VPN cannot start; the app should still open and
  say so.
- It cannot make a third-party signer sign the embedded framework. The diagnostic artifact
  exists to test whether that is what fails.
- Static verification says what the package contains, never that it runs. Device results are
  the tester's to produce.

## 5. Reproduce

```
# the Apple source is pinned twice: by the branch's gitlink and by the workflow's env
git checkout experiment/ios-selfsign-compat
gh workflow run experiment-ios-selfsign.yml --ref experiment/ios-selfsign-compat
# or push to the branch; the workflow triggers on push because dispatch is restricted to
# the default branch in this repository's configuration
```

The artifacts are `jiejiebox-ios-selfsign-experiment-<sha>`: the two IPAs, `AUDIT.txt` and
`BUILD-INFO.txt`.
