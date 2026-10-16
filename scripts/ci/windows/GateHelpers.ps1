# Shared exit-status handling for the Windows CI gates.
#
# # The problem this exists for
#
# GitHub's pwsh wrapper ends a step with `exit $LASTEXITCODE`. $LASTEXITCODE is whatever
# the last NATIVE command left behind - not what the script decided - and several commands
# in these gates are expected to exit non-zero as part of a PASS:
#
#   sc.exe query <name>            returns 1060 when the service is, correctly, absent
#   signtool verify /kp /c ...     returns non-zero for a driver its catalog does not cover
#
# Left alone, that turns a passing gate into a failed step. Gate 5 did exactly that: it
# printed
#
#   PASS: install, SCM registration, Running, status, stop and uninstall all verified
#
# and the step still reported "Process completed with exit code 1", because the last native
# command to run was the `sc.exe query` that proved the service was gone.
#
# # Why not just `exit 0`
#
# An unconditional `exit 0` at the end of a script also hides a real failure that happened
# to leave a non-zero status, and it makes the exit code say nothing about the verdict. The
# leak is closed at its source instead:
#
#   * Invoke-NativeCommand runs a native command, records its exit code and its output, and
#     clears $LASTEXITCODE immediately. Every caller then judges ExitCode explicitly, so an
#     expected non-zero is a positive assertion rather than something tolerated.
#   * Complete-Gate ends the script. Every failure path reaches Fail-Gate first, which exits
#     1 and writes the record, so reaching Complete-Gate IS the verdict.
#
# Nothing here suppresses a failure: a command whose non-zero exit was not expected still
# fails the gate through the caller's check on ExitCode.

$script:GateFailures = New-Object System.Collections.Generic.List[string]

# Runs a native command and returns its output and exit code, clearing $LASTEXITCODE so the
# value cannot leak into the caller's process exit status. The caller MUST judge ExitCode.
function Invoke-NativeCommand {
    param([Parameter(Mandatory = $true)][scriptblock]$Command)
    $output = & $Command 2>&1 | Out-String
    $exitCode = $LASTEXITCODE
    $global:LASTEXITCODE = 0
    return [pscustomobject]@{ Output = $output; ExitCode = $exitCode }
}

# Clears a leaked native status when a command was invoked directly rather than through
# Invoke-NativeCommand. Prefer Invoke-NativeCommand; this exists for the few places where
# the command is part of a larger expression.
function Clear-NativeExitStatus {
    $global:LASTEXITCODE = 0
}

# Polls a condition until it holds or a deadline passes. NSIS uninstallers commonly copy
# themselves to a temporary directory and hand off, so the process that was waited on can
# exit before the work is finished; post-conditions are waited for rather than sampled.
function Wait-Until {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Condition,
        [Parameter(Mandatory = $true)][string]$Description,
        [int]$TimeoutSeconds = 300
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) { return $true }
        Start-Sleep -Seconds 2
    }
    Write-Host "  timed out after $TimeoutSeconds s waiting for: $Description"
    return $false
}

# Ends the script with the exit code the gate decided, not the one a native command left.
# Fail-Gate exits 1 before this is ever reached, so this line means the gate passed.
function Complete-Gate {
    if ($script:GateFailures.Count -gt 0) {
        Write-Error "gate recorded $($script:GateFailures.Count) failure(s) but reached the end; this is a bug in the gate"
        foreach ($failure in $script:GateFailures) { Write-Error "  - $failure" -ErrorAction Continue }
        exit 1
    }
    exit 0
}

# --- installation identity ---------------------------------------------------
#
# # Why identity is structural and never a display name
#
# The first version of the installer gate matched the uninstall registry entry with
#
#     Where-Object { $_.DisplayName -eq "Jiejiebox" }
#
# That is a fixture bug. A display name is presentation: it is the product name, and the
# executable and installation directory are deliberately NOT the product name (they are
# "sing-box", because experimental/boxdd authenticates the application by that exact path).
# So the entry may legitimately be named either way, and a name cannot establish identity
# even when it happens to match - the first entry with that name would be accepted.
#
# These helpers establish identity from things that are facts:
#
#   1. the SCM's own record of where the daemon runs  (Win32_Service.PathName), which is
#      the daemon's command line, parsed with path APIs rather than string replacement;
#   2. the directory layout that command line implies  (<root>\resources\daemon\...),
#      which is the layout experimental/boxdd itself requires;
#   3. the installer's own layout registry key          (LayoutVersion 2);
#   4. and only then the uninstall entry whose InstallLocation IS that root.
#
# DisplayName is recorded as audit metadata. It is never a key.

