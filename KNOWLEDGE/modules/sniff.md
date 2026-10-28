# Sniff

## FakeIP and fragmented QUIC after v0.1.9

**VERIFIED_SOURCE**, testing target `011f3fdcd99`: `route/route.go`'s
`packetSniffUsesKnownFakeIPDomain` handles a FakeIP reverse mapping that already supplies the domain.
On that eligible path the router stops waiting for the rest of a fragmented QUIC ClientHello; this
does not justify suppressing sniffing for every fragmented packet. See
`route/fragmented_quic_known_domain_test.go` and the helper's conditions. Source inspection is not
evidence of interoperability against arbitrary QUIC clients.

TLS and QUIC SNI also pass through `ParseSocksaddrHostPort(...).Fqdn` before being published to
`metadata.Domain` (`common/sniff/tls.go`, `common/sniff/quic.go`). This keeps an IP-literal SNI from
being represented as a domain for rule matching; see `common/sniff/sni_domain_normalization_test.go`.
The unfiltered `common/sniff` package tests passed locally in this refresh.

**Scope:** `common/sniff/*` (13 non-test files) and its consumer `route/route.go actionSniff`.

**Provenance:** the fork modified `sniff.go`, `quic.go`, `http.go`, `tls.go` and added `wireguard.go`.
`dns.go`, `bittorrent.go`, `dtls.go`, `ntp.go`, `rdp.go`, `ssh.go`, `stun.go` and
`internal/qtls/qtls.go` are **byte-identical** at `e4b7e692` and `a267e99` — their contents are
upstream's, whatever the fork's commit list suggests. 25 fork commits touch `common/sniff`;
**22 test files were added**.

**Load this module when changing:** any sniffer, peek/read behaviour, sniff timeouts, fragmented
QUIC, or the sniff→routing metadata contract.

---

## 1. Inventory

| Kind | Sniffer | Produces | Anchor |
|---|---|---|---|
| stream | `TLSClientHello` | `Protocol=TLS`, `Domain` from SNI, JA3-based `Client` | `tls.go:30-99` |
| stream | `HTTPHost` | `Protocol=HTTP`, `Domain=Host` | `http.go:28-241` |
| stream | `StreamDomainNameQuery` | `Protocol=DNS` | `dns.go` |
| stream | `BitTorrent` | `Protocol=BitTorrent` | `bittorrent.go` |
| stream | `SSH` | `Protocol=SSH`, `Client` = banner[8:] | `ssh.go` |
| stream | `RDP` | `Protocol=RDP` | `rdp.go` |
| packet | `DomainNameQuery` | `Protocol=DNS` | `dns.go:41-74` |
| packet | `QUICClientHello` | `Protocol=QUIC`, `Domain`, `Client`, `SniffContext` | `quic.go:56-476` |
| packet | `STUNMessage` | — | `stun.go` |
| packet | `WireGuard` | — | `wireguard.go` (added by the fork) |
| packet | `UTP` | `Protocol=BitTorrent` | `bittorrent.go:56-94` |
| packet | `UDPTracker` | `Protocol=BitTorrent` | `bittorrent.go:95-96` |
| packet | `DTLSRecord` | — | `dtls.go` |
| packet | `NTP` | — | `ntp.go` |

Plan order is **not** a priority list: a parser that claims the payload ends the attempt
(`sniff.go:45-52`, `:68-77`). Order matters in exactly one place — WireGuard precedes UTP because UTP
is the weakest check and was stealing WireGuard handshakes (`sniff.go:59-67`, commit `912778dd6`).

`sniff.Skip(metadata)` short-circuits SMTP 25/465/587, IMAP 143/993, POP3 110/995 (`sniff.go:23-37`).

### What sniffers may write

Exactly four metadata fields: `Client`, `Domain`, `Protocol`, `SniffContext`.

---

## 2. Ownership — the rules that a "zero-copy improvement" breaks

