# GATE: installs, reinstalls and uninstalls the real NSIS installer, silently.
#
# Usage:
#   Test-InstallerLifecycle.ps1 -InstallerPath <...\Jiejiebox-v0.1.5-windows-x64.exe> `
#                               -ExpectedVersion 0.1.5 -RecordPath <file>
#
# # Why this gate exists
#
# Extracting the installer and auditing its payload proves what is inside the file. It
# does not prove the file installs. The installer's own customInstall runs
# `sing-box-daemon.exe service install` and, on any non-zero exit, runs the uninstaller
# and aborts - so a signed-build regression, a layout mistake or a service that cannot
# start turns the installer into a rollback, and only running it shows that.
#
# # What is covered
#
#   install            silent, default per-machine location, no /D override
#   files              sing-box.exe, the daemon, the native module and the drivers exist
#   version            the installed daemon reports the expected version
#   service            registered and Running, and the app layout registry key written
#   reinstall          the same version over itself, which is where a service that is
#                      left stopped, StopPending or marked for deletion shows up
#   uninstall          files removed, service removed, no orphan process
#   clean reinstall    install again after a full uninstall and tear it down again
#
# Silent mode is used deliberately rather than GUI automation. The installer has custom
# pages, and in a default per-machine install none of them raise a prompt that /S cannot
# answer - the pages with a MessageBox are the unsafe-installation and repair paths, which
# a secure Program Files installation does not enter. A timeout guards the case where that
# reasoning is wrong: a prompt nobody can answer fails the gate instead of hanging the job.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$InstallerPath,
    [Parameter(Mandatory = $true)][string]$ExpectedVersion,
    [Parameter(Mandatory = $true)][string]$RecordPath,
    [int]$OperationTimeoutSeconds = 900,
    # The installer's CI-only diagnostic channel: an absolute path OUTSIDE $PLUGINSDIR and
    # $INSTDIR that survives customInstall's rollback. Without it a failure leaves nothing
    # but "exited 2", because the installer deletes its own evidence. Empty means the
    # installer runs exactly as it does for a user, with no diagnostic option at all.
    [string]$DiagnosticPath = ""
)

$ErrorActionPreference = "Stop"

. (Join-Path $PSScriptRoot "GateHelpers.ps1")

# Native commands here are expected to exit non-zero in normal operation - `sc.exe query`
# on a service that should be absent returns 1060 - so a non-zero exit must not become a
# terminating error. Each call checks $LASTEXITCODE where the result matters.
$PSNativeCommandUseErrorActionPreference = $false

# The service name and the layout registry key are NOT redeclared here. They are this client's
# identity, they are defined once in GateHelpers.ps1 ($script:JiejieboxServiceName /
# $script:JiejieboxInstallationLayoutKey), and Get-InstallationState reads them from there. A
# second copy in this file is how the gate and the workflows' cleanup steps would come to
# disagree about which service and which registry key this installation owns.
$serviceName = $script:JiejieboxServiceName
$installationLayoutKey = $script:JiejieboxInstallationLayoutKey
# Set once per run: the uninstall registry is dumped in full the first time an installation
# is identified, so the record shows what the runner actually has.
$candidateDumpWritten = $false

$lines = New-Object System.Collections.Generic.List[string]
$lines.Add("windows NSIS installer lifecycle smoke test")
$lines.Add("installer         $InstallerPath")
$lines.Add("expected version  $ExpectedVersion")
$lines.Add("")

