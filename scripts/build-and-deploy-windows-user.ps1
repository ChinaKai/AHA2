[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$DataDir,
    [string]$WebVersion = "",
    [string]$RepoPath = "",
    [string]$InstallDir = "",
    [string]$Distro = "",
    [string]$ProxyURL = "",
    [string]$Listen = "127.0.0.1:8766",
    [string]$HealthURL = "http://127.0.0.1:8766/healthz",
    [string]$AgentAPIURL = "",
    [switch]$AllowInsecureAgentAPI,
    [int]$MinimumFreeGB = 3,
    [string]$ResultPath = "",
    [switch]$AllowCustomUserWritableInstallDir,
    [switch]$UpdateExistingInstallInPlace,
    [switch]$DeployOnly,
    [switch]$BuildOnly,
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$global:LASTEXITCODE = 0

if ($DeployOnly -and $BuildOnly) { throw "DeployOnly and BuildOnly cannot be used together." }
if ($MinimumFreeGB -lt 1) { throw "MinimumFreeGB must be at least 1." }
$systemRoot = if ([string]::IsNullOrWhiteSpace($env:SystemRoot)) { "C:\Windows" } else { $env:SystemRoot }
$powershellExecutable = Join-Path $systemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$wslExecutable = Join-Path $systemRoot "System32\wsl.exe"
$localAppData = $env:LOCALAPPDATA
if ([string]::IsNullOrWhiteSpace($localAppData)) {
    $localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
}
if ([string]::IsNullOrWhiteSpace($localAppData)) {
    throw "Could not resolve the current user's LocalApplicationData directory."
}
$tempRoot = if ([string]::IsNullOrWhiteSpace($env:TEMP)) { Join-Path $localAppData "Temp" } else { $env:TEMP }
New-Item -ItemType Directory -Path $tempRoot -Force | Out-Null
$env:TEMP = $tempRoot
$env:TMP = $tempRoot
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = Join-Path $localAppData "Programs\AHA2"
}

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
if ([string]::IsNullOrWhiteSpace($RepoPath)) { $RepoPath = Split-Path -Parent $scriptRoot }
$repo = (Resolve-Path -LiteralPath $RepoPath).ProviderPath
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

# PowerShell's call operator silently yields nothing for some console programs
# launched with a non-Windows current directory; wsl.exe is one of them, which
# made the WSL discovery below look like "no distribution is installed". Capture
# stdout/stderr and the exit code through a process instead of relying on `&`.
function Invoke-NativeCapture([string]$Executable, [string[]]$Arguments, [switch]$AllowFailure) {
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $Executable
    $startInfo.Arguments = ($Arguments | ForEach-Object {
        if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ }
    }) -join ' '
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.StandardOutputEncoding = [Text.Encoding]::UTF8
    $startInfo.StandardErrorEncoding = [Text.Encoding]::UTF8
    $startInfo.CreateNoWindow = $true
    if (-not [string]::IsNullOrWhiteSpace($env:SystemDrive)) {
        $startInfo.WorkingDirectory = "$($env:SystemDrive)\"
    }
    $process = [Diagnostics.Process]::Start($startInfo)
    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    $lines = @($stdout -split "`r?`n" |
        ForEach-Object { ($_ -replace "`0", "").Trim() } |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if (-not $AllowFailure -and $process.ExitCode -ne 0) {
        $detail = if ([string]::IsNullOrWhiteSpace($stderr)) { $stdout.Trim() } else { $stderr.Trim() }
        throw "$Executable exited with code $($process.ExitCode). $detail"
    }
    return [pscustomobject]@{ ExitCode = $process.ExitCode; Lines = $lines; Output = $stdout }
}

function Invoke-Native([string]$Executable, [string[]]$Arguments) {
    # Streams to the console so build output stays visible, and still fails the
    # step on a non-zero exit code.
    [void](Invoke-NativeCapture $Executable $Arguments)
}

# The Web build calls module.stripTypeScriptTypes, which Node added in 22.13.0
# and 23.2.0, so a bare `node` is not enough: Ubuntu 24.04's nodejs package is
# Node 18, and running the build with it fails inside build-web.mjs as a module
# SyntaxError that names a Node internal rather than the version requirement.
# A version manager may hold a usable interpreter while its shell hook lives in
# ~/.bashrc, which non-interactive shells never source. Probe the capability and
# prefer the highest candidate instead of trusting PATH.
$wslNodeCandidates = @(
    '$HOME/.nvm/versions/node/*/bin/node',
    '/usr/local/bin/node',
    '/usr/bin/node'
)

