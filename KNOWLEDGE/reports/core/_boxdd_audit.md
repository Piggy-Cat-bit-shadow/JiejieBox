# experimental/boxdd audit at 8a008c5d02d2c7f7b24d43581af9360bfc110fa6

Read-only audit. Nothing inside `singbox-consolidation` was modified. Note for provenance: the
working tree was **already dirty** on entry — `KNOWLEDGE/INDEX.md`, `KNOWLEDGE/modules/README.md`,
`KNOWLEDGE/modules/ci-release.md` show as modified. Those are not this audit's changes.

File count correction: `git ls-files 'experimental/boxdd/**'` gives **52** `.go` files, plus
`desktop_service.proto` = 53 files in the directory. The brief's "53 Go files" counts the `.proto`.

Build evidence (read-only, output written to `%TEMP%`):
`GOOS=windows GOARCH=amd64 go build -tags "$(DEFAULT_BUILD_TAGS_OTHERS)" ./experimental/boxdd/` → exit 0.
`... -tags "$(DEFAULT_BUILD_TAGS_WINDOWS)" ...` → exit 0. The **production** Windows/amd64 profile is
`DEFAULT_BUILD_TAGS_WINDOWS` (`cmd/internal/build_boxdd/main.go:214-220`); `DEFAULT_BUILD_TAGS_OTHERS`
is selected only for `windows/386`. `go vet` → exit 1 with exactly one finding
(`authenticode_windows.go:220: possible misuse of unsafe.Pointer`).

## Subsystem boundary

- files that form the trust boundary:
  **Windows enforcement (the boundary itself)**
  - `experimental/boxdd/authenticode_windows.go` — signer extraction + certificate policy. The core of the module.
  - `experimental/boxdd/peer_windows.go` — named-pipe server handshake, `peerIdentity`, per-identity token duplication.
  - `experimental/boxdd/cmd_worker_windows.go` — worker self/parent authentication, worker pipe ACLs, worker→daemon relay, and authentication of the daemon end in the reverse direction.
  - `experimental/boxdd/security_windows.go` — install-time path/volume/ACL checks, daemon↔application signer equality, service DACL.
  - `experimental/boxdd/cmd_service_windows.go` — `service install|uninstall|set-insecure-mode`, SCM config, recovery actions.
  - `experimental/boxdd/cmd_run_windows.go` — SCM entry (`svc.Run`), service-mode refusals, session-change dispatch.
  - `experimental/boxdd/platform_windows.go` — owner token acquisition/validation, impersonation, per-user working directory.
  - `experimental/boxdd/server_windows.go` — pipe name + pipe security descriptor.
  - `experimental/boxdd/update_windows.go`, `experimental/boxdd/update_executable.go` — update-installer authentication + SYSTEM-token launch; NSIS validation.
  - `experimental/boxdd/insecure_mode_windows.go`, `experimental/boxdd/insecure_mode_path_windows.go`, `experimental/boxdd/user_token_windows.go`.
  **Platform-neutral code carrying the same boundary**: `main.go` (`serviceName`, cobra root), `cmd_run.go`, `cmd_worker.go`, `cmd_service.go`, `server.go` (Daemon, gRPC server, authorizer, peer registry), `peer.go`, `desktop_service.go`, `application_service.go`, `managed.go`, `settings.go`, `snapshot.go`.

