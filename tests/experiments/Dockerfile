# syntax=docker/dockerfile:1
#
# The experiments' runtime: the released dispat binary beside the release
# tools it is compared with, a local npm registry and the fault-injecting
# proxy in front of it. One container is one experiment run; the registry
# starts inside it, so no run ever shares a registry with another.
#
# The binary under test is taken from the published dispat-alpine image, so
# what runs here is the exact bytes a release shipped, fetched through the
# channel it shipped by, and never a build of the checkout.
#
#   docker buildx build --file tests/experiments/Dockerfile \
#     --build-arg DISPAT_VERSION=1.7.0 --load -t dispat-experiments tests/experiments
#   docker run --rm -v "$PWD/results:/results" dispat-experiments midrelease dispat
ARG DISPAT_VERSION=1.7.0
ARG NODE_VERSION=22

FROM docker.io/yohimik/dispat-alpine:${DISPAT_VERSION} AS official

FROM node:${NODE_VERSION}-slim AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends git python3 ca-certificates curl jq \
 && rm -rf /var/lib/apt/lists/*

# The compared tools at the pinned versions the results are about, installed
# once here so no phase installs anything over the experiment registry.
# Fixtures symlink /opt/tools/node_modules to satisfy nx's local-install check.
RUN mkdir -p /opt/tools && cd /opt/tools && npm init -y >/dev/null \
 && npm i --no-audit --no-fund \
      lerna@10.0.1 nx@23.1.2 @nx/js@23.1.2 @changesets/cli@3.0.1 verdaccio@6.2.0 \
 && npm cache clean --force >/dev/null 2>&1
ENV PATH=/opt/tools/node_modules/.bin:$PATH

COPY --from=official /usr/local/bin/dispat /usr/local/bin/dispat

# The identity every fixture repository commits under, and the dispat
# defaults an unattended run needs.
RUN git config --global user.email experiments@dispat.dev \
 && git config --global user.name experiments \
 && git config --global init.defaultBranch main \
 && git config --global advice.detachedHead false
ENV DISPAT_UPDATE_CHECK=0 \
    NX_DAEMON=false NX_TUI=false NX_NO_CLOUD=true \
    TURBO_TELEMETRY_DISABLED=1 \
    NPM_CONFIG_UPDATE_NOTIFIER=false

COPY lib /exp/lib
COPY orphan /exp/orphan
COPY midrelease /exp/midrelease
COPY run.sh /exp/run.sh
RUN chmod +x /exp/run.sh /exp/lib/git-shim

WORKDIR /work
ENTRYPOINT ["/exp/run.sh"]
