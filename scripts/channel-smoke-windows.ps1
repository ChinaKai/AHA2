param(
    [string]$CoreExecutable = "$PSScriptRoot\..\dist\aha2-channel-test-windows-amd64.exe",
    [string]$PluginExecutable = "$PSScriptRoot\..\dist\plugins\feishu\windows-amd64\aha2-channel-feishu.exe",
    [string]$PluginManifest = "$PSScriptRoot\..\dist\plugins\feishu\windows-amd64\plugin.json",
    [switch]$KeepData,
    [switch]$CheckRegistrationQR
)

$ErrorActionPreference = 'Stop'
$core = (Resolve-Path -LiteralPath $CoreExecutable).Path
$plugin = (Resolve-Path -LiteralPath $PluginExecutable).Path
$manifest = (Resolve-Path -LiteralPath $PluginManifest).Path
$smokeRoot = Join-Path ([IO.Path]::GetTempPath()) "AHA2-channel-smoke\$([Guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Force -Path $smokeRoot | Out-Null

function Get-FreePort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { return ([Net.IPEndPoint]$listener.LocalEndpoint).Port }
    finally { $listener.Stop() }
}

function Wait-Health([string]$BaseURL) {
    $deadline = (Get-Date).AddSeconds(20)
    do {
        try {
            $health = Invoke-RestMethod -Uri "$BaseURL/healthz" -TimeoutSec 2
            if ($health.ok) { return }
        }
        catch { }
        Start-Sleep -Milliseconds 200
    } while ((Get-Date) -lt $deadline)
    throw 'health timeout'
}

function Invoke-JSON {
    param(
        [string]$Method,
        [string]$URI,
        [object]$Payload,
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session,
        [hashtable]$Headers = @{}
    )
    $arguments = @{Method=$Method; Uri=$URI; WebSession=$Session; Headers=$Headers; TimeoutSec=20}
    if ($null -ne $Payload) {
        $json = $Payload | ConvertTo-Json -Depth 8 -Compress
        $arguments.ContentType = 'application/json; charset=utf-8'
        $arguments.Body = [Text.Encoding]::UTF8.GetBytes($json)
    }
    Invoke-RestMethod @arguments
}

