# The command channel's state contract for the Apple client

**Scope:** what `experimental/libbox` now tells a UI about the core's control channel, and the
minimum change the Apple client needs. **The Apple client repository was not modified in this
change** — this document is the contract, not a claim that the UI was updated.

## 1. The incident

On 2026-11-17, expanding a strategy group in the iOS client produced:

```text
错误
Failed to update group expansion
set group expand: get command client: probe command server:
rpc error: code = Unavailable
  desc = connection error: desc = "transport: Error while dialing:
  dial unix /private/var/mobile/Containers/Shared/AppGroup/<uuid>/command.sock:
  connect: connection refused"
```

`ECONNREFUSED` on a unix socket is a specific fact. It is not ENOENT (no socket file) and not EINVAL
(the path-length failure of `docs/fork/ios-command-socket-path-fix.md`). It means **the socket file
exists and nothing is accepting on it** — which is what a Network Extension leaves behind when it
exits without running `Close`: Go's `UnixListener` unlinks its file on `Close`, and a process killed by
a fault does not reach `Close`.

The same device session had a SIGSEGV in the OOM profile writer (see
`docs/fork/go-1274-compatibility-report-2026-11-17.md` §C). The control channel's own state at that
moment — no listener, stale file — is the signature of that exit. The two reports are consistent with
one chain, which is why the fix below is about recognising "the core is gone" rather than about the
socket path.

## 2. What the kernel now reports

### 2.1 A stable error prefix, and what it does NOT prove

A call that fails because **nothing is listening** is returned as:

```text
<operation prefix>: command server is not running: <original cause>
```

for example, the incident above now reads:

```text
set group expand: command server is not running: rpc error: code = Unavailable desc = ...
```

The prefix is `ErrCommandServerNotRunning` (`"command server is not running"`). In Go,
`errors.Is(err, libbox.ErrCommandServerNotRunning)` is true. Across the gomobile bridge an error is a
string, so **match the prefix** or, preferably, ask the probe below.

**The error proves the channel was unreachable when the call failed. It does NOT prove the request was
never delivered or never applied.** A review of the first version of this contract found that the
implementation promised more than a post-hoc probe can know:

- the request may never have left the app (no listener, nothing could dial); **or**
- it may have been delivered, executed, and the reply lost when the transport died between the server's
  commit and the app's read.

A probe run afterwards cannot tell those apart. Treat the error as **"core unreachable, outcome
unknown"**: re-read the affected state before re-issuing anything with a side effect. That is why the
kernel still does not retry an RPC automatically - a retry is exactly the operation whose effect nobody
observed.

The classification is decided by an independent probe of the endpoint, not by reading gRPC's error text.
It is made only after a call has failed, and only when the probe is DEFINITIVE: a timeout or a permission
error is reported as the original error, never as "the core is down".

### 2.1a A late failure cannot clear a newer session

Reported by the same review and fixed: a failure of an OLD request used to close "the client's
connection", which by then could be a NEW connection that was healthy. A tap that failed before a
reconnect could therefore knock out the session the reconnect had just established.

Every request now carries the identity of the connection it ran on, and an invalidation only ever
applies to that connection. A failure whose connection has already been replaced is reported as the
original error and changes nothing - it is history, not a statement about the current session. Nothing in
the UI needs to change for this: it is the absence of a spurious disconnect.

### 2.2 A probe the UI can ask before acting

```go
func (c *CommandClient) ProbeCommandServer() *StringBox
```

returns a JSON document:

```json
{"state":"running"}
{"state":"notRunning","detail":"command.sock in a 96-byte directory"}
{"state":"unknown","detail":"remote daemon"}
```

| `state` | Meaning | What a UI should do |
|---|---|---|
| `running` | a listener accepted the probe | perform the operation; a failure is about the operation |
| `notRunning` | nothing is listening **right now** | tell the user the VPN is not running; do not auto-retry, and re-read state before re-issuing anything with a side effect |
| `unknown` | this process cannot probe the transport (XPC, remote daemon, unresolvable path) | treat as a normal error |

`state` describes the present only. `notRunning` says nothing about whether an EARLIER request was
applied - see §2.1.

`detail` is bounded on purpose: a file name and a directory LENGTH, never a container path. It is safe
to log and to paste into an issue.

### 2.3 A dead connection is no longer reused, and a stale one cannot take a live session with it

When a call fails and the probe proves nothing is listening, the client drops **the connection that call
ran on**. The next operation re-establishes and probes once, instead of sending into a
`grpc.ClientConn` whose own reconnect backoff can be minutes long by then. **No RPC is retried
automatically**: an operation whose result nobody observed must not be applied twice. The caller sees one
failure, and the next call is the one that recovers.

The drop is scoped to that connection (§2.1a). A failure delivered after a reconnect leaves the new
session alone.

## 3. The minimum change on the Apple side

Neither of these is implemented here; both are small and independent.

1. **Classify the failure before presenting it.** Where the client currently surfaces the error
   string from a control operation (the strategy-group expand path is the reported one), check
   `ProbeCommandServer()` first, or match the `command server is not running` prefix. Present
   "the VPN is not running" rather than a raw RPC error, and do not offer "retry" for that state: it
   cannot succeed until the tunnel is up.
2. **Re-drive the pending state once the tunnel reports connected - but re-read state first.**
   `SetGroupExpand` is a UI-state write that the core stores; the local expansion state the user
   already sees can be kept, and the call re-issued when the extension reports
   `Connected`/`Reasserting` finished. Because the error cannot prove the earlier call was not applied
   (§2.1), re-issue means *re-assert the desired state*, not *repeat the command*: read the group's
   current expansion and set it only if it differs.

`SetGroupExpand`'s own contract is unchanged by this work: the call still sets one group's expansion
on the core, and nothing about the socket path, the authentication secret, or the group model moved.

## 4. What is verified, and where

| Claim | Evidence |
|---|---|
| the iOS error is classifiable as "the core is not running" | `experimental/libbox/command_client_control_test.go` (real gRPC server on TCP; the decision is transport-independent) |
| the next call recovers after the core returns | same file, `TestAClientRecoversWhenTheCoreReturns` |
| a live server's `Unavailable` answer does NOT tear the connection down | same file, `TestAServerAnswerOfUnavailableKeepsTheConnection` |
| a stale socket file is cleared, a live one is not stolen | `experimental/libbox/command_socket_claim_test.go` |
| a second instance cannot even reach the probe while the first owns the path (the probe-then-remove window) | same file, `TestTheOwnershipLockSerialisesInstances`, `TestASecondInstanceCannotDeleteALiveSocket` |
| an undecidable probe refuses instead of deleting | same file, `TestClaimCommandSocketRefusesWhenItCannotTell` |
| a late failure cannot clear a newer connection | `experimental/libbox/command_boundary_test.go`, `TestAStaleFailureCannotInvalidateANewerConnection` |
| the probe and the dialer agree on where the server is | `TestCommandEndpointMatchesDialTarget` |
| the unix transport itself, including the stale socket file | `experimental/libbox/command_transport_unix_test.go` — a real AF_UNIX listener through the production resolver, dialer, probe and classifier, then the file/listener state the incident reported, then recovery. Runs on any host where AF_UNIX works. |
| the iOS/NetworkExtension path itself | **NOT_RUN**: no device in this environment. The Go side, including the unix transport, is verified on the host; what remains is the device's own lifecycle (Jetsam, extension relaunch, App Group container). |
