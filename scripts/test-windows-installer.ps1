[CmdletBinding()]
param([string]$RepoPath = (Split-Path -Parent $PSScriptRoot))

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = [IO.Path]::GetFullPath($RepoPath)
$missingInput = Join-Path ([IO.Path]::GetTempPath()) ("aha2-installer-validation-" + [Guid]::NewGuid().ToString("N") + ".exe")
& (Join-Path $repo "scripts\build-windows-installer.ps1") -RepoPath $repo -InputExe $missingInput -ValidateOnly

$installer = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $repo "installer\windows\AHA2.iss")
foreach ($contract in @("PrivilegesRequired=admin", "ArchitecturesAllowed=x64compatible", "uninsneveruninstall", "CurUninstallStepChanged", '[Icons]', 'AHA2 local AI task and agent control plane')) {
    if (-not $installer.Contains($contract)) {
        throw "Installer safety contract is missing: $contract"
    }
}

$workflowPath = Join-Path $repo ".github\workflows\release.yml"
$workflow = Get-Content -Raw -Encoding UTF8 -LiteralPath $workflowPath
$requiredWorkflowContracts = @(
    "runs-on: windows-latest",
    "build-windows-installer.ps1",
    "AHA2-Setup-x64.exe",
    "AHA2-Setup-x64.exe.sha256",
    "merge-multiple: true",
    "portable-release",
    "aha2-windows-amd64.exe",
    "main.version=",
    "gh release create"
)
foreach ($contract in $requiredWorkflowContracts) {
    if (-not $workflow.Contains($contract)) {
        throw "Release workflow contract is missing: $contract"
    }
}
Write-Output "Windows installer and Release workflow contracts are valid."