| # | Rule | Anchor | What breaks if violated |
|---|---|---|---|
| S1 | `payloadReader` borrows segments and **must not outlive the `PeekStream` call that created it** | `sniff.go:79-91` | a sniffer retaining the cursor observes a **recycled pool buffer** |
| S2 | `segments` are views captured **once**, with the newest slot re-read after each `ReadOnceFrom` | `sniff.go:132-137`, `:162` | a one-segment-per-read assumption reads a stale segment |
| S3 | The cursor is **rewound, not rebuilt**, between rounds | `sniff.go:186` | an allocation per round, and divergence between sniffers |
| S4 | The read deadline is **set and cleared around each read** | `sniff.go:146-151` | a leaked deadline poisons the connection for later reads |
| S5 | The buffer comes from the **caller** (`buf.NewPacket()`, 16 KiB), and `ReadOnceFrom` returns `io.ErrShortBuffer` when full | `route/route.go:1241`; `buffer.go:137-144` | the 16 KiB ceiling is PeekStream's byte ceiling — do not "raise" it casually |
| S6 | `takePacketBytes` returns a **view into the caller's datagram**, valid only while the datagram lives | `quic.go:36-54` | a retained DCID / PN sample reads recycled memory |
| S7 | QUIC's working copy `newPacket` is a **fresh packet-sized `make`, deliberately not pooled**; the decrypted buffer aliases `newPacket` | `quic.go:183-184`, `:226` | pooling it makes a fragment outlive its owner; see S8 |
| S8 | Retained `qCryptoFragment.payload` **aliases `newPacket`, never the caller's datagram**; the reassembly buffer is released by `defer` | `quic.go:305-306`, `:341-342`, `:472-476` | a retained fragment survives `Release` of the datagram |
| S9 | Header protection writes **4 bytes in place** inside the packet, so a `Length` that cannot reach them is rejected | `quic.go:125-131` (returns `os.ErrInvalid`; `:190-192`) | out-of-bounds write into the datagram tail |
| S10 | `PeekPacket`'s success path is allocation-free (two local error slots, then a slice only on failure) | `sniff.go:205-236` | an allocation per datagram in the packet hot path |

How S6–S8 are proved (this is unusually strong evidence, and it is why the copy boundary can be
trusted): `quic_ownership_test.go:314-350` asserts an **address range**, `:206-281` and
`quic_boundary_test.go:427-456` **poison the caller's datagram** and show `newPacket` is unaffected,
and `sniff_bounded_copy_internal_test.go:120-156` shows the retained `cap` does not grow with a
coalesced tail.

---

## 3. The two loops

### Stream (`sniff.go:125-202`)

* one `sniffBuffer` per round, allocated by the **caller**;
* loops while **any** sniffer returned `ErrNeedMoreData` (`:182-199`);
* bounded by the per-read deadline (~300 ms default, `action.Timeout` overrides) **and** by
  `io.ErrShortBuffer` on the 16 KiB buffer;
* the aggregate is built only on the **terminating** round (`:154-163`, `:191`).

### Packet, and fragmented QUIC (`route/route.go:1274-1421`)

1. The first round replays the already-captured `inputPacketBuffers` (`:1292-1315`).
2. Continuation is `errors.Is(err, ErrNeedMoreData)` (`:1309`, `:1394`).
3. **Once "need more", later rounds run `sniff.QUICClientHello` ONLY** (`quicMoreData()` `:1281-1283`;
   branches `:1293-1299`, `:1372-1378`). This is why a fragmented QUIC ClientHello does not re-run the
   whole packet plan on every continuation datagram.
4. Each round reads a fresh 16 KiB `buf.NewPacket()` in a goroutine with its own deadline
   (`:1316-1357`).
5. **Bound: `maxSniffRounds = 64`** (`:1211-1217`, enforced `:1340-1346`). On hitting the cap the
   just-taken buffer is **released** (`:1343`) and the flow is treated as unsniffable.
6. Other exits: per-round deadline (`:1349-1355`), context cancel (`:1358-1364`), a non-timeout read
   error (`:1365-1370`), any non-need-more verdict (`:1394-1400`).

The comment at `:1322-1339` records the **measured** pre-fix growth: 1,000 crafted datagrams retained
1,000 buffers (+16.7 MB live), and per-round QUIC buffer grew from 33 KB to 1.06 MB — 541,710,584
bytes allocated for 1,083,915 bytes of input.

