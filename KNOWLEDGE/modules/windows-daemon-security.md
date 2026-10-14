# Windows daemon and daemon trust boundary

**Scope:** `experimental/boxdd/**` (52 `.go` files + `desktop_service.proto`), the process roles it
defines, and the local trust boundary between them. Covers the Windows service, the per-user worker,
the named-pipe peer handshake, executable/peer authentication, Authenticode semantics, and the update
path. `daemon/**` is covered by `libbox-platform-contract.md`; this module owns only the **boundary**.

**Load this module when** changing anything in `experimental/boxdd/**`, a Windows service or
installer path, peer authentication, signer handling, or before asserting anything about what the
Windows daemon does or does not verify.

**Evidence:** every claim is `VERIFIED_SOURCE` against the `v0.1.9` baseline unless marked otherwise.
No claim here is `VERIFIED_CI` — no workflow-run record exists in the tree. No Windows end-to-end
runtime test was run for this module; see `## Known limitations`.

---

## 1. Current design

`sing-box-daemon` is one binary with four commands (`main.go:20`, `:25`, `cmd_run.go:24`,
`cmd_service.go:15`, `cmd_worker.go:30`):

```text
sing-box-daemon run                    the daemon itself (foreground, or as a service)
sing-box-daemon service start|stop|status        cmd_service.go:64-66
sing-box-daemon service install|uninstall        cmd_service_windows.go:71-72   Windows only
sing-box-daemon service set-insecure-mode <b>    cmd_service_windows.go:73 / cmd_service_linux.go:48
sing-box-daemon worker                 the per-user relay              cmd_worker.go:45
sing-box-daemon version                main.go:35
```

**There is no separate core process on Windows.** The daemon hosts `box.Box` **in-process** through
`daemon.StartedService` (`server.go:240` → `daemon/instance.go:85-153`). The `worker` is a per-user
process spawned by the Electron application; it serves `ApplicationService` and relays daemon
connections. **The authenticated peer identity is the APPLICATION, not the worker**
(`peer_windows.go:237`) — a distinction that matters for every check in §4.

The package is platform-split throughout: `*_windows.go` / `*_linux.go` / `*_stub.go` triples for
`peer`, `platform`, `cmd_run`, `cmd_service`, `cmd_worker`, `insecure_mode` and `update`. **Only the
Windows half carries a security boundary**; the others are the same command surface without peer
authentication.

## 2. Process / service / worker relationship

```text
Windows SCM
    └─ sing-box-daemon run            LocalSystem, session 0
           ├─ hosts box.Box in-process
           ├─ listens on the named pipe
           └─ peer_windows.go decides who may connect

Electron application (per-user, interactive session)
    └─ sing-box-daemon worker         spawned by the application, per-user
           └─ connects to the daemon's pipe on the application's behalf
```

The service entrypoint is `cmd_run_windows.go:26` (`svc.Run(serviceName, &windowsService{})`), with
`Execute` at `:54` handling `svc.ChangeRequest`s including session change. `--listen` is **refused** in
service mode (`cmd_run_windows.go:56-58`).

## 3. Named Pipe trust boundary

**The pipe ACL is not the gate.** `server_windows.go:15` grants `GRGW` to `Authenticated Users`, so any
authenticated local user can *open* the pipe. Protection is two-layered: the pipe name is under
`ProtectedPrefix\Administrators` (so only an administrator can create a competing instance), and the
**handshake** decides whether the connection is accepted. Read this before claiming "only the worker
can connect".

## 4. Executable / peer authentication — the handshake, in order

`peer_windows.go:83-237`. Each row is a real check with a real failure string.

