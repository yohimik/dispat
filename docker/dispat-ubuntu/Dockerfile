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
ARG FETCH_ALPINE_VERSION=3.24

FROM alpine:${FETCH_ALPINE_VERSION} AS fetch
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
      org.opencontainers.image.url="https://dispat.dev" \
      org.opencontainers.image.licenses="MIT"

# The install retries around apt's one flaky failure mode: Ubuntu mirrors
# delete a superseded .deb the moment a security update replaces it, so an
# index fetched moments before the rotation 404s mid-download — which is what
# failed the arm64 leg of the 1.2.0 release, since that leg reads
# ports.ubuntu.com rather than the primary archive. A retry is only worth
# anything if it refreshes the index first, so the update is inside the loop
# and the stale lists are dropped before each new attempt.
RUN set -eu; \
    attempt=1; \
    while :; do \
      rc=0; \
      { apt-get update \
        && apt-get install -y --no-install-recommends \
             git \
             ca-certificates \
             openssh-client \
             tzdata; } || rc=$?; \
      [ "$rc" -eq 0 ] && break; \
      [ "$attempt" -ge 3 ] && exit "$rc"; \
      echo "apt attempt $attempt failed; refreshing the index and retrying" >&2; \
      attempt=$((attempt + 1)); \
      rm -rf /var/lib/apt/lists/*; \
      sleep 15; \
    done; \
    rm -rf /var/lib/apt/lists/*; \
    # dispat shells out to git (internal/app/app.go refuses to start without
    # it) over a checkout bind-mounted from the host, which another uid owns.
    # Without this every git call fails with "dubious ownership".
    git config --system --add safe.directory '*'

COPY --from=fetch /out/dispat /usr/local/bin/dispat

# A self-update cannot outlive the container, so the background check only costs
# latency and prints a notice about an upgrade nobody here can apply.
ENV DISPAT_UPDATE_CHECK=0

WORKDIR /workspace

# No ENTRYPOINT on purpose: this is a CI job image, and GitLab CI and GitHub
# Actions container jobs run their script through the image's shell. A fixed
# entrypoint is the thing that makes an otherwise fine image unusable there.
CMD ["dispat", "--help"]
