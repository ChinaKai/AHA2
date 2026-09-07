[CmdletBinding()]
param([string]$RepoPath = (Split-Path -Parent $PSScriptRoot))

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = [IO.Path]::GetFullPath($RepoPath)
$missingServer = Join-Path ([IO.Path]::GetTempPath()) ("aha2-server-validation-" + [Guid]::NewGuid().ToString("N") + ".exe")
$missingTray = Join-Path ([IO.Path]::GetTempPath()) ("aha2-tray-validation-" + [Guid]::NewGuid().ToString("N") + ".exe")
& (Join-Path $repo "scripts\build-windows-installer.ps1") -RepoPath $repo -InputExe $missingServer -InputTrayExe $missingTray -ValidateOnly

$installerPath = Join-Path $repo "installer\windows\AHA2.iss"
$installer = Get-Content -Raw -Encoding UTF8 -LiteralPath $installerPath
foreach ($contract in @(
    "PrivilegesRequired=admin", "ArchitecturesAllowed=x64compatible", "CurUninstallStepChanged", "DeinitializeSetup",
    '[Icons]', '[Run]', '[Files]', '[InstallDelete]', 'Type: files; Name: "{app}\Run-AHA2User.vbs"', 'CreateInputDirPage', 'CreateInputOptionPage', 'CreateInputQueryPage',
    'RegisterPreviousData', 'remoteip=localsubnet profile=private', '{code:LocalManagementURL}', '{commonappdata}\AHA2',
    'DestName: "aha2.exe"', 'DestName: "aha2-tray.exe"', 'TrayParameters', '--server', '--listen', '--data-dir',
    'runasoriginaluser', 'StopInstalledUserProcesses', "Get-Process -Name ''aha2-tray'',''aha2''",
    'DisableAndStopLegacyAHA2Service(True)', 'RemoveLegacyAHA2Service()', 'Register-AHA2UserTask.ps1',
    'Prepare-AHA2DataDir.ps1', 'ConfigureAHA2UserTask', 'AHA2UserTaskName', 'RemoveAHA2UserTask',
    'ExecAsOriginalUser', 'AgentAPIPage', '--agent-api-url', '--allow-insecure-agent-api',
    'WaitForAHA2Health', 'LocalHealthURL', "ExpandConstant('{app}\aha2-tray.exe')"
)) {
    if (-not $installer.Contains($contract)) {
        throw "Installer safety contract is missing: $contract"
    }
}
foreach ($forbidden in @('Source: "Run-AHA2User.vbs"', "UserLauncherParameters", "wscript.exe", "service run --listen", "actions= restart/", "ConfigureAndStartAHA2Service", '{userstartup}\AHA2')) {
    if ($installer.Contains($forbidden)) {
        throw "Installer still contains the forbidden legacy runtime contract: $forbidden"
    }
}
if ($installer.Contains("RequireSC('create") -or $installer.Contains("RunSC('create")) {
    throw "Installer must never create a Windows service."
}
if ($installer.Contains("RunSC(" + "'start ") -or -not $installer.Contains('start= disabled') -or -not $installer.Contains('remains stopped and disabled')) {
    throw "Legacy LocalSystem service must remain stopped and disabled."
}
if (-not $installer.Contains("procedure RemoveLegacyAHA2Service();") -or -not $installer.Contains("DisableAndStopLegacyAHA2Service(True);")) {
    throw "Legacy service deletion must require the service to be disabled first."
}

$installStep = $installer.IndexOf("if CurStep = ssInstall then")
$disableService = $installer.IndexOf("DisableAndStopLegacyAHA2Service(True);", $installStep)
$stopTask = $installer.IndexOf("StopAHA2UserTask();", $installStep)
$stopProcesses = $installer.IndexOf("StopInstalledUserProcesses();", $stopTask)
if ($installStep -lt 0 -or $disableService -le $installStep -or $stopTask -le $disableService -or $stopProcesses -le $stopTask) {
    throw "Upgrade must disable the legacy service, end the unique task, and stop tray/server before file replacement."
}
$postInstall = $installer.IndexOf("else if CurStep = ssPostInstall then")
$startTask = $installer.IndexOf("if not RunScheduledTask('/Run /TN", $postInstall)
$health = $installer.IndexOf("WaitForAHA2Health();", $startTask)
$removeService = $installer.IndexOf("RemoveLegacyAHA2Service();", $health)
$commit = $installer.IndexOf("InstallCommitted := True;", $removeService)
if ($postInstall -lt 0 -or $startTask -le $postInstall -or $health -le $startTask -or $removeService -le $health -or $commit -le $removeService) {
    throw "Installer transition must start the tray task, verify health, delete the legacy service best-effort, and then commit."
}
$uninstall = $installer.IndexOf("if CurUninstallStep = usUninstall then")
$uninstallTask = $installer.IndexOf("RemoveAHA2UserTask();", $uninstall)
$uninstallProcesses = $installer.IndexOf("StopInstalledUserProcesses();", $uninstallTask)
if ($uninstall -lt 0 -or $uninstallTask -le $uninstall -or $uninstallProcesses -le $uninstallTask) {
    throw "Uninstall must remove the unique task before stopping tray/server."
}

