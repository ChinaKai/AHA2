$ErrorActionPreference = 'Stop'
Write-Host '=== PROCESSES ==='
Get-CimInstance Win32_Process -Filter "Name='aha2.exe' or Name='aha2-tray.exe' or Name='aha2-channel-feishu.exe'" |
  Select-Object ProcessId,Name,ExecutablePath,CreationDate | Format-List
Write-Host '=== TASK ==='
$task = Get-ScheduledTask -TaskName 'AHA2 User' -ErrorAction SilentlyContinue
if ($null -eq $task) { Write-Host 'MISSING AHA2 User' } else {
  $task.Principal | Select-Object UserId,RunLevel,LogonType | Format-List
  $task.Actions | Select-Object Execute,Arguments,WorkingDirectory | Format-List
  Write-Host ("State=" + $task.State)
}
Write-Host '=== HEALTH ==='
try {
  $response = Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 'http://127.0.0.1:8766/healthz'
  Write-Host ("HTTP=" + [int]$response.StatusCode)
  Write-Host $response.Content
} catch {
  Write-Host ("HEALTH_ERROR=" + $_.Exception.Message)
}
Write-Host '=== VERSION ==='
if (Test-Path -LiteralPath 'D:\Program Files\AHA2\aha2.exe') {
  & 'D:\Program Files\AHA2\aha2.exe' version
  Get-FileHash -Algorithm SHA256 -LiteralPath 'D:\Program Files\AHA2\aha2.exe','D:\Program Files\AHA2\aha2-tray.exe' |
    Select-Object Path,Hash | Format-List
} else {
  Write-Host 'MISSING D:\Program Files\AHA2\aha2.exe'
}
Write-Host '=== PORT ==='
Get-NetTCPConnection -LocalPort 8766 -State Listen -ErrorAction SilentlyContinue |
  Select-Object LocalAddress,LocalPort,OwningProcess | Format-List
