[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$InputExe,
  [Parameter(Mandatory = $true)][string]$InputTrayExe,
  [string]$InputFeishuPlugin = "",
  [string]$InputFeishuManifest = "",
  [string]$RepoPath = (Split-Path -Parent $PSScriptRoot),
  [string]$InstallDir = "",
  [string]$DataDir = "",
  [string]$Version = "dev",
  [string]$TaskName = "AHA2 User",
	[string]$UpdateTaskName = "AHA2 User Update",
  [string]$Listen = "127.0.0.1:8766",
  [string]$AgentAPIURL = "",
	[switch]$AllowInsecureAgentAPI,
  [string]$HealthURL = "http://127.0.0.1:8766/healthz",
  [int]$HealthTimeoutSeconds = 60,
  [string]$ResultPath = "",
  [switch]$DetachedWorker,
  [switch]$AllowCustomUserWritableInstallDir,
  [switch]$UpdateExistingInstallInPlace,
  [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$systemRoot = if ([string]::IsNullOrWhiteSpace($env:SystemRoot)) { "C:\Windows" } else { $env:SystemRoot }
$powershellExecutable = Join-Path $systemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$taskkillExecutable = Join-Path $systemRoot "System32\taskkill.exe"
foreach ($requiredSystemExecutable in @($powershellExecutable, $taskkillExecutable)) {
  if (-not (Test-Path -LiteralPath $requiredSystemExecutable -PathType Leaf)) {
    throw "Required Windows system executable was not found: $requiredSystemExecutable"
  }
}

$localAppData = $env:LOCALAPPDATA
if ([string]::IsNullOrWhiteSpace($localAppData)) {
  $localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
}
if ([string]::IsNullOrWhiteSpace($localAppData)) {
  throw "Could not resolve the current user's LocalApplicationData directory."
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) { $InstallDir = Join-Path $localAppData "Programs\AHA2" }
if ([string]::IsNullOrWhiteSpace($DataDir)) { $DataDir = Join-Path $localAppData "AHA2" }

$repo = (Resolve-Path -LiteralPath $RepoPath).ProviderPath
$serverInput = (Resolve-Path -LiteralPath $InputExe).ProviderPath
$trayInput = (Resolve-Path -LiteralPath $InputTrayExe).ProviderPath
$pluginInput = ""
$pluginManifestInput = ""
if ([string]::IsNullOrWhiteSpace($InputFeishuPlugin) -ne [string]::IsNullOrWhiteSpace($InputFeishuManifest)) {
  throw "Feishu plugin executable and manifest must be supplied together."
}
if (-not [string]::IsNullOrWhiteSpace($InputFeishuPlugin)) {
  $pluginInput = (Resolve-Path -LiteralPath $InputFeishuPlugin).ProviderPath
  $pluginManifestInput = (Resolve-Path -LiteralPath $InputFeishuManifest).ProviderPath
  $pluginHash = (Get-FileHash -LiteralPath $pluginInput -Algorithm SHA256).Hash.ToLowerInvariant()
  $pluginManifestData = Get-Content -Raw -Encoding UTF8 -LiteralPath $pluginManifestInput | ConvertFrom-Json
  if ([string]$pluginManifestData.sha256 -ne $pluginHash) {
    throw "Feishu plugin manifest SHA256 does not match the candidate executable."
  }
}
$install = [IO.Path]::GetFullPath($InstallDir)
$data = [IO.Path]::GetFullPath($DataDir)
$localRoot = [IO.Path]::GetFullPath($localAppData).TrimEnd('\') + '\'
$isLocalInstall = $install.StartsWith($localRoot, [StringComparison]::OrdinalIgnoreCase)
if (-not $isLocalInstall -and -not $AllowCustomUserWritableInstallDir) {
  throw "Per-user install directory must stay under LOCALAPPDATA unless -AllowCustomUserWritableInstallDir is explicitly supplied."
}
if (-not $isLocalInstall) {
  if (-not (Test-Path -LiteralPath $install -PathType Container)) {
    throw "Custom install directory must already exist so its current-user write access can be verified."
  }
  $writeProbe = Join-Path $install (".aha2-write-probe-{0}.tmp" -f [Guid]::NewGuid().ToString("N"))
  try {
    $stream = [IO.File]::Open($writeProbe, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    $stream.Dispose()
  } catch {
    throw "Custom install directory is not writable by the current user: $install"
  } finally {
    if (Test-Path -LiteralPath $writeProbe -PathType Leaf) { Remove-Item -LiteralPath $writeProbe -Force }
  }
}
$deploymentMode = if ($isLocalInstall) { "per-user" } else { "per-user-custom" }
if ($HealthTimeoutSeconds -lt 5 -or $HealthTimeoutSeconds -gt 300) {
  throw "HealthTimeoutSeconds must be between 5 and 300."
}
$listenAddress = $null
$listenPort = 0
$listenParts = $Listen.Split(':')
if ($listenParts.Count -ne 2 -or
    -not [Net.IPAddress]::TryParse($listenParts[0], [ref]$listenAddress) -or
    -not [int]::TryParse($listenParts[1], [ref]$listenPort) -or
    $listenPort -lt 1 -or $listenPort -gt 65535) {
  throw "AHA2 listen address is invalid."
}
$builder = Join-Path $repo "scripts\build-windows-installer.ps1"
if (-not (Test-Path -LiteralPath $builder -PathType Leaf)) { throw "Installer builder was not found." }

$installerDir = Join-Path $repo "dist\user-deploy-installer"
$installer = Join-Path $installerDir "AHA2-Setup-User-x64.exe"
$server = Join-Path $install "aha2.exe"
$tray = Join-Path $install "aha2-tray.exe"
$plugin = Join-Path $data "plugins\channels\feishu\aha2-channel-feishu.exe"
$pluginManifest = Join-Path $data "plugins\channels\feishu\plugin.json"
$pluginExisted = Test-Path -LiteralPath $plugin -PathType Leaf
$pluginManifestExisted = Test-Path -LiteralPath $pluginManifest -PathType Leaf
$registerTask = Join-Path $install "Register-AHA2UserTask.ps1"
if ($UpdateExistingInstallInPlace) {
  foreach ($required in @($server,$tray,$registerTask)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
      throw "Existing-install update requires an installed file: $required"
    }
  }
}

if ($ValidateOnly) {
  [pscustomobject]@{Mode=$deploymentMode;Repository=$repo;InstallDir=$install;DataDir=$data;Listen=$Listen;HealthURL=$HealthURL;WriteAccessVerified=$true;ElevationRequired=$false}
  return
}

if ($DetachedWorker) {
  Start-Transcript -LiteralPath (Join-Path $repo "dist\user-deploy.log") -Force | Out-Null
}

function Test-AHA2Ancestor {
  $current = $PID
  for ($depth = 0; $depth -lt 32 -and $current -gt 0; $depth++) {
    $process = Get-CimInstance Win32_Process -Filter "ProcessId=$current" -ErrorAction SilentlyContinue
    if (-not $process) { return $false }
    if ($process.Name -ieq "aha2.exe") { return $true }
    $current = [int]$process.ParentProcessId
  }
  return $false
}

function ConvertTo-CommandLineArgument([string]$Value) {
  if ($Value.Contains('"')) { throw "Scheduled deployment argument contains an unsupported quote." }
  return '"' + $Value + '"'
}

function Write-DeploymentResult([string]$Status, $Value) {
  if ([string]::IsNullOrWhiteSpace($ResultPath)) { return }
  $target = [IO.Path]::GetFullPath($ResultPath)
  $repoPrefix = $repo.TrimEnd('\') + '\'
  if (-not $target.StartsWith($repoPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Deployment result path must stay inside the repository."
  }
  New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
  @{Status=$Status;Value=$Value;FinishedAt=[DateTime]::UtcNow.ToString("o")} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $target -Encoding UTF8
}

if (-not $DetachedWorker -and (Test-AHA2Ancestor)) {
  if ([string]::IsNullOrWhiteSpace($ResultPath)) {
    $ResultPath = Join-Path $repo "dist\user-deploy-result.json"
  }
  $workerArguments = @(
    "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", $PSCommandPath,
    "-DetachedWorker", "-RepoPath", $repo, "-InputExe", $serverInput, "-InputTrayExe", $trayInput,
    "-InstallDir", $install, "-DataDir", $data, "-Version", $Version, "-TaskName", $TaskName,
    "-UpdateTaskName", $UpdateTaskName, "-Listen", $Listen, "-HealthURL", $HealthURL,
    "-HealthTimeoutSeconds", [string]$HealthTimeoutSeconds, "-ResultPath", $ResultPath
  )
  if ($AgentAPIURL) { $workerArguments += @("-AgentAPIURL", $AgentAPIURL) }
  if ($AllowInsecureAgentAPI) { $workerArguments += "-AllowInsecureAgentAPI" }
  if ($AllowCustomUserWritableInstallDir) { $workerArguments += "-AllowCustomUserWritableInstallDir" }
  if ($UpdateExistingInstallInPlace) { $workerArguments += "-UpdateExistingInstallInPlace" }
  if ($pluginInput -and $pluginManifestInput) {
    $workerArguments += @("-InputFeishuPlugin", $pluginInput, "-InputFeishuManifest", $pluginManifestInput)
  }
  $argumentLine = ($workerArguments | ForEach-Object { ConvertTo-CommandLineArgument ([string]$_) }) -join ' '
  $action = New-ScheduledTaskAction -Execute $powershellExecutable -Argument $argumentLine -WorkingDirectory $install
  $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
  $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 20) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
  Register-ScheduledTask -TaskName $UpdateTaskName -Action $action -Principal $principal -Settings $settings -Force | Out-Null
  Start-ScheduledTask -TaskName $UpdateTaskName
  [pscustomobject]@{Mode=$deploymentMode;State="scheduled";Result=$ResultPath;WriteAccessVerified=$true;ElevationRequired=$false}
  return
}

function Wait-AHA2Health {
  $deadline = [DateTime]::UtcNow.AddSeconds($HealthTimeoutSeconds)
  do {
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $HealthURL -TimeoutSec 3
      if ($response.StatusCode -eq 200) { return }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "AHA2 health check did not pass."
}

function Get-AHA2ProcessTree {
  $processes = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)
  $targets = @{}
  foreach ($process in $processes) {
    if ($process.Name -ieq "aha2.exe" -or $process.Name -ieq "aha2-tray.exe") {
      $targets[[int]$process.ProcessId] = [pscustomobject]@{Process=$process;Depth=0}
    }
  }
  do {
    $added = $false
    foreach ($process in $processes) {
      $processId = [int]$process.ProcessId
      $parentId = [int]$process.ParentProcessId
      if (-not $targets.ContainsKey($processId) -and $targets.ContainsKey($parentId)) {
        $targets[$processId] = [pscustomobject]@{Process=$process;Depth=([int]$targets[$parentId].Depth + 1)}
        $added = $true
      }
    }
  } while ($added)
  @($targets.Values | ForEach-Object {
    [pscustomobject]@{
      Id = [int]$_.Process.ProcessId
      Name = [string]$_.Process.Name
      CreationDate = [string]$_.Process.CreationDate
      Depth = [int]$_.Depth
    }
  })
}

function Test-AHA2ProcessIdentity($Target) {
  $current = Get-CimInstance Win32_Process -Filter "ProcessId=$($Target.Id)" -ErrorAction SilentlyContinue
  return $current -and
    $current.Name -ieq $Target.Name -and
    [string]$current.CreationDate -eq $Target.CreationDate
}

function Invoke-TaskKillNoThrow([int]$ProcessId) {
  $previousPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = "Continue"
    $output = @(& $taskkillExecutable /PID $ProcessId /T /F 2>&1)
    $exitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $previousPreference
  }
  if ($exitCode -ne 0) {
    Write-Warning ("taskkill failed for PID {0} with code {1}: {2}" -f $ProcessId,$exitCode,($output -join " "))
  }
}

function Stop-AHA2ProcessTrees {
  $targets = @(Get-AHA2ProcessTree)
  foreach ($root in @($targets | Where-Object Depth -eq 0)) {
    Invoke-TaskKillNoThrow $root.Id
  }

  $deadline = [DateTime]::UtcNow.AddSeconds(10)
  do {
    $remaining = @($targets | Where-Object { Test-AHA2ProcessIdentity $_ })
    if ($remaining.Count -eq 0) { break }
    foreach ($target in @($remaining | Sort-Object Depth -Descending)) {
      Stop-Process -Id $target.Id -Force -ErrorAction SilentlyContinue
    }
    Start-Sleep -Milliseconds 250
  } while ([DateTime]::UtcNow -lt $deadline)

  $remaining = @($targets | Where-Object { Test-AHA2ProcessIdentity $_ })
  if ($remaining.Count -gt 0) {
    $description = ($remaining | ForEach-Object { "$($_.Name) PID $($_.Id)" }) -join ", "
    throw "AHA2 processes could not be stopped within 10 seconds: $description"
  }
  Start-Sleep -Milliseconds 500
}

if (-not $UpdateExistingInstallInPlace) {
  $buildArgs = @("-ExecutionPolicy","Bypass","-File",$builder,"-RepoPath",$repo,"-InputExe",$serverInput,"-InputTrayExe",$trayInput,"-OutputDir",$installerDir,"-Version",$Version,"-PerUser")
  if ($InputFeishuPlugin -and $InputFeishuManifest) {
    $buildArgs += @("-InputFeishuPlugin",$InputFeishuPlugin,"-InputFeishuManifest",$InputFeishuManifest)
  }
  & $powershellExecutable @buildArgs
  if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $installer -PathType Leaf)) { throw "Per-user installer build failed." }
}

$stamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
$backup = Join-Path $data "backups\user-installed-$stamp"
New-Item -ItemType Directory -Path $backup -Force | Out-Null
$previousTaskXML = ""
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  $previousTaskXML = Export-ScheduledTask -TaskName $TaskName
  [IO.File]::WriteAllText((Join-Path $backup "AHA2-User-Task.xml"), $previousTaskXML, [Text.UTF8Encoding]::new($false))
}

