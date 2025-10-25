@echo off
setlocal EnableExtensions EnableDelayedExpansion

REM ===========================================
REM   Modulr build script (CMD, Windows 10+)
REM ===========================================

REM Enable ANSI colors in modern terminals
for /F "delims=" %%A in ('echo prompt $E^| cmd') do set "ESC=%%A"
set "BOLD=%ESC%[1m"
set "RESET=%ESC%[0m"
set "YELLOW_BG=%ESC%[43m"
set "GREEN_BG=%ESC%[42m"
set "RED_BG=%ESC%[41m"

REM Timestamp helper
set "TS=%date% %time%"

echo(
echo %YELLOW_BG%%BOLD%Fetching dependencies  •  %TS%%RESET%
echo ------------------------------------------------------------
go mod download
if errorlevel 1 goto FAIL

set "TS=%date% %time%"
echo(
echo %GREEN_BG%%BOLD%Core building process started  •  %TS%%RESET%
echo ------------------------------------------------------------
echo %BOLD%Building the project...%RESET%
go build -o modulr.exe .
if errorlevel 1 goto FAIL

echo(
echo %GREEN_BG%%BOLD%Build succeeded!%RESET%
echo Binary: modulr.exe
echo Path  : %cd%\modulr.exe
goto END

:FAIL
echo(
echo %RED_BG%%BOLD%Build failed!%RESET%
exit /b 1

:END
exit /b 0
