# syntax=docker/dockerfile:1
#
# The dispat CLI's build stage, containerised: the six release binaries, built
# from this checkout with the release version baked in and exported to the host
# as a plain folder.
#
# One binary per mainstream platform, because `dispat self-update` downloads the
# asset named after the running platform: a target missing here is a platform
# that cannot update itself. The names are part of that contract and must not
# change.
#
# The build runs here rather than on the runner so that a CI job needs a Go
# toolchain for nothing but bootstrapping dispat itself. Cross-compilation is
# Go's own, from one native builder — there is no --platform fan-out and no
# emulation, which is why six targets cost roughly what one does.
ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build

# The version the ldflags bake in. `dev` is what an un-stamped build reports
# (see internal/cli.Version), so it is the honest default for a build run
# outside a release.
ARG DISPAT_VERSION=dev

WORKDIR /src

# pkg/ beside services/, in the layout the host has: the release build is
# bracketed by `dispat autowriter --link-local`, which writes
# `replace github.com/yohimik/dispat/pkg/<name> => ../../pkg/<name>` into
# services/dispat/go.mod. Those redirects resolve in here only because the two
# folders sit at the same relative depth they do in the repository.
COPY pkg ./pkg
COPY services/dispat ./services/dispat

WORKDIR /src/services/dispat

# go.work is deliberately not part of the build context, and this says so out
# loud. A workspace build would paper over a gap in this module's own go.mod or
# go.sum with another module's requires; the published module has no workspace,
# so neither does its build.
ENV GOWORK=off

# The asset list is accumulated in the loop rather than written out a second
# time, so the export and the loop can never disagree about which binaries
# exist. It leaves this stage as bare names in /out/dispat-output; the staged
# stage below turns them into the paths the caller will see.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    mkdir -p /out; \
    assets=""; \
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
      GOOS="${target%/*}"; \
      GOARCH="${target#*/}"; \
      case "$GOOS" in windows) ext=".exe" ;; *) ext="" ;; esac; \
      name="dispat-${GOOS}-${GOARCH}${ext}"; \
      GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X github.com/yohimik/dispat/services/dispat/internal/cli.Version=${DISPAT_VERSION}" \
        -o "/out/$name" .; \
      echo "built $name (version ${DISPAT_VERSION})"; \
      assets="${assets}${assets:+ }$name"; \
    done; \
    echo "DISPAT_EXPORT_GITHUB=$assets" > /out/dispat-output

# The stage's outputs, GITHUB_OUTPUT-style, as the caller will read them: the
# build above knows the binaries' names and nothing about where the export will
# land, so the destination is passed in and joined onto them here. What comes
# out is a file whoever exported the folder can append to the real
# $DISPAT_OUTPUT unread — see the `build` script in dispat.yaml.
#
# A stage of its own, and the only place DISPAT_EXPORT_BASE is declared, so
# building from a different checkout path does not invalidate the
# cross-compile layer above it.
#
# The default is what a hand build of this target gets: names relative to the
# folder that was exported, which is the one reading they are always correct
# under. A release passes the absolute path.
FROM build AS staged
ARG DISPAT_EXPORT_BASE=.
RUN set -eu; \
    while IFS= read -r line; do \
      paths=""; \
      for name in ${line#*=}; do paths="$paths${paths:+ }$DISPAT_EXPORT_BASE/$name"; done; \
      printf '%s=%s\n' "${line%%=*}" "$paths"; \
    done < /out/dispat-output > /out/dispat-output.joined; \
    mv /out/dispat-output.joined /out/dispat-output; \
    cat /out/dispat-output

# Nothing but the artefacts: `--output type=local` writes this stage's whole
# filesystem to the destination folder, so anything else here would land in
# dist/ beside the binaries.
FROM scratch AS export
COPY --from=staged /out /