try {
  New-Item -ItemType Directory -Path $data -Force | Out-Null
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Stop-AHA2ProcessTrees
  foreach ($name in @("aha2.db","aha2.db-wal","aha2.db-shm","secrets.json","setup-token")) {
    $source = Join-Path $data $name
    if (Test-Path -LiteralPath $source -PathType Leaf) { Copy-Item -LiteralPath $source -Destination (Join-Path $backup $name) -Force }
  }
  foreach ($entry in @(@{Path=$server;Name="aha2.exe"},@{Path=$tray;Name="aha2-tray.exe"},@{Path=$plugin;Name="aha2-channel-feishu.exe"},@{Path=$pluginManifest;Name="feishu-plugin.json"})) {
    if (Test-Path -LiteralPath $entry.Path -PathType Leaf) { Copy-Item -LiteralPath $entry.Path -Destination (Join-Path $backup $entry.Name) -Force }
  }
  if ($UpdateExistingInstallInPlace) {
    Copy-Item -LiteralPath $serverInput -Destination $server -Force
    Copy-Item -LiteralPath $trayInput -Destination $tray -Force
    if ($pluginInput -and $pluginManifestInput) {
      New-Item -ItemType Directory -Path (Split-Path -Parent $plugin) -Force | Out-Null
      Copy-Item -LiteralPath $pluginInput -Destination $plugin -Force
      Copy-Item -LiteralPath $pluginManifestInput -Destination $pluginManifest -Force
    }
  } else {
    $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/CLOSEAPPLICATIONS','/SKIPUSERTASK=1',('/DIR="{0}"' -f $install),('/DATADIR="{0}"' -f $data))
    $process = Start-Process -FilePath $installer -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw "Per-user installer exited with code $($process.ExitCode)." }
  }
  if (-not (Test-Path -LiteralPath $registerTask -PathType Leaf)) { throw "Installed task registration script is missing." }
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Stop-AHA2ProcessTrees
  $registerArguments = @{
    TrayExecutable = $tray
    ServerExecutable = $server
    Listen = $Listen
    DataDir = $data
    TaskName = $TaskName
    Start = $true
  }
  if ($AgentAPIURL) { $registerArguments.AgentAPIURL = $AgentAPIURL }
  if ($AllowInsecureAgentAPI) { $registerArguments.AllowInsecureAgentAPI = $true }
  & $registerTask @registerArguments
  Wait-AHA2Health

  $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction Stop
  $action = $task.Actions | Select-Object -First 1
  if (-not $action -or -not ([IO.Path]::GetFullPath($action.Execute)).Equals([IO.Path]::GetFullPath($tray), [StringComparison]::OrdinalIgnoreCase)) {
    throw "Per-user login task does not target the installed tray."
  }

  $serverHash = (Get-FileHash -LiteralPath $server -Algorithm SHA256).Hash
  $trayHash = (Get-FileHash -LiteralPath $tray -Algorithm SHA256).Hash
  if ($serverHash -ne (Get-FileHash -LiteralPath $serverInput -Algorithm SHA256).Hash -or $trayHash -ne (Get-FileHash -LiteralPath $trayInput -Algorithm SHA256).Hash) {
    throw "Installed per-user binary hash does not match the candidate."
  }
  $installedPluginHash = ""
  if ($pluginInput -and $pluginManifestInput) {
    $installedPluginHash = (Get-FileHash -LiteralPath $plugin -Algorithm SHA256).Hash
    if ($installedPluginHash -ne (Get-FileHash -LiteralPath $pluginInput -Algorithm SHA256).Hash) {
      throw "Installed Feishu plugin hash does not match the candidate."
    }
  }
  $deploymentResult = [pscustomobject]@{Mode=$deploymentMode;UpdateType=$(if ($UpdateExistingInstallInPlace) { "existing-install-in-place" } else { "installer" });Health="ok";URL=$HealthURL;Installer=$(if ($UpdateExistingInstallInPlace) { "" } else { $installer });Backup=$backup;ServerSHA256=$serverHash;TraySHA256=$trayHash;PluginSHA256=$installedPluginHash;WriteAccessVerified=$true;ElevationRequired=$false}
  Write-DeploymentResult "succeeded" $deploymentResult
  if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
  $deploymentResult
} catch {
  $deploymentError = $_
  Write-DeploymentResult "failed" $deploymentError.Exception.Message
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Stop-AHA2ProcessTrees
  foreach ($entry in @(@{Path=$server;Name="aha2.exe"},@{Path=$tray;Name="aha2-tray.exe"},@{Path=$plugin;Name="aha2-channel-feishu.exe"},@{Path=$pluginManifest;Name="feishu-plugin.json"})) {
    $backupFile = Join-Path $backup $entry.Name
    if (Test-Path -LiteralPath $backupFile -PathType Leaf) { Copy-Item -LiteralPath $backupFile -Destination $entry.Path -Force }
  }
  if ($pluginInput -and -not $pluginExisted) { Remove-Item -LiteralPath $plugin -Force -ErrorAction SilentlyContinue }
  if ($pluginManifestInput -and -not $pluginManifestExisted) { Remove-Item -LiteralPath $pluginManifest -Force -ErrorAction SilentlyContinue }
  foreach ($name in @("aha2.db","aha2.db-wal","aha2.db-shm","secrets.json","setup-token")) {
    $backupFile = Join-Path $backup $name
    if (Test-Path -LiteralPath $backupFile -PathType Leaf) { Copy-Item -LiteralPath $backupFile -Destination (Join-Path $data $name) -Force }
  }
  if ($previousTaskXML) {
    if (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue)) {
      Register-ScheduledTask -TaskName $TaskName -Xml $previousTaskXML -Force | Out-Null
    }
    Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  }
  if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
  throw "$($deploymentError.Exception.Message) Backup: $backup"
}