> **That measurement is not reproducible from the worktree.** A grep of `route/*_test.go` for
> `maxSniffRounds|sniffRounds|"abandoned after"` returns nothing. The cap and the
> release-on-abandon are `VERIFIED_SOURCE` **only**; do not cite them as tested.

### What "unsniffable" means

Sniffing is **optional and fallible**. Only "need more data" is a retry; every other failure is
recorded as `metadata.SniffError` and is **not fatal** (`route/route.go:1251-1252`, `:1307-1308`,
`:1392-1393`). `sniff.go:205-208` states that nothing routes on `SniffError`.

---

## 4. The sniff → routing contract

* A sniffed domain reaches routing through `resolveLookupName` (`route/route.go:1503-1535`), with UoT
  sentinels excluded (`:1530-1533`) and strict validation via `validSniffedDomain` (`:1544-1565`).
* `metadata.DestinationAddresses` (`:1493`) is consumed as **dial candidates**
  (`route/conn.go:284-285`, `:523-531`, `:562-563`; `common/dialer/default_parallel_network.go:15-35`).
* Sniffing alone does not rewrite `Destination`; an explicit, deprecated per-action
  `OverrideDestination` is the sniff-related exception, with port preserved.
* Separately, FakeIP reverse mapping in `route/route.go` saves `OriginDestination`, sets
  `Destination.Fqdn` to the mapped name and marks `FakeIP`. This translation is not a sniff override.
* A sniffed domain **disqualifies the Direct Offload fast path**:
  `BypassRefusedSniffedDomain` (`route/route.go:791-796`, `route/bypass_verdict.go:29`, `:54`).
* `metadata.Domain` is not the only representation of a known domain: the current FakeIP path can
  leave it empty while setting `FakeIP` and `Destination.Fqdn`. See
  `packetSniffUsesKnownFakeIPDomain` for the exact accepted cases.

---

## 5. Perf vs behaviour vs hardening — the classification

The distinction is load-bearing: a performance change described as a protocol-semantics change (or
the reverse) is the failure mode this section exists to prevent. **No such mislabelling was found in
the sniff history.**

| Class | Changes |
|---|---|
| **PERF** (allocation/copy reduction, identical semantics) | planner indirection replaced by explicit plans (`sniff.go:45-77`); `payloadReader` replacing `io.MultiReader` (`:92-123`); lazy aggregate (`:204-248`); TLS header gate + sentinel; `takePacketBytes` views; `frameSummary`; **packet-bounded QUIC copy**; the HTTP gate + replay reader |
| **FIX** (behaviour defect) | the WireGuard sniffer itself; **WireGuard-before-UTP ordering**; `matchRule`'s error-path buffer release (`route/route.go:390-403`) |
| **HARDENING** (hostile input) | the `Length < hdrLen+4` guard; `maxSniffRounds` + release-on-abandon |
| **REFACTOR** | `defaultPacketSniffers` indirection (`route/route.go:41`) |
| **TEST** | 22 added test files; the wireguard.go doc block is comment-only |

Two honest caveats:

1. **`bfe721454` fuses PERF and HARDENING in one commit.** It narrows the QUIC working copy to the
   current packet *and* needs the `Length` lower-bound guard, because otherwise the 4-byte
   header-protection write panics. Both labels are true of it. (The verdict is unchanged either way:
   a sub-4-byte packet can never authenticate under GCM.)
2. **`9167595c7`'s `perf` label rests on an equivalence that is a correctness claim** —
   `frameSummary` must preserve what the frame sequence said.

Also: **the fork fixed a copy that an adapter review measured as semantically free.** Reverting the
whole-datagram copy left the entire package green (`sniff_bounded_copy_internal_test.go:22-28`), which
is exactly why the memory-amplification fix needed a different test than behavioural equivalence.

---

## 6. Detection specifics, including two documented weaknesses the fork did NOT fix

* **TLS** — 5-byte header gate, type ∈ {20, 22}, version `< 0x1000`, and it never decides on a partial
  header (`tls.go:29-30`, `:51-62`, `:83-87`). `errClientHelloSniffed` is a sentinel that must not
  escape `TLSClientHello` (`tls.go:97-101`), so a non-TLS payload does not cause a re-read.
  Exhaustively checked against real `crypto/tls` output (`sniff_gate_internal_test.go:56-80`).
