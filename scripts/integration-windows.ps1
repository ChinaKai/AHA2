param(
    [string]$Executable = "$PSScriptRoot\..\dist\aha2-windows-amd64.exe",
    [int]$Port = 18766,
    [switch]$SkipCodex,
    [switch]$KeepData
)

$ErrorActionPreference = 'Stop'
$exe = (Resolve-Path -LiteralPath $Executable).Path
$runId = [Guid]::NewGuid().ToString('N')
$dataDir = Join-Path ([IO.Path]::GetTempPath()) "AHA2-integration\$runId"
$workspaceDir = Join-Path $dataDir 'workspace'
New-Item -ItemType Directory -Force -Path $workspaceDir | Out-Null

function New-RandomToken {
    $bytes = New-Object byte[] 24
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

$setup = New-RandomToken
$password = New-RandomToken
$env:AHA2_SETUP_TOKEN = $setup
$env:AHA2_IMPORT_AHA_CONFIG = 'E:\AHA\.aha\config.json'
$stdout = Join-Path $dataDir 'server.out.log'
$stderr = Join-Path $dataDir 'server.err.log'
$process = $null
$base = "http://127.0.0.1:$Port"
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession

function Invoke-API {
    param(
        [string]$Method,
        [string]$Path,
        [object]$Body = $null,
        [hashtable]$Headers = @{}
    )
    $parameters = @{
        Method = $Method
        Uri = "$base$Path"
        WebSession = $session
        Headers = $Headers
        TimeoutSec = 180
    }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = $Body | ConvertTo-Json -Depth 10
    }
    return Invoke-RestMethod @parameters
}

function Wait-Task {
    param([string]$TaskId, [int]$Seconds = 180)
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        $detail = Invoke-API GET "/api/v1/tasks/$TaskId"
        if ($detail.task.status -in @('waiting_user', 'failed', 'blocked', 'completed')) {
            return $detail
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "Task timed out: $TaskId"
}

try {
    $process = Start-Process `
        -FilePath $exe `
        -ArgumentList @('serve', '--listen', "127.0.0.1:$Port", '--data-dir', $dataDir, '--log-level', 'info') `
        -PassThru `
        -WindowStyle Hidden `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr

    $deadline = (Get-Date).AddSeconds(30)
    do {
        try {
            $health = Invoke-RestMethod "$base/healthz" -TimeoutSec 2
            break
        }
        catch {
            Start-Sleep -Milliseconds 300
        }
    } while ((Get-Date) -lt $deadline)
    if (-not $health.ok) {
        throw 'Health check failed.'
    }

    $registered = Invoke-API POST '/api/v1/auth/register' @{
        setup_token = $setup
        username = 'owner'
        password = $password
    }
    $headers = @{'X-CSRF-Token' = $registered.csrf_token}
    $imported = Invoke-API POST '/api/v1/config/import-aha' @{path = 'E:\AHA\.aha\config.json'} $headers
    $project = (Invoke-API POST '/api/v1/projects' @{
        name = 'Integration Project'
        description = 'AHA2 integration test'
        default_branch = 'main'
    } $headers).project
    $workspace = (Invoke-API POST '/api/v1/workspaces' @{
        project_id = $project.id
        name = 'Local Integration'
        locality = 'local'
        transport = 'native'
        root_path = $workspaceDir
        ssh_port = 22
    } $headers).workspace
    $workspace = (Invoke-API POST "/api/v1/workspaces/$($workspace.id)/detect" @{} $headers).workspace
    $models = (Invoke-API GET '/api/v1/models').models
    $envGroups = (Invoke-API GET '/api/v1/env-groups').env_groups

    $stubTask = (Invoke-API POST '/api/v1/tasks' @{
        project_id = $project.id
        workspace_id = $workspace.id
        title = 'Stub multi turn'
        request = 'Validate stub session reuse'
        backend = 'stub'
        model_id = 'model_stub'
        env_group_id = 'env_stub'
        reasoning_effort = 'high'
    } $headers).task
    $stubFirst = Wait-Task $stubTask.id 30
    $null = Invoke-API POST "/api/v1/tasks/$($stubTask.id)/messages" @{content = 'second stub turn'} $headers
    $stubSecond = Wait-Task $stubTask.id 30
    if (
        $stubSecond.turns.Count -ne 2 -or
        $stubSecond.turns[0].backend_session_id -ne $stubSecond.turns[1].backend_session_id
    ) {
        throw 'Stub session was not reused.'
    }

    $codexResult = $null
    if (-not $SkipCodex) {
        $codexModel = $models |
            Where-Object {$_.wire_model -eq 'gpt-5.6-sol' -and $_.backend -eq 'codex'} |
            Select-Object -First 1
        if (-not $codexModel) {
            throw 'Imported Codex model was not found.'
        }
        $codexEnv = $envGroups |
            Where-Object {$_.id -eq $codexModel.default_env_group_id} |
            Select-Object -First 1
        if (-not $codexEnv) {
            throw 'Imported Codex Env Group was not found.'
        }
        $codexTask = (Invoke-API POST '/api/v1/tasks' @{
            project_id = $project.id
            workspace_id = $workspace.id
            title = 'Codex multi turn'
            request = 'Reply with AHA2_CODEX_OK. Do not modify files. Include the required aha2 checkpoint.'
            backend = 'codex'
            model_id = $codexModel.id
            env_group_id = $codexEnv.id
            reasoning_effort = 'high'
        } $headers).task
        $codexFirst = Wait-Task $codexTask.id 180
        if ($codexFirst.task.status -ne 'waiting_user') {
            throw "Codex first turn failed: $($codexFirst.turns[-1].error)"
        }
        $null = Invoke-API POST "/api/v1/tasks/$($codexTask.id)/messages" @{
            content = 'Reply with AHA2_CODEX_RESUME_OK. Do not modify files. Include the required aha2 checkpoint.'
        } $headers
        $codexSecond = Wait-Task $codexTask.id 180
        if ($codexSecond.task.status -ne 'waiting_user') {
            throw "Codex second turn failed: $($codexSecond.turns[-1].error)"
        }
        if (
            $codexSecond.turns.Count -ne 2 -or
            $codexSecond.turns[0].backend_session_id -ne $codexSecond.turns[1].backend_session_id
        ) {
            throw 'Codex session was not reused.'
        }
        $codexResult = @{
            turns = $codexSecond.turns.Count
            session_reused = $true
            last_reply = (
                $codexSecond.messages |
                    Where-Object {$_.role -eq 'assistant'} |
                    Select-Object -Last 1
            ).content
        }
    }

    [pscustomobject]@{
        health = $health.ok
        imported_models = $imported.summary.models
        imported_env_groups = $imported.summary.env_groups
        workspace_health = $workspace.health
        codex_detected = $workspace.capabilities.codex.status
        stub_turns = $stubSecond.turns.Count
        stub_session_reused = $true
        codex = $codexResult
        temporary_data_cleaned = -not $KeepData
    } | ConvertTo-Json -Depth 8
}
finally {
    if ($process -and -not $process.HasExited) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item Env:AHA2_SETUP_TOKEN -ErrorAction SilentlyContinue
    Remove-Item Env:AHA2_IMPORT_AHA_CONFIG -ErrorAction SilentlyContinue
    if (-not $KeepData -and (Test-Path -LiteralPath $dataDir)) {
        $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
        $resolved = [IO.Path]::GetFullPath($dataDir)
        if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to clean test data outside TEMP: $resolved"
        }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
