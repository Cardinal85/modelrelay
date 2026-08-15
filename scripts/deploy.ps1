#requires -version 5.1
<#
ModelRelay 一键部署脚本（Windows）

示例：
  .\scripts\deploy.ps1 -SourceDir .\dist\modelrelay-0.2.0-windows-amd64 -Component Relay
  .\scripts\deploy.ps1 -SourceDir .\dist\modelrelay-0.2.0-windows-amd64 `
    -Component Agent -NodeId gpu-001 `
    -RelayUrl wss://relay.example.com:9443/agent/v1/connect

优先使用 NSSM；未安装 NSSM 时使用 Windows Task Scheduler 作为启动托管。
脚本不会覆盖现有配置、证书或私钥。
#>
[CmdletBinding()]
param(
    [ValidateSet("Relay", "Agent", "Both")]
    [string]$Component = "Both",
    [string]$SourceDir = "",
    [string]$InstallRoot = "C:\ModelRelay",
    [string]$RelayId = $env:COMPUTERNAME,
    [string]$NodeId = $env:COMPUTERNAME,
    [string]$RelayUrl = "wss://relay.example.com:9443/agent/v1/connect",
    [string]$LocalBaseUrl = "http://127.0.0.1:8000/v1",
    [string]$RelayCert = "",
    [string]$RelayKey = "",
    [string]$AgentCA = "",
    [string]$AgentCert = "",
    [string]$AgentKey = "",
    [string]$RelayCA = "",
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    throw "deploy.ps1: $Message"
}

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail "run this script in an elevated PowerShell (right-click Windows PowerShell, Run as administrator)"
    }
}

function Quote-Yaml([string]$Value) {
    return "'" + $Value.Replace("'", "''") + "'"
}

function New-RandomHex {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return (($bytes | ForEach-Object { $_.ToString("x2") }) -join "")
}

function Ensure-Directory([string]$Path) {
    New-Item -ItemType Directory -Force -Path $Path | Out-Null
}

function Copy-Binary([string]$Name) {
    $source = Join-Path $SourceDir "$Name.exe"
    if (-not (Test-Path $source -PathType Leaf)) {
        Fail "发布目录缺少 $source"
    }
    Copy-Item -Force $source (Join-Path $BinDir "$Name.exe")
}

function Write-RelayConfig {
    if (Test-Path $RelayConfig -PathType Leaf) { return }
    $text = @"
relay_id: $(Quote-Yaml $RelayId)
relay_name: $(Quote-Yaml $RelayId)
http_listen: "127.0.0.1:9100"
wss_listen: "0.0.0.0:9443"
tls_cert: $(Quote-Yaml $RelayCert)
tls_key: $(Quote-Yaml $RelayKey)
agent_ca: $(Quote-Yaml $AgentCA)
internal_auth:
  enabled: true
  token: "`${RELAY_INTERNAL_TOKEN}"
limits:
  max_body_bytes: 67108864
  max_concurrency: 64
  queue_length: 256
  queue_timeout_sec: 30
  ttft_timeout_sec: 120
  idle_timeout_sec: 300
  request_timeout_sec: 1800
  heartbeat_timeout_sec: 60
admin:
  listen: "127.0.0.1:9200"
  session_ttl_min: 30
  users:
    - username: admin
      password: "`${RELAY_ADMIN_PASSWORD}"
      role: admin
store:
  db_path: $(Quote-Yaml $DataDir\modelrelay.db)
retention:
  keep_prompt_response: false
  retention_days: 30
log_level: info
"@
    Set-Content -Path $RelayConfig -Value $text -Encoding UTF8
    Write-Host "created $RelayConfig; review certificate paths before starting Relay"
}

function Write-AgentConfig {
    if (Test-Path $AgentConfig -PathType Leaf) { return }
    $text = @"
node_id: $(Quote-Yaml $NodeId)
max_body_bytes: 16777216
relays:
  - url: $(Quote-Yaml $RelayUrl)
    priority: 1
tls:
  cert: $(Quote-Yaml $AgentCert)
  key: $(Quote-Yaml $AgentKey)
  ca: $(Quote-Yaml $RelayCA)
  insecure_skip_verify: false
local:
  base_url: $(Quote-Yaml $LocalBaseUrl)
  api_key: "`${LOCAL_MODEL_API_KEY}"
  tls_verify: true
  connect_timeout_sec: 5
  response_timeout_sec: 1800
  max_concurrency: 8
probe:
  interval_sec: 600
  enabled: [chat, chat_stream, completions, embeddings, responses, tools]
heartbeat_interval: 20
log_level: info
"@
    Set-Content -Path $AgentConfig -Value $text -Encoding UTF8
    Write-Host "created $AgentConfig; review Relay, certificate, and model URL settings"
}

