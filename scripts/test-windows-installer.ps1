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
	'SourceHelperDir', 'DestDir: "{app}\agent-proxy"',
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

$userDeployPath = Join-Path $repo "scripts\deploy-windows-user.ps1"
$userDeploy = Get-Content -Raw -Encoding UTF8 -LiteralPath $userDeployPath
foreach ($contract in @("ProviderPath", "GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)", "-WorkingDirectory `$install", "AllowCustomUserWritableInstallDir", "UpdateExistingInstallInPlace", "existing-install-in-place", "Custom install directory is not writable", "WriteAccessVerified", "-PerUser", "ElevationRequired=`$false", "AHA2-Setup-User-x64.exe", "Wait-AHA2Health", "Get-FileHash", "AHA2 User", "AHA2 User Update", "DetachedWorker", "New-ScheduledTaskAction", "RunLevel Limited", "Stop-AHA2ProcessTrees", "System32\taskkill.exe", "`$taskkillExecutable", "`$global:LASTEXITCODE = 0", "/T", "/SKIPUSERTASK=1", "user-deploy-result.json", "stagedInstaller", "Unblock-File", "Start-Process -FilePath `$stagedInstaller", "-SelfTest", "StopTimeoutSeconds", "Test-AHA2ExclusiveFileProbe", "Set-AHA2StagedFileAtomic", "Restore-AHA2DatabaseFromBackup", "PRAGMA quick_check", "winsqlite3.dll", "LockedFiles", "BackupSHA256", "RollbackVerified", "rollback_health", "BackupPresent", "Get-AHA2FileRollbackAction", "Get-AHA2ProcessList", "EnumerationError", "Set-AHA2InstalledFile", "Get-AHA2DeploymentPreflight", "Get-AHA2VolumeChecks", "Get-AHA2PreflightRequest", "Get-AHA2PreflightRequirementText", "Invoke-AHA2DeploymentPreflight", "Get-AHA2VolumeFreeBytes", "FreeBytes", "RequiredBytes", "Volumes", "aha2TempSpaceAllowanceBytes", "Copy-AHA2DatabaseBackup", "databaseBackupFileNames", "Get-AHA2ScriptEncodingState", "Assert-AHA2WindowsPowerShellScriptEncoding", "Resolve-AHA2DetachedHandoff", "Assert-AHA2DetachedWorkerAlive", "HandoffTimeoutSeconds", "Test-AHA2WorkerStartedSince", "Get-AHA2UtcTimestamp")) {
    if (-not $userDeploy.Contains($contract)) {
        throw "Per-user deployment contract is missing: $contract"
    }
}
if ($userDeploy.Contains("Get-CimInstance Win32_Process -ErrorAction SilentlyContinue") -or
    $userDeploy.Contains('-Filter "ProcessId=$($Target.Id)" -ErrorAction SilentlyContinue') -or
    $userDeploy.Contains('-Filter "ProcessId=$current" -ErrorAction SilentlyContinue')) {
    throw "Process enumeration and identity lookups must fail closed instead of swallowing CIM errors."
}
if ($userDeploy.Contains("Start-Process -FilePath `$installer")) {
    throw "Per-user deployment must not launch the installer directly from the WSL UNC path; stage a local copy first."
}
if ($userDeploy.Contains('Copy-Item -LiteralPath $backupFile -Destination')) {
    throw "Per-user rollback must not copy a backup over a live file; stage and atomically replace it."
}
if (-not $userDeploy.Contains("[IO.File]::Replace")) {
    throw "Per-user rollback must use an atomic file replacement for binaries and the database."
}
if ($userDeploy.Contains("[IO.File]::Replace(`$TemporaryPath, `$Destination, `$null)") -or
    $userDeploy.Contains("[IO.File]::Replace(`$temporaryTarget, `$target, `$null)")) {
    throw "Atomic replacement must pass [NullString]::Value, not `$null, as the backup name."
}
if (-not $userDeploy.Contains("[NullString]::Value")) {
    throw "Atomic replacement must pass [NullString]::Value as the null backup name."
}
$preflightIndex = $userDeploy.IndexOf('$preflight = Invoke-AHA2DeploymentPreflight')
$stopStageIndex = $userDeploy.IndexOf('$stop = Stop-AHA2ProcessTrees')
$backupDirectoryIndex = $userDeploy.IndexOf('New-Item -ItemType Directory -Path $backup')
if ($preflightIndex -lt 0 -or $stopStageIndex -lt 0 -or $backupDirectoryIndex -lt 0 -or
    $preflightIndex -gt $stopStageIndex -or $preflightIndex -gt $backupDirectoryIndex) {
    throw "Per-user deployment must complete the disk-space preflight before creating the backup directory or stopping AHA2."
}
$preflightRequestMarker = 'Get-AHA2PreflightRequest -DataDirectory $data -DatabasePaths @($databasePath,$databaseWalPath,$databaseShmPath) -InstallDirectory $install -TempDirectory ([IO.Path]::GetTempPath()) -UseInstaller (-not $UpdateExistingInstallInPlace)'
if (-not $userDeploy.Contains($preflightRequestMarker)) {
    throw "Per-user deployment must build the preflight request from the data, install, and temp directories, and skip temp for in-place updates."
}
$taskSnapshotIndex = $userDeploy.IndexOf('$previousTaskXML')
$stopBeforeBackupIndex = $userDeploy.IndexOf('Stop-AHA2ProcessTrees', $taskSnapshotIndex)
$databaseBackupIndex = $userDeploy.IndexOf('$state.Stage = "backup"', $taskSnapshotIndex)
if ($stopBeforeBackupIndex -lt 0 -or $databaseBackupIndex -lt 0 -or $stopBeforeBackupIndex -gt $databaseBackupIndex) {
    throw "Per-user deployment must stop and verify the complete AHA2 process tree before copying SQLite backup files."
}
$backupListMarker = '$databaseBackupFileNames = @("aha2.db", "aha2.db-wal")'
if (-not $userDeploy.Contains($backupListMarker)) {
    throw "Per-user deployment must limit database backups to aha2.db and aha2.db-wal; aha2.db-shm is volatile."
}
if ($userDeploy.Contains('$databaseFileNames + @("secrets.json"')) {
    throw "Per-user backup loop still iterates the full database trio, including the volatile shm file."
}
$backupFunctionStart = $userDeploy.IndexOf('function Test-AHA2BackupCopy')
$backupFunctionEnd = $userDeploy.IndexOf('function Copy-AHA2DatabaseBackup', $backupFunctionStart)
if ($backupFunctionStart -lt 0 -or $backupFunctionEnd -le $backupFunctionStart) {
    throw "Per-user deployment is missing the backup verification helpers."
}
$backupFunction = $userDeploy.Substring($backupFunctionStart, $backupFunctionEnd - $backupFunctionStart)
if (-not $backupFunction.Contains('Get-FileHash -LiteralPath $Source -Algorithm SHA256 -ErrorAction Stop') -or
    -not $backupFunction.Contains('catch')) {
    throw "Test-AHA2BackupCopy must stop on Get-FileHash errors and report the file with its reason."
}
$restoreFunctionStart = $userDeploy.IndexOf('function Restore-AHA2DatabaseFromBackup')
$restoreFunctionEnd = $userDeploy.IndexOf('function Test-AHA2ExclusiveFileProbe', $restoreFunctionStart)
if ($restoreFunctionStart -lt 0 -or $restoreFunctionEnd -le $restoreFunctionStart) {
    throw "Per-user deployment is missing the database restore helper."
}
$restoreFunction = $userDeploy.Substring($restoreFunctionStart, $restoreFunctionEnd - $restoreFunctionStart)
if (-not $restoreFunction.Contains('$databaseBackupFileNames') -or $restoreFunction.Contains('"aha2.db-shm"')) {
    throw "Database restore must only consider durable backup files and must never copy shm back."
}
$handoffStart = $userDeploy.IndexOf('if (-not $DetachedWorker -and (Test-AHA2Ancestor))')
$handoffEnd = $userDeploy.IndexOf('$stamp = [DateTime]::UtcNow.ToString', $handoffStart)
if ($handoffStart -lt 0 -or $handoffEnd -le $handoffStart) {
    throw "Per-user deployment is missing the detached worker handoff block."
}
$handoffSection = $userDeploy.Substring($handoffStart, $handoffEnd - $handoffStart)
foreach ($marker in @('Assert-AHA2WindowsPowerShellScriptEncoding', 'Start-ScheduledTask', '$decision -eq "failed"', 'Assert-AHA2DetachedWorkerAlive')) {
    if (-not $handoffSection.Contains($marker)) {
        throw "Detached handoff must fail loudly when the worker exits without a result: missing $marker"
    }
}
$legacyDeploy = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\deploy-windows.ps1")
if ($legacyDeploy -match 'runtimeFiles\s*=\s*@\([^)]*aha2\.db-shm') {
    throw "Legacy deploy-windows.ps1 must not back up the volatile aha2.db-shm."
}
if (-not $legacyDeploy.Contains('foreach ($staleName in @("aha2.db-shm", "aha2.db-wal"))')) {
    throw "Legacy deploy-windows.ps1 rollback must delete stale shm and WAL before restoring the database."
}
& $userDeployPath -SelfTest
& $userDeployPath -RepoPath $repo -InputExe (Join-Path $repo "go.mod") -InputTrayExe (Join-Path $repo "go.mod") -InstallDir (Join-Path $localAppData "Programs\AHA2-validation") -DataDir (Join-Path ([IO.Path]::GetTempPath()) "AHA2 User Data") -ValidateOnly
$customInstallValidation = Join-Path $repo ".tools\AHA2 Custom Install Validation"
New-Item -ItemType Directory -Path $customInstallValidation -Force | Out-Null
$previousLocalAppData = $env:LOCALAPPDATA
try {
    $env:LOCALAPPDATA = Join-Path $repo ".tools\fake-local-app-data"
    $customValidation = & $userDeployPath -RepoPath $repo -InputExe (Join-Path $repo "go.mod") -InputTrayExe (Join-Path $repo "go.mod") -InstallDir $customInstallValidation -DataDir (Join-Path ([IO.Path]::GetTempPath()) "AHA2 User Data") -AllowCustomUserWritableInstallDir -ValidateOnly
    if ($customValidation.Mode -ne "per-user-custom" -or -not $customValidation.WriteAccessVerified) {
        throw "Custom user-writable deployment validation did not report the expected mode."
    }
} finally {
    $env:LOCALAPPDATA = $previousLocalAppData
    Remove-Item -LiteralPath $customInstallValidation -Recurse -Force -ErrorAction SilentlyContinue
}

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
$buildAndDeploy = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "scripts\build-and-deploy-windows-user.ps1")
# These assertions name the mechanism, so reworking an invocation legitimately
# changes them -- the guard is that the capability is still present, not that the
# wording froze. Keep each one pointing at a real marker: the toolchain probe, the
# free-space probe, and the node that runs the Web build.
foreach ($contract in @("ProviderPath", "GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)", "aha2-version-", "main.webVersion=", "InputFeishuPlugin", "InputFeishuManifest", "AllowCustomUserWritableInstallDir", "UpdateExistingInstallInPlace", "No WSL Node.js able to run the Web build was found", "NodeRuntime", '$wslNode, "scripts/build-web.mjs"', '"test", "-x", "$linuxRepo/.tools/go/bin/go"', '"df", "-Pk", "."')) {
    if (-not $buildAndDeploy.Contains($contract)) {
        throw "Windows build-and-deploy contract is missing: $contract"
    }
}
$localBuildScript = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "local_build.sh")
foreach ($contract in @(
    "scripts/build-web.mjs", "web/tests/build.test.mjs", "test ./...", "vet ./...",
    "CGO_ENABLED=0 GOOS=windows GOARCH=amd64", "-H windowsgui", "plugins/feishu",
    "build-and-deploy-windows-user.ps1", "-DeployOnly", "AllowCustomUserWritableInstallDir",
    "UpdateExistingInstallInPlace", "user-deploy-result-local-build.json",
    "build-windows-installer.ps1", "AHA2_INSTALLER_DIR", "AHA2_ISCC_PATH",
    "AHA2-Setup-User-x64.exe", "-PerUser"
)) {
    if (-not $localBuildScript.Contains($contract)) {
        throw "WSL local build contract is missing: $contract"
    }
}
if ($localBuildScript.Contains(".aha2-planned-restart")) {
    throw "WSL local build must not restore the removed planned-restart marker."
}
Write-Output "Windows tray installer and Release workflow contracts are valid."
