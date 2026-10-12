# Acceptance evidence

| Area | Evidence that is VERIFIED | Not established |
|---|---|---|
| Core/lifecycle | scope, coordinator, route retirement, physical-path and static-validation contract tests | all real network/device conditions |
| Routing/DNS/sniff | route/DNS generation, FakeIP, resolver, QUIC boundary and ownership tests | full multi-platform DNS environment matrix |
| MASQUE/Naive | unit, lifecycle, ownership and protocol/interop harnesses | complete H3/macOS device matrix |
| Apple | pinned source, ABI, provenance, branding/identity, signing/build gates | physical-device NetworkExtension/UI acceptance |
| Android | clean-clone ABI and AAR provenance/architecture/build-tag gates | final APK/device routing |
| Windows | PE/provenance/feature/ZIP validation | external-loader, installer, service/signing and Windows 7 execution |
| Release | artifact selection, byte re-verification, hashes and metadata | a replacement for product/device validation |

`verify-evidence/`, `.github/workflows/`, named contract tests, and `docs/RELEASE-CERTIFICATE.md` are evidence locations. A CI job declaration is not evidence that the job ran.
