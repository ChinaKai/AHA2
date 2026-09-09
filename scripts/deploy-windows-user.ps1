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
  [string]$HealthURL = "http://127.0.0.1:8766/healthz",
  [int]$HealthTimeoutSeconds = 60,
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
$builder = Join-Path $repo "scripts\build-windows-installer.ps1"
if (-not (Test-Path -LiteralPath $builder -PathType Leaf)) { throw "Installer builder was not found." }

$installerDir = Join-Path $repo "dist\user-deploy-installer"
$installer = Join-Path $installerDir "AHA2-Setup-User-x64.exe"
$server = Join-Path $install "aha2.exe"
$tray = Join-Path $install "aha2-tray.exe"
$plugin = Join-Path $data "plugins\channels\feishu\aha2-channel-feishu.exe"

if ($ValidateOnly) {
  [pscustomobject]@{Mode="per-user";Repository=$repo;InstallDir=$install;DataDir=$data;HealthURL=$HealthURL;ElevationRequired=$false}
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

$buildArgs = @("-ExecutionPolicy","Bypass","-File",$builder,"-RepoPath",$repo,"-InputExe",$serverInput,"-InputTrayExe",$trayInput,"-OutputDir",$installerDir,"-Version",$Version,"-PerUser")
if ($InputFeishuPlugin -and $InputFeishuManifest) {
  $buildArgs += @("-InputFeishuPlugin",$InputFeishuPlugin,"-InputFeishuManifest",$InputFeishuManifest)
}
& powershell.exe @buildArgs
if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $installer -PathType Leaf)) { throw "Per-user installer build failed." }

$stamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
$backup = Join-Path $data "backups\user-installed-$stamp"
New-Item -ItemType Directory -Path $backup -Force | Out-Null
foreach ($name in @("aha2.db","aha2.db-wal","aha2.db-shm","secrets.json","setup-token")) {
  $source = Join-Path $data $name
  if (Test-Path -LiteralPath $source -PathType Leaf) { Copy-Item -LiteralPath $source -Destination (Join-Path $backup $name) -Force }
}
foreach ($entry in @(@{Path=$server;Name="aha2.exe"},@{Path=$tray;Name="aha2-tray.exe"},@{Path=$plugin;Name="aha2-channel-feishu.exe"})) {
  if (Test-Path -LiteralPath $entry.Path -PathType Leaf) { Copy-Item -LiteralPath $entry.Path -Destination (Join-Path $backup $entry.Name) -Force }
}
$previousTaskXML = ""
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  $previousTaskXML = Export-ScheduledTask -TaskName $TaskName
  [IO.File]::WriteAllText((Join-Path $backup "AHA2-User-Task.xml"), $previousTaskXML, [Text.UTF8Encoding]::new($false))
}

try {
  $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/CLOSEAPPLICATIONS',('/DIR="{0}"' -f $install),('/DATADIR="{0}"' -f $data))
  $process = Start-Process -FilePath $installer -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
  if ($process.ExitCode -ne 0) { throw "Per-user installer exited with code $($process.ExitCode)." }
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
  [pscustomobject]@{Mode="per-user";Health="ok";URL=$HealthURL;Installer=$installer;Backup=$backup;ServerSHA256=$serverHash;TraySHA256=$trayHash;ElevationRequired=$false}
} catch {
  $deploymentError = $_
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Get-Process -Name "aha2-tray","aha2" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
  foreach ($entry in @(@{Path=$server;Name="aha2.exe"},@{Path=$tray;Name="aha2-tray.exe"},@{Path=$plugin;Name="aha2-channel-feishu.exe"})) {
    $backupFile = Join-Path $backup $entry.Name
    if (Test-Path -LiteralPath $backupFile -PathType Leaf) { Copy-Item -LiteralPath $backupFile -Destination $entry.Path -Force }
  }
  foreach ($name in @("aha2.db","aha2.db-wal","aha2.db-shm","secrets.json","setup-token")) {
    $backupFile = Join-Path $backup $name
    if (Test-Path -LiteralPath $backupFile -PathType Leaf) { Copy-Item -LiteralPath $backupFile -Destination (Join-Path $data $name) -Force }
  }
  if ($previousTaskXML) {
    Register-ScheduledTask -TaskName $TaskName -Xml $previousTaskXML -Force | Out-Null
    Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  }
  throw "$($deploymentError.Exception.Message) Backup: $backup"
}
