# Upstream audit — design notes (2026-10-24)

These are the transferable conclusions from the 73-commit audit in
[upstream-2026-10-24-audit.md](upstream-2026-10-24-audit.md). They are about reusable mechanism, not
about the individual patches.

## 1. Lifecycle callbacks have an order even when the type system does not say so

`92fe54be05f` is a two-callback fan-out: `AttachFlow` and `CloseFlow` arrive from different goroutines,
and a tracker behind the fan-out can see them concurrently. The fork's `multiFlowTracker` was a slice;
the fix is a mutex around those two calls only. The general rule: **when two callbacks mutate the same
per-flow state and one of them destroys it, "they never overlap" is an invariant that must be encoded,
not assumed.** The per-packet counters stay lock-free because they neither destroy state nor depend on
the attach; serializing them would make the fix cost more than the race.

The same shape appears in the open queue: `37bddc915c02` (packet handshake failure skipping the close
handler), `cd00580d87a2` (packets after forward close) and `0164ce7b8e0a` (a connection inheriting an
interrupt mark) are all "the close path and the establish path disagree about ownership".

## 2. A nil interface is not a policy decision

`75eef0951443` is the clearest example. The fork already computed
`platformInterface != nil && platformInterface.UsePlatformInterface()` in `NewInbound`, then threw the
boolean away and kept testing `platformInterface == nil` at five later gates. On Android the platform
interface exists but may not be *used* for the interface; nil-ness therefore answers the wrong
question. The fix carries the boolean in the struct and replaces every gate.

This is the same class as the `fast_path` tri-state work: `unset` is not `false`, and "the object is
absent" is not "the operator said no". The fork's own knowledge base already states it for `fast_path`;
this commit extends it to the platform interface.

## 3. Control-plane listeners need the same address-family rule as the data plane

`fdc44acf6b14` changes three `net.Listen("tcp", addr)` calls (debug HTTP, Clash API, V2Ray API) to
resolve the address family from the configured address. `net.Listen("tcp", "[::]:9090")` on a
dual-stack host may bind v6-only or v4-mapped depending on the platform; a wildcard control endpoint
that silently accepts only one family is a policy difference, not a cosmetic one. The fix is small but
it is the same "listen on the family you meant" rule the inbound listeners already follow.

## 4. Compression is a wire contract, not an optimisation

`e2008d6b7dcb` sets `Compress = true` on the response copy in three paths: UDP truncation, the
stream (TCP) DNS writer, and the Tailscale peer-DNS reply. DNS responses are allowed to use name
compression, and some clients measure the uncompressed size; always compressing is the safe direction.
The fork had the same three `Pack`/`PackBuffer` calls without the flag.

## 5. Dependency-mediated fixes are invisible to main-repo tests

`03ab009ea1c` and `cc2f91b77a6` are one-line `go.mod` bumps. The TUN flow-table retention and the
system-stack closed-session retention live in `sing-tun`; the main tree's tests cannot see either, and
`go test ./...` passes exactly as before. The audit therefore had to follow each pin into the replaced
module. This is why the dependency ledger is a first-class deliverable and why "behind=0" alone would
be a false signal for these two fixes.

`3454d6d2411` is the same lesson in reverse: the MASQUE inbound backpressure needs a wait/wake API the
fork's `sing-tun` pin does not have. A main-repo patch that blocks without that API would turn a
drop into an unbounded stall.

## 6. What the fork already owns

The audit found 13 rows that are already equivalent or independently owned: the DNS cache-hit
rejection (`db4e5da711e` — the fork's `ErrResponseRejectedCached` path already runs the checker on the
cached response), the DNS search expansion removal (`972a052ef537`), the MASQUE feature set
(`306235ebc3d4`), the go TUN stack and gVisor retirement (`d91e1175f22c`, `5a1012dfeea6`), the AnyTLS
library migration (`0159d28b4098`), the HTTP proxy rewrite (`a579142add2f`), the idle-connection and
process-search work, and the FakeIP buffered cache. These are not "we forgot to look"; they are rows
with a cited fork implementation.

The practical consequence: **the upstream commit list is not a to-do list.** Roughly half of the 73
rows are either already present in a different shape or are dependency pins whose real content is
elsewhere. The audit's value is in the 40 rows that are not.

## 7. Why this round does not merge ancestry

An ancestry-only merge (`-s ours`) would make `behind_by=0` while 23 `MISSING_FIX` rows stay open. The
task brief forbids exactly that ordering. The audit and the sixteen ports (one partial) are the real
work; the merge is
withheld until the open table is empty, and the next round starts from
[upstream-2026-10-24-audit.md](upstream-2026-10-24-audit.md#open-work--the-missing_fix-rows) instead of
re-auditing the already-classified rows.
