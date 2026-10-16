# GATE: a real coexistence smoke test.
#
#     official sing-box installed and running
#       -> install Jiejiebox
#       -> both daemons healthy
#       -> uninstall Jiejiebox
#       -> official sing-box still healthy
#
# # Why this gate exists
#
# Every other gate runs on an EMPTY machine, so "there is another sing-box client here already" -
# the ordinary state of a real user's machine - is never constructed. That is exactly how a
# regression shipped in which installing OR uninstalling this fork DELETED the official client's
# `sing-box-daemon` service: the ownership proof could not tell the two clients apart, because both
# write the same marker, the same layout registry key and the same data layout.
#
# The official client is really installed here, from its real published installer. Nothing is faked
# with a hand-made service, because a hand-made service would not reproduce the state the cleanup
# reads (a real image path under a real installation tree, with the real product name in its
# version resources).
#
# # What this gate does NOT do
#
# It is deliberately a smoke test: no per-file hashes, no ACL comparison, no profile comparison, no
# reverse direction (Jiejiebox first, then the official installer), no `$INSTDIR` change, no
# registry cosmetic cleanup. Those are separate decisions.
#
# # INFRA vs PRODUCT
#
# The official installer is downloaded from GitHub at run time, and a download or a network hiccup
# is NOT a product regression. Anything that prevents the gate from being able to judge the product
# is reported as `RESULT = INFRA_DOWNLOAD_FAILURE` with its own exit code, and is kept visually
# distinct from a product failure so it can never be cited as one.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$InstallerPath,
    [Parameter(Mandatory = $true)][string]$ExpectedVersion,
    [Parameter(Mandatory = $true)][string]$RecordPath,
    # Pinned so the gate is repeatable. A stable release, not a pre-release: the gate is about the
    # ordinary case.
    [string]$OfficialVersion = "1.14.3",
    [string]$OfficialInstallerUrl = "",
    # The pinned installer's SHA-256. Checked, not optional: this is a 100+ MB download over a
    # network that has been measured truncating it, and a truncated file still starts, still
    # reports the right ProductName, and then fails to install - which reads as "the official
    # client does not install" unless the hash says otherwise. A size floor is not enough: the
    # observed truncation delivered 20.6 MB of 114 MB, comfortably over any floor worth setting.
    [string]$OfficialInstallerSha256 = "",
    [int]$OperationTimeoutSeconds = 900
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "GateHelpers.ps1")
$PSNativeCommandUseErrorActionPreference = $false

# Exit codes. 1 is a product failure - the gate ran and the product was wrong. 44 is the gate being
# unable to judge, which must never be read as a product result.
$exitProduct = 1
$exitInfra = 44

$officialService = "sing-box-daemon"
$forkService = "jiejiebox-daemon"
$officialRoot = "C:\Program Files\sing-box"
if ([string]::IsNullOrWhiteSpace($OfficialInstallerUrl)) {
    $OfficialInstallerUrl = "https://github.com/SagerNet/sing-box/releases/download/v$OfficialVersion/SFW-$OfficialVersion-x64.exe"
}
if ([string]::IsNullOrWhiteSpace($OfficialInstallerSha256)) {
    # Measured from the real published asset. Only defined for the pinned default version, so a
    # caller that overrides the version must supply its own hash rather than silently skipping the
    # check.
    if ($OfficialVersion -eq "1.14.3") {
        $OfficialInstallerSha256 = "ba65f354f9096ff31b612ce6784abce417ab3628faa06e8b47c0203cc045c731"
    }
}

$lines = New-Object System.Collections.Generic.List[string]
$lines.Add("windows coexistence smoke gate")
$lines.Add("")
$lines.Add("official version      : $OfficialVersion")
$lines.Add("official installer    : $OfficialInstallerUrl")
$lines.Add("Jiejiebox installer   : $InstallerPath")
$lines.Add("Jiejiebox version     : $ExpectedVersion")
$lines.Add("")

