[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$DataDir,
    [string]$RepoPath = "",
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\AHA2"),
    [string]$Distro = "",
    [string]$ProxyURL = "",
    [string]$Listen = "127.0.0.1:8766",
    [string]$HealthURL = "http://127.0.0.1:8766/healthz",
    [string]$AgentAPIURL = "",
    [switch]$AllowInsecureAgentAPI,
    [int]$MinimumFreeGB = 3,
    [string]$ResultPath = "",
    [switch]$DeployOnly,
    [switch]$BuildOnly,
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

if ($DeployOnly -and $BuildOnly) { throw "DeployOnly and BuildOnly cannot be used together." }
if ($MinimumFreeGB -lt 1) { throw "MinimumFreeGB must be at least 1." }

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
if ([string]::IsNullOrWhiteSpace($RepoPath)) { $RepoPath = Split-Path -Parent $scriptRoot }
$repo = (Resolve-Path -LiteralPath $RepoPath).Path
if (-not (Test-Path -LiteralPath (Join-Path $repo "go.mod") -PathType Leaf)) {
    throw "RepoPath is not an AHA2 repository: $repo"
}
$serverOutput = Join-Path $repo "dist\aha2-windows-amd64.exe"
$trayOutput = Join-Path $repo "dist\aha2-tray-windows-amd64.exe"
$pluginOutputDir = Join-Path $repo "dist\plugins\feishu\windows-amd64"
$pluginOutput = Join-Path $pluginOutputDir "aha2-channel-feishu.exe"
$pluginManifestOutput = Join-Path $pluginOutputDir "plugin.json"
$deployScript = Join-Path $repo "scripts\deploy-windows-user.ps1"
$normalizedVersion = $Version.Trim()
if ($normalizedVersion.StartsWith("v", [StringComparison]::OrdinalIgnoreCase)) {
    $normalizedVersion = $normalizedVersion.Substring(1)
}

function Assert-PathUnderRepository([string]$Path) {
    $fullPath = [IO.Path]::GetFullPath($Path)
    $repoPrefix = $repo.TrimEnd('\') + '\'
    if (-not $fullPath.StartsWith($repoPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path must stay inside the repository: $fullPath"
    }
    return $fullPath
}

function Invoke-Step([string]$Name, [scriptblock]$Action) {
    $watch = [Diagnostics.Stopwatch]::StartNew()
    Write-Host "==> $Name"
    & $Action
    $watch.Stop()
    Write-Host ("<== {0} ({1:n1}s)" -f $Name, $watch.Elapsed.TotalSeconds)
}

function Invoke-Native([string]$Executable, [string[]]$Arguments) {
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Executable exited with code $LASTEXITCODE." }
}

function Assert-WindowsPortableExecutable([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    try {
        if ($stream.ReadByte() -ne 0x4D -or $stream.ReadByte() -ne 0x5A) {
            throw "Deployment candidate is not a Windows PE executable: $Path"
        }
    } finally {
        $stream.Dispose()
    }
}

$resolvedDistro = ""
$linuxRepo = ""
if (-not $DeployOnly) {
    if (-not (Get-Command node.exe -ErrorAction SilentlyContinue)) { throw "node.exe was not found." }
    if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) { throw "wsl.exe was not found." }
    if (-not (Test-Path -LiteralPath (Join-Path $repo ".tools\go\bin\go") -PathType Leaf)) {
        throw "Repository Linux Go toolchain was not found. Do not download another toolchain during deployment."
    }
    $installedDistros = @(@(& wsl.exe -l -q 2>$null) |
        ForEach-Object { ($_ -replace "`0", "").Trim() } |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($LASTEXITCODE -ne 0 -or $installedDistros.Count -eq 0) { throw "No WSL distribution is available." }
    if ([string]::IsNullOrWhiteSpace($Distro)) {
        $resolvedDistro = $installedDistros[0]
    } else {
        $resolvedDistro = $installedDistros | Where-Object { $_ -eq $Distro } | Select-Object -First 1
        if ([string]::IsNullOrWhiteSpace($resolvedDistro)) {
            throw "WSL distribution '$Distro' was not found. Available: $($installedDistros -join ', ')"
        }
    }
    $linuxRepo = ((& wsl.exe -d $resolvedDistro --cd $repo -- pwd) -join "").Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($linuxRepo)) {
        throw "Could not translate the repository path for WSL."
    }
    $driveName = [IO.Path]::GetPathRoot($repo).TrimEnd('\').TrimEnd(':')
    $freeGB = [Math]::Round((Get-PSDrive -Name $driveName -ErrorAction Stop).Free / 1GB, 2)
    if ($freeGB -lt $MinimumFreeGB) {
        throw "Repository drive has $freeGB GB free; at least $MinimumFreeGB GB is required."
    }
}

$linuxGoCache = "$linuxRepo/.tools/gocache-linux"
$linuxGoPath = "$linuxRepo/.tools/gopath-linux"
function Invoke-WslGo([string]$WorkingDirectory, [string[]]$Arguments, [string[]]$EnvironmentEntries = @()) {
    $wslArguments = @("-d", $resolvedDistro, "--cd", $WorkingDirectory, "--", "env", "GOCACHE=$linuxGoCache", "GOPATH=$linuxGoPath")
    $wslArguments += $EnvironmentEntries
    if (-not [string]::IsNullOrWhiteSpace($ProxyURL)) {
        $wslArguments += @("HTTP_PROXY=$ProxyURL", "HTTPS_PROXY=$ProxyURL")
    }
    $wslArguments += "$linuxRepo/.tools/go/bin/go"
    $wslArguments += $Arguments
    Invoke-Native "wsl.exe" $wslArguments
}

$summary = [ordered]@{
    Repository = $repo
    Version = $Version
    Mode = if ($DeployOnly) { "deploy-only" } elseif ($BuildOnly) { "build-only" } else { "build-and-deploy" }
    Distro = $resolvedDistro
    InstallDir = [IO.Path]::GetFullPath($InstallDir)
    DataDir = [IO.Path]::GetFullPath($DataDir)
    Listen = $Listen
    HealthURL = $HealthURL
}
if ($ValidateOnly) {
    [pscustomobject]$summary
    return
}

if (-not $DeployOnly) {
    Invoke-Step "Build Web assets" {
        Invoke-Native "node.exe" @((Join-Path $repo "scripts\build-web.mjs"))
    }
    Invoke-Step "Test Web build" {
        Invoke-Native "node.exe" @("--test", (Join-Path $repo "web\tests\build.test.mjs"))
    }
    Invoke-Step "Sync embedded Web assets" {
        $assetTarget = Assert-PathUnderRepository (Join-Path $repo "internal\webassets\dist")
        New-Item -ItemType Directory -Path $assetTarget -Force | Out-Null
        Get-ChildItem -LiteralPath $assetTarget -Force | Remove-Item -Recurse -Force
        Copy-Item -Path (Join-Path $repo "web\dist\*") -Destination $assetTarget -Recurse -Force
    }
    New-Item -ItemType Directory -Path (Join-Path $repo ".tools\gocache-linux"), (Join-Path $repo ".tools\gopath-linux"), (Join-Path $repo "dist") -Force | Out-Null
    Invoke-Step "Run Go tests" {
        Invoke-WslGo $linuxRepo @("test", "./...")
    }
    Invoke-Step "Run go vet" {
        Invoke-WslGo $linuxRepo @("vet", "./...")
    }

    $windowsGoEnvironment = @("CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
    Invoke-Step "Build Windows server" {
        Invoke-WslGo -WorkingDirectory $linuxRepo -EnvironmentEntries $windowsGoEnvironment -Arguments @(
            "build", "-trimpath", "-ldflags=-s -w -X main.version=$normalizedVersion",
            "-o", "dist/aha2-windows-amd64.exe", "./cmd/aha"
        )
    }
    Invoke-Step "Build Windows tray" {
        Invoke-WslGo -WorkingDirectory $linuxRepo -EnvironmentEntries $windowsGoEnvironment -Arguments @(
            "build", "-trimpath", "-ldflags=-s -w -H windowsgui -X main.version=$normalizedVersion",
            "-o", "dist/aha2-tray-windows-amd64.exe", "./cmd/aha-tray"
        )
    }
    if (Test-Path -LiteralPath (Join-Path $repo "plugins\feishu\go.mod") -PathType Leaf) {
        $linuxPluginDir = "$linuxRepo/plugins/feishu"
        Invoke-Step "Test Feishu plugin" {
            Invoke-WslGo $linuxPluginDir @("test", "./...")
        }
        Invoke-Step "Build Feishu plugin" {
            New-Item -ItemType Directory -Path $pluginOutputDir -Force | Out-Null
            Invoke-WslGo -WorkingDirectory $linuxPluginDir -EnvironmentEntries $windowsGoEnvironment -Arguments @(
                "build", "-trimpath", "-ldflags=-s -w",
                "-o", "$linuxRepo/dist/plugins/feishu/windows-amd64/aha2-channel-feishu.exe", "."
            )
            Copy-Item -LiteralPath (Join-Path $repo "plugins\feishu\plugin.json") -Destination $pluginManifestOutput -Force
            $pluginHash = (Get-FileHash -LiteralPath $pluginOutput -Algorithm SHA256).Hash.ToLowerInvariant()
            $manifest = (Get-Content -Raw -Encoding UTF8 -LiteralPath $pluginManifestOutput).Replace("SET_BY_PACKAGING", $pluginHash)
            [IO.File]::WriteAllText($pluginManifestOutput, $manifest, [Text.UTF8Encoding]::new($false))
        }
    }
}

foreach ($candidate in @($serverOutput, $trayOutput)) {
    if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        throw "Deployment candidate is missing: $candidate"
    }
    Assert-WindowsPortableExecutable $candidate
}
if (Test-Path -LiteralPath $pluginOutput -PathType Leaf) {
    Assert-WindowsPortableExecutable $pluginOutput
}
$serverVersion = (& $serverOutput version | Out-String).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Could not read the deployment candidate version."
}
$expectedServerVersion = "aha2 $normalizedVersion"
if ($serverVersion -ne $expectedServerVersion) {
    throw "Deployment candidate version mismatch: expected '$expectedServerVersion', got '$serverVersion'."
}
$summary.ServerVersion = $serverVersion
$summary.ServerSHA256 = (Get-FileHash -LiteralPath $serverOutput -Algorithm SHA256).Hash
$summary.TraySHA256 = (Get-FileHash -LiteralPath $trayOutput -Algorithm SHA256).Hash
if (Test-Path -LiteralPath $pluginOutput -PathType Leaf) {
    $summary.PluginSHA256 = (Get-FileHash -LiteralPath $pluginOutput -Algorithm SHA256).Hash
}
if ($BuildOnly) {
    [pscustomobject]$summary
    return
}

if ([string]::IsNullOrWhiteSpace($ResultPath)) {
    $stamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
    $ResultPath = Join-Path $repo "dist\user-deploy-result-$stamp.json"
}
$ResultPath = Assert-PathUnderRepository $ResultPath
$deployArguments = @(
    "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $deployScript,
    "-RepoPath", $repo,
    "-InputExe", $serverOutput,
    "-InputTrayExe", $trayOutput,
    "-InstallDir", $InstallDir,
    "-DataDir", $DataDir,
    "-Version", $Version,
    "-Listen", $Listen,
    "-HealthURL", $HealthURL,
    "-ResultPath", $ResultPath
)
if (-not [string]::IsNullOrWhiteSpace($AgentAPIURL)) {
    $deployArguments += @("-AgentAPIURL", $AgentAPIURL)
}
if ($AllowInsecureAgentAPI) { $deployArguments += "-AllowInsecureAgentAPI" }

Invoke-Step "Validate per-user deployment" {
    Invoke-Native "powershell.exe" ($deployArguments + "-ValidateOnly")
}
Invoke-Step "Deploy and restart AHA2" {
    Invoke-Native "powershell.exe" $deployArguments
}
$summary.ResultPath = $ResultPath
[pscustomobject]$summary
