# External and cross-tree design study

Every claim below names the artefact it was read from. Local module sources are the versions the build
actually uses; upstream sing-box is read from the fork's `upstream/testing` ref (SHA-verified below).
One item is marked `UNVERIFIED_EXTERNAL` because this environment cannot fetch the page, and it is not
used to justify any change.

| Artefact | Version / ref | How read |
|---|---|---|
| Mieru SDK | `github.com/enfein/mieru/v3 v3.38.1` | module cache, full source |
| quic-go (the replacement this fork builds) | `github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261011111900-4d740322a18a` | module cache, full source |
| upstream sing-box | `upstream/testing` = `dfb80349bf5779ec0a3f834c9ec2794345bff8f0` | `git show` of the tree |
| this fork's prior art | `c363e04bb3065de68e2123182013124cbb65a900` | working tree |

## 1. The Mieru SDK's handshake is context-blind for 10 s

**Observation.** `apis/internal/handshake.go`:

```go
if err := req.WriteToSocks5(conn); err != nil { ... }
common.SetReadTimeout(conn, 10*time.Second)
if err := resp.ReadFromSocks5(conn); err != nil { ... }
```

`PostDialHandshake(conn, destination)` takes no context. `apis/client/client.go:152` calls it after
`mux.DialContext(ctx)` has returned, so the caller's deadline and cancellation have no effect once the
underlay exists (measured: a 500 ms budget returned after 10.002 s).

**Correspondence.** This is the exact floor of the Mieru silent-blackhole experience.

**Can existing interfaces close it?** No. There is no error to classify earlier — the transport is
waiting for bytes that never come. `FailureFacts`/Admission/epoch are all pre- or post-attempt facts.

**Applicability.** Mieru: decisive. MASQUE: not applicable (different protocol). Group health: none.

**Cost of changing it.** Only a forked/vendored SDK (GPLv3, upgrade path, divergence). Adoption of a
per-dial goroutine to abandon the call early was rejected — see ARCHITECTURE-DECISIONS.

**Decision: adopt the fact, reject the shortcut.** Classify the timeout correctly (caller vs
transport), fall back when it is transport, and state the 10 s floor honestly. A wrong-credential
attempt remains indistinguishable from a blackhole for exactly this reason.

## 2. The SDK destroys the typed socket error with `%v` — the proof exists, but only briefly

**Observation.** `pkg/protocol/mux.go`:

```go
return nil, fmt.Errorf("NewTCPUnderlay() failed: %v", err)
return nil, fmt.Errorf("NewUDPUnderlay() failed: %v", err)
return nil, fmt.Errorf("AddSession() failed: %v", err)
```

while `pkg/protocol/underlay_stream.go` wraps correctly (`"DialContext() failed: %w"`). The chain
therefore survives to the mux and is replaced by a string there.

**Correspondence.** This is why every previous classifier in this package had to match English prose,
and why a Windows refusal in the system language was not provable.

**Can existing interfaces close it?** Yes, with no new public interface: capture the error at the
bridge this package already owns (`serverDialer`), before the SDK flattens it.

**Applicability.** Mieru only. The group keeps consuming `FailureFacts`; MASQUE has its own error
boundaries.

**Cost.** One context wrapper per SDK dial (measured 45 ns, 48 B, 1 alloc; one per new session, never
per packet). No lock on the success path beyond one record, no goroutine, no timer.

**Decision: adopted** (`dial_observation.go`, `classifySocketError`).

## 3. quic-go's handshake bound is 10 s, not 15 s — the 3 s window is the only way H2 gets a turn

**Observation.** In the quic-go build this fork uses:

```go
// config.go
func (c *Config) handshakeTimeout() time.Duration { return 2 * c.HandshakeIdleTimeout }
// internal/protocol/params.go
const DefaultHandshakeIdleTimeout = 5 * time.Second
// connection.go
} else if !c.handshakeComplete && now.Sub(c.creationTime) >= c.config.handshakeTimeout() {
    c.destroyImpl(qerr.ErrHandshakeTimeout)
```

`httpclient.NewQUICConfig` does not set `HandshakeIdleTimeout`, so an H3 connection to a silent peer is
destroyed after 10 s — below the MASQUE caller's 15 s. `DialEarly` returns before the handshake, so the
wait is inside the CONNECT setup, exactly where the tunnel branch had no bound.

