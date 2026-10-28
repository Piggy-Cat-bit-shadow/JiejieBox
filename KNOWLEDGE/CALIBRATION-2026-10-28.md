# Knowledge calibration ledger — 2026-10-28

**Status: INCOMPLETE.** Resume using `REFRESH.md`; recheck the target and worktree first.
This is an operational ledger, not a replacement for the modules or a new production baseline.

- Old production baseline: `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` (v0.1.9).
- Examined testing target: `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`; 791 commits after baseline.
- Published v0.1.30 resolves to `6112aa17052297ec9890e0e7f1541a06120b84b2` and is an ancestor of the target. No origin `main` was advertised when checked.
- Concurrent branch advancement: `cb71cf80d7224a51823b455f0be5959ad8556933` updates the version file to 0.1.31 and the `cronet-go/all` pin. A later check found remote tag v0.1.31 peeling to that commit, but no GitHub Release. The previously run tests are at the prior dependency state; re-run relevant checks against the new pin.
- Coarse delta covers protocol, dialer, routing, DNS, TUN, transport, platform, dependencies, CI and release. The large delta has not been fully classified or audited.
- Source-checked corrections: TUN start/reset handoff, dual-stack admission and ownership, empty family logging, FakeIP/sniff translation, transparent-group fast path, capability warning severity, Naive fallback, VLESS client encryption, MASQUE close, progressive memory trim, TUIC path ceiling, Linux kTLS tail ownership, TLS selection and workflow declarations. See the relevant modules and `ACCEPTANCE.md` for anchors and tests. Old global no-delta and no-CI-workflow claims corrected.
- Other module entries, invariants, acceptance rows, fork provenance, protocol and platform coverage and cross-file contradictions remain to be individually checked. Historical reports remain archives; no wholesale disposition audit was completed. Baseline must remain at v0.1.9 until the unresolved load-bearing gaps are resolved.
- Source and local tests do not establish device, interoperability, release artifact or CI-run behavior. No production code, dependency, build or release configuration was changed by this calibration.
- Windows 10 Enterprise LTSC 2021 build 19044, Go 1.25.5: `go test -tags with_utls ./common/tls -count=1` failed three Windows TLS 1.3 handshake tests with `unexpected EOF`; a filtered rerun reproduced them. Cause unknown. Targeted uTLS/ECH tests passed. Linux kTLS tests cannot execute on this Windows host.
- `go test -tags with_naive_outbound ./protocol/naive -run TestH3Fallback -count=1` remained at the Cronet fork download and was interrupted; it produced no test result. Retry with the required dependency available.
- A later offline retry with `with_naive_outbound,with_purego` failed at setup because both pinned
  Cronet modules were absent from this Go cache and `GOPROXY=off`; no Naive test executed.
- A subsequent retry used a temporary modfile pointing both Cronet modules to the clean local
  `cronet-fork` checkout at the exact pinned `c9c0e6ec8845`; the `TestH3Fallback` cases passed with
  `with_naive_outbound,with_purego`. This tests the local source substitution and test doubles,
  not downloaded module provenance, the native library or live interoperability.
- Additional cross-checks corrected the DNS delivery-versus-cache contract, MASQUE per-chunk ownership,
  the pinned sing splice fallback, fork provenance interpretation, current sing-tun replacement and
  the historical MASQUE initial-size versus hard-ceiling claim. `go test ./route -count=1` failed
  after a 120-second setup timeout in `TestTeardown_OneSidedCompletionNeverRunsOnClose`; an isolated
  rerun passed. The failure remains recorded in `ACCEPTANCE.md`, not silently reclassified as a pass.
- GitHub run records were independently queried for the release Windows daemon job and the later
  Verify/Android jobs; each is scoped to its actual SHA in the corresponding module. Root DNS/HTTP
  detour tests, Mieru classification, XHTTP package tests and `with_quic` MASQUE chunk tests passed
  locally; see `ACCEPTANCE.md` for evidence scope.
- A mechanical scan of backticked bare `name()` references in module documents found Go symbols
  in the current tree; two remaining names were verified as shell functions. This is a limited
  spelling check, not proof that each claim or historical line number is correct. Relative Markdown
  links across the knowledge tree were checked after excluding fenced code; two broken archival
  links were corrected. This does not validate prose path references or Markdown anchors.
- Additional source review distinguished ShadowTLS's first-record deadline and target cleanup,
  endpoint-owned DNS resolver retirement, power-observer notification copying, and Android's separate
  procfs helper from the removed `UseProcFS` platform-interface method. Corresponding targeted
  tests are recorded in `ACCEPTANCE.md`; client-device ABI behavior remains separately unverified.
- Current FakeIP range protection at the TUN L0 decision was distinguished from the router's
  issuance-ledger and retired-range checks; the former does not consult those latter records.
  Metadata flush failure is now returned, superseding that particular historical oracle finding.
  Scope panic behavior and Box start/close stage anchors were checked against source. Focused
  Windows Go 1.26.10 unit results are recorded in `ACCEPTANCE.md`.
- The current pinned Cronet fork has Linux and Windows platform files; the Windows files require
  `with_purego`. Corrected obsolete Apple-only build claims and the contradictory uTLS profile
  description. AnyTLS server-only IPv4 preference and per-call error naming were traced through
  their dialer and outbound source; they do not prove peer interoperability or throughput.

### Module review status

The following inventory records the **extent** of this calibration, not a claim that every statement
or historical line anchor was re-derived:

| Entry | Status | Evidence or remaining work |
|---|---|---|
| `buffer-ownership`, `mtu-and-geometry` | update, partial | dependency path scope and kTLS/TUIC boundaries checked; re-audit other pool and MTU claims against the exact fork pin |
| `build-pruning-release`, `ci-release` | update, partial | current registry, TLS selection and selected workflows checked; full profile and workflow/run/artifact audit remains |
| `core-lifecycle-runtime`, `power-mobile-lifecycle` | update, partial | transition ownership and trim boundaries checked; re-audit all lifecycle and platform callbacks |
| `dns-demand`, `dns-adblock-action` | update, partial | family lookup/admission and selected adblock tests checked; re-audit cache, FakeIP, transport and rule-set interactions |
| `fast-path-group-policy`, `data-path-optimization` | update, partial | removed stale AI authorization, checked transparent-direct policy and entry boundary; re-audit all bypass blockers and scheduler claims |
| `sniff` | update, partial | FakeIP rewrite and known-domain fragmented QUIC verified; remaining protocol sniffers require review |
| `path-selection-validation` | update, partial | fatal versus warning taxonomy checked; re-audit all topology and runtime group claims |
| `libbox-platform-contract` | update, partial | TUN handoff checked; full client ABI/platform contract not re-audited |
| `masque-connectip`, `naive-http-transport`, `xhttp-client-transport` | update, partial | MASQUE close, Naive fallback and VLESS encryption boundaries checked; interop and remaining protocol paths unverified |
| `windows-daemon-security` | update, partial | target source and released SHA CI run checked; no end-to-end service handshake verified, and the local signing fixture skipped |
| root architecture, invariants, acceptance and index | update, partial | corrected identified conflicts and links; old line anchors and all remaining invariants/acceptance claims still need individual revalidation |

Next: inventory each entry, classify the delta by subsystem, inspect current source and tests, reconcile
cross-module claims and indexes, then independently check citations and links. Record actual run evidence
and environment limitations. Advance the baseline only after `REFRESH.md` §39 is satisfied.