function Save-Record {
    $directory = Split-Path -Parent $RecordPath
    if (-not [string]::IsNullOrWhiteSpace($directory) -and -not (Test-Path -LiteralPath $directory)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    [System.IO.File]::WriteAllLines($RecordPath, $lines)
}


# Get-InstallationState and Add-InstallationIdentity used to be defined here. They now live in
# GateHelpers.ps1, which this script dot-sources, because the `cleanup:` steps of BOTH Windows
# workflows call Get-InstallationState in their own PowerShell process - where a function defined
# in a gate step cannot exist. See the identity section of GateHelpers.ps1 for the full reasoning
# and for what "this installation" means.


function Get-ServiceState {
    # `Get-Service` hands back a ServiceController that holds a HANDLE on the service, and
    # Windows keeps a service alive - its SCM entry and its
    # HKLM\SYSTEM\CurrentControlSet\Services key - until every handle to it is closed. An
    # undisposed ServiceController therefore keeps the service in exactly the state this test
    # waits for it to leave, and 1072 (ERROR_SERVICE_MARKED_FOR_DELETE) persists for as long as
    # the handle does.
    #
    # This function used to return only the status and drop the object, leaving the handle to
    # the garbage collector. Whoever calls it in a poll loop is deciding how long that takes.
    # Disposing explicitly makes the handle's lifetime the function call's, not the GC's.
    $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
    if ($null -eq $service) { return "absent" }
    try {
        return $service.Status.ToString()
    } finally {
        $service.Dispose()
    }
}

function Get-InstallArguments {
    $arguments = @("/S")
    if (-not [string]::IsNullOrWhiteSpace($DiagnosticPath)) {
        $arguments += "/CI-DIAGNOSTIC-PATH=$DiagnosticPath"
    }
    return $arguments
}

function Show-InstallerDiagnostic {
    if ([string]::IsNullOrWhiteSpace($DiagnosticPath)) {
        Write-Host "-- installer diagnostic channel --"
        Write-Host "   (not requested for this run)"
        $lines.Add("installer diagnostic channel: not requested")
        return
    }
    Write-Host "-- installer diagnostic channel ($DiagnosticPath) --"    if (-not (Test-Path -LiteralPath $DiagnosticPath)) {
        Write-Host "   (the installer did not create it, so it failed before customInit or could not write)"
        $lines.Add("installer diagnostic channel: file not created at $DiagnosticPath")
        return
    }
    $content = Get-Content -LiteralPath $DiagnosticPath -Raw -ErrorAction SilentlyContinue
    if ([string]::IsNullOrWhiteSpace($content)) { $content = "(empty)" }
    Write-Host $content
    $lines.Add("installer diagnostic channel:")
    foreach ($line in ($content -split "`r?`n")) { $lines.Add("  $line") }
    # Preserved beside the record so it reaches the artifact.
    $preserved = Join-Path (Split-Path -Parent $RecordPath) "installer-diagnostic.txt"
    try {
        Copy-Item -LiteralPath $DiagnosticPath -Destination $preserved -Force -ErrorAction Stop
        Write-Host "   preserved as $preserved"
    } catch {
        Write-Host "   could not preserve it: $($_.Exception.Message)"
    }
}

function Save-UninstallerProvenance {
    <#
        Preserve the INSTALLED uninstaller and record its identity, at a named lifecycle point.

        WHY BINARY IDENTITY AND NOT THE PATH. Everything established so far says the generated
        uninstaller for the CURRENT build contains un.onInit -> initMultiUser -> customUnInit. None
        of that says the binary sitting in the installation directory at a given moment is from
        this build. Windows installers rewrite their uninstaller, upgrades may keep an older one,
        and electron-builder removes the previous version in its own order - so the process that
        actually ran may be a different generation from the one the producer just built.

        The asymmetry this is aimed at: the FIRST uninstall writes nothing and leaves
        jiejiebox-daemon RUNNING, while the FINAL uninstall does write and does clean up. A single
        GetOptions quirk does not explain that asymmetry. A stale uninstaller after the reinstall,
        replaced by a current one only when the clean install writes it, explains all of it.

        The file is COPIED before it can delete itself, and its hash is the answer: if the copies
        from different points differ, no amount of parameter tracing is relevant.

        THE ROOT IS A PARAMETER, AND THAT IS NOT COSMETIC. This function used to read a bare
        `$InstallationRoot`, which is the name of Assert-Uninstalled's parameter and NOT a
        lifecycle-scope variable - the lifecycle scope spells it `$installationRoot`, lower case,
        and it is only assigned after the first Assert-Installed returns. So this function
        depended on a variable that did not exist at any of its call sites. It was also called
        with `$installed` / `$reinstalled` BEFORE those variables were assigned, so it received
        $null for the uninstall entry as well.

        The consequence was not an error - it was silence. With no entry and no root, every
        candidate path was empty, the function took its (correct, guarded) `NOT FOUND` branch and
        returned, which is why every provenance point cost 4 lines and the FIRST_VS_FINAL hash
        matrix this whole gate exists to produce never appeared in a single record. Passing the
        root in explicitly removes the dependency on call-site scoping, and the empty-root case
        now says so instead of looking like a machine with no uninstaller.
    #>
    param(
        [string]$Point,
        [Parameter(Mandatory = $true)][string]$InstallationRoot,
        [Parameter(Mandatory = $false)]$UninstallEntry
    )

    # An empty root cannot reach here: InstallationRoot is Mandatory, so PowerShell's own binding
    # refuses `''` and `$null` before the body runs. That is deliberate and is the difference
    # between the old silent-NOT-FOUND and a harness bug that stops the run.
    $records = Split-Path -Parent $RecordPath
    $safe = ($Point -replace '[^A-Za-z0-9]+', '-').Trim('-')

    # Prefer the path the registry names, falling back to the installation root.
    $candidate = $null
    if ($null -ne $UninstallEntry -and -not [string]::IsNullOrWhiteSpace($UninstallEntry.UninstallString)) {
        $candidate = Get-CommandExecutablePath ([string]$UninstallEntry.UninstallString)
    }
    if ([string]::IsNullOrWhiteSpace($candidate) -or -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        foreach ($guess in @(
            (Join-Path $InstallationRoot "Uninstall sing-box.exe"),
            (Join-Path $InstallationRoot "Uninstall Jiejiebox.exe")
        )) {
            # Every path test is guarded for null/empty. `Test-Path -LiteralPath $null` throws
            # "Cannot bind argument to parameter 'Path' because it is null", and this function runs
            # at points where the installation root may legitimately not be established yet - so an
            # unguarded test turns a diagnostic into the failure it was meant to describe.
            if ([string]::IsNullOrWhiteSpace($guess)) { continue }
            if (Test-Path -LiteralPath $guess -PathType Leaf) { $candidate = $guess; break }
        }
    }
    if ([string]::IsNullOrWhiteSpace($candidate)) {
        $lines.Add("  uninstaller at '$Point': NOT FOUND (no UninstallString target, no guess present)")
        return
    }
    if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        $lines.Add("  uninstaller at '$Point': NOT FOUND at '$candidate'")
        return
    }

    $item = Get-Item -LiteralPath $candidate
    # Hashed from the bytes rather than with Get-FileHash, so the value cannot come back null and
    # take the whole diagnostic down with it.
    $hash = ""
    try {
        $bytes = [System.IO.File]::ReadAllBytes($candidate)
        $sha = [System.Security.Cryptography.SHA256]::Create()
        try {
            $hash = (($sha.ComputeHash($bytes) | ForEach-Object { $_.ToString('x2') }) -join '')
        } finally {
            $sha.Dispose()
        }
    } catch {
        $hash = "<hash failed: $($_.Exception.Message)>"
    }
    $dest = Join-Path $records "uninstaller-$safe.exe"
    try { Copy-Item -LiteralPath $candidate -Destination $dest -Force -ErrorAction Stop } catch {
        $lines.Add("  uninstaller at '$Point': could not copy - $($_.Exception.Message)")
    }

    $fileVersion = ""; $productVersion = ""
    try {
        $vi = $item.VersionInfo
        $fileVersion = [string]$vi.FileVersion
        $productVersion = [string]$vi.ProductVersion
    } catch { }

    $sigSubject = ""; $sigStatus = ""
    try {
        $sig = Get-AuthenticodeSignature -LiteralPath $candidate
        $sigStatus = [string]$sig.Status
        if ($null -ne $sig.SignerCertificate) { $sigSubject = [string]$sig.SignerCertificate.Subject }
    } catch { }

    $lines.Add("  uninstaller at '$Point':")
    $lines.Add("      path        : $candidate")
    $lines.Add("      sha256      : $hash")
    $lines.Add("      size        : $($item.Length)")
    $lines.Add("      lastWrite   : $($item.LastWriteTimeUtc.ToString('o'))")
    $lines.Add("      fileVersion : $fileVersion")
    $lines.Add("      prodVersion : $productVersion")
    $lines.Add("      signature   : $sigStatus  $sigSubject")
    if ($null -ne $UninstallEntry) {
        $lines.Add("      registry    : UninstallString=$($UninstallEntry.UninstallString)")
        $lines.Add("      registry    : InstallLocation=$($UninstallEntry.InstallLocation) DisplayVersion=$($UninstallEntry.DisplayVersion)")
    }
    $lines.Add("      copied to   : uninstaller-$safe.exe")

    # Recorded so the end-of-run verdict compares generations rather than leaving it to a reader.
    $script:UninstallerHashes[$Point] = $hash
    Write-Host "   uninstaller at '$Point': sha256=$($hash.Substring(0,16)) size=$($item.Length) version=$fileVersion"
}

# The hashes seen so far, so the report can state plainly whether they are all the same binary.
$script:UninstallerHashes = [ordered]@{}
$script:ChannelSizes = [ordered]@{}

function Save-DiagnosticSnapshot {
    <#
        Copy the diagnostic channel aside under a phase-specific name, and report its size and
        last lines.

        WHY PER-PHASE SNAPSHOTS. The channel is one file that every installer and uninstaller
        invocation appends to, and the uninstaller re-launches itself from a copy in a temp
        directory. Reading it only at the end means the phase that actually failed is
        indistinguishable from the ones that succeeded, and a single overwrite or truncation
        anywhere in the run destroys the whole sequence.

        Size is reported because "the file did not grow" is itself a finding: ciDiagnostic opens
        with mode `a`, so a phase that ran and wrote nothing leaves the size unchanged.
    #>
    param([string]$Phase)

    if ([string]::IsNullOrWhiteSpace($DiagnosticPath)) { return }
    $records = Split-Path -Parent $RecordPath
    $safe = ($Phase -replace '[^A-Za-z0-9]+', '-').Trim('-')
    $dest = Join-Path $records "installer-diagnostic.$safe.txt"

    if (-not (Test-Path -LiteralPath $DiagnosticPath)) {
        $lines.Add("  diagnostic channel after '$Phase': FILE ABSENT at $DiagnosticPath")
        Write-Host "   diagnostic after '$Phase': file absent"
        return
    }
    $size = (Get-Item -LiteralPath $DiagnosticPath).Length
    try {
        Copy-Item -LiteralPath $DiagnosticPath -Destination $dest -Force -ErrorAction Stop
    } catch {
        $lines.Add("  diagnostic channel after '$Phase': could not copy - $($_.Exception.Message)")
        return
    }
    $content = Get-Content -LiteralPath $DiagnosticPath -Raw -ErrorAction SilentlyContinue
    $all = @(($content -split "`r?`n") | Where-Object { $_ -match '\S' })
    $script:ChannelSizes[$Phase] = $size
    $lines.Add("  diagnostic channel after '$Phase': $size bytes, $($all.Count) non-blank line(s)")
    # The tail is what matters for locating a stop: the last marker before silence.
    foreach ($line in ($all | Select-Object -Last 8)) { $lines.Add("      $line") }
    Write-Host "   diagnostic after '$Phase': $size bytes; last lines:"
    foreach ($line in ($all | Select-Object -Last 8)) { Write-Host "      $line" }
}

