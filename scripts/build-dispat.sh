#!/bin/sh
# The dispat CLI's own build stage: cross-compiles the release binaries with
# the release version baked in, and exports them as the GitHub release assets.
# Runs inside services/dispat with the stage environment
# ($DISPAT_NEW_VERSION, $DISPAT_OUTPUT).
#
# One binary per mainstream platform, because `dispat self-update` downloads
# the asset named after the running platform: a target missing here is a
# platform that cannot update itself.
#
# The binaries are built against this checkout, not against the pkg/* versions
# go.mod happens to pin. Those pins are only as fresh as the last release that
# bumped them, so a provider changed without a version bump would ship as its
# published copy while every test in CI ran the working tree. The build is
# therefore bracketed by `dispat autowriter --link-local`, which points each
# workspace dependency at its folder, and `--unlink-local`, which takes the
# redirects back out.
set -eu

# The redirects must not outlive this script. beforePublish runs
# `dispat commit --tag --push`, which stages services/dispat, so a link still
# in place there is published as a go.mod no consumer can resolve.
link() { dispat autowriter --package dispat --since all --sync-lock=false "$@"; }
trap 'link --unlink-local >/dev/null 2>&1 || true' EXIT INT TERM
link --link-local

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
  # GOWORK=off even with the links in place: only the intra-repo modules are
  # redirected, so the build still proves this module's own go.mod and go.sum
  # cover its third-party requirements. A go.work build would paper over a gap
  # there with another module's requires.
  GOWORK=off GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/yohimik/dispat/services/dispat/internal/cli.Version=${DISPAT_NEW_VERSION}" \
    -o "$OUT" .
  echo "built $OUT (version ${DISPAT_NEW_VERSION})"
  # Accumulated here rather than listed again below, so the export and the
  # loop can never disagree about which binaries exist.
  ASSETS="${ASSETS}${ASSETS:+ }$PWD/$OUT"
done

# Explicitly, so the bracket closes before the stage reports success rather
# than on the way out: a failure here fails the build, which is still ahead of
# anything being published. The trap stays as the net for the paths that never
# reach this line.
link --unlink-local
if grep -q '^replace github.com/yohimik/dispat' go.mod; then
  echo "build-dispat: a local link survived the build" >&2
  exit 1
fi
# The quieter half of the same mistake. `go work sync` — and anything else that
# reconciles the workspace while the links are in place — drops the go.sum
# entries the redirects made redundant, and nothing needs them again until
# someone builds the published module.
for m in ccme manifest models scanner writer; do
  if ! grep -q "^github.com/yohimik/dispat/pkg/$m " go.sum; then
    echo "build-dispat: go.sum lost its entry for pkg/$m" >&2
    exit 1
  fi
done

echo "DISPAT_EXPORT_GITHUB=$ASSETS" >> "$DISPAT_OUTPUT"
