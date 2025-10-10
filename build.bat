@echo off
setlocal EnableExtensions EnableDelayedExpansion

REM ---------------------------
REM ANSI colors (Win10+)
REM ---------------------------
for /F "delims=" %%A in ('echo prompt $E^| cmd') do set "ESC=%%A"

if defined NO_COLOR (
  set "BOLD="
  set "RESET="
  set "YELLOW_BG="
  set "GREEN_BG="
  set "RED_BG="
) else (
  set "BOLD=%ESC%[1m"
  set "RESET=%ESC%[0m"
  set "YELLOW_BG=%ESC%[43m"
  set "GREEN_BG=%ESC%[42m"
  set "RED_BG=%ESC%[41m"
)

REM ---------------------------
REM Helpers
REM ---------------------------
:banner
set "bg=%~1"
set "msg=%~2"
echo(
echo %bg%%BOLD%%msg%%RESET%
goto :eof

:say
echo %*
goto :eof

:hr
echo ------------------------------------------------------------
goto :eof

:ts
set "TS=%date% %time%"
goto :eof

REM ---------------------------
REM Config
REM ---------------------------
set "BIN_NAME=modulr"
set "TARGET_OS=%~1"
set "TARGET_ARCH=%~2"

if "%TARGET_OS%"=="" (
  for /f "delims=" %%a in ('go env GOOS') do set "TARGET_OS=%%a"
)
if "%TARGET_ARCH%"=="" (
  for /f "delims=" %%a in ('go env GOARCH') do set "TARGET_ARCH=%%a"
)

set "OUT_NAME=%BIN_NAME%"
if /I "%TARGET_OS%"=="windows" set "OUT_NAME=%BIN_NAME%.exe"

REM ---------------------------
REM Timer start
REM ---------------------------
for /f "delims=" %%a in ('powershell -NoProfile -Command "[DateTime]::UtcNow.Ticks"') do set "START_TICKS=%%a"

call :ts
call :banner "%YELLOW_BG%" "Fetching dependencies  •  %TS%"
call :hr
for /f "tokens=2,3*" %%v in ('go version') do set "GOVER=%%v %%w"
call :say Working dir  : %cd%
call :say Go version   : %GOVER%
call :say Target       : %TARGET_OS%/%TARGET_ARCH%
call :hr

go mod download
if errorlevel 1 goto fail

call :ts
call :banner "%GREEN_BG%" "Core building process started  •  %TS%"
call :say %BOLD%Building the project for %TARGET_OS%/%TARGET_ARCH%...%RESET%
call :hr
cmd /c set GOOS=%TARGET_OS%^& set GOARCH=%TARGET_ARCH%^& go build -o "%OUT_NAME%" .
if errorlevel 1 goto fail

for /f "delims=" %%a in ('powershell -NoProfile -Command "$elapsed = ([DateTime]::UtcNow.Ticks - %START_TICKS%) / 10000000; [Math]::Round($elapsed,2)"') do set "ELAPSED=%%a"

call :ts
call :banner "%GREEN_BG%" "Build succeeded  •  %TS%"
call :say Binary       : %OUT_NAME%
call :say Target       : %TARGET_OS%/%TARGET_ARCH%
call :say Output path  : %cd%\%OUT_NAME%
call :say Elapsed time : %ELAPSED%s
call :hr
exit /b 0

:fail
call :ts
call :banner "%RED_BG%" "Build failed  •  %TS%"
exit /b 1
