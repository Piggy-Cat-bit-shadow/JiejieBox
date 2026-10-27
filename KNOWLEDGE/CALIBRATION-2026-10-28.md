# Knowledge calibration ledger — 2026-10-28

**Status: INCOMPLETE.** Resume using `REFRESH.md`; recheck the target and worktree first.
This is an operational ledger, not a replacement for the modules or a new production baseline.

- Old production baseline: `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` (v0.1.9).
- Examined testing target: `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`; 791 commits after baseline.
- Published v0.1.30 resolves to `6112aa17052297ec9890e0e7f1541a06120b84b2` and is an ancestor of the target. No origin `main` was advertised when checked.
- Coarse delta covers protocol, dialer, routing, DNS, TUN, transport, platform, dependencies, CI and release. The large delta has not been fully classified or audited.
- Source-checked corrections: TUN start/reset handoff and post-start events (`modules/libbox-platform-contract.md`); empty family probe logging (`modules/dns-demand.md`); declared Windows daemon test workflow (`modules/ci-release.md`). Old global no-delta and no-CI-workflow claims corrected.
- Other module entries, invariants, acceptance rows, fork provenance, protocol and platform coverage and cross-file contradictions remain to be individually checked. Historical reports remain archives; no wholesale disposition audit was completed. Baseline must remain at v0.1.9 until the unresolved load-bearing gaps are resolved.
- Source and local tests do not establish device, interoperability, release artifact or CI-run behavior. No production code, dependency, build or release configuration was changed by this calibration.

Next: inventory each entry, classify the delta by subsystem, inspect current source and tests, reconcile
cross-module claims and indexes, then independently check citations and links. Record actual run evidence
and environment limitations. Advance the baseline only after `REFRESH.md` §39 is satisfied.
