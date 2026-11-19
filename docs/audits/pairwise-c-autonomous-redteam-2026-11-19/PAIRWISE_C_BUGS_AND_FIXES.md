# PAIRWISE C — Bugs and fixes

Two defects were confirmed and fixed. Both were recorded-but-unfixed findings from window B that this
window turned into work items: the diagnosis existed, the fix did not.

---

## C-1〔已确认，已修复；B window's R-B1〕The listener phase bypassed the generation gate in six of seven owners

**Trigger.** Any physical network change with at least one `vless`, `vmess`, `trojan`, `hysteria2`,
`tuic` or `masque` outbound, when a demand arrives between the coordinator's epoch advance and the
listener phase (which runs up to 6 s later under its own budget).

**What happened.** The gate exists so the push notification and a demand that arrives inside the
transition window are *one* action: `GenerationGate.NetworkChanged` is the listener form and
`applyTransition` runs the owner's reset at most once per generation
(`common/runtimecoord/generation_gate.go:8-16,127-156`). Every owner passed its reset to
`NewGenerationGate` — and then exposed `InterfaceUpdated` as the listener entry, so the listener ran
the action directly, never touching the ledger. `protocol/trusttunnel` was the only owner that routed
through the gate (`protocol/trusttunnel/outbound.go:324-330`).

Sequence: `Advance(N+1)` → demand D: `BeforeDial` sees stale → runs the action → D builds a session on
the new network and acknowledges N+1 → the listener phase reaches the same outbound → the action runs
**again** → hysteria2/tuic `CloseWithError` cuts the session D just built; vless/vmess/trojan reset the
transport that was just published; masque cancels its session. The result is a session that dies
seconds after every network change and a reconnect on the next demand — indistinguishable, from a
report, from "slow reconnect".

**Why the existing judgement missed it.** The gate's own tests cover the listener/demand interaction
*for the gate* (`generation_gate_test.go`), and each outbound's generation test asserts only that a gate
was registered. No test drove the outbound's `InterfaceUpdated` against a gate that had already applied
the generation. The asymmetry between seven owners was invisible because each owner is correct in
isolation.

**Is the fix safe?** The only question is whether every listener call is preceded by a generation
advance, because the gate's ledger would otherwise suppress a legitimate reset. Checked: `Advance` is
called from the defer of `NetworkManager.beginTransition` (`route/network.go:1423-1431`), and
`notifyNetworkChangeListeners` is called **only** from `performTransitionLocked` (`:1713-1717`), whose
seven callers all take the token from a `beginTransition`. A transition always increments
`transitionOwner`, so the epoch advances for every listener phase — including the wake and
initial-environment paths, which go through the same `performTransitionLocked`.

**Fix.** For all six owners: the existing body becomes `resetForNetworkChange(ctx)` (the function the
gate runs), `NewGenerationGate` is given that function, and `InterfaceUpdated` becomes

```go
func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	if h.generation != nil {
		h.generation.NetworkChanged(ctx)
		return
	}
	h.resetForNetworkChange(ctx)
}
```

The nil-gate branch keeps the previous behaviour for every build and test without a coordinator.

**Regression.** `protocol/{vless,vmess,trojan,hysteria2,tuic}/gate_routing_test.go` and
`protocol/masque/gate_routing_test.go` — `TestInterfaceUpdatedRoutesThroughTheGenerationGate`: with a
gate whose reset counts, generation 1 through the listener must count 1; the same generation again must
still be 1; generation 2 must make it 2.

**Mutation control MC-C1.** Replacing the wrapper with a direct `resetForNetworkChange` call makes the
test fail (`expected 1, actual 0`) — the listener no longer reaches the gate at all.

**Commits.** `fix(runtimecoord): route every gated outbound's listener through the gate`.

---

## C-2〔已确认，已修复；B window's R-B8〕A reload without `store_fakeip` re-issued the previous generation's addresses

**Trigger.** A configuration reload (any reload) with a `fakeip` DNS transport and **no**
`experimental.cache_file` / `store_fakeip`, while a client still holds an address the previous Box
issued.

**What happened.** With no cache file the storage is `MemoryStorage`, whose `FakeIPMetadata()` is nil, so
`Store.Start` had no cursor to restore and began again at the first address of the range
(`dns/transport/fakeip/store.go:119-152`). The mapping table is per-store, so the address a client holds
for domain X was re-issued to whichever domain asked first after the reload — domain Y —
and `route/route.go:1368-1381` rewrites the destination from the address, so the client's connection
went to **Y**. A wrong destination, silently, for the TTL of the client's cached answer.

**Why the existing judgement missed it.** The store's tests pin restart *with* a cache file
(`store_test.go`), and the e2e rows about a previous generation's addresses are about *refusing* an
unattributable placeholder, not about re-issuing one. The reload case has no cache file, so it fell
between the two.

**Fix.** `Store.Start` consults the in-process issuance ledger — the one state whose whole purpose is to
survive the Box — for the highest address any generation recorded inside the current range, and starts
issuing past it (`resumeAfterLedger`). Addresses stay inside the configured range; an exhausted range
wraps exactly as before.

**What it does not cover.** A *process restart* without `store_fakeip` has an empty ledger and no
evidence at all; that case is what the durable record covers when a cache file exists (B-5). The fix
closes the reload case, which is the routine one.

**Regression.** `dns/transport/fakeip/store_reload_test.go`:
`TestAReloadWithoutACacheFileDoesNotReissueAddresses` (two stores, one ledger, no cache file: the second
must not return the first's address and must start above it, and the ledger must still attribute the
address the first handed out) and `TestConcurrentQueriesForOneDomainShareOneAddress` (the
double-checked allocation still gives one address per domain).

**Mutation control MC-C2.** Removing the ledger seeding makes the test fail with the exact measured
message: `the reload re-issued 198.18.0.2, which a client may still be holding for the previous domain`.

**Commits.** `fix(fakeip): do not re-issue a reload's predecessor's addresses`.

---

## Not attempted, with reasons

- **R-B9 (reload is stop-then-build).** The fix is a lifecycle redesign (build-then-swap, with a capture
  window for the TUN), not a local patch; it also needs a device to judge whether a gap in capture is
  worse than a failed reload. Recorded in window B.
- **R-B3 (silent H3→H2→HTTP/1.1 demotion), R-B4 (naive's unarmed fallback window).** Both need a
  product decision about what "degraded" must be observable as, or a change to what "ambiguous" means
  for a probe. Recorded.
- **C-3 (health probe × selector) and C-5/C-11 (reload atomicity).** The largest untouched families;
  running out of session rather than out of ideas. Recorded as the next round's first candidates in
  `PAIRWISE_C_PATTERNS.md`.
