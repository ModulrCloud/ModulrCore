#!/bin/bash
set -e

echo "Downloading dependencies..."
go mod download

# Build the project
echo "Building the project..."
GOOS=linux GOARCH=amd64 go build -o modulr

echo "Done. Binary name is : modulr"
