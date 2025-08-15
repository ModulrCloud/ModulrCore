@echo off
setlocal
echo Downloading dependencies...
go mod download

:: Build the project
echo Building the project...
set GOOS=windows
set GOARCH=amd64
go build -o modulr.exe

echo Done. Binary name is: modulr.exe
