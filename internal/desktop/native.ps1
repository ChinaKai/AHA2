$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
try {
    $setupJSON = [Console]::In.ReadLine()
    if ($null -eq $setupJSON -or $setupJSON.Length -gt 262144) { throw 'request_limit' }
    $setup = $setupJSON | ConvertFrom-Json
    Add-Type -TypeDefinition $setup.source -ReferencedAssemblies @(
        'Accessibility', 'UIAutomationClient', 'UIAutomationTypes', 'WindowsBase',
        'System.Drawing', 'System.Web.Extensions'
    ) -ErrorAction Stop
    [AHADesktop.Native]::Serve($setup.lane)
} catch {
    [Console]::Out.WriteLine('{"id":0,"result":{"ok":false,"error":"native_unavailable"}}')
}