function Show-ProcessSnapshot {
    # Records the process and window state AROUND a hung installer step.
    #
    # # Why this exists
    #
    # The old timeout message said "in silent mode that means a prompt appeared that /S could not
    # answer". That reading is too strong: it asserts a CAUSE from a SYMPTOM. A process can also stay
    # alive with no prompt at all - a child that never returns, an attached helper, a message loop or
    # timer that outlives the work, a handle held on a mutex.
    #
    # Those cases are distinguishable, but only if the state is captured while the process is still
    # there. So this runs BEFORE anything is killed, and it records the facts that separate them: who
    # the installer's children are, whether it has a window at all, and whether it is responding.
    #
    # Read-only on purpose. It never kills, stops or mutates anything, so it is safe to call from an
    # already-failing path - and so a run that hangs can be diagnosed without a second run.
    param([System.Diagnostics.Process]$Process)

    $snapshot = [System.Collections.Generic.List[string]]::new()

    if ($null -eq $Process) {
        $snapshot.Add("(no process object; the step did not reach Start-Process)")
        return $snapshot
    }

    $procId = $Process.Id
    $snapshot.Add("hung process: PID $procId")
    try {
        $Process.Refresh()
        $snapshot.Add("  image            : $($Process.ProcessName)")
        if (-not [string]::IsNullOrWhiteSpace($Process.Path)) { $snapshot.Add("  path             : $($Process.Path)") }
        $snapshot.Add("  started          : $($Process.StartTime.ToUniversalTime().ToString('o'))")
        $snapshot.Add("  total processor  : $([math]::Round($Process.TotalProcessorTime.TotalSeconds, 1))s")
        # Responding is false for a blocked message pump, and true for a live loop. It is the single
        # cheapest discriminator between "waiting for an answer" and "still working".
        $snapshot.Add("  responding       : $($Process.Responding)")
        Add-Member -InputObject $Process -NotePropertyName SeenMainWindowTitle -NotePropertyValue "" -Force -ErrorAction SilentlyContinue
        $title = ""
        try { $title = $Process.MainWindowTitle } catch { $title = "(unavailable)" }
        $snapshot.Add("  main window title: '$(if ([string]::IsNullOrEmpty($title)) { '<none>' } else { $title })'")
        $snapshot.Add("  main window handle: $($Process.MainWindowHandle)")
        try { $snapshot.Add("  thread count     : $($Process.Threads.Count)") } catch { $snapshot.Add("  thread count     : (unavailable)") }
        try { $snapshot.Add("  handle count     : $($Process.HandleCount)") } catch { $snapshot.Add("  handle count     : (unavailable)") }
    } catch {
        $snapshot.Add("  (process introspection failed: $($_.Exception.Message))")
    }

    # Every process whose parent chain reaches the hung installer. NSIS spawns children for plugin
    # calls (nsExec, PowerShell helpers, the daemon), and a child that outlives its parent's work is
    # exactly hypothesis B/F.
    try {
        $all = Get-CimInstance Win32_Process -ErrorAction Stop |
            Select-Object ProcessId, ParentProcessId, Name, CommandLine, CreationDate
        $byParent = @{}
        foreach ($p in $all) {
            $key = [int]$p.ParentProcessId
            if (-not $byParent.ContainsKey($key)) { $byParent[$key] = [System.Collections.Generic.List[object]]::new() }
            $byParent[$key].Add($p)
        }
        $descendants = [System.Collections.Generic.List[object]]::new()
        $frontier = @($procId)
        $seen = @{}
        while ($frontier.Count -gt 0) {
            $next = @()
            foreach ($parentId in $frontier) {
                if ($seen.ContainsKey($parentId)) { continue }
                $seen[$parentId] = $true
                if (-not $byParent.ContainsKey($parentId)) { continue }
                foreach ($child in $byParent[$parentId]) {
                    $descendants.Add($child)
                    $next += [int]$child.ProcessId
                }
            }
            $frontier = $next
        }
        if ($descendants.Count -eq 0) {
            $snapshot.Add("descendants: NONE (the installer has no child process, so it is not waiting on one)")
        } else {
            $snapshot.Add("descendants: $($descendants.Count)")
            foreach ($d in $descendants) {
                $cli = $d.CommandLine
                if ([string]::IsNullOrWhiteSpace($cli)) { $cli = "(no command line)" }
                if ($cli.Length -gt 400) { $cli = $cli.Substring(0, 400) + "..." }
                $snapshot.Add("  PID $($d.ProcessId)  parent $($d.ParentProcessId)  $($d.Name)")
                $snapshot.Add("      started: $($d.CreationDate)")
                $snapshot.Add("      cmdline: $cli")
            }
        }

        # Daemon and any surviving Electron/PowerShell helpers, whether or not they are descendants.
        foreach ($pattern in @('sing-box-daemon', 'electron', 'powershell', 'pwsh', 'nsExec', 'Un_A', 'Uninstall')) {
            $hits = $all | Where-Object { $_.Name -like "*$pattern*" }
            if ($hits) {
                $snapshot.Add("matching '*${pattern}*':")
                foreach ($h in $hits) {
                    $snapshot.Add("  PID $($h.ProcessId)  parent $($h.ParentProcessId)  $($h.Name)  started $($h.CreationDate)")
                }
            }
        }
    } catch {
        $snapshot.Add("(process enumeration failed: $($_.Exception.Message))")
    }

    # Top-level windows that are visible but unowned by a console, which is what a prompt looks like.
    try {
        $windows = Get-Process -ErrorAction SilentlyContinue |
            Where-Object { $_.MainWindowHandle -ne 0 -and -not [string]::IsNullOrEmpty($_.MainWindowTitle) } |
            Select-Object Id, ProcessName, MainWindowTitle
        if ($windows) {
            $snapshot.Add("visible top-level windows:")
            foreach ($w in $windows) { $snapshot.Add("  PID $($w.Id)  $($w.ProcessName)  '$($w.MainWindowTitle)'") }
        } else {
            $snapshot.Add("visible top-level windows: none")
        }
    } catch {
        $snapshot.Add("(window enumeration failed: $($_.Exception.Message))")
    }

    return $snapshot
}

