# Mieru phase 3 — debug ledger

Baseline for this round: `789bf4893c6` (phase 2 evidence at `2026-10-18-84a38ca85f7`).
Code fixes: `3f9303286e0` (plus the test-only follow-up in this evidence commit).
SDK: `github.com/enfein/mieru/v3 v3.38.1` (unchanged).
Host: Windows 10 LTSC 19044, Go 1.26.8, AMD Ryzen 7 4800H, loopback only.

All fixes were first reproduced by a failing/racing test; the test names below are in
`protocol/mieru/*_test.go` and are part of the same tree.

## BUG-001 — P1 — UDP listener was never released; partial Start leaked it

**Affected:** `protocol/mieru/inbound.go` listener factories and `Close` (before `3f93032`).

**Reproduction:** `TestMieruInboundCloseReleasesListener`. Start a `transport: both`
inbound, close it, then bind TCP and UDP on the same port. Before the fix TCP bound and UDP
returned `bind: Only one usage of each socket address`. The same failure appeared after a
partial Start (`TestMieruInboundPartialStartClosesTheFirstListener`,
`TestMieruInboundPartialStartClosesTheUDPListener`) and blocked rebind after reload
(`TestMieruInboundCanRebindAfterClose`).

**Root cause:** the SDK's `PacketUnderlay.Close` only sets a read deadline and closes its
sessions; it does not close the `PacketConn` returned by `PacketListenerFactory`. The Mux
listener loop also closes the raw TCP listener from a context goroutine that `Mux.Close`
does not join. The adapter returned raw listeners and did not track them, so the UDP fd
stayed bound after the inbound was gone. On a partial start the same fd was left behind
because only the SDK's own cleanup ran.

**Fix:** a `listenerRegistry` in `Inbound`; `streamListenerFactory`/`packetListenerFactory`
return `trackedListener`/`trackedPacketConn` wrappers that unregister on `Close`.
`Inbound.Close` closes the registry after `server.Stop`; `Inbound.Start` closes and resets
it on a failed start (so a failed start is retryable). The registry refuses new listeners
once closed, which also covers a late factory call during shutdown.

**Regression tests:** `TestMieruInboundCloseReleasesListener`,
`TestMieruInboundPartialStartClosesTheFirstListener`,
`TestMieruInboundPartialStartClosesTheUDPListener`,
`TestMieruInboundCanRebindAfterClose`,
`TestMieruInboundCanRetryAfterBindFailure`,
`TestMieruInboundCanRetryAfterUDPBindFailure`.

**Performance:** setup/close only; no data path. Four-round A/B below shows no systematic
change. **Residual:** Windows can release a closed TCP port asynchronously; the retry test
allows up to 2 s for the rebind, while the registry assertion proves the adapter called
`Close` synchronously.

## BUG-002 — P1 — concurrent UoT reads shared one framer

**Affected:** `protocol/mieru/uot.go` `uotFramer` (before `3f93032`).

**Reproduction:** `TestUOTPacketConnConcurrentReads`, run with `-race`. Eight concurrent
`ReadFrom` calls on one `uotPacketConn` produced:
`WARNING: DATA RACE` between `net.(*pipe).read` → `readFrame` (write of `frameHeader`) and
`binary.bigEndian.Uint16` → `readFrame` (read of the length); packets were reported with
prefixes `0x01`, `0x02`, `0x35`, `0x63`, and several were lost. `net.PacketConn` and
`net.Conn` both permit concurrent readers, so this is a contract violation, not just a test
artifact.

**Root cause:** the framer reuses `frameHeader`, `udpHeader`, `addressType`, `ipv4`,
`ipv6`, `fqdn` and `remaining` across calls. Two readers interleave their headers and
address reads.

**Fix:** `uotFramer.access sync.Mutex` around `readFrame`. The write path is unchanged
(`writeUOTFrame` allocates its own frame; `Session.Write` serialises stream writes).

**Regression test:** `TestUOTPacketConnConcurrentReads` under `-race`.

**Performance:** microbenchmark read path is 88.79 ns/op, 0 B, 0 allocs (was 79.83 ns/op
before the mutex; +9 ns). Four-round end-to-end UDP echo: tcp-low 0.946x, tcp-high 1.057x,
udp-low 0.954x — mixed and within run variance; TCP cells unchanged. Correctness wins over
9 ns on a 1400-byte datagram; no production reader is concurrent today, but the interface
allows it.

## BUG-003 — P1 — outbound UDP underlay socket could survive Stop/rebuild

