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

---

# Second device report: the fallback itself was 110 bytes

Reported 2026-11-04, after the fix above shipped:

```text
Failed to start service (packet-tunnel): start command server error:
listen command server: socket path is 110 bytes, the kernel allows 103:
/private/var/mobile/Containers/Data/PluginKitPlugin/<UUID>/tmp/jjb-13de0c06.sock
```

## Why the first fix was not enough

It chose `os.TempDir()` as the alternative on the assumption that a temporary directory is short.
Inside an iOS Packet Tunnel the temporary directory **is** the container's: 92 bytes before a file
name. The eight-hex-digit fallback name pushed it to 110. The five tests passed because they ran
on a host whose temp directory is short, so the arithmetic they exercised was never the failing
one.

The lesson is not "pick a different directory": any single alternative is another assumption about
length. What the limit needs is a decision that takes the directory's actual length as input.

## The resolver now

`experimental/libbox/command_socket.go`:

* **Candidates in order**: the base path, then the platform temp directory (`sTempPath`, else
  `os.TempDir()`), then the container root (four bytes shorter than its `tmp`).
* **The name is sized to the budget**, not fixed: the base path keeps `command.sock` untouched;
  any other directory gets `h<hex>.sock`, with the hash shortened as the budget requires (4 to 8
  hex digits, derived from the logical socket path so two instances differ). A name that cannot
  fit makes the candidate fail rather than produce an over-long path.
* **Both sides share it**: `command_server.go` and `command_client.go` call the same function with
  the same inputs, so they cannot disagree.
* **Failure is a sentence, not an EINVAL**: if no candidate fits, the error reports the limit, the
  number of candidates and the bytes the shortest one leaves - no container path, because those
  end up pasted into issues.
* **Cleanup only touches its own names**: `command.sock` or a generated `h…sock`; anything else in
  those directories is left alone.

For the reported device layout the answer is the container root with a four-digit name:
88 + 1 + 10 = 99 bytes.

## Tests

Unit (every platform): the reported layout reproduces the old 110-byte fallback; the resolver
returns a bindable generated name for it; 103 fits and 104 does not; multibyte directories are
measured in bytes; an impossible layout returns an error that names sizes and no paths; the rule is
deterministic and distinct per base path; only generated names are removable.

Darwin (`command_socket_darwin_test.go`, run by the experiment workflow on a macOS runner): builds
a container of the device's shape, **binds** the resolved path, dials it, and separately asserts the
kernel refuses a 104-byte name. That is the check the unit tests cannot make.

## What is still not known

Whether the tunnel then connects, whether the app's statistics reach it (no App Group means they
cannot), and whether the local signing loopback answers. All device-side.

---

# Third device report: the system temporary directory was never a candidate

Reported 2026-11-04, after the second fix shipped:

```text
command server: no bindable socket path within 103 bytes;
3 candidate directories, the shortest leaves -31 bytes for a name
```

## What the second fix got wrong

It added "the platform temp path and its parent" to the candidate list, and consulted
`os.TempDir()` only when the caller passed no temp path. The extension always passes one:

```swift
options.tempPath = FilePath.cacheDirectory.relativePath   // <shared>/Library/Caches
```

so the three candidates were the base path (133 bytes), `Library/Caches` under it (148), and that
directory's parent - `/shared` (133) - which was already candidate one and was deduplicated away.
The one directory that is short inside the container, `TMPDIR` = `<container>/tmp` (92 bytes), was
never considered. Every candidate was hopeless, and the resolver correctly said so.

The second defect was in that message: it reported the budget of the **last** candidate, not of the
shortest one, so the number it printed was not the best the process could do.

The third defect was in the tests: they passed the host's `/tmp` as the platform temp argument, so
they exercised a layout that never occurs on a device. Two rounds of green tests, two failures.

## What the resolver does now

* `os.TempDir()` is a candidate **always**: inside a Packet Tunnel it is `<container>/tmp`, the
  shortest sanctioned writable directory the process has.
* The container root is derived from **that** directory, not from whatever the caller passed.
* The platform's own temp path is still tried, and so is its parent: on macOS it is a real
  temporary directory, and on a correctly signed iOS build the base path wins before any of this.
* Generated names carry **no suffix**: inside a container there are ten bytes for a name, and
  `.sock` would spend five of them on a string nobody reads. `h` plus four to eight hex digits.
* When nothing fits, the error reports the budget of the **shortest** candidate.

For the reported layout: `<container>/tmp` (92) + `/` + `h` + eight hex digits = **102 bytes**.

## Why this round's evidence is different

The tests now take the device's three paths as constants - base path, the `Library/Caches` the
extension actually passes, and the 92-byte `TMPDIR` - and assert that resolution lands in the
container temporary directory at exactly 102 bytes. The resolver's system-temp input is an
argument (`resolveCommandSocketPathIn`), so a test can model a device instead of asserting about
the host.

On Darwin the tests build that container shape under `/tmp`, padded so the base path really does
exceed the limit, then call `net.ListenUnix` on the resolved path and dial it. A 104-byte control
proves the kernel's boundary is the one being satisfied.

## Still not claimed

That the tunnel connects, and that the app can reach the command channel. The second needs the App
Group: without it the extension's socket is in the extension's container and the app's is in the
app's, which no path arithmetic can change.
