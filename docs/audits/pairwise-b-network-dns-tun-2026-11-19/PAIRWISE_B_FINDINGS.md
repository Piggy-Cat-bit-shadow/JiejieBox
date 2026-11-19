# PAIRWISE B — Findings

Five defects were confirmed and fixed in this window; eight more were confirmed by reading and are
recorded with the exact sequence, the reason existing tests miss them, and the test that would settle
them. Two of the fixed ones (B-4, B-5) were first recorded as R-B6 and R-B7 and are numbered in place
to keep the history readable. Nothing is marked fixed without a regression that fails when the fix is removed.

Windows B's pairs overlap areas that earlier rounds hardened heavily (the dialer carries ~60
dual-stack test files, the fake-IP store a full issuance ledger, `route` ~7.5 min of tests). Both
fixes below were found by the same two moves: run a suite that no job runs, and check what the
*toolchain* contributes to a wire format this repository pins.

---

## B-1〔已确认，已修复〕The Go 1.27 move changed TrustTunnel's HTTP/2 preface; the pin was red and unrun

**Pair:** B05 (protocol fallback/camouflage) × the toolchain as a participant in the wire format.

**Trigger.** Any TrustTunnel H2 session, i.e. every build with `with_trusttunnel`.

**What happened.** `transport/trusttunnel`'s `TestHTTP2Preface` was **red at HEAD**:

```
--- FAIL: TestHTTP2Preface
    client_http2_test.go:90: SETTINGS [... [MAX_FRAME_SIZE = 1048576] ...],
                               expected [... [MAX_FRAME_SIZE = 16384] ...]
```

Root cause, measured in the toolchain itself: Go 1.27's `net/http` HTTP/2 implementation carries
`defaultMaxReadFrameSize = 1 << 20` (`$GOROOT/src/net/http/internal/http2/http2.go:82`) and advertises
it as `SettingMaxFrameSize` on every client connection (`config_test.go:37`), where the pre-1.27
implementation emitted the 16 KiB protocol default. TrustTunnel's session is built to present
**Chrome's** connection preface — the file's own comment lists each value — so the preface silently
stopped being Chrome's with no line of this repository edited.

**Why it was not noticed.** The detector existed and was correctly gated *for code changes*:
`linux-kernel-verify.yml` runs `./transport/trusttunnel` when a path under `transport/trusttunnel/**`
or `protocol/trusttunnel/**` changes. A toolchain bump changes none of those paths, and this
repository moved to `toolchain go1.27.4` without any path under them changing. The pin stayed red on
`testing`.

**Fix.** `chromeMaxFrameSize = 16384` is now set explicitly in the `http.HTTP2Config` the session is
built from, so the parameter the toolchain would otherwise choose is part of the camouflage contract
like the other four. The comment records the measured drift and the Go source line.

**Regression.** `TestHTTP2Preface` is green **unchanged** — the code moved back to the contract
rather than the test moving to the code:

```
SETTINGS [[ENABLE_PUSH = 0] [INITIAL_WINDOW_SIZE = 6291456] [MAX_FRAME_SIZE = 16384]
          [MAX_HEADER_LIST_SIZE = 262144] [HEADER_TABLE_SIZE = 65536]] WINDOW_UPDATE 15663105
```

**Negative control MC-B1** is the pre-fix run itself: the same test reported `1048576` with the single
line removed.

**CI.** `verify.yml` (no path filter, runs on every push) gained a step that runs this pin — plus the
two new pins below — beside the existing "the module's toolchain directive is the toolchain that
runs" step. ~1.3 s, guarded by `require-executed-coverage.sh` so a run that skipped everything cannot
pass.

**Not fixed, recorded (B-1b):** the same Go default reaches every *other* Go HTTP/2 client in the
tree. `common/httpclient` builds its `HTTP2Config` without `MaxReadFrameSize`, so provider/dashboard
requests now advertise 1 MiB; that has no camouflage contract and the bound is the receive window, so
it is left alone and noted. `transport/v2rayxhttp`'s H2 pool uses `http2.Transport` defaults and
therefore advertises it too; **no** test or comment in that package claims a browser-shaped preface,
so nothing is asserted and nothing was changed — but if a later round decides XHTTP must look like a
browser at the frame layer, this is where it starts.

