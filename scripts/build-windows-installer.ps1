[CmdletBinding()]
param(
    [string]$RepoPath = (Split-Path -Parent $PSScriptRoot),
    [string]$InputExe = "",
    [string]$InputTrayExe = "",
    [string]$OutputDir = "",
    [string]$Version = "dev",
    [string]$ISCCPath = "",
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = [IO.Path]::GetFullPath($RepoPath)
$iss = Join-Path $repo "installer\windows\AHA2.iss"
$userTaskScript = Join-Path $repo "installer\windows\Register-AHA2UserTask.ps1"
$dataDirScript = Join-Path $repo "installer\windows\Prepare-AHA2DataDir.ps1"
if (-not (Test-Path -LiteralPath (Join-Path $repo "go.mod") -PathType Leaf)) {
    throw "RepoPath is not an AHA2 repository: $repo"
}
foreach ($requiredFile in @($iss, $userTaskScript, $dataDirScript)) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "Windows installer source is missing: $requiredFile"
    }
}
if ([string]::IsNullOrWhiteSpace($InputExe)) {
    $InputExe = Join-Path $repo "dist\aha2-windows-amd64.exe"
}
if ([string]::IsNullOrWhiteSpace($InputTrayExe)) {
    $InputTrayExe = Join-Path $repo "dist\aha2-tray-windows-amd64.exe"
}
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $repo "dist\installer"
}
$inputPath = [IO.Path]::GetFullPath($InputExe)
$trayInputPath = [IO.Path]::GetFullPath($InputTrayExe)
$outputPath = [IO.Path]::GetFullPath($OutputDir)
$normalizedVersion = $Version.Trim()
if ($normalizedVersion.StartsWith("v", [StringComparison]::OrdinalIgnoreCase)) {
    $normalizedVersion = $normalizedVersion.Substring(1)
}
if ([string]::IsNullOrWhiteSpace($normalizedVersion)) {
    throw "Installer version cannot be empty."
}

$source = Get-Content -Raw -Encoding UTF8 -LiteralPath $iss
$requiredContracts = @(
    '#define ServiceName "AHA2"',
    '#define ListenAddress "127.0.0.1:8766"',
    '#define SourceTrayExe',
    'DestName: "aha2.exe"',
    'DestName: "aha2-tray.exe"',
    '--server "'' + ExpandConstant(''{app}\aha2.exe'')',
    "CreateInputDirPage",
    "CreateInputOptionPage",
    "SelectedListenAddress",
    "RegisterPreviousData",
    "remoteip=localsubnet profile=private",
    "{code:LocalManagementURL}",
    "{commonappdata}\AHA2",
    "runasoriginaluser",
    "TrayParameters",
    "StopInstalledUserProcesses",
    "Get-Process -Name ''aha2-tray'',''aha2''",
    "DisableAndStopLegacyAHA2Service(True)",
    "RemoveLegacyAHA2Service()",
    "Register-AHA2UserTask.ps1",
    "Prepare-AHA2DataDir.ps1",
    "[InstallDelete]",
    'Type: files; Name: "{app}\Run-AHA2User.vbs"',
    "ConfigureAHA2UserTask",
    "AHA2UserTaskName",
    "RemoveAHA2UserTask",
    "ExecAsOriginalUser",
    "AgentAPIPage",
    "--agent-api-url",
    "--allow-insecure-agent-api",
    "WaitForAHA2Health",
    "LocalHealthURL",
    "http://127.0.0.1:' + SelectedPort()"
)
foreach ($contract in $requiredContracts) {
    if (-not $source.Contains($contract)) {
        throw "Installer contract is missing: $contract"
    }
}
$userTaskSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $userTaskScript
foreach ($contract in @("TrayExecutable", "ServerExecutable", "New-ScheduledTaskAction -Execute `$trayPath", "--server", "--listen", "--data-dir", "New-ScheduledTaskTrigger -AtLogOn", "WindowsIdentity]::GetCurrent().Name", "Win32_ComputerSystem", "OrdinalIgnoreCase", "-LogonType Interactive", "-RunLevel Limited", "-RestartCount 3", "-RestartInterval", "Register-ScheduledTask", "Start-ScheduledTask", "AllowInsecureAgentAPI")) {
    if (-not $userTaskSource.Contains($contract)) {
        throw "Windows tray login task contract is missing: $contract"
    }
}
foreach ($forbidden in @("wscript.exe", ".vbs", "powershell.exe", "service run", "SYSTEM")) {
    if ($userTaskSource.Contains($forbidden)) {
        throw "Windows login task still contains a forbidden long-running host contract: $forbidden"
    }
}
$dataDirSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $dataDirScript
foreach ($contract in @("Get-ScheduledTask", "Principal.UserId", "SetAccessRuleProtection", "SecurityIdentifier]'S-1-5-18'", "SecurityIdentifier]'S-1-5-32-544'", "Set-Acl", "FileSystemRights]::Modify", "New-Item -ItemType Directory")) {
    if (-not $dataDirSource.Contains($contract)) {
        throw "Windows user data directory contract is missing: $contract"
    }
}
foreach ($forbidden in @("service run --listen", "actions= restart/", "ConfigureAndStartAHA2Service", 'Source: "Run-AHA2User.vbs"', "wscript.exe")) {
    if ($source.Contains($forbidden)) {
        throw "Installer still contains a forbidden legacy runtime contract: $forbidden"
    }
}
if ($ValidateOnly) {
    $serverState = if (Test-Path -LiteralPath $inputPath -PathType Leaf) {"found"} else {"not present"}
    $trayState = if (Test-Path -LiteralPath $trayInputPath -PathType Leaf) {"found"} else {"not present"}
    Write-Output "Installer definition valid; server input $serverState; tray input $trayState."
    exit 0
}

