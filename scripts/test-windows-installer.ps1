[CmdletBinding()]
param([string]$RepoPath = (Split-Path -Parent $PSScriptRoot))

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = [IO.Path]::GetFullPath($RepoPath)

# Windows PowerShell 5.1 reads a script without a UTF-8 BOM using the system
# ANSI code page. Every checked-in script that 5.1 may execute must therefore
# start with EF BB BF, and the parser must accept it. (internal/desktop/*.ps1
# are passed as -EncodedCommand strings, not read from disk by 5.1.)
function Assert-ParsesCleanly([string]$Path) {
  $tokens = $null
  $errors = $null
  [void][System.Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
  if ($errors.Count -gt 0) {
    throw ("PowerShell parser rejected {0}: {1}" -f $Path, (($errors | ForEach-Object { $_.Message }) -join "; "))
  }
}
$windowsScripts = @(
  Get-ChildItem -Path (Join-Path $repo "scripts\*.ps1") -File
  Get-ChildItem -Path (Join-Path $repo "installer\windows\*.ps1") -File
)
foreach ($scriptFile in $windowsScripts) {
  $bytes = [IO.File]::ReadAllBytes($scriptFile.FullName)
  if ($bytes.Length -lt 3 -or $bytes[0] -ne 0xEF -or $bytes[1] -ne 0xBB -or $bytes[2] -ne 0xBF) {
    throw ("{0} must be saved as UTF-8 with a BOM; Windows PowerShell 5.1 reads a BOM-less file as ANSI." -f $scriptFile.FullName)
  }
  Assert-ParsesCleanly $scriptFile.FullName
}

# Regression for the 0.5.471 failure: the same text parses with a BOM and must
# not parse when 5.1's ANSI decoding is applied. The negative half is skipped
# only on a host whose ANSI code page is already UTF-8, where the BOM is moot.
$bomlessRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-bomless-selftest-" + [Guid]::NewGuid().ToString("N"))
try {
  New-Item -ItemType Directory -Path $bomlessRoot -Force | Out-Null
  $utf8NoBom = [Text.UTF8Encoding]::new($false)
  # First line breaks under a single-byte ANSI code page (0x93 becomes a smart
  # quote); second line breaks under a DBCS code page (the trailing byte pairs
  # with the closing brace and swallows it).
  $fixtureText = "Write-Output 专`nif (`$true) { Write-Output 中}"
  $fixtureBytes = $utf8NoBom.GetBytes($fixtureText)
  $withBomPath = Join-Path $bomlessRoot "with-bom.ps1"
  $withBomBytes = [byte[]]::new(3 + $fixtureBytes.Length)
  $withBomBytes[0] = 0xEF; $withBomBytes[1] = 0xBB; $withBomBytes[2] = 0xBF
  [Array]::Copy($fixtureBytes, 0, $withBomBytes, 3, $fixtureBytes.Length)
  [IO.File]::WriteAllBytes($withBomPath, $withBomBytes)
  Assert-ParsesCleanly $withBomPath
  if ([Text.Encoding]::Default.CodePage -ne 65001) {
    $ansiText = [Text.Encoding]::Default.GetString($fixtureBytes)
    $tokens = $null
    $errors = $null
    [void][System.Management.Automation.Language.Parser]::ParseInput($ansiText, [ref]$tokens, [ref]$errors)
    if ($errors.Count -eq 0) {
      throw "A BOM-less UTF-8 script decoded with the ANSI code page unexpectedly parsed; the BOM regression test is ineffective."
    }
  } else {
    Write-Host "Skipping the BOM-less negative parse check: the host ANSI code page is UTF-8."
  }
} finally {
  Remove-Item -LiteralPath $bomlessRoot -Recurse -Force -ErrorAction SilentlyContinue
}

$localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
$tempRoot = if ([string]::IsNullOrWhiteSpace($env:TEMP)) { Join-Path $localAppData "Temp" } else { $env:TEMP }
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
$env:TEMP = $tempRoot
$env:TMP = $tempRoot
$missingServer = Join-Path ([IO.Path]::GetTempPath()) ("aha2-server-validation-" + [Guid]::NewGuid().ToString("N") + ".exe")
$missingTray = Join-Path ([IO.Path]::GetTempPath()) ("aha2-tray-validation-" + [Guid]::NewGuid().ToString("N") + ".exe")
& (Join-Path $repo "scripts\build-windows-installer.ps1") -RepoPath $repo -InputExe $missingServer -InputTrayExe $missingTray -ValidateOnly
& (Join-Path $repo "scripts\build-windows-installer.ps1") -RepoPath $repo -InputExe $missingServer -InputTrayExe $missingTray -PerUser -ValidateOnly

$installerPath = Join-Path $repo "installer\windows\AHA2.iss"
$installer = Get-Content -Raw -Encoding UTF8 -LiteralPath $installerPath
foreach ($contract in @(
	"PrivilegesRequired=admin", "PrivilegesRequired=lowest", "PerUserInstall", "AHA2-Setup-User-x64", "{localappdata}\Programs\AHA2", "DisableDirPage=no", "ArchitecturesAllowed=x64compatible", "CurUninstallStepChanged", "DeinitializeSetup",
    '[Icons]', '[Run]', '[Files]', '[InstallDelete]', 'Type: files; Name: "{app}\Run-AHA2User.vbs"', 'CreateInputDirPage', 'CreateInputOptionPage', 'CreateInputQueryPage',
    'RegisterPreviousData', 'remoteip=localsubnet profile=private', '{code:LocalManagementURL}', '{commonappdata}\AHA2',
    'DestName: "aha2.exe"', 'DestName: "aha2-tray.exe"', 'TrayParameters', '--server', '--listen', '--data-dir',
	'DestName: "Xray-core-MPL-2.0.txt"', 'DestName: "Xray-core-SOURCE.txt"',
    'runasoriginaluser', 'StopInstalledUserProcesses', "Get-Process -Name ''aha2-tray'',''aha2''",
    'DisableAndStopLegacyAHA2Service(True)', 'RemoveLegacyAHA2Service()', 'Register-AHA2UserTask.ps1',
    'Prepare-AHA2DataDir.ps1', 'ConfigureAHA2UserTask', 'AHA2UserTaskName', 'RemoveAHA2UserTask',
    'ExecAsOriginalUser', 'InstallStarted',
    'WaitForAHA2Health', 'LocalHealthURL', "ExpandConstant('{app}\aha2-tray.exe')"
)) {
    if (-not $installer.Contains($contract)) {
        throw "Installer safety contract is missing: $contract"
    }
}
# Agent API must not be an installer input. Leaving it unset is what lets the
# server derive a loopback base URL, and that is the value the reverse tunnel
# needs; asking here invited an answer that could only make the result worse.
# InstallStarted is the guard that keeps a cancelled wizard from stopping a
# running installation and deleting its login task.
foreach ($forbidden in @('AgentAPIPage', 'InsecureAgentAPIPage', '--agent-api-url', '--allow-insecure-agent-api')) {
    if ($installer.Contains($forbidden)) {
        throw "Installer still asks the user for Agent API settings: $forbidden"
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
$stopProcesses = $installer.IndexOf("StopInstalledUserProcesses();", $installStep)
$stopTask = $installer.IndexOf("StopAHA2UserTask();", $stopProcesses)
if ($installStep -lt 0 -or $disableService -le $installStep -or $stopProcesses -le $disableService -or $stopTask -le $stopProcesses) {
    throw "Upgrade must disable the legacy service, kill the complete runtime tree, and then end the unique task before file replacement."
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
$uninstallProcesses = $installer.IndexOf("StopInstalledUserProcesses();", $uninstall)
$uninstallTask = $installer.IndexOf("RemoveAHA2UserTask();", $uninstallProcesses)
if ($uninstall -lt 0 -or $uninstallProcesses -le $uninstall -or $uninstallTask -le $uninstallProcesses) {
    throw "Uninstall must kill the complete runtime tree before removing the unique task."
}

$taskScriptPath = Join-Path $repo "installer\windows\Register-AHA2UserTask.ps1"
$taskScript = Get-Content -Raw -Encoding UTF8 -LiteralPath $taskScriptPath
foreach ($contract in @(
    "TrayExecutable", "ServerExecutable", "New-ScheduledTaskAction -Execute `$trayPath", "--server", "--listen", "--data-dir",
    "New-ScheduledTaskTrigger -AtLogOn", "WindowsIdentity]::GetCurrent().Name", "Win32_ComputerSystem", "OrdinalIgnoreCase",
    "-LogonType Interactive", "-RunLevel Limited", "-RestartCount 3", "-RestartInterval", "Register-ScheduledTask",
    "-Force", "Start-ScheduledTask", "Test-ExistingAHA2UserTask", "Resolve-IdentitySid",
    "existingAction.Arguments", "existingAction.WorkingDirectory", "reusing it"
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
$taskScript = Get-Content -Raw -Encoding UTF8 -LiteralPath $taskScriptPath
if ($taskScript.IndexOf("if (Test-ExistingAHA2UserTask)") -gt $taskScript.LastIndexOf("Register-ScheduledTask -TaskName")) {
    throw "Windows user task does not reuse an exact existing task before attempting registration."
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
    throw "Windows tray task accepted a non-loopback HTTP Agent API URL."
}
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
foreach ($contract in @("AHA2 User", "aha2-tray.exe", "--server", "aha2.exe", '`LocalSystem`', "Program Files", "ProgramData\AHA2", "Agent API", "--agent-api-url")) {
    if (-not $deployment.Contains($contract)) {
        throw "Windows deployment documentation is missing: $contract"
    }
}
foreach ($contract in @("taskkill.exe", "/T", "backend CLI")) {
  if ($installer -notmatch [regex]::Escape($contract)) { throw "Installer process-tree shutdown contract is missing: $contract" }
}
foreach ($contract in @("SKIPUSERTASK", "authoritative listen and data-directory arguments", "if UserTaskWasPresent then", "preserved for per-user upgrade")) {
  if ($installer -notmatch [regex]::Escape($contract)) { throw "Installer task-registration handoff contract is missing: $contract" }
}
$taskCommandProbe = $installer.IndexOf("function ExistingUserTaskCommandLine")
$captureTask = $installer.IndexOf("ExecAndCaptureOutput", $taskCommandProbe)
$dataDirProbe = $installer.IndexOf("function ExistingUserTaskDataDir", $captureTask)
$dataDirArgument = $installer.IndexOf("--data-dir", $dataDirProbe)
$explicitDataDir = $installer.IndexOf("if ExplicitDataDir <> '' then")
$taskDataDir = $installer.IndexOf("else if TaskDataDir <> '' then", $explicitDataDir)
$previousDataDir = $installer.IndexOf("DataDirPage.Values[0] := PreviousDataDir", $taskDataDir)
if ($taskCommandProbe -lt 0 -or $captureTask -le $taskCommandProbe -or $dataDirProbe -le $captureTask -or $dataDirArgument -le $dataDirProbe -or
    $explicitDataDir -lt 0 -or $taskDataDir -le $explicitDataDir -or $previousDataDir -le $taskDataDir) {
  throw "The per-user wizard must prefer explicit DATADIR, then the existing task data directory, then previous/default data."
}
$listenProbe = $installer.IndexOf("function ExistingUserTaskListenAddress")
$listenArgument = $installer.IndexOf("--listen", $listenProbe)
$listenModeRestore = $installer.IndexOf("if TaskListenHost <> '' then")
$listenPortRestore = $installer.IndexOf("if TaskListenPort <> '' then", $listenModeRestore)
$skipStart = $installer.IndexOf("function ShouldSkipPage")
$skipEnd = $installer.IndexOf("function NextButtonClick", $skipStart)
$skipSection = $installer.Substring($skipStart, $skipEnd - $skipStart)
if ($listenProbe -lt 0 -or $listenArgument -le $listenProbe -or $listenModeRestore -lt 0 -or $listenPortRestore -le $listenModeRestore -or
    $skipSection.Contains("PageID = ListenModePage.ID") -or -not $skipSection.Contains("PageID = FirewallPage.ID")) {
  throw "The per-user wizard must show listen mode, restore the existing --listen value, and keep only the firewall page unavailable."
}
$preserveTask = $installer.IndexOf("Existing AHA2 user task will be preserved for per-user upgrade.")
$enableTask = $installer.IndexOf("/Change /Enable /TN", $preserveTask)
$preserveExit = $installer.IndexOf("Exit;", $preserveTask)
if ($preserveTask -lt 0 -or $enableTask -le $preserveTask -or $preserveExit -le $enableTask) {
  throw "A preserved per-user login task must be re-enabled before ConfigureAHA2UserTask exits."
}
foreach ($forbidden in @("delayed-auto Windows", "aha2.exe service run --listen", "wscript.exe", "Run-AHA2User.vbs")) {
    if ($deployment.Contains($forbidden)) {
        throw "Windows deployment documentation still describes an old runtime: $forbidden"
    }
}

$workflow = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo ".github\workflows\release.yml")
foreach ($contract in @(
    "runs-on: windows-latest", "cmd\aha-tray", "aha2-tray.exe", "-H windowsgui", "build-windows-installer.ps1", "-InputTrayExe",
	"AHA2-Setup-x64.exe", "AHA2-Setup-User-x64.exe", "build-feishu-plugin.sh", "build-linux-packages.sh", "build-linux-sync-packages.sh", "aha2-sync_", "build-macos-packages.sh",
    "ubuntu-24.04-arm", "SHA256SUMS", "merge-multiple: true", "pattern: package-*", "main.version=", "main.webVersion=", "gh release create"
)) {
    if (-not $workflow.Contains($contract)) {
        throw "Release workflow contract is missing: $contract"
    }
}
if ($workflow.Contains("portable-release") -or $workflow.Contains("Upload portable artifacts")) {
    throw "Release workflow must not publish portable binaries."
}
$installerBuilder = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\build-windows-installer.ps1")
foreach ($contract in @(
    "Get-PEWindowsSubsystem", "Windows GUI subsystem", "-H windowsgui",
    "Start-Process", "-Wait", 'Remove-Item -LiteralPath $setup,$hashFile'
)) {
    if (-not $installerBuilder.Contains($contract)) {
        throw "Windows installer builder does not enforce the GUI tray subsystem: $contract"
    }
}
$localBuild = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\build-all.sh")
foreach ($contract in @("./cmd/aha-tray", "aha2-tray-", "-H windowsgui")) {
    if (-not $localBuild.Contains($contract)) {
        throw "Local cross-build contract is missing: $contract"
    }
}
$packageBuilder = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\build-windows-package.sh")
foreach ($contract in @(
    "scripts/build-web.mjs", "web/tests/build.test.mjs", "CGO_ENABLED=0 GOOS=windows GOARCH=amd64",
    "-H windowsgui", "plugins/feishu", "aha2-windows-amd64.exe", "aha2-tray-windows-amd64.exe",
    "release-manifest.json", "scripts/release-manifest.mjs"
)) {
    if (-not $packageBuilder.Contains($contract)) {
        throw "Windows package build contract is missing: $contract"
    }
}
$manifestGenerator = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\release-manifest.mjs")
foreach ($contract in @("schema_version", "version", "commit", "built_at", "generator", "files", "sha256")) {
    if (-not $manifestGenerator.Contains($contract)) {
        throw "Release manifest generator contract is missing: $contract"
    }
}
$localBuildScript = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "local_build.sh")
if (-not $localBuildScript.Contains("scripts/build-windows-package.sh")) {
    throw "local_build.sh must delegate to the Windows package build script."
}
if ($localBuildScript.Contains(".aha2-planned-restart")) {
    throw "WSL local build must not restore the removed planned-restart marker."
}
Write-Output "Windows tray installer and Release workflow contracts are valid."
