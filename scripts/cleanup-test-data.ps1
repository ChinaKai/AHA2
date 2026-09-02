$ErrorActionPreference = 'Stop'
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$targets = @()
$integrationRoot = Join-Path $tempRoot 'AHA2-integration'
if (Test-Path -LiteralPath $integrationRoot) {
    $targets += Get-Item -LiteralPath $integrationRoot
}
$targets += Get-ChildItem -LiteralPath $tempRoot -Directory -Filter 'aha2-ui-smoke-*' -ErrorAction SilentlyContinue
$targets += Get-ChildItem -LiteralPath $tempRoot -Directory -Filter 'aha2-debug-*' -ErrorAction SilentlyContinue

foreach ($target in $targets) {
    $resolved = [IO.Path]::GetFullPath($target.FullName)
    if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to clean path outside TEMP: $resolved"
    }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}

Write-Output "cleaned=$($targets.Count)"