function Show-ServiceStateProbe {
    <#
        Six facts about the daemon service, printed the moment an uninstall returns 0.

        WHY THIS EXISTS. The uninstaller exits 0 and then the settle wait times out, and the
        dump taken moments later shows the service already gone. Two very different causes look
        identical from outside: the service is genuinely slow to leave, or THE HARNESS ITSELF is
        holding it open.

        The second is not hypothetical. `Get-Service` returns a ServiceController that holds a
        handle on the service, and Windows will not remove a service - nor its
        HKLM\SYSTEM\CurrentControlSet\Services key - until every handle to it is closed. A
        ServiceController that is never disposed keeps that handle for as long as the object is
        reachable, so a test that polls Get-Service can pin the very service it is waiting to
        disappear. 1072 (ERROR_SERVICE_MARKED_FOR_DELETE) surviving indefinitely is the classic
        signature.

        So this probe reports the state in a form that distinguishes them, using sc.exe and CIM
        only - it deliberately does NOT call Get-Service, because a probe that itself takes a
        handle could not be trusted to answer the question.
    #>
    param([string]$Phase)

    $lines.Add("  -- service state probe ($Phase) --")

    # (1) queryex: state plus the daemon PID, if any.
    $queryex = Invoke-NativeCommand { sc.exe queryex $serviceName }
    $lines.Add("  sc.exe queryex $serviceName : exit $($queryex.ExitCode)")
    foreach ($l in ($queryex.Output -split "`r?`n" | Where-Object { $_ -match '\S' })) { $lines.Add("      $l") }

    # (2) qc: the registered configuration, and whether it still exists at all.
    $qc = Invoke-NativeCommand { sc.exe qc $serviceName }
    $lines.Add("  sc.exe qc $serviceName : exit $($qc.ExitCode)")

    # (3) CIM view: State and ProcessId in one place. CIM rather than Get-Service, deliberately.
    $cim = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
    if ($null -eq $cim) {
        $lines.Add("  Win32_Service : absent")
    } else {
        $lines.Add("  Win32_Service : State=$($cim.State) ProcessId=$($cim.ProcessId) Started=$($cim.Started) ExitCode=$($cim.ExitCode)")
    }

    # (4) the registry key whose survival is what actually keeps the settle condition false.
    $serviceKey = "HKLM:\SYSTEM\CurrentControlSet\Services\$serviceName"
    if (Test-Path -LiteralPath $serviceKey) {
        $stamp = Get-ItemProperty -Path $serviceKey -ErrorAction SilentlyContinue
        $lines.Add("  $serviceKey : PRESENT (DeleteFlag=$($stamp.DeleteFlag) ImagePath=$($stamp.ImagePath))")
    } else {
        $lines.Add("  $serviceKey : absent")
    }

    # (5) is the daemon process itself still alive?
    $daemonPids = @(Get-CimInstance Win32_Process -Filter "Name='sing-box-daemon.exe'" -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ProcessId)
    if ($daemonPids.Count -eq 0) {
        $lines.Add("  sing-box-daemon.exe processes : none")
    } else {
        $lines.Add("  sing-box-daemon.exe processes : $($daemonPids -join ', ')  <-- still alive")
    }

    # (6) the specific code that means "deleted, but something still holds it open".
    $markedForDelete = ($queryex.Output -match '1060') -eq $false -and ($queryex.Output -match 'marked for deletion')
    $lines.Add("  interpreted: absent=$(($queryex.Output -match '1060')) markedForDelete=$markedForDelete")
    return [pscustomobject]@{
        Absent          = [bool]($queryex.Output -match '1060')
        MarkedForDelete = [bool]$markedForDelete
        RegistryPresent = [bool](Test-Path -LiteralPath $serviceKey)
        CimState        = if ($null -eq $cim) { "absent" } else { [string]$cim.State }
        DaemonPids      = $daemonPids
    }
}

function Show-Diagnostics {
    Show-InstallerDiagnostic
    foreach ($diagnostic in @(
        @{ Name = "installation identity"; Command = { $state = Get-InstallationState; Add-InstallationIdentity $state | Out-Null; ($lines | Select-Object -Last 6) -join [Environment]::NewLine } },
        @{ Name = "uninstall registry candidates"; Command = { (Format-UninstallEntries -Entries @(Get-UninstallRegistryEntries)) -join [Environment]::NewLine } },
        @{ Name = "$installationLayoutKey"; Command = { Get-ItemProperty -Path $installationLayoutKey -ErrorAction SilentlyContinue | Format-List | Out-String } },
        @{ Name = "sc.exe query $serviceName"; Command = { sc.exe query $serviceName 2>&1 | Out-String } },
        @{ Name = "service config"; Command = { sc.exe qc $serviceName 2>&1 | Out-String } },
        @{ Name = "install directory"; Command = { $state = Get-InstallationState; $d = ""; if ($null -ne $state) { $d = $state.Root }; if ($d -and (Test-Path -LiteralPath $d)) { Get-ChildItem -LiteralPath $d -Recurse -File | Select-Object -First 60 FullName, Length | Format-Table -AutoSize | Out-String } else { "(not present)" } } },
        @{ Name = "sing-box-daemon processes"; Command = { Get-Process -Name "sing-box-daemon" -ErrorAction SilentlyContinue | Format-Table Id, ProcessName, Path -AutoSize | Out-String } },
        @{ Name = "System log (sing-box sources)"; Command = { Get-EventLog -LogName System -Newest 60 -ErrorAction SilentlyContinue | Where-Object { $_.Source -match 'sing-box' } | Format-List TimeGenerated, Source, EntryType, Message | Out-String } },
        @{ Name = "Application log (sing-box sources)"; Command = { Get-EventLog -LogName Application -Newest 60 -ErrorAction SilentlyContinue | Where-Object { $_.Source -match 'sing-box' } | Format-List TimeGenerated, Source, EntryType, Message | Out-String } }
    )) {
        Write-Host "-- $($diagnostic.Name) --"
        $output = ""
        try { $output = & $diagnostic.Command } catch { $output = "diagnostic failed: $($_.Exception.Message)" }
        if ([string]::IsNullOrWhiteSpace($output)) { $output = "(no output)" }
        Write-Host $output
        $lines.Add("diagnostic: $($diagnostic.Name)")
        $lines.Add($output)
    }
}

function Fail-Gate {
    param([string]$Message)
    $lines.Add("")
    $lines.Add("FAIL: $Message")
    Write-Host ""
    Write-Host "== diagnostics =="
    Show-Diagnostics
    Save-Record
    Write-Error "installer lifecycle smoke test failed: $Message"
    exit 1
}

function Invoke-Silently {
    param([string]$FilePath, [string[]]$Arguments, [string]$What)
    Write-Host "  > $what $FilePath $($Arguments -join ' ')"
    $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -PassThru
    $null = $process | Wait-Process -Timeout $OperationTimeoutSeconds -ErrorAction SilentlyContinue
    if (-not $process.HasExited) {
        # Captured BEFORE the kill, because the state that explains the hang is destroyed by it.
        $snapshot = Show-ProcessSnapshot -Process $process
        Write-Host ""
        Write-Host "-- hung-step process snapshot --"
        foreach ($item in $snapshot) { Write-Host $item }
        $lines.Add("")
        $lines.Add("hung-step process snapshot ($What):")
        foreach ($item in $snapshot) { $lines.Add("  $item") }
        $snapshotPath = Join-Path (Split-Path -Parent $RecordPath) "hung-step-process-snapshot.txt"
        try { $snapshot | Set-Content -LiteralPath $snapshotPath -Encoding utf8 -ErrorAction Stop } catch { }

        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        # The message states the SYMPTOM and stops there.
        #
        # It used to say "in silent mode that means a prompt appeared that /S could not answer",
        # which asserts a CAUSE from a SYMPTOM. A process can outlive its work without any prompt: a
        # child that never returns, an attached helper, a message loop or timer, a held handle. That
        # wording pointed every reader at NSIS pages and away from those, and it has now been
        # falsified once - see the snapshot's `descendants` and `responding` rows.
        Fail-Gate "$What did not finish within $OperationTimeoutSeconds seconds. It may be waiting on an unanswerable prompt, on a child process, or on a loop/timer that outlived its work; the process snapshot above distinguishes them."
    }
    Write-Host "    exit code $($process.ExitCode)"
    return $process.ExitCode
}