$results = @()
try {
    foreach ($installed in @($false, $true)) {
        $caseName = if ($installed) { 'installed' } else { 'missing' }
        $caseDir = Join-Path $smokeRoot $caseName
        New-Item -ItemType Directory -Force -Path $caseDir | Out-Null
        if ($installed) {
            $pluginDir = Join-Path $caseDir 'plugins\channels\feishu'
            New-Item -ItemType Directory -Force -Path $pluginDir | Out-Null
            Copy-Item -LiteralPath $plugin -Destination (Join-Path $pluginDir 'aha2-channel-feishu.exe')
            Copy-Item -LiteralPath $manifest -Destination (Join-Path $pluginDir 'plugin.json')
        }
        $port = Get-FreePort
        $base = "http://127.0.0.1:$port"
        $setup = [Guid]::NewGuid().ToString('N')
        $password = "Smoke-$([Guid]::NewGuid().ToString('N'))"
        $env:AHA2_SETUP_TOKEN = $setup
        $process = Start-Process -FilePath $core -ArgumentList @('serve','--listen',"127.0.0.1:$port",'--data-dir',$caseDir,'--agent-api-url',"http://127.0.0.1:$port",'--log-level','error') -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $caseDir 'stdout.log') -RedirectStandardError (Join-Path $caseDir 'stderr.log')
        try {
            Wait-Health $base
            $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
            $registered = Invoke-JSON POST "$base/api/v1/auth/register" @{setup_token=$setup; username='owner'; password=$password} $session
            $csrf = @{'X-CSRF-Token'=$registered.csrf_token}
            $providers = (Invoke-JSON GET "$base/api/v1/channel-providers" $null $session).providers
            $projectsBefore = (Invoke-JSON GET "$base/api/v1/projects" $null $session).projects
            if (-not $installed) {
                if ($providers.Count -ne 0 -or $projectsBefore.Count -ne 0) { throw 'missing-plugin isolation failed' }
                $results += [pscustomobject]@{case='missing'; providers=0; core_ok=$true}
                continue
            }
            if ($providers.Count -ne 1 -or -not $providers[0].available) { throw 'installed provider unavailable' }
            $headers = @{'X-CSRF-Token'=$registered.csrf_token; 'Idempotency-Key'='smoke-create'}
            $created = (Invoke-JSON POST "$base/api/v1/channel-instances" @{plugin_id='feishu'; name='Smoke'} $session $headers).instance
            $projects = (Invoke-JSON GET "$base/api/v1/projects" $null $session).projects
            if (-not ($projects | Where-Object {$_.id -eq $created.host_project_id -and $_.project_type -eq 'channel'})) { throw 'channel host is not visible' }
            $protected = $false
            try { Invoke-JSON DELETE "$base/api/v1/projects/$($created.host_project_id)" $null $session $csrf | Out-Null }
            catch { if ($_.Exception.Response.StatusCode.value__ -eq 409) { $protected = $true } }
            if (-not $protected) { throw 'managed channel project deletion was not blocked' }
            $qrReady = $null
            if ($CheckRegistrationQR) {
                $onboardingHeaders = @{'X-CSRF-Token'=$registered.csrf_token; 'Idempotency-Key'='smoke-onboarding'}
                $onboarding = (Invoke-JSON POST "$base/api/v1/channel-instances/$($created.id)/onboarding-sessions" @{} $session $onboardingHeaders).onboarding
                $deadline = (Get-Date).AddSeconds(45)
                do {
                    $onboarding = (Invoke-JSON GET "$base/api/v1/channel-onboarding-sessions/$($onboarding.id)" $null $session).onboarding
                    if ($onboarding.status -eq 'qr_ready') { break }
                    if ($onboarding.status -in @('failed','expired','cancelled')) { throw "registration status: $($onboarding.status)" }
                    Start-Sleep -Milliseconds 500
                } while ((Get-Date) -lt $deadline)
                if ($onboarding.status -ne 'qr_ready' -or -not $onboarding.verification_url.StartsWith('https://')) { throw 'registration QR was not produced' }
                $web = New-Object Net.WebClient
                try {
                    $web.Headers['Cookie'] = $session.Cookies.GetCookieHeader([Uri]$base)
                    $qr = $web.DownloadData("$base/api/v1/channel-onboarding-sessions/$($onboarding.id)/qr")
                }
                finally { $web.Dispose() }
                if ($qr.Length -lt 100 -or $qr[0] -ne 0x89 -or $qr[1] -ne 0x50 -or $qr[2] -ne 0x4e -or $qr[3] -ne 0x47) { throw 'local QR image was invalid' }
                Invoke-JSON POST "$base/api/v1/channel-onboarding-sessions/$($onboarding.id)/cancel" @{} $session $csrf | Out-Null
                $qrReady = $true
            }
            $results += [pscustomobject]@{case='installed'; providers=1; instances=1; host_visible=$true; delete_protected=$true; qr_ready=$qrReady}
        }
        finally {
            if ($process -and -not $process.HasExited) { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue }
            Remove-Item Env:AHA2_SETUP_TOKEN -ErrorAction SilentlyContinue
        }
    }
    $results | ConvertTo-Json -Compress
}
finally {
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    $resolved = [IO.Path]::GetFullPath($smokeRoot)
    if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) { throw "refusing cleanup outside TEMP: $resolved" }
    if (-not $KeepData -and (Test-Path -LiteralPath $resolved)) { Remove-Item -LiteralPath $resolved -Recurse -Force }
    elseif ($KeepData) { Write-Output "smoke_data=$resolved" }
}