| # | Check | Proves | Line |
|---|---|---|---|
| 1 | the endpoint is a Windows named pipe | not a TCP impersonator | `:86` |
| 2 | client process image path == `expectedWorkerPath` (case-insensitive) | the installed worker | `:125` |
| 3 | `sameWindowsFile(processImage, daemonExecutable)` | it is the *same file*, not a copy | `:128` |
| 4 | `authenticodeSigner(worker)` == `daemonSigner` | **signer byte-equality** | `:139` |
| 5 | `validateWorkerProcessRole` — command line, private pipe paths, parent PID | it is a worker, not a look-alike | `:360-379` |
| 6 | `processIdentity` — token user SID, interactive session | which user is connecting | `:313-334` |
| 7 | worker and application share a process identity | one user's pair, not a mix | `:165` |
| 8 | parent creation time < worker creation time | the parent is the real ancestor | `:175` |
| 9 | parent image path == `expectedApplicationPath` | the installed application | `:205` |
| 10 | `sameWindowsFile(parentProcessImage, expectedApplication)` | same file, not a copy | `:208` |
| 11 | `validateApplicationProcessRole` — not an Electron child, parent is the application | role, not just path | `:339-355` |
| 12 | `authenticodeSigner(application)` == `daemonSigner` | **signer byte-equality again** | `:223` |
| 13 | neither process exited during authentication | no TOCTOU on a dead process | `:235` |

Two properties make this robust rather than checklist-shaped:

* **`openLockedExecutable` uses `FILE_SHARE_READ`** (`:466`), so a writer cannot rewrite the image
  while it is being inspected — an anti-TOCTOU measure, not a convenience.
* **`finalWindowsPath` resolves the path through the open handle** (`:482`), so the path compared is the
  path of the file actually held open, not a string that could be raced.

**`expectedApplicationPath` is derived from the daemon's own path** (`peer_windows.go:303-304`), which
is the unforgeable part of the design: a peer cannot nominate which application is legitimate.

## 5. Authenticode semantics

`authenticode_windows.go`. Read §5 and §6 together: **three different properties** are involved and
conflating them is the most likely error.

```text
SIGNATURE INTEGRITY   WinVerifyTrustEx: does the signature match the bytes?     -> MUST hold
CHAIN TRUST           is the signer issued by a trusted CA?                     -> deliberately NOT required
SIGNER IDENTITY       is the signer byte-equal to the daemon's own?             -> MUST hold
```

### The accepted-signer chain, in order

1. `WinVerifyTrustEx` (`:114`) — the raw verdict.
2. `isAllowedUntrustedAuthenticodeError` (`:78`, applied at `:115`) — the **allowlist** (below).
3. `verifiedSignerCertificate(trustData.StateData)` (`:120`) — extract the signer's DER bytes.
4. `validateCodeSigningCertificate` (`:129`) — parse, and require the code-signing EKU.
5. On the tolerated-error branch only: `reportAuthenticodeCertificateTime` (`:153`) then
   `validateUntrustedSelfSignedCertificate` (`:154`) — self-signed and self-signature valid.

### The allowlist is exactly four codes plus success

```text
err == nil                     accepted outright
CERT_E_UNTRUSTEDROOT     0x800B0109   tolerated
CERT_E_CHAINING          0x800B010A   tolerated
CERT_E_EXPIRED           0x800B0101   tolerated
CERT_E_VALIDITYPERIODNESTING 0x800B0102   tolerated
```

Matched with `E.IsMulti` → `errors.Is` (`sing common/exceptions/multi.go:60-67`), so membership is
**exact** — no ranges, no prefixes.

**Measured rejections** (`TRUST_E_NOSIGNATURE` 0x800B0100, `TRUST_E_SUBJECT_FORM_UNKNOWN` 0x800B0003,
`TRUST_E_BAD_DIGEST` 0x80096010, `CERT_E_WRONG_USAGE` 0x800B0110, any other errno, any non-errno error).
The measured code for a **tampered signed PE** is `TRUST_E_BAD_DIGEST`; for an **unsigned** file it is
`TRUST_E_SUBJECT_FORM_UNKNOWN`, and for an unsigned but well-formed PE, `TRUST_E_NOSIGNATURE`.

### Certificate validity period is NOT a gate

`authenticodeCertificateTimeProblem` (`:197`) **returns a description string, never an error**, and
`reportAuthenticodeCertificateTime` (`:166`) logs it at `Debug` when valid and `Warn` when expired or
not-yet-valid. **There is no path from certificate time to a failure**, and the logger cannot become a
gate either: a nil logger returns early (`:168`) and a parse failure only warns (`:173`).

> **Why this is safe, and why re-adding it would be a regression.** The certificate is **self-signed and
> pinned by byte equality**, not by chain trust. Its NotBefore/NotAfter window therefore adds no
> security: an attacker-chosen certificate changes the bytes every comparison checks, and the
> certificate cannot be renewed without re-signing the whole product. What the gate *did* do was stop
> the product the moment a CI test certificate expired. See §11.

