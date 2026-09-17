param([Parameter(Mandatory=$true)][string]$ReportPath,[Parameter(Mandatory=$true)][string]$OutputPath)
$ErrorActionPreference = 'Stop'
$reportFile = (Resolve-Path -LiteralPath $ReportPath).Path
$profilePath = Join-Path (Split-Path $PSScriptRoot -Parent) 'reports\day11-memory\preview-profile-cdp'
$browser = Start-Process -FilePath 'C:\Program Files\Google\Chrome\Application\chrome.exe' -WindowStyle Hidden -PassThru -ArgumentList @('--headless','--disable-gpu','--disable-background-networking','--no-first-run','--no-default-browser-check','--remote-debugging-port=9327',"--user-data-dir=$profilePath",([System.Uri]$reportFile).AbsoluteUri)
$socket = [System.Net.WebSockets.ClientWebSocket]::new()
$cancelToken = [System.Threading.CancellationToken]::None
$script:requestID = 0
function Invoke-CDP([string]$Method,[hashtable]$Params) {
 $script:requestID++
 $currentID=$script:requestID
 $json=@{id=$currentID;method=$Method;params=$Params} | ConvertTo-Json -Depth 15 -Compress
 $bytes=[Text.Encoding]::UTF8.GetBytes($json)
 $socket.SendAsync([ArraySegment[byte]]::new($bytes),[System.Net.WebSockets.WebSocketMessageType]::Text,$true,$cancelToken).GetAwaiter().GetResult()
 while ($true) {
  $buffer=New-Object byte[] 65536
  $stream=[IO.MemoryStream]::new()
  do {
   $packet=$socket.ReceiveAsync([ArraySegment[byte]]::new($buffer),$cancelToken).GetAwaiter().GetResult()
   $stream.Write($buffer,0,$packet.Count)
  } while (-not $packet.EndOfMessage)
  $reply=[Text.Encoding]::UTF8.GetString($stream.ToArray()) | ConvertFrom-Json
  $stream.Dispose()
  if ($reply.id -eq $currentID) {if ($reply.error) {throw ($reply.error | ConvertTo-Json -Compress)};return $reply.result}
 }
}
try {
 $targets=$null
 for ($attempt=0;$attempt -lt 100;$attempt++) {
  try {$targets=Invoke-RestMethod 'http://127.0.0.1:9327/json';break} catch {Start-Sleep -Milliseconds 100}
 }
 if (-not $targets) {throw 'Chrome debug endpoint unavailable'}
 $target=$targets | Where-Object type -eq 'page' | Select-Object -First 1
 $socket.ConnectAsync([Uri]$target.webSocketDebuggerUrl,$cancelToken).GetAwaiter().GetResult()
 $null=Invoke-CDP 'Emulation.setDeviceMetricsOverride' @{width=390;height=1600;deviceScaleFactor=1;mobile=$true}
 $null=Invoke-CDP 'Runtime.evaluate' @{expression='document.fonts.ready.then(()=>true)';awaitPromise=$true}
 $layout=Invoke-CDP 'Runtime.evaluate' @{expression='JSON.stringify({innerWidth:innerWidth,scrollWidth:document.documentElement.scrollWidth,clientWidth:document.documentElement.clientWidth,externalResources:performance.getEntriesByType("resource").filter(x=>/^https?:/.test(x.name)).length})';returnByValue=$true}
 Write-Output $layout.result.value
 $shot=Invoke-CDP 'Page.captureScreenshot' @{format='png';captureBeyondViewport=$false}
 [IO.File]::WriteAllBytes([IO.Path]::GetFullPath($OutputPath),[Convert]::FromBase64String($shot.data))
} finally {
 $socket.Dispose()
 Stop-Process -Id $browser.Id -ErrorAction SilentlyContinue
}
