# GATE: installs the real installer onto a machine that ALREADY HAS the previous release line's
# application data, and proves it adopts that data instead of refusing to install.
#
# Usage:
#   Test-InstallerLegacyAdoption.ps1 -InstallerPath <...\Jiejiebox-v0.1.16-windows-x64.exe> `
#                                    -ExpectedVersion 0.1.16 -RecordPath <file>
#
# # Why this gate exists
#
# Every other gate runs on an EMPTY machine, so the application data directory is created fresh and
# the one decision that matters most to a real user is never exercised. v0.1.16 shipped a defect
# only a machine with a PREVIOUS INSTALLATION can reach:
#
#     The application data directory must be empty.        -> Exit-Installer 25
#
# The installer read only its own layout key, believed it was on a clean machine, defaulted the
# application data directory to the same path the previous release line had used - both clients
# derive it from APP_FILENAME - found that older client's profiles already in it, found no marker
# of its own, and aborted. The user was told a directory holding their own data "must be empty",
# with no way forward but deleting it by hand.
#
# The lifecycle gate cannot catch that: on its runner the directory does not exist yet. This gate
# manufactures the shape and asserts what the user cares about - the install succeeds, and their
# data is still there afterwards.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$InstallerPath,
    [Parameter(Mandatory = $true)][string]$ExpectedVersion,
    [Parameter(Mandatory = $true)][string]$RecordPath,
    [int]$OperationTimeoutSeconds = 900
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "GateHelpers.ps1")
# Native commands here are expected to exit non-zero in normal operation (`sc.exe query` on an
# absent service returns 1060), so a non-zero exit must not become a terminating error. Each call
# checks $LASTEXITCODE where the result matters.
$PSNativeCommandUseErrorActionPreference = $false

$serviceName = "jiejiebox-daemon"
$installationLayoutKey = "HKLM:\SOFTWARE\Jiejiebox\jiejiebox"
$legacyLayoutKey = "HKLM:\SOFTWARE\SagerNet\sing-box"
$fixtureName = "sing-box-legacy-adoption-gate"
$fixtureRoot = Join-Path $env:ProgramData $fixtureName
$expectedRoot = "C:\Program Files\Jiejiebox\sing-box"

$lines = New-Object System.Collections.Generic.List[string]
$lines.Add("windows installer legacy-adoption test")
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

function Fail-Gate([string]$Message) {
    $lines.Add("")
    $lines.Add("FAIL: $Message")
    Save-Record
    foreach ($line in $lines) { Write-Host $line }
    Write-Error "legacy adoption gate failed: $Message"
    exit 1
}

# Reads the directory's own marker stream. `Get-Item -Stream` needs the directory itself, and a
# missing stream is the normal case here rather than an error.
function Get-DirectoryMarker([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path)) { return $null }
    $streams = @(Get-Item -LiteralPath $Path -Stream * -ErrorAction SilentlyContinue)
    $marker = $streams | Where-Object { $_.Stream -eq "jiejiebox.installation-id" } | Select-Object -First 1
    if ($null -eq $marker) { return $null }
    $value = Get-Content -LiteralPath "$Path`:jiejiebox.installation-id" -Raw -ErrorAction SilentlyContinue
    if ([string]::IsNullOrWhiteSpace($value)) { return "" }
    return $value.Trim()
}

function Get-ServiceStatus {
    $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
    if ($null -eq $service) { return "absent" }
    try { return $service.Status.ToString() } finally { $service.Dispose() }
}

# --- preconditions ------------------------------------------------------------------------
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail-Gate "this gate installs per-machine and needs an elevated process (running as $($identity.Name))"
}
if (-not (Test-Path -LiteralPath $InstallerPath -PathType Leaf)) {
    Fail-Gate "the installer does not exist: $InstallerPath"
}

# The installer runs its own PowerShell 5.1 children; see Test-InstallerLifecycle.ps1 for why the
# module path is rebuilt from the 5.1 locations rather than appended to.
$windowsPowerShellModules = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\Modules"
if (-not (Test-Path -LiteralPath $windowsPowerShellModules)) {
    Fail-Gate "the Windows PowerShell 5.1 module directory is missing: $windowsPowerShellModules"
}
$env:PSModulePath = @(
    $windowsPowerShellModules,
    (Join-Path $env:ProgramFiles "WindowsPowerShell\Modules"),
    (Join-Path $env:USERPROFILE "Documents\WindowsPowerShell\Modules")
) -join ";"

