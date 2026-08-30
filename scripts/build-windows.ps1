param(
    [string]$FFmpegDirectory = ""
)
$ErrorActionPreference = "Stop"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
New-Item -ItemType Directory -Force -Path "dist\SermonCompanion" | Out-Null
go build -trimpath -ldflags "-s -w" -o "dist\SermonCompanion\SermonCompanion.exe" .\cmd\sermon-companion
Copy-Item "scripts\Start Sermon Companion.cmd" "dist\SermonCompanion\Start Sermon Companion.cmd"
Copy-Item "docs\WINDOWS-SETUP.md" "dist\SermonCompanion\README.txt"
if ($FFmpegDirectory) {
    Copy-Item (Join-Path $FFmpegDirectory "ffmpeg.exe") "dist\SermonCompanion\ffmpeg.exe"
    Copy-Item (Join-Path $FFmpegDirectory "ffprobe.exe") "dist\SermonCompanion\ffprobe.exe"
    Write-Host "Bundled FFmpeg and FFprobe"
} else {
    Write-Warning "FFmpeg was not bundled. Re-run with -FFmpegDirectory C:\path\to\ffmpeg\bin for a self-contained operator package."
}
Write-Host "Built dist\SermonCompanion\SermonCompanion.exe"