---

## B-2〔已确认，已修复〕A network change made every later vless/vmess/trojan HTTP stream pay a new handshake

**Pair:** B04 (H2/QUIC pool × generation) × B05 (the transport's reset contract).

**Trigger.** One network change on a `vless`/`vmess`/`trojan` outbound whose transport is HTTP/2
(`transport/v2rayhttp`) or gRPC-lite (`transport/v2raygrpclite`), then any later stream.

**What happened.** The outbound's `InterfaceUpdated` prefers `ResetContext`, then `Reset`, and falls
back to `Close` (`protocol/vless/outbound.go:201-221`, and the same shape in vmess/trojan). Both
transports implemented no `Reset`, so the fallback ran: `Client.Close()` → `pool.CloseAll()` +
`removeRegistration()` (`transport/v2rayhttp/client.go:268-284`).

`runtimecoord`'s registration reports a **closed** registration as stale forever and refuses to
acknowledge it afterwards (`common/runtimecoord/coordinator.go:544-546` and `:564-567`). The pool
asks exactly that question at the hand-out (`generationStale()` → `retireGeneration()`), so from then
on **every** dial took the "previous generation" branch: a new `http2.Transport` per stream, a new
TCP + TLS + HTTP/2 handshake per stream, and the replaced transports accumulating in the pool's
terminated count. Same shape in gRPC-lite (same pool, same check).

**Why the existing judgement did not catch it.** The generation tests are thorough about the
*publish* path — an idle previous-generation connection is not lent, a busy one is not cut, a late
result is discarded (`generation_test.go`, three named tests) — and one of them asserts that Close
unregisters the registration, which is correct for teardown. Nothing dialed *after* a Close, so the
difference between "this pool was reset" and "this pool was destroyed" was never observed. The
outbound-side test for vless asserts only that the gate is registered
(`TestVLESSOutboundRegistersTheGenerationGate`).

**Fix.** `Reset()` on both clients: replace the pooled generation and keep the registration valid
(`v2rayhttp.Client.Reset`, `v2raygrpclite.Client.Reset`). The outbound's existing interface assertion
then takes the intended path — the same one `transport/v2rayquic` and `transport/v2rayxhttp` already
provide (`ResetContext`/`Reset`).

**Regression.** `transport/v2rayhttp/reset_test.go`:
- `TestResetKeepsThePoolReusableAcrossAGenerationChange` — one reset, then two demands; the second
  must reuse the connection the first opened (observed at the **server's** accept counter, not in the
  transport's bookkeeping), and the pool must report itself current again;
- `TestResetDoesNotCutAStreamThatIsAlreadyRunning` — the reset retires the previous generation while
  the stream already using it finishes.

**Positive/negative control MC-B2.** Implementing `Reset()` as `return c.Close()` turns the first test
red with `expected 2, actual 3` — the measured "one handshake per stream" shape.

**Not fixed, recorded (B-2b):** the same fallback-to-Close path exists for any transport that has
neither `Reset` nor `ResetContext`; the right long-term shape is a capability check in
`InterfaceUpdated` that refuses to treat teardown as a reset, which is a maintenance decision about
the adapter contract rather than a local fix.

---

## B-3〔已确认，已修复〕A degenerate fake-IP range issued addresses outside itself, which the router then dialled as literals

**Pair:** B02 (fake-IP × TUN/route) at its smallest scale.

**Trigger.** A `fakeip` transport configured with a range that has no issuable address: `/31` or `/32`
for IPv4, `/127` or `/128` for IPv6.

**What happened.** `nextAddress` (`dns/transport/fakeip/store.go:90-96`) wraps the cursor to
`range.Addr().Next().Next()` whenever the next address reaches the range's last address or leaves the
range. On a `/31` that wrap target is `198.18.0.2` — outside `198.18.0.0/31` — so **every** allocation
returned the same out-of-range address; on a `/32` it returned `198.18.0.9`.

That is not a cosmetic misallocation. `route/route.go:1368` recognises a placeholder with
`Store().Contains(address)`; an address outside the range fails that test, so the flow skips the
restore **and** the refusal branch and is treated as an ordinary literal destination — the exact leak
class the store's issuance ledger exists to prevent (`route/route.go:1382-1414`), reached without any
ledger involvement. Two domains also shared one synthetic address.

**Why the existing judgement did not catch it.** The store's tests exercise a working range
(`store_test.go`), and the issuance/refusal e2e rows use realistic `/15`-style ranges. No test
configured a range too small to allocate from, and the allocator has no size validation
(`dns/transport/fakeip/fakeip.go:33-37`).

**Fix.** `Create` now refuses to issue an address that is not a host address of its own configured
range (`issuableAddress`): inside the prefix, and neither the network nor the broadcast address. A
degenerate range therefore fails the first query with a message naming the range, instead of handing
the router an address it cannot recognise.

**Regression.** `dns/transport/fakeip/store_range_test.go`:
- `TestADegenerateRangeIssuesNothingOutsideItself` — `/32` and `/31` must return an error and no
  address;
- `TestEveryIssuedAddressIsInsideItsRange` — the positive half: on a `/29`, sixteen allocations are all
  inside the prefix, none is the network address, `Store.Contains` agrees with the range, and the
  allocator does hand out more than one address before wrapping.

**Negative control MC-B3** — with the guard disabled the tests fail with the measured addresses:
`must refuse instead of issuing 198.18.0.9` (`/32`) and `198.18.0.2` (`/31`).

---

## B-4〔已确认，已修复；原 R-B6〕A placeholder from a retired fake-IP generation was bypassed to the platform

**Pair:** B02 (fake-IP × TUN fast path) — the L0 guard.

**Trigger.** A client still holding a fake-IP address minted by an earlier generation (a range move, or
fake-IP removed from the configuration) sends traffic while `route_address_set` (or an exclude set) is
configured.

**What happened.** The TUN fast path gated its route-set bypass with
`routeSetsApply := t.fakeIPStore == nil || !t.fakeIPStore.Contains(destinationAddress)`
(`protocol/tun/inbound.go:1248`), i.e. with the CURRENT-range question. An address from a retired range
fails `Contains`, so `routeSetsApply` became true and an include-set miss returned
`tun.FlowVerdict{Action: tun.ActionBypass}` — **before** `adapter.JudgeFlow`, which is where
`route/route.go:1382-1414` refuses an unattributable placeholder. The synthetic address was handed to
the platform as a literal: no route exists for it, and no domain could be recovered for it either.

The guard's own comment showed the premise it was built on ("without this guard the placeholder is
silently black-holed instead of being unmapped"): the store can identify every placeholder. It cannot —
it identifies the ones its CURRENT configuration would mint.

**Fix.** `protocol/tun/inbound.go`: the flow path asks `fakeIPPlaceholder(address)`, which answers both
halves — the current range (`Contains`) and the retirement snapshot resolved once at `Start`
(`Store.RetiredAddresses()`), so a retired placeholder still reaches the router and is refused there
with its explanation. The snapshot is a slice read without a lock on the per-flow path, and everything
it can miss was issued after `Start`, i.e. inside the current range.

**Regression.** `protocol/tun/fakeip_retired_guard_test.go`
(`TestARetiredPlaceholderIsNotBypassedByTheRouteSet`): a store whose current range is `198.20.0.0/16`
and whose retired interval is `198.18.0.2–.9`, an include set that does not contain the destination, and
a flow to `198.18.0.5` — the router must be consulted and the verdict must not be `ActionBypass`. The
test's own control asserts that an address in the same range **past the issuance cursor**
(`198.18.0.200`) still bypasses, so the fix is proof-based rather than a blanket range block.
**Mutation control MC-B4:** restoring the range-only expression turns it red with
`router preMatchCalls = 0`.

## B-5〔已确认，已修复；原 R-B7〕With no fakeip server configured, the durable retirement record was unreachable

**Pair:** B02, the router half — the refusal itself.

**Trigger.** A restart into a configuration that has `experimental.cache_file` with `store_fakeip` but
**no** `fakeip` DNS transport, while the client still holds an address the previous process minted.

**What happened.** `route/route.go:1471-1478` returned `false` immediately when
`r.dnsTransport.FakeIP() == nil`. The durable record (`Store.RetiredAddresses` persisted by
`store_fakeip`) is reachable only through a live fakeip store, so with none configured there was no
reader at all: the address was dialled as an ordinary literal — the exact sequence the record was added
for, with each such dial burning a connect timeout.

**Fix.** `route/route.go`: `retiredFakeIPAddress` falls back to the cache file's record
(`Router.durableFakeIPRetired`, resolved lazily and once) whenever no live store can answer. The lookup
is lazy because the router is constructed (`box.go:665`) before the service manager creates the cache
file (`:800`); the two are never added together because a live store's own `RetiredAddresses()` already
includes that record. `!cacheFile.StoreFakeIP()` means "this installation keeps no record", which is the
same answer as no record.

**Regression.** `route/fakeip_durable_retired_test.go`:
`TestARetiredPlaceholderIsRefusedWithoutAFakeIPStore` seeds the durable record and requires the refusal
for `198.18.0.5`, while `198.18.0.200` (past the cursor) and a real address keep crossing;
`TestNoFakeIPHistoryMeansNoRefusal` pins the degradation for a machine that never configured fake-IP.
**Mutation control MC-B5:** removing the fallback turns the first test red.

The TUN half of the same case is pinned separately:
`TestARetiredPlaceholderIsRecognizedWithoutAFakeIPTransport` drives `fakeIPPlaceholder` with no store at
all, so the durable record is the only thing that can recognise the address.

---

# Recorded, not fixed

## R-B1〔HIGH〕The generation gate's listener half exists in one owner of seven, so a destructive transition runs twice per generation

`common/runtimecoord/generation_gate.go:134-139` deduplicates the push notification against a demand
for the same generation. Only `protocol/trusttunnel/outbound.go:324-330` routes the listener call
through `gate.NetworkChanged`. The other six owners take the listener call directly
(`route/network.go:1713-1718`, `:1791-1795`, ≤6 s budget at `:1727`): `protocol/vless/outbound.go:201`,
`protocol/vmess/outbound.go:133`, `protocol/trojan/outbound.go:166`,
`protocol/hysteria2/outbound.go:430`, `protocol/tuic/outbound.go:318`, `protocol/masque/client.go:624`.

Sequence: `Advance(N+1)` → a demand arrives → `BeforeDial` sees stale → runs the boundary (closes the
old carrier) → the demand builds a session on the new network and acknowledges N+1 → the listener
phase, up to 6 s later, reaches the same outbound → `InterfaceUpdated` runs **again** (the gate's
ledger is never consulted) → hysteria2/tuic `CloseWithError` cuts the session that demand just
built; vless/vmess/trojan reset the transport that was just published. The user-visible shape is a
session that dies seconds after every network change and is rebuilt on the next demand.

Why no test sees it: `generation_gate_test.go`'s listener/demand test drives `gate.NetworkChanged`,
i.e. only the trusttunnel shape; the other outbounds' gate tests assert registration only.
Test that settles it: a counting outbound with a gate; `Advance(1)`; dial through it (counter 1); call
`InterfaceUpdated` exactly as route's walk does; assert the counter is still 1. Today it is 2.
Not fixed here: making the six owners route through the gate is a contract change across protocols,
and doing it without device coverage of reconnect behaviour after a Wi-Fi→cellular switch is the kind
of change that should not be made at the end of a session.

## R-B2〔MEDIUM，4 of 4 sites fixed〕The generation publish decision was a compare followed by an acknowledgement

The dial path holds the generation it was built for and must decide whether the result may be
published. Three pools did that in two steps — compare `currentGeneration() != startGeneration`, then
`acknowledgeGeneration()` — which leaves the instructions between them open: an `Advance` landing there
makes the acknowledgement stamp the NEW epoch on a connection built on the OLD network, after which
nothing retires it and every later demand is handed it.

**Fixed** in `transport/v2rayhttp`, `transport/v2rayxhttp` and `transport/v2raygrpclite`: the decision
is now the single atomic call `Registration.AcknowledgeIfCurrent(epoch)`, which exists for exactly this
(coordinator.go §AcknowledgeIfCurrent: "the check and the write are under the registration lock while
the coordinator lock is read, so no new epoch can slip between the comparison and the store").

**Regression.** `transport/v2rayhttp/publish_decision_test.go`:
`TestAnEpochChangeDuringThePublishDecisionDiscardsTheConnection` uses a test hook
(`testHookAfterGenerationCompare`, the same technique as `generation_gate.go`'s) to move the epoch inside
the window; the dial's late-bound conn must then fail with the retryable generation error and the pool
must still report itself stale. `TestThePublishDecisionIsAPlainAcknowledgementWithoutANetworkChange` is
the guard-rail that the atomic form does not turn a healthy publish into a discard.
**Mutation control MC-B6:** restoring the compare-then-acknowledge shape (with the hook between the two)
turns the first test red — the connection is published with the new epoch stamped on it.

**Fixed at the fourth site too** (`transport/v2rayquic`, triplewise C): the publish decision moved
inside `connAccess` and the acknowledgement became `AcknowledgeIfCurrent(startGeneration)`, which is
what makes "publish" and "the pool is current for this generation" one step. A refused acknowledgement
now sets the retryable stale-generation error and releases the connection instead of publishing it, so
the waiters of that offer are told rather than handed a session from the network that just ended. The
migration path got the same treatment with the epoch its reset began under (`resetGeneration`): a
migration takes seconds, so the window there was wider than the publish path's, and a second change
landing during it must leave the pool stale. Regression:
`transport/v2rayquic/client_publish_generation_test.go` moves the epoch with `testHookBeforePublish`
and asserts the dial returns the stale error, publishes nothing, and leaves `generationStale()` true,
then that the next demand dials instead of reusing it. **Mutation control:** restoring the
publish-then-acknowledge shape turns it red with the connection handed to the caller ("a connection
built for the previous generation must not be handed out"). The existing migration suite is the
positive control for the migration guard: a wrong `resetGeneration` capture would refuse the
acknowledgement and those tests would stop seeing the migrated connection reused.

## R-B3〔MEDIUM〕A silent fallback is invisible: `common/httpclient`'s H3→H2→HTTP/1.1 demotion

`common/httpclient/http3_transport.go:218-249` decides the fallback and `:841-869` records the verdict
with no log line; `http2_fallback_transport.go:73-86` pins an authority to HTTP/1.1 for the life of
the epoch, also silently. The recorded transport reaches exactly one consumer
(`protocol/masque/client.go:901-912`, which only stops DoH coalescing) and no ledger or status field.
So "MASQUE quietly degraded to h2 and DoH stopped coalescing" is indistinguishable from "worked",
which is the B-window's stated failure mode. Not fixed: making it observable is a reporting-contract
change (which field, which surface) rather than a bug fix.

## R-B4〔MEDIUM〕naive's fallback window never arms when our own probe budget fires first

`protocol/naive/h3_fallback.go:268-283`: when the probe budget (`min(remaining/2, 10 s)`, `:217-230`)
expires before Chromium reaches a verdict, the retry over H2 happens but the window is deliberately
**not** armed. With a short caller deadline that is the normal case, so every CONNECT pays the full
H3 probe again — while the window's own comment (`:23-36`) says it exists so that a failed H3 path
costs one probe per window rather than one per connection. Test that settles it: a stub client whose
H3 attempt blocks until its probe context expires; 3 sequential dials with a 200 ms caller deadline;
assert the H3 attempt count is 1 (today 3). Not fixed: it changes the meaning of "ambiguous" in a
transport whose failure classification was already carefully reasoned.

## R-B5〔已确认，不影响产品行为〕An idle TrustTunnel session really sends nothing

`newHTTP2Transport`'s "no PING" contract had no test. It is now pinned by
`TestAnIdleSessionSendsNoPing`, with a positive control that configures a transport which *must* ping
so a zero count cannot be the detector failing: the production transport sends 0 PING frames in
600 ms, the control sends at least one. The same file pins `StrictMaxConcurrentStreams` and
`ReadIdleTimeout == 0`, which are the two settings that keep one session on one connection.
Recorded as a verified contract, not as a defect.

## R-B8〔HIGH〕A reload without `store_fakeip` restarts the cursor, so a client-held address can be remapped to a different domain

`dns/transport/fakeip/store.go:111-152`: with no cache file (the default) `Start` falls back to
`MemoryStorage`, whose metadata is nil, so the cursor restarts at the beginning of the range and the
mapping table is empty; `route/route.go:1368-1381` then rewrites whatever `Lookup` returns. A client
that resolved `X` before a reload and dials its address after it can therefore be sent to `Y`, which
was allocated the same address in the new generation — a wrong-destination bug, not a refusal. With
`store_fakeip` and unchanged ranges the cursor and the mappings survive (`store.go:120`).

Why no test sees it: `store_test.go` pins restart only WITH a cache file, and the e2e rows use one.
Test that settles it: two Boxes sequentially without `cache_file`; resolve `X` in the first, close,
resolve `Y` in the second, then connect to `X`'s address through the second and assert the peer is
asked for `X` (today: `Y`, or a refusal if nothing was re-allocated yet).

## R-B9〔MEDIUM〕Hot reload is stop-then-build, with no preservation on a semantic validation failure

`daemon/started_service.go:642-662` closes the running instance and only then builds the new one; only
the *parse* is pre-checked (`:553-571`), while `box.New` (all semantic validation) runs after the
close (`daemon/instance.go:242-250`). A configuration that parses but does not build leaves nothing
running unless config recovery is enabled (`config_recovery.go:168-193`), and the TUN device plus
every tracked connection are closed for the whole rebuild. Recorded, not fixed: changing the order is
a lifecycle redesign, and the daemon gate that exists (`verify.yml` "daemon configuration recovery")
was not challenged here.

## R-B10〔LOW〕Two domains can share one placeholder when the cursor wraps

`nextAddress` wraps to the start of the range once the cursor reaches the last address, and the mapping
table is keyed by address, so a small range hands the same address to a later domain and `Lookup`
returns the newer one. Upstream-compatible overwrite semantics rather than a defect of this round;
recorded because B-3's guard now makes the *degenerate* case loud while leaving this one as designed.

## Not defects (checked with evidence)

- **The Go ClientHello is already handled.** `scripts/ci/clienthello-probe` measured the key-exchange
  group set against the toolchain and the `go` directive, and the repository keeps `go 1.25.5` so the
  pre-1.26 group set survives (recorded in `docs/fork/go-1274-regressions.md` W2). The HTTP/2 preface
  was the member of that family the W-list missed.
- **The fake-IP refusal path is proof-based and complete.** `route/route.go:1368-1414` refuses a
  missing record, a placeholder this instance issued, and a retired range it can prove it issued from;
  the positive controls that forbid a blanket range guard exist as e2e tests (B-3).
- **Recording, though it cannot be executed by any job:** the 58-test `e2e` package (fake-IP escape,
  DNS chain, resolve match-only, lifecycle chain) is green here (35.4 s) and is run by **no workflow**;
  see `PAIRWISE_B_VERIFICATION.md` §5 for the census and the mount.
