$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
$settings = Join-Path $PSScriptRoot '.env.local.ps1'
if (Test-Path -LiteralPath $settings) { . $settings }
$go = if ($LocalGo) { $LocalGo } else { 'go' }
if ($LocalInventoryDSN -and !$env:MYSQL_DSN) { $env:MYSQL_DSN = $LocalInventoryDSN }
if ($LocalMySQLBase -and $LocalMySQLData) {
  $mysqld = Join-Path $LocalMySQLBase 'bin\mysqld.exe'
  $running = $false
  try { $socket = [Net.Sockets.TcpClient]::new('127.0.0.1',3307); $socket.Close(); $running=$true } catch {}
  if (!$running) {
    if (!(Test-Path -LiteralPath $mysqld)) { throw 'Configured MySQL executable was not found.' }
    $arguments = @('--no-defaults', "--basedir=`"$LocalMySQLBase`"", "--datadir=`"$LocalMySQLData`"", '--port=3307', '--bind-address=127.0.0.1', '--skip-log-bin', '--console')
    Start-Process -FilePath $mysqld -ArgumentList $arguments -WindowStyle Hidden
    for ($i=0; $i -lt 30; $i++) { try { $socket=[Net.Sockets.TcpClient]::new('127.0.0.1',3307); $socket.Close(); $running=$true; break } catch { Start-Sleep -Milliseconds 500 } }
    if (!$running) { throw 'MySQL did not start; check its local logs.' }
  }
}
& $go run .
