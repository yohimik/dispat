# syntax=docker/dockerfile:1
#
# dispat beside a Docker daemon, for release flows whose build stage is itself a
# `docker build`. The base is Docker's own dind image, so this one keeps the
# base's ENTRYPOINT (dockerd-entrypoint.sh) instead of declaring its own: the
# image has to go on behaving exactly like docker:dind, or every documented way
# of running dind stops working. It therefore also needs --privileged, or to be
# used as a `services: docker:dind` companion; see docker/README.md.
ARG DOCKER_VERSION=29-dind
ARG FETCH_ALPINE_VERSION=3.24

FROM alpine:${FETCH_ALPINE_VERSION} AS fetch
ARG DISPAT_VERSION
ARG TARGETARCH
# curl rather than the base's busybox wget: with a token, install.sh fetches
# the asset from its API endpoint, which busybox wget cannot do without
# carrying the credential across the redirect to the storage host.
RUN apk add --no-cache ca-certificates curl
COPY install.sh /tmp/install.sh
# The GitHub token arrives as a build secret, never a build arg, so it stays
# out of the image's history. It raises the release lookups above the small
# anonymous quota a runner's address shares with its neighbours. Without the
# secret the variable stays unset and install.sh asks anonymously.
RUN --mount=type=secret,id=github_token,env=GITHUB_TOKEN \
    sh /tmp/install.sh --version "${DISPAT_VERSION}" --os linux --arch "${TARGETARCH}" --bin-dir /out

FROM docker:${DOCKER_VERSION}

LABEL org.opencontainers.image.title="dispat" \
      org.opencontainers.image.description="The dispat release orchestrator, with a Docker daemon" \
      org.opencontainers.image.source="https://github.com/yohimik/dispat" \
      org.opencontainers.image.url="https://dispat.dev" \
      org.opencontainers.image.licenses="MIT"

# The dind image is Alpine with a daemon on top: git and the rest are not in it.
RUN apk add --no-cache \
      git \
      ca-certificates \
      openssh-client \
      tzdata \
 # dispat shells out to git over a checkout bind-mounted from the host, which
 # another uid owns; without this every git call fails with "dubious ownership".
 && git config --system --add safe.directory '*'

COPY --from=fetch /out/dispat /usr/local/bin/dispat

ENV DISPAT_UPDATE_CHECK=0

WORKDIR /workspace
