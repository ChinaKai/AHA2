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

# The fixed task name plus -Force is the uniqueness boundary. Reinstalling to a
# new path or with new network settings replaces the previous action in place.
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
if ($Start) {
    Start-ScheduledTask -TaskName $taskName
}
