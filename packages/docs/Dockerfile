# syntax=docker/dockerfile:1
#
# The documentation site's build stage, containerised: the static site, built
# from this checkout and exported to the host as packages/docs/build, which is
# where the publish stage (the deploy-docs script in dispat.yaml beside this
# file) expects to find it.
#
# The build is also the link checker — docusaurus.config.ts sets onBrokenLinks,
# onBrokenAnchors and onBrokenMarkdownLinks to 'throw' — and the `typecheck`
# target below is the second gate, so a CI job needs no Node of its own for
# either.
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

# corepack takes the pnpm version from packageManager in the root package.json,
# so the container installs with exactly the pnpm the lockfile was written by.
# The prompt would block a non-interactive build.
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable

WORKDIR /src

# The manifests alone, before the sources: this layer is the expensive one, and
# it should survive every edit that does not change a dependency. The workspace
# root owns the lockfile, so the install runs there and reaches the one member.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/docs/package.json ./packages/docs/
RUN --mount=type=cache,target=/pnpm-store \
    pnpm config set store-dir /pnpm-store \
 && pnpm install --frozen-lockfile

# The lockfile sync the packages space runs after autoVersion rewrites a
# package.json: pnpm regenerates the lockfile in here, and the export hands
# it back over the working tree's. No host Node is involved, and no
# node_modules leaves the build — the site build installs inside this
# Dockerfile already.
FROM deps AS lockfile
RUN --mount=type=cache,target=/pnpm-store pnpm install --lockfile-only
FROM scratch AS lockfile-export
COPY --from=lockfile /src/pnpm-lock.yaml /

FROM deps AS source

# docusaurus.config.ts sets showLastUpdateTime, which reads the git history of
# every page, so the repository has to come along and git has to be here to read
# it. Another uid owns the copied tree, hence safe.directory.
RUN apk add --no-cache git \
 && git config --system --add safe.directory '*'

# staticDirectories reaches out of the package to ../../imgs, so it is part of
# this build even though it sits outside packages/docs.
COPY imgs ./imgs
# The landing page is read out of the two READMEs at build time
# (plugins/readme), so they are inputs to this build as much as any page is.
# The layout under /src has to match the repository's, because the plugin
# resolves them from the site folder as ../../.
COPY README.md ./README.md
COPY services/dispat/README.md ./services/dispat/README.md
COPY packages/docs ./packages/docs
COPY .git ./.git

# One gate that is not the build, so it can be asked for on its own.
FROM source AS typecheck
RUN pnpm --filter dispat-docs typecheck

# The build, with the version snapshot folded into it.
#
# Freezing a Docusaurus version is not a stage of its own because `docs` and
# `dispat` share the `cli` version group: they release together, always, so the
# snapshot belongs to the same build that produces the site it appears in — and
# the site is then built *with* it, which is what ships.
#
# DOCS_VERSION is the minor to freeze, or empty for no cut at all (a
# prerelease: the rc train runs long, and every one of them would freeze
# another full copy of the docs). An already-cut minor is left alone, so
# building twice is a no-op the second time.
#
# DOCS_REQUIRE_REPORT=1 makes the build refuse to start without
# packages/docs/data/report.json, the coverage, test-results and benchmark
# numbers the release measured. The release passes it and nothing else does: a
# local build and the CI build gate have no report and render those pages
# without figures, which is fine for them and would not be fine for the site
# people read.
FROM source AS build
ARG DOCS_VERSION=""
ARG DOCS_REQUIRE_REPORT=""
ARG DOCS_BENCH_PACKAGES=""
ARG DOCS_EXPERIMENTS=""
ENV DISPAT_DOCS_REQUIRE_REPORT=${DOCS_REQUIRE_REPORT}
# The per-package freshness check, before anything is built.
#
# The release's beforeBuild hook measured a set of modules and said which; the
# report has to carry every one of them, or a page would ship with a package
# silently absent from a table that lists the others. Checked here rather than
# in the site's own code because a missing section renders as an empty table
# and an empty table reads as "this package has no benchmarks".
#
# It also checks that every frozen docs version carries the benchmarks and
# experiments pages, and that its sidebar lists them. The numbers are global
# (every version's page renders the same measured report), so a version whose
# copy of a page is missing is a version that shows the site's other numbers
# and not those, and a patch release cuts no new snapshot to fix it with. A
# page the sidebar does not list is the same failure with the file present:
# the build succeeds, the page ships, and no reader of that version has a way
# to reach it.
RUN <<'EOF'
set -eu
[ -n "${DOCS_BENCH_PACKAGES}${DOCS_EXPERIMENTS}" ] || exit 0
for pkg in ${DOCS_BENCH_PACKAGES}; do
  grep -q "\"path\": \"${pkg}\"" packages/docs/data/report.json || {
    echo "the release measured ${pkg} but data/report.json carries nothing for it" >&2
    exit 1
  }
  echo "report carries ${pkg}"
done
# The same check for the experiments: the hook ran a campaign and said which
# cells it recorded, and a cell missing from the report is a row missing from
# a table that lists the other eleven, which reads as a cell that was never
# run rather than as one that was lost.
for cell in ${DOCS_EXPERIMENTS}; do
  grep -q "\"id\": \"${cell}\"" packages/docs/data/report.json || {
    echo "the release recorded ${cell} but data/report.json carries nothing for it" >&2
    exit 1
  }
  echo "report carries ${cell}"
done
for dir in packages/docs/versioned_docs/version-*; do
  [ -d "$dir" ] || continue
  sidebars="packages/docs/versioned_sidebars/${dir##*/}-sidebars.json"
  for page in benchmarks experiments; do
    [ -f "$dir/internals/$page.mdx" ] || {
      echo "$dir has no internals/$page.mdx: the measured numbers would reach the current docs and not that version" >&2
      exit 1
    }
    grep -q "\"id\": \"internals/$page\"" "$sidebars" || {
      echo "$sidebars does not list internals/$page: that version's page would build with no way to reach it" >&2
      exit 1
    }
  done
done
EOF
RUN if [ -n "${DOCS_VERSION}" ] && [ ! -d "packages/docs/versioned_docs/version-${DOCS_VERSION}" ]; then \
      pnpm --filter dispat-docs exec docusaurus docs:version "${DOCS_VERSION}"; \
      echo "cut docs version ${DOCS_VERSION}"; \
    fi \
 && pnpm --filter dispat-docs build

# The build produces two different kinds of thing, and both go back to the
# package folder:
#
#   build/            the site — output, gitignored, read by the publish stage
#   versioned_docs/   the snapshot — tracked source, picked up by the release
#   versioned_sidebars/   commit that follows the build
#   versions.json
#
# They are staged together because `--output type=local` writes a stage's whole
# filesystem to one destination. The three source paths are copied only if they
# exist: before the first stable release none of them do.
FROM build AS staged
RUN mkdir -p /out \
 && cp -R packages/docs/build /out/build \
 && for p in versioned_docs versioned_sidebars versions.json; do \
      if [ -e "packages/docs/$p" ]; then cp -R "packages/docs/$p" "/out/$p"; fi; \
    done

FROM scratch AS export
COPY --from=staged /out /