### Unsigned binaries can never pass — and the reason is structural

`authenticodeSigner` has **seven** `return nil, err` paths and exactly **one** success path,
`return signer, nil` (`:159`), which is reachable only after `verifiedSignerCertificate` succeeded —
and that function refuses a nil/empty certificate context (`:222-225`). So a nil signer **cannot**
accompany a nil error.

This matters because `bytes.Equal(nil, nil)` is **true**. The defence is therefore *not* the comparison
but the ordering: **every one of the call sites checks `err` before it compares.** Do not "harden" this
by adding `len(signer) > 0` guards; the invariant is already structural, and a guard would imply the
comparison is the defence, which it is not.

## 6. The signer pin

**One pin, six comparisons, and the reference bytes always come from the daemon's own installed
executable.** `authenticodeSigner` has nine call sites:

| Site | Identity decided |
|---|---|
| `peer_windows.go:305` | **the daemon's own signer — the reference for every other comparison** |
| `peer_windows.go:135` | worker == daemon |
| `peer_windows.go:219` | application == daemon |
| `cmd_worker_windows.go:143` | worker == the worker's own path |
| `cmd_worker_windows.go:147` | parent application == its own path |
| `cmd_worker_windows.go:396` | relayed peer == parent signer |
| `security_windows.go:48` | daemon (install-time) |
| `security_windows.go:56` | application (install-time) |
| `update_windows.go:104` | installer == daemon signer |

`peer_windows.go:305` propagates through `:63` → `platform_windows.go:63` →
`cmd_worker_windows.go:177`. Comparisons: `peer_windows.go:139`, `:223`,
`cmd_worker_windows.go:151`, `:400`, `security_windows.go:60`, `update_windows.go:108`.

**Measured:** two binaries signed by two genuinely valid, distinct self-signed code-signing
certificates produce signer payloads of **800** and **798** bytes, and `bytes.Equal` is **false** — so
the pin distinguishes them. The same certificate compares equal, so the pin is not vacuously false.

## 7. Service installation

`secureWindowsInstallation` (`security_windows.go:29`), reached from `cmd_service_windows.go:140`. It
checks the installed path's layout and ancestors (owner must be SYSTEM, Administrators or
TrustedInstaller), which is why installing under a user-owned directory fails with `code 1`
(`docs/fork/WINDOWS-SFW-BUILD-RECORD.md` §P0-2).

> **Not monolithic — state this precisely.** The signer-equality and layout checks are hard, but the
> volume/ancestor/ACL checks are relaxed by `--allow-unsafe-installation-directory-permissions`, which
> is **also propagated into the service command line**, so runtime re-validation is relaxed too. **"The
> daemon always refuses an unsafe working directory" is false.** A module that said so would misdescribe
> a live escape hatch.

## 8. Relationship to the desktop client

The client is `clients/desktop` (a separate repository, empty in a clean worktree). It supplies the
Electron application and the worker it spawns; the daemon authenticates both by the §4 handshake. The
packaging side is `cmd/internal/build_boxdd` and the release workflows — see
`build-pruning-release.md` and `ci-release.md` §5 for artifact provenance.

**Build profile:** the production Windows/amd64 daemon builds under **`DEFAULT_BUILD_TAGS_WINDOWS`**
(`cmd/internal/build_boxdd/main.go:214-220`), not `DEFAULT_BUILD_TAGS_OTHERS`. `OTHERS` is for
windows/386. Both were verified to build.

## 9. Relationship to release artifacts

The daemon ships inside `Jiejiebox-<version>-windows-x64.exe`, signed by the same certificate as the
application and the core. `windows-core-amd64.yml` produces the core artifact; the desktop producer
builds, signs and packages the installer. **The signed-installer path is `workflow_dispatch`-only**, so
a release depends on it having been dispatched — see `ci-release.md` §5.

