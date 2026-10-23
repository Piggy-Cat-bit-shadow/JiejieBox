# Upstream audit — debug and verification record (2026-10-24)

This is the reusable evidence for the audit in
[upstream-2026-10-24-audit.md](upstream-2026-10-24-audit.md). It records what was run, what the tests
actually pin, and what remains unverified.

## Snapshot

| item | value |
|---|---|
| Upstream tip observed | `824dbcfb4dd0a95921840b1737b950cde590241e`, 2026-10-23 22:39 +0800 |
| Fork tip at audit | `6ac2a1f9ba5049ef531631402e9cab7192804855` |
| Merge base | `d630fa94c53b2e2bdf256fad8b9d1cff809146ed` |
| Upstream-only commits | 109; 17 patch-equivalent; **73 authored after the last absorbed tip** |
| Toolchain | Go 1.25.5, `DEFAULT_BUILD_TAGS_OTHERS`, `CGO_ENABLED=0` for the normal gates |

## How the queue was derived

1. `git fetch upstream testing` (HTTP/1.1; the smart-HTTP TLS handshake flakes on this host).
2. `git rev-list --left-right --count HEAD...upstream/testing` → 2562 fork-only / 109 upstream-only.
3. `git cherry -v HEAD upstream/testing` → 17 commits already have a patch-equivalent in the fork.
4. Author-date cut at the last absorbed upstream tip (`e4b7e692f`, 2026-10-11 14:24 +0800): 73
   commits. This matters because upstream rewrote `testing` again on 2026-10-23: `e4b7e692f` is no
   longer an ancestor of the current tip, and the SHA-containment count overstates the new work.
5. An added-line presence probe (same method as the earlier `docs/fork/line-presence.sh`) was used only
   as triage; the classification comes from reading the fork code, not from the ratio.
6. Eight parallel read-only audits, one table row per commit, with the fork file:line in
   `fork_evidence`; the raw result is [upstream-2026-10-24-audit.json](upstream-2026-10-24-audit.json).

## What was actually run for the ports

| command | result |
|---|---|
| `gofmt -w` on every touched file | clean |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | PASS |
| `go test -count=1 -tags <tags> ./protocol/direct ./protocol/redirect ./protocol/group ./transport/wireguard ./dns ./protocol/dns ./protocol/tailscale` | PASS (redirect and protocol/dns have no test files) |
| `go test -count=1 -tags <tags> ./protocol/cloudflare ./protocol/openvpn` after the `go get` pins | PASS |
| `go test -count=1 -tags <tags> -run 'TestMultiFlowTracker' ./route` | PASS |
| `go test -count=1 -tags <tags> -run 'TestICMPLoopback' ./protocol/direct` | PASS |
| full `./route ./protocol/tun` | see the round report; the route suite is the long one |

`-race` was not run for this batch: the host needs `CGO_ENABLED=1` + mingw (the earlier TUN round used
that combination), and the two new tests are deterministic rather than race-detector tests. This is a
limitation, not a PASS.

## The two new negative controls

### `route/flow_tracker_serialization_test.go`

`TestMultiFlowTrackerSerializesAttachAndClose` holds `AttachFlow` open inside a tracker and starts
`CloseFlow` concurrently. With the old `multiFlowTracker []tun.FlowTracker` the close enters while the
attach is still inside the tracker and the test fails at the `select`; with the locked struct the close
waits for the mutex, the timer fires, and the release unblocks both. It is deterministic (a channel
handshake, not a sleep race).

`TestMultiFlowTrackerLeavesCountersLockFree` pins the half upstream deliberately left unlocked:
`CountForward` must return while `AttachFlow` holds the lifecycle lock, or every byte would serialize
behind a slow close.

### `protocol/direct/icmp_loopback_test.go`

`TestICMPLoopbackToTheTunRangeIsRejected` builds an `Outbound` with its `myAddresses` set to
`198.18.0.0/15` and a non-nil ICMP port, then checks the three actions: reject inside the range,
`PreMatchFlow` for a real destination, `PreMatchContinue` for TCP. Before the port the first assertion
returns `PreMatchFlow` — a ping to the TUN's own address would be fed back through the ICMP port.

## What the audit did NOT verify

- No device, no real TUN, no Linux namespace host, no macOS/iOS signing: every platform-specific row
  (Apple TLS engine, Android procfs, Windows weak-host send, Tailscale SSH, WireGuard darwin) is
  `CODE_PROVEN` at best, never `TESTED` on the target.
- The two dependency-mediated TUN retention fixes are not in the tree; `03ab009ea1c` and
  `cc2f91b77a6` are one-line `sing-tun` pin bumps, so the main-repo tests cannot see them.
- CI on the final SHA of this round is reported by the round report, not by this file.
