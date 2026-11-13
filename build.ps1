# Builds gameforge.exe as a GUI app (no console window). No C compiler needed:
# Tk is used without CGO.
$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '0'
go build -ldflags '-H windowsgui -s -w' -o gameforge.exe .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host 'OK: gameforge.exe'
