# syntax=docker/dockerfile:1
#
# The dispat CLI's build-and-test stage, containerised: the six release
# binaries, built from this checkout with the release version baked in, smoke
# tested, and exported to the host as a plain folder. The export descends from
# the test stage, so a failed check fails this build and nothing comes out:
# what lands in dist/ is six binaries that were tested, never six that merely
# compiled.
#
# One binary per mainstream platform, because `dispat self-update` downloads the
# asset named after the running platform: a target missing here is a platform
# that cannot update itself. The names are part of that contract and must not
# change.
#
# Two more linux binaries ride along, built from the same source by the
# TinyGo fork at roughly half the size and named `dispat-tiny-linux-<arch>`.
# They are additive: the six names above are what self-update and install.sh
# resolve, and nothing about them changes. See internals/tinygo.md.
#
# What the smoke proves is tiered by what one builder can execute. Every gc
# binary is read back (`go version -m`) to prove it is a Go executable for the
# platform its name claims — a TinyGo binary carries no such build info, so
# the tiny pair is proven by running it instead. All four linux binaries are
# run — the foreign architecture through binfmt, which the workflows'
# setup-qemu step and Docker Desktop both provide. The builder's own gc linux
# binary then carries the e2e smoke suite (tests/integration, -run
# 'TestSmoke'). The darwin and windows binaries cannot run here at all;
# executing those falls to the release's post-release matrix, which installs
# the published binary on all three runner operating systems.
#
# The build runs here rather than on the runner so that a CI job needs a Go
# toolchain for nothing but bootstrapping dispat itself. Cross-compilation is
# Go's own, from one native builder — there is no --platform fan-out and no
# emulation, which is why six targets cost roughly what one does.
ARG GO_VERSION=1.26

# The TinyGo fork the two extra linux assets are built with, and the digests
# its release published for the tarballs this build downloads. A pin like
# GO_VERSION, never passed as --build-arg: moving to a newer fork is bumping
# these three values, and the same number is bumped in Dockerfile.tinygo,
# which is where the toolchain is interrogated rather than merely used.
ARG TINYGO_FORK_VERSION=0.42.0-net.4
ARG TINYGO_FORK_SHA256_AMD64=793f100e5ade5c909e8fc0854d3a28b67af1a289c951213669eb289341960752
ARG TINYGO_FORK_SHA256_ARM64=5f6d49ee57d583b740beeffd4dd3e14f2e534ffde6b573a7a7c7a0ebefcdd5e2

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

# The two extra linux binaries, built by the TinyGo fork: the same source and
# the same version stamp, roughly half the bytes. They are additive downloads
# under names of their own — `dispat self-update` and install.sh resolve
# `dispat-<os>-<arch>`, which is a contract nothing here touches — so a
# release carries them beside the six rather than instead of any of them.
#
# The toolchain is fetched as its release tarball, by URL and against the
# digest that release published, rather than with `dispat install`: the thing
# being built here is dispat, and a build of dispat that needs a working
# dispat to start is a bootstrap cycle. The checksum is the part `install`
# would have done, kept.
#
# Debian rather than alpine, unlike every other stage in this file: the fork
# ships a glibc-linked LLVM beside its own binary, and musl does not run it.
# TinyGo's linux targets link its bundled musl statically, so what comes out
# is a static binary either way — see internals/tinygo.md for what those
# binaries can do, which as of the fork is everything the gc ones can.
FROM golang:${GO_VERSION} AS tiny-toolchain
ARG TARGETARCH
ARG TINYGO_FORK_VERSION
ARG TINYGO_FORK_SHA256_AMD64
ARG TINYGO_FORK_SHA256_ARM64
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) sha="$TINYGO_FORK_SHA256_AMD64" ;; \
      arm64) sha="$TINYGO_FORK_SHA256_ARM64" ;; \
      *) echo "no fork toolchain pinned for $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    url="https://github.com/yohimik/tinygo/releases/download/v${TINYGO_FORK_VERSION}/tinygo${TINYGO_FORK_VERSION}.linux-${TARGETARCH}.tar.gz"; \
    curl -fsSL -o /tmp/tinygo.tar.gz "$url"; \
    echo "$sha  /tmp/tinygo.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/tinygo.tar.gz -C /usr/local; \
    rm /tmp/tinygo.tar.gz; \
    /usr/local/tinygo/bin/tinygo version

