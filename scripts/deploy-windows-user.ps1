[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$InputExe,
  [Parameter(Mandatory = $true)][string]$InputTrayExe,
  [string]$InputFeishuPlugin = "",
  [string]$InputFeishuManifest = "",
  [string]$RepoPath = (Split-Path -Parent $PSScriptRoot),
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\AHA2"),
  [string]$DataDir = (Join-Path $env:LOCALAPPDATA "AHA2"),
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
  [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = (Resolve-Path -LiteralPath $RepoPath).Path
$serverInput = (Resolve-Path -LiteralPath $InputExe).Path
$trayInput = (Resolve-Path -LiteralPath $InputTrayExe).Path
$install = [IO.Path]::GetFullPath($InstallDir)
$data = [IO.Path]::GetFullPath($DataDir)
$localRoot = [IO.Path]::GetFullPath($env:LOCALAPPDATA).TrimEnd('\') + '\'
if (-not $install.StartsWith($localRoot, [StringComparison]::OrdinalIgnoreCase)) {
  throw "Per-user install directory must stay under LOCALAPPDATA."
}
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

if ($ValidateOnly) {
  [pscustomobject]@{Mode="per-user";Repository=$repo;InstallDir=$install;DataDir=$data;Listen=$Listen;HealthURL=$HealthURL;ElevationRequired=$false}
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
  if ($InputFeishuPlugin -and $InputFeishuManifest) {
    $workerArguments += @("-InputFeishuPlugin", (Resolve-Path -LiteralPath $InputFeishuPlugin).Path, "-InputFeishuManifest", (Resolve-Path -LiteralPath $InputFeishuManifest).Path)
  }
  $argumentLine = ($workerArguments | ForEach-Object { ConvertTo-CommandLineArgument ([string]$_) }) -join ' '
  $powershell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
  $action = New-ScheduledTaskAction -Execute $powershell -Argument $argumentLine -WorkingDirectory $repo
  $principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
  $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 20) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
  Register-ScheduledTask -TaskName $UpdateTaskName -Action $action -Principal $principal -Settings $settings -Force | Out-Null
  Start-ScheduledTask -TaskName $UpdateTaskName
  [pscustomobject]@{Mode="per-user";State="scheduled";Result=$ResultPath;ElevationRequired=$false}
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
    $output = @(& taskkill.exe /PID $ProcessId /T /F 2>&1)
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

$buildArgs = @("-ExecutionPolicy","Bypass","-File",$builder,"-RepoPath",$repo,"-InputExe",$serverInput,"-InputTrayExe",$trayInput,"-OutputDir",$installerDir,"-Version",$Version,"-PerUser")
if ($InputFeishuPlugin -and $InputFeishuManifest) {
  $buildArgs += @("-InputFeishuPlugin",$InputFeishuPlugin,"-InputFeishuManifest",$InputFeishuManifest)
}
& powershell.exe @buildArgs
if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $installer -PathType Leaf)) { throw "Per-user installer build failed." }

$stamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
$backup = Join-Path $data "backups\user-installed-$stamp"
New-Item -ItemType Directory -Path $backup -Force | Out-Null
$previousTaskXML = ""
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  $previousTaskXML = Export-ScheduledTask -TaskName $TaskName
  [IO.File]::WriteAllText((Join-Path $backup "AHA2-User-Task.xml"), $previousTaskXML, [Text.UTF8Encoding]::new($false))
}

try {
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Stop-AHA2ProcessTrees
  foreach ($name in @("aha2.db","aha2.db-wal","aha2.db-shm","secrets.json","setup-token")) {
    $source = Join-Path $data $name
    if (Test-Path -LiteralPath $source -PathType Leaf) { Copy-Item -LiteralPath $source -Destination (Join-Path $backup $name) -Force }
  }
  foreach ($entry in @(@{Path=$server;Name="aha2.exe"},@{Path=$tray;Name="aha2-tray.exe"},@{Path=$plugin;Name="aha2-channel-feishu.exe"})) {
    if (Test-Path -LiteralPath $entry.Path -PathType Leaf) { Copy-Item -LiteralPath $entry.Path -Destination (Join-Path $backup $entry.Name) -Force }
  }
  $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/CLOSEAPPLICATIONS','/SKIPUSERTASK=1',('/DIR="{0}"' -f $install),('/DATADIR="{0}"' -f $data))
  $process = Start-Process -FilePath $installer -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
  if ($process.ExitCode -ne 0) { throw "Per-user installer exited with code $($process.ExitCode)." }
  $registerTask = Join-Path $install "Register-AHA2UserTask.ps1"
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
  $deploymentResult = [pscustomobject]@{Mode="per-user";Health="ok";URL=$HealthURL;Installer=$installer;Backup=$backup;ServerSHA256=$serverHash;TraySHA256=$trayHash;ElevationRequired=$false}
  Write-DeploymentResult "succeeded" $deploymentResult
  if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
  $deploymentResult
} catch {
  $deploymentError = $_
  Write-DeploymentResult "failed" $deploymentError.Exception.Message
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Stop-AHA2ProcessTrees
  foreach ($entry in @(@{Path=$server;Name="aha2.exe"},@{Path=$tray;Name="aha2-tray.exe"},@{Path=$plugin;Name="aha2-channel-feishu.exe"})) {
    $backupFile = Join-Path $backup $entry.Name
    if (Test-Path -LiteralPath $backupFile -PathType Leaf) { Copy-Item -LiteralPath $backupFile -Destination $entry.Path -Force }
  }
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
