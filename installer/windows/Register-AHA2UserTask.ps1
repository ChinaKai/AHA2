[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$TrayExecutable,
    [Parameter(Mandatory = $true)]
    [string]$ServerExecutable,
    [Parameter(Mandatory = $true)]
    [string]$Listen,
    [Parameter(Mandatory = $true)]
    [string]$DataDir,
    [string]$TaskName = "AHA2 User",
    [string]$AgentAPIURL = "",
    [switch]$AllowInsecureAgentAPI,
    [switch]$Start,
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$taskName = $TaskName.Trim()
if ([string]::IsNullOrWhiteSpace($taskName) -or $taskName.Contains('"')) {
    throw "AHA2 task name is invalid."
}
$trayPath = [IO.Path]::GetFullPath($TrayExecutable)
$serverPath = [IO.Path]::GetFullPath($ServerExecutable)
$dataPath = [IO.Path]::GetFullPath($DataDir)
foreach ($application in @($trayPath, $serverPath)) {
    if (-not (Test-Path -LiteralPath $application -PathType Leaf)) {
        throw "An AHA2 application executable is missing."
    }
    if ($application.Contains('"')) {
        throw "An AHA2 application path is invalid."
    }
}
if ([string]::IsNullOrWhiteSpace($Listen) -or $Listen.Contains('"')) {
    throw "AHA2 listen address is invalid."
}
$listenParts = $Listen.Split(':')
$listenAddress = $null
$listenPort = 0
if ($listenParts.Count -ne 2 -or
    -not [Net.IPAddress]::TryParse($listenParts[0], [ref]$listenAddress) -or
    -not [int]::TryParse($listenParts[1], [ref]$listenPort) -or
    $listenPort -lt 1 -or $listenPort -gt 65535) {
    throw "AHA2 listen address is invalid."
}
if ($dataPath.Contains('"')) {
    throw "AHA2 data directory is invalid."
}
if ($dataPath.EndsWith('\')) {
    $dataPath += "."
}

$agentURL = $AgentAPIURL.Trim().TrimEnd('/')
if ($agentURL) {
    $parsedURL = $null
    if (-not [Uri]::TryCreate($agentURL, [UriKind]::Absolute, [ref]$parsedURL) -or
        $parsedURL.Scheme -notin @('http', 'https') -or $agentURL -match '[\s"]' -or
        -not [string]::IsNullOrEmpty($parsedURL.UserInfo) -or
        ($parsedURL.AbsolutePath -ne '/') -or
        $parsedURL.Query -or $parsedURL.Fragment) {
        throw "AHA2 Agent API URL is invalid."
    }
    $isLoopback = [string]::Equals($parsedURL.Host, 'localhost', [StringComparison]::OrdinalIgnoreCase)
    $address = $null
    if ([Net.IPAddress]::TryParse($parsedURL.Host, [ref]$address)) {
        $isLoopback = [Net.IPAddress]::IsLoopback($address)
    }
    if ($parsedURL.Scheme -eq 'http' -and -not $isLoopback -and -not $AllowInsecureAgentAPI) {
        throw "A non-loopback HTTP Agent API URL requires AllowInsecureAgentAPI."
    }
}

if ($ValidateOnly) {
    Write-Output "Windows tray login task definition is valid."
    exit 0
}

$identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$interactiveIdentity = (Get-CimInstance Win32_ComputerSystem -ErrorAction Stop).UserName
if (-not [string]::IsNullOrWhiteSpace($interactiveIdentity) -and
    -not [string]::Equals($identity, $interactiveIdentity, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Setup must be launched normally by the Windows user who will run AHA2."
}

$taskArguments = '--server "{0}" --listen "{1}" --data-dir "{2}"' -f $serverPath, $Listen, $dataPath
if ($agentURL) {
    $taskArguments += ' --agent-api-url "{0}"' -f $agentURL
}
if ($agentURL -and $AllowInsecureAgentAPI) {
    $taskArguments += ' --allow-insecure-agent-api'
}
$action = New-ScheduledTaskAction -Execute $trayPath -Argument $taskArguments -WorkingDirectory (Split-Path -Parent $trayPath)
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity
$principal = New-ScheduledTaskPrincipal -UserId $identity -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) -Hidden

function Resolve-IdentitySid([string]$Value) {
    try {
        if ($Value -match '^S-') {
            return (New-Object Security.Principal.SecurityIdentifier($Value)).Value
        }
        return (New-Object Security.Principal.NTAccount($Value)).Translate([Security.Principal.SecurityIdentifier]).Value
    } catch {
        return ""
    }
}

function Test-ExistingAHA2UserTask {
    try {
        $existing = Get-ScheduledTask -TaskName $taskName -ErrorAction Stop
        $actions = @($existing.Actions)
        if ($actions.Count -ne 1 -or $existing.State -eq 'Disabled') {
            return $false
        }
        $existingAction = $actions[0]
        if ([string]::IsNullOrWhiteSpace([string]$existingAction.Execute) -or
            [string]::IsNullOrWhiteSpace([string]$existingAction.WorkingDirectory)) {
            return $false
        }
        $expectedWorkingDirectory = [IO.Path]::GetFullPath((Split-Path -Parent $trayPath))
        $actualWorkingDirectory = [IO.Path]::GetFullPath([string]$existingAction.WorkingDirectory)
        $sameExecutable = [string]::Equals([IO.Path]::GetFullPath([string]$existingAction.Execute), $trayPath, [StringComparison]::OrdinalIgnoreCase)
        $sameArguments = [string]::Equals([string]$existingAction.Arguments, $taskArguments, [StringComparison]::Ordinal)
        $sameWorkingDirectory = [string]::Equals($actualWorkingDirectory, $expectedWorkingDirectory, [StringComparison]::OrdinalIgnoreCase)
        $sameIdentity = (Resolve-IdentitySid ([string]$existing.Principal.UserId)) -eq (Resolve-IdentitySid $identity)
        $limited = [string]::Equals([string]$existing.Principal.RunLevel, 'Limited', [StringComparison]::OrdinalIgnoreCase)
        return $sameExecutable -and $sameArguments -and $sameWorkingDirectory -and $sameIdentity -and $limited
    } catch {
        return $false
    }
}

# Reuse an exact existing task before attempting a write. Some valid per-user
# tasks are readable and startable by their owner but have an ACL that rejects
# Register-ScheduledTask -Force.
if (Test-ExistingAHA2UserTask) {
    Write-Output "Existing AHA2 user task already matches the requested configuration; reusing it."
} else {
    # The fixed task name plus -Force is the uniqueness boundary when the
    # requested runtime configuration actually changed.
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
}
if ($Start) {
    Start-ScheduledTask -TaskName $taskName
}
