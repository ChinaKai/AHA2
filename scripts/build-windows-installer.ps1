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
    [string]$ExpectedCommit = "",
    [string]$ExpectedWebVersion = "",
    [string]$OutputDir = "",
    [string]$Version = "dev",
    [string]$ISCCPath = "",
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$global:LASTEXITCODE = 0

$repo = [IO.Path]::GetFullPath($RepoPath)
$iss = Join-Path $repo "installer\windows\AHA2.iss"
$userTaskScript = Join-Path $repo "installer\windows\Register-AHA2UserTask.ps1"
if (-not (Test-Path -LiteralPath (Join-Path $repo "go.mod") -PathType Leaf)) {
    throw "RepoPath is not an AHA2 repository: $repo"
}
foreach ($requiredFile in @($iss, $userTaskScript)) {
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
# The Windows installer is per-user only. These contracts pin the parts that
# make that true (no elevation, LocalAppData install, user login task) and the
# parts the release verifier depends on (manifest, browser host, Node runtime).
$requiredContracts = @(
    '#define ListenAddress "127.0.0.1:8766"',
    'PrivilegesRequired=lowest',
    'DefaultDirName={localappdata}\Programs\AHA2',
    'OutputBaseFilename=AHA2-Setup-User-x64',
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
    "{code:LocalManagementURL}",
    "IconFilename: ""{app}\aha2-tray.exe""",
    "runasoriginaluser",
    "TrayParameters",
    "StopInstalledUserProcesses",
    "Get-Process -Name ''aha2-tray'',''aha2''",
    "Register-AHA2UserTask.ps1",
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
	Write-Output "Installer definition valid (per-user); server input $serverState; tray input $trayState; browser host $browserHostState; node runtime $nodeState; Feishu plugin $feishuState; release manifest $manifestState."
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
# exactly the files the installer is about to copy. The verifier is shared with
# the Node tests and the Release archive check: it validates identity, path
# safety, required scope, per-file SHA256 and the bidirectional mapping between
# manifest entries and the files Inno Setup actually copies (the browser-host
# tree is copied recursively, so deleting one file from that tree must fail).
if (-not (Test-Path -LiteralPath $manifestInputPath -PathType Leaf)) {
    throw "Release manifest not found: $manifestInputPath. Run scripts\build-windows-package.sh first."
}
# The commit is the trusted anchor for the manifest identity. Derive it from the
# checkout when the caller does not pass one; the manifest's own commit is
# never used to validate itself.
if ([string]::IsNullOrWhiteSpace($ExpectedCommit)) {
    $gitCommand = Get-Command "git.exe" -ErrorAction SilentlyContinue
    if (-not $gitCommand) {
        $gitCommand = Get-Command "git" -ErrorAction SilentlyContinue
    }
    if ($gitCommand) {
        $ExpectedCommit = (& $gitCommand.Source -C $repo rev-parse HEAD 2>$null | Out-String).Trim()
    }
    if ([string]::IsNullOrWhiteSpace($ExpectedCommit)) {
        throw "Expected commit is required when the repository is not a git checkout; pass -ExpectedCommit."
    }
}
$nodeCommand = Get-Command "node.exe" -ErrorAction SilentlyContinue
if (-not $nodeCommand) {
    $nodeCommand = Get-Command "node" -ErrorAction SilentlyContinue
}
if (-not $nodeCommand) {
    throw "Node.js is required to verify the Windows package against release-manifest.json."
}
$verifier = Join-Path $repo "scripts\verify-windows-package.mjs"
if (-not (Test-Path -LiteralPath $verifier -PathType Leaf)) {
    throw "Windows package verifier is missing: $verifier"
}
$verifyArgs = @(
    $verifier,
    "--manifest", $manifestInputPath,
    "--root", (Split-Path -Parent $manifestInputPath),
    "--version", $normalizedVersion,
    "--expected-commit", $ExpectedCommit,
    "--server", $inputPath,
    "--tray", $trayInputPath,
    "--browser-host", $browserHostPath,
    "--node-exe", $nodeInputPath,
    "--node-license", $nodeLicensePath
)
if ($feishuPluginPath) {
    $verifyArgs += @("--feishu-plugin", $feishuPluginPath, "--feishu-manifest", $feishuManifestPath)
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedWebVersion)) {
    $verifyArgs += @("--expected-web-version", $ExpectedWebVersion)
}
& $nodeCommand.Source @verifyArgs
if ($LASTEXITCODE -ne 0) {
    throw "Windows package verification failed for $manifestInputPath."
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
$setupName = "AHA2-Setup-User-x64.exe"
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
