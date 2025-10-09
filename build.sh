#!/bin/bash
set -e

TARGET_OS=${1:-$(go env GOOS)}
TARGET_ARCH=${2:-$(go env GOARCH)}

echo "Downloading dependencies..."
go mod download

echo "Building the project for $TARGET_OS/$TARGET_ARCH..."
GOOS=$TARGET_OS GOARCH=$TARGET_ARCH go build -o modulr

echo "Done. Binary name is: modulr"