# Preserve and restore the real machine's legacy record: this gate rewrites it, and leaving the
# runner's own values replaced would make the NEXT run's precondition meaningless.
$legacyExisted = Test-Path -LiteralPath $legacyLayoutKey
$legacyBackup = @{}
if ($legacyExisted) {
    foreach ($name in (Get-Item -LiteralPath $legacyLayoutKey).Property) {
        $legacyBackup[$name] = (Get-ItemProperty -LiteralPath $legacyLayoutKey -Name $name).$name
    }
}

try {
    $lines.Add("precondition")
    $installedService = Get-ServiceStatus
    $forkLayout = Get-ItemProperty -LiteralPath $installationLayoutKey -ErrorAction SilentlyContinue
    $lines.Add("  service $serviceName : $installedService")
    $lines.Add("  fork layout key      : $(if ($null -ne $forkLayout) { 'PRESENT' } else { 'absent' })")
    if ($installedService -ne "absent" -or $null -ne $forkLayout) {
        Fail-Gate "this gate needs a machine with no $serviceName service and no fork layout key; it manufactures the PREVIOUS release line's shape itself"
    }

    # --- the shape a real user has ---------------------------------------------------------
    $lines.Add("")
    $lines.Add("fixture: the previous release line's application data")
    if (Test-Path -LiteralPath $fixtureRoot) { Remove-Item -LiteralPath $fixtureRoot -Recurse -Force }
    New-Item -ItemType Directory -Path (Join-Path $fixtureRoot "profiles") -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $fixtureRoot "settings.db") -Value "legacy-settings" -NoNewline
    Set-Content -LiteralPath (Join-Path $fixtureRoot "profiles\legacy-profile.json") -Value '{"legacy":true}' -NoNewline

    $markerBefore = Get-DirectoryMarker $fixtureRoot
    $lines.Add("  directory    : $fixtureRoot")
    $lines.Add("  files        : $((Get-ChildItem -LiteralPath $fixtureRoot -Recurse -Force).Count)")
    $lines.Add("  our marker   : $(if ($null -eq $markerBefore) { 'ABSENT - this is what made the shipped installer refuse' } else { $markerBefore })")
    if ($null -ne $markerBefore) { Fail-Gate "the fixture directory unexpectedly carries this installation's marker" }

    New-Item -Path $legacyLayoutKey -Force | Out-Null
    New-ItemProperty -LiteralPath $legacyLayoutKey -Name "LayoutVersion" -Value 2 -PropertyType DWord -Force | Out-Null
    New-ItemProperty -LiteralPath $legacyLayoutKey -Name "InstallationID" -Value "{11111111-1111-1111-1111-111111111111}" -PropertyType String -Force | Out-Null
    New-ItemProperty -LiteralPath $legacyLayoutKey -Name "ApplicationDataDirectory" -Value $fixtureRoot -PropertyType String -Force | Out-Null
    New-ItemProperty -LiteralPath $legacyLayoutKey -Name "DaemonDataDirectory" -Value (Join-Path $env:ProgramData "jiejiebox-daemon") -PropertyType String -Force | Out-Null
    $lines.Add("  legacy record: $legacyLayoutKey names $fixtureRoot")

    # --- the install -----------------------------------------------------------------------
    $lines.Add("")
    $lines.Add("[1/3] silent install onto a machine that already has the previous line's data")
    $process = Start-Process -FilePath $InstallerPath -ArgumentList @("/S") -PassThru
    $null = $process | Wait-Process -Timeout $OperationTimeoutSeconds -ErrorAction SilentlyContinue
    if (-not $process.HasExited) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        Fail-Gate "the installer did not finish within $OperationTimeoutSeconds seconds"
    }
    $lines.Add("  exit code $($process.ExitCode)")
    if ($process.ExitCode -ne 0) {
        Fail-Gate "the installer exited $($process.ExitCode). Exit 25 is 'The application data directory must be empty.' - the previous release line's data was refused instead of adopted."
    }

    # --- the assertions --------------------------------------------------------------------
    $lines.Add("")
    $lines.Add("[2/3] the installer reached the installed state")
    $root = Get-InstallationRootFromDaemonPath (Get-CommandExecutablePath (Get-DaemonServiceCommandLine -ServiceName $serviceName))
    $lines.Add("  installation root : $(if ([string]::IsNullOrWhiteSpace($root)) { 'NOT IDENTIFIED' } else { $root })")
    if ([string]::IsNullOrWhiteSpace($root)) { Fail-Gate "no installation could be identified from the SCM after a successful install" }
    foreach ($relative in @("sing-box.exe", "resources\daemon\sing-box-daemon.exe")) {
        $path = Join-Path $root $relative
        $present = Test-Path -LiteralPath $path -PathType Leaf
        $lines.Add("  $relative : $(if ($present) { 'present' } else { 'MISSING' })")
        if (-not $present) { Fail-Gate "$relative is missing at $path" }
    }
    $version = Invoke-NativeCommand { & (Join-Path $root "resources\daemon\sing-box-daemon.exe") version }
    $lines.Add("  daemon version    : $($version.Output.Trim()) (exit $($version.ExitCode))")
    if ($version.Output -notmatch [regex]::Escape("version $ExpectedVersion")) {
        Fail-Gate "the installed daemon reports '$($version.Output.Trim())', expected version $ExpectedVersion"
    }

    $lines.Add("")
    $lines.Add("[3/3] the user's data survived and the directory was re-owned")
    foreach ($relative in @("settings.db", "profiles\legacy-profile.json")) {
        $present = Test-Path -LiteralPath (Join-Path $fixtureRoot $relative)
        $lines.Add("  $relative : $(if ($present) { 'present' } else { 'MISSING' })")
        if (-not $present) { Fail-Gate "$relative did not survive the adoption" }
    }
    $markerAfter = Get-DirectoryMarker $fixtureRoot
    $lines.Add("  our marker        : $(if ([string]::IsNullOrWhiteSpace($markerAfter)) { 'ABSENT' } else { $markerAfter })")
    if ([string]::IsNullOrWhiteSpace($markerAfter)) {
        Fail-Gate "the directory was not re-owned: this installation wrote no marker, so the NEXT upgrade would refuse the directory all over again"
    }
    $layout = Get-ItemProperty -LiteralPath $installationLayoutKey -ErrorAction SilentlyContinue
    if ($null -eq $layout) { Fail-Gate "the fork's own layout key was not written" }
    $lines.Add("  fork layout       : LayoutVersion $($layout.LayoutVersion) InstallationID $($layout.InstallationID)")
    $lines.Add("  layout names      : $($layout.ApplicationDataDirectory)")
    if ((Get-NormalizedPath $layout.ApplicationDataDirectory) -ne (Get-NormalizedPath $fixtureRoot)) {
        Fail-Gate "the recorded layout names '$($layout.ApplicationDataDirectory)', not the adopted directory '$fixtureRoot'"
    }
    $lines.Add("  PASS")

    $lines.Add("")
    $lines.Add("PASS: the installer adopted the previous release line's application data directory,")
    $lines.Add("      the user's files are still there, and the directory now belongs to this installation")
    Save-Record
    foreach ($line in $lines) { Write-Host $line }
} finally {
    $lines.Add("")
    $lines.Add("teardown")
    $uninstaller = Join-Path $expectedRoot "Uninstall sing-box.exe"
    if (Test-Path -LiteralPath $uninstaller) {
        $p = Start-Process -FilePath $uninstaller -ArgumentList @("/S", "/allusers", "--delete-app-data") -PassThru
        $null = $p | Wait-Process -Timeout 600 -ErrorAction SilentlyContinue
        $lines.Add("  uninstaller exit $($p.ExitCode)")
    } else {
        $lines.Add("  no uninstaller at $uninstaller")
    }
    if (Test-Path -LiteralPath $fixtureRoot) {
        Remove-Item -LiteralPath $fixtureRoot -Recurse -Force -ErrorAction SilentlyContinue
        $lines.Add("  fixture directory removed: $fixtureRoot")
    }
    foreach ($name in @("LayoutVersion", "InstallationID", "ApplicationDataDirectory", "DaemonDataDirectory")) {
        Remove-ItemProperty -LiteralPath $legacyLayoutKey -Name $name -ErrorAction SilentlyContinue
    }
    if (-not $legacyExisted) {
        Remove-Item -LiteralPath $legacyLayoutKey -Recurse -Force -ErrorAction SilentlyContinue
    } else {
        foreach ($name in $legacyBackup.Keys) {
            New-ItemProperty -LiteralPath $legacyLayoutKey -Name $name -Value $legacyBackup[$name] -Force | Out-Null
        }
    }
    $lines.Add("  legacy record restored")
    Save-Record
}

Complete-Gate