function Assert-Installed {
    param([string]$Phase)
    $null = Wait-Until -Description "$Phase installation to become identifiable" -TimeoutSeconds 180 -Condition {
        -not [string]::IsNullOrWhiteSpace((Get-InstallationRootFromDaemonPath (Get-CommandExecutablePath (Get-DaemonServiceCommandLine -ServiceName $serviceName))))
    }
    $state = Get-InstallationState
    if ($null -eq $state -or [string]::IsNullOrWhiteSpace($state.Root)) {
        Add-InstallationIdentity $state
        Fail-Gate "$Phase`: no installation could be identified from the SCM"
    }
    $root = $state.Root
    $lines.Add("  installation root: $root")

    $required = [ordered]@{
        "sing-box.exe"                          = (Join-Path $root "sing-box.exe")
        "resources\daemon\sing-box-daemon.exe" = (Join-Path $root "resources\daemon\sing-box-daemon.exe")
        "resources\daemon\libcronet.dll"       = (Join-Path $root "resources\daemon\libcronet.dll")
        "resources\daemon\WinDivert64.sys"     = (Join-Path $root "resources\daemon\WinDivert64.sys")
        "resources\native\windows_share.node"  = (Join-Path $root "resources\native\windows_share.node")
    }
    foreach ($name in $required.Keys) {
        $exists = Test-Path -LiteralPath $required[$name] -PathType Leaf
        $lines.Add("  $name : $(if ($exists) { 'present' } else { 'MISSING' })")
        if (-not $exists) { Fail-Gate "$Phase`: $name is missing at $($required[$name])" }
    }

    if ($null -eq $state.LayoutVersion -or [int]$state.LayoutVersion -ne 2) {
        Fail-Gate "$Phase`: $installationLayoutKey does not record LayoutVersion 2 (found '$($state.LayoutVersion)'); clients/desktop reads that key at startup"
    }
    $lines.Add("  layout registry: LayoutVersion 2, InstallationID $($state.InstallationID)")

    # Fail closed on the uninstall entry, and never silently pick the first.
    $matches = @($state.UninstallMatches)
    if ($matches.Count -eq 0) {
        $lines.Add("  uninstall entries: NONE matched $root; every candidate follows")
        foreach ($line in (Format-UninstallEntries -Entries @(Get-UninstallRegistryEntries))) { $lines.Add($line) }
        Fail-Gate "$Phase`: no uninstall registry entry has an InstallLocation - or an uninstaller - inside $root"
    }
    if ($matches.Count -gt 1) {
        $lines.Add("  uninstall entries: $($matches.Count) matched $root")
        foreach ($line in (Format-UninstallEntries -Entries $matches)) { $lines.Add($line) }
        Fail-Gate "$Phase`: $($matches.Count) uninstall registry entries match $root; refusing to choose one"
    }
    if (-not $candidateDumpWritten) {
        $candidateDumpWritten = $true
        $allCandidates = @(Get-UninstallRegistryEntries)
        $lines.Add("  every uninstall registry entry on this machine: $($allCandidates.Count)")
        foreach ($line in (Format-UninstallEntries -Entries $allCandidates)) { $lines.Add($line) }
    }

    $entry = $matches[0].Entry
    $lines.Add("  uninstall entry:    $($entry.PSPath)")
    $lines.Add("  display name:       $($entry.DisplayName)   (audit metadata only, never a key)")
    $lines.Add("  display version:    $($entry.DisplayVersion)")
    $lines.Add("  install location:   $($entry.InstallLocation)")
    $lines.Add("  uninstall string:   $($entry.UninstallString)")
    $lines.Add("  matched by:         $($matches[0].MatchMethod)")
    if ([string]::IsNullOrWhiteSpace($entry.UninstallString)) {
        Fail-Gate "$Phase`: the matched uninstall entry has no UninstallString"
    }

    $daemonPath = $required["resources\daemon\sing-box-daemon.exe"]
    $version = Invoke-NativeCommand { & $daemonPath version }
    $versionOutput = $version.Output.Trim()
    $lines.Add("  daemon version: $versionOutput (exit code $($version.ExitCode))")
    if ($version.ExitCode -ne 0) { Fail-Gate "$Phase`: the installed daemon's version command exited $($version.ExitCode)" }
    if ($versionOutput -notmatch [regex]::Escape("version $ExpectedVersion")) {
        Fail-Gate "$Phase`: the installed daemon reports '$versionOutput', expected version $ExpectedVersion"
    }

    $serviceState = Get-ServiceState
    $lines.Add("  service: $serviceState")
    if ($serviceState -ne "Running") {
        Fail-Gate "$Phase`: the $serviceName service is '$serviceState', expected Running"
    }

    return [pscustomobject]@{ Root = $root; UninstallEntry = $entry }
}