function Get-NormalizedPath {
    param([string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return "" }
    $trimmed = $Path.Trim().Trim('"')
    $expanded = [System.Environment]::ExpandEnvironmentVariables($trimmed)
    try {
        $full = [System.IO.Path]::GetFullPath($expanded)
    } catch {
        return $expanded.TrimEnd('\').ToLowerInvariant()
    }
    return $full.TrimEnd('\').ToLowerInvariant()
}

# The executable out of a command line, honouring a quoted path and otherwise splitting on
# the first space. Used for both Win32_Service.PathName and UninstallString.
function Get-CommandExecutablePath {
    param([string]$CommandLine)
    if ([string]::IsNullOrWhiteSpace($CommandLine)) { return "" }
    $value = $CommandLine.Trim()
    if ($value.StartsWith('"')) {
        $end = $value.IndexOf('"', 1)
        if ($end -lt 0) { return "" }
        return $value.Substring(1, $end - 1)
    }
    return ($value -split ' ')[0]
}

# The daemon's command line as the SCM has it. Get-CimInstance rather than parsing
# `sc.exe qc` output, which is localised.
#
# The name is REQUIRED rather than defaulted. A default here is a silent way to gate the
# wrong service: this client keeps its own service identity (`jiejiebox-daemon`) precisely so
# that it does not share the upstream client's, and a stale default would make every check
# below report on a service this installation does not own - passing on an empty machine and
# failing on a correct one.
function Get-DaemonServiceCommandLine {
    param([Parameter(Mandatory = $true)][string]$ServiceName)
    $service = Get-CimInstance -ClassName Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
    if ($null -eq $service) { return "" }
    return [string]$service.PathName
}

# <root>\resources\daemon\sing-box-daemon.exe -> <root>, using path APIs and checking each
# component, so a path that merely contains the string "resources" is not accepted.
function Get-InstallationRootFromDaemonPath {
    param([string]$DaemonPath)
    if ([string]::IsNullOrWhiteSpace($DaemonPath)) { return "" }
    if (-not (Test-Path -LiteralPath $DaemonPath -PathType Leaf)) { return "" }
    $daemonDirectory = Split-Path -Parent $DaemonPath
    $resourcesDirectory = Split-Path -Parent $daemonDirectory
    $root = Split-Path -Parent $resourcesDirectory
    if ([string]::IsNullOrWhiteSpace($root)) { return "" }
    if ((Split-Path -Leaf $DaemonPath) -ine "sing-box-daemon.exe") { return "" }
    if ((Split-Path -Leaf $daemonDirectory) -ine "daemon") { return "" }
    if ((Split-Path -Leaf $resourcesDirectory) -ine "resources") { return "" }
    return (Get-NormalizedPath $root)
}

# The installer's own layout key. Required for the same reason as the service name above:
# this client records its layout under its own key, and reading the upstream client's key
# would report the other product's installation as this one's.
function Get-InstallationLayoutRegistry {
    param([Parameter(Mandatory = $true)][string]$RegistryPath)
    $key = Get-ItemProperty -Path $RegistryPath -ErrorAction SilentlyContinue
    if ($null -eq $key) { return $null }
    return $key
}

function Get-UninstallRegistryEntries {
    $roots = @(
        "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
    )
    $entries = New-Object System.Collections.Generic.List[object]
    foreach ($root in $roots) {
        foreach ($entry in @(Get-ItemProperty -Path $root -ErrorAction SilentlyContinue)) {
            if ($null -eq $entry) { continue }
            # Entries with neither an uninstall command nor an install location cannot be
            # matched structurally and are not candidates.
            if ([string]::IsNullOrWhiteSpace($entry.UninstallString) -and [string]::IsNullOrWhiteSpace($entry.InstallLocation)) { continue }
            $entries.Add([pscustomobject]@{
                PSPath              = $entry.PSPath
                Key                 = $entry.PSChildName
                DisplayName         = $entry.DisplayName
                DisplayVersion      = $entry.DisplayVersion
                Publisher           = $entry.Publisher
                InstallLocation     = $entry.InstallLocation
                UninstallString     = $entry.UninstallString
                QuietUninstallString = $entry.QuietUninstallString
            })
        }
    }
    return $entries
}

# Every uninstall entry that IS this installation, by InstallLocation first and by the
# uninstaller's own location second. Returns matches with the method that matched, so a
# fallback is never silent.
function Get-UninstallEntriesForInstallation {
    param([Parameter(Mandatory = $true)][string]$InstallationRoot)
    $normalizedRoot = Get-NormalizedPath $InstallationRoot
    $matches = New-Object System.Collections.Generic.List[object]
    foreach ($entry in Get-UninstallRegistryEntries) {
        $method = ""
        if (-not [string]::IsNullOrWhiteSpace($entry.InstallLocation)) {
            if ((Get-NormalizedPath $entry.InstallLocation) -eq $normalizedRoot) { $method = "InstallLocation" }
        }
        if ([string]::IsNullOrWhiteSpace($method) -and -not [string]::IsNullOrWhiteSpace($entry.UninstallString)) {
            $uninstaller = Get-CommandExecutablePath $entry.UninstallString
            if (-not [string]::IsNullOrWhiteSpace($uninstaller)) {
                $parent = Split-Path -Parent $uninstaller
                if (-not [string]::IsNullOrWhiteSpace($parent) -and (Get-NormalizedPath $parent) -eq $normalizedRoot) {
                    $method = "UninstallString parent"
                }
            }
        }
        if (-not [string]::IsNullOrWhiteSpace($method)) {
            $matches.Add([pscustomobject]@{ Entry = $entry; MatchMethod = $method })
        }
    }
    return $matches
}

# --- the two identity constants, and the state resolver that uses them ------------------------
#
# These live here rather than in the lifecycle gate because BOTH the gate and the two workflows'
# `cleanup:` steps need them. GitHub Actions gives every step a fresh PowerShell process, so a
# function defined in a gate step does not exist in the cleanup step that follows it - and the
# cleanup steps dot-source THIS file. That is exactly the failure that was recorded as
#
#     The term 'Get-InstallationState' is not recognized ...
#
# in the runtime-audit workflow's cleanup while the function plainly existed in the tree, in
# Test-InstallerLifecycle.ps1, one step away. Two copies that drift is the alternative, and it is
# worse: the gate and cleanup would disagree about what "this installation" means.
#
# The names are this client's own, deliberately. The product is "Jiejiebox", the installation
# directory and executable are "sing-box", and the Windows service is "jiejiebox-daemon" - so a
# default derived from any one of them would report on a different object than intended.
$script:JiejieboxServiceName = "jiejiebox-daemon"
$script:JiejieboxInstallationLayoutKey = "HKLM:\SOFTWARE\Jiejiebox\jiejiebox"

# The installation this machine currently has, resolved structurally:
#
#   1. the SCM's own record of the daemon's command line (Win32_Service.PathName),
#   2. the directory layout that command line implies (<root>\resources\daemon\sing-box-daemon.exe),
#   3. the installer's own layout registry key (LayoutVersion 2),
#   4. and only then the uninstall entry whose InstallLocation IS that root.
#
# A display name is never used to match. See the display-name note above.
#
# Every step is guarded: the installation legitimately may not exist, and "absent" is a state this
# has to be able to return rather than throw on.
function Get-InstallationState {
    param(
        [string]$ServiceName = $script:JiejieboxServiceName,
        [string]$LayoutRegistryKey = $script:JiejieboxInstallationLayoutKey
    )
    $commandLine = Get-DaemonServiceCommandLine -ServiceName $ServiceName
    $daemonPath = Get-CommandExecutablePath $commandLine
    $root = Get-InstallationRootFromDaemonPath $daemonPath
    $layout = Get-InstallationLayoutRegistry -RegistryPath $LayoutRegistryKey
    if ([string]::IsNullOrWhiteSpace($root) -and $null -eq $layout) { return $null }
    $matches = @()
    if (-not [string]::IsNullOrWhiteSpace($root)) {
        $matches = @(Get-UninstallEntriesForInstallation -InstallationRoot $root)
    }
    return [pscustomobject]@{
        ServiceCommandLine   = $commandLine
        DaemonPath           = $daemonPath
        Root                 = $root
        LayoutVersion        = $(if ($null -ne $layout) { $layout.LayoutVersion } else { $null })
        InstallationID       = $(if ($null -ne $layout) { $layout.InstallationID } else { $null })
        DaemonDataDirectory  = $(if ($null -ne $layout) { $layout.DaemonDataDirectory } else { $null })
        UninstallMatches     = $matches
    }
}

# Appends the resolver's six fields to the caller's record, through the caller's own `$lines`
# list. It writes to `$lines` rather than returning text so a gate record and a cleanup log can
# render the same facts without either owning the other's formatting.
function Add-InstallationIdentity {
    param($State)
    if ($null -eq $State) {
        $lines.Add("  installation identity: absent")
        return
    }
    $lines.Add("  service command line: $($State.ServiceCommandLine)")
    $lines.Add("  derived install root: $($State.Root)")
    $lines.Add("  layout version:       $($State.LayoutVersion)")
    $lines.Add("  installation id:      $($State.InstallationID)")
    $lines.Add("  daemon data dir:      $($State.DaemonDataDirectory)")
    $lines.Add("  uninstall matches:    $(@($State.UninstallMatches).Count)")
}

function Format-UninstallEntries {
    param([Parameter(Mandatory = $true)]$Entries)
    $lines = New-Object System.Collections.Generic.List[string]
    if (@($Entries).Count -eq 0) {
        $lines.Add("  (none)")
        return $lines
    }
    $index = 0
    foreach ($item in @($Entries)) {
        $index++
        $entry = $item.Entry
        if ($null -eq $entry) { $entry = $item }
        $lines.Add("  [$index] PSPath           $($entry.PSPath)")
        $lines.Add("      DisplayName        $($entry.DisplayName)")
        $lines.Add("      DisplayVersion     $($entry.DisplayVersion)")
        $lines.Add("      Publisher          $($entry.Publisher)")
        $lines.Add("      InstallLocation    $($entry.InstallLocation)")
        $lines.Add("      UninstallString    $($entry.UninstallString)")
        $lines.Add("      QuietUninstall     $($entry.QuietUninstallString)")
        if ($null -ne $item.MatchMethod) { $lines.Add("      matched by         $($item.MatchMethod)") }
    }
    return $lines
}
