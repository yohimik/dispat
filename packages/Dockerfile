# syntax=docker/dockerfile:1
ARG NODE_VERSION=22.22.2
FROM node:${NODE_VERSION}-alpine AS base
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/docs/package.json ./packages/docs/
COPY packages/cli/package.json ./packages/cli/

# This stage deliberately does not inherit a frozen install. During a release,
# autoVersion has already changed one or both manifests and this stage exists
# to produce the corresponding lockfile.
FROM base AS lockfile
RUN --mount=type=cache,target=/pnpm-store \
    pnpm config set store-dir /pnpm-store \
 && pnpm install --lockfile-only --no-frozen-lockfile --ignore-scripts
FROM scratch AS lockfile-export
COPY --from=lockfile /src/pnpm-lock.yaml /
