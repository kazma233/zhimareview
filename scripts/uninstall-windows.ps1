param([string]$App = "zhimareview")

$dir = Join-Path $env:LOCALAPPDATA "Programs\$App"
Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\$App.lnk") -Force -ErrorAction SilentlyContinue