function Invoke-Uninstall {
    param([string]$Phase, [Parameter(Mandatory = $true)]$UninstallEntry, [switch]$DeleteAppData)
    # The uninstaller is the one the discovered entry names. No path is constructed from a
    # product name.
    $uninstallString = [string]$UninstallEntry.UninstallString
    if ([string]::IsNullOrWhiteSpace($uninstallString)) {
        Fail-Gate "$Phase`: the matched uninstall entry has no UninstallString"
    }
    $uninstaller = Get-CommandExecutablePath $uninstallString
    if ([string]::IsNullOrWhiteSpace($uninstaller) -or -not (Test-Path -LiteralPath $uninstaller -PathType Leaf)) {
        Fail-Gate "$Phase`: the uninstaller '$uninstaller' named by $($UninstallEntry.PSPath) does not exist"
    }
    # The installer's own rollback uses /S /allusers, so that pair is the supported one.
    $arguments = @("/S", "/allusers")
    # The diagnostic channel must be requested for the UNINSTALLER too.
    #
    # The installer is launched through Get-InstallArguments, which appends this option; the
    # uninstaller was launched with a hardcoded argument pair that never did. The uninstaller is a
    # separate executable image with its own command line, and customUnInit reads ITS OWN
    # parameters - so $ciDiagnosticPath stayed empty in the uninstall pass and every
    # uninstall-side marker was inert: the guard `$ciDiagnosticPath != ""` was false and nothing
    # was written.
    #
    # That is why the diagnostic file sat at 162 bytes with no uninstall markers, and why "there
    # is no [service uninstall] marker" was read as evidence that the daemon's uninstall never
    # ran. It was not evidence. Two independent mistakes produced the same false negative: the
    # uninstall pass never initialised the variable, and this harness never passed the option.
    if (-not [string]::IsNullOrWhiteSpace($DiagnosticPath)) {
        $arguments += "/CI-DIAGNOSTIC-PATH=$DiagnosticPath"
    }
    if ($DeleteAppData) {
        # build/installer.nsh: `--delete-app-data` sets $keepUninstallData to unchecked,
        # which is the ONLY path that runs the data-removal branch. Without it the
        # uninstaller keeps the data by design (electron-builder.yml:
        # deleteAppDataOnUninstall: false) and the installer layout registry key is
        # deliberately left behind so a reinstall can reuse the data directories.
        $arguments += "--delete-app-data"
    }
    # Observe the uninstaller from OUTSIDE, and record every image of it that appears.
    #
    # WHY EXTERNAL OBSERVATION RATHER THAN INSTRUMENTING THE UNINSTALLER. The question is whether
    # the process that actually reaches customUnInit received /CI-DIAGNOSTIC-PATH=. Asking
    # ciDiagnostic to answer that is circular: if the path failed to parse, nothing is written, and
    # "nothing written" is then read as "the code did not run". This captures the command lines
    # from Win32_Process instead, so it holds regardless of whether the diagnostic channel works.
    #
    # electron-builder's uninstaller COPIES ITSELF to a temp directory and re-launches, and
    # ExecWait-based re-launches are a classic place for arguments to be dropped. The daemon's
    # uninstall path is a separate image again. So there can be two or three processes named like
    # the uninstaller, with different command lines, and only one of them is the one that runs our
    # macro. Sampling around the call catches all of them.
    $before = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -like '*Uninstall*' -or $_.CommandLine -like "*$([System.IO.Path]::GetFileName($uninstaller))*" })
    $lines.Add("  uninstaller processes BEFORE the call: $($before.Count)")
    foreach ($p in $before) {
        $lines.Add("      PID $($p.ProcessId) PPID $($p.ParentProcessId) :: $($p.CommandLine)")
    }

    $exitCode = Invoke-Silently -FilePath $uninstaller -Arguments $arguments -What "$Phase uninstall"

    $after = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -like '*Uninstall*' -or $_.CommandLine -like "*$([System.IO.Path]::GetFileName($uninstaller))*" })
    $lines.Add("  uninstaller processes AFTER the call: $($after.Count)")
    foreach ($p in $after) {
        $lines.Add("      PID $($p.ProcessId) PPID $($p.ParentProcessId) :: $($p.CommandLine)")
    }
    # The composed arguments are recorded too, so the recorded command lines can be compared
    # against what the harness intended to pass.
    $lines.Add("  harness passed: `"$uninstaller`" $($arguments -join ' ')")

    if ($exitCode -ne 0) { Fail-Gate "$Phase`: the uninstaller exited $exitCode" }
}


function Assert-Uninstalled {
    param([string]$Phase, [string]$InstallationRoot, $UninstallEntry, [switch]$DataPreserved)

    # The settle condition must match what the uninstaller is SUPPOSED to do in this phase.
    #
    # Both phases used to wait for the same thing: the service gone AND the layout key gone.
    # But the data-preserving uninstall keeps the layout key on purpose - build/installer.nsh
    # says so, electron-builder.yml sets deleteAppDataOnUninstall: false, and the body of this
    # very function handles that case explicitly a few lines below. So this wait could never be
    # satisfied on the data-preserving phase: it burned its full 300 s and then reported a
    # timeout, which reads exactly like the uninstaller hanging.
    #
    # That is what made this look like a product hang. The uninstaller had already exited 0 and
    # removed the service correctly; the CONDITION was wrong, not the installer.
    #
    # Making it conditional is the whole fix, and it keeps the assertion strong: the phase that
    # passes --delete-app-data still waits for the key to disappear.
    $null = Wait-Until -Description "$Phase uninstall to settle" -TimeoutSeconds 300 -Condition {
        if ($DataPreserved) {
            $null -eq (Get-DaemonServiceCommandLine -ServiceName $serviceName)
        } else {
            ($null -eq (Get-DaemonServiceCommandLine -ServiceName $serviceName)) -and
            ($null -eq (Get-InstallationLayoutRegistry -RegistryPath $installationLayoutKey))
        }
    }

    $state = Get-ServiceState
    $lines.Add("  service after uninstall: $state")
    if ($state -ne "absent") { Fail-Gate "$Phase`: the $serviceName service is still '$state'" }
    $query = Invoke-NativeCommand { sc.exe query $serviceName }
    if ($query.ExitCode -eq 0) { Fail-Gate "$Phase`: sc.exe query still reports $serviceName" }
    if ($query.Output -match "marked for deletion") {
        Fail-Gate "$Phase`: the service is marked for deletion; the next install would fail"
    }

    $layout = Get-InstallationLayoutRegistry -RegistryPath $installationLayoutKey
    if ($DataPreserved) {
        # The uninstaller keeps the data directories unless it is told not to, and it keeps
        # the layout key with them so a reinstall can reuse them. Asserting the key gone
        # here would be asserting behaviour the product does not have; the next phase runs
        # the uninstall that DOES remove it, so the assertion is still made - just where it
        # is actually about something.
        if ($null -ne $layout) {
            $lines.Add("  layout registry: retained by design (LayoutVersion $($layout.LayoutVersion), InstallationID $($layout.InstallationID))")
            $lines.Add("    the uninstaller preserves data unless --delete-app-data is given; the final phase passes it")
        } else {
            $lines.Add("  layout registry: absent (not retained by this uninstall)")
        }
    } else {
        if ($null -ne $layout) {
            Fail-Gate "$Phase`: the installer layout registry key survived a data-removing uninstall"
        }
        $lines.Add("  layout registry: absent after --delete-app-data")
    }

    if ($null -ne $UninstallEntry) {
        $survivors = @(Get-UninstallRegistryEntries | Where-Object { $_.PSPath -eq $UninstallEntry.PSPath })
        if ($survivors.Count -gt 0) {
            Fail-Gate "$Phase`: the uninstall registry entry $($UninstallEntry.PSPath) survived uninstall"
        }
        $lines.Add("  uninstall entry: absent ($($UninstallEntry.PSPath))")
    }

    if (-not [string]::IsNullOrWhiteSpace($InstallationRoot)) {
        foreach ($relative in @("sing-box.exe", "resources\daemon\sing-box-daemon.exe")) {
            $path = Join-Path $InstallationRoot $relative
            if (Test-Path -LiteralPath $path) {
                Fail-Gate "$Phase`: $relative survived uninstall at $path"
            }
        }
        $lines.Add("  product payload: absent from $InstallationRoot")
        if (Test-Path -LiteralPath $InstallationRoot) {
            # Reported rather than failed: only product payload is asserted absent. A left
            # behind log or an empty directory is not a broken installation.
            $remaining = @(Get-ChildItem -LiteralPath $InstallationRoot -Recurse -File -ErrorAction SilentlyContinue)
            $lines.Add("  note: the installation directory remains with $($remaining.Count) file(s)")
        }
    }

    $remaining = @(Get-Process -Name "sing-box-daemon" -ErrorAction SilentlyContinue)
    $lines.Add("  orphan daemon processes: $($remaining.Count)")
    if ($remaining.Count -gt 0) { Fail-Gate "$Phase`: a sing-box-daemon process survived uninstall" }
}

# --- preconditions -----------------------------------------------------------
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail-Gate "the installer is per-machine and needs an elevated process (running as $($identity.Name))"
}
if (-not (Test-Path -LiteralPath $InstallerPath -PathType Leaf)) {
    Fail-Gate "the installer does not exist: $InstallerPath"
}