- process roles:
  1. **daemon** — `sing-box-daemon.exe run`, started by the SCM as LocalSystem in session 0 (`cmd_service_windows.go:153`, `cmd_run_windows.go:34`). Serves gRPC on `\\.\pipe\ProtectedPrefix\Administrators\sing-box` (`server_windows.go:26`). Holds the core **in-process** — `box.New`/`Start()` via `daemon.StartedService` (`server.go:240`, `daemon/instance.go:85-153`) — there is **no separate core process**.
  2. **worker** — `sing-box-daemon.exe worker --socket … --parent-pid … --daemon-relay-socket …`, spawned by the Electron application *as that user* (`cmd_worker.go:42-44`; spawn site `clients/desktop/src/main/worker.ts:42-49`). Serves `ApplicationService` only (`cmd_worker.go:65-71`) and relays authenticated daemon connections (`cmd_worker_windows.go:259-276`).
  3. **application** — `sing-box.exe` (Electron). Its identity (user SID + session + installed path + signer) is what the daemon authenticates (*not* the worker's: `peer_windows.go:237`).
  4. **core** — not a process; an in-process `box.Box` inside role 1.

- PRIMARY owner candidate / CROSS-MODULE:
  **PRIMARY**: the Windows daemon↔worker↔application local trust boundary — Authenticode pinning, named-pipe peer handshake, install-time privilege, owner token. That is `windows-daemon-security.md`.
  **CROSS-MODULE** (reference, do not own):
  - `daemon/**` — the platform-neutral daemon/core API this binary hosts in-process (`daemon.StartedService`, `daemon.ManagedService`, `daemon/instance.go`). Larger than this boundary; also used by libbox and Linux.
  - `clients/desktop/**` — the peer. `worker.ts` is the counterparty of every check in `peer_windows.go`.
  - `cmd/internal/build_boxdd/**` + `release/DEFAULT_BUILD_TAGS_WINDOWS` — build recipe and version stamp (`build_boxdd/main.go:84`, `:213-256`).
  - release/CI packaging that preserves the signer relationship: `.github/workflows/client-desktop-windows.yml`, `.github/workflows/client-desktop-windows-runtime-audit.yml`, `scripts/ci/windows/**`, `clients/desktop/electron-builder.yml`, `clients/desktop/scripts/afterPack.cjs`.

- what is NOT part of this subsystem:
  - the **Linux siblings in the same package** — different trust model, no Authenticode: `peer_linux.go`, `platform_linux.go`, `authorize_linux.go` (PolicyKit over D-Bus, `authorize_linux.go:20-22`), `cmd_run_linux.go`, `cmd_service_linux.go`, `cmd_worker_unix.go`, `insecure_mode_linux.go`, `insecure_mode_path_linux.go`.
  - stubs: `peer_stub.go`, `platform_stub.go`, `cmd_run_stub.go`, `cmd_service_stub.go`, `update_stub.go`, `authorize_stub.go`, `insecure_mode_stub.go`, `debug.go`/`debug_stub.go`.
  - generated protobuf: `desktop_service.pb.go`, `desktop_service_grpc.pb.go`.
  - reporting/telemetry plumbing, no identity decision: `report.go`, `crash_report.go`, `oom_report.go`, `power_report.go`.
  - **`transport/**` — absent from this boundary.** No file under `experimental/boxdd` imports `github.com/sagernet/sing-box/transport` (verified by import scan). The daemon's only peer-facing transport is local gRPC over a named pipe. Do not route this module through the transport modules.

## Trust boundary: the peer handshake, in order

Entry point `windowsTransportCredentials.serverHandshake` (`peer_windows.go:83`), registered as the
gRPC server credential by `platformServerOptions` (`peer_windows.go:54-65`) and wired in
`newDaemon` (`server.go:82`). Ordered as executed:

| # | check | what it proves | file:line | on failure |
|---|---|---|---|---|
| 1 | connection implements `Fd()` | endpoint is a Windows named pipe and its client PID is askable | peer_windows.go:84-87 | error `daemon endpoint is not a Windows named pipe`; gRPC handshake fails |
| 2 | `GetNamedPipeClientProcessId` | the **kernel's** PID for the client end, not a client-supplied value | peer_windows.go:88-92 | handshake fails |
| 3 | `OpenProcess(QUERY_LIMITED_INFORMATION\|SYNCHRONIZE)` | that PID is queryable and waitable | peer_windows.go:93-96 | handshake fails |
| 4 | client token user SID | the connecting process's user | peer_windows.go:103 → 313-327 | handshake fails |
| 5 | client session ≠ 0 (`ProcessIdToSessionId`) | the peer is in an interactive session, never session 0 | peer_windows.go:103 → 328-335 | `not in an interactive session` |
| 6 | `QueryFullProcessImageName` | client executable path as the kernel sees it | peer_windows.go:107-110 | handshake fails |
| 7 | `openLockedExecutable`: `GENERIC_READ` + `FILE_SHARE_READ` | an open handle that **excludes writers/deleters** for the rest of the handshake (anti-TOCTOU) | peer_windows.go:111-114 → 466-480 | handshake fails |
| 8 | `finalWindowsPath` (`GetFinalPathNameByHandle`) | canonical path resolved **by handle**, so junctions/`\\?\` games cannot spoof it | peer_windows.go:121-124 → 482-501 | handshake fails |
| 9 | `EqualFold(workerFinalPath, expectedWorkerPath)` | the client executable **is the daemon's own installed path** | peer_windows.go:125-127 | `named pipe client is not the installed sing-box worker` |
| 10 | `sameWindowsFile(workerImage, daemonExecutable)` | same volume serial + file index: the file at that path was not replaced | peer_windows.go:128-134 → 503-517 | `named pipe client worker executable was replaced` |
| 11 | `authenticodeSigner(workerPath, lockedHandle)` | signature integrity + signer extractability + EKU | peer_windows.go:135-138 | `authenticate sing-box worker: …` |
| 12 | `bytes.Equal(workerSigner, c.daemonSigner)` | signed by the **same certificate bytes** as this daemon | peer_windows.go:139-141 | `sing-box worker and daemon have different signing certificates` |
| 13 | parent PID (`NtQueryInformationProcess`/`ProcessBasicInformation`) | the real inherited parent | peer_windows.go:142-145 → 446-464 | handshake fails |
| 14 | worker command line is exactly 8 args: `[1]=="worker"`, `[2]=="--socket"`, `[4]=="--parent-pid"`, `[6]=="--daemon-relay-socket"` | the process was started in the **worker role**, not as the app or a stray daemon invocation | peer_windows.go:146-149 → 360-371 | `not a sing-box worker process` |
| 15 | args 3 and 7 both start with `\\.\pipe\sing-box-worker.` **and differ** | the worker's private pipes are app-chosen per-run pipes | peer_windows.go:372-376 | `invalid private pipe paths` |
| 16 | arg 5 parses as uint32 **and equals the kernel parent PID** | declared parent matches the real parent | peer_windows.go:377-380 | `invalid parent process ID` |
| 17 | `OpenProcess(parent)` + `processIdentity(parent)` | parent's SID and session | peer_windows.go:150-163 | handshake fails |
| 18 | parent SID == worker SID **and** parent session == worker session | same user, same interactive session | peer_windows.go:164-166 | `worker and application have different process identities` |
| 19 | `GetProcessTimes` on both | `parentCreationTime < workerCreationTime` | peer_windows.go:167-177 | `worker parent was created after the worker` |
| 20 | `openLockedExecutable(expectedApplicationPath)` | the app path is **derived from the daemon's own installed layout**, never supplied by the peer | peer_windows.go:192-196 → 298-304; security_windows.go:89-99 | handshake fails |
| 21 | `EqualFold(parentFinalPath, expectedApplicationFinalPath)` | the parent **is** the installed `sing-box.exe` | peer_windows.go:197-207 | `worker parent is not the installed application` |
| 22 | `sameWindowsFile(parentImage, expectedApplication)` | the installed application file was not replaced | peer_windows.go:208-214 | `worker parent executable was replaced` |
| 23 | `validateApplicationProcessRole`: no `--type` / `--type=` argument | the parent is not an Electron child process | peer_windows.go:215-218 → 339-349 | `named pipe client is an Electron child process` |
| 24 | same function: parent's own parent is not the application | the peer **is** the app, not a child of it | peer_windows.go:350-356 | `named pipe client is a child of the sing-box application` |
| 25 | `authenticodeSigner(application)` | app signature integrity + signer extractability | peer_windows.go:219-222 | `authenticate sing-box application: …` |
| 26 | `bytes.Equal(applicationSigner, c.daemonSigner)` | app signed by the daemon's own certificate bytes | peer_windows.go:223-225 | `application and daemon have different signing certificates` |
| 27 | `WaitForSingleObject(worker,0)` **and** `(parent,0)` both `WAIT_TIMEOUT` | neither PID was reaped/recycled during the handshake | peer_windows.go:226-236 | `worker or application exited during authentication` |
| 28 | `identity = parentIdentity; ProcessID = parentProcessID` | the authenticated peer identity is the **application** | peer_windows.go:237, 252-255 | — |
| 29 | `registerPeerConnection` | connection reachable for token duplication by identity | peer_windows.go:251 → peer.go:49-56 | — |

The pipe ACL is **not** the boundary: `server_windows.go:15` grants `GRGW` to Authenticated Users so
any interactive user can open the pipe. Protection is the `ProtectedPrefix\Administrators` name
(only an administrator can create it, `server_windows.go:26`) **plus** the handshake above. The worker
pipe ACL is per-user (`cmd_worker_windows.go:195-198`).

## authenticodeSigner call sites

Exactly 9 (verified by full-repo grep). In every one the **reference** bytes are the daemon's own
installed executable, so this is one pin, not nine checks.

| site | identity decision it feeds | mismatch consequence | file:line |
|---|---|---|---|
| 1 | establishes `c.daemonSigner` — the **root** of the pin; also fixes `expectedWorkerPath` (own path) and `expectedApplicationPath` (installed app) | `platformServerOptions` fails → `newDaemon` fails (`server.go:82-85`) → service mode: `Execute` exitCode 1 + event log (`cmd_run_windows.go:73-78`); standalone: `log.Fatal` (`cmd_run.go:83-86`). Daemon never starts, so the core never starts. | peer_windows.go:305 (context 279-311) |
| 2 | worker signer vs daemon signer | handshake refused; event log via peer_windows.go:78 | peer_windows.go:135 → 139-141 |
| 3 | application signer vs daemon signer | handshake refused | peer_windows.go:219 → 223-225 |
| 4 | worker's **own** executable signer (worker authenticates itself) | `runWorker` errors → `log.Fatal` (`cmd_worker.go:35`) → worker exits 1; app sees `application worker exited with code 1: …` (`worker.ts:71-81`) | cmd_worker_windows.go:143 |
| 5 | parent application signer | same as #4, via `:151-153` `worker and application have different signing certificates` | cmd_worker_windows.go:147 → 151-153 |
| 6 | signer of the process owning the **daemon-side** pipe the worker dialled (reverse direction) | `connectDaemon` fails → relay drops the app connection (`cmd_worker_windows.go:307-311`) → GUI daemon RPCs fail | cmd_worker_windows.go:396 → 400-402 |
| 7 | installed daemon signer at `service install` | `secureWindowsInstallation` errors → `serviceInstall` fails → `log.Fatal` (`cmd_service_windows.go:35`), exit 1 | security_windows.go:48 |
| 8 | installed application signer, compared to the daemon's | `installed application and daemon have different signing certificates` → install refused; NSIS then rolls back (`clients/desktop/build/installer.nsh` `customInstall` → `Abort registerServiceFailedRolledBack`) | security_windows.go:56 → 60-62 |
| 9 | update installer signer vs `platform.daemonSigner` | **not an error**: returns `INSTALL_UPDATE_RESULT_SIGNER_MISMATCH` → client maps to `"signer-mismatch"` (`clients/desktop/src/main/updates.ts:453-455`) | update_windows.go:104 → 108-110 |

Byte-equality pin across the four identities is confirmed: `bytes.Equal` at
`peer_windows.go:139`, `peer_windows.go:223`, `cmd_worker_windows.go:151`, `cmd_worker_windows.go:400`,
`security_windows.go:60`, `update_windows.go:108`. The daemon-side reference is produced once at
`peer_windows.go:305`, copied into the platform at `peer_windows.go:63` → `platform_windows.go:63`,
and propagated into relay state as `windowsWorkerParent.signer` at `cmd_worker_windows.go:177`.

## Authenticode current semantics

### must-verify chain (in order)

1. `WinVerifyTrustEx(WINTRUST_ACTION_GENERIC_VERIFY_V2, WTD_CHOICE_FILE, WTD_UI_NONE, WTD_REVOKE_NONE, WTD_CACHE_ONLY_URL_RETRIEVAL|WTD_REVOCATION_CHECK_NONE|WTD_DISABLE_MD2_MD4, WTD_UICONTEXT_EXECUTE)` against the already-open file handle — `authenticode_windows.go:97-114`.
2. The returned error must be `nil` **or** one of exactly four codes, else the state is closed and the call returns — `:115-119`, `:78-90`.
3. `WTHelperProvDataFromStateData` → `WTHelperGetProvSignerFromChain` → `WTHelperGetProvCertFromChain` must each be non-zero; the cert context must be non-nil with non-zero `Length` and non-nil `EncodedCert` — `:207-226`.
4. The DER bytes must parse with `x509.ParseCertificate` — `:231-235`.
5. The EKU list must contain `ExtKeyUsageCodeSigning` **or** `ExtKeyUsageAny`; an empty EKU list is rejected — `:236-241`.
6. **Only if** WinTrust returned a tolerated error: `bytes.Equal(RawSubject, RawIssuer)` — `:248-250` — and the self-signature must verify — `:251-254`.
7. `StateAction = CLOSE` is always issued (`:116-117`, `:121-122`); a close failure is itself an error (`:126-128`).

### tolerated WinTrust codes (exact allowlist)

`isAllowedUntrustedAuthenticodeError` (`:78-90`) accepts exactly:

| accepted | value | source of value |
|---|---|---|
| `err == nil` | — | chain trusted outright, nothing tolerated (`:79-81`) |
| `CERT_E_EXPIRED` | `0x800B0101` | `:85` |
| `CERT_E_VALIDITYPERIODNESTING` | `0x800B0102` | `:86` |
| `CERT_E_CHAINING` | `0x800B010A` | `:87` |
| `CERT_E_UNTRUSTEDROOT` | `0x800B0109` | `:88` |

Membership is **exact**: `E.IsMulti` is `errors.Is` over the list (`sing/common/exceptions/multi.go:60-67`
in `sing v0.9.6-0.20261011105649-bde7ddda55af`), so only those errno values match — no range, prefix or
substring matching exists. Explicitly **outside** the list and therefore rejected: `TRUST_E_NOSIGNATURE`
(`0x800B0100`), `TRUST_E_SUBJECT_FORM_UNKNOWN` (`0x800B0003`, the measured code for an unsigned file),
`TRUST_E_BAD_DIGEST` (`0x80096010`), `CERT_E_WRONG_USAGE` (`0x800B0110`), `CERT_E_REVOKED`,
`CERT_E_MALFORMED`, every other errno, and every non-errno error. Constant values verified in
`golang.org/x/sys@v0.47.0/windows/zerrors_windows.go`.

### warning-only, never gates

- **Certificate validity window.** `reportAuthenticodeCertificateTime` (`:166-189`) logs `Warn` when
  `authenticodeCertificateTimeProblem` (`:197-205`) reports `is not valid yet (NotBefore …)` or
  `has expired (NotAfter …)`, and `Debug` when inside the window. It returns nothing; it cannot fail the
  call. A parse failure inside it is also only a `Warn` (`:171-175`). Called at `:153`.
- The literal `runtime launch is not blocked` (`:173`, `:187`) is the product's statement of that.
- Nothing else in this file is warning-only — every other branch returns an error.

### fail-closed

- Any WinTrust error not on the allowlist: no signature, corrupt/mismatched signature, wrong usage,
  revoked, malformed, unknown provider, any non-errno error — `:115-119`.
- Missing provider data / signer / certificate, or empty cert context — `:209-226`.
- Unparsable certificate — `:233-235`.
- No code-signing (or `Any`) EKU — `:241`.
- Tolerated-error path where the certificate is not self-signed (`:249`) or its self-signature does not
  verify (`:253`).
- `WinVerifyTrustEx` CLOSE failing — `:126-128`.
- No path returns `(nil, nil)`.

### certificate validity period: current behaviour

**Not a gate.** The only place it could gate is `validateUntrustedSelfSignedCertificate`, which
explicitly does not check the window (`:244-256`, esp. `:245-246`), and its caller only logs
(`:153-154`). When the chain *is* trusted outright, Windows has already applied the clock while building
the chain, so an expired-but-chain-trusted file surfaces as `CERT_E_EXPIRED` — i.e. the *tolerated*
branch — never as `nil`. Net current behaviour:

- expired or not-yet-valid **and** untrusted root → accepted, one `Warn`, launch proceeds (`:153-157`, `:184-188`)
- expired **and** trusted root → accepted via the `CERT_E_EXPIRED` tolerance, one `Warn`
- which of the two codes appears is decided by the machine trust store, not by this code — the CI script
  imports its certificate into `LocalMachine\Root` for the run (`scripts/ci/windows/New-TestSigningCertificate.ps1:110-111`)
- the window cannot be renewed without changing the pinned bytes, which is the stated reason it adds no
  security here (`:145-152`)

Re-adding the window as a gate is a **regression**, not a hardening: it previously stopped the daemon,
which stopped the core, on a machine whose system proxy still pointed at the local port.

### chain trust vs signer identity vs signature integrity

Three different properties, three different code paths. The module must not conflate them.

- **SIGNATURE INTEGRITY** — "these bytes are the bytes that were signed." Enforced by
  `WinVerifyTrustEx` itself. A mismatch is `TRUST_E_BAD_DIGEST`, not on the allowlist, so a tampered PE
  is refused (`:114-119`). The only property the time-window relaxation did not touch.
- **CHAIN TRUST** — "some CA vouches for this certificate." **Deliberately not required.** The four
  tolerated codes are precisely the "no chain trust" and "clock window" verdicts (`:50-77`, `:83-89`).
- **SIGNER IDENTITY** — "this is the byte-identical certificate the daemon itself was signed with."
  **This is the actual gate**, and it is not in this file: it is `bytes.Equal` at the 8 comparison sites
  listed above. This file only guarantees that a candidate signer exists, is intact, and is a
  code-signing certificate.

### unsigned binaries

**Can never be accepted.** Three structural layers, each independently sufficient:

1. An unsigned file makes `WinVerifyTrustEx` fail with a code outside the allowlist — measured as
   `TRUST_E_SUBJECT_FORM_UNKNOWN` `0x800B0003` (`authenticode_windows_test.go:439-451`);
   `TRUST_E_NOSIGNATURE` is also outside the list.
2. Even if a trust verdict were tolerated, `verifiedSignerCertificate` errors with no provider
   certificate (`:217-219`) or an empty context (`:221-226`).
3. `authenticodeSigner` returns `(nil, err)` on **every** failure path, and all 9 call sites check `err`
   before comparing (e.g. `peer_windows.go:135-141`). `bytes.Equal(nil, nil)` **is** `true`, so the
   property that saves this design is "error first, compare second" — **not** the comparison. Nothing in
   the code path makes a nil signer a *successful* return value; the test file pins both halves
   (`authenticode_windows_test.go:415-425`, `:440-452`).

## Failure behaviour

| failure | product symptom | where |
|---|---|---|
| daemon's **own** executable has no verifiable signer / wrong EKU | daemon never starts. Service mode: service starts then stops, exit code 1, Windows event log entry (source `sing-box-daemon`, ID 1, Error), then SCM recovery restarts it 3× at 5 s; `service status` reports `stopped`/`not installed` (exit 2/3). Standalone `run`: `FATAL` on stderr, exit 1. Core never starts because the daemon never starts. | peer_windows.go:305 → 59-62; cmd_run_windows.go:73-78, 115-122; cmd_service_windows.go:197-205, 373-400 |
| daemon executable not in the installed layout (`<install>/resources/daemon/sing-box-daemon.exe`) | same as above — `installedApplicationPath` refuses | peer_windows.go:298-301; security_windows.go:89-99 |
| peer handshake fails (any of the 29 checks) | connection refused; **event log Error** `reject Windows daemon connection: <reason>` (the one place handshake reasons are logged); the GUI sees a failed/refused gRPC connection on its daemon transport; the worker's relay just closes the app socket | peer_windows.go:75-81; cmd_worker_windows.go:307-311 |
| worker cannot authenticate its parent application | worker process exits 1 with `FATAL run application worker: …` on stderr; the GUI surfaces `application worker exited with code 1: <stderr>` and every ApplicationService RPC fails | cmd_worker.go:35-38, 55-58; clients/desktop/src/main/worker.ts:71-81 |
| worker's daemon relay cannot authenticate the daemon end | relay connection dropped; daemon RPCs through the relay fail while local worker RPCs still work | cmd_worker_windows.go:396-402, 307-311 |
| `service install` run unprivileged / from a non-fixed or non-NTFS volume / through a reparse point / from an ancestor writable by an unprivileged principal | `FATAL install service: <reason>` on stderr, exit 1, service not created. In the product path the NSIS installer invokes this via `installer-service.ps1` (`service install --working-directory …`), sees non-zero, runs the uninstaller and aborts with `registerServiceFailedRolledBack` | cmd_service_windows.go:32-37, 128-227; security_windows.go:29-87, 301-337, 373-451; clients/desktop/build/installer.nsh `customInstall` |
| installed application and daemon have different signing certificates | same as above — service refuses to install | security_windows.go:56-62 |
| update installer not signed by the daemon's signer, or product name not accepted, or not an NSIS PE, or not newer | **not fatal**: update screen reports `signer-mismatch` / refusal. Product name, NSIS shape and version are gRPC `InvalidArgument` errors; signer mismatch is a *result enum*, not an error | update_windows.go:93-116; clients/desktop/src/main/updates.ts:448-456 |
| owner token does not match the authenticated application | service start refused, `owner token identity does not match authenticated application`; owner-protected RPCs return `PermissionDenied` | platform_windows.go:267, 542-551; server.go:412-424, 356-373 |
| daemon takes > 3 s to close on service stop | process forcibly exits 1 | cmd_run_windows.go:105-108 |
| `--listen` given in service mode | service refuses to run, exit code 1, event log | cmd_run_windows.go:56-60 |

## Invariants vs conventions

| rule | verdict | why | file:line |
|---|---|---|---|
| signer byte-equality between daemon and its peers | **HARD INVARIANT** | This is the whole authentication. Chain trust is deliberately not required, so identity-by-bytes is the only thing distinguishing our binaries from any other validly-signed binary. Weakening it to "same subject", "same thumbprint field", or "both signed" collapses the boundary. | peer_windows.go:139, 223; cmd_worker_windows.go:151, 400; security_windows.go:60; update_windows.go:108 |
| code-signing EKU required | **DESIGN RULE** (not load-bearing for identity) | It rejects a certificate that is not for code signing, but `ExtKeyUsageAny` is accepted, so it is a shape filter, not an identity check. The pin is what binds. | authenticode_windows.go:236-241 |
| self-signed check required for an untrusted signer | **DESIGN RULE** (defence in depth) | Applies only on the tolerated-error branch. Removing it would not by itself admit an attacker: the same bytes are still required to match the daemon's certificate. It does refuse an untrusted *leaf*, which is not the shipped shape. | authenticode_windows.go:247-255 |
| fixed installed path required (`<install>/resources/daemon/sing-box-daemon.exe` + `<install>/sing-box.exe`) | **HARD INVARIANT** | It is the value `expectedApplicationPath` is derived from, and the derivation is what makes the expected peer path unforgeable by the peer. Without it a copy of both signed binaries at an attacker-chosen path would satisfy path equality. | security_windows.go:89-99; peer_windows.go:125-127, 192-207, 298-304; cmd_worker_windows.go:112, 129-131 |
| `sameWindowsFile` required | **HARD INVARIANT** | Path equality alone is spoofable by replacing the file at a stable path. Volume serial + file index on a `FILE_SHARE_READ` handle closes that window. Removing it re-opens path-replacement tampering. | peer_windows.go:128-134, 208-214; cmd_worker_windows.go:132-138, 389-395; peer_windows.go:503-517 with :466-480 |
| service must be installed from a privileged root (`secureWindowsInstallation`) | **HARD INVARIANT** for the signer-equality + layout checks; **CONVENTION** for volume/ancestor/ACL checks | The volume (`DRIVE_FIXED`+NTFS), ancestor-ownership and reparse-point checks have a documented escape hatch: `--allow-unsafe-installation-directory-permissions` skips them (`security_windows.go:63-65`), and the flag is propagated into the service arguments so runtime re-validation is also relaxed (`cmd_service_windows.go:154-156`). The signer comparison at `:60-62` is **not** behind the flag. | security_windows.go:29-87 (esp. 60-65), 301-337, 373-451; cmd_service_windows.go:65-70, 140, 154-156 |
| certificate validity period is NOT a gate | **DESIGN RULE** (deliberate non-gate) | Not a security invariant — it is a deliberate decision that the clock window adds nothing for a self-signed, byte-pinned certificate, and that gating on it caused a production outage. Its regression value is high: re-adding the gate is a **bug**, not a hardening. | authenticode_windows.go:133-158, 244-256, 197-205; cmd_worker_windows.go / peer sites above |
| unsigned binary must be rejected | **HARD INVARIANT** | If an unsigned file could return `(nil, nil)`, two unsigned endpoints would make `bytes.Equal(nil, nil)` true and the pin would silently pass. This is why the trust-code list is an allowlist and why every call site checks `err` first. | authenticode_windows.go:78-90, 207-229; peer_windows.go:135-141 |
| the authenticated peer identity is the **application**, not the worker | **HARD INVARIANT** | Token duplication and every owner authorization key off it. Changing it to the worker's identity would let a worker impersonate a different owner. | peer_windows.go:237, 252-255; platform_windows.go:263; server.go:359, 389 |
| worker pipe ACL grants only SYSTEM / Administrators / one user SID | **CONVENTION** | Defence in depth only; the ACL is not what admits or refuses a peer — the handshake is. | cmd_worker_windows.go:195-198 |
| daemon pipe must be the `ProtectedPrefix\Administrators\sing-box` path | **DESIGN RULE** | Custom paths are refused so the pipe name cannot be created by an unprivileged process. The pipe ACL itself grants Authenticated Users `GRGW`. | server_windows.go:15, 26, 29-32 |
| `--listen` disables the entire Windows boundary | **KNOWN LIMITATION** (development-only escape hatch) | With `--listen`, `platformServerOptions` returns no credentials, the authorizer becomes `allowAllAuthorizer`, and the working directory is a plain `MkdirAll`. Guarded only by service mode refusing `--listen` and by the flag's own help text. A module claiming "every connection is authenticated" would be wrong without this caveat. | peer_windows.go:55-57; server.go:321-325, 327-341, 96-98, 104; cmd_run_windows.go:37-40, 56-60 |
| `ExtKeyUsageAny` accepted as a code-signing EKU | **KNOWN LIMITATION** | A certificate whose EKU is `anyExtendedKeyUsage` passes `validateCodeSigningCertificate`. Identity still binds, so it is not exploitable on its own; do not "fix" it by narrowing the pin. | authenticode_windows.go:237 |
| `peerIdentity.ProcessStartTime` is never set on Windows | **KNOWN LIMITATION** | Always `0` for every Windows peer; only Linux sets it. Any future Windows code that starts using it as a distinguisher would compare `0 == 0`. PID recycling is instead handled by the liveness check. That is weaker than Linux's start-time identity — it distinguishes a *dead* PID, not a *reused* one. | peer.go:14-19; peer_windows.go:313-337 (no assignment); peer_linux.go:108-115; authorize_linux.go:69, 88 |
| `updateProductNames` allowlist `{Jiejiebox, sing-box}` | **CONVENTION** explicitly not a security boundary | Source says so: "It is NOT a security boundary — the Authenticode signer comparison below is, and a name is trivially forgeable." Also a product/branding coupling to electron-builder's `appInfo.productName`. | update_windows.go:30-60 |
| signer mismatch on update is a result code, not an error | **CONVENTION** (protocol shape) | Lets the client distinguish "wrong binary" from "download failed". Refusals that happen earlier (name, NSIS shape, version) *are* errors. Do not unify them. | update_windows.go:93-116; updates.ts:448-456 |
| `go vet` finding at the provider-certificate cast | **KNOWN LIMITATION** | `uintptr → unsafe.Pointer` round-trip through the syscall return; vet flags it. In practice the state data outlives the conversion (CLOSE happens after). | authenticode_windows.go:220 (vet exit 1) |
| `peer_windows.go` has no Go test | **KNOWN LIMITATION** | The only two test files in the package are `authenticode_windows_test.go` and `peer_linux_test.go`. The 29-step handshake is covered only by the Windows CI runtime gates, not by unit tests. | `Get-ChildItem experimental/boxdd -Filter *_test.go` |

## Provenance

Range `a267e99e0bf1811dc975b013e2e8d7a15ab40d23..HEAD` (HEAD = `8a008c5d02d2c7f7b24d43581af9360bfc110fa6`,
`v0.1.9-5-g8a008c5d0`; the tag is reported for context only — the family of fork tags was not validated).
`a267e99e` is upstream-side (`git rev-list --count a267e99e --not --remotes=upstream` = 1834).

| commit | subject | what changed |
|---|---|---|
| `cb19a3ba5` | `fix(boxdd): an expired test certificate no longer stops the daemon launching the core` | `authenticode_windows.go` +70/−5, test +142. Removed the `NotBefore`/`NotAfter` gate from `validateUntrustedSelfSignedCertificate`; split the time classification into the pure function `authenticodeCertificateTimeProblem` and made the reporting warning-only. |
| `9f13b33da` | `fix(boxdd): tolerate the WinTrust time codes so an expired CI certificate cannot stop the core` | `authenticode_windows.go`: introduced `isAllowedUntrustedAuthenticodeError` as an explicit **allowlist**; added `CERT_E_EXPIRED` and `CERT_E_VALIDITYPERIODNESTING` to the previous two-code set (`CERT_E_UNTRUSTEDROOT`, `CERT_E_CHAINING`). Stated purpose: the previous fix was unreachable because the front guard rejected before the inner check. |
| `8a008c5d0` (HEAD) | `test(boxdd): prove the tamper, unsigned, EKU and signer-pin boundaries through the real WinVerifyTrust` | test only (`authenticode_windows_test.go` +269): allowlist contract in both directions, expired-certificate acceptance, EKU matrix, signer-pin distinctness, the nil-signer argument, and real `WinVerifyTrustEx` calls for unsigned / tampered / system-signed files. |

No other commit in the range touches `experimental/boxdd`. No other production package in the range
touches `experimental/boxdd`.

**Established from current source, not from these messages:** the four-member allowlist, the exact
comparison operators, the removal of the validity gate, and the warning-only reporting. The commit
messages additionally *claim* that Windows reaches the chain verdict before the clock and that the
observed code for an expired signature is `CERT_E_UNTRUSTEDROOT`; that is a benchmark claim whose only
in-tree support is the code comment at `authenticode_windows.go:50-77` and the log line in
`authenticode_windows_test.go:447-448`. Treat it as MEASURED-BY-AUTHOR, not re-measured here.

## Proposed module outline

Skeleton only — no prose. Each section lists the facts to state and their evidence.

1. **Scope and ownership** — what `experimental/boxdd` is (the Windows daemon binary, 52 `.go` files); the PRIMARY boundary vs the four CROSS-MODULE neighbours (`daemon/**`, `clients/desktop/**`, `cmd/internal/build_boxdd/**`, release packaging); explicit exclusion of the Linux siblings, stubs and generated protobuf; explicit statement that `transport/**` is not involved (no such import exists).
   Evidence: the file/role lists above; `cmd/internal/build_boxdd/main.go:84`; `server_windows.go:26`.
2. **Process model** — three roles (daemon/SYSTEM/session 0, worker/per-user, application) plus the in-process core; the daemon does **not** spawn a core process; the worker is spawned by the app and serves only `ApplicationService`.
   Evidence: `cmd_run_windows.go:34`; `server.go:240`; `daemon/instance.go:85-153`; `cmd_worker.go:42-44, 65-71`; `worker.ts:42-49`; `peer_windows.go:237`.
3. **The peer handshake** — the ordered 29-check table, with the note that the authenticated identity is the application; that the pipe ACL is not the gate; and where each failure surfaces.
   Evidence: the handshake table above; `server_windows.go:15, 26`.
4. **The signer pin** — one pin, not nine checks; the daemon's own signer is the root; all four identities compared; the 9 call sites and what each decides.
   Evidence: the call-site table above, esp. `peer_windows.go:305`, `:63` → `platform_windows.go:63`, `cmd_worker_windows.go:177`.
5. **Authenticode semantics** — the 7-step must-verify chain; the exact 4-code allowlist with values; integrity vs chain vs identity; unsigned can never pass and *why* (`bytes.Equal(nil,nil)` is true, so "error first" is the defence).
   Evidence: `authenticode_windows.go:97-119, 78-90, 207-256`.
6. **Certificate time policy** — the window is not a gate; what happens on expired and not-yet-valid; the warning-only logger; why re-adding the gate is a regression.
   Evidence: `authenticode_windows.go:133-158, 166-205, 244-256`; `New-TestSigningCertificate.ps1:110-111`.
7. **Install-time privilege** — the layout triple, signer equality, volume/ancestor/reparse/ACL checks, the `--allow-unsafe-installation-directory-permissions` escape hatch and exactly what it skips (and what it does not), service DACL, recovery actions.
   Evidence: `security_windows.go:29-87, 257-277, 301-337, 373-451`; `cmd_service_windows.go:128-227, 65-70, 154-156`.
8. **Owner identity and per-user privilege** — owner token acquired per authenticated identity, validated against SID+session, impersonation for system-proxy writes, session-change handling.
   Evidence: `platform_windows.go:252-301, 356-389, 492-551`; `peer_windows.go:523-547`; `server.go:412-424`.
9. **Update path** — installer identity (name/NSIS/version) is convenience, the signer comparison is the boundary, installer runs with a duplicated SYSTEM token in the target session, mismatch is a result code.
   Evidence: `update_windows.go:62-124, 209-251`; `updates.ts:448-456`.
10. **Failure behaviour and user-visible symptoms** — the failure table above; the event log is the only place handshake reasons appear.
    Evidence: `peer_windows.go:75-81`; `cmd_run_windows.go:115-122`; `cmd_service_windows.go:373-400`.
11. **Packaging and release** — what the artifact must contain (`resources/daemon/sing-box-daemon.exe` beside `sing-box.exe`, plus the listed drivers/DLL), how the one-signer relationship is preserved (afterPack signs the daemon and native module separately because electron-builder's app signature does not cover them; `forceCodeSigning` is turned on exactly when a certificate is configured), and the CI gates that assert it.
    Evidence: `electron-builder.yml:45-79`; `afterPack.cjs:38-50`; `package.ts:229-266, 308-320`; `audit-windows-installer-payload.sh:113-121`; `Test-WindowsSigning.ps1:54-101`; `client-desktop-windows.yml:530-544`; `client-desktop-windows-runtime-audit.yml:332-360`.
12. **Invariants, conventions, limitations** — the verdict table above, verbatim in intent.
13. **Provenance** — the three-commit table plus the SHA of the audited tree.
14. **AI cautions** — the list below.

## Known limitations and AI cautions

Reasonable-but-wrong changes a future AI might make here, each with why it is wrong:

1. **"`isAllowedUntrustedAuthenticodeError` should be `if trustError != nil { … }` / should also tolerate `TRUST_E_NOSIGNATURE`, because an unsigned build is fine."** Wrong. Tolerating `TRUST_E_NOSIGNATURE` or `TRUST_E_SUBJECT_FORM_UNKNOWN` removes the only thing preventing `verifySignerCertificate` from yielding no signer; combined with `bytes.Equal(nil, nil) == true`, an all-unsigned pair would authenticate. The list is an allowlist *because* a denylist cannot express "no signer".
2. **"Re-add the `NotBefore`/`NotAfter` check to `validateUntrustedSelfSignedCertificate` — an expired certificate is obviously a security problem."** Wrong here, and it has already been paid for twice (`cb19a3ba5`, then `9f13b33da` which fixed the *front* guard). The certificate is self-signed and pinned by byte equality; the window cannot be refreshed without changing the bytes every comparison checks. Re-adding it stops the daemon, stops the core, and on a machine whose system proxy still points at the local port removes network access.
3. **"The `CERT_E_EXPIRED` entry is dead code, delete it."** Wrong. Which code Windows reports depends on whether the certificate's root is in the machine trust store (`authenticode_windows.go:61-77`), and the CI script deliberately imports its certificate into `LocalMachine\Root` for the run — so both configurations occur. Deleting either entry re-creates the outage in one of the two configurations.
4. **"The pipe ACL already restricts access, so the handshake checks are redundant / can be relaxed."** Wrong. `server_windows.go:15` grants `GRGW` to Authenticated Users; any interactive user can open the pipe. The `ProtectedPrefix\Administrators` name only prevents an unprivileged *creator*. The checks are the gate.
5. **"`validateApplicationProcessRole` is about the worker"** or **"the peer identity is the worker."** Wrong. It is called with the *parent* process, and the authenticated identity is the **application** (`peer_windows.go:215` receives `parentProcess`; `:237` overwrites `ProcessID` with the parent's). Token duplication and every owner decision key off that identity.
6. **"`updateProductNames` is a security control, so extend it / make it strict."** Wrong framing. `update_windows.go:30-44` states it is not a boundary and that a name is trivially forgeable; it is an early cheap rejection coupled to electron-builder's `appInfo.productName` branding. Hardening it changes nothing security-wise and couples the daemon to packaging strings.
7. **"Signer mismatch on `InstallUpdate` should be an error like the other refusals."** Wrong, or at least a protocol change: the client distinguishes `SIGNER_MISMATCH` / `NOT_NEWER` result codes from transport/argument errors (`updates.ts:448-456`). Unifying them changes user-visible update behaviour.
8. **"`--allow-unsafe-installation-directory-permissions` is just a test flag, ignore it."** Wrong. It is plumbed from `service install` into the service's own command line (`cmd_service_windows.go:154-156`) and read back at `cmd_run_windows.go:41-49`, so the *runtime* working-directory validation is also relaxed for that installation. Any statement like "the daemon always refuses an unsafe working directory" is false.
9. **"`--listen` is development-only and can be ignored in the module."** Wrong to omit. `platformServerOptions` returns **no** credentials when `--listen` is set, and the authorizer becomes `allowAllAuthorizer` — the entire Windows boundary is off. Service mode refuses `--listen`, which is the only guard. This caveat must be in the module.
10. **"Fix the `ExtKeyUsageAny` acceptance by removing it."** It is a known limitation, not a bug you can safely tighten in isolation: any change to `validateCodeSigningCertificate` changes which signatures are acceptable at all 9 sites at once, including the daemon's own. If it is ever narrowed, it must be a deliberate product decision with the pin unchanged.
11. **"`ProcessStartTime` proves the peer was not re-created."** On Windows it is always `0` (`peer_windows.go:313-337` never sets it). PID-recycling is handled only by `WaitForSingleObject(…, 0) == WAIT_TIMEOUT`, which distinguishes a dead PID, not a reused one. Do not describe Windows peer identity as equivalent to Linux's.
12. **"Move/replace the fixed installed-path layout, it is just a convention."** Wrong. `expectedApplicationPath` is *derived* from the daemon's own path (`peer_windows.go:298-304` → `security_windows.go:89-99`), and that derivation is what makes the expected peer path unforgeable by the peer. Changing the layout silently changes the authentication reference.
13. **"The daemon re-checks the signature every time it launches the core, so an expired/rotated certificate blocks the core."** Wrong. The signer check runs once per daemon process, in `newDaemon` → `platformServerOptions` → `initializeServerIdentity`. There is no per-core-start signature check; `startServiceLocked` never calls `authenticodeSigner`. The outage was caused by the daemon failing to start, not by a per-start check.
14. **"`peer_windows.go` is covered by tests."** It is not — the package's only tests are `authenticode_windows_test.go` and `peer_linux_test.go`. Handshake behaviour is asserted only by Windows CI runtime gates.
15. **Trusting the workflow header that says the Windows desktop experiment was retired.** `.github/workflows/windows-core-amd64.yml:5-10` states the core workflow deliberately contains "no daemon and no Authenticode signing… Those belonged to the retired desktop experiment." Yet `clients/desktop/**`, `client-desktop-windows.yml`, `client-desktop-windows-runtime-audit.yml` and `scripts/ci/release-artifacts.sh:818-1003` still build, sign, gate and stage the Windows desktop installer. Do not resolve this from the comment; it is a product-ownership question recorded below.

## Claims I could not verify

- **The real WinVerifyTrust classification on this platform** (which code an expired-but-signed PE actually returns; whether an unsigned file yields `TRUST_E_SUBJECT_FORM_UNKNOWN`; whether a tampered PE yields `TRUST_E_BAD_DIGEST`). `BLOCKED_BY_ENVIRONMENT` — the tests exist and are real (`authenticode_windows_test.go:237-598`) but this audit ran no Windows test. Settling command, on Windows with signtool and a Windows SDK present:
  `GOOS=windows GOARCH=amd64 go test -tags "<DEFAULT_BUILD_TAGS_WINDOWS>,tfogo_checklinkname0" -run 'TestWinTrust|TestExpiredCertificate|TestUnsignedFile|TestTamperedSignedPE|TestSignerPin|TestNilSigner|TestSignedSystemBinary' -v ./experimental/boxdd/`
  (the PE-signing cases `SKIP` rather than pass without `signtool.exe`).
- **The 29-step peer handshake end to end.** `BLOCKED_BY_ENVIRONMENT` — no unit test covers `peer_windows.go`, and it needs a packaged install. Settling command: the runtime-audit gate `scripts/ci/windows/Test-DaemonService.ps1` invoked by `.github/workflows/client-desktop-windows-runtime-audit.yml:332-341`.
- **Service-install privilege behaviour and path validation in practice.** `BLOCKED_BY_ENVIRONMENT` — needs an elevated Windows host. Settling commands: `scripts/ci/windows/Test-SecureServiceInstall.ps1` (gate 5b) and `scripts/ci/windows/Test-DaemonServiceRegister.ps1`.
- **The packaged artifact's real payload and one-signer property.** `BLOCKED_BY_ENVIRONMENT` — needs a built installer. Settling commands: `scripts/ci/windows/Test-WindowsSigning.ps1` (gate 4) and `scripts/ci/audit-windows-installer-payload.sh` (gate 3).
- **Whether certificate validity is enforced by Windows in the chain-trusted case.** Read from `authenticode_windows.go:61-77` and consistent with the `CERT_E_EXPIRED` entry, but `UNKNOWN` — this audit did not re-measure the ordering of Windows' chain verdict versus its clock verdict, and the code cannot show it.
- **Whether the Windows desktop client is still the shipped Windows delivery vehicle.** `UNKNOWN` — the core workflow header declares the desktop experiment retired (`windows-core-amd64.yml:5-10`) while the desktop client, its two workflows and `release-artifacts.sh` still exist and stage it. This is a product-ownership decision, not a code read.
- **CI toolchain parity.** I built with the local Go 1.25.5 / `GOTOOLCHAIN=local`; the exact toolchain CI pins was not verified, and a different toolchain could change `crypto/x509` EKU parsing or `windows.Errno` behaviour. Settling command: read the `go` directive and toolchain line in `go.mod` and the setup step of `.github/workflows/client-desktop-windows.yml`.
- **`daemon/**` internals.** Read only as far as the boundary needs (`instance.go`, the `StartOrReloadService`/`CloseService`/`Instance` surface of `started_service.go`). A module that describes the shared daemon API belongs to a different owner.
