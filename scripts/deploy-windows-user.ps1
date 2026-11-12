[CmdletBinding()]
param(
  [string]$InputExe = "",
  [string]$InputTrayExe = "",
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
	# Deprecated no-op, kept for callers written before the security convergence.
	[switch]$AllowInsecureAgentAPI,
  [string]$HealthURL = "http://127.0.0.1:8766/healthz",
  [int]$HealthTimeoutSeconds = 60,
  # How long the stop stage keeps retrying process exit and exclusive file
  # probes before it refuses to touch the installation.
  [int]$StopTimeoutSeconds = 45,
  # How long the non-detached handoff watches the scheduled worker before it
  # reports "scheduled". A worker that exits earlier without a result is a
  # failure, not a successful handoff.
  [int]$HandoffTimeoutSeconds = 8,
  [string]$ResultPath = "",
  [switch]$DetachedWorker,
  [switch]$AllowCustomUserWritableInstallDir,
  [switch]$UpdateExistingInstallInPlace,
  [switch]$ValidateOnly,
  # Runs platform-independent checks for the stop/backup/rollback helpers and
  # exits without touching any installed file, process, task, or database.
  [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$global:LASTEXITCODE = 0

$systemRoot = if ([string]::IsNullOrWhiteSpace($env:SystemRoot)) { "C:\Windows" } else { $env:SystemRoot }
$powershellExecutable = ""
$taskkillExecutable = ""

$aha2ProcessNames = @("aha2.exe", "aha2-tray.exe", "aha2-channel-feishu.exe")
# Only durable database files are backed up. aha2.db-shm is a volatile
# shared-memory cache that SQLite recreates; it is sized during preflight and
# deleted during restore, but never copied or hashed. Hashing it while SQLite
# still touched it aborted the 0.5.471 backup stage.
$databaseBackupFileNames = @("aha2.db", "aha2.db-wal")

# Deployment preflight allowance: binaries and plugins need about 200 MB in the
# install directory, the installer is staged in the temp directory (300 MB), and
# another 1 GiB absorbs WAL growth and transient writes on the data volume.
$aha2InstallSpaceAllowanceBytes = [long](200MB)
$aha2TempSpaceAllowanceBytes = [long](300MB)
$aha2DiskSafetyMarginBytes = [long](1GB)

# ---------------------------------------------------------------------------
# Pure helpers. These do not call Windows-only cmdlets and are exercised by
# -SelfTest, so the stop/backup/rollback policy can be checked on any host.
# ---------------------------------------------------------------------------

function Test-AHA2StopSatisfied {
  param($RemainingProcesses, $LockedFiles)
  return (@($RemainingProcesses).Count -eq 0) -and (@($LockedFiles).Count -eq 0)
}

function Get-AHA2StopAttemptDecision {
  param([bool]$Satisfied, [bool]$TimedOut)
  if ($Satisfied) { return "verified" }
  if ($TimedOut) { return "timeout" }
  return "retry"
}

function Get-AHA2ScriptEncodingState {
  param([string]$Path)
  # Windows PowerShell 5.1 decodes a script without a UTF-8 BOM using the
  # system ANSI code page. Non-ASCII text (for example the Chinese diagnostics
  # added in task-308) then turns into mojibake and can break parsing before
  # any statement runs. A download or tar step that drops the BOM is therefore
  # fatal for the scheduled worker, so the handoff checks this before handing
  # the same file to a different PowerShell host.
  $bytes = [IO.File]::ReadAllBytes($Path)
  $hasBom = $bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
  $hasNonAscii = $false
  foreach ($byte in $bytes) {
    if ($byte -gt 0x7F) { $hasNonAscii = $true; break }
  }
  return [pscustomobject]@{
    HasBOM = $hasBom
    HasNonAscii = $hasNonAscii
    Ok = ($hasBom -or -not $hasNonAscii)
  }
}

function Assert-AHA2WindowsPowerShellScriptEncoding {
  param([string]$Path)
  $state = Get-AHA2ScriptEncodingState -Path $Path
  if (-not $state.Ok) {
    throw ("Refusing to hand {0} to Windows PowerShell 5.1: it is UTF-8 without a BOM and contains non-ASCII bytes. PowerShell 5.1 reads such a file using the ANSI code page and can fail with a parser error before any statement runs. Save it as UTF-8 with BOM." -f $Path)
  }
  return $state
}

function Get-AHA2UtcTimestamp {
  param($Value)
  # Get-ScheduledTaskInfo.LastRunTime is a local DateTime while the handoff
  # starts from [DateTime]::UtcNow. Comparing their ticks directly shifts the
  # result by the local UTC offset, so normalize every input first.
  if ($null -eq $Value) { return [DateTime]::MinValue }
  if ($Value -is [DateTimeOffset]) { return $Value.UtcDateTime }
  $timestamp = [DateTime]$Value
  if ($timestamp -eq [DateTime]::MinValue) { return $timestamp }
  if ($timestamp.Kind -eq [DateTimeKind]::Utc) { return $timestamp }
  return $timestamp.ToUniversalTime()
}

function Test-AHA2WorkerStartedSince {
  param($LastRunTime, [DateTime]$StartUtc, [int]$SlackSeconds = 2)
  $lastRunUtc = Get-AHA2UtcTimestamp -Value $LastRunTime
  if ($lastRunUtc -eq [DateTime]::MinValue) { return $false }
  return ($lastRunUtc -ge $StartUtc.AddSeconds(-$SlackSeconds))
}

function Resolve-AHA2DetachedHandoff {
  param([bool]$ResultWritten, [bool]$WorkerStarted, [bool]$WorkerRunning)
  if ($ResultWritten) { return "completed" }
  if ($WorkerStarted -and -not $WorkerRunning) { return "failed" }
  return "scheduled"
}

function Get-AHA2LogTail {
  param([string]$Path, [int]$LineCount = 20)
  if ([string]::IsNullOrWhiteSpace($Path)) { return "" }
  if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return "" }
  try {
    return (@(Get-Content -LiteralPath $Path -Tail $LineCount -ErrorAction Stop) -join [Environment]::NewLine)
  } catch {
    return ""
  }
}

function Assert-AHA2DetachedWorkerAlive {
  param($TaskInfo, [string]$LogPath)
  $lastResult = if ($null -ne $TaskInfo) { [string]$TaskInfo.LastTaskResult } else { "unknown" }
  $tail = Get-AHA2LogTail -Path $LogPath
  $message = "Deployment worker exited before writing a result (LastTaskResult=$lastResult)."
  if ([string]::IsNullOrWhiteSpace($tail)) {
    $message += " No worker log was written, so the worker probably failed before running (for example a parser error)."
  } else {
    $message += " stderr/log tail:" + [Environment]::NewLine + $tail
  }
  throw $message
}

function Get-AHA2FileSetBytes {
  param([string[]]$Paths)
  $total = [long]0
  foreach ($path in @($Paths)) {
    if ([string]::IsNullOrWhiteSpace($path)) { continue }
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { continue }
    $total += [long](Get-Item -LiteralPath $path -Force).Length
  }
  return $total
}

function Get-AHA2VolumeRoot {
  param([string]$Path)
  return [string][IO.Path]::GetPathRoot([IO.Path]::GetFullPath($Path))
}

function Get-AHA2VolumeFreeBytes {
  param(
    [string]$Volume,
    # Injection point for the self-test; production reads the real volume.
    [scriptblock]$FreeSpaceProvider = $null
  )
  if ($null -ne $FreeSpaceProvider) {
    return [long](& $FreeSpaceProvider $Volume)
  }
  $drive = [IO.DriveInfo]::new($Volume)
  return [long]$drive.AvailableFreeSpace
}

function Get-AHA2BackupRequiredBytes {
  param([long]$DatabaseBytes)
  # The database trio is copied once; the second copy covers WAL growth and
  # temp files during the copy, plus a 1 GiB safety margin on the data volume.
  return ($DatabaseBytes * 2) + $script:aha2DiskSafetyMarginBytes
}

function Get-AHA2VolumeChecks {
  param(
    $Requirements,
    [scriptblock]$FreeSpaceProvider = $null,
    [scriptblock]$VolumeResolver = $null
  )
  if ($null -eq $VolumeResolver) {
    $VolumeResolver = { param($Path) return Get-AHA2VolumeRoot -Path $Path }
  }
  # Requirements that land on the same volume share one check, so a deployment
  # whose data, install, and temp directories all live on D: is measured
  # against their sum instead of three independent thresholds.
  $order = New-Object System.Collections.ArrayList
  $byVolume = @{}
  foreach ($requirement in @($Requirements)) {
    $volume = [string](& $VolumeResolver $requirement.Path)
    if ([string]::IsNullOrWhiteSpace($volume)) {
      throw ("Could not resolve the volume for {0}." -f $requirement.Path)
    }
    if (-not $byVolume.ContainsKey($volume)) {
      $byVolume[$volume] = [pscustomobject]@{Volume = $volume; RequiredBytes = [long]0; Roles = @()}
      [void]$order.Add($volume)
    }
    $byVolume[$volume].RequiredBytes += [long]$requirement.RequiredBytes
    $byVolume[$volume].Roles += [string]$requirement.Role
  }

  $checks = New-Object System.Collections.ArrayList
  foreach ($volume in $order) {
    $entry = $byVolume[$volume]
    $freeBytes = Get-AHA2VolumeFreeBytes -Volume $volume -FreeSpaceProvider $FreeSpaceProvider
    [void]$checks.Add([pscustomobject]@{
      Volume = [string]$volume
      Roles = @($entry.Roles)
      FreeBytes = [long]$freeBytes
      RequiredBytes = [long]$entry.RequiredBytes
      Ok = [bool]($freeBytes -ge $entry.RequiredBytes)
    })
  }
  return @($checks)
}

function Get-AHA2DeploymentPreflight {
  param(
    [string]$DataDirectory,
    [string[]]$DatabasePaths,
    [string]$InstallDirectory = "",
    [string]$TempDirectory = "",
    [scriptblock]$FreeSpaceProvider = $null,
    # Injection point for the self-test; production resolves real volumes.
    [scriptblock]$VolumeResolver = $null
  )
  if ($null -eq $VolumeResolver) {
    $VolumeResolver = { param($Path) return Get-AHA2VolumeRoot -Path $Path }
  }
  $databaseBytes = Get-AHA2FileSetBytes -Paths $DatabasePaths
  $requirements = New-Object System.Collections.ArrayList
  [void]$requirements.Add([pscustomobject]@{
    Role = "data"
    Path = $DataDirectory
    RequiredBytes = [long](Get-AHA2BackupRequiredBytes -DatabaseBytes $databaseBytes)
  })
  if (-not [string]::IsNullOrWhiteSpace($InstallDirectory)) {
    [void]$requirements.Add([pscustomobject]@{
      Role = "install"
      Path = $InstallDirectory
      RequiredBytes = $script:aha2InstallSpaceAllowanceBytes
    })
  }
  if (-not [string]::IsNullOrWhiteSpace($TempDirectory)) {
    [void]$requirements.Add([pscustomobject]@{
      Role = "temp"
      Path = $TempDirectory
      RequiredBytes = $script:aha2TempSpaceAllowanceBytes
    })
  }

  $checks = @(Get-AHA2VolumeChecks -Requirements $requirements -FreeSpaceProvider $FreeSpaceProvider -VolumeResolver $VolumeResolver)
  $dataVolume = [string](& $VolumeResolver $DataDirectory)
  $dataCheck = @($checks | Where-Object { $_.Volume -ieq $dataVolume }) | Select-Object -First 1
  if (-not $dataCheck) {
    throw ("Could not resolve the data volume for {0}." -f $DataDirectory)
  }
  $insufficient = @($checks | Where-Object { -not $_.Ok })
  return [pscustomobject]@{
    Ok = [bool]($insufficient.Count -eq 0)
    Volume = [string]$dataCheck.Volume
    FreeBytes = [long]$dataCheck.FreeBytes
    RequiredBytes = [long]$dataCheck.RequiredBytes
    DatabaseBytes = [long]$databaseBytes
    Volumes = $checks
    InsufficientVolumes = $insufficient
  }
}

function Get-AHA2PreflightRequest {
  param(
    [string]$DataDirectory,
    [string[]]$DatabasePaths,
    [string]$InstallDirectory,
    [string]$TempDirectory,
    [bool]$UseInstaller
  )
  # Only the installer branch copies the setup executable into the OS temp
  # directory. In-place updates stage every file beside its destination, so
  # requiring temp space there would reject otherwise valid upgrades.
  $effectiveTemp = if ($UseInstaller) { $TempDirectory } else { "" }
  return @{
    DataDirectory = $DataDirectory
    DatabasePaths = @($DatabasePaths)
    InstallDirectory = $InstallDirectory
    TempDirectory = $effectiveTemp
  }
}

function Format-AHA2ByteSize {
  param([long]$Bytes)
  if ($Bytes -ge 1GB) { return ("{0:n2} GiB" -f ($Bytes / 1GB)) }
  if ($Bytes -ge 1MB) { return ("{0:n1} MiB" -f ($Bytes / 1MB)) }
  if ($Bytes -ge 1KB) { return ("{0:n1} KiB" -f ($Bytes / 1KB)) }
  return ("{0} B" -f $Bytes)
}

function Get-AHA2PreflightRequirementText {
  param($Check)
  # The failure text must describe only the roles this preflight actually
  # checked. An in-place update checks data and install; claiming a temp
  # requirement there would mislead the operator about what failed.
  $checkedRoles = @($Check.Volumes | ForEach-Object { @($_.Roles) } | Select-Object -Unique)
  $parts = New-Object System.Collections.ArrayList
  if ($checkedRoles -contains "data") {
    [void]$parts.Add(("数据库三件套当前 {0} 字节 × 2 + 1GiB 数据卷余量" -f [long]$Check.DatabaseBytes))
  }
  if ($checkedRoles -contains "install") {
    [void]$parts.Add(("安装目录预留 {0} 字节" -f $script:aha2InstallSpaceAllowanceBytes))
  }
  if ($checkedRoles -contains "temp") {
    [void]$parts.Add(("临时目录预留 {0} 字节" -f $script:aha2TempSpaceAllowanceBytes))
  }
  return ("需求 = " + ($parts -join "；") + "；同一卷上的需求合并计算。")
}

function Set-AHA2PreflightFailure {
  param($Record, $Check)
  $Record.Status = "failed"
  $Record.Stage = "preflight"
  $Record.FailedStage = "preflight"
  $Record.Volume = [string]$Check.Volume
  $Record.FreeBytes = [long]$Check.FreeBytes
  $Record.RequiredBytes = [long]$Check.RequiredBytes
  $Record.Volumes = @($Check.Volumes)
  $details = @($Check.InsufficientVolumes | ForEach-Object {
    ("卷 {0}（{1}）可用 {2}（{3} 字节），需要 {4}（{5} 字节）" -f @(
      $_.Volume,
      ($_.Roles -join "+"),
      (Format-AHA2ByteSize $_.FreeBytes),
      $_.FreeBytes,
      (Format-AHA2ByteSize $_.RequiredBytes),
      $_.RequiredBytes
    ))
  }) -join "；"
  $requirementText = Get-AHA2PreflightRequirementText -Check $Check
  $message = ("部署前磁盘空间不足，已取消部署：{0}。{1}" +
    "AHA2 未被停止，也没有复制或替换任何文件。") -f @(
      $details,
      $requirementText
    )
  $Record.Value = $message
  return $message
}

function Invoke-AHA2DeploymentPreflight {
  param(
    $Record,
    [string]$DataDirectory,
    [string[]]$DatabasePaths,
    [string]$InstallDirectory = "",
    [string]$TempDirectory = "",
    [scriptblock]$FreeSpaceProvider = $null,
    [scriptblock]$VolumeResolver = $null
  )
  # This is the production preflight path: it records the volumes that were
  # checked and throws on any shortfall, so the caller never reaches the stop,
  # backup, or file-replacement stages.
  $arguments = @{
    DataDirectory = $DataDirectory
    DatabasePaths = $DatabasePaths
    InstallDirectory = $InstallDirectory
    TempDirectory = $TempDirectory
    FreeSpaceProvider = $FreeSpaceProvider
    VolumeResolver = $VolumeResolver
  }
  try {
    $check = Get-AHA2DeploymentPreflight @arguments
  } catch {
    $Record.Status = "failed"
    $Record.Stage = "preflight"
    $Record.FailedStage = "preflight"
    $Record.Value = ("部署前磁盘空间预检失败，已取消部署：无法读取卷可用空间：{0}。AHA2 未被停止，也没有复制或替换任何文件。" -f $_.Exception.Message)
    throw $Record.Value
  }
  $Record.Volume = [string]$check.Volume
  $Record.FreeBytes = [long]$check.FreeBytes
  $Record.RequiredBytes = [long]$check.RequiredBytes
  $Record.Volumes = @($check.Volumes)
  if ($check.Ok) { return $check }
  $message = Set-AHA2PreflightFailure -Record $Record -Check $check
  throw $message
}

function Get-AHA2TargetProcessIdentities {
  param(
    [object[]]$Processes,
    [string[]]$RootNames = @("aha2.exe", "aha2-tray.exe", "aha2-channel-feishu.exe")
  )
  $childrenByParent = @{}
  foreach ($process in @($Processes)) {
    $parentId = [int]$process.ParentProcessId
    if (-not $childrenByParent.ContainsKey($parentId)) {
      $childrenByParent[$parentId] = New-Object System.Collections.ArrayList
    }
    [void]$childrenByParent[$parentId].Add($process)
  }

  # A plain hashtable is required here: OrderedDictionary binds integer keys to
  # its positional indexer in PowerShell, which fails for sparse PID keys.
  $targets = @{}
  foreach ($process in @($Processes)) {
    if ($RootNames -contains [string]$process.Name) {
      $targets[[int]$process.ProcessId] = [pscustomobject]@{
        Id = [int]$process.ProcessId
        Name = [string]$process.Name
        CreationDate = [string]$process.CreationDate
        Depth = 0
      }
    }
  }

  $queue = New-Object System.Collections.Queue
  foreach ($target in $targets.Values) { $queue.Enqueue($target.Id) }
  while ($queue.Count -gt 0) {
    $parentId = [int]$queue.Dequeue()
    if (-not $childrenByParent.ContainsKey($parentId)) { continue }
    foreach ($child in $childrenByParent[$parentId]) {
      $childId = [int]$child.ProcessId
      if ($targets.Contains($childId)) { continue }
      $targets[$childId] = [pscustomobject]@{
        Id = $childId
        Name = [string]$child.Name
        CreationDate = [string]$child.CreationDate
        Depth = [int]$targets[$parentId].Depth + 1
      }
      $queue.Enqueue($childId)
    }
  }
  return @($targets.Values)
}

function Test-AHA2BackupCopy {
  param([string]$Source, [string]$Destination)
  if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) {
    return [pscustomobject]@{Ok=$false;Reason="source is missing";Size=0;SHA256=""}
  }
  if (-not (Test-Path -LiteralPath $Destination -PathType Leaf)) {
    return [pscustomobject]@{Ok=$false;Reason="backup copy is missing";Size=0;SHA256=""}
  }
  $sourceSize = (Get-Item -LiteralPath $Source).Length
  $destinationSize = (Get-Item -LiteralPath $Destination).Length
  if ($sourceSize -ne $destinationSize) {
    return [pscustomobject]@{Ok=$false;Reason=("size mismatch: {0} != {1}" -f $sourceSize,$destinationSize);Size=$sourceSize;SHA256=""}
  }
  # Get-FileHash writes a non-terminating error when the file is briefly held
  # by SQLite or antivirus. Under StrictMode the old code then read .Hash from
  # $null and aborted the whole deployment with a property-not-found error.
  # Stop on the cmdlet error and report the file and reason instead.
  try {
    $sourceHash = (Get-FileHash -LiteralPath $Source -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
    $destinationHash = (Get-FileHash -LiteralPath $Destination -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
  } catch {
    return [pscustomobject]@{
      Ok=$false
      Reason=("could not hash backup file source={0} destination={1}: {2}" -f $Source,$Destination,$_.Exception.Message)
      Size=$sourceSize
      SHA256=""
    }
  }
  if ($sourceHash -ne $destinationHash) {
    return [pscustomobject]@{Ok=$false;Reason="SHA256 mismatch";Size=$sourceSize;SHA256=$sourceHash}
  }
  return [pscustomobject]@{Ok=$true;Reason="";Size=$sourceSize;SHA256=$sourceHash}
}

function Copy-AHA2DatabaseBackup {
  param(
    [string]$DataDirectory,
    [string]$BackupDirectory,
    # The caller may pass an explicit list for a self-test; production uses the
    # two durable files. aha2.db-shm is deliberately not in that list.
    [string[]]$FileNames = $null
  )
  if ($null -eq $FileNames) { $FileNames = $script:databaseBackupFileNames }
  $results = New-Object System.Collections.ArrayList
  foreach ($name in @($FileNames)) {
    $sourcePath = Join-Path $DataDirectory $name
    if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) { continue }
    $destinationPath = Join-Path $BackupDirectory $name
    Copy-Item -LiteralPath $sourcePath -Destination $destinationPath -Force
    $check = Test-AHA2BackupCopy -Source $sourcePath -Destination $destinationPath
    if (-not $check.Ok) {
      throw ("Backup verification failed for {0}: {1}" -f $name,$check.Reason)
    }
    [void]$results.Add([pscustomobject]@{
      Name = $name
      SourcePath = $sourcePath
      BackupPath = $destinationPath
      SHA256 = $check.SHA256
      Size = $check.Size
    })
  }
  return @($results)
}

function Test-AHA2RollbackPrerequisite {
  param([bool]$ExistedBefore, [bool]$BackupPresent)
  # Only files the deployment replaces need a rollback copy. A first-time
  # install has no previous binary to back up, so its absence is not an error.
  return ((-not $ExistedBefore) -or $BackupPresent)
}

function Get-AHA2FileRollbackAction {
  param([bool]$ExistedBefore, [bool]$BackupPresent)
  if ($BackupPresent) { return "restore" }
  if (-not $ExistedBefore) { return "delete" }
  return "blocked"
}

function Get-AHA2DatabaseRestorePlan {
  param([string[]]$BackupNames)
  $actions = New-Object System.Collections.ArrayList
  if ($BackupNames -notcontains "aha2.db") {
    return [pscustomobject]@{
      Blocked = $true
      Reason = "the verified backup does not contain aha2.db"
      Actions = @()
    }
  }
  # A slightly stale shared-memory file is worse than none, and a WAL from the
  # failed version must never be replayed onto the restored image. Delete both
  # sidecars unconditionally before swapping the database: relying on the files
  # seen at inspection time leaves a window for a late writer to recreate one.
  [void]$actions.Add([pscustomobject]@{Action="delete";Name="aha2.db-shm"})
  [void]$actions.Add([pscustomobject]@{Action="delete";Name="aha2.db-wal"})
  [void]$actions.Add([pscustomobject]@{Action="replace";Name="aha2.db"})
  if ($BackupNames -contains "aha2.db-wal") {
    [void]$actions.Add([pscustomobject]@{Action="replace";Name="aha2.db-wal"})
  }
  return [pscustomobject]@{Blocked=$false;Reason="";Actions=@($actions)}
}

function New-AHA2StagedFileCopy {
  param(
    [string]$Source,
    [string]$DestinationDirectory,
    [string]$Label = "restore",
    [string]$ExpectedSHA256 = ""
  )
  New-Item -ItemType Directory -Path $DestinationDirectory -Force | Out-Null
  $temporaryPath = Join-Path $DestinationDirectory (".aha2-{0}-{1}.tmp" -f $Label,[Guid]::NewGuid().ToString("N"))
  Copy-Item -LiteralPath $Source -Destination $temporaryPath -Force
  # Same failure mode as Test-AHA2BackupCopy: a transient lock must surface as a
  # named error, not as a non-terminating Get-FileHash error followed by
  # $null.Hash under StrictMode.
  try {
    $sourceHash = (Get-FileHash -LiteralPath $Source -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
    $temporaryHash = (Get-FileHash -LiteralPath $temporaryPath -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
  } catch {
    Remove-Item -LiteralPath $temporaryPath -Force -ErrorAction SilentlyContinue
    throw ("Could not hash staged file {0}: {1}" -f $Source,$_.Exception.Message)
  }
  if ($sourceHash -ne $temporaryHash) {
    Remove-Item -LiteralPath $temporaryPath -Force -ErrorAction SilentlyContinue
    throw "Staged copy hash mismatch for $Source."
  }
  if (-not [string]::IsNullOrWhiteSpace($ExpectedSHA256) -and $sourceHash -ne $ExpectedSHA256.ToLowerInvariant()) {
    Remove-Item -LiteralPath $temporaryPath -Force -ErrorAction SilentlyContinue
    throw ("Backup file {0} no longer matches the SHA256 recorded when it was captured." -f $Source)
  }
  return [pscustomobject]@{TemporaryPath=$temporaryPath;SHA256=$sourceHash;Size=([IO.FileInfo]::new($temporaryPath)).Length}
}

function Set-AHA2StagedFileAtomic {
  param(
    [string]$TemporaryPath,
    [string]$Destination,
    [int]$RetryMilliseconds = 2000
  )
  $deadline = [DateTime]::UtcNow.AddMilliseconds($RetryMilliseconds)
  while ($true) {
    try {
      if (Test-Path -LiteralPath $Destination -PathType Leaf) {
        # ReplaceFile is atomic: a locked destination fails instead of being
        # truncated and partially rewritten, which is what corrupted aha2.db
        # during the 2026-11-12 incident. [NullString]::Value passes a real null
        # backup name; a plain $null is converted to an empty string and makes
        # every replacement fail with "The value cannot be an empty string".
        [IO.File]::Replace($TemporaryPath, $Destination, [NullString]::Value)
      } else {
        [IO.File]::Move($TemporaryPath, $Destination)
      }
      return
    } catch {
      if ([DateTime]::UtcNow -ge $deadline) {
        throw ("Could not atomically replace {0}; a process still holds it: {1}" -f $Destination,$_.Exception.Message)
      }
      Start-Sleep -Milliseconds 200
    }
  }
}

function Set-AHA2FileFromSource {
  param(
    [string]$Source,
    [string]$Destination,
    [int]$RetryMilliseconds = 2000,
    [string]$ExpectedSHA256 = ""
  )
  $directory = Split-Path -Parent $Destination
  $staged = New-AHA2StagedFileCopy -Source $Source -DestinationDirectory $directory -Label "atomic" -ExpectedSHA256 $ExpectedSHA256
  try {
    Set-AHA2StagedFileAtomic -TemporaryPath $staged.TemporaryPath -Destination $Destination -RetryMilliseconds $RetryMilliseconds
    $staged.TemporaryPath = ""
  } finally {
    if ($staged.TemporaryPath -and (Test-Path -LiteralPath $staged.TemporaryPath -PathType Leaf)) {
      Remove-Item -LiteralPath $staged.TemporaryPath -Force -ErrorAction SilentlyContinue
    }
  }
  return $staged.SHA256
}

function Set-AHA2InstalledFile {
  param($Record, [string]$Source, [string]$Destination)
  try {
    [void](Set-AHA2FileFromSource -Source $Source -Destination $Destination)
  } catch {
    # Record the path that could not be atomically replaced so the failure
    # result names it instead of leaving LockedFiles empty.
    Add-AHA2LockedFile -Record $Record -Paths @($Destination)
    throw
  }
}

function Remove-AHA2FileIfPresent {
  param([string]$Path)
  if (Test-Path -LiteralPath $Path -PathType Leaf) {
    [IO.File]::Delete($Path)
  }
}

function Invoke-AHA2FileRollbackEntry {
  param($Entry)
  $action = Get-AHA2FileRollbackAction -ExistedBefore ([bool]$Entry.ExistedBefore) -BackupPresent ([bool]$Entry.BackupPresent)
  switch ($action) {
    "restore" {
      [void](Set-AHA2FileFromSource -Source $Entry.BackupPath -Destination $Entry.TargetPath -ExpectedSHA256 $Entry.SHA256)
    }
    "delete" {
      # The file did not exist before this deployment. Remove whatever the
      # failed install created so the previous version cannot load it.
      Remove-AHA2FileIfPresent -Path $Entry.TargetPath
    }
    default {
      return ("blocked: {0} existed before the deployment but has no verified backup" -f $Entry.TargetPath)
    }
  }
  return ""
}

function Restore-AHA2DatabaseFromBackup {
  param(
    [string]$BackupDirectory,
    [string]$DataDirectory,
    $ExpectedSHA256 = $null
  )
  # Only durable database files are restorable. A shm left in an old backup
  # directory is ignored: recreating it from stale bytes would be worse than
  # deleting it and letting SQLite rebuild it on the next start.
  $backupNames = @($databaseBackupFileNames | Where-Object {
    Test-Path -LiteralPath (Join-Path $BackupDirectory $_) -PathType Leaf
  })
  $plan = Get-AHA2DatabaseRestorePlan -BackupNames $backupNames
  if ($plan.Blocked) { throw $plan.Reason }

  # Stage the database (and WAL when present) before deleting anything, so the
  # delete-and-swap window only covers metadata operations, not a multi-GB copy.
  $stagedFiles = @{}
  try {
    $expectedDatabaseHash = if ($ExpectedSHA256 -and $ExpectedSHA256.Contains("aha2.db")) { [string]$ExpectedSHA256["aha2.db"] } else { "" }
    $stagedFiles["aha2.db"] = New-AHA2StagedFileCopy -Source (Join-Path $BackupDirectory "aha2.db") -DestinationDirectory $DataDirectory -Label "db" -ExpectedSHA256 $expectedDatabaseHash
    if ($backupNames -contains "aha2.db-wal") {
      $expectedWalHash = if ($ExpectedSHA256 -and $ExpectedSHA256.Contains("aha2.db-wal")) { [string]$ExpectedSHA256["aha2.db-wal"] } else { "" }
      $stagedFiles["aha2.db-wal"] = New-AHA2StagedFileCopy -Source (Join-Path $BackupDirectory "aha2.db-wal") -DestinationDirectory $DataDirectory -Label "wal" -ExpectedSHA256 $expectedWalHash
    }
    foreach ($action in @($plan.Actions)) {
      if ($action.Action -eq "delete") {
        Remove-AHA2FileIfPresent -Path (Join-Path $DataDirectory $action.Name)
        continue
      }
      $staged = $stagedFiles[$action.Name]
      Set-AHA2StagedFileAtomic -TemporaryPath $staged.TemporaryPath -Destination (Join-Path $DataDirectory $action.Name)
      $staged.TemporaryPath = ""
    }
  } finally {
    foreach ($staged in @($stagedFiles.Values)) {
      if ($staged -and $staged.TemporaryPath -and (Test-Path -LiteralPath $staged.TemporaryPath -PathType Leaf)) {
        Remove-Item -LiteralPath $staged.TemporaryPath -Force -ErrorAction SilentlyContinue
      }
    }
  }
}

function Test-AHA2ExclusiveFileProbe {
  param([string]$Path)
  if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
  try {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    $stream.Dispose()
    return $true
  } catch [UnauthorizedAccessException] {
    # A read-only attribute is not a lock. Retry without write access so the
    # probe reports locks, not file attributes.
    try {
      $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::None)
      $stream.Dispose()
      return $true
    } catch {
      return $false
    }
  } catch {
    return $false
  }
}

function Test-AHA2QuickCheckOutput {
  param([object[]]$Lines)
  $clean = @($Lines |
    ForEach-Object { ([string]$_).Trim() } |
    Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  return ($clean.Count -eq 1 -and $clean[0] -ieq "ok")
}

function New-AHA2ResultRecord {
  param(
    [string]$Status = "running",
    [string]$Value = "",
    [string]$Stage = "init",
    [object[]]$LockedFiles = @(),
    $BackupSHA256 = $null,
    [bool]$RollbackVerified = $false,
    [string]$RollbackHealth = "not-run"
  )
  if ($null -eq $BackupSHA256) { $BackupSHA256 = [ordered]@{} }
  return [ordered]@{
    Status = $Status
    Value = $Value
    Stage = $Stage
    FailedStage = ""
    Volume = ""
    FreeBytes = [long]0
    RequiredBytes = [long]0
    Volumes = @()
    LockedFiles = @($LockedFiles)
    Backup = ""
    BackupSHA256 = $BackupSHA256
    BackupSizes = [ordered]@{}
    BackupVerified = $false
    StopVerified = $false
    StopAttempts = 0
    RollbackVerified = $RollbackVerified
    rollback_health = $RollbackHealth
    ManualRecovery = ""
    Mode = ""
    UpdateType = ""
    Health = ""
    URL = ""
    Installer = ""
    ServerSHA256 = ""
    TraySHA256 = ""
    PluginSHA256 = ""
    WriteAccessVerified = $true
    ElevationRequired = $false
    FinishedAt = ""
  }
}

function Add-AHA2LockedFile {
  param($Record, [string[]]$Paths)
  foreach ($path in @($Paths)) {
    if ([string]::IsNullOrWhiteSpace($path)) { continue }
    if (@($Record.LockedFiles) -notcontains $path) {
      $Record.LockedFiles = @(@($Record.LockedFiles) + $path)
    }
  }
}

function Invoke-AHA2SelfTest {
  $script:selfTestChecks = 0
  $script:selfTestFailures = New-Object System.Collections.ArrayList
  function Assert-AHA2SelfTest([bool]$Condition, [string]$Name) {
    $script:selfTestChecks++
    if ($Condition) {
      Write-Host ("PASS  " + $Name)
    } else {
      Write-Host ("FAIL  " + $Name)
      [void]$script:selfTestFailures.Add($Name)
    }
  }

  Write-Host "AHA2 deployment self-test (no installed file, process, task, or database is touched)"

  # Process targeting: named roots plus every descendant, including the Feishu
  # channel plugin when it is not a child of aha2.exe.
  $fixture = @(
    [pscustomobject]@{ProcessId=10;ParentProcessId=1;Name="aha2.exe";CreationDate="t10"},
    [pscustomobject]@{ProcessId=11;ParentProcessId=10;Name="conhost.exe";CreationDate="t11"},
    [pscustomobject]@{ProcessId=14;ParentProcessId=11;Name="plugin-supervisor.exe";CreationDate="t14"},
    [pscustomobject]@{ProcessId=12;ParentProcessId=10;Name="aha2-channel-feishu.exe";CreationDate="t12"},
    [pscustomobject]@{ProcessId=13;ParentProcessId=12;Name="plugin-worker.exe";CreationDate="t13"},
    [pscustomobject]@{ProcessId=20;ParentProcessId=1;Name="aha2-channel-feishu.exe";CreationDate="t20"},
    [pscustomobject]@{ProcessId=21;ParentProcessId=20;Name="plugin-helper.exe";CreationDate="t21"},
    [pscustomobject]@{ProcessId=22;ParentProcessId=1;Name="aha2-tray.exe";CreationDate="t22"},
    [pscustomobject]@{ProcessId=99;ParentProcessId=1;Name="notepad.exe";CreationDate="t99"}
  )
  $targets = @(Get-AHA2TargetProcessIdentities -Processes $fixture)
  $targetIds = @($targets | ForEach-Object { $_.Id } | Sort-Object)
  Assert-AHA2SelfTest (($targetIds -join ",") -eq "10,11,12,13,14,20,21,22") "targets only AHA2 roots and their descendants"
  Assert-AHA2SelfTest (@($targets | Where-Object { $_.Id -eq 14 })[0].Depth -eq 2) "descendant depth is recorded for ordered termination"
  Assert-AHA2SelfTest (-not ($targetIds -contains 99)) "unrelated processes are never targeted"
  $emptyTree = @(Get-AHA2TargetProcessIdentities -Processes @())
  Assert-AHA2SelfTest ($emptyTree.Count -eq 0) "empty process table produces no targets"

  Assert-AHA2SelfTest (Test-AHA2StopSatisfied -RemainingProcesses @() -LockedFiles @()) "stop is satisfied only when processes and locks are both gone"
  Assert-AHA2SelfTest (-not (Test-AHA2StopSatisfied -RemainingProcesses @([pscustomobject]@{Id=1}) -LockedFiles @())) "remaining process blocks stop"
  Assert-AHA2SelfTest (-not (Test-AHA2StopSatisfied -RemainingProcesses @() -LockedFiles @("aha2.db"))) "locked file blocks stop"
  Assert-AHA2SelfTest ((Get-AHA2StopAttemptDecision -Satisfied $true -TimedOut $false) -eq "verified") "stop retry decision verifies a satisfied stop"
  Assert-AHA2SelfTest ((Get-AHA2StopAttemptDecision -Satisfied $false -TimedOut $false) -eq "retry") "stop retry decision retries while time remains"
  Assert-AHA2SelfTest ((Get-AHA2StopAttemptDecision -Satisfied $false -TimedOut $true) -eq "timeout") "stop retry decision reports a clear timeout"
  Assert-AHA2SelfTest ((Get-AHA2StopAttemptDecision -Satisfied $true -TimedOut $true) -eq "verified") "a satisfied stop wins over a simultaneous timeout"

  # Deployment preflight: data, install, and temp directories may live on
  # different volumes. Each volume gets its own requirement (database trio x2 +
  # 1 GiB on the data volume, 200 MB for the install directory, 300 MB for the
  # installer staged in temp); requirements on the same volume are summed, and
  # any shortfall must fail before the stop stage is ever reached. Requests are
  # built by the same pure function the main flow calls, so the parameters
  # production passes are what these cases exercise.
  $preflightRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-preflight-selftest-" + [Guid]::NewGuid().ToString("N"))
  try {
    New-Item -ItemType Directory -Path $preflightRoot -Force | Out-Null
    $preflightDatabase = Join-Path $preflightRoot "aha2.db"
    $preflightWal = Join-Path $preflightRoot "aha2.db-wal"
    $preflightShm = Join-Path $preflightRoot "aha2.db-shm"
    $preflightInstall = Join-Path $preflightRoot "install"
    $preflightTemp = Join-Path $preflightRoot "temp"
    New-Item -ItemType Directory -Path $preflightInstall,$preflightTemp -Force | Out-Null
    [IO.File]::WriteAllBytes($preflightDatabase, [byte[]]::new(4096))
    [IO.File]::WriteAllBytes($preflightWal, [byte[]]::new(2048))
    [IO.File]::WriteAllBytes($preflightShm, [byte[]]::new(1024))
    $databaseBytes = [long](4096 + 2048 + 1024)
    $dataRequired = ($databaseBytes * 2) + $script:aha2DiskSafetyMarginBytes
    $preflightPaths = @($preflightDatabase, $preflightWal, $preflightShm)

    # The production request assembly decides which roles are checked.
    $installerRequest = Get-AHA2PreflightRequest -DataDirectory $preflightRoot -DatabasePaths $preflightPaths -InstallDirectory $preflightInstall -TempDirectory $preflightTemp -UseInstaller $true
    $inPlaceRequest = Get-AHA2PreflightRequest -DataDirectory $preflightRoot -DatabasePaths $preflightPaths -InstallDirectory $preflightInstall -TempDirectory $preflightTemp -UseInstaller $false
    Assert-AHA2SelfTest ($installerRequest.DataDirectory -eq $preflightRoot -and $installerRequest.InstallDirectory -eq $preflightInstall -and $installerRequest.TempDirectory -eq $preflightTemp) "installer preflight request carries the data, install, and temp directories"
    Assert-AHA2SelfTest ($inPlaceRequest.DataDirectory -eq $preflightRoot -and $inPlaceRequest.InstallDirectory -eq $preflightInstall -and $inPlaceRequest.TempDirectory -eq "") "in-place preflight request omits the temp directory"
    Assert-AHA2SelfTest ((@($installerRequest.DatabasePaths) -join ",") -eq ($preflightPaths -join ",")) "preflight request carries the database trio"

    # This fixture keeps all three directories on one volume: the check must use
    # their sum instead of only the data requirement.
    $sameVolumeRequired = $dataRequired + $script:aha2InstallSpaceAllowanceBytes + $script:aha2TempSpaceAllowanceBytes
    $sameVolumeCheck = Get-AHA2DeploymentPreflight @installerRequest -FreeSpaceProvider { param($Volume) return $sameVolumeRequired - 1 }
    Assert-AHA2SelfTest (-not $sameVolumeCheck.Ok) "preflight rejects a volume below the summed data/install/temp requirement"
    Assert-AHA2SelfTest ($sameVolumeCheck.RequiredBytes -eq $sameVolumeRequired) "same-volume requirements are summed into one threshold"
    Assert-AHA2SelfTest ($sameVolumeCheck.Volumes.Count -eq 1 -and @($sameVolumeCheck.Volumes[0].Roles).Count -eq 3) "same-volume roles share one volume check"
    $sameVolumeRoles = @($sameVolumeCheck.Volumes[0].Roles)
    Assert-AHA2SelfTest (($sameVolumeRoles -contains "data") -and ($sameVolumeRoles -contains "install") -and ($sameVolumeRoles -contains "temp")) "installer preflight checks the data, install, and temp roles"
    Assert-AHA2SelfTest ($sameVolumeCheck.FreeBytes -eq ($sameVolumeRequired - 1)) "preflight reports the injected free-space value"

    # The production preflight path is invoked directly: a shortfall must throw
    # with the Chinese error, fill the result record, and never reach the stop
    # stage (the injected stub stays at zero).
    $script:selfTestStopCalls = 0
    $stopStub = { $script:selfTestStopCalls++ }
    $failedRecord = New-AHA2ResultRecord
    $preflightThrew = $false
    $preflightMessage = ""
    try {
      [void](Invoke-AHA2DeploymentPreflight -Record $failedRecord @installerRequest -FreeSpaceProvider { param($Volume) return $sameVolumeRequired - 1 })
      [void](& $stopStub)
    } catch {
      $preflightThrew = $true
      $preflightMessage = $_.Exception.Message
    }
    Assert-AHA2SelfTest $preflightThrew "production preflight aborts on insufficient space"
    Assert-AHA2SelfTest ($preflightMessage -match "磁盘空间不足") "production preflight throws the Chinese space error"
    Assert-AHA2SelfTest ($script:selfTestStopCalls -eq 0) "production preflight never reaches the stop stage"
    Assert-AHA2SelfTest ($failedRecord.Stage -eq "preflight" -and $failedRecord.FailedStage -eq "preflight") "production preflight records Stage=preflight"
    Assert-AHA2SelfTest ($failedRecord.FreeBytes -eq $sameVolumeCheck.FreeBytes -and $failedRecord.RequiredBytes -eq $sameVolumeCheck.RequiredBytes -and $failedRecord.Volume -eq $sameVolumeCheck.Volume) "production preflight records Volume, FreeBytes, and RequiredBytes"
    Assert-AHA2SelfTest (@($failedRecord.Volumes).Count -eq 1) "production preflight records per-volume details"

    # Enough space on the same volume returns normally, so the stop stage runs.
    $continued = $false
    try {
      [void](Invoke-AHA2DeploymentPreflight -Record (New-AHA2ResultRecord) @installerRequest -FreeSpaceProvider { param($Volume) return $sameVolumeRequired })
      [void](& $stopStub)
      $continued = $true
    } catch {}
    Assert-AHA2SelfTest $continued "production preflight lets a sufficient deployment continue"
    Assert-AHA2SelfTest ($script:selfTestStopCalls -eq 1) "the stop stage runs only after a passing preflight"

    # Split-volume case: D: holds data+install, F: holds temp. D: is ample and
    # F: is short, so only the temp volume may be reported as insufficient.
    $volumeMap = @{}
    $volumeMap[$preflightRoot] = "D:\"
    $volumeMap[$preflightInstall] = "D:\"
    $volumeMap[$preflightTemp] = "F:\"
    $volumeResolver = { param($Path) return $volumeMap[[string]$Path] }
    $freeByVolume = @{}
    $freeByVolume["D:\"] = [long]($dataRequired + $script:aha2InstallSpaceAllowanceBytes)
    $freeByVolume["F:\"] = [long]$script:aha2TempSpaceAllowanceBytes
    $freeSpaceProvider = { param($Volume) return $freeByVolume[[string]$Volume] }
    $splitCheck = Get-AHA2DeploymentPreflight @installerRequest -FreeSpaceProvider $freeSpaceProvider -VolumeResolver $volumeResolver
    Assert-AHA2SelfTest $splitCheck.Ok "preflight accepts split volumes when every volume has enough space"
    Assert-AHA2SelfTest (@($splitCheck.Volumes).Count -eq 2) "split-volume preflight checks each volume separately"
    $splitDataCheck = @($splitCheck.Volumes | Where-Object { $_.Volume -eq "D:\" })[0]
    $splitTempCheck = @($splitCheck.Volumes | Where-Object { $_.Volume -eq "F:\" })[0]
    Assert-AHA2SelfTest ($splitDataCheck.RequiredBytes -eq ($dataRequired + $script:aha2InstallSpaceAllowanceBytes)) "data and install requirements are summed per volume"
    Assert-AHA2SelfTest ($splitTempCheck.RequiredBytes -eq $script:aha2TempSpaceAllowanceBytes) "temp volume requires its own allowance"

    # An in-place update stages files beside their destinations and never uses
    # the OS temp directory, so a short temp volume must not block it.
    $freeByVolume["F:\"] = [long]0
    $inPlaceCheck = Get-AHA2DeploymentPreflight @inPlaceRequest -FreeSpaceProvider $freeSpaceProvider -VolumeResolver $volumeResolver
    Assert-AHA2SelfTest $inPlaceCheck.Ok "in-place preflight passes when the temp volume is short"
    $inPlaceRoles = @($inPlaceCheck.Volumes[0].Roles)
    Assert-AHA2SelfTest (@($inPlaceCheck.Volumes).Count -eq 1 -and ($inPlaceRoles -contains "data") -and ($inPlaceRoles -contains "install") -and -not ($inPlaceRoles -contains "temp")) "in-place preflight checks only the data and install roles"
    $inPlaceContinued = $false
    try {
      [void](Invoke-AHA2DeploymentPreflight -Record (New-AHA2ResultRecord) @inPlaceRequest -FreeSpaceProvider $freeSpaceProvider -VolumeResolver $volumeResolver)
      $inPlaceContinued = $true
    } catch {}
    Assert-AHA2SelfTest $inPlaceContinued "production preflight accepts an in-place update with a short temp volume"

    $freeByVolume["F:\"] = [long]($script:aha2TempSpaceAllowanceBytes - 1)
    $splitShort = Get-AHA2DeploymentPreflight @installerRequest -FreeSpaceProvider $freeSpaceProvider -VolumeResolver $volumeResolver
    Assert-AHA2SelfTest (-not $splitShort.Ok) "preflight fails when only the temp volume is short"
    Assert-AHA2SelfTest (@($splitShort.InsufficientVolumes).Count -eq 1 -and $splitShort.InsufficientVolumes[0].Volume -eq "F:\" -and (@($splitShort.InsufficientVolumes[0].Roles) -contains "temp")) "preflight names the insufficient temp volume and role"

    $splitRecord = New-AHA2ResultRecord
    $splitThrew = $false
    try {
      [void](Invoke-AHA2DeploymentPreflight -Record $splitRecord @installerRequest -FreeSpaceProvider $freeSpaceProvider -VolumeResolver $volumeResolver)
    } catch {
      $splitThrew = $true
    }
    Assert-AHA2SelfTest $splitThrew "production preflight throws when the temp volume is short"
    Assert-AHA2SelfTest (@($splitRecord.Volumes).Count -eq 2) "split-volume failure records every checked volume"
    Assert-AHA2SelfTest ($splitRecord.Value -match "F:\\" -and $splitRecord.Value -match "temp") "split-volume failure names the short volume and role"
    Assert-AHA2SelfTest ($splitRecord.Value -match "临时目录预留" -and $splitRecord.Value -match ([string]$script:aha2TempSpaceAllowanceBytes)) "installer preflight failure text explains the temp allowance"

    # In-place failures must describe only the roles they checked; temp wording
    # in the user-facing message would contradict the actual check.
    $freeByVolume["D:\"] = [long]($dataRequired + $script:aha2InstallSpaceAllowanceBytes - 1)
    $inPlaceShortRecord = New-AHA2ResultRecord
    $inPlaceShortThrew = $false
    $inPlaceShortMessage = ""
    try {
      [void](Invoke-AHA2DeploymentPreflight -Record $inPlaceShortRecord @inPlaceRequest -FreeSpaceProvider $freeSpaceProvider -VolumeResolver $volumeResolver)
    } catch {
      $inPlaceShortThrew = $true
      $inPlaceShortMessage = $_.Exception.Message
    }
    Assert-AHA2SelfTest $inPlaceShortThrew "production in-place preflight aborts when its volumes are short"
    Assert-AHA2SelfTest ($inPlaceShortMessage -match "磁盘空间不足") "in-place preflight failure gives the Chinese space error"
    Assert-AHA2SelfTest ($inPlaceShortMessage -notmatch "临时目录|temp|300MB") "in-place preflight failure text never mentions temp space"
    Assert-AHA2SelfTest ($inPlaceShortMessage -match "安装目录预留") "in-place preflight failure text describes the install allowance"
    Assert-AHA2SelfTest ($inPlaceShortRecord.Value -eq $inPlaceShortMessage) "in-place failure result value matches the thrown message"

    # Missing sidecars count as zero bytes instead of failing the preflight.
    $databaseOnly = Get-AHA2DeploymentPreflight -DataDirectory $preflightRoot -DatabasePaths @($preflightDatabase) -FreeSpaceProvider { param($Volume) return [long]0 }
    Assert-AHA2SelfTest ($databaseOnly.RequiredBytes -eq ((4096 * 2) + $script:aha2DiskSafetyMarginBytes)) "missing database sidecars contribute no bytes to the requirement"
  } finally {
    Remove-Item -LiteralPath $preflightRoot -Recurse -Force -ErrorAction SilentlyContinue
  }

  # WMI/CIM failure must fail closed: an enumeration error can never count as
  # "no processes", so the stop stage exhausts its timeout and reports failure.
  $stopFailureEnumerator = { throw "CIM unavailable (self-test stub)" }
  $missingProbe = Join-Path ([IO.Path]::GetTempPath()) ("aha2-missing-probe-" + [Guid]::NewGuid().ToString("N"))
  $failedStop = Stop-AHA2ProcessTrees -TimeoutSeconds 0 -ProbePaths @($missingProbe) -ProcessEnumerator $stopFailureEnumerator -AncestorIds @{}
  Assert-AHA2SelfTest (-not $failedStop.Verified) "process enumeration failure never verifies a stop"
  Assert-AHA2SelfTest ($failedStop.EnumerationError -like "*CIM unavailable*") "process enumeration failure is reported in the stop result"

  # An identity lookup that fails cannot prove the process is gone; it must be
  # treated as alive so the stop stage keeps trying.
  $identityTarget = [pscustomobject]@{Id=4242;Name="aha2.exe";CreationDate="t42"}
  Assert-AHA2SelfTest (Test-AHA2ProcessIdentity -Target $identityTarget -ProcessLookup { throw "CIM unavailable (self-test stub)" }) "identity lookup failure is treated as a live process"
  Assert-AHA2SelfTest (-not (Test-AHA2ProcessIdentity -Target $identityTarget -ProcessLookup { $null })) "identity lookup of a vanished process is not alive"

  # Exclusive probe: an unheld file opens, a file already held by this process
  # cannot be opened with FileShare.None (checked on Windows, where the lock
  # semantics match production).
  $tempFile = [IO.Path]::GetTempFileName()
  try {
    Assert-AHA2SelfTest ((Test-AHA2ExclusiveFileProbe -Path $tempFile) -eq $true) "exclusive probe opens an unheld file"
    Assert-AHA2SelfTest ($null -eq (Test-AHA2ExclusiveFileProbe -Path ($tempFile + ".missing"))) "missing probe path is skipped instead of reported locked"
    if ($env:OS -eq "Windows_NT") {
      $held = [IO.File]::Open($tempFile, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
      try {
        Assert-AHA2SelfTest ((Test-AHA2ExclusiveFileProbe -Path $tempFile) -eq $false) "exclusive probe detects a held file"
      } finally {
        $held.Dispose()
      }
    }
  } finally {
    Remove-Item -LiteralPath $tempFile -Force -ErrorAction SilentlyContinue
  }

  # Script encoding: Windows PowerShell 5.1 reads a BOM-less UTF-8 file as ANSI.
  # The handoff must refuse to give it non-ASCII text without a BOM.
  $encodingRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-encoding-selftest-" + [Guid]::NewGuid().ToString("N"))
  try {
    New-Item -ItemType Directory -Path $encodingRoot -Force | Out-Null
    $utf8NoBom = [Text.UTF8Encoding]::new($false)
    $asciiPath = Join-Path $encodingRoot "ascii.ps1"
    [IO.File]::WriteAllText($asciiPath, "Write-Output 1", $utf8NoBom)
    $asciiState = Get-AHA2ScriptEncodingState -Path $asciiPath
    Assert-AHA2SelfTest ($asciiState.Ok -and -not $asciiState.HasBOM -and -not $asciiState.HasNonAscii) "ASCII script without a BOM needs no encoding guard"
    $chinesePath = Join-Path $encodingRoot "chinese-no-bom.ps1"
    [IO.File]::WriteAllBytes($chinesePath, $utf8NoBom.GetBytes("Write-Output 中文"))
    $chineseState = Get-AHA2ScriptEncodingState -Path $chinesePath
    Assert-AHA2SelfTest ($chineseState.HasNonAscii -and -not $chineseState.HasBOM -and -not $chineseState.Ok) "non-ASCII script without a BOM is rejected"
    $encodingThrew = $false
    try {
      [void](Assert-AHA2WindowsPowerShellScriptEncoding -Path $chinesePath)
    } catch {
      $encodingThrew = $_.Exception.Message -match "without a BOM"
    }
    Assert-AHA2SelfTest $encodingThrew "handoff refuses non-ASCII script without a BOM"
    $bomPath = Join-Path $encodingRoot "chinese-with-bom.ps1"
    $bodyBytes = $utf8NoBom.GetBytes("Write-Output 中文")
    $bomBytes = [byte[]]::new(3 + $bodyBytes.Length)
    $bomBytes[0] = 0xEF; $bomBytes[1] = 0xBB; $bomBytes[2] = 0xBF
    [Array]::Copy($bodyBytes, 0, $bomBytes, 3, $bodyBytes.Length)
    [IO.File]::WriteAllBytes($bomPath, $bomBytes)
    $bomState = Get-AHA2ScriptEncodingState -Path $bomPath
    [void](Assert-AHA2WindowsPowerShellScriptEncoding -Path $bomPath)
    Assert-AHA2SelfTest ($bomState.Ok -and $bomState.HasBOM) "non-ASCII script with a BOM is accepted"
  } finally {
    Remove-Item -LiteralPath $encodingRoot -Recurse -Force -ErrorAction SilentlyContinue
  }

  # Backup verification: size and SHA256 must both match.
  $source = [IO.Path]::GetTempFileName()
  $copy = [IO.Path]::GetTempFileName()
  try {
    [IO.File]::WriteAllText($source, "aha2-backup-self-test", [Text.UTF8Encoding]::new($false))
    Copy-Item -LiteralPath $source -Destination $copy -Force
    $good = Test-AHA2BackupCopy -Source $source -Destination $copy
    Assert-AHA2SelfTest ($good.Ok -and $good.SHA256.Length -eq 64) "backup copy with matching size and SHA256 is accepted"
    [IO.File]::WriteAllText($copy, "tampered", [Text.UTF8Encoding]::new($false))
    $bad = Test-AHA2BackupCopy -Source $source -Destination $copy
    Assert-AHA2SelfTest (-not $bad.Ok) "tampered backup copy is rejected"
    $missing = Test-AHA2BackupCopy -Source $source -Destination ($copy + ".missing")
    Assert-AHA2SelfTest (-not $missing.Ok) "missing backup copy is rejected"
    if ($env:OS -eq "Windows_NT") {
      # A transient lock makes Get-FileHash fail non-terminating. The helper
      # must return a named failure instead of crashing on $null.Hash. Restore
      # the matching destination first; the tampered 8-byte copy above would
      # otherwise fail on size and never reach the hash.
      Copy-Item -LiteralPath $source -Destination $copy -Force
      $heldSource = [IO.File]::Open($source, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
      try {
        $locked = Test-AHA2BackupCopy -Source $source -Destination $copy
      } finally {
        $heldSource.Dispose()
      }
      Assert-AHA2SelfTest (-not $locked.Ok) "locked source is reported as a failed backup verification"
      Assert-AHA2SelfTest ($locked.Reason -match [regex]::Escape([IO.Path]::GetFileName($source)) -and $locked.Reason -match "could not hash") "locked-file failure names the file and the reason"
    }
  } finally {
    Remove-Item -LiteralPath $source,$copy -Force -ErrorAction SilentlyContinue
  }

  # Database backup: only the durable files are copied. A locked shm must not
  # participate, and a stale shm in an old backup directory must never be
  # restored over the live data directory.
  Assert-AHA2SelfTest ($databaseBackupFileNames -contains "aha2.db" -and $databaseBackupFileNames -contains "aha2.db-wal" -and $databaseBackupFileNames -notcontains "aha2.db-shm") "database backup list keeps db and WAL and excludes volatile shm"
  $databaseBackupRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-db-backup-selftest-" + [Guid]::NewGuid().ToString("N"))
  try {
    $dbBackupData = Join-Path $databaseBackupRoot "data"
    $dbBackupTarget = Join-Path $databaseBackupRoot "backup"
    New-Item -ItemType Directory -Path $dbBackupData,$dbBackupTarget -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $dbBackupData "aha2.db"), "db-bytes", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $dbBackupData "aha2.db-wal"), "wal-bytes", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $dbBackupData "aha2.db-shm"), "shm-bytes", [Text.UTF8Encoding]::new($false))
    $heldShm = $null
    if ($env:OS -eq "Windows_NT") {
      $heldShm = [IO.File]::Open((Join-Path $dbBackupData "aha2.db-shm"), [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    }
    try {
      $copied = @(Copy-AHA2DatabaseBackup -DataDirectory $dbBackupData -BackupDirectory $dbBackupTarget)
    } finally {
      if ($null -ne $heldShm) { $heldShm.Dispose() }
    }
    Assert-AHA2SelfTest ($copied.Count -eq 2 -and (($copied | ForEach-Object { $_.Name }) -join ",") -eq "aha2.db,aha2.db-wal") "database backup copies only db and WAL"
    Assert-AHA2SelfTest (-not (Test-Path -LiteralPath (Join-Path $dbBackupTarget "aha2.db-shm"))) "database backup never copies shm"
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $dbBackupTarget "aha2.db")) -eq "db-bytes" -and [IO.File]::ReadAllText((Join-Path $dbBackupTarget "aha2.db-wal")) -eq "wal-bytes") "database backup preserves db and WAL bytes"
  } finally {
    Remove-Item -LiteralPath $databaseBackupRoot -Recurse -Force -ErrorAction SilentlyContinue
  }

  # Rollback prerequisites: an in-place update must have a verified backup of
  # every binary it replaces, while a first-time install has nothing to restore.
  Assert-AHA2SelfTest (Test-AHA2RollbackPrerequisite -ExistedBefore $true -BackupPresent $true) "existing binary with a verified backup passes the rollback prerequisite"
  Assert-AHA2SelfTest (-not (Test-AHA2RollbackPrerequisite -ExistedBefore $true -BackupPresent $false)) "existing binary without a backup blocks the deployment"
  Assert-AHA2SelfTest (Test-AHA2RollbackPrerequisite -ExistedBefore $false -BackupPresent $false) "first-time install needs no rollback binary"

  # Database restore ordering: the failed version's shm/wal are deleted
  # unconditionally before the main database is replaced, and a WAL absent
  # from the backup must never survive.
  $plan = Get-AHA2DatabaseRestorePlan -BackupNames @("aha2.db","aha2.db-wal")
  Assert-AHA2SelfTest (-not $plan.Blocked) "restore plan accepts a backup containing aha2.db"
  $planText = (@($plan.Actions | ForEach-Object { $_.Action + ":" + $_.Name }) -join ",")
  Assert-AHA2SelfTest ($planText -eq "delete:aha2.db-shm,delete:aha2.db-wal,replace:aha2.db,replace:aha2.db-wal") "restore removes stale sidecars before replacing the database"
  $planNoWal = Get-AHA2DatabaseRestorePlan -BackupNames @("aha2.db")
  $planNoWalText = (@($planNoWal.Actions | ForEach-Object { $_.Action + ":" + $_.Name }) -join ",")
  Assert-AHA2SelfTest ($planNoWalText -eq "delete:aha2.db-shm,delete:aha2.db-wal,replace:aha2.db") "restore deletes the live WAL even when the backup has no WAL"
  $blocked = Get-AHA2DatabaseRestorePlan -BackupNames @("aha2.db-wal")
  Assert-AHA2SelfTest ($blocked.Blocked) "restore is blocked without a database backup"

  # End-to-end restore in a throw-away directory: the live database must be
  # replaced by the verified backup, the stale sidecars must disappear, and no
  # staging file may survive.
  $restoreRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-restore-selftest-" + [Guid]::NewGuid().ToString("N"))
  try {
    $liveDir = Join-Path $restoreRoot "data"
    $backupDir = Join-Path $restoreRoot "backup"
    New-Item -ItemType Directory -Path $liveDir,$backupDir -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $backupDir "aha2.db"), "verified-backup-db", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $backupDir "aha2.db-wal"), "verified-backup-wal", [Text.UTF8Encoding]::new($false))
    # An old backup directory may still contain a shm from before this fix.
    # Restore must ignore it instead of copying volatile bytes back.
    [IO.File]::WriteAllText((Join-Path $backupDir "aha2.db-shm"), "stale-backup-shm", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db"), "mutated-live-db", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db-wal"), "mutated-live-wal", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db-shm"), "stale-shm", [Text.UTF8Encoding]::new($false))
    Restore-AHA2DatabaseFromBackup -BackupDirectory $backupDir -DataDirectory $liveDir
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $liveDir "aha2.db")) -eq "verified-backup-db") "restore replaces the live database with the verified backup"
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $liveDir "aha2.db-wal")) -eq "verified-backup-wal") "restore replaces the live WAL with the verified backup WAL"
    Assert-AHA2SelfTest (-not (Test-Path -LiteralPath (Join-Path $liveDir "aha2.db-shm"))) "restore removes the stale shared-memory file"
    Assert-AHA2SelfTest (@(Get-ChildItem -LiteralPath $liveDir -Force -Filter "*.tmp").Count -eq 0) "restore leaves no staging files behind"

    # Negative path: a backup without aha2.db must fail without touching any
    # live database byte.
    $blockedBackup = Join-Path $restoreRoot "blocked-backup"
    New-Item -ItemType Directory -Path $blockedBackup -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $blockedBackup "aha2.db-wal"), "only-wal", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db"), "must-stay", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db-wal"), "must-stay-wal", [Text.UTF8Encoding]::new($false))
    $blockedThrew = $false
    try {
      Restore-AHA2DatabaseFromBackup -BackupDirectory $blockedBackup -DataDirectory $liveDir
    } catch {
      $blockedThrew = $true
    }
    Assert-AHA2SelfTest $blockedThrew "restore without a database backup refuses to run"
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $liveDir "aha2.db")) -eq "must-stay") "blocked restore leaves the live database byte-identical"
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $liveDir "aha2.db-wal")) -eq "must-stay-wal") "blocked restore leaves the live WAL byte-identical"

    # A backup whose bytes no longer match the SHA256 recorded at capture time
    # must be rejected before the live database is touched.
    $hashMismatchBackup = Join-Path $restoreRoot "hash-mismatch-backup"
    New-Item -ItemType Directory -Path $hashMismatchBackup -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $hashMismatchBackup "aha2.db"), "tampered-after-backup", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db"), "hash-live-db", [Text.UTF8Encoding]::new($false))
    $hashMismatchThrew = $false
    try {
      Restore-AHA2DatabaseFromBackup -BackupDirectory $hashMismatchBackup -DataDirectory $liveDir -ExpectedSHA256 @{"aha2.db"="0000000000000000000000000000000000000000000000000000000000000000"}
    } catch {
      $hashMismatchThrew = $true
    }
    Assert-AHA2SelfTest $hashMismatchThrew "restore rejects a backup that no longer matches its recorded SHA256"
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $liveDir "aha2.db")) -eq "hash-live-db") "hash-mismatched restore leaves the live database byte-identical"
    Assert-AHA2SelfTest (@(Get-ChildItem -LiteralPath $liveDir -Force -Filter "*.tmp").Count -eq 0) "hash-mismatched restore leaves no staging file behind"

    # Reviewer scenario: the backup has no WAL but the failed version left one
    # behind. Restoring the database without deleting that WAL would let SQLite
    # replay the failed version's changes on top of the restored image.
    $noWalBackup = Join-Path $restoreRoot "no-wal-backup"
    New-Item -ItemType Directory -Path $noWalBackup -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $noWalBackup "aha2.db"), "backup-without-wal", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db"), "failed-version-db", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db-wal"), "failed-version-wal", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $liveDir "aha2.db-shm"), "failed-version-shm", [Text.UTF8Encoding]::new($false))
    Restore-AHA2DatabaseFromBackup -BackupDirectory $noWalBackup -DataDirectory $liveDir
    Assert-AHA2SelfTest ([IO.File]::ReadAllText((Join-Path $liveDir "aha2.db")) -eq "backup-without-wal") "restore without a backup WAL still replaces the database"
    Assert-AHA2SelfTest (-not (Test-Path -LiteralPath (Join-Path $liveDir "aha2.db-wal"))) "restore removes the failed version WAL when the backup has none"
    Assert-AHA2SelfTest (-not (Test-Path -LiteralPath (Join-Path $liveDir "aha2.db-shm"))) "restore removes the failed version SHM when the backup has none"
  } finally {
    Remove-Item -LiteralPath $restoreRoot -Recurse -Force -ErrorAction SilentlyContinue
  }

  # Atomic replacement: an unheld destination takes the new bytes; a held
  # destination must fail without being truncated. The locked-file variant runs
  # only on Windows, where the production sharing semantics apply.
  $atomicRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-atomic-selftest-" + [Guid]::NewGuid().ToString("N"))
  try {
    New-Item -ItemType Directory -Path $atomicRoot -Force | Out-Null
    $atomicSource = Join-Path $atomicRoot "source"
    $atomicDestination = Join-Path $atomicRoot "destination"
    [IO.File]::WriteAllText($atomicSource, "replacement", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($atomicDestination, "original", [Text.UTF8Encoding]::new($false))
    [void](Set-AHA2FileFromSource -Source $atomicSource -Destination $atomicDestination)
    Assert-AHA2SelfTest ([IO.File]::ReadAllText($atomicDestination) -eq "replacement") "atomic replacement writes the new file when unheld"
    Assert-AHA2SelfTest (@(Get-ChildItem -LiteralPath $atomicRoot -Force -Filter "*.tmp").Count -eq 0) "atomic replacement leaves no staging file behind"

    if ($env:OS -eq "Windows_NT") {
      [IO.File]::WriteAllText($atomicSource, "must-not-land", [Text.UTF8Encoding]::new($false))
      [IO.File]::WriteAllText($atomicDestination, "protected", [Text.UTF8Encoding]::new($false))
      $held = [IO.File]::Open($atomicDestination, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::None)
      $lockThrew = $false
      try {
        [void](Set-AHA2FileFromSource -Source $atomicSource -Destination $atomicDestination -RetryMilliseconds 300)
      } catch {
        $lockThrew = $true
      } finally {
        $held.Dispose()
      }
      Assert-AHA2SelfTest $lockThrew "atomic replacement refuses a destination held by a process"
      Assert-AHA2SelfTest ([IO.File]::ReadAllText($atomicDestination) -eq "protected") "refused replacement leaves the destination byte-identical"
      Assert-AHA2SelfTest (@(Get-ChildItem -LiteralPath $atomicRoot -Force -Filter "*.tmp").Count -eq 0) "refused replacement cleans up its staging file"
    }
  } finally {
    Remove-Item -LiteralPath $atomicRoot -Recurse -Force -ErrorAction SilentlyContinue
  }

  # First-time Feishu plugin install followed by a failed deployment: the
  # plugin and manifest did not exist before, so rollback must delete both
  # instead of restarting the old server with an incompatible plugin. Files
  # that did exist before are restored from their verified backup.
  $rollbackRoot = Join-Path ([IO.Path]::GetTempPath()) ("aha2-file-rollback-selftest-" + [Guid]::NewGuid().ToString("N"))
  try {
    $pluginDir = Join-Path $rollbackRoot "plugins"
    $backupDir = Join-Path $rollbackRoot "backup"
    New-Item -ItemType Directory -Path $pluginDir,$backupDir -Force | Out-Null
    $newPlugin = Join-Path $pluginDir "aha2-channel-feishu.exe"
    $newManifest = Join-Path $pluginDir "plugin.json"
    [IO.File]::WriteAllText($newPlugin, "new-plugin", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($newManifest, "new-manifest", [Text.UTF8Encoding]::new($false))
    $newEntries = @(
      [pscustomobject]@{Name="aha2-channel-feishu.exe";BackupPath=(Join-Path $backupDir "aha2-channel-feishu.exe");TargetPath=$newPlugin;ExistedBefore=$false;BackupPresent=$false;SHA256=""},
      [pscustomobject]@{Name="feishu-plugin.json";BackupPath=(Join-Path $backupDir "feishu-plugin.json");TargetPath=$newManifest;ExistedBefore=$false;BackupPresent=$false;SHA256=""}
    )
    foreach ($entry in $newEntries) {
      Assert-AHA2SelfTest ((Get-AHA2FileRollbackAction -ExistedBefore $entry.ExistedBefore -BackupPresent $entry.BackupPresent) -eq "delete") ("new file {0} is scheduled for deletion" -f $entry.Name)
      Assert-AHA2SelfTest ((Invoke-AHA2FileRollbackEntry -Entry $entry) -eq "") ("new file {0} rollback succeeds" -f $entry.Name)
    }
    Assert-AHA2SelfTest (-not (Test-Path -LiteralPath $newPlugin)) "rollback deletes a first-time plugin install"
    Assert-AHA2SelfTest (-not (Test-Path -LiteralPath $newManifest)) "rollback deletes a first-time plugin manifest"

    $existingBackup = Join-Path $backupDir "aha2.exe"
    $existingTarget = Join-Path $rollbackRoot "aha2.exe"
    [IO.File]::WriteAllText($existingBackup, "old-binary", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($existingTarget, "new-binary", [Text.UTF8Encoding]::new($false))
    $existingEntry = [pscustomobject]@{
      Name = "aha2.exe"
      BackupPath = $existingBackup
      TargetPath = $existingTarget
      ExistedBefore = $true
      BackupPresent = $true
      SHA256 = (Get-FileHash -LiteralPath $existingBackup -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    Assert-AHA2SelfTest ((Get-AHA2FileRollbackAction -ExistedBefore $true -BackupPresent $true) -eq "restore") "existing binary with a backup is scheduled for restore"
    Assert-AHA2SelfTest ((Invoke-AHA2FileRollbackEntry -Entry $existingEntry) -eq "") "existing binary rollback succeeds"
    Assert-AHA2SelfTest ([IO.File]::ReadAllText($existingTarget) -eq "old-binary") "existing binary is restored from the verified backup"
    Assert-AHA2SelfTest ((Get-AHA2FileRollbackAction -ExistedBefore $true -BackupPresent $false) -eq "blocked") "existing file without a backup blocks rollback"
  } finally {
    Remove-Item -LiteralPath $rollbackRoot -Recurse -Force -ErrorAction SilentlyContinue
  }

  $okOutput = Test-AHA2QuickCheckOutput -Lines @("ok")
  Assert-AHA2SelfTest $okOutput "quick_check output 'ok' is healthy"
  Assert-AHA2SelfTest (-not (Test-AHA2QuickCheckOutput -Lines @("ok","extra"))) "multiple quick_check rows are not treated as healthy"
  Assert-AHA2SelfTest (-not (Test-AHA2QuickCheckOutput -Lines @("*** in database main ***"))) "quick_check corruption output is rejected"
  Assert-AHA2SelfTest (-not (Test-AHA2QuickCheckOutput -Lines @())) "empty quick_check output is rejected"

  # Result JSON contract: the fields an operator needs to locate the failed
  # stage and decide whether a rollback is trustworthy are always present.
  $record = New-AHA2ResultRecord -Status "failed" -Value "boom" -Stage "install" -LockedFiles @("C:\AHA2\aha2.exe") -RollbackHealth "pending"
  foreach ($field in @("Status","Value","Stage","LockedFiles","BackupSHA256","RollbackVerified","rollback_health","BackupVerified","StopVerified","StopAttempts","ManualRecovery","Volume","FreeBytes","RequiredBytes","Volumes")) {
    Assert-AHA2SelfTest ($record.Contains($field)) ("result record always contains " + $field)
  }
  Assert-AHA2SelfTest ($record.LockedFiles.Count -eq 1 -and $record.RollbackVerified -eq $false) "result record keeps locked files and defaults rollback to false"
  $json = $record | ConvertTo-Json -Depth 8
  Assert-AHA2SelfTest ($json.Contains('"LockedFiles"') -and $json.Contains('"BackupSHA256"') -and $json.Contains('"rollback_health"')) "result record serializes the diagnostic fields"
  $deduped = New-AHA2ResultRecord
  Add-AHA2LockedFile -Record $deduped -Paths @("C:\x\aha2.exe","C:\x\aha2.exe","C:\x\aha2.db")
  Add-AHA2LockedFile -Record $deduped -Paths @("C:\x\aha2.exe")
  Assert-AHA2SelfTest ($deduped.LockedFiles.Count -eq 2) "locked file list is de-duplicated"

  # Detached handoff: a worker that exits before writing a result must be a
  # failure with the log tail, not a successful "scheduled" return.
  Assert-AHA2SelfTest ((Resolve-AHA2DetachedHandoff -ResultWritten $true -WorkerStarted $true -WorkerRunning $false) -eq "completed") "handoff completes when the worker wrote a result"
  Assert-AHA2SelfTest ((Resolve-AHA2DetachedHandoff -ResultWritten $false -WorkerStarted $true -WorkerRunning $false) -eq "failed") "handoff fails when a started worker exits without a result"
  Assert-AHA2SelfTest ((Resolve-AHA2DetachedHandoff -ResultWritten $false -WorkerStarted $false -WorkerRunning $false) -eq "scheduled") "handoff stays scheduled before the worker starts"
  Assert-AHA2SelfTest ((Resolve-AHA2DetachedHandoff -ResultWritten $false -WorkerStarted $true -WorkerRunning $true) -eq "scheduled") "handoff stays scheduled while the worker runs"
  $handoffStartUtc = [DateTime]::new(2026, 1, 1, 0, 0, 0, [DateTimeKind]::Utc)
  Assert-AHA2SelfTest (Test-AHA2WorkerStartedSince -LastRunTime $handoffStartUtc.AddSeconds(5) -StartUtc $handoffStartUtc) "a recent UTC task start counts as started"
  Assert-AHA2SelfTest (-not (Test-AHA2WorkerStartedSince -LastRunTime $handoffStartUtc.AddSeconds(-30) -StartUtc $handoffStartUtc)) "an old UTC task start is not mistaken for this worker"
  # Non-UTC case: 07:00+08:00 is 23:00Z the previous day, before the 00:00Z
  # handoff. Comparing wall-clock ticks would wrongly report "started".
  $plus8Stale = [DateTimeOffset]::new(2026, 1, 1, 7, 0, 0, [TimeSpan]::FromHours(8))
  $plus8Started = [DateTimeOffset]::new(2026, 1, 1, 8, 0, 30, [TimeSpan]::FromHours(8))
  $plus8StaleUtc = Get-AHA2UtcTimestamp -Value $plus8Stale
  Assert-AHA2SelfTest ($plus8StaleUtc.Kind -eq [DateTimeKind]::Utc -and $plus8StaleUtc.Hour -eq 23 -and $plus8StaleUtc.Day -eq 31) "a +08 task start is normalized to UTC"
  Assert-AHA2SelfTest (-not (Test-AHA2WorkerStartedSince -LastRunTime $plus8Stale -StartUtc $handoffStartUtc)) "a stale run from a +08 timezone is not treated as started"
  Assert-AHA2SelfTest (Test-AHA2WorkerStartedSince -LastRunTime $plus8Started -StartUtc $handoffStartUtc) "a fresh run from a +08 timezone counts as started"
  $localStale = $handoffStartUtc.AddHours(-1).ToLocalTime()
  $localStarted = $handoffStartUtc.AddHours(1).ToLocalTime()
  Assert-AHA2SelfTest (-not (Test-AHA2WorkerStartedSince -LastRunTime $localStale -StartUtc $handoffStartUtc)) "a stale local task start is not mistaken for this worker"
  Assert-AHA2SelfTest (Test-AHA2WorkerStartedSince -LastRunTime $localStarted -StartUtc $handoffStartUtc) "a fresh local task start counts as started"
  $handoffLog = Join-Path ([IO.Path]::GetTempPath()) ("aha2-handoff-log-" + [Guid]::NewGuid().ToString("N"))
  try {
    [IO.File]::WriteAllText($handoffLog, "first line`nworker-stderr-tail-marker`nlast line", [Text.UTF8Encoding]::new($false))
    $handoffThrew = $false
    $handoffMessage = ""
    try {
      Assert-AHA2DetachedWorkerAlive -TaskInfo ([pscustomobject]@{LastTaskResult=1}) -LogPath $handoffLog
    } catch {
      $handoffThrew = $true
      $handoffMessage = $_.Exception.Message
    }
    Assert-AHA2SelfTest $handoffThrew "early worker exit is thrown instead of returning 0"
    Assert-AHA2SelfTest ($handoffMessage -match "worker-stderr-tail-marker" -and $handoffMessage -match "LastTaskResult=1") "early-exit error carries the log tail and task result"
    $noLogThrew = $false
    $noLogMessage = ""
    try {
      Assert-AHA2DetachedWorkerAlive -TaskInfo ([pscustomobject]@{LastTaskResult=0}) -LogPath ($handoffLog + ".missing")
    } catch {
      $noLogThrew = $true
      $noLogMessage = $_.Exception.Message
    }
    Assert-AHA2SelfTest ($noLogThrew -and $noLogMessage -match "parser error") "early exit without a log explains the likely parser failure"
  } finally {
    Remove-Item -LiteralPath $handoffLog -Force -ErrorAction SilentlyContinue
  }

  # A failed install replacement must name the destination in LockedFiles.
  $installRecord = New-AHA2ResultRecord
  $missingSource = Join-Path ([IO.Path]::GetTempPath()) ("aha2-missing-source-" + [Guid]::NewGuid().ToString("N"))
  $installDestination = Join-Path ([IO.Path]::GetTempPath()) ("aha2-install-target-" + [Guid]::NewGuid().ToString("N"))
  $installThrew = $false
  try {
    Set-AHA2InstalledFile -Record $installRecord -Source $missingSource -Destination $installDestination
  } catch {
    $installThrew = $true
  }
  Assert-AHA2SelfTest $installThrew "failed install replacement throws"
  Assert-AHA2SelfTest (@($installRecord.LockedFiles) -contains $installDestination) "failed install replacement records the destination in LockedFiles"

  if ($script:selfTestFailures.Count -gt 0) {
    throw ("AHA2 deployment self-test failed ({0} of {1} checks): {2}" -f $script:selfTestFailures.Count,$script:selfTestChecks,($script:selfTestFailures -join "; "))
  }
  Write-Host ("AHA2 deployment self-test passed: {0} checks." -f $script:selfTestChecks)
}

function Get-AHA2ProcessList {
  # Win32_Process enumeration must fail loudly: swallowing the error would let
  # the stop stage verify a stop against a process table it never actually read.
  $processes = @(Get-CimInstance Win32_Process -ErrorAction Stop)
  if ($processes.Count -eq 0) {
    # Windows always has system processes; an empty table means the provider
    # returned nonsense, which is not evidence that AHA2 is stopped.
    throw "Win32_Process enumeration returned an empty process table."
  }
  return $processes
}

function Get-AHA2ProcessTree {
  param([scriptblock]$ProcessEnumerator = $null)
  if ($null -eq $ProcessEnumerator) { $ProcessEnumerator = { Get-AHA2ProcessList } }
  $processes = @(& $ProcessEnumerator)
  return @(Get-AHA2TargetProcessIdentities -Processes $processes -RootNames $aha2ProcessNames)
}

function Test-AHA2ProcessIdentity($Target, [scriptblock]$ProcessLookup = $null) {
  try {
    if ($null -ne $ProcessLookup) {
      $current = & $ProcessLookup $Target.Id
    } else {
      $current = Get-CimInstance Win32_Process -Filter "ProcessId=$($Target.Id)" -ErrorAction Stop
    }
  } catch {
    # A failed lookup cannot prove the process is gone. Treat it as still
    # alive so the caller keeps trying to stop it instead of skipping the kill.
    return $true
  }
  if (-not $current) { return $false }
  return $current.Name -ieq $Target.Name -and
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

function Get-AHA2StopProbePaths {
  return @($server, $tray, $plugin, $databasePath, $databaseWalPath, $databaseShmPath) |
    Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
}

function Stop-AHA2ProcessTrees {
  param(
    [int]$TimeoutSeconds = $StopTimeoutSeconds,
    [string[]]$ProbePaths = @(),
    # Injection points for the self-test. Production always uses the real CIM
    # enumeration and ancestor lookup.
    [scriptblock]$ProcessEnumerator = $null,
    [hashtable]$AncestorIds = $null
  )
  if ($ProbePaths.Count -eq 0) { $ProbePaths = @(Get-AHA2StopProbePaths) }
  $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
  $attempts = 0
  $lockedFiles = @()
  $remainingProcesses = @()
  $enumerationError = ""

  while ($true) {
    $attempts++
    $targets = @()
    $enumerationError = ""
    try {
      # The ancestor lookup reads CIM too, so it belongs to the same fail-closed
      # attempt: if either query is unavailable, this attempt cannot verify.
      $ancestorIdsForAttempt = if ($null -ne $AncestorIds) { $AncestorIds } else { Get-AHA2AncestorIds }
      $targets = @(Get-AHA2ProcessTree -ProcessEnumerator $ProcessEnumerator |
        Where-Object { -not $ancestorIdsForAttempt.ContainsKey([int]$_.Id) })
    } catch {
      # Fail closed: an unavailable WMI/CIM service means we cannot prove the
      # old processes are gone, so this attempt can never verify a stop.
      $enumerationError = $_.Exception.Message
    }
    $remainingProcesses = $targets
    if (-not $enumerationError -and $targets.Count -gt 0) {
      foreach ($root in @($targets | Where-Object { $_.Depth -eq 0 })) {
        Invoke-TaskKillNoThrow $root.Id
      }
      foreach ($target in @($targets | Sort-Object Depth -Descending)) {
        if (Test-AHA2ProcessIdentity $target) {
          Stop-Process -Id $target.Id -Force -ErrorAction SilentlyContinue
        }
      }
      Start-Sleep -Milliseconds 200
      try {
        $targets = @(Get-AHA2ProcessTree -ProcessEnumerator $ProcessEnumerator |
          Where-Object { -not $ancestorIdsForAttempt.ContainsKey([int]$_.Id) })
        $remainingProcesses = $targets
      } catch {
        $enumerationError = $_.Exception.Message
      }
    }

    $lockedFiles = @()
    foreach ($probePath in @($ProbePaths)) {
      if ((Test-AHA2ExclusiveFileProbe -Path $probePath) -eq $false) {
        $lockedFiles += $probePath
      }
    }

    $satisfied = (-not $enumerationError) -and (Test-AHA2StopSatisfied -RemainingProcesses $targets -LockedFiles $lockedFiles)
    $decision = Get-AHA2StopAttemptDecision -Satisfied $satisfied -TimedOut ([DateTime]::UtcNow -ge $deadline)
    if ($decision -eq "verified") {
      return [pscustomobject]@{
        Verified = $true
        Attempts = $attempts
        LockedFiles = @()
        Processes = @()
        EnumerationError = ""
      }
    }
    if ($decision -eq "timeout") { break }
    Start-Sleep -Milliseconds 300
  }

  $remainingDescription = @($remainingProcesses | ForEach-Object { "$($_.Name) PID $($_.Id)" })
  return [pscustomobject]@{
    Verified = $false
    Attempts = $attempts
    LockedFiles = @($lockedFiles)
    Processes = $remainingDescription
    EnumerationError = $enumerationError
  }
}

if ($SelfTest) {
  Invoke-AHA2SelfTest
  return
}

# ---------------------------------------------------------------------------
# Windows prerequisites and path validation.
# ---------------------------------------------------------------------------

$powershellExecutable = Join-Path $systemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$taskkillExecutable = Join-Path $systemRoot "System32\taskkill.exe"
foreach ($requiredSystemExecutable in @($powershellExecutable, $taskkillExecutable)) {
  if (-not (Test-Path -LiteralPath $requiredSystemExecutable -PathType Leaf)) {
    throw "Required Windows system executable was not found: $requiredSystemExecutable"
  }
}
if ([string]::IsNullOrWhiteSpace($InputExe)) { throw "-InputExe is required." }
if ([string]::IsNullOrWhiteSpace($InputTrayExe)) { throw "-InputTrayExe is required." }
if ($StopTimeoutSeconds -lt 5 -or $StopTimeoutSeconds -gt 600) {
  throw "StopTimeoutSeconds must be between 5 and 600."
}
if ($HealthTimeoutSeconds -lt 5 -or $HealthTimeoutSeconds -gt 300) {
  throw "HealthTimeoutSeconds must be between 5 and 300."
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
$databasePath = Join-Path $data "aha2.db"
$databaseWalPath = Join-Path $data "aha2.db-wal"
$databaseShmPath = Join-Path $data "aha2.db-shm"
$serverExisted = Test-Path -LiteralPath $server -PathType Leaf
$trayExisted = Test-Path -LiteralPath $tray -PathType Leaf
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
  [pscustomobject]@{Mode=$deploymentMode;Repository=$repo;InstallDir=$install;DataDir=$data;Listen=$Listen;HealthURL=$HealthURL;WriteAccessVerified=$true;ElevationRequired=$false;StopTimeoutSeconds=$StopTimeoutSeconds}
  return
}

if ($DetachedWorker) {
  # A worker launched by another PowerShell host (for example pwsh, which
  # reads UTF-8 without BOM) must not hand this file to Windows PowerShell 5.1
  # with the BOM stripped. Fail before any deployment work instead of exiting
  # silently; the outer handoff reports the failure with this message.
  [void](Assert-AHA2WindowsPowerShellScriptEncoding -Path $PSCommandPath)
  Start-Transcript -LiteralPath (Join-Path $repo "dist\user-deploy.log") -Force | Out-Null
}

# ---------------------------------------------------------------------------
# Windows helpers: process stop verification, exclusive probes, atomic file
# replacement, and SQLite quick_check.
# ---------------------------------------------------------------------------

function Test-AHA2Ancestor {
  $current = $PID
  for ($depth = 0; $depth -lt 32 -and $current -gt 0; $depth++) {
    try {
      $process = Get-CimInstance Win32_Process -Filter "ProcessId=$current" -ErrorAction Stop
    } catch {
      # The ancestry cannot be inspected. Detaching is the safe choice: it
      # keeps the deployment out of the AHA2 process tree it is about to stop,
      # and the detached worker's stop stage still fails closed on CIM errors.
      return $true
    }
    if (-not $process) { return $false }
    if ($process.Name -ieq "aha2.exe") { return $true }
    $current = [int]$process.ParentProcessId
  }
  return $false
}

function Get-AHA2AncestorIds {
  $ids = @{}
  $current = $PID
  for ($depth = 0; $depth -lt 32 -and $current -gt 0; $depth++) {
    $ids[[int]$current] = $true
    # Failure here is not "no ancestors": the stop stage must not kill blindly,
    # so let the CIM error propagate to its fail-closed retry loop.
    $process = Get-CimInstance Win32_Process -Filter "ProcessId=$current" -ErrorAction Stop
    if (-not $process) { break }
    $current = [int]$process.ParentProcessId
  }
  return $ids
}

function ConvertTo-CommandLineArgument([string]$Value) {
  if ($Value.Contains('"')) { throw "Scheduled deployment argument contains an unsupported quote." }
  return '"' + $Value + '"'
}

function Write-DeploymentResult($Record) {
  if ([string]::IsNullOrWhiteSpace($ResultPath)) { return }
  $temporaryTarget = ""
  try {
    $target = [IO.Path]::GetFullPath($ResultPath)
    $repoPrefix = $repo.TrimEnd('\') + '\'
    if (-not $target.StartsWith($repoPrefix, [StringComparison]::OrdinalIgnoreCase)) {
      Write-Warning "Deployment result path must stay inside the repository: $target"
      return
    }
    New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
    $Record.FinishedAt = [DateTime]::UtcNow.ToString("o")
    $json = $Record | ConvertTo-Json -Depth 8
    $temporaryTarget = Join-Path (Split-Path -Parent $target) (".{0}.{1}.tmp" -f [IO.Path]::GetFileName($target),[Guid]::NewGuid().ToString("N"))
    try {
      [IO.File]::WriteAllText($temporaryTarget, $json, [Text.UTF8Encoding]::new($false))
      if (Test-Path -LiteralPath $target -PathType Leaf) {
        # See Set-AHA2StagedFileAtomic: $null would become an empty backup name.
        [IO.File]::Replace($temporaryTarget, $target, [NullString]::Value)
      } else {
        [IO.File]::Move($temporaryTarget, $target)
      }
      $temporaryTarget = ""
      return
    } catch {
      $atomicError = $_.Exception.Message
    }
    # The result file is diagnostic. A locked target or a filesystem without
    # atomic replace support (e.g. some UNC shares) must never turn a
    # deployment that already succeeded into a rollback, so fall back to a
    # direct write and otherwise only warn.
    try {
      [IO.File]::WriteAllText($target, $json, [Text.UTF8Encoding]::new($false))
    } catch {
      Write-Warning ("Could not write deployment result {0}: atomic error: {1}; direct write error: {2}" -f $target,$atomicError,$_.Exception.Message)
    }
  } catch {
    Write-Warning ("Could not prepare deployment result {0}: {1}" -f $ResultPath,$_.Exception.Message)
  } finally {
    if ($temporaryTarget -and (Test-Path -LiteralPath $temporaryTarget -PathType Leaf)) {
      Remove-Item -LiteralPath $temporaryTarget -Force -ErrorAction SilentlyContinue
    }
  }
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

$script:sqliteQuickCheckSource = @'
using System;
using System.Runtime.InteropServices;
using System.Text;

public static class Aha2SqliteQuickCheck
{
    private const int SQLITE_OK = 0;
    private const int SQLITE_ROW = 100;
    private const int SQLITE_DONE = 101;

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern int sqlite3_open_v2(byte[] filename, out IntPtr db, int flags, IntPtr vfs);

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern int sqlite3_prepare_v2(IntPtr db, byte[] sql, int nByte, out IntPtr statement, IntPtr tail);

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern int sqlite3_step(IntPtr statement);

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern IntPtr sqlite3_column_text(IntPtr statement, int column);

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern int sqlite3_finalize(IntPtr statement);

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern int sqlite3_close(IntPtr db);

    [DllImport("winsqlite3.dll", CallingConvention = CallingConvention.Cdecl)]
    private static extern IntPtr sqlite3_errmsg(IntPtr db);

    public static string Run(string path)
    {
        byte[] fileName = Encoding.UTF8.GetBytes(path + "\0");
        IntPtr db;
        // SQLITE_OPEN_READWRITE (2) lets SQLite recover a restored WAL; no
        // process holds the file at this point because the stop stage verified it.
        int flags = 2 | 0x8000;
        int rc = sqlite3_open_v2(fileName, out db, flags, IntPtr.Zero);
        if (rc != SQLITE_OK)
        {
            string openError = db != IntPtr.Zero ? Marshal.PtrToStringAnsi(sqlite3_errmsg(db)) : "";
            if (db != IntPtr.Zero) { sqlite3_close(db); }
            return "ERROR " + rc + ": " + openError;
        }
        IntPtr statement = IntPtr.Zero;
        StringBuilder rows = new StringBuilder();
        try
        {
            byte[] sql = Encoding.UTF8.GetBytes("PRAGMA quick_check;\0");
            rc = sqlite3_prepare_v2(db, sql, -1, out statement, IntPtr.Zero);
            if (rc != SQLITE_OK)
            {
                return "ERROR " + rc + ": " + Marshal.PtrToStringAnsi(sqlite3_errmsg(db));
            }
            while (true)
            {
                rc = sqlite3_step(statement);
                if (rc == SQLITE_ROW)
                {
                    IntPtr text = sqlite3_column_text(statement, 0);
                    if (text != IntPtr.Zero)
                    {
                        if (rows.Length > 0) { rows.Append('\n'); }
                        rows.Append(Marshal.PtrToStringAnsi(text));
                    }
                    continue;
                }
                if (rc == SQLITE_DONE) { break; }
                return "ERROR " + rc + ": " + Marshal.PtrToStringAnsi(sqlite3_errmsg(db));
            }
            return rows.ToString();
        }
        finally
        {
            if (statement != IntPtr.Zero) { sqlite3_finalize(statement); }
            sqlite3_close(db);
        }
    }
}
'@

function Invoke-AHA2SqliteQuickCheck {
  param([string]$DatabasePath)
  if (-not (Test-Path -LiteralPath $DatabasePath -PathType Leaf)) {
    return [pscustomobject]@{Ok=$false;Health="failed: restored database file is missing";Output=""}
  }

  $output = $null
  $checkerError = ""
  try {
    if (-not ("Aha2SqliteQuickCheck" -as [type])) {
      Add-Type -TypeDefinition $script:sqliteQuickCheckSource -Language CSharp -ErrorAction Stop
    }
    $output = [Aha2SqliteQuickCheck]::Run($DatabasePath)
  } catch {
    $checkerError = $_.Exception.Message
  }

  if ($null -eq $output -and $checkerError) {
    # Older images without winsqlite3.dll: use the sqlite3 shell when present.
    $sqliteCommand = Get-Command sqlite3.exe -ErrorAction SilentlyContinue
    if ($sqliteCommand) {
      try {
        $output = @(& $sqliteCommand.Source $DatabasePath "PRAGMA quick_check;" 2>&1) -join "`n"
      } catch {
        $checkerError = $_.Exception.Message
      }
    }
  }

  if ($null -eq $output) {
    return [pscustomobject]@{Ok=$false;Health=("unavailable: " + $checkerError);Output=""}
  }
  if ($output -like "ERROR *") {
    return [pscustomobject]@{Ok=$false;Health=$output;Output=$output}
  }
  $lines = @($output -split "`r?`n")
  if (Test-AHA2QuickCheckOutput -Lines $lines) {
    return [pscustomobject]@{Ok=$true;Health="ok";Output=$output}
  }
  return [pscustomobject]@{Ok=$false;Health=("failed: PRAGMA quick_check returned: " + ($lines -join " | "));Output=$output}
}

function Get-AHA2ManualRecoveryText {
  param($Record, [string]$BackupDirectory)
  $hashText = @($Record.BackupSHA256.Keys | ForEach-Object { "$_=$($Record.BackupSHA256[$_])" }) -join ", "
  $message = "AHA2 was left stopped because rollback could not be verified. Backup: {0}. Verified backup SHA256: {1}. " +
    "Manual recovery: stop aha2.exe, aha2-tray.exe and aha2-channel-feishu.exe; move aha2.db, aha2.db-wal and aha2.db-shm out of {2}; " +
    "copy the backup aha2.db into {2} (and aha2.db-wal only when the backup contains it); run PRAGMA quick_check; " +
    "start AHA2 only when it returns ok."
  return ($message -f $BackupDirectory,$hashText,$data)
}

# ---------------------------------------------------------------------------
# Deployment.
# ---------------------------------------------------------------------------

if (-not $DetachedWorker -and (Test-AHA2Ancestor)) {
  if ([string]::IsNullOrWhiteSpace($ResultPath)) {
    $ResultPath = Join-Path $repo "dist\user-deploy-result.json"
  }
  # The scheduled worker runs under Windows PowerShell 5.1. Refuse to hand it a
  # copy whose BOM was stripped in packaging or download: 5.1 would read the
  # non-ASCII diagnostics as ANSI and exit with a parser error before writing a
  # result. This is the fail-loud counterpart to the silent handoff reported in
  # the 0.5.471 deployment.
  [void](Assert-AHA2WindowsPowerShellScriptEncoding -Path $PSCommandPath)
  $workerLogPath = Join-Path $repo "dist\user-deploy.log"
  # A result from an earlier deployment must not be mistaken for this worker's
  # output, so only a file written after the handoff starts counts.
  $resultWrittenBefore = if (Test-Path -LiteralPath $ResultPath -PathType Leaf) { (Get-Item -LiteralPath $ResultPath).LastWriteTimeUtc } else { [DateTime]::MinValue }
  $handoffStartedUtc = [DateTime]::UtcNow
  $workerArguments = @(
    "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", $PSCommandPath,
    "-DetachedWorker", "-RepoPath", $repo, "-InputExe", $serverInput, "-InputTrayExe", $trayInput,
    "-InstallDir", $install, "-DataDir", $data, "-Version", $Version, "-TaskName", $TaskName,
    "-UpdateTaskName", $UpdateTaskName, "-Listen", $Listen, "-HealthURL", $HealthURL,
    "-HealthTimeoutSeconds", [string]$HealthTimeoutSeconds, "-StopTimeoutSeconds", [string]$StopTimeoutSeconds,
    "-ResultPath", $ResultPath
  )
  if ($AgentAPIURL) { $workerArguments += @("-AgentAPIURL", $AgentAPIURL) }
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

  # Wait briefly for evidence that the worker actually started and either
  # wrote a result or is still running. A worker that exits without a result
  # (parser error, missing file, immediate crash) must fail this call instead
  # of looking like a successful handoff.
  $workerStarted = $false
  $handoffState = "scheduled"
  $handoffDeadline = $handoffStartedUtc.AddSeconds([Math]::Max(1, $HandoffTimeoutSeconds))
  while ([DateTime]::UtcNow -lt $handoffDeadline) {
    $resultWritten = $false
    if (Test-Path -LiteralPath $ResultPath -PathType Leaf) {
      $resultWritten = (Get-Item -LiteralPath $ResultPath).LastWriteTimeUtc -gt $resultWrittenBefore
    }
    $task = Get-ScheduledTask -TaskName $UpdateTaskName -ErrorAction SilentlyContinue
    $taskInfo = $null
    try {
      $taskInfo = Get-ScheduledTaskInfo -TaskName $UpdateTaskName -ErrorAction Stop
    } catch {}
    $workerRunning = ($null -ne $task) -and ([string]$task.State -eq "Running")
    if ($workerRunning) { $workerStarted = $true }
    if ($null -ne $taskInfo -and (Test-AHA2WorkerStartedSince -LastRunTime $taskInfo.LastRunTime -StartUtc $handoffStartedUtc)) {
      $workerStarted = $true
    }
    $decision = Resolve-AHA2DetachedHandoff -ResultWritten $resultWritten -WorkerStarted $workerStarted -WorkerRunning $workerRunning
    if ($decision -eq "completed") {
      $handoffState = "completed"
      break
    }
    if ($decision -eq "failed") {
      Assert-AHA2DetachedWorkerAlive -TaskInfo $taskInfo -LogPath $workerLogPath
    }
    Start-Sleep -Milliseconds 250
  }
  [pscustomobject]@{Mode=$deploymentMode;State=$handoffState;Result=$ResultPath;WriteAccessVerified=$true;ElevationRequired=$false}
  return
}

$stamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
$backup = Join-Path $data "backups\user-installed-$stamp"

$state = New-AHA2ResultRecord
$state.Backup = $backup
$state.Mode = $deploymentMode
$state.UpdateType = if ($UpdateExistingInstallInPlace) { "existing-install-in-place" } else { "installer" }

# --- preflight: refuse to stop AHA2 or touch any file when the data, install,
# or temp volume cannot hold its share of the deployment. This runs before the
# stop/backup stages and outside the deployment try block, so a failure never
# reaches the stop, rollback, or file-replacement paths.
$preflightRequest = Get-AHA2PreflightRequest -DataDirectory $data -DatabasePaths @($databasePath,$databaseWalPath,$databaseShmPath) -InstallDirectory $install -TempDirectory ([IO.Path]::GetTempPath()) -UseInstaller (-not $UpdateExistingInstallInPlace)
try {
  $preflight = Invoke-AHA2DeploymentPreflight -Record $state @preflightRequest
} catch {
  Write-DeploymentResult $state
  throw
}
foreach ($volumeCheck in @($preflight.Volumes)) {
  Write-Host ("Deployment preflight {0} volume {1}: FreeBytes={2} ({3}), RequiredBytes={4} ({5})" -f @(
    ($volumeCheck.Roles -join "+"),
    $volumeCheck.Volume,
    (Format-AHA2ByteSize $volumeCheck.FreeBytes),
    $volumeCheck.FreeBytes,
    (Format-AHA2ByteSize $volumeCheck.RequiredBytes),
    $volumeCheck.RequiredBytes
  ))
}

if (-not $UpdateExistingInstallInPlace) {
  $buildArgs = @("-ExecutionPolicy","Bypass","-File",$builder,"-RepoPath",$repo,"-InputExe",$serverInput,"-InputTrayExe",$trayInput,"-OutputDir",$installerDir,"-Version",$Version,"-PerUser")
  if ($InputFeishuPlugin -and $InputFeishuManifest) {
    $buildArgs += @("-InputFeishuPlugin",$InputFeishuPlugin,"-InputFeishuManifest",$InputFeishuManifest)
  }
  & $powershellExecutable @buildArgs
  if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $installer -PathType Leaf)) { throw "Per-user installer build failed." }
}

New-Item -ItemType Directory -Path $backup -Force | Out-Null
$previousTaskXML = ""
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  $previousTaskXML = Export-ScheduledTask -TaskName $TaskName
  [IO.File]::WriteAllText((Join-Path $backup "AHA2-User-Task.xml"), $previousTaskXML, [Text.UTF8Encoding]::new($false))
}

$databaseExistedBefore = Test-Path -LiteralPath $databasePath -PathType Leaf
$binaryBackups = @()
$runtimeFileBackups = @()
$backupVerified = $false
$mutationStageReached = $false

try {
  New-Item -ItemType Directory -Path $data -Force | Out-Null

  # --- stop: refuse to touch anything until every process has exited and the
  # binary/database handles can be opened exclusively.
  $state.Stage = "stop"
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  $stop = Stop-AHA2ProcessTrees
  $state.StopAttempts = $stop.Attempts
  if (-not $stop.Verified) {
    Add-AHA2LockedFile -Record $state -Paths $stop.LockedFiles
    $state.StopVerified = $false
    $detail = @()
    if ($stop.LockedFiles.Count -gt 0) { $detail += ("locked files: " + ($stop.LockedFiles -join ", ")) }
    if ($stop.Processes.Count -gt 0) { $detail += ("remaining processes: " + ($stop.Processes -join ", ")) }
    if ($stop.EnumerationError) { $detail += ("process enumeration failed: " + $stop.EnumerationError) }
    throw ("AHA2 processes or file handles did not stop within {0}s; refusing to modify the installation. {1}" -f $StopTimeoutSeconds,($detail -join "; "))
  }
  $state.StopVerified = $true

  # --- backup: copy only after the exclusive probes passed, then verify size
  # and SHA256 of every copied file before any installed file is replaced.
  # aha2.db-shm is intentionally absent: it is a volatile cache that SQLite
  # rebuilds, and hashing it while SQLite still touches it was what aborted
  # the 0.5.471 backup stage.
  $state.Stage = "backup"
  foreach ($copy in @(Copy-AHA2DatabaseBackup -DataDirectory $data -BackupDirectory $backup)) {
    $state.BackupSHA256[$copy.Name] = $copy.SHA256
    $state.BackupSizes[$copy.Name] = $copy.Size
  }
  foreach ($name in @("secrets.json","setup-token")) {
    $sourcePath = Join-Path $data $name
    if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) { continue }
    $destinationPath = Join-Path $backup $name
    Copy-Item -LiteralPath $sourcePath -Destination $destinationPath -Force
    $check = Test-AHA2BackupCopy -Source $sourcePath -Destination $destinationPath
    if (-not $check.Ok) {
      throw ("Backup verification failed for {0}: {1}" -f $name,$check.Reason)
    }
    $state.BackupSHA256[$name] = $check.SHA256
    $state.BackupSizes[$name] = $check.Size
    $runtimeFileBackups += [pscustomobject]@{
      Name = $name
      BackupPath = $destinationPath
      TargetPath = $sourcePath
      SHA256 = $check.SHA256
    }
  }
  if ($databaseExistedBefore -and -not $state.BackupSHA256.Contains("aha2.db")) {
    throw "The requested aha2.db backup was not captured and verified."
  }
  foreach ($entry in @(
    @{Source=$server;Name="aha2.exe";Target=$server;ExistedBefore=$serverExisted},
    @{Source=$tray;Name="aha2-tray.exe";Target=$tray;ExistedBefore=$trayExisted},
    @{Source=$plugin;Name="aha2-channel-feishu.exe";Target=$plugin;ExistedBefore=$pluginExisted},
    @{Source=$pluginManifest;Name="feishu-plugin.json";Target=$pluginManifest;ExistedBefore=$pluginManifestExisted}
  )) {
    $destinationPath = Join-Path $backup $entry.Name
    $backupPresent = $false
    $entryHash = ""
    if (Test-Path -LiteralPath $entry.Source -PathType Leaf) {
      Copy-Item -LiteralPath $entry.Source -Destination $destinationPath -Force
      $check = Test-AHA2BackupCopy -Source $entry.Source -Destination $destinationPath
      if (-not $check.Ok) {
        throw ("Backup verification failed for {0}: {1}" -f $entry.Name,$check.Reason)
      }
      $state.BackupSHA256[$entry.Name] = $check.SHA256
      $state.BackupSizes[$entry.Name] = $check.Size
      $backupPresent = $true
      $entryHash = $check.SHA256
    }
    # Record every target, including files that did not exist before this
    # deployment: rollback must delete the ones a failed install created.
    $binaryBackups += [pscustomobject]@{
      Name = $entry.Name
      BackupPath = $destinationPath
      TargetPath = $entry.Target
      ExistedBefore = [bool]$entry.ExistedBefore
      BackupPresent = $backupPresent
      SHA256 = $entryHash
    }
  }
  # A file that existed before the deployment must have a verified backup
  # before any installed file is replaced; otherwise rollback cannot restore it.
  foreach ($requiredFile in $binaryBackups) {
    if (-not (Test-AHA2RollbackPrerequisite -ExistedBefore ([bool]$requiredFile.ExistedBefore) -BackupPresent ([bool]$requiredFile.BackupPresent))) {
      throw ("Required rollback file {0} is missing from the backup." -f $requiredFile.Name)
    }
  }
  $backupVerified = $true
  $state.BackupVerified = $true

  # --- install: every installed file is staged beside its destination, hash
  # checked, and atomically replaced. A locked destination throws instead of
  # being truncated.
  $state.Stage = "install"
  $mutationStageReached = $true
  if ($UpdateExistingInstallInPlace) {
    Set-AHA2InstalledFile -Record $state -Source $serverInput -Destination $server
    Set-AHA2InstalledFile -Record $state -Source $trayInput -Destination $tray
    if ($pluginInput -and $pluginManifestInput) {
      New-Item -ItemType Directory -Path (Split-Path -Parent $plugin) -Force | Out-Null
      Set-AHA2InstalledFile -Record $state -Source $pluginInput -Destination $plugin
      Set-AHA2InstalledFile -Record $state -Source $pluginManifestInput -Destination $pluginManifest
    }
  } else {
    # The repo lives on the WSL filesystem, so $installer is a UNC path. Windows
    # shows "Open File - Security Warning" for executables launched from a
    # network location and waits for a click, which breaks unattended deploys.
    # Stage a local copy and clear any Mark-of-the-Web before launching it.
    $stagedInstaller = Join-Path ([IO.Path]::GetTempPath()) ("AHA2-Setup-User-x64-{0}.exe" -f $stamp)
    Copy-Item -LiteralPath $installer -Destination $stagedInstaller -Force
    Unblock-File -LiteralPath $stagedInstaller -ErrorAction SilentlyContinue
    try {
      $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/CLOSEAPPLICATIONS','/SKIPUSERTASK=1',('/DIR="{0}"' -f $install),('/DATADIR="{0}"' -f $data))
      $process = Start-Process -FilePath $stagedInstaller -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
      if ($process.ExitCode -ne 0) { throw "Per-user installer exited with code $($process.ExitCode)." }
    } finally {
      Remove-Item -LiteralPath $stagedInstaller -Force -ErrorAction SilentlyContinue
    }
  }
  if (-not (Test-Path -LiteralPath $registerTask -PathType Leaf)) { throw "Installed task registration script is missing." }

  # --- health: start only after another verified stop; a health failure goes
  # through the verified rollback instead of restarting the restored service
  # immediately.
  $state.Stage = "health"
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  $stopBeforeStart = Stop-AHA2ProcessTrees
  $state.StopAttempts = [Math]::Max([int]$state.StopAttempts, [int]$stopBeforeStart.Attempts)
  if (-not $stopBeforeStart.Verified) {
    Add-AHA2LockedFile -Record $state -Paths $stopBeforeStart.LockedFiles
    $stopDetail = @()
    if ($stopBeforeStart.LockedFiles.Count -gt 0) { $stopDetail += ("locked files: " + ($stopBeforeStart.LockedFiles -join ", ")) }
    if ($stopBeforeStart.Processes.Count -gt 0) { $stopDetail += ("remaining processes: " + ($stopBeforeStart.Processes -join ", ")) }
    if ($stopBeforeStart.EnumerationError) { $stopDetail += ("process enumeration failed: " + $stopBeforeStart.EnumerationError) }
    throw ("AHA2 processes or file handles could not be verified stopped before startup. " + ($stopDetail -join "; "))
  }
  $state.StopVerified = $true
  $registerArguments = @{
    TrayExecutable = $tray
    ServerExecutable = $server
    Listen = $Listen
    DataDir = $data
    TaskName = $TaskName
    Start = $true
  }
  if ($AgentAPIURL) { $registerArguments.AgentAPIURL = $AgentAPIURL }
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
  $deploymentResult = [pscustomobject]@{Mode=$deploymentMode;UpdateType=$state.UpdateType;Health="ok";URL=$HealthURL;Installer=$(if ($UpdateExistingInstallInPlace) { "" } else { $installer });Backup=$backup;ServerSHA256=$serverHash;TraySHA256=$trayHash;PluginSHA256=$installedPluginHash;WriteAccessVerified=$true;ElevationRequired=$false;Stage="health";BackupSHA256=$state.BackupSHA256;LockedFiles=$state.LockedFiles;RollbackVerified=$false;rollback_health="not-required"}
  $state.Status = "succeeded"
  $state.Stage = "health"
  $state.Health = "ok"
  $state.URL = $HealthURL
  $state.Installer = if ($UpdateExistingInstallInPlace) { "" } else { $installer }
  $state.ServerSHA256 = $serverHash
  $state.TraySHA256 = $trayHash
  $state.PluginSHA256 = $installedPluginHash
  $state.RollbackVerified = $false
  $state.rollback_health = "not-required"
  $state.Value = $deploymentResult
  Write-DeploymentResult $state
  if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
  $deploymentResult
} catch {
  $deploymentError = $_
  $failedStage = $state.Stage
  $state.Status = "failed"
  $state.Value = $deploymentError.Exception.Message
  $state.FailedStage = $failedStage
  $state.rollback_health = "pending"
  Write-DeploymentResult $state

  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  $stopAfterFailure = Stop-AHA2ProcessTrees
  $state.StopAttempts = [Math]::Max([int]$state.StopAttempts, [int]$stopAfterFailure.Attempts)
  Add-AHA2LockedFile -Record $state -Paths $stopAfterFailure.LockedFiles
  if (-not $stopAfterFailure.Verified) {
    $state.StopVerified = $false
    $state.RollbackVerified = $false
    $detailParts = @($stopAfterFailure.LockedFiles) + @($stopAfterFailure.Processes)
    if ($stopAfterFailure.EnumerationError) { $detailParts += ("process enumeration failed: " + $stopAfterFailure.EnumerationError) }
    $detail = ($detailParts -join ", ")
    if ($mutationStageReached) {
      $state.Stage = "rollback"
      $state.rollback_health = ("blocked: AHA2 processes or file handles did not stop; nothing was restored. Details: " + $detail)
      $state.ManualRecovery = Get-AHA2ManualRecoveryText -Record $state -BackupDirectory $backup
      Write-DeploymentResult $state
      if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
      throw ("$($deploymentError.Exception.Message) Rollback blocked; AHA2 was left stopped. Backup: $backup. $($state.ManualRecovery)")
    }
    # No file was modified yet, so this is not a rollback failure; AHA2 simply
    # could not be confirmed stopped and must not be restarted blindly.
    $state.Stage = $failedStage
    $state.rollback_health = ("not-required: deployment failed during {0} before any file was modified; AHA2 could not be confirmed stopped ({1}) and was not restarted" -f $failedStage,$detail)
    Write-DeploymentResult $state
    if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
    throw "$($deploymentError.Exception.Message) Backup: $backup"
  }
  $state.StopVerified = $true

  if (-not $mutationStageReached) {
    # The failure happened before any binary or database file was replaced
    # (stop/backup). There is nothing to roll back; only restart the previous
    # service if the stop was verified so no mixed state can be created.
    $state.RollbackVerified = $false
    $state.rollback_health = ("not-required: deployment failed during {0} before any binary or database file was modified" -f $failedStage)
    if ($previousTaskXML) {
      if (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue)) {
        Register-ScheduledTask -TaskName $TaskName -Xml $previousTaskXML -Force | Out-Null
      }
      Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
      $state.rollback_health += "; previous service restarted"
    }
    Write-DeploymentResult $state
    if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
    throw "$($deploymentError.Exception.Message) Backup: $backup"
  }

  # --- rollback: restore binaries and database only after another verified
  # stop, use atomic replacement for every file, and gate any service start on
  # PRAGMA quick_check against the restored database.
  $state.Stage = "rollback"
  $rollbackHealth = ""
  $rollbackVerified = $false
  if (-not $backupVerified) {
    $rollbackHealth = "failed: rollback is not allowed without a verified backup"
  }
  foreach ($binary in $binaryBackups) {
    if ($rollbackHealth) { break }
    try {
      $failure = Invoke-AHA2FileRollbackEntry -Entry $binary
    } catch {
      Add-AHA2LockedFile -Record $state -Paths @($binary.TargetPath)
      $failure = ("file rollback failed for {0}: {1}" -f $binary.TargetPath,$_.Exception.Message)
    }
    if ($failure) {
      $rollbackHealth = ("failed: " + $failure)
      break
    }
  }
  # secrets.json/setup-token were part of the pre-deployment snapshot under the
  # previous implementation; restore them atomically too, but never delete a
  # file that did not exist before (a first run may legitimately create it).
  foreach ($runtimeFile in $runtimeFileBackups) {
    if ($rollbackHealth) { break }
    try {
      if (Test-Path -LiteralPath $runtimeFile.BackupPath -PathType Leaf) {
        [void](Set-AHA2FileFromSource -Source $runtimeFile.BackupPath -Destination $runtimeFile.TargetPath -ExpectedSHA256 $runtimeFile.SHA256)
      }
    } catch {
      Add-AHA2LockedFile -Record $state -Paths @($runtimeFile.TargetPath)
      $rollbackHealth = ("failed: runtime file restore failed for {0}: {1}" -f $runtimeFile.TargetPath,$_.Exception.Message)
      break
    }
  }
  if (-not $rollbackHealth) {
    if ($databaseExistedBefore) {
      try {
        Restore-AHA2DatabaseFromBackup -BackupDirectory $backup -DataDirectory $data -ExpectedSHA256 $state.BackupSHA256
        $quickCheck = Invoke-AHA2SqliteQuickCheck -DatabasePath $databasePath
        $state.rollback_health = $quickCheck.Health
        if ($quickCheck.Ok) {
          $rollbackVerified = $true
        } else {
          Add-AHA2LockedFile -Record $state -Paths @($databasePath)
          $rollbackHealth = ("failed: restored database did not pass PRAGMA quick_check: {0}" -f $quickCheck.Health)
        }
      } catch {
        Add-AHA2LockedFile -Record $state -Paths @($databasePath,$databaseWalPath,$databaseShmPath)
        $rollbackHealth = ("failed: database restore failed: {0}" -f $_.Exception.Message)
      }
    } elseif (Test-Path -LiteralPath $databasePath -PathType Leaf) {
      $rollbackHealth = "manual: no database existed before deployment; the newly created database was left untouched and AHA2 was not started"
    } else {
      $rollbackVerified = $true
      $state.rollback_health = "not-required: no database existed before deployment"
    }
  }

  $state.RollbackVerified = $rollbackVerified
  if ($rollbackVerified) {
    if ($previousTaskXML) {
      if (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue)) {
        Register-ScheduledTask -TaskName $TaskName -Xml $previousTaskXML -Force | Out-Null
      }
      Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
      try {
        Wait-AHA2Health
        $state.rollback_health = "ok: PRAGMA quick_check ok after atomic restore; previous service healthy"
      } catch {
        $state.rollback_health = ("quick_check ok after atomic restore; previous service started but health check failed: {0}" -f $_.Exception.Message)
      }
    } else {
      $state.rollback_health = "ok: PRAGMA quick_check ok after atomic restore; no previous login task to start"
    }
  } else {
    $state.rollback_health = $rollbackHealth
    $state.ManualRecovery = Get-AHA2ManualRecoveryText -Record $state -BackupDirectory $backup
  }

  Write-DeploymentResult $state
  if ($DetachedWorker) { Unregister-ScheduledTask -TaskName $UpdateTaskName -Confirm:$false -ErrorAction SilentlyContinue }
  if ($rollbackVerified) {
    throw "$($deploymentError.Exception.Message) Rollback verified from backup: $backup"
  }
  throw ("$($deploymentError.Exception.Message) Rollback failed: $($state.rollback_health). Backup: $backup. $($state.ManualRecovery)")
}