function Ensure-EnvFile([string]$Path, [bool]$Relay) {
    if (Test-Path $Path -PathType Leaf) { return }
    if ($Relay) {
        @(
            "RELAY_INTERNAL_TOKEN=$(New-RandomHex)"
            "RELAY_ADMIN_PASSWORD=$(New-RandomHex)"
        ) | Set-Content -Path $Path -Encoding ASCII
        Write-Host "created $Path; save the generated admin password before sharing the host"
    } else {
        "LOCAL_MODEL_API_KEY=" | Set-Content -Path $Path -Encoding ASCII
    }
}

function Write-Runner([string]$Name, [string]$Executable, [string]$Config, [string]$EnvFile) {
    $runner = Join-Path $BinDir "run-$Name.ps1"
    $logPath = Join-Path $DataDir "$Name.log"
    $text = @(
        '$ErrorActionPreference = "Stop"'
        '$log = ''' + $logPath.Replace("'", "''") + ''''
        'New-Item -ItemType Directory -Force -Path (Split-Path $log) | Out-Null'
        'Get-Content -LiteralPath ''' + $EnvFile.Replace("'", "''") + ''' | ForEach-Object {'
        '    if ($_ -match ''^\s*([^#=]+)=(.*)$'') {'
        '        [Environment]::SetEnvironmentVariable($matches[1].Trim(), $matches[2])'
        '    }'
        '}'
        'Write-Output ("{0:yyyy-MM-dd HH:mm:ss} starting {1}" -f (Get-Date), ''' + $Executable.Replace("'", "''") + ''') | Add-Content -LiteralPath $log'
        '& ''' + $Executable.Replace("'", "''") + ''' -config ''' + $Config.Replace("'", "''") + ''' *>> $log'
        'exit $LASTEXITCODE'
    ) -join [Environment]::NewLine
    [System.IO.File]::WriteAllText($runner, $text)
    return $runner
}

function Install-NssmTask([string]$Name, [string]$Runner, [string]$WorkingDirectory, [string]$TaskName) {
    foreach ($legacy in @($TaskName, "ModelRelay-$Name")) {
        Unregister-ScheduledTask -TaskName $legacy -Confirm:$false -ErrorAction SilentlyContinue
    }
    $nssm = Get-Command nssm.exe -ErrorAction SilentlyContinue
    if ($null -ne $nssm) {
        & $nssm.Source stop $Name 2>$null | Out-Null
        & $nssm.Source remove $Name confirm 2>$null | Out-Null
        & $nssm.Source install $Name "$PSHOME\powershell.exe" | Out-Null
        & $nssm.Source set $Name AppParameters "-NoProfile -ExecutionPolicy Bypass -File `"$Runner`"" | Out-Null
        & $nssm.Source set $Name AppDirectory $WorkingDirectory | Out-Null
        & $nssm.Source set $Name Start SERVICE_AUTO_START | Out-Null
        & $nssm.Source set $Name AppStdout (Join-Path $DataDir "$Name.out.log") | Out-Null
        & $nssm.Source set $Name AppStderr (Join-Path $DataDir "$Name.err.log") | Out-Null
        return "nssm"
    }

    $action = New-ScheduledTaskAction -Execute "$PSHOME\powershell.exe" `
        -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$Runner`"" `
        -WorkingDirectory $WorkingDirectory
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
    $settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
        -Principal $principal -Settings $settings -Force | Out-Null
    return "task"
}

Assert-Administrator

if ([string]::IsNullOrWhiteSpace($SourceDir)) {
    $SourceDir = Join-Path $PSScriptRoot "..\bin"
}
$SourceDir = (Resolve-Path $SourceDir).Path
$BinDir = Join-Path $InstallRoot "bin"
$EtcDir = Join-Path $InstallRoot "etc"
$DataDir = Join-Path $InstallRoot "data"
$RelayConfigDir = Join-Path $EtcDir "relay"
$AgentConfigDir = Join-Path $EtcDir "agent"
$RelayConfig = Join-Path $RelayConfigDir "relay.yaml"
$AgentConfig = Join-Path $AgentConfigDir "agent.yaml"
$RelayEnv = Join-Path $RelayConfigDir "relay.env"
$AgentEnv = Join-Path $AgentConfigDir "agent.env"

if ([string]::IsNullOrWhiteSpace($RelayCert)) { $RelayCert = Join-Path $RelayConfigDir "relay.crt" }
if ([string]::IsNullOrWhiteSpace($RelayKey)) { $RelayKey = Join-Path $RelayConfigDir "relay.key" }
if ([string]::IsNullOrWhiteSpace($AgentCA)) { $AgentCA = Join-Path $RelayConfigDir "agent-ca.crt" }
if ([string]::IsNullOrWhiteSpace($AgentCert)) { $AgentCert = Join-Path $AgentConfigDir "$NodeId.crt" }
if ([string]::IsNullOrWhiteSpace($AgentKey)) { $AgentKey = Join-Path $AgentConfigDir "$NodeId.key" }
if ([string]::IsNullOrWhiteSpace($RelayCA)) { $RelayCA = Join-Path $AgentConfigDir "relay-ca.crt" }

Ensure-Directory $BinDir
Ensure-Directory $DataDir
Ensure-Directory $RelayConfigDir
Ensure-Directory $AgentConfigDir

if ($Component -eq "Relay" -or $Component -eq "Both") { Copy-Binary "relay" }
if ($Component -eq "Agent" -or $Component -eq "Both") { Copy-Binary "agent" }
if (Test-Path (Join-Path $SourceDir "certctl.exe") -PathType Leaf) { Copy-Binary "certctl" }

$relayMode = $null
$agentMode = $null
$relayRunner = $null
$agentRunner = $null

if ($Component -eq "Relay" -or $Component -eq "Both") {
    Write-RelayConfig
    Ensure-EnvFile $RelayEnv $true
    $relayRunner = Write-Runner "relay" (Join-Path $BinDir "relay.exe") $RelayConfig $RelayEnv
    $relayMode = Install-NssmTask "ModelRelayRelay" $relayRunner $DataDir "ModelRelay-Relay"
}
if ($Component -eq "Agent" -or $Component -eq "Both") {
    Write-AgentConfig
    Ensure-EnvFile $AgentEnv $false
    $agentRunner = Write-Runner "agent" (Join-Path $BinDir "agent.exe") $AgentConfig $AgentEnv
    $agentMode = Install-NssmTask "ModelRelayAgent" $agentRunner $DataDir "ModelRelay-Agent"
}

if (-not $NoStart) {
    if ($relayMode -eq "nssm") { Start-Service ModelRelayRelay }
    elseif ($relayMode -eq "task") { Start-ScheduledTask -TaskName "ModelRelay-Relay" }
    if ($agentMode -eq "nssm") { Start-Service ModelRelayAgent }
    elseif ($agentMode -eq "task") { Start-ScheduledTask -TaskName "ModelRelay-Agent" }
}

Write-Host ""
Write-Host "ModelRelay deployment files installed."
if ($relayMode) {
    Write-Host "Relay host only. Do not run these commands on a GPU Agent machine."
    if ($relayMode -eq "nssm") {
        Write-Host "Start Relay:  Start-Service ModelRelayRelay"
        Write-Host "Logs:         $(Join-Path $DataDir 'ModelRelayRelay.err.log')"
    } else {
        Write-Host "Start Relay:  Start-ScheduledTask -TaskName ModelRelay-Relay"
        Write-Host "Logs:         $(Join-Path $DataDir 'relay.log')"
    }
}
if ($agentMode) {
    Write-Host "GPU Agent only. Do not start ModelRelayRelay on this machine."
    if ($agentMode -eq "nssm") {
        Write-Host "Start Agent:  Start-Service ModelRelayAgent"
        Write-Host "Logs:         $(Join-Path $DataDir 'ModelRelayAgent.err.log')"
    } else {
        Write-Host "Start Agent:  Start-ScheduledTask -TaskName ModelRelay-Agent"
        Write-Host "Manual start: powershell -NoProfile -ExecutionPolicy Bypass -File `"$agentRunner`""
        Write-Host "Logs:         $(Join-Path $DataDir 'agent.log')"
    }
    Write-Host "Next: generate CSR with certctl.exe, copy gpu-001.crt and relay-ca.crt, then start Agent."
}
