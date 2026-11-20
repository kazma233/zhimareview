param([string]$App = "zhimareview")

$dir = Join-Path $env:LOCALAPPDATA "Programs\$App"
New-Item -ItemType Directory -Force -Path $dir | Out-Null

$exe = Join-Path $PSScriptRoot "..\build\windows-amd64\$App.exe"
Copy-Item $exe $dir -Force

$ws = New-Object -ComObject WScript.Shell
$lnk = $ws.CreateShortcut([IO.Path]::Combine($env:APPDATA, "Microsoft\Windows\Start Menu\Programs\$App.lnk"))
$lnk.TargetPath = Join-Path $dir "$App.exe"
$lnk.Save()
Write-Host "installed $App -> $dir"