# The installer runs its preflight and its service command through Windows PowerShell 5.1
# ($SYSDIR\WindowsPowerShell\v1.0\powershell.exe). Started from PowerShell 7 - which is what
# this gate and the runner use - that child inherits PowerShell 7's PSModulePath, and 5.1
# then resolves Microsoft.PowerShell.Security to PowerShell 7's incompatible copy of the
# module ahead of its own:
#
#     [preflight] exit=30  The 'Get-Acl' command was found in the module
#     'Microsoft.PowerShell.Security', but the module could not be loaded.
#
# Prepending the 5.1 directory is not enough, because PowerShell 7 already appends it:
# the check that looked for it found it, skipped the change, and the failure stayed. The
# child's module path is therefore rebuilt from the 5.1 locations only, so it cannot pick up
# a module built for a different PowerShell. This changes the harness's environment, not the
# product's behaviour: a user double-clicking the installer inherits Explorer's environment.
$windowsPowerShellModules = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\Modules"
if (-not (Test-Path -LiteralPath $windowsPowerShellModules)) {
    Fail-Gate "the Windows PowerShell 5.1 module directory is missing: $windowsPowerShellModules"
}
$installerModulePath = @(
    $windowsPowerShellModules,
    (Join-Path $env:ProgramFiles "WindowsPowerShell\Modules"),
    (Join-Path $env:USERPROFILE "Documents\WindowsPowerShell\Modules")
) -join ";"
$lines.Add("psmodulepath for the installer's Windows PowerShell 5.1 children: $installerModulePath")
Write-Host "setting PSModulePath for the installer to the Windows PowerShell 5.1 module locations"
$env:PSModulePath = $installerModulePath

# A previous gate may have left a service behind; a clean state is a precondition, and it
# is reported rather than assumed.
$preconditionState = Get-InstallationState
$preconditionMatches = @()
if ($null -ne $preconditionState) { $preconditionMatches = @($preconditionState.UninstallMatches) }
if ((Get-ServiceState) -ne "absent" -or $null -ne $preconditionState -or $preconditionMatches.Count -gt 1) {
    $lines.Add("clean state: a previous installation or service was present; removing it first")
    Write-Host "an installation or service was already present; uninstalling before the test"
    Add-InstallationIdentity $preconditionState
    if ($preconditionMatches.Count -gt 1) {
        foreach ($line in (Format-UninstallEntries -Entries $preconditionMatches)) { $lines.Add($line) }
        Fail-Gate "the machine has $($preconditionMatches.Count) installations matching one root; refusing to remove one"
    }
    if ($preconditionMatches.Count -eq 1) {
        # Only this installation's own uninstaller, discovered structurally.
        Invoke-Uninstall -Phase "precondition" -UninstallEntry $preconditionMatches[0].Entry
    } elseif ($null -ne $preconditionState -and -not [string]::IsNullOrWhiteSpace($preconditionState.Root)) {
        # A service without a usable uninstall entry: remove only that service, and say so.
        $lines.Add("precondition: no uninstall entry matched $($preconditionState.Root); deleting the service only")
        $deleted = Invoke-NativeCommand { sc.exe delete $serviceName }
        $lines.Add("precondition: sc.exe delete exited $($deleted.ExitCode)")
    } elseif ((Get-ServiceState) -ne "absent") {
        $deleted = Invoke-NativeCommand { sc.exe delete $serviceName }
        $lines.Add("precondition: sc.exe delete exited $($deleted.ExitCode)")
    }
    Start-Sleep -Seconds 3
    if ((Get-ServiceState) -ne "absent") { Fail-Gate "could not return the machine to a clean state" }
}
$lines.Add("clean state: no service, no uninstall entry")
$lines.Add("")

# --- first install -----------------------------------------------------------
$lines.Add("[1/5] silent install")
$exitCode = Invoke-Silently -FilePath $InstallerPath -Arguments (Get-InstallArguments) -What "install"
$lines.Add("  exit code $exitCode")
if ($exitCode -ne 0) {
    # Which stage aborted is not inferred from the exit code; the diagnostic channel says.
    Fail-Gate "the installer exited $exitCode. The stage that aborted is recorded in the installer diagnostic channel above."
}
# ORDER IS LOAD-BEARING.
#
# Save-UninstallerProvenance used to be called ABOVE Assert-Installed, with
# `-UninstallEntry $installed.UninstallEntry` - a variable that is not assigned until the line
# below it. So the entry was always $null AND the root came from `$InstallationRoot`, which does
# not exist in this scope at all (this scope spells it `$installationRoot`). The provenance points
# cost 4 lines each and proved nothing.
#
# It now runs after Assert-Installed, because that call is what RESOLVES the installation and
# returns the root and the registry entry this function needs. That is still early enough: the
# installation is intact at this moment and the uninstaller has not been touched, which is
# exactly the state "after-install" is supposed to describe. Everything between the two lines is
# a read, so nothing is disturbed.
$installed = Assert-Installed -Phase "after first install"
Save-UninstallerProvenance -Point "after-install" -InstallationRoot $installed.Root -UninstallEntry $installed.UninstallEntry
Save-DiagnosticSnapshot -Phase "1-install"
$installationRoot = $installed.Root
$uninstallEntry = $installed.UninstallEntry
$lines.Add("  PASS")
$lines.Add("")

# --- same-version reinstall --------------------------------------------------
$lines.Add("[2/5] same-version reinstall")
$exitCode = Invoke-Silently -FilePath $InstallerPath -Arguments (Get-InstallArguments) -What "reinstall"
$lines.Add("  exit code $exitCode")
if ($exitCode -ne 0) { Fail-Gate "the same-version reinstall exited $exitCode" }
# Same ordering rule as the first install: the state has to be resolved before its uninstaller
# can be preserved. This is the point that answers "did the reinstall replace the uninstaller?".
$reinstalled = Assert-Installed -Phase "after same-version reinstall"
Save-UninstallerProvenance -Point "after-reinstall" -InstallationRoot $reinstalled.Root -UninstallEntry $reinstalled.UninstallEntry
Save-DiagnosticSnapshot -Phase "2-reinstall"
if ($reinstalled.Root -ne $installationRoot) {
    Fail-Gate "the reinstall moved the installation from $installationRoot to $($reinstalled.Root)"
}
$lines.Add("  PASS (same root, no ERROR_SERVICE_EXISTS, no marked-for-deletion, no StopPending stall)")
$lines.Add("")

# --- uninstall ---------------------------------------------------------------
$lines.Add("[3/5] silent uninstall")
Save-UninstallerProvenance -Point "before-first-uninstall" -InstallationRoot $installationRoot -UninstallEntry $uninstallEntry
Invoke-Uninstall -Phase "first" -UninstallEntry $uninstallEntry
Save-DiagnosticSnapshot -Phase "3-first-uninstall"
# Reported BEFORE the settle wait, so the record shows the machine's actual state at the moment
# the uninstaller returned 0 - not a state reconstructed after a timeout, which is what made this
# indistinguishable from a product hang for several rounds.
$firstUninstallProbe = Show-ServiceStateProbe -Phase "immediately after the first uninstall returned 0"
Assert-Uninstalled -Phase "after first uninstall" -InstallationRoot $installationRoot -UninstallEntry $uninstallEntry -DataPreserved
$lines.Add("  PASS")
$lines.Add("")

