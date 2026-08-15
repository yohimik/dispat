# syntax=docker/dockerfile:1
#
# The small dispat image. The release binaries are static (CGO_ENABLED=0, see
# services/dispat/Dockerfile), so musl costs nothing here.
#
# The fetch stage is kept even though Alpine has busybox wget and could run the
# installer in place: it keeps the four images identical in shape, and it leaves
# install.sh out of the published filesystem.
ARG ALPINE_VERSION=3.24

FROM alpine:3.24 AS fetch
ARG DISPAT_VERSION
ARG TARGETARCH
RUN apk add --no-cache ca-certificates
COPY install.sh /tmp/install.sh
RUN sh /tmp/install.sh --version "${DISPAT_VERSION}" --os linux --arch "${TARGETARCH}" --bin-dir /out

FROM alpine:${ALPINE_VERSION}

LABEL org.opencontainers.image.title="dispat" \
      org.opencontainers.image.description="The dispat release orchestrator, on Alpine" \
      org.opencontainers.image.source="https://github.com/yohimik/dispat" \
      org.opencontainers.image.url="https://yohimik.github.io/dispat" \
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
