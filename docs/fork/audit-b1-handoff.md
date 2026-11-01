# audit-b1 handoff — LX bug-oracle core hardening

**Stage closed.** No new investigation. This document is the complete handover for the branch below.

| item | value |
| --- | --- |
| branch (remote) | `audit/lx-bug-oracle-core-hardening-b1` |
| range | `c32c210b0db..afd36ab7f58` |
| commits | 14 (10 fixes + 4 dependent/infra) |
| diff | 28 files, +2905 / −103 |
| base | `testing` @ `c32c210b0dbb1a23fb9b19ef731d164cd8745d34` — untouched, not merged |
| method | `evidence → reproduce/falsify → fix → verify → isomorphic sweep` |
| build tags | `release/DEFAULT_BUILD_TAGS_OTHERS` |
| toolchain | Go 1.25.5 · `CGO_ENABLED=1` + MinGW gcc for `-race` (see §5) |

Not merged, not tagged, no release. `origin/testing` unchanged.

---

## 1. Ledger — 14 commits, in order

| # | SHA | change | Pre-fix (must-fail) | Post-fix |
| --- | --- | --- | --- | --- |
| 1 | `34cc5834d9a` | WG endpoint: move pause registration outside `stateAccess`; publish handles in the same critical section that checks `closing`; `UnregisterCallback` after unlock. Callbacks stay **synchronous**. | `TestPauseRegistrationDoesNotRunUnderTheStateLock` deadlocks: manager mid-`emit` holding `pause.access`, callback wants `stateAccess`, registration wants `pause.access`. | 3 tests PASS. Mailbox/async variant **was built and discarded** — it broke the synchronous pause contract (`TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` caught it). |
| 2 | `4ac5627a499` | `transport/device/system.go`: add `readErrorIsFatal(err)` (`E.IsClosed \|\| E.IsMulti(EBADF)` → fatal; `tun.IsRecoverableReadError` → Debug + `tun.ReadRetry.Wait`); wire `readRetry` into all three loops (`readLoop` / `readLoopLinux` / `readLoopDarwin`) @140,146,149. | All 5 subtests FAIL: ENOBUFS / ENOMEM / EAGAIN / EINTR / ErrTooManySegments each ended the read loop, dropping every later packet until reload. | 5 PASS, asserted by a **real packet queued behind the error** reaching the packet writer (recovered, not merely alive). |
| 3 | `69741849a1d` | `protocol/bridge/backend_darwin.go` `batchReadLoop`: same predicate as `backend_linux.go:329-334` — `IsRecoverableReadError` @264 else `readRetry.Wait` @270. Was a bare `continue` on every error (spin). | Not run at runtime. | **NOT VERIFIED at runtime** — Darwin-only branch, this host cannot run it. Compile-verified darwin/linux/windows amd64. Recorded, not claimed. |
| 4 | `3997c861aca` | `route/network.go`: add `closed atomic.Bool` @96, guard `notifyInterfaceUpdate` @1075 and `notifyWindowsPowerEvent` @1282, set it in the scope cleanup that unregisters. Old guard was `if r.startedCtx != nil` — never nil once set (teardown *cancels*, does not clear). | `TestClosedNetworkManagerDoesNotWriteTheSessionSharedPauseManager` FAILS: a closed manager wrote `NetworkPause()` onto the **session-shared** pause manager, i.e. the NEXT Box's network axis. | 2 PASS. Both carry a **positive control** proving a live manager does write, so the assertion cannot pass vacuously. |
| 5 | `fa45dcfc2cc` | `protocol/naive/inbound_conn.go`: `hijackedConn` gains `WriterReplaceable() true` @579, `ReaderReplaceable() false` @583, `CloseWrite()` @591, `CloseRead()`. Wrapper was losing the socket's half-close. | Subtests 1 and 3 FAIL with `WriterReplaceable false`. | All PASS; `-race ./protocol/naive/` PASS. Real `*net.TCPConn` socket pair. |
| 6 | `e2dcf398ac1` | `common/sniff/tls.go` @114 + `quic.go` @371: `metadata.Domain = M.ParseSocksaddrHostPort(<sni>, 0).Fqdn` (was raw `ServerName`). | Test built with **length-preserving in-place SNI substitution**. | PASS. **LIMITATION recorded:** the bare-IP-literal case is **not buildable in-process** — `tls.Client` filters IP literals out of SNI on send, and padding `1.2.3.4` to the captured 29 bytes yields `1.2.3.4.a.a…`. Declared unreproducible rather than faked. |
| 7 | `a0821574bf4` | `transport/wireguard/pause_deadlock_test.go`: drop one assertion that raced the scheduler. | — (test-only; claim was not test-level) | Removes a flake. No product change. |
| 8 | `5ab370cea27` | `adapter/fakeip_metadata.go`: drop `common.Must(binary.Read(...))`; return `E.Cause(err, "read element length")` @63. | 3 of 6 FAIL: *"a malformed FakeIP metadata value destroyed an UNRELATED cache bucket"*. Minimal input = 3 bytes `00 01 00`. | 6 malformed shapes + blast-radius + control PASS. **CORRECTION in-commit:** the companion claim that the bucket sweep's `name[0]` also kills startup is **NOT reachable** — bbolt refuses a zero-length bucket name (`"bucket name required"`), so `tx.ForEach` cannot hand it an empty key. My first probe reported `accepted=true` by misreading `(nil, nil)`; corrected by measurement. |
| 9 | `dfb93cb81a6` | `common/power/governor.go` `flushNotifications`: bounded `maxPasses = 8` @857-858, `slices.Clone` under lock @869,874, re-arm `pendingNotify`/`pendingReuse` if a registration landed during the pass. | FAILS: *"an observer registered during a notification was never notified: the debt was cleared before the walk"*. MEASURED: the observer registered during the first delivery was never called **at all**. | PASS; `-race ./common/power/ ./route/ .` PASS. Re-arm is conditional on a real length change, so an ordinary transition still delivers exactly once (the notification is an EDGE). |
| 10 | `6117453a777` | `experimental/boxdd/server.go`: `hasServiceMethodPrefix` @453 strips exactly **one** leading slash before the prefix test (mirrors grpc dispatch); `ownerProtectedMethod` @458 and the `DesktopService` test @352 use it. | `ownerProtectedMethod("daemon.StartedService/GetVersion")` = **false**; owner refused with `"the service is not available"`; `/desktop.DesktopService/GetDaemonInfo` slashless refused too. | 4 tests PASS. **Self-correction in-commit:** my first test asserted an *escalation*; it is **fail-CLOSED** (a non-owner is refused too, by `Authorize`'s fallback). Escalation claim withdrawn; kept as a documented defect. |
| 11 | `a782715037a` | `experimental/cachefile/`: split `flush() error` @pending.go:71 from `Flush()` @101 (signature must stay — it is in `adapter.CacheFile`); `FakeIPSaveMetadata` @fakeip.go:84 returns `c.flush()`. | `TestFakeIPSaveMetadataReportsAFailedFlush` FAILS: *"An error is expected but got nil"*. DB closed underneath ⇒ nil returned, cursor lost with only a log line. | PASS, reporting `database not open`. Caller's existing branch (`dns/transport/fakeip/store.go:231-234`) becomes reachable. |
| 12 | `396a018ac3b` | `common/tls/utls_client.go`: split `modernFingerprints` @412 from `realityFingerprints` @415 (Chrome/Firefox/Safari); `random` draws from the latter @431. Register gains a 4th class `fingerprintHybridFromPool`; `random` leaves the incompatible set. | Falsification printed the drawn preset: `… does not contain tls.ClientHelloID{Client:"Edge", Version:"85"}…` — the old pool really drew **Edge/85**. | PASS; register + all REALITY/uTLS/ALPN/fragment/spoof PASS. `docs/fork/utls-fingerprint-decision.md` updated (it claimed "broken on 2 of 5 draws"). |
| 13 | `720c0053f27` | `go.mod`: `grpc v1.79.1 → v1.79.3`, `go-jose/v4 v4.1.3 → v4.1.6`. | See §1.1. | `go build ./...`, `go mod verify` clean, no transitive cascade (protobuf/genproto unchanged). |
| 14 | `afd36ab7f58` | `go.sum`: drop 4 stale lines left by `go get` (replace) vs `go mod tidy` (prune). | — | `go mod tidy` now a no-op. **Note:** my first stability check was a **false positive** — `go` was not on that shell's PATH, so `tidy` never ran. Re-run with PATH and confirmed. |

### 1.1 The two-layer defence: `6117453a777` × `720c0053f27`

| layer | artifact | what it does | status |
| --- | --- | --- | --- |
| **L1 — dependency** | grpc `v1.79.3` | Dispatch itself rejects a `:path` with no leading slash (`server.go`: `if sm[0] != '/' { handleMalformedMethodName; return }`), gated by `GRPC_GO_EXPERIMENTAL_DISABLE_STRICT_PATH_CHECKING`, **default `false`**. | **ACTIVE gate** on this pin. |
| **L2 — fork** | `hasServiceMethodPrefix` @`experimental/boxdd/server.go:453` | The authorization layer normalizes exactly as dispatch does, so a slashless method is classified by the METHOD rather than by the peer's spelling. | **Defence in depth.** Under L1 no slashless path reaches an interceptor, so L2 is currently redundant — but it is correct on its own terms and becomes the necessary gate again if that env var is set to `true`. Its tests document the invariant. |

Both close the same invariant: **an access-control decision must be a property of the method, not of the string the peer spelled it with.** Upstream chose to make *dispatch* strict rather than teach authorization to accept the loose spelling — the same direction as L2. L2 was **not reverted** after L1 landed.

**Reachability, stated separately from the fix:** CVE-2026-33186 needs a server that authorizes by path (`grpc/authz` / xDS RBAC). This fork uses **none** — all 6 `grpc.NewServer` sites use a bearer-token interceptor, TLS, or nothing; no `grpc/authz`, no RBAC, no xDS anywhere. So the CVE was **not reachable** here, and the bump is not a reachability fix.

---

## 2. Evidence-complete residuals — proven, deliberately not changed

Each entry: why not fixed = an explicit architectural/semantic red line, not missing evidence.

| residual | core measurement | why not changed |
| --- | --- | --- |
| **SPEC 122 — uTLS retains TLS1.3 handshake state for the connection lifetime** | On a LIVE conn after a successful TLS1.3 handshake: raw ClientHello **1750 B** (chrome) / 1814 B / **1883 B** (firefox); `State13.KeyShareKeys.Mlkem` = `mlkem.DecapsulationKey768`, **7808 B in 6 fields**; `Ecdhe` + `MlkemEcdhe` **32 B each**. ⇒ **≈9.6 KB per connection, including a 7.8 KB ML-KEM PRIVATE key**. TLS1.2 keeps the greeting but not the key share. | Release has **no hook anywhere**: `clientHandshake` only swaps `getPublicPtr()/getPrivatePtr()` (`u_handshake_client.go:385-387`); `isHandshakeComplete` proves only "not re-run". **Ownership:** the state belongs to uTLS, and the fork should not manage a dependency's private lifecycle from a consumer — that is the `d092c64` class of fix (fork-side), not a consumer patch. Releasing here would inject a new "when may I drop handshake state" invariant into the **authentication path**. `UConn.HandshakeState` **is** addressable from the fork (exported field, `u_conn.go:39`), so the fix is localized — but see §3 precondition. |
| **DoH → HTTP/1.1 permanent latch** | `dns/transport/https_transport.go`: one ALPN result `!= h2` @43 does `fallback.Store(true)` @45; `RoundTrip` then routes to `httpTransport` forever when `fallback.Load()` @72. `fallback` is created once at `https.go:133` and **has no clear point** — `Reset()` (`https.go:173` → `resetTransportLocked` @192) rebuilds the wrapper at `https.go:194` around the **same `fallback` pointer**, so even an explicit reset does not unlatch it. | Latched permanently from a **single** observation, and cannot self-heal when H2 recovers. **But there is no upstream reference fix**, so any change would be my own invented recovery policy — exactly the "do not change upstream semantics by analogy" red line. Recorded rather than patched. |
| **`dialer` nil-deref — NOT reachable** | `common/dialer/default_parallel_interface.go:227,236,261` dereference `InterfaceMonitor()` unguarded, while `DefaultDialer` treats `networkManager` as nullable (`default.go:116,146,184,306,757`). **Derivation:** `networkStrategy` is assigned a non-nil value **only** inside `networkManager != nil` branches (`default.go:146,154,157,166-167`), and `dialParallelInterface` runs only when strategy is non-nil (`:331,353`). | **Not a defect at any in-repo call site.** `DialParallelInterface` (`:362`) is a public entry point, but its only production callers (`default_parallel_network.go:28,130`) run with a non-nil manager. A guard here would be speculative code with no trigger — not added. |
| **`clashmode.Manager` hook-list race — not observed** | `-race`, **300 switches × 3 concurrent registrars**: **no data race reported**. Functional half IS real and was measured by my own test: a hook registered *after* `SetMode`'s snapshot does not receive that switch (`AddUpdateHook` under `updateAccess`; `SetMode` walks a snapshot after unlock). | Race detector is the arbiter and it is clean, so the race is **not established** (recorded as "not observed", never "not a race"). The functional loss is bounded and **self-healing**: a mode is a **LEVEL** a hook can read via `Mode()`, and the next switch reaches it — unlike the governor, where the notification is an **EDGE**. Same code shape, different severity; kept as a test, not a fix. |
| **go-jose — zero JWE reachability** | Chain from `go mod graph` / `go mod why` (not grep): `protocol/cloudflare → sing-cloudflared → go-oidc/v3 → go-jose/v4`. Every consumer uses **JWS only**: go-oidc references `JSONWebSignature`, `ParseSigned`, `SignerAlgorithm`, `NewSigner`, `JSONWebKeySet`, `EdDSA/ES/PS/RS` — and `JSONWebEncryption`, `NewDecrypter`, `Decrypt`, `Encrypter` **nowhere**. Same for `certificate-transparency-go`, `acmez`, `sing-cloudflared`. | CVE-2026-34986 is a **JWE-decryption** issue ⇒ not reachable. Bumped to 4.1.6 anyway (§1 #13) as cheap class-closure; the JWS path was verified **positive and negative** under both 4.1.3 and 4.1.6. |

---

## 3. Pending — not started, with the blocking precondition for each

| item | precondition to resume |
| --- | --- |
| **sing-tun `d092c64`** IPv6-only TCP accept-loop self-heal: rebase onto the real current pin `6937951` and bump the pseudo-version. | `d092c64` is **NOT an ancestor of pin `6937951`**. The fix + its production-path test (`stack_system_listenconfig_test.go`, 196 lines, the seam the pin lacks) already exist in `<WORKSPACE_ROOT>\sing-tun-fork` @ `fix/listenconfig-ipv6-only-selfheal`. **Explicitly not a second implementation.** Requires a cross-repo commit + `go.mod` pin bump. |
| **`sing` pause primitive root fix** | `sing` is a **separate repo with no local checkout**. Root cause located: `service/pause/default.go` `emit` (134-137) calls foreign callbacks **inline under `d.access`**. Target shape: *snapshot under lock → unlock → synchronous invoke* (contract: callback completes synchronously before `DevicePause`/`DeviceWake` returns — asserted at the boundary with **no grace period**). Fix belongs in `sing`, then a pin bump. Commit #1 works around it **only** on the registration side. |
| **URLTest lock order** — `protocol/group/urltest.go:1528-1567` `performUpdateCheck` holds `updateAccess` across `Interrupt()` @1563 and `history.NotifyUpdated()` @1566. | Needs a **real cycle or reentrancy** or a provable blocking risk first. No formalism unlocking without one. The correct pattern already exists in-tree at `common/urltest/urltest.go:294-310`. |
| **VLESS half-close** — `protocol/vless/encryption` `CommonConn`/`XorConn` under `mlkem768x25519plus`. | Needs a **runtime reproducer** first: is half-close upgraded to full close? If confirmed, fix by the same invariant as commit #5 (`WriterReplaceable`/`ReaderReplaceable`). No reproducer yet. |
| **SPEC 122 release** (§2) | Needs a guard test proving **no post-handshake read** of `HandshakeState` anywhere, THEN the release under that guard. Current evidence: production reads are **only** `common/tls/reality_client.go:271,297`, both pre-handshake (`prepareFirstFlight`, called from `ClientHandshake:233` before `HandshakeContext`), plus `utls_client.go:184,191` which are themselves pre-handshake. Grep-level evidence, **not yet a guard test**. Correct owner is the uTLS fork. |
| **`randomized` fingerprint** | Hybrid share comes from the dependency's own `KeyShare_Append_RandomGroups: 0.50` weight, not from a list this fork controls. Tightening means overriding a uTLS weight = changing a generated spec. Open by design, measured in the register. |

---

## 4. Out-of-repo probes — minimal archive

Directories are **not** in the repository and are not part of the diff. They live under `<WORKSPACE_ROOT>\`.
Environment for all three: `GOPATH=<WORKSPACE_ROOT>\_gotool\gopath`, `go` at `<WORKSPACE_ROOT>\_gotool\go\bin`, `GOPROXY` reachable.

### 4.1 `<WORKSPACE_ROOT>\utls-lifetime-probe` — SPEC 122 measurement

Module `utlslifetime`; `replace github.com/metacubex/utls => github.com/Piggy-Cat-bit-shadow/utls v1.8.8-0.20261009084700-6c3e08ed4c4d`. Real handshake over `net.Pipe` against a stdlib TLS1.3 server, then READ the state on the live conn.

```go
// The measurement: what a successful TLS1.3 handshake leaves on the CLIENT connection.
func measureRetained(uConn *utls.UConn) retainedHandshake {
	var r retainedHandshake
	if hello := uConn.HandshakeState.Hello; hello != nil {
		r.greetingReachable = true
		r.greetingRawBytes = len(hello.Raw) // the on-the-wire ClientHello: every extension
	}
	if keys := uConn.HandshakeState.State13.KeyShareKeys; keys != nil {
		r.keyShareReachable = true
		if keys.Ecdhe != nil {
			r.ecdhPrivateBytes += len(keys.Ecdhe.Bytes())
		}
		if keys.MlkemEcdhe != nil {
			r.mlkemEcdhePrivateSize += len(keys.MlkemEcdhe.Bytes())
		}
		if keys.Mlkem != nil {
			// uTLS keeps ML-KEM in internal/mlkem, so the field is reachable but not nameable
			// here: reflection gives the real size and type name without importing it.
			t := reflect.TypeOf(keys.Mlkem).Elem()
			r.mlkemPrivateType = t.String() // mlkem.DecapsulationKey768
			r.mlkemPrivateBytes = int(t.Size()) // 7808
			r.mlkemPrivateChunks = t.NumField() // 6
		}
	}
	return r
}
```

`handshakePair` builds a real handshake: `net.Pipe()`, `tls.Server` with a generated self-signed cert, `utls.UClient(...).HandshakeContext(ctx)`, then asserts `ConnectionState().HandshakeComplete`. Measured: 1750 B chrome / 1814 B / 1883 B firefox; 7808 B ML-KEM; 32 B ECDHE + 32 B MlkemEcdhe; TLS1.2 keeps greeting, `keyShareReachable=false`.

### 4.2 `<WORKSPACE_ROOT>\grpc-path-probe` — the `:path` bypass, measured

A grpc **client** cannot produce this request: `golang.org/x/net/http2` rejects it with `http2: invalid request :path "probe.Service/Echo"`. So the request is written as **raw frames**. `UnknownServiceHandler` is the discriminator (grpc calls it only AFTER dispatch), so `HANDLER_RAN false` means "rejected before dispatch".

```go
conn, _ := net.DialTimeout("tcp", address, 5*time.Second)
conn.Write([]byte(http2.ClientPreface))
framer := http2.NewFramer(conn, conn)
framer.WriteSettings()

var block []byte
enc := hpack.NewEncoder(bufferWriter{&block})
for _, f := range []hpack.HeaderField{
	{Name: ":method", Value: "POST"},
	{Name: ":scheme", Value: "http"},
	{Name: ":authority", Value: address},
	{Name: ":path", Value: path}, // verbatim: "probe.Service/Echo" or "/probe.Service/Echo"
	{Name: "content-type", Value: "application/grpc"},
	{Name: "te", Value: "trailers"},
} {
	enc.WriteField(f)
}
framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block, EndHeaders: true, EndStream: true})
// then read frames: HeadersFrame -> read grpc-status; RSTStream/GoAway -> protocol rejection
```

Server side: `grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, _ grpc.ServerStream) error { handlerRan.Store(true); return nil }))`, served over h2c.

| grpc | `:path` | `grpc-status` | `HANDLER_RAN` |
| --- | --- | --- | --- |
| **1.79.1** | `probe.Service/Echo` | `0` | **true** — dispatched |
| **1.79.3** | `probe.Service/Echo` | `12` (Unimplemented) | **false** — rejected before dispatch |
| both | `/probe.Service/Echo` | `0` | true (control: harness works) |
| both | unregistered service | `0` | true (control: routing works) |

Only `server.go` differs between the two versions (`rbac_engine.go`, `grpcutil/method.go`, `authz/*`, `http2_server.go` byte-identical).

### 4.3 `<WORKSPACE_ROOT>\jose-verify-probe` — JWS path under both go-jose versions

Module `joseverifyprobe`; JWKS served by `httptest`, verified through go-oidc's own entry point (the only place the fork's chain touches go-jose). **Passes identically on 4.1.3 and 4.1.6**, which is the point — and why it is not a repo test.

```go
// Sign a minimal ID token, publish the public half as a JWKS, verify through go-oidc.
signer, _ := jose.NewSigner(
	jose.SigningKey{Algorithm: jose.ES256, Key: key},
	(&jose.SignerOptions{}).WithType("JWT"))
raw, _ := jwt.Signed(signer).Claims(jwt.Claims{
	Issuer: probeIssuer, Audience: jwt.Audience{probeAud}, Subject: probeSub,
	Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour)),
}).Serialize()

doc, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
	Key: key.Public(), KeyID: probeKeyID, Algorithm: string(jose.ES256), Use: "sig"}}})
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(doc) }))
keySet := oidc.NewRemoteKeySet(context.Background(), srv.URL)

payload, err := keySet.VerifySignature(context.Background(), raw) // must succeed
```

| case | 4.1.3 | 4.1.6 |
| --- | --- | --- |
| valid token signed by the published key | verified | **verified** |
| token signed by a different key | rejected | **rejected** |
| `""`, `"   "`, `"{"`, `"{}"`, `"a.b"` | rejected | **rejected** |

---

## 5. Reproduce

```powershell
$env:PATH = "<WORKSPACE_ROOT>\xhttp-outperform\tools\mingw\mingw64\bin;<WORKSPACE_ROOT>\_gotool\go\bin;C:\src\MinGit\cmd;" + $env:PATH
$env:GOPATH = "<WORKSPACE_ROOT>\_gotool\gopath"
$env:CGO_ENABLED = "1"
$env:CC = "<WORKSPACE_ROOT>\xhttp-outperform\tools\mingw\mingw64\bin\gcc.exe"   # cgo/MinGW, required for -race
$tags = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()

go build -tags "$tags" ./...
go test -count=1 -race -tags "$tags" -timeout 1200s `
  ./transport/wireguard/ ./transport/device/ ./protocol/naive/ ./common/sniff/ `
  ./adapter/ ./experimental/cachefile/ ./common/power/ ./route/ ./daemon/ `
  ./experimental/boxdd/ ./experimental/clashmode/ ./common/tls/ ./experimental/libbox/
```

### Known-state caveats

| caveat | detail |
| --- | --- |
| **3 pre-existing failures** | `common/tls`: `TestWindowsClientHandshakeTLS13`, `TestWindowsClientRoundtripTLS13`, `TestWindowsClientTLS13PostHandshakeConcurrentWrite` — `tls handshake: read handshake: unexpected EOF` on the Windows Schannel path. **Fail identically on the untouched base**; unrelated to this branch. |
| **3 pre-existing `go vet` warnings** | `possible misuse of unsafe.Pointer`: `daemon/managed_service.go:75`, `experimental/libbox/debug.go:11`, `experimental/boxdd/authenticode_windows.go:220`. Pre-existing. |
| **cross-compile** | Needs `CGO_ENABLED=0`. Windows/linux/darwin amd64 all green. |
| **Darwin runtime** | Commit #3 is **NOT VERIFIED at runtime** — no Darwin host. Compile-only. |
| **PowerShell** | No heredoc: write commit messages to a file and use `git commit -F`. `Set-Content -NoNewline` unavailable — use `[System.IO.File]::WriteAllText`. |

### Evidence levels used

`MEASURED` (ran, output quoted) · `NOT VERIFIED` (stated as such, never as PASS) · `BLOCKED_BY_ENVIRONMENT` (command named) · `NOT REACHABLE` (derivation given) · `NOT OBSERVED` (detector clean, explicitly not "not a race"). Two in-commit self-corrections are preserved rather than rewritten: the `boxdd` escalation→fail-closed correction (#10) and the bbolt zero-length-key non-reachability (#8).
