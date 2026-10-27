# Knowledge calibration ledger — 2026-10-28

**Status: INCOMPLETE.** Resume using `REFRESH.md`; recheck the target and worktree first.
This is an operational ledger, not a replacement for the modules or a new production baseline.

- Old production baseline: `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` (v0.1.9).
- Examined testing target: `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`; 791 commits after baseline.
- Published v0.1.30 resolves to `6112aa17052297ec9890e0e7f1541a06120b84b2` and is an ancestor of the target. No origin `main` was advertised when checked.
- Coarse delta covers protocol, dialer, routing, DNS, TUN, transport, platform, dependencies, CI and release. The large delta has not been fully classified or audited.
- Source-checked corrections: TUN start/reset handoff, dual-stack admission and ownership, empty family logging, FakeIP/sniff translation, transparent-group fast path, capability warning severity, Naive fallback, VLESS client encryption, MASQUE close, progressive memory trim, TUIC path ceiling, Linux kTLS tail ownership, TLS selection and workflow declarations. See the relevant modules and `ACCEPTANCE.md` for anchors and tests. Old global no-delta and no-CI-workflow claims corrected.
- Other module entries, invariants, acceptance rows, fork provenance, protocol and platform coverage and cross-file contradictions remain to be individually checked. Historical reports remain archives; no wholesale disposition audit was completed. Baseline must remain at v0.1.9 until the unresolved load-bearing gaps are resolved.
- Source and local tests do not establish device, interoperability, release artifact or CI-run behavior. No production code, dependency, build or release configuration was changed by this calibration.
- Windows 10 Enterprise LTSC 2021 build 19044, Go 1.25.5: `go test -tags with_utls ./common/tls -count=1` failed three Windows TLS 1.3 handshake tests with `unexpected EOF`; a filtered rerun reproduced them. Cause unknown. Targeted uTLS/ECH tests passed. Linux kTLS tests cannot execute on this Windows host.
- `go test -tags with_naive_outbound ./protocol/naive -run TestH3Fallback -count=1` remained at the Cronet fork download and was interrupted; it produced no test result. Retry with the required dependency available.

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
| `windows-daemon-security` | evidence insufficient for current target | historical source entry and current workflow declaration exist; current trust boundary and actual run record require independent review |
| root architecture, invariants, acceptance and index | update, partial | corrected identified conflicts and links; old line anchors and all remaining invariants/acceptance claims still need individual revalidation |

Next: inventory each entry, classify the delta by subsystem, inspect current source and tests, reconcile
cross-module claims and indexes, then independently check citations and links. Record actual run evidence
and environment limitations. Advance the baseline only after `REFRESH.md` §39 is satisfied.
