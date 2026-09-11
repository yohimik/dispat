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
RUN pnpm --filter @dispat/cli test
FROM test AS typecheck
