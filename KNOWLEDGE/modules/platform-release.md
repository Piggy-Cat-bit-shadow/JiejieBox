# Platforms, build, CI and release

## Current design

Apple uses one pinned `clients/apple` source revision and one Libbox build shared by iOS/macOS. Android uses a matching client fork and derives NDK/gomobile inputs from project-pinned sources. Windows release is a Windows 10/11 x64 core-only `sing-box.exe` for an external loader, not the former desktop installer/service product. Release downloads already-built product artifacts and re-verifies exact bytes; `a267e99e` includes verified Android/Windows artifacts in the v0.1.6 release plan.

## Provenance

Apple: `d86e7d73`, `6afd04e6`, `8a30bccf`, `c7014ddc`, `344b22ec`; Android: `fb51b507`, `42926537`, `8cbc00fc`, `2a636fae`; Windows: `982d7ca5`, `a267e99e`; release: `e63ce659`, `9aef595e`, `d660c979`. These are **OWN**. Windows owner resolution `38b7a586` and Android auto-redirect lineage are **UPSTREAM-ABSORBED**.

## Invariants and acceptance

Never split Apple GUI source pins, substitute display branding for product identity, or treat a skipped job as passing. Android AAR and Windows artifacts must carry the expected commit/provenance/architecture/build-info; Windows fork profile has a negative upstream-baseline control. Release rejects failed/cancelled/timed-out/skipped artifacts and checks hashes and metadata.

Evidence lives in `.github/workflows/`, `scripts/ci/check-apple-*`, `check-libbox-abi.sh`, Windows audit scripts and `verify-evidence/`. CI/build proof is **not** device acceptance. Apple NetworkExtension/UI, Android final APK routing, Windows external-loader execution, real TUN/routing transitions, and Windows 7 are **UNVERIFIED**.