if (-not (Test-Path -LiteralPath $inputPath -PathType Leaf)) {
    throw "Windows amd64 server executable not found: $inputPath"
}
if (-not (Test-Path -LiteralPath $trayInputPath -PathType Leaf)) {
    throw "Windows amd64 tray executable not found: $trayInputPath"
}

function Get-PEWindowsSubsystem([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -lt 256 -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) {
        throw "Windows executable is not a valid PE file: $Path"
    }
    $peOffset = [BitConverter]::ToInt32($bytes, 0x3C)
    $optionalHeader = $peOffset + 24
    if ($peOffset -lt 0 -or $optionalHeader + 70 -ge $bytes.Length) {
        throw "Windows executable PE header is truncated: $Path"
    }
    return [BitConverter]::ToUInt16($bytes, $optionalHeader + 68)
}

if ((Get-PEWindowsSubsystem $trayInputPath) -ne 2) {
    throw "AHA2 tray executable must use the Windows GUI subsystem (-H windowsgui): $trayInputPath"
}
if ([string]::IsNullOrWhiteSpace($ISCCPath)) {
    $command = Get-Command "ISCC.exe" -ErrorAction SilentlyContinue
    if ($command) {
        $ISCCPath = $command.Path
    } else {
        $candidates = @(
            (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe"),
            (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe"),
            (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
        )
        $ISCCPath = $candidates | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
    }
}
if ([string]::IsNullOrWhiteSpace($ISCCPath) -or -not (Test-Path -LiteralPath $ISCCPath -PathType Leaf)) {
    throw "Inno Setup 6 compiler (ISCC.exe) was not found."
}

New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
& $ISCCPath "/DMyAppVersion=$normalizedVersion" "/DSourceExe=$inputPath" "/DSourceTrayExe=$trayInputPath" "/DOutputDir=$outputPath" $iss
if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup compilation failed with exit code $LASTEXITCODE."
}

$setup = Join-Path $outputPath "AHA2-Setup-x64.exe"
if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw "Expected installer was not produced: $setup"
}
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $setup).Hash.ToLowerInvariant()
$hashFile = "$setup.sha256"
[IO.File]::WriteAllText($hashFile, "$hash  AHA2-Setup-x64.exe`n", [Text.UTF8Encoding]::new($false))
Write-Output "Installer: $setup"
Write-Output "SHA256: $hashFile"