**Correspondence.** The MASQUE "H3 never falls back" bug: the caller's 15 s expired first, and the
returned error was the caller's deadline, not `ErrHTTP3Unavailable`.

**Can existing interfaces close it?** No — the transport layer must decide how long one version
attempt may take before the version fallback is allowed to run. It is a policy of
`transport/http`, which already owns `http3EstablishTimeout` for streams.

**Applicability.** MASQUE CONNECT-IP and CONNECT-UDP (both go through `openTunnel`). Mieru: not
applicable. Group: none.

**Cost.** One child context per tunnel setup attempt (already created later by `openHttp2` etc.);
no new goroutine; the window only changes when the fallback starts.

**Decision: adopted**, as a separately tunable `http3TunnelEstablishTimeout = 3 s`, justified by
"4 round trips at 750 ms" and by leaving 12 s of the 15 s budget. Measured loopback setup: 3.4 ms.

## 4. Upstream sing-box has no H3 establishment window at all — the fork's stream fix was never generalised

**Observation.** `git show upstream/testing:transport/http/client.go`:

```go
if c.http3Available() {
    conn, err := c.http3.DialContext(ctx, destination)
    ...
    if c.disableVersionFallback || !errors.Is(err, ErrHTTP3Unavailable) { return nil, err }
    c.markHTTP3Broken()
}
```

There is no `EstablishTimeout` in the file, and `upstream/testing:transport/http/tunnel_client.go`
passes `ctx` straight to `c.http3.OpenTunnel` with the same error gate. The fork added the stream
window (`http3EstablishTimeout`) in an earlier round; the CONNECT-IP branch was not included.

**Correspondence.** Confirms the gap is not an artefact of reading only the fork: it exists upstream,
and the fork's own fix stopped at the stream path.

**Decision: adopted as the second half of finding 3** (the fork-generalisation), with no upstream
change attempted from here.

## 5. The fork's own Windows errno work is the right classifier; string matching was the wrong layer

**Observation.** `common/dialer/windows_socket_errors.go` documents that Go's Windows `syscall.Errno`
constants are not the Winsock numbers (`syscall.ENETUNREACH` is `0x20000043`, the wire value is
`10051`), that the same defect appeared three times in this tree, and that `ErroredWith` plus the
exported sets (`PathFailureErrnos`, `PeerAnsweredErrnos`, `ResetErrnos`, `UnreachableErrnos`,
`TimeoutErrnos`) exist so no caller restates them. `protocol/group/loadbalance_failover.go` already
consumes them for retry/penalty.

**Correspondence.** Mieru was the one consumer still matching English text; on Windows that text is
localised, and the syscall constants would never have matched anyway.

**Can existing interfaces close it?** Yes — this is exactly the "reuse the common entry" the
directive asks for, once finding 2 makes the typed error reachable.

**Applicability.** Mieru (implemented). The same bridge shape is a candidate if another protocol in
this fork flattens errors the same way; not changed here.

**Cost.** None beyond finding 2.

**Decision: adopted.**

## 6. Precedent from the fork's own MASQUE outcome ordering: stamp the attempt before you start it

**Observation.** `transport/http/client.go` `beginHTTP3Attempt`/`claimHTTP3Outcome`: the attempt
sequence number is taken **before** the attempt, because "newer" must mean "started later", not
"reported later"; a failure superseded by a newer success must not arm the broken memory, and a stale
success must not clear a newer failure. This was a real measured race (`1758/200000` in the reset
case, per the source comment).

**Correspondence.** The new tunnel window must not introduce its own ordering bug: a window-expiry
failure for an attempt that a newer attempt has already superseded must be ignored, and a success must
clear the right one. The implementation reuses `beginHTTP3Attempt`/`markHTTP3Broken`/`clearHTTP3Broken`
rather than adding new bookkeeping, so it inherits the ordering rule.

**Cost.** None (the existing atomics).

**Decision: adopted by reuse** — one of the reasons the change is small.

## 7. The SDK already has a 5 s session-clean ticker per underlay — do not add a health ticker

