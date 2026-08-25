# syntax=docker/dockerfile:1
#
# The Debian dispat image. Same shape as dispat-ubuntu, for pipelines that
# standardise on Debian; see docker/dispat-ubuntu/Dockerfile for why the binary
# is fetched in a stage of its own rather than copied in.
ARG DEBIAN_VERSION=trixie-slim
ARG FETCH_ALPINE_VERSION=3.24

FROM alpine:${FETCH_ALPINE_VERSION} AS fetch
ARG DISPAT_VERSION
ARG TARGETARCH
RUN apk add --no-cache ca-certificates
COPY install.sh /tmp/install.sh
RUN sh /tmp/install.sh --version "${DISPAT_VERSION}" --os linux --arch "${TARGETARCH}" --bin-dir /out

FROM debian:${DEBIAN_VERSION}

LABEL org.opencontainers.image.title="dispat" \
      org.opencontainers.image.description="The dispat release orchestrator, on Debian" \
      org.opencontainers.image.source="https://github.com/yohimik/dispat" \
      org.opencontainers.image.url="https://dispat.dev" \
      org.opencontainers.image.licenses="MIT"

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git \
      ca-certificates \
      openssh-client \
      tzdata \
 && rm -rf /var/lib/apt/lists/* \
 # dispat shells out to git over a checkout bind-mounted from the host, which
 # another uid owns; without this every git call fails with "dubious ownership".
 && git config --system --add safe.directory '*'

COPY --from=fetch /out/dispat /usr/local/bin/dispat

ENV DISPAT_UPDATE_CHECK=0

WORKDIR /workspace

CMD ["dispat", "--help"]