**Affected:** `protocol/mieru/dialer.go` `serverDialer.ListenPacket`, `outbound.go`
`Close`/`InterfaceUpdated` (before `3f93032`).

**Reproduction:** a raw official-client harness with a fake packet dialer that counts
`Close`. Under the full `-race` suite the counter stayed 0 for 5 s after `client.Stop()`
although the same test passed alone; the SDK's packet loop can overwrite the wake-up
deadline with `readOneSegmentTimeout` after `PacketUnderlay.Close` set it to now, leaving
`ReadFrom` blocked and the socket open. The socket was only released if the event loop
happened to observe the deadline before it was overwritten.

**Root cause:** same SDK ownership gap as BUG-001 on the client side, plus a lost wake-up
race between `PacketUnderlay.Close` and `readOneSegment`.

**Fix:** `serverDialer` tracks every `dialPacketConn` it creates. `Outbound.Close` calls
`shutdownPacketConns()` after stopping the client; `InterfaceUpdated` closes the old
generation's sockets after the previous client stops and leaves the registry open for the
new generation. `Close` is idempotent and refuses late registrations.

**Regression tests:** `TestServerDialerClosesPacketGeneration`,
`TestMieruOutboundUDPCloseReleasesUnderlaySockets`,
`TestMieruInterfaceUpdateReopensUDPUnderlay`.

**Performance:** close path only. **Residual:** a dial that exceeds the 5 s drain timeout
can still register a socket after `InterfaceUpdated` has drained; the next update/Close
closes it. The final `Close` refuses it.

## BUG-004 — P2 — Start on a closing scope returned nil for a closed object

**Affected:** `protocol/mieru/inbound.go` and `outbound.go` `Start`.

**Reproduction:** `TestOutboundStartOnClosingScopeDoesNotDeadlock` and
`TestMieruInboundStartOnClosingScopeDoesNotDeadlock` previously asserted `NoError`.
`scope.Add` runs the cleanup synchronously on an already-closing scope, so the object was
closed and `Start` still returned nil.

**Fix:** after `scope.Add`, re-check `closed` and return an explicit error. The existing
tests now require an error and still prove there is no deadlock.

## BUG-005 — P2 — SOCKS5 success was sent for requests the server could not route

**Affected:** `protocol/mieru/inbound.go` `handleConnection`.

**Reproduction:** `TestHandleConnectionRejectsBeforeSuccess` (unsupported command and a
destination port of 70000). Before the fix the first response byte pair was `05 00`
(success) and the connection was then closed.

**Fix:** parse the destination and validate the command before writing the response; send
`Socks5ReplyCommandNotSupported` (7) or `Socks5ReplyServerFailure` (1), log, and close.
Valid CONNECT/UDP ASSOCIATE paths still receive the synthetic success mita sends.

## BUG-006 — P2 — trackedConn hid the authenticated user

**Affected:** `protocol/mieru/inbound.go` `trackedConn`.

**Reproduction:** `TestTrackedConnPreservesUserName`. The router metadata got the user from
the raw SDK session, but the handed-off `trackedConn` did not implement
`apicommon.UserContext`, so a downstream capability check saw an anonymous connection.

**Fix:** forward `UserName()` from the wrapped session and assert the interface at compile
time. No credential is logged.

## BUG-007 — P2 — oversized UoT frame did not validate its suffix

**Affected:** `protocol/mieru/uot.go` short-buffer path.

**Reproduction:** `TestOversizedFrameRejectsBadSuffix`; a too-large frame ending in `0x00`
was reported as `io.ErrShortBuffer` without checking the suffix.

**Fix:** discard exactly the payload, read and validate the suffix, then return
`io.ErrShortBuffer`. The frame remains consumed (the caller cannot use a truncated
datagram), but a malformed frame is no longer indistinguishable from a valid oversized one.

## Not a bug — duplicate/overlapping port ranges

`FlatPortBindings` deduplicates ports per protocol, so `server_ports: ["9000-9001",
"9000-9001"]` or a range that repeats the primary port is accepted and produces one
endpoint per port. The table cases that expected an error were removed; zero, reversed,
out-of-range and empty ranges still fail with `invalid port binding`.

## Out of scope

No other protocol, UI, release, Apple lifecycle or shared-core area was modified. The only
shared files touched by this round remain `protocol/mieru`, `option/mieru.go` (unchanged),
and the phase-1 `common/listener` extraction already covered by its own tests.
