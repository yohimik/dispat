#!/bin/sh
# Publish stage for the `docs` package: force-push the built site to an
# orphan gh-pages branch.
#
# A publish stage is a shell command, not a GitHub Action, so the official
# actions/deploy-pages route is unavailable. Pushing a branch is the same
# mechanism the coverage badge already uses (see the coverage-badge job in
# .github/workflows/tests.yml), and it needs nothing beyond the contents:write
# and GITHUB_TOKEN that release.yml already grants.
set -eu

# The tag is the record that this leg committed, so a deploy must only ever
# happen inside the release run. Without this guard `dispat test deploy-docs
# docs` would publish a developer's working tree to the live site.
if [ "${CI:-}" != "true" ]; then
  echo "refusing to deploy outside CI (set CI=true to override)" >&2
  exit 1
fi
: "${GITHUB_TOKEN:?GITHUB_TOKEN is required to push gh-pages}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required to push gh-pages}"

# GitHub Pages runs Jekyll over a branch deploy unless told not to, which
# would drop any path segment starting with an underscore.
touch build/.nojekyll

cd build
git init -q -b gh-pages
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add -A
git commit -qm "docs: deploy ${DISPAT_NEW_VERSION}"
git push -f "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.git" gh-pages
echo "deployed ${DISPAT_NEW_VERSION} to gh-pages"