function Test-WslNodeCanBuildWeb([string]$ExpandedPath) {
    $probe = Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--",
        "env", "NODE_PATH=", $ExpandedPath, "-e",
        'const m=require("node:module");process.exit(typeof m.stripTypeScriptTypes==="function"?0:1)') -AllowFailure
    return $probe.ExitCode -eq 0
}

function Resolve-WslNode {
    $expanded = @()
    foreach ($candidate in $wslNodeCandidates) {
        # Let the remote shell expand $HOME, then sort highest version first so
        # the choice does not depend on directory listing order.
        $glob = Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--",
            "sh", "-c", "ls -1d $candidate 2>/dev/null") -AllowFailure
        if ($glob.ExitCode -eq 0) { $expanded += $glob.Lines }
    }
    foreach ($path in @($expanded | Sort-Object -Descending { [regex]::Replace($_, '\d+', { $args[0].Value.PadLeft(10, '0') }) })) {
        if (Test-WslNodeCanBuildWeb $path) {
            Write-Host "Using WSL Node at $path"
            return $path
        }
    }
    throw "No WSL Node.js able to run the Web build was found (needs module.stripTypeScriptTypes with {mode:`"transform`"}, present in Node 22.13+/23.2+ and removed in 26)."
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
$windowsNode = ""
if (-not $DeployOnly) {
    if (-not (Test-Path -LiteralPath $wslExecutable -PathType Leaf)) { throw "wsl.exe was not found." }
    $distroList = Invoke-NativeCapture $wslExecutable @("-l", "-q") -AllowFailure
    $installedDistros = $distroList.Lines
    if ($distroList.ExitCode -ne 0 -or $installedDistros.Count -eq 0) { throw "No WSL distribution is available." }
    if ([string]::IsNullOrWhiteSpace($Distro)) {
        $resolvedDistro = $installedDistros[0]
    } else {
        $resolvedDistro = $installedDistros | Where-Object { $_ -eq $Distro } | Select-Object -First 1
        if ([string]::IsNullOrWhiteSpace($resolvedDistro)) {
            throw "WSL distribution '$Distro' was not found. Available: $($installedDistros -join ', ')"
        }
    }
    $linuxRepo = (Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--cd", $repo, "--", "pwd")).Lines -join ""
    if ([string]::IsNullOrWhiteSpace($linuxRepo)) {
        throw "Could not translate the repository path for WSL."
    }
    $goProbe = Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--", "test", "-x", "$linuxRepo/.tools/go/bin/go") -AllowFailure
    if ($goProbe.ExitCode -ne 0) {
        throw "Repository Linux Go toolchain was not found. Do not download another toolchain during deployment."
    }
    $diskProbe = Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--", "df", "-Pk", ".") -AllowFailure
    $diskLine = $diskProbe.Lines | Select-Object -Last 1
    $diskFields = @($diskLine -split '\s+' | Where-Object { $_ -ne "" })
    if ($diskProbe.ExitCode -ne 0 -or $diskFields.Count -lt 4) {
        throw "Could not inspect free space for the WSL repository."
    }
    $freeGB = [Math]::Round(([double]$diskFields[3] * 1KB) / 1GB, 2)
    if ($freeGB -lt $MinimumFreeGB) {
        throw "Repository drive has $freeGB GB free; at least $MinimumFreeGB GB is required."
    }
    $nodeCommand = Get-Command node.exe -ErrorAction SilentlyContinue
    if ($nodeCommand) {
        $windowsNode = $nodeCommand.Source
    } else {
        $wslNode = Resolve-WslNode
    }
}

$linuxGoCache = "$linuxRepo/.tools/gocache-linux"
$linuxGoPath = "$linuxRepo/.tools/gopath-linux"
$resolvedWebVersion = $WebVersion.Trim()
if (-not $DeployOnly -and [string]::IsNullOrWhiteSpace($resolvedWebVersion)) {
    $gitHash = (Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--", "git", "rev-parse", "--short=12", "HEAD")).Lines -join ""
    if ($gitHash -notmatch '^[0-9a-fA-F]{12}$') {
        throw "Could not resolve the 12-character Git hash for the Web version."
    }
    $gitStatus = (Invoke-NativeCapture $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--", "git", "status", "--porcelain")).Lines -join "`n"
    $dirtySuffix = if ([string]::IsNullOrWhiteSpace($gitStatus)) { "" } else { ".dirty" }
    $resolvedWebVersion = "v$normalizedVersion.$([DateTime]::UtcNow.ToString('yyyyMMdd')).$gitHash$dirtySuffix"
}
function Invoke-WslGo([string]$WorkingDirectory, [string[]]$Arguments, [string[]]$EnvironmentEntries = @()) {
    $wslArguments = @("-d", $resolvedDistro, "--cd", $WorkingDirectory, "--", "env", "GOCACHE=$linuxGoCache", "GOPATH=$linuxGoPath")
    $wslArguments += $EnvironmentEntries
    if (-not [string]::IsNullOrWhiteSpace($ProxyURL)) {
        $wslArguments += @("HTTP_PROXY=$ProxyURL", "HTTPS_PROXY=$ProxyURL")
    }
    $wslArguments += "$linuxRepo/.tools/go/bin/go"
    $wslArguments += $Arguments
    Invoke-Native $wslExecutable $wslArguments
}

