# syntax=docker/dockerfile:1
#
# The default dispat image: Ubuntu, the base most CI runners already are.
#
# Two stages, because the binary is fetched rather than copied in. The fetch
# stage runs the repository's own install.sh — the same script the docs hand to
# users — so there is one download path in this project rather than two that can
# drift. It has to be its own stage: ubuntu ships with neither curl nor wget, and
# adding one to the runtime image to use it once would be a permanent cost for a
# build-time need.
ARG UBUNTU_VERSION=24.04

FROM alpine:3.24 AS fetch
ARG DISPAT_VERSION
ARG TARGETARCH
# busybox wget is enough for install.sh; the certificates are not in the base.
RUN apk add --no-cache ca-certificates
COPY install.sh /tmp/install.sh
# --arch over `uname`: this stage runs emulated when the image is cross-built,
# and TARGETARCH already spells the architecture the way the release assets do.
RUN sh /tmp/install.sh --version "${DISPAT_VERSION}" --os linux --arch "${TARGETARCH}" --bin-dir /out

FROM ubuntu:${UBUNTU_VERSION}

LABEL org.opencontainers.image.title="dispat" \
      org.opencontainers.image.description="The dispat release orchestrator, on Ubuntu" \
      org.opencontainers.image.source="https://github.com/yohimik/dispat" \
      org.opencontainers.image.url="https://yohimik.github.io/dispat" \
      org.opencontainers.image.licenses="MIT"

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git \
      ca-certificates \
      openssh-client \
      tzdata \
 && rm -rf /var/lib/apt/lists/* \
 # dispat shells out to git (internal/app/app.go refuses to start without it)
 # over a checkout bind-mounted from the host, which another uid owns. Without
 # this every git call fails with "dubious ownership".
 && git config --system --add safe.directory '*'

COPY --from=fetch /out/dispat /usr/local/bin/dispat

# A self-update cannot outlive the container, so the background check only costs
# latency and prints a notice about an upgrade nobody here can apply.
ENV DISPAT_UPDATE_CHECK=0

WORKDIR /workspace

# No ENTRYPOINT on purpose: this is a CI job image, and GitLab CI and GitHub
# Actions container jobs run their script through the image's shell. A fixed
# entrypoint is the thing that makes an otherwise fine image unusable there.
CMD ["dispat", "--help"]
