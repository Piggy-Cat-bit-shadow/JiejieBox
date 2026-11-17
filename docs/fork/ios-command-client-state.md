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

### 2.1 A stable error prefix

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

The classification is decided by an independent probe of the endpoint (a `connect(2)` to the command
socket, or to the local TCP port), not by reading gRPC's error text. It is made only after a call has
failed, and only when the probe is DEFINITIVE: a timeout or a permission error is reported as the
original error, never as "the core is down".

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
| `notRunning` | nothing is listening: the core is stopped, crashed, or not started yet | tell the user the VPN is not running; do not retry the same operation in a loop |
| `unknown` | this process cannot probe the transport (XPC, remote daemon, unresolvable path) | treat as a normal error |

`detail` is bounded on purpose: a file name and a directory LENGTH, never a container path. It is safe
to log and to paste into an issue.

### 2.3 A dead connection is no longer reused

When a call fails and the probe proves nothing is listening, the client drops its cached connection.
The next operation re-establishes and probes once, instead of sending into a `grpc.ClientConn` whose
own reconnect backoff can be minutes long by then. **No RPC is retried automatically**: an operation
whose result nobody observed must not be applied twice. The caller sees one failure, and the next
call is the one that recovers.

## 3. The minimum change on the Apple side

Neither of these is implemented here; both are small and independent.

1. **Classify the failure before presenting it.** Where the client currently surfaces the error
   string from a control operation (the strategy-group expand path is the reported one), check
   `ProbeCommandServer()` first, or match the `command server is not running` prefix. Present
   "the VPN is not running" rather than a raw RPC error, and do not offer "retry" for that state: it
   cannot succeed until the tunnel is up.
2. **Re-drive the pending state once the tunnel reports connected.** `SetGroupExpand` is a UI-state
   write that the core stores; the local expansion state the user already sees can be kept, and the
   call re-issued when the extension reports `Connected`/`Reasserting` finished. This is what turns
   "my tap was lost" into "my tap applied a second later".

`SetGroupExpand`'s own contract is unchanged by this work: the call still sets one group's expansion
on the core, and nothing about the socket path, the authentication secret, or the group model moved.

## 4. What is verified, and where

| Claim | Evidence |
|---|---|
| the iOS error is classifiable as "the core is not running" | `experimental/libbox/command_client_control_test.go` (real gRPC server on TCP; the decision is transport-independent) |
| the next call recovers after the core returns | same file, `TestAClientRecoversWhenTheCoreReturns` |
| a live server's `Unavailable` answer does NOT tear the connection down | same file, `TestAServerAnswerOfUnavailableKeepsTheConnection` |
| a stale socket file is cleared, a live one is not stolen | `experimental/libbox/command_socket_claim_test.go` |
| the probe and the dialer agree on where the server is | `TestCommandEndpointMatchesDialTarget` |
| the unix transport itself, including the stale socket file | `experimental/libbox/command_transport_unix_test.go` — a real AF_UNIX listener through the production resolver, dialer, probe and classifier, then the file/listener state the incident reported, then recovery. Runs on any host where AF_UNIX works. |
| the iOS/NetworkExtension path itself | **NOT_RUN**: no device in this environment. The Go side, including the unix transport, is verified on the host; what remains is the device's own lifecycle (Jetsam, extension relaunch, App Group container). |