# --- clean reinstall ---------------------------------------------------------
$lines.Add("[4/5] clean reinstall after a full uninstall")
$exitCode = Invoke-Silently -FilePath $InstallerPath -Arguments (Get-InstallArguments) -What "reinstall"
$lines.Add("  exit code $exitCode")
if ($exitCode -ne 0) { Fail-Gate "the clean reinstall exited $exitCode" }
# Same ordering rule again: resolve the state, then preserve its uninstaller. This point is the
# one that answers "did the clean install write the current uninstaller, where the reinstall did
# not?" - so it must be collected from the installation this phase just produced.
$reinstalled = Assert-Installed -Phase "after clean reinstall"
Save-UninstallerProvenance -Point "after-clean-reinstall" -InstallationRoot $reinstalled.Root -UninstallEntry $reinstalled.UninstallEntry
Save-DiagnosticSnapshot -Phase "4-clean-reinstall"
$installationRoot = $reinstalled.Root
$uninstallEntry = $reinstalled.UninstallEntry
$lines.Add("  PASS")
$lines.Add("")

# --- final teardown ----------------------------------------------------------
# Strict per-phase increments, measured from the live channel.
#
# "1-install -> 2-reinstall -> 3-first-uninstall -> (clean reinstall) -> 5-final-uninstall" must be
# reported as four separate numbers. An earlier report merged the clean reinstall and the final
# uninstall into one "+713" and attributed it to the final uninstall, which the two steps cannot
# support between them.
function Get-ChannelSize {
    if ([string]::IsNullOrWhiteSpace($DiagnosticPath)) { return -1 }
    if (-not (Test-Path -LiteralPath $DiagnosticPath)) { return -1 }
    return (Get-Item -LiteralPath $DiagnosticPath).Length
}

$lines.Add("[5/5] final uninstall")
Save-UninstallerProvenance -Point "before-final-uninstall" -InstallationRoot $installationRoot -UninstallEntry $uninstallEntry
Invoke-Uninstall -Phase "final" -UninstallEntry $uninstallEntry -DeleteAppData
Save-DiagnosticSnapshot -Phase "5-final-uninstall"
Assert-Uninstalled -Phase "after final uninstall" -InstallationRoot $installationRoot -UninstallEntry $uninstallEntry
$lines.Add("  PASS")
$lines.Add("")

# --- strict per-phase increments, and whether one binary or several ran -----------------------
#
# Both blocks exist because earlier reports stated things the measurements did not support: a
# "+713 bytes for the final uninstall" that was really the clean reinstall and the final uninstall
# added together, and an "uninstaller never writes" generalised from a single phase contributing
# zero. Deltas are computed from the per-phase snapshot sizes, and the hash verdict is computed from
# the preserved copies, so neither is a hand-written number.
$lines.Add("")
$lines.Add("  -- diagnostic channel: strict per-phase increments --")
$prevSize = $null
foreach ($name in $script:ChannelSizes.Keys) {
    $size = $script:ChannelSizes[$name]
    if ($null -eq $prevSize) {
        $lines.Add(("      {0,-26} {1,7} bytes" -f $name, $size))
    } else {
        $delta = $size - $prevSize
        $lines.Add(("      {0,-26} {1,7} bytes   delta {2}" -f $name, $size, $(if ($delta -ge 0) { "+$delta" } else { "$delta" })))
    }
    $prevSize = $size
}

$lines.Add("")
$lines.Add("  -- installed uninstaller identity: same binary at every checkpoint? --")
if ($script:UninstallerHashes.Count -eq 0) {
    $lines.Add("      (no uninstaller was found at any checkpoint)")
} else {
    foreach ($point in $script:UninstallerHashes.Keys) {
        $lines.Add(("      {0,-26} {1}" -f $point, $script:UninstallerHashes[$point]))
    }

    # The PAIRWISE relations, not just a distinct count.
    #
    # A distinct count cannot answer the question this gate exists for. "3 distinct values in 4
    # checkpoints" is consistent with the replacement happening at the reinstall, at the clean
    # reinstall, or with one checkpoint simply failing to read the file - and those imply
    # different root causes. Each adjacent pair is printed, so the record names the step at which
    # the installed uninstaller changed rather than leaving it to be inferred from a count.
    $points = @($script:UninstallerHashes.Keys)
    $pairs = @(
        @{ Label = "after-install        == after-reinstall       ?"; From = "after-install";        To = "after-reinstall" },
        @{ Label = "after-reinstall      == before-first-uninstall ?"; From = "after-reinstall";      To = "before-first-uninstall" },
        @{ Label = "before-first-uninstall == after-clean-reinstall ?"; From = "before-first-uninstall"; To = "after-clean-reinstall" },
        @{ Label = "after-clean-reinstall == before-final-uninstall ?"; From = "after-clean-reinstall"; To = "before-final-uninstall" }
    )
    $lines.Add("      -- pairwise --")
    foreach ($pair in $pairs) {
        if (-not $script:UninstallerHashes.Contains($pair.From) -or -not $script:UninstallerHashes.Contains($pair.To)) {
            $lines.Add("      $($pair.Label) UNKNOWN (a checkpoint did not resolve a binary)")
            continue
        }
        $relation = if ($script:UninstallerHashes[$pair.From] -eq $script:UninstallerHashes[$pair.To]) { "SAME" } else { "DIFFERENT" }
        $lines.Add("      $($pair.Label) $relation")
    }

    # FIRST_UNINSTALL_SHA / FINAL_UNINSTALL_SHA, stated as the two named values the audit is
    # decided on, rather than left for a reader to pick out of the list above.
    $firstPoint = "before-first-uninstall"
    $finalPoint = "before-final-uninstall"
    if ($script:UninstallerHashes.Contains($firstPoint) -and $script:UninstallerHashes.Contains($finalPoint)) {
        $firstSha = $script:UninstallerHashes[$firstPoint]
        $finalSha = $script:UninstallerHashes[$finalPoint]
        $verdict = if ($firstSha -eq $finalSha) { "SAME" } else { "DIFFERENT" }
        $lines.Add("")
        $lines.Add("      FIRST_UNINSTALL_SHA = $firstSha")
        $lines.Add("      FINAL_UNINSTALL_SHA = $finalSha")
        $lines.Add("      FIRST_VS_FINAL      = $verdict")
    } else {
        $lines.Add("")
        $lines.Add("      FIRST_VS_FINAL      = UNKNOWN (a checkpoint did not resolve a binary)")
    }

    $distinct = @($script:UninstallerHashes.Values | Sort-Object -Unique)
    $lines.Add("      distinct sha256 values: $($distinct.Count)   (auxiliary signal only)")
    if ($distinct.Count -gt 1) {
        $lines.Add("      VERDICT: DIFFERENT BINARIES ran at different lifecycle points.")
        $lines.Add("               The first and final uninstalls did not execute the same executable, so")
        $lines.Add("               parameter handling is not the first suspect - the replacement of the")
        $lines.Add("               installed uninstaller during reinstall or clean install is.")
    } else {
        $lines.Add("      VERDICT: the SAME binary ran at every checkpoint.")
        $lines.Add("               A parameter-parsing difference between the first and final uninstall is")
        $lines.Add("               therefore not excluded, and the bootstrap probe is justified.")
        if ($points.Count -lt 4) {
            $lines.Add("               CAUTION: only $($points.Count) of the 4 provenance points resolved a")
            $lines.Add("               binary, so 'every checkpoint' is weaker than it reads.")
        }
    }
}
$lines.Add("")
$lines.Add("PASS: install, verify, same-version reinstall, uninstall, clean reinstall and")
$lines.Add("      final uninstall all completed with no residual service or process state")
Save-Record
foreach ($line in $lines) { Write-Host $line }

Complete-Gate
