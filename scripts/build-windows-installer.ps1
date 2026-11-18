[CmdletBinding()]
param(
    [string]$RepoPath = (Split-Path -Parent $PSScriptRoot),
    [string]$InputExe = "",
    [string]$InputTrayExe = "",
    [string]$InputBrowserHost = "",
    [string]$InputNodeExe = "",
    [string]$InputNodeLicense = "",
	[string]$InputFeishuPlugin = "",
	[string]$InputFeishuManifest = "",
    [string]$InputManifest = "",
    [string]$OutputDir = "",
    [string]$Version = "dev",
    [string]$ISCCPath = "",
	[switch]$PerUser,
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$global:LASTEXITCODE = 0

$repo = [IO.Path]::GetFullPath($RepoPath)
$iss = Join-Path $repo "installer\windows\AHA2.iss"
$userTaskScript = Join-Path $repo "installer\windows\Register-AHA2UserTask.ps1"
$dataDirScript = Join-Path $repo "installer\windows\Prepare-AHA2DataDir.ps1"
if (-not (Test-Path -LiteralPath (Join-Path $repo "go.mod") -PathType Leaf)) {
    throw "RepoPath is not an AHA2 repository: $repo"
}
foreach ($requiredFile in @($iss, $userTaskScript, $dataDirScript)) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "Windows installer source is missing: $requiredFile"
    }
}
if ([string]::IsNullOrWhiteSpace($InputExe)) {
    $InputExe = Join-Path $repo "dist\aha2-windows-amd64.exe"
}
if ([string]::IsNullOrWhiteSpace($InputTrayExe)) {
    $InputTrayExe = Join-Path $repo "dist\aha2-tray-windows-amd64.exe"
}
if ([string]::IsNullOrWhiteSpace($InputBrowserHost)) {
    $InputBrowserHost = Join-Path $repo "dist\browser-host"
}
if ([string]::IsNullOrWhiteSpace($InputNodeExe)) {
    $InputNodeExe = Join-Path $repo "dist\node\node.exe"
}
if ([string]::IsNullOrWhiteSpace($InputNodeLicense)) {
    $InputNodeLicense = Join-Path $repo "dist\node\LICENSE"
}
if ([string]::IsNullOrWhiteSpace($InputManifest)) {
    $InputManifest = Join-Path $repo "dist\release-manifest.json"
}
$defaultFeishuDir = Join-Path $repo "dist\plugins\feishu\windows-amd64"
if ([string]::IsNullOrWhiteSpace($InputFeishuPlugin) -and [string]::IsNullOrWhiteSpace($InputFeishuManifest)) {
	$defaultPlugin = Join-Path $defaultFeishuDir "aha2-channel-feishu.exe"
	$defaultManifest = Join-Path $defaultFeishuDir "plugin.json"
	if ((Test-Path -LiteralPath $defaultPlugin -PathType Leaf) -and (Test-Path -LiteralPath $defaultManifest -PathType Leaf)) {
		$InputFeishuPlugin = $defaultPlugin
		$InputFeishuManifest = $defaultManifest
	}
}
if ([string]::IsNullOrWhiteSpace($InputFeishuPlugin) -xor [string]::IsNullOrWhiteSpace($InputFeishuManifest)) {
	throw "Feishu plugin executable and manifest must be provided together."
}
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $repo "dist\installer"
}
$inputPath = [IO.Path]::GetFullPath($InputExe)
$trayInputPath = [IO.Path]::GetFullPath($InputTrayExe)
$browserHostPath = [IO.Path]::GetFullPath($InputBrowserHost)
$nodeInputPath = [IO.Path]::GetFullPath($InputNodeExe)
$nodeLicensePath = [IO.Path]::GetFullPath($InputNodeLicense)
$manifestInputPath = [IO.Path]::GetFullPath($InputManifest)
$feishuPluginPath = if ([string]::IsNullOrWhiteSpace($InputFeishuPlugin)) { "" } else { [IO.Path]::GetFullPath($InputFeishuPlugin) }
$feishuManifestPath = if ([string]::IsNullOrWhiteSpace($InputFeishuManifest)) { "" } else { [IO.Path]::GetFullPath($InputFeishuManifest) }
$outputPath = [IO.Path]::GetFullPath($OutputDir)
$normalizedVersion = $Version.Trim()
if ($normalizedVersion.StartsWith("v", [StringComparison]::OrdinalIgnoreCase)) {
    $normalizedVersion = $normalizedVersion.Substring(1)
}
if ([string]::IsNullOrWhiteSpace($normalizedVersion)) {
    throw "Installer version cannot be empty."
}