$taskScriptPath = Join-Path $repo "installer\windows\Register-AHA2UserTask.ps1"
$taskScript = Get-Content -Raw -Encoding UTF8 -LiteralPath $taskScriptPath
foreach ($contract in @(
    "TrayExecutable", "ServerExecutable", "New-ScheduledTaskAction -Execute `$trayPath", "--server", "--listen", "--data-dir",
    "New-ScheduledTaskTrigger -AtLogOn", "WindowsIdentity]::GetCurrent().Name", "Win32_ComputerSystem", "OrdinalIgnoreCase",
    "-LogonType Interactive", "-RunLevel Limited", "-RestartCount 3", "-RestartInterval", "Register-ScheduledTask",
    "-Force", "Start-ScheduledTask", "AllowInsecureAgentAPI"
)) {
    if (-not $taskScript.Contains($contract)) {
        throw "Windows tray login task contract is missing: $contract"
    }
}
foreach ($forbidden in @("wscript.exe", ".vbs", "powershell.exe", "service run", "SYSTEM")) {
    if ($taskScript.Contains($forbidden)) {
        throw "Windows login task contains a forbidden long-running host: $forbidden"
    }
}

$dataDirScript = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "installer\windows\Prepare-AHA2DataDir.ps1")
foreach ($contract in @("Get-ScheduledTask", "Principal.UserId", "SetAccessRuleProtection", "SecurityIdentifier]'S-1-5-18'", "SecurityIdentifier]'S-1-5-32-544'", "Set-Acl", "FileSystemRights]::Modify", "New-Item -ItemType Directory")) {
    if (-not $dataDirScript.Contains($contract)) {
        throw "Windows user data directory contract is missing: $contract"
    }
}

$validationServer = [IO.Path]::GetTempFileName()
$validationTray = [IO.Path]::GetTempFileName()
try {
    & $taskScriptPath -TrayExecutable $validationTray -ServerExecutable $validationServer -Listen "127.0.0.1:8766" -DataDir (Join-Path ([IO.Path]::GetTempPath()) "AHA2 User Data") -AgentAPIURL "https://aha.example.test" -ValidateOnly
} finally {
    Remove-Item -LiteralPath $validationServer,$validationTray -Force -ErrorAction SilentlyContinue
}
& (Join-Path $repo "installer\windows\Prepare-AHA2DataDir.ps1") -DataDir (Join-Path ([IO.Path]::GetTempPath()) "AHA2 User Data") -ValidateOnly

$unsafeListenRejected = $false
try {
    & $taskScriptPath -TrayExecutable (Join-Path $repo "go.mod") -ServerExecutable (Join-Path $repo "go.mod") -Listen '127.0.0.1:8766"' -DataDir $repo -ValidateOnly
} catch {
    $unsafeListenRejected = $true
}
if (-not $unsafeListenRejected) {
    throw "Windows tray task accepted an unsafe listen argument."
}
$insecureAgentURLRejected = $false
try {
    & $taskScriptPath -TrayExecutable (Join-Path $repo "go.mod") -ServerExecutable (Join-Path $repo "go.mod") -Listen "127.0.0.1:8766" -DataDir $repo -AgentAPIURL "http://192.0.2.10:8766" -ValidateOnly
} catch {
    $insecureAgentURLRejected = $true
}
if (-not $insecureAgentURLRejected) {
    throw "Windows tray task accepted a non-loopback HTTP Agent API URL without confirmation."
}
& $taskScriptPath -TrayExecutable (Join-Path $repo "go.mod") -ServerExecutable (Join-Path $repo "go.mod") -Listen "127.0.0.1:8766" -DataDir $repo -AgentAPIURL "http://192.0.2.10:8766" -AllowInsecureAgentAPI -ValidateOnly

$uncDataRejected = $false
try {
    & (Join-Path $repo "installer\windows\Prepare-AHA2DataDir.ps1") -DataDir "\\server\share\AHA2" -ValidateOnly
} catch {
    $uncDataRejected = $true
}
if (-not $uncDataRejected) {
    throw "Windows data directory helper accepted a UNC path."
}

$deployment = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "docs\deployment.md")
foreach ($contract in @("AHA2 User", "aha2-tray.exe", "--server", "aha2.exe", '`LocalSystem`', "Program Files", "ProgramData\AHA2", "Agent API", "--agent-api-url", "--allow-insecure-agent-api")) {
    if (-not $deployment.Contains($contract)) {
        throw "Windows deployment documentation is missing: $contract"
    }
}
foreach ($forbidden in @("delayed-auto Windows", "aha2.exe service run --listen", "wscript.exe", "Run-AHA2User.vbs")) {
    if ($deployment.Contains($forbidden)) {
        throw "Windows deployment documentation still describes an old runtime: $forbidden"
    }
}

$workflow = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo ".github\workflows\release.yml")
foreach ($contract in @(
    "runs-on: windows-latest", "cmd\aha-tray", "aha2-tray.exe", "-H windowsgui", "build-windows-installer.ps1", "-InputTrayExe",
    "AHA2-Setup-x64.exe", "build-linux-packages.sh", "build-linux-sync-packages.sh", "aha2-sync_", "build-macos-packages.sh",
    "ubuntu-24.04-arm", "SHA256SUMS", "merge-multiple: true", "pattern: package-*", "main.version=", "gh release create"
)) {
    if (-not $workflow.Contains($contract)) {
        throw "Release workflow contract is missing: $contract"
    }
}
if ($workflow.Contains("portable-release") -or $workflow.Contains("Upload portable artifacts")) {
    throw "Release workflow must not publish portable binaries."
}
$localBuild = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\build-all.sh")
foreach ($contract in @("./cmd/aha-tray", "aha2-tray-", "-H windowsgui")) {
    if (-not $localBuild.Contains($contract)) {
        throw "Local cross-build contract is missing: $contract"
    }
}
Write-Output "Windows tray installer and Release workflow contracts are valid."