* **HTTP** — the gate restates only `net/http`'s own request-line rules through
  `common/badhttp`'s `//go:linkname net/http.readRequest` (`badhttp.go:13-14`). Its contract is
  one-directional and testable: **`GateRejects ⇒ ParserRejects`**, and the gate must never reject
  where the parser would ask for more — verified over >500 prefixes (`http.go:118-133`, `:152-155`).
  `httpReplayReader` (`:62-83`) replays what the gate consumed.
* **QUIC** — cryptographic: the ClientHello must **authenticate** (`quic.go:159-229`).
  **Known unfixed false positive:** `frames.count == 1 ⇒ ClientFirefox` (`quic.go:368-371`), so a
  three-fragment Chrome ClientHello is reported as Firefox. `quic_ownership_test.go:271-280` logs it
  as MEASURED and says the fix was out of scope. **Do not report this as a fork regression and do not
  "fix" it without an interop story.**
* **QUIC v1 0-RTT** passes the type check but cannot authenticate — a **pre-existing** false negative.
* **WireGuard** — structural only: reserved bytes `[1:4] == 0` plus an exact length
  (`wireguard.go:76-113`). The `1/2^16` uTP residual is **documented and deliberately not narrowed**
  (`wireguard.go:44-72`, `TestSniffWireGuardUTPAmbiguityIsStructural`). No AmneziaWG support.

---

## 7. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| QUIC packet/datagram boundaries, coalesced packets, 9 header variants, malformed headers, `Length < 4` | `VERIFIED_UNIT` + fuzz | `go test -count=1 ./common/sniff/...`; `FuzzQUICPacketBoundary`, `FuzzQUICPacketLengthBoundary` |
| Fragment non-aliasing | `VERIFIED_UNIT` (address + behavioural) | `quic_ownership_test.go` |
| TLS/HTTP gate soundness | `VERIFIED_UNIT` | `sniff_gate_internal_test.go` |
| WireGuard matrix + the uTP ambiguity | `VERIFIED_UNIT` | `TestSniffWireGuardUTPAmbiguityIsStructural` |
| PeekStream retry/aggregate incl. an impostor error | `VERIFIED_UNIT` | `sniff_aggregate_test.go` |
| The 16 KiB ceiling / bounded copy | `VERIFIED_UNIT` | `sniff_bounded_copy_internal_test.go` |
| `maxSniffRounds` cap, release-on-abandon, `matchRule` error-path release | **`VERIFIED_SOURCE` only** — no test, no reproducible measurement | — |
| Race-freedom | **`BLOCKED_BY_ENVIRONMENT`** | needs cgo |
| The sniff goroutine census | `VERIFIED_UNIT` but **load-sensitive**: the failing test name differs run to run (`TestAgentFFrameShape*`, `TestTheSniffCensusMovesForExactlyOneLeakedGoroutine`); documented 5/5 passes in isolation | re-run under load |

Measured in this audit: `go test ./common/sniff/... -count=1 -v` → **PASS, ok 6.557s, exit 0**;
`-count=2` → ok 8.848s; `go vet` clean. The documented flake was **not reproduced** in a single run.

Benchmarks re-measured: `PeekPacketQUIC` 7,339 B/op / 73 allocs; `PeekStreamTLSFragmented`
70,754 B/op / 212 allocs; the HTTP gate vs ungated DNS-over-stream is 205 B/3 allocs vs 4,889 B/9.

---

## 8. Provenance

