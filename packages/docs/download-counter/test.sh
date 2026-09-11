#!/bin/sh
set -eu
cd "$(dirname "$0")"
export GOWORK=off

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  printf '%s\n' "$unformatted" >&2
  exit 1
fi
go test -race -coverprofile=/tmp/download-counter.coverage ./...
go vet ./...
go tool cover -func=/tmp/download-counter.coverage > /tmp/download-counter-coverage.txt
cat /tmp/download-counter-coverage.txt
awk '/^total:/ { found=1; if ($3+0 < 95) exit 1 } END { if (!found) exit 1 }' /tmp/download-counter-coverage.txt
