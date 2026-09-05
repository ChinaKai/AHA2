[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$BinaryPath,
  [string]$RepoPath = (Get-Location).Path,
  [string]$ManagedCwd = "",
  [string]$DataDir = (Join-Path $env:LOCALAPPDATA "AHA2"),
  [string]$AhaCLI = (Join-Path $env:LOCALAPPDATA "AHA\aha"),
  [string]$AhaHome = $env:AHA_HOME,
  [string]$Python = "python",
  [string]$ServiceName = "aha2-v1",
  [string]$Listen = "0.0.0.0:8766",
  [string]$HealthURL = "http://127.0.0.1:8766/healthz",
  [string]$AgentAPIURL = "",
  [string]$RunID = $env:AHA_RUN_ID,
  [string]$TaskID = $env:AHA_TASK_ID,
  [string]$AgentID = $env:AHA_AGENT_ID,
  [int]$HealthTimeoutSeconds = 45,
  [bool]$AllowCrossOrigin = $true,
  [switch]$AllowInsecureAgentAPI,
  [switch]$Worker,
  [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
$repository = (Resolve-Path -LiteralPath $RepoPath).Path
$managedDirectory = if ($ManagedCwd) { (Resolve-Path -LiteralPath $ManagedCwd).Path } else { $repository }
$candidate = (Resolve-Path -LiteralPath $BinaryPath).Path
$data = [IO.Path]::GetFullPath($DataDir)
$cli = [IO.Path]::GetFullPath($AhaCLI)
$stable = Join-Path $repository "dist\aha2-windows-amd64.exe"

if (-not (Test-Path -LiteralPath (Join-Path $repository "go.mod"))) { throw "AHA2 go.mod was not found." }
if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) { throw "Candidate binary was not found." }
if (-not (Test-Path -LiteralPath $cli -PathType Leaf)) { throw "Host AHA CLI was not found: $cli" }
if ($HealthTimeoutSeconds -lt 5 -or $HealthTimeoutSeconds -gt 300) { throw "HealthTimeoutSeconds must be between 5 and 300." }

$scopeArgs = @()
if ($RunID) { $scopeArgs += @("--run-id", $RunID) }
if ($TaskID) { $scopeArgs += @("--task-id", $TaskID) }
if ($AgentID) { $scopeArgs += @("--agent-id", $AgentID) }

if ($ValidateOnly) {
  [pscustomobject]@{ Repository = $repository; ManagedCwd = $managedDirectory; Candidate = $candidate; DataDir = $data; AhaCLI = $cli; ServiceName = $ServiceName; HealthURL = $HealthURL; Worker = [bool]$Worker }
  return
}

function Invoke-ManagedProcess([string[]]$Arguments, [switch]$AllowFailure) {
  $rootArgs = @()
  if ($AhaHome) { $rootArgs += @("--home", $AhaHome) }
  & $Python $cli @rootArgs managed-process @Arguments
  $code = $LASTEXITCODE
  if ($code -ne 0 -and -not $AllowFailure) { throw "aha managed-process failed with exit code $code." }
  return $code
}

function Start-AHA2([string]$Executable) {
  $arguments = @("start") + $scopeArgs + @("--cwd", $managedDirectory, $ServiceName, "--", $Executable, "serve", "--listen", $Listen, "--data-dir", $data)
  if ($AllowCrossOrigin) { $arguments += "--allow-cross-origin" }
  if ($AgentAPIURL) { $arguments += @("--agent-api-url", $AgentAPIURL) }
  if ($AllowInsecureAgentAPI) { $arguments += "--allow-insecure-agent-api" }
  Invoke-ManagedProcess $arguments | Out-Null
}

function Wait-AHA2Health {
  $deadline = [DateTime]::UtcNow.AddSeconds($HealthTimeoutSeconds)
  do {
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $HealthURL -TimeoutSec 3
      if ($response.StatusCode -eq 200) { return }
    } catch {
      Start-Sleep -Milliseconds 500
    }
  } while ([DateTime]::UtcNow -lt $deadline)
  throw "AHA2 health check did not pass within $HealthTimeoutSeconds seconds: $HealthURL"
}

if (-not $Worker) {
  $workerName = "aha2-deploy-" + [DateTime]::UtcNow.ToString("yyyyMMddHHmmss")
  $workerCommand = @(
    "powershell.exe", "-ExecutionPolicy", "Bypass", "-File", $PSCommandPath, "-Worker",
    "-BinaryPath", $candidate, "-RepoPath", $repository, "-ManagedCwd", $managedDirectory, "-DataDir", $data,
    "-AhaCLI", $cli, "-AhaHome", $AhaHome, "-Python", $Python, "-ServiceName", $ServiceName,
    "-Listen", $Listen, "-HealthURL", $HealthURL, "-HealthTimeoutSeconds", [string]$HealthTimeoutSeconds
  )
  if ($AgentAPIURL) { $workerCommand += @("-AgentAPIURL", $AgentAPIURL) }
  if ($AllowInsecureAgentAPI) { $workerCommand += "-AllowInsecureAgentAPI" }
  if ($RunID) { $workerCommand += @("-RunID", $RunID) }
  if ($TaskID) { $workerCommand += @("-TaskID", $TaskID) }
  if ($AgentID) { $workerCommand += @("-AgentID", $AgentID) }
  $arguments = @("start") + $scopeArgs + @("--cwd", $managedDirectory, $workerName, "--") + $workerCommand
  Invoke-ManagedProcess $arguments | Out-Null
  [pscustomobject]@{ Worker = $workerName; Service = $ServiceName; Status = "submitted" }
  return
}

$stopArguments = @("stop") + $scopeArgs + @($ServiceName)
Invoke-ManagedProcess $stopArguments -AllowFailure | Out-Null

$timestamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
$backupDir = Join-Path $data "backups\$timestamp"
New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
$runtimeFiles = @("aha2.db", "aha2.db-wal", "aha2.db-shm", "secrets.json", "setup-token")
foreach ($name in $runtimeFiles) {
  $source = Join-Path $data $name
  if (Test-Path -LiteralPath $source -PathType Leaf) { Copy-Item -LiteralPath $source -Destination (Join-Path $backupDir $name) -Force }
}
$binaryBackup = ""
if (Test-Path -LiteralPath $stable -PathType Leaf) {
  $binaryBackup = Join-Path $backupDir "aha2-windows-amd64.exe"
  Copy-Item -LiteralPath $stable -Destination $binaryBackup -Force
}
Copy-Item -LiteralPath $candidate -Destination $stable -Force

try {
  Start-AHA2 $stable
  Wait-AHA2Health
  $hash = (Get-FileHash -LiteralPath $stable -Algorithm SHA256).Hash
  [pscustomobject]@{ Service = $ServiceName; Health = "ok"; URL = $HealthURL; Backup = $backupDir; SHA256 = $hash }
} catch {
  $deploymentError = $_
  Invoke-ManagedProcess $stopArguments -AllowFailure | Out-Null
  if ($binaryBackup -and (Test-Path -LiteralPath $binaryBackup -PathType Leaf)) {
    Copy-Item -LiteralPath $binaryBackup -Destination $stable -Force
    foreach ($name in $runtimeFiles) {
      $backupFile = Join-Path $backupDir $name
      if (Test-Path -LiteralPath $backupFile -PathType Leaf) { Copy-Item -LiteralPath $backupFile -Destination (Join-Path $data $name) -Force }
    }
    try { Start-AHA2 $stable; Wait-AHA2Health } catch { Write-Warning "Rollback restart failed: $($_.Exception.Message)" }
  }
  throw $deploymentError
}
