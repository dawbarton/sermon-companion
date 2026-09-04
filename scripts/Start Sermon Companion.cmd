@echo off
rem Sermon Companion runs in the notification area and needs no console window,
rem so the executable can also be double-clicked directly. This launcher is kept
rem for existing shortcuts.
cd /d "%~dp0"
start "Sermon Companion" "%~dp0SermonCompanion.exe"
