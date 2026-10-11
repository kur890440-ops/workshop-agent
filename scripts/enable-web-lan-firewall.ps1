$ErrorActionPreference = 'Stop'
$ruleName = 'WorkshopAgent-WebChat-LAN-8443'
$existing = Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue
if ($existing) { $existing | Remove-NetFirewallRule }
New-NetFirewallRule -Name $ruleName -DisplayName 'WorkshopAgent HTTPS' -Direction Inbound -Action Allow -Protocol TCP -LocalAddress '192.168.11.41' -LocalPort 8443 -RemoteAddress Any -Program 'C:\TEMP\WorkshopAgent\bin\caddy.exe' -Profile Any | Out-Null
'Firewall rule installed: TCP 8443, source Any' | Set-Content -LiteralPath 'C:\TEMP\WorkshopAgent\.tmp\lan-proxy\firewall-result.txt' -Encoding UTF8

Get-NetFirewallRule -Name $ruleName | Get-NetFirewallAddressFilter | Select-Object LocalAddress,RemoteAddress | ConvertTo-Json | Set-Content -LiteralPath 'C:\TEMP\WorkshopAgent\.tmp\lan-proxy\firewall-verified.json' -Encoding UTF8
