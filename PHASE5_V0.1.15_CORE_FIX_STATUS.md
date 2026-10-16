# v0.1.15: the core swallow-error fix is in, and the gate still fails — with the reason now visible

```text
CANDIDATE_VERSION = 0.1.15   tag v0.1.15 -> f85007a2dbd54961024b274563e629d38a81f841
FINAL_RELEASE_SHA = none
INSTALLER_GATE6   = FAIL
CORE_FIX          = serviceUninstall no longer swallows the stop error  (COMMITTED, testable)
RELEASE           = HELD
```

## The fix, which is real

`experimental/boxdd/cmd_service_windows.go`, `serviceUninstall()`:

```go
// before
err = stopServiceAndWait(service)
if err != nil {
    log.Warn("stop service: ", err)   // swallowed -> exit 0 while RUNNING
}
err = service.Delete()

// after
err = stopServiceAndWait(service)
if err != nil {
    return E.Cause(err, "stop service")
}
err = service.Delete()
```

That is the deterministic defect you identified, and it is worth fixing on its own terms: a zero
exit code from `service uninstall` must mean "the service is gone", not "I tried", and the
swallowed error was discarding exactly the state/PID/exit-code diagnostic that would explain a
daemon that will not stop.

Regression test: `experimental/boxdd/cmd_service_uninstall_contract_test.go` asserts the error is
returned, the swallow is gone, and the return sits between the stop and the delete. It is
source-level because the real path needs a Windows SCM, a real service and a daemon that refuses
to stop. **Falsified**: with the old form restored, both tests fail.

## It is not sufficient, and here is the measurement that says why

Built into `v0.1.15` (producer 38863089134 succeeded; gate 6 = audit 38863904357 failed), the
probe taken immediately after the uninstaller returned 0 is **unchanged**:

```text
sc.exe queryex : STATE = 4 RUNNING, PID 4432
Win32_Service  : State=Running ProcessId=4432
HKLM\SYSTEM\CurrentControlSet\Services\jiejiebox-daemon : PRESENT, DeleteFlag empty
sing-box-daemon.exe processes : 4432   (still alive)
interpreted: absent=False markedForDelete=False
```

**The daemon's `service uninstall` is never executed during the uninstall.** The same run's
record shows it:

* the installer diagnostic channel contains no `[service uninstall]` marker and no daemon output;
* the whole 609 KB lifecycle record contains no daemon output for the uninstall, while a direct
  `service uninstall` elsewhere prints `daemon version: jiejiebox-daemon version 0.1.15` and
  `core api version 4 (exit code 0)`.

So the service survives because **nothing asks it to stop**. Returning the stop error cannot help
when no stop is attempted, which is why the fix — correct in itself — does not move the gate.

The suspect is `customUnInstall`:

```nsis
${ifNot} ${isUpdated}
  !insertmacro daemonExecutable $0
  ${if} ${FileExists} "$0"
    !insertmacro executeDaemonServiceCommand "uninstall" "" $1 $3
```

`$0` is `$INSTDIR\resources\daemon\sing-box-daemon.exe`. If that file is not there when the
uninstaller reaches this point, the whole block is skipped silently and the uninstaller still
returns 0 — which fits every observation, including the empty DeleteFlag and the service that
later dies on its own (the record's `orphan daemon processes: 0` is taken minutes later, after
the settle timeout).

## The one measurement that settles it

Run the uninstaller from a real installation and capture its output and exit code:

```powershell
# after a normal install, from an elevated prompt
& "C:\Program Files\Jiejiebox\sing-box\Uninstall sing-box.exe" /S /allusers
# then, separately, ask the daemon directly
& "C:\Program Files\Jiejiebox\sing-box\resources\daemon\sing-box-daemon.exe" service uninstall; $LASTEXITCODE
```

If the second returns non-zero with the `timeout waiting for service state ..., current state ...,
process ID ...` text that `waitServiceState` already composes, the daemon-side shutdown is the
thing to fix and the new error path will have surfaced it. If it returns 0 and removes the
service, the uninstaller is skipping the block above and the fix belongs in `installer.nsh`.

## Ledger

```text
v0.1.11  c781d96c635...  HELD failed candidate, tag untouched
v0.1.12  3f4e5b559ac...  failed; instrumented build that located the install-side hang
v0.1.13  3066876e6af...  failed; my broken /SD placement
v0.1.14  41529f7c6db...  failed; install+reinstall fixed, uninstall never reached
v0.1.15  f85007a2dbd...  current: install+reinstall green; core swallow fixed; uninstall never
                         asks the daemon to stop
nothing published
```
