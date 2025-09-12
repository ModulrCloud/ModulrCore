@echo off
setlocal
echo Downloading dependencies...
go mod download

echo Building the project...
go build -o modulr.exe

echo Done. Binary name is: modulr.exe
