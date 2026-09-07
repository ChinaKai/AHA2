[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$DataDir,
    [string]$TaskName = "AHA2 User",
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$dataPath = [IO.Path]::GetFullPath($DataDir)
if ($dataPath.StartsWith('\\') -or $dataPath.Contains('"')) {
    throw "AHA2 data directory must be a local path."
}
if ($ValidateOnly) {
    Write-Output "Windows user data directory definition is valid."
    exit 0
}

$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction Stop
$identity = $task.Principal.UserId
if ([string]::IsNullOrWhiteSpace($identity)) {
    throw "AHA2 login task has no user principal."
}
New-Item -ItemType Directory -Force -Path $dataPath | Out-Null
$userSid = ([Security.Principal.NTAccount]$identity).Translate([Security.Principal.SecurityIdentifier])
$systemSid = [Security.Principal.SecurityIdentifier]'S-1-5-18'
$administratorsSid = [Security.Principal.SecurityIdentifier]'S-1-5-32-544'
$allow = [Security.AccessControl.AccessControlType]::Allow
$directoryInheritance = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'

function Set-AHA2DirectoryACL([string]$Path) {
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($userSid, [Security.AccessControl.FileSystemRights]::Modify, $directoryInheritance, [Security.AccessControl.PropagationFlags]::None, $allow))) | Out-Null
    foreach ($principal in @($systemSid, $administratorsSid)) {
        $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($principal, [Security.AccessControl.FileSystemRights]::FullControl, $directoryInheritance, [Security.AccessControl.PropagationFlags]::None, $allow))) | Out-Null
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Set-AHA2FileACL([string]$Path) {
    $acl = New-Object Security.AccessControl.FileSecurity
    $acl.SetAccessRuleProtection($true, $false)
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($userSid, [Security.AccessControl.FileSystemRights]::Modify, $allow))) | Out-Null
    foreach ($principal in @($systemSid, $administratorsSid)) {
        $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($principal, [Security.AccessControl.FileSystemRights]::FullControl, $allow))) | Out-Null
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

Set-AHA2DirectoryACL $dataPath
Get-ChildItem -LiteralPath $dataPath -Recurse -Force -Directory | ForEach-Object { Set-AHA2DirectoryACL $_.FullName }
Get-ChildItem -LiteralPath $dataPath -Recurse -Force -File | ForEach-Object { Set-AHA2FileACL $_.FullName }
