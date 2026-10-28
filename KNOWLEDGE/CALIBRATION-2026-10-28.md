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
- Independent cross-reading corrected the project overview's old release and dependency statements,
  the architecture overview's scope/validation and MASQUE size overclaims, and the corresponding
  MASQUE acceptance and invariant rows. Historical counts are explicitly revision-scoped.
- Dual-stack admission was checked against the observer, local-family gate and scheduler: a
  stale local verdict cannot deny, strict configured strategy remains independent, and the
  bounded, expiring family-health record changes race timing rather than blacklisting a family.
- Libbox monitor refresh/publish ordering and the close-during-refresh fence were source-checked and
  targeted tests passed with Go 1.26.10 and `p2ios` on Windows. The Apple lifecycle's sleep measurement
  start and reuse-boundary publication were distinguished; neither test result is device validation.
- The MTU module's claim that only Hysteria2 passes `InitialPacketSizeCeiling` was corrected: TUIC
  does so too, while MASQUE's initial clamp alone does not prove a hard later-packet ceiling.
  Focused scheduler default, arbitration and refusal-ownership tests passed locally.
- Historical XHTTP benchmark provenance was checked by ancestry; the recorded measurements are not
  current performance evidence and do not support the claim that the fork was never behind. The
  performance index now scopes its dated values and avoids protocol-independent conclusions.
- Removed a stale CI-module diagnosis claiming the Windows TLS 1.3 tests contact a remote host and
  proving unsupported Schannel. Their local handshake failure was reproduced, but its cause remains
  unknown; a historical failure does not rule out a later regression.
- The direct-offload layer table now distinguishes route-prefix setup from auto-redirect flow
  verdicts and no longer asserts that kernel-assisted copy is available on every OS or flow.
- Windows daemon source review corrected the claim that Linux has no peer authentication: its local
  socket path uses `SO_PEERCRED` and process-start identity. The Windows certificate-expiry policy is
  documented as a scoped tradeoff rather than an unconditional security guarantee.
- Full-audit BUG-009/015 were revisited: the current remote client has a five-minute HTTP timeout,
  publication checks closure and TUN serializes redirect updates with close. Updater Close does not
  join, and already-copied general callbacks are not globally recalled; the targeted TUN ordering
  test passed, with no Linux integration claim.
- Historical BUG-011/014 were rechecked against the uTLS guard and exact local Cronet pin: unsupported
  ECH fingerprints are rejected at configuration, and Cronet's recovered start panic is assigned to
  a named return. Native startup failure and every allowed ECH wire combination remain untested.
- Historical BUG-002/010 were rechecked against `common/ktls/ktls.go` and the exact pinned sing
  splice source: the successful hook path forwards returned bytes and resumes, and post-handshake
  messages are drained. Linux kTLS splice/KeyUpdate execution remains unverified on this Windows host.
- Historical BUG-005 was checked against exact quic-go pin `2daa76f5bd52`: successful no-headroom
  copies release the payload; a batch failure retains caller ownership even if a copied prefix was
  already queued. Chunk ownership atomicity therefore does not establish exactly-once transmission.
- The historical audit's remaining indexed items were mapped to the TUN, Naive, group, lifecycle
  and MTU sections. Active-stream silent stall and startup-fixed IPv6 entry presentment remain
  documented limitations, rather than being labeled universally repaired.
- Current workflow declarations were cross-checked: Android ARM64 has a path-filtered testing push
  in addition to dispatch, and the strict release-acceptance self-test runs in the release-contract
  workflow, while a mention in Verify is a comment rather than a direct invocation.
- The local libbox ABI pin-only script passed for both client gitlinks using MSYS2 bash. It did not
  execute the full client ABI sweep; its gomobile pin display was unknown in this environment.
- The tracked schema comparison passed locally with Go 1.26.10 and `DEFAULT_BUILD_TAGS_OTHERS`.
  The current Verify race declaration also includes `protocol/mieru`; the historical command list
  and package count were corrected or marked as revision-scoped.
- The UoT per-datagram guard caches by destination and forwards the original domain after checking
  resolved addresses. Its check alone does not pin the forwarded address; the session-level
  resolve/check/dial guarantee cannot be extended to this path without downstream evidence.
  Focused memo/key/no-match tests passed locally; the memo-growth test inserts keys directly and
  does not measure an end-to-end attack. The acceptance rows were narrowed accordingly.
- A further acceptance/baseline pass corrected the Apple pin gate, root CI test scope, historical
  release self-test attribution, ancestor-count diagram and the erroneous implication that an empty
  delta proves old knowledge valid. Historical tests remain scoped to their own revisions.
- The reusable-owner test checks named types rather than forbidding every second `ReuseSuspect`
  implementation; its previously cited test command matched no test. Corrected the related
  cautions and distinguished the sing fork's splice workflow declaration from an observed run.
- A test-name scan found that the cited duplicate-member lint test is absent; the implementation is
  source-verified, while nearby collision tests cover a different setup. Prefix regexes and explicit
  historical names require manual interpretation rather than being counted as missing tests wholesale.
- Correction to an earlier calibration statement: Verify runs `TestNetLogEventCount` after `cd test`,
  in the separate test module. It is not a main-root-package test; the acceptance and CI entries have
  been corrected after checking the workflow's working directory and test definitions.
- Offline module-integrity script passed under Go 1.26.10 for all three discovered modules; root/test
  replacement parity included 36 entries and all 31 Cronet paths. This did not run `go mod verify`
  or establish native archive/artifact provenance.
- DNS group selection, lazy bounded health records, cancellation and NXDOMAIN classification were
  reviewed against source and the earlier local DNS-transport suite. The FakeIP overview was aligned
  with the narrower current-range TUN guard and separate router issuance checks.

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