$source = Get-Content -Raw -Encoding UTF8 -LiteralPath $iss
$requiredContracts = @(
    '#define ServiceName "AHA2"',
    '#define ListenAddress "127.0.0.1:8766"',
    '#define SourceTrayExe',
    'DestName: "aha2.exe"',
    'DestName: "aha2-tray.exe"',
    'DestName: "release-manifest.json"',
    'SourceManifest',
    'DestDir: "{app}\browser-host"',
    "recursesubdirs",
    "SourceBrowserHost",
    '#define SourceNodeExe',
    'DestDir: "{app}\node"',
    "SourceNodeExe",
    "SourceNodeLicense",
	'DestName: "Hysteria-LICENSE.txt"',
	'DestName: "yaml-v3-LICENSE.txt"',
	'DestName: "Xray-core-MPL-2.0.txt"',
	'DestName: "Xray-core-SOURCE.txt"',
    '--server "'' + ExpandConstant(''{app}\aha2.exe'')',
    "CreateInputDirPage",
    "CreateInputOptionPage",
    "SelectedListenAddress",
    "RegisterPreviousData",
    "remoteip=localsubnet profile=private",
    "{code:LocalManagementURL}",
    "IconFilename: ""{app}\aha2-tray.exe""",
    "{commonappdata}\AHA2",
    "runasoriginaluser",
    "TrayParameters",
    "StopInstalledUserProcesses",
    "Get-Process -Name ''aha2-tray'',''aha2''",
    "DisableAndStopLegacyAHA2Service(True)",
    "RemoveLegacyAHA2Service()",
    "Register-AHA2UserTask.ps1",
    "Prepare-AHA2DataDir.ps1",
    "[InstallDelete]",
    'Type: files; Name: "{app}\Run-AHA2User.vbs"',
    "ConfigureAHA2UserTask",
    "AHA2UserTaskName",
    "RemoveAHA2UserTask",
    "ExecAsOriginalUser",
    "InstallStarted",
    "WaitForAHA2Health",
    "LocalHealthURL",
	"http://127.0.0.1:' + SelectedPort()",
	"SourceFeishuPlugin",
	"SourceFeishuManifest",
	"plugins\channels\feishu"
)
foreach ($contract in $requiredContracts) {
    if (-not $source.Contains($contract)) {
        throw "Installer contract is missing: $contract"
    }
}
$userTaskSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $userTaskScript
foreach ($contract in @("TrayExecutable", "ServerExecutable", "New-ScheduledTaskAction -Execute `$trayPath", "--server", "--listen", "--data-dir", "New-ScheduledTaskTrigger -AtLogOn", "WindowsIdentity]::GetCurrent().Name", "Win32_ComputerSystem", "OrdinalIgnoreCase", "-LogonType Interactive", "-RunLevel Limited", "-RestartCount 3", "-RestartInterval", "Register-ScheduledTask", "Start-ScheduledTask", "--agent-api-url")) {
    if (-not $userTaskSource.Contains($contract)) {
        throw "Windows tray login task contract is missing: $contract"
    }
}
foreach ($forbidden in @("wscript.exe", ".vbs", "powershell.exe", "service run", "SYSTEM")) {
    if ($userTaskSource.Contains($forbidden)) {
        throw "Windows login task still contains a forbidden long-running host contract: $forbidden"
    }
}
$dataDirSource = Get-Content -Raw -Encoding UTF8 -LiteralPath $dataDirScript
foreach ($contract in @("Get-ScheduledTask", "Principal.UserId", "SetAccessRuleProtection", "SecurityIdentifier]'S-1-5-18'", "SecurityIdentifier]'S-1-5-32-544'", "Set-Acl", "FileSystemRights]::Modify", "New-Item -ItemType Directory")) {
    if (-not $dataDirSource.Contains($contract)) {
        throw "Windows user data directory contract is missing: $contract"
    }
}
foreach ($forbidden in @("service run --listen", "actions= restart/", "ConfigureAndStartAHA2Service", 'Source: "Run-AHA2User.vbs"', "wscript.exe")) {
    if ($source.Contains($forbidden)) {
        throw "Installer still contains a forbidden legacy runtime contract: $forbidden"
    }
}
if ($ValidateOnly) {
    $serverState = if (Test-Path -LiteralPath $inputPath -PathType Leaf) {"found"} else {"not present"}
    $trayState = if (Test-Path -LiteralPath $trayInputPath -PathType Leaf) {"found"} else {"not present"}
    $browserHostState = if (Test-Path -LiteralPath (Join-Path $browserHostPath "host.mjs") -PathType Leaf) {"found"} else {"not present"}
    $nodeState = if ((Test-Path -LiteralPath $nodeInputPath -PathType Leaf) -and (Test-Path -LiteralPath $nodeLicensePath -PathType Leaf)) {"found"} else {"not present"}
	$feishuState = if ($feishuPluginPath -and (Test-Path -LiteralPath $feishuPluginPath -PathType Leaf) -and (Test-Path -LiteralPath $feishuManifestPath -PathType Leaf)) { "found" } else { "not bundled" }
	$manifestState = if (Test-Path -LiteralPath $manifestInputPath -PathType Leaf) { "found" } else { "not present" }
	$mode = if ($PerUser) { "per-user" } else { "per-machine" }
	Write-Output "Installer definition valid ($mode); server input $serverState; tray input $trayState; browser host $browserHostState; node runtime $nodeState; Feishu plugin $feishuState; release manifest $manifestState."
    exit 0
}

