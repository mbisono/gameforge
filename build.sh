#!/bin/sh
# Builds gameforge for Linux. No C compiler needed: Tk is used without CGO.
set -e
cd "$(dirname "$0")"
CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o gameforge .
echo "OK: gameforge"