**Observation.** `pkg/protocol/underlay_base.go:39` `sessionCleanInterval = 5 * time.Second`;
`underlay_stream.go:108` and `underlay_packet.go:97` start a ticker for it. So "the selector has no
background work" does **not** mean "enabling two SDK clients has no background work"; there is a
per-underlay ticker once a transport is used, plus the Mux's own cleanup.

**Correspondence.** A proposed "early detection by probing every N seconds" would add a second,
worse source of wakeups on top of an existing one, and would still not prove a remote UDP endpoint
unreachable.

**Decision: rejected** — no probe, no ticker, no background goroutine was added this round. The
observation is recorded so a future round does not re-litigate it as "the SDK is idle anyway".

## 9. The SDK's own shutdown has an ordering race that a caller can only fix by closing its sockets first

**Observation.** The server `PacketUnderlay` is created when the UDP listener starts
(`pkg/protocol/mux.go`, `udp` case). Its event loop blocks in `ReadFrom` with a randomised
60–120 s deadline (`pkg/protocol/underlay_base.go`: `readOneSegmentTimeout = 60 + rng.FixedIntVH(61)`).
`PacketUnderlay.Close` (`pkg/protocol/underlay_packet.go`) does, in order: stop the clean ticker,
`SetReadDeadline(time.Now())`, then `baseUnderlay.Close()` which closes sessions and **then** closes
`done`. The woken read can therefore re-arm a fresh 60–120 s deadline before `done` closes, and
`Mux.Close` waits for that loop (`serverUnderlayLoopWG.Wait()`).

**Correspondence.** This is the 65 s stall found while settling this round: a *listener shutdown* that
the caller cannot shorten from inside the SDK API, but can from outside — by closing the `PacketConn`
the fork created (the SDK's `PacketUnderlay.Close` explicitly does not close it; the previous round
already recorded that fact for the client side).

**Can existing interfaces close it?** It is not a `FailureFacts`/Admission matter at all. It is an
ownership fact: the fork owns the listener socket, so the fork can unblock the read.

**Cost.** None: the socket was going to be closed microseconds later anyway; the ordering change only
moves it before `Stop`.

**Decision: adopted** (see the ledger, M9). This is also a good example of the study's own rule: the
SDK is not "the thing that owns shutdown" — the adapter owns the sockets it created.

## 10. Mieru's `NO_WAIT` handshake is not a recovery probe

**Observation.** `apis/client/client.go:138-151`: with `HANDSHAKE_NO_WAIT`, the client returns an
`EarlyConn` **before** `PostDialHandshake`, and the fork rejects `fallback` + `no_wait` in
`resolveFallbackOptions` for this reason.

**Correspondence.** An idea worth testing: use a cheap no-wait dial to verify primary recovery without
user payload. It cannot work: the dial returns success before the server has authenticated or answered,
so it would "prove" recovery on exactly the dead and misconfigured endpoints the probe exists to
reject.

**Decision: rejected** (kept the real request as the probe, single-flight, as before).

## 11. curl's layered connection racing — `UNVERIFIED_EXTERNAL`, not adopted

A web search (the environment cannot fetch external pages) surfaced curl's Happy Eyeballs material
(`https://everything.curl.dev/usingcurl/connections/happy.html`) and curl issue #19740
("Alt-Svc breaks Happy Eyeballs v2: Both ballers use QUIC instead of QUIC+TCP fallback",
`https://github.com/curl/curl/issues/19740`). The relevant idea — racing a new transport against the
old one with a short stagger and keeping the winner — was **not** adopted here: a CONNECT-IP tunnel is
long-lived and a lost race means a whole second connection to the same MASQUE server, while Mieru's
two transports share one account and would double the connection budget on the healthy path. Both are
explicitly outside what this round is allowed to do. The citation is recorded only as a direction for
a future measurement, not as evidence for any claim above.

## What was searched for and could not be verified

- mihomo's Mieru adapter source: not present locally this round, and external fetch is blocked. The
  prior round's interop run against mihomo configurations is the only mihomo evidence
  (`verify-evidence/mieru/2026-10-18-*`); no adapter-internal claim is made here.
- Chromium Alt-Svc/QUIC failure memory and WireGuard handshake recovery: not read from source this
  round, therefore not cited as a basis for a decision.