if (-not (Test-Path -LiteralPath $inputPath -PathType Leaf)) {
    throw "Windows amd64 server executable not found: $inputPath"
}
if (-not (Test-Path -LiteralPath $trayInputPath -PathType Leaf)) {
    throw "Windows amd64 tray executable not found: $trayInputPath"
}
if (-not (Test-Path -LiteralPath (Join-Path $browserHostPath "host.mjs") -PathType Leaf)) {
    throw "Browser host package not found: $browserHostPath\host.mjs"
}
if (-not (Test-Path -LiteralPath $nodeInputPath -PathType Leaf)) {
    throw "Bundled Node runtime not found: $nodeInputPath"
}
if (-not (Test-Path -LiteralPath $nodeLicensePath -PathType Leaf)) {
    throw "Bundled Node runtime license not found: $nodeLicensePath"
}
if ($feishuPluginPath -and ((-not (Test-Path -LiteralPath $feishuPluginPath -PathType Leaf)) -or (-not (Test-Path -LiteralPath $feishuManifestPath -PathType Leaf)))) {
	throw "Feishu plugin bundle inputs were not found."
}

# The installer writes {app}\release-manifest.json byte-for-byte. That manifest
# is the deploy verification contract, so fail the build unless it describes
# exactly the files the installer is about to copy: identity, path safety,
# required scope and per-file SHA256 must all match the input tree.
if (-not (Test-Path -LiteralPath $manifestInputPath -PathType Leaf)) {
    throw "Release manifest not found: $manifestInputPath. Run scripts\build-windows-package.sh first."
}
$manifest = Get-Content -Raw -Encoding UTF8 -LiteralPath $manifestInputPath | ConvertFrom-Json
if ($manifest.schema_version -ne 1 -or [string]$manifest.product -ne "AHA2") {
    throw "Release manifest is not an AHA2 schema_version 1 manifest: $manifestInputPath"
}
if ([string]$manifest.version -ne $normalizedVersion) {
    throw "Release manifest version $($manifest.version) does not match installer version $normalizedVersion."
}
if ([string]$manifest.commit -notmatch '^[0-9a-fA-F]{12,40}$') {
    throw "Release manifest commit is missing or malformed: $($manifest.commit)"
}
if ($null -eq $manifest.generator -or [string]::IsNullOrWhiteSpace([string]$manifest.generator.web_version)) {
    throw "Release manifest generator.web_version is missing."
}
$manifestEntries = @($manifest.files)
if ($manifestEntries.Count -eq 0) {
    throw "Release manifest lists no files."
}
$manifestByPath = @{}
foreach ($entry in $manifestEntries) {
    $relative = [string]$entry.path
    if ([string]::IsNullOrWhiteSpace($relative) -or $relative.Contains('\') -or $relative.StartsWith('/') -or $relative -match '^[A-Za-z]:' -or (($relative -split '/') -contains '..')) {
        throw "Release manifest path is not a safe package-relative path: $relative"
    }
    if ($manifestByPath.ContainsKey($relative)) {
        throw "Release manifest lists $relative twice."
    }
    if ([string]$entry.sha256 -notmatch '^[0-9a-f]{64}$') {
        throw "Release manifest has a malformed SHA256 for $relative."
    }
    $manifestByPath[$relative] = $entry
}
$requiredManifestFiles = @(
    "aha2-windows-amd64.exe",
    "aha2-tray-windows-amd64.exe",
    "browser-host/host.mjs",
    "browser-host/server.mjs",
    "browser-host/package.json",
    "node/node.exe",
    "node/LICENSE"
)
$pluginManifestFiles = @(
    "plugins/feishu/windows-amd64/aha2-channel-feishu.exe",
    "plugins/feishu/windows-amd64/plugin.json"
)
foreach ($required in $requiredManifestFiles) {
    if (-not $manifestByPath.ContainsKey($required)) {
        throw "Release manifest is missing required install file: $required"
    }
}
$manifestHasPlugin = @($pluginManifestFiles | Where-Object { $manifestByPath.ContainsKey($_) }).Count -gt 0
if ($manifestHasPlugin -and @($pluginManifestFiles | Where-Object { -not $manifestByPath.ContainsKey($_) }).Count -gt 0) {
    throw "Release manifest lists only part of the Feishu plugin."
}
if ($manifestHasPlugin -and [string]::IsNullOrWhiteSpace($feishuPluginPath)) {
    throw "Release manifest covers the Feishu plugin but installer inputs do not include it."
}
if (-not $manifestHasPlugin -and -not [string]::IsNullOrWhiteSpace($feishuPluginPath)) {
    throw "Installer inputs include the Feishu plugin but the release manifest does not."
}
$allowedExactManifestFiles = @("aha2-windows-amd64.exe", "aha2-tray-windows-amd64.exe") + $pluginManifestFiles
foreach ($relative in $manifestByPath.Keys) {
    if ($relative.StartsWith("browser-host/") -or $relative.StartsWith("node/")) { continue }
    if ($allowedExactManifestFiles -notcontains $relative) {
        throw "Release manifest entry is outside the installer's install scope: $relative"
    }
}
$manifestInputs = @{
    "aha2-windows-amd64.exe" = $inputPath
    "aha2-tray-windows-amd64.exe" = $trayInputPath
    "node/node.exe" = $nodeInputPath
    "node/LICENSE" = $nodeLicensePath
}
if ($manifestHasPlugin) {
    $manifestInputs["plugins/feishu/windows-amd64/aha2-channel-feishu.exe"] = $feishuPluginPath
    $manifestInputs["plugins/feishu/windows-amd64/plugin.json"] = $feishuManifestPath
}
foreach ($relative in $manifestByPath.Keys) {
    if ($relative.StartsWith("browser-host/")) {
        $inputFile = Join-Path $browserHostPath (($relative.Substring("browser-host/".Length)) -replace '/', '\')
    } else {
        $inputFile = $manifestInputs[$relative]
    }
    if ([string]::IsNullOrWhiteSpace($inputFile) -or -not (Test-Path -LiteralPath $inputFile -PathType Leaf)) {
        throw "Release manifest lists $relative but the installer has no matching input."
    }
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $inputFile).Hash.ToLowerInvariant()
    if ($actualHash -ne ([string]$manifestByPath[$relative].sha256)) {
        throw "Release manifest SHA256 does not match the installer input for $relative."
    }
}

function Get-PEWindowsSubsystem([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -lt 256 -or $bytes[0] -ne 0x4D -or $bytes[1] -ne 0x5A) {
        throw "Windows executable is not a valid PE file: $Path"
    }
    $peOffset = [BitConverter]::ToInt32($bytes, 0x3C)
    $optionalHeader = $peOffset + 24
    if ($peOffset -lt 0 -or $optionalHeader + 70 -ge $bytes.Length) {
        throw "Windows executable PE header is truncated: $Path"
    }
    return [BitConverter]::ToUInt16($bytes, $optionalHeader + 68)
}

if ((Get-PEWindowsSubsystem $trayInputPath) -ne 2) {
    throw "AHA2 tray executable must use the Windows GUI subsystem (-H windowsgui): $trayInputPath"
}

if ([string]::IsNullOrWhiteSpace($ISCCPath)) {
    $command = Get-Command "ISCC.exe" -ErrorAction SilentlyContinue
    if ($command) {
        $ISCCPath = $command.Path
    } else {
        $localAppData = $env:LOCALAPPDATA
        if ([string]::IsNullOrWhiteSpace($localAppData)) {
            $localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
        }
        $candidates = @()
        if (-not [string]::IsNullOrWhiteSpace($localAppData)) {
            $candidates += Join-Path $localAppData "Programs\Inno Setup 6\ISCC.exe"
        }
        if (-not [string]::IsNullOrWhiteSpace(${env:ProgramFiles(x86)})) {
            $candidates += Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe"
        }
        if (-not [string]::IsNullOrWhiteSpace($env:ProgramFiles)) {
            $candidates += Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe"
        }
        $ISCCPath = $candidates | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
    }
}
if ([string]::IsNullOrWhiteSpace($ISCCPath) -or -not (Test-Path -LiteralPath $ISCCPath -PathType Leaf)) {
    throw "Inno Setup 6 compiler (ISCC.exe) was not found."
}

New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
$compilerArgs = @(
	"/DMyAppVersion=$normalizedVersion",
	"/DSourceExe=$inputPath",
	"/DSourceTrayExe=$trayInputPath",
	"/DSourceBrowserHost=$browserHostPath",
	"/DSourceNodeExe=$nodeInputPath",
	"/DSourceNodeLicense=$nodeLicensePath",
	"/DSourceManifest=$manifestInputPath",
	"/DOutputDir=$outputPath"
)
if ($feishuPluginPath) {
	$compilerArgs += "/DSourceFeishuPlugin=$feishuPluginPath"
	$compilerArgs += "/DSourceFeishuManifest=$feishuManifestPath"
}
if ($PerUser) {
	$compilerArgs += "/DPerUserInstall=1"
}
$setupName = if ($PerUser) { "AHA2-Setup-User-x64.exe" } else { "AHA2-Setup-x64.exe" }
$setup = Join-Path $outputPath $setupName
$hashFile = "$setup.sha256"
Remove-Item -LiteralPath $setup,$hashFile -Force -ErrorAction SilentlyContinue
function ConvertTo-ProcessArgument([string]$Value) {
    if ($Value.Contains('"')) {
        throw "Inno Setup argument contains an unsupported quote."
    }
    if ($Value -match '\s') {
        return '"' + $Value + '"'
    }
    return $Value
}
$compilerArgumentLine = (@($compilerArgs) + @($iss) | ForEach-Object {
    ConvertTo-ProcessArgument ([string]$_)
}) -join " "
$compilerProcess = Start-Process -FilePath $ISCCPath -ArgumentList $compilerArgumentLine -NoNewWindow -Wait -PassThru
if ($compilerProcess.ExitCode -ne 0) {
    throw "Inno Setup compilation failed with exit code $($compilerProcess.ExitCode)."
}

$deadline = [DateTime]::UtcNow.AddSeconds(5)
$previousLength = -1L
do {
    if (Test-Path -LiteralPath $setup -PathType Leaf) {
        $length = (Get-Item -LiteralPath $setup).Length
        if ($length -gt 0 -and $length -eq $previousLength) {
            break
        }
        $previousLength = $length
    }
    Start-Sleep -Milliseconds 200
} while ([DateTime]::UtcNow -lt $deadline)
if (-not (Test-Path -LiteralPath $setup -PathType Leaf) -or (Get-Item -LiteralPath $setup).Length -le 0) {
    throw "Expected installer was not produced: $setup"
}
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $setup).Hash.ToLowerInvariant()
[IO.File]::WriteAllText($hashFile, "$hash  $setupName`n", [Text.UTF8Encoding]::new($false))
Write-Output "Installer: $setup"
Write-Output "SHA256: $hashFile"
Write-Output "Release manifest: $manifestInputPath"
