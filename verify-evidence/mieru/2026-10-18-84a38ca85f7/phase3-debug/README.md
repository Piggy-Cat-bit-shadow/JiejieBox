# Mieru phase 3 — deep debug

Baseline: `789bf4893c6` (phase 2). Code fixes: `3f9303286e0`; this evidence/test commit follows it.
SDK: `github.com/enfein/mieru/v3 v3.38.1` (unchanged).
Scope: Mieru only; no other protocol, UI, release or shared-core redesign.

## What was actually wrong

Three P1 defects were reproduced and fixed, plus three P2 defects:

1. **The UDP listener was never released.** The SDK's `PacketUnderlay.Close` only wakes its
   read loop; it does not close the `PacketConn`. After `Inbound.Close`, the UDP port stayed
   bound, partial starts leaked the first listener, and a reload could not rebind. Fixed with
   a listener registry and close-on-close/partial-start/retry.
2. **Concurrent UoT reads raced on the framer's scratch arrays.** `net.PacketConn` and
   `net.Conn` permit concurrent readers; the race detector caught header/address interleaving
   before the fix. Fixed with a per-association read mutex and a race regression test.
3. **The outbound UDP underlay socket could survive Stop/rebuild.** The SDK's packet read
   loop can overwrite the close deadline; the adapter now owns and closes the generation's
   sockets on `Close` and `InterfaceUpdated`.
4. **Start on a closing scope returned nil for a closed object.** Now returns an error.
5. **SOCKS5 success was sent for unsupported commands/invalid destinations.** Now replies 7/1.
6. **`trackedConn` hid the authenticated user.** It now forwards `UserName()`.

Plus one framing hardening: an oversized UoT frame now validates its suffix before reporting
`io.ErrShortBuffer`.

See [BUG-LEDGER.md](BUG-LEDGER.md) for reproduction, root cause, fix, tests, performance and
residual risk per defect.

## Verification

- `go test ./protocol/mieru ./option ./include`: PASS (21.2 s).
- `CGO_ENABLED=1 go test -race ./protocol/mieru`: PASS (90.1 s).
- `go vet`, full tagged build, root package + `common/listener` smoke: PASS.
- 4-round old/new performance A/B: TCP within ±2%, UDP echo ±5% in opposite directions,
  latency/startup unchanged; raw cells in `performance/`.
- Real `mita` 3.38.1 was built from the official source and run on Windows (unmodified;
  foreground `mita run` with `MITA_INSECURE_UDS=1` for its Unix socket). Official client and
  target both reach it: TCP+UDP PASS; the 4-round matrix is in `mita/`. Against real mita the
  target is +15% / +14% on TCP download, +3% on UDP-transport download, +45% on UDP echo,
  but **first-byte P50 is ~2x** (1.0 ms vs 0.52 ms; P95 equal) and UDP-transport upload is
  0.739x. Those negatives are recorded, not hidden.
- Interop rerun after the fixes: official `mieru.exe` → this inbound TCP+UDP PASS; this
  outbound → official `apis/server` TCP+UDP PASS; logs in `interop-phase3/`.
- CI: final `testing` commit `931697f3a19`: Verify SUCCESS (18m37s, including race);
  Android Core ARM64 and Mobile contract SUCCESS.

`BLOCKED` (not PASS): mbox, extended, Hiddify (no WSL/Docker/scriptable Windows build). Real
`mita` is no longer in this list; the phase-2 "no Windows build" assumption was wrong and is
corrected in `mita/README.md`.
`NOT_TESTED`: 3000 short connections/long soak/loss matrix, TUN, iOS/Android devices, Apple
sleep/wake. The exact reasons are in the phase-2 `variants.json`/`RISKS.md`.

## Files

| File | Contents |
| --- | --- |
| `BUG-LEDGER.md` | BUG-001..007, each with repro, root cause, fix, tests, performance, residual |
| `TEST-MATRIX.md` | PASS / BLOCKED / NOT_TESTED matrix, commands, A/B table |
| `performance/old`, `performance/new` | 4-round summary, manifest, raw cells, `comparison.csv` |
| `mita/` | real mita 3.38.1 build/run, interop logs, 4-round official-vs-target matrix |
| `interop-phase3/` | rerun logs for both official directions |
| `reproduction.md` | exact commands to rebuild the evidence |
