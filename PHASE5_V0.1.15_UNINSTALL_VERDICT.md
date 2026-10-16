# v0.1.15: the uninstall settle is a PRODUCT bug — measured, not inferred

```text
CANDIDATE_VERSION  = 0.1.15   tag v0.1.15 -> fdf587ce52c90f21ad57b0916838d167ecb08918
                              NEVER published; no new version number spent
INSTALLER_GATE6    = FAIL
HANG_OWNER         = the uninstaller leaves the daemon service RUNNING and the process alive,
                     then reports success (exit 0)
PRODUCT_FIX        = the install-side MessageBox default (DONE, proven); the uninstall-side
                     service teardown is the remaining fix
RELEASE            = HELD
```

## The probe, and the verdict it forces

Printed immediately after the first uninstall returned 0, before any waiting, using `sc.exe` and
CIM only — never `Get-Service`, because a probe that took a service handle could not be trusted
to answer a question about handles:

```text
-- service state probe (immediately after the first uninstall returned 0) --
sc.exe queryex jiejiebox-daemon : exit 0
        STATE   : 4  RUNNING
        PID     : 6712
sc.exe qc jiejiebox-daemon : exit 0
Win32_Service : State=Running ProcessId=6712 Started=True ExitCode=0
HKLM:\SYSTEM\CurrentControlSet\Services\jiejiebox-daemon :
        PRESENT (DeleteFlag= ImagePath="C:\Program Files\Jiejiebox\sing-box\resources\daemon\sing-box-daemon.exe" run ...)
sing-box-daemon.exe processes : 6712  <-- still alive
interpreted: absent=False markedForDelete=False
```

Applying the three rules that were agreed in advance:

| rule | condition | measured? |
|---|---|---|
| test bug | service absent (1060) but the test still waits | **no** — `absent=False` |
| harness holds a handle | Stopped + marked for deletion, PID gone | **no** — `markedForDelete=False`, `DeleteFlag` empty, PID alive |
| **product uninstall bug** | still Running / StopPending, or daemon PID alive | **YES** |

So this is a **product bug in the uninstall path**, and the interesting part is the shape of it:
the uninstaller **exits 0** while the service is still RUNNING with a live PID and its SCM
registry key intact. A success code is being returned for work that did not happen, which is why
this looked like a test-side wait problem for several rounds — `Assert-Uninstalled`'s own output
even reported `service after uninstall: absent` and `orphan daemon processes: 0` after its wait,
because by then the process had *finally* died, far outside the 300 s window.

The suspect is `customUnInstall`'s teardown:

```nsis
!insertmacro executeDaemonServiceCommand "uninstall" "" $1 $3
${if} $1 != 0
  Abort "$(removeServiceFailed)"
${endif}
```

`$1` is 0, so no abort — yet the service survived. The uninstall needs to **wait for the daemon
to stop and assert the SCM entry is gone**, rather than treating a zero exit code from
`service uninstall` as the service having been removed.

## The one thing that was ruled out

`Get-ServiceState` did leak: `Get-Service` returns a `ServiceController` holding a service
handle, and Windows will not remove a service while any handle to it is open, so a leaked handle
can pin the very service a test waits to disappear. That is a real latent defect and it is
**fixed** — the object is now disposed in a `finally`, so its lifetime is the function call's
rather than the garbage collector's.

It is **not** the cause here. A handle leak leaves the service `markedForDelete=True` with the
PID gone; the measurement shows `markedForDelete=False` with the PID alive. Recorded explicitly
so nobody re-fixes it expecting this failure to move.

## Next step, and it is the only one

Fix the uninstall-side service teardown in `sing-box-for-desktop`: make it wait for the daemon
process to exit and verify the service is gone, and fail loudly if it is not. Then re-point
`v0.1.15` and re-run `uninstall → clean reinstall → gate 6`.

Do **not** spend a new version number on this: `v0.1.15` has never been published.

## Ledger (nothing published)

```text
v0.1.11  c781d96c635...  HELD failed candidate, tag untouched
v0.1.12  3f4e5b559ac...  failed; instrumented build that located the install-side hang
v0.1.13  3066876e6af...  failed; my broken /SD placement
v0.1.14  41529f7c6db...  failed; install+reinstall fixed, uninstall never reached
v0.1.15  fdf587ce52c...  CURRENT; install+reinstall green; uninstall teardown is the open bug
```