> **Recorded tension, not resolved here.** `.github/workflows/windows-core-amd64.yml:5-10` describes the
> Windows desktop experiment as **retired** ("no daemon and no Authenticode signing"), while
> `clients/desktop/**`, `client-desktop-windows.yml`, `client-desktop-windows-runtime-audit.yml` and
> `scripts/ci/release-artifacts.sh` still build, sign, gate and stage the installer. This is a
> **product-ownership decision, not a code read**. Status: `UNKNOWN` — do not resolve it by choosing
> whichever file you read first.

## 10. Hard invariants

Violating any of these is a security hole that no configuration justifies.

| Invariant | Why | Evidence |
|---|---|---|
| **Signer byte-equality between the daemon and every peer it accepts** | this *is* the identity check; chain trust is deliberately absent | `peer_windows.go:139`, `:223` |
| **The reference signer is the daemon's own installed executable** | a peer must not be able to nominate what is legitimate | `peer_windows.go:305`, `:303-304` |
| **The fixed installed path is required** | a signer-valid binary elsewhere is not this installation | `peer_windows.go:125`, `:205` |
| **`sameWindowsFile` is required** | path equality is not file equality | `peer_windows.go:128`, `:208` |
| **An unsigned binary must be rejected** | `bytes.Equal(nil,nil)` is true; two unsigned peers would match | `:118`, `:222-225` |
| **The authenticated peer is the APPLICATION** | the worker is a relay, not the principal | `peer_windows.go:237` |
| **Signature integrity must hold** | tolerating a bad digest would make the signature decorative | allowlist excludes `TRUST_E_BAD_DIGEST` |

## 11. Current conventions and deliberate non-gates

| Rule | Status | Why |
|---|---|---|
| Certificate validity period is **not** a gate | **deliberate non-gate** | self-signed + byte-pinned; the window added no security and broke the product on CI expiry. Re-adding it is a **regression**, not hardening |
| Certificate chain trust is **not** required | deliberate | a CI test certificate is self-signed by construction |
| Code-signing EKU is required | design rule | but `ExtKeyUsageAny` is **also accepted** (`:236`) — a known weakening |
| Self-signed check | defence in depth | applies only on the tolerated-error branch; the pin is the real check |
| Worker pipe ACL | convention, **not** the gate | `Authenticated Users` can open it; the handshake decides |
| Update product-name allowlist | convention | the source says explicitly it is **not** a security boundary |
| `--listen` disables peer authentication | **known limitation** | guarded only by service mode refusing it |

## 12. Provenance

| Commit | Subject | Effect |
|---|---|---|
| `cb19a3ba5` | removed the NotBefore/NotAfter hard gate from `validateUntrustedSelfSignedCertificate` | time reporting became warning-only |
| `9f13b33da` | tolerated the WinTrust time codes | added the allowlist and `CERT_E_EXPIRED` + `CERT_E_VALIDITYPERIODNESTING` to the previous two-code set |
| `8a008c5d0` | proved the tamper, unsigned, EKU and signer-pin boundaries | tests only; no production change |

**Measured but not re-measured here:** the claim in those commits that Windows reports
`CERT_E_UNTRUSTEDROOT` before the clock (verified once by the author against a genuinely expired signed
PE). The code does not depend on which of the four codes arrives, because all four are tolerated —
which is why the module does not need to settle it.

## 13. Important evolution

The boundary did **not** start as a byte-equality pin. It arrived there by removing things that could
not be relied on: chain trust (a CI certificate is self-signed), then the validity window (which broke
the product on expiry), while **identity stayed and became the whole check**. The direction of travel is
therefore *fewer trust assumptions, one identity property* — and the failure mode to avoid is restoring
an assumption that was removed because it was both useless and harmful.

## 14. Verification

| What | How | Level |
|---|---|---|
| the allowlist's exact membership | `authenticode_windows_test.go` — a 14-row table plus a both-directions enumeration of all 24 `CERT_E_*`/`TRUST_E_*` codes | `VERIFIED_UNIT` |
| tampered signed PE is rejected | `TestTamperedSignedPEIsRejectedByTheFirstLayer` — builds and signs its own PE, then tampers it; measured `TRUST_E_BAD_DIGEST` | `VERIFIED_UNIT` |
| unsigned is rejected | `TestUnsignedFileIsRejectedByTheFirstLayer` + the real `WinVerifyTrustEx` call | `VERIFIED_UNIT` |
| code-signing EKU is required | `TestCodeSigningEKUIsRequired` (incl. `ExtKeyUsageAny` accepted, ClientAuth refused) | `VERIFIED_UNIT` |
| the pin distinguishes two valid certificates | `TestSignerPinDistinguishesTwoValidCertificates` | `VERIFIED_UNIT` |
| expiry cannot block | `TestExpiredCertificateIsNoLongerFatal`, `TestNotYetValidCertificateIsNoLongerFatal`, `TestTimeProblemBoundaries` | `VERIFIED_UNIT` |
| the package builds for Windows | `GOOS=windows go build`, amd64 + arm64, both tag profiles | `VERIFIED_SOURCE` |
| **the daemon actually starts and serves with an expired certificate** | install the service and connect | **NOT RUN** — see §15 |