function Save-Record {
    $directory = Split-Path -Parent $RecordPath
    if (-not [string]::IsNullOrWhiteSpace($directory) -and -not (Test-Path -LiteralPath $directory)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    [System.IO.File]::WriteAllLines($RecordPath, $lines)
}

function Write-Record {
    Save-Record
    foreach ($line in $lines) { Write-Host $line }
}

# The gate could not be brought to the point of judging the product. Never a product result.
function Fail-Infra([string]$Message) {
    $lines.Add("")
    $lines.Add("INFRA: $Message")
    $lines.Add("")
    $lines.Add("RESULT = INFRA_DOWNLOAD_FAILURE")
    $lines.Add("This is NOT a product regression. The gate could not install or start the official")
    $lines.Add("client, so it never got to judge whether Jiejiebox coexists with it.")
    Write-Record
    Write-Host "::warning::coexistence gate: INFRA_DOWNLOAD_FAILURE - $Message"
    exit $exitInfra
}

# The gate ran and the product was wrong.
function Fail-Product([string]$Message) {
    $lines.Add("")
    $lines.Add("FAIL: $Message")
    $lines.Add("")
    $lines.Add("RESULT = PRODUCT_FAILURE")
    Write-Record
    Write-Host "::error::coexistence gate: PRODUCT_FAILURE - $Message"
    exit $exitProduct
}

function Get-ServiceState([string]$Name) {
    $service = Get-Service -Name $Name -ErrorAction SilentlyContinue
    if ($null -eq $service) { return "absent" }
    try { return $service.Status.ToString() } finally { $service.Dispose() }
}

function Get-SagerNetUninstallEntry {
    return @(Get-UninstallRegistryEntries | Where-Object {
        $_.DisplayName -match 'sing-box' -and $_.Publisher -eq 'SagerNet'
    }) | Select-Object -First 1
}

function Get-ForkUninstallEntry {
    return @(Get-UninstallRegistryEntries | Where-Object {
        $_.DisplayName -match 'Jiejiebox'
    }) | Select-Object -First 1
}

# The installer APPENDS to the diagnostic file, so reading it whole after every phase repeats
# everything and buries the phase that just ran. Only what is new since the last read is reported.
$diagnosticLinesRead = 0
function Read-NewDiagnostic {
    param([System.Collections.Generic.List[string]]$Into)
    if (-not (Test-Path -LiteralPath $diagnosticPath)) { return }
    $all = @(Get-Content -LiteralPath $diagnosticPath -ErrorAction SilentlyContinue)
    if ($all.Count -le $script:diagnosticLinesRead) { return }
    for ($i = $script:diagnosticLinesRead; $i -lt $all.Count; $i++) {
        $Into.Add("  diag: $($all[$i])")
    }
    $script:diagnosticLinesRead = $all.Count
}

# --- the machine must be able to run the scenario at all ------------------------------------
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail-Infra "this gate installs per-machine and needs an elevated process (running as $($identity.Name))"
}
if (-not (Test-Path -LiteralPath $InstallerPath -PathType Leaf)) {
    Fail-Infra "the Jiejiebox installer to test does not exist: $InstallerPath"
}

$windowsPowerShellModules = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\Modules"
if (-not (Test-Path -LiteralPath $windowsPowerShellModules)) {
    Fail-Infra "the Windows PowerShell 5.1 module directory is missing: $windowsPowerShellModules"
}
$env:PSModulePath = @(
    $windowsPowerShellModules,
    (Join-Path $env:ProgramFiles "WindowsPowerShell\Modules"),
    (Join-Path $env:USERPROFILE "Documents\WindowsPowerShell\Modules")
) -join ";"

$officialInstaller = Join-Path $env:TEMP "SFW-$OfficialVersion-x64.exe"
$workspace = Join-Path $env:TEMP "coexistence-smoke"
New-Item -ItemType Directory -Path $workspace -Force | Out-Null
$diagnosticPath = Join-Path $workspace "installer-diagnostic.txt"

