#!/bin/sh
# The dispat CLI's own build stage: cross-compiles the release binaries with
# the release version baked in, and exports them as the GitHub release assets.
# Runs inside services/dispat with the stage environment
# ($DISPAT_NEW_VERSION, $DISPAT_OUTPUT).
#
# One binary per mainstream platform, because `dispat self-update` downloads
# the asset named after the running platform: a target missing here is a
# platform that cannot update itself.
set -eu

rm -rf dist
mkdir -p dist
ASSETS=""
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  case "$GOOS" in
    windows) EXT=".exe" ;;
    *) EXT="" ;;
  esac
  OUT="dist/dispat-${GOOS}-${GOARCH}${EXT}"
  # GOWORK=off: build in pure module mode, exactly as `go install` would —
  # the dependency tags this run already pushed are what gets resolved.
  GOWORK=off GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/yohimik/dispat/services/dispat/internal/cli.Version=${DISPAT_NEW_VERSION}" \
    -o "$OUT" .
  echo "built $OUT (version ${DISPAT_NEW_VERSION})"
  # Accumulated here rather than listed again below, so the export and the
  # loop can never disagree about which binaries exist.
  ASSETS="${ASSETS}${ASSETS:+ }$PWD/$OUT"
done

echo "DISPAT_EXPORT_GITHUB=$ASSETS" >> "$DISPAT_OUTPUT"