## 15. Known limitations

* **No end-to-end test exists for the trust boundary.** `peer_windows.go` has **no Go test file**, and
  the handshake cannot be exercised without a real service installation, a real named pipe and two real
  processes. Everything in §4 is `VERIFIED_SOURCE` only.
* **No CI runs these tests.** No workflow executes `go test` on a Windows runner —
  `windows-core-amd64.yml` runs on `ubuntu-latest` and only cross-builds. The eleven
  `//go:build windows` tests in this package are therefore **never executed in CI**.
* `ExtKeyUsageAny` is accepted alongside code signing, which is broader than the name suggests.
* `--listen` disables the entire boundary; only service mode refuses it.
* `ProcessStartTime` is never set on Windows (always 0), so the "process started at" observable is
  absent — the creation-time *ordering* check in §4 does not depend on it.
* `secureWindowsInstallation`'s ancestor/ACL checks are relaxable at install **and** at runtime (§7).
* The expired-certificate end-to-end path (daemon start, core start, IPC) was **not** run in a real
  installation. See §14.

## 16. AI cautions

Each is a "reasonable but wrong" change this codebase has already paid for.

1. **Do not re-add the certificate validity gate.** It looks like hardening and is a regression: it
   broke the product when a CI certificate expired, and it protects nothing because the certificate is
   byte-pinned. §11.
2. **Do not "fix" `CERT_E_EXPIRED` handling by widening the allowlist.** It is an allowlist on purpose.
   `TRUST_E_BAD_DIGEST` in particular must never be tolerated — that is the difference between
   verifying a signature and decorating a file. §5.
3. **Do not add `len(signer) > 0` guards and call it the fix.** Nil signers are already impossible with
   a nil error; the defence is that callers check `err` first. Adding a comparison guard obscures where
   the real invariant lives. §5.
4. **Do not "simplify" the handshake by comparing paths instead of files.** `sameWindowsFile` exists
   because a path can be re-pointed; `:128`/`:208` are load-bearing.
5. **Do not replace `openLockedExecutable`'s `FILE_SHARE_READ` with a plain open.** It excludes writers
   while the image is inspected. §4.
6. **Do not assume the worker is the authenticated principal.** It is the application (`:237`); checks
   written against the worker will accept the wrong process.
7. **Do not assume the pipe ACL protects the daemon.** `Authenticated Users` can open the pipe by
   design; the handshake is the gate. §3.
8. **Do not use `DEFAULT_BUILD_TAGS_OTHERS` for the production Windows daemon.** It is
   `DEFAULT_BUILD_TAGS_WINDOWS` (`build_boxdd/main.go:214-220`). §8.
9. **Do not treat the update product-name allowlist as a security boundary.** The source says it is not.
   §11.
10. **Do not claim the daemon always refuses an unsafe installation directory.** The escape hatch is
    propagated to runtime. §7.
11. **Do not resolve the §9 retirement tension by picking a side.** It is a product decision; `UNKNOWN`
    is the honest status.
12. **Do not cite this module as proof the daemon works.** It records what the source verifies. No
    end-to-end run exists. §15.

## 17. Follow-up

* **A Windows runner that executes `go test ./experimental/boxdd/`** — the single highest-value gap,
  since eleven tests exist and none run in CI.
* **An end-to-end trust-boundary test**: install the service, connect a worker, and assert both the
  accept and the reject paths. Requires a machine where a service can be installed.
* Decide whether `ExtKeyUsageAny` should remain accepted (§11).
* Decide the §9 retirement question.