$summary = [ordered]@{
    Repository = $repo
    Version = $Version
    WebVersion = $resolvedWebVersion
    Mode = if ($DeployOnly) { "deploy-only" } elseif ($BuildOnly) { "build-only" } else { "build-and-deploy" }
    Distro = $resolvedDistro
    NodeRuntime = if ($DeployOnly) { "" } elseif ($windowsNode) { $windowsNode } else { "WSL:${resolvedDistro}:${wslNode}" }
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
        if ($windowsNode) {
            Invoke-Native $windowsNode @((Join-Path $repo "scripts\build-web.mjs"))
        } else {
            Invoke-Native $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--", $wslNode, "scripts/build-web.mjs")
        }
    }
    Invoke-Step "Test Web build" {
        if ($windowsNode) {
            Invoke-Native $windowsNode @("--test", (Join-Path $repo "web\tests\build.test.mjs"))
        } else {
            Invoke-Native $wslExecutable @("-d", $resolvedDistro, "--cd", $linuxRepo, "--", $wslNode, "--test", "web/tests/build.test.mjs")
        }
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
            "build", "-trimpath", "-ldflags=-s -w -X main.version=$normalizedVersion -X main.webVersion=$resolvedWebVersion",
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
$serverVersion = ""
$candidateExitCode = 0
if ($serverOutput.StartsWith("\\", [StringComparison]::Ordinal)) {
    $versionProbeID = [Guid]::NewGuid().ToString("N")
    $versionCandidate = Join-Path $tempRoot ("aha2-version-" + $versionProbeID + ".exe")
    $versionOutput = Join-Path $tempRoot ("aha2-version-" + $versionProbeID + ".out.txt")
    $versionError = Join-Path $tempRoot ("aha2-version-" + $versionProbeID + ".err.txt")
    try {
        Copy-Item -LiteralPath $serverOutput -Destination $versionCandidate -Force
        $versionProcess = Start-Process -FilePath $versionCandidate -ArgumentList @("version") `
            -RedirectStandardOutput $versionOutput -RedirectStandardError $versionError `
            -Wait -PassThru -NoNewWindow
        $candidateExitCode = $versionProcess.ExitCode
        if (Test-Path -LiteralPath $versionOutput -PathType Leaf) {
            $serverVersion = (Get-Content -Raw -LiteralPath $versionOutput).Trim()
        }
    } finally {
        Remove-Item -LiteralPath $versionCandidate,$versionOutput,$versionError -Force -ErrorAction SilentlyContinue
    }
} else {
    $serverVersion = (& $serverOutput version | Out-String).Trim()
    $candidateExitCode = $LASTEXITCODE
}
if ($candidateExitCode -ne 0) {
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
if ($AllowCustomUserWritableInstallDir) { $deployArguments += "-AllowCustomUserWritableInstallDir" }
if ($UpdateExistingInstallInPlace) { $deployArguments += "-UpdateExistingInstallInPlace" }
if ((Test-Path -LiteralPath $pluginOutput -PathType Leaf) -and (Test-Path -LiteralPath $pluginManifestOutput -PathType Leaf)) {
    $deployArguments += @("-InputFeishuPlugin", $pluginOutput, "-InputFeishuManifest", $pluginManifestOutput)
}

Invoke-Step "Validate per-user deployment" {
    Invoke-Native $powershellExecutable ($deployArguments + "-ValidateOnly")
}
Invoke-Step "Deploy and restart AHA2" {
    Invoke-Native $powershellExecutable $deployArguments
}
$summary.ResultPath = $ResultPath
[pscustomobject]$summary
