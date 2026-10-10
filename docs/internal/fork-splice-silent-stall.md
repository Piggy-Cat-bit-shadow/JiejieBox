# Fork defect: a failed splice setup silently stops the connection instead of copying

**Status: cannot be fixed from sing-box.** The code is in the sing fork, which exists on this host only as a
read-only module cache. This note is the deliverable — with the exact reasoning and the one-line fix.

Affects: `github.com/Piggy-Cat-bit-shadow/sing` @ `v0.9.6-0.20261008194531-3af46fe99d3b`,
`common/bufio/splice_linux.go` + `common/bufio/copy.go`. **Linux only** (`splice_stub.go` returns early
everywhere else, so Windows and darwin never reach this path).

---

## The defect

```go
// common/bufio/splice_linux.go
func splice(...) (handed bool, n int64, err error) {
	handed = true                        // (1) claimed BEFORE anything is attempted
	var pipeFDs [2]int
	err = unix.Pipe2(pipeFDs[:], syscall.O_CLOEXEC|syscall.O_NONBLOCK)
	if err != nil {
		return                       // (2) handed stays true; n == 0
	}
```

```go
// common/bufio/copy.go, CopyWithCounters
for {
	handled, directN, directErr := copyDirect(...)
	n += directN
	if handled {                         // (3) true, so copyExtended is NEVER reached
		if errors.Is(directErr, N.ErrHandshakeCompleted) { refreshUnwrap(); continue }
		return n, directErr          // (4) ...unless the error is ErrHandshakeCompleted
	}
	extN, extErr := copyExtended(...)    // the userspace fallback - unreachable here
	...
}
```

So when `Pipe2` fails:

* `handed == true` terminates the copy loop,
* `copyExtended` — the backend that would actually move the bytes — is **never called**,
* and the error is returned to a caller that treats it as a normal teardown.

**The connection stops carrying data. Bytes copied is zero. No data-loss error is reported by the copy
engine.** To the session above it this is indistinguishable from an idle peer.

Note step (4): `handed=true` plus a non-`ErrHandshakeCompleted` error returns immediately rather than falling
back. That is deliberate for a *mid-stream* splice failure — the connection is already broken and cannot be
resumed in userspace — but it is the wrong response to a *setup* failure, where nothing has been attempted
and the userspace path is fully available.

## Reachability

`unix.Pipe2` fails with `EMFILE`/`ENFILE` (process or system file-descriptor limit) or `ENOMEM`. A proxy
holding many concurrent connections reaches the fd ceiling in ordinary operation, and that is precisely the
process-wide condition under which a new pipe cannot be allocated. So this is not a theoretical branch — it
is the branch that fires exactly when the process is busiest.

The same shape exists one step further in: the splice is driven through `RawConn.Read`/`Write` callbacks, and
if the first read returns zero bytes with a nil error, `writeSize` stays `0`, `writeFunc` is never invoked,
`unix.Splice` is never called, and the result is again `handed=true, n=0` — silent.

## Test coverage

`splice_linux_test.go` has exactly one test, `TestSplicePartialWriteCounters`, and it asserts
`require.True(t, handled)` **on the success path**. The setup-failure path has no test, which is why this
survived: the only assertion in the file confirms the flag is set, and the flag is set unconditionally.

## The fix (fork-side, one line)

```go
	err = unix.Pipe2(pipeFDs[:], syscall.O_CLOEXEC|syscall.O_NONBLOCK)
	if err != nil {
		handed = false               // let the copy engine fall back to copyExtended
		return
	}
```

Returning the error instead would close a working connection; returning `handed = false` routes the session
into the userspace copy that was always available. The `err` value should still be surfaced for logging —
`handed = false` with the error preserved lets the caller decide, which is the design the API already has for
"the syscall path was not usable": `copyDirect` returns `handed=false` for exactly this reason when
`SyscallAvailableForRead/Write` is false or the `RawConn` is nil.

A regression test would need to force `Pipe2` to fail — the fd limit is the natural lever (`setrlimit`
`RLIMIT_NOFILE`, or exhaust descriptors) — and assert that the copy makes progress and returns the data
rather than returning zero bytes with a nil error.

## Why this matters to this project's objective

The copy-backend census cannot be built from sing-box (see `ZEROCOPY_AUDIT_CLOSED.md` §1a: `handed` is
consumed inside the fork). This defect is the same seam viewed from the other side: `handed` is set
unconditionally at the top of the function, so it means *"the syscall path was selected"* and never *"the
splice ran"* or *"the data moved"*. Any census, fallback reason, or `copy_direct_selected` counter derived
from `handed` inherits that meaning — and on this specific branch it would report a **selected and successful**
syscall backend for a connection that copied nothing.

That is the exact failure mode the objective's item 5 names: a fallback reason that must not be fabricated.
Here the fabrication is the reverse direction — a non-fallback reported as a fallback — but it comes from the
same unconditional flag.
