# syntax=docker/dockerfile:1
#
# The small dispat image. The release binaries are static (CGO_ENABLED=0, see
# services/dispat/Dockerfile), so musl costs nothing here.
#
# The fetch stage is kept even though Alpine has busybox wget and could run the
# installer in place: it keeps the four images identical in shape, and it leaves
# install.sh out of the published filesystem.
ARG ALPINE_VERSION=3.24

FROM alpine:${ALPINE_VERSION} AS fetch
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

FROM alpine:${ALPINE_VERSION}

LABEL org.opencontainers.image.title="dispat" \
      org.opencontainers.image.description="The dispat release orchestrator, on Alpine" \
      org.opencontainers.image.source="https://github.com/yohimik/dispat" \
      org.opencontainers.image.url="https://dispat.dev" \
      org.opencontainers.image.licenses="MIT"

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

CMD ["dispat", "--help"]
