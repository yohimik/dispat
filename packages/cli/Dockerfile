# syntax=docker/dockerfile:1
ARG NODE_VERSION=22.22.2
FROM node:${NODE_VERSION}-alpine AS test
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/docs/package.json ./packages/docs/
COPY packages/cli/package.json ./packages/cli/
RUN --mount=type=cache,target=/pnpm-store pnpm config set store-dir /pnpm-store \
 && pnpm install --frozen-lockfile --ignore-scripts
COPY packages/cli ./packages/cli
RUN pnpm --filter @dispat/bin test

# npm's default-denial behavior is version-specific. Keep the supported Node
# baseline above, and execute the packed global-install regression on npm 12.
FROM node:24.18.0-alpine AS npm12-test
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable \
 && npm install --global --ignore-scripts npm@12.0.1 \
 && test "$(npm --version)" = 12.0.1
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/docs/package.json ./packages/docs/
COPY packages/cli/package.json ./packages/cli/
RUN --mount=type=cache,target=/pnpm-store pnpm config set store-dir /pnpm-store \
 && pnpm install --frozen-lockfile --ignore-scripts
COPY packages/cli ./packages/cli
RUN pnpm --filter @dispat/bin compile \
 && pnpm --filter @dispat/bin compile:test \
 && cd packages/cli \
 && node --test test-build/global-install-integration.test.js \
 && touch /tmp/npm12-passed

FROM test AS typecheck
COPY --from=npm12-test /tmp/npm12-passed /tmp/npm12-passed