# The compile itself, in a stage of its own so that a source edit never
# re-downloads the toolchain. Same layout and same GOWORK=off as the gc build
# above, for the same reasons.
#
# One LLVM warning repeats once per inline asm block on arm64 — hundreds of
# identical lines that bury every other answer — so the build's output is
# captured, the warning counted rather than kept, and the whole of it printed
# when the compile fails. Exactly what the spike does with the same noise.
FROM tiny-toolchain AS tiny-build
ARG DISPAT_VERSION=dev
WORKDIR /src
COPY pkg ./pkg
COPY services/dispat ./services/dispat
WORKDIR /src/services/dispat
ENV GOWORK=off
ENV PATH=/usr/local/tinygo/bin:$PATH
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/tinygo \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    mkdir -p /out; \
    for arch in amd64 arm64; do \
      name="dispat-tiny-linux-${arch}"; \
      if GOOS=linux GOARCH="$arch" tinygo build -opt=z -no-debug \
        -ldflags "-X github.com/yohimik/dispat/services/dispat/internal/cli.Version=${DISPAT_VERSION}" \
        -o "/out/$name" . > /tmp/tinygo.raw 2>&1; then :; else \
        cat /tmp/tinygo.raw >&2; exit 1; fi; \
      noise="Reserved registers on the clobber list"; \
      n="$(grep -c "$noise" /tmp/tinygo.raw || true)"; \
      grep -v "$noise" /tmp/tinygo.raw || true; \
      [ "$n" -eq 0 ] || echo "($n identical \"$noise\" warnings elided)"; \
      echo "built $name (version ${DISPAT_VERSION})"; \
    done

# The two binaries joined to the six, and the asset list extended with their
# names. The list is rewritten as one line because that is what the staged
# stage below joins paths onto; the tiny names go last, so the six the
# contract names keep the order they were built in.
FROM build AS tiny
COPY --from=tiny-build /out/dispat-tiny-linux-amd64 /out/dispat-tiny-linux-arm64 /out/
RUN set -eu; \
    assets="$(sed -n 's/^DISPAT_EXPORT_GITHUB=//p' /out/dispat-output)"; \
    for name in dispat-tiny-linux-amd64 dispat-tiny-linux-arm64; do \
      [ -s "/out/$name" ] || { echo "$name did not come out of the fork build" >&2; exit 1; }; \
      assets="$assets $name"; \
    done; \
    echo "DISPAT_EXPORT_GITHUB=$assets" > /out/dispat-output; \
    cat /out/dispat-output

# The smoke gate: the binaries above, proven to work before anything exports
# them. Tests run against /out's exact bytes — a fresh native build passing
# says nothing about what a cross-compile produced — via DISPAT_TEST_BINARY,
# which points the integration harness at a prebuilt binary instead of its
# own `go build` (see tests/integration/internal/harness/binary.go).
#
# The suite drives dispat against disposable git repositories, so git is
# installed and then asserted: without it the harness skips every test, and a
# gate that passes by skipping is worse than no gate.
FROM tiny AS test

# The builder's own architecture — which of the six binaries can carry the
# test suite natively.
ARG TARGETARCH

# Redeclared: a stage does not inherit its parent's ARGs, only the value the
# caller passed survives into a stage that asks for it again. What the loop
# below expects of --version is decided by this, and an empty value stamps a
# binary that reports dev (see selfupdate.Describe), so empty and dev are the
# same case there.
ARG DISPAT_VERSION=dev

RUN apk add --no-cache git && git --version

# Copied here, after the compiles, so a test-only change never invalidates
# the cross-compile layer above.
COPY tests/integration /src/tests/integration

# The suite resolves the pkg/* modules through a workspace, exactly as the
# checkout does: the pins in tests/integration/go.mod are only as fresh as the
# last release, so a harness field added beside a new feature would not
# compile against the published copies. The workspace is written rather than
# copied because the checkout's go.work also names modules this context
# deliberately leaves out, and it is scoped to the test invocation below:
# GOWORK stays off for the binaries' own build, whose whole point is the
# published module's workspace-less environment.
ARG GO_VERSION
RUN printf 'go %s\n\nuse (\n\t./tests/integration\n\t./pkg/ccme\n\t./pkg/manifest\n\t./pkg/models\n\t./pkg/scanner\n\t./pkg/writer\n)\n' \
      "$GO_VERSION" > /src/go.work