| Commit | Type | What it did |
|---|---|---|
| `e053cc733` | PERF + REFACTOR | explicit plan lists (`sniff.go:45-77`); `payloadReader` (`:92-123`); TLS header gate + `errClientHelloSniffed`; the `defaultPacketSniffers` indirection; `route/route.go:1239-1241` |
| `300b167b4` | PERF | retry driven by the previous round's failures (`sniff.go:132-199`). One documented divergence: a message-mimicking non-wrapping error makes it read **more** (`:176-181`), with a dedicated test. |
| `912778dd6` | FIX | adds the WireGuard sniffer; reorders WireGuard before UTP (`sniff.go:59-67`, `route/rule/rule_action.go:537-540`, `:547`) |
| `91577eaa2` | PERF | HTTP gate + replay reader (`http.go:28-241`) |
| `4dff25c90` | PERF | `takePacketBytes` returns views (`quic.go:36-54`) |
| `9167595c7` | PERF | `frameSummary` replaces a frame-type slice (`quic.go:407-470`) |
| `bfe721454` | PERF + HARDENING | packet-bounded copy (`quic.go:118-124`, `:183-188`) **and** the `Length` lower-bound guard (`:125-131`); a port of `1b0cedf40` |
| `578663e0` | HARDENING + FIX | `maxSniffRounds = 64` and release-on-abandon (`route/route.go:1211-1217`, `:1340-1347`); releases the pooled 16 KiB buffer on `matchRule`'s error path (`:390-403`) |
| `1292a85a5` | TEST | the `wireguard.go` comment block — **0 non-comment added lines**, verified |
| `a05a6954f` | UPSTREAM-ABSORBED | "Fix DNS sniffer matching non-DNS packets" — `dns.go` is byte-identical at both ends; **do not attribute `verifyDNSQueryHeader` to the fork** |
| `b93c30f2a` | UPSTREAM-ABSORBED | "Fix protocol input validation" — `quic.go:302-304`'s CRYPTO length guard and `:351` are present at `e4b7e692` |
| `dd4f62edc` | UPSTREAM-ABSORBED | "Rewrite HTTP proxy protocol" — `http.go:14`, `:95` are present at `e4b7e692` |

The three `UPSTREAM-ABSORBED` rows were established by **blob equality at the baseline**, not by
reachability: the fork re-applied upstream patches that its own baseline already contained.

---

## 9. AI cautions

1. **Never make the QUIC working copy (`newPacket`) pooled or cross-call.** It is a fresh per-call
   `make` precisely so a retained fragment cannot outlive a released datagram (`quic.go:183-184`,
   `:472-476`).
2. **Never drop the `currentPacketEnd < hdrLen+4` guard.** The header-protection unmask writes 4 bytes
   in place (`quic.go:125-131`).
3. **Never unmask inside the caller's datagram.** Every boundary test asserts the input is
   byte-identical afterwards.
4. **Never let `payloadReader` escape `PeekStream`.** It borrows pooled segments (`sniff.go:79-91`).
5. **Never reject a prefix the parser would only ask more data for** (`http.go:118-133`,
   `tls.go:51-54`). That converts `ErrNeedMoreData` into a verdict and loses every fragmented flow.
6. **Never let `errClientHelloSniffed` escape `TLSClientHello`** (`tls.go:97-101`).
7. **Never re-accumulate `roundErrors` across rounds or rebuild the aggregate every round**
   (`sniff.go:154-163`, `:191`).
8. **Never reuse a packet buffer across fragment rounds, and never drop the release on abandon**
   (`route/route.go:1318`, `:1343`, `:1386-1391`).
9. **Never raise `maxSniffRounds` without re-measuring.** The 64 is a DoS bound with a recorded
   pre-fix measurement.
10. **Never tighten the UTP check or start inspecting WireGuard bytes 4–147**
    (`wireguard.go:54-71`). The ambiguity is documented and structural.
11. **Never rewrite `Destination` from a sniffed domain** outside the deprecated
    `OverrideDestination` (`route/route.go:1437-1444`), and never resolve a sniffed name without
    `validSniffedDomain` (`:1544-1565`).
12. **Do not infer the entire FakeIP state from `metadata.Domain`.** Current reverse mapping can
    carry the known domain in `Destination.Fqdn` instead.
13. **Never use `runtime.NumGoroutine` for the leak census** (`sniff_stream_test.go:21-47`, `:89-126`);
    the tests are calibrated around a scoped instrument.
14. **Never attribute `quic.go:302-304` or `http.go:95` to the fork.** Blob equality at `e4b7e692`
    proves otherwise.
15. **Do not report the QUIC single-frame `ClientFirefox` misclassification as a fork regression.**
    It is measured, documented and deliberately out of scope.
