#!/bin/sh
# The dispat CLI's own build stage: cross-compiles the three release binaries
# with the release version baked in, and exports them as the GitHub release
# assets. Runs inside services/dispat with the stage environment
# ($DISPAT_NEW_VERSION, $DISPAT_OUTPUT).
set -eu

rm -rf dist
mkdir -p dist
for target in linux/amd64 darwin/arm64 windows/amd64; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  case "$GOOS" in
    windows) EXT=".exe" ;;
    *) EXT="" ;;
  esac
  OUT="dist/dispat-${GOOS}-${GOARCH}${EXT}"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/yohimik/dispat/services/dispat/internal/cli.Version=${DISPAT_NEW_VERSION}" \
    -o "$OUT" .
  echo "built $OUT (version ${DISPAT_NEW_VERSION})"
done

echo "DISPAT_EXPORT_GITHUB=$PWD/dist/dispat-linux-amd64 $PWD/dist/dispat-darwin-arm64 $PWD/dist/dispat-windows-amd64.exe" >> "$DISPAT_OUTPUT"
