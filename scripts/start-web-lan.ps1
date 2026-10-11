$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$agentExe = Join-Path $projectRoot 'bin\workshop-agent.exe'
$proxyExe = Join-Path $projectRoot 'bin\caddy.exe'
$proxyConfig = Join-Path $projectRoot 'scripts\Caddyfile.lan'
foreach ($required in @($agentExe, $proxyExe, $proxyConfig)) {
    if (-not (Test-Path -LiteralPath $required)) { throw "Missing: $required" }
}
# These exact paths identify checkout-owned processes only.
Get-Process -Name workshop-agent -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $agentExe } | Stop-Process -ErrorAction Stop
$env:WEB_ENABLED = 'true'
$env:WEB_LISTEN_ADDRESS = '127.0.0.1:8080'
$env:WEB_PUBLIC_BASE_URL = 'https://192.168.11.41:8443'
$env:WEB_TRUSTED_PROXIES = '127.0.0.1/32'
$env:XDG_CONFIG_HOME = Join-Path $projectRoot '.tmp\lan-proxy\config'
$env:XDG_DATA_HOME = Join-Path $projectRoot '.tmp\lan-proxy\data'
Start-Process -FilePath $agentExe -WorkingDirectory $projectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $projectRoot '.tmp\lan-agent.out.log') -RedirectStandardError (Join-Path $projectRoot '.tmp\lan-agent.err.log') | Out-Null
$proxyRunning = Get-Process -Name caddy -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $proxyExe }
if (-not $proxyRunning) {
    Start-Process -FilePath $proxyExe -ArgumentList @('run','--config', $proxyConfig, '--adapter','caddyfile') -WorkingDirectory $projectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $projectRoot '.tmp\lan-proxy\stdout.log') -RedirectStandardError (Join-Path $projectRoot '.tmp\lan-proxy\stderr.log') | Out-Null
}
Write-Output 'Web Chat: https://192.168.11.41:8443/chat'
