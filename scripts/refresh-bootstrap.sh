#!/bin/sh
# Build the CI bootstrap driver bin/dispat (linux/amd64) inside Docker.
#
# The committed binary is what the workflows drive dispat with, so a Go
# toolchain on the runner stays a bootstrap concern of the release job alone.
# Run this and commit the result whenever dispat.yaml or the workflows start
# using a driver feature newer than the committed binary; a stale driver fails
# loudly (an unknown config key or flag), never silently.
#
# TEMPORARY until 1.0.0: once a stable release exists, the workflows install
# it through the composite action instead, and bin/dispat and this script are
# deleted.
#
# The whole repository is the build context so go.work is in effect and the
# pkg/* modules resolve from the tree. Keep the image in sync with GO_VERSION
# in services/dispat/Dockerfile.
set -eu
cd "$(dirname -- "$0")/.."
docker run --rm -v "$PWD":/src -w /src golang:1.26 \
  env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS=-buildvcs=false \
  go build -C services/dispat -o /src/bin/dispat .