try {
    # --- precondition: nothing of OURS is installed ------------------------------------------
    $existingFork = Get-ForkUninstallEntry
    $lines.Add("precondition")
    $lines.Add("  $forkService : $(Get-ServiceState $forkService)")
    $lines.Add("  Jiejiebox uninstall entry : $(if ($null -ne $existingFork) { $existingFork.DisplayName } else { 'absent' })")
    if ((Get-ServiceState $forkService) -ne "absent" -or $null -ne $existingFork) {
        Fail-Infra "this gate needs a machine with no Jiejiebox installation; it installs the official client itself and then Jiejiebox, and a pre-existing Jiejiebox would make the uninstall phase ambiguous"
    }

    # --- 1. the official client, really installed ---------------------------------------------
    $lines.Add("")
    $lines.Add("[1/4] install the official sing-box client")

    # If the installer is already on the runner (a cached copy, or a caller that pre-fetched it),
    # use it. That also makes the INFRA path testable without breaking the network on purpose.
    $downloadNeeded = -not ((Test-Path -LiteralPath $officialInstaller -PathType Leaf) -and
        (Get-Item -LiteralPath $officialInstaller).Length -gt 10MB)
    if ($downloadNeeded) {
        $lines.Add("  source     $OfficialInstallerUrl")
        # `curl.exe` failing is expected here - a download really can fail - so it must not be able
        # to end the script. A native command that writes to stderr becomes a TERMINATING error
        # while $ErrorActionPreference is "Stop", which would exit 1: identical to a product
        # failure, and exactly the confusion the INFRA/PRODUCT split exists to prevent. This is how
        # that was found - the first local run reported PRODUCT_FAILURE for a network outage.
        #
        # Retried until the hash matches, because a truncated transfer is a routine event on this
        # network and it produces a file that looks plausible: right product name, over any size
        # floor, and then it fails to install.
        $attempts = 3
        for ($attempt = 1; $attempt -le $attempts; $attempt++) {
            $lines.Add("  attempt $attempt/$attempts")
            $previousPreference = $ErrorActionPreference
            $ErrorActionPreference = "Continue"
            try {
                & curl.exe -sSL --retry 2 --retry-delay 5 --max-time 900 -o $officialInstaller $OfficialInstallerUrl 2>&1 |
                    ForEach-Object { $lines.Add("    curl: $_") }
            } catch {
                $lines.Add("    curl threw: $($_.Exception.Message)")
            } finally {
                $ErrorActionPreference = $previousPreference
            }
            if ((Test-Path -LiteralPath $officialInstaller -PathType Leaf) -and
                (Get-Item -LiteralPath $officialInstaller).Length -gt 10MB) {
                $got = (Get-FileHash -LiteralPath $officialInstaller -Algorithm SHA256).Hash.ToLower()
                if ([string]::IsNullOrWhiteSpace($OfficialInstallerSha256) -or $got -eq $OfficialInstallerSha256.ToLower()) {
                    break
                }
                $lines.Add("    hash mismatch: got $got")
                Remove-Item -LiteralPath $officialInstaller -Force -ErrorAction SilentlyContinue
            } else {
                $size = if (Test-Path -LiteralPath $officialInstaller) { (Get-Item -LiteralPath $officialInstaller).Length } else { 0 }
                $lines.Add("    incomplete: $size bytes")
                Remove-Item -LiteralPath $officialInstaller -Force -ErrorAction SilentlyContinue
            }
        }
    } else {
        $lines.Add("  source     pre-existing $officialInstaller (download skipped)")
    }
    if (-not (Test-Path -LiteralPath $officialInstaller -PathType Leaf) -or
        (Get-Item -LiteralPath $officialInstaller).Length -lt 10MB) {
        Fail-Infra "the official installer could not be downloaded from $OfficialInstallerUrl (see the curl output above for the transport error)"
    }
    $officialHash = (Get-FileHash -LiteralPath $officialInstaller -Algorithm SHA256).Hash.ToLower()
    if ([string]::IsNullOrWhiteSpace($OfficialInstallerSha256)) {
        Fail-Infra "no pinned SHA-256 was supplied for the official installer, so a truncated download cannot be told from a real one; pass -OfficialInstallerSha256"
    }
    if ($officialHash -ne $OfficialInstallerSha256.ToLower()) {
        Fail-Infra "the downloaded official installer has sha256 $officialHash, expected $OfficialInstallerSha256 - the transfer was not complete or the asset changed"
    }
    $lines.Add("  downloaded $((Get-Item -LiteralPath $officialInstaller).Length) bytes")
    $lines.Add("  sha256     $officialHash (matches the pinned value)")

    $officialProduct = (Get-Item -LiteralPath $officialInstaller).VersionInfo.ProductName
    $lines.Add("  product    $officialProduct")
    if ($officialProduct -ne "sing-box") {
        Fail-Infra "the downloaded file does not identify itself as the official sing-box client (ProductName='$officialProduct')"
    }

    $install = Start-Process -FilePath $officialInstaller -ArgumentList @("/S") -PassThru
    $null = $install | Wait-Process -Timeout $OperationTimeoutSeconds -ErrorAction SilentlyContinue
    if (-not $install.HasExited) {
        Stop-Process -Id $install.Id -Force -ErrorAction SilentlyContinue
        Fail-Infra "the official installer did not finish within $OperationTimeoutSeconds seconds"
    }
    $lines.Add("  installer exit $($install.ExitCode)")
    if ($install.ExitCode -ne 0) {
        Fail-Infra "the official installer exited $($install.ExitCode); the gate cannot set up the scenario"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $officialRoot "sing-box.exe") -PathType Leaf)) {
        Fail-Infra "the official installer reported success but $officialRoot\sing-box.exe is not there"
    }

    # The daemon service is what the cleanup under test acts on, so the scenario is not set up
    # until it is registered AND running. Waiting rather than failing immediately: the service is
    # registered by the client, which may still be starting.
    $registered = Wait-Until -Description "the official daemon service to register" -TimeoutSeconds 120 -Condition {
        (Get-ServiceState $officialService) -ne "absent"
    }
    if (-not $registered) {
        Fail-Infra "the official client installed but registered no '$officialService' service, so the scenario the gate exists to build was never built"
    }
    $running = Wait-Until -Description "the official daemon service to run" -TimeoutSeconds 120 -Condition {
        (Get-ServiceState $officialService) -eq "Running"
    }
    if (-not $running) {
        Fail-Infra "the official '$officialService' service is registered but did not reach Running (state $(Get-ServiceState $officialService))"
    }

    # --- 2. the assertions BEFORE our install -------------------------------------------------
    $lines.Add("")
    $lines.Add("[2/4] BEFORE: the official client is healthy")
    $officialEntryBefore = Get-SagerNetUninstallEntry
    $lines.Add("  $officialService exists  : $((Get-ServiceState $officialService) -ne 'absent')")
    $lines.Add("  $officialService         : $(Get-ServiceState $officialService)")
    $lines.Add("  official uninstall entry : $(if ($null -ne $officialEntryBefore) { $officialEntryBefore.DisplayName } else { 'ABSENT' })")
    if ($null -eq $officialEntryBefore) {
        Fail-Infra "the official client has no uninstall entry, so the gate could not tell whether it survived"
    }
    if ((Get-ServiceState $officialService) -ne "Running") {
        Fail-Product "the official '$officialService' service is not Running before Jiejiebox is installed"
    }
    $lines.Add("  PASS")

    # --- 3. install Jiejiebox -----------------------------------------------------------------
    $lines.Add("")
    $lines.Add("[3/4] install Jiejiebox beside it")
    # Both clients write to the SAME diagnostic file and append, so it is cleared per phase: the
    # record then shows what THIS phase did rather than every phase that came before it.
    Remove-Item -LiteralPath $diagnosticPath -Force -ErrorAction SilentlyContinue
    $script:diagnosticLinesRead = 0
    $forkInstall = Start-Process -FilePath $InstallerPath `
        -ArgumentList @("/S", "/CI-DIAGNOSTIC-PATH=$diagnosticPath", "/") -PassThru
    $null = $forkInstall | Wait-Process -Timeout $OperationTimeoutSeconds -ErrorAction SilentlyContinue
    if (-not $forkInstall.HasExited) {
        Stop-Process -Id $forkInstall.Id -Force -ErrorAction SilentlyContinue
        Fail-Product "the Jiejiebox installer did not finish within $OperationTimeoutSeconds seconds"
    }
    $lines.Add("  installer exit $($forkInstall.ExitCode)")
    Read-NewDiagnostic -Into $lines
    if ($forkInstall.ExitCode -ne 0) {
        Fail-Product "the Jiejiebox installer exited $($forkInstall.ExitCode)"
    }

    $lines.Add("")
    $lines.Add("  AFTER INSTALL:")
    $lines.Add("    $officialService : $(Get-ServiceState $officialService)")
    $lines.Add("    $forkService : $(Get-ServiceState $forkService)")
    $officialEntryAfterInstall = Get-SagerNetUninstallEntry
    $lines.Add("    official uninstall entry : $(if ($null -ne $officialEntryAfterInstall) { $officialEntryAfterInstall.DisplayName } else { 'ABSENT' })")

    # THE REGRESSION THIS GATE EXISTS FOR: the fork's install removed the official service.
    if ((Get-ServiceState $officialService) -eq "absent") {
        Fail-Product "installing Jiejiebox REMOVED the official '$officialService' service. This is the regression this gate exists for."
    }
    if ((Get-ServiceState $officialService) -ne "Running") {
        Fail-Product "after installing Jiejiebox the official '$officialService' service is $(Get-ServiceState $officialService), not Running"
    }
    if ($null -eq $officialEntryAfterInstall) {
        Fail-Product "installing Jiejiebox removed the official client's uninstall entry"
    }
    $forkRunning = Wait-Until -Description "the Jiejiebox daemon to run" -TimeoutSeconds 180 -Condition {
        (Get-ServiceState $forkService) -eq "Running"
    }
    if (-not $forkRunning) {
        Fail-Product "the Jiejiebox '$forkService' service did not reach Running (state $(Get-ServiceState $forkService))"
    }
    if ((Get-ServiceState $officialService) -ne "Running") {
        Fail-Product "the official '$officialService' service stopped while Jiejiebox was starting"
    }
    $lines.Add("  PASS: both daemons Running")

    # --- 4. uninstall Jiejiebox ---------------------------------------------------------------
    $lines.Add("")
    $lines.Add("[4/4] uninstall Jiejiebox; the official client must be untouched")
    $forkEntry = Get-ForkUninstallEntry
    if ($null -eq $forkEntry) {
        Fail-Product "Jiejiebox installed but registered no uninstall entry, so it cannot be removed"
    }
    $uninstaller = Get-CommandExecutablePath ([string]$forkEntry.UninstallString)
    if ([string]::IsNullOrWhiteSpace($uninstaller) -or -not (Test-Path -LiteralPath $uninstaller -PathType Leaf)) {
        Fail-Product "the Jiejiebox uninstall entry points at no runnable uninstaller ('$($forkEntry.UninstallString)')"
    }
    $forkRoot = Split-Path -Parent $uninstaller
    $lines.Add("  uninstaller $uninstaller")

    # The uninstaller must be run IN PLACE (`_?=<dir>`): run normally it copies itself to %TEMP%,
    # and the copy cannot see the installation. The trailing `/` terminates the diagnostic option,
    # because ${GetOptions} in NSIS reads to end-of-line or the next `/` - and `_?=` starts with an
    # underscore, not a slash, so without it the two options are read as one value.
    Remove-Item -LiteralPath $diagnosticPath -Force -ErrorAction SilentlyContinue
    $script:diagnosticLinesRead = 0
    $uninstall = Start-Process -FilePath $uninstaller `
        -ArgumentList @("/S", "/allusers", "/CI-DIAGNOSTIC-PATH=$diagnosticPath", "/", "_?=$forkRoot") -PassThru
    $null = $uninstall | Wait-Process -Timeout $OperationTimeoutSeconds -ErrorAction SilentlyContinue
    if (-not $uninstall.HasExited) {
        Stop-Process -Id $uninstall.Id -Force -ErrorAction SilentlyContinue
        Fail-Product "the Jiejiebox uninstaller did not finish within $OperationTimeoutSeconds seconds"
    }
    $lines.Add("  uninstaller exit $($uninstall.ExitCode)")
    if ($uninstall.ExitCode -ne 0) {
        Fail-Product "the Jiejiebox uninstaller exited $($uninstall.ExitCode)"
    }
    Read-NewDiagnostic -Into $lines

    # An NSIS uninstaller relaunches itself from %TEMP%, so its exit code arrives before its work
    # does. Wait for the effect rather than reading the machine too early - reading immediately is
    # what made an earlier investigation report a working uninstaller as broken.
    $gone = Wait-Until -Description "the Jiejiebox daemon service to be removed" -TimeoutSeconds 300 -Condition {
        (Get-ServiceState $forkService) -eq "absent"
    }
    if (-not $gone) {
        Fail-Product "the Jiejiebox uninstaller reported success but '$forkService' is still $(Get-ServiceState $forkService)"
    }
    $entryGone = Wait-Until -Description "the Jiejiebox uninstall entry to be removed" -TimeoutSeconds 120 -Condition {
        $null -eq (Get-ForkUninstallEntry)
    }
    if (-not $entryGone) {
        Fail-Product "the Jiejiebox uninstall entry is still present after its uninstaller reported success"
    }

    $lines.Add("")
    $lines.Add("  AFTER UNINSTALL:")
    $lines.Add("    $officialService : $(Get-ServiceState $officialService)")
    $lines.Add("    $forkService : $(Get-ServiceState $forkService)")
    $officialEntryAfterUninstall = Get-SagerNetUninstallEntry
    $forkEntryAfterUninstall = Get-ForkUninstallEntry
    $lines.Add("    official uninstall entry  : $(if ($null -ne $officialEntryAfterUninstall) { $officialEntryAfterUninstall.DisplayName } else { 'ABSENT' })")
    $lines.Add("    Jiejiebox uninstall entry : $(if ($null -ne $forkEntryAfterUninstall) { $forkEntryAfterUninstall.DisplayName } else { 'absent' })")

    # THE OTHER HALF OF THE REGRESSION: the fork's uninstall removed the official service.
    if ((Get-ServiceState $officialService) -eq "absent") {
        Fail-Product "uninstalling Jiejiebox REMOVED the official '$officialService' service. This is the regression this gate exists for."
    }
    if ((Get-ServiceState $officialService) -ne "Running") {
        Fail-Product "after uninstalling Jiejiebox the official '$officialService' service is $(Get-ServiceState $officialService), not Running"
    }
    if ($null -eq $officialEntryAfterUninstall) {
        Fail-Product "uninstalling Jiejiebox removed the official client's uninstall entry"
    }
    if ($null -ne $forkEntryAfterUninstall) {
        Fail-Product "the Jiejiebox uninstall entry survived its own uninstall"
    }
    $lines.Add("  PASS")

    # --- the acceptance record -----------------------------------------------------------------
    $installedVersion = $ExpectedVersion
    # Read from the FORK's own layout key. The two clients record their layout under different
    # keys, and reading the upstream client's key would report the other product's installation
    # as this one's - which is the confusion this whole gate is about.
    $forkLayout = Get-InstallationLayoutRegistry -RegistryPath $script:JiejieboxInstallationLayoutKey
    if ($null -ne $forkLayout -and -not [string]::IsNullOrWhiteSpace($forkLayout.InstallationID)) {
        $lines.Add("")
        $lines.Add("  (Jiejiebox installation id was $($forkLayout.InstallationID))")
    }

    $lines.Add("")
    $lines.Add("===== ACCEPTANCE =====")
    $lines.Add("official version:          $OfficialVersion")
    $lines.Add("official installer source: $OfficialInstallerUrl")
    $lines.Add("official installer sha256: $officialHash")
    $lines.Add("Jiejiebox commit:          $env:GITHUB_SHA")
    $lines.Add("workflow:                  $env:GITHUB_WORKFLOW")
    $lines.Add("run id:                    $env:GITHUB_RUN_ID")
    $lines.Add("")
    $lines.Add("before:")
    $lines.Add("$officialService = Running")
    $lines.Add("")
    $lines.Add("after fork install:")
    $lines.Add("$officialService = Running")
    $lines.Add("$forkService = Running")
    $lines.Add("")
    $lines.Add("after fork uninstall:")
    $lines.Add("$officialService = Running")
    $lines.Add("$forkService = absent")
    $lines.Add("")
    $lines.Add("official uninstall entry = present")
    $lines.Add("Jiejiebox uninstall entry = absent")
    $lines.Add("")
    $lines.Add("RESULT = PASS")
    $lines.Add("")
    $lines.Add("PASS: the official client was healthy before, during and after this fork was")
    $lines.Add("      installed beside it and removed again.")
    Write-Record
    Write-Host "::notice::coexistence smoke gate: RESULT = PASS"
} finally {
    # Never leave either client behind for whatever runs next on this runner.
    $lines.Add("")
    $lines.Add("teardown")
    try {
        $forkEntryFinal = Get-ForkUninstallEntry
        if ($null -ne $forkEntryFinal) {
            $uninstallerFinal = Get-CommandExecutablePath ([string]$forkEntryFinal.UninstallString)
            if (-not [string]::IsNullOrWhiteSpace($uninstallerFinal) -and (Test-Path -LiteralPath $uninstallerFinal -PathType Leaf)) {
                $rootFinal = Split-Path -Parent $uninstallerFinal
                $p = Start-Process -FilePath $uninstallerFinal -ArgumentList @("/S", "/allusers", "_?=$rootFinal") -PassThru
                $null = $p | Wait-Process -Timeout 600 -ErrorAction SilentlyContinue
                $lines.Add("  removed Jiejiebox (exit $($p.ExitCode))")
            }
        }
    } catch {
        $lines.Add("  could not remove Jiejiebox: $($_.Exception.Message)")
    }
    try {
        $officialEntryFinal = Get-SagerNetUninstallEntry
        if ($null -ne $officialEntryFinal) {
            $officialUninstaller = Get-CommandExecutablePath ([string]$officialEntryFinal.UninstallString)
            if (-not [string]::IsNullOrWhiteSpace($officialUninstaller) -and (Test-Path -LiteralPath $officialUninstaller -PathType Leaf)) {
                $p = Start-Process -FilePath $officialUninstaller -ArgumentList @("/allusers", "/S") -PassThru
                $null = $p | Wait-Process -Timeout 600 -ErrorAction SilentlyContinue
                $lines.Add("  removed the official client (exit $($p.ExitCode))")
            }
        }
    } catch {
        $lines.Add("  could not remove the official client: $($_.Exception.Message)")
    }
    Start-Sleep -Seconds 20
    Remove-Item -LiteralPath $officialInstaller -Force -ErrorAction SilentlyContinue
    Save-Record
}

Complete-Gate
