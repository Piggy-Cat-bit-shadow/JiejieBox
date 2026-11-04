# iOS hotfix — the command socket could not bind

Reported from a device, on an experimental IPA re-signed with a third-party profile:

```
Failed to start service (packet-tunnel): start command server error:
listen command server: listen unix
/var/mobile/Containers/Data/PluginKitPlugin/BCBBA237-…/Library/Application Support/JiejieBox-Shared/command.sock:
bind: invalid argument
```

## 1. What failed, exactly

The chain is four steps and every one of them is a decision made earlier in this line of work:

1. The App Group container is unavailable to a re-signed build, so `FilePath` falls back to a
   **private** container (`Library/Shared/FilePath.swift`).
2. That directory is handed to libbox as `LibBoxSetupOptions.basePath`
   (`ExtensionProvider.swift`).
3. The command server joins `command.sock` onto it (`experimental/libbox/command_server.go`).
4. `bind(2)` receives a 138-byte name. Darwin's `sockaddr_un.sun_path` is **104 bytes**, and the
   value passed is a C string, so the maximum usable path is **103 bytes**. `bind` answers
   `EINVAL` — which reads like a malformed address, not an over-long one.

The tunnel therefore started, reached `startService`, and died creating its own control socket.

## 2. The fix

`experimental/libbox/command_socket.go` resolves the socket path for **both** the server and the
client through one rule:

* the base path's `command.sock` when it fits in 103 bytes (unchanged behaviour, and the only case
  on a correctly signed build);
* otherwise a deterministic `jjb-<crc32 of the full path>.sock` under the **process temporary
  directory**, which on iOS is ~70 bytes deep, so the result fits;
* if even that is too long, the server fails with a message naming both lengths instead of
  letting `bind` return an opaque `EINVAL`.

Deterministic is required: the server and the client resolve the path independently, and the
socket is how they would otherwise talk. A truncation was rejected — it could collide with a
different directory and would name a path that does not exist.

Five tests pin it: the reported container path really is longer than 103 bytes; a long base path
resolves to a short, parsable socket name outside that container; a short base path is used
verbatim; the fallback is stable for one base path and different for another; the fallback lives
in the process temp directory.

## 3. What this does and does not restore

| Capability | Without an App Group, after this fix |
|---|---|
| Packet Tunnel starts | **yes** — this was the crash |
| VPN connects and carries traffic | **yes** — the configuration is passed in the start options, not through the shared directory |
| Changing a setting, switching profile, reload | **yes** — `ExtensionProfile.reloadService()` uses `NETunnelProviderSession.sendProviderMessage`, not the socket |
| In-app traffic statistics, connection list, command channel | **no** — those use the command socket, and in a private container the extension's socket is reachable only from the extension |

The last row is the honest boundary of this fix: it is not possible to share a socket between two
processes that have no shared container. Restoring it needs a provisioning profile that grants
the App Group the build asks for (`group.io.jiejiebox.app`), and the signing tool must apply that
entitlement to **both** the app and the Packet Tunnel extension. When it does, `FilePath` opens
the real group container, both processes land in the same directory, and the socket is short
enough to bind — the fallback is never used.

The extension logs the degraded state at startup, so "statistics are broken" reads as "this build
has no shared container" rather than as a mystery.

## 4. How to verify the new IPA

1. Re-sign and install as before, open the app, confirm the main screen still opens.
2. Start the tunnel. In the failure case the tunnel died at `start command server`; now it should
   reach connected and carry traffic.
3. With the App Group granted: statistics and the connection list populate, and the log does not
   contain the "private container" line.
4. Without it: VPN works, statistics stay empty, and the log carries the line naming the missing
   group. That is the expected degradation, not a new failure.

## 5. Evidence

| Item | Result |
|---|---|
| `go test ./experimental/libbox/ -run 'TestTheReportedContainerPath\|TestALongBasePath\|TestAShortBasePath\|TestTheFallback'` | 5 tests PASS |
| `go build -tags <windows profile> ./...` | PASS |
| New IPA build | the experiment workflow run on the fixed commit, artifact `jiejiebox-ios-selfsign-experiment-*` |
