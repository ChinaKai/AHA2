param(
  [Parameter(Mandatory = $true)][string]$Executable,
  [int]$Port = 18767,
  [switch]$KeepData
)

$ErrorActionPreference = "Stop"
$binary = (Resolve-Path -LiteralPath $Executable).Path
$runID = [Guid]::NewGuid().ToString("N")
$dataDir = Join-Path ([IO.Path]::GetTempPath()) "AHA2-agent-api-smoke\$runID"
$baseURL = "http://127.0.0.1:$Port"
New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
$process = Start-Process -FilePath $binary `
  -ArgumentList @("serve", "--listen", "127.0.0.1:$Port", "--data-dir", $dataDir, "--agent-api-url", $baseURL) `
  -PassThru -WindowStyle Hidden

try {
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  $health = $null
  do {
    try { $health = Invoke-RestMethod -Uri "$baseURL/healthz" -TimeoutSec 2 } catch { Start-Sleep -Milliseconds 300 }
  } while (-not $health.ok -and [DateTime]::UtcNow -lt $deadline)
  if (-not $health.ok) { throw "Temporary AHA2 health check failed." }
  $status = 0
  try { Invoke-WebRequest -UseBasicParsing -Uri "$baseURL/api/v1/agent/capabilities" -TimeoutSec 3 | Out-Null } catch { $status = [int]$_.Exception.Response.StatusCode }
  if ($status -ne 401) { throw "Agent API anonymous request returned $status instead of 401." }
  [pscustomobject]@{ Health = "ok"; AgentAPIAnonymousStatus = $status; Port = $Port }
} finally {
  if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force; $process.WaitForExit() }
  if (-not $KeepData -and (Test-Path -LiteralPath $dataDir)) {
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    $resolved = [IO.Path]::GetFullPath($dataDir)
    if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) { throw "Refusing cleanup outside TEMP." }
    Remove-Item -LiteralPath $resolved -Recurse -Force
  }
}
