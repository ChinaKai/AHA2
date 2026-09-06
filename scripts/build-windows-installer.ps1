[CmdletBinding()]
param(
    [string]$RepoPath = (Split-Path -Parent $PSScriptRoot),
    [string]$InputExe = "",
    [string]$OutputDir = "",
    [string]$Version = "dev",
    [string]$ISCCPath = "",
    [switch]$ValidateOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = [IO.Path]::GetFullPath($RepoPath)
$iss = Join-Path $repo "installer\windows\AHA2.iss"
if (-not (Test-Path -LiteralPath (Join-Path $repo "go.mod") -PathType Leaf)) {
    throw "RepoPath is not an AHA2 repository: $repo"
}
if (-not (Test-Path -LiteralPath $iss -PathType Leaf)) {
    throw "Inno Setup script not found: $iss"
}
if ([string]::IsNullOrWhiteSpace($InputExe)) {
    $InputExe = Join-Path $repo "dist\aha2-windows-amd64.exe"
}
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $repo "dist\installer"
}
$inputPath = [IO.Path]::GetFullPath($InputExe)
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
    "service run --listen {#ListenAddress} --data-dir",
    "{commonappdata}\AHA2",
    "start= delayed-auto",
    "AHA2 local AI task and agent control plane",
    "actions= restart/5000/restart/15000/restart/60000",
    "RunSC('delete",
    "http://127.0.0.1:8766"
)
foreach ($contract in $requiredContracts) {
    if (-not $source.Contains($contract)) {
        throw "Installer contract is missing: $contract"
    }
}
if ($ValidateOnly) {
    if (Test-Path -LiteralPath $inputPath -PathType Leaf) {
        Write-Output "Installer definition valid; input executable found."
    } else {
        Write-Output "Installer definition valid; input executable is not present, so compilation was not attempted."
    }
    exit 0
}

if (-not (Test-Path -LiteralPath $inputPath -PathType Leaf)) {
    throw "Windows amd64 executable not found: $inputPath"
}
if ([string]::IsNullOrWhiteSpace($ISCCPath)) {
    $command = Get-Command "ISCC.exe" -ErrorAction SilentlyContinue
    if ($command) {
        $ISCCPath = $command.Path
    } else {
        $candidates = @(
            (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe"),
            (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
        )
        $ISCCPath = $candidates | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) } | Select-Object -First 1
    }
}
if ([string]::IsNullOrWhiteSpace($ISCCPath) -or -not (Test-Path -LiteralPath $ISCCPath -PathType Leaf)) {
    throw "Inno Setup 6 compiler (ISCC.exe) was not found."
}

New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
& $ISCCPath "/DMyAppVersion=$normalizedVersion" "/DSourceExe=$inputPath" "/DOutputDir=$outputPath" $iss
if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup compilation failed with exit code $LASTEXITCODE."
}

$setup = Join-Path $outputPath "AHA2-Setup-x64.exe"
if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw "Expected installer was not produced: $setup"
}
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $setup).Hash.ToLowerInvariant()
$hashFile = "$setup.sha256"
[IO.File]::WriteAllText($hashFile, "$hash  AHA2-Setup-x64.exe`n", [Text.UTF8Encoding]::new($false))
Write-Output "Installer: $setup"
Write-Output "SHA256: $hashFile"
