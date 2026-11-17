$ErrorActionPreference = 'Stop'
$base = 'D:\Andree\Portfolio Projects\Local MySQL\mysql-8.4.13-winx64'
$data = 'D:\Andree\Portfolio Projects\Local MySQL\data'
$mysqld = Join-Path $base 'bin\mysqld.exe'
$go = 'C:\Program Files\Go\bin\go.exe'
if (!(Test-Path -LiteralPath $mysqld) -or !(Test-Path -LiteralPath $go)) { throw 'Local MySQL or Go was not found. See README for setup.' }
$running = $false
try { $socket = [Net.Sockets.TcpClient]::new('127.0.0.1',3307); $socket.Close(); $running=$true } catch {}
if (!$running) {
  $arguments = @('--no-defaults', "--basedir=`"$base`"", "--datadir=`"$data`"", '--port=3307', '--bind-address=127.0.0.1', '--skip-log-bin', '--console')
  Start-Process -FilePath $mysqld -ArgumentList $arguments -WindowStyle Hidden
  for ($i=0; $i -lt 30; $i++) { try { $socket=[Net.Sockets.TcpClient]::new('127.0.0.1',3307); $socket.Close(); $running=$true; break } catch { Start-Sleep -Milliseconds 500 } }
  if (!$running) { throw 'MySQL did not start; check its local logs.' }
}
Set-Location -LiteralPath $PSScriptRoot
& $go run .
