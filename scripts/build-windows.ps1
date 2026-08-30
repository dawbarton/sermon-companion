param(
    [string]$FFmpegDirectory = "",
    [string]$CCompiler = ""
)
$ErrorActionPreference = "Stop"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "1"
if ($CCompiler) {
    $env:CC = $CCompiler
}
$compiler = $env:CC
if (-not $compiler) {
    $compiler = "gcc"
}
if (-not (Get-Command $compiler -ErrorAction SilentlyContinue)) {
    throw "A Windows-capable C compiler is required for miniaudio. Install GCC on Windows, or pass -CCompiler x86_64-w64-mingw32-gcc when cross-compiling."
}
$env:CC = $compiler
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