# The same bypass the workflows use: the pkg/* modules this suite pins are
# fetched from their tags directly, not from a proxy that may not have seen a
# tag dispat itself created moments ago.
ENV GOPRIVATE=github.com/yohimik/dispat

# A stamped binary's --version otherwise starts a background update check
# against the GitHub API; anything that parses as false turns it off.
ENV DISPAT_UPDATE_CHECK=false

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
      GOOS="${target%/*}"; \
      GOARCH="${target#*/}"; \
      case "$GOOS" in windows) ext=".exe" ;; *) ext="" ;; esac; \
      name="dispat-${GOOS}-${GOARCH}${ext}"; \
      info="$(go version -m "/out/$name")"; \
      echo "$info" | grep -q "GOOS=$GOOS" || { echo "$name is not a $GOOS binary" >&2; exit 1; }; \
      echo "$info" | grep -q "GOARCH=$GOARCH" || { echo "$name is not a $GOARCH binary" >&2; exit 1; }; \
      echo "validated $name"; \
    done; \
    for name in dispat-linux-amd64 dispat-linux-arm64 \
                dispat-tiny-linux-amd64 dispat-tiny-linux-arm64; do \
      arch="${name##*-}"; \
      out="$(/out/$name --version)"; \
      echo "$out" | grep -q "(linux_$arch)" || { echo "$name reports the wrong platform: $out" >&2; exit 1; }; \
      if [ -n "$DISPAT_VERSION" ] && [ "$DISPAT_VERSION" != "dev" ]; then \
        echo "$out" | grep -q "dispat $DISPAT_VERSION " || { echo "$name reports the wrong version: $out" >&2; exit 1; }; \
      fi; \
      echo "executed $name: $(echo "$out" | grep 'dispat ')"; \
    done; \
    GOWORK=/src/go.work DISPAT_TEST_BINARY="/out/dispat-linux-${TARGETARCH}" \
      go test -C /src/tests/integration -run 'TestSmoke' -count=1 -timeout 20m .; \
    echo "smoke suite passed against dispat-linux-${TARGETARCH}"

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
FROM test AS staged
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

# The CI bootstrap: the fresh dispat every workflow job builds from its
# checkout before any dispat command runs, exported as a single native-arch
# binary. This is the containerised twin of the host build the jobs used to
# run (`go build -C services/dispat`), so it resolves the pkg/* modules
# through a workspace exactly as the checkout does — unlike the release
# build above, whose whole point is the published module's workspace-less
# environment — and it stays unstamped: an un-stamped build reports `dev`,
# which is the honest answer for a gate binary that is not a release.
#
# The workspace is written rather than copied because the checkout's go.work
# also names modules this context deliberately leaves out. Dependencies
# download in a layer keyed on the manifests alone, so the CI cache carries
# them across runs and a source edit re-runs only the compile.
FROM golang:${GO_VERSION}-alpine AS bootstrap-deps
WORKDIR /src
# ccme and manifest carry no go.sum: they have no module dependencies to sum,
# and a COPY that names a file that does not exist fails the build.
COPY pkg/ccme/go.mod pkg/ccme/
COPY pkg/config/go.mod pkg/config/go.sum pkg/config/
COPY pkg/manifest/go.mod pkg/manifest/
COPY pkg/models/go.mod pkg/models/go.sum pkg/models/
COPY pkg/scanner/go.mod pkg/scanner/go.sum pkg/scanner/
COPY pkg/writer/go.mod pkg/writer/go.sum pkg/writer/
COPY services/dispat/go.mod services/dispat/go.sum services/dispat/
ARG GO_VERSION
RUN printf 'go %s\n\nuse (\n\t./services/dispat\n\t./pkg/ccme\n\t./pkg/config\n\t./pkg/manifest\n\t./pkg/models\n\t./pkg/scanner\n\t./pkg/writer\n)\n' \
      "$GO_VERSION" > /src/go.work
RUN go mod download

FROM bootstrap-deps AS bootstrap
COPY pkg ./pkg
COPY services/dispat ./services/dispat
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -C services/dispat -o /out/dispat .

FROM scratch AS bootstrap-export
COPY --from=bootstrap /out/dispat /dispat
