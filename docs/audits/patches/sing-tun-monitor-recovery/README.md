# Prepared patch: the netlink monitor recovers when its subscription dies

**Behaviour is verified in both directions** on the pinned revision. The patch is **not pinned**: the pin
needs a sing-tun revision plus `release/source-lock.json` and a CI run for that revision.

## What it changes

`github.com/sagernet/sing-tun` at the pinned revision `v0.9.7-0.20261109145802-79ed0653fdc6`,
`monitor_linux.go`:

- `Start`'s socket creation and bind move into `openSocket()`, so the recovery path binds exactly like
  the first one (same groups, same buffer sizing, same gVisor `EOPNOTSUPP`/`EACCES` handling);
- `loopRead` no longer returns on a read error. It closes the dead socket and reopens it with
  exponential backoff (1 s doubling to 30 s), aborting on `Close()`, and emits **one** update after a
  successful reopen so the layer above re-reads interfaces and routes it may have missed;
- the socket handle gains a mutex and `Close` clears it, so a concurrent recovery cannot resurrect a
  closed monitor.

Why: the pinned code tolerates `ENOBUFS` and returns on every other read error. After that the monitor
never reports anything again - the process keeps running with a dead event source, and TUN/routes/outbound
bindings stop following the system until restart. Upstream `SagerNet/sing-tun#110` records the opposite
symptom (a 100% CPU spin); this fork's rewrite traded the spin for silence, and neither is a monitor that
recovers.

## The behaviour evidence

`monitor_recovery_test.go.txt` (copy it into the sing-tun copy as `.go`; it needs root and Linux because
it uses netlink). It starts the monitor, drains, **closes the subscription** the way an unrecoverable read
error leaves it, waits long enough for a recovery loop to reopen (2.5 s), then creates a dummy link and
requires an update.

| Revision | Result | Transcript |
| --- | --- | --- |
| pinned, unpatched | **FAIL** — "no update after the subscription died: the monitor never reopened its netlink socket" | `sing-tun-monitor-recovery-unpatched.txt` |
| pinned + this patch | **PASS** (3.52 s), and the pre-existing overrun test still **PASS** (4.52 s) | `sing-tun-monitor-recovery-patched.txt` |

```
# in a copy of the pinned module, with the test file beside monitor_linux.go
wsl -u root -- bash -lc 'cd <copy> && go test -run "TestNetworkUpdateMonitor" -count=1 -v -timeout 180s .'
```

## Limits, stated

- The test closes the socket directly from the test goroutine, which is a test-only unsynchronised access
  to the handle; the behaviour check is therefore run **without** `-race`. The patch itself guards the
  handle with a mutex; a `-race` run of the package (not just this test) is part of what the sing-tun CI
  for the new revision should do.
- The patch does not add jitter to the backoff and does not cap the number of attempts: the loop runs
  until `Close`, which matches the "event source must recover" requirement but means a permanently
  refused bind retries forever at 30 s intervals.
- This is a *model* of the recovery, not a test of every failure mode: `ENOBUFS` behaviour is unchanged,
  and the `EOPNOTSUPP`/`EACCES` path still reports a working monitor with nothing to report (gVisor).

## Still open

1. **The consumer.** Nothing in JiejieBox's `common/networkmanager` recreates the monitor; whether the
   recovery above is enough for a TUN that changed its addresses while the subscription was dead depends
   on the update callback re-reading interfaces, which is what the patch's post-recovery `emit` is for -
   not verified end to end.
2. **The pin.** A sing-tun revision, `release/source-lock.json`, `python scripts/ci/release-freeze.py
   verify --lock release/source-lock.json`, and the CI run for that revision.